package app

import (
	"bytes"
	"encoding/csv"
	"testing"

	"aws-billing-simulator/internal/persistence"
)

func TestInvoiceCSVNeutralizesSpreadsheetFormulaStrings(t *testing.T) {
	t.Parallel()

	body, err := invoiceCSVBytes(persistence.PrintableInvoice{
		Document: persistence.InvoiceDocument{
			InvoiceID:          "=invoice",
			BillID:             "+bill",
			Status:             "issued",
			BillingPeriodStart: "2026-02-01",
			BillingPeriodEnd:   "2026-03-01",
			InvoiceDate:        "2026-03-01",
			DueDate:            "2026-03-15",
			PayerAccountID:     "999988887777",
		},
		Obligation: persistence.InvoiceObligation{Status: "due"},
		LineItems: []persistence.InvoiceLineItem{{
			ID:                    "=line-item",
			ResourceID:            "=resource",
			ResourceName:          "+resource name",
			UsageAccountID:        "111122223333",
			ServiceCode:           "AmazonEC2",
			ServiceName:           "-service name",
			LineItemType:          "Usage",
			RegionCode:            "us-east-1",
			UsageType:             "instance-hours:t3.medium",
			Operation:             "RunInstances",
			UsageStartTime:        "2026-02-01T00:00:00Z",
			UsageEndTime:          "2026-02-01T01:00:00Z",
			PricingQuantityMicros: 1_000_000,
			PricingUnit:           "Hours",
			UnblendedRateMicros:   41_600,
			UnblendedCostMicros:   -41_600,
			CurrencyCode:          "USD",
			Description:           "@description",
		}},
	})
	if err != nil {
		t.Fatalf("invoiceCSVBytes() error = %v", err)
	}
	records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("read invoice CSV: %v\n%s", err, body)
	}
	if len(records) != 2 {
		t.Fatalf("invoice CSV records = %+v, want header and one row", records)
	}
	row := records[1]
	for column, want := range map[string]string{
		"invoice_id":     "'=invoice",
		"bill_id":        "'+bill",
		"line_item_id":   "'=line-item",
		"resource_id":    "'=resource",
		"resource_name":  "'+resource name",
		"service_name":   "'-service name",
		"description":    "'@description",
		"unblended_cost": "-0.041600",
	} {
		assertCSVDownloadColumn(t, records[0], row, column, want)
	}
}

func TestCostExplorerResultsCSVNeutralizesSpreadsheetFormulaGroups(t *testing.T) {
	t.Parallel()

	body, err := costExplorerResultsCSVBytes(persistence.CostExplorerQueryResult{
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Granularity:    "daily",
		Rows: []persistence.CostExplorerQueryRow{{
			TimePeriodStart: "2026-02-01",
			TimePeriodEnd:   "2026-02-02",
			GroupValues: []persistence.CostExplorerGroupValue{
				{Type: "=tag", Key: "+owner", Value: "-finance"},
				{Type: "dimension", Key: "service", Value: "@service"},
			},
			UsageQuantityMicros: 1_000_000,
			UnblendedCostMicros: 2_500_000,
			BlendedCostMicros:   2_500_000,
			NetCostMicros:       2_500_000,
			AmortizedCostMicros: 2_500_000,
			LineItemCount:       1,
			CurrencyCode:        "USD",
		}},
	}, costExplorerBuilderView{Metric: "unblended_cost"})
	if err != nil {
		t.Fatalf("costExplorerResultsCSVBytes() error = %v", err)
	}
	records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("read Cost Explorer CSV: %v\n%s", err, body)
	}
	if len(records) != 2 {
		t.Fatalf("Cost Explorer CSV records = %+v, want header and one row", records)
	}
	row := records[1]
	for column, want := range map[string]string{
		"group_1_type":  "'=tag",
		"group_1_key":   "'+owner",
		"group_1_value": "'-finance",
		"group_2_value": "'@service",
		"metric_value":  "2.500000",
	} {
		assertCSVDownloadColumn(t, records[0], row, column, want)
	}
}

func assertCSVDownloadColumn(t *testing.T, header, row []string, column, want string) {
	t.Helper()

	if got := row[csvResponseColumnIndex(t, header, column)]; got != want {
		t.Fatalf("CSV column %s = %q, want %q in %v", column, got, want, row)
	}
}
