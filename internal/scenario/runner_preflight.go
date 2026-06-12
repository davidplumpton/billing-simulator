package scenario

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

type scenarioEventPreflight struct {
	runID        string
	definition   Definition
	resources    map[string]scenarioPreflightResource
	plannedUsage []scenarioPlannedUsageRef
}

type scenarioPlannedUsageRef struct {
	EventID     string
	AccountID   string
	PeriodStart string
	PeriodEnd   string
	UsageEnd    time.Time
}

type scenarioPreflightResource struct {
	AccountID string
}

type scenarioClosedPeriodConflict struct {
	EventID        string
	PayerAccountID string
	PeriodStart    string
	PeriodEnd      string
}

func (c scenarioClosedPeriodConflict) Error() string {
	usageLabel := "usage"
	if periodStart, err := time.Parse(time.DateOnly, c.PeriodStart); err == nil {
		usageLabel = periodStart.Format("January 2006") + " usage"
	}
	return fmt.Sprintf(
		"Cannot price %s because billing period %s to %s is already closed for payer %s. Reset or clone the workspace before launching this scenario.",
		usageLabel,
		c.PeriodStart,
		c.PeriodEnd,
		c.PayerAccountID,
	)
}

// preflightClosedPeriodPricing catches scenario-authored pricing conflicts before domain rows are mutated.
func (r Runner) preflightClosedPeriodPricing(ctx context.Context, runID string, definition Definition, startTime time.Time) (scenarioClosedPeriodConflict, bool, error) {
	state := scenarioExecutionState{
		runID:                runID,
		definition:           definition,
		startTime:            startTime,
		accountAliasesByKey:  map[string]string{},
		resourceAliasesByKey: map[string]string{},
		categoryAliasesByKey: map[string]string{},
	}
	preflight := scenarioEventPreflight{
		runID:      runID,
		definition: definition,
		resources:  map[string]scenarioPreflightResource{},
	}

	for _, event := range definition.Events {
		scheduledAt, err := scheduledEventTime(startTime, event)
		if err != nil {
			return scenarioClosedPeriodConflict{}, false, err
		}

		spec, ok := scenarioEventActionSpecFor(event.Action)
		if !ok {
			continue
		}
		if conflict, found, err := spec.preflight(ctx, r, &state, &preflight, event, scheduledAt); err != nil {
			return scenarioClosedPeriodConflict{}, false, err
		} else if found {
			return conflict, true, nil
		}
	}
	return scenarioClosedPeriodConflict{}, false, nil
}

// describeClosedPeriodPricingFailure replaces raw SQLite trigger failures with a learner-facing recovery message.
func (r Runner) describeClosedPeriodPricingFailure(ctx context.Context, state *scenarioExecutionState, event Event, scheduledAt time.Time, err error) error {
	if !isClosedPeriodPricingFailure(err) {
		return err
	}

	payerID := state.resolveAccountID(event.PayerAccountID, event.PayerAccount)
	periodStart, periodEnd, throughTime := "", "", scheduledAt.UTC().Format(time.RFC3339)
	if event.Action == EventActionCloseBillingPeriod || event.Action == EventActionIssueBill {
		if period, periodErr := scenarioPreflightClosePeriod(event, scheduledAt); periodErr == nil {
			periodStart = period.Start
			periodEnd = period.End
			if periodEndTime, endErr := scenarioPreflightPeriodEndTime(period); endErr == nil {
				throughTime = periodEndTime.Format(time.RFC3339)
			}
		}
	}

	if conflict, found, findErr := r.closedPeriodConflictForUnpricedMetering(ctx, event.ID, payerID, throughTime, periodStart, periodEnd); findErr == nil && found {
		return conflict
	}
	if periodStart != "" && periodEnd != "" && payerID != "" {
		if closed, closedErr := r.billingPeriodClosed(ctx, periodStart, periodEnd, payerID); closedErr == nil && closed {
			return scenarioClosedPeriodConflict{EventID: event.ID, PayerAccountID: payerID, PeriodStart: periodStart, PeriodEnd: periodEnd}
		}
	}
	if payerID == "" {
		payerID = "unknown"
	}
	if periodStart == "" || periodEnd == "" {
		if period, periodErr := persistence.BillingPeriodForTime(scheduledAt); periodErr == nil {
			periodStart = period.Start
			periodEnd = period.End
		}
	}
	return scenarioClosedPeriodConflict{EventID: event.ID, PayerAccountID: payerID, PeriodStart: periodStart, PeriodEnd: periodEnd}
}

func scenarioPreflightUsageRefsForAddUsage(state *scenarioExecutionState, resources map[string]scenarioPreflightResource, runID string, event Event, scheduledAt time.Time) ([]scenarioPlannedUsageRef, error) {
	accountID := state.resolveAccountID(event.AccountID, event.Account)
	resource, hasResource := scenarioPreflightResourceForEvent(state, resources, event)
	if accountID == "" && hasResource {
		accountID = resource.AccountID
	}
	if !hasResource {
		resourceID := scenarioResourceID(runID, event)
		rememberScenarioPreflightResource(state, resources, event, resourceID, accountID)
	}
	if accountID == "" {
		return nil, nil
	}

	usageStart, usageEnd, err := scenarioUsageWindow(scheduledAt, event)
	if err != nil {
		return nil, err
	}
	period, usageEndTime, err := scenarioPreflightUsagePeriod(usageStart, usageEnd)
	if err != nil {
		return nil, err
	}
	return []scenarioPlannedUsageRef{{
		EventID:     event.ID,
		AccountID:   accountID,
		PeriodStart: period.Start,
		PeriodEnd:   period.End,
		UsageEnd:    usageEndTime,
	}}, nil
}

func scenarioPreflightUsageRefsForGeneratedUsage(state *scenarioExecutionState, resources map[string]scenarioPreflightResource, event Event, scheduledAt time.Time) ([]scenarioPlannedUsageRef, error) {
	resource, ok := scenarioPreflightResourceForEvent(state, resources, event)
	if !ok || resource.AccountID == "" {
		return nil, nil
	}

	refs := make([]scenarioPlannedUsageRef, 0, event.Days)
	for day := 0; day < event.Days; day++ {
		usageStart := scheduledAt.AddDate(0, 0, day).UTC()
		usageEnd := usageStart.AddDate(0, 0, 1)
		period, usageEndTime, err := scenarioPreflightUsagePeriod(usageStart.Format(time.RFC3339), usageEnd.Format(time.RFC3339))
		if err != nil {
			return nil, err
		}
		refs = append(refs, scenarioPlannedUsageRef{
			EventID:     event.ID,
			AccountID:   resource.AccountID,
			PeriodStart: period.Start,
			PeriodEnd:   period.End,
			UsageEnd:    usageEndTime,
		})
	}
	return refs, nil
}

func rememberScenarioPreflightResource(state *scenarioExecutionState, resources map[string]scenarioPreflightResource, event Event, resourceID, accountID string) {
	if resourceID == "" {
		return
	}
	state.rememberResource(event, resourceID)
	resource := scenarioPreflightResource{AccountID: accountID}
	for _, alias := range []string{resourceID, event.ResourceID, event.Resource, event.ID} {
		key := scenarioAliasKey(alias)
		if key != "" {
			resources[key] = resource
		}
	}
}

func scenarioPreflightResourceForEvent(state *scenarioExecutionState, resources map[string]scenarioPreflightResource, event Event) (scenarioPreflightResource, bool) {
	aliases := []string{state.resolveResourceID(event), event.ResourceID, event.Resource, event.ID}
	for _, alias := range aliases {
		key := scenarioAliasKey(alias)
		if key == "" {
			continue
		}
		if resource, ok := resources[key]; ok {
			return resource, true
		}
	}
	return scenarioPreflightResource{}, false
}

func scenarioPreflightUsagePeriod(usageStartValue, usageEndValue string) (persistence.BillingPeriod, time.Time, error) {
	usageStart, err := time.Parse(time.RFC3339, strings.TrimSpace(usageStartValue))
	if err != nil {
		return persistence.BillingPeriod{}, time.Time{}, fmt.Errorf("usage start time must use RFC3339: %w", err)
	}
	usageEnd, err := time.Parse(time.RFC3339, strings.TrimSpace(usageEndValue))
	if err != nil {
		return persistence.BillingPeriod{}, time.Time{}, fmt.Errorf("usage end time must use RFC3339: %w", err)
	}
	usageStart = usageStart.UTC()
	usageEnd = usageEnd.UTC()
	if !usageStart.Before(usageEnd) {
		return persistence.BillingPeriod{}, time.Time{}, fmt.Errorf("usage start time must be before end time")
	}
	period, err := persistence.BillingPeriodForTime(usageStart)
	if err != nil {
		return persistence.BillingPeriod{}, time.Time{}, err
	}
	periodEnd, err := scenarioPreflightPeriodEndTime(period)
	if err != nil {
		return persistence.BillingPeriod{}, time.Time{}, err
	}
	if usageEnd.After(periodEnd) {
		return persistence.BillingPeriod{}, time.Time{}, fmt.Errorf("usage window %s to %s crosses billing period %s to %s", usageStart.Format(time.RFC3339), usageEnd.Format(time.RFC3339), period.Start, period.End)
	}
	return period, usageEnd, nil
}

func scenarioPreflightClosePeriod(event Event, scheduledAt time.Time) (persistence.BillingPeriod, error) {
	if event.BillingPeriodStart != "" || event.BillingPeriodEnd != "" {
		return scenarioPreflightBillingPeriodFromRange(event.BillingPeriodStart, event.BillingPeriodEnd)
	}
	currentPeriod, err := persistence.BillingPeriodForTime(scheduledAt)
	if err != nil {
		return persistence.BillingPeriod{}, err
	}
	currentStart, err := time.Parse(time.DateOnly, currentPeriod.Start)
	if err != nil {
		return persistence.BillingPeriod{}, fmt.Errorf("parse current billing period start: %w", err)
	}
	return persistence.BillingPeriodForTime(currentStart.AddDate(0, -1, 0))
}

func scenarioPreflightBillingPeriodFromRange(periodStart, periodEnd string) (persistence.BillingPeriod, error) {
	start, err := time.Parse(time.DateOnly, strings.TrimSpace(periodStart))
	if err != nil {
		return persistence.BillingPeriod{}, fmt.Errorf("billing period start must use YYYY-MM-DD: %w", err)
	}
	if _, err := time.Parse(time.DateOnly, strings.TrimSpace(periodEnd)); err != nil {
		return persistence.BillingPeriod{}, fmt.Errorf("billing period end must use YYYY-MM-DD: %w", err)
	}
	period, err := persistence.BillingPeriodForTime(start)
	if err != nil {
		return persistence.BillingPeriod{}, err
	}
	if period.Start != strings.TrimSpace(periodStart) || period.End != strings.TrimSpace(periodEnd) {
		return persistence.BillingPeriod{}, fmt.Errorf("scenario billing period must match one UTC calendar billing period")
	}
	return period, nil
}

func scenarioPreflightPeriodEndTime(period persistence.BillingPeriod) (time.Time, error) {
	periodEnd, err := time.Parse(time.DateOnly, period.End)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse billing period end: %w", err)
	}
	return periodEnd.UTC(), nil
}

// closedPeriodConflictForPlannedUsage checks planned usage against already-closed payer periods.
func (r Runner) closedPeriodConflictForPlannedUsage(ctx context.Context, definition Definition, plannedUsage []scenarioPlannedUsageRef, eventID, pricingPayerAccountID string, throughTime time.Time, onlyPeriodStart, onlyPeriodEnd string) (scenarioClosedPeriodConflict, bool, error) {
	for _, ref := range plannedUsage {
		if ref.UsageEnd.After(throughTime.UTC()) {
			continue
		}
		if onlyPeriodStart != "" && ref.PeriodStart != onlyPeriodStart {
			continue
		}
		if onlyPeriodEnd != "" && ref.PeriodEnd != onlyPeriodEnd {
			continue
		}
		payerID := strings.TrimSpace(pricingPayerAccountID)
		if payerID == "" {
			payerID = scenarioDefaultPayerAccountID(definition.OrganizationTemplate, ref.AccountID)
		}
		if payerID == "" {
			continue
		}
		closed, err := r.billingPeriodClosed(ctx, ref.PeriodStart, ref.PeriodEnd, payerID)
		if err != nil {
			return scenarioClosedPeriodConflict{}, false, err
		}
		if closed {
			return scenarioClosedPeriodConflict{
				EventID:        eventID,
				PayerAccountID: payerID,
				PeriodStart:    ref.PeriodStart,
				PeriodEnd:      ref.PeriodEnd,
			}, true, nil
		}
	}
	return scenarioClosedPeriodConflict{}, false, nil
}

func scenarioDefaultPayerAccountID(template, usageAccountID string) string {
	usageAccountID = strings.TrimSpace(usageAccountID)
	if usageAccountID == "" {
		return ""
	}
	if persistence.IsAnyCompanyRetailTemplate(template) {
		return persistence.AnyCompanyRetailManagementAccountID
	}
	return usageAccountID
}

// billingPeriodClosed checks the immutable close table for one payer-period.
func (r Runner) billingPeriodClosed(ctx context.Context, periodStart, periodEnd, payerAccountID string) (bool, error) {
	var id string
	err := r.db.QueryRowContext(ctx, `SELECT id
		FROM billing_period_closes
		WHERE billing_period_start = ?
		  AND billing_period_end = ?
		  AND payer_account_id = ?
		  AND status = 'closed'
		LIMIT 1`,
		periodStart,
		periodEnd,
		payerAccountID,
	).Scan(&id)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("check closed billing period %s to %s payer %s: %w", periodStart, periodEnd, payerAccountID, err)
}

// closedPeriodConflictForUnpricedMetering infers the payer-period behind a trigger failure.
func (r Runner) closedPeriodConflictForUnpricedMetering(ctx context.Context, eventID, pricingPayerAccountID, throughTime, onlyPeriodStart, onlyPeriodEnd string) (scenarioClosedPeriodConflict, bool, error) {
	var conflict scenarioClosedPeriodConflict
	err := r.db.QueryRowContext(ctx, `WITH candidate_metering AS (
			SELECT
				date(m.usage_start_time, 'start of month') AS billing_period_start,
				date(m.usage_start_time, 'start of month', '+1 month') AS billing_period_end,
				CASE
					WHEN ? <> '' THEN ?
					WHEN COALESCE(NULLIF(a.payer_account_id, ''), '') <> '' THEN a.payer_account_id
					ELSE m.account_id
				END AS payer_account_id,
				m.usage_end_time
			FROM metering_records m
			LEFT JOIN bill_line_items b ON b.metering_record_id = m.id
			LEFT JOIN accounts a ON a.id = m.account_id
			WHERE b.id IS NULL
			  AND (? = '' OR m.usage_end_time <= ?)
		)
		SELECT c.billing_period_start, c.billing_period_end, c.payer_account_id
		FROM candidate_metering m
		JOIN billing_period_closes c
		  ON c.billing_period_start = m.billing_period_start
		 AND c.billing_period_end = m.billing_period_end
		 AND c.payer_account_id = m.payer_account_id
		 AND c.status = 'closed'
		WHERE (? = '' OR c.billing_period_start = ?)
		  AND (? = '' OR c.billing_period_end = ?)
		ORDER BY c.billing_period_start, c.payer_account_id
		LIMIT 1`,
		strings.TrimSpace(pricingPayerAccountID),
		strings.TrimSpace(pricingPayerAccountID),
		strings.TrimSpace(throughTime),
		strings.TrimSpace(throughTime),
		strings.TrimSpace(onlyPeriodStart),
		strings.TrimSpace(onlyPeriodStart),
		strings.TrimSpace(onlyPeriodEnd),
		strings.TrimSpace(onlyPeriodEnd),
	).Scan(&conflict.PeriodStart, &conflict.PeriodEnd, &conflict.PayerAccountID)
	if err == nil {
		conflict.EventID = eventID
		return conflict, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return scenarioClosedPeriodConflict{}, false, nil
	}
	return scenarioClosedPeriodConflict{}, false, fmt.Errorf("find closed-period pricing conflict: %w", err)
}

func isClosedPeriodPricingFailure(err error) bool {
	return errors.Is(err, persistence.ErrClosedBillingPeriod)
}
