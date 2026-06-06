package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	defaultInvoiceDocumentLimit         = 25
	maxInvoiceDocumentLimit             = 100
	defaultInvoiceDocumentVersion       = 1
	invoiceDocumentStatusIssued         = "issued"
	defaultInvoiceSellerOfRecord        = "AWS Billing Simulator"
	defaultInvoiceSellerAddress         = "Local synthetic invoice lab"
	defaultInvoiceSellerTaxRegistration = ""
	defaultInvoiceBillToName            = "AnyCompany Retail"
	defaultInvoiceBillToEmail           = "billing@anycompany.example"
	defaultInvoiceBillToAddress         = "100 AnyCompany Way, Example City"
	defaultInvoiceBillToTaxRegistration = ""
)

type invoiceDocumentProfileFields struct {
	SellerOfRecord        string
	SellerAddress         string
	SellerTaxRegistration string
	BillToName            string
	BillToEmail           string
	BillToAddress         string
	BillToTaxRegistration string
}

// InvoiceDocument stores the durable header and profile fields for a generated invoice.
type InvoiceDocument struct {
	InvoiceID             string
	BillID                string
	InvoiceObligationID   string
	DocumentVersion       int
	Status                string
	BillingPeriodStart    string
	BillingPeriodEnd      string
	InvoiceDate           string
	DueDate               string
	SellerOfRecord        string
	SellerAddress         string
	SellerTaxRegistration string
	PayerAccountID        string
	BillToName            string
	BillToEmail           string
	BillToAddress         string
	BillToTaxRegistration string
	CurrencyCode          string
	LineItemCount         int
	UsageChargeMicros     int64
	CreditMicros          int64
	RefundMicros          int64
	TaxMicros             int64
	TotalMicros           int64
	CreatedAt             string
	UpdatedAt             string
}

// PrintableInvoice combines the durable invoice document with current payment status and detail rows.
type PrintableInvoice struct {
	Document         InvoiceDocument
	Obligation       InvoiceObligation
	ServiceSummaries []InvoiceChargeSummary
	AccountSummaries []InvoiceAccountChargeSummary
	LineItems        []InvoiceLineItem
}

// InvoiceChargeSummary stores invoice totals grouped by service.
type InvoiceChargeSummary struct {
	ServiceCode   string
	ServiceName   string
	CurrencyCode  string
	LineItemCount int
	ChargeMicros  int64
	CreditMicros  int64
	RefundMicros  int64
	TaxMicros     int64
	TotalMicros   int64
}

// InvoiceAccountChargeSummary stores invoice totals grouped by usage account.
type InvoiceAccountChargeSummary struct {
	UsageAccountID string
	CurrencyCode   string
	LineItemCount  int
	ChargeMicros   int64
	CreditMicros   int64
	RefundMicros   int64
	TaxMicros      int64
	TotalMicros    int64
}

// InvoiceLineItem stores one printable source line item for an invoice.
type InvoiceLineItem struct {
	ID                    string
	ResourceID            string
	ResourceName          string
	UsageAccountID        string
	ServiceCode           string
	ServiceName           string
	LineItemType          string
	RegionCode            string
	UsageType             string
	Operation             string
	UsageStartTime        string
	UsageEndTime          string
	PricingQuantityMicros int64
	PricingUnit           string
	UnblendedRateMicros   int64
	UnblendedCostMicros   int64
	CurrencyCode          string
	Description           string
}

// InvoiceDocumentRepository creates and reads synthetic invoice documents.
type InvoiceDocumentRepository struct {
	db *sql.DB
}

// NewInvoiceDocumentRepository creates an invoice document repository backed by a workspace database.
func NewInvoiceDocumentRepository(db *sql.DB) InvoiceDocumentRepository {
	return InvoiceDocumentRepository{db: db}
}

// CreateForIssuedBill persists a deterministic invoice document for an issued bill.
func (r InvoiceDocumentRepository) CreateForIssuedBill(ctx context.Context, bill Bill, obligation InvoiceObligation) (InvoiceDocument, error) {
	if r.db == nil {
		return InvoiceDocument{}, fmt.Errorf("database handle is required")
	}
	profile, err := invoiceDocumentProfileForPayer(ctx, r.db, bill.PayerAccountID, bill.CurrencyCode)
	if err != nil {
		return InvoiceDocument{}, err
	}
	document, err := invoiceDocumentFromBillWithProfile(bill, obligation, profile)
	if err != nil {
		return InvoiceDocument{}, err
	}
	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		_, err := insertInvoiceDocumentIfMissing(ctx, tx, document)
		return err
	}); err != nil {
		return InvoiceDocument{}, err
	}
	return r.GetByBillID(ctx, bill.ID)
}

// GetByBillID reads the invoice document generated for a bill.
func (r InvoiceDocumentRepository) GetByBillID(ctx context.Context, billID string) (InvoiceDocument, error) {
	if r.db == nil {
		return InvoiceDocument{}, fmt.Errorf("database handle is required")
	}
	billID = strings.TrimSpace(billID)
	if billID == "" {
		return InvoiceDocument{}, fmt.Errorf("bill ID is required")
	}

	row := r.db.QueryRowContext(
		ctx,
		invoiceDocumentSelectSQL+`
		 WHERE bill_id = ?`,
		billID,
	)
	document, err := scanInvoiceDocument(row)
	if err != nil {
		return InvoiceDocument{}, fmt.Errorf("get invoice document for bill %q: %w", billID, err)
	}
	return document, nil
}

// ListRecent reads recent invoice documents in newest-first order.
func (r InvoiceDocumentRepository) ListRecent(ctx context.Context, limit int) ([]InvoiceDocument, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	if limit <= 0 {
		limit = defaultInvoiceDocumentLimit
	}
	if limit > maxInvoiceDocumentLimit {
		limit = maxInvoiceDocumentLimit
	}

	rows, err := r.db.QueryContext(
		ctx,
		invoiceDocumentSelectSQL+`
		 ORDER BY invoice_date DESC, invoice_id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list invoice documents: %w", err)
	}
	defer rows.Close()

	var documents []InvoiceDocument
	for rows.Next() {
		document, err := scanInvoiceDocument(rows)
		if err != nil {
			return nil, err
		}
		documents = append(documents, document)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate invoice documents: %w", err)
	}
	return documents, nil
}

// GetPrintableByInvoiceID reads a printable invoice document and its final source line item details.
func (r InvoiceDocumentRepository) GetPrintableByInvoiceID(ctx context.Context, invoiceID string) (PrintableInvoice, error) {
	if r.db == nil {
		return PrintableInvoice{}, fmt.Errorf("database handle is required")
	}
	invoiceID = strings.TrimSpace(invoiceID)
	if invoiceID == "" {
		return PrintableInvoice{}, fmt.Errorf("invoice ID is required")
	}

	document, err := r.getByInvoiceID(ctx, invoiceID)
	if err != nil {
		return PrintableInvoice{}, err
	}
	obligation, err := r.getObligation(ctx, document.InvoiceObligationID)
	if err != nil {
		return PrintableInvoice{}, err
	}
	serviceSummaries, err := r.listPrintableServiceSummaries(ctx, document)
	if err != nil {
		return PrintableInvoice{}, err
	}
	accountSummaries, err := r.listPrintableAccountSummaries(ctx, document)
	if err != nil {
		return PrintableInvoice{}, err
	}
	lineItems, err := r.listPrintableLineItems(ctx, document)
	if err != nil {
		return PrintableInvoice{}, err
	}
	return PrintableInvoice{
		Document:         document,
		Obligation:       obligation,
		ServiceSummaries: serviceSummaries,
		AccountSummaries: accountSummaries,
		LineItems:        lineItems,
	}, nil
}

func invoiceDocumentFromBill(bill Bill, obligation InvoiceObligation) (InvoiceDocument, error) {
	return invoiceDocumentFromBillWithProfile(bill, obligation, defaultInvoiceDocumentProfileFields())
}

func invoiceDocumentFromBillWithProfile(bill Bill, obligation InvoiceObligation, profile invoiceDocumentProfileFields) (InvoiceDocument, error) {
	billID := strings.TrimSpace(bill.ID)
	obligationID := strings.TrimSpace(obligation.ID)
	invoiceID := strings.TrimSpace(obligation.InvoiceID)
	if billID == "" {
		return InvoiceDocument{}, fmt.Errorf("bill ID is required")
	}
	if obligationID == "" {
		return InvoiceDocument{}, fmt.Errorf("invoice obligation ID is required")
	}
	if invoiceID == "" {
		return InvoiceDocument{}, fmt.Errorf("invoice ID is required")
	}
	if strings.TrimSpace(obligation.BillID) != billID {
		return InvoiceDocument{}, fmt.Errorf("invoice obligation bill ID must match bill ID")
	}
	if strings.TrimSpace(obligation.CurrencyCode) != strings.TrimSpace(bill.CurrencyCode) {
		return InvoiceDocument{}, fmt.Errorf("invoice obligation currency must match bill currency")
	}
	profile = profile.withDefaults()

	return InvoiceDocument{
		InvoiceID:             invoiceID,
		BillID:                billID,
		InvoiceObligationID:   obligationID,
		DocumentVersion:       defaultInvoiceDocumentVersion,
		Status:                invoiceDocumentStatusIssued,
		BillingPeriodStart:    bill.BillingPeriodStart,
		BillingPeriodEnd:      bill.BillingPeriodEnd,
		InvoiceDate:           obligation.InvoiceDate,
		DueDate:               obligation.DueDate,
		SellerOfRecord:        profile.SellerOfRecord,
		SellerAddress:         profile.SellerAddress,
		SellerTaxRegistration: profile.SellerTaxRegistration,
		PayerAccountID:        bill.PayerAccountID,
		BillToName:            profile.BillToName,
		BillToEmail:           profile.BillToEmail,
		BillToAddress:         profile.BillToAddress,
		BillToTaxRegistration: profile.BillToTaxRegistration,
		CurrencyCode:          bill.CurrencyCode,
		LineItemCount:         bill.LineItemCount,
		UsageChargeMicros:     bill.UsageChargeMicros,
		CreditMicros:          bill.CreditMicros,
		RefundMicros:          bill.RefundMicros,
		TaxMicros:             bill.TaxMicros,
		TotalMicros:           bill.TotalMicros,
	}, nil
}

func defaultInvoiceDocumentProfileFields() invoiceDocumentProfileFields {
	return invoiceDocumentProfileFields{
		SellerOfRecord:        defaultInvoiceSellerOfRecord,
		SellerAddress:         defaultInvoiceSellerAddress,
		SellerTaxRegistration: defaultInvoiceSellerTaxRegistration,
		BillToName:            defaultInvoiceBillToName,
		BillToEmail:           defaultInvoiceBillToEmail,
		BillToAddress:         defaultInvoiceBillToAddress,
		BillToTaxRegistration: defaultInvoiceBillToTaxRegistration,
	}
}

func (profile invoiceDocumentProfileFields) withDefaults() invoiceDocumentProfileFields {
	defaults := defaultInvoiceDocumentProfileFields()
	if strings.TrimSpace(profile.SellerOfRecord) == "" {
		profile.SellerOfRecord = defaults.SellerOfRecord
	}
	if strings.TrimSpace(profile.SellerAddress) == "" {
		profile.SellerAddress = defaults.SellerAddress
	}
	if strings.TrimSpace(profile.SellerTaxRegistration) == "" {
		profile.SellerTaxRegistration = defaults.SellerTaxRegistration
	}
	if strings.TrimSpace(profile.BillToName) == "" {
		profile.BillToName = defaults.BillToName
	}
	if strings.TrimSpace(profile.BillToEmail) == "" {
		profile.BillToEmail = defaults.BillToEmail
	}
	if strings.TrimSpace(profile.BillToAddress) == "" {
		profile.BillToAddress = defaults.BillToAddress
	}
	if strings.TrimSpace(profile.BillToTaxRegistration) == "" {
		profile.BillToTaxRegistration = defaults.BillToTaxRegistration
	}
	return profile
}

func (r InvoiceDocumentRepository) getByInvoiceID(ctx context.Context, invoiceID string) (InvoiceDocument, error) {
	row := r.db.QueryRowContext(
		ctx,
		invoiceDocumentSelectSQL+`
		 WHERE invoice_id = ?`,
		invoiceID,
	)
	document, err := scanInvoiceDocument(row)
	if err != nil {
		return InvoiceDocument{}, fmt.Errorf("get invoice document %q: %w", invoiceID, err)
	}
	return document, nil
}

func (r InvoiceDocumentRepository) getObligation(ctx context.Context, obligationID string) (InvoiceObligation, error) {
	obligation, err := getInvoiceObligationWithPaymentState(ctx, r.db, `o.id = ?`, obligationID)
	if err != nil {
		return InvoiceObligation{}, fmt.Errorf("get invoice obligation %q: %w", obligationID, err)
	}
	return obligation, nil
}

func (r InvoiceDocumentRepository) listPrintableServiceSummaries(ctx context.Context, document InvoiceDocument) ([]InvoiceChargeSummary, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			service_code,
			service_name,
			currency_code,
			COUNT(*),
			COALESCE(SUM(CASE WHEN line_item_type IN ('Usage', 'Fee') THEN unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN line_item_type = 'Credit' THEN unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN line_item_type = 'Refund' THEN unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN line_item_type = 'Tax' THEN unblended_cost_micros ELSE 0 END), 0)
		 FROM bill_line_items
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND payer_account_id = ?
		   AND currency_code = ?
		   AND line_item_status = ?
		 GROUP BY service_code, service_name, currency_code
		 ORDER BY service_name, service_code`,
		document.BillingPeriodStart,
		document.BillingPeriodEnd,
		document.PayerAccountID,
		document.CurrencyCode,
		billLineItemStatusFinal,
	)
	if err != nil {
		return nil, fmt.Errorf("list printable invoice service summaries: %w", err)
	}
	defer rows.Close()

	var summaries []InvoiceChargeSummary
	for rows.Next() {
		summary, err := scanInvoiceChargeSummary(rows)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate printable invoice service summaries: %w", err)
	}
	return summaries, nil
}

func (r InvoiceDocumentRepository) listPrintableAccountSummaries(ctx context.Context, document InvoiceDocument) ([]InvoiceAccountChargeSummary, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			usage_account_id,
			currency_code,
			COUNT(*),
			COALESCE(SUM(CASE WHEN line_item_type IN ('Usage', 'Fee') THEN unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN line_item_type = 'Credit' THEN unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN line_item_type = 'Refund' THEN unblended_cost_micros ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN line_item_type = 'Tax' THEN unblended_cost_micros ELSE 0 END), 0)
		 FROM bill_line_items
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND payer_account_id = ?
		   AND currency_code = ?
		   AND line_item_status = ?
		 GROUP BY usage_account_id, currency_code
		 ORDER BY usage_account_id`,
		document.BillingPeriodStart,
		document.BillingPeriodEnd,
		document.PayerAccountID,
		document.CurrencyCode,
		billLineItemStatusFinal,
	)
	if err != nil {
		return nil, fmt.Errorf("list printable invoice account summaries: %w", err)
	}
	defer rows.Close()

	var summaries []InvoiceAccountChargeSummary
	for rows.Next() {
		summary, err := scanInvoiceAccountChargeSummary(rows)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate printable invoice account summaries: %w", err)
	}
	return summaries, nil
}

func (r InvoiceDocumentRepository) listPrintableLineItems(ctx context.Context, document InvoiceDocument) ([]InvoiceLineItem, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			li.id,
			COALESCE(li.resource_id, ''),
			COALESCE(NULLIF(r.resource_name, ''), ''),
			li.usage_account_id,
			li.service_code,
			li.service_name,
			li.line_item_type,
			li.region_code,
			li.usage_type,
			li.operation,
			li.usage_start_time,
			li.usage_end_time,
			li.pricing_quantity_micros,
			li.pricing_unit,
			li.unblended_rate_micros,
			li.unblended_cost_micros,
			li.currency_code,
			li.description
		 FROM bill_line_items li
		 LEFT JOIN resources r ON r.id = li.resource_id
		 WHERE li.billing_period_start = ?
		   AND li.billing_period_end = ?
		   AND li.payer_account_id = ?
		   AND li.currency_code = ?
		   AND li.line_item_status = ?
		 ORDER BY
			li.service_code,
			li.usage_account_id,
			li.line_item_type,
			li.usage_start_time,
			li.id`,
		document.BillingPeriodStart,
		document.BillingPeriodEnd,
		document.PayerAccountID,
		document.CurrencyCode,
		billLineItemStatusFinal,
	)
	if err != nil {
		return nil, fmt.Errorf("list printable invoice line items: %w", err)
	}
	defer rows.Close()

	var items []InvoiceLineItem
	for rows.Next() {
		var item InvoiceLineItem
		if err := rows.Scan(
			&item.ID,
			&item.ResourceID,
			&item.ResourceName,
			&item.UsageAccountID,
			&item.ServiceCode,
			&item.ServiceName,
			&item.LineItemType,
			&item.RegionCode,
			&item.UsageType,
			&item.Operation,
			&item.UsageStartTime,
			&item.UsageEndTime,
			&item.PricingQuantityMicros,
			&item.PricingUnit,
			&item.UnblendedRateMicros,
			&item.UnblendedCostMicros,
			&item.CurrencyCode,
			&item.Description,
		); err != nil {
			return nil, fmt.Errorf("scan printable invoice line item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate printable invoice line items: %w", err)
	}
	return items, nil
}

func insertInvoiceDocument(ctx context.Context, tx *sql.Tx, document InvoiceDocument) error {
	if _, err := tx.ExecContext(ctx, invoiceDocumentInsertSQL, invoiceDocumentInsertArgs(document)...); err != nil {
		return fmt.Errorf("insert invoice document: %w", err)
	}
	return nil
}

func insertInvoiceDocumentIfMissing(ctx context.Context, tx *sql.Tx, document InvoiceDocument) (bool, error) {
	result, err := tx.ExecContext(ctx, invoiceDocumentInsertSQL+` ON CONFLICT(invoice_id) DO NOTHING`, invoiceDocumentInsertArgs(document)...)
	if err != nil {
		return false, fmt.Errorf("insert invoice document: %w", err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read invoice document insert count: %w", err)
	}
	return rowsAffected > 0, nil
}

type invoiceChargeSummaryRow interface {
	Scan(dest ...any) error
}

func scanInvoiceChargeSummary(row invoiceChargeSummaryRow) (InvoiceChargeSummary, error) {
	var summary InvoiceChargeSummary
	if err := row.Scan(
		&summary.ServiceCode,
		&summary.ServiceName,
		&summary.CurrencyCode,
		&summary.LineItemCount,
		&summary.ChargeMicros,
		&summary.CreditMicros,
		&summary.RefundMicros,
		&summary.TaxMicros,
	); err != nil {
		return InvoiceChargeSummary{}, fmt.Errorf("scan printable invoice service summary: %w", err)
	}
	summary.TotalMicros = billChargeTotalMicros(summary.ChargeMicros, summary.CreditMicros, summary.RefundMicros, summary.TaxMicros)
	return summary, nil
}

func scanInvoiceAccountChargeSummary(row invoiceChargeSummaryRow) (InvoiceAccountChargeSummary, error) {
	var summary InvoiceAccountChargeSummary
	if err := row.Scan(
		&summary.UsageAccountID,
		&summary.CurrencyCode,
		&summary.LineItemCount,
		&summary.ChargeMicros,
		&summary.CreditMicros,
		&summary.RefundMicros,
		&summary.TaxMicros,
	); err != nil {
		return InvoiceAccountChargeSummary{}, fmt.Errorf("scan printable invoice account summary: %w", err)
	}
	summary.TotalMicros = billChargeTotalMicros(summary.ChargeMicros, summary.CreditMicros, summary.RefundMicros, summary.TaxMicros)
	return summary, nil
}

func invoiceDocumentInsertArgs(document InvoiceDocument) []any {
	return []any{
		document.InvoiceID,
		document.BillID,
		document.InvoiceObligationID,
		document.DocumentVersion,
		document.Status,
		document.BillingPeriodStart,
		document.BillingPeriodEnd,
		document.InvoiceDate,
		document.DueDate,
		document.SellerOfRecord,
		document.SellerAddress,
		document.SellerTaxRegistration,
		document.PayerAccountID,
		document.BillToName,
		document.BillToEmail,
		document.BillToAddress,
		document.BillToTaxRegistration,
		document.CurrencyCode,
		document.LineItemCount,
		document.UsageChargeMicros,
		document.CreditMicros,
		document.RefundMicros,
		document.TaxMicros,
		document.TotalMicros,
	}
}

type invoiceDocumentRow interface {
	Scan(dest ...any) error
}

func scanInvoiceDocument(row invoiceDocumentRow) (InvoiceDocument, error) {
	var document InvoiceDocument
	if err := row.Scan(
		&document.InvoiceID,
		&document.BillID,
		&document.InvoiceObligationID,
		&document.DocumentVersion,
		&document.Status,
		&document.BillingPeriodStart,
		&document.BillingPeriodEnd,
		&document.InvoiceDate,
		&document.DueDate,
		&document.SellerOfRecord,
		&document.SellerAddress,
		&document.SellerTaxRegistration,
		&document.PayerAccountID,
		&document.BillToName,
		&document.BillToEmail,
		&document.BillToAddress,
		&document.BillToTaxRegistration,
		&document.CurrencyCode,
		&document.LineItemCount,
		&document.UsageChargeMicros,
		&document.CreditMicros,
		&document.RefundMicros,
		&document.TaxMicros,
		&document.TotalMicros,
		&document.CreatedAt,
		&document.UpdatedAt,
	); err != nil {
		return InvoiceDocument{}, fmt.Errorf("scan invoice document: %w", err)
	}
	return document, nil
}

const invoiceDocumentSelectSQL = `SELECT
			invoice_id,
			bill_id,
			invoice_obligation_id,
			document_version,
			status,
			billing_period_start,
			billing_period_end,
			invoice_date,
			due_date,
			seller_of_record,
			seller_address,
			seller_tax_registration,
			payer_account_id,
			bill_to_name,
			bill_to_email,
			bill_to_address,
			bill_to_tax_registration,
			currency_code,
			line_item_count,
			usage_charge_micros,
			credit_micros,
			refund_micros,
			tax_micros,
			total_micros,
			created_at,
			updated_at
		 FROM invoice_documents`

const invoiceDocumentInsertSQL = `INSERT INTO invoice_documents (
			invoice_id,
			bill_id,
			invoice_obligation_id,
			document_version,
			status,
			billing_period_start,
			billing_period_end,
			invoice_date,
			due_date,
			seller_of_record,
			seller_address,
			seller_tax_registration,
			payer_account_id,
			bill_to_name,
			bill_to_email,
			bill_to_address,
			bill_to_tax_registration,
			currency_code,
			line_item_count,
			usage_charge_micros,
			credit_micros,
			refund_micros,
			tax_micros,
			total_micros
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
