package persistence

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// costCategoryPreviewEvaluator holds ordered Cost Category rules for deterministic in-memory evaluation.
type costCategoryPreviewEvaluator struct {
	categories      map[string]CostCategory
	rulesByCategory map[string][]CostCategoryRule
}

type costCategoryPreviewAssignment struct {
	Value     string
	RuleID    string
	RuleOrder int
	Matched   bool
}

func (r CostCategoryRepository) newCostCategoryPreviewEvaluator(ctx context.Context) (costCategoryPreviewEvaluator, error) {
	return newCostCategoryEvaluator(ctx, r.db)
}

// newCostCategoryEvaluator loads categories and ordered rules from storage into a pure evaluator.
func newCostCategoryEvaluator(ctx context.Context, q costCategoryQueryer) (costCategoryPreviewEvaluator, error) {
	categories, err := listCostCategories(ctx, q)
	if err != nil {
		return costCategoryPreviewEvaluator{}, err
	}
	evaluator := costCategoryPreviewEvaluator{
		categories:      map[string]CostCategory{},
		rulesByCategory: map[string][]CostCategoryRule{},
	}
	for _, category := range categories {
		evaluator.categories[category.ID] = category
		rules, err := listCostCategoryRules(ctx, q, category.ID)
		if err != nil {
			return costCategoryPreviewEvaluator{}, err
		}
		evaluator.rulesByCategory[category.ID] = rules
	}
	return evaluator, nil
}

func (a costCategoryPreviewAssignment) ValueForMatchedRule() string {
	if !a.Matched {
		return ""
	}
	return a.Value
}

func (e costCategoryPreviewEvaluator) orderedCategories() []CostCategory {
	categories := make([]CostCategory, 0, len(e.categories))
	for _, category := range e.categories {
		categories = append(categories, category)
	}
	sort.Slice(categories, func(i, j int) bool {
		left := strings.ToLower(categories[i].Name)
		right := strings.ToLower(categories[j].Name)
		if left == right {
			return categories[i].ID < categories[j].ID
		}
		return left < right
	})
	return categories
}

func (e costCategoryPreviewEvaluator) evaluateCategory(item BillLineItem, categoryID string, stack map[string]bool) (costCategoryPreviewAssignment, error) {
	category, ok := e.categories[categoryID]
	if !ok {
		return costCategoryPreviewAssignment{}, fmt.Errorf("cost category %q is not loaded for preview", categoryID)
	}
	matches, err := e.matchingRules(item, categoryID, stack)
	if err != nil {
		return costCategoryPreviewAssignment{}, err
	}
	if len(matches) == 0 {
		return costCategoryPreviewAssignment{Value: category.DefaultValue}, nil
	}
	return costCategoryPreviewAssignment{
		Value:     matches[0].Value,
		RuleID:    matches[0].ID,
		RuleOrder: matches[0].RuleOrder,
		Matched:   true,
	}, nil
}

func (e costCategoryPreviewEvaluator) matchingRules(item BillLineItem, categoryID string, stack map[string]bool) ([]CostCategoryRule, error) {
	if stack[categoryID] {
		categoryName := categoryID
		if category, ok := e.categories[categoryID]; ok {
			categoryName = category.Name
		}
		return nil, fmt.Errorf("cost category rule reference cycle includes %q", categoryName)
	}
	if _, ok := e.categories[categoryID]; !ok {
		return nil, fmt.Errorf("cost category %q is not loaded for preview", categoryID)
	}
	stack[categoryID] = true
	defer delete(stack, categoryID)

	var matches []CostCategoryRule
	for _, rule := range e.rulesByCategory[categoryID] {
		matched, err := e.ruleMatches(item, rule, stack)
		if err != nil {
			return nil, err
		}
		if matched {
			matches = append(matches, rule)
		}
	}
	return matches, nil
}

func (e costCategoryPreviewEvaluator) ruleMatches(item BillLineItem, rule CostCategoryRule, stack map[string]bool) (bool, error) {
	for _, condition := range rule.Conditions {
		matched, err := e.conditionMatches(item, condition, stack)
		if err != nil {
			return false, err
		}
		if !matched {
			return false, nil
		}
	}
	return true, nil
}

func (e costCategoryPreviewEvaluator) conditionMatches(item BillLineItem, condition CostCategoryRuleCondition, stack map[string]bool) (bool, error) {
	actualValues, err := e.conditionActualValues(item, condition, stack)
	if err != nil {
		return false, err
	}
	matched := costCategoryPreviewAnyValueMatches(actualValues, condition.Values)
	switch condition.Operator {
	case CostCategoryRuleOperatorNotIn:
		return !matched, nil
	default:
		return matched, nil
	}
}

func (e costCategoryPreviewEvaluator) conditionActualValues(item BillLineItem, condition CostCategoryRuleCondition, stack map[string]bool) ([]string, error) {
	switch condition.Dimension {
	case CostCategoryRuleMatchAccount:
		return []string{item.UsageAccountID}, nil
	case CostCategoryRuleMatchService:
		return uniqueNonEmptyStrings(item.ServiceCode, item.ServiceName), nil
	case CostCategoryRuleMatchRegion:
		return []string{item.RegionCode}, nil
	case CostCategoryRuleMatchUsageType:
		return []string{item.UsageType}, nil
	case CostCategoryRuleMatchLineItemType:
		return []string{item.LineItemType}, nil
	case CostCategoryRuleMatchTag:
		value, ok := item.TagSnapshot[condition.TagKey]
		if !ok {
			return nil, nil
		}
		return []string{value}, nil
	case CostCategoryRuleMatchCostCategory:
		assignment, err := e.evaluateCategory(item, condition.CostCategoryID, stack)
		if err != nil {
			return nil, err
		}
		return []string{assignment.Value}, nil
	default:
		return nil, fmt.Errorf("cost category rule condition dimension %q is not supported", condition.Dimension)
	}
}

func costCategoryPreviewAnyValueMatches(actualValues, expectedValues []string) bool {
	for _, actual := range actualValues {
		actual = strings.TrimSpace(actual)
		if actual == "" {
			continue
		}
		for _, expected := range expectedValues {
			if actual == strings.TrimSpace(expected) {
				return true
			}
		}
	}
	return false
}

func uniqueNonEmptyStrings(values ...string) []string {
	seen := map[string]bool{}
	var unique []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}
