package persistence

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func TestCostAnomalyRepositoryRefreshesServiceAccountTagAndCategoryAlerts(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	product, err := NewCostCategoryRepository(db).CreateCategory(ctx, CostCategoryCreateRequest{
		Name:         "Product",
		Description:  "Product showback grouping",
		DefaultValue: "Unallocated",
	})
	if err != nil {
		t.Fatalf("CreateCategory(Product) error = %v", err)
	}

	baselineLineItemID := insertCostAnomalyTestLineItem(t, ctx, db, costAnomalyTestLineItem{
		ID:                 "line-anomaly-baseline-ec2",
		BillingPeriodStart: "2026-01-01",
		BillingPeriodEnd:   "2026-02-01",
		UsageStartTime:     "2026-01-10T00:00:00Z",
		UsageEndTime:       "2026-01-10T01:00:00Z",
		CostMicros:         100_000,
		TagSnapshotJSON:    `{"app":"storefront"}`,
	})
	insertCostAnomalyTestAssignment(t, ctx, db, baselineLineItemID, product, "2026-01-01", "2026-02-01", 100_000)

	currentLineItemID := insertCostAnomalyTestLineItem(t, ctx, db, costAnomalyTestLineItem{
		ID:                 "line-anomaly-current-ec2",
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		UsageStartTime:     "2026-02-10T00:00:00Z",
		UsageEndTime:       "2026-02-10T01:00:00Z",
		CostMicros:         500_000,
		TagSnapshotJSON:    `{"app":"storefront"}`,
	})
	insertCostAnomalyTestAssignment(t, ctx, db, currentLineItemID, product, "2026-02-01", "2026-03-01", 500_000)

	repo := NewCostAnomalyRepository(db)
	result, err := repo.RefreshAlerts(ctx, CostAnomalyRefreshRequest{
		BillingPeriodStart:       "2026-02-01",
		BillingPeriodEnd:         "2026-03-01",
		BaselinePeriodStart:      "2026-01-01",
		BaselinePeriodEnd:        "2026-02-01",
		ThresholdBasisPoints:     20_000,
		MinimumCurrentCostMicros: 50_000,
	})
	if err != nil {
		t.Fatalf("RefreshAlerts() error = %v", err)
	}
	if len(result.Alerts) != 4 {
		t.Fatalf("RefreshAlerts() alerts = %+v, want service, account, tag, and Cost Category alerts", result.Alerts)
	}

	byDimension := map[string]CostAnomalyAlert{}
	for _, alert := range result.Alerts {
		byDimension[alert.DimensionType] = alert
		if alert.CurrentCostMicros != 500_000 ||
			alert.BaselineCostMicros != 100_000 ||
			alert.IncreaseCostMicros != 400_000 ||
			alert.CurrentCostBasisPoints != 50_000 ||
			alert.SpikeKind != CostAnomalySpikeIncrease {
			t.Fatalf("alert %+v, want 5x increase over previous period", alert)
		}
	}
	if byDimension[CostAnomalyDimensionService].DimensionValue != serviceAmazonEC2 {
		t.Fatalf("service alert = %+v, want EC2 service spike", byDimension[CostAnomalyDimensionService])
	}
	if byDimension[CostAnomalyDimensionAccount].DimensionValue != "111122223333" {
		t.Fatalf("account alert = %+v, want Storefront account spike", byDimension[CostAnomalyDimensionAccount])
	}
	if tag := byDimension[CostAnomalyDimensionTag]; tag.DimensionKey != "app" || tag.DimensionValue != "storefront" {
		t.Fatalf("tag alert = %+v, want app=storefront spike", tag)
	}
	if category := byDimension[CostAnomalyDimensionCostCategory]; category.DimensionKey != product.ID || category.DimensionValue != "Storefront" {
		t.Fatalf("Cost Category alert = %+v, want Product=Storefront spike", category)
	}

	listed, err := repo.ListAlerts(ctx, CostAnomalyListRequest{
		BillingPeriodStart:  "2026-02-01",
		BillingPeriodEnd:    "2026-03-01",
		BaselinePeriodStart: "2026-01-01",
		BaselinePeriodEnd:   "2026-02-01",
	})
	if err != nil {
		t.Fatalf("ListAlerts() error = %v", err)
	}
	if len(listed) != len(result.Alerts) {
		t.Fatalf("ListAlerts() = %+v, want persisted refresh results", listed)
	}
}

type costAnomalyTestLineItem struct {
	ID                 string
	BillingPeriodStart string
	BillingPeriodEnd   string
	UsageStartTime     string
	UsageEndTime       string
	CostMicros         int64
	TagSnapshotJSON    string
}

func insertCostAnomalyTestLineItem(t *testing.T, ctx context.Context, db *sql.DB, item costAnomalyTestLineItem) string {
	t.Helper()

	resourceID := "resource-" + item.ID
	usageID := "usage-" + item.ID
	meteringID := "metering-" + item.ID
	periodStart, err := time.Parse(time.DateOnly, item.BillingPeriodStart)
	if err != nil {
		t.Fatalf("parse period start: %v", err)
	}
	periodEnd, err := time.Parse(time.DateOnly, item.BillingPeriodEnd)
	if err != nil {
		t.Fatalf("parse period end: %v", err)
	}
	periodDays := int(periodEnd.Sub(periodStart).Hours() / 24)
	if periodDays <= 0 {
		t.Fatalf("period days = %d, want positive", periodDays)
	}

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO resources (
			id,
			account_id,
			region_code,
			service_code,
			resource_type,
			resource_name,
			status,
			started_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		resourceID,
		"111122223333",
		"us-east-1",
		serviceAmazonEC2,
		"ec2_instance",
		item.ID,
		"active",
		item.BillingPeriodStart+"T00:00:00Z",
	); err != nil {
		t.Fatalf("insert anomaly resource: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO usage_events (
			id,
			resource_id,
			account_id,
			service_code,
			usage_type,
			operation,
			region_code,
			usage_start_time,
			usage_end_time,
			usage_quantity_micros,
			usage_unit,
			tag_snapshot_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		usageID,
		resourceID,
		"111122223333",
		serviceAmazonEC2,
		"instance-hours:t3.medium",
		"RunInstances",
		"us-east-1",
		item.UsageStartTime,
		item.UsageEndTime,
		1_000_000,
		"Hours",
		item.TagSnapshotJSON,
	); err != nil {
		t.Fatalf("insert anomaly usage event: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO metering_records (
			id,
			usage_event_id,
			resource_id,
			account_id,
			service_code,
			usage_type,
			operation,
			region_code,
			usage_start_time,
			usage_end_time,
			usage_quantity_micros,
			usage_unit,
			tag_snapshot_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		meteringID,
		usageID,
		resourceID,
		"111122223333",
		serviceAmazonEC2,
		"instance-hours:t3.medium",
		"RunInstances",
		"us-east-1",
		item.UsageStartTime,
		item.UsageEndTime,
		1_000_000,
		"Hours",
		item.TagSnapshotJSON,
	); err != nil {
		t.Fatalf("insert anomaly metering record: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO bill_line_items (
			id,
			metering_record_id,
			usage_event_id,
			resource_id,
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
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID,
		meteringID,
		usageID,
		resourceID,
		item.BillingPeriodStart,
		item.BillingPeriodEnd,
		periodDays,
		AnyCompanyRetailManagementAccountID,
		"111122223333",
		serviceAmazonEC2,
		"Amazon EC2",
		"Compute Instance",
		"instance-hours:t3.medium",
		"RunInstances",
		"us-east-1",
		billLineItemTypeUsage,
		billLineItemStatusEstimated,
		item.UsageStartTime,
		item.UsageEndTime,
		1_000_000,
		"Hours",
		"InstanceHour",
		1_000_000,
		1_000,
		item.CostMicros,
		defaultBillCurrencyCode,
		"SIM-EC2-T3-MEDIUM-HR",
		"2026-01-01",
		item.TagSnapshotJSON,
		"Anomaly comparison fixture",
	); err != nil {
		t.Fatalf("insert anomaly bill line item: %v", err)
	}
	return item.ID
}

func insertCostAnomalyTestAssignment(t *testing.T, ctx context.Context, db *sql.DB, lineItemID string, category CostCategory, periodStart, periodEnd string, costMicros int64) {
	t.Helper()

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO cost_category_line_item_assignments (
			line_item_id,
			cost_category_id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			usage_account_id,
			line_item_status,
			cost_category_name,
			category_default_value,
			assigned_value,
			assignment_source,
			matched_rule_id,
			matched_rule_order,
			matched_rule_value,
			currency_code,
			unblended_cost_micros
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		lineItemID,
		category.ID,
		periodStart,
		periodEnd,
		AnyCompanyRetailManagementAccountID,
		"111122223333",
		billLineItemStatusEstimated,
		category.Name,
		category.DefaultValue,
		"Storefront",
		"rule",
		"rule-anomaly-product-storefront",
		10,
		"Storefront",
		defaultBillCurrencyCode,
		costMicros,
	); err != nil {
		t.Fatalf("insert anomaly cost category assignment: %v", err)
	}
}
