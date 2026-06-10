package app

import (
	"context"
	"database/sql"
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

func TestCostAllocationTagsUIRequiresWorkspace(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/tags")
	if err != nil {
		t.Fatalf("GET /tags without workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tags without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Tags - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/tags">Tags</a>`,
		"Workspace Required",
		`href="/workspaces"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /tags without workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/tags/activate", url.Values{"tag_key": {"app"}})
	if err != nil {
		t.Fatalf("POST /tags/activate without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /tags/activate without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before activating cost allocation tags.") {
		t.Fatalf("POST /tags/activate without workspace missing workspace message: %s", body)
	}

	resp, err = client.PostForm(server.URL+"/tags/refresh", url.Values{})
	if err != nil {
		t.Fatalf("POST /tags/refresh without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /tags/refresh without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before refreshing cost allocation tag discovery.") {
		t.Fatalf("POST /tags/refresh without workspace missing workspace message: %s", body)
	}
}

func TestCostAllocationTagsGetDoesNotRefreshDiscovery(t *testing.T) {
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

	usageRepo := persistence.NewResourceUsageRepository(db)
	if _, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-tags-persisted-app",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Persisted app tag",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
		Tags: map[string]string{
			"app": "storefront",
		},
	}); err != nil {
		t.Fatalf("CreateResource(persisted app tag) error = %v", err)
	}
	if _, err := persistence.NewCostAllocationTagRepository(db).RefreshDiscoveredTags(ctx, "2026-02-01T00:00:00Z"); err != nil {
		t.Fatalf("RefreshDiscoveredTags(initial) error = %v", err)
	}
	if _, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-tags-undiscovered-review",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Undiscovered review tag",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
		Tags: map[string]string{
			"billingreview": "new-value",
		},
	}); err != nil {
		t.Fatalf("CreateResource(undiscovered review tag) error = %v", err)
	}

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	for i := 0; i < 2; i++ {
		resp, err := client.Get(server.URL + "/tags")
		if err != nil {
			t.Fatalf("GET /tags repeat %d error = %v", i+1, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /tags repeat %d status = %d, want %d; body=%s", i+1, resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, "app") || !strings.Contains(body, "storefront") {
			t.Fatalf("GET /tags repeat %d missing persisted discovery: %s", i+1, body)
		}
		if strings.Contains(body, "billingreview") || strings.Contains(body, "new-value") {
			t.Fatalf("GET /tags repeat %d discovered new resource tag without explicit refresh: %s", i+1, body)
		}
	}

	keyCount, valueCount := readCostAllocationTagDiscoveryCounts(t, ctx, db)
	if keyCount != 1 || valueCount != 1 {
		t.Fatalf("cost allocation tag discovery counts = %d/%d, want persisted 1 key and 1 value after repeated GET", keyCount, valueCount)
	}
}

func TestCostAllocationTagsRefreshActionDiscoversResourceTags(t *testing.T) {
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

	if _, err := persistence.NewResourceUsageRepository(db).CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-tags-explicit-refresh",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Explicit refresh tag",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
		Tags: map[string]string{
			"refreshable": "explicit-action",
		},
	}); err != nil {
		t.Fatalf("CreateResource(explicit refresh tag) error = %v", err)
	}

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/tags")
	if err != nil {
		t.Fatalf("GET /tags before explicit refresh error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tags before explicit refresh status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if strings.Contains(body, "refreshable") || strings.Contains(body, "explicit-action") {
		t.Fatalf("GET /tags before explicit refresh discovered resource tag: %s", body)
	}

	body = postTagDiscoveryRefresh(t, client, server.URL)
	for _, want := range []string{
		"Refreshed tag discovery: 1 keys and 1 values discovered",
		"refreshable",
		"explicit-action",
		`action="/tags/refresh"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /tags/refresh body missing %q: %s", want, body)
		}
	}

	keyCount, valueCount := readCostAllocationTagDiscoveryCounts(t, ctx, db)
	if keyCount != 1 || valueCount != 1 {
		t.Fatalf("cost allocation tag discovery counts = %d/%d, want 1 key and 1 value after explicit refresh", keyCount, valueCount)
	}
}

// TestTagsCostCategoriesAndAllocationEpicWorksInFreshWorkspace keeps bd-2rx guarded across the combined attribution workflow.
func TestTagsCostCategoriesAndAllocationEpicWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "tags-allocation-epic-workspace")
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
			"tags":           {"product=" + product + "\nowner=retail-finops"},
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
		if !strings.Contains(body, "Generated "+days+" usage events") || !strings.Contains(body, "product=") {
			t.Fatalf("generator response for %s missing usage/tag snapshot: %s", resourceID, body)
		}
	}

	storefrontResourceID := createResource("Epic allocation storefront web", "111122223333", "storefront")
	paymentsResourceID := createResource("Epic allocation payments web", "444455556666", "payments")
	generateUsage(storefrontResourceID, "2")
	generateUsage(paymentsResourceID, "1")

	body := postClockAdvance(t, &client, server.URL(), "2", string(persistence.SimulatorClockAdvanceDays))
	if !strings.Contains(body, "Advanced clock to 2026-02-03T00:00:00Z") ||
		!strings.Contains(body, "daily metering created 3 metering records and 4 bill line items") ||
		!strings.Contains(body, "AWSSupport") {
		t.Fatalf("clock advance response missing epic line items: %s", body)
	}

	body = postTagDiscoveryRefresh(t, &client, server.URL())
	for _, want := range []string{
		"Refreshed tag discovery: 2 keys and 3 values discovered",
		"Cost Allocation Tag Manager",
		"Spend Coverage",
		"Account Coverage",
		"Service Coverage",
		"product",
		"storefront",
		"payments",
		"Not activated",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /tags/refresh with epic spend missing %q: %s", want, body)
		}
	}

	resp, err := client.PostForm(server.URL()+"/tags/activate", url.Values{"tag_key": {"product"}})
	if err != nil {
		t.Fatalf("POST /tags/activate product error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /tags/activate product final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Activated product for cost allocation") ||
		!strings.Contains(body, "Pending until 2026-02-04T00:00:00Z") {
		t.Fatalf("POST /tags/activate product missing pending visibility: %s", body)
	}

	body = postClockAdvance(t, &client, server.URL(), "1", string(persistence.SimulatorClockAdvanceDays))
	if !strings.Contains(body, "Advanced clock to 2026-02-04T00:00:00Z") {
		t.Fatalf("tag visibility clock advance response missing Feb 4 state: %s", body)
	}
	resp, err = client.Get(server.URL() + "/tags")
	if err != nil {
		t.Fatalf("GET /tags visible product error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tags visible product status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Visible since 2026-02-04T00:00:00Z") ||
		strings.Contains(body, "Pending until 2026-02-04T00:00:00Z") {
		t.Fatalf("GET /tags visible product did not show billing-visible state: %s", body)
	}

	resp, err = client.PostForm(server.URL()+"/cost-categories/categories/create", url.Values{
		"name":          {"Product"},
		"default_value": {"Unmapped"},
		"description":   {"Epic product showback"},
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
			"category_id": {productID},
			"rule_order":  {"10"},
			"value":       {"Storefront"},
			"dimension":   {persistence.CostCategoryRuleMatchTag},
			"operator":    {persistence.CostCategoryRuleOperatorIn},
			"values":      {"storefront"},
			"tag_key":     {"product"},
			"description": {"Storefront product tag"},
		},
		{
			"category_id": {productID},
			"rule_order":  {"20"},
			"value":       {"Payments"},
			"dimension":   {persistence.CostCategoryRuleMatchTag},
			"operator":    {persistence.CostCategoryRuleOperatorIn},
			"values":      {"payments"},
			"tag_key":     {"product"},
			"description": {"Payments product tag"},
		},
		{
			"category_id": {productID},
			"rule_order":  {"30"},
			"value":       {"Shared Platform"},
			"dimension":   {persistence.CostCategoryRuleMatchService},
			"operator":    {persistence.CostCategoryRuleOperatorIn},
			"values":      {"AWSSupport"},
			"description": {"Support is allocated to tagged products"},
		},
	} {
		resp, err = client.PostForm(server.URL()+"/cost-categories/rules/create", form)
		if err != nil {
			t.Fatalf("POST create Product rule %s error = %v", form.Get("value"), err)
		}
		body = readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST create Product rule %s final status = %d, want %d; body=%s", form.Get("value"), resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, "Created rule") || !strings.Contains(body, form.Get("value")) {
			t.Fatalf("POST create Product rule %s body missing confirmation: %s", form.Get("value"), body)
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
	valueCounts := map[string]int{}
	for _, assignment := range assignments {
		valueCounts[assignment.AssignedValue]++
		if assignment.LineItemStatus != "estimated" {
			t.Fatalf("open-period assignment = %+v, want estimated line item status", assignment)
		}
	}
	if len(assignments) != 4 ||
		valueCounts["Storefront"] != 2 ||
		valueCounts["Payments"] != 1 ||
		valueCounts["Shared Platform"] != 1 {
		t.Fatalf("Product assignments = %+v, want 2 Storefront, 1 Payments, 1 Shared Platform", assignments)
	}

	var supportCostMicros int64
	if err := db.QueryRowContext(ctx, `
		SELECT unblended_cost_micros
		FROM bill_line_items
		WHERE service_code = 'AWSSupport'
		  AND billing_period_start = '2026-02-01'
		  AND billing_period_end = '2026-03-01'
	`).Scan(&supportCostMicros); err != nil {
		t.Fatalf("read epic Support source cost: %v", err)
	}
	if supportCostMicros <= 0 {
		t.Fatalf("Support source cost = %d, want positive cost", supportCostMicros)
	}

	resp, err = client.PostForm(server.URL()+"/cost-categories/splits/create", url.Values{
		"category_id":   {productID},
		"source_value":  {"Shared Platform"},
		"method":        {persistence.CostCategorySplitMethodEven},
		"target_values": {"Storefront\nPayments"},
		"description":   {"Allocate shared Support to product tags"},
	})
	if err != nil {
		t.Fatalf("POST create Product split rule error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create Product split rule final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Created split rule for Shared Platform",
		"Split Charge Rules",
		"Allocation Comparison",
		"Storefront",
		"Payments",
		"Shared Platform",
		"1 split allocation",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST create Product split rule body missing %q: %s", want, body)
		}
	}

	splitRepo := persistence.NewCostCategorySplitChargeRepository(db)
	comparisonBeforeClose, err := splitRepo.CompareAllocations(ctx, persistence.CostCategorySplitChargeComparisonRequest{
		CostCategoryID:     productID,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("CompareAllocations(Product open period) error = %v", err)
	}
	if comparisonBeforeClose.SplitInCostMicros != supportCostMicros ||
		comparisonBeforeClose.SplitOutCostMicros != supportCostMicros ||
		comparisonBeforeClose.UnallocatedResidualCostMicros != 0 {
		t.Fatalf("open Product comparison = %+v, want Support fully reallocated", comparisonBeforeClose)
	}

	body = postClockAdvance(t, &client, server.URL(), "1", string(persistence.SimulatorClockAdvanceBillingPeriods))
	if !strings.Contains(body, "Advanced clock to 2026-03-01T00:00:00Z") {
		t.Fatalf("billing-period advance response missing March state: %s", body)
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
	if !strings.Contains(body, "Month-end close finalized 4 line items into bill") ||
		!strings.Contains(body, "SIM-INV-202602-") {
		t.Fatalf("month-end close response missing epic finalized bill details: %s", body)
	}

	comparisonAfterClose, err := splitRepo.CompareAllocations(ctx, persistence.CostCategorySplitChargeComparisonRequest{
		CostCategoryID:     productID,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("CompareAllocations(Product closed period) error = %v", err)
	}
	if comparisonAfterClose.TotalAllocatedCostMicros != comparisonBeforeClose.TotalAllocatedCostMicros ||
		comparisonAfterClose.SplitInCostMicros != comparisonBeforeClose.SplitInCostMicros ||
		comparisonAfterClose.SplitOutCostMicros != comparisonBeforeClose.SplitOutCostMicros {
		t.Fatalf("closed Product comparison = %+v, want preserved totals from %+v", comparisonAfterClose, comparisonBeforeClose)
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
	for _, assignment := range assignments {
		if assignment.LineItemStatus != "final" {
			t.Fatalf("closed-period assignment = %+v, want final line item status", assignment)
		}
	}
}

func TestCostAllocationTagManagerWorkflow(t *testing.T) {
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

	usageRepo := persistence.NewResourceUsageRepository(db)
	for _, request := range []persistence.ResourceCreateRequest{
		{
			ID:           "resource-tags-web",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  "AmazonEC2",
			ResourceType: "ec2_instance",
			ResourceName: "Tagged web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app":   "storefront",
				"owner": "web-platform",
			},
		},
		{
			ID:           "resource-tags-worker",
			AccountID:    "444455556666",
			RegionCode:   "us-east-1",
			ServiceCode:  "AmazonS3",
			ResourceType: "s3_bucket",
			ResourceName: "Tagged worker",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app":   "storefront",
				"Owner": "payments-team",
			},
		},
	} {
		if _, err := usageRepo.CreateResource(ctx, request); err != nil {
			t.Fatalf("CreateResource(%s) error = %v", request.ID, err)
		}
	}
	for _, request := range []persistence.UsageEventCreateRequest{
		{
			ID:                  "usage-tags-web",
			ResourceID:          "resource-tags-web",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
		{
			ID:                  "usage-tags-worker",
			ResourceID:          "resource-tags-worker",
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
	if _, err := persistence.NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}
	if _, err := persistence.NewBillLineItemRepository(db).GenerateBillLineItems(ctx, persistence.BillLineItemGenerationRequest{}); err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	body := postTagDiscoveryRefresh(t, client, server.URL)
	for _, want := range []string{
		"Refreshed tag discovery: 3 keys and 3 values discovered",
		"Cost Allocation Tag Manager",
		"Spend Coverage",
		"Account Coverage",
		"Service Coverage",
		"Tag Key Coverage",
		"Discovered Values",
		"app",
		"storefront",
		"2 resources",
		"owner",
		"Owner",
		"Case Mismatch",
		"$0.0907",
		"$0.0075",
		"Not activated",
		`action="/tags/refresh"`,
		`action="/tags/activate"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /tags/refresh body missing %q: %s", want, body)
		}
	}

	resp, err := client.PostForm(server.URL+"/tags/activate", url.Values{"tag_key": {"app"}})
	if err != nil {
		t.Fatalf("POST /tags/activate error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /tags/activate final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Activated app for cost allocation",
		"Pending until 2026-02-02T00:00:00Z",
		"Cost Explorer 2026-02-02T00:00:00Z",
		`action="/tags/deactivate"`,
		"Activation History",
		"activate",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /tags/activate response missing %q: %s", want, body)
		}
	}

	var activationStatus string
	var visibleAt sql.NullString
	if err := db.QueryRowContext(ctx, `SELECT activation_status, cost_explorer_visible_at FROM cost_allocation_tag_keys WHERE tag_key = ?`, "app").Scan(&activationStatus, &visibleAt); err != nil {
		t.Fatalf("read activated app tag key: %v", err)
	}
	if activationStatus != "active" || !visibleAt.Valid || visibleAt.String != "2026-02-02T00:00:00Z" {
		t.Fatalf("activated app state = %q/%v, want active visible on 2026-02-02", activationStatus, visibleAt)
	}

	if _, err := persistence.NewSimulatorClockRepository(db).Advance(ctx, persistence.SimulatorClockAdvanceRequest{
		Amount: 1,
		Unit:   persistence.SimulatorClockAdvanceDays,
	}); err != nil {
		t.Fatalf("Advance(clock) error = %v", err)
	}
	resp, err = client.Get(server.URL + "/tags")
	if err != nil {
		t.Fatalf("GET /tags after clock advance error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tags after clock advance status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Visible since 2026-02-02T00:00:00Z") || strings.Contains(body, "Pending until 2026-02-02T00:00:00Z") {
		t.Fatalf("GET /tags after clock advance did not show billing-visible state: %s", body)
	}

	resp, err = client.PostForm(server.URL+"/tags/deactivate", url.Values{"tag_key": {"app"}})
	if err != nil {
		t.Fatalf("POST /tags/deactivate error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /tags/deactivate final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Deactivated app for cost allocation",
		"deactivated",
		"Not visible after deactivation",
		"deactivate",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /tags/deactivate response missing %q: %s", want, body)
		}
	}

	if err := db.QueryRowContext(ctx, `SELECT activation_status, cost_explorer_visible_at FROM cost_allocation_tag_keys WHERE tag_key = ?`, "app").Scan(&activationStatus, &visibleAt); err != nil {
		t.Fatalf("read deactivated app tag key: %v", err)
	}
	if activationStatus != "deactivated" || visibleAt.Valid {
		t.Fatalf("deactivated app state = %q/%v, want deactivated with cleared visibility", activationStatus, visibleAt)
	}
}

// TestCostAllocationTagLifecycleFeatureWorksInFreshWorkspace keeps bd-2rx.1 guarded through the browser-facing tag workflow.
func TestCostAllocationTagLifecycleFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "tag-lifecycle-feature-workspace")
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

	resp, err := client.Get(server.URL() + "/tags")
	if err != nil {
		t.Fatalf("GET /tags fresh workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tags fresh workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Tags - AWS Billing Simulator</title>`,
		"Cost Allocation Tag Manager",
		"Discovered Keys",
		"No resource tag keys discovered",
		`action="/tags/refresh"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /tags fresh workspace body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL()+"/resources/create", url.Values{
		"account_id":     {"111122223333"},
		"region_code":    {"us-east-1"},
		"service_preset": {"ec2_t3_medium"},
		"size":           {"t3.medium"},
		"resource_name":  {"Feature tagged web"},
		"status":         {"active"},
		"started_at":     {"2026-02-01T00:00"},
		"tags":           {"app=storefront\nowner=web-platform"},
	})
	if err != nil {
		t.Fatalf("POST /resources/create error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/create final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Feature tagged web") || !strings.Contains(body, "app=storefront") {
		t.Fatalf("resource create response missing feature resource/tag: %s", body)
	}

	resourceID := readOnlyResourceIDByName(t, db, "Feature tagged web")
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
		!strings.Contains(body, "owner=web-platform") {
		t.Fatalf("generator response missing feature usage/tag snapshot: %s", body)
	}

	body = postClockAdvance(t, &client, server.URL(), "1", string(persistence.SimulatorClockAdvanceDays))
	if !strings.Contains(body, "Advanced clock to 2026-02-02T00:00:00Z") ||
		!strings.Contains(body, "daily metering created 1 metering records") ||
		!strings.Contains(body, "Bill Line Items") {
		t.Fatalf("clock advance response missing priced tag workflow data: %s", body)
	}

	body = postTagDiscoveryRefresh(t, &client, server.URL())
	for _, want := range []string{
		"Refreshed tag discovery: 2 keys and 2 values discovered",
		"Spend Coverage",
		"Account Coverage",
		"Service Coverage",
		"Tag Key Coverage",
		"app",
		"storefront",
		"owner",
		"web-platform",
		"Not activated",
		"Untagged Spend",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /tags/refresh with billed spend body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL()+"/tags/activate", url.Values{"tag_key": {"app"}})
	if err != nil {
		t.Fatalf("POST /tags/activate error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /tags/activate final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Activated app for cost allocation",
		"Pending until 2026-02-03T00:00:00Z",
		`action="/tags/deactivate"`,
		"Activation History",
		"activate",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /tags/activate response missing %q: %s", want, body)
		}
	}

	var snapshot string
	if err := db.QueryRowContext(ctx, `
		SELECT tag_snapshot_json
		FROM bill_line_items
		WHERE resource_id = ? AND service_code = 'AmazonEC2'
		ORDER BY usage_start_time
		LIMIT 1
	`, resourceID).Scan(&snapshot); err != nil {
		t.Fatalf("read feature line item tag snapshot: %v", err)
	}
	for _, want := range []string{`"app":"storefront"`, `"owner":"web-platform"`} {
		if !strings.Contains(snapshot, want) {
			t.Fatalf("line item tag snapshot = %s, want %s", snapshot, want)
		}
	}

	body = postClockAdvance(t, &client, server.URL(), "1", string(persistence.SimulatorClockAdvanceDays))
	if !strings.Contains(body, "Advanced clock to 2026-02-03T00:00:00Z") {
		t.Fatalf("second clock advance response missing visible timestamp: %s", body)
	}
	resp, err = client.Get(server.URL() + "/tags")
	if err != nil {
		t.Fatalf("GET /tags after visibility delay error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tags after visibility delay status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Visible since 2026-02-03T00:00:00Z") || strings.Contains(body, "Pending until 2026-02-03T00:00:00Z") {
		t.Fatalf("GET /tags after visibility delay did not show visible state: %s", body)
	}

	resp, err = client.PostForm(server.URL()+"/tags/deactivate", url.Values{"tag_key": {"app"}})
	if err != nil {
		t.Fatalf("POST /tags/deactivate error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /tags/deactivate final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Deactivated app for cost allocation") ||
		!strings.Contains(body, "Not visible after deactivation") ||
		!strings.Contains(body, "deactivate") {
		t.Fatalf("POST /tags/deactivate response missing lifecycle close-out: %s", body)
	}

	var activationStatus string
	var eventCount int
	if err := db.QueryRowContext(ctx, `
		SELECT activation_status
		FROM cost_allocation_tag_keys
		WHERE tag_key = ?
	`, "app").Scan(&activationStatus); err != nil {
		t.Fatalf("read app activation status: %v", err)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM cost_allocation_tag_activation_events
		WHERE tag_key = ?
	`, "app").Scan(&eventCount); err != nil {
		t.Fatalf("count app activation events: %v", err)
	}
	if activationStatus != "deactivated" || eventCount != 2 {
		t.Fatalf("app lifecycle state = %q with %d events, want deactivated with activate/deactivate history", activationStatus, eventCount)
	}
}
