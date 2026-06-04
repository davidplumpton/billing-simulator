package persistence

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestBillLineItemRepositoryGeneratesPricedUsageLineItems(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	meteringRepo := NewMeteringRepository(db)
	lineItemRepo := NewBillLineItemRepository(db)

	resource, err := usageRepo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-bill-line-ec2",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  serviceAmazonEC2,
		ResourceType: "ec2_instance",
		ResourceName: "Billable web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
		Tags: map[string]string{
			"app":   "storefront",
			"owner": "platform",
		},
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	event, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-bill-line-ec2-hours",
		ResourceID:          resource.ID,
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: int64(2_000_000),
		UsageUnit:           "Hours",
	})
	if err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	metering, err := meteringRepo.GenerateMeteringRecords(ctx)
	if err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}

	result, err := lineItemRepo.GenerateBillLineItems(ctx, BillLineItemGenerationRequest{
		PayerAccountID: "999988887777",
	})
	if err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}
	if result.ItemsCreated != 1 || len(result.Items) != 1 {
		t.Fatalf("GenerateBillLineItems() = %+v, want one created item", result)
	}
	item := result.Items[0]
	if item.MeteringRecordID != metering.Records[0].ID || item.UsageEventID != event.ID || item.ResourceID != resource.ID {
		t.Fatalf("bill line item lineage = %+v, want metering, usage event, and resource IDs", item)
	}
	if item.PayerAccountID != "999988887777" || item.UsageAccountID != "111122223333" {
		t.Fatalf("bill line item accounts = payer %q usage %q, want payer override and usage account", item.PayerAccountID, item.UsageAccountID)
	}
	if item.ServiceCode != serviceAmazonEC2 || item.ServiceName != "Amazon EC2" || item.ProductFamily != "Compute Instance" {
		t.Fatalf("bill line item service metadata = %+v, want EC2 metadata", item)
	}
	if item.LineItemType != "Usage" || item.CurrencyCode != "USD" {
		t.Fatalf("bill line item type/currency = %q/%q, want Usage/USD", item.LineItemType, item.CurrencyCode)
	}
	if item.LineItemStatus != billLineItemStatusEstimated {
		t.Fatalf("bill line item status = %q, want estimated", item.LineItemStatus)
	}
	if item.BillingPeriodStart != "2026-02-01" || item.BillingPeriodEnd != "2026-03-01" || item.BillingPeriodDays != 28 {
		t.Fatalf("billing period = %s/%s days %d, want February 2026", item.BillingPeriodStart, item.BillingPeriodEnd, item.BillingPeriodDays)
	}
	if item.UsageQuantityMicros != 2_000_000 || item.UsageUnit != "Hours" {
		t.Fatalf("usage amount = %d %s, want original metered quantity", item.UsageQuantityMicros, item.UsageUnit)
	}
	if item.PricingUnit != "InstanceHour" || item.PricingQuantityMicros != 2_000_000 {
		t.Fatalf("pricing quantity = %d %s, want 2 InstanceHour", item.PricingQuantityMicros, item.PricingUnit)
	}
	if item.PriceCatalogSKU != "SIM-EC2-T3-MEDIUM-HR" || item.PriceEffectiveDate != "2026-01-01" {
		t.Fatalf("price lineage = %q/%q, want EC2 synthetic SKU", item.PriceCatalogSKU, item.PriceEffectiveDate)
	}
	if item.UnblendedRateMicros != 41_600 || item.UnblendedCostMicros != 83_200 {
		t.Fatalf("pricing math = rate %d cost %d, want 41600/83200 micros", item.UnblendedRateMicros, item.UnblendedCostMicros)
	}
	if item.TagSnapshot["app"] != "storefront" || item.TagSnapshot["owner"] != "platform" {
		t.Fatalf("tag snapshot = %+v, want usage tag snapshot", item.TagSnapshot)
	}

	items, err := lineItemRepo.ListBillLineItems(ctx, 10)
	if err != nil {
		t.Fatalf("ListBillLineItems() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != item.ID || items[0].CreatedAt == "" {
		t.Fatalf("ListBillLineItems() = %+v, want persisted bill line item", items)
	}
}

func TestBillLineItemRepositoryIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	meteringRepo := NewMeteringRepository(db)
	lineItemRepo := NewBillLineItemRepository(db)

	resource, err := usageRepo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-bill-line-s3",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  serviceAmazonS3,
		ResourceType: "s3_bucket",
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-bill-line-s3-put",
		ResourceID:          resource.ID,
		UsageType:           "requests:put-1k",
		Operation:           "PutObject",
		UsageStartTime:      "2026-02-02T00:00:00Z",
		UsageEndTime:        "2026-02-03T00:00:00Z",
		UsageQuantityMicros: int64(1_500_000_000),
		UsageUnit:           "Request",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if _, err := meteringRepo.GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}

	first, err := lineItemRepo.GenerateBillLineItems(ctx, BillLineItemGenerationRequest{})
	if err != nil {
		t.Fatalf("first GenerateBillLineItems() error = %v", err)
	}
	second, err := lineItemRepo.GenerateBillLineItems(ctx, BillLineItemGenerationRequest{})
	if err != nil {
		t.Fatalf("second GenerateBillLineItems() error = %v", err)
	}
	if first.ItemsCreated != 1 || second.ItemsCreated != 0 {
		t.Fatalf("bill line item created counts = %d/%d, want 1/0", first.ItemsCreated, second.ItemsCreated)
	}
	if first.Items[0].PricingQuantityMicros != 1_500_000 || first.Items[0].UnblendedCostMicros != 7_500 {
		t.Fatalf("S3 PUT pricing = quantity %d cost %d, want 1500000/7500 micros", first.Items[0].PricingQuantityMicros, first.Items[0].UnblendedCostMicros)
	}
}

func TestBillLineItemRepositoryProratesStorageByBillingPeriodDays(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	meteringRepo := NewMeteringRepository(db)
	lineItemRepo := NewBillLineItemRepository(db)

	resource, err := usageRepo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-bill-line-ebs",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  serviceAmazonEBS,
		ResourceType: "ebs_volume",
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-bill-line-ebs-storage",
		ResourceID:          resource.ID,
		UsageType:           "storage:gp3-gb-month",
		Operation:           "VolumeStorage",
		UsageStartTime:      "2026-02-10T00:00:00Z",
		UsageEndTime:        "2026-02-11T00:00:00Z",
		UsageQuantityMicros: int64(280_000_000),
		UsageUnit:           "GBDay",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if _, err := meteringRepo.GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}

	result, err := lineItemRepo.GenerateBillLineItems(ctx, BillLineItemGenerationRequest{})
	if err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}
	if result.ItemsCreated != 1 {
		t.Fatalf("GenerateBillLineItems() created %d, want 1", result.ItemsCreated)
	}
	item := result.Items[0]
	if item.BillingPeriodDays != 28 {
		t.Fatalf("billing period days = %d, want February 2026 proration over 28 days", item.BillingPeriodDays)
	}
	if item.PricingUnit != "GBMonth" || item.PricingQuantityMicros != 10_000_000 {
		t.Fatalf("storage pricing quantity = %d %s, want 10 GBMonth", item.PricingQuantityMicros, item.PricingUnit)
	}
	if item.UnblendedRateMicros != 80_000 || item.UnblendedCostMicros != 800_000 {
		t.Fatalf("storage pricing = rate %d cost %d, want 80000/800000 micros", item.UnblendedRateMicros, item.UnblendedCostMicros)
	}
}

func TestBillLineItemRepositoryRejectsCrossBillingPeriodUsage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	meteringRepo := NewMeteringRepository(db)
	lineItemRepo := NewBillLineItemRepository(db)

	resource, err := usageRepo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-bill-line-cross-period",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  serviceAmazonEC2,
		ResourceType: "ec2_instance",
		Status:       "active",
		StartedAt:    "2026-02-28T22:00:00Z",
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-bill-line-cross-period",
		ResourceID:          resource.ID,
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-28T22:00:00Z",
		UsageEndTime:        "2026-03-01T02:00:00Z",
		UsageQuantityMicros: 4_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	metering, err := meteringRepo.GenerateMeteringRecords(ctx)
	if err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}

	_, err = lineItemRepo.GenerateBillLineItems(ctx, BillLineItemGenerationRequest{})
	if err == nil {
		t.Fatal("GenerateBillLineItems(cross-period usage) error = nil, want rejection")
	}
	if !strings.Contains(err.Error(), "crosses billing period") || !strings.Contains(err.Error(), metering.Records[0].ID) {
		t.Fatalf("GenerateBillLineItems(cross-period usage) error = %q, want metering context and cross-period message", err.Error())
	}
	items, listErr := lineItemRepo.ListBillLineItems(ctx, 10)
	if listErr != nil {
		t.Fatalf("ListBillLineItems() error = %v", listErr)
	}
	if len(items) != 0 {
		t.Fatalf("bill line items after cross-period rejection = %+v, want none", items)
	}
}

func TestBillLineItemSchemaRejectsCrossPeriodPersistence(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	item := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-bill-line-trigger",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-bill-line-trigger",
			ResourceID:          "resource-bill-line-trigger",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-28T22:00:00Z",
			UsageEndTime:        "2026-03-01T00:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)

	_, err := db.ExecContext(ctx, `UPDATE bill_line_items SET usage_end_time = ? WHERE id = ?`, "2026-03-01T00:00:01Z", item.ID)
	if err == nil {
		t.Fatal("UPDATE bill_line_items(cross-period usage) error = nil, want trigger rejection")
	}
	if !strings.Contains(err.Error(), "crosses billing period") {
		t.Fatalf("UPDATE bill_line_items(cross-period usage) error = %q, want cross-period trigger message", err.Error())
	}
}

func TestBillLineItemRepositoryPricesBoundaryScenarios(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tests := []struct {
		name     string
		resource ResourceCreateRequest
		usage    UsageEventCreateRequest
		want     billLineItemBoundaryWant
	}{
		{
			name: "stopped instance charges recorded hours through stop time",
			resource: ResourceCreateRequest{
				ID:           "resource-boundary-ec2-stopped",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  serviceAmazonEC2,
				ResourceType: "ec2_instance",
				Status:       "stopped",
				StartedAt:    "2026-03-31T18:00:00Z",
				StoppedAt:    "2026-04-01T00:00:00Z",
			},
			usage: UsageEventCreateRequest{
				ID:                  "usage-boundary-ec2-stopped",
				ResourceID:          "resource-boundary-ec2-stopped",
				UsageType:           "instance-hours:t3.medium",
				Operation:           "RunInstances",
				UsageStartTime:      "2026-03-31T18:00:00Z",
				UsageEndTime:        "2026-04-01T00:00:00Z",
				UsageQuantityMicros: 6_000_000,
				UsageUnit:           "Hours",
			},
			want: billLineItemBoundaryWant{
				BillingPeriodStart:    "2026-03-01",
				BillingPeriodEnd:      "2026-04-01",
				BillingPeriodDays:     31,
				UsageStartTime:        "2026-03-31T18:00:00Z",
				UsageEndTime:          "2026-04-01T00:00:00Z",
				UsageQuantityMicros:   6_000_000,
				UsageUnit:             "Hours",
				PricingUnit:           "InstanceHour",
				PricingQuantityMicros: 6_000_000,
				UnblendedRateMicros:   41_600,
				UnblendedCostMicros:   249_600,
				PriceCatalogSKU:       "SIM-EC2-T3-MEDIUM-HR",
			},
		},
		{
			name: "daily storage proration uses leap year February days and rounds cost",
			resource: ResourceCreateRequest{
				ID:           "resource-boundary-ebs-leap",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  serviceAmazonEBS,
				ResourceType: "ebs_volume",
				Status:       "active",
			},
			usage: UsageEventCreateRequest{
				ID:                  "usage-boundary-ebs-leap",
				ResourceID:          "resource-boundary-ebs-leap",
				UsageType:           "storage:gp3-gb-month",
				Operation:           "VolumeStorage",
				UsageStartTime:      "2028-02-29T00:00:00Z",
				UsageEndTime:        "2028-03-01T00:00:00Z",
				UsageQuantityMicros: 100_000_000,
				UsageUnit:           "GBDay",
			},
			want: billLineItemBoundaryWant{
				BillingPeriodStart:    "2028-02-01",
				BillingPeriodEnd:      "2028-03-01",
				BillingPeriodDays:     29,
				UsageStartTime:        "2028-02-29T00:00:00Z",
				UsageEndTime:          "2028-03-01T00:00:00Z",
				UsageQuantityMicros:   100_000_000,
				UsageUnit:             "GBDay",
				PricingUnit:           "GBMonth",
				PricingQuantityMicros: 3_448_276,
				UnblendedRateMicros:   80_000,
				UnblendedCostMicros:   275_862,
				PriceCatalogSKU:       "SIM-EBS-GP3-GBMO",
			},
		},
		{
			name: "daily storage proration uses 31 day month at boundary",
			resource: ResourceCreateRequest{
				ID:           "resource-boundary-s3-march",
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  serviceAmazonS3,
				ResourceType: "s3_bucket",
				Status:       "active",
			},
			usage: UsageEventCreateRequest{
				ID:                  "usage-boundary-s3-march",
				ResourceID:          "resource-boundary-s3-march",
				UsageType:           "storage:standard-gb-month",
				Operation:           "StandardStorage",
				UsageStartTime:      "2026-03-31T00:00:00Z",
				UsageEndTime:        "2026-04-01T00:00:00Z",
				UsageQuantityMicros: 100_000_000,
				UsageUnit:           "GBDay",
			},
			want: billLineItemBoundaryWant{
				BillingPeriodStart:    "2026-03-01",
				BillingPeriodEnd:      "2026-04-01",
				BillingPeriodDays:     31,
				UsageStartTime:        "2026-03-31T00:00:00Z",
				UsageEndTime:          "2026-04-01T00:00:00Z",
				UsageQuantityMicros:   100_000_000,
				UsageUnit:             "GBDay",
				PricingUnit:           "GBMonth",
				PricingQuantityMicros: 3_225_806,
				UnblendedRateMicros:   23_000,
				UnblendedCostMicros:   74_194,
				PriceCatalogSKU:       "SIM-S3-STANDARD-GBMO",
			},
		},
		{
			name: "monthly subscription starting before month boundary stays in start period",
			resource: ResourceCreateRequest{
				ID:           "resource-boundary-marketplace",
				AccountID:    "111122223333",
				RegionCode:   "global",
				ServiceCode:  "AWSMarketplace",
				ResourceType: "marketplace_subscription",
				Status:       "active",
			},
			usage: UsageEventCreateRequest{
				ID:                  "usage-boundary-marketplace",
				ResourceID:          "resource-boundary-marketplace",
				UsageType:           "marketplace-security-tool-month",
				Operation:           "MonthlySubscription",
				UsageStartTime:      "2026-04-30T23:00:00Z",
				UsageEndTime:        "2026-05-01T00:00:00Z",
				UsageQuantityMicros: 1_000_000,
				UsageUnit:           "Months",
			},
			want: billLineItemBoundaryWant{
				BillingPeriodStart:    "2026-04-01",
				BillingPeriodEnd:      "2026-05-01",
				BillingPeriodDays:     30,
				UsageStartTime:        "2026-04-30T23:00:00Z",
				UsageEndTime:          "2026-05-01T00:00:00Z",
				UsageQuantityMicros:   1_000_000,
				UsageUnit:             "Months",
				PricingUnit:           "SubscriptionMonth",
				PricingQuantityMicros: 1_000_000,
				UnblendedRateMicros:   250_000_000,
				UnblendedCostMicros:   250_000_000,
				PriceCatalogSKU:       "SIM-MARKETPLACE-SECURITY-MONTH",
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			db := openTestWorkspace(t)
			item := recordAndPriceSingleUsage(t, ctx, db, tt.resource, tt.usage)

			assertBillLineItemBoundary(t, item, tt.want)
		})
	}
}

func TestBillLineItemRepositorySurfacesMissingCatalogRate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	meteringRepo := NewMeteringRepository(db)
	lineItemRepo := NewBillLineItemRepository(db)

	resource, err := usageRepo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-bill-line-missing-rate",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  serviceAmazonS3,
		ResourceType: "s3_bucket",
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-bill-line-missing-rate",
		ResourceID:          resource.ID,
		UsageType:           "requests:delete-1k",
		Operation:           "DeleteObject",
		UsageStartTime:      "2026-02-02T00:00:00Z",
		UsageEndTime:        "2026-02-03T00:00:00Z",
		UsageQuantityMicros: int64(1_000_000),
		UsageUnit:           "Request",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	metering, err := meteringRepo.GenerateMeteringRecords(ctx)
	if err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}

	_, err = lineItemRepo.GenerateBillLineItems(ctx, BillLineItemGenerationRequest{})
	if !errors.Is(err, ErrPriceCatalogRateNotFound) {
		t.Fatalf("GenerateBillLineItems() error = %v, want ErrPriceCatalogRateNotFound", err)
	}
	if !strings.Contains(err.Error(), metering.Records[0].ID) {
		t.Fatalf("GenerateBillLineItems() error = %q, want metering record context", err.Error())
	}
	items, listErr := lineItemRepo.ListBillLineItems(ctx, 10)
	if listErr != nil {
		t.Fatalf("ListBillLineItems() error = %v", listErr)
	}
	if len(items) != 0 {
		t.Fatalf("bill line items after failed pricing = %+v, want none", items)
	}
}

func TestBillLineItemRepositoryRejectsNilDB(t *testing.T) {
	t.Parallel()

	repo := NewBillLineItemRepository(nil)
	if _, err := repo.ListBillLineItems(context.Background(), 10); err == nil {
		t.Fatal("ListBillLineItems() error = nil, want database handle validation error")
	}
	if _, err := repo.GenerateBillLineItems(context.Background(), BillLineItemGenerationRequest{}); err == nil {
		t.Fatal("GenerateBillLineItems() error = nil, want database handle validation error")
	}
}

type billLineItemBoundaryWant struct {
	BillingPeriodStart    string
	BillingPeriodEnd      string
	BillingPeriodDays     int
	UsageStartTime        string
	UsageEndTime          string
	UsageQuantityMicros   int64
	UsageUnit             string
	PricingUnit           string
	PricingQuantityMicros int64
	UnblendedRateMicros   int64
	UnblendedCostMicros   int64
	PriceCatalogSKU       string
}

// recordAndPriceSingleUsage exercises the usage -> metering -> bill line item path for one test case.
func recordAndPriceSingleUsage(t *testing.T, ctx context.Context, db *sql.DB, resourceRequest ResourceCreateRequest, usageRequest UsageEventCreateRequest) BillLineItem {
	t.Helper()

	usageRepo := NewResourceUsageRepository(db)
	meteringRepo := NewMeteringRepository(db)
	lineItemRepo := NewBillLineItemRepository(db)

	resource, err := usageRepo.CreateResource(ctx, resourceRequest)
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	event, err := usageRepo.RecordUsageEvent(ctx, usageRequest)
	if err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	metering, err := meteringRepo.GenerateMeteringRecords(ctx)
	if err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}
	result, err := lineItemRepo.GenerateBillLineItems(ctx, BillLineItemGenerationRequest{})
	if err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}
	if result.ItemsCreated != 1 || len(result.Items) != 1 {
		t.Fatalf("GenerateBillLineItems() = %+v, want one created line item", result)
	}
	item := result.Items[0]
	if item.ResourceID != resource.ID || item.UsageEventID != event.ID || item.MeteringRecordID != metering.Records[0].ID {
		t.Fatalf("line item lineage = %+v, want resource %q usage event %q metering %q", item, resource.ID, event.ID, metering.Records[0].ID)
	}
	return item
}

// assertBillLineItemBoundary verifies the fields that define billing boundary math.
func assertBillLineItemBoundary(t *testing.T, item BillLineItem, want billLineItemBoundaryWant) {
	t.Helper()

	if item.BillingPeriodStart != want.BillingPeriodStart || item.BillingPeriodEnd != want.BillingPeriodEnd || item.BillingPeriodDays != want.BillingPeriodDays {
		t.Fatalf("billing period = %s/%s days %d, want %s/%s days %d", item.BillingPeriodStart, item.BillingPeriodEnd, item.BillingPeriodDays, want.BillingPeriodStart, want.BillingPeriodEnd, want.BillingPeriodDays)
	}
	if item.UsageStartTime != want.UsageStartTime || item.UsageEndTime != want.UsageEndTime {
		t.Fatalf("usage window = %s/%s, want %s/%s", item.UsageStartTime, item.UsageEndTime, want.UsageStartTime, want.UsageEndTime)
	}
	if item.UsageQuantityMicros != want.UsageQuantityMicros || item.UsageUnit != want.UsageUnit {
		t.Fatalf("usage quantity = %d %s, want %d %s", item.UsageQuantityMicros, item.UsageUnit, want.UsageQuantityMicros, want.UsageUnit)
	}
	if item.PricingUnit != want.PricingUnit || item.PricingQuantityMicros != want.PricingQuantityMicros {
		t.Fatalf("pricing quantity = %d %s, want %d %s", item.PricingQuantityMicros, item.PricingUnit, want.PricingQuantityMicros, want.PricingUnit)
	}
	if item.UnblendedRateMicros != want.UnblendedRateMicros || item.UnblendedCostMicros != want.UnblendedCostMicros {
		t.Fatalf("pricing math = rate %d cost %d, want rate %d cost %d", item.UnblendedRateMicros, item.UnblendedCostMicros, want.UnblendedRateMicros, want.UnblendedCostMicros)
	}
	if item.PriceCatalogSKU != want.PriceCatalogSKU || item.PriceEffectiveDate != "2026-01-01" {
		t.Fatalf("price lineage = %q/%q, want SKU %q effective 2026-01-01", item.PriceCatalogSKU, item.PriceEffectiveDate, want.PriceCatalogSKU)
	}
}
