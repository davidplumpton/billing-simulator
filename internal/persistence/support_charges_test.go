package persistence

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestSupportChargeRepositoryGeneratesMinimumFeeFromEstimatedEligibleSpend(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	lineItem := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-support-minimum-ec2",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-support-minimum-ec2",
			ResourceID:          "resource-support-minimum-ec2",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)

	repo := NewSupportChargeRepository(db)
	result, err := repo.GenerateSupportCharges(ctx, SupportChargeGenerationRequest{
		PayerAccountID: "111122223333",
		PeriodStart:    "2026-02-01",
		PeriodEnd:      "2026-03-01",
		LineItemStatus: billLineItemStatusEstimated,
	})
	if err != nil {
		t.Fatalf("GenerateSupportCharges() error = %v", err)
	}
	if result.ItemsCreated != 1 || result.ItemsUpdated != 0 || len(result.Items) != 1 {
		t.Fatalf("GenerateSupportCharges() = %+v, want one created support item", result)
	}
	support := result.Items[0]
	if support.LineItemType != billLineItemTypeFee ||
		support.LineItemStatus != billLineItemStatusEstimated ||
		support.ServiceCode != serviceAWSSupport ||
		support.UsageQuantityMicros != lineItem.UnblendedCostMicros ||
		support.PricingQuantityMicros != lineItem.UnblendedCostMicros ||
		support.UnblendedRateMicros != 100_000 ||
		support.UnblendedCostMicros != supportBusinessMinimumCostMicros {
		t.Fatalf("support item = %+v, want 10%% rate with synthetic minimum", support)
	}
	if support.MeteringRecordID != "" || support.UsageEventID != "" || support.ResourceID != "" {
		t.Fatalf("support lineage = %q/%q/%q, want period-level fee without usage lineage", support.MeteringRecordID, support.UsageEventID, support.ResourceID)
	}
	if !strings.Contains(support.Description, "1 eligible line item") {
		t.Fatalf("support description = %q, want source count", support.Description)
	}

	sources, err := repo.ListSupportChargeSources(ctx, support.ID)
	if err != nil {
		t.Fatalf("ListSupportChargeSources() error = %v", err)
	}
	if len(sources) != 1 ||
		sources[0].SourceBillLineItemID != lineItem.ID ||
		sources[0].SourceCostMicros != lineItem.UnblendedCostMicros {
		t.Fatalf("support sources = %+v, want source line item %s", sources, lineItem.ID)
	}

	replay, err := repo.GenerateSupportCharges(ctx, SupportChargeGenerationRequest{
		PayerAccountID: "111122223333",
		PeriodStart:    "2026-02-01",
		PeriodEnd:      "2026-03-01",
		LineItemStatus: billLineItemStatusEstimated,
	})
	if err != nil {
		t.Fatalf("GenerateSupportCharges(replay) error = %v", err)
	}
	if replay.ItemsCreated != 0 || replay.ItemsUpdated != 1 || len(replay.Items) != 1 {
		t.Fatalf("GenerateSupportCharges(replay) = %+v, want idempotent update", replay)
	}
}

func TestSupportChargeRepositoryUsesPercentageAboveMinimumAndExcludesIneligibleTypes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	eligible := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-support-marketplace",
			AccountID:    "111122223333",
			RegionCode:   "global",
			ServiceCode:  "AWSMarketplace",
			ResourceType: "marketplace_subscription",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-support-marketplace",
			ResourceID:          "resource-support-marketplace",
			UsageType:           "marketplace-security-tool-month",
			Operation:           "MonthlySubscription",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-03-01T00:00:00Z",
			UsageQuantityMicros: 1_000_000,
			UsageUnit:           "Months",
		},
	)
	insertIneligibleCreditLineItem(t, ctx, db, "credit-support-excluded", "111122223333", 750_000_000)

	repo := NewSupportChargeRepository(db)
	result, err := repo.GenerateSupportCharges(ctx, SupportChargeGenerationRequest{
		PayerAccountID: "111122223333",
		PeriodStart:    "2026-02-01",
		PeriodEnd:      "2026-03-01",
		LineItemStatus: billLineItemStatusEstimated,
	})
	if err != nil {
		t.Fatalf("GenerateSupportCharges() error = %v", err)
	}
	if result.ItemsCreated != 1 || len(result.Items) != 1 {
		t.Fatalf("GenerateSupportCharges() = %+v, want one support item", result)
	}
	support := result.Items[0]
	if support.UsageQuantityMicros != eligible.UnblendedCostMicros ||
		support.UnblendedCostMicros != 25_000_000 {
		t.Fatalf("support item = %+v, want 10%% of eligible marketplace spend and excluded credit", support)
	}

	sources, err := repo.ListSupportChargeSources(ctx, support.ID)
	if err != nil {
		t.Fatalf("ListSupportChargeSources() error = %v", err)
	}
	if len(sources) != 1 || sources[0].SourceBillLineItemID != eligible.ID {
		t.Fatalf("support sources = %+v, want only eligible usage source %s", sources, eligible.ID)
	}
}

func insertIneligibleCreditLineItem(t *testing.T, ctx context.Context, db *sql.DB, id, payerAccountID string, costMicros int64) {
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
		payerAccountID,
		"111122223333",
		serviceAmazonEC2,
		"Amazon EC2",
		"Compute Instance",
		"synthetic-credit",
		"CreditAdjustment",
		"us-east-1",
		"Credit",
		billLineItemStatusEstimated,
		"2026-02-01T00:00:00Z",
		"2026-03-01T00:00:00Z",
		1_000_000,
		"USD",
		"USD",
		1_000_000,
		0,
		costMicros,
		"USD",
		"SIM-EC2-T3-MEDIUM-HR",
		"2026-01-01",
		"{}",
		"Synthetic credit that must not count toward Support",
	); err != nil {
		t.Fatalf("insert ineligible credit line item: %v", err)
	}
}
