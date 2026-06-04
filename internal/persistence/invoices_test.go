package persistence

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestInvoiceDocumentRepositoryCreatesDocumentForIssuedBill(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	bill, obligation := insertIssuedBillForInvoiceTest(t, ctx, db)
	repo := NewInvoiceDocumentRepository(db)

	document, err := repo.CreateForIssuedBill(ctx, bill, obligation)
	if err != nil {
		t.Fatalf("CreateForIssuedBill() error = %v", err)
	}
	requireInvoiceDocumentMatchesBill(t, document, bill, obligation)
	if document.CreatedAt == "" || document.UpdatedAt == "" {
		t.Fatalf("document timestamps = %q/%q, want database defaults", document.CreatedAt, document.UpdatedAt)
	}

	replay, err := repo.CreateForIssuedBill(ctx, bill, obligation)
	if err != nil {
		t.Fatalf("CreateForIssuedBill(replay) error = %v", err)
	}
	if replay.InvoiceID != document.InvoiceID || replay.CreatedAt != document.CreatedAt {
		t.Fatalf("CreateForIssuedBill(replay) = %+v, want existing document %+v", replay, document)
	}

	byBill, err := repo.GetByBillID(ctx, bill.ID)
	if err != nil {
		t.Fatalf("GetByBillID() error = %v", err)
	}
	if byBill.InvoiceID != document.InvoiceID {
		t.Fatalf("GetByBillID() = %+v, want invoice %s", byBill, document.InvoiceID)
	}
	recent, err := repo.ListRecent(ctx, 10)
	if err != nil {
		t.Fatalf("ListRecent() error = %v", err)
	}
	if len(recent) != 1 || recent[0].InvoiceID != document.InvoiceID {
		t.Fatalf("ListRecent() = %+v, want one created invoice document", recent)
	}
}

func TestInvoiceDocumentSchemaRejectsInvalidRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	bill, obligation := insertIssuedBillForInvoiceTest(t, ctx, db)

	document, err := invoiceDocumentFromBill(bill, obligation)
	if err != nil {
		t.Fatalf("invoiceDocumentFromBill() error = %v", err)
	}
	document.Status = "draft"
	if err := WithTransaction(ctx, db, func(tx *sql.Tx) error {
		return insertInvoiceDocument(ctx, tx, document)
	}); err == nil {
		t.Fatal("insertInvoiceDocument(invalid status) error = nil, want schema rejection")
	}

	document, err = invoiceDocumentFromBill(bill, obligation)
	if err != nil {
		t.Fatalf("invoiceDocumentFromBill(second) error = %v", err)
	}
	document.SellerOfRecord = ""
	if err := WithTransaction(ctx, db, func(tx *sql.Tx) error {
		return insertInvoiceDocument(ctx, tx, document)
	}); err == nil {
		t.Fatal("insertInvoiceDocument(blank seller) error = nil, want schema rejection")
	}
}

func TestInvoiceDocumentRepositoryRejectsNilDB(t *testing.T) {
	t.Parallel()

	repo := NewInvoiceDocumentRepository(nil)
	if _, err := repo.CreateForIssuedBill(context.Background(), Bill{}, InvoiceObligation{}); err == nil {
		t.Fatal("CreateForIssuedBill(nil DB) error = nil, want database handle validation error")
	}
	if _, err := repo.GetByBillID(context.Background(), "bill_1"); err == nil {
		t.Fatal("GetByBillID(nil DB) error = nil, want database handle validation error")
	}
	if _, err := repo.ListRecent(context.Background(), 10); err == nil {
		t.Fatal("ListRecent(nil DB) error = nil, want database handle validation error")
	}
}

func insertIssuedBillForInvoiceTest(t *testing.T, ctx context.Context, db *sql.DB) (Bill, InvoiceObligation) {
	t.Helper()

	close := BillingPeriodClose{
		ID:                     billingPeriodCloseID("2026-02-01", "2026-03-01", "999988887777"),
		BillingPeriodStart:     "2026-02-01",
		BillingPeriodEnd:       "2026-03-01",
		PayerAccountID:         "999988887777",
		Status:                 billingPeriodCloseStatusClosed,
		FinalizedLineItemCount: 3,
		FinalizedCostMicros:    2_050_000,
		CurrencyCode:           defaultBillCurrencyCode,
	}
	bill := Bill{
		ID:                 billID(close.BillingPeriodStart, close.BillingPeriodEnd, close.PayerAccountID, close.CurrencyCode),
		CloseID:            close.ID,
		BillingPeriodStart: close.BillingPeriodStart,
		BillingPeriodEnd:   close.BillingPeriodEnd,
		PayerAccountID:     close.PayerAccountID,
		BillState:          billStateIssued,
		CurrencyCode:       close.CurrencyCode,
		LineItemCount:      3,
		UsageChargeMicros:  2_000_000,
		CreditMicros:       250_000,
		TaxMicros:          300_000,
		TotalMicros:        2_050_000,
	}
	obligation := invoiceObligationFromBill(bill, 21)

	if err := WithTransaction(ctx, db, func(tx *sql.Tx) error {
		if err := insertBillingPeriodClose(ctx, tx, close); err != nil {
			return err
		}
		if err := insertBill(ctx, tx, bill); err != nil {
			return err
		}
		return insertInvoiceObligation(ctx, tx, obligation)
	}); err != nil {
		t.Fatalf("insert issued bill fixture: %v", err)
	}
	return bill, obligation
}

func requireInvoiceDocumentMatchesBill(t *testing.T, document InvoiceDocument, bill Bill, obligation InvoiceObligation) {
	t.Helper()

	if document.InvoiceID != obligation.InvoiceID ||
		document.BillID != bill.ID ||
		document.InvoiceObligationID != obligation.ID ||
		document.DocumentVersion != defaultInvoiceDocumentVersion ||
		document.Status != invoiceDocumentStatusIssued {
		t.Fatalf("document identity = %+v, want issued invoice for bill %+v and obligation %+v", document, bill, obligation)
	}
	if document.BillingPeriodStart != bill.BillingPeriodStart ||
		document.BillingPeriodEnd != bill.BillingPeriodEnd ||
		document.InvoiceDate != obligation.InvoiceDate ||
		document.DueDate != obligation.DueDate ||
		document.PayerAccountID != bill.PayerAccountID {
		t.Fatalf("document dates/payer = %+v, want bill and obligation values", document)
	}
	if document.CurrencyCode != bill.CurrencyCode ||
		document.LineItemCount != bill.LineItemCount ||
		document.UsageChargeMicros != bill.UsageChargeMicros ||
		document.CreditMicros != bill.CreditMicros ||
		document.RefundMicros != bill.RefundMicros ||
		document.TaxMicros != bill.TaxMicros ||
		document.TotalMicros != bill.TotalMicros {
		t.Fatalf("document totals = %+v, want bill totals %+v", document, bill)
	}
	if strings.TrimSpace(document.SellerOfRecord) == "" ||
		strings.TrimSpace(document.SellerAddress) == "" ||
		strings.TrimSpace(document.BillToName) == "" ||
		strings.TrimSpace(document.BillToEmail) == "" ||
		strings.TrimSpace(document.BillToAddress) == "" {
		t.Fatalf("document profile fields = %+v, want seller and bill-to defaults", document)
	}
}
