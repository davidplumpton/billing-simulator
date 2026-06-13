package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestReservedInstanceRepositoryAppliesFeesCoverageAndSharing(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	riRepo := NewReservedInstanceRepository(db)

	purchase, err := riRepo.CreatePurchase(ctx, ReservedInstancePurchaseCreateRequest{
		ID:                        "ri-shared-t3-medium",
		PayerAccountID:            AnyCompanyRetailManagementAccountID,
		OwnerAccountID:            "111122223333",
		UsageType:                 "instance-hours:t3.medium",
		RegionCode:                "us-east-1",
		InstanceCount:             1,
		SharingScope:              reservedInstanceSharingOrg,
		TermStartTime:             "2026-02-01T00:00:00Z",
		TermEndTime:               "2026-02-01T03:00:00Z",
		UpfrontFeeMicros:          1_000_000,
		MonthlyRecurringFeeMicros: 200_000,
		Description:               "Shared t3.medium training RI",
	})
	if err != nil {
		t.Fatalf("CreatePurchase() error = %v", err)
	}
	if purchase.PriceCatalogSKU != "SIM-EC2-T3-MEDIUM-HR" || purchase.PriceEffectiveDate != "2026-01-01" {
		t.Fatalf("purchase price lineage = %+v, want EC2 synthetic SKU", purchase)
	}

	recordReservedInstanceTestUsage(t, ctx, usageRepo, "resource-ri-owner", "usage-ri-owner", "111122223333", "2026-02-01T00:00:00Z", "2026-02-01T02:00:00Z", 2_000_000)
	recordReservedInstanceTestUsage(t, ctx, usageRepo, "resource-ri-shared", "usage-ri-shared", "444455556666", "2026-02-01T00:00:00Z", "2026-02-01T02:00:00Z", 2_000_000)
	if _, err := NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}

	result, err := NewBillLineItemRepository(db).GenerateBillLineItems(ctx, BillLineItemGenerationRequest{})
	if err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}
	if result.ItemsCreated != 6 {
		t.Fatalf("GenerateBillLineItems() created %d, want two usage, two fees, and two coverage credits", result.ItemsCreated)
	}

	items, err := NewBillLineItemRepository(db).ListBillLineItems(ctx, 20)
	if err != nil {
		t.Fatalf("ListBillLineItems() error = %v", err)
	}
	requireReservedInstanceTestLineItem(t, items, billLineItemTypeFee, "111122223333", "upfront charge", 1_000_000, priceQuantityMicros)
	requireReservedInstanceTestLineItem(t, items, billLineItemTypeFee, "111122223333", "recurring charge", 200_000, priceQuantityMicros)
	requireReservedInstanceTestLineItem(t, items, "Credit", "111122223333", "coverage credit", 83_200, 2_000_000)
	requireReservedInstanceTestLineItem(t, items, "Credit", "444455556666", "coverage credit", 41_600, 1_000_000)

	sources, err := riRepo.ListLineItemSources(ctx, purchase.ID)
	if err != nil {
		t.Fatalf("ListLineItemSources() error = %v", err)
	}
	if len(sources) != 4 {
		t.Fatalf("RI line item sources = %+v, want upfront, recurring, and two coverage rows", sources)
	}
	var coveredQuantity, coveredCost int64
	var coverageRows, feeRows int
	for _, source := range sources {
		switch source.LineItemKind {
		case reservedInstanceKindCoverageCredit:
			coverageRows++
			coveredQuantity += source.CoveredQuantityMicros
			coveredCost += source.CoveredCostMicros
			if source.SourceBillLineItemID == "" {
				t.Fatalf("coverage source = %+v, want source bill line item", source)
			}
		case reservedInstanceKindUpfrontFee, reservedInstanceKindRecurringFee:
			feeRows++
			if source.SourceBillLineItemID != "" || source.CoveredQuantityMicros != 0 || source.CoveredCostMicros != 0 {
				t.Fatalf("fee source = %+v, want fee metadata without source usage", source)
			}
		}
	}
	if coverageRows != 2 || feeRows != 2 || coveredQuantity != 3_000_000 || coveredCost != 124_800 {
		t.Fatalf("RI source totals = coverage rows %d fee rows %d quantity %d cost %d, want 2/2/3000000/124800", coverageRows, feeRows, coveredQuantity, coveredCost)
	}

	second, err := NewBillLineItemRepository(db).GenerateBillLineItems(ctx, BillLineItemGenerationRequest{})
	if err != nil {
		t.Fatalf("second GenerateBillLineItems() error = %v", err)
	}
	if second.ItemsCreated != 0 {
		t.Fatalf("second GenerateBillLineItems() created %d, want idempotent RI generation", second.ItemsCreated)
	}
}

func TestReservedInstanceRepositoryHonorsOwnerAccountSharingScope(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	riRepo := NewReservedInstanceRepository(db)

	if _, err := riRepo.CreatePurchase(ctx, ReservedInstancePurchaseCreateRequest{
		ID:                        "ri-owner-only-t3-medium",
		PayerAccountID:            AnyCompanyRetailManagementAccountID,
		OwnerAccountID:            "111122223333",
		UsageType:                 "instance-hours:t3.medium",
		RegionCode:                "us-east-1",
		InstanceCount:             1,
		SharingScope:              reservedInstanceSharingOwner,
		TermStartTime:             "2026-02-02T00:00:00Z",
		TermEndTime:               "2026-02-02T03:00:00Z",
		MonthlyRecurringFeeMicros: 200_000,
	}); err != nil {
		t.Fatalf("CreatePurchase() error = %v", err)
	}
	recordReservedInstanceTestUsage(t, ctx, usageRepo, "resource-ri-owner-only", "usage-ri-owner-only", "111122223333", "2026-02-02T00:00:00Z", "2026-02-02T01:00:00Z", 1_000_000)
	recordReservedInstanceTestUsage(t, ctx, usageRepo, "resource-ri-non-owner", "usage-ri-non-owner", "444455556666", "2026-02-02T00:00:00Z", "2026-02-02T01:00:00Z", 1_000_000)
	if _, err := NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}
	if _, err := NewBillLineItemRepository(db).GenerateBillLineItems(ctx, BillLineItemGenerationRequest{}); err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}

	items, err := NewBillLineItemRepository(db).ListBillLineItems(ctx, 20)
	if err != nil {
		t.Fatalf("ListBillLineItems() error = %v", err)
	}
	requireReservedInstanceTestLineItem(t, items, "Credit", "111122223333", "coverage credit", 41_600, 1_000_000)
	if item := reservedInstanceTestLineItem(items, "Credit", "444455556666", "coverage credit"); item != nil {
		t.Fatalf("non-owner account received RI owner-only credit: %+v", *item)
	}
}

func recordReservedInstanceTestUsage(t *testing.T, ctx context.Context, repo ResourceUsageRepository, resourceID, usageID, accountID, start, end string, quantityMicros int64) {
	t.Helper()
	resource, err := repo.CreateResource(ctx, ResourceCreateRequest{
		ID:           resourceID,
		AccountID:    accountID,
		RegionCode:   "us-east-1",
		ServiceCode:  serviceAmazonEC2,
		ResourceType: "ec2_instance",
		ResourceName: resourceID,
		Status:       "active",
		StartedAt:    start,
	})
	if err != nil {
		t.Fatalf("CreateResource(%s) error = %v", resourceID, err)
	}
	if _, err := repo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  usageID,
		ResourceID:          resource.ID,
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      start,
		UsageEndTime:        end,
		UsageQuantityMicros: quantityMicros,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(%s) error = %v", usageID, err)
	}
}

func requireReservedInstanceTestLineItem(t *testing.T, items []BillLineItem, lineItemType, usageAccountID, descriptionContains string, costMicros, quantityMicros int64) BillLineItem {
	t.Helper()
	item := reservedInstanceTestLineItem(items, lineItemType, usageAccountID, descriptionContains)
	if item == nil {
		t.Fatalf("missing line item type=%s usage_account=%s description containing %q in %+v", lineItemType, usageAccountID, descriptionContains, items)
	}
	if item.UnblendedCostMicros != costMicros || item.PricingQuantityMicros != quantityMicros {
		t.Fatalf("line item %+v cost/quantity = %d/%d, want %d/%d", *item, item.UnblendedCostMicros, item.PricingQuantityMicros, costMicros, quantityMicros)
	}
	return *item
}

func reservedInstanceTestLineItem(items []BillLineItem, lineItemType, usageAccountID, descriptionContains string) *BillLineItem {
	for idx := range items {
		item := &items[idx]
		if item.LineItemType == lineItemType &&
			item.UsageAccountID == usageAccountID &&
			strings.Contains(item.Description, descriptionContains) {
			return item
		}
	}
	return nil
}
