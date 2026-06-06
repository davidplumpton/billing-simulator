package persistence

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestCostCategorySplitChargeRepositoryAllocatesEvenFixedAndProportional(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	seed := seedCostCategorySplitChargeSpend(t, ctx, db)
	categoryRepo := NewCostCategoryRepository(db)
	splitRepo := NewCostCategorySplitChargeRepository(db)

	evenCategory := createSplitChargeCostCategory(t, ctx, categoryRepo, "Product Even")
	evenRule, err := splitRepo.CreateRule(ctx, CostCategorySplitChargeRuleCreateRequest{
		CostCategoryID: evenCategory.ID,
		SourceValue:    "Shared Platform",
		Method:         CostCategorySplitMethodEven,
		Targets: []CostCategorySplitChargeTargetCreateRequest{
			{TargetValue: "Storefront"},
			{TargetValue: "Payments"},
			{TargetValue: "Analytics"},
		},
	})
	if err != nil {
		t.Fatalf("CreateRule(even) error = %v", err)
	}
	requireSplitAllocation(t, listSplitAllocations(t, ctx, splitRepo, evenRule.ID, seed.Support.ID), "Storefront", 333_334, 83_200, 0)
	requireSplitAllocation(t, listSplitAllocations(t, ctx, splitRepo, evenRule.ID, seed.Support.ID), "Payments", 333_333, 7_500, 0)
	requireSplitAllocation(t, listSplitAllocations(t, ctx, splitRepo, evenRule.ID, seed.Support.ID), "Analytics", 333_333, 0, 0)

	fixedCategory := createSplitChargeCostCategory(t, ctx, categoryRepo, "Product Fixed")
	fixedRule, err := splitRepo.CreateRule(ctx, CostCategorySplitChargeRuleCreateRequest{
		CostCategoryID: fixedCategory.ID,
		SourceValue:    "Shared Platform",
		Method:         CostCategorySplitMethodFixed,
		Targets: []CostCategorySplitChargeTargetCreateRequest{
			{TargetValue: "Storefront", FixedShareMicros: 500_000},
			{TargetValue: "Payments", FixedShareMicros: 300_000},
			{TargetValue: "Analytics", FixedShareMicros: 200_000},
		},
	})
	if err != nil {
		t.Fatalf("CreateRule(fixed) error = %v", err)
	}
	requireSplitAllocation(t, listSplitAllocations(t, ctx, splitRepo, fixedRule.ID, seed.Support.ID), "Storefront", 500_000, 83_200, 500_000)
	requireSplitAllocation(t, listSplitAllocations(t, ctx, splitRepo, fixedRule.ID, seed.Support.ID), "Payments", 300_000, 7_500, 300_000)
	requireSplitAllocation(t, listSplitAllocations(t, ctx, splitRepo, fixedRule.ID, seed.Support.ID), "Analytics", 200_000, 0, 200_000)

	proportionalCategory := createSplitChargeCostCategory(t, ctx, categoryRepo, "Product Proportional")
	proportionalRule, err := splitRepo.CreateRule(ctx, CostCategorySplitChargeRuleCreateRequest{
		CostCategoryID: proportionalCategory.ID,
		SourceValue:    "Shared Platform",
		Method:         CostCategorySplitMethodProportional,
		Targets: []CostCategorySplitChargeTargetCreateRequest{
			{TargetValue: "Storefront"},
			{TargetValue: "Payments"},
			{TargetValue: "Analytics"},
		},
	})
	if err != nil {
		t.Fatalf("CreateRule(proportional) error = %v", err)
	}
	requireSplitAllocation(t, listSplitAllocations(t, ctx, splitRepo, proportionalRule.ID, seed.Support.ID), "Storefront", 917_310, 83_200, 0)
	requireSplitAllocation(t, listSplitAllocations(t, ctx, splitRepo, proportionalRule.ID, seed.Support.ID), "Payments", 82_690, 7_500, 0)
	requireSplitAllocation(t, listSplitAllocations(t, ctx, splitRepo, proportionalRule.ID, seed.Support.ID), "Analytics", 0, 0, 0)

	allAllocations, err := splitRepo.ListAllocations(ctx, CostCategorySplitChargeAllocationListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		Limit:              50,
	})
	if err != nil {
		t.Fatalf("ListAllocations(period) error = %v", err)
	}
	requireSplitAllocationsReconcileBySource(t, allAllocations)

	refresh, err := splitRepo.RefreshAllocationsForBillingPeriod(ctx, "2026-02-01", "2026-03-01")
	if err != nil {
		t.Fatalf("RefreshAllocationsForBillingPeriod() error = %v", err)
	}
	if refresh.RulesEvaluated != 3 ||
		refresh.SourceLineItemsEvaluated != 3 ||
		refresh.AllocationsRefreshed != 9 ||
		refresh.SourceCostMicros != 3_000_000 ||
		refresh.AllocatedCostMicros != 3_000_000 ||
		refresh.UnallocatedSourceCostMicros != 0 {
		t.Fatalf("refresh result = %+v, want three fully allocated support sources", refresh)
	}
}

func TestCostCategorySplitChargeRepositoryReconcilesZeroCostSources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	insertSyntheticSplitSourceLineItem(t, ctx, db, "line-split-zero-support", 0)
	categoryRepo := NewCostCategoryRepository(db)
	splitRepo := NewCostCategorySplitChargeRepository(db)
	category := createSplitChargeSourceOnlyCategory(t, ctx, categoryRepo, "Zero Cost Product")
	rule, err := splitRepo.CreateRule(ctx, CostCategorySplitChargeRuleCreateRequest{
		CostCategoryID: category.ID,
		SourceValue:    "Shared Platform",
		Method:         CostCategorySplitMethodEven,
		Targets: []CostCategorySplitChargeTargetCreateRequest{
			{TargetValue: "Storefront"},
			{TargetValue: "Payments"},
		},
	})
	if err != nil {
		t.Fatalf("CreateRule(zero even) error = %v", err)
	}

	allocations := listSplitAllocations(t, ctx, splitRepo, rule.ID, "line-split-zero-support")
	if len(allocations) != 2 {
		t.Fatalf("zero-cost allocations = %+v, want one audit row per target", allocations)
	}
	requireSplitAllocationForSource(t, allocations, "Storefront", 0, 0, 0, 0)
	requireSplitAllocationForSource(t, allocations, "Payments", 0, 0, 0, 0)
	requireSplitAllocationsReconcileBySource(t, allocations)

	comparison, err := splitRepo.CompareAllocations(ctx, CostCategorySplitChargeComparisonRequest{
		CostCategoryID:     category.ID,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("CompareAllocations(zero) error = %v", err)
	}
	if comparison.RawCostMicros != 0 ||
		comparison.SplitInCostMicros != 0 ||
		comparison.SplitOutCostMicros != 0 ||
		comparison.TotalAllocatedCostMicros != 0 ||
		comparison.UnallocatedResidualCostMicros != 0 {
		t.Fatalf("zero-cost comparison = %+v, want no allocated cost or residual", comparison)
	}
	requireSplitComparisonRow(t, comparison.Rows, "Shared Platform", 0, 0, 0, 0, 0, 1, 0)
	requireSplitComparisonRow(t, comparison.Rows, "Storefront", 0, 0, 0, 0, 0, 0, 1)
	requireSplitComparisonRow(t, comparison.Rows, "Payments", 0, 0, 0, 0, 0, 0, 1)
}

func TestCostCategorySplitChargeRepositoryReportsMissingProportionalTargetBases(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	insertSyntheticSplitSourceLineItem(t, ctx, db, "line-split-missing-target-support", supportBusinessMinimumCostMicros)
	categoryRepo := NewCostCategoryRepository(db)
	splitRepo := NewCostCategorySplitChargeRepository(db)
	category := createSplitChargeSourceOnlyCategory(t, ctx, categoryRepo, "Missing Target Product")
	rule, err := splitRepo.CreateRule(ctx, CostCategorySplitChargeRuleCreateRequest{
		CostCategoryID: category.ID,
		SourceValue:    "Shared Platform",
		Method:         CostCategorySplitMethodProportional,
		Targets: []CostCategorySplitChargeTargetCreateRequest{
			{TargetValue: "Storefront"},
			{TargetValue: "Payments"},
		},
	})
	if err != nil {
		t.Fatalf("CreateRule(missing target proportional) error = %v", err)
	}

	allocations := listSplitAllocations(t, ctx, splitRepo, rule.ID, "line-split-missing-target-support")
	if len(allocations) != 2 {
		t.Fatalf("missing-target allocations = %+v, want one zero-allocation row per target", allocations)
	}
	requireSplitAllocationForSource(t, allocations, "Storefront", supportBusinessMinimumCostMicros, 0, 0, 0)
	requireSplitAllocationForSource(t, allocations, "Payments", supportBusinessMinimumCostMicros, 0, 0, 0)
	requireSplitAllocationResidual(t, allocations, supportBusinessMinimumCostMicros, 0)

	refresh, err := splitRepo.RefreshAllocationsForBillingPeriod(ctx, "2026-02-01", "2026-03-01")
	if err != nil {
		t.Fatalf("RefreshAllocationsForBillingPeriod(missing target) error = %v", err)
	}
	if refresh.SourceLineItemsEvaluated != 1 ||
		refresh.AllocationsRefreshed != 2 ||
		refresh.SourceCostMicros != supportBusinessMinimumCostMicros ||
		refresh.AllocatedCostMicros != 0 ||
		refresh.UnallocatedSourceCostMicros != supportBusinessMinimumCostMicros {
		t.Fatalf("missing-target refresh = %+v, want full source cost reported as unallocated", refresh)
	}

	comparison, err := splitRepo.CompareAllocations(ctx, CostCategorySplitChargeComparisonRequest{
		CostCategoryID:     category.ID,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("CompareAllocations(missing target) error = %v", err)
	}
	if comparison.RawCostMicros != supportBusinessMinimumCostMicros ||
		comparison.SplitInCostMicros != 0 ||
		comparison.SplitOutCostMicros != 0 ||
		comparison.TotalAllocatedCostMicros != supportBusinessMinimumCostMicros ||
		comparison.UnallocatedResidualCostMicros != supportBusinessMinimumCostMicros {
		t.Fatalf("missing-target comparison = %+v, want source cost retained with unallocated residual", comparison)
	}
	requireSplitComparisonRow(t, comparison.Rows, "Shared Platform", supportBusinessMinimumCostMicros, 0, 0, supportBusinessMinimumCostMicros, supportBusinessMinimumCostMicros, 1, 0)
	requireSplitComparisonRow(t, comparison.Rows, "Storefront", 0, 0, 0, 0, 0, 0, 1)
	requireSplitComparisonRow(t, comparison.Rows, "Payments", 0, 0, 0, 0, 0, 0, 1)
}

func TestCostCategorySplitChargeRepositoryComparesAllocationTotals(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	seedCostCategorySplitChargeSpend(t, ctx, db)
	categoryRepo := NewCostCategoryRepository(db)
	splitRepo := NewCostCategorySplitChargeRepository(db)
	category := createSplitChargeCostCategory(t, ctx, categoryRepo, "Product")
	if _, err := splitRepo.CreateRule(ctx, CostCategorySplitChargeRuleCreateRequest{
		CostCategoryID: category.ID,
		SourceValue:    "Shared Platform",
		Method:         CostCategorySplitMethodEven,
		Targets: []CostCategorySplitChargeTargetCreateRequest{
			{TargetValue: "Storefront"},
			{TargetValue: "Payments"},
			{TargetValue: "Analytics"},
		},
	}); err != nil {
		t.Fatalf("CreateRule(even) error = %v", err)
	}

	comparison, err := splitRepo.CompareAllocations(ctx, CostCategorySplitChargeComparisonRequest{
		CostCategoryID:     category.ID,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("CompareAllocations() error = %v", err)
	}
	if comparison.CostCategoryName != "Product" ||
		comparison.RawCostMicros != 1_090_700 ||
		comparison.CategoryCostMicros != 90_700 ||
		comparison.SplitInCostMicros != supportBusinessMinimumCostMicros ||
		comparison.SplitOutCostMicros != supportBusinessMinimumCostMicros ||
		comparison.NetSplitCostMicros != 0 ||
		comparison.TotalAllocatedCostMicros != 1_090_700 ||
		comparison.UnallocatedResidualCostMicros != 0 {
		t.Fatalf("comparison totals = %+v, want direct costs plus fully allocated support", comparison)
	}

	requireSplitComparisonRow(t, comparison.Rows, "Analytics", 0, 333_333, 0, 333_333, 0, 0, 1)
	requireSplitComparisonRow(t, comparison.Rows, "Payments", 7_500, 333_333, 0, 340_833, 0, 1, 1)
	requireSplitComparisonRow(t, comparison.Rows, "Shared Platform", supportBusinessMinimumCostMicros, 0, supportBusinessMinimumCostMicros, 0, 0, 1, 0)
	requireSplitComparisonRow(t, comparison.Rows, "Storefront", 83_200, 333_334, 0, 416_534, 0, 1, 1)
}

func TestCostCategorySplitChargeRepositoryPreservesClosedAllocations(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	seed := seedCostCategorySplitChargeSpend(t, ctx, db)
	categoryRepo := NewCostCategoryRepository(db)
	splitRepo := NewCostCategorySplitChargeRepository(db)
	category := createSplitChargeCostCategory(t, ctx, categoryRepo, "Product")
	rule, err := splitRepo.CreateRule(ctx, CostCategorySplitChargeRuleCreateRequest{
		CostCategoryID: category.ID,
		SourceValue:    "Shared Platform",
		Method:         CostCategorySplitMethodEven,
		Targets: []CostCategorySplitChargeTargetCreateRequest{
			{TargetValue: "Storefront"},
			{TargetValue: "Payments"},
		},
	})
	if err != nil {
		t.Fatalf("CreateRule(even) error = %v", err)
	}
	beforeClose := listSplitAllocations(t, ctx, splitRepo, rule.ID, seed.Support.ID)
	if len(beforeClose) != 2 {
		t.Fatalf("allocations before close = %+v, want two rows", beforeClose)
	}

	if _, err := NewSimulatorClockRepository(db).Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}
	if _, err := NewMonthEndCloseRepository(db).ClosePreviousPeriod(ctx, MonthEndCloseRequest{
		PayerAccountID: AnyCompanyRetailManagementAccountID,
	}); err != nil {
		t.Fatalf("ClosePreviousPeriod() error = %v", err)
	}

	if _, err := categoryRepo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: category.ID,
		RuleOrder:      5,
		Value:          "Corporate Support",
		Conditions: []CostCategoryRuleCondition{
			{Dimension: CostCategoryRuleMatchService, Values: []string{serviceAWSSupport}},
		},
	}); err != nil {
		t.Fatalf("CreateRule(closed support reassignment) error = %v", err)
	}
	refresh, err := splitRepo.RefreshAllocationsForBillingPeriod(ctx, "2026-02-01", "2026-03-01")
	if err != nil {
		t.Fatalf("RefreshAllocationsForBillingPeriod(closed) error = %v", err)
	}
	if refresh.SourceLineItemsEvaluated != 0 || refresh.AllocationsRefreshed != 0 {
		t.Fatalf("closed refresh result = %+v, want no closed-period rewrites", refresh)
	}

	afterClose := listSplitAllocations(t, ctx, splitRepo, rule.ID, seed.Support.ID)
	if len(afterClose) != len(beforeClose) {
		t.Fatalf("allocations after close = %+v, want preserved rows %+v", afterClose, beforeClose)
	}
	if got := requireSplitAllocation(t, afterClose, "Storefront", 500_000, 83_200, 0); got.CreatedAt == "" || got.UpdatedAt == "" {
		t.Fatalf("closed allocation timestamps = %+v, want populated audit fields", got)
	}
	if _, err := db.ExecContext(ctx, `UPDATE cost_category_split_charge_allocations SET allocated_cost_micros = allocated_cost_micros WHERE rule_id = ?`, rule.ID); err == nil || !strings.Contains(err.Error(), "closed billing period") {
		t.Fatalf("direct closed split allocation update error = %v, want closed-period trigger", err)
	}
}

func TestCostCategorySplitChargeRepositoryValidatesRules(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	categoryRepo := NewCostCategoryRepository(db)
	category, err := categoryRepo.CreateCategory(ctx, CostCategoryCreateRequest{Name: "Product"})
	if err != nil {
		t.Fatalf("CreateCategory() error = %v", err)
	}
	splitRepo := NewCostCategorySplitChargeRepository(db)

	if _, err := splitRepo.CreateRule(ctx, CostCategorySplitChargeRuleCreateRequest{
		CostCategoryID: category.ID,
		Method:         CostCategorySplitMethodEven,
		Targets: []CostCategorySplitChargeTargetCreateRequest{
			{TargetValue: "Storefront"},
			{TargetValue: "Payments"},
		},
	}); err == nil || !strings.Contains(err.Error(), "source value is required") {
		t.Fatalf("CreateRule(no source) error = %v, want source validation", err)
	}
	if _, err := splitRepo.CreateRule(ctx, CostCategorySplitChargeRuleCreateRequest{
		CostCategoryID: category.ID,
		SourceValue:    "Shared Platform",
		Method:         CostCategorySplitMethodFixed,
		Targets: []CostCategorySplitChargeTargetCreateRequest{
			{TargetValue: "Storefront", FixedShareMicros: 500_000},
			{TargetValue: "Payments", FixedShareMicros: 400_000},
		},
	}); err == nil || !strings.Contains(err.Error(), "fixed split shares sum") {
		t.Fatalf("CreateRule(bad fixed shares) error = %v, want share sum validation", err)
	}
	if _, err := splitRepo.CreateRule(ctx, CostCategorySplitChargeRuleCreateRequest{
		CostCategoryID: category.ID,
		SourceValue:    "Shared Platform",
		Method:         CostCategorySplitMethodEven,
		Targets: []CostCategorySplitChargeTargetCreateRequest{
			{TargetValue: "Storefront", FixedShareMicros: 500_000},
			{TargetValue: "Payments"},
		},
	}); err == nil || !strings.Contains(err.Error(), "cannot set a fixed share") {
		t.Fatalf("CreateRule(even fixed share) error = %v, want fixed-share validation", err)
	}
	if _, err := splitRepo.CreateRule(ctx, CostCategorySplitChargeRuleCreateRequest{
		CostCategoryID: category.ID,
		SourceValue:    "Shared Platform",
		Method:         CostCategorySplitMethodEven,
		Targets: []CostCategorySplitChargeTargetCreateRequest{
			{TargetValue: "Storefront"},
			{TargetValue: "Storefront"},
		},
	}); err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("CreateRule(duplicate target) error = %v, want duplicate validation", err)
	}
	if _, err := NewCostCategorySplitChargeRepository(nil).ListAllocations(ctx, CostCategorySplitChargeAllocationListRequest{}); err == nil {
		t.Fatal("ListAllocations(nil DB) error = nil, want database handle validation")
	}
}

type costCategorySplitChargeSeed struct {
	Storefront BillLineItem
	Payments   BillLineItem
	Support    BillLineItem
}

func seedCostCategorySplitChargeSpend(t *testing.T, ctx context.Context, db *sql.DB) costCategorySplitChargeSeed {
	t.Helper()

	storefront := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-split-storefront-ec2",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Split storefront web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app": "storefront",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-split-storefront-ec2",
			ResourceID:          "resource-split-storefront-ec2",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)
	payments := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-split-payments-s3",
			AccountID:    "444455556666",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonS3,
			ResourceType: "s3_bucket",
			ResourceName: "Split payments bucket",
			Status:       "active",
			StartedAt:    "2026-02-02T00:00:00Z",
		},
		UsageEventCreateRequest{
			ID:                  "usage-split-payments-s3",
			ResourceID:          "resource-split-payments-s3",
			UsageType:           "requests:put-1k",
			Operation:           "PutObject",
			UsageStartTime:      "2026-02-02T00:00:00Z",
			UsageEndTime:        "2026-02-03T00:00:00Z",
			UsageQuantityMicros: 1_500_000_000,
			UsageUnit:           "Request",
		},
	)
	support, err := NewSupportChargeRepository(db).GenerateSupportCharges(ctx, SupportChargeGenerationRequest{
		PayerAccountID: AnyCompanyRetailManagementAccountID,
		PeriodStart:    "2026-02-01",
		PeriodEnd:      "2026-03-01",
		LineItemStatus: billLineItemStatusEstimated,
	})
	if err != nil {
		t.Fatalf("GenerateSupportCharges() error = %v", err)
	}
	if support.ItemsCreated != 1 || len(support.Items) != 1 {
		t.Fatalf("GenerateSupportCharges() = %+v, want one support fee", support)
	}
	return costCategorySplitChargeSeed{
		Storefront: storefront,
		Payments:   payments,
		Support:    support.Items[0],
	}
}

func createSplitChargeCostCategory(t *testing.T, ctx context.Context, repo CostCategoryRepository, name string) CostCategory {
	t.Helper()

	category, err := repo.CreateCategory(ctx, CostCategoryCreateRequest{
		Name:         name,
		DefaultValue: "Unmapped",
	})
	if err != nil {
		t.Fatalf("CreateCategory(%s) error = %v", name, err)
	}
	for _, request := range []CostCategoryRuleCreateRequest{
		{
			CostCategoryID: category.ID,
			RuleOrder:      10,
			Value:          "Storefront",
			Conditions: []CostCategoryRuleCondition{
				{Dimension: CostCategoryRuleMatchTag, TagKey: "app", Values: []string{"storefront"}},
			},
		},
		{
			CostCategoryID: category.ID,
			RuleOrder:      20,
			Value:          "Payments",
			Conditions: []CostCategoryRuleCondition{
				{Dimension: CostCategoryRuleMatchService, Values: []string{serviceAmazonS3}},
			},
		},
		{
			CostCategoryID: category.ID,
			RuleOrder:      30,
			Value:          "Shared Platform",
			Conditions: []CostCategoryRuleCondition{
				{Dimension: CostCategoryRuleMatchService, Values: []string{serviceAWSSupport}},
			},
		},
	} {
		if _, err := repo.CreateRule(ctx, request); err != nil {
			t.Fatalf("CreateRule(%s %s) error = %v", name, request.Value, err)
		}
	}
	return category
}

// createSplitChargeSourceOnlyCategory assigns Support spend to a source value without creating target bases.
func createSplitChargeSourceOnlyCategory(t *testing.T, ctx context.Context, repo CostCategoryRepository, name string) CostCategory {
	t.Helper()

	category, err := repo.CreateCategory(ctx, CostCategoryCreateRequest{
		Name:         name,
		DefaultValue: "Unmapped",
	})
	if err != nil {
		t.Fatalf("CreateCategory(%s) error = %v", name, err)
	}
	if _, err := repo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: category.ID,
		RuleOrder:      10,
		Value:          "Shared Platform",
		Conditions: []CostCategoryRuleCondition{
			{Dimension: CostCategoryRuleMatchService, Values: []string{serviceAWSSupport}},
		},
	}); err != nil {
		t.Fatalf("CreateRule(%s Shared Platform) error = %v", name, err)
	}
	return category
}

// insertSyntheticSplitSourceLineItem creates a period-level Support line item for split-charge edge cases.
func insertSyntheticSplitSourceLineItem(t *testing.T, ctx context.Context, db *sql.DB, id string, costMicros int64) {
	t.Helper()

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO bill_line_items (
			id,
			billing_period_start,
			billing_period_end,
			billing_period_days,
			payer_account_id,
			usage_account_id,
			service_code,
			service_name,
			product_family,
			usage_type,
			operation,
			region_code,
			line_item_type,
			line_item_status,
			usage_start_time,
			usage_end_time,
			usage_quantity_micros,
			usage_unit,
			pricing_unit,
			pricing_quantity_micros,
			unblended_rate_micros,
			unblended_cost_micros,
			currency_code,
			price_catalog_sku,
			price_effective_date,
			tag_snapshot_json,
			description
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id,
		"2026-02-01",
		"2026-03-01",
		28,
		AnyCompanyRetailManagementAccountID,
		AnyCompanyRetailManagementAccountID,
		serviceAWSSupport,
		"AWS Support",
		"Support",
		supportBusinessUsageType,
		supportBusinessOperation,
		supportRegionGlobal,
		billLineItemTypeFee,
		billLineItemStatusEstimated,
		"2026-02-01T00:00:00Z",
		"2026-03-01T00:00:00Z",
		1_000_000,
		supportUsageUnitUSD,
		supportUsageUnitUSD,
		costMicros,
		100_000,
		costMicros,
		defaultBillCurrencyCode,
		"SIM-SUPPORT-BUSINESS-PCT",
		"2026-01-01",
		"{}",
		"Synthetic Support split-charge source",
	); err != nil {
		t.Fatalf("insert synthetic split source line item %s: %v", id, err)
	}
}

func listSplitAllocations(t *testing.T, ctx context.Context, repo CostCategorySplitChargeRepository, ruleID, sourceLineItemID string) []CostCategorySplitChargeAllocation {
	t.Helper()

	allocations, err := repo.ListAllocations(ctx, CostCategorySplitChargeAllocationListRequest{
		RuleID:           ruleID,
		SourceLineItemID: sourceLineItemID,
		Limit:            10,
	})
	if err != nil {
		t.Fatalf("ListAllocations() error = %v", err)
	}
	return allocations
}

func requireSplitAllocation(t *testing.T, allocations []CostCategorySplitChargeAllocation, targetValue string, allocatedCostMicros, allocationBaseCostMicros int64, fixedShareMicros int) CostCategorySplitChargeAllocation {
	t.Helper()

	return requireSplitAllocationForSource(t, allocations, targetValue, supportBusinessMinimumCostMicros, allocatedCostMicros, allocationBaseCostMicros, fixedShareMicros)
}

// requireSplitAllocationForSource checks one target allocation against its expected source and target math.
func requireSplitAllocationForSource(t *testing.T, allocations []CostCategorySplitChargeAllocation, targetValue string, sourceCostMicros, allocatedCostMicros, allocationBaseCostMicros int64, fixedShareMicros int) CostCategorySplitChargeAllocation {
	t.Helper()

	for _, allocation := range allocations {
		if allocation.TargetValue != targetValue {
			continue
		}
		if allocation.SourceCostMicros != sourceCostMicros ||
			allocation.AllocatedCostMicros != allocatedCostMicros ||
			allocation.AllocationBaseCostMicros != allocationBaseCostMicros ||
			allocation.FixedShareMicros != fixedShareMicros ||
			allocation.CurrencyCode != defaultBillCurrencyCode {
			t.Fatalf("allocation for %s = %+v, want source %d allocated %d base %d fixed %d USD", targetValue, allocation, sourceCostMicros, allocatedCostMicros, allocationBaseCostMicros, fixedShareMicros)
		}
		return allocation
	}
	t.Fatalf("allocation for target %q not found in %+v", targetValue, allocations)
	return CostCategorySplitChargeAllocation{}
}

// requireSplitAllocationsReconcileBySource verifies each source line's target rows sum back to the source cost.
func requireSplitAllocationsReconcileBySource(t *testing.T, allocations []CostCategorySplitChargeAllocation) {
	t.Helper()

	type sourceKey struct {
		ruleID       string
		lineItemID   string
		currencyCode string
	}
	type sourceTotal struct {
		sourceCostMicros    int64
		allocatedCostMicros int64
		rows                int
	}
	totals := map[sourceKey]sourceTotal{}
	for _, allocation := range allocations {
		key := sourceKey{
			ruleID:       allocation.RuleID,
			lineItemID:   allocation.SourceLineItemID,
			currencyCode: allocation.CurrencyCode,
		}
		total := totals[key]
		if total.rows > 0 && total.sourceCostMicros != allocation.SourceCostMicros {
			t.Fatalf("source allocation %v has inconsistent source cost %d then %d", key, total.sourceCostMicros, allocation.SourceCostMicros)
		}
		total.sourceCostMicros = allocation.SourceCostMicros
		total.allocatedCostMicros += allocation.AllocatedCostMicros
		total.rows++
		totals[key] = total
	}
	if len(totals) == 0 {
		t.Fatalf("allocations = %+v, want at least one source group", allocations)
	}
	for key, total := range totals {
		if total.allocatedCostMicros != total.sourceCostMicros {
			t.Fatalf("source allocation %v total = %+v, want allocated cost to equal source cost", key, total)
		}
	}
}

// requireSplitAllocationResidual checks the expected unallocated amount for a source group.
func requireSplitAllocationResidual(t *testing.T, allocations []CostCategorySplitChargeAllocation, sourceCostMicros, allocatedCostMicros int64) {
	t.Helper()

	var totalAllocated int64
	for _, allocation := range allocations {
		if allocation.SourceCostMicros != sourceCostMicros {
			t.Fatalf("allocation source cost = %+v, want %d", allocation, sourceCostMicros)
		}
		totalAllocated += allocation.AllocatedCostMicros
	}
	if totalAllocated != allocatedCostMicros {
		t.Fatalf("allocated cost total = %d, want %d for source cost %d", totalAllocated, allocatedCostMicros, sourceCostMicros)
	}
}

func requireSplitComparisonRow(t *testing.T, rows []CostCategorySplitChargeComparisonRow, value string, rawCostMicros, splitInCostMicros, splitOutCostMicros, totalAllocatedCostMicros, residualCostMicros int64, lineItems, allocations int) CostCategorySplitChargeComparisonRow {
	t.Helper()

	for _, row := range rows {
		if row.Value != value {
			continue
		}
		if row.RawCostMicros != rawCostMicros ||
			row.SplitInCostMicros != splitInCostMicros ||
			row.SplitOutCostMicros != splitOutCostMicros ||
			row.TotalAllocatedCostMicros != totalAllocatedCostMicros ||
			row.UnallocatedResidualCostMicros != residualCostMicros ||
			row.LineItemCount != lineItems ||
			row.AllocationCount != allocations ||
			row.CurrencyCode != defaultBillCurrencyCode ||
			row.PayerAccountID != AnyCompanyRetailManagementAccountID {
			t.Fatalf("comparison row for %s = %+v", value, row)
		}
		return row
	}
	t.Fatalf("comparison row %q not found in %+v", value, rows)
	return CostCategorySplitChargeComparisonRow{}
}
