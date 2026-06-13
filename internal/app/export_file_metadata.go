package app

import (
	"fmt"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

func curCSVExportRequestFromExportFile(file persistence.ExportFile) (persistence.CURCSVExportRequest, error) {
	return csvExportRequestFromExportFile(file, persistence.ExportFileTypeCURCSV, "CUR CSV")
}

func focusCSVExportRequestFromExportFile(file persistence.ExportFile) (persistence.CURCSVExportRequest, error) {
	return csvExportRequestFromExportFile(file, persistence.ExportFileTypeFOCUSCSV, "FOCUS CSV")
}

func csvExportRequestFromExportFile(file persistence.ExportFile, exportType, label string) (persistence.CURCSVExportRequest, error) {
	if file.ExportType != exportType {
		return persistence.CURCSVExportRequest{}, fmt.Errorf("export type %q cannot be regenerated as %s", file.ExportType, label)
	}
	visibility, err := curCSVExportVisibilityFromGenerationParameters(file.GenerationParameters)
	if err != nil {
		return persistence.CURCSVExportRequest{}, err
	}
	request := persistence.CURCSVExportRequest{
		BillingPeriodStart: file.BillingPeriodStart,
		BillingPeriodEnd:   file.BillingPeriodEnd,
		PayerAccountID:     file.PayerAccountID,
		UsageAccountID:     file.UsageAccountID,
		LineItemStatus:     file.GenerationParameters["line_item_status"],
		Visibility:         visibility,
	}
	if rawLimit := strings.TrimSpace(file.GenerationParameters["limit"]); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return persistence.CURCSVExportRequest{}, fmt.Errorf("stored export limit must be an integer")
		}
		request.Limit = limit
	}
	return request, nil
}

func curCSVExportFilename(request persistence.CURCSVExportRequest) string {
	return csvExportFilename("cur", request)
}

func focusCSVExportFilename(request persistence.CURCSVExportRequest) string {
	return csvExportFilename("focus", request)
}

// focusCSVMetadataFilename derives the sidecar JSON name from the matching FOCUS CSV request.
func focusCSVMetadataFilename(request persistence.CURCSVExportRequest) string {
	return strings.TrimSuffix(focusCSVExportFilename(request), ".csv") + "-metadata.json"
}

func csvExportFilename(prefix string, request persistence.CURCSVExportRequest) string {
	limitPart := "default"
	if request.Limit > 0 {
		limitPart = strconv.Itoa(request.Limit)
	}
	parts := []string{
		prefix,
		safeCSVFilenamePart(request.BillingPeriodStart, "period-start"),
		safeCSVFilenamePart(request.BillingPeriodEnd, "period-end"),
		"payer",
		safeCSVFilenamePart(request.PayerAccountID, "payer"),
		"usage",
		safeCSVFilenamePart(request.UsageAccountID, "all-accounts"),
		"status",
		safeCSVFilenamePart(request.LineItemStatus, "all-statuses"),
		"limit",
		safeCSVFilenamePart(limitPart, "default"),
	}
	if request.Visibility.UsageAccountID != "" {
		parts = append(parts, "visibility", "usage", safeCSVFilenamePart(request.Visibility.UsageAccountID, "usage-account"))
	}
	return strings.Join(parts, "-") + ".csv"
}

func safeCSVFilenamePart(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	var safe strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			safe.WriteRune(r)
		} else {
			safe.WriteByte('-')
		}
	}
	result := strings.Trim(safe.String(), "-")
	if result == "" {
		return fallback
	}
	return result
}

func curCSVExportGenerationParameters(request persistence.CURCSVExportRequest, result persistence.CURCSVExportResult) map[string]string {
	visibilityScope, visibilityAccountID := curCSVExportVisibilityScope(request.Visibility)
	parameters := csvExportGenerationParameters(request, result)
	parameters["visibility_scope"] = visibilityScope
	parameters["visibility_account_id"] = visibilityAccountID
	return parameters
}

func focusCSVExportGenerationParameters(request persistence.CURCSVExportRequest, result persistence.CURCSVExportResult) map[string]string {
	parameters := curCSVExportGenerationParameters(request, result)
	parameters["schema"] = "FOCUS-like"
	parameters["schema_version"] = "FOCUS-like-2026-06-13-v1.4"
	parameters["target_focus_spec_version"] = persistence.FOCUSTargetSpecificationVersion
	parameters["target_focus_spec_url"] = persistence.FOCUSTargetSpecificationURL
	parameters["focus_dataset"] = persistence.FOCUSTargetDataset
	parameters["conformance_claim"] = persistence.FOCUSConformanceClaim
	return parameters
}

// focusCSVMetadataGenerationParameters records the validator sidecar provenance and conformance boundary.
func focusCSVMetadataGenerationParameters(request persistence.CURCSVExportRequest, result persistence.CURCSVExportResult, sourceExportFilename string) map[string]string {
	parameters := focusCSVExportGenerationParameters(request, result)
	parameters["source_export_filename"] = sourceExportFilename
	parameters["validator_target"] = "FOCUS Validator"
	parameters["validator_expected_result"] = persistence.FOCUSConformanceClaim
	return parameters
}

func csvExportGenerationParameters(request persistence.CURCSVExportRequest, result persistence.CURCSVExportResult) map[string]string {
	parameters := map[string]string{
		"billing_period_start": request.BillingPeriodStart,
		"billing_period_end":   request.BillingPeriodEnd,
		"payer_account_id":     request.PayerAccountID,
		"usage_account_id":     request.UsageAccountID,
		"line_item_status":     request.LineItemStatus,
		"generated_at":         result.GeneratedAt,
		"source_bill_id":       result.SourceBillID,
		"rows_written":         strconv.Itoa(result.RowsWritten),
	}
	if request.Limit > 0 {
		parameters["limit"] = strconv.Itoa(request.Limit)
	}
	return parameters
}

const (
	exportVisibilityScopeAllAccounts   = "all-accounts"
	exportVisibilityScopePayerAccount  = "payer-account"
	exportVisibilityScopeUsageAccount  = "usage-account"
	exportVisibilityScopeKey           = "visibility_scope"
	exportVisibilityAccountIDParameter = "visibility_account_id"
)

// curCSVExportVisibilityScope serializes the policy row scope used when producing stored export bytes.
func curCSVExportVisibilityScope(visibility persistence.BillingVisibilityFilter) (string, string) {
	usageAccountID := strings.TrimSpace(visibility.UsageAccountID)
	if usageAccountID != "" {
		return exportVisibilityScopeUsageAccount, usageAccountID
	}
	payerAccountID := strings.TrimSpace(visibility.PayerAccountID)
	if payerAccountID != "" {
		return exportVisibilityScopePayerAccount, payerAccountID
	}
	return exportVisibilityScopeAllAccounts, ""
}

// curCSVExportVisibilityFromGenerationParameters restores the row scope needed to regenerate a stored export.
func curCSVExportVisibilityFromGenerationParameters(parameters map[string]string) (persistence.BillingVisibilityFilter, error) {
	scope := strings.TrimSpace(parameters[exportVisibilityScopeKey])
	accountID := strings.TrimSpace(parameters[exportVisibilityAccountIDParameter])
	switch scope {
	case "":
		return persistence.BillingVisibilityFilter{}, nil
	case exportVisibilityScopeAllAccounts:
		return persistence.BillingVisibilityFilter{}, nil
	case exportVisibilityScopePayerAccount:
		if accountID == "" {
			return persistence.BillingVisibilityFilter{}, fmt.Errorf("stored export payer visibility account ID is required")
		}
		return persistence.BillingVisibilityFilter{PayerAccountID: accountID}, nil
	case exportVisibilityScopeUsageAccount:
		if accountID == "" {
			return persistence.BillingVisibilityFilter{}, fmt.Errorf("stored export usage visibility account ID is required")
		}
		return persistence.BillingVisibilityFilter{UsageAccountID: accountID}, nil
	default:
		return persistence.BillingVisibilityFilter{}, fmt.Errorf("stored export visibility scope %q is unsupported", scope)
	}
}

// exportFileVisibilityScope reads the stored generation scope used for member export authorization.
func exportFileVisibilityScope(file persistence.ExportFile) (string, string, error) {
	visibility, err := curCSVExportVisibilityFromGenerationParameters(file.GenerationParameters)
	if err != nil {
		return "", "", err
	}
	scope, accountID := curCSVExportVisibilityScope(visibility)
	if strings.TrimSpace(file.GenerationParameters[exportVisibilityScopeKey]) == "" {
		scope = ""
		accountID = ""
	}
	return scope, accountID, nil
}

func exportFileContentType(exportType string) string {
	switch exportType {
	case persistence.ExportFileTypeCURCSV, persistence.ExportFileTypeFOCUSCSV:
		return "text/csv; charset=utf-8"
	case persistence.ExportFileTypeFOCUSMetadataJSON:
		return "application/json; charset=utf-8"
	default:
		return "application/octet-stream"
	}
}
