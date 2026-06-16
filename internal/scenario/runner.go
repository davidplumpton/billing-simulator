package scenario

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

type scenarioServiceDefaults struct {
	ServiceCode         string
	ServiceName         string
	ResourceType        string
	DefaultResourceName string
	RegionCode          string
	UsageType           string
	Operation           string
	UsageUnit           string
	Attributes          map[string]string
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

func isMissingCostCategory(err error) bool {
	return errors.Is(err, persistence.ErrCostCategoryNotFound)
}

// createSavingsPlan prepares a simplified Compute Savings Plan for commitment coverage labs.
func (r Runner) createSavingsPlan(ctx context.Context, state *scenarioExecutionState, event Event) (persistence.SavingsPlanPurchase, error) {
	termStart, ok := parseScenarioDateOrTimestamp(event.TermStartAt)
	if !ok {
		return persistence.SavingsPlanPurchase{}, fmt.Errorf("scenario savings plan %q term_start_at is invalid", event.ID)
	}
	termEnd, ok := parseScenarioDateOrTimestamp(event.TermEndAt)
	if !ok {
		return persistence.SavingsPlanPurchase{}, fmt.Errorf("scenario savings plan %q term_end_at is invalid", event.ID)
	}
	purchaseID := event.SavingsPlanID
	if purchaseID == "" {
		purchaseID = stableScenarioID("sp_scn", state.runID, event.ID, event.OwnerAccount, event.OwnerAccountID, event.UsageType, event.Region)
	}
	return r.savingsPlans.CreatePurchase(ctx, persistence.SavingsPlanPurchaseCreateRequest{
		ID:                     purchaseID,
		PayerAccountID:         state.resolveAccountID(event.PayerAccountID, event.PayerAccount),
		OwnerAccountID:         state.resolveAccountID(event.OwnerAccountID, event.OwnerAccount),
		ReferenceUsageType:     event.UsageType,
		Operation:              event.Operation,
		RegionCode:             event.Region,
		SharingScope:           event.SharingScope,
		TermStartTime:          termStart.Format(time.RFC3339),
		TermEndTime:            termEnd.Format(time.RFC3339),
		HourlyCommitmentMicros: event.HourlyCommitmentMicros,
		UpfrontFeeMicros:       event.UpfrontFeeMicros,
		CurrencyCode:           event.CurrencyCode,
		Status:                 event.Status,
		Description:            event.Description,
	})
}

// createBudget prepares a budget lab guardrail and reuses matching rows so scenario reruns stay executable.
func (r Runner) createBudget(ctx context.Context, state *scenarioExecutionState, payload scenarioBudgetEventPayload) (persistence.Budget, error) {
	if existing, ok, err := r.existingScenarioBudget(ctx, payload); err != nil {
		return persistence.Budget{}, err
	} else if ok {
		return existing, nil
	}

	scopeValue := payload.ScopeValue
	if payload.ScopeType == persistence.BudgetScopeAccount {
		scopeValue = state.resolveAccountID("", payload.ScopeValue)
	}
	thresholds := make([]persistence.BudgetThresholdCreateRequest, 0, len(payload.Thresholds))
	for i, threshold := range payload.Thresholds {
		thresholdID := threshold.ID
		if thresholdID == "" {
			thresholdID = stableScenarioID("budt_scn", state.runID, payload.ID, threshold.Type, strconv.Itoa(threshold.BasisPoints), strconv.Itoa(i))
		}
		thresholds = append(thresholds, persistence.BudgetThresholdCreateRequest{
			ID:                   thresholdID,
			ThresholdType:        threshold.Type,
			ThresholdBasisPoints: threshold.BasisPoints,
		})
	}
	budgetID := payload.BudgetID
	if budgetID == "" {
		budgetID = stableScenarioID("bud_scn", state.runID, payload.ID, payload.BudgetName)
	}
	return r.budgets.CreateBudget(ctx, persistence.BudgetCreateRequest{
		ID:                 budgetID,
		Name:               payload.BudgetName,
		Description:        payload.Description,
		BillingPeriodStart: payload.BillingPeriodStart,
		BillingPeriodEnd:   payload.BillingPeriodEnd,
		BudgetAmountMicros: payload.BudgetAmountMicros,
		CurrencyCode:       payload.CurrencyCode,
		ScopeType:          payload.ScopeType,
		ScopeKey:           payload.ScopeKey,
		ScopeValue:         scopeValue,
		Status:             payload.Status,
		Thresholds:         thresholds,
	})
}

// refreshBudgetForecasts mirrors the browser refresh action for packaged budget labs.
func (r Runner) refreshBudgetForecasts(ctx context.Context, payload scenarioBudgetEventPayload) error {
	forecast, err := r.budgets.RefreshForecastSummaries(ctx, persistence.BudgetForecastRefreshRequest{
		BillingPeriodStart: payload.BillingPeriodStart,
		BillingPeriodEnd:   payload.BillingPeriodEnd,
	})
	if err != nil {
		return err
	}
	evaluations, err := r.budgets.EvaluateBudgets(ctx, persistence.BudgetEvaluationRequest{
		BillingPeriodStart: forecast.BillingPeriodStart,
		BillingPeriodEnd:   forecast.BillingPeriodEnd,
	})
	if err != nil {
		return err
	}
	_, err = r.budgets.RecordAlertNotifications(ctx, evaluations)
	return err
}

// createSavedReport seeds a Cost Explorer drilldown report and updates it on rerun.
func (r Runner) createSavedReport(ctx context.Context, state *scenarioExecutionState, event Event) (persistence.SavedReport, error) {
	request := persistence.SavedReportCreateRequest{
		ID:             event.ReportID,
		Name:           event.ReportName,
		Description:    event.Description,
		OwnerAccountID: state.resolveAccountID(event.OwnerAccountID, event.OwnerAccount),
		OwnerRole:      event.OwnerRole,
		DateRangeStart: event.DateRangeStart,
		DateRangeEnd:   event.DateRangeEnd,
		Granularity:    event.Granularity,
		Filters:        event.Filters,
		Groupings:      scenarioReportGroupings(event.Groupings),
		Metrics:        append([]string(nil), event.Metrics...),
		ChartType:      event.ChartType,
	}
	if request.OwnerRole == "" {
		request.OwnerRole = defaultScenarioReportRole
	}
	if existing, ok, err := r.existingScenarioSavedReport(ctx, request); err != nil {
		return persistence.SavedReport{}, err
	} else if ok {
		return r.rewriteScenarioSavedReport(ctx, existing.ID, request)
	}
	if request.ID == "" {
		request.ID = stableScenarioID("sr_scn", state.runID, event.ID, event.ReportName)
	}
	return r.reports.Create(ctx, request)
}

func (r Runner) existingScenarioBudget(ctx context.Context, payload scenarioBudgetEventPayload) (persistence.Budget, bool, error) {
	var id string
	var err error
	if payload.BudgetID != "" {
		err = r.db.QueryRowContext(ctx, `SELECT id FROM budgets WHERE id = ?`, payload.BudgetID).Scan(&id)
	} else {
		err = r.db.QueryRowContext(ctx, `SELECT id
			FROM budgets
			WHERE billing_period_start = ?
			  AND billing_period_end = ?
			  AND lower(name) = lower(?)`,
			payload.BillingPeriodStart,
			payload.BillingPeriodEnd,
			payload.BudgetName,
		).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return persistence.Budget{}, false, nil
	}
	if err != nil {
		return persistence.Budget{}, false, fmt.Errorf("check existing scenario budget %q: %w", payload.BudgetName, err)
	}
	budget, err := r.budgets.GetBudget(ctx, id)
	if err != nil {
		return persistence.Budget{}, false, err
	}
	return budget, true, nil
}

func (r Runner) existingScenarioSavedReport(ctx context.Context, request persistence.SavedReportCreateRequest) (persistence.SavedReport, bool, error) {
	var id string
	var err error
	if request.ID != "" {
		err = r.db.QueryRowContext(ctx, `SELECT id FROM saved_reports WHERE id = ?`, request.ID).Scan(&id)
	} else {
		err = r.db.QueryRowContext(ctx, `SELECT id
			FROM saved_reports
			WHERE owner_account_id = ?
			  AND owner_role = ?
			  AND lower(name) = lower(?)`,
			request.OwnerAccountID,
			request.OwnerRole,
			request.Name,
		).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return persistence.SavedReport{}, false, nil
	}
	if err != nil {
		return persistence.SavedReport{}, false, fmt.Errorf("check existing scenario saved report %q: %w", request.Name, err)
	}
	report, err := r.reports.Get(ctx, id)
	if err != nil {
		return persistence.SavedReport{}, false, err
	}
	return report, true, nil
}

func (r Runner) rewriteScenarioSavedReport(ctx context.Context, id string, request persistence.SavedReportCreateRequest) (persistence.SavedReport, error) {
	return r.reports.Update(ctx, persistence.SavedReportUpdateRequest{
		ID:             id,
		Name:           request.Name,
		Description:    request.Description,
		OwnerAccountID: request.OwnerAccountID,
		OwnerRole:      request.OwnerRole,
		DateRangeStart: request.DateRangeStart,
		DateRangeEnd:   request.DateRangeEnd,
		Granularity:    request.Granularity,
		Filters:        request.Filters,
		Groupings:      request.Groupings,
		Metrics:        request.Metrics,
		ChartType:      request.ChartType,
	})
}

func scenarioReportGroupings(groupings []ReportGrouping) []persistence.SavedReportGrouping {
	converted := make([]persistence.SavedReportGrouping, 0, len(groupings))
	for _, grouping := range groupings {
		converted = append(converted, persistence.SavedReportGrouping{
			Type: grouping.Type,
			Key:  grouping.Key,
		})
	}
	return converted
}

// createPaymentMethod prepares the payer method state a payment remediation lab should start from.
func (r Runner) createPaymentMethod(ctx context.Context, state *scenarioExecutionState, event Event) (persistence.PaymentMethod, error) {
	profileID, err := r.resolvePaymentProfileID(ctx, state, event)
	if err != nil {
		return persistence.PaymentMethod{}, err
	}
	methodID := event.PaymentMethodID
	if methodID == "" {
		methodID = stableScenarioID("paymeth_scn", state.runID, event.ID, event.DisplayName)
	}
	return r.profiles.CreatePaymentMethod(ctx, persistence.PaymentMethodCreateRequest{
		ID:                      methodID,
		PaymentProfileID:        profileID,
		MethodType:              event.MethodType,
		DisplayName:             event.DisplayName,
		Status:                  event.Status,
		IsDefault:               event.IsDefault,
		CurrencyCode:            event.CurrencyCode,
		CardBrand:               event.CardBrand,
		AccountLast4:            event.AccountLast4,
		ExpirationMonth:         event.ExpirationMonth,
		ExpirationYear:          event.ExpirationYear,
		BankName:                event.BankName,
		RemittanceDestination:   event.RemittanceDestination,
		AdvancePayBalanceMicros: event.AdvancePayBalanceMicros,
		FailureReason:           event.FailureReason,
	})
}

// applyPaymentLifecycleEvent moves the current scenario invoice through the same state machine as the UI.
func (r Runner) applyPaymentLifecycleEvent(ctx context.Context, state *scenarioExecutionState, payload scenarioPaymentLifecycleEventPayload, scheduledAt time.Time) (persistence.PaymentLifecycleResult, error) {
	obligationID := chooseFirst(payload.InvoiceObligationID, state.lastInvoiceObligationID)
	if obligationID == "" {
		return persistence.PaymentLifecycleResult{}, fmt.Errorf("scenario payment event %q requires invoice_obligation_id or a prior close_billing_period event", payload.ID)
	}
	request := persistence.PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligationID,
		AmountMicros:        payload.AmountMicros,
		Reason:              payload.Reason,
		OccurredAt:          scheduledAt.UTC().Format(time.RFC3339),
	}
	switch payload.Action {
	case EventActionSchedulePayment:
		return r.payments.SchedulePayment(ctx, request)
	case EventActionProcessPayment:
		return r.payments.StartProcessing(ctx, request)
	case EventActionFailPayment:
		return r.payments.FailPayment(ctx, request)
	case EventActionMarkPaymentDue:
		return r.payments.MarkDue(ctx, request)
	case EventActionMarkPaymentPastDue:
		return r.payments.MarkPastDue(ctx, request)
	case EventActionCollectPayment:
		return r.payments.ApplyPayment(ctx, request)
	default:
		return persistence.PaymentLifecycleResult{}, fmt.Errorf("scenario event action %q is not a payment lifecycle action", payload.Action)
	}
}

// resolvePaymentProfileID finds the profile named directly or through a payer account reference.
func (r Runner) resolvePaymentProfileID(ctx context.Context, state *scenarioExecutionState, event Event) (string, error) {
	if event.PaymentProfileID != "" {
		return event.PaymentProfileID, nil
	}
	payerID := state.resolveAccountID(event.PayerAccountID, event.PayerAccount)
	if payerID == "" {
		return "", fmt.Errorf("scenario payment method %q requires payment_profile_id or payer_account", event.ID)
	}
	details, found, err := r.profiles.GetDefaultPaymentProfileForPayer(ctx, payerID, chooseFirst(event.CurrencyCode, "USD"))
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("default payment profile for payer %q is not available", payerID)
	}
	return details.Profile.ID, nil
}

// refreshCostAllocationTags records the scenario's current resource tag inventory for billing workflows.
func (r Runner) refreshCostAllocationTags(ctx context.Context, scheduledAt time.Time) (persistence.CostAllocationTagRefreshResult, error) {
	return r.tags.RefreshDiscoveredTags(ctx, scheduledAt.UTC().Format(time.RFC3339))
}

// activateCostAllocationTag turns one discovered resource tag key into a billing cost allocation key.
func (r Runner) activateCostAllocationTag(ctx context.Context, state *scenarioExecutionState, event Event, scheduledAt time.Time) (persistence.CostAllocationTagKey, error) {
	return r.tags.ActivateTag(ctx, persistence.CostAllocationTagActivationRequest{
		ID:                    stableScenarioID("cat_evt_scn", state.runID, event.ID, event.TagKey),
		Key:                   event.TagKey,
		RequestedAt:           scheduledAt.UTC().Format(time.RFC3339),
		EventSource:           scenarioEventSource,
		ScenarioRunID:         state.runID,
		ScenarioEventID:       event.ID,
		ScenarioEventSequence: event.Sequence,
	})
}

// createCostCategory prepares an allocation dimension for a scenario lab, reusing by name so reruns remain executable.
func (r Runner) createCostCategory(ctx context.Context, state *scenarioExecutionState, event Event) (persistence.CostCategory, error) {
	if existing, err := r.categories.GetCategoryByName(ctx, event.Category); err == nil {
		state.rememberCostCategory(event, existing.ID)
		if _, refreshErr := r.categories.RefreshAssignmentsForOpenPeriods(ctx); refreshErr != nil {
			return persistence.CostCategory{}, refreshErr
		}
		return existing, nil
	} else if !isMissingCostCategory(err) {
		return persistence.CostCategory{}, err
	}

	category, err := r.categories.CreateCategory(ctx, persistence.CostCategoryCreateRequest{
		ID:           stableScenarioID("cc_scn", state.runID, event.ID, event.Category),
		Name:         event.Category,
		Description:  event.Description,
		DefaultValue: event.DefaultValue,
		Status:       event.Status,
	})
	if err != nil {
		return persistence.CostCategory{}, err
	}
	state.rememberCostCategory(event, category.ID)
	return category, nil
}

// createCostCategoryRule adds a single-condition assignment rule and refreshes open-period snapshots for lab spend.
func (r Runner) createCostCategoryRule(ctx context.Context, state *scenarioExecutionState, event Event) (persistence.CostCategoryRule, error) {
	categoryID := state.resolveCostCategoryID(event.CategoryID, event.Category)
	if categoryID == "" {
		return persistence.CostCategoryRule{}, fmt.Errorf("scenario cost category %q is not available", chooseFirst(event.CategoryID, event.Category))
	}
	if existing, ok, err := r.existingCostCategoryRule(ctx, categoryID, event.RuleOrder); err != nil {
		return persistence.CostCategoryRule{}, err
	} else if ok {
		if _, refreshErr := r.categories.RefreshAssignmentsForOpenPeriods(ctx); refreshErr != nil {
			return persistence.CostCategoryRule{}, refreshErr
		}
		return existing, nil
	}

	condition := persistence.CostCategoryRuleCondition{
		ID:               stableScenarioID("ccrc_scn", state.runID, event.ID, event.Dimension, strings.Join(event.Values, "\x00")),
		ConditionOrder:   1,
		Dimension:        event.Dimension,
		Operator:         event.Operator,
		TagKey:           event.TagKey,
		CostCategoryID:   state.resolveCostCategoryID(event.ReferencedCategoryID, event.ReferencedCategory),
		CostCategoryName: event.ReferencedCategory,
		Values:           append([]string(nil), event.Values...),
	}
	rule, err := r.categories.CreateRule(ctx, persistence.CostCategoryRuleCreateRequest{
		ID:             stableScenarioID("ccr_scn", state.runID, event.ID, event.Value),
		CostCategoryID: categoryID,
		RuleOrder:      event.RuleOrder,
		Value:          event.Value,
		Description:    event.Description,
		MatchType:      event.MatchType,
		Conditions:     []persistence.CostCategoryRuleCondition{condition},
	})
	if err != nil {
		return persistence.CostCategoryRule{}, err
	}
	return rule, nil
}

// createCostCategorySplitRule adds a split-charge rule and refreshes allocations for current open periods.
func (r Runner) createCostCategorySplitRule(ctx context.Context, state *scenarioExecutionState, event Event) (persistence.CostCategorySplitChargeRule, error) {
	categoryID := state.resolveCostCategoryID(event.CategoryID, event.Category)
	if categoryID == "" {
		return persistence.CostCategorySplitChargeRule{}, fmt.Errorf("scenario cost category %q is not available", chooseFirst(event.CategoryID, event.Category))
	}
	if existing, ok, err := r.existingSplitChargeRule(ctx, categoryID, event.SourceValue); err != nil {
		return persistence.CostCategorySplitChargeRule{}, err
	} else if ok {
		if _, refreshErr := r.splitCharges.RefreshAllocationsForOpenPeriods(ctx); refreshErr != nil {
			return persistence.CostCategorySplitChargeRule{}, refreshErr
		}
		return existing, nil
	}

	targets := make([]persistence.CostCategorySplitChargeTargetCreateRequest, 0, len(event.Targets))
	for i, target := range event.Targets {
		targets = append(targets, persistence.CostCategorySplitChargeTargetCreateRequest{
			ID:               stableScenarioID("ccst_scn", state.runID, event.ID, target.Value),
			TargetOrder:      i + 1,
			TargetValue:      target.Value,
			FixedShareMicros: target.FixedShareMicros,
		})
	}
	rule, err := r.splitCharges.CreateRule(ctx, persistence.CostCategorySplitChargeRuleCreateRequest{
		ID:             stableScenarioID("ccs_scn", state.runID, event.ID, event.SourceValue),
		CostCategoryID: categoryID,
		SourceValue:    event.SourceValue,
		Method:         event.Method,
		Description:    event.Description,
		Status:         event.Status,
		Targets:        targets,
	})
	if err != nil {
		return persistence.CostCategorySplitChargeRule{}, err
	}
	return rule, nil
}

// existingCostCategoryRule finds an already-created rule at the same order for rerunnable scenario definitions.
func (r Runner) existingCostCategoryRule(ctx context.Context, categoryID string, ruleOrder int) (persistence.CostCategoryRule, bool, error) {
	rules, err := r.categories.ListRules(ctx, categoryID)
	if err != nil {
		return persistence.CostCategoryRule{}, false, err
	}
	for _, rule := range rules {
		if rule.RuleOrder == ruleOrder {
			return rule, true, nil
		}
	}
	return persistence.CostCategoryRule{}, false, nil
}

// existingSplitChargeRule finds an already-created split source for rerunnable scenario definitions.
func (r Runner) existingSplitChargeRule(ctx context.Context, categoryID, sourceValue string) (persistence.CostCategorySplitChargeRule, bool, error) {
	rules, err := r.splitCharges.ListRules(ctx, categoryID)
	if err != nil {
		return persistence.CostCategorySplitChargeRule{}, false, err
	}
	for _, rule := range rules {
		if rule.SourceValue == sourceValue {
			return rule, true, nil
		}
	}
	return persistence.CostCategorySplitChargeRule{}, false, nil
}

// createAccount adds a scenario-owned member account and records lifecycle lineage.
func (r Runner) createAccount(ctx context.Context, state *scenarioExecutionState, event Event, scheduledAt time.Time) (persistence.AccountLifecycleResult, error) {
	organizationID := event.OrganizationID
	if organizationID == "" {
		organization, err := r.organization.GetOrganizationByTemplate(ctx, state.definition.OrganizationTemplate)
		if err != nil {
			return persistence.AccountLifecycleResult{}, err
		}
		organizationID = organization.ID
	}
	result, err := r.organization.CreateAccount(ctx, persistence.AccountCreateRequest{
		ID:                    event.AccountID,
		OrganizationID:        organizationID,
		ParentUnitID:          event.ParentUnitID,
		Name:                  event.Account,
		Email:                 event.AccountEmail,
		EffectiveAt:           scheduledAt.UTC().Format(time.RFC3339),
		EventSource:           scenarioEventSource,
		ScenarioRunID:         state.runID,
		ScenarioEventID:       event.ID,
		ScenarioEventSequence: event.Sequence,
	})
	if err != nil {
		return persistence.AccountLifecycleResult{}, err
	}
	state.rememberAccount(event, result.Account.ID)
	return result, nil
}

func (r Runner) createResource(ctx context.Context, state *scenarioExecutionState, event Event, scheduledAt time.Time) (persistence.Resource, error) {
	service, err := scenarioServiceDefaultsForEvent(event)
	if err != nil {
		return persistence.Resource{}, err
	}
	status := event.Status
	if status == "" {
		status = "active"
	}
	startedAt, stoppedAt, deletedAt := scenarioResourceTimes(status, scheduledAt)
	resourceID := scenarioResourceID(state.runID, event)
	attributes := copyScenarioStringMap(service.Attributes)
	for key, value := range event.Attributes {
		attributes[key] = value
	}
	if service.ServiceName != "" {
		attributes["display_service"] = service.ServiceName
	}

	resource, err := r.usage.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:                    resourceID,
		AccountID:             state.resolveAccountID(event.AccountID, event.Account),
		RegionCode:            chooseFirst(event.Region, service.RegionCode, "us-east-1"),
		ServiceCode:           service.ServiceCode,
		ResourceType:          chooseFirst(event.ResourceType, service.ResourceType, "scenario_resource"),
		ResourceName:          chooseFirst(event.Resource, service.DefaultResourceName, event.ID),
		Status:                status,
		StartedAt:             startedAt,
		StoppedAt:             stoppedAt,
		DeletedAt:             deletedAt,
		Attributes:            attributes,
		Tags:                  event.Tags,
		Notes:                 fmt.Sprintf("Created by scenario %q event %q.", state.definition.Name, event.ID),
		EventSource:           scenarioEventSource,
		ScenarioRunID:         state.runID,
		ScenarioEventID:       event.ID,
		ScenarioEventSequence: event.Sequence,
	})
	if err != nil {
		return persistence.Resource{}, err
	}
	state.rememberResource(event, resource.ID)
	return resource, nil
}

func (r Runner) addUsage(ctx context.Context, state *scenarioExecutionState, event Event, scheduledAt time.Time) (persistence.UsageEvent, bool, error) {
	service, err := scenarioServiceDefaultsForEvent(event)
	if err != nil {
		return persistence.UsageEvent{}, false, err
	}
	resource, created, err := r.ensureUsageResource(ctx, state, event, service, scheduledAt)
	if err != nil {
		return persistence.UsageEvent{}, false, err
	}
	quantityMicros, unit, err := scenarioUsageQuantity(event, service)
	if err != nil {
		return persistence.UsageEvent{}, false, err
	}
	usageStart, usageEnd, err := scenarioUsageWindow(scheduledAt, event)
	if err != nil {
		return persistence.UsageEvent{}, created, err
	}
	attributes := copyScenarioStringMap(event.Attributes)
	attributes["scenario_event_id"] = event.ID

	usageEvent, err := r.usage.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                    stableScenarioID("use_scn", state.runID, event.ID, resource.ID),
		ResourceID:            resource.ID,
		ServiceCode:           service.ServiceCode,
		UsageType:             chooseFirst(event.UsageType, service.UsageType),
		Operation:             chooseFirst(event.Operation, service.Operation),
		RegionCode:            chooseFirst(event.Region, service.RegionCode),
		UsageStartTime:        usageStart,
		UsageEndTime:          usageEnd,
		UsageQuantityMicros:   quantityMicros,
		UsageUnit:             unit,
		Attributes:            attributes,
		EventSource:           scenarioEventSource,
		ScenarioRunID:         state.runID,
		ScenarioEventID:       event.ID,
		ScenarioEventSequence: event.Sequence,
	})
	if err != nil {
		return persistence.UsageEvent{}, created, err
	}
	return usageEvent, created, nil
}

func (r Runner) ensureUsageResource(ctx context.Context, state *scenarioExecutionState, event Event, service scenarioServiceDefaults, scheduledAt time.Time) (persistence.Resource, bool, error) {
	if resourceID := state.resolveResourceID(event); resourceID != "" {
		resource, err := r.usage.GetResource(ctx, resourceID)
		if err == nil {
			return resource, false, nil
		}
		if event.ResourceID != "" && event.Resource == "" {
			return persistence.Resource{}, false, err
		}
	}

	createEvent := event
	if createEvent.Resource == "" {
		createEvent.Resource = chooseFirst(service.DefaultResourceName, event.ID)
	}
	if createEvent.ResourceType == "" {
		createEvent.ResourceType = service.ResourceType
	}
	if createEvent.Region == "" {
		createEvent.Region = service.RegionCode
	}
	resource, err := r.createResource(ctx, state, createEvent, scheduledAt)
	if err != nil {
		return persistence.Resource{}, false, err
	}
	return resource, true, nil
}

func (r Runner) generateUsage(ctx context.Context, state *scenarioExecutionState, event Event, scheduledAt time.Time) (persistence.UsageGenerationResult, error) {
	resourceID := state.resolveResourceID(event)
	if resourceID == "" {
		return persistence.UsageGenerationResult{}, fmt.Errorf("scenario resource %q was not created before generate_usage", chooseFirst(event.ResourceID, event.Resource))
	}
	return r.usage.GenerateUsage(ctx, persistence.UsageGenerationRequest{
		ResourceID:            resourceID,
		Pattern:               persistence.UsageGenerationPattern(event.Pattern),
		StartDate:             scheduledAt.Format(time.DateOnly),
		Days:                  event.Days,
		EventSource:           scenarioEventSource,
		ScenarioRunID:         state.runID,
		ScenarioEventID:       event.ID,
		ScenarioEventSequence: event.Sequence,
	})
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

func scenarioResourceTimes(status string, scheduledAt time.Time) (string, string, string) {
	timestamp := scheduledAt.UTC().Format(time.RFC3339)
	switch status {
	case "planned":
		return "", "", ""
	case "stopped":
		return timestamp, timestamp, ""
	case "deleted":
		return timestamp, timestamp, timestamp
	default:
		return timestamp, "", ""
	}
}

func scenarioUsageEndTime(scheduledAt time.Time, event Event) time.Time {
	if event.AmountHours != nil {
		if hours, ok := positiveJSONNumber(event.AmountHours); ok && hours <= 24 {
			return scheduledAt.Add(time.Duration(math.Ceil(hours)) * time.Hour)
		}
	}
	return scheduledAt.AddDate(0, 0, 1)
}

func scenarioUsageWindow(scheduledAt time.Time, event Event) (string, string, error) {
	if event.UsageStartAt == "" && event.UsageEndAt == "" {
		return scheduledAt.UTC().Format(time.RFC3339), scenarioUsageEndTime(scheduledAt, event).Format(time.RFC3339), nil
	}
	start, err := parseScenarioEventTime(event.UsageStartAt)
	if err != nil {
		return "", "", fmt.Errorf("scenario usage_start_at: %w", err)
	}
	end, err := parseScenarioEventTime(event.UsageEndAt)
	if err != nil {
		return "", "", fmt.Errorf("scenario usage_end_at: %w", err)
	}
	if !start.Before(end) {
		return "", "", fmt.Errorf("scenario usage_start_at must be before usage_end_at")
	}
	return start.Format(time.RFC3339), end.Format(time.RFC3339), nil
}

func scenarioUsageQuantity(event Event, service scenarioServiceDefaults) (int64, string, error) {
	if event.QuantityMicros > 0 {
		return event.QuantityMicros, chooseFirst(event.Unit, service.UsageUnit), nil
	}
	if event.Quantity != nil {
		quantity, err := jsonNumberQuantityMicros(event.Quantity)
		return quantity, chooseFirst(event.Unit, service.UsageUnit), err
	}
	if event.AmountGB != nil {
		quantity, err := jsonNumberQuantityMicros(event.AmountGB)
		return quantity, chooseFirst(event.Unit, service.UsageUnit, "GB"), err
	}
	if event.AmountHours != nil {
		quantity, err := jsonNumberQuantityMicros(event.AmountHours)
		return quantity, chooseFirst(event.Unit, "Hours"), err
	}
	return 0, "", fmt.Errorf("scenario usage quantity is required")
}

func jsonNumberQuantityMicros(number *json.Number) (int64, error) {
	value, ok := positiveJSONNumber(number)
	if !ok {
		return 0, fmt.Errorf("scenario usage quantity must be a positive number")
	}
	micros := math.Round(value * 1_000_000)
	if micros > 9_223_372_036_854_775_807 {
		return 0, fmt.Errorf("scenario usage quantity is too large")
	}
	return int64(micros), nil
}

func positiveJSONNumber(number *json.Number) (float64, bool) {
	if number == nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(number.String(), 64)
	if err != nil || math.IsInf(value, 0) || math.IsNaN(value) || value <= 0 {
		return 0, false
	}
	return value, true
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

func scenarioResourceID(runID string, event Event) string {
	return stableScenarioID("res_scn", runID, event.ID, chooseFirst(event.ResourceID, event.Resource))
}

func scenarioRunEventID(runID string, event Event) string {
	return stableScenarioID("sce", runID, event.ID)
}

func stableScenarioID(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + "_" + hex.EncodeToString(sum[:])[:16]
}

func scenarioServiceDefaultsForEvent(event Event) (scenarioServiceDefaults, error) {
	serviceCode := strings.TrimSpace(event.ServiceCode)
	if serviceCode == "" && event.Service != "" {
		serviceCode = scenarioServiceCodeForName(event.Service)
	}
	if serviceCode == "" {
		if event.Service != "" {
			return scenarioServiceDefaults{}, fmt.Errorf("scenario service %q is not supported", event.Service)
		}
		return scenarioServiceDefaults{}, fmt.Errorf("scenario service is required")
	}
	if defaults, ok := scenarioServiceDefaultsByCode()[serviceCode]; ok {
		return defaults, nil
	}
	if event.UsageType != "" && event.Operation != "" && event.Unit != "" {
		return scenarioServiceDefaults{
			ServiceCode:         serviceCode,
			ServiceName:         chooseFirst(event.Service, serviceCode),
			ResourceType:        chooseFirst(event.ResourceType, "scenario_resource"),
			DefaultResourceName: chooseFirst(event.Resource, serviceCode+" scenario resource"),
			RegionCode:          chooseFirst(event.Region, "us-east-1"),
			UsageType:           event.UsageType,
			Operation:           event.Operation,
			UsageUnit:           event.Unit,
		}, nil
	}
	return scenarioServiceDefaults{}, fmt.Errorf("scenario service %q is not supported", chooseFirst(event.Service, event.ServiceCode))
}

func scenarioServiceCodeForName(name string) string {
	return scenarioServiceNameAliases()[scenarioLookupKey(name)]
}

func scenarioServiceDefaultsByCode() map[string]scenarioServiceDefaults {
	return map[string]scenarioServiceDefaults{
		"AmazonEC2": {
			ServiceCode:         "AmazonEC2",
			ServiceName:         "Amazon EC2",
			ResourceType:        "ec2_instance",
			DefaultResourceName: "Scenario EC2 instance",
			RegionCode:          "us-east-1",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageUnit:           "Hours",
			Attributes:          map[string]string{"instance_type": "t3.medium", "operating_system": "linux", "tenancy": "shared"},
		},
		"AmazonEBS": {
			ServiceCode:         "AmazonEBS",
			ServiceName:         "Amazon EBS",
			ResourceType:        "ebs_volume",
			DefaultResourceName: "Scenario gp3 volume",
			RegionCode:          "us-east-1",
			UsageType:           "storage:gp3-gb-month",
			Operation:           "VolumeStorage",
			UsageUnit:           "GBDay",
			Attributes:          map[string]string{"volume_type": "gp3", "size": "100 GB"},
		},
		"AmazonS3": {
			ServiceCode:         "AmazonS3",
			ServiceName:         "Amazon S3",
			ResourceType:        "s3_bucket",
			DefaultResourceName: "Scenario bucket",
			RegionCode:          "us-east-1",
			UsageType:           "storage:standard-gb-month",
			Operation:           "StandardStorage",
			UsageUnit:           "GBDay",
			Attributes:          map[string]string{"storage_class": "standard", "size": "standard"},
		},
		"AWSLambda": {
			ServiceCode:         "AWSLambda",
			ServiceName:         "AWS Lambda",
			ResourceType:        "lambda_function",
			DefaultResourceName: "Scenario function",
			RegionCode:          "us-east-1",
			UsageType:           "requests:lambda-1m",
			Operation:           "Invoke",
			UsageUnit:           "Request",
			Attributes:          map[string]string{"memory_mb": "512", "runtime": "go"},
		},
		"AmazonRDS": {
			ServiceCode:         "AmazonRDS",
			ServiceName:         "Amazon RDS",
			ResourceType:        "rds_instance",
			DefaultResourceName: "Scenario database",
			RegionCode:          "us-east-1",
			UsageType:           "instance-hours:db.t3.medium",
			Operation:           "CreateDBInstance",
			UsageUnit:           "Hours",
			Attributes:          map[string]string{"instance_class": "db.t3.medium", "engine": "postgres"},
		},
		"AmazonVPCNATGateway": {
			ServiceCode:         "AmazonVPCNATGateway",
			ServiceName:         "NAT Gateway",
			ResourceType:        "nat_gateway",
			DefaultResourceName: "Scenario NAT Gateway",
			RegionCode:          "us-east-1",
			UsageType:           "nat-gateway-data-processed-gb",
			Operation:           "NatGatewayDataProcessing",
			UsageUnit:           "GB",
			Attributes:          map[string]string{"network_role": "egress", "size": "standard"},
		},
		"AWSDataTransfer": {
			ServiceCode:         "AWSDataTransfer",
			ServiceName:         "AWS Data Transfer",
			ResourceType:        "data_transfer_path",
			DefaultResourceName: "Scenario internet egress path",
			RegionCode:          "global",
			UsageType:           "data-transfer-out-internet-gb",
			Operation:           "DataTransferOut",
			UsageUnit:           "GB",
			Attributes:          map[string]string{"path": "internet", "size": "internet"},
		},
		"AmazonCloudWatchLogs": {
			ServiceCode:         "AmazonCloudWatchLogs",
			ServiceName:         "CloudWatch Logs",
			ResourceType:        "log_group",
			DefaultResourceName: "Scenario log group",
			RegionCode:          "us-east-1",
			UsageType:           "logs-ingestion-gb",
			Operation:           "PutLogEvents",
			UsageUnit:           "GB",
			Attributes:          map[string]string{"retention": "standard"},
		},
	}
}

func scenarioServiceNameAliases() map[string]string {
	aliases := map[string]string{}
	for code, defaults := range scenarioServiceDefaultsByCode() {
		aliases[scenarioLookupKey(code)] = code
		aliases[scenarioLookupKey(defaults.ServiceName)] = code
	}
	aliases[scenarioLookupKey("EC2")] = "AmazonEC2"
	aliases[scenarioLookupKey("S3")] = "AmazonS3"
	aliases[scenarioLookupKey("Lambda")] = "AWSLambda"
	aliases[scenarioLookupKey("RDS")] = "AmazonRDS"
	aliases[scenarioLookupKey("NAT Gateway")] = "AmazonVPCNATGateway"
	return aliases
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

func scenarioLookupKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func chooseFirst(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func copyScenarioStringMap(values map[string]string) map[string]string {
	copied := map[string]string{}
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
