package scenario

import (
	"context"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func normalizeAdvanceClockScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.Unit)
	return event
}

func normalizeBillingPeriodScenarioEvent(event Event) Event {
	event = normalizePayerScenarioEvent(event)
	trimScenarioEventStrings(&event.BillingPeriodStart, &event.BillingPeriodEnd)
	return event
}

func normalizeCostAllocationTagScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.TagKey)
	return event
}

func validateBillingPeriodScenarioEvent(path string, event Event, problems *validationProblems) {
	validateOptionalDate(path+".billing_period_start", event.BillingPeriodStart, problems)
	validateOptionalDate(path+".billing_period_end", event.BillingPeriodEnd, problems)
	if event.BillingPeriodStart != "" && event.BillingPeriodEnd == "" {
		problems.add("%s.billing_period_end is required when billing_period_start is set", path)
	}
	if event.BillingPeriodEnd != "" && event.BillingPeriodStart == "" {
		problems.add("%s.billing_period_start is required when billing_period_end is set", path)
	}
	validateBillingEvent(path, event, problems)
}

func validatePayerScenarioEventSemantics(path, organizationTemplate string, event Event, createdAccounts map[string]string, problems *validationProblems) {
	validateScenarioAccountReference(path+".payer_account", organizationTemplate, event.PayerAccountID, event.PayerAccount, createdAccounts, problems)
}

func preflightRunDailyMeteringScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, preflight *scenarioEventPreflight, event Event, scheduledAt time.Time) (scenarioClosedPeriodConflict, bool, error) {
	payerID := state.resolveAccountID(event.PayerAccountID, event.PayerAccount)
	return r.closedPeriodConflictForPlannedUsage(ctx, preflight.definition, preflight.plannedUsage, event.ID, payerID, scheduledAt, "", "")
}

func preflightCloseBillingPeriodScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, preflight *scenarioEventPreflight, event Event, scheduledAt time.Time) (scenarioClosedPeriodConflict, bool, error) {
	period, err := scenarioPreflightClosePeriod(event, scheduledAt)
	if err != nil {
		return scenarioClosedPeriodConflict{}, false, err
	}
	periodEnd, err := scenarioPreflightPeriodEndTime(period)
	if err != nil {
		return scenarioClosedPeriodConflict{}, false, err
	}
	payerID := state.resolveAccountID(event.PayerAccountID, event.PayerAccount)
	return r.closedPeriodConflictForPlannedUsage(ctx, preflight.definition, preflight.plannedUsage, event.ID, payerID, periodEnd, period.Start, period.End)
}

func applyAdvanceClockScenarioEvent(ctx context.Context, r Runner, _ *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.clock.Advance(ctx, persistence.SimulatorClockAdvanceRequest{
		Amount: event.Amount,
		Unit:   persistence.SimulatorClockAdvanceUnit(event.Unit),
	}); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyRunDailyMeteringScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	metering, err := r.daily.Run(ctx, persistence.DailyMeteringJobRequest{
		Trigger:        persistence.DailyMeteringJobTriggerOnDemand,
		PayerAccountID: state.resolveAccountID(event.PayerAccountID, event.PayerAccount),
	})
	if err != nil {
		err = r.describeClosedPeriodPricingFailure(ctx, state, event, scheduledAt, err)
		return failScenarioRunEvent(audit, err)
	}
	audit.MeteringRecordsCreated = metering.MeteringRecordsCreated
	audit.BillLineItemsCreated = metering.BillLineItemsCreated
	return audit, nil
}

func applyCloseBillingPeriodScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	closed, err := r.monthEnd.ClosePreviousPeriod(ctx, persistence.MonthEndCloseRequest{
		PayerAccountID: state.resolveAccountID(event.PayerAccountID, event.PayerAccount),
		PeriodStart:    event.BillingPeriodStart,
		PeriodEnd:      event.BillingPeriodEnd,
	})
	if err != nil {
		err = r.describeClosedPeriodPricingFailure(ctx, state, event, scheduledAt, err)
		return failScenarioRunEvent(audit, err)
	}
	audit.MeteringRecordsCreated = closed.MeteringRecordsCreated
	audit.BillLineItemsCreated = closed.BillLineItemsCreated
	audit.BillID = closed.Bill.ID
	audit.BillsIssued = boolToInt(closed.Bill.ID != "")
	state.lastInvoiceObligationID = closed.InvoiceObligation.ID
	return audit, nil
}

func applyRefreshCostAllocationTagsScenarioEvent(ctx context.Context, r Runner, _ *scenarioExecutionState, _ Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.refreshCostAllocationTags(ctx, scheduledAt); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyActivateCostAllocationTagScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, scheduledAt time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.activateCostAllocationTag(ctx, state, event, scheduledAt); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

// refreshCostAllocationTags records the scenario's current resource tag inventory for billing workflows.
func (r Runner) refreshCostAllocationTags(ctx context.Context, scheduledAt time.Time) (persistence.CostAllocationTagRefreshResult, error) {
	return r.tags.RefreshDiscoveredTags(ctx, scheduledAt.UTC().Format(time.RFC3339))
}

// activateCostAllocationTag turns one discovered resource tag key into a billing cost allocation key.
func (r Runner) activateCostAllocationTag(ctx context.Context, state *scenarioExecutionState, event Event, scheduledAt time.Time) (persistence.CostAllocationTagKey, error) {
	return r.tags.ActivateTag(ctx, persistence.CostAllocationTagActivationRequest{
		ID:                    stableScenarioID("cat_evt_scn", state.runID, event.ID, event.TagKey),
		Key:                   event.TagKey,
		RequestedAt:           scheduledAt.UTC().Format(time.RFC3339),
		EventSource:           scenarioEventSource,
		ScenarioRunID:         state.runID,
		ScenarioEventID:       event.ID,
		ScenarioEventSequence: event.Sequence,
	})
}
