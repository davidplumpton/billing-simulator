package app

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aws-billing-simulator/internal/persistence"
	"aws-billing-simulator/internal/scenario"
)

func TestScenarioFeedbackDataSourceLabelsReferenceSchemaObjects(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := persistence.OpenWorkspace(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	defer db.Close()

	schemaObjects := scenarioFeedbackSchemaObjectNames(t, ctx, db)
	for _, actionType := range supportedScenarioFeedbackActionTypes() {
		assertScenarioFeedbackDataSourceExists(t, schemaObjects, "action "+string(actionType), scenarioActionDataSource(string(actionType)))
	}
	assertScenarioFeedbackDataSourceExists(t, schemaObjects, "action fallback", scenarioActionDataSource("unsupported_action"))

	checkTypes := []scenario.CheckType{
		scenario.CheckTypeSavedReportExists,
		scenario.CheckTypeIdentifiesTopDriver,
		scenario.CheckTypeCostAllocationTagActivated,
		scenario.CheckTypeCostCategoryRuleCreated,
		scenario.CheckTypeBillReconciled,
		scenario.CheckTypePaymentStatus,
	}
	for _, checkType := range checkTypes {
		assertScenarioFeedbackDataSourceExists(t, schemaObjects, "check "+string(checkType), scenarioCheckDataSource(string(checkType)))
	}
	assertScenarioFeedbackDataSourceExists(t, schemaObjects, "check fallback", scenarioCheckDataSource("unsupported_check"))
}

func TestScenarioFeedbackSupportedActionsHaveSpecificLearnerCopy(t *testing.T) {
	t.Parallel()

	genericWhatChanged := scenarioActionWhatChanged("unsupported_action")
	genericBillingConcept := scenarioActionBillingConcept("unsupported_action")
	for _, actionType := range supportedScenarioFeedbackActionTypes() {
		action := string(actionType)
		if got := scenarioActionWhatChanged(action); got == "" || got == genericWhatChanged {
			t.Errorf("scenarioActionWhatChanged(%q) = %q, want supported-action copy", action, got)
		}
		if got := scenarioActionBillingConcept(action); got == "" || got == genericBillingConcept {
			t.Errorf("scenarioActionBillingConcept(%q) = %q, want supported-action concept copy", action, got)
		}
	}
}

func TestScenarioFeedbackPackagedRunsUseSchemaBackedDataSources(t *testing.T) {
	cases := []struct {
		name        string
		key         string
		definition  string
		wantSources []string
		wantText    []string
		staleNames  []string
		staleText   []string
	}{
		{
			name:       "account and usage actions",
			key:        "first-consolidated-bill",
			definition: "First consolidated bill",
			wantSources: []string{
				"accounts, organization_account_hierarchy, account_lifecycle_events",
				"resources, usage_events",
			},
			staleNames: []string{
				"organization_accounts",
				"resource_usage_events",
			},
		},
		{
			name:       "tag actions",
			key:        "missing-tags",
			definition: "Missing Tags",
			wantSources: []string{
				"cost_allocation_tag_keys, cost_allocation_tag_inventory, cost_allocation_tag_activation_events",
				"resources, usage_events",
			},
			staleNames: []string{
				"cost_allocation_tag_values",
				"resource_usage_events",
			},
		},
		{
			name:       "payment actions",
			key:        "payment-failure",
			definition: "Payment Failure",
			wantSources: []string{
				"payment_profiles, payment_methods",
				"invoice_obligations, invoice_payment_states, invoice_payment_events",
			},
			staleNames: []string{
				"payer_payment_profiles",
			},
		},
		{
			name:       "budget forecast and saved report actions",
			key:        "forecast-budget-alert",
			definition: "Forecast and Budget Alert",
			wantSources: []string{
				"budgets, budget_thresholds",
				"budget_forecast_summaries, budget_alert_notifications",
				"saved_reports",
			},
			wantText: []string{
				"Created or reused a monthly budget guardrail with actual and forecast thresholds.",
				"Recomputed budget forecast summaries and in-app alert notifications for the billing period.",
				"Created or updated a Cost Explorer saved report definition for the lab drilldown.",
				"Budgets compare actual and forecast spend against learner-defined thresholds for a billing period.",
				"Budget forecasts estimate open-period spend and alert notifications surface threshold breaches before month end.",
				"Cost Explorer saved reports preserve reusable grouping, filter, metric, and chart choices for spend analysis.",
			},
			staleText: []string{
				"Recorded a scenario action outcome.",
				"Scenario audit rows make the billing lab reproducible and inspectable.",
			},
		},
		{
			name:       "savings plan actions",
			key:        "savings-plan-coverage",
			definition: "Savings Plan coverage",
			wantSources: []string{
				"savings_plan_purchases, savings_plan_line_item_sources, bill_line_items",
				"metering_records, bill_line_items",
			},
			wantText: []string{
				"Created a simplified Compute Savings Plan commitment for estimated billing coverage.",
				"Savings Plans add commitment fees and coverage negations that reports reconcile back to source usage.",
			},
			staleText: []string{
				"Recorded a scenario action outcome.",
				"Scenario audit rows make the billing lab reproducible and inspectable.",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := startScenarioFeedbackTestServer(t, tc.key)
			db := server.workspace.DB()
			client := appTestHTTPClientWithTimeout(3 * time.Second)

			resp, err := client.PostForm(server.URL()+"/scenarios/launch", url.Values{
				"scenario_key": {tc.key},
			})
			if err != nil {
				t.Fatalf("POST /scenarios/launch error = %v", err)
			}
			body := readResponseBody(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("POST /scenarios/launch status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
			}

			runID := latestScenarioRunID(t, context.Background(), db, tc.definition)
			resp, err = client.Get(server.URL() + scenarioFeedbackPath(runID))
			if err != nil {
				t.Fatalf("GET /scenarios/feedback error = %v", err)
			}
			body = readResponseBody(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /scenarios/feedback status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
			}
			for _, want := range tc.wantSources {
				if !strings.Contains(body, want) {
					t.Fatalf("feedback body missing source %q: %s", want, body)
				}
			}
			for _, want := range tc.wantText {
				if !strings.Contains(body, want) {
					t.Fatalf("feedback body missing text %q: %s", want, body)
				}
			}
			for _, staleName := range tc.staleNames {
				if strings.Contains(body, staleName) {
					t.Fatalf("feedback body still contains stale source %q: %s", staleName, body)
				}
			}
			for _, staleText := range tc.staleText {
				if strings.Contains(body, staleText) {
					t.Fatalf("feedback body still contains stale text %q: %s", staleText, body)
				}
			}
		})
	}
}

// supportedScenarioFeedbackActionTypes mirrors the parser's executable action set for mapper regression checks.
func supportedScenarioFeedbackActionTypes() []scenario.EventAction {
	return []scenario.EventAction{
		scenario.EventActionCreateAccount,
		scenario.EventActionCreateResource,
		scenario.EventActionAddUsage,
		scenario.EventActionGenerateUsage,
		scenario.EventActionAdvanceClock,
		scenario.EventActionRunDailyMetering,
		scenario.EventActionCloseBillingPeriod,
		scenario.EventActionIssueBill,
		scenario.EventActionRefreshCostAllocationTags,
		scenario.EventActionActivateCostAllocationTag,
		scenario.EventActionCreateCostCategory,
		scenario.EventActionCreateCostCategoryRule,
		scenario.EventActionCreateCostCategorySplitRule,
		scenario.EventActionCreatePaymentMethod,
		scenario.EventActionSchedulePayment,
		scenario.EventActionProcessPayment,
		scenario.EventActionFailPayment,
		scenario.EventActionMarkPaymentDue,
		scenario.EventActionMarkPaymentPastDue,
		scenario.EventActionCollectPayment,
		scenario.EventActionCreateBudget,
		scenario.EventActionRefreshBudgetForecasts,
		scenario.EventActionCreateSavingsPlan,
		scenario.EventActionCreateSavedReport,
	}
}

func scenarioFeedbackSchemaObjectNames(t *testing.T, ctx context.Context, db *sql.DB) map[string]bool {
	t.Helper()

	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type IN ('table', 'view')`)
	if err != nil {
		t.Fatalf("read sqlite schema objects: %v", err)
	}
	defer rows.Close()

	names := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan sqlite schema object: %v", err)
		}
		names[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite schema objects: %v", err)
	}
	return names
}

func assertScenarioFeedbackDataSourceExists(t *testing.T, schemaObjects map[string]bool, label, source string) {
	t.Helper()

	for _, name := range splitScenarioFeedbackDataSource(source) {
		if !schemaObjects[name] {
			t.Errorf("%s data source %q references %q, which is not a migrated table or view", label, source, name)
		}
	}
}

func splitScenarioFeedbackDataSource(source string) []string {
	parts := strings.Split(source, ",")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "and "))
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func startScenarioFeedbackTestServer(t *testing.T, name string) *Server {
	t.Helper()

	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, name+"-workspace")
	cfg.StatePath = filepath.Join(root, "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})
	if server.workspace.DB() == nil {
		t.Fatal("Start() did not open workspace database")
	}
	return server
}

func latestScenarioRunID(t *testing.T, ctx context.Context, db *sql.DB, definitionName string) string {
	t.Helper()

	var runID string
	if err := db.QueryRowContext(ctx, `
		SELECT id
		FROM scenario_runs
		WHERE definition_name = ?
		ORDER BY started_at DESC, id DESC
		LIMIT 1
	`, definitionName).Scan(&runID); err != nil {
		t.Fatalf("read latest scenario run for %q: %v", definitionName, err)
	}
	return runID
}
