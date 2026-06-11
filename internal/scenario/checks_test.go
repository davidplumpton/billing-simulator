package scenario

import (
	"context"
	"strings"
	"testing"

	"aws-billing-simulator/internal/persistence"
)

func TestEvaluatorEvaluatesPackagedScenarioChecks(t *testing.T) {
	for _, seedKey := range []string{ForecastBudgetAlertSeedKey, SharedNetworkingAllocationSeedKey} {
		t.Run(seedKey, func(t *testing.T) {
			ctx := context.Background()
			db := openScenarioTestWorkspace(t)
			definition, err := LoadSeedDefinition(seedKey)
			if err != nil {
				t.Fatalf("LoadSeedDefinition(%q) error = %v", seedKey, err)
			}
			if _, err := NewRunner(db).Run(ctx, definition); err != nil {
				t.Fatalf("Run(%q) error = %v", seedKey, err)
			}

			result, err := NewEvaluator(db).Evaluate(ctx, definition)
			if err != nil {
				t.Fatalf("Evaluate(%q) error = %v", seedKey, err)
			}
			if result.ChecksTotal != len(definition.Checks) ||
				result.ChecksPassed != len(definition.Checks) ||
				result.ChecksFailed != 0 {
				t.Fatalf("Evaluate(%q) = %+v, want all packaged checks passed", seedKey, result)
			}
			for _, check := range result.Results {
				if !check.Passed || check.Status != CheckStatusPassed || check.Actual == "" || check.Message == "" {
					t.Fatalf("check evaluation = %+v, want passed evaluation with evidence", check)
				}
			}
		})
	}
}

func TestEvaluatorEvaluatesTagActivationCheck(t *testing.T) {
	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition, err := LoadSeedDefinition(MissingTagsSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", MissingTagsSeedKey, err)
	}
	definition.Checks = []Check{
		{
			ID:     "check-owner-tag-active",
			Type:   CheckTypeCostAllocationTagActivated,
			TagKey: "owner",
			Status: "active",
		},
	}
	if _, err := NewRunner(db).Run(ctx, definition); err != nil {
		t.Fatalf("Run(%q) error = %v", MissingTagsSeedKey, err)
	}

	result, err := NewEvaluator(db).Evaluate(ctx, definition)
	if err != nil {
		t.Fatalf("Evaluate(%q) error = %v", MissingTagsSeedKey, err)
	}
	requireAllScenarioChecksPassed(t, result)
}

func TestEvaluatorReportsMissingCostCategoryRuleCheckAsFailedEvidence(t *testing.T) {
	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition := parseScenarioDefinitionForTest(t, `{
		"name": "Missing Cost Category check",
		"clock": {"start": "2026-02-01"},
		"organization_template": "anycompany-retail",
		"checks": [
			{
				"id": "check-missing-category",
				"type": "cost_category_rule_created",
				"category": "Missing Category",
				"value": "Storefront"
			}
		]
	}`)

	result, err := NewEvaluator(db).Evaluate(ctx, definition)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.ChecksTotal != 1 || result.ChecksPassed != 0 || result.ChecksFailed != 1 {
		t.Fatalf("Evaluate() = %+v, want one failed missing-category check", result)
	}
	if result.Results[0].Passed || result.Results[0].Actual != "not found" ||
		!strings.Contains(result.Results[0].Message, `cost category "Missing Category" was not found`) {
		t.Fatalf("missing category check = %+v, want failed not-found evidence", result.Results[0])
	}
}

func TestEvaluatorScopesSavedReportExistsByOwnerRole(t *testing.T) {
	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	reportRepo := persistence.NewSavedReportRepository(db)
	if _, err := reportRepo.Create(ctx, persistence.SavedReportCreateRequest{
		ID:             "saved-report-check-management",
		Name:           "Payer role report",
		OwnerAccountID: persistence.AnyCompanyRetailManagementAccountID,
		OwnerRole:      "management-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
	}); err != nil {
		t.Fatalf("Create(management report) error = %v", err)
	}
	if _, err := reportRepo.Create(ctx, persistence.SavedReportCreateRequest{
		ID:             "saved-report-check-finance",
		Name:           "Payer role report",
		OwnerAccountID: persistence.AnyCompanyRetailManagementAccountID,
		OwnerRole:      "finance",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
	}); err != nil {
		t.Fatalf("Create(finance report) error = %v", err)
	}
	definition := parseScenarioDefinitionForTest(t, `{
		"name": "Role-scoped saved report check",
		"clock": {"start": "2026-02-01"},
		"organization_template": "anycompany-retail",
		"checks": [
			{
				"id": "check-finance-report",
				"type": "saved_report_exists",
				"report_name": "Payer role report",
				"account_id": "999988887777",
				"owner_role": "finance"
			},
			{
				"id": "check-instructor-report",
				"type": "saved_report_exists",
				"report_name": "Payer role report",
				"account_id": "999988887777",
				"owner_role": "instructor"
			}
		]
	}`)

	result, err := NewEvaluator(db).Evaluate(ctx, definition)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
	if result.ChecksTotal != 2 || result.ChecksPassed != 1 || result.ChecksFailed != 1 {
		t.Fatalf("Evaluate() = %+v, want one role-scoped pass and one role-scoped failure", result)
	}
	if !result.Results[0].Passed ||
		!strings.Contains(result.Results[0].Actual, "saved-report-check-finance") ||
		!strings.Contains(result.Results[0].Actual, "owner=finance/999988887777") {
		t.Fatalf("finance check = %+v, want finance-owned report evidence", result.Results[0])
	}
	if result.Results[1].Passed || result.Results[1].Actual != "not found" {
		t.Fatalf("instructor check = %+v, want not found for unmatched owner_role", result.Results[1])
	}
}

func TestEvaluatorEvaluatesBillReconciliationAndPaymentStatusChecks(t *testing.T) {
	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition, err := LoadSeedDefinition(PaymentFailureSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", PaymentFailureSeedKey, err)
	}
	definition.Checks = []Check{
		{
			ID:                 "check-payment-bill-balanced",
			Type:               CheckTypeBillReconciled,
			PayerAccount:       "Management",
			BillingPeriodStart: "2026-03-01",
			BillingPeriodEnd:   "2026-04-01",
			Status:             "balanced",
		},
		{
			ID:                 "check-payment-returned-due",
			Type:               CheckTypePaymentStatus,
			PayerAccount:       "Management",
			BillingPeriodStart: "2026-03-01",
			BillingPeriodEnd:   "2026-04-01",
			Status:             "due",
		},
	}
	if _, err := NewRunner(db).Run(ctx, definition); err != nil {
		t.Fatalf("Run(%q) error = %v", PaymentFailureSeedKey, err)
	}

	result, err := NewEvaluator(db).Evaluate(ctx, definition)
	if err != nil {
		t.Fatalf("Evaluate(%q) error = %v", PaymentFailureSeedKey, err)
	}
	requireAllScenarioChecksPassed(t, result)
}

func TestEvaluatorReportsFailedCheckEvidence(t *testing.T) {
	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition, err := LoadSeedDefinition(ForecastBudgetAlertSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", ForecastBudgetAlertSeedKey, err)
	}
	definition.Checks = []Check{
		{
			ID:              "check-wrong-driver",
			Type:            CheckTypeIdentifiesTopDriver,
			ExpectedService: "Amazon S3",
			Account:         "Storefront Prod",
			Tags:            map[string]string{"owner": "storefront-team"},
		},
	}
	if _, err := NewRunner(db).Run(ctx, definition); err != nil {
		t.Fatalf("Run(%q) error = %v", ForecastBudgetAlertSeedKey, err)
	}

	result, err := NewEvaluator(db).Evaluate(ctx, definition)
	if err != nil {
		t.Fatalf("Evaluate(%q) error = %v", ForecastBudgetAlertSeedKey, err)
	}
	if result.ChecksTotal != 1 || result.ChecksPassed != 0 || result.ChecksFailed != 1 {
		t.Fatalf("Evaluate(%q) = %+v, want one failed check", ForecastBudgetAlertSeedKey, result)
	}
	if result.Results[0].Passed ||
		result.Results[0].Status != CheckStatusFailed ||
		result.Results[0].Expected != "Amazon S3" ||
		result.Results[0].Actual == "" ||
		result.Results[0].Message == "" {
		t.Fatalf("failed check evidence = %+v, want expected/actual/message", result.Results[0])
	}
}

func TestEvaluatorEvaluateRunRecordsCheckProgress(t *testing.T) {
	ctx := context.Background()
	db := openScenarioTestWorkspace(t)
	definition, err := LoadSeedDefinition(ForecastBudgetAlertSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition(%q) error = %v", ForecastBudgetAlertSeedKey, err)
	}
	run, err := NewRunner(db).Run(ctx, definition)
	if err != nil {
		t.Fatalf("Run(%q) error = %v", ForecastBudgetAlertSeedKey, err)
	}

	result, err := NewEvaluator(db).EvaluateRun(ctx, run.Run.ID, definition)
	if err != nil {
		t.Fatalf("EvaluateRun(%q) error = %v", ForecastBudgetAlertSeedKey, err)
	}
	requireAllScenarioChecksPassed(t, result)

	progressRepo := persistence.NewScenarioLearnerProgressRepository(db)
	progress, err := progressRepo.Get(ctx, run.Run.ID)
	if err != nil {
		t.Fatalf("Get(progress) error = %v", err)
	}
	if progress.CurrentObjectiveState != persistence.ScenarioProgressStateCompleted ||
		progress.ChecksPassed != len(definition.Checks) ||
		progress.ChecksFailed != 0 ||
		progress.CompletedAt == "" {
		t.Fatalf("progress after EvaluateRun = %+v, want completed passed checks", progress)
	}
	checks, err := progressRepo.ListCheckResults(ctx, run.Run.ID)
	if err != nil {
		t.Fatalf("ListCheckResults() error = %v", err)
	}
	if len(checks) != len(definition.Checks) ||
		checks[0].CheckID != definition.Checks[0].ID ||
		checks[0].Status != CheckStatusPassed ||
		checks[0].Actual == "" {
		t.Fatalf("persisted checks = %+v, want passed check evidence", checks)
	}
}

func requireAllScenarioChecksPassed(t *testing.T, result CheckEvaluationResult) {
	t.Helper()
	if result.ChecksTotal == 0 || result.ChecksPassed != result.ChecksTotal || result.ChecksFailed != 0 {
		t.Fatalf("check evaluation result = %+v, want all checks passed", result)
	}
	for _, check := range result.Results {
		if !check.Passed || check.Status != CheckStatusPassed {
			t.Fatalf("check evaluation = %+v, want passed", check)
		}
	}
}
