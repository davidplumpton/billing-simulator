package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func TestCostCategoriesUIRequiresWorkspace(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/cost-categories")
	if err != nil {
		t.Fatalf("GET /cost-categories without workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-categories without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Cost Categories - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/cost-categories">Cost Categories</a>`,
		"Workspace Required",
		`href="/workspaces"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-categories without workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/cost-categories/categories/create", url.Values{"name": {"Product"}})
	if err != nil {
		t.Fatalf("POST /cost-categories/categories/create without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /cost-categories/categories/create without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before creating cost categories.") {
		t.Fatalf("POST /cost-categories/categories/create without workspace missing workspace message: %s", body)
	}

	resp, err = client.PostForm(server.URL+"/cost-categories/splits/create", url.Values{"category_id": {"cc-product"}})
	if err != nil {
		t.Fatalf("POST /cost-categories/splits/create without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /cost-categories/splits/create without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before creating split-charge rules.") {
		t.Fatalf("POST /cost-categories/splits/create without workspace missing workspace message: %s", body)
	}
}

func TestCostCategoryPreviewWorkflow(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := persistence.OpenWorkspace(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	seedCostCategoryPreviewSpend(t, ctx, db)

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/cost-categories")
	if err != nil {
		t.Fatalf("GET /cost-categories error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-categories status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Cost Category Preview",
		"New Category",
		"Categories",
		"No cost categories",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-categories body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/cost-categories/categories/create", url.Values{
		"name":          {"Environment"},
		"default_value": {"Unknown"},
		"description":   {"Deployment lifecycle"},
	})
	if err != nil {
		t.Fatalf("POST create Environment category error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create Environment category final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	environmentID := readCostCategoryID(t, db, "Environment")

	resp, err = client.PostForm(server.URL+"/cost-categories/rules/create", url.Values{
		"category_id": {environmentID},
		"rule_order":  {"1"},
		"value":       {"Production"},
		"dimension":   {persistence.CostCategoryRuleMatchService},
		"operator":    {persistence.CostCategoryRuleOperatorIn},
		"values":      {"AmazonEC2"},
	})
	if err != nil {
		t.Fatalf("POST create Environment rule error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create Environment rule final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}

	resp, err = client.PostForm(server.URL+"/cost-categories/categories/create", url.Values{
		"name":          {"Product"},
		"default_value": {"Unmapped"},
		"description":   {"Showback product"},
	})
	if err != nil {
		t.Fatalf("POST create Product category error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create Product category final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	productID := readCostCategoryID(t, db, "Product")

	for _, form := range []url.Values{
		{
			"category_id":            {productID},
			"rule_order":             {"10"},
			"value":                  {"Storefront"},
			"dimension":              {persistence.CostCategoryRuleMatchCostCategory},
			"operator":               {persistence.CostCategoryRuleOperatorIn},
			"referenced_category_id": {environmentID},
			"values":                 {"Production"},
		},
		{
			"category_id": {productID},
			"rule_order":  {"20"},
			"value":       {"Compute"},
			"dimension":   {persistence.CostCategoryRuleMatchService},
			"operator":    {persistence.CostCategoryRuleOperatorIn},
			"values":      {"AmazonEC2"},
		},
	} {
		resp, err = client.PostForm(server.URL+"/cost-categories/rules/create", form)
		if err != nil {
			t.Fatalf("POST create Product rule error = %v", err)
		}
		body = readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST create Product rule final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
		}
	}

	resp, err = client.Get(server.URL + "/cost-categories?category_id=" + url.QueryEscape(productID))
	if err != nil {
		t.Fatalf("GET /cost-categories Product preview error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-categories Product preview status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Rule Order Effects",
		"Line Item Preview",
		"Split Charge Rules",
		"Allocation Comparison",
		"Storefront",
		"Compute",
		"cost category Environment is Production",
		"$0.0832",
		"$0.0075",
		"Unmapped",
		"No rule",
		"resource-cost-category-web",
		"app=storefront",
		`<a class="active" aria-current="page" href="/cost-categories">Cost Categories</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-categories Product preview missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/cost-categories/splits/create", url.Values{
		"category_id":   {productID},
		"source_value":  {"Unmapped"},
		"method":        {persistence.CostCategorySplitMethodEven},
		"target_values": {"Storefront\nCompute"},
		"description":   {"Default storage shared across products"},
	})
	if err != nil {
		t.Fatalf("POST create Product split rule error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create Product split rule final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Created split rule for Unmapped",
		"Default storage shared across products",
		"Even",
		"Storefront, Compute",
		"$0.08695",
		"$0.00375",
		"-$0.0075",
		"1 split allocation",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST create Product split rule body missing %q: %s", want, body)
		}
	}
}

// TestCostCategoryRulesFeatureWorksInFreshWorkspace keeps bd-2rx.2 guarded through the browser-facing category workflow.
func TestCostCategoryRulesFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "cost-category-feature-workspace")
	cfg.StatePath = filepath.Join(root, "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("Start() did not open workspace database")
	}
	client := appTestHTTPClient()

	resp, err := client.Get(server.URL() + "/cost-categories")
	if err != nil {
		t.Fatalf("GET /cost-categories fresh workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-categories fresh workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Cost Categories - AWS Billing Simulator</title>`,
		"Cost Category Preview",
		"No cost categories",
		"Line Items",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-categories fresh workspace body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL()+"/resources/create", url.Values{
		"account_id":     {"111122223333"},
		"region_code":    {"us-east-1"},
		"service_preset": {"ec2_t3_medium"},
		"size":           {"t3.medium"},
		"resource_name":  {"Feature category web"},
		"status":         {"active"},
		"started_at":     {"2026-02-01T00:00"},
		"tags":           {"app=storefront\nenv=prod"},
	})
	if err != nil {
		t.Fatalf("POST /resources/create error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/create final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Feature category web") || !strings.Contains(body, "env=prod") {
		t.Fatalf("resource create response missing category feature resource/tag: %s", body)
	}

	resourceID := readOnlyResourceIDByName(t, db, "Feature category web")
	resp, err = client.PostForm(server.URL()+"/resources/generate", url.Values{
		"resource_id":           {resourceID},
		"generation_pattern":    {"daily_instance_hours"},
		"generation_start_date": {"2026-02-01"},
		"generation_days":       {"1"},
	})
	if err != nil {
		t.Fatalf("POST /resources/generate error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/generate final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Generated 1 usage events") ||
		!strings.Contains(body, "instance-hours:t3.medium") ||
		!strings.Contains(body, "env=prod") {
		t.Fatalf("generator response missing category feature usage/tag snapshot: %s", body)
	}

	body = postClockAdvance(t, &client, server.URL(), "1", string(persistence.SimulatorClockAdvanceDays))
	if !strings.Contains(body, "Advanced clock to 2026-02-02T00:00:00Z") ||
		!strings.Contains(body, "daily metering created 1 metering records and 2 bill line items") ||
		!strings.Contains(body, "AWSSupport") {
		t.Fatalf("clock advance response missing priced category workflow data: %s", body)
	}

	var usageLineItemID, supportLineItemID string
	if err := db.QueryRowContext(ctx, `
		SELECT id
		FROM bill_line_items
		WHERE resource_id = ? AND service_code = 'AmazonEC2'
	`, resourceID).Scan(&usageLineItemID); err != nil {
		t.Fatalf("read EC2 bill line item: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT id
		FROM bill_line_items
		WHERE service_code = 'AWSSupport'
	`).Scan(&supportLineItemID); err != nil {
		t.Fatalf("read Support bill line item: %v", err)
	}

	resp, err = client.PostForm(server.URL()+"/cost-categories/categories/create", url.Values{
		"name":          {"Environment"},
		"default_value": {"Unknown"},
		"description":   {"Deployment lifecycle"},
	})
	if err != nil {
		t.Fatalf("POST create Environment category error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create Environment category final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	environmentID := readCostCategoryID(t, db, "Environment")

	resp, err = client.PostForm(server.URL()+"/cost-categories/rules/create", url.Values{
		"category_id": {environmentID},
		"rule_order":  {"10"},
		"value":       {"Production"},
		"dimension":   {persistence.CostCategoryRuleMatchTag},
		"operator":    {persistence.CostCategoryRuleOperatorIn},
		"values":      {"prod"},
		"tag_key":     {"env"},
		"description": {"Production resources carry env=prod"},
	})
	if err != nil {
		t.Fatalf("POST create Environment tag rule error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create Environment tag rule final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Created rule 10 for Environment",
		"tag env is prod",
		"Production",
		resourceID,
		"env=prod",
		"Unknown",
		"AWS Support",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST create Environment tag rule body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL()+"/cost-categories/categories/create", url.Values{
		"name":          {"Product"},
		"default_value": {"Unmapped"},
		"description":   {"Business product showback"},
	})
	if err != nil {
		t.Fatalf("POST create Product category error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create Product category final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	productID := readCostCategoryID(t, db, "Product")

	resp, err = client.PostForm(server.URL()+"/cost-categories/rules/create", url.Values{
		"category_id":            {productID},
		"rule_order":             {"10"},
		"value":                  {"Storefront"},
		"dimension":              {persistence.CostCategoryRuleMatchCostCategory},
		"operator":               {persistence.CostCategoryRuleOperatorIn},
		"referenced_category_id": {environmentID},
		"values":                 {"Production"},
		"description":            {"Storefront product uses Production environment costs"},
	})
	if err != nil {
		t.Fatalf("POST create Product referenced-category rule error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create Product referenced-category rule final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Created rule 10 for Product",
		"Rule Order Effects",
		"Line Item Preview",
		"Storefront",
		"Unmapped",
		"cost category Environment is Production",
		resourceID,
		"app=storefront",
		"env=prod",
		"AWS Support",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST create Product referenced-category rule body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL()+"/cost-categories/splits/create", url.Values{
		"category_id":   {productID},
		"source_value":  {"Unmapped"},
		"method":        {persistence.CostCategorySplitMethodEven},
		"target_values": {"Storefront\nPlatform"},
		"description":   {"Share support across product values"},
	})
	if err != nil {
		t.Fatalf("POST create Product split rule error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create Product split rule final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Created split rule for Unmapped",
		"Split Charge Rules",
		"Allocation Comparison",
		"Storefront, Platform",
		"$0.50",
		"-$1.00",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST create Product split rule body missing %q: %s", want, body)
		}
	}

	repo := persistence.NewCostCategoryRepository(db)
	assignments, err := repo.ListLineItemAssignments(ctx, persistence.CostCategoryAssignmentListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		CostCategoryID:     productID,
		Limit:              10,
	})
	if err != nil {
		t.Fatalf("ListLineItemAssignments(Product open period) error = %v", err)
	}
	if len(assignments) != 2 {
		t.Fatalf("Product assignments before close = %+v, want usage and Support rows", assignments)
	}
	usageAssignment := requireCostCategoryAssignmentByLineItem(t, assignments, usageLineItemID)
	if usageAssignment.AssignedValue != "Storefront" ||
		usageAssignment.AssignmentSource != "rule" ||
		usageAssignment.MatchedRuleValue != "Storefront" ||
		usageAssignment.LineItemStatus != "estimated" {
		t.Fatalf("usage assignment before close = %+v, want estimated Storefront rule snapshot", usageAssignment)
	}
	supportAssignment := requireCostCategoryAssignmentByLineItem(t, assignments, supportLineItemID)
	if supportAssignment.AssignedValue != "Unmapped" ||
		supportAssignment.AssignmentSource != "default" ||
		supportAssignment.LineItemStatus != "estimated" {
		t.Fatalf("Support assignment before close = %+v, want estimated default snapshot", supportAssignment)
	}

	body = postClockAdvance(t, &client, server.URL(), "1", string(persistence.SimulatorClockAdvanceBillingPeriods))
	if !strings.Contains(body, "Advanced clock to 2026-03-01T00:00:00Z") ||
		!strings.Contains(body, "2026-03-01 to 2026-04-01 (31 days)") {
		t.Fatalf("billing-period advance response missing March clock state: %s", body)
	}

	resp, err = client.PostForm(server.URL()+"/resources/month-close", url.Values{
		"payer_account_id": {"999988887777"},
		"invoice_due_days": {"14"},
	})
	if err != nil {
		t.Fatalf("POST /resources/month-close error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/month-close final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Month-end close finalized 2 line items into bill",
		"Issued Bills",
		"SIM-INV-202602-",
		"final",
		"due",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("month-end close response missing %q: %s", want, body)
		}
	}

	assignments, err = repo.ListLineItemAssignments(ctx, persistence.CostCategoryAssignmentListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		CostCategoryID:     productID,
		Limit:              10,
	})
	if err != nil {
		t.Fatalf("ListLineItemAssignments(Product closed period) error = %v", err)
	}
	if got := requireCostCategoryAssignmentByLineItem(t, assignments, usageLineItemID); got.AssignedValue != "Storefront" || got.LineItemStatus != "final" {
		t.Fatalf("closed usage assignment = %+v, want final Storefront", got)
	}
	if got := requireCostCategoryAssignmentByLineItem(t, assignments, supportLineItemID); got.AssignedValue != "Unmapped" || got.LineItemStatus != "final" {
		t.Fatalf("closed Support assignment = %+v, want preserved final Unmapped", got)
	}

	resp, err = client.PostForm(server.URL()+"/cost-categories/rules/create", url.Values{
		"category_id": {productID},
		"rule_order":  {"20"},
		"value":       {"Shared Platform"},
		"dimension":   {persistence.CostCategoryRuleMatchService},
		"operator":    {persistence.CostCategoryRuleOperatorIn},
		"values":      {"AWSSupport"},
		"description": {"Support should not rewrite closed-period assignments"},
	})
	if err != nil {
		t.Fatalf("POST create Product Support rule after close error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create Product Support rule after close final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Created rule 20 for Product") || !strings.Contains(body, "No line items in the current billing period") {
		t.Fatalf("POST create Product Support rule after close missing March preview state: %s", body)
	}

	assignments, err = repo.ListLineItemAssignments(ctx, persistence.CostCategoryAssignmentListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		CostCategoryID:     productID,
		Limit:              10,
	})
	if err != nil {
		t.Fatalf("ListLineItemAssignments(Product after closed rule change) error = %v", err)
	}
	if got := requireCostCategoryAssignmentByLineItem(t, assignments, supportLineItemID); got.AssignedValue != "Unmapped" || got.MatchedRuleID != "" {
		t.Fatalf("closed Support assignment after rule change = %+v, want preserved default", got)
	}
}

// TestSharedCostSplitChargesFeatureWorksInFreshWorkspace keeps bd-2rx.3 guarded through the browser-facing split-charge workflow.
func TestSharedCostSplitChargesFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "split-charge-feature-workspace")
	cfg.StatePath = filepath.Join(root, "state.json")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			t.Errorf("Close() error = %v", err)
		}
		if err := server.Wait(); err != nil {
			t.Errorf("Wait() error = %v", err)
		}
	})

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("Start() did not open workspace database")
	}
	client := appTestHTTPClient()

	createResource := func(name, accountID, product string) string {
		t.Helper()

		resp, err := client.PostForm(server.URL()+"/resources/create", url.Values{
			"account_id":     {accountID},
			"region_code":    {"us-east-1"},
			"service_preset": {"ec2_t3_medium"},
			"size":           {"t3.medium"},
			"resource_name":  {name},
			"status":         {"active"},
			"started_at":     {"2026-02-01T00:00"},
			"tags":           {"product=" + product},
		})
		if err != nil {
			t.Fatalf("POST /resources/create %s error = %v", name, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /resources/create %s final status = %d, want %d; body=%s", name, resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, name) || !strings.Contains(body, "product="+product) {
			t.Fatalf("resource create response for %s missing resource/tag: %s", name, body)
		}
		return readOnlyResourceIDByName(t, db, name)
	}

	generateUsage := func(resourceID, days string) {
		t.Helper()

		resp, err := client.PostForm(server.URL()+"/resources/generate", url.Values{
			"resource_id":           {resourceID},
			"generation_pattern":    {string(persistence.UsageGenerationDailyInstanceHours)},
			"generation_start_date": {"2026-02-01"},
			"generation_days":       {days},
		})
		if err != nil {
			t.Fatalf("POST /resources/generate %s error = %v", resourceID, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /resources/generate %s final status = %d, want %d; body=%s", resourceID, resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, "Generated "+days+" usage events") || !strings.Contains(body, "instance-hours:t3.medium") {
			t.Fatalf("generator response for %s missing usage details: %s", resourceID, body)
		}
	}

	storefrontResourceID := createResource("Feature split storefront web", "111122223333", "storefront")
	paymentsResourceID := createResource("Feature split payments web", "444455556666", "payments")
	generateUsage(storefrontResourceID, "2")
	generateUsage(paymentsResourceID, "1")

	body := postClockAdvance(t, &client, server.URL(), "2", string(persistence.SimulatorClockAdvanceDays))
	if !strings.Contains(body, "Advanced clock to 2026-02-03T00:00:00Z") ||
		!strings.Contains(body, "daily metering created 3 metering records and 4 bill line items") ||
		!strings.Contains(body, "AWSSupport") {
		t.Fatalf("clock advance response missing split-charge source/target line items: %s", body)
	}

	var supportCostMicros int64
	if err := db.QueryRowContext(ctx, `
		SELECT unblended_cost_micros
		FROM bill_line_items
		WHERE service_code = 'AWSSupport'
		  AND billing_period_start = '2026-02-01'
		  AND billing_period_end = '2026-03-01'
	`).Scan(&supportCostMicros); err != nil {
		t.Fatalf("read Support split source cost: %v", err)
	}
	if supportCostMicros <= 0 {
		t.Fatalf("Support split source cost = %d, want positive cost", supportCostMicros)
	}

	createProductCategory := func(name string) string {
		t.Helper()

		resp, err := client.PostForm(server.URL()+"/cost-categories/categories/create", url.Values{
			"name":          {name},
			"default_value": {"Unmapped"},
			"description":   {"Shared-cost split feature " + name},
		})
		if err != nil {
			t.Fatalf("POST create %s category error = %v", name, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST create %s category final status = %d, want %d; body=%s", name, resp.StatusCode, http.StatusOK, body)
		}
		categoryID := readCostCategoryID(t, db, name)

		for _, form := range []url.Values{
			{
				"category_id": {categoryID},
				"rule_order":  {"10"},
				"value":       {"Storefront"},
				"dimension":   {persistence.CostCategoryRuleMatchTag},
				"operator":    {persistence.CostCategoryRuleOperatorIn},
				"values":      {"storefront"},
				"tag_key":     {"product"},
				"description": {"Storefront product tag"},
			},
			{
				"category_id": {categoryID},
				"rule_order":  {"20"},
				"value":       {"Payments"},
				"dimension":   {persistence.CostCategoryRuleMatchTag},
				"operator":    {persistence.CostCategoryRuleOperatorIn},
				"values":      {"payments"},
				"tag_key":     {"product"},
				"description": {"Payments product tag"},
			},
			{
				"category_id": {categoryID},
				"rule_order":  {"30"},
				"value":       {"Shared Platform"},
				"dimension":   {persistence.CostCategoryRuleMatchService},
				"operator":    {persistence.CostCategoryRuleOperatorIn},
				"values":      {"AWSSupport"},
				"description": {"Support is allocated as shared platform cost"},
			},
		} {
			resp, err = client.PostForm(server.URL()+"/cost-categories/rules/create", form)
			if err != nil {
				t.Fatalf("POST create %s rule %s error = %v", name, form.Get("value"), err)
			}
			body = readResponseBody(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("POST create %s rule %s final status = %d, want %d; body=%s", name, form.Get("value"), resp.StatusCode, http.StatusOK, body)
			}
			if !strings.Contains(body, "Created rule") || !strings.Contains(body, form.Get("value")) {
				t.Fatalf("POST create %s rule %s body missing confirmation: %s", name, form.Get("value"), body)
			}
		}
		return categoryID
	}

	createSplitRule := func(categoryID, sourceValue, method, fixedShares string) string {
		t.Helper()

		resp, err := client.PostForm(server.URL()+"/cost-categories/splits/create", url.Values{
			"category_id":        {categoryID},
			"source_value":       {sourceValue},
			"method":             {method},
			"target_values":      {"Storefront\nPayments"},
			"fixed_share_micros": {fixedShares},
			"description":        {"Allocate " + sourceValue + " by " + method},
		})
		if err != nil {
			t.Fatalf("POST create %s split rule error = %v", method, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST create %s split rule final status = %d, want %d; body=%s", method, resp.StatusCode, http.StatusOK, body)
		}
		for _, want := range []string{
			"Created split rule for " + sourceValue,
			"Split Charge Rules",
			"Allocation Comparison",
			"Storefront",
			"Payments",
			"Shared Platform",
			"1 split allocation",
		} {
			if !strings.Contains(body, want) {
				t.Fatalf("POST create %s split rule body missing %q: %s", method, want, body)
			}
		}
		return body
	}

	splitRepo := persistence.NewCostCategorySplitChargeRepository(db)
	compare := func(categoryID string) persistence.CostCategorySplitChargeComparison {
		t.Helper()

		comparison, err := splitRepo.CompareAllocations(ctx, persistence.CostCategorySplitChargeComparisonRequest{
			CostCategoryID:     categoryID,
			BillingPeriodStart: "2026-02-01",
			BillingPeriodEnd:   "2026-03-01",
		})
		if err != nil {
			t.Fatalf("CompareAllocations(%s) error = %v", categoryID, err)
		}
		if comparison.SplitInCostMicros != supportCostMicros ||
			comparison.SplitOutCostMicros != supportCostMicros ||
			comparison.UnallocatedResidualCostMicros != 0 {
			t.Fatalf("comparison %s totals = %+v, want support fully reallocated", categoryID, comparison)
		}
		return comparison
	}
	requireRow := func(rows []persistence.CostCategorySplitChargeComparisonRow, value string) persistence.CostCategorySplitChargeComparisonRow {
		t.Helper()

		for _, row := range rows {
			if row.Value == value {
				return row
			}
		}
		t.Fatalf("comparison rows = %+v, want value %q", rows, value)
		return persistence.CostCategorySplitChargeComparisonRow{}
	}

	evenCategoryID := createProductCategory("Product Even")
	evenBody := createSplitRule(evenCategoryID, "Shared Platform", persistence.CostCategorySplitMethodEven, "")
	if !strings.Contains(evenBody, "Even") || !strings.Contains(evenBody, formatUSDMicros(supportCostMicros/2)) {
		t.Fatalf("even split UI did not show even method and half support allocation: %s", evenBody)
	}
	evenComparison := compare(evenCategoryID)
	evenStorefront := requireRow(evenComparison.Rows, "Storefront")
	evenPayments := requireRow(evenComparison.Rows, "Payments")
	if evenStorefront.SplitInCostMicros != supportCostMicros/2 || evenPayments.SplitInCostMicros != supportCostMicros/2 {
		t.Fatalf("even split rows = storefront %+v payments %+v, want half of %d each", evenStorefront, evenPayments, supportCostMicros)
	}

	fixedCategoryID := createProductCategory("Product Fixed")
	fixedBody := createSplitRule(fixedCategoryID, "Shared Platform", persistence.CostCategorySplitMethodFixed, "Storefront=600000\nPayments=400000")
	if !strings.Contains(fixedBody, "Fixed") || !strings.Contains(fixedBody, "Storefront 60%, Payments 40%") {
		t.Fatalf("fixed split UI did not show fixed method and target shares: %s", fixedBody)
	}
	fixedComparison := compare(fixedCategoryID)
	fixedStorefront := requireRow(fixedComparison.Rows, "Storefront")
	fixedPayments := requireRow(fixedComparison.Rows, "Payments")
	if fixedStorefront.SplitInCostMicros != supportCostMicros*6/10 || fixedPayments.SplitInCostMicros != supportCostMicros*4/10 {
		t.Fatalf("fixed split rows = storefront %+v payments %+v, want 60/40 of %d", fixedStorefront, fixedPayments, supportCostMicros)
	}

	proportionalCategoryID := createProductCategory("Product Proportional")
	proportionalBody := createSplitRule(proportionalCategoryID, "Shared Platform", persistence.CostCategorySplitMethodProportional, "")
	if !strings.Contains(proportionalBody, "Proportional") {
		t.Fatalf("proportional split UI did not show proportional method: %s", proportionalBody)
	}
	proportionalBeforeClose := compare(proportionalCategoryID)
	proportionalStorefront := requireRow(proportionalBeforeClose.Rows, "Storefront")
	proportionalPayments := requireRow(proportionalBeforeClose.Rows, "Payments")
	if proportionalStorefront.RawCostMicros <= proportionalPayments.RawCostMicros ||
		proportionalStorefront.SplitInCostMicros <= proportionalPayments.SplitInCostMicros {
		t.Fatalf("proportional rows = storefront %+v payments %+v, want larger raw target to receive larger split", proportionalStorefront, proportionalPayments)
	}
	if shared := requireRow(proportionalBeforeClose.Rows, "Shared Platform"); shared.TotalAllocatedCostMicros != 0 || shared.SourceLineItemCount != 1 {
		t.Fatalf("proportional source row = %+v, want source split out of category cost", shared)
	}

	body = postClockAdvance(t, &client, server.URL(), "1", string(persistence.SimulatorClockAdvanceBillingPeriods))
	if !strings.Contains(body, "Advanced clock to 2026-03-01T00:00:00Z") {
		t.Fatalf("billing-period advance response missing March state: %s", body)
	}
	resp, err := client.PostForm(server.URL()+"/resources/month-close", url.Values{
		"payer_account_id": {"999988887777"},
		"invoice_due_days": {"14"},
	})
	if err != nil {
		t.Fatalf("POST /resources/month-close error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/month-close final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Month-end close finalized 4 line items into bill") || !strings.Contains(body, "SIM-INV-202602-") {
		t.Fatalf("month-end close response missing split-charge finalized bill details: %s", body)
	}

	proportionalAfterClose := compare(proportionalCategoryID)
	if proportionalAfterClose.TotalAllocatedCostMicros != proportionalBeforeClose.TotalAllocatedCostMicros ||
		proportionalAfterClose.SplitInCostMicros != proportionalBeforeClose.SplitInCostMicros ||
		proportionalAfterClose.SplitOutCostMicros != proportionalBeforeClose.SplitOutCostMicros {
		t.Fatalf("closed proportional comparison = %+v, want preserved totals from %+v", proportionalAfterClose, proportionalBeforeClose)
	}
}
