package persistence

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPriceCatalogSeededForMVPServices(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewPriceCatalogRepository(db)

	if err := repo.Validate(ctx); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

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

func TestPriceCatalogValidateRejectsWorkspaceInconsistency(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewPriceCatalogRepository(db)

	_, err := db.ExecContext(ctx, `INSERT INTO price_catalog_items (
		sku,
		service_code,
		service_name,
		product_family,
		usage_type,
		operation,
		region_code,
		unit,
		rate_micros,
		currency_code,
		effective_date,
		price_source,
		pricing_formula,
		notes
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"SIM-BAD-CATALOG",
		"AmazonS3",
		"Amazon S3",
		"Object Storage",
		"storage:bad-gb-month",
		"StandardStorage",
		"eu-west-1",
		"",
		int64(1000),
		"EUR",
		"2026-01-01",
		"synthetic",
		"mystery_quantity * rate",
		"Invalid row used to verify catalog validation.",
	)
	if err != nil {
		t.Fatalf("insert invalid catalog row: %v", err)
	}

	err = repo.Validate(ctx)
	if err == nil {
		t.Fatal("Validate() error = nil, want catalog validation error")
	}
	for _, want := range []string{
		`SKU "SIM-BAD-CATALOG" uses unsupported region "eu-west-1"`,
		`SKU "SIM-BAD-CATALOG" unit is required`,
		`SKU "SIM-BAD-CATALOG" currency = "EUR", want USD`,
		`SKU "SIM-BAD-CATALOG" uses unsupported pricing formula "mystery_quantity * rate"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Validate() error = %q, want to contain %q", err.Error(), want)
		}
	}
}

func TestValidatePriceCatalogItemsRejectsDuplicateSKUVersion(t *testing.T) {
	t.Parallel()

	item := validPriceCatalogTestItem()
	err := validatePriceCatalogItems([]PriceCatalogItem{item, item})
	if err == nil {
		t.Fatal("validatePriceCatalogItems() error = nil, want duplicate SKU error")
	}
	if want := `duplicate SKU "SIM-TEST-CATALOG" at effective_date "2026-01-01"`; !strings.Contains(err.Error(), want) {
		t.Fatalf("validatePriceCatalogItems() error = %q, want to contain %q", err.Error(), want)
	}
}

func TestValidatePriceCatalogItemsRejectsFormulaUnitMismatch(t *testing.T) {
	t.Parallel()

	item := validPriceCatalogTestItem()
	item.SKU = "SIM-BAD-FORMULA-UNIT"
	item.Unit = "GB"
	err := validatePriceCatalogItems([]PriceCatalogItem{item})
	if err == nil {
		t.Fatal("validatePriceCatalogItems() error = nil, want formula coverage error")
	}
	if want := `pricing formula "usage_quantity * rate" does not cover unit "GB"`; !strings.Contains(err.Error(), want) {
		t.Fatalf("validatePriceCatalogItems() error = %q, want to contain %q", err.Error(), want)
	}
}

func TestPriceCatalogLookupMapsMeteringRecordToCatalogItem(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewPriceCatalogRepository(db)

	result, err := repo.Lookup(ctx, PriceLookupRequest{
		ServiceCode:         "AmazonS3",
		UsageType:           "requests:put-1k",
		Operation:           "PutObject",
		RegionCode:          "us-east-1",
		UsageUnit:           "Requests",
		UsageQuantityMicros: 1500 * priceQuantityMicros,
		UsageDate:           "2026-02-15",
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}

	if result.Item.SKU != "SIM-S3-PUT-1K" {
		t.Fatalf("Lookup() SKU = %q, want SIM-S3-PUT-1K", result.Item.SKU)
	}
	if result.Item.ServiceName != "Amazon S3" || result.Item.Unit != "ThousandRequests" || result.Item.CurrencyCode != "USD" {
		t.Fatalf("Lookup() item metadata = %+v, want S3 ThousandRequests USD", result.Item)
	}
	if result.UsageQuantityMicros != 1_500_000 {
		t.Fatalf("Lookup() normalized quantity = %d, want 1500000", result.UsageQuantityMicros)
	}
	if result.CostMicros != 7_500 {
		t.Fatalf("Lookup() cost micros = %d, want 7500", result.CostMicros)
	}
}

func validPriceCatalogTestItem() PriceCatalogItem {
	return PriceCatalogItem{
		SKU:            "SIM-TEST-CATALOG",
		ServiceCode:    "AmazonEC2",
		ServiceName:    "Amazon EC2",
		ProductFamily:  "Compute Instance",
		UsageType:      "instance-hours:t3.medium",
		Operation:      "RunInstances",
		RegionCode:     "us-east-1",
		Unit:           "InstanceHour",
		RateMicros:     41600,
		CurrencyCode:   "USD",
		EffectiveDate:  "2026-01-01",
		PriceSource:    "synthetic",
		PricingFormula: "usage_quantity * rate",
		Notes:          "Synthetic row used to verify catalog validation.",
	}
}

func TestPriceCatalogLookupFallsBackToGlobalRegion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewPriceCatalogRepository(db)

	result, err := repo.Lookup(ctx, PriceLookupRequest{
		ServiceCode:         "AWSDataTransfer",
		UsageType:           "data-transfer-out-internet-gb",
		Operation:           "DataTransferOut",
		RegionCode:          "us-east-1",
		UsageUnit:           "GB",
		UsageQuantityMicros: 2 * priceQuantityMicros,
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}

	if result.Item.SKU != "SIM-DATAXFER-INTERNET-GB" || result.Item.RegionCode != "global" {
		t.Fatalf("Lookup() item = %+v, want global data transfer SKU", result.Item)
	}
	if result.CostMicros != 180_000 {
		t.Fatalf("Lookup() cost micros = %d, want 180000", result.CostMicros)
	}
}

func TestPriceCatalogLookupConvertsGBDaysToGBMonths(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewPriceCatalogRepository(db)

	result, err := repo.Lookup(ctx, PriceLookupRequest{
		ServiceCode:         "AmazonEBS",
		UsageType:           "storage:gp3-gb-month",
		Operation:           "VolumeStorage",
		RegionCode:          "us-east-1",
		UsageUnit:           "GBDays",
		UsageQuantityMicros: 3000 * priceQuantityMicros,
		BillingPeriodDays:   30,
	})
	if err != nil {
		t.Fatalf("Lookup() error = %v", err)
	}

	if result.Item.SKU != "SIM-EBS-GP3-GBMO" || result.Item.Unit != "GBMonth" {
		t.Fatalf("Lookup() item = %+v, want EBS GBMonth SKU", result.Item)
	}
	if result.UsageQuantityMicros != 100*priceQuantityMicros {
		t.Fatalf("Lookup() normalized quantity = %d, want %d", result.UsageQuantityMicros, 100*priceQuantityMicros)
	}
	if result.CostMicros != 8_000_000 {
		t.Fatalf("Lookup() cost micros = %d, want 8000000", result.CostMicros)
	}
}

func TestPriceCatalogLookupReturnsClearMissingRateError(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewPriceCatalogRepository(db)

	_, err := repo.Lookup(ctx, PriceLookupRequest{
		ServiceCode:         "AmazonS3",
		UsageType:           "requests:delete-1k",
		Operation:           "DeleteObject",
		RegionCode:          "us-east-1",
		UsageUnit:           "Requests",
		UsageQuantityMicros: 25 * priceQuantityMicros,
		UsageDate:           "2026-02-15",
	})
	if !errors.Is(err, ErrPriceCatalogRateNotFound) {
		t.Fatalf("Lookup() error = %v, want ErrPriceCatalogRateNotFound", err)
	}
	for _, want := range []string{`service_code="AmazonS3"`, `usage_type="requests:delete-1k"`, `operation="DeleteObject"`, `region_code="us-east-1"`, "effective_date=<= 2026-02-15"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Lookup() error = %q, want to contain %q", err.Error(), want)
		}
	}
}

func TestPriceCatalogLookupRejectsUnsupportedUnitConversion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewPriceCatalogRepository(db)

	_, err := repo.Lookup(ctx, PriceLookupRequest{
		ServiceCode:         "AWSLambda",
		UsageType:           "requests:lambda-1m",
		Operation:           "Invoke",
		RegionCode:          "us-east-1",
		UsageUnit:           "GB",
		UsageQuantityMicros: 2 * priceQuantityMicros,
	})
	if !errors.Is(err, ErrUnsupportedUnitConversion) {
		t.Fatalf("Lookup() error = %v, want ErrUnsupportedUnitConversion", err)
	}
	if !strings.Contains(err.Error(), `SKU "SIM-LAMBDA-REQUESTS-1M"`) {
		t.Fatalf("Lookup() error = %q, want SKU context", err.Error())
	}
}

func TestPriceCatalogRepositoryRejectsNilDB(t *testing.T) {
	t.Parallel()

	repo := NewPriceCatalogRepository(nil)
	if _, err := repo.List(context.Background()); err == nil {
		t.Fatal("List() error = nil, want database handle validation error")
	}
	if _, err := repo.Lookup(context.Background(), PriceLookupRequest{
		ServiceCode:         "AmazonS3",
		UsageType:           "requests:put-1k",
		Operation:           "PutObject",
		RegionCode:          "us-east-1",
		UsageUnit:           "Requests",
		UsageQuantityMicros: priceQuantityMicros,
	}); err == nil {
		t.Fatal("Lookup() error = nil, want database handle validation error")
	}
}
