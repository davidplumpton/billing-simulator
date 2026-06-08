package persistence

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestPaymentLifecycleRepositoryMovesObligationThroughPaymentStates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	bill, obligation := insertIssuedBillForInvoiceTest(t, ctx, db)
	repo := NewPaymentLifecycleRepository(db)

	scheduled, err := repo.SchedulePayment(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		OccurredAt:          "2026-03-02",
	})
	if err != nil {
		t.Fatalf("SchedulePayment() error = %v", err)
	}
	if scheduled.Obligation.Status != invoiceObligationStatusScheduled ||
		scheduled.Obligation.AmountDueMicros != bill.TotalMicros ||
		scheduled.Event.FromStatus != invoiceObligationStatusDue ||
		scheduled.Event.ToStatus != invoiceObligationStatusScheduled {
		t.Fatalf("SchedulePayment() = %+v, want scheduled with original due amount", scheduled)
	}

	processing, err := repo.StartProcessing(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		OccurredAt:          "2026-03-03",
	})
	if err != nil {
		t.Fatalf("StartProcessing() error = %v", err)
	}
	if processing.Obligation.Status != invoiceObligationStatusProcessing {
		t.Fatalf("StartProcessing() status = %q, want processing", processing.Obligation.Status)
	}

	partial, err := repo.ApplyPayment(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		AmountMicros:        500_000,
		Reason:              "Manual partial remittance",
		OccurredAt:          "2026-03-04",
	})
	if err != nil {
		t.Fatalf("ApplyPayment(partial) error = %v", err)
	}
	if partial.Obligation.Status != invoiceObligationStatusPartiallyPaid ||
		partial.Obligation.AmountPaidMicros != 500_000 ||
		partial.Obligation.AmountDueMicros != bill.TotalMicros-500_000 ||
		partial.Event.AmountMicros != 500_000 {
		t.Fatalf("ApplyPayment(partial) = %+v, want partially paid balance", partial)
	}

	retry, err := repo.StartProcessing(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		OccurredAt:          "2026-03-05",
	})
	if err != nil {
		t.Fatalf("StartProcessing(retry) error = %v", err)
	}
	if retry.Obligation.Status != invoiceObligationStatusProcessing {
		t.Fatalf("StartProcessing(retry) status = %q, want processing", retry.Obligation.Status)
	}

	succeeded, err := repo.ApplyPayment(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		AmountMicros:        bill.TotalMicros - 500_000,
		OccurredAt:          "2026-03-06",
	})
	if err != nil {
		t.Fatalf("ApplyPayment(final) error = %v", err)
	}
	if succeeded.Obligation.Status != invoiceObligationStatusSucceeded ||
		succeeded.Obligation.AmountDueMicros != 0 ||
		succeeded.Obligation.AmountPaidMicros != bill.TotalMicros {
		t.Fatalf("ApplyPayment(final) = %+v, want succeeded paid balance", succeeded)
	}

	summary := requireBillStateSummaryFromDB(t, ctx, db, bill.ID)
	if summary.BillState != billStatePaid || summary.InvoiceStatus != invoiceObligationStatusSucceeded || summary.InvoiceAmountDueMicros != 0 {
		t.Fatalf("bill summary after payment = %+v, want paid bill with succeeded invoice", summary)
	}

	events, err := repo.ListEvents(ctx, obligation.ID, 10)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 6 ||
		events[0].ToStatus != invoiceObligationStatusSucceeded ||
		events[len(events)-1].TransitionKind != paymentTransitionCreated {
		t.Fatalf("events = %+v, want initial plus scheduled/processing/partial/retry/succeeded history", events)
	}
}

func TestPaymentLifecycleRepositoryHandlesFailurePastDueAndRefund(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	bill, obligation := insertIssuedBillForInvoiceTest(t, ctx, db)
	repo := NewPaymentLifecycleRepository(db)

	if _, err := repo.SchedulePayment(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		OccurredAt:          "2026-03-02",
	}); err != nil {
		t.Fatalf("SchedulePayment() error = %v", err)
	}
	if _, err := repo.StartProcessing(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		OccurredAt:          "2026-03-03",
	}); err != nil {
		t.Fatalf("StartProcessing() error = %v", err)
	}
	failed, err := repo.FailPayment(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		Reason:              "card expired",
		OccurredAt:          "2026-03-04",
	})
	if err != nil {
		t.Fatalf("FailPayment() error = %v", err)
	}
	if failed.Obligation.Status != invoiceObligationStatusFailed || failed.Event.Reason != "card expired" {
		t.Fatalf("FailPayment() = %+v, want failed event with reason", failed)
	}

	pastDue, err := repo.MarkPastDue(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		OccurredAt:          "2026-03-23",
	})
	if err != nil {
		t.Fatalf("MarkPastDue() error = %v", err)
	}
	if pastDue.Obligation.Status != invoiceObligationStatusPastDue ||
		requireBillStateSummaryFromDB(t, ctx, db, bill.ID).BillState != billStatePastDue {
		t.Fatalf("MarkPastDue() = %+v, want past-due obligation and bill", pastDue)
	}

	if _, err := repo.StartProcessing(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		OccurredAt:          "2026-03-24",
	}); err != nil {
		t.Fatalf("StartProcessing(retry) error = %v", err)
	}
	if _, err := repo.ApplyPayment(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		AmountMicros:        bill.TotalMicros,
		OccurredAt:          "2026-03-25",
	}); err != nil {
		t.Fatalf("ApplyPayment(full) error = %v", err)
	}
	refunded, err := repo.RefundPayment(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		AmountMicros:        bill.TotalMicros,
		Reason:              "duplicate remittance returned",
		OccurredAt:          "2026-03-26",
	})
	if err != nil {
		t.Fatalf("RefundPayment() error = %v", err)
	}
	if refunded.Obligation.Status != invoiceObligationStatusRefunded ||
		refunded.Obligation.AmountDueMicros != bill.TotalMicros ||
		refunded.Obligation.AmountPaidMicros != 0 {
		t.Fatalf("RefundPayment() = %+v, want refunded obligation with due balance restored", refunded)
	}
}

func TestPaymentLifecycleRepositoryPreservesPastDueBillAfterPartialPayment(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	bill, obligation := insertIssuedBillForInvoiceTest(t, ctx, db)
	repo := NewPaymentLifecycleRepository(db)

	if _, err := repo.MarkPastDue(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		OccurredAt:          "2026-03-23",
	}); err != nil {
		t.Fatalf("MarkPastDue() error = %v", err)
	}
	partial, err := repo.ApplyPayment(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		AmountMicros:        500_000,
		Reason:              "Partial past-due remittance",
		OccurredAt:          "2026-03-24",
	})
	if err != nil {
		t.Fatalf("ApplyPayment(partial past-due) error = %v", err)
	}
	if partial.Obligation.Status != invoiceObligationStatusPartiallyPaid ||
		partial.Obligation.AmountDueMicros != bill.TotalMicros-500_000 ||
		partial.Obligation.AmountPaidMicros != 500_000 {
		t.Fatalf("ApplyPayment(partial past-due) = %+v, want partially paid remaining balance", partial)
	}

	summary := requireBillStateSummaryFromDB(t, ctx, db, bill.ID)
	if summary.BillState != billStatePastDue ||
		summary.InvoiceStatus != invoiceObligationStatusPartiallyPaid ||
		summary.InvoiceAmountDueMicros != bill.TotalMicros-500_000 ||
		summary.InvoiceAmountPaidMicros != 500_000 {
		t.Fatalf("bill summary after partial past-due payment = %+v, want past-due bill with partial invoice balance", summary)
	}

	due, err := repo.MarkDue(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		Reason:              "Resume ordinary collections on remaining balance",
		OccurredAt:          "2026-03-25",
	})
	if err != nil {
		t.Fatalf("MarkDue(partially paid) error = %v", err)
	}
	if due.Obligation.Status != invoiceObligationStatusDue ||
		due.Obligation.AmountDueMicros != bill.TotalMicros-500_000 ||
		due.Obligation.AmountPaidMicros != 500_000 ||
		due.Event.FromStatus != invoiceObligationStatusPartiallyPaid ||
		due.Event.ToStatus != invoiceObligationStatusDue {
		t.Fatalf("MarkDue(partially paid) = %+v, want due state preserving partial balance", due)
	}

	summary = requireBillStateSummaryFromDB(t, ctx, db, bill.ID)
	if summary.BillState != billStateIssued ||
		summary.InvoiceStatus != invoiceObligationStatusDue ||
		summary.InvoiceAmountDueMicros != bill.TotalMicros-500_000 ||
		summary.InvoiceAmountPaidMicros != 500_000 {
		t.Fatalf("bill summary after marking partial payment due = %+v, want issued bill with due partial invoice balance", summary)
	}
}

func TestPaymentLifecycleRepositoryRejectsInvalidTransitions(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	_, obligation := insertIssuedBillForInvoiceTest(t, ctx, db)
	repo := NewPaymentLifecycleRepository(db)

	_, err := repo.MarkPastDue(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		OccurredAt:          "2026-03-10",
	})
	if err == nil || !strings.Contains(err.Error(), "not past due") {
		t.Fatalf("MarkPastDue(before due date) error = %v, want not-past-due validation", err)
	}

	if _, err := repo.SchedulePayment(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		OccurredAt:          "2026-03-02",
	}); err != nil {
		t.Fatalf("SchedulePayment() error = %v", err)
	}
	_, err = repo.ApplyPayment(ctx, PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligation.ID,
		AmountMicros:        1,
		OccurredAt:          "2026-03-03",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot transition") {
		t.Fatalf("ApplyPayment(from scheduled) error = %v, want transition validation", err)
	}
}

func requireBillStateSummaryFromDB(t *testing.T, ctx context.Context, db *sql.DB, billID string) BillStateSummary {
	t.Helper()

	summaries, err := NewBillsRepository(db).ListBillStateSummaries(ctx, BillStateSummaryRequest{
		Limit:                 50,
		DefaultPayerAccountID: "999988887777",
	})
	if err != nil {
		t.Fatalf("ListBillStateSummaries() error = %v", err)
	}
	for _, summary := range summaries {
		if summary.ID == billID {
			return summary
		}
	}
	t.Fatalf("bill summaries = %+v, want bill %s", summaries, billID)
	return BillStateSummary{}
}
