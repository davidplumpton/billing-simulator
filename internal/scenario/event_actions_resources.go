package scenario

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func normalizeCreateAccountEvent(event Event) Event {
	trimScenarioEventStrings(&event.Account, &event.AccountID, &event.AccountEmail, &event.OrganizationID, &event.ParentUnitID)
	return event
}

func normalizeAccountReferenceScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.Account, &event.AccountID)
	return event
}

func normalizeResourceScenarioEvent(event Event) Event {
	event = normalizeAccountReferenceScenarioEvent(event)
	trimScenarioEventStrings(&event.Service, &event.ServiceCode, &event.Resource, &event.ResourceID, &event.ResourceType, &event.Region, &event.Status)
	event.Tags = normalizeStringMap(event.Tags)
	event.Attributes = normalizeStringMap(event.Attributes)
	return event
}

func normalizeUsageScenarioEvent(event Event) Event {
	event = normalizeResourceScenarioEvent(event)
	trimScenarioEventStrings(&event.UsageType, &event.Operation, &event.UsageStartAt, &event.UsageEndAt, &event.Unit)
	return event
}

func normalizeGenerateUsageScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.Resource, &event.ResourceID, &event.Pattern)
	return event
}

func validateCreateResourceScenarioEvent(path string, event Event, problems *validationProblems) {
	validateScenarioTagMap(path+".tags", event.Tags, problems)
	validateStringMap(path+".attributes", event.Attributes, problems)
	validateCreateResourceEvent(path, event, problems)
}

func validateAddUsageScenarioEvent(path string, event Event, problems *validationProblems) {
	validateScenarioTagMap(path+".tags", event.Tags, problems)
	validateStringMap(path+".attributes", event.Attributes, problems)
	validateOptionalTimestamp(path+".usage_start_at", event.UsageStartAt, problems)
	validateOptionalTimestamp(path+".usage_end_at", event.UsageEndAt, problems)
	validateScenarioUsageWindow(path, event, problems)
	validateAddUsageEvent(path, event, problems)
}

func validateResourceUsageScenarioEventSemantics(path, organizationTemplate string, event Event, createdAccounts map[string]string, problems *validationProblems) {
	validateScenarioAccountReference(path+".account", organizationTemplate, event.AccountID, event.Account, createdAccounts, problems)
	validateScenarioEventService(path, event, problems)
}

func preflightCreateAccountScenarioEvent(_ context.Context, _ Runner, state *scenarioExecutionState, _ *scenarioEventPreflight, event Event, _ time.Time) (scenarioClosedPeriodConflict, bool, error) {
	if event.AccountID != "" {
		state.rememberAccount(event, event.AccountID)
	}
	return scenarioClosedPeriodConflict{}, false, nil
}

func preflightCreateResourceScenarioEvent(_ context.Context, _ Runner, state *scenarioExecutionState, preflight *scenarioEventPreflight, event Event, _ time.Time) (scenarioClosedPeriodConflict, bool, error) {
	resourceID := scenarioResourceID(preflight.runID, event)
	accountID := state.resolveAccountID(event.AccountID, event.Account)
	rememberScenarioPreflightResource(state, preflight.resources, event, resourceID, accountID)
	return scenarioClosedPeriodConflict{}, false, nil
}

func preflightAddUsageScenarioEvent(_ context.Context, _ Runner, state *scenarioExecutionState, preflight *scenarioEventPreflight, event Event, scheduledAt time.Time) (scenarioClosedPeriodConflict, bool, error) {
	refs, err := scenarioPreflightUsageRefsForAddUsage(state, preflight.resources, preflight.runID, event, scheduledAt)
	if err != nil {
		return scenarioClosedPeriodConflict{}, false, err
	}
	preflight.plannedUsage = append(preflight.plannedUsage, refs...)
	return scenarioClosedPeriodConflict{}, false, nil
}

func preflightGenerateUsageScenarioEvent(_ context.Context, _ Runner, state *scenarioExecutionState, preflight *scenarioEventPreflight, event Event, scheduledAt time.Time) (scenarioClosedPeriodConflict, bool, error) {
	refs, err := scenarioPreflightUsageRefsForGeneratedUsage(state, preflight.resources, event, scheduledAt)
	if err != nil {
		return scenarioClosedPeriodConflict{}, false, err
	}
	preflight.plannedUsage = append(preflight.plannedUsage, refs...)
	return scenarioClosedPeriodConflict{}, false, nil
}

func applyCreateAccountScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createAccount(ctx, state, event, scheduledAt); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyCreateResourceScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	resource, err := r.createResource(ctx, state, event, scheduledAt)
	if err != nil {
		return failScenarioRunEvent(audit, err)
	}
	audit.ResourceID = resource.ID
	audit.ResourcesCreated = 1
	return audit, nil
}

func applyAddUsageScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	usageEvent, resourceCreated, err := r.addUsage(ctx, state, event, scheduledAt)
	if err != nil {
		return failScenarioRunEvent(audit, err)
	}
	audit.ResourceID = usageEvent.ResourceID
	audit.UsageEventID = usageEvent.ID
	audit.ResourcesCreated = boolToInt(resourceCreated)
	audit.UsageEventsCreated = 1
	return audit, nil
}

func applyGenerateUsageScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	generated, err := r.generateUsage(ctx, state, event, scheduledAt)
	if err != nil {
		return failScenarioRunEvent(audit, err)
	}
	audit.ResourceID = generated.Resource.ID
	audit.GeneratedUsageEventCount = len(generated.Events)
	audit.UsageEventsCreated = generated.EventsCreated
	if len(generated.Events) > 0 {
		audit.UsageEventID = generated.Events[0].ID
	}
	return audit, nil
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

func scenarioResourceID(runID string, event Event) string {
	return stableScenarioID("res_scn", runID, event.ID, chooseFirst(event.ResourceID, event.Resource))
}

func copyScenarioStringMap(values map[string]string) map[string]string {
	copied := map[string]string{}
	for key, value := range values {
		copied[key] = value
	}
	return copied
}
