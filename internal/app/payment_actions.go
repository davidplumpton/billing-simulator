package app

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"aws-billing-simulator/internal/billingvisibility"
	"aws-billing-simulator/internal/persistence"
)

type paymentActionRunner struct {
	db        *sql.DB
	lifecycle persistence.PaymentLifecycleRepository
	profiles  persistence.PaymentProfileRepository
	clock     persistence.SimulatorClockRepository
}

type paymentActionSpec struct {
	name  string
	apply func(context.Context, paymentActionRunner, url.Values, billingvisibility.Policy) (string, error)
}

type paymentLifecycleApplyFunc func(persistence.PaymentLifecycleRepository, context.Context, persistence.PaymentLifecycleTransitionRequest) (persistence.PaymentLifecycleResult, error)
type paymentLifecycleFlashFunc func(string) string

// paymentActionRunnerFromHandler isolates state-changing payment dependencies from page rendering.
func paymentActionRunnerFromHandler(h paymentsHandler) paymentActionRunner {
	return paymentActionRunner{
		db:        h.db,
		lifecycle: h.lifecycle,
		profiles:  h.profiles,
		clock:     h.clock,
	}
}

// Apply validates and executes one registered payment command from submitted form values.
func (r paymentActionRunner) Apply(ctx context.Context, values url.Values) (string, error) {
	action := strings.TrimSpace(values.Get("action"))
	spec, ok := paymentActionSpecFor(action)
	if !ok {
		return "", fmt.Errorf("unsupported payment action %q", action)
	}
	policy, err := paymentPolicyFromValues(ctx, r.db, values)
	if err != nil {
		return "", fmt.Errorf("payment action: %w", err)
	}
	return spec.apply(ctx, r, values, policy)
}

// paymentActionSpecFor returns the command-local behavior for a submitted payment action.
func paymentActionSpecFor(action string) (paymentActionSpec, bool) {
	spec, ok := paymentActionSpecsByName[strings.TrimSpace(action)]
	return spec, ok
}

var paymentActionSpecsByName = newPaymentActionSpecsByName()

// newPaymentActionSpecsByName builds the action registry used by POST /payments/action.
func newPaymentActionSpecsByName() map[string]paymentActionSpec {
	specs := []paymentActionSpec{
		paymentLifecycleActionSpec(
			"schedule",
			false,
			func(repo persistence.PaymentLifecycleRepository, ctx context.Context, request persistence.PaymentLifecycleTransitionRequest) (persistence.PaymentLifecycleResult, error) {
				return repo.SchedulePayment(ctx, request)
			},
			func(invoiceID string) string { return "Scheduled payment for " + invoiceID },
		),
		paymentLifecycleActionSpec(
			"process",
			false,
			func(repo persistence.PaymentLifecycleRepository, ctx context.Context, request persistence.PaymentLifecycleTransitionRequest) (persistence.PaymentLifecycleResult, error) {
				return repo.StartProcessing(ctx, request)
			},
			func(invoiceID string) string { return "Started payment processing for " + invoiceID },
		),
		paymentLifecycleActionSpec(
			"fail",
			false,
			func(repo persistence.PaymentLifecycleRepository, ctx context.Context, request persistence.PaymentLifecycleTransitionRequest) (persistence.PaymentLifecycleResult, error) {
				return repo.FailPayment(ctx, request)
			},
			func(invoiceID string) string { return "Recorded failed payment for " + invoiceID },
		),
		paymentLifecycleActionSpec(
			"mark_due",
			false,
			func(repo persistence.PaymentLifecycleRepository, ctx context.Context, request persistence.PaymentLifecycleTransitionRequest) (persistence.PaymentLifecycleResult, error) {
				return repo.MarkDue(ctx, request)
			},
			func(invoiceID string) string { return "Marked " + invoiceID + " due" },
		),
		paymentLifecycleActionSpec(
			"mark_past_due",
			false,
			func(repo persistence.PaymentLifecycleRepository, ctx context.Context, request persistence.PaymentLifecycleTransitionRequest) (persistence.PaymentLifecycleResult, error) {
				return repo.MarkPastDue(ctx, request)
			},
			func(invoiceID string) string { return "Marked " + invoiceID + " past due" },
		),
		paymentLifecycleActionSpec(
			"collect",
			true,
			func(repo persistence.PaymentLifecycleRepository, ctx context.Context, request persistence.PaymentLifecycleTransitionRequest) (persistence.PaymentLifecycleResult, error) {
				return repo.ApplyPayment(ctx, request)
			},
			func(invoiceID string) string { return "Collected payment for " + invoiceID },
		),
		paymentLifecycleActionSpec(
			"refund",
			true,
			func(repo persistence.PaymentLifecycleRepository, ctx context.Context, request persistence.PaymentLifecycleTransitionRequest) (persistence.PaymentLifecycleResult, error) {
				return repo.RefundPayment(ctx, request)
			},
			func(invoiceID string) string { return "Recorded refund for " + invoiceID },
		),
		{
			name: "fix_method",
			apply: func(ctx context.Context, runner paymentActionRunner, values url.Values, policy billingvisibility.Policy) (string, error) {
				method, err := runner.ensurePaymentMethodVisible(ctx, policy, values.Get("method_id"))
				if err != nil {
					return "", err
				}
				method, err = runner.profiles.ResolvePaymentMethodFailure(ctx, method.ID)
				if err != nil {
					return "", err
				}
				return "Fixed payment method " + method.DisplayName, nil
			},
		},
		{
			name: "set_default_method",
			apply: func(ctx context.Context, runner paymentActionRunner, values url.Values, policy billingvisibility.Policy) (string, error) {
				method, err := runner.ensurePaymentMethodVisible(ctx, policy, values.Get("method_id"))
				if err != nil {
					return "", err
				}
				method, err = runner.profiles.SetDefaultPaymentMethod(ctx, method.ID)
				if err != nil {
					return "", err
				}
				return "Selected default payment method " + method.DisplayName, nil
			},
		},
		{
			name: "set_default_profile",
			apply: func(ctx context.Context, runner paymentActionRunner, values url.Values, policy billingvisibility.Policy) (string, error) {
				profile, err := runner.ensurePaymentProfileVisible(ctx, policy, values.Get("profile_id"))
				if err != nil {
					return "", err
				}
				profile, err = runner.profiles.SetDefaultPaymentProfile(ctx, profile.ID)
				if err != nil {
					return "", err
				}
				return "Selected default payment profile " + profile.ProfileName, nil
			},
		},
	}

	byName := make(map[string]paymentActionSpec, len(specs))
	for _, spec := range specs {
		if spec.apply == nil {
			panic("payment action spec " + spec.name + " has no apply function")
		}
		if _, exists := byName[spec.name]; exists {
			panic("duplicate payment action spec " + spec.name)
		}
		byName[spec.name] = spec
	}
	return byName
}

// paymentLifecycleActionSpec adapts invoice lifecycle repository methods into registry commands.
func paymentLifecycleActionSpec(name string, includeAmount bool, apply paymentLifecycleApplyFunc, flash paymentLifecycleFlashFunc) paymentActionSpec {
	return paymentActionSpec{
		name: name,
		apply: func(ctx context.Context, runner paymentActionRunner, values url.Values, policy billingvisibility.Policy) (string, error) {
			request, err := runner.visibleTransitionRequestFromValues(ctx, values, policy, includeAmount)
			if err != nil {
				return "", err
			}
			result, err := apply(runner.lifecycle, ctx, request)
			if err != nil {
				return "", err
			}
			return flash(result.Obligation.InvoiceID), nil
		},
	}
}

// visibleTransitionRequestFromValues validates one invoice transition against the active payment policy.
func (r paymentActionRunner) visibleTransitionRequestFromValues(ctx context.Context, values url.Values, policy billingvisibility.Policy, includeAmount bool) (persistence.PaymentLifecycleTransitionRequest, error) {
	request, err := r.transitionRequestFromValues(ctx, values, includeAmount)
	if err != nil {
		return persistence.PaymentLifecycleTransitionRequest{}, err
	}
	if err := r.ensurePaymentObligationVisible(ctx, policy, request.InvoiceObligationID); err != nil {
		return persistence.PaymentLifecycleTransitionRequest{}, err
	}
	return request, nil
}

// transitionRequestFromValues parses common lifecycle command fields from submitted form values.
func (r paymentActionRunner) transitionRequestFromValues(ctx context.Context, values url.Values, includeAmount bool) (persistence.PaymentLifecycleTransitionRequest, error) {
	request := persistence.PaymentLifecycleTransitionRequest{
		InvoiceObligationID: values.Get("invoice_obligation_id"),
		Reason:              values.Get("reason"),
		OccurredAt:          strings.TrimSpace(values.Get("occurred_at")),
	}
	if request.OccurredAt == "" {
		if clock, err := r.clock.Get(ctx); err == nil {
			request.OccurredAt = clock.CurrentTime
		}
	}
	if includeAmount {
		amount, err := parsePaymentAmountMicros(values.Get("amount"))
		if err != nil {
			return persistence.PaymentLifecycleTransitionRequest{}, err
		}
		request.AmountMicros = amount
	}
	return request, nil
}

// ensurePaymentObligationVisible checks the payer for an invoice obligation before mutation.
func (r paymentActionRunner) ensurePaymentObligationVisible(ctx context.Context, policy billingvisibility.Policy, invoiceObligationID string) error {
	payerAccountID, err := r.paymentObligationPayerAccountID(ctx, invoiceObligationID)
	if err != nil {
		return err
	}
	return ensurePaymentPayerVisible(policy, payerAccountID)
}

// paymentObligationPayerAccountID reads the payer account attached to an invoice obligation.
func (r paymentActionRunner) paymentObligationPayerAccountID(ctx context.Context, invoiceObligationID string) (string, error) {
	if r.db == nil {
		return "", fmt.Errorf("database handle is required")
	}
	invoiceObligationID = strings.TrimSpace(invoiceObligationID)
	if invoiceObligationID == "" {
		return "", fmt.Errorf("invoice obligation ID is required")
	}
	var payerAccountID string
	if err := r.db.QueryRowContext(
		ctx,
		`SELECT b.payer_account_id
		   FROM invoice_obligations o
		   JOIN bills b ON b.id = o.bill_id
		  WHERE o.id = ?`,
		invoiceObligationID,
	).Scan(&payerAccountID); err != nil {
		return "", fmt.Errorf("get invoice obligation payer %q: %w", invoiceObligationID, err)
	}
	return payerAccountID, nil
}

// ensurePaymentMethodVisible checks a payment method's parent profile against the active policy.
func (r paymentActionRunner) ensurePaymentMethodVisible(ctx context.Context, policy billingvisibility.Policy, methodID string) (persistence.PaymentMethod, error) {
	method, err := r.profiles.GetPaymentMethod(ctx, methodID)
	if err != nil {
		return persistence.PaymentMethod{}, err
	}
	if _, err := r.ensurePaymentProfileVisible(ctx, policy, method.PaymentProfileID); err != nil {
		return persistence.PaymentMethod{}, err
	}
	return method, nil
}

// ensurePaymentProfileVisible checks a payment profile payer against the active policy.
func (r paymentActionRunner) ensurePaymentProfileVisible(ctx context.Context, policy billingvisibility.Policy, profileID string) (persistence.PaymentProfile, error) {
	profile, err := r.profiles.GetPaymentProfile(ctx, profileID)
	if err != nil {
		return persistence.PaymentProfile{}, err
	}
	if err := ensurePaymentPayerVisible(policy, profile.PayerAccountID); err != nil {
		return persistence.PaymentProfile{}, err
	}
	return profile, nil
}

// ensurePaymentPayerVisible centralizes payer-level payment authorization.
func ensurePaymentPayerVisible(policy billingvisibility.Policy, payerAccountID string) error {
	if policy.AllowsPayerAccount(payerAccountID) {
		return nil
	}
	return paymentAccessError{err: fmt.Errorf("billing role %q cannot manage payments for payer account %q", policy.Role, strings.TrimSpace(payerAccountID))}
}

// parsePaymentAmountMicros converts decimal dollar inputs into micros for payment commands.
func parsePaymentAmountMicros(value string) (int64, error) {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "$", ""), ",", ""))
	if value == "" {
		return 0, fmt.Errorf("payment amount is required")
	}
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("payment amount must be numeric: %w", err)
	}
	if amount <= 0 {
		return 0, fmt.Errorf("payment amount must be greater than zero")
	}
	micros := math.Round(amount * 1_000_000)
	if micros > float64(math.MaxInt64) {
		return 0, fmt.Errorf("payment amount is too large")
	}
	return int64(micros), nil
}
