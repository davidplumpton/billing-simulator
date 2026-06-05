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

func TestCostCategoryRepositoryPreviewsAssignmentsAndRuleOrderEffects(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db := openTestWorkspace(t)
	usageRepo := NewResourceUsageRepository(db)
	for _, request := range []ResourceCreateRequest{
		{
			ID:           "resource-cost-category-web",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonEC2,
			ResourceType: "ec2_instance",
			ResourceName: "Preview web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app": "storefront",
			},
		},
		{
			ID:           "resource-cost-category-bucket",
			AccountID:    "222233334444",
			RegionCode:   "us-east-1",
			ServiceCode:  serviceAmazonS3,
			ResourceType: "s3_bucket",
			ResourceName: "Preview bucket",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
		},
	} {
		if _, err := usageRepo.CreateResource(ctx, request); err != nil {
			t.Fatalf("CreateResource(%s) error = %v", request.ID, err)
		}
	}
	for _, request := range []UsageEventCreateRequest{
		{
			ID:                  "usage-cost-category-web",
			ResourceID:          "resource-cost-category-web",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
		{
			ID:                  "usage-cost-category-bucket",
			ResourceID:          "resource-cost-category-bucket",
			UsageType:           "requests:put-1k",
			Operation:           "PutObject",
			UsageStartTime:      "2026-02-02T00:00:00Z",
			UsageEndTime:        "2026-02-03T00:00:00Z",
			UsageQuantityMicros: 1_500_000_000,
			UsageUnit:           "Request",
		},
	} {
		if _, err := usageRepo.RecordUsageEvent(ctx, request); err != nil {
			t.Fatalf("RecordUsageEvent(%s) error = %v", request.ID, err)
		}
	}
	if _, err := NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}
	if _, err := NewBillLineItemRepository(db).GenerateBillLineItems(ctx, BillLineItemGenerationRequest{}); err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}

	repo := NewCostCategoryRepository(db)
	environment, err := repo.CreateCategory(ctx, CostCategoryCreateRequest{
		Name:         "Environment",
		DefaultValue: "Unknown",
	})
	if err != nil {
		t.Fatalf("CreateCategory(Environment) error = %v", err)
	}
	if _, err := repo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: environment.ID,
		RuleOrder:      1,
		Value:          "Production",
		Conditions: []CostCategoryRuleCondition{
			{Dimension: CostCategoryRuleMatchService, Values: []string{serviceAmazonEC2}},
		},
	}); err != nil {
		t.Fatalf("CreateRule(Environment Production) error = %v", err)
	}
	product, err := repo.CreateCategory(ctx, CostCategoryCreateRequest{
		Name:         "Product",
		DefaultValue: "Unmapped",
	})
	if err != nil {
		t.Fatalf("CreateCategory(Product) error = %v", err)
	}
	if _, err := repo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: product.ID,
		RuleOrder:      10,
		Value:          "Storefront",
		Conditions: []CostCategoryRuleCondition{
			{Dimension: CostCategoryRuleMatchTag, TagKey: "app", Values: []string{"storefront"}},
			{Dimension: CostCategoryRuleMatchCostCategory, CostCategoryID: environment.ID, Values: []string{"Production"}},
		},
	}); err != nil {
		t.Fatalf("CreateRule(Product Storefront) error = %v", err)
	}
	if _, err := repo.CreateRule(ctx, CostCategoryRuleCreateRequest{
		CostCategoryID: product.ID,
		RuleOrder:      20,
		Value:          "Compute",
		Conditions: []CostCategoryRuleCondition{
			{Dimension: CostCategoryRuleMatchService, Values: []string{serviceAmazonEC2}},
		},
	}); err != nil {
		t.Fatalf("CreateRule(Product Compute) error = %v", err)
	}

	preview, err := repo.PreviewCategory(ctx, CostCategoryPreviewRequest{
		CostCategoryID:     product.ID,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("PreviewCategory(Product) error = %v", err)
	}
	if preview.TotalLineItemCount != 2 || preview.MatchedLineItemCount != 1 || preview.UnmatchedLineItemCount != 1 {
		t.Fatalf("preview line item counts = total %d matched %d unmatched %d, want 2/1/1", preview.TotalLineItemCount, preview.MatchedLineItemCount, preview.UnmatchedLineItemCount)
	}
	if preview.TotalCostMicros != 90_700 || preview.MatchedCostMicros != 83_200 || preview.UnmatchedCostMicros != 7_500 {
		t.Fatalf("preview costs = total %d matched %d unmatched %d, want 90700/83200/7500", preview.TotalCostMicros, preview.MatchedCostMicros, preview.UnmatchedCostMicros)
	}
	if len(preview.RuleSummaries) != 2 {
		t.Fatalf("preview rule summaries = %d, want 2", len(preview.RuleSummaries))
	}
	if preview.RuleSummaries[0].Value != "Storefront" ||
		preview.RuleSummaries[0].MatchedLineItemCount != 1 ||
		preview.RuleSummaries[0].MatchedCostMicros != 83_200 ||
		!strings.Contains(strings.Join(preview.RuleSummaries[0].ConditionDescriptions, " | "), "cost category Environment is Production") {
		t.Fatalf("Storefront rule summary = %+v, want first-match spend and referenced category description", preview.RuleSummaries[0])
	}
	if preview.RuleSummaries[1].Value != "Compute" ||
		preview.RuleSummaries[1].MatchedLineItemCount != 0 ||
		preview.RuleSummaries[1].ShadowedLineItemCount != 1 ||
		preview.RuleSummaries[1].ShadowedCostMicros != 83_200 {
		t.Fatalf("Compute rule summary = %+v, want shadowed EC2 spend", preview.RuleSummaries[1])
	}

	itemsByResource := map[string]CostCategoryPreviewLineItem{}
	for _, item := range preview.LineItems {
		itemsByResource[item.ResourceID] = item
	}
	web := itemsByResource["resource-cost-category-web"]
	if web.BeforeValue != "Unmapped" || web.PreviewValue != "Storefront" || web.MatchedRuleOrder != 10 || web.TagSnapshot["app"] != "storefront" {
		t.Fatalf("web preview row = %+v, want Unmapped -> Storefront by rule 10", web)
	}
	if len(web.ShadowedRules) != 1 || web.ShadowedRules[0].Value != "Compute" || web.ShadowedRules[0].RuleOrder != 20 {
		t.Fatalf("web shadowed rules = %+v, want Compute rule 20", web.ShadowedRules)
	}
	bucket := itemsByResource["resource-cost-category-bucket"]
	if bucket.BeforeValue != "Unmapped" || bucket.PreviewValue != "Unmapped" || bucket.MatchedRuleID != "" {
		t.Fatalf("bucket preview row = %+v, want unmatched default assignment", bucket)
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
