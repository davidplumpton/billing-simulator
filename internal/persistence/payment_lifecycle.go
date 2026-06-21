package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	defaultPaymentLifecycleEventLimit = 25
	maxPaymentLifecycleEventLimit     = 100

	invoiceObligationStatusScheduled     = "scheduled"
	invoiceObligationStatusProcessing    = "processing"
	invoiceObligationStatusSucceeded     = "succeeded"
	invoiceObligationStatusFailed        = "failed"
	invoiceObligationStatusPastDue       = "past_due"
	invoiceObligationStatusPartiallyPaid = "partially_paid"
	invoiceObligationStatusRefunded      = "refunded"
	legacyInvoiceObligationStatusPaid    = "paid"

	paymentTransitionCreated       = "created"
	paymentTransitionDue           = "due"
	paymentTransitionScheduled     = "scheduled"
	paymentTransitionProcessing    = "processing"
	paymentTransitionSucceeded     = "succeeded"
	paymentTransitionFailed        = "failed"
	paymentTransitionPastDue       = "past_due"
	paymentTransitionPartiallyPaid = "partially_paid"
	paymentTransitionRefunded      = "refunded"

	billStatePaid    = "paid"
	billStatePastDue = "past_due"
)

// PaymentLifecycleTransitionRequest identifies one invoice obligation state transition.
type PaymentLifecycleTransitionRequest struct {
	InvoiceObligationID string
	AmountMicros        int64
	Reason              string
	OccurredAt          string
}

// PaymentLifecycleEvent stores one durable payment state transition for an invoice obligation.
type PaymentLifecycleEvent struct {
	ID                  string
	InvoiceObligationID string
	TransitionKind      string
	FromStatus          string
	ToStatus            string
	AmountMicros        int64
	CurrencyCode        string
	Reason              string
	OccurredAt          string
	CreatedAt           string
}

// PaymentLifecycleResult returns the updated obligation and the event written for a transition.
type PaymentLifecycleResult struct {
	Obligation InvoiceObligation
	Event      PaymentLifecycleEvent
}

// PaymentLifecycleRepository moves issued invoice obligations through simulated payment states.
type PaymentLifecycleRepository struct {
	db *sql.DB
}

// NewPaymentLifecycleRepository creates a payment lifecycle repository backed by a workspace database.
func NewPaymentLifecycleRepository(db *sql.DB) PaymentLifecycleRepository {
	return PaymentLifecycleRepository{db: db}
}

// GetObligation reads one invoice obligation with its current payment lifecycle state.
func (r PaymentLifecycleRepository) GetObligation(ctx context.Context, invoiceObligationID string) (InvoiceObligation, error) {
	if r.db == nil {
		return InvoiceObligation{}, fmt.Errorf("database handle is required")
	}
	invoiceObligationID = strings.TrimSpace(invoiceObligationID)
	if invoiceObligationID == "" {
		return InvoiceObligation{}, fmt.Errorf("invoice obligation ID is required")
	}
	obligation, err := getInvoiceObligationWithPaymentState(ctx, r.db, `o.id = ?`, invoiceObligationID)
	if err != nil {
		return InvoiceObligation{}, fmt.Errorf("get invoice obligation %q: %w", invoiceObligationID, err)
	}
	return obligation, nil
}

// ListEvents reads recent payment lifecycle events for one invoice obligation.
func (r PaymentLifecycleRepository) ListEvents(ctx context.Context, invoiceObligationID string, limit int) ([]PaymentLifecycleEvent, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	invoiceObligationID = strings.TrimSpace(invoiceObligationID)
	if invoiceObligationID == "" {
		return nil, fmt.Errorf("invoice obligation ID is required")
	}
	if limit <= 0 {
		limit = defaultPaymentLifecycleEventLimit
	}
	if limit > maxPaymentLifecycleEventLimit {
		limit = maxPaymentLifecycleEventLimit
	}
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			invoice_obligation_id,
			transition_kind,
			from_status,
			to_status,
			amount_micros,
			currency_code,
			reason,
			occurred_at,
			created_at
		 FROM invoice_payment_events
		 WHERE invoice_obligation_id = ?
		 ORDER BY occurred_at DESC, created_at DESC, id DESC
		 LIMIT ?`,
		invoiceObligationID,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list payment lifecycle events: %w", err)
	}
	defer rows.Close()

	var events []PaymentLifecycleEvent
	for rows.Next() {
		event, err := scanPaymentLifecycleEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate payment lifecycle events: %w", err)
	}
	return events, nil
}

// SchedulePayment marks an unpaid obligation as scheduled for collection.
func (r PaymentLifecycleRepository) SchedulePayment(ctx context.Context, request PaymentLifecycleTransitionRequest) (PaymentLifecycleResult, error) {
	return r.transition(ctx, request, func(current InvoiceObligation, request PaymentLifecycleTransitionRequest) (paymentLifecycleUpdate, error) {
		if current.AmountDueMicros == 0 {
			return paymentLifecycleUpdate{}, fmt.Errorf("invoice obligation %q has no amount due", current.ID)
		}
		return paymentLifecycleUpdate{
			Status:           invoiceObligationStatusScheduled,
			AmountDueMicros:  current.AmountDueMicros,
			AmountPaidMicros: current.AmountPaidMicros,
			TransitionKind:   paymentTransitionScheduled,
			Reason:           strings.TrimSpace(request.Reason),
			OccurredAt:       request.OccurredAt,
		}, nil
	})
}

// MarkDue returns an unpaid obligation to the ordinary due state after a cancellation or profile fix.
func (r PaymentLifecycleRepository) MarkDue(ctx context.Context, request PaymentLifecycleTransitionRequest) (PaymentLifecycleResult, error) {
	return r.transition(ctx, request, func(current InvoiceObligation, request PaymentLifecycleTransitionRequest) (paymentLifecycleUpdate, error) {
		if current.AmountDueMicros == 0 {
			return paymentLifecycleUpdate{}, fmt.Errorf("invoice obligation %q has no amount due", current.ID)
		}
		return paymentLifecycleUpdate{
			Status:           invoiceObligationStatusDue,
			AmountDueMicros:  current.AmountDueMicros,
			AmountPaidMicros: current.AmountPaidMicros,
			TransitionKind:   paymentTransitionDue,
			Reason:           strings.TrimSpace(request.Reason),
			OccurredAt:       request.OccurredAt,
		}, nil
	})
}

// StartProcessing marks an unpaid obligation as actively processing a payment.
func (r PaymentLifecycleRepository) StartProcessing(ctx context.Context, request PaymentLifecycleTransitionRequest) (PaymentLifecycleResult, error) {
	return r.transition(ctx, request, func(current InvoiceObligation, request PaymentLifecycleTransitionRequest) (paymentLifecycleUpdate, error) {
		if current.AmountDueMicros == 0 {
			return paymentLifecycleUpdate{}, fmt.Errorf("invoice obligation %q has no amount due", current.ID)
		}
		return paymentLifecycleUpdate{
			Status:           invoiceObligationStatusProcessing,
			AmountDueMicros:  current.AmountDueMicros,
			AmountPaidMicros: current.AmountPaidMicros,
			TransitionKind:   paymentTransitionProcessing,
			Reason:           strings.TrimSpace(request.Reason),
			OccurredAt:       request.OccurredAt,
		}, nil
	})
}

// FailPayment records a failed payment attempt while preserving the outstanding amount.
func (r PaymentLifecycleRepository) FailPayment(ctx context.Context, request PaymentLifecycleTransitionRequest) (PaymentLifecycleResult, error) {
	return r.transition(ctx, request, func(current InvoiceObligation, request PaymentLifecycleTransitionRequest) (paymentLifecycleUpdate, error) {
		if current.AmountDueMicros == 0 {
			return paymentLifecycleUpdate{}, fmt.Errorf("invoice obligation %q has no amount due", current.ID)
		}
		reason := strings.TrimSpace(request.Reason)
		if reason == "" {
			reason = "Payment attempt failed"
		}
		return paymentLifecycleUpdate{
			Status:           invoiceObligationStatusFailed,
			AmountDueMicros:  current.AmountDueMicros,
			AmountPaidMicros: current.AmountPaidMicros,
			TransitionKind:   paymentTransitionFailed,
			Reason:           reason,
			OccurredAt:       request.OccurredAt,
		}, nil
	})
}

// MarkPastDue moves an unpaid obligation past due once the effective date is after its due date.
func (r PaymentLifecycleRepository) MarkPastDue(ctx context.Context, request PaymentLifecycleTransitionRequest) (PaymentLifecycleResult, error) {
	return r.transition(ctx, request, func(current InvoiceObligation, request PaymentLifecycleTransitionRequest) (paymentLifecycleUpdate, error) {
		if current.AmountDueMicros == 0 {
			return paymentLifecycleUpdate{}, fmt.Errorf("invoice obligation %q has no amount due", current.ID)
		}
		if err := validatePastDueDate(current, request.OccurredAt); err != nil {
			return paymentLifecycleUpdate{}, err
		}
		return paymentLifecycleUpdate{
			Status:           invoiceObligationStatusPastDue,
			AmountDueMicros:  current.AmountDueMicros,
			AmountPaidMicros: current.AmountPaidMicros,
			TransitionKind:   paymentTransitionPastDue,
			Reason:           strings.TrimSpace(request.Reason),
			OccurredAt:       request.OccurredAt,
		}, nil
	})
}

// ApplyPayment records a simulated payment against an obligation and derives partial or successful state.
func (r PaymentLifecycleRepository) ApplyPayment(ctx context.Context, request PaymentLifecycleTransitionRequest) (PaymentLifecycleResult, error) {
	return r.transition(ctx, request, func(current InvoiceObligation, request PaymentLifecycleTransitionRequest) (paymentLifecycleUpdate, error) {
		if request.AmountMicros <= 0 {
			return paymentLifecycleUpdate{}, fmt.Errorf("payment amount must be positive")
		}
		if current.AmountDueMicros == 0 {
			return paymentLifecycleUpdate{}, fmt.Errorf("invoice obligation %q has no amount due", current.ID)
		}
		if request.AmountMicros > current.AmountDueMicros {
			return paymentLifecycleUpdate{}, fmt.Errorf("payment amount exceeds amount due")
		}
		amountDue := current.AmountDueMicros - request.AmountMicros
		amountPaid := current.AmountPaidMicros + request.AmountMicros
		status := invoiceObligationStatusPartiallyPaid
		transition := paymentTransitionPartiallyPaid
		if amountDue == 0 {
			status = invoiceObligationStatusSucceeded
			transition = paymentTransitionSucceeded
		}
		return paymentLifecycleUpdate{
			Status:            status,
			AmountDueMicros:   amountDue,
			AmountPaidMicros:  amountPaid,
			TransitionKind:    transition,
			EventAmountMicros: request.AmountMicros,
			Reason:            strings.TrimSpace(request.Reason),
			OccurredAt:        request.OccurredAt,
		}, nil
	})
}

// RefundPayment records returned funds and marks the obligation as refunded.
func (r PaymentLifecycleRepository) RefundPayment(ctx context.Context, request PaymentLifecycleTransitionRequest) (PaymentLifecycleResult, error) {
	return r.transition(ctx, request, func(current InvoiceObligation, request PaymentLifecycleTransitionRequest) (paymentLifecycleUpdate, error) {
		if request.AmountMicros <= 0 {
			return paymentLifecycleUpdate{}, fmt.Errorf("refund amount must be positive")
		}
		if current.AmountPaidMicros == 0 {
			return paymentLifecycleUpdate{}, fmt.Errorf("invoice obligation %q has no paid amount to refund", current.ID)
		}
		if request.AmountMicros > current.AmountPaidMicros {
			return paymentLifecycleUpdate{}, fmt.Errorf("refund amount exceeds amount paid")
		}
		return paymentLifecycleUpdate{
			Status:            invoiceObligationStatusRefunded,
			AmountDueMicros:   current.AmountDueMicros + request.AmountMicros,
			AmountPaidMicros:  current.AmountPaidMicros - request.AmountMicros,
			TransitionKind:    paymentTransitionRefunded,
			EventAmountMicros: request.AmountMicros,
			Reason:            strings.TrimSpace(request.Reason),
			OccurredAt:        request.OccurredAt,
		}, nil
	})
}

type paymentLifecycleUpdate struct {
	Status            string
	AmountDueMicros   int64
	AmountPaidMicros  int64
	TransitionKind    string
	EventAmountMicros int64
	Reason            string
	OccurredAt        string
}

type paymentLifecycleMutator func(InvoiceObligation, PaymentLifecycleTransitionRequest) (paymentLifecycleUpdate, error)

func (r PaymentLifecycleRepository) transition(ctx context.Context, request PaymentLifecycleTransitionRequest, mutate paymentLifecycleMutator) (PaymentLifecycleResult, error) {
	if r.db == nil {
		return PaymentLifecycleResult{}, fmt.Errorf("database handle is required")
	}
	if mutate == nil {
		return PaymentLifecycleResult{}, fmt.Errorf("payment lifecycle transition is required")
	}
	request, err := normalizePaymentLifecycleTransitionRequest(request)
	if err != nil {
		return PaymentLifecycleResult{}, err
	}

	var result PaymentLifecycleResult
	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		current, err := getInvoiceObligationWithPaymentState(ctx, tx, `o.id = ?`, request.InvoiceObligationID)
		if err != nil {
			return fmt.Errorf("get invoice obligation %q: %w", request.InvoiceObligationID, err)
		}
		current.Status = canonicalInvoicePaymentStatus(current.Status)

		update, err := mutate(current, request)
		if err != nil {
			return err
		}
		update.Status = canonicalInvoicePaymentStatus(update.Status)
		if err := validateInvoicePaymentStatus(update.Status); err != nil {
			return err
		}
		if err := validateInvoicePaymentTransition(current.Status, update.Status); err != nil {
			return err
		}

		event := paymentLifecycleEventFromUpdate(current, update)
		if err := persistPaymentLifecycleUpdate(ctx, tx, current, update); err != nil {
			return err
		}
		if err := insertPaymentLifecycleEvent(ctx, tx, event); err != nil {
			return err
		}
		updated, err := getInvoiceObligationWithPaymentState(ctx, tx, `o.id = ?`, current.ID)
		if err != nil {
			return fmt.Errorf("get updated invoice obligation %q: %w", current.ID, err)
		}
		result = PaymentLifecycleResult{
			Obligation: updated,
			Event:      event,
		}
		return nil
	})
	if err != nil {
		return PaymentLifecycleResult{}, err
	}
	return result, nil
}

func normalizePaymentLifecycleTransitionRequest(request PaymentLifecycleTransitionRequest) (PaymentLifecycleTransitionRequest, error) {
	request.InvoiceObligationID = strings.TrimSpace(request.InvoiceObligationID)
	if request.InvoiceObligationID == "" {
		return PaymentLifecycleTransitionRequest{}, fmt.Errorf("invoice obligation ID is required")
	}
	request.Reason = strings.TrimSpace(request.Reason)
	occurredAt, err := normalizePaymentLifecycleTimestamp(request.OccurredAt)
	if err != nil {
		return PaymentLifecycleTransitionRequest{}, err
	}
	request.OccurredAt = occurredAt
	return request, nil
}

func normalizePaymentLifecycleTimestamp(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("payment event timestamp is required")
	}
	if _, err := time.Parse(time.DateOnly, value); err == nil {
		return value, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return "", fmt.Errorf("payment event timestamp %q must be YYYY-MM-DD or RFC3339", value)
	}
	return parsed.UTC().Format(time.RFC3339), nil
}

func validatePastDueDate(obligation InvoiceObligation, occurredAt string) error {
	effectiveDate, err := paymentLifecycleDate(occurredAt)
	if err != nil {
		return err
	}
	dueDate, err := time.Parse(time.DateOnly, obligation.DueDate)
	if err != nil {
		return fmt.Errorf("invoice obligation %q has invalid due date %q", obligation.ID, obligation.DueDate)
	}
	if !effectiveDate.After(dueDate) {
		return domainErrorf(ErrInvoicePaymentNotPastDue, "invoice obligation %q is not past due until after %s", obligation.ID, obligation.DueDate)
	}
	return nil
}

func paymentLifecycleDate(value string) (time.Time, error) {
	if len(value) == len(time.DateOnly) {
		parsed, err := time.Parse(time.DateOnly, value)
		if err != nil {
			return time.Time{}, fmt.Errorf("payment event date %q is invalid", value)
		}
		return parsed, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("payment event timestamp %q is invalid", value)
	}
	return time.Parse(time.DateOnly, parsed.UTC().Format(time.DateOnly))
}

func paymentLifecycleEventFromUpdate(current InvoiceObligation, update paymentLifecycleUpdate) PaymentLifecycleEvent {
	event := PaymentLifecycleEvent{
		InvoiceObligationID: current.ID,
		TransitionKind:      update.TransitionKind,
		FromStatus:          current.Status,
		ToStatus:            update.Status,
		AmountMicros:        update.EventAmountMicros,
		CurrencyCode:        current.CurrencyCode,
		Reason:              update.Reason,
		OccurredAt:          update.OccurredAt,
	}
	event.ID = paymentLifecycleEventID(current, update)
	return event
}

func paymentLifecycleEventID(current InvoiceObligation, update paymentLifecycleUpdate) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		current.ID,
		current.Status,
		update.Status,
		update.TransitionKind,
		fmt.Sprintf("%d", current.AmountDueMicros),
		fmt.Sprintf("%d", current.AmountPaidMicros),
		fmt.Sprintf("%d", update.AmountDueMicros),
		fmt.Sprintf("%d", update.AmountPaidMicros),
		fmt.Sprintf("%d", update.EventAmountMicros),
		update.Reason,
		update.OccurredAt,
	}, "\x00")))
	return "payevt_" + hex.EncodeToString(sum[:8])
}

func persistPaymentLifecycleUpdate(ctx context.Context, tx *sql.Tx, current InvoiceObligation, update paymentLifecycleUpdate) error {
	legacyStatus := legacyInvoiceObligationStatus(update.Status)
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE invoice_obligations
		    SET status = ?,
		        amount_due_micros = ?,
		        amount_paid_micros = ?,
		        updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		  WHERE id = ?`,
		legacyStatus,
		update.AmountDueMicros,
		update.AmountPaidMicros,
		current.ID,
	); err != nil {
		return fmt.Errorf("update invoice obligation payment totals: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO invoice_payment_states (
			invoice_obligation_id,
			status,
			amount_due_micros,
			amount_paid_micros,
			currency_code,
			last_failure_reason,
			updated_at
		 ) VALUES (?, ?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
		 ON CONFLICT(invoice_obligation_id) DO UPDATE SET
			status = excluded.status,
			amount_due_micros = excluded.amount_due_micros,
			amount_paid_micros = excluded.amount_paid_micros,
			currency_code = excluded.currency_code,
			last_failure_reason = excluded.last_failure_reason,
			updated_at = excluded.updated_at`,
		current.ID,
		update.Status,
		update.AmountDueMicros,
		update.AmountPaidMicros,
		current.CurrencyCode,
		failureReasonForPaymentState(update),
	); err != nil {
		return fmt.Errorf("upsert invoice payment state: %w", err)
	}
	if err := updateBillStateForPaymentStatus(ctx, tx, current.BillID, update.Status, update.AmountDueMicros); err != nil {
		return err
	}
	return nil
}

func failureReasonForPaymentState(update paymentLifecycleUpdate) string {
	if update.Status == invoiceObligationStatusFailed {
		return update.Reason
	}
	return ""
}

func updateBillStateForPaymentStatus(ctx context.Context, tx *sql.Tx, billID, paymentStatus string, amountDueMicros int64) error {
	billState := billStateIssued
	preservePastDue := false
	switch paymentStatus {
	case invoiceObligationStatusSucceeded:
		billState = billStatePaid
	case invoiceObligationStatusPastDue:
		billState = billStatePastDue
	case invoiceObligationStatusScheduled,
		invoiceObligationStatusProcessing,
		invoiceObligationStatusFailed,
		invoiceObligationStatusPartiallyPaid,
		invoiceObligationStatusRefunded:
		preservePastDue = amountDueMicros > 0
	}
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE bills
		    SET bill_state = CASE
		        WHEN ? AND bill_state = 'past_due' THEN 'past_due'
		        ELSE ?
		        END
		  WHERE id = ?
		    AND bill_state IN ('issued', 'paid', 'past_due')`,
		preservePastDue,
		billState,
		billID,
	); err != nil {
		return fmt.Errorf("update bill state for payment status: %w", err)
	}
	return nil
}

func insertPaymentLifecycleEvent(ctx context.Context, tx *sql.Tx, event PaymentLifecycleEvent) error {
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO invoice_payment_events (
			id,
			invoice_obligation_id,
			transition_kind,
			from_status,
			to_status,
			amount_micros,
			currency_code,
			reason,
			occurred_at
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ID,
		event.InvoiceObligationID,
		event.TransitionKind,
		event.FromStatus,
		event.ToStatus,
		event.AmountMicros,
		event.CurrencyCode,
		event.Reason,
		event.OccurredAt,
	); err != nil {
		return fmt.Errorf("insert payment lifecycle event: %w", err)
	}
	return nil
}

func validateInvoicePaymentTransition(fromStatus, toStatus string) error {
	if fromStatus == toStatus {
		return domainErrorf(ErrInvoicePaymentInvalidTransition, "invoice payment state is already %s", toStatus)
	}
	allowed := map[string]map[string]bool{
		invoiceObligationStatusDue: {
			invoiceObligationStatusScheduled:     true,
			invoiceObligationStatusProcessing:    true,
			invoiceObligationStatusPastDue:       true,
			invoiceObligationStatusPartiallyPaid: true,
			invoiceObligationStatusSucceeded:     true,
		},
		invoiceObligationStatusScheduled: {
			invoiceObligationStatusDue:        true,
			invoiceObligationStatusProcessing: true,
			invoiceObligationStatusFailed:     true,
			invoiceObligationStatusPastDue:    true,
		},
		invoiceObligationStatusProcessing: {
			invoiceObligationStatusFailed:        true,
			invoiceObligationStatusPartiallyPaid: true,
			invoiceObligationStatusSucceeded:     true,
		},
		invoiceObligationStatusFailed: {
			invoiceObligationStatusDue:           true,
			invoiceObligationStatusScheduled:     true,
			invoiceObligationStatusProcessing:    true,
			invoiceObligationStatusPastDue:       true,
			invoiceObligationStatusPartiallyPaid: true,
			invoiceObligationStatusSucceeded:     true,
		},
		invoiceObligationStatusPastDue: {
			invoiceObligationStatusDue:           true,
			invoiceObligationStatusScheduled:     true,
			invoiceObligationStatusProcessing:    true,
			invoiceObligationStatusPartiallyPaid: true,
			invoiceObligationStatusSucceeded:     true,
		},
		invoiceObligationStatusPartiallyPaid: {
			invoiceObligationStatusDue:        true,
			invoiceObligationStatusScheduled:  true,
			invoiceObligationStatusProcessing: true,
			invoiceObligationStatusFailed:     true,
			invoiceObligationStatusPastDue:    true,
			invoiceObligationStatusSucceeded:  true,
			invoiceObligationStatusRefunded:   true,
		},
		invoiceObligationStatusSucceeded: {
			invoiceObligationStatusRefunded: true,
		},
		invoiceObligationStatusRefunded: {
			invoiceObligationStatusDue:           true,
			invoiceObligationStatusScheduled:     true,
			invoiceObligationStatusProcessing:    true,
			invoiceObligationStatusPastDue:       true,
			invoiceObligationStatusPartiallyPaid: true,
			invoiceObligationStatusSucceeded:     true,
		},
	}
	if !allowed[fromStatus][toStatus] {
		return domainErrorf(ErrInvoicePaymentInvalidTransition, "cannot transition invoice payment state from %s to %s", fromStatus, toStatus)
	}
	return nil
}

func validateInvoicePaymentStatus(status string) error {
	switch status {
	case invoiceObligationStatusDue,
		invoiceObligationStatusScheduled,
		invoiceObligationStatusProcessing,
		invoiceObligationStatusSucceeded,
		invoiceObligationStatusFailed,
		invoiceObligationStatusPastDue,
		invoiceObligationStatusPartiallyPaid,
		invoiceObligationStatusRefunded:
		return nil
	default:
		return fmt.Errorf("unknown invoice payment status %q", status)
	}
}

func canonicalInvoicePaymentStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == legacyInvoiceObligationStatusPaid {
		return invoiceObligationStatusSucceeded
	}
	return status
}

func legacyInvoiceObligationStatus(status string) string {
	switch canonicalInvoicePaymentStatus(status) {
	case invoiceObligationStatusSucceeded:
		return legacyInvoiceObligationStatusPaid
	case invoiceObligationStatusPartiallyPaid, invoiceObligationStatusRefunded:
		return invoiceObligationStatusDue
	default:
		return status
	}
}

type invoiceObligationStateQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func getInvoiceObligationWithPaymentState(ctx context.Context, q invoiceObligationStateQuerier, whereSQL string, args ...any) (InvoiceObligation, error) {
	row := q.QueryRowContext(
		ctx,
		`SELECT
			o.id,
			o.bill_id,
			o.invoice_id,
			COALESCE(ps.status, CASE o.status WHEN 'paid' THEN 'succeeded' ELSE o.status END) AS status,
			COALESCE(ps.amount_due_micros, o.amount_due_micros) AS amount_due_micros,
			COALESCE(ps.amount_paid_micros, o.amount_paid_micros) AS amount_paid_micros,
			o.currency_code,
			o.invoice_date,
			o.due_date,
			o.created_at,
			COALESCE(ps.updated_at, o.updated_at) AS updated_at
		 FROM invoice_obligations o
		 LEFT JOIN invoice_payment_states ps ON ps.invoice_obligation_id = o.id
		 WHERE `+whereSQL,
		args...,
	)
	obligation, err := scanInvoiceObligation(row)
	if err != nil {
		return InvoiceObligation{}, err
	}
	obligation.Status = canonicalInvoicePaymentStatus(obligation.Status)
	return obligation, nil
}

type paymentLifecycleEventRow interface {
	Scan(dest ...any) error
}

func scanPaymentLifecycleEvent(row paymentLifecycleEventRow) (PaymentLifecycleEvent, error) {
	var event PaymentLifecycleEvent
	if err := row.Scan(
		&event.ID,
		&event.InvoiceObligationID,
		&event.TransitionKind,
		&event.FromStatus,
		&event.ToStatus,
		&event.AmountMicros,
		&event.CurrencyCode,
		&event.Reason,
		&event.OccurredAt,
		&event.CreatedAt,
	); err != nil {
		return PaymentLifecycleEvent{}, fmt.Errorf("scan payment lifecycle event: %w", err)
	}
	return event, nil
}
