package scenario

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func normalizeCreateCostCategoryScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.Category, &event.CategoryID, &event.DefaultValue, &event.Description, &event.Status)
	return event
}

func normalizeCreateCostCategoryRuleScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.Category, &event.CategoryID, &event.Description, &event.Value, &event.MatchType, &event.Dimension, &event.Operator, &event.TagKey, &event.ReferencedCategory, &event.ReferencedCategoryID)
	for i := range event.Values {
		event.Values[i] = strings.TrimSpace(event.Values[i])
	}
	return event
}

func normalizeCreateCostCategorySplitRuleScenarioEvent(event Event) Event {
	trimScenarioEventStrings(&event.Category, &event.CategoryID, &event.SourceValue, &event.Method, &event.Description, &event.Status)
	for i := range event.Targets {
		event.Targets[i].Value = strings.TrimSpace(event.Targets[i].Value)
	}
	return event
}

func applyCreateCostCategoryScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createCostCategory(ctx, state, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyCreateCostCategoryRuleScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createCostCategoryRule(ctx, state, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func applyCreateCostCategorySplitRuleScenarioEvent(ctx context.Context, r Runner, state *scenarioExecutionState, event Event, _ time.Time, audit ScenarioRunEvent) (ScenarioRunEvent, error) {
	if _, err := r.createCostCategorySplitRule(ctx, state, event); err != nil {
		return failScenarioRunEvent(audit, err)
	}
	return audit, nil
}

func isMissingCostCategory(err error) bool {
	return errors.Is(err, persistence.ErrCostCategoryNotFound)
}

// createCostCategory prepares an allocation dimension for a scenario lab, reusing by name so reruns remain executable.
func (r Runner) createCostCategory(ctx context.Context, state *scenarioExecutionState, event Event) (persistence.CostCategory, error) {
	if existing, err := r.categories.GetCategoryByName(ctx, event.Category); err == nil {
		state.rememberCostCategory(event, existing.ID)
		if _, refreshErr := r.categories.RefreshAssignmentsForOpenPeriods(ctx); refreshErr != nil {
			return persistence.CostCategory{}, refreshErr
		}
		return existing, nil
	} else if !isMissingCostCategory(err) {
		return persistence.CostCategory{}, err
	}

	category, err := r.categories.CreateCategory(ctx, persistence.CostCategoryCreateRequest{
		ID:           stableScenarioID("cc_scn", state.runID, event.ID, event.Category),
		Name:         event.Category,
		Description:  event.Description,
		DefaultValue: event.DefaultValue,
		Status:       event.Status,
	})
	if err != nil {
		return persistence.CostCategory{}, err
	}
	state.rememberCostCategory(event, category.ID)
	return category, nil
}

// createCostCategoryRule adds a single-condition assignment rule and refreshes open-period snapshots for lab spend.
func (r Runner) createCostCategoryRule(ctx context.Context, state *scenarioExecutionState, event Event) (persistence.CostCategoryRule, error) {
	categoryID := state.resolveCostCategoryID(event.CategoryID, event.Category)
	if categoryID == "" {
		return persistence.CostCategoryRule{}, fmt.Errorf("scenario cost category %q is not available", chooseFirst(event.CategoryID, event.Category))
	}
	if existing, ok, err := r.existingCostCategoryRule(ctx, categoryID, event.RuleOrder); err != nil {
		return persistence.CostCategoryRule{}, err
	} else if ok {
		if _, refreshErr := r.categories.RefreshAssignmentsForOpenPeriods(ctx); refreshErr != nil {
			return persistence.CostCategoryRule{}, refreshErr
		}
		return existing, nil
	}

	condition := persistence.CostCategoryRuleCondition{
		ID:               stableScenarioID("ccrc_scn", state.runID, event.ID, event.Dimension, strings.Join(event.Values, "\x00")),
		ConditionOrder:   1,
		Dimension:        event.Dimension,
		Operator:         event.Operator,
		TagKey:           event.TagKey,
		CostCategoryID:   state.resolveCostCategoryID(event.ReferencedCategoryID, event.ReferencedCategory),
		CostCategoryName: event.ReferencedCategory,
		Values:           append([]string(nil), event.Values...),
	}
	rule, err := r.categories.CreateRule(ctx, persistence.CostCategoryRuleCreateRequest{
		ID:             stableScenarioID("ccr_scn", state.runID, event.ID, event.Value),
		CostCategoryID: categoryID,
		RuleOrder:      event.RuleOrder,
		Value:          event.Value,
		Description:    event.Description,
		MatchType:      event.MatchType,
		Conditions:     []persistence.CostCategoryRuleCondition{condition},
	})
	if err != nil {
		return persistence.CostCategoryRule{}, err
	}
	return rule, nil
}

// createCostCategorySplitRule adds a split-charge rule and refreshes allocations for current open periods.
func (r Runner) createCostCategorySplitRule(ctx context.Context, state *scenarioExecutionState, event Event) (persistence.CostCategorySplitChargeRule, error) {
	categoryID := state.resolveCostCategoryID(event.CategoryID, event.Category)
	if categoryID == "" {
		return persistence.CostCategorySplitChargeRule{}, fmt.Errorf("scenario cost category %q is not available", chooseFirst(event.CategoryID, event.Category))
	}
	if existing, ok, err := r.existingSplitChargeRule(ctx, categoryID, event.SourceValue); err != nil {
		return persistence.CostCategorySplitChargeRule{}, err
	} else if ok {
		if _, refreshErr := r.splitCharges.RefreshAllocationsForOpenPeriods(ctx); refreshErr != nil {
			return persistence.CostCategorySplitChargeRule{}, refreshErr
		}
		return existing, nil
	}

	targets := make([]persistence.CostCategorySplitChargeTargetCreateRequest, 0, len(event.Targets))
	for i, target := range event.Targets {
		targets = append(targets, persistence.CostCategorySplitChargeTargetCreateRequest{
			ID:               stableScenarioID("ccst_scn", state.runID, event.ID, target.Value),
			TargetOrder:      i + 1,
			TargetValue:      target.Value,
			FixedShareMicros: target.FixedShareMicros,
		})
	}
	rule, err := r.splitCharges.CreateRule(ctx, persistence.CostCategorySplitChargeRuleCreateRequest{
		ID:             stableScenarioID("ccs_scn", state.runID, event.ID, event.SourceValue),
		CostCategoryID: categoryID,
		SourceValue:    event.SourceValue,
		Method:         event.Method,
		Description:    event.Description,
		Status:         event.Status,
		Targets:        targets,
	})
	if err != nil {
		return persistence.CostCategorySplitChargeRule{}, err
	}
	return rule, nil
}

// existingCostCategoryRule finds an already-created rule at the same order for rerunnable scenario definitions.
func (r Runner) existingCostCategoryRule(ctx context.Context, categoryID string, ruleOrder int) (persistence.CostCategoryRule, bool, error) {
	rules, err := r.categories.ListRules(ctx, categoryID)
	if err != nil {
		return persistence.CostCategoryRule{}, false, err
	}
	for _, rule := range rules {
		if rule.RuleOrder == ruleOrder {
			return rule, true, nil
		}
	}
	return persistence.CostCategoryRule{}, false, nil
}

// existingSplitChargeRule finds an already-created split source for rerunnable scenario definitions.
func (r Runner) existingSplitChargeRule(ctx context.Context, categoryID, sourceValue string) (persistence.CostCategorySplitChargeRule, bool, error) {
	rules, err := r.splitCharges.ListRules(ctx, categoryID)
	if err != nil {
		return persistence.CostCategorySplitChargeRule{}, false, err
	}
	for _, rule := range rules {
		if rule.SourceValue == sourceValue {
			return rule, true, nil
		}
	}
	return persistence.CostCategorySplitChargeRule{}, false, nil
}
