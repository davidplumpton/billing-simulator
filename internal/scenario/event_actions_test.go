package scenario

import (
	"testing"

	"aws-billing-simulator/internal/persistence"
)

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

func TestBudgetActionSpecNormalizesAndValidatesPayload(t *testing.T) {
	t.Parallel()

	createSpec, ok := scenarioEventActionSpecFor(EventActionCreateBudget)
	if !ok {
		t.Fatal("create_budget action spec is not registered")
	}

	createEvent := Event{
		ID:                 "budget-guardrail",
		Action:             EventActionCreateBudget,
		BudgetID:           " budget-123 ",
		BudgetName:         " Storefront forecast guardrail ",
		Description:        " forecast budget ",
		BillingPeriodStart: " 2026-02-01 ",
		BillingPeriodEnd:   " 2026-03-01 ",
		CurrencyCode:       " usd ",
		BudgetAmountMicros: 0,
		ScopeType:          " account ",
		ScopeValue:         " Storefront Prod ",
		Status:             " active ",
		Account:            "  unrelated account field stays outside the payload  ",
		Thresholds: []BudgetThreshold{
			{ID: " actual-threshold ", Type: " actual ", BasisPoints: 8000},
		},
	}
	normalizedCreate := createSpec.normalize(createEvent)
	if normalizedCreate.BudgetName != "Storefront forecast guardrail" ||
		normalizedCreate.Description != "forecast budget" ||
		normalizedCreate.BillingPeriodStart != "2026-02-01" ||
		normalizedCreate.BillingPeriodEnd != "2026-03-01" ||
		normalizedCreate.CurrencyCode != "USD" ||
		normalizedCreate.ScopeType != persistence.BudgetScopeAccount ||
		normalizedCreate.ScopeValue != "Storefront Prod" ||
		normalizedCreate.Thresholds[0].ID != "actual-threshold" ||
		normalizedCreate.Thresholds[0].Type != persistence.BudgetThresholdTypeActual {
		t.Fatalf("normalized create budget event = %+v", normalizedCreate)
	}
	if normalizedCreate.Account != createEvent.Account {
		t.Fatalf("unrelated account field = %q, want %q", normalizedCreate.Account, createEvent.Account)
	}

	var createProblems validationProblems
	createSpec.validate("events[0]", normalizedCreate, &createProblems)
	if len(createProblems) != 1 || createProblems[0] != "events[0].budget_amount_micros must be greater than zero for create_budget" {
		t.Fatalf("create budget validation problems = %+v", createProblems)
	}

	refreshSpec, ok := scenarioEventActionSpecFor(EventActionRefreshBudgetForecasts)
	if !ok {
		t.Fatal("refresh_budget_forecasts action spec is not registered")
	}
	refreshEvent := Event{
		ID:                 "refresh-budget",
		Action:             EventActionRefreshBudgetForecasts,
		BillingPeriodStart: " 2026-02-01 ",
		BudgetName:         "  unrelated budget name remains untouched  ",
	}
	normalizedRefresh := refreshSpec.normalize(refreshEvent)
	if normalizedRefresh.BillingPeriodStart != "2026-02-01" {
		t.Fatalf("refresh billing_period_start = %q, want trimmed payload field", normalizedRefresh.BillingPeriodStart)
	}
	if normalizedRefresh.BudgetName != refreshEvent.BudgetName {
		t.Fatalf("refresh budget_name = %q, want %q", normalizedRefresh.BudgetName, refreshEvent.BudgetName)
	}

	var refreshProblems validationProblems
	refreshSpec.validate("events[1]", normalizedRefresh, &refreshProblems)
	if len(refreshProblems) != 1 || refreshProblems[0] != "events[1].billing_period_end is required when billing_period_start is set" {
		t.Fatalf("refresh budget validation problems = %+v", refreshProblems)
	}
}
