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

func TestBudgetRepositoryRefreshesMonthEndForecastSummaries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	actualItem := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-budget-forecast-ec2",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Budget forecast storefront",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app": "storefront",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-budget-forecast-actual",
			ResourceID:          "resource-budget-forecast-ec2",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-02T00:00:00Z",
			UsageQuantityMicros: 24_000_000,
			UsageUnit:           "Hours",
		},
	)
	if actualItem.UnblendedCostMicros != 998_400 {
		t.Fatalf("actual item cost = %d, want 998400", actualItem.UnblendedCostMicros)
	}

	if _, err := NewResourceUsageRepository(db).RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                    "usage-budget-forecast-scheduled",
		ResourceID:            "resource-budget-forecast-ec2",
		UsageType:             "instance-hours:t3.medium",
		Operation:             "RunInstances",
		UsageStartTime:        "2026-02-20T00:00:00Z",
		UsageEndTime:          "2026-02-20T02:00:00Z",
		UsageQuantityMicros:   2_000_000,
		UsageUnit:             "Hours",
		EventSource:           "scenario",
		ScenarioRunID:         "scenario-budget-forecast",
		ScenarioEventID:       "future-scale-up",
		ScenarioEventSequence: 2,
	}); err != nil {
		t.Fatalf("RecordUsageEvent(future scenario) error = %v", err)
	}

	categoryRepo := NewCostCategoryRepository(db)
	category, err := categoryRepo.CreateCategory(ctx, CostCategoryCreateRequest{
		ID:           "cc_budget_forecast_product",
		Name:         "Budget Forecast Product",
		DefaultValue: "Unallocated",
	})
	if err != nil {
		t.Fatalf("CreateCategory() error = %v", err)
	}
	if _, err := categoryRepo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		ID:             "ccr_budget_forecast_storefront",
		CostCategoryID: category.ID,
		RuleOrder:      1,
		Value:          "Storefront",
		Conditions: []CostCategoryRuleCondition{
			{
				ID:        "ccrc_budget_forecast_app",
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
		ID:                 "budget-forecast-account",
		Name:               "Forecast account",
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		BudgetAmountMicros: 2_850_000,
		ScopeType:          BudgetScopeAccount,
		ScopeValue:         "111122223333",
		Thresholds: []BudgetThresholdCreateRequest{
			{ThresholdType: BudgetThresholdTypeActual, ThresholdBasisPoints: 10000},
			{ThresholdType: BudgetThresholdTypeForecast, ThresholdBasisPoints: 10000},
		},
	})
	categoryBudget := createBudgetForTest(t, ctx, budgetRepo, BudgetCreateRequest{
		ID:                 "budget-forecast-category",
		Name:               "Forecast category",
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		BudgetAmountMicros: 2_850_000,
		ScopeType:          BudgetScopeCostCategory,
		ScopeKey:           category.Name,
		ScopeValue:         "Storefront",
		Thresholds: []BudgetThresholdCreateRequest{
			{ThresholdType: BudgetThresholdTypeForecast, ThresholdBasisPoints: 10000},
		},
	})

	result, err := budgetRepo.RefreshForecastSummaries(ctx, BudgetForecastRefreshRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		CurrentTime:        "2026-02-11T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("RefreshForecastSummaries() error = %v", err)
	}
	if result.BillingPeriodStart != "2026-02-01" || result.BillingPeriodEnd != "2026-03-01" || result.CurrentTime != "2026-02-11T00:00:00Z" {
		t.Fatalf("forecast result period = %+v, want February at current time", result)
	}
	summaries := mapBudgetForecastSummariesByID(result.Summaries)
	for _, budget := range []Budget{accountBudget, categoryBudget} {
		summary := summaries[budget.ID]
		if summary.ActualCostMicros != 998_400 ||
			summary.ElapsedDays != 10 ||
			summary.PeriodDays != 28 ||
			summary.RunRateForecastMicros != 2_795_520 ||
			summary.ScheduledEventCostMicros != 83_200 ||
			summary.ForecastCostMicros != 2_878_720 ||
			summary.LineItemCount != 1 ||
			summary.ScheduledUsageEventCount != 1 {
			t.Fatalf("forecast summary for %s = %+v, want actual, run-rate, scheduled event, and final forecast", budget.ID, summary)
		}
	}

	evaluations, err := budgetRepo.EvaluateBudgets(ctx, BudgetEvaluationRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("EvaluateBudgets() error = %v", err)
	}
	byID := mapBudgetEvaluationsByID(evaluations)
	account := byID[accountBudget.ID]
	if account.ActualCostMicros != 998_400 || account.ForecastCostMicros != 2_878_720 {
		t.Fatalf("account evaluation = %+v, want persisted forecast", account)
	}
	if account.ThresholdChecks[0].Breached {
		t.Fatalf("account actual threshold = %+v, want actual under budget", account.ThresholdChecks[0])
	}
	if !account.ThresholdChecks[1].Breached {
		t.Fatalf("account forecast threshold = %+v, want persisted forecast breach", account.ThresholdChecks[1])
	}
	categoryEval := byID[categoryBudget.ID]
	if categoryEval.ForecastCostMicros != 2_878_720 || !categoryEval.ThresholdChecks[0].Breached {
		t.Fatalf("category evaluation = %+v, want future scenario cost matched by Cost Category rule", categoryEval)
	}
}

func TestBudgetRepositoryRecordsBudgetAlertNotifications(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-budget-alert-ec2",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Budget alert storefront web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app": "storefront",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-budget-alert-ec2",
			ResourceID:          "resource-budget-alert-ec2",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)

	budgetRepo := NewBudgetRepository(db)
	budget := createBudgetForTest(t, ctx, budgetRepo, BudgetCreateRequest{
		ID:                 "budget-alert-account",
		Name:               "Alert account",
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		BudgetAmountMicros: 100_000,
		ScopeType:          BudgetScopeAccount,
		ScopeValue:         "111122223333",
		Thresholds: []BudgetThresholdCreateRequest{
			{ID: "budget-alert-actual", ThresholdType: BudgetThresholdTypeActual, ThresholdBasisPoints: 8000},
			{ID: "budget-alert-forecast", ThresholdType: BudgetThresholdTypeForecast, ThresholdBasisPoints: 12000},
		},
	})

	evaluations, err := budgetRepo.EvaluateBudgets(ctx, BudgetEvaluationRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		ForecastCostMicrosByBudgetID: map[string]int64{
			budget.ID: 130_000,
		},
	})
	if err != nil {
		t.Fatalf("EvaluateBudgets() error = %v", err)
	}
	notifications, err := budgetRepo.RecordAlertNotifications(ctx, evaluations)
	if err != nil {
		t.Fatalf("RecordAlertNotifications() error = %v", err)
	}
	if len(notifications) != 2 {
		t.Fatalf("RecordAlertNotifications() rows = %+v, want actual and forecast alerts", notifications)
	}
	byType := mapBudgetAlertNotificationsByType(notifications)
	actual := byType[BudgetThresholdTypeActual]
	if actual.BudgetID != budget.ID ||
		actual.BudgetThresholdID != "budget-alert-actual" ||
		actual.NotificationChannel != "in_app" ||
		actual.SpendMicros != 83_200 ||
		actual.ThresholdAmountMicros != 80_000 ||
		actual.PercentUsedBasisPoints != 8320 ||
		actual.LineItemCount != 1 ||
		!strings.Contains(actual.Message, "actual threshold crossed") ||
		actual.FirstTriggeredAt == "" ||
		actual.LastObservedAt == "" {
		t.Fatalf("actual alert notification = %+v, want in-app actual threshold crossing", actual)
	}
	forecast := byType[BudgetThresholdTypeForecast]
	if forecast.BudgetThresholdID != "budget-alert-forecast" ||
		forecast.SpendMicros != 130_000 ||
		forecast.ThresholdAmountMicros != 120_000 ||
		!strings.Contains(forecast.Message, "forecast threshold crossed") {
		t.Fatalf("forecast alert notification = %+v, want forecast threshold crossing", forecast)
	}

	rechecked, err := budgetRepo.EvaluateBudgets(ctx, BudgetEvaluationRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		ForecastCostMicrosByBudgetID: map[string]int64{
			budget.ID: 140_000,
		},
	})
	if err != nil {
		t.Fatalf("EvaluateBudgets(rechecked) error = %v", err)
	}
	updated, err := budgetRepo.RecordAlertNotifications(ctx, rechecked)
	if err != nil {
		t.Fatalf("RecordAlertNotifications(rechecked) error = %v", err)
	}
	if len(updated) != 2 {
		t.Fatalf("RecordAlertNotifications(rechecked) rows = %+v, want no duplicate alerts", updated)
	}
	updatedByType := mapBudgetAlertNotificationsByType(updated)
	if updatedByType[BudgetThresholdTypeForecast].ID != forecast.ID ||
		updatedByType[BudgetThresholdTypeForecast].SpendMicros != 140_000 {
		t.Fatalf("updated forecast alert = %+v, want same notification row with latest spend", updatedByType[BudgetThresholdTypeForecast])
	}
	if updatedByType[BudgetThresholdTypeActual].ID != actual.ID {
		t.Fatalf("updated actual alert ID = %q, want original %q", updatedByType[BudgetThresholdTypeActual].ID, actual.ID)
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

func mapBudgetForecastSummariesByID(summaries []BudgetForecastSummary) map[string]BudgetForecastSummary {
	byID := map[string]BudgetForecastSummary{}
	for _, summary := range summaries {
		byID[summary.BudgetID] = summary
	}
	return byID
}

func mapBudgetAlertNotificationsByType(notifications []BudgetAlertNotification) map[string]BudgetAlertNotification {
	byType := map[string]BudgetAlertNotification{}
	for _, notification := range notifications {
		byType[notification.ThresholdType] = notification
	}
	return byType
}
