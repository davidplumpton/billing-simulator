package scenario

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type scenarioEventActionSpec struct {
	action            EventAction
	normalize         func(Event) Event
	validate          func(string, Event, *validationProblems)
	validateSemantics func(string, string, Event, map[string]string, *validationProblems)
	preflight         func(context.Context, Runner, *scenarioExecutionState, *scenarioEventPreflight, Event, time.Time) (scenarioClosedPeriodConflict, bool, error)
	apply             func(context.Context, Runner, *scenarioExecutionState, Event, time.Time, ScenarioRunEvent) (ScenarioRunEvent, error)
}

type scenarioEventPayloadActionSpec[T any] struct {
	action            EventAction
	payloadFromEvent  func(Event) T
	mergePayload      func(Event, T) Event
	normalize         func(T) T
	validate          func(string, T, *validationProblems)
	validateSemantics func(string, string, T, map[string]string, *validationProblems)
	preflight         func(context.Context, Runner, *scenarioExecutionState, *scenarioEventPreflight, T, time.Time) (scenarioClosedPeriodConflict, bool, error)
	apply             func(context.Context, Runner, *scenarioExecutionState, T, time.Time, ScenarioRunEvent) (ScenarioRunEvent, error)
}

// asEventActionSpec adapts an action-local payload contract to the parser's public Event surface.
func (payloadSpec scenarioEventPayloadActionSpec[T]) asEventActionSpec() scenarioEventActionSpec {
	if payloadSpec.payloadFromEvent == nil {
		panic("scenario event payload spec requires payloadFromEvent")
	}
	spec := scenarioEventActionSpec{
		action: payloadSpec.action,
	}
	if payloadSpec.normalize != nil || payloadSpec.mergePayload != nil {
		spec.normalize = func(event Event) Event {
			payload := payloadSpec.payloadFromEvent(event)
			if payloadSpec.normalize != nil {
				payload = payloadSpec.normalize(payload)
			}
			if payloadSpec.mergePayload == nil {
				return event
			}
			return payloadSpec.mergePayload(event, payload)
		}
	}
	if payloadSpec.validate != nil {
		spec.validate = func(path string, event Event, problems *validationProblems) {
			payloadSpec.validate(path, payloadSpec.payloadFromEvent(event), problems)
		}
	}
	if payloadSpec.validateSemantics != nil {
		spec.validateSemantics = func(path, organizationTemplate string, event Event, createdAccounts map[string]string, problems *validationProblems) {
			payloadSpec.validateSemantics(path, organizationTemplate, payloadSpec.payloadFromEvent(event), createdAccounts, problems)
		}
	}
	if payloadSpec.preflight != nil {
		spec.preflight = func(ctx context.Context, r Runner, state *scenarioExecutionState, preflight *scenarioEventPreflight, event Event, scheduledAt time.Time) (scenarioClosedPeriodConflict, bool, error) {
			return payloadSpec.preflight(ctx, r, state, preflight, payloadSpec.payloadFromEvent(event), scheduledAt)
		}
	}
	if payloadSpec.apply != nil {
		spec.apply = func(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
			return payloadSpec.apply(ctx, r, state, payloadSpec.payloadFromEvent(event), scheduledAt, audit)
		}
	}
	return spec
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
		newPaymentLifecycleScenarioEventActionSpec(EventActionSchedulePayment),
		newPaymentLifecycleScenarioEventActionSpec(EventActionProcessPayment),
		newPaymentLifecycleScenarioEventActionSpec(EventActionFailPayment),
		newPaymentLifecycleScenarioEventActionSpec(EventActionMarkPaymentDue),
		newPaymentLifecycleScenarioEventActionSpec(EventActionMarkPaymentPastDue),
		newPaymentLifecycleScenarioEventActionSpec(EventActionCollectPayment),
		newBudgetScenarioEventActionSpec(EventActionCreateBudget),
		newBudgetScenarioEventActionSpec(EventActionRefreshBudgetForecasts),
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

func normalizePayerScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.PayerAccount, &event.PayerAccountID)
	return event
}

func validateNoopScenarioEvent(string, Event, *validationProblems) {}

func validateNoopScenarioEventSemantics(string, string, Event, map[string]string, *validationProblems) {
}

func preflightNoopScenarioEvent(context.Context, Runner, *scenarioExecutionState, *scenarioEventPreflight, Event, time.Time) (scenarioClosedPeriodConflict, bool, error) {
	return scenarioClosedPeriodConflict{}, false, nil
}
