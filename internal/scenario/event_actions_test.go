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
		EventActionCreateSavingsPlan,
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

func TestPaymentLifecycleActionSpecNormalizesAndValidatesPayload(t *testing.T) {
	t.Parallel()

	spec, ok := scenarioEventActionSpecFor(EventActionCollectPayment)
	if !ok {
		t.Fatal("collect_payment action spec is not registered")
	}

	event := Event{
		ID:                  "collect-retry",
		Action:              EventActionCollectPayment,
		InvoiceObligationID: " obligation-123 ",
		Reason:              " retry after payment method fix ",
		Account:             "  unrelated account field stays outside the payload  ",
		AmountMicros:        0,
	}
	normalized := spec.normalize(event)
	if normalized.InvoiceObligationID != "obligation-123" {
		t.Fatalf("invoice_obligation_id = %q, want trimmed payload field", normalized.InvoiceObligationID)
	}
	if normalized.Reason != "retry after payment method fix" {
		t.Fatalf("reason = %q, want trimmed payload field", normalized.Reason)
	}
	if normalized.Account != event.Account {
		t.Fatalf("unrelated account field = %q, want %q", normalized.Account, event.Account)
	}

	var problems validationProblems
	spec.validate("events[0]", normalized, &problems)
	if len(problems) != 1 || problems[0] != "events[0].amount_micros must be greater than zero for collect_payment" {
		t.Fatalf("payment lifecycle validation problems = %+v", problems)
	}
}
