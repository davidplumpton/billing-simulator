package scenario

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

const (
	scenarioRunStatusRunning   = "running"
	scenarioRunStatusSucceeded = "succeeded"
	scenarioRunStatusFailed    = "failed"
	scenarioEventSource        = "scenario"
	defaultScenarioReportRole  = "management-account"
	maxScenarioRunIDAttempts   = 10_000
)

// Runner applies parsed scenario definitions to a workspace database.
type Runner struct {
	db           *sql.DB
	clock        persistence.SimulatorClockRepository
	usage        persistence.ResourceUsageRepository
	tags         persistence.CostAllocationTagRepository
	categories   persistence.CostCategoryRepository
	splitCharges persistence.CostCategorySplitChargeRepository
	budgets      persistence.BudgetRepository
	reports      persistence.SavedReportRepository
	profiles     persistence.PaymentProfileRepository
	payments     persistence.PaymentLifecycleRepository
	savingsPlans persistence.SavingsPlanRepository
	daily        persistence.DailyMeteringJobRepository
	monthEnd     persistence.MonthEndCloseRepository
	organization persistence.OrganizationRepository
	progress     persistence.ScenarioLearnerProgressRepository
}

// NewRunner creates a scenario runner backed by the workspace database.
func NewRunner(db *sql.DB) Runner {
	return Runner{
		db:           db,
		clock:        persistence.NewSimulatorClockRepository(db),
		usage:        persistence.NewResourceUsageRepository(db),
		tags:         persistence.NewCostAllocationTagRepository(db),
		categories:   persistence.NewCostCategoryRepository(db),
		splitCharges: persistence.NewCostCategorySplitChargeRepository(db),
		budgets:      persistence.NewBudgetRepository(db),
		reports:      persistence.NewSavedReportRepository(db),
		profiles:     persistence.NewPaymentProfileRepository(db),
		payments:     persistence.NewPaymentLifecycleRepository(db),
		savingsPlans: persistence.NewSavingsPlanRepository(db),
		daily:        persistence.NewDailyMeteringJobRepository(db),
		monthEnd:     persistence.NewMonthEndCloseRepository(db),
		organization: persistence.NewOrganizationRepository(db),
		progress:     persistence.NewScenarioLearnerProgressRepository(db),
	}
}

// Run executes a parsed scenario definition and records scenario audit rows.
func (r Runner) Run(ctx context.Context, definition Definition) (RunResult, error) {
	if r.db == nil {
		return RunResult{}, fmt.Errorf("database handle is required")
	}
	definition, err := normalizeAndValidate(definition)
	if err != nil {
		return RunResult{}, err
	}
	startTime, err := scenarioStartTime(definition.Clock.Start)
	if err != nil {
		return RunResult{}, err
	}

	run, err := r.startScenarioRun(ctx, definition, startTime)
	if err != nil {
		return RunResult{}, err
	}

	result := RunResult{
		Run: run,
	}
	if conflict, found, err := r.preflightClosedPeriodPricing(ctx, run.ID, definition, startTime); err != nil {
		return r.failRun(ctx, result, "", err)
	} else if found {
		return r.failRun(ctx, result, conflict.EventID, conflict)
	}

	if err := r.prepareScenarioWorkspace(ctx, definition, startTime); err != nil {
		return r.failRun(ctx, result, "", err)
	}

	state := newScenarioExecutionState(run.ID, definition, startTime)

	for _, event := range definition.Events {
		eventAudit, err := r.applyEvent(ctx, &state, event)
		result.addEventAudit(eventAudit)
		if err := r.recordScenarioEventOutcome(ctx, event, eventAudit, err); err != nil {
			return r.failRun(ctx, result, event.ID, err)
		}
		result.Run.EventsSucceeded++
	}

	if err := r.completeSuccessfulRun(ctx, &result, definition); err != nil {
		return result, err
	}
	return result, nil
}

// RunResult summarizes one scenario execution.
type RunResult struct {
	Run                    ScenarioRun
	Events                 []ScenarioRunEvent
	ResourcesCreated       int
	UsageEventsCreated     int
	MeteringRecordsCreated int
	BillLineItemsCreated   int
	BillsIssued            int
}

// ScenarioRun is the durable audit header for one scenario execution.
type ScenarioRun struct {
	ID                               string
	DefinitionName                   string
	OrganizationTemplate             string
	RandomSeed                       int64
	Status                           string
	ClockStart                       string
	CurrentEventID                   string
	EventsTotal                      int
	EventsSucceeded                  int
	ResourcesCreated                 int
	UsageEventsCreated               int
	MeteringRecordsCreated           int
	BillLineItemsCreated             int
	BillsIssued                      int
	ErrorMessage                     string
	PriceCatalogID                   string
	PriceCatalogSourceURL            string
	PriceCatalogFetchDate            string
	PriceCatalogEffectiveDate        string
	PriceCatalogSupportedRegions     string
	PriceCatalogCompatibilityKey     string
	PriceCatalogCompatibilityStatus  string
	PriceCatalogCompatibilityMessage string
}

// ScenarioRunEvent is the durable audit row for one applied scenario event.
type ScenarioRunEvent struct {
	ID                       string
	ScenarioRunID            string
	ScenarioEventID          string
	ScenarioEventSequence    int
	Action                   EventAction
	ScheduledAt              string
	Status                   string
	ResourceID               string
	UsageEventID             string
	GeneratedUsageEventCount int
	MeteringRecordsCreated   int
	BillLineItemsCreated     int
	BillID                   string
	BillsIssued              int
	ResourcesCreated         int
	UsageEventsCreated       int
	ErrorMessage             string
}

type scenarioExecutionState struct {
	runID                   string
	definition              Definition
	startTime               time.Time
	accountAliasesByKey     map[string]string
	resourceAliasesByKey    map[string]string
	categoryAliasesByKey    map[string]string
	lastInvoiceObligationID string
}

func (r Runner) applyEvent(ctx context.Context, state *scenarioExecutionState, event Event) (ScenarioRunEvent, error) {
	scheduledAt, err := scheduledEventTime(state.startTime, event)
	if err != nil {
		return failedScenarioRunEvent(state.runID, event, state.startTime, err), err
	}
	audit := ScenarioRunEvent{
		ID:                    scenarioRunEventID(state.runID, event),
		ScenarioRunID:         state.runID,
		ScenarioEventID:       event.ID,
		ScenarioEventSequence: event.Sequence,
		Action:                event.Action,
		ScheduledAt:           scheduledAt.Format(time.RFC3339),
		Status:                scenarioRunStatusSucceeded,
	}

	if _, err := r.clock.Set(ctx, audit.ScheduledAt); err != nil {
		audit.Status = scenarioRunStatusFailed
		audit.ErrorMessage = err.Error()
		return audit, err
	}

	spec, ok := scenarioEventActionSpecFor(event.Action)
	if !ok {
		err := fmt.Errorf("scenario event action %q is not executable", event.Action)
		return failScenarioRunEvent(audit, err)
	}
	return spec.apply(ctx, r, state, event, scheduledAt, audit)
}

func scenarioStartTime(startDate string) (time.Time, error) {
	parsed, err := time.Parse(time.DateOnly, strings.TrimSpace(startDate))
	if err != nil {
		return time.Time{}, fmt.Errorf("scenario clock start must use YYYY-MM-DD: %w", err)
	}
	return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
}

func scheduledEventTime(startTime time.Time, event Event) (time.Time, error) {
	if event.At != "" {
		return parseScenarioEventTime(event.At)
	}
	return startTime.AddDate(0, 0, event.Day-1).UTC(), nil
}

func parseScenarioEventTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if parsed, err := time.Parse(time.DateOnly, value); err == nil {
		return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, time.UTC), nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("scenario event time must use YYYY-MM-DD or RFC3339: %w", err)
	}
	return parsed.UTC().Truncate(time.Second), nil
}

// rememberAccount stores aliases that later scenario events can use in account fields.
func (s *scenarioExecutionState) rememberAccount(event Event, accountID string) {
	for _, alias := range []string{accountID, event.AccountID, event.Account, event.ID} {
		key := scenarioAliasKey(alias)
		if key != "" {
			s.accountAliasesByKey[key] = accountID
		}
	}
}

// resolveAccountID maps explicit account IDs, scenario-created aliases, and seeded AnyCompany names to an account ID.
func (s scenarioExecutionState) resolveAccountID(explicitID, name string) string {
	if strings.TrimSpace(explicitID) != "" {
		return strings.TrimSpace(explicitID)
	}
	key := scenarioAliasKey(name)
	if key != "" {
		if accountID := s.accountAliasesByKey[key]; accountID != "" {
			return accountID
		}
	}
	return resolveScenarioAccountID(s.definition.OrganizationTemplate, explicitID, name)
}

func (s *scenarioExecutionState) rememberResource(event Event, resourceID string) {
	for _, alias := range []string{resourceID, event.ResourceID, event.Resource, event.ID} {
		key := scenarioAliasKey(alias)
		if key != "" {
			s.resourceAliasesByKey[key] = resourceID
		}
	}
}

func (s scenarioExecutionState) resolveResourceID(event Event) string {
	for _, alias := range []string{event.ResourceID, event.Resource} {
		key := scenarioAliasKey(alias)
		if key == "" {
			continue
		}
		if resourceID := s.resourceAliasesByKey[key]; resourceID != "" {
			return resourceID
		}
		if alias == event.ResourceID {
			return event.ResourceID
		}
	}
	return ""
}

// rememberCostCategory stores category aliases that later scenario events can reference.
func (s *scenarioExecutionState) rememberCostCategory(event Event, categoryID string) {
	for _, alias := range []string{categoryID, event.CategoryID, event.Category, event.ID} {
		key := scenarioAliasKey(alias)
		if key != "" {
			s.categoryAliasesByKey[key] = categoryID
		}
	}
}

// resolveCostCategoryID maps explicit IDs and scenario-created category aliases to a category ID.
func (s scenarioExecutionState) resolveCostCategoryID(explicitID, name string) string {
	if strings.TrimSpace(explicitID) != "" {
		return strings.TrimSpace(explicitID)
	}
	key := scenarioAliasKey(name)
	if key != "" {
		return s.categoryAliasesByKey[key]
	}
	return ""
}

func scenarioAliasKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func scenarioRunID(definition Definition, attempt int) string {
	base := stableScenarioID(
		"scr",
		"v2",
		scenarioDefinitionIdentity(definition),
		scenarioDefinitionFingerprint(definition),
	)
	if attempt <= 1 {
		return base
	}
	return fmt.Sprintf("%s_a%d", base, attempt)
}

func scenarioDefinitionIdentity(definition Definition) string {
	return strings.Join([]string{
		definition.Name,
		definition.Clock.Start,
		definition.OrganizationTemplate,
		strconv.FormatInt(definition.RandomSeed, 10),
	}, "\x00")
}

func scenarioDefinitionFingerprint(definition Definition) string {
	data, err := json.Marshal(definition)
	if err != nil {
		return fmt.Sprintf("%#v", definition)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func scenarioRunEventID(runID string, event Event) string {
	return stableScenarioID("sce", runID, event.ID)
}

func stableScenarioID(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + "_" + hex.EncodeToString(sum[:])[:16]
}

func resolveScenarioAccountID(template, explicitID, name string) string {
	if strings.TrimSpace(explicitID) != "" {
		return strings.TrimSpace(explicitID)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if persistence.IsAnyCompanyRetailTemplate(template) {
		if accountID, ok := persistence.AnyCompanyRetailAccountIDForName(name); ok {
			return accountID
		}
	}
	return stableScenarioID("acct", name)
}

func chooseFirst(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
