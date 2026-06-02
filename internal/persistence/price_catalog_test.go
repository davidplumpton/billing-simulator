package persistence

import (
	"context"
	"testing"
)

func TestPriceCatalogSeededForMVPServices(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewPriceCatalogRepository(db)

	items, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(items) != 18 {
		t.Fatalf("price catalog item count = %d, want 18", len(items))
	}

	requiredServices := map[string]bool{
		"Amazon EC2":        false,
		"Amazon EBS":        false,
		"Amazon S3":         false,
		"AWS Lambda":        false,
		"Amazon RDS":        false,
		"NAT Gateway":       false,
		"CloudWatch Logs":   false,
		"AWS Data Transfer": false,
		"AWS Support":       false,
		"AWS Marketplace":   false,
	}
	seenSKUs := map[string]bool{}
	for _, item := range items {
		requiredServices[item.ServiceName] = true
		if item.SKU == "" {
			t.Fatal("price catalog item has blank SKU")
		}
		if seenSKUs[item.SKU] {
			t.Fatalf("duplicate SKU %q", item.SKU)
		}
		seenSKUs[item.SKU] = true
		if item.ServiceCode == "" || item.ProductFamily == "" || item.UsageType == "" || item.Operation == "" {
			t.Fatalf("price catalog item %q has missing service metadata: %+v", item.SKU, item)
		}
		if item.RegionCode == "" || item.Unit == "" || item.RateMicros <= 0 {
			t.Fatalf("price catalog item %q has missing price metadata: %+v", item.SKU, item)
		}
		if item.CurrencyCode != "USD" {
			t.Fatalf("price catalog item %q currency = %q, want USD", item.SKU, item.CurrencyCode)
		}
		if item.EffectiveDate != "2026-01-01" || item.PriceSource != "synthetic" {
			t.Fatalf("price catalog item %q source/date = %q/%q, want synthetic 2026-01-01", item.SKU, item.PriceSource, item.EffectiveDate)
		}
		if item.PricingFormula == "" || item.Notes == "" {
			t.Fatalf("price catalog item %q has missing formula or notes: %+v", item.SKU, item)
		}
	}

	for serviceName, seen := range requiredServices {
		if !seen {
			t.Fatalf("required service %q was not seeded in the price catalog", serviceName)
		}
	}
}

func TestPriceCatalogRepositoryRejectsNilDB(t *testing.T) {
	t.Parallel()

	repo := NewPriceCatalogRepository(nil)
	if _, err := repo.List(context.Background()); err == nil {
		t.Fatal("List() error = nil, want database handle validation error")
	}
}
