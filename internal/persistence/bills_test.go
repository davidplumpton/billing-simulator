package persistence

import (
	"context"
	"database/sql"
	"testing"
)

func TestBillsRepositoryListsOpenPendingAndStoredBillStates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	if _, err := NewSimulatorClockRepository(db).Set(ctx, "2026-03-15T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}

	recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-bills-february",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-bills-february",
			ResourceID:          "resource-bills-february",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	)
	recordAndPriceSingleUsage(t, ctx, db,
		ResourceCreateRequest{
			ID:           "resource-bills-march",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			Status:       "active",
		},
		UsageEventCreateRequest{
			ID:                  "usage-bills-march",
			ResourceID:          "resource-bills-march",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-03-02T00:00:00Z",
			UsageEndTime:        "2026-03-02T01:00:00Z",
			UsageQuantityMicros: 1_000_000,
			UsageUnit:           "Hours",
		},
	)

	insertStoredBillState(t, ctx, db, "2025-10-01", "2025-11-01", "111122223333", billStateIssued, invoiceObligationStatusDue, 1_000_000, 0, 0, 0)
	insertStoredBillState(t, ctx, db, "2025-11-01", "2025-12-01", "111122223333", "adjusted", invoiceObligationStatusDue, 3_000_000, 500_000, 0, 200_000)
	insertStoredBillState(t, ctx, db, "2025-12-01", "2026-01-01", "111122223333", "paid", "paid", 4_000_000, 0, 0, 0)
	insertStoredBillState(t, ctx, db, "2026-01-01", "2026-02-01", "111122223333", "past_due", "past_due", 5_000_000, 0, 0, 0)

	summaries, err := NewBillsRepository(db).ListBillStateSummaries(ctx, BillStateSummaryRequest{
		Limit:                 50,
		DefaultPayerAccountID: "111122223333",
	})
	if err != nil {
		t.Fatalf("ListBillStateSummaries() error = %v", err)
	}

	open := requireBillStateSummary(t, summaries, billStateOpen, "2026-03-01")
	if open.LineItemCount != 1 || open.UsageChargeMicros != 41_600 || open.TotalMicros != 41_600 {
		t.Fatalf("open summary = %+v, want current March estimated charge", open)
	}
	pending := requireBillStateSummary(t, summaries, billStatePendingClose, "2026-02-01")
	if pending.LineItemCount != 1 || pending.UsageChargeMicros != 83_200 || pending.TotalMicros != 83_200 {
		t.Fatalf("pending summary = %+v, want completed February estimated charge", pending)
	}
	issued := requireBillStateSummary(t, summaries, billStateIssued, "2025-10-01")
	if issued.InvoiceStatus != invoiceObligationStatusDue || issued.TotalMicros != 1_000_000 {
		t.Fatalf("issued summary = %+v, want due issued bill", issued)
	}
	adjusted := requireBillStateSummary(t, summaries, "adjusted", "2025-11-01")
	if adjusted.UsageChargeMicros != 3_000_000 || adjusted.CreditMicros != 500_000 || adjusted.TaxMicros != 200_000 || adjusted.TotalMicros != 2_700_000 {
		t.Fatalf("adjusted summary = %+v, want charges, credits, tax, and adjusted total", adjusted)
	}
	paid := requireBillStateSummary(t, summaries, "paid", "2025-12-01")
	if paid.InvoiceStatus != "paid" || paid.InvoiceAmountPaidMicros != 4_000_000 || paid.InvoiceAmountDueMicros != 0 {
		t.Fatalf("paid summary = %+v, want paid invoice obligation", paid)
	}
	pastDue := requireBillStateSummary(t, summaries, "past_due", "2026-01-01")
	if pastDue.InvoiceStatus != "past_due" || pastDue.TotalMicros != 5_000_000 {
		t.Fatalf("past-due summary = %+v, want past-due bill", pastDue)
	}
}

func TestBillsRepositoryAddsEmptyOpenSummaryForFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)

	summaries, err := NewBillsRepository(db).ListBillStateSummaries(ctx, BillStateSummaryRequest{
		DefaultPayerAccountID: "111122223333",
	})
	if err != nil {
		t.Fatalf("ListBillStateSummaries() error = %v", err)
	}
	open := requireBillStateSummary(t, summaries, billStateOpen, "2026-02-01")
	if open.LineItemCount != 0 || open.TotalMicros != 0 || open.PayerAccountID != "111122223333" {
		t.Fatalf("empty open summary = %+v, want zero-dollar current period", open)
	}
}

func TestBillsRepositoryRejectsNilDB(t *testing.T) {
	t.Parallel()

	if _, err := NewBillsRepository(nil).ListBillStateSummaries(context.Background(), BillStateSummaryRequest{}); err == nil {
		t.Fatal("ListBillStateSummaries(nil DB) error = nil, want database handle validation error")
	}
}

func insertStoredBillState(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	periodStart string,
	periodEnd string,
	payerAccountID string,
	billState string,
	invoiceStatus string,
	usageChargeMicros int64,
	creditMicros int64,
	refundMicros int64,
	taxMicros int64,
) {
	t.Helper()

	totalMicros := usageChargeMicros + taxMicros - creditMicros - refundMicros
	if totalMicros < 0 {
		totalMicros = 0
	}
	close := BillingPeriodClose{
		ID:                     billingPeriodCloseID(periodStart, periodEnd, payerAccountID),
		BillingPeriodStart:     periodStart,
		BillingPeriodEnd:       periodEnd,
		PayerAccountID:         payerAccountID,
		Status:                 billingPeriodCloseStatusClosed,
		FinalizedLineItemCount: 1,
		FinalizedCostMicros:    totalMicros,
		CurrencyCode:           defaultBillCurrencyCode,
	}
	bill := Bill{
		ID:                 billID(periodStart, periodEnd, payerAccountID, defaultBillCurrencyCode),
		CloseID:            close.ID,
		BillingPeriodStart: periodStart,
		BillingPeriodEnd:   periodEnd,
		PayerAccountID:     payerAccountID,
		BillState:          billState,
		CurrencyCode:       defaultBillCurrencyCode,
		LineItemCount:      1,
		UsageChargeMicros:  usageChargeMicros,
		CreditMicros:       creditMicros,
		RefundMicros:       refundMicros,
		TaxMicros:          taxMicros,
		TotalMicros:        totalMicros,
	}
	obligation := invoiceObligationFromBill(bill, defaultInvoiceObligationDueDay)
	obligation.Status = invoiceStatus
	if invoiceStatus == "paid" {
		obligation.AmountPaidMicros = totalMicros
		obligation.AmountDueMicros = 0
	}

	if err := WithTransaction(ctx, db, func(tx *sql.Tx) error {
		if err := insertBillingPeriodClose(ctx, tx, close); err != nil {
			return err
		}
		if err := insertBill(ctx, tx, bill); err != nil {
			return err
		}
		return insertInvoiceObligation(ctx, tx, obligation)
	}); err != nil {
		t.Fatalf("insert stored bill state %s: %v", billState, err)
	}
}

func requireBillStateSummary(t *testing.T, summaries []BillStateSummary, state, periodStart string) BillStateSummary {
	t.Helper()

	for _, summary := range summaries {
		if summary.BillState == state && summary.BillingPeriodStart == periodStart {
			return summary
		}
	}
	t.Fatalf("summaries = %+v, want state %s for period %s", summaries, state, periodStart)
	return BillStateSummary{}
}
