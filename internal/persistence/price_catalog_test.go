package persistence

import (
	"context"
	"database/sql"
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
	seedItems, err := SyntheticPriceCatalogSeedItems()
	if err != nil {
		t.Fatalf("SyntheticPriceCatalogSeedItems() error = %v", err)
	}
	assertPriceCatalogSeedManifestMatchesWorkspace(t, seedItems, items)

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

func assertPriceCatalogSeedManifestMatchesWorkspace(t *testing.T, seedItems, workspaceItems []PriceCatalogItem) {
	t.Helper()

	if len(seedItems) != len(workspaceItems) {
		t.Fatalf("packaged seed item count = %d, workspace item count = %d", len(seedItems), len(workspaceItems))
	}
	workspaceBySKU := make(map[string]PriceCatalogItem, len(workspaceItems))
	for _, item := range workspaceItems {
		workspaceBySKU[item.SKU] = item
	}
	for _, seed := range seedItems {
		item, ok := workspaceBySKU[seed.SKU]
		if !ok {
			t.Fatalf("packaged seed SKU %q is missing from migrated workspace catalog", seed.SKU)
		}
		if item.ServiceCode != seed.ServiceCode ||
			item.ServiceName != seed.ServiceName ||
			item.ProductFamily != seed.ProductFamily ||
			item.UsageType != seed.UsageType ||
			item.Operation != seed.Operation ||
			item.RegionCode != seed.RegionCode ||
			item.Unit != seed.Unit ||
			item.RateMicros != seed.RateMicros ||
			item.CurrencyCode != seed.CurrencyCode ||
			item.EffectiveDate != seed.EffectiveDate ||
			item.PriceSource != seed.PriceSource {
			t.Fatalf("workspace catalog item %q = %+v, want packaged seed %+v", seed.SKU, item, seed)
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

func TestValidatePriceCatalogItemsRejectsAmbiguousLookupIdentity(t *testing.T) {
	t.Parallel()

	item := validPriceCatalogTestItem()
	ambiguous := item
	ambiguous.SKU = "SIM-TEST-CATALOG-ALT"
	ambiguous.RateMicros = item.RateMicros + 1
	ambiguous.Notes = "Synthetic row that collides with another catalog lookup identity."

	err := validatePriceCatalogItems([]PriceCatalogItem{item, ambiguous})
	if err == nil {
		t.Fatal("validatePriceCatalogItems() error = nil, want ambiguous lookup identity error")
	}
	for _, want := range []string{
		"ambiguous lookup identity",
		`service_code="AmazonEC2"`,
		`usage_type="instance-hours:t3.medium"`,
		`operation="RunInstances"`,
		`region_code="us-east-1"`,
		`effective_date="2026-01-01"`,
		`SKUs "SIM-TEST-CATALOG" and "SIM-TEST-CATALOG-ALT"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("validatePriceCatalogItems() error = %q, want to contain %q", err.Error(), want)
		}
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

func TestValidatePriceCatalogItemsRejectsNonPositiveRate(t *testing.T) {
	t.Parallel()

	item := validPriceCatalogTestItem()
	item.SKU = "SIM-ZERO-RATE"
	item.RateMicros = 0

	err := validatePriceCatalogItems([]PriceCatalogItem{item})
	if err == nil {
		t.Fatal("validatePriceCatalogItems() error = nil, want rate validation error")
	}
	if want := `SKU "SIM-ZERO-RATE" rate_micros must be positive`; !strings.Contains(err.Error(), want) {
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

func TestPriceCatalogSupportsVersionedRatesForSameSKU(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewPriceCatalogRepository(db)

	laterRate := validPriceCatalogTestItem()
	laterRate.SKU = "SIM-S3-PUT-1K"
	laterRate.ServiceCode = "AmazonS3"
	laterRate.ServiceName = "Amazon S3"
	laterRate.ProductFamily = "API Request"
	laterRate.UsageType = "requests:put-1k"
	laterRate.Operation = "PutObject"
	laterRate.Unit = "ThousandRequests"
	laterRate.RateMicros = 7000
	laterRate.EffectiveDate = "2026-03-01"
	laterRate.PricingFormula = "(request_count / 1000) * rate"
	laterRate.Notes = "Synthetic S3 PUT rate update used to verify versioned catalog keys."
	insertPriceCatalogTestItem(t, db, laterRate)

	if err := repo.Validate(ctx); err != nil {
		t.Fatalf("Validate() after versioned insert error = %v", err)
	}

	historical, err := repo.Lookup(ctx, PriceLookupRequest{
		ServiceCode:         "AmazonS3",
		UsageType:           "requests:put-1k",
		Operation:           "PutObject",
		RegionCode:          "us-east-1",
		UsageUnit:           "Requests",
		UsageQuantityMicros: 1000 * priceQuantityMicros,
		UsageDate:           "2026-02-15",
	})
	if err != nil {
		t.Fatalf("Lookup() historical rate error = %v", err)
	}
	if historical.Item.EffectiveDate != "2026-01-01" || historical.Item.RateMicros != 5000 || historical.CostMicros != 5000 {
		t.Fatalf("Lookup() historical rate = %+v cost %d, want 2026-01-01 at 5000 micros", historical.Item, historical.CostMicros)
	}

	current, err := repo.Lookup(ctx, PriceLookupRequest{
		ServiceCode:         "AmazonS3",
		UsageType:           "requests:put-1k",
		Operation:           "PutObject",
		RegionCode:          "us-east-1",
		UsageUnit:           "Requests",
		UsageQuantityMicros: 1000 * priceQuantityMicros,
		UsageDate:           "2026-03-15",
	})
	if err != nil {
		t.Fatalf("Lookup() current rate error = %v", err)
	}
	if current.Item.EffectiveDate != "2026-03-01" || current.Item.RateMicros != 7000 || current.CostMicros != 7000 {
		t.Fatalf("Lookup() current rate = %+v cost %d, want 2026-03-01 at 7000 micros", current.Item, current.CostMicros)
	}

	if err := insertPriceCatalogTestItemRow(ctx, db, laterRate); err == nil {
		t.Fatal("duplicate same-SKU same-date insert error = nil, want primary-key violation")
	}
}

func TestPriceCatalogLookupIdentityUniqueIndexRejectsAmbiguousRates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)

	ambiguous := validPriceCatalogTestItem()
	ambiguous.SKU = "SIM-S3-PUT-ALT-1K"
	ambiguous.ServiceCode = "AmazonS3"
	ambiguous.ServiceName = "Amazon S3"
	ambiguous.ProductFamily = "API Request"
	ambiguous.UsageType = "requests:put-1k"
	ambiguous.Operation = "PutObject"
	ambiguous.RegionCode = "us-east-1"
	ambiguous.Unit = "ThousandRequests"
	ambiguous.RateMicros = 6000
	ambiguous.EffectiveDate = "2026-01-01"
	ambiguous.PricingFormula = "(request_count / 1000) * rate"
	ambiguous.Notes = "Synthetic S3 PUT alternate row used to verify lookup identity uniqueness."

	if err := insertPriceCatalogTestItemRow(ctx, db, ambiguous); err == nil {
		t.Fatal("ambiguous same-date lookup identity insert error = nil, want unique constraint violation")
	}
}

func TestPriceCatalogRateMicrosConstraintRejectsZeroRate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)

	zeroRate := validPriceCatalogTestItem()
	zeroRate.SKU = "SIM-ZERO-RATE"
	zeroRate.UsageType = "instance-hours:zero-rate-test"
	zeroRate.RateMicros = 0

	if err := insertPriceCatalogTestItemRow(ctx, db, zeroRate); err == nil {
		t.Fatal("zero-rate catalog insert error = nil, want check constraint violation")
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

// insertPriceCatalogTestItem adds one complete catalog row for tests that need custom rate versions.
func insertPriceCatalogTestItem(t *testing.T, db *sql.DB, item PriceCatalogItem) {
	t.Helper()

	if err := insertPriceCatalogTestItemRow(context.Background(), db, item); err != nil {
		t.Fatalf("insert price catalog item %q at %q: %v", item.SKU, item.EffectiveDate, err)
	}
}

// insertPriceCatalogTestItemRow keeps expected database constraint failures observable in tests.
func insertPriceCatalogTestItemRow(ctx context.Context, db *sql.DB, item PriceCatalogItem) error {
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
		item.SKU,
		item.ServiceCode,
		item.ServiceName,
		item.ProductFamily,
		item.UsageType,
		item.Operation,
		item.RegionCode,
		item.Unit,
		item.RateMicros,
		item.CurrencyCode,
		item.EffectiveDate,
		item.PriceSource,
		item.PricingFormula,
		item.Notes,
	)
	return err
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
