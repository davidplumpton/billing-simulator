package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestBudgetRepositoryChecksMonthlyBudgetThresholdsByScope(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-budget-ec2",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Budget storefront web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app": "storefront",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-budget-ec2",
			ResourceID:          "resource-budget-ec2",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)
	recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-budget-s3",
			AccountID:    "222233334444",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonS3,
			ResourceType: "s3_bucket",
			ResourceName: "Budget payments objects",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app": "payments",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-budget-s3",
			ResourceID:          "resource-budget-s3",
			UsageType:           "requests:put-1k",
			Operation:           "PutObject",
			UsageStartTime:      "2026-02-02T00:00:00Z",
			UsageEndTime:        "2026-02-03T00:00:00Z",
			UsageQuantityMicros: 1_500_000_000,
			UsageUnit:           "Request",
		},
	)

	categoryRepo := NewCostCategoryRepository(db)
	category, err := categoryRepo.CreateCategory(ctx, CostCategoryCreateRequest{
		ID:           "cc_budget_product",
		Name:         "Budget Product",
		DefaultValue: "Unallocated",
	})
	if err != nil {
		t.Fatalf("CreateCategory() error = %v", err)
	}
	if _, err := categoryRepo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		ID:             "ccr_budget_storefront",
		CostCategoryID: category.ID,
		RuleOrder:      1,
		Value:          "Storefront",
		MatchType:      defaultCostCategoryMatchType,
		Conditions: []CostCategoryRuleCondition{
			{
				ID:        "ccrc_budget_storefront_app",
				Dimension: CostCategoryRuleMatchTag,
				Operator:  CostCategoryRuleOperatorIn,
				TagKey:    "app",
				Values:    []string{"storefront"},
			},
		},
	}); err != nil {
		t.Fatalf("CreateRule() error = %v", err)
	}

	budgetRepo := NewBudgetRepository(db)
	accountBudget := createBudgetForTest(t, ctx, budgetRepo, BudgetCreateRequest{
		ID:                 "budget-account-storefront",
		Name:               "Storefront account",
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		BudgetAmountMicros: 100_000,
		ScopeType:          BudgetScopeAccount,
		ScopeValue:         "111122223333",
		Thresholds: []BudgetThresholdCreateRequest{
			{ThresholdType: BudgetThresholdTypeActual, ThresholdBasisPoints: 8000},
			{ThresholdType: BudgetThresholdTypeForecast, ThresholdBasisPoints: 12000},
		},
	})
	createBudgetForTest(t, ctx, budgetRepo, BudgetCreateRequest{
		ID:                 "budget-service-s3",
		Name:               "S3 service",
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		BudgetAmountMicros: 10_000,
		ScopeType:          BudgetScopeService,
		ScopeValue:         serviceAmazonS3,
		Thresholds: []BudgetThresholdCreateRequest{
			{ThresholdType: BudgetThresholdTypeActual, ThresholdBasisPoints: 10000},
		},
	})
	createBudgetForTest(t, ctx, budgetRepo, BudgetCreateRequest{
		ID:                 "budget-tag-storefront",
		Name:               "Storefront tag",
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		BudgetAmountMicros: 90_000,
		ScopeType:          BudgetScopeTag,
		ScopeKey:           "app",
		ScopeValue:         "storefront",
		Thresholds: []BudgetThresholdCreateRequest{
			{ThresholdType: BudgetThresholdTypeActual, ThresholdBasisPoints: 9000},
		},
	})
	categoryBudget := createBudgetForTest(t, ctx, budgetRepo, BudgetCreateRequest{
		ID:                 "budget-category-storefront",
		Name:               "Storefront category",
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		BudgetAmountMicros: 90_000,
		ScopeType:          BudgetScopeCostCategory,
		ScopeKey:           category.Name,
		ScopeValue:         "Storefront",
		Thresholds: []BudgetThresholdCreateRequest{
			{ThresholdType: BudgetThresholdTypeActual, ThresholdBasisPoints: 9500},
		},
	})
	if categoryBudget.ScopeKey != category.ID {
		t.Fatalf("category budget scope key = %q, want resolved category ID %q", categoryBudget.ScopeKey, category.ID)
	}

	evaluations, err := budgetRepo.EvaluateBudgets(ctx, BudgetEvaluationRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		ForecastCostMicrosByBudgetID: map[string]int64{
			accountBudget.ID: 130_000,
		},
	})
	if err != nil {
		t.Fatalf("EvaluateBudgets() error = %v", err)
	}
	if len(evaluations) != 4 {
		t.Fatalf("EvaluateBudgets() rows = %+v, want four active budgets", evaluations)
	}
	byID := mapBudgetEvaluationsByID(evaluations)

	account := byID[accountBudget.ID]
	if account.ActualCostMicros != 83_200 || account.ForecastCostMicros != 130_000 || account.LineItemCount != 1 {
		t.Fatalf("account budget evaluation = %+v, want EC2 actual plus forecast override", account)
	}
	if len(account.ThresholdChecks) != 2 {
		t.Fatalf("account threshold checks = %+v, want actual and forecast checks", account.ThresholdChecks)
	}
	if actual := account.ThresholdChecks[0]; !actual.Breached || actual.ThresholdAmountMicros != 80_000 || actual.PercentUsedBasisPoints != 8320 {
		t.Fatalf("account actual check = %+v, want breached 80%% threshold at 83.20%% used", actual)
	}
	if forecast := account.ThresholdChecks[1]; !forecast.Breached || forecast.SpendMicros != 130_000 || forecast.ThresholdAmountMicros != 120_000 {
		t.Fatalf("account forecast check = %+v, want breached forecast threshold", forecast)
	}

	service := byID["budget-service-s3"]
	if service.ActualCostMicros != 7_500 || service.ThresholdChecks[0].Breached || service.ThresholdChecks[0].RemainingCostMicros != 2_500 {
		t.Fatalf("service budget evaluation = %+v, want S3 under threshold", service)
	}

	tag := byID["budget-tag-storefront"]
	if tag.ActualCostMicros != 83_200 || !tag.ThresholdChecks[0].Breached || tag.ThresholdChecks[0].ThresholdAmountMicros != 81_000 {
		t.Fatalf("tag budget evaluation = %+v, want storefront tag actual breach", tag)
	}

	categoryEval := byID[categoryBudget.ID]
	if categoryEval.ActualCostMicros != 83_200 || categoryEval.ThresholdChecks[0].Breached || categoryEval.ThresholdChecks[0].ThresholdAmountMicros != 85_500 {
		t.Fatalf("Cost Category budget evaluation = %+v, want storefront category under 95%% threshold", categoryEval)
	}
}

func TestBudgetRepositoryValidatesDefinitionsAndEvaluationRequests(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewBudgetRepository(db)

	_, err := repo.CreateBudget(ctx, BudgetCreateRequest{
		Name:               "Partial month",
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-02-15",
		BudgetAmountMicros: 100_000,
		ScopeType:          BudgetScopeAccount,
		ScopeValue:         "111122223333",
		Thresholds: []BudgetThresholdCreateRequest{
			{ThresholdType: BudgetThresholdTypeActual, ThresholdBasisPoints: 8000},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "exactly one UTC calendar month") {
		t.Fatalf("CreateBudget(partial month) error = %v, want calendar-month validation", err)
	}

	_, err = repo.CreateBudget(ctx, BudgetCreateRequest{
		Name:               "Missing tag key",
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		BudgetAmountMicros: 100_000,
		ScopeType:          BudgetScopeTag,
		ScopeValue:         "storefront",
		Thresholds: []BudgetThresholdCreateRequest{
			{ThresholdType: BudgetThresholdTypeActual, ThresholdBasisPoints: 8000},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "scope key is required") {
		t.Fatalf("CreateBudget(missing tag key) error = %v, want scope key validation", err)
	}

	_, err = repo.CreateBudget(ctx, BudgetCreateRequest{
		Name:               "No thresholds",
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		BudgetAmountMicros: 100_000,
		ScopeType:          BudgetScopeAccount,
		ScopeValue:         "111122223333",
	})
	if err == nil || !strings.Contains(err.Error(), "at least one threshold") {
		t.Fatalf("CreateBudget(no thresholds) error = %v, want threshold validation", err)
	}

	_, err = repo.CreateBudget(ctx, BudgetCreateRequest{
		Name:               "Duplicate thresholds",
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		BudgetAmountMicros: 100_000,
		ScopeType:          BudgetScopeAccount,
		ScopeValue:         "111122223333",
		Thresholds: []BudgetThresholdCreateRequest{
			{ThresholdType: BudgetThresholdTypeActual, ThresholdBasisPoints: 8000},
			{ThresholdType: BudgetThresholdTypeActual, ThresholdBasisPoints: 8000},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("CreateBudget(duplicate thresholds) error = %v, want duplicate validation", err)
	}

	_, err = repo.EvaluateBudgets(ctx, BudgetEvaluationRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		ForecastCostMicrosByBudgetID: map[string]int64{
			"budget": -1,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "must be non-negative") {
		t.Fatalf("EvaluateBudgets(negative forecast) error = %v, want forecast validation", err)
	}
}

func createBudgetForTest(t *testing.T, ctx context.Context, repo BudgetRepository, request BudgetCreateRequest) Budget {
	t.Helper()

	budget, err := repo.CreateBudget(ctx, request)
	if err != nil {
		t.Fatalf("CreateBudget(%s) error = %v", request.Name, err)
	}
	if len(budget.Thresholds) != len(request.Thresholds) {
		t.Fatalf("CreateBudget(%s) thresholds = %+v, want %d thresholds", request.Name, budget.Thresholds, len(request.Thresholds))
	}
	return budget
}

func mapBudgetEvaluationsByID(evaluations []BudgetEvaluation) map[string]BudgetEvaluation {
	byID := map[string]BudgetEvaluation{}
	for _, evaluation := range evaluations {
		byID[evaluation.Budget.ID] = evaluation
	}
	return byID
}
