package persistence

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
)

const focusLikeSchemaVersion = "FOCUS-like-2026-06-09"

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
