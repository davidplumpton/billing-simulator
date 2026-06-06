package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestCostExplorerRepositoryFiltersAndGroupsBillLineItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-cost-explorer-ec2",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Cost Explorer web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app":   "storefront",
				"owner": "platform",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-cost-explorer-ec2",
			ResourceID:          "resource-cost-explorer-ec2",
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
			ID:           "resource-cost-explorer-s3",
			AccountID:    "222233334444",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonS3,
			ResourceType: "s3_bucket",
			ResourceName: "Cost Explorer objects",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app": "payments",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-cost-explorer-s3",
			ResourceID:          "resource-cost-explorer-s3",
			UsageType:           "requests:put-1k",
			Operation:           "PutObject",
			UsageStartTime:      "2026-02-02T00:00:00Z",
			UsageEndTime:        "2026-02-03T00:00:00Z",
			UsageQuantityMicros: 1_500_000_000,
			UsageUnit:           "Request",
		},
	)
	recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-cost-explorer-march-ec2",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Cost Explorer later web",
			Status:       "active",
			StartedAt:    "2026-03-01T00:00:00Z",
			Tags: map[string]string{
				"app": "storefront",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-cost-explorer-march-ec2",
			ResourceID:          "resource-cost-explorer-march-ec2",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-03-01T00:00:00Z",
			UsageEndTime:        "2026-03-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)

	result, err := NewCostExplorerRepository(db).Query(ctx, CostExplorerQueryRequest{
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Granularity:    "daily",
		Filters: map[string][]string{
			"service":        {"Amazon EC2"},
			"linked_account": {"111122223333"},
			"region":         {"us-east-1"},
			"usage_type":     {"instance-hours:t3.medium"},
			"line_item_type": {"Usage"},
			"tag:app":        {"storefront"},
		},
		Groupings: []CostExplorerGrouping{
			{Type: "dimension", Key: "service"},
			{Type: "tag", Key: "app"},
		},
	})
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	if result.TotalLineItemCount != 1 || result.TotalUsageQuantityMicros != 2_000_000 || result.TotalUnblendedCostMicros != 83_200 {
		t.Fatalf("query totals = line items %d usage %d cost %d, want one filtered February EC2 charge", result.TotalLineItemCount, result.TotalUsageQuantityMicros, result.TotalUnblendedCostMicros)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("query rows = %+v, want one row", result.Rows)
	}
	row := result.Rows[0]
	if row.TimePeriodStart != "2026-02-01" || row.TimePeriodEnd != "2026-02-02" || row.CurrencyCode != "USD" {
		t.Fatalf("row period/currency = %s/%s %s, want daily USD bucket", row.TimePeriodStart, row.TimePeriodEnd, row.CurrencyCode)
	}
	if row.UnblendedCostMicros != 83_200 || row.UsageQuantityMicros != 2_000_000 || row.LineItemCount != 1 {
		t.Fatalf("row metrics = %+v, want one EC2 line item", row)
	}
	if len(row.GroupValues) != 2 ||
		row.GroupValues[0] != (CostExplorerGroupValue{Type: "dimension", Key: "service", Value: serviceAmazonEC2}) ||
		row.GroupValues[1] != (CostExplorerGroupValue{Type: "tag", Key: "app", Value: "storefront"}) {
		t.Fatalf("row group values = %+v, want service and app groups", row.GroupValues)
	}
}

func TestCostExplorerRepositoryListsAggregateSourceLineItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-cost-explorer-drilldown-storefront",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Cost Explorer drilldown storefront",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app": "storefront",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-cost-explorer-drilldown-storefront",
			ResourceID:          "resource-cost-explorer-drilldown-storefront",
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
			ID:           "resource-cost-explorer-drilldown-untagged",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Cost Explorer drilldown untagged",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"owner": "platform",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-cost-explorer-drilldown-untagged",
			ResourceID:          "resource-cost-explorer-drilldown-untagged",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T03:00:00Z",
			UsageEndTime:        "2026-02-01T04:00:00Z",
			UsageQuantityMicros: 1_000_000,
			UsageUnit:           "Hours",
		},
	)

	request := CostExplorerQueryRequest{
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Granularity:    "monthly",
		Groupings: []CostExplorerGrouping{
			{Type: "tag", Key: "app"},
		},
	}
	repo := NewCostExplorerRepository(db)
	result, err := repo.Query(ctx, request)
	if err != nil {
		t.Fatalf("Query() error = %v", err)
	}
	var untaggedRow CostExplorerQueryRow
	for _, row := range result.Rows {
		if len(row.GroupValues) == 1 && row.GroupValues[0].Value == costExplorerMissingGroupValue {
			untaggedRow = row
			break
		}
	}
	if untaggedRow.TimePeriodStart == "" {
		t.Fatalf("query rows = %+v, want missing-tag aggregate row", result.Rows)
	}
	items, err := repo.ListLineItems(ctx, CostExplorerLineItemRequest{
		Query:           request,
		TimePeriodStart: untaggedRow.TimePeriodStart,
		TimePeriodEnd:   untaggedRow.TimePeriodEnd,
		GroupValues:     untaggedRow.GroupValues,
	})
	if err != nil {
		t.Fatalf("ListLineItems(missing tag row) error = %v", err)
	}
	if len(items) != 1 ||
		items[0].UsageEventID != "usage-cost-explorer-drilldown-untagged" ||
		items[0].ResourceID != "resource-cost-explorer-drilldown-untagged" ||
		items[0].UnblendedCostMicros != 41_600 {
		t.Fatalf("missing-tag drilldown items = %+v, want the untagged EC2 line item", items)
	}

	_, err = repo.ListLineItems(ctx, CostExplorerLineItemRequest{
		Query:           request,
		TimePeriodStart: untaggedRow.TimePeriodStart,
		TimePeriodEnd:   untaggedRow.TimePeriodEnd,
	})
	if err == nil || !strings.Contains(err.Error(), "group count") {
		t.Fatalf("ListLineItems(missing group values) error = %v, want group count validation", err)
	}
}

func TestCostExplorerRepositoryFiltersAndGroupsCostCategoryAssignments(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-cost-explorer-category-ec2",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Category storefront",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app": "storefront",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-cost-explorer-category-ec2",
			ResourceID:          "resource-cost-explorer-category-ec2",
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
			ID:           "resource-cost-explorer-category-s3",
			AccountID:    "222233334444",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonS3,
			ResourceType: "s3_bucket",
			ResourceName: "Category payments",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app": "payments",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-cost-explorer-category-s3",
			ResourceID:          "resource-cost-explorer-category-s3",
			UsageType:           "requests:put-1k",
			Operation:           "PutObject",
			UsageStartTime:      "2026-02-02T00:00:00Z",
			UsageEndTime:        "2026-02-03T00:00:00Z",
			UsageQuantityMicros: 1_500_000_000,
			UsageUnit:           "Request",
		},
	)

	categoryRepo := NewCostCategoryRepository(db)
	product, err := categoryRepo.CreateCategory(ctx, CostCategoryCreateRequest{
		Name:         "Product",
		DefaultValue: "Unmapped",
	})
	if err != nil {
		t.Fatalf("CreateCategory(Product) error = %v", err)
	}
	if _, err := categoryRepo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: product.ID,
		RuleOrder:      10,
		Value:          "Storefront",
		Conditions: []CostCategoryRuleCondition{
			{Dimension: CostCategoryRuleMatchTag, TagKey: "app", Values: []string{"storefront"}},
		},
	}); err != nil {
		t.Fatalf("CreateRule(Storefront) error = %v", err)
	}
	if _, err := categoryRepo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: product.ID,
		RuleOrder:      20,
		Value:          "Payments",
		Conditions: []CostCategoryRuleCondition{
			{Dimension: CostCategoryRuleMatchTag, TagKey: "app", Values: []string{"payments"}},
		},
	}); err != nil {
		t.Fatalf("CreateRule(Payments) error = %v", err)
	}

	result, err := NewCostExplorerRepository(db).Query(ctx, CostExplorerQueryRequest{
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Granularity:    "monthly",
		Filters: map[string][]string{
			"cost_category:Product": {"Storefront"},
		},
		Groupings: []CostExplorerGrouping{
			{Type: "cost_category", Key: "Product"},
			{Type: "dimension", Key: "linked_account"},
		},
	})
	if err != nil {
		t.Fatalf("Query(cost category) error = %v", err)
	}
	if result.TotalLineItemCount != 1 || result.TotalUnblendedCostMicros != 83_200 {
		t.Fatalf("cost category query totals = line items %d cost %d, want Storefront EC2 only", result.TotalLineItemCount, result.TotalUnblendedCostMicros)
	}
	if len(result.Rows) != 1 {
		t.Fatalf("cost category rows = %+v, want one row", result.Rows)
	}
	row := result.Rows[0]
	if row.TimePeriodStart != "2026-02-01" || row.TimePeriodEnd != "2026-03-01" {
		t.Fatalf("cost category row period = %s/%s, want February monthly bucket", row.TimePeriodStart, row.TimePeriodEnd)
	}
	if len(row.GroupValues) != 2 ||
		row.GroupValues[0] != (CostExplorerGroupValue{Type: "cost_category", Key: "Product", Value: "Storefront"}) ||
		row.GroupValues[1] != (CostExplorerGroupValue{Type: "dimension", Key: "linked_account", Value: "111122223333"}) {
		t.Fatalf("cost category row group values = %+v, want product and linked account", row.GroupValues)
	}
}

func TestCostExplorerRepositoryRefreshesSummaryTablesAfterBillingChanges(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	tagRepo := NewCostAllocationTagRepository(db)

	resources := []ResourceCreateRequest{
		{
			ID:           "resource-cost-summary-web",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Summary web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app":   "storefront",
				"owner": "platform",
			},
		},
		{
			ID:           "resource-cost-summary-batch",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Summary batch",
			Status:       "active",
			StartedAt:    "2026-02-02T00:00:00Z",
			Tags: map[string]string{
				"app": "payments",
			},
		},
		{
			ID:           "resource-cost-summary-assets",
			AccountID:    "444455556666",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonS3,
			ResourceType: "s3_bucket",
			ResourceName: "Summary assets",
			Status:       "active",
			StartedAt:    "2026-02-02T00:00:00Z",
			Tags: map[string]string{
				"app":   "storefront",
				"Owner": "platform",
			},
		},
	}
	for _, request := range resources {
		if _, err := usageRepo.CreateResource(ctx, request); err != nil {
			t.Fatalf("CreateResource(%s) error = %v", request.ID, err)
		}
	}
	if _, err := tagRepo.RefreshDiscoveredTags(ctx, "2026-02-02T00:00:00Z"); err != nil {
		t.Fatalf("RefreshDiscoveredTags() error = %v", err)
	}

	usageEvents := []UsageEventCreateRequest{
		{
			ID:                  "usage-cost-summary-web",
			ResourceID:          "resource-cost-summary-web",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
		{
			ID:                  "usage-cost-summary-batch",
			ResourceID:          "resource-cost-summary-batch",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-02T00:00:00Z",
			UsageEndTime:        "2026-02-02T01:00:00Z",
			UsageQuantityMicros: 1_000_000,
			UsageUnit:           "Hours",
		},
		{
			ID:                  "usage-cost-summary-assets",
			ResourceID:          "resource-cost-summary-assets",
			UsageType:           "requests:put-1k",
			Operation:           "PutObject",
			UsageStartTime:      "2026-02-02T00:00:00Z",
			UsageEndTime:        "2026-02-03T00:00:00Z",
			UsageQuantityMicros: 1_500_000_000,
			UsageUnit:           "Request",
		},
	}
	for _, request := range usageEvents {
		if _, err := usageRepo.RecordUsageEvent(ctx, request); err != nil {
			t.Fatalf("RecordUsageEvent(%s) error = %v", request.ID, err)
		}
	}
	if result, err := NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	} else if result.RecordsCreated != 3 {
		t.Fatalf("GenerateMeteringRecords() = %+v, want three records", result)
	}
	if result, err := NewBillLineItemRepository(db).GenerateBillLineItems(ctx, BillLineItemGenerationRequest{
		PayerAccountID: AnyCompanyRetailManagementAccountID,
	}); err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	} else if result.ItemsCreated != 3 {
		t.Fatalf("GenerateBillLineItems() = %+v, want three line items", result)
	}

	var dailyRows int
	var dailyLineItems, dailyUsageMicros, dailyCostMicros int64
	if err := db.QueryRowContext(
		ctx,
		`SELECT
			COUNT(*),
			COALESCE(SUM(line_item_count), 0),
			COALESCE(SUM(usage_quantity_micros), 0),
			COALESCE(SUM(unblended_cost_micros), 0)
		 FROM daily_cost_summary
		 WHERE billing_period_start = ? AND billing_period_end = ?`,
		"2026-02-01",
		"2026-03-01",
	).Scan(&dailyRows, &dailyLineItems, &dailyUsageMicros, &dailyCostMicros); err != nil {
		t.Fatalf("read daily cost summary: %v", err)
	}
	if dailyRows != 3 || dailyLineItems != 3 || dailyUsageMicros != 1_503_000_000 || dailyCostMicros != 132_300 {
		t.Fatalf("daily summary rows/count/usage/cost = %d/%d/%d/%d, want 3 rows, 3 lines, 1503000000 usage, 132300 cost", dailyRows, dailyLineItems, dailyUsageMicros, dailyCostMicros)
	}

	var ec2MonthlyLineItems int
	var ec2MonthlyUsageMicros, ec2MonthlyCostMicros int64
	if err := db.QueryRowContext(
		ctx,
		`SELECT line_item_count, usage_quantity_micros, unblended_cost_micros
		 FROM monthly_account_service_summary
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND payer_account_id = ?
		   AND usage_account_id = ?
		   AND service_code = ?
		   AND line_item_status = ?`,
		"2026-02-01",
		"2026-03-01",
		AnyCompanyRetailManagementAccountID,
		"111122223333",
		serviceAmazonEC2,
		billLineItemStatusEstimated,
	).Scan(&ec2MonthlyLineItems, &ec2MonthlyUsageMicros, &ec2MonthlyCostMicros); err != nil {
		t.Fatalf("read monthly account service summary: %v", err)
	}
	if ec2MonthlyLineItems != 2 || ec2MonthlyUsageMicros != 3_000_000 || ec2MonthlyCostMicros != 124_800 {
		t.Fatalf("EC2 monthly summary = lines %d usage %d cost %d, want two EC2 items totaling 124800", ec2MonthlyLineItems, ec2MonthlyUsageMicros, ec2MonthlyCostMicros)
	}

	var ownerLineItems, ownerResources, ownerTaggedResources, ownerUntaggedResources, ownerCaseResources int
	var ownerTaggedCostMicros, ownerUntaggedCostMicros, ownerCaseMismatchCostMicros, ownerTotalCostMicros int64
	var ownerCaseMismatchKeysJSON string
	if err := db.QueryRowContext(
		ctx,
		`SELECT
			line_item_count,
			resource_count,
			tagged_resource_count,
			untagged_resource_count,
			case_mismatch_resource_count,
			tagged_cost_micros,
			untagged_cost_micros,
			case_mismatch_cost_micros,
			total_cost_micros,
			case_mismatch_keys_json
		 FROM tag_coverage_summary
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND tag_key = ?
		   AND dimension = ?`,
		"2026-02-01",
		"2026-03-01",
		"owner",
		CostAllocationCoverageDimensionKey,
	).Scan(
		&ownerLineItems,
		&ownerResources,
		&ownerTaggedResources,
		&ownerUntaggedResources,
		&ownerCaseResources,
		&ownerTaggedCostMicros,
		&ownerUntaggedCostMicros,
		&ownerCaseMismatchCostMicros,
		&ownerTotalCostMicros,
		&ownerCaseMismatchKeysJSON,
	); err != nil {
		t.Fatalf("read owner tag coverage summary: %v", err)
	}
	if ownerLineItems != 3 ||
		ownerResources != 3 ||
		ownerTaggedResources != 1 ||
		ownerUntaggedResources != 1 ||
		ownerCaseResources != 1 ||
		ownerTaggedCostMicros != 83_200 ||
		ownerUntaggedCostMicros != 41_600 ||
		ownerCaseMismatchCostMicros != 7_500 ||
		ownerTotalCostMicros != 132_300 ||
		ownerCaseMismatchKeysJSON != `["Owner"]` {
		t.Fatalf("owner tag coverage summary = lines %d resources %d/%d/%d/%d costs %d/%d/%d/%d keys %s, want exact/missing/case-mismatch coverage",
			ownerLineItems,
			ownerResources,
			ownerTaggedResources,
			ownerUntaggedResources,
			ownerCaseResources,
			ownerTaggedCostMicros,
			ownerUntaggedCostMicros,
			ownerCaseMismatchCostMicros,
			ownerTotalCostMicros,
			ownerCaseMismatchKeysJSON,
		)
	}

	categoryRepo := NewCostCategoryRepository(db)
	product, err := categoryRepo.CreateCategory(ctx, CostCategoryCreateRequest{
		Name:         "Product",
		DefaultValue: "Unmapped",
	})
	if err != nil {
		t.Fatalf("CreateCategory(Product) error = %v", err)
	}
	if _, err := categoryRepo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: product.ID,
		RuleOrder:      10,
		Value:          "Storefront",
		Conditions: []CostCategoryRuleCondition{
			{Dimension: CostCategoryRuleMatchTag, TagKey: "app", Values: []string{"storefront"}},
		},
	}); err != nil {
		t.Fatalf("CreateRule(Storefront) error = %v", err)
	}
	if _, err := categoryRepo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: product.ID,
		RuleOrder:      20,
		Value:          "Payments",
		Conditions: []CostCategoryRuleCondition{
			{Dimension: CostCategoryRuleMatchTag, TagKey: "app", Values: []string{"payments"}},
		},
	}); err != nil {
		t.Fatalf("CreateRule(Payments) error = %v", err)
	}

	var storefrontLineItems, paymentsLineItems int
	var storefrontCostMicros, paymentsCostMicros int64
	if err := db.QueryRowContext(
		ctx,
		`SELECT
			COALESCE(SUM(CASE WHEN assigned_value = 'Storefront' THEN line_item_count ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN assigned_value = 'Storefront' THEN unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN assigned_value = 'Payments' THEN line_item_count ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN assigned_value = 'Payments' THEN unblended_cost_micros ELSE 0 END), 0)
		 FROM cost_category_summary
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND cost_category_id = ?`,
		"2026-02-01",
		"2026-03-01",
		product.ID,
	).Scan(&storefrontLineItems, &storefrontCostMicros, &paymentsLineItems, &paymentsCostMicros); err != nil {
		t.Fatalf("read cost category summary: %v", err)
	}
	if storefrontLineItems != 2 || storefrontCostMicros != 90_700 || paymentsLineItems != 1 || paymentsCostMicros != 41_600 {
		t.Fatalf("cost category summary = Storefront %d/%d Payments %d/%d, want category assignment rollups", storefrontLineItems, storefrontCostMicros, paymentsLineItems, paymentsCostMicros)
	}
}

func TestCostExplorerRepositoryValidatesQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewCostExplorerRepository(db)

	tests := []struct {
		name    string
		request CostExplorerQueryRequest
		want    string
	}{
		{
			name: "invalid date",
			request: CostExplorerQueryRequest{
				DateRangeStart: "2026/02/01",
				DateRangeEnd:   "2026-03-01",
			},
			want: "date range start",
		},
		{
			name: "unsupported granularity",
			request: CostExplorerQueryRequest{
				DateRangeStart: "2026-02-01",
				DateRangeEnd:   "2026-03-01",
				Granularity:    "weekly",
			},
			want: "granularity",
		},
		{
			name: "unsupported dimension filter",
			request: CostExplorerQueryRequest{
				DateRangeStart: "2026-02-01",
				DateRangeEnd:   "2026-03-01",
				Filters: map[string][]string{
					"operation": {"RunInstances"},
				},
			},
			want: "dimension",
		},
		{
			name: "empty tag filter key",
			request: CostExplorerQueryRequest{
				DateRangeStart: "2026-02-01",
				DateRangeEnd:   "2026-03-01",
				Filters: map[string][]string{
					"tag:": {"storefront"},
				},
			},
			want: "tag filter key",
		},
		{
			name: "empty filter values",
			request: CostExplorerQueryRequest{
				DateRangeStart: "2026-02-01",
				DateRangeEnd:   "2026-03-01",
				Filters: map[string][]string{
					"service": {""},
				},
			},
			want: "needs at least one value",
		},
		{
			name: "too many groupings",
			request: CostExplorerQueryRequest{
				DateRangeStart: "2026-02-01",
				DateRangeEnd:   "2026-03-01",
				Groupings: []CostExplorerGrouping{
					{Type: "dimension", Key: "service"},
					{Type: "dimension", Key: "linked_account"},
					{Type: "dimension", Key: "region"},
				},
			},
			want: "at most two groupings",
		},
		{
			name: "duplicate grouping",
			request: CostExplorerQueryRequest{
				DateRangeStart: "2026-02-01",
				DateRangeEnd:   "2026-03-01",
				Groupings: []CostExplorerGrouping{
					{Type: "dimension", Key: "service"},
					{Type: "dimension", Key: "service"},
				},
			},
			want: "duplicated",
		},
		{
			name: "unknown cost category filter",
			request: CostExplorerQueryRequest{
				DateRangeStart: "2026-02-01",
				DateRangeEnd:   "2026-03-01",
				Filters: map[string][]string{
					"cost_category:Missing": {"Shared"},
				},
			},
			want: "does not exist",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := repo.Query(ctx, tt.request)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Query() error = %v, want containing %q", err, tt.want)
			}
		})
	}
}
