package persistence

import (
	"strings"
	"testing"
)

func TestCostCategoryEvaluatorOrdersFirstMatchAndKeepsShadowedMatches(t *testing.T) {
	t.Parallel()

	evaluator := costCategoryPreviewEvaluator{
		categories: map[string]CostCategory{
			"cc_product": {
				ID:           "cc_product",
				Name:         "Product",
				DefaultValue: "Unmapped",
			},
		},
		rulesByCategory: map[string][]CostCategoryRule{
			"cc_product": {
				{
					ID:        "rule_storefront",
					RuleOrder: 10,
					Value:     "Storefront",
					Conditions: []CostCategoryRuleCondition{
						{Dimension: CostCategoryRuleMatchTag, TagKey: "app", Values: []string{"storefront"}},
					},
				},
				{
					ID:        "rule_compute",
					RuleOrder: 20,
					Value:     "Compute",
					Conditions: []CostCategoryRuleCondition{
						{Dimension: CostCategoryRuleMatchService, Values: []string{serviceAmazonEC2}},
					},
				},
			},
		},
	}
	item := BillLineItem{
		ServiceCode: serviceAmazonEC2,
		ServiceName: "Amazon Elastic Compute Cloud",
		TagSnapshot: map[string]string{"app": "storefront"},
	}

	matches, err := evaluator.matchingRules(item, "cc_product", map[string]bool{})
	if err != nil {
		t.Fatalf("matchingRules() error = %v", err)
	}
	if len(matches) != 2 || matches[0].ID != "rule_storefront" || matches[1].ID != "rule_compute" {
		t.Fatalf("matchingRules() = %+v, want Storefront first and Compute as shadowed match", matches)
	}
	assignment, err := evaluator.evaluateCategory(item, "cc_product", map[string]bool{})
	if err != nil {
		t.Fatalf("evaluateCategory() error = %v", err)
	}
	if assignment.Value != "Storefront" || assignment.RuleID != "rule_storefront" || assignment.RuleOrder != 10 || !assignment.Matched {
		t.Fatalf("evaluateCategory() = %+v, want first matching Storefront rule", assignment)
	}

	defaultAssignment, err := evaluator.evaluateCategory(BillLineItem{ServiceCode: serviceAmazonS3}, "cc_product", map[string]bool{})
	if err != nil {
		t.Fatalf("evaluateCategory(default) error = %v", err)
	}
	if defaultAssignment.Value != "Unmapped" || defaultAssignment.Matched {
		t.Fatalf("evaluateCategory(default) = %+v, want category default", defaultAssignment)
	}
}

func TestCostCategoryEvaluatorDetectsReferenceCycles(t *testing.T) {
	t.Parallel()

	evaluator := costCategoryPreviewEvaluator{
		categories: map[string]CostCategory{
			"cc_product":     {ID: "cc_product", Name: "Product", DefaultValue: "Unmapped"},
			"cc_environment": {ID: "cc_environment", Name: "Environment", DefaultValue: "Unknown"},
		},
		rulesByCategory: map[string][]CostCategoryRule{
			"cc_product": {
				{
					ID:        "rule_product_from_environment",
					RuleOrder: 10,
					Value:     "Storefront",
					Conditions: []CostCategoryRuleCondition{
						{
							Dimension:      CostCategoryRuleMatchCostCategory,
							CostCategoryID: "cc_environment",
							Values:         []string{"Production"},
						},
					},
				},
			},
			"cc_environment": {
				{
					ID:        "rule_environment_from_product",
					RuleOrder: 10,
					Value:     "Production",
					Conditions: []CostCategoryRuleCondition{
						{
							Dimension:      CostCategoryRuleMatchCostCategory,
							CostCategoryID: "cc_product",
							Values:         []string{"Storefront"},
						},
					},
				},
			},
		},
	}

	_, err := evaluator.matchingRules(BillLineItem{}, "cc_product", map[string]bool{})
	if err == nil || !strings.Contains(err.Error(), `reference cycle includes "Product"`) {
		t.Fatalf("matchingRules(cycle) error = %v, want Product reference cycle", err)
	}
}
