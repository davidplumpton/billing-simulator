package scenario

import (
	"context"
	"fmt"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func normalizeCreatePaymentMethodScenarioEvent(event Event) Event {
	event = normalizePayerScenarioEvent(event)
	trimScenarioEventStrings(&event.PaymentProfileID, &event.PaymentMethodID, &event.MethodType, &event.DisplayName, &event.Status, &event.CardBrand, &event.AccountLast4, &event.BankName, &event.RemittanceDestination, &event.FailureReason)
	trimUpperScenarioEventString(&event.CurrencyCode)
	return event
}

type scenarioPaymentLifecycleEventPayload struct {
	ID                  string
	Action              EventAction
	InvoiceObligationID string
	Reason              string
	AmountMicros        int64
}

func newPaymentLifecycleScenarioEventActionSpec(action EventAction) scenarioEventActionSpec {
	return scenarioEventPayloadActionSpec[scenarioPaymentLifecycleEventPayload]{
		action:           action,
		payloadFromEvent: paymentLifecyclePayloadFromEvent,
		mergePayload:     mergePaymentLifecyclePayload,
		normalize:        normalizePaymentLifecycleScenarioPayload,
		validate:         validatePaymentLifecyclePayload,
		apply:            applyPaymentLifecycleScenarioPayload,
	}.asEventActionSpec()
}

func paymentLifecyclePayloadFromEvent(event Event) scenarioPaymentLifecycleEventPayload {
	return scenarioPaymentLifecycleEventPayload{
		ID:                  event.ID,
		Action:              event.Action,
		InvoiceObligationID: event.InvoiceObligationID,
		Reason:              event.Reason,
		AmountMicros:        event.AmountMicros,
	}
}

func mergePaymentLifecyclePayload(event Event, payload scenarioPaymentLifecycleEventPayload) Event {
	event.InvoiceObligationID = payload.InvoiceObligationID
	event.Reason = payload.Reason
	event.AmountMicros = payload.AmountMicros
	return event
}

func normalizePaymentLifecycleScenarioPayload(payload scenarioPaymentLifecycleEventPayload) scenarioPaymentLifecycleEventPayload {
	trimScenarioEventStrings(&payload.InvoiceObligationID, &payload.Reason)
	return payload
}

func validatePaymentMethodScenarioEventSemantics(path, organizationTemplate string, event Event, createdAccounts map[string]string, problems *validationProblems) {
	validateScenarioAccountReference(path+".payer_account", organizationTemplate, event.PayerAccountID, event.PayerAccount, createdAccounts, problems)
}

func validatePaymentLifecyclePayload(path string, payload scenarioPaymentLifecycleEventPayload, problems *validationProblems) {
	if payload.AmountMicros < 0 {
		problems.add("%s.amount_micros must be zero or greater", path)
	}
	if payload.Action == EventActionCollectPayment && payload.AmountMicros <= 0 {
		problems.add("%s.amount_micros must be greater than zero for collect_payment", path)
	}
}

func applyCreatePaymentMethodScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createPaymentMethod(ctx, state, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyPaymentLifecycleScenarioPayload(ctx context.Context, r Runner, state *scenarioExecutionState, payload scenarioPaymentLifecycleEventPayload, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.applyPaymentLifecycleEvent(ctx, state, payload, scheduledAt); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

// createPaymentMethod prepares the payer method state a payment remediation lab should start from.
func (r Runner) createPaymentMethod(ctx context.Context, state *scenarioExecutionState, event Event) (persistence.PaymentMethod, error) {
	profileID, err := r.resolvePaymentProfileID(ctx, state, event)
	if err != nil {
		return persistence.PaymentMethod{}, err
	}
	methodID := event.PaymentMethodID
	if methodID == "" {
		methodID = stableScenarioID("paymeth_scn", state.runID, event.ID, event.DisplayName)
	}
	return r.profiles.CreatePaymentMethod(ctx, persistence.PaymentMethodCreateRequest{
		ID:                      methodID,
		PaymentProfileID:        profileID,
		MethodType:              event.MethodType,
		DisplayName:             event.DisplayName,
		Status:                  event.Status,
		IsDefault:               event.IsDefault,
		CurrencyCode:            event.CurrencyCode,
		CardBrand:               event.CardBrand,
		AccountLast4:            event.AccountLast4,
		ExpirationMonth:         event.ExpirationMonth,
		ExpirationYear:          event.ExpirationYear,
		BankName:                event.BankName,
		RemittanceDestination:   event.RemittanceDestination,
		AdvancePayBalanceMicros: event.AdvancePayBalanceMicros,
		FailureReason:           event.FailureReason,
	})
}

// applyPaymentLifecycleEvent moves the current scenario invoice through the same state machine as the UI.
func (r Runner) applyPaymentLifecycleEvent(ctx context.Context, state *scenarioExecutionState, payload scenarioPaymentLifecycleEventPayload, scheduledAt time.Time) (persistence.PaymentLifecycleResult, error) {
	obligationID := chooseFirst(payload.InvoiceObligationID, state.lastInvoiceObligationID)
	if obligationID == "" {
		return persistence.PaymentLifecycleResult{}, fmt.Errorf("scenario payment event %q requires invoice_obligation_id or a prior close_billing_period event", payload.ID)
	}
	request := persistence.PaymentLifecycleTransitionRequest{
		InvoiceObligationID: obligationID,
		AmountMicros:        payload.AmountMicros,
		Reason:              payload.Reason,
		OccurredAt:          scheduledAt.UTC().Format(time.RFC3339),
	}
	switch payload.Action {
	case EventActionSchedulePayment:
		return r.payments.SchedulePayment(ctx, request)
	case EventActionProcessPayment:
		return r.payments.StartProcessing(ctx, request)
	case EventActionFailPayment:
		return r.payments.FailPayment(ctx, request)
	case EventActionMarkPaymentDue:
		return r.payments.MarkDue(ctx, request)
	case EventActionMarkPaymentPastDue:
		return r.payments.MarkPastDue(ctx, request)
	case EventActionCollectPayment:
		return r.payments.ApplyPayment(ctx, request)
	default:
		return persistence.PaymentLifecycleResult{}, fmt.Errorf("scenario event action %q is not a payment lifecycle action", payload.Action)
	}
}

// resolvePaymentProfileID finds the profile named directly or through a payer account reference.
func (r Runner) resolvePaymentProfileID(ctx context.Context, state *scenarioExecutionState, event Event) (string, error) {
	if event.PaymentProfileID != "" {
		return event.PaymentProfileID, nil
	}
	payerID := state.resolveAccountID(event.PayerAccountID, event.PayerAccount)
	if payerID == "" {
		return "", fmt.Errorf("scenario payment method %q requires payment_profile_id or payer_account", event.ID)
	}
	details, found, err := r.profiles.GetDefaultPaymentProfileForPayer(ctx, payerID, chooseFirst(event.CurrencyCode, "USD"))
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("default payment profile for payer %q is not available", payerID)
	}
	return details.Profile.ID, nil
}
