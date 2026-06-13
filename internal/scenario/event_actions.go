package scenario

import (
	"context"
	"fmt"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

type scenarioEventActionSpec struct {
	action            EventAction
	normalize         func(Event) Event
	validate          func(string, Event, *validationProblems)
	validateSemantics func(string, string, Event, map[string]string, *validationProblems)
	preflight         func(context.Context, Runner, *scenarioExecutionState, *scenarioEventPreflight, Event, time.Time) (scenarioClosedPeriodConflict, bool, error)
	apply             func(context.Context, Runner, *scenarioExecutionState, Event, time.Time, ScenarioRunEvent) (ScenarioRunEvent, error)
}

// scenarioEventActionSpecFor returns the action-local behavior for a parsed scenario event.
func scenarioEventActionSpecFor(action EventAction) (scenarioEventActionSpec, bool) {
	spec, ok := scenarioEventActionSpecsByAction[action]
	return spec, ok
}

var scenarioEventActionSpecsByAction = newScenarioEventActionSpecsByAction()

func newScenarioEventActionSpecsByAction() map[EventAction]scenarioEventActionSpec {
	specs := []scenarioEventActionSpec{
		{
			action:    EventActionCreateAccount,
			normalize: normalizeCreateAccountEvent,
			validate:  validateCreateAccountEvent,
			preflight: preflightCreateAccountScenarioEvent,
			apply:     applyCreateAccountScenarioEvent,
		},
		{
			action:            EventActionCreateResource,
			normalize:         normalizeResourceScenarioEvent,
			validate:          validateCreateResourceScenarioEvent,
			validateSemantics: validateResourceUsageScenarioEventSemantics,
			preflight:         preflightCreateResourceScenarioEvent,
			apply:             applyCreateResourceScenarioEvent,
		},
		{
			action:            EventActionAddUsage,
			normalize:         normalizeUsageScenarioEvent,
			validate:          validateAddUsageScenarioEvent,
			validateSemantics: validateResourceUsageScenarioEventSemantics,
			preflight:         preflightAddUsageScenarioEvent,
			apply:             applyAddUsageScenarioEvent,
		},
		{
			action:    EventActionGenerateUsage,
			normalize: normalizeGenerateUsageScenarioEvent,
			validate:  validateGenerateUsageEvent,
			preflight: preflightGenerateUsageScenarioEvent,
			apply:     applyGenerateUsageScenarioEvent,
		},
		{
			action:    EventActionAdvanceClock,
			normalize: normalizeAdvanceClockScenarioEvent,
			validate:  validateAdvanceClockEvent,
			apply:     applyAdvanceClockScenarioEvent,
		},
		{
			action:            EventActionRunDailyMetering,
			normalize:         normalizePayerScenarioEvent,
			validate:          validateBillingEvent,
			validateSemantics: validatePayerScenarioEventSemantics,
			preflight:         preflightRunDailyMeteringScenarioEvent,
			apply:             applyRunDailyMeteringScenarioEvent,
		},
		{
			action:            EventActionCloseBillingPeriod,
			normalize:         normalizeBillingPeriodScenarioEvent,
			validate:          validateBillingPeriodScenarioEvent,
			validateSemantics: validatePayerScenarioEventSemantics,
			preflight:         preflightCloseBillingPeriodScenarioEvent,
			apply:             applyCloseBillingPeriodScenarioEvent,
		},
		{
			action:            EventActionIssueBill,
			normalize:         normalizeBillingPeriodScenarioEvent,
			validate:          validateBillingPeriodScenarioEvent,
			validateSemantics: validatePayerScenarioEventSemantics,
			preflight:         preflightCloseBillingPeriodScenarioEvent,
			apply:             applyCloseBillingPeriodScenarioEvent,
		},
		{
			action:    EventActionRefreshCostAllocationTags,
			normalize: normalizeNoopScenarioEvent,
			validate:  validateNoopScenarioEvent,
			apply:     applyRefreshCostAllocationTagsScenarioEvent,
		},
		{
			action:    EventActionActivateCostAllocationTag,
			normalize: normalizeCostAllocationTagScenarioEvent,
			validate:  validateCostAllocationTagEvent,
			apply:     applyActivateCostAllocationTagScenarioEvent,
		},
		{
			action:    EventActionCreateCostCategory,
			normalize: normalizeCreateCostCategoryScenarioEvent,
			validate:  validateCreateCostCategoryEvent,
			apply:     applyCreateCostCategoryScenarioEvent,
		},
		{
			action:    EventActionCreateCostCategoryRule,
			normalize: normalizeCreateCostCategoryRuleScenarioEvent,
			validate:  validateCreateCostCategoryRuleEvent,
			apply:     applyCreateCostCategoryRuleScenarioEvent,
		},
		{
			action:    EventActionCreateCostCategorySplitRule,
			normalize: normalizeCreateCostCategorySplitRuleScenarioEvent,
			validate:  validateCreateCostCategorySplitRuleEvent,
			apply:     applyCreateCostCategorySplitRuleScenarioEvent,
		},
		{
			action:            EventActionCreatePaymentMethod,
			normalize:         normalizeCreatePaymentMethodScenarioEvent,
			validate:          validateCreatePaymentMethodEvent,
			validateSemantics: validatePaymentMethodScenarioEventSemantics,
			apply:             applyCreatePaymentMethodScenarioEvent,
		},
		{
			action:    EventActionSchedulePayment,
			normalize: normalizePaymentLifecycleScenarioEvent,
			validate:  validatePaymentLifecycleEvent,
			apply:     applyPaymentLifecycleScenarioEvent,
		},
		{
			action:    EventActionProcessPayment,
			normalize: normalizePaymentLifecycleScenarioEvent,
			validate:  validatePaymentLifecycleEvent,
			apply:     applyPaymentLifecycleScenarioEvent,
		},
		{
			action:    EventActionFailPayment,
			normalize: normalizePaymentLifecycleScenarioEvent,
			validate:  validatePaymentLifecycleEvent,
			apply:     applyPaymentLifecycleScenarioEvent,
		},
		{
			action:    EventActionMarkPaymentDue,
			normalize: normalizePaymentLifecycleScenarioEvent,
			validate:  validatePaymentLifecycleEvent,
			apply:     applyPaymentLifecycleScenarioEvent,
		},
		{
			action:    EventActionMarkPaymentPastDue,
			normalize: normalizePaymentLifecycleScenarioEvent,
			validate:  validatePaymentLifecycleEvent,
			apply:     applyPaymentLifecycleScenarioEvent,
		},
		{
			action:    EventActionCollectPayment,
			normalize: normalizePaymentLifecycleScenarioEvent,
			validate:  validatePaymentLifecycleEvent,
			apply:     applyPaymentLifecycleScenarioEvent,
		},
		{
			action:    EventActionCreateBudget,
			normalize: normalizeCreateBudgetScenarioEvent,
			validate:  validateCreateBudgetScenarioEvent,
			apply:     applyCreateBudgetScenarioEvent,
		},
		{
			action:    EventActionRefreshBudgetForecasts,
			normalize: normalizeRefreshBudgetForecastsScenarioEvent,
			validate:  validateRefreshBudgetForecastsScenarioEvent,
			apply:     applyRefreshBudgetForecastsScenarioEvent,
		},
		{
			action:            EventActionCreateSavingsPlan,
			normalize:         normalizeCreateSavingsPlanScenarioEvent,
			validate:          validateCreateSavingsPlanScenarioEvent,
			validateSemantics: validateCreateSavingsPlanScenarioEventSemantics,
			apply:             applyCreateSavingsPlanScenarioEvent,
		},
		{
			action:            EventActionCreateSavedReport,
			normalize:         normalizeCreateSavedReportScenarioEvent,
			validate:          validateCreateSavedReportScenarioEvent,
			validateSemantics: validateCreateSavedReportScenarioEventSemantics,
			apply:             applyCreateSavedReportScenarioEvent,
		},
	}

	byAction := make(map[EventAction]scenarioEventActionSpec, len(specs))
	for _, spec := range specs {
		if spec.normalize == nil {
			spec.normalize = normalizeNoopScenarioEvent
		}
		if spec.validate == nil {
			spec.validate = validateNoopScenarioEvent
		}
		if spec.validateSemantics == nil {
			spec.validateSemantics = validateNoopScenarioEventSemantics
		}
		if spec.preflight == nil {
			spec.preflight = preflightNoopScenarioEvent
		}
		if _, exists := byAction[spec.action]; exists {
			panic("duplicate scenario event action spec " + string(spec.action))
		}
		byAction[spec.action] = spec
	}
	return byAction
}

func normalizeEventEnvelope(event Event, index int) Event {
	event.ID = strings.TrimSpace(event.ID)
	if event.ID == "" {
		event.ID = fmt.Sprintf("event-%03d", index+1)
	}
	event.Sequence = index + 1
	event.At = strings.TrimSpace(event.At)
	event.Action = EventAction(strings.ToLower(strings.TrimSpace(string(event.Action))))
	return event
}

func normalizeNoopScenarioEvent(event Event) Event {
	return event
}

func trimScenarioEventStrings(values ...*string) {
	for _, value := range values {
		*value = strings.TrimSpace(*value)
	}
}

func trimUpperScenarioEventString(value *string) {
	*value = strings.ToUpper(strings.TrimSpace(*value))
}

func normalizeCreateAccountEvent(event Event) Event {
	trimScenarioEventStrings(&event.Account, &event.AccountID, &event.AccountEmail, &event.OrganizationID, &event.ParentUnitID)
	return event
}

func normalizeAccountReferenceScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.Account, &event.AccountID)
	return event
}

func normalizePayerScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.PayerAccount, &event.PayerAccountID)
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

func normalizeAdvanceClockScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.Unit)
	return event
}

func normalizeBillingPeriodScenarioEvent(event Event) Event {
	event = normalizePayerScenarioEvent(event)
	trimScenarioEventStrings(&event.BillingPeriodStart, &event.BillingPeriodEnd)
	return event
}

func normalizeCostAllocationTagScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.TagKey)
	return event
}

func normalizeCreateCostCategoryScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.Category, &event.CategoryID, &event.DefaultValue, &event.Description, &event.Status)
	return event
}

func normalizeCreateCostCategoryRuleScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.Category, &event.CategoryID, &event.Description, &event.Value, &event.MatchType, &event.Dimension, &event.Operator, &event.TagKey, &event.ReferencedCategory, &event.ReferencedCategoryID)
	for i := range event.Values {
		event.Values[i] = strings.TrimSpace(event.Values[i])
	}
	return event
}

func normalizeCreateCostCategorySplitRuleScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.Category, &event.CategoryID, &event.SourceValue, &event.Method, &event.Description, &event.Status)
	for i := range event.Targets {
		event.Targets[i].Value = strings.TrimSpace(event.Targets[i].Value)
	}
	return event
}

func normalizeCreatePaymentMethodScenarioEvent(event Event) Event {
	event = normalizePayerScenarioEvent(event)
	trimScenarioEventStrings(&event.PaymentProfileID, &event.PaymentMethodID, &event.MethodType, &event.DisplayName, &event.Status, &event.CardBrand, &event.AccountLast4, &event.BankName, &event.RemittanceDestination, &event.FailureReason)
	trimUpperScenarioEventString(&event.CurrencyCode)
	return event
}

func normalizePaymentLifecycleScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.InvoiceObligationID, &event.Reason)
	return event
}

func normalizeCreateBudgetScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.BudgetID, &event.BudgetName, &event.Description, &event.BillingPeriodStart, &event.BillingPeriodEnd, &event.ScopeType, &event.ScopeKey, &event.ScopeValue, &event.Status)
	trimUpperScenarioEventString(&event.CurrencyCode)
	for i := range event.Thresholds {
		trimScenarioEventStrings(&event.Thresholds[i].ID, &event.Thresholds[i].Type)
	}
	return event
}

func normalizeRefreshBudgetForecastsScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.BillingPeriodStart, &event.BillingPeriodEnd)
	return event
}

func normalizeCreateSavingsPlanScenarioEvent(event Event) Event {
	event = normalizePayerScenarioEvent(event)
	trimScenarioEventStrings(&event.OwnerAccount, &event.OwnerAccountID, &event.SavingsPlanID, &event.UsageType, &event.Operation, &event.Region, &event.TermStartAt, &event.TermEndAt, &event.SharingScope, &event.Description, &event.Status)
	trimUpperScenarioEventString(&event.CurrencyCode)
	return event
}

func normalizeCreateSavedReportScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.ReportID, &event.ReportName, &event.Description, &event.OwnerAccount, &event.OwnerAccountID, &event.OwnerRole, &event.DateRangeStart, &event.DateRangeEnd, &event.Granularity, &event.ChartType)
	event.Filters = normalizeStringListMap(event.Filters)
	for i := range event.Groupings {
		trimScenarioEventStrings(&event.Groupings[i].Type, &event.Groupings[i].Key)
	}
	event.Metrics = normalizeStringList(event.Metrics)
	return event
}

func validateNoopScenarioEvent(string, Event, *validationProblems) {}

func validateNoopScenarioEventSemantics(string, string, Event, map[string]string, *validationProblems) {
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

func validateBillingPeriodScenarioEvent(path string, event Event, problems *validationProblems) {
	validateOptionalDate(path+".billing_period_start", event.BillingPeriodStart, problems)
	validateOptionalDate(path+".billing_period_end", event.BillingPeriodEnd, problems)
	if event.BillingPeriodStart != "" && event.BillingPeriodEnd == "" {
		problems.add("%s.billing_period_end is required when billing_period_start is set", path)
	}
	if event.BillingPeriodEnd != "" && event.BillingPeriodStart == "" {
		problems.add("%s.billing_period_start is required when billing_period_end is set", path)
	}
	validateBillingEvent(path, event, problems)
}

func validateRefreshBudgetForecastsScenarioEvent(path string, event Event, problems *validationProblems) {
	validateOptionalDate(path+".billing_period_start", event.BillingPeriodStart, problems)
	validateOptionalDate(path+".billing_period_end", event.BillingPeriodEnd, problems)
	if event.BillingPeriodStart != "" && event.BillingPeriodEnd == "" {
		problems.add("%s.billing_period_end is required when billing_period_start is set", path)
	}
	if event.BillingPeriodEnd != "" && event.BillingPeriodStart == "" {
		problems.add("%s.billing_period_start is required when billing_period_end is set", path)
	}
}

func validateCreateBudgetScenarioEvent(path string, event Event, problems *validationProblems) {
	validateOptionalDate(path+".billing_period_start", event.BillingPeriodStart, problems)
	validateOptionalDate(path+".billing_period_end", event.BillingPeriodEnd, problems)
	if event.BillingPeriodStart != "" && event.BillingPeriodEnd == "" {
		problems.add("%s.billing_period_end is required when billing_period_start is set", path)
	}
	if event.BillingPeriodEnd != "" && event.BillingPeriodStart == "" {
		problems.add("%s.billing_period_start is required when billing_period_end is set", path)
	}
	validateCreateBudgetEvent(path, event, problems)
}

func validateCreateSavingsPlanScenarioEvent(path string, event Event, problems *validationProblems) {
	validateOptionalTimestamp(path+".term_start_at", event.TermStartAt, problems)
	validateOptionalTimestamp(path+".term_end_at", event.TermEndAt, problems)
	if event.TermStartAt == "" || event.TermEndAt == "" {
		problems.add("%s.term_start_at and %s.term_end_at are required for create_savings_plan", path, path)
	}
	if event.TermStartAt != "" && event.TermEndAt != "" {
		start, startOK := parseScenarioDateOrTimestamp(event.TermStartAt)
		end, endOK := parseScenarioDateOrTimestamp(event.TermEndAt)
		if startOK && endOK && !start.Before(end) {
			problems.add("%s.term_start_at must be before term_end_at", path)
		}
	}
	validateCreateSavingsPlanEvent(path, event, problems)
}

func validateCreateSavedReportScenarioEvent(path string, event Event, problems *validationProblems) {
	validateOptionalDate(path+".date_range_start", event.DateRangeStart, problems)
	validateOptionalDate(path+".date_range_end", event.DateRangeEnd, problems)
	if event.DateRangeStart != "" && event.DateRangeEnd == "" {
		problems.add("%s.date_range_end is required when date_range_start is set", path)
	}
	if event.DateRangeEnd != "" && event.DateRangeStart == "" {
		problems.add("%s.date_range_start is required when date_range_end is set", path)
	}
	validateCreateSavedReportEvent(path, event, problems)
}

func validateResourceUsageScenarioEventSemantics(path, organizationTemplate string, event Event, createdAccounts map[string]string, problems *validationProblems) {
	validateScenarioAccountReference(path+".account", organizationTemplate, event.AccountID, event.Account, createdAccounts, problems)
	validateScenarioEventService(path, event, problems)
}

func validatePayerScenarioEventSemantics(path, organizationTemplate string, event Event, createdAccounts map[string]string, problems *validationProblems) {
	validateScenarioAccountReference(path+".payer_account", organizationTemplate, event.PayerAccountID, event.PayerAccount, createdAccounts, problems)
}

func validatePaymentMethodScenarioEventSemantics(path, organizationTemplate string, event Event, createdAccounts map[string]string, problems *validationProblems) {
	validateScenarioAccountReference(path+".payer_account", organizationTemplate, event.PayerAccountID, event.PayerAccount, createdAccounts, problems)
}

func validateCreateSavedReportScenarioEventSemantics(path, organizationTemplate string, event Event, createdAccounts map[string]string, problems *validationProblems) {
	validateScenarioAccountReference(path+".owner_account", organizationTemplate, event.OwnerAccountID, event.OwnerAccount, createdAccounts, problems)
}

func validateCreateSavingsPlanScenarioEventSemantics(path, organizationTemplate string, event Event, createdAccounts map[string]string, problems *validationProblems) {
	validateScenarioAccountReference(path+".payer_account", organizationTemplate, event.PayerAccountID, event.PayerAccount, createdAccounts, problems)
	validateScenarioAccountReference(path+".owner_account", organizationTemplate, event.OwnerAccountID, event.OwnerAccount, createdAccounts, problems)
}

func preflightNoopScenarioEvent(context.Context, Runner, *scenarioExecutionState, *scenarioEventPreflight, Event, time.Time) (scenarioClosedPeriodConflict, bool, error) {
	return scenarioClosedPeriodConflict{}, false, nil
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

func preflightRunDailyMeteringScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, preflight *scenarioEventPreflight, event Event, scheduledAt time.Time) (scenarioClosedPeriodConflict, bool, error) {
	payerID := state.resolveAccountID(event.PayerAccountID, event.PayerAccount)
	return r.closedPeriodConflictForPlannedUsage(ctx, preflight.definition, preflight.plannedUsage, event.ID, payerID, scheduledAt, "", "")
}

func preflightCloseBillingPeriodScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, preflight *scenarioEventPreflight, event Event, scheduledAt time.Time) (scenarioClosedPeriodConflict, bool, error) {
	period, err := scenarioPreflightClosePeriod(event, scheduledAt)
	if err != nil {
		return scenarioClosedPeriodConflict{}, false, err
	}
	periodEnd, err := scenarioPreflightPeriodEndTime(period)
	if err != nil {
		return scenarioClosedPeriodConflict{}, false, err
	}
	payerID := state.resolveAccountID(event.PayerAccountID, event.PayerAccount)
	return r.closedPeriodConflictForPlannedUsage(ctx, preflight.definition, preflight.plannedUsage, event.ID, payerID, periodEnd, period.Start, period.End)
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

func applyAdvanceClockScenarioEvent(ctx context.Context, r Runner, _ *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.clock.Advance(ctx, persistence.SimulatorClockAdvanceRequest{
		Amount: event.Amount,
		Unit:   persistence.SimulatorClockAdvanceUnit(event.Unit),
	}); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyRunDailyMeteringScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	metering, err := r.daily.Run(ctx, persistence.DailyMeteringJobRequest{
		Trigger:        persistence.DailyMeteringJobTriggerOnDemand,
		PayerAccountID: state.resolveAccountID(event.PayerAccountID, event.PayerAccount),
	})
	if err != nil {
		err = r.describeClosedPeriodPricingFailure(ctx, state, event, scheduledAt, err)
		return failScenarioRunEvent(audit, err)
	}
	audit.MeteringRecordsCreated = metering.MeteringRecordsCreated
	audit.BillLineItemsCreated = metering.BillLineItemsCreated
	return audit, nil
}

func applyCloseBillingPeriodScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	closed, err := r.monthEnd.ClosePreviousPeriod(ctx, persistence.MonthEndCloseRequest{
		PayerAccountID: state.resolveAccountID(event.PayerAccountID, event.PayerAccount),
		PeriodStart:    event.BillingPeriodStart,
		PeriodEnd:      event.BillingPeriodEnd,
	})
	if err != nil {
		err = r.describeClosedPeriodPricingFailure(ctx, state, event, scheduledAt, err)
		return failScenarioRunEvent(audit, err)
	}
	audit.MeteringRecordsCreated = closed.MeteringRecordsCreated
	audit.BillLineItemsCreated = closed.BillLineItemsCreated
	audit.BillID = closed.Bill.ID
	audit.BillsIssued = boolToInt(closed.Bill.ID != "")
	state.lastInvoiceObligationID = closed.InvoiceObligation.ID
	return audit, nil
}

func applyRefreshCostAllocationTagsScenarioEvent(ctx context.Context, r Runner, _ *scenarioExecutionState, _ Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.refreshCostAllocationTags(ctx, scheduledAt); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyActivateCostAllocationTagScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.activateCostAllocationTag(ctx, state, event, scheduledAt); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyCreateCostCategoryScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createCostCategory(ctx, state, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyCreateCostCategoryRuleScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createCostCategoryRule(ctx, state, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyCreateCostCategorySplitRuleScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createCostCategorySplitRule(ctx, state, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyCreatePaymentMethodScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createPaymentMethod(ctx, state, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyPaymentLifecycleScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.applyPaymentLifecycleEvent(ctx, state, event, scheduledAt); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyCreateBudgetScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createBudget(ctx, state, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyRefreshBudgetForecastsScenarioEvent(ctx context.Context, r Runner, _ *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if err := r.refreshBudgetForecasts(ctx, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyCreateSavingsPlanScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createSavingsPlan(ctx, state, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyCreateSavedReportScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createSavedReport(ctx, state, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}
