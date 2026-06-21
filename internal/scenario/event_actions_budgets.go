package scenario

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"aws-billing-simulator/internal/persistence"
)

type scenarioBudgetEventPayload struct {
	ID                 string
	Action             EventAction
	BudgetID           string
	BudgetName         string
	Description        string
	BillingPeriodStart string
	BillingPeriodEnd   string
	CurrencyCode       string
	BudgetAmountMicros int64
	ScopeType          string
	ScopeKey           string
	ScopeValue         string
	Status             string
	Thresholds         []BudgetThreshold
}

// newBudgetScenarioEventActionSpec binds budget actions to a narrow scenario payload.
func newBudgetScenarioEventActionSpec(action EventAction) scenarioEventActionSpec {
	return scenarioEventPayloadActionSpec[scenarioBudgetEventPayload]{
		action:           action,
		payloadFromEvent: budgetPayloadFromEvent,
		mergePayload:     mergeBudgetPayload,
		normalize:        normalizeBudgetScenarioPayload,
		validate:         validateBudgetScenarioPayload,
		apply:            applyBudgetScenarioPayload,
	}.asEventActionSpec()
}

func budgetPayloadFromEvent(event Event) scenarioBudgetEventPayload {
	return scenarioBudgetEventPayload{
		ID:                 event.ID,
		Action:             event.Action,
		BudgetID:           event.BudgetID,
		BudgetName:         event.BudgetName,
		Description:        event.Description,
		BillingPeriodStart: event.BillingPeriodStart,
		BillingPeriodEnd:   event.BillingPeriodEnd,
		CurrencyCode:       event.CurrencyCode,
		BudgetAmountMicros: event.BudgetAmountMicros,
		ScopeType:          event.ScopeType,
		ScopeKey:           event.ScopeKey,
		ScopeValue:         event.ScopeValue,
		Status:             event.Status,
		Thresholds:         append([]BudgetThreshold(nil), event.Thresholds...),
	}
}

func mergeBudgetPayload(event Event, payload scenarioBudgetEventPayload) Event {
	event.BudgetID = payload.BudgetID
	event.BudgetName = payload.BudgetName
	event.Description = payload.Description
	event.BillingPeriodStart = payload.BillingPeriodStart
	event.BillingPeriodEnd = payload.BillingPeriodEnd
	event.CurrencyCode = payload.CurrencyCode
	event.BudgetAmountMicros = payload.BudgetAmountMicros
	event.ScopeType = payload.ScopeType
	event.ScopeKey = payload.ScopeKey
	event.ScopeValue = payload.ScopeValue
	event.Status = payload.Status
	event.Thresholds = append([]BudgetThreshold(nil), payload.Thresholds...)
	return event
}

func normalizeBudgetScenarioPayload(payload scenarioBudgetEventPayload) scenarioBudgetEventPayload {
	switch payload.Action {
	case EventActionCreateBudget:
		trimScenarioEventStrings(&payload.BudgetID, &payload.BudgetName, &payload.Description, &payload.BillingPeriodStart, &payload.BillingPeriodEnd, &payload.ScopeType, &payload.ScopeKey, &payload.ScopeValue, &payload.Status)
		trimUpperScenarioEventString(&payload.CurrencyCode)
		for i := range payload.Thresholds {
			trimScenarioEventStrings(&payload.Thresholds[i].ID, &payload.Thresholds[i].Type)
		}
	case EventActionRefreshBudgetForecasts:
		trimScenarioEventStrings(&payload.BillingPeriodStart, &payload.BillingPeriodEnd)
	}
	return payload
}

func validateBudgetScenarioPayload(path string, payload scenarioBudgetEventPayload, problems *validationProblems) {
	switch payload.Action {
	case EventActionCreateBudget:
		validateCreateBudgetScenarioPayload(path, payload, problems)
	case EventActionRefreshBudgetForecasts:
		validateRefreshBudgetForecastsScenarioPayload(path, payload, problems)
	default:
		problems.add("%s.action %q is not a budget action", path, payload.Action)
	}
}

func validateBudgetPeriodPayload(path string, payload scenarioBudgetEventPayload, problems *validationProblems) {
	validateOptionalDate(path+".billing_period_start", payload.BillingPeriodStart, problems)
	validateOptionalDate(path+".billing_period_end", payload.BillingPeriodEnd, problems)
	if payload.BillingPeriodStart != "" && payload.BillingPeriodEnd == "" {
		problems.add("%s.billing_period_end is required when billing_period_start is set", path)
	}
	if payload.BillingPeriodEnd != "" && payload.BillingPeriodStart == "" {
		problems.add("%s.billing_period_start is required when billing_period_end is set", path)
	}
}

func validateRefreshBudgetForecastsScenarioPayload(path string, payload scenarioBudgetEventPayload, problems *validationProblems) {
	validateBudgetPeriodPayload(path, payload, problems)
}

func validateCreateBudgetScenarioPayload(path string, payload scenarioBudgetEventPayload, problems *validationProblems) {
	validateBudgetPeriodPayload(path, payload, problems)
	if payload.BudgetName == "" {
		problems.add("%s.budget_name is required for create_budget", path)
	}
	if payload.BillingPeriodStart == "" || payload.BillingPeriodEnd == "" {
		problems.add("%s.billing_period_start and %s.billing_period_end are required for create_budget", path, path)
	}
	if payload.BudgetAmountMicros <= 0 {
		problems.add("%s.budget_amount_micros must be greater than zero for create_budget", path)
	}
	switch payload.ScopeType {
	case persistence.BudgetScopeAccount, persistence.BudgetScopeService:
		if payload.ScopeKey != "" {
			problems.add("%s.scope_key is only supported for tag and Cost Category budgets", path)
		}
	case persistence.BudgetScopeTag, persistence.BudgetScopeCostCategory:
		if payload.ScopeKey == "" {
			problems.add("%s.scope_key is required for %s budgets", path, payload.ScopeType)
		}
	default:
		problems.add("%s.scope_type %q is not supported for create_budget", path, payload.ScopeType)
	}
	if payload.ScopeValue == "" {
		problems.add("%s.scope_value is required for create_budget", path)
	}
	if len(payload.Thresholds) == 0 {
		problems.add("%s.thresholds needs at least one threshold for create_budget", path)
	}
	seen := map[string]bool{}
	for i, threshold := range payload.Thresholds {
		thresholdPath := fmt.Sprintf("%s.thresholds[%d]", path, i)
		switch threshold.Type {
		case persistence.BudgetThresholdTypeActual, persistence.BudgetThresholdTypeForecast:
		default:
			problems.add("%s.type %q is not supported", thresholdPath, threshold.Type)
		}
		if threshold.BasisPoints <= 0 {
			problems.add("%s.basis_points must be greater than zero", thresholdPath)
		}
		if threshold.BasisPoints > 100000 {
			problems.add("%s.basis_points must be 100000 or fewer", thresholdPath)
		}
		key := threshold.Type + ":" + strconv.Itoa(threshold.BasisPoints)
		if seen[key] {
			problems.add("%s duplicates threshold %q", thresholdPath, key)
		}
		seen[key] = true
	}
}

func applyBudgetScenarioPayload(ctx context.Context, r Runner, state *scenarioExecutionState, payload scenarioBudgetEventPayload, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	switch payload.Action {
	case EventActionCreateBudget:
		if _, err := r.createBudget(ctx, state, payload); err != nil {
			return failScenarioRunEvent(audit, err)
		}
	case EventActionRefreshBudgetForecasts:
		if err := r.refreshBudgetForecasts(ctx, payload); err != nil {
			return failScenarioRunEvent(audit, err)
		}
	default:
		return failScenarioRunEvent(audit, fmt.Errorf("scenario event action %q is not a budget action", payload.Action))
	}
	return audit, nil
}

// createBudget prepares a budget lab guardrail and reuses matching rows so scenario reruns stay executable.
func (r Runner) createBudget(ctx context.Context, state *scenarioExecutionState, payload scenarioBudgetEventPayload) (persistence.Budget, error) {
	if existing, ok, err := r.existingScenarioBudget(ctx, payload); err != nil {
		return persistence.Budget{}, err
	} else if ok {
		return existing, nil
	}

	scopeValue := payload.ScopeValue
	if payload.ScopeType == persistence.BudgetScopeAccount {
		scopeValue = state.resolveAccountID("", payload.ScopeValue)
	}
	thresholds := make([]persistence.BudgetThresholdCreateRequest, 0, len(payload.Thresholds))
	for i, threshold := range payload.Thresholds {
		thresholdID := threshold.ID
		if thresholdID == "" {
			thresholdID = stableScenarioID("budt_scn", state.runID, payload.ID, threshold.Type, strconv.Itoa(threshold.BasisPoints), strconv.Itoa(i))
		}
		thresholds = append(thresholds, persistence.BudgetThresholdCreateRequest{
			ID:                   thresholdID,
			ThresholdType:        threshold.Type,
			ThresholdBasisPoints: threshold.BasisPoints,
		})
	}
	budgetID := payload.BudgetID
	if budgetID == "" {
		budgetID = stableScenarioID("bud_scn", state.runID, payload.ID, payload.BudgetName)
	}
	return r.budgets.CreateBudget(ctx, persistence.BudgetCreateRequest{
		ID:                 budgetID,
		Name:               payload.BudgetName,
		Description:        payload.Description,
		BillingPeriodStart: payload.BillingPeriodStart,
		BillingPeriodEnd:   payload.BillingPeriodEnd,
		BudgetAmountMicros: payload.BudgetAmountMicros,
		CurrencyCode:       payload.CurrencyCode,
		ScopeType:          payload.ScopeType,
		ScopeKey:           payload.ScopeKey,
		ScopeValue:         scopeValue,
		Status:             payload.Status,
		Thresholds:         thresholds,
	})
}

// refreshBudgetForecasts mirrors the browser refresh action for packaged budget labs.
func (r Runner) refreshBudgetForecasts(ctx context.Context, payload scenarioBudgetEventPayload) error {
	forecast, err := r.budgets.RefreshForecastSummaries(ctx, persistence.BudgetForecastRefreshRequest{
		BillingPeriodStart: payload.BillingPeriodStart,
		BillingPeriodEnd:   payload.BillingPeriodEnd,
	})
	if err != nil {
		return err
	}
	evaluations, err := r.budgets.EvaluateBudgets(ctx, persistence.BudgetEvaluationRequest{
		BillingPeriodStart: forecast.BillingPeriodStart,
		BillingPeriodEnd:   forecast.BillingPeriodEnd,
	})
	if err != nil {
		return err
	}
	_, err = r.budgets.RecordAlertNotifications(ctx, evaluations)
	return err
}

func (r Runner) existingScenarioBudget(ctx context.Context, payload scenarioBudgetEventPayload) (persistence.Budget, bool, error) {
	var id string
	var err error
	if payload.BudgetID != "" {
		err = r.db.QueryRowContext(ctx, `SELECT id FROM budgets WHERE id = ?`, payload.BudgetID).Scan(&id)
	} else {
		err = r.db.QueryRowContext(ctx, `SELECT id
			FROM budgets
			WHERE billing_period_start = ?
			  AND billing_period_end = ?
			  AND lower(name) = lower(?)`,
			payload.BillingPeriodStart,
			payload.BillingPeriodEnd,
			payload.BudgetName,
		).Scan(&id)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return persistence.Budget{}, false, nil
	}
	if err != nil {
		return persistence.Budget{}, false, fmt.Errorf("check existing scenario budget %q: %w", payload.BudgetName, err)
	}
	budget, err := r.budgets.GetBudget(ctx, id)
	if err != nil {
		return persistence.Budget{}, false, err
	}
	return budget, true, nil
}
