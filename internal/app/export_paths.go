package app

import (
	"net/url"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

func exportFileDownloadPath(filename string) string {
	return "/exports/files/" + url.PathEscape(filename)
}

func exportFileDownloadPathWithViewer(filename string, viewer exportViewerFields) string {
	path := exportFileDownloadPath(filename)
	values := url.Values{}
	viewer.appendToValues(values)
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}

func exportFileDownloadFilenameFromPath(path string) (string, bool) {
	const prefix = "/exports/files/"
	if !strings.HasPrefix(path, prefix) {
		return "", false
	}
	raw := strings.TrimPrefix(path, prefix)
	if raw == "" || strings.Contains(raw, "/") {
		return "", false
	}
	filename, err := url.PathUnescape(raw)
	if err != nil {
		return "", false
	}
	return filename, true
}

func curCSVExportPath(request persistence.CURCSVExportRequest) string {
	return curCSVExportPathWithViewer(request, exportViewerFields{})
}

func curCSVExportPathWithViewer(request persistence.CURCSVExportRequest, viewer exportViewerFields) string {
	values := url.Values{}
	appendQueryValue(values, "billing_period_start", request.BillingPeriodStart)
	appendQueryValue(values, "billing_period_end", request.BillingPeriodEnd)
	appendQueryValue(values, "payer_account_id", request.PayerAccountID)
	appendQueryValue(values, "usage_account_id", request.UsageAccountID)
	appendQueryValue(values, "line_item_status", request.LineItemStatus)
	if request.Limit > 0 {
		values.Set("limit", strconv.Itoa(request.Limit))
	}
	viewer.appendToValues(values)
	if len(values) == 0 {
		return "/exports/cur.csv"
	}
	return "/exports/cur.csv?" + values.Encode()
}

func curExportReconciliationPath(request persistence.CURExportReconciliationRequest) string {
	return curExportReconciliationPathWithViewer(request, exportViewerFields{})
}

func curExportReconciliationPathWithViewer(request persistence.CURExportReconciliationRequest, viewer exportViewerFields) string {
	values := url.Values{}
	appendQueryValue(values, "billing_period_start", request.BillingPeriodStart)
	appendQueryValue(values, "billing_period_end", request.BillingPeriodEnd)
	appendQueryValue(values, "payer_account_id", request.PayerAccountID)
	appendQueryValue(values, "usage_account_id", request.UsageAccountID)
	appendQueryValue(values, "line_item_status", request.LineItemStatus)
	if request.Limit > 0 {
		values.Set("limit", strconv.Itoa(request.Limit))
	}
	viewer.appendToValues(values)
	if len(values) == 0 {
		return "/exports/reconciliation"
	}
	return "/exports/reconciliation?" + values.Encode()
}
