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

func TestInvoiceDocumentRepositoryBuildsPrintableInvoice(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	if _, err := usageRepo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-printable-invoice-ec2",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  serviceAmazonEC2,
		ResourceType: "ec2_instance",
		ResourceName: "Invoice web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource(EC2) error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-printable-invoice-ec2",
		ResourceID:          "resource-printable-invoice-ec2",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: 2_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(EC2) error = %v", err)
	}
	if _, err := usageRepo.CreateResource(ctx, ResourceCreateRequest{
		ID:           "resource-printable-invoice-s3",
		AccountID:    "222233334444",
		RegionCode:   "us-east-1",
		ServiceCode:  serviceAmazonS3,
		ResourceType: "s3_bucket",
		ResourceName: "Invoice receipts",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource(S3) error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, UsageEventCreateRequest{
		ID:                  "usage-printable-invoice-s3",
		ResourceID:          "resource-printable-invoice-s3",
		UsageType:           "requests:put-1k",
		Operation:           "PutObject",
		UsageStartTime:      "2026-02-02T00:00:00Z",
		UsageEndTime:        "2026-02-03T00:00:00Z",
		UsageQuantityMicros: 1_500_000_000,
		UsageUnit:           "Request",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(S3) error = %v", err)
	}
	if _, err := NewSimulatorClockRepository(db).Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}
	closeResult, err := NewMonthEndCloseRepository(db).ClosePreviousPeriod(ctx, MonthEndCloseRequest{
		PayerAccountID: "999988887777",
		InvoiceDueDays: 10,
	})
	if err != nil {
		t.Fatalf("ClosePreviousPeriod() error = %v", err)
	}

	printable, err := NewInvoiceDocumentRepository(db).GetPrintableByInvoiceID(ctx, closeResult.InvoiceDocument.InvoiceID)
	if err != nil {
		t.Fatalf("GetPrintableByInvoiceID() error = %v", err)
	}
	if printable.Document.InvoiceID != closeResult.InvoiceDocument.InvoiceID ||
		printable.Document.BillID != closeResult.Bill.ID ||
		printable.Obligation.Status != invoiceObligationStatusDue ||
		printable.Obligation.AmountDueMicros != 1_090_700 ||
		printable.Document.TotalMicros != 1_090_700 {
		t.Fatalf("printable invoice header/payment = %+v / %+v, want issued due document", printable.Document, printable.Obligation)
	}
	if len(printable.LineItems) != 3 || printable.Document.LineItemCount != 3 {
		t.Fatalf("printable invoice line count = %d document count %d, want three final line items", len(printable.LineItems), printable.Document.LineItemCount)
	}

	ec2 := requireInvoiceServiceSummary(t, printable.ServiceSummaries, serviceAmazonEC2)
	if ec2.LineItemCount != 1 || ec2.ChargeMicros != 83_200 || ec2.TotalMicros != 83_200 {
		t.Fatalf("EC2 invoice summary = %+v, want two t3.medium hours", ec2)
	}
	s3 := requireInvoiceServiceSummary(t, printable.ServiceSummaries, serviceAmazonS3)
	if s3.LineItemCount != 1 || s3.ChargeMicros != 7_500 || s3.TotalMicros != 7_500 {
		t.Fatalf("S3 invoice summary = %+v, want PUT request charge", s3)
	}
	support := requireInvoiceServiceSummary(t, printable.ServiceSummaries, serviceAWSSupport)
	if support.LineItemCount != 1 || support.ChargeMicros != 1_000_000 || support.TotalMicros != 1_000_000 {
		t.Fatalf("Support invoice summary = %+v, want minimum Business Support fee", support)
	}

	account1111 := requireInvoiceAccountSummary(t, printable.AccountSummaries, "111122223333")
	if account1111.LineItemCount != 1 || account1111.TotalMicros != 83_200 {
		t.Fatalf("account 111122223333 summary = %+v, want EC2 charge only", account1111)
	}
	account2222 := requireInvoiceAccountSummary(t, printable.AccountSummaries, "222233334444")
	if account2222.LineItemCount != 1 || account2222.TotalMicros != 7_500 {
		t.Fatalf("account 222233334444 summary = %+v, want S3 charge only", account2222)
	}
	payerAccount := requireInvoiceAccountSummary(t, printable.AccountSummaries, "999988887777")
	if payerAccount.LineItemCount != 1 || payerAccount.TotalMicros != 1_000_000 {
		t.Fatalf("payer account summary = %+v, want period-level Support fee", payerAccount)
	}

	supportLine := requireInvoiceLineItem(t, printable.LineItems, serviceAWSSupport)
	if supportLine.ResourceID != "" ||
		supportLine.LineItemType != billLineItemTypeFee ||
		supportLine.UsageAccountID != "999988887777" ||
		!strings.Contains(supportLine.Description, "AWS Support Business") {
		t.Fatalf("support invoice line = %+v, want period-level Support fee detail", supportLine)
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
	if _, err := repo.GetPrintableByInvoiceID(context.Background(), "SIM-INV-1"); err == nil {
		t.Fatal("GetPrintableByInvoiceID(nil DB) error = nil, want database handle validation error")
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

func requireInvoiceServiceSummary(t *testing.T, summaries []InvoiceChargeSummary, serviceCode string) InvoiceChargeSummary {
	t.Helper()

	for _, summary := range summaries {
		if summary.ServiceCode == serviceCode {
			return summary
		}
	}
	t.Fatalf("invoice service summaries = %+v, want service %s", summaries, serviceCode)
	return InvoiceChargeSummary{}
}

func requireInvoiceAccountSummary(t *testing.T, summaries []InvoiceAccountChargeSummary, usageAccountID string) InvoiceAccountChargeSummary {
	t.Helper()

	for _, summary := range summaries {
		if summary.UsageAccountID == usageAccountID {
			return summary
		}
	}
	t.Fatalf("invoice account summaries = %+v, want account %s", summaries, usageAccountID)
	return InvoiceAccountChargeSummary{}
}

func requireInvoiceLineItem(t *testing.T, items []InvoiceLineItem, serviceCode string) InvoiceLineItem {
	t.Helper()

	for _, item := range items {
		if item.ServiceCode == serviceCode {
			return item
		}
	}
	t.Fatalf("invoice line items = %+v, want service %s", items, serviceCode)
	return InvoiceLineItem{}
}
