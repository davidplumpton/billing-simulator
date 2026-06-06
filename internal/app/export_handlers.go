package app

import (
	"bytes"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

type exportsHandler struct {
	db  *sql.DB
	cur persistence.CURLineItemRepository
}

func newExportsHandler(db *sql.DB) exportsHandler {
	return exportsHandler{
		db:  db,
		cur: persistence.NewCURLineItemRepository(db),
	}
}

// handleCURCSV exports payer-period bill line items in the simulator's CUR-like CSV schema.
func (h exportsHandler) handleCURCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w)
		return
	}
	if h.db == nil {
		http.Error(w, "workspace required", http.StatusConflict)
		return
	}

	request, err := curCSVExportRequestFromQuery(r)
	if err != nil {
		http.Error(w, "export CUR CSV: "+err.Error(), http.StatusBadRequest)
		return
	}

	var body bytes.Buffer
	if _, err := h.cur.WriteCSVExport(r.Context(), &body, request); err != nil {
		http.Error(w, "export CUR CSV: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+curCSVExportFilename(request)+`"`)
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body.Bytes())
	}
}

func curCSVExportRequestFromQuery(r *http.Request) (persistence.CURCSVExportRequest, error) {
	query := r.URL.Query()
	request := persistence.CURCSVExportRequest{
		BillingPeriodStart: query.Get("billing_period_start"),
		BillingPeriodEnd:   query.Get("billing_period_end"),
		PayerAccountID:     query.Get("payer_account_id"),
		UsageAccountID:     query.Get("usage_account_id"),
		LineItemStatus:     query.Get("line_item_status"),
	}
	if rawLimit := strings.TrimSpace(query.Get("limit")); rawLimit != "" {
		limit, err := strconv.Atoi(rawLimit)
		if err != nil {
			return persistence.CURCSVExportRequest{}, fmt.Errorf("limit must be an integer")
		}
		request.Limit = limit
	}
	return request, nil
}

func curCSVExportFilename(request persistence.CURCSVExportRequest) string {
	parts := []string{
		"cur",
		safeCSVFilenamePart(request.BillingPeriodStart, "period-start"),
		safeCSVFilenamePart(request.BillingPeriodEnd, "period-end"),
		safeCSVFilenamePart(request.PayerAccountID, "payer"),
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
