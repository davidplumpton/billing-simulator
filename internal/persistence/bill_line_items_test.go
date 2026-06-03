package persistence

import (
	"context"
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
