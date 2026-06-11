package scenario

import "testing"

func TestScenarioEventActionRegistryCoversSupportedActions(t *testing.T) {
	t.Parallel()

	actions := []EventAction{
		EventActionCreateAccount,
		EventActionCreateResource,
		EventActionAddUsage,
		EventActionGenerateUsage,
		EventActionAdvanceClock,
		EventActionRunDailyMetering,
		EventActionCloseBillingPeriod,
		EventActionIssueBill,
		EventActionRefreshCostAllocationTags,
		EventActionActivateCostAllocationTag,
		EventActionCreateCostCategory,
		EventActionCreateCostCategoryRule,
		EventActionCreateCostCategorySplitRule,
		EventActionCreatePaymentMethod,
		EventActionSchedulePayment,
		EventActionProcessPayment,
		EventActionFailPayment,
		EventActionMarkPaymentDue,
		EventActionMarkPaymentPastDue,
		EventActionCollectPayment,
		EventActionCreateBudget,
		EventActionRefreshBudgetForecasts,
		EventActionCreateSavedReport,
	}

	if len(scenarioEventActionSpecsByAction) != len(actions) {
		t.Fatalf("registered action specs = %d, want %d", len(scenarioEventActionSpecsByAction), len(actions))
	}
	for _, action := range actions {
		spec, ok := scenarioEventActionSpecFor(action)
		if !ok {
			t.Fatalf("scenario action %q is not registered", action)
		}
		if spec.normalize == nil || spec.validate == nil || spec.validateSemantics == nil || spec.preflight == nil || spec.apply == nil {
			t.Fatalf("scenario action %q has incomplete behavior: %+v", action, spec)
		}
	}
}
