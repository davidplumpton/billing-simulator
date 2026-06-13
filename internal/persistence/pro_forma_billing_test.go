package persistence

import (
	"context"
	"database/sql"
	"testing"
)

func TestProFormaBillingRepositoryAppliesPricingPlanWithoutChangingBillableCost(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	meteringRepo := NewMeteringRepository(db)
	lineItemRepo := NewBillLineItemRepository(db)
	proFormaRepo := NewProFormaBillingRepository(db)

	for _, request := range []ResourceCreateRequest{
		{
			ID:           "resource-pro-forma-web",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Pro forma storefront web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
		},
		{
			ID:           "resource-pro-forma-bucket",
			AccountID:    "222233334444",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonS3,
			ResourceType: "s3_bucket",
			ResourceName: "Pro forma shared bucket",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
		},
	} {
		if _, err := usageRepo.CreateResource(ctx, request); err != nil {
			t.Fatalf("CreateResource(%s) error = %v", request.ID, err)
		}
	}
	for _, request := range []UsageEventCreateRequest{
		{
			ID:                  "usage-pro-forma-web",
			ResourceID:          "resource-pro-forma-web",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
		{
			ID:                  "usage-pro-forma-bucket",
			ResourceID:          "resource-pro-forma-bucket",
			UsageType:           "requests:put-1k",
			Operation:           "PutObject",
			UsageStartTime:      "2026-02-02T00:00:00Z",
			UsageEndTime:        "2026-02-03T00:00:00Z",
			UsageQuantityMicros: 1_500_000_000,
			UsageUnit:           "Request",
		},
	} {
		if _, err := usageRepo.RecordUsageEvent(ctx, request); err != nil {
			t.Fatalf("RecordUsageEvent(%s) error = %v", request.ID, err)
		}
	}
	if _, err := meteringRepo.GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}
	if _, err := lineItemRepo.GenerateBillLineItems(ctx, BillLineItemGenerationRequest{}); err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}
	sourceCountBefore, sourceCostBefore := proFormaBillLineItemTotals(t, ctx, db)

	plan, err := proFormaRepo.CreatePricingPlan(ctx, ProFormaPricingPlanCreateRequest{
		Name:        "Retail Showback",
		Description: "Internal rates for product teams",
	})
	if err != nil {
		t.Fatalf("CreatePricingPlan() error = %v", err)
	}
	if _, err := proFormaRepo.CreatePricingRule(ctx, ProFormaPricingRuleCreateRequest{
		PricingPlanID:             plan.ID,
		ServiceCode:               serviceAmazonEC2,
		RateMultiplierBasisPoints: 15_000,
		Description:               "Compute margin",
	}); err != nil {
		t.Fatalf("CreatePricingRule() error = %v", err)
	}
	group, err := proFormaRepo.CreateBillingGroup(ctx, ProFormaBillingGroupCreateRequest{
		Name:          "Storefront Showback",
		Description:   "Product team pro forma view",
		PricingPlanID: plan.ID,
	})
	if err != nil {
		t.Fatalf("CreateBillingGroup() error = %v", err)
	}
	for _, accountID := range []string{"111122223333", "222233334444"} {
		if _, err := proFormaRepo.AssignAccountToGroup(ctx, ProFormaBillingGroupAccountCreateRequest{
			BillingGroupID: group.ID,
			AccountID:      accountID,
		}); err != nil {
			t.Fatalf("AssignAccountToGroup(%s) error = %v", accountID, err)
		}
	}

	refresh, err := proFormaRepo.RefreshLineItems(ctx, ProFormaRefreshRequest{
		BillingGroupID:     group.ID,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("RefreshLineItems() error = %v", err)
	}
	if refresh.BillingGroupsRefreshed != 1 ||
		refresh.SourceLineItems != 2 ||
		refresh.ProFormaLineItems != 2 ||
		refresh.SourceCostMicros != 90_700 ||
		refresh.ProFormaCostMicros != 132_300 ||
		refresh.AdjustmentMicros != 41_600 {
		t.Fatalf("RefreshLineItems() = %+v, want EC2 uplift with unchanged S3 source", refresh)
	}

	summaries, err := proFormaRepo.ListBillingGroupSummaries(ctx, ProFormaSummaryRequest{
		BillingGroupID:     group.ID,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("ListBillingGroupSummaries() error = %v", err)
	}
	if len(summaries) != 1 ||
		summaries[0].SourceCostMicros != 90_700 ||
		summaries[0].ProFormaCostMicros != 132_300 ||
		summaries[0].AdjustmentMicros != 41_600 {
		t.Fatalf("summaries = %+v, want one adjusted showback row", summaries)
	}
	items, err := proFormaRepo.ListLineItems(ctx, ProFormaLineItemListRequest{
		BillingGroupID:     group.ID,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("ListLineItems() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("ListLineItems() length = %d, want 2: %+v", len(items), items)
	}
	requireProFormaLineItem(t, items, serviceAmazonEC2, 83_200, 124_800, 41_600, 15_000)
	requireProFormaLineItem(t, items, serviceAmazonS3, 7_500, 7_500, 0, proFormaDefaultMultiplierBPS)

	sourceCountAfter, sourceCostAfter := proFormaBillLineItemTotals(t, ctx, db)
	if sourceCountAfter != sourceCountBefore || sourceCostAfter != sourceCostBefore {
		t.Fatalf("billable line items changed from count/cost %d/%d to %d/%d", sourceCountBefore, sourceCostBefore, sourceCountAfter, sourceCostAfter)
	}
}

type proFormaQueryRower interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func proFormaBillLineItemTotals(t *testing.T, ctx context.Context, db proFormaQueryRower) (int, int64) {
	t.Helper()

	var count int
	var cost int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(unblended_cost_micros), 0)
		FROM bill_line_items
		WHERE billing_period_start = '2026-02-01'
		  AND billing_period_end = '2026-03-01'
	`).Scan(&count, &cost); err != nil {
		t.Fatalf("read bill line item totals: %v", err)
	}
	return count, cost
}

func requireProFormaLineItem(t *testing.T, items []ProFormaLineItem, serviceCode string, sourceCost, proFormaCost, adjustment int64, multiplier int) {
	t.Helper()

	for _, item := range items {
		if item.ServiceCode != serviceCode {
			continue
		}
		if item.SourceCostMicros != sourceCost ||
			item.ProFormaCostMicros != proFormaCost ||
			item.AdjustmentMicros != adjustment ||
			item.RateMultiplierBasisPoints != multiplier ||
			item.SourceBillLineItemID == "" {
			t.Fatalf("pro forma %s item = %+v, want source %d pro forma %d adjustment %d multiplier %d", serviceCode, item, sourceCost, proFormaCost, adjustment, multiplier)
		}
		return
	}
	t.Fatalf("pro forma items = %+v, want service %s", items, serviceCode)
}
