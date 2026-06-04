package scenario

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
)

// Runner applies parsed scenario definitions to a workspace database.
type Runner struct {
	db       *sql.DB
	clock    persistence.SimulatorClockRepository
	usage    persistence.ResourceUsageRepository
	daily    persistence.DailyMeteringJobRepository
	monthEnd persistence.MonthEndCloseRepository
}

// NewRunner creates a scenario runner backed by the workspace database.
func NewRunner(db *sql.DB) Runner {
	return Runner{
		db:       db,
		clock:    persistence.NewSimulatorClockRepository(db),
		usage:    persistence.NewResourceUsageRepository(db),
		daily:    persistence.NewDailyMeteringJobRepository(db),
		monthEnd: persistence.NewMonthEndCloseRepository(db),
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

	run := ScenarioRun{
		ID:                   scenarioRunID(definition),
		DefinitionName:       definition.Name,
		OrganizationTemplate: definition.OrganizationTemplate,
		RandomSeed:           definition.RandomSeed,
		Status:               scenarioRunStatusRunning,
		ClockStart:           startTime.Format(time.RFC3339),
		EventsTotal:          len(definition.Events),
	}
	if err := r.insertScenarioRun(ctx, run); err != nil {
		return RunResult{}, err
	}

	result := RunResult{
		Run: run,
	}
	state := scenarioExecutionState{
		runID:                run.ID,
		definition:           definition,
		startTime:            startTime,
		resourceAliasesByKey: map[string]string{},
	}

	if _, err := r.clock.Set(ctx, startTime.Format(time.RFC3339)); err != nil {
		return r.failRun(ctx, result, "", fmt.Errorf("set scenario start clock: %w", err))
	}

	for _, event := range definition.Events {
		eventAudit, err := r.applyEvent(ctx, &state, event)
		result.Events = append(result.Events, eventAudit)
		result.ResourcesCreated += eventAudit.ResourcesCreated
		result.UsageEventsCreated += eventAudit.UsageEventsCreated
		result.MeteringRecordsCreated += eventAudit.MeteringRecordsCreated
		result.BillLineItemsCreated += eventAudit.BillLineItemsCreated
		result.BillsIssued += eventAudit.BillsIssued
		result.Run.CurrentEventID = event.ID
		if err != nil {
			if insertErr := r.insertScenarioRunEvent(ctx, eventAudit); insertErr != nil {
				err = fmt.Errorf("%w; record failed scenario event: %v", err, insertErr)
			}
			return r.failRun(ctx, result, event.ID, err)
		}
		if err := r.insertScenarioRunEvent(ctx, eventAudit); err != nil {
			return r.failRun(ctx, result, event.ID, fmt.Errorf("record scenario event %q: %w", event.ID, err))
		}
		result.Run.EventsSucceeded++
	}

	result.Run.Status = scenarioRunStatusSucceeded
	result.Run.ResourcesCreated = result.ResourcesCreated
	result.Run.UsageEventsCreated = result.UsageEventsCreated
	result.Run.MeteringRecordsCreated = result.MeteringRecordsCreated
	result.Run.BillLineItemsCreated = result.BillLineItemsCreated
	result.Run.BillsIssued = result.BillsIssued
	if err := r.completeScenarioRun(ctx, result.Run); err != nil {
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
	ID                     string
	DefinitionName         string
	OrganizationTemplate   string
	RandomSeed             int64
	Status                 string
	ClockStart             string
	CurrentEventID         string
	EventsTotal            int
	EventsSucceeded        int
	ResourcesCreated       int
	UsageEventsCreated     int
	MeteringRecordsCreated int
	BillLineItemsCreated   int
	BillsIssued            int
	ErrorMessage           string
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
	runID                string
	definition           Definition
	startTime            time.Time
	resourceAliasesByKey map[string]string
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

	switch event.Action {
	case EventActionCreateResource:
		resource, err := r.createResource(ctx, state, event, scheduledAt)
		if err != nil {
			return failScenarioRunEvent(audit, err)
		}
		audit.ResourceID = resource.ID
		audit.ResourcesCreated = 1
	case EventActionAddUsage:
		usageEvent, resourceCreated, err := r.addUsage(ctx, state, event, scheduledAt)
		if err != nil {
			return failScenarioRunEvent(audit, err)
		}
		audit.ResourceID = usageEvent.ResourceID
		audit.UsageEventID = usageEvent.ID
		audit.ResourcesCreated = boolToInt(resourceCreated)
		audit.UsageEventsCreated = 1
	case EventActionGenerateUsage:
		generated, err := r.generateUsage(ctx, state, event, scheduledAt)
		if err != nil {
			return failScenarioRunEvent(audit, err)
		}
		audit.ResourceID = generated.Resource.ID
		audit.GeneratedUsageEventCount = len(generated.Events)
		audit.UsageEventsCreated = len(generated.Events)
		if len(generated.Events) > 0 {
			audit.UsageEventID = generated.Events[0].ID
		}
	case EventActionAdvanceClock:
		if _, err := r.clock.Advance(ctx, persistence.SimulatorClockAdvanceRequest{
			Amount: event.Amount,
			Unit:   persistence.SimulatorClockAdvanceUnit(event.Unit),
		}); err != nil {
			return failScenarioRunEvent(audit, err)
		}
	case EventActionRunDailyMetering:
		metering, err := r.daily.Run(ctx, persistence.DailyMeteringJobRequest{
			Trigger:        persistence.DailyMeteringJobTriggerOnDemand,
			PayerAccountID: resolveScenarioAccountID(state.definition.OrganizationTemplate, event.PayerAccountID, event.PayerAccount),
		})
		if err != nil {
			return failScenarioRunEvent(audit, err)
		}
		audit.MeteringRecordsCreated = metering.MeteringRecordsCreated
		audit.BillLineItemsCreated = metering.BillLineItemsCreated
	case EventActionCloseBillingPeriod, EventActionIssueBill:
		closed, err := r.monthEnd.ClosePreviousPeriod(ctx, persistence.MonthEndCloseRequest{
			PayerAccountID: resolveScenarioAccountID(state.definition.OrganizationTemplate, event.PayerAccountID, event.PayerAccount),
			PeriodStart:    event.BillingPeriodStart,
			PeriodEnd:      event.BillingPeriodEnd,
		})
		if err != nil {
			return failScenarioRunEvent(audit, err)
		}
		audit.MeteringRecordsCreated = closed.MeteringRecordsCreated
		audit.BillLineItemsCreated = closed.BillLineItemsCreated
		audit.BillID = closed.Bill.ID
		audit.BillsIssued = boolToInt(closed.Bill.ID != "")
	default:
		err := fmt.Errorf("scenario event action %q is not executable", event.Action)
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
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
	resourceID := event.ResourceID
	if resourceID == "" {
		resourceID = stableScenarioID("res_scn", state.runID, event.ID, event.Resource)
	}
	attributes := copyScenarioStringMap(service.Attributes)
	for key, value := range event.Attributes {
		attributes[key] = value
	}
	if service.ServiceName != "" {
		attributes["display_service"] = service.ServiceName
	}

	resource, err := r.usage.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:                    resourceID,
		AccountID:             resolveScenarioAccountID(state.definition.OrganizationTemplate, event.AccountID, event.Account),
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
	usageStart := scheduledAt.UTC().Format(time.RFC3339)
	usageEnd := scenarioUsageEndTime(scheduledAt, event).Format(time.RFC3339)
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
	if createEvent.ResourceID == "" {
		createEvent.ResourceID = stableScenarioID("res_scn", state.runID, event.ID, "implicit")
	}
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

func (r Runner) insertScenarioRun(ctx context.Context, run ScenarioRun) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO scenario_runs (
			id,
			definition_name,
			organization_template,
			random_seed,
			status,
			clock_start,
			events_total
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.ID,
		run.DefinitionName,
		run.OrganizationTemplate,
		run.RandomSeed,
		run.Status,
		run.ClockStart,
		run.EventsTotal,
	)
	if err != nil {
		return fmt.Errorf("insert scenario run %q: %w", run.ID, err)
	}
	return nil
}

func (r Runner) insertScenarioRunEvent(ctx context.Context, event ScenarioRunEvent) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO scenario_run_events (
			id,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence,
			action,
			scheduled_at,
			status,
			resource_id,
			usage_event_id,
			generated_usage_event_count,
			metering_records_created,
			bill_line_items_created,
			bill_id,
			error_message
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.ScenarioRunID,
		event.ScenarioEventID,
		event.ScenarioEventSequence,
		string(event.Action),
		event.ScheduledAt,
		event.Status,
		event.ResourceID,
		event.UsageEventID,
		event.GeneratedUsageEventCount,
		event.MeteringRecordsCreated,
		event.BillLineItemsCreated,
		event.BillID,
		event.ErrorMessage,
	)
	if err != nil {
		return fmt.Errorf("insert scenario event %q: %w", event.ScenarioEventID, err)
	}
	return nil
}

func (r Runner) completeScenarioRun(ctx context.Context, run ScenarioRun) error {
	_, err := r.db.ExecContext(
		ctx,
		`UPDATE scenario_runs
		 SET status = ?,
			current_event_id = ?,
			events_succeeded = ?,
			resources_created = ?,
			usage_events_created = ?,
			metering_records_created = ?,
			bill_line_items_created = ?,
			bills_issued = ?,
			error_message = ?,
			completed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE id = ?`,
		run.Status,
		run.CurrentEventID,
		run.EventsSucceeded,
		run.ResourcesCreated,
		run.UsageEventsCreated,
		run.MeteringRecordsCreated,
		run.BillLineItemsCreated,
		run.BillsIssued,
		run.ErrorMessage,
		run.ID,
	)
	if err != nil {
		return fmt.Errorf("complete scenario run %q: %w", run.ID, err)
	}
	return nil
}

func (r Runner) failRun(ctx context.Context, result RunResult, currentEventID string, runErr error) (RunResult, error) {
	result.Run.Status = scenarioRunStatusFailed
	result.Run.CurrentEventID = currentEventID
	result.Run.ResourcesCreated = result.ResourcesCreated
	result.Run.UsageEventsCreated = result.UsageEventsCreated
	result.Run.MeteringRecordsCreated = result.MeteringRecordsCreated
	result.Run.BillLineItemsCreated = result.BillLineItemsCreated
	result.Run.BillsIssued = result.BillsIssued
	result.Run.ErrorMessage = runErr.Error()
	if err := r.completeScenarioRun(ctx, result.Run); err != nil {
		return result, fmt.Errorf("%w; complete failed scenario run: %v", runErr, err)
	}
	return result, runErr
}

func failedScenarioRunEvent(runID string, event Event, scheduledAt time.Time, err error) ScenarioRunEvent {
	return ScenarioRunEvent{
		ID:                    scenarioRunEventID(runID, event),
		ScenarioRunID:         runID,
		ScenarioEventID:       event.ID,
		ScenarioEventSequence: event.Sequence,
		Action:                event.Action,
		ScheduledAt:           scheduledAt.Format(time.RFC3339),
		Status:                scenarioRunStatusFailed,
		ErrorMessage:          err.Error(),
	}
}

func failScenarioRunEvent(event ScenarioRunEvent, err error) (ScenarioRunEvent, error) {
	event.Status = scenarioRunStatusFailed
	event.ErrorMessage = err.Error()
	return event, err
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

func scenarioAliasKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func scenarioRunID(definition Definition) string {
	return stableScenarioID(
		"scr",
		definition.Name,
		definition.Clock.Start,
		definition.OrganizationTemplate,
		strconv.FormatInt(definition.RandomSeed, 10),
	)
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
	if scenarioLookupKey(template) == scenarioLookupKey("anycompany-retail") {
		if accountID := anyCompanyRetailAccountAliases()[scenarioLookupKey(name)]; accountID != "" {
			return accountID
		}
	}
	return stableScenarioID("acct", name)
}

func anyCompanyRetailAccountAliases() map[string]string {
	return map[string]string{
		scenarioLookupKey("Management"):           "999988887777",
		scenarioLookupKey("Management Account"):   "999988887777",
		scenarioLookupKey("Log Archive"):          "000011112222",
		scenarioLookupKey("Audit"):                "000011112223",
		scenarioLookupKey("Shared Networking"):    "222233334444",
		scenarioLookupKey("Platform Services"):    "222233334445",
		scenarioLookupKey("Developer Sandbox 1"):  "333344445555",
		scenarioLookupKey("Developer Sandbox 2"):  "333344445556",
		scenarioLookupKey("Storefront Dev"):       "111122223332",
		scenarioLookupKey("Storefront Prod"):      "111122223333",
		scenarioLookupKey("Payments Dev"):         "444455556665",
		scenarioLookupKey("Payments Prod"):        "444455556666",
		scenarioLookupKey("Analytics Prod"):       "555566667777",
		scenarioLookupKey("Deprecated Prototype"): "666677778888",
	}
}

func anyCompanyRetailAccountNames() []string {
	return []string{
		"Management",
		"Management Account",
		"Log Archive",
		"Audit",
		"Shared Networking",
		"Platform Services",
		"Developer Sandbox 1",
		"Developer Sandbox 2",
		"Storefront Dev",
		"Storefront Prod",
		"Payments Dev",
		"Payments Prod",
		"Analytics Prod",
		"Deprecated Prototype",
	}
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
