package scenario

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func normalizeCreateSavingsPlanScenarioEvent(event Event) Event {
	event = normalizePayerScenarioEvent(event)
	trimScenarioEventStrings(&event.OwnerAccount, &event.OwnerAccountID, &event.SavingsPlanID, &event.UsageType, &event.Operation, &event.Region, &event.TermStartAt, &event.TermEndAt, &event.SharingScope, &event.Description, &event.Status)
	trimUpperScenarioEventString(&event.CurrencyCode)
	return event
}

func normalizeCreateSavedReportScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.ReportID, &event.ReportName, &event.Description, &event.OwnerAccount, &event.OwnerAccountID, &event.OwnerRole, &event.DateRangeStart, &event.DateRangeEnd, &event.Granularity, &event.ChartType)
	event.Filters = normalizeStringListMap(event.Filters)
	for i := range event.Groupings {
		trimScenarioEventStrings(&event.Groupings[i].Type, &event.Groupings[i].Key)
	}
	event.Metrics = normalizeStringList(event.Metrics)
	return event
}

func validateCreateSavingsPlanScenarioEvent(path string, event Event, problems *validationProblems) {
	validateOptionalTimestamp(path+".term_start_at", event.TermStartAt, problems)
	validateOptionalTimestamp(path+".term_end_at", event.TermEndAt, problems)
	if event.TermStartAt == "" || event.TermEndAt == "" {
		problems.add("%s.term_start_at and %s.term_end_at are required for create_savings_plan", path, path)
	}
	if event.TermStartAt != "" && event.TermEndAt != "" {
		start, startOK := parseScenarioDateOrTimestamp(event.TermStartAt)
		end, endOK := parseScenarioDateOrTimestamp(event.TermEndAt)
		if startOK && endOK && !start.Before(end) {
			problems.add("%s.term_start_at must be before term_end_at", path)
		}
	}
	validateCreateSavingsPlanEvent(path, event, problems)
}

func validateCreateSavedReportScenarioEvent(path string, event Event, problems *validationProblems) {
	validateOptionalDate(path+".date_range_start", event.DateRangeStart, problems)
	validateOptionalDate(path+".date_range_end", event.DateRangeEnd, problems)
	if event.DateRangeStart != "" && event.DateRangeEnd == "" {
		problems.add("%s.date_range_end is required when date_range_start is set", path)
	}
	if event.DateRangeEnd != "" && event.DateRangeStart == "" {
		problems.add("%s.date_range_start is required when date_range_end is set", path)
	}
	validateCreateSavedReportEvent(path, event, problems)
}

func validateCreateSavedReportScenarioEventSemantics(path, organizationTemplate string, event Event, createdAccounts map[string]string, problems *validationProblems) {
	validateScenarioAccountReference(path+".owner_account", organizationTemplate, event.OwnerAccountID, event.OwnerAccount, createdAccounts, problems)
}

func validateCreateSavingsPlanScenarioEventSemantics(path, organizationTemplate string, event Event, createdAccounts map[string]string, problems *validationProblems) {
	validateScenarioAccountReference(path+".payer_account", organizationTemplate, event.PayerAccountID, event.PayerAccount, createdAccounts, problems)
	validateScenarioAccountReference(path+".owner_account", organizationTemplate, event.OwnerAccountID, event.OwnerAccount, createdAccounts, problems)
}

func applyCreateSavingsPlanScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createSavingsPlan(ctx, state, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyCreateSavedReportScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createSavedReport(ctx, state, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

// createSavingsPlan prepares a simplified Compute Savings Plan for commitment coverage labs.
func (r Runner) createSavingsPlan(ctx context.Context, state *scenarioExecutionState, event Event) (persistence.SavingsPlanPurchase, error) {
	termStart, ok := parseScenarioDateOrTimestamp(event.TermStartAt)
	if !ok {
		return persistence.SavingsPlanPurchase{}, fmt.Errorf("scenario savings plan %q term_start_at is invalid", event.ID)
	}
	termEnd, ok := parseScenarioDateOrTimestamp(event.TermEndAt)
	if !ok {
		return persistence.SavingsPlanPurchase{}, fmt.Errorf("scenario savings plan %q term_end_at is invalid", event.ID)
	}
	purchaseID := event.SavingsPlanID
	if purchaseID == "" {
		purchaseID = stableScenarioID("sp_scn", state.runID, event.ID, event.OwnerAccount, event.OwnerAccountID, event.UsageType, event.Region)
	}
	return r.savingsPlans.CreatePurchase(ctx, persistence.SavingsPlanPurchaseCreateRequest{
		ID:                     purchaseID,
		PayerAccountID:         state.resolveAccountID(event.PayerAccountID, event.PayerAccount),
		OwnerAccountID:         state.resolveAccountID(event.OwnerAccountID, event.OwnerAccount),
		ReferenceUsageType:     event.UsageType,
		Operation:              event.Operation,
		RegionCode:             event.Region,
		SharingScope:           event.SharingScope,
		TermStartTime:          termStart.Format(time.RFC3339),
		TermEndTime:            termEnd.Format(time.RFC3339),
		HourlyCommitmentMicros: event.HourlyCommitmentMicros,
		UpfrontFeeMicros:       event.UpfrontFeeMicros,
		CurrencyCode:           event.CurrencyCode,
		Status:                 event.Status,
		Description:            event.Description,
	})
}

// createSavedReport seeds a Cost Explorer drilldown report and updates it on rerun.
func (r Runner) createSavedReport(ctx context.Context, state *scenarioExecutionState, event Event) (persistence.SavedReport, error) {
	request := persistence.SavedReportCreateRequest{
		ID:             event.ReportID,
		Name:           event.ReportName,
		Description:    event.Description,
		OwnerAccountID: state.resolveAccountID(event.OwnerAccountID, event.OwnerAccount),
		OwnerRole:      event.OwnerRole,
		DateRangeStart: event.DateRangeStart,
		DateRangeEnd:   event.DateRangeEnd,
		Granularity:    event.Granularity,
		Filters:        event.Filters,
		Groupings:      scenarioReportGroupings(event.Groupings),
		Metrics:        append([]string(nil), event.Metrics...),
		ChartType:      event.ChartType,
	}
	if request.OwnerRole == "" {
		request.OwnerRole = defaultScenarioReportRole
	}
	if existing, ok, err := r.existingScenarioSavedReport(ctx, request); err != nil {
		return persistence.SavedReport{}, err
	} else if ok {
		return r.rewriteScenarioSavedReport(ctx, existing.ID, request)
	}
	if request.ID == "" {
		request.ID = stableScenarioID("sr_scn", state.runID, event.ID, event.ReportName)
	}
	return r.reports.Create(ctx, request)
}

func (r Runner) existingScenarioSavedReport(ctx context.Context, request persistence.SavedReportCreateRequest) (persistence.SavedReport, bool, error) {
	var id string
	var err error
	if request.ID != "" {
		err = r.db.QueryRowContext(ctx, `SELECT id FROM saved_reports WHERE id = ?`, request.ID).Scan(&id)
	} else {
		err = r.db.QueryRowContext(ctx, `SELECT id
			FROM saved_reports
			WHERE owner_account_id = ?
			  AND owner_role = ?
			  AND lower(name) = lower(?)`,
			request.OwnerAccountID,
			request.OwnerRole,
			request.Name,
		).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return persistence.SavedReport{}, false, nil
	}
	if err != nil {
		return persistence.SavedReport{}, false, fmt.Errorf("check existing scenario saved report %q: %w", request.Name, err)
	}
	report, err := r.reports.Get(ctx, id)
	if err != nil {
		return persistence.SavedReport{}, false, err
	}
	return report, true, nil
}

func (r Runner) rewriteScenarioSavedReport(ctx context.Context, id string, request persistence.SavedReportCreateRequest) (persistence.SavedReport, error) {
	return r.reports.Update(ctx, persistence.SavedReportUpdateRequest{
		ID:             id,
		Name:           request.Name,
		Description:    request.Description,
		OwnerAccountID: request.OwnerAccountID,
		OwnerRole:      request.OwnerRole,
		DateRangeStart: request.DateRangeStart,
		DateRangeEnd:   request.DateRangeEnd,
		Granularity:    request.Granularity,
		Filters:        request.Filters,
		Groupings:      request.Groupings,
		Metrics:        request.Metrics,
		ChartType:      request.ChartType,
	})
}

func scenarioReportGroupings(groupings []ReportGrouping) []persistence.SavedReportGrouping {
	converted := make([]persistence.SavedReportGrouping, 0, len(groupings))
	for _, grouping := range groupings {
		converted = append(converted, persistence.SavedReportGrouping{
			Type: grouping.Type,
			Key:  grouping.Key,
		})
	}
	return converted
}
