package app

import (
	"bytes"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"aws-billing-simulator/internal/csvsafe"
	"aws-billing-simulator/internal/persistence"
)

// handleInvoiceCSV serves a machine-readable detailed-charge export for one invoice.
func (h billsHandler) handleInvoiceCSV(w http.ResponseWriter, r *http.Request, invoiceID string) {
	if h.db == nil {
		http.Error(w, "workspace required", http.StatusConflict)
		return
	}
	printable, err := h.invoices.GetPrintableByInvoiceID(r.Context(), invoiceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "export invoice CSV: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.ensureInvoiceViewerAccess(r.Context(), r, printable.Document.PayerAccountID); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+invoiceCSVFilename(invoiceID)+`"`)
		w.WriteHeader(http.StatusOK)
		return
	}
	body, err := invoiceCSVBytes(printable)
	if err != nil {
		http.Error(w, "export invoice CSV: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+invoiceCSVFilename(invoiceID)+`"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// handleInvoicePDF serves a packaged PDF rendering of the printable invoice document.
func (h billsHandler) handleInvoicePDF(w http.ResponseWriter, r *http.Request, invoiceID string) {
	if h.db == nil {
		http.Error(w, "workspace required", http.StatusConflict)
		return
	}
	printable, err := h.invoices.GetPrintableByInvoiceID(r.Context(), invoiceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "prepare invoice PDF: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.ensureInvoiceViewerAccess(r.Context(), r, printable.Document.PayerAccountID); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	viewer := exportViewerFieldsFromBillsFilter(billsFilterFromRequest(r))
	htmlPath := invoicePathForIDWithViewer(invoiceID, viewer)
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="`+invoicePDFFilename(invoiceID)+`"`)
		w.Header().Set("Link", "<"+htmlPath+`>; rel="alternate"; type="text/html"`)
		w.WriteHeader(http.StatusOK)
		return
	}
	data := invoicePageDataFromPrintable(printable, viewer)
	body, err := invoicePDFBytes(data)
	if err != nil {
		http.Error(w, "render invoice PDF: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="`+invoicePDFFilename(invoiceID)+`"`)
	w.Header().Set("Link", "<"+htmlPath+`>; rel="alternate"; type="text/html"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// invoiceCSVBytes writes the detailed invoice line-item export using the printable invoice read model.
func invoiceCSVBytes(printable persistence.PrintableInvoice) ([]byte, error) {
	var body bytes.Buffer
	writer := csv.NewWriter(&body)
	if err := writer.Write(invoiceCSVHeader()); err != nil {
		return nil, err
	}
	for _, item := range printable.LineItems {
		if err := writer.Write(invoiceCSVRecord(printable, item)); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

// invoiceCSVHeader returns stable column names for the invoice detailed-charge export.
func invoiceCSVHeader() []string {
	return []string{
		"invoice_id",
		"bill_id",
		"document_status",
		"payment_status",
		"billing_period_start",
		"billing_period_end",
		"invoice_date",
		"due_date",
		"payer_account_id",
		"usage_account_id",
		"line_item_id",
		"line_item_type",
		"service_code",
		"service_name",
		"region_code",
		"resource_id",
		"resource_name",
		"usage_type",
		"operation",
		"usage_start_time",
		"usage_end_time",
		"pricing_quantity",
		"pricing_unit",
		"unblended_rate",
		"unblended_cost",
		"currency_code",
		"description",
	}
}

// invoiceCSVRecord formats one invoice source line item as a CSV row.
func invoiceCSVRecord(printable persistence.PrintableInvoice, item persistence.InvoiceLineItem) []string {
	document := printable.Document
	obligation := printable.Obligation
	safe := csvsafe.SpreadsheetString
	return []string{
		safe(document.InvoiceID),
		safe(document.BillID),
		safe(document.Status),
		safe(obligation.Status),
		safe(document.BillingPeriodStart),
		safe(document.BillingPeriodEnd),
		safe(document.InvoiceDate),
		safe(document.DueDate),
		safe(document.PayerAccountID),
		safe(item.UsageAccountID),
		safe(item.ID),
		safe(item.LineItemType),
		safe(item.ServiceCode),
		safe(item.ServiceName),
		safe(item.RegionCode),
		safe(item.ResourceID),
		safe(item.ResourceName),
		safe(item.UsageType),
		safe(item.Operation),
		safe(item.UsageStartTime),
		safe(item.UsageEndTime),
		formatMicrosDecimal(item.PricingQuantityMicros),
		safe(item.PricingUnit),
		formatMicrosDecimal(item.UnblendedRateMicros),
		formatMicrosDecimal(item.UnblendedCostMicros),
		safe(item.CurrencyCode),
		safe(item.Description),
	}
}

// formatMicrosDecimal renders micros as a fixed six-decimal value for CSV readers.
func formatMicrosDecimal(value int64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	return fmt.Sprintf("%s%d.%06d", sign, value/1_000_000, value%1_000_000)
}

// invoiceRouteFromPath extracts the invoice ID and optional export kind from an invoice URL path.
func invoiceRouteFromPath(path string) (invoiceRoute, bool) {
	const prefix = "/invoices/"
	if !strings.HasPrefix(path, prefix) {
		return invoiceRoute{}, false
	}
	rawID := strings.TrimSpace(strings.TrimPrefix(path, prefix))
	export := invoiceExportHTML
	if strings.HasSuffix(rawID, invoiceCSVPathSuffix) {
		rawID = strings.TrimSuffix(rawID, invoiceCSVPathSuffix)
		export = invoiceExportCSV
	} else if strings.HasSuffix(rawID, invoicePDFPathSuffix) {
		rawID = strings.TrimSuffix(rawID, invoicePDFPathSuffix)
		export = invoiceExportPDF
	}
	if rawID == "" || strings.Contains(rawID, "/") {
		return invoiceRoute{}, false
	}
	invoiceID, err := url.PathUnescape(rawID)
	invoiceID = strings.TrimSpace(invoiceID)
	if err != nil || invoiceID == "" || decodedPathSegmentHasSeparator(invoiceID) {
		return invoiceRoute{}, false
	}
	return invoiceRoute{InvoiceID: invoiceID, Export: export}, true
}

// invoiceIDFromPath extracts and unescapes the invoice ID from /invoices/{id}.
func invoiceIDFromPath(path string) (string, bool) {
	route, ok := invoiceRouteFromPath(path)
	if !ok || route.Export != invoiceExportHTML {
		return "", false
	}
	return route.InvoiceID, true
}

// invoicePathForID escapes an invoice ID for use as an internal invoice link.
func invoicePathForID(invoiceID string) string {
	return "/invoices/" + url.PathEscape(strings.TrimSpace(invoiceID))
}

func invoicePathForIDWithViewer(invoiceID string, viewer exportViewerFields) string {
	return invoicePathWithViewer(invoicePathForID(invoiceID), viewer)
}

// invoiceCSVPathForID returns the detailed-charge CSV download URL for an invoice.
func invoiceCSVPathForID(invoiceID string) string {
	return invoicePathForID(invoiceID) + invoiceCSVPathSuffix
}

func invoiceCSVPathForIDWithViewer(invoiceID string, viewer exportViewerFields) string {
	return invoicePathWithViewer(invoiceCSVPathForID(invoiceID), viewer)
}

// invoicePDFPathForID returns the packaged-PDF download URL for an invoice.
func invoicePDFPathForID(invoiceID string) string {
	return invoicePathForID(invoiceID) + invoicePDFPathSuffix
}

func invoicePDFPathForIDWithViewer(invoiceID string, viewer exportViewerFields) string {
	return invoicePathWithViewer(invoicePDFPathForID(invoiceID), viewer)
}

func invoicePathWithViewer(path string, viewer exportViewerFields) string {
	values := url.Values{}
	viewer.appendToValues(values)
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}

// invoiceCSVFilename sanitizes invoice IDs for the CSV content-disposition filename.
func invoiceCSVFilename(invoiceID string) string {
	return invoiceSafeFilenameBase(invoiceID) + "-line-items.csv"
}

// invoicePDFFilename sanitizes invoice IDs for the PDF content-disposition filename.
func invoicePDFFilename(invoiceID string) string {
	return invoiceSafeFilenameBase(invoiceID) + "-document.pdf"
}

func invoiceSafeFilenameBase(invoiceID string) string {
	var builder strings.Builder
	for _, r := range strings.TrimSpace(invoiceID) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('-')
		}
	}
	safe := strings.Trim(builder.String(), "-")
	if safe == "" {
		safe = "invoice"
	}
	return safe
}
