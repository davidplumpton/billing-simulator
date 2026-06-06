package persistence

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/csv"
	"slices"
	"testing"
)

func TestCURLineItemColumnsIncludeRequiredExportFields(t *testing.T) {
	t.Parallel()

	columns := CURLineItemColumns()
	required := []string{
		"payer_account_id",
		"usage_account_id",
		"product_code",
		"usage_type",
		"usage_amount",
		"unblended_rate",
		"unblended_cost",
		"line_item_type",
		"resource_id",
		"tags_json",
		"legal_entity",
	}
	for _, column := range required {
		if !slices.Contains(columns, column) {
			t.Fatalf("CURLineItemColumns() missing %q in %+v", column, columns)
		}
	}
	columns[0] = "mutated"
	if CURLineItemColumns()[0] == "mutated" {
		t.Fatal("CURLineItemColumns() returned mutable backing slice")
	}

	csvColumns := CURCSVExportColumns()
	for _, column := range []string{"export_generated_at", "source_bill_id", "line_item_id"} {
		if !slices.Contains(csvColumns, column) {
			t.Fatalf("CURCSVExportColumns() missing %q in %+v", column, csvColumns)
		}
	}
	csvColumns[0] = "mutated"
	if CURCSVExportColumns()[0] == "mutated" {
		t.Fatal("CURCSVExportColumns() returned mutable backing slice")
	}
}

func TestCURLineItemRepositoryMapsBillLineItemsToExportRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	createCURExportCostCategory(t, ctx, db)

	ec2Item := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-cur-ec2",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "CUR web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app":   "storefront",
				"owner": "finops",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-cur-ec2-hours",
			ResourceID:          "resource-cur-ec2",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)
	supportResult, err := NewSupportChargeRepository(db).GenerateSupportCharges(ctx, SupportChargeGenerationRequest{
		PayerAccountID: ec2Item.PayerAccountID,
		PeriodStart:    ec2Item.BillingPeriodStart,
		PeriodEnd:      ec2Item.BillingPeriodEnd,
		LineItemStatus: billLineItemStatusEstimated,
	})
	if err != nil {
		t.Fatalf("GenerateSupportCharges() error = %v", err)
	}
	if supportResult.ItemsCreated != 1 || len(supportResult.Items) != 1 {
		t.Fatalf("GenerateSupportCharges() = %+v, want one support fee", supportResult)
	}

	rows, err := NewCURLineItemRepository(db).ListLineItems(ctx, CURLineItemListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     AnyCompanyRetailManagementAccountID,
		Limit:              10,
	})
	if err != nil {
		t.Fatalf("ListLineItems() error = %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("ListLineItems() returned %d rows: %+v, want usage and support rows", len(rows), rows)
	}

	usage := requireCURLineItem(t, rows, ec2Item.ID)
	if usage.PayerAccountID != AnyCompanyRetailManagementAccountID ||
		usage.UsageAccountID != "111122223333" ||
		usage.AccountName != "Storefront Prod" {
		t.Fatalf("usage accounts = %+v, want management payer and Storefront Prod usage account", usage)
	}
	if usage.ServiceCode != serviceAmazonEC2 ||
		usage.ServiceName != "Amazon EC2" ||
		usage.ProductCode != serviceAmazonEC2 ||
		usage.Region != "us-east-1" ||
		usage.AvailabilityZone != "" {
		t.Fatalf("usage product/location fields = %+v, want EC2 us-east-1 without AZ", usage)
	}
	if usage.UsageType != "instance-hours:t3.medium" ||
		usage.Operation != "RunInstances" ||
		usage.LineItemType != billLineItemTypeUsage ||
		usage.ResourceID != "resource-cur-ec2" {
		t.Fatalf("usage identity fields = %+v, want source line item values", usage)
	}
	if usage.UsageAmountMicros != ec2Item.PricingQuantityMicros ||
		usage.UsageUnit != ec2Item.PricingUnit ||
		usage.UnblendedRateMicros != 41_600 ||
		usage.UnblendedCostMicros != 83_200 ||
		usage.Currency != defaultBillCurrencyCode {
		t.Fatalf("usage pricing fields = %+v, want pricing quantity/rate/cost in USD", usage)
	}
	if usage.LegalEntity != defaultInvoiceSellerOfRecord || usage.InvoiceEntity != defaultInvoiceSellerOfRecord {
		t.Fatalf("usage legal entities = %q/%q, want default seller", usage.LegalEntity, usage.InvoiceEntity)
	}
	if usage.Tags["app"] != "storefront" || usage.Tags["owner"] != "finops" {
		t.Fatalf("usage tags = %+v, want captured resource tags", usage.Tags)
	}
	if usage.CostCategories["Product"] != "Storefront" {
		t.Fatalf("usage cost categories = %+v, want Product Storefront assignment", usage.CostCategories)
	}

	support := requireCURLineItem(t, rows, supportResult.Items[0].ID)
	if support.LineItemType != billLineItemTypeFee ||
		support.ServiceCode != serviceAWSSupport ||
		support.UsageAccountID != AnyCompanyRetailManagementAccountID ||
		support.ResourceID != "" {
		t.Fatalf("support row = %+v, want payer-scoped fee without resource lineage", support)
	}
	if support.UsageAmountMicros != ec2Item.UnblendedCostMicros ||
		support.UsageUnit != supportResult.Items[0].PricingUnit ||
		support.UnblendedRateMicros != 100_000 ||
		support.UnblendedCostMicros != supportBusinessMinimumCostMicros {
		t.Fatalf("support pricing = %+v, want eligible spend amount and minimum fee", support)
	}
	if len(support.Tags) != 0 {
		t.Fatalf("support tags = %+v, want empty tag snapshot", support.Tags)
	}
}

func TestCURLineItemRepositoryFiltersAndValidatesRequests(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	item := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-cur-filter",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonS3,
			ResourceType: "s3_bucket",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-cur-filter",
			ResourceID:          "resource-cur-filter",
			UsageType:           "requests:put-1k",
			Operation:           "PutObject",
			UsageStartTime:      "2026-02-02T00:00:00Z",
			UsageEndTime:        "2026-02-03T00:00:00Z",
			UsageQuantityMicros: 1_500_000_000,
			UsageUnit:           "Request",
		},
	)

	rows, err := NewCURLineItemRepository(db).ListLineItems(ctx, CURLineItemListRequest{
		BillingPeriodStart: item.BillingPeriodStart,
		BillingPeriodEnd:   item.BillingPeriodEnd,
		UsageAccountID:     "111122223333",
		LineItemStatus:     billLineItemStatusEstimated,
	})
	if err != nil {
		t.Fatalf("ListLineItems(filtered) error = %v", err)
	}
	if len(rows) != 1 || rows[0].LineItemID != item.ID {
		t.Fatalf("ListLineItems(filtered) = %+v, want one S3 row", rows)
	}

	if _, err := NewCURLineItemRepository(db).ListLineItems(ctx, CURLineItemListRequest{
		BillingPeriodStart: item.BillingPeriodStart,
	}); err == nil {
		t.Fatal("ListLineItems(period start only) error = nil, want validation error")
	}
	if _, err := NewCURLineItemRepository(db).ListLineItems(ctx, CURLineItemListRequest{
		LineItemStatus: "draft",
	}); err == nil {
		t.Fatal("ListLineItems(unsupported status) error = nil, want validation error")
	}
	if _, err := NewCURLineItemRepository(nil).ListLineItems(ctx, CURLineItemListRequest{}); err == nil {
		t.Fatal("ListLineItems(nil db) error = nil, want database validation error")
	}
}

func TestCURLineItemRepositoryWritesCSVExportWithBillMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	item := recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-cur-csv-ec2",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "CUR CSV web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app":   "storefront",
				"owner": "finops",
			},
		},
		UsageEventCreateRequest{
			ID:                  "usage-cur-csv-hours",
			ResourceID:          "resource-cur-csv-ec2",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)
	if _, err := NewSimulatorClockRepository(db).Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}
	closeResult, err := NewMonthEndCloseRepository(db).ClosePreviousPeriod(ctx, MonthEndCloseRequest{
		PayerAccountID: AnyCompanyRetailManagementAccountID,
		InvoiceDueDays: 14,
	})
	if err != nil {
		t.Fatalf("ClosePreviousPeriod() error = %v", err)
	}

	var body bytes.Buffer
	result, err := NewCURLineItemRepository(db).WriteCSVExport(ctx, &body, CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     AnyCompanyRetailManagementAccountID,
		GeneratedAt:        "2026-03-02T01:02:03+13:00",
	})
	if err != nil {
		t.Fatalf("WriteCSVExport() error = %v", err)
	}
	if result.GeneratedAt != "2026-03-01T12:02:03Z" ||
		result.SourceBillID != closeResult.Bill.ID ||
		result.RowsWritten != 2 {
		t.Fatalf("WriteCSVExport() = %+v, want UTC generated time, bill ID, and two rows", result)
	}

	records, err := csv.NewReader(bytes.NewReader(body.Bytes())).ReadAll()
	if err != nil {
		t.Fatalf("read CUR CSV export: %v\n%s", err, body.String())
	}
	if len(records) != 3 {
		t.Fatalf("CUR CSV records = %d (%+v), want header plus usage and support rows", len(records), records)
	}
	if !slices.Equal(records[0], CURCSVExportColumns()) {
		t.Fatalf("CUR CSV header = %+v, want %+v", records[0], CURCSVExportColumns())
	}

	usage := requireCSVRecord(t, records, "line_item_id", item.ID)
	for column, want := range map[string]string{
		"export_generated_at": "2026-03-01T12:02:03Z",
		"source_bill_id":      closeResult.Bill.ID,
		"resource_id":         "resource-cur-csv-ec2",
		"usage_amount":        "2.000000",
		"unblended_rate":      "0.041600",
		"unblended_cost":      "0.083200",
		"tags_json":           `{"app":"storefront","owner":"finops"}`,
	} {
		if got := usage[csvColumnIndex(t, records[0], column)]; got != want {
			t.Fatalf("usage CSV column %s = %q, want %q in %v", column, got, want, usage)
		}
	}

	support := requireCSVRecord(t, records, "service_code", serviceAWSSupport)
	if got := support[csvColumnIndex(t, records[0], "source_bill_id")]; got != closeResult.Bill.ID {
		t.Fatalf("support source_bill_id = %q, want %q", got, closeResult.Bill.ID)
	}
	if got := support[csvColumnIndex(t, records[0], "line_item_type")]; got != billLineItemTypeFee {
		t.Fatalf("support line_item_type = %q, want Fee", got)
	}
}

func TestCURLineItemRepositoryWriteCSVExportValidatesRequest(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewCURLineItemRepository(db)
	var body bytes.Buffer

	if _, err := NewCURLineItemRepository(nil).WriteCSVExport(ctx, &body, CURCSVExportRequest{}); err == nil {
		t.Fatal("WriteCSVExport(nil db) error = nil, want database validation error")
	}
	if _, err := repo.WriteCSVExport(ctx, nil, CURCSVExportRequest{}); err == nil {
		t.Fatal("WriteCSVExport(nil writer) error = nil, want writer validation error")
	}
	if _, err := repo.WriteCSVExport(ctx, &body, CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	}); err == nil {
		t.Fatal("WriteCSVExport(missing payer) error = nil, want validation error")
	}
	if _, err := repo.WriteCSVExport(ctx, &body, CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		PayerAccountID:     AnyCompanyRetailManagementAccountID,
	}); err == nil {
		t.Fatal("WriteCSVExport(period start only) error = nil, want validation error")
	}
	if _, err := repo.WriteCSVExport(ctx, &body, CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     AnyCompanyRetailManagementAccountID,
		GeneratedAt:        "March 2",
	}); err == nil {
		t.Fatal("WriteCSVExport(invalid generated_at) error = nil, want validation error")
	}
}

// createCURExportCostCategory creates a persisted assignment so CUR rows can expose business dimensions.
func createCURExportCostCategory(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	repo := NewCostCategoryRepository(db)
	category, err := repo.CreateCategory(ctx, CostCategoryCreateRequest{
		Name:         "Product",
		DefaultValue: "Unmapped",
	})
	if err != nil {
		t.Fatalf("CreateCategory(Product) error = %v", err)
	}
	if _, err := repo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: category.ID,
		RuleOrder:      10,
		Value:          "Storefront",
		Conditions: []CostCategoryRuleCondition{
			{Dimension: CostCategoryRuleMatchService, Values: []string{serviceAmazonEC2}},
		},
	}); err != nil {
		t.Fatalf("CreateRule(Product Storefront) error = %v", err)
	}
}

func requireCURLineItem(t *testing.T, items []CURLineItem, id string) CURLineItem {
	t.Helper()

	for _, item := range items {
		if item.LineItemID == id {
			return item
		}
	}
	t.Fatalf("CUR line items = %+v, want ID %s", items, id)
	return CURLineItem{}
}

func requireCSVRecord(t *testing.T, records [][]string, column, value string) []string {
	t.Helper()

	index := csvColumnIndex(t, records[0], column)
	for _, record := range records[1:] {
		if record[index] == value {
			return record
		}
	}
	t.Fatalf("CSV records = %+v, want %s=%q", records, column, value)
	return nil
}

func csvColumnIndex(t *testing.T, header []string, column string) int {
	t.Helper()

	for idx, name := range header {
		if name == column {
			return idx
		}
	}
	t.Fatalf("CSV header = %+v, missing %q", header, column)
	return -1
}
