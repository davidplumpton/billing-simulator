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
