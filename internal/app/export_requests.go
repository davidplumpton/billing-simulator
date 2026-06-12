package app

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

func curCSVExportRequestFromQuery(r *http.Request) (persistence.CURCSVExportRequest, error) {
	return curCSVExportRequestFromValues(r.URL.Query())
}

func curCSVExportRequestFromForm(r *http.Request) (persistence.CURCSVExportRequest, error) {
	return curCSVExportRequestFromValues(r.PostForm)
}

func curCSVExportRequestFromValues(values url.Values) (persistence.CURCSVExportRequest, error) {
	request := persistence.CURCSVExportRequest{
		BillingPeriodStart: values.Get("billing_period_start"),
		BillingPeriodEnd:   values.Get("billing_period_end"),
		PayerAccountID:     values.Get("payer_account_id"),
		UsageAccountID:     values.Get("usage_account_id"),
		LineItemStatus:     values.Get("line_item_status"),
	}
	if rawLimit := strings.TrimSpace(values.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return persistence.CURCSVExportRequest{}, fmt.Errorf("limit must be an integer")
		}
		request.Limit = limit
	}
	return request, nil
}

func curExportReconciliationRequestFromQuery(r *http.Request) (persistence.CURExportReconciliationRequest, error) {
	query := r.URL.Query()
	request := persistence.CURExportReconciliationRequest{
		BillingPeriodStart: query.Get("billing_period_start"),
		BillingPeriodEnd:   query.Get("billing_period_end"),
		PayerAccountID:     query.Get("payer_account_id"),
		UsageAccountID:     query.Get("usage_account_id"),
		LineItemStatus:     query.Get("line_item_status"),
	}
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return persistence.CURExportReconciliationRequest{}, fmt.Errorf("limit must be an integer")
		}
		request.Limit = limit
	}
	return request, nil
}

func exportFileListRequestFromFilter(filter exportFileFilterView) (persistence.ExportFileListRequest, error) {
	request := persistence.ExportFileListRequest{
		ExportType:         filter.ExportType,
		BillingPeriodStart: filter.BillingPeriodStart,
		BillingPeriodEnd:   filter.BillingPeriodEnd,
		PayerAccountID:     filter.PayerAccountID,
		UsageAccountID:     filter.UsageAccountID,
	}
	if rawLimit := strings.TrimSpace(filter.Limit); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return persistence.ExportFileListRequest{}, fmt.Errorf("limit must be an integer")
		}
		request.Limit = limit
	}
	return request, nil
}
