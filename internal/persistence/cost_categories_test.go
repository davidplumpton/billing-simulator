package persistence

import (
	"context"
	"strings"
	"testing"
)

func TestCostCategoryRepositoryCreatesOrderedRuleModel(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewCostCategoryRepository(db)

	environment, err := repo.CreateCategory(ctx, CostCategoryCreateRequest{
		Name:         "Environment",
		Description:  "Deployment lifecycle",
		DefaultValue: "Unmapped",
	})
	if err != nil {
		t.Fatalf("CreateCategory(Environment) error = %v", err)
	}
	product, err := repo.CreateCategory(ctx, CostCategoryCreateRequest{
		Name:        "Product",
		Description: "Business product showback",
	})
	if err != nil {
		t.Fatalf("CreateCategory(Product) error = %v", err)
	}

	if _, err := repo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: product.ID,
		RuleOrder:      20,
		Value:          "Shared Platform",
		Conditions: []CostCategoryRuleCondition{
			{
				Dimension: CostCategoryRuleMatchService,
				Values:    []string{"AWS Data Transfer", "AWS Support"},
			},
		},
	}); err != nil {
		t.Fatalf("CreateRule(shared platform) error = %v", err)
	}

	storefront, err := repo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: product.ID,
		RuleOrder:      10,
		Value:          "Storefront",
		Description:    "Storefront production and related EC2 usage",
		Conditions: []CostCategoryRuleCondition{
			{
				Dimension: CostCategoryRuleMatchAccount,
				Values:    []string{"111122223333", "444455556666"},
			},
			{
				Dimension: CostCategoryRuleMatchService,
				Values:    []string{"AmazonEC2"},
			},
			{
				Dimension: CostCategoryRuleMatchRegion,
				Values:    []string{"us-east-1"},
			},
			{
				Dimension: CostCategoryRuleMatchUsageType,
				Values:    []string{"BoxUsage:t3.medium"},
			},
			{
				Dimension: CostCategoryRuleMatchLineItemType,
				Values:    []string{"Usage"},
			},
			{
				Dimension: CostCategoryRuleMatchTag,
				TagKey:    "app",
				Values:    []string{"storefront"},
			},
			{
				Dimension:        CostCategoryRuleMatchCostCategory,
				CostCategoryName: "Environment",
				Values:           []string{"Production"},
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateRule(storefront) error = %v", err)
	}
	if storefront.CostCategoryName != "Product" {
		t.Fatalf("CostCategoryName = %q, want Product", storefront.CostCategoryName)
	}
	if len(storefront.Conditions) != 7 {
		t.Fatalf("storefront conditions = %d, want 7", len(storefront.Conditions))
	}

	rules, err := repo.ListRules(ctx, product.ID)
	if err != nil {
		t.Fatalf("ListRules(Product) error = %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("ListRules(Product) count = %d, want 2", len(rules))
	}
	if rules[0].Value != "Storefront" || rules[0].RuleOrder != 10 {
		t.Fatalf("first rule = (%q, %d), want Storefront order 10", rules[0].Value, rules[0].RuleOrder)
	}
	if rules[1].Value != "Shared Platform" || rules[1].RuleOrder != 20 {
		t.Fatalf("second rule = (%q, %d), want Shared Platform order 20", rules[1].Value, rules[1].RuleOrder)
	}

	conditions := costCategoryConditionsByDimension(rules[0].Conditions)
	if got := conditions[CostCategoryRuleMatchAccount].Values; len(got) != 2 || got[0] != "111122223333" || got[1] != "444455556666" {
		t.Fatalf("account condition values = %#v, want two account IDs", got)
	}
	tagCondition := conditions[CostCategoryRuleMatchTag]
	if tagCondition.TagKey != "app" || tagCondition.Values[0] != "storefront" {
		t.Fatalf("tag condition = (%q, %#v), want app=storefront", tagCondition.TagKey, tagCondition.Values)
	}
	categoryCondition := conditions[CostCategoryRuleMatchCostCategory]
	if categoryCondition.CostCategoryID != environment.ID || categoryCondition.CostCategoryName != "Environment" || categoryCondition.Values[0] != "Production" {
		t.Fatalf("cost category condition = (%q, %q, %#v), want Environment=Production", categoryCondition.CostCategoryID, categoryCondition.CostCategoryName, categoryCondition.Values)
	}

	categories, err := repo.ListCategories(ctx)
	if err != nil {
		t.Fatalf("ListCategories() error = %v", err)
	}
	if len(categories) != 2 || categories[0].Name != "Environment" || categories[1].Name != "Product" {
		t.Fatalf("ListCategories() = %#v, want Environment then Product", categories)
	}
}

func TestCostCategoryRepositoryValidatesRules(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	repo := NewCostCategoryRepository(db)
	category, err := repo.CreateCategory(ctx, CostCategoryCreateRequest{Name: "Product"})
	if err != nil {
		t.Fatalf("CreateCategory() error = %v", err)
	}

	if _, err := repo.CreateCategory(ctx, CostCategoryCreateRequest{Name: " product "}); err == nil || !strings.Contains(err.Error(), "cost_categories_name") {
		t.Fatalf("CreateCategory(duplicate) error = %v, want unique name error", err)
	}
	if _, err := repo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: category.ID,
		RuleOrder:      1,
		Value:          "Storefront",
	}); err == nil || !strings.Contains(err.Error(), "at least one condition") {
		t.Fatalf("CreateRule(no conditions) error = %v, want validation error", err)
	}
	if _, err := repo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: category.ID,
		RuleOrder:      1,
		Value:          "Storefront",
		Conditions: []CostCategoryRuleCondition{
			{
				Dimension: CostCategoryRuleMatchTag,
				Values:    []string{"storefront"},
			},
		},
	}); err == nil || !strings.Contains(err.Error(), "tag key is required") {
		t.Fatalf("CreateRule(tag without key) error = %v, want validation error", err)
	}
	if _, err := repo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: category.ID,
		RuleOrder:      1,
		Value:          "Storefront",
		Conditions: []CostCategoryRuleCondition{
			{
				Dimension: CostCategoryRuleMatchCostCategory,
				Values:    []string{"Production"},
			},
		},
	}); err == nil || !strings.Contains(err.Error(), "referenced cost category is required") {
		t.Fatalf("CreateRule(cost category without reference) error = %v, want validation error", err)
	}
}

func TestCostCategorySchemaRejectsInvalidRules(t *testing.T) {
	t.Parallel()

	db := openTestWorkspace(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO cost_categories (id, name) VALUES (?, ?)`, "cc_product", "Product"); err != nil {
		t.Fatalf("insert cost category: %v", err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO cost_category_rules (id, cost_category_id, rule_order, value) VALUES (?, ?, ?, ?)`, "ccr_storefront", "cc_product", 1, "Storefront"); err != nil {
		t.Fatalf("insert cost category rule: %v", err)
	}

	assertExecFails(t, db, `INSERT INTO cost_category_rule_conditions (
		id,
		rule_id,
		condition_order,
		dimension,
		values_json
	) VALUES (?, ?, ?, ?, ?)`, "ccrc_bad_dimension", "ccr_storefront", 1, "availability_zone", `["us-east-1a"]`)

	assertExecFails(t, db, `INSERT INTO cost_category_rule_conditions (
		id,
		rule_id,
		condition_order,
		dimension,
		values_json
	) VALUES (?, ?, ?, ?, ?)`, "ccrc_tag_missing_key", "ccr_storefront", 2, "tag", `["storefront"]`)

	assertExecFails(t, db, `INSERT INTO cost_category_rule_conditions (
		id,
		rule_id,
		condition_order,
		dimension,
		values_json
	) VALUES (?, ?, ?, ?, ?)`, "ccrc_empty_values", "ccr_storefront", 3, "service", `[]`)
}

func costCategoryConditionsByDimension(conditions []CostCategoryRuleCondition) map[string]CostCategoryRuleCondition {
	byDimension := make(map[string]CostCategoryRuleCondition, len(conditions))
	for _, condition := range conditions {
		byDimension[condition.Dimension] = condition
	}
	return byDimension
}
