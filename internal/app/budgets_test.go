package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func TestBudgetsUIRequiresWorkspace(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/budgets")
	if err != nil {
		t.Fatalf("GET /budgets without workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /budgets without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Budgets - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/budgets">Budgets</a>`,
		"Workspace Required",
		`href="/workspaces"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /budgets without workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/budgets/create", url.Values{"name": {"Spend"}})
	if err != nil {
		t.Fatalf("POST /budgets/create without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /budgets/create without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before creating budgets.") {
		t.Fatalf("POST /budgets/create without workspace missing workspace message: %s", body)
	}

	resp, err = client.PostForm(server.URL+"/budgets/refresh", url.Values{})
	if err != nil {
		t.Fatalf("POST /budgets/refresh without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /budgets/refresh without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before refreshing budgets.") {
		t.Fatalf("POST /budgets/refresh without workspace missing workspace message: %s", body)
	}
}

func TestBudgetsPageCreatesAndEvaluatesBudget(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := persistence.OpenWorkspace(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	seedCostCategoryPreviewSpend(t, ctx, db)
	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-02-11T00:00:00Z"); err != nil {
		t.Fatalf("Set simulator clock error = %v", err)
	}
	if _, err := persistence.NewResourceUsageRepository(db).RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                    "usage-budget-ui-scheduled",
		ResourceID:            "resource-cost-category-web",
		UsageType:             "instance-hours:t3.medium",
		Operation:             "RunInstances",
		UsageStartTime:        "2026-02-20T00:00:00Z",
		UsageEndTime:          "2026-02-20T02:00:00Z",
		UsageQuantityMicros:   2_000_000,
		UsageUnit:             "Hours",
		EventSource:           "scenario",
		ScenarioRunID:         "scenario-budget-ui",
		ScenarioEventID:       "future-scale-up",
		ScenarioEventSequence: 2,
	}); err != nil {
		t.Fatalf("RecordUsageEvent(future scenario) error = %v", err)
	}

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/budgets")
	if err != nil {
		t.Fatalf("GET /budgets error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /budgets status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Budget Definition",
		"Month and Scope",
		"Thresholds",
		"Create Budget",
		"Refresh Forecasts and Alerts",
		"Alert Notifications",
		"Forecast Summaries",
		"No budget threshold checks",
		"No budget alert notifications",
		"No budget forecast summaries",
		`<a class="active" aria-current="page" href="/budgets">Budgets</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /budgets body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/budgets/create", url.Values{
		"name":                 {"Storefront Feb Budget"},
		"description":          {"Storefront account guardrail"},
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"amount":               {"0.10"},
		"scope_type":           {persistence.BudgetScopeAccount},
		"scope_value":          {"111122223333"},
		"actual_threshold":     {"80"},
		"forecast_threshold":   {"400"},
	})
	if err != nil {
		t.Fatalf("POST /budgets/create error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /budgets/create final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Created budget Storefront Feb Budget",
		"Storefront Feb Budget",
		"Account 111122223333",
		"Actual",
		"Forecast",
		"80% / $0.08",
		"$0.0832",
		"83.2%",
		"Alert Notifications",
		"No budget alert notifications",
		"No budget forecast summaries",
		"Breached",
		"OK",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /budgets/create body missing %q: %s", want, body)
		}
	}

	budgets, err := persistence.NewBudgetRepository(db).ListBudgets(ctx, persistence.BudgetListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		Status:             "active",
	})
	if err != nil {
		t.Fatalf("ListBudgets() error = %v", err)
	}
	if len(budgets) != 1 || len(budgets[0].Thresholds) != 2 {
		t.Fatalf("persisted budgets = %+v, want one budget with actual and forecast thresholds", budgets)
	}
	budgetRepo := persistence.NewBudgetRepository(db)
	forecasts, err := budgetRepo.ListForecastSummaries(ctx, persistence.BudgetForecastSummaryListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("ListForecastSummaries(after create) error = %v", err)
	}
	alerts, err := budgetRepo.ListAlertNotifications(ctx, persistence.BudgetAlertNotificationListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("ListAlertNotifications(after create) error = %v", err)
	}
	if len(forecasts) != 0 || len(alerts) != 0 {
		t.Fatalf("generated budget state after create redirect = forecasts %+v alerts %+v, want no refresh side effects", forecasts, alerts)
	}

	resp, err = client.PostForm(server.URL+"/budgets/refresh", url.Values{})
	if err != nil {
		t.Fatalf("POST /budgets/refresh error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /budgets/refresh final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Refreshed budget forecasts and alerts",
		"Storefront Feb Budget",
		"Forecast Summaries",
		"10/28",
		"$0.31616",
		"Scheduled Events",
		"Alert Notifications",
		"In-app",
		"actual threshold crossed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /budgets/refresh body missing %q: %s", want, body)
		}
	}

	alerts, err = budgetRepo.ListAlertNotifications(ctx, persistence.BudgetAlertNotificationListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("ListAlertNotifications() error = %v", err)
	}
	if len(alerts) != 1 ||
		alerts[0].BudgetID != budgets[0].ID ||
		alerts[0].ThresholdType != persistence.BudgetThresholdTypeActual ||
		alerts[0].NotificationChannel != "in_app" {
		t.Fatalf("persisted alert notifications = %+v, want one in-app actual threshold alert", alerts)
	}

	beforeGET := readBudgetGeneratedStateFingerprint(t, ctx, db, "2026-02-01", "2026-03-01")
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 2; i++ {
		resp, err = client.Get(server.URL + "/budgets")
		if err != nil {
			t.Fatalf("GET /budgets idempotency check %d error = %v", i+1, err)
		}
		body = readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /budgets idempotency check %d status = %d, want %d; body=%s", i+1, resp.StatusCode, http.StatusOK, body)
		}
	}
	afterGET := readBudgetGeneratedStateFingerprint(t, ctx, db, "2026-02-01", "2026-03-01")
	if afterGET != beforeGET {
		t.Fatalf("GET /budgets changed generated budget state:\nbefore=%s\nafter=%s", beforeGET, afterGET)
	}
}
