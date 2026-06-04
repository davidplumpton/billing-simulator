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
	document, err := invoiceDocumentFromBill(bill, obligation)
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

func invoiceDocumentFromBill(bill Bill, obligation InvoiceObligation) (InvoiceDocument, error) {
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
		SellerOfRecord:        defaultInvoiceSellerOfRecord,
		SellerAddress:         defaultInvoiceSellerAddress,
		SellerTaxRegistration: defaultInvoiceSellerTaxRegistration,
		PayerAccountID:        bill.PayerAccountID,
		BillToName:            defaultInvoiceBillToName,
		BillToEmail:           defaultInvoiceBillToEmail,
		BillToAddress:         defaultInvoiceBillToAddress,
		BillToTaxRegistration: defaultInvoiceBillToTaxRegistration,
		CurrencyCode:          bill.CurrencyCode,
		LineItemCount:         bill.LineItemCount,
		UsageChargeMicros:     bill.UsageChargeMicros,
		CreditMicros:          bill.CreditMicros,
		RefundMicros:          bill.RefundMicros,
		TaxMicros:             bill.TaxMicros,
		TotalMicros:           bill.TotalMicros,
	}, nil
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
