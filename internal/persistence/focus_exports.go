package persistence

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
)

const (
	// FOCUSTargetSpecificationVersion is the public FOCUS version this simulator maps toward.
	FOCUSTargetSpecificationVersion = "1.4"
	// FOCUSTargetSpecificationURL points to the selected public FOCUS specification.
	FOCUSTargetSpecificationURL = "https://focus.finops.org/focus-specification/v1-4/"
	// FOCUSTargetDataset records the FOCUS dataset represented by the CSV row shape.
	FOCUSTargetDataset = "Cost and Usage"
	// FOCUSConformanceClaim keeps metadata explicit that this is not a formal FOCUS dataset.
	FOCUSConformanceClaim = "not_conformant"

	focusLikeSchemaVersion = "FOCUS-like-2026-06-13-v1.4"
)

// FOCUSCSVExportMetadata is the JSON sidecar for one generated FOCUS-like CSV export.
type FOCUSCSVExportMetadata struct {
	Schema                  string                         `json:"schema"`
	SchemaVersion           string                         `json:"schema_version"`
	TargetFOCUSSpecVersion  string                         `json:"target_focus_spec_version"`
	TargetFOCUSSpecURL      string                         `json:"target_focus_spec_url"`
	Dataset                 string                         `json:"dataset"`
	SourceExportFilename    string                         `json:"source_export_filename"`
	GeneratedAt             string                         `json:"generated_at"`
	SourceBillID            string                         `json:"source_bill_id"`
	RowsWritten             int                            `json:"rows_written"`
	Visibility              FOCUSCSVVisibilityMetadata     `json:"visibility"`
	Conformance             FOCUSCSVConformanceMetadata    `json:"conformance"`
	Validator               FOCUSCSVValidatorMetadata      `json:"validator"`
	Columns                 []FOCUSCSVExportColumnMetadata `json:"columns"`
	UnsupportedRequirements []string                       `json:"unsupported_requirements"`
}

// FOCUSCSVVisibilityMetadata describes the row and document scope used for the export.
type FOCUSCSVVisibilityMetadata struct {
	Scope                     string `json:"scope"`
	AccountID                 string `json:"account_id,omitempty"`
	DocumentIdentifiersHidden bool   `json:"document_identifiers_hidden"`
}

// FOCUSCSVConformanceMetadata explains the simulator's FOCUS conformance boundary.
type FOCUSCSVConformanceMetadata struct {
	Claim  string `json:"claim"`
	Reason string `json:"reason"`
}

// FOCUSCSVValidatorMetadata gives validator-oriented context without claiming a pass.
type FOCUSCSVValidatorMetadata struct {
	TargetTool     string   `json:"target_tool"`
	InputFormat    string   `json:"input_format"`
	Dataset        string   `json:"dataset"`
	SpecVersion    string   `json:"spec_version"`
	ExpectedResult string   `json:"expected_result"`
	Boundary       string   `json:"boundary"`
	Capabilities   []string `json:"capabilities"`
}

// FOCUSCSVExportColumnMetadata classifies one CSV column as FOCUS-mapped or simulator-specific.
type FOCUSCSVExportColumnMetadata struct {
	Name           string `json:"name"`
	Classification string `json:"classification"`
	Source         string `json:"source"`
}

var focusCSVExportColumns = []string{
	"x_SimulatorExportGeneratedAt",
	"x_SimulatorSourceBillId",
	"x_SimulatorLineItemId",
	"x_SimulatorSchema",
	"BillingAccountId",
	"BillingAccountName",
	"BillingAccountType",
	"BillingCurrency",
	"BillingPeriodStart",
	"BillingPeriodEnd",
	"ChargeCategory",
	"ChargeDescription",
	"ChargePeriodStart",
	"ChargePeriodEnd",
	"ConsumedQuantity",
	"ConsumedUnit",
	"EffectiveCost",
	"InvoiceId",
	"InvoiceIssuer",
	"ListCost",
	"ListUnitPrice",
	"PricingCategory",
	"PricingQuantity",
	"PricingUnit",
	"Provider",
	"Publisher",
	"RegionId",
	"ResourceId",
	"ResourceName",
	"ResourceType",
	"ServiceCategory",
	"ServiceName",
	"SkuId",
	"SkuMeter",
	"SubAccountId",
	"SubAccountName",
	"SubAccountType",
	"Tags",
	"x_SimulatorCostCategories",
	"x_SimulatorProductFamily",
	"x_SimulatorUsageType",
	"x_SimulatorOperation",
}

// FOCUSCSVExportColumns returns the stable column order for FOCUS-like CSV exports.
func FOCUSCSVExportColumns() []string {
	columns := make([]string, len(focusCSVExportColumns))
	copy(columns, focusCSVExportColumns)
	return columns
}

// BuildFOCUSCSVExportMetadata creates the validator-oriented sidecar for a FOCUS-like CSV export.
func BuildFOCUSCSVExportMetadata(request CURCSVExportRequest, result CURCSVExportResult, sourceExportFilename string) FOCUSCSVExportMetadata {
	request = normalizeCURCSVExportRequest(request)
	scope, accountID := focusCSVVisibilityScope(request.Visibility)
	documentIDsHidden := request.Visibility.UsageAccountID != ""
	sourceBillID := result.SourceBillID
	if documentIDsHidden {
		sourceBillID = ""
	}
	return FOCUSCSVExportMetadata{
		Schema:                 "FOCUS-like",
		SchemaVersion:          focusLikeSchemaVersion,
		TargetFOCUSSpecVersion: FOCUSTargetSpecificationVersion,
		TargetFOCUSSpecURL:     FOCUSTargetSpecificationURL,
		Dataset:                FOCUSTargetDataset,
		SourceExportFilename:   sourceExportFilename,
		GeneratedAt:            result.GeneratedAt,
		SourceBillID:           sourceBillID,
		RowsWritten:            result.RowsWritten,
		Visibility: FOCUSCSVVisibilityMetadata{
			Scope:                     scope,
			AccountID:                 accountID,
			DocumentIdentifiersHidden: documentIDsHidden,
		},
		Conformance: FOCUSCSVConformanceMetadata{
			Claim:  FOCUSConformanceClaim,
			Reason: "The simulator produces a teaching-oriented FOCUS-like Cost and Usage CSV from synthetic line items, but it does not yet emit every FOCUS v1.4 dataset, metadata document, or conditional field required for formal conformance.",
		},
		Validator: FOCUSCSVValidatorMetadata{
			TargetTool:     "FOCUS Validator",
			InputFormat:    "CSV",
			Dataset:        FOCUSTargetDataset,
			SpecVersion:    FOCUSTargetSpecificationVersion,
			ExpectedResult: FOCUSConformanceClaim,
			Boundary:       "Use this sidecar to identify the intended FOCUS version, dataset, visibility scope, and simulator extension columns before running external validator experiments.",
			Capabilities: []string{
				"ACCOUNT_NAMING_SUPPORTED",
				"MULTIPLE_BILLING_ACCOUNT_TYPES_SUPPORTED",
				"MULTIPLE_SUB_ACCOUNT_TYPES_SUPPORTED",
				"REGION_SUPPORTED",
				"SUB_ACCOUNT_SUPPORTED",
				"TAGGING_SUPPORTED",
				"USAGE_MEASUREMENT_SUPPORTED",
			},
		},
		Columns: focusCSVExportColumnMetadata(),
		UnsupportedRequirements: []string{
			"FOCUS v1.4 Billing Period, Contract Commitment, and Invoice Detail datasets are not exported.",
			"FOCUS v1.4 Recency and Schema metadata documents are represented only by this simulator sidecar.",
			"Credits, taxes, blended rates, and FOCUS-native amortized/net cost fields are not modeled in this phase; simplified Reserved Instance and Savings Plan rows remain simulator line items rather than formal FOCUS Contract Commitment datasets.",
		},
	}
}

// WriteFOCUSCSVExportMetadata streams a stable JSON sidecar for a FOCUS-like CSV export.
func WriteFOCUSCSVExportMetadata(writer io.Writer, request CURCSVExportRequest, result CURCSVExportResult, sourceExportFilename string) error {
	if writer == nil {
		return fmt.Errorf("FOCUS-like metadata writer is required")
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(BuildFOCUSCSVExportMetadata(request, result, sourceExportFilename)); err != nil {
		return fmt.Errorf("encode FOCUS-like metadata: %w", err)
	}
	return nil
}

func focusCSVVisibilityScope(visibility BillingVisibilityFilter) (string, string) {
	visibility = normalizeBillingVisibilityFilter(visibility)
	if visibility.UsageAccountID != "" {
		return "usage-account", visibility.UsageAccountID
	}
	if visibility.PayerAccountID != "" {
		return "payer-account", visibility.PayerAccountID
	}
	return "all-accounts", ""
}

func focusCSVExportColumnMetadata() []FOCUSCSVExportColumnMetadata {
	columns := FOCUSCSVExportColumns()
	metadata := make([]FOCUSCSVExportColumnMetadata, 0, len(columns))
	for _, column := range columns {
		classification := "focus-mapped"
		if len(column) >= len("x_Simulator") && column[:len("x_Simulator")] == "x_Simulator" {
			classification = "simulator-extension"
		}
		metadata = append(metadata, FOCUSCSVExportColumnMetadata{
			Name:           column,
			Classification: classification,
			Source:         focusCSVExportColumnSource(column),
		})
	}
	return metadata
}

func focusCSVExportColumnSource(column string) string {
	if source, ok := focusCSVExportColumnSources[column]; ok {
		return source
	}
	return "bill_line_items projection"
}

var focusCSVExportColumnSources = map[string]string{
	"x_SimulatorExportGeneratedAt": "simulator clock",
	"x_SimulatorSourceBillId":      "issued bill ID, hidden for member-scoped exports",
	"x_SimulatorLineItemId":        "bill_line_items.id",
	"x_SimulatorSchema":            "local schema marker",
	"BillingAccountId":             "bill_line_items.payer_account_id",
	"BillingAccountName":           "payer account name",
	"BillingAccountType":           "derived payer account type",
	"BillingCurrency":              "bill_line_items.currency_code",
	"BillingPeriodStart":           "bill_line_items.billing_period_start",
	"BillingPeriodEnd":             "bill_line_items.billing_period_end",
	"ChargeCategory":               "simulator line item type mapping",
	"ChargeDescription":            "bill_line_items.description",
	"ChargePeriodStart":            "usage window start",
	"ChargePeriodEnd":              "usage window end",
	"ConsumedQuantity":             "consumed amount",
	"ConsumedUnit":                 "consumed unit",
	"EffectiveCost":                "synthetic unblended cost",
	"InvoiceId":                    "invoice document ID, hidden for member-scoped exports",
	"InvoiceIssuer":                "invoice seller snapshot",
	"ListCost":                     "synthetic unblended cost",
	"ListUnitPrice":                "synthetic unblended rate",
	"PricingCategory":              "line item pricing category mapping",
	"PricingQuantity":              "priced usage quantity",
	"PricingUnit":                  "priced usage unit",
	"Provider":                     "synthetic seller of record",
	"Publisher":                    "synthetic legal entity",
	"RegionId":                     "resource region",
	"ResourceId":                   "resource lineage",
	"ResourceName":                 "resource display name",
	"ResourceType":                 "resource type",
	"ServiceCategory":              "derived service category",
	"ServiceName":                  "service display name",
	"SkuId":                        "synthetic price SKU",
	"SkuMeter":                     "simulator usage type",
	"SubAccountId":                 "usage account ID",
	"SubAccountName":               "usage account name",
	"SubAccountType":               "derived usage account type",
	"Tags":                         "usage-window tag snapshot JSON",
	"x_SimulatorCostCategories":    "persisted Cost Category assignment JSON",
	"x_SimulatorProductFamily":     "synthetic price product family",
	"x_SimulatorUsageType":         "simulator usage type",
	"x_SimulatorOperation":         "simulator operation",
}

// WriteFOCUSCSVExport streams a deterministic FOCUS-like CSV export to writer.
func (r CURLineItemRepository) WriteFOCUSCSVExport(ctx context.Context, writer io.Writer, request CURCSVExportRequest) (CURCSVExportResult, error) {
	if r.db == nil {
		return CURCSVExportResult{}, fmt.Errorf("database handle is required")
	}
	if writer == nil {
		return CURCSVExportResult{}, fmt.Errorf("FOCUS-like CSV export writer is required")
	}
	request = normalizeCURCSVExportRequest(request)
	if err := validateCURCSVExportRequest(request); err != nil {
		return CURCSVExportResult{}, err
	}

	generatedAt, err := r.resolveCURCSVExportGeneratedAt(ctx, request.GeneratedAt)
	if err != nil {
		return CURCSVExportResult{}, err
	}
	sourceBillID, err := r.sourceBillIDForCURCSVExport(ctx, request)
	if err != nil {
		return CURCSVExportResult{}, err
	}
	items, err := r.ListLineItems(ctx, CURLineItemListRequest{
		BillingPeriodStart: request.BillingPeriodStart,
		BillingPeriodEnd:   request.BillingPeriodEnd,
		PayerAccountID:     request.PayerAccountID,
		UsageAccountID:     request.UsageAccountID,
		LineItemStatus:     request.LineItemStatus,
		Visibility:         request.Visibility,
		Limit:              request.Limit,
	})
	if err != nil {
		return CURCSVExportResult{}, err
	}

	hideDocumentIDs := request.Visibility.UsageAccountID != ""
	csvWriter := csv.NewWriter(writer)
	if err := csvWriter.Write(FOCUSCSVExportColumns()); err != nil {
		return CURCSVExportResult{}, err
	}
	for _, item := range items {
		record, err := focusCSVExportRecord(generatedAt, sourceBillID, item, hideDocumentIDs)
		if err != nil {
			return CURCSVExportResult{}, err
		}
		if err := csvWriter.Write(record); err != nil {
			return CURCSVExportResult{}, err
		}
	}
	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		return CURCSVExportResult{}, err
	}

	return CURCSVExportResult{
		GeneratedAt:  generatedAt,
		SourceBillID: sourceBillID,
		RowsWritten:  len(items),
	}, nil
}

func focusCSVExportRecord(generatedAt, sourceBillID string, item CURLineItem, hideDocumentIDs bool) ([]string, error) {
	tagsJSON, err := marshalStringMap(item.Tags)
	if err != nil {
		return nil, fmt.Errorf("encode FOCUS-like tags for line item %q: %w", item.LineItemID, err)
	}
	costCategoriesJSON, err := marshalStringMap(item.CostCategories)
	if err != nil {
		return nil, fmt.Errorf("encode FOCUS-like cost categories for line item %q: %w", item.LineItemID, err)
	}

	invoiceID := item.InvoiceID
	if hideDocumentIDs {
		sourceBillID = ""
		invoiceID = ""
	}
	return []string{
		generatedAt,
		sourceBillID,
		item.LineItemID,
		focusLikeSchemaVersion,
		item.PayerAccountID,
		item.PayerAccountName,
		focusBillingAccountType(item),
		item.Currency,
		item.BillingPeriodStart,
		item.BillingPeriodEnd,
		focusChargeCategory(item.LineItemType),
		item.Description,
		item.UsageStartTime,
		item.UsageEndTime,
		formatCURMicrosDecimal(item.ConsumedAmountMicros),
		item.ConsumedUnit,
		formatCURMicrosDecimal(item.UnblendedCostMicros),
		invoiceID,
		item.InvoiceEntity,
		formatCURMicrosDecimal(item.UnblendedCostMicros),
		formatCURMicrosDecimal(item.UnblendedRateMicros),
		focusPricingCategory(item.LineItemType),
		formatCURMicrosDecimal(item.UsageAmountMicros),
		item.UsageUnit,
		defaultInvoiceSellerOfRecord,
		item.LegalEntity,
		item.Region,
		item.ResourceID,
		item.ResourceName,
		item.ResourceType,
		focusServiceCategory(item.ServiceCode, item.ProductFamily),
		item.ServiceName,
		item.PriceCatalogSKU,
		item.UsageType,
		item.UsageAccountID,
		item.AccountName,
		focusSubAccountType(item),
		tagsJSON,
		costCategoriesJSON,
		item.ProductFamily,
		item.UsageType,
		item.Operation,
	}, nil
}

func focusBillingAccountType(item CURLineItem) string {
	if item.PayerAccountID == item.UsageAccountID {
		return "Management Account"
	}
	return "Payer Account"
}

func focusSubAccountType(item CURLineItem) string {
	if item.PayerAccountID == item.UsageAccountID {
		return "Management Account"
	}
	return "Linked Account"
}

func focusChargeCategory(lineItemType string) string {
	switch lineItemType {
	case billLineItemTypeUsage:
		return "Usage"
	case billLineItemTypeFee:
		return "Fee"
	case "Credit":
		return "Credit"
	case "Refund":
		return "Refund"
	case "Tax":
		return "Tax"
	default:
		return lineItemType
	}
}

func focusPricingCategory(lineItemType string) string {
	switch lineItemType {
	case billLineItemTypeUsage:
		return "Standard"
	case billLineItemTypeFee:
		return "Fee"
	default:
		return lineItemType
	}
}

func focusServiceCategory(serviceCode, productFamily string) string {
	switch serviceCode {
	case serviceAmazonEC2, serviceAmazonEBS, serviceAmazonRDS:
		return "Compute"
	case serviceAmazonS3:
		return "Storage"
	case serviceAmazonVPCNATGateway:
		return "Networking"
	case serviceAWSSupport:
		return "Support"
	case serviceAWSDataTransfer:
		return "Networking"
	case serviceAWSLambda:
		return "Compute"
	case "AmazonCloudWatchLogs":
		return "Monitoring"
	case "AWSMarketplace":
		return "Marketplace"
	default:
		if productFamily != "" {
			return productFamily
		}
		return "Other"
	}
}
