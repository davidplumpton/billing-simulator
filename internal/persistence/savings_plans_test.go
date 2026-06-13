package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestSavingsPlanRepositoryAppliesFeesNegationsAndAmortizedSources(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	spRepo := NewSavingsPlanRepository(db)

	purchase, err := spRepo.CreatePurchase(ctx, SavingsPlanPurchaseCreateRequest{
		ID:                     "sp-shared-compute",
		PayerAccountID:         AnyCompanyRetailManagementAccountID,
		OwnerAccountID:         "111122223333",
		ReferenceUsageType:     "instance-hours:t3.medium",
		RegionCode:             "us-east-1",
		SharingScope:           savingsPlanSharingOrg,
		TermStartTime:          "2026-02-01T00:00:00Z",
		TermEndTime:            "2026-02-01T03:00:00Z",
		HourlyCommitmentMicros: 100_000,
		UpfrontFeeMicros:       90_000,
		Description:            "Shared compute Savings Plan",
	})
	if err != nil {
		t.Fatalf("CreatePurchase() error = %v", err)
	}
	if purchase.PriceCatalogSKU != "SIM-EC2-T3-MEDIUM-HR" || purchase.PriceEffectiveDate != "2026-01-01" {
		t.Fatalf("purchase price lineage = %+v, want EC2 synthetic SKU", purchase)
	}

	recordSavingsPlanTestUsage(t, ctx, usageRepo, "resource-sp-owner", "usage-sp-owner", "111122223333", "2026-02-01T00:00:00Z", "2026-02-01T02:00:00Z", 2_000_000)
	recordSavingsPlanTestUsage(t, ctx, usageRepo, "resource-sp-shared", "usage-sp-shared", "444455556666", "2026-02-01T00:00:00Z", "2026-02-01T02:00:00Z", 2_000_000)
	if _, err := NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}

	result, err := NewBillLineItemRepository(db).GenerateBillLineItems(ctx, BillLineItemGenerationRequest{})
	if err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}
	if result.ItemsCreated != 6 {
		t.Fatalf("GenerateBillLineItems() created %d, want two usage, two fees, and two negations", result.ItemsCreated)
	}

	items, err := NewBillLineItemRepository(db).ListBillLineItems(ctx, 20)
	if err != nil {
		t.Fatalf("ListBillLineItems() error = %v", err)
	}
	requireSavingsPlanTestLineItem(t, items, billLineItemTypeFee, "111122223333", "upfront commitment charge", 90_000, priceQuantityMicros)
	requireSavingsPlanTestLineItem(t, items, billLineItemTypeFee, "111122223333", "recurring commitment charge", 300_000, priceQuantityMicros)
	requireSavingsPlanTestLineItem(t, items, "Credit", "111122223333", "negation", 83_200, 2_000_000)
	requireSavingsPlanTestLineItem(t, items, "Credit", "444455556666", "negation", 83_200, 2_000_000)

	sources, err := spRepo.ListLineItemSources(ctx, purchase.ID)
	if err != nil {
		t.Fatalf("ListLineItemSources() error = %v", err)
	}
	if len(sources) != 4 {
		t.Fatalf("Savings Plan line item sources = %+v, want upfront, recurring, and two negations", sources)
	}
	var coveredQuantity, coveredCost, amortizedCost int64
	var negationRows, feeRows int
	for _, source := range sources {
		switch source.LineItemKind {
		case savingsPlanKindNegation:
			negationRows++
			coveredQuantity += source.CoveredQuantityMicros
			coveredCost += source.CoveredCostMicros
			amortizedCost += source.AmortizedCommitmentCostMicros
			if source.SourceBillLineItemID == "" {
				t.Fatalf("negation source = %+v, want source bill line item", source)
			}
		case savingsPlanKindUpfrontFee, savingsPlanKindRecurringFee:
			feeRows++
			if source.SourceBillLineItemID != "" || source.CoveredQuantityMicros != 0 || source.CoveredCostMicros != 0 || source.AmortizedCommitmentCostMicros != 0 {
				t.Fatalf("fee source = %+v, want fee metadata without source usage", source)
			}
		}
	}
	if negationRows != 2 || feeRows != 2 || coveredQuantity != 4_000_000 || coveredCost != 166_400 || amortizedCost != 216_320 {
		t.Fatalf("Savings Plan source totals = negations %d fees %d quantity %d covered %d amortized %d, want 2/2/4000000/166400/216320", negationRows, feeRows, coveredQuantity, coveredCost, amortizedCost)
	}
	purchases, err := spRepo.ListPurchases(ctx)
	if err != nil {
		t.Fatalf("ListPurchases() error = %v", err)
	}
	if len(purchases) != 1 || purchases[0].ID != purchase.ID {
		t.Fatalf("ListPurchases() = %+v, want created purchase", purchases)
	}
	details, err := spRepo.ListLineItemSourceDetails(ctx, purchase.ID)
	if err != nil {
		t.Fatalf("ListLineItemSourceDetails() error = %v", err)
	}
	if len(details) != 4 {
		t.Fatalf("ListLineItemSourceDetails() = %+v, want four generated rows", details)
	}
	var detailedNegations int
	for _, detail := range details {
		if detail.GeneratedDescription == "" || detail.GeneratedCostMicros == 0 {
			t.Fatalf("source detail missing generated line item context: %+v", detail)
		}
		if detail.LineItemKind == savingsPlanKindNegation {
			detailedNegations++
			if detail.SourceBillLineItemID == "" || detail.SourceDescription == "" || detail.SourceCostMicros == 0 || detail.AmortizedCommitmentCostMicros == 0 {
				t.Fatalf("negation detail missing source/amortized context: %+v", detail)
			}
		}
	}
	if detailedNegations != 2 {
		t.Fatalf("ListLineItemSourceDetails() negation rows = %d, want 2", detailedNegations)
	}

	second, err := NewBillLineItemRepository(db).GenerateBillLineItems(ctx, BillLineItemGenerationRequest{})
	if err != nil {
		t.Fatalf("second GenerateBillLineItems() error = %v", err)
	}
	if second.ItemsCreated != 0 {
		t.Fatalf("second GenerateBillLineItems() created %d, want idempotent Savings Plan generation", second.ItemsCreated)
	}
}

func TestSavingsPlanRepositoryLeavesReservedInstanceCoveredUsageUnnegated(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	riRepo := NewReservedInstanceRepository(db)
	spRepo := NewSavingsPlanRepository(db)

	if _, err := riRepo.CreatePurchase(ctx, ReservedInstancePurchaseCreateRequest{
		ID:             "ri-before-sp",
		PayerAccountID: AnyCompanyRetailManagementAccountID,
		OwnerAccountID: "111122223333",
		UsageType:      "instance-hours:t3.medium",
		RegionCode:     "us-east-1",
		InstanceCount:  1,
		SharingScope:   reservedInstanceSharingOrg,
		TermStartTime:  "2026-02-02T00:00:00Z",
		TermEndTime:    "2026-02-02T01:00:00Z",
		Description:    "RI should consume owner usage first",
	}); err != nil {
		t.Fatalf("CreatePurchase(RI) error = %v", err)
	}
	purchase, err := spRepo.CreatePurchase(ctx, SavingsPlanPurchaseCreateRequest{
		ID:                     "sp-after-ri",
		PayerAccountID:         AnyCompanyRetailManagementAccountID,
		OwnerAccountID:         "111122223333",
		ReferenceUsageType:     "instance-hours:t3.medium",
		RegionCode:             "us-east-1",
		SharingScope:           savingsPlanSharingOrg,
		TermStartTime:          "2026-02-02T00:00:00Z",
		TermEndTime:            "2026-02-02T01:00:00Z",
		HourlyCommitmentMicros: 100_000,
	})
	if err != nil {
		t.Fatalf("CreatePurchase(Savings Plan) error = %v", err)
	}

	recordSavingsPlanTestUsage(t, ctx, usageRepo, "resource-sp-ri-owner", "usage-sp-ri-owner", "111122223333", "2026-02-02T00:00:00Z", "2026-02-02T01:00:00Z", 1_000_000)
	recordSavingsPlanTestUsage(t, ctx, usageRepo, "resource-sp-ri-shared", "usage-sp-ri-shared", "444455556666", "2026-02-02T00:00:00Z", "2026-02-02T01:00:00Z", 1_000_000)
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
	requireSavingsPlanTestLineItem(t, items, "Credit", "111122223333", "Reserved Instance", 41_600, 1_000_000)
	requireSavingsPlanTestLineItem(t, items, "Credit", "444455556666", "Savings Plan", 41_600, 1_000_000)
	if item := savingsPlanTestLineItem(items, "Credit", "111122223333", "Savings Plan"); item != nil {
		t.Fatalf("Savings Plan negated RI-covered owner usage: %+v", *item)
	}

	sources, err := spRepo.ListLineItemSources(ctx, purchase.ID)
	if err != nil {
		t.Fatalf("ListLineItemSources() error = %v", err)
	}
	var negationRows int
	var coveredCost int64
	for _, source := range sources {
		if source.LineItemKind == savingsPlanKindNegation {
			negationRows++
			coveredCost += source.CoveredCostMicros
		}
	}
	if negationRows != 1 || coveredCost != 41_600 {
		t.Fatalf("Savings Plan negation coverage = rows %d cost %d, want one shared-account row for 41600", negationRows, coveredCost)
	}
}

func TestSavingsPlanRepositoryRejectsUnsupportedPlanShapes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	spRepo := NewSavingsPlanRepository(db)

	_, err := spRepo.CreatePurchase(ctx, SavingsPlanPurchaseCreateRequest{
		ID:                     "sp-unsupported-operation",
		PayerAccountID:         AnyCompanyRetailManagementAccountID,
		OwnerAccountID:         "111122223333",
		ReferenceUsageType:     "compute:lambda-gb-second",
		Operation:              "Invoke",
		RegionCode:             "us-east-1",
		TermStartTime:          "2026-02-01T00:00:00Z",
		TermEndTime:            "2026-02-01T01:00:00Z",
		HourlyCommitmentMicros: 100_000,
	})
	if err == nil {
		t.Fatal("CreatePurchase() error = nil, want unsupported operation error")
	}
	if !strings.Contains(err.Error(), "only EC2 RunInstances hourly usage is supported") {
		t.Fatalf("CreatePurchase() error = %q, want supported-scope message", err.Error())
	}
}

func recordSavingsPlanTestUsage(t *testing.T, ctx context.Context, repo ResourceUsageRepository, resourceID, usageID, accountID, start, end string, quantityMicros int64) {
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

func requireSavingsPlanTestLineItem(t *testing.T, items []BillLineItem, lineItemType, usageAccountID, descriptionContains string, costMicros, quantityMicros int64) BillLineItem {
	t.Helper()
	item := savingsPlanTestLineItem(items, lineItemType, usageAccountID, descriptionContains)
	if item == nil {
		t.Fatalf("missing line item type=%s usage_account=%s description containing %q in %+v", lineItemType, usageAccountID, descriptionContains, items)
	}
	if item.UnblendedCostMicros != costMicros || item.PricingQuantityMicros != quantityMicros {
		t.Fatalf("line item %+v cost/quantity = %d/%d, want %d/%d", *item, item.UnblendedCostMicros, item.PricingQuantityMicros, costMicros, quantityMicros)
	}
	return *item
}

func savingsPlanTestLineItem(items []BillLineItem, lineItemType, usageAccountID, descriptionContains string) *BillLineItem {
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
