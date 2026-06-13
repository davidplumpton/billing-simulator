package app

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"aws-billing-simulator/internal/persistence"
)

func TestSavingsPlanUIRequiresWorkspace(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/savings-plans")
	if err != nil {
		t.Fatalf("GET /savings-plans without workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /savings-plans without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Savings Plans - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/savings-plans">Savings Plans</a>`,
		"Workspace Required",
		`href="/workspaces"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /savings-plans without workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/savings-plans/create", url.Values{
		"payer_account_id":      {persistence.AnyCompanyRetailManagementAccountID},
		"owner_account_id":      {"111122223333"},
		"reference_usage_type":  {"instance-hours:t3.medium"},
		"region_code":           {"us-east-1"},
		"sharing_scope":         {persistence.SavingsPlanSharingScopeOrganization},
		"hourly_commitment_usd": {"0.10"},
		"upfront_fee_usd":       {"0.09"},
		"term_start_time":       {"2026-02-01T00:00"},
		"term_end_time":         {"2026-02-01T03:00"},
		"description":           {"Shared compute Savings Plan"},
	})
	if err != nil {
		t.Fatalf("POST /savings-plans/create without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /savings-plans/create without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before creating Savings Plans.") {
		t.Fatalf("POST /savings-plans/create without workspace missing workspace message: %s", body)
	}
}

func TestSavingsPlanWorkflowShowsGeneratedCoverage(t *testing.T) {
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

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/savings-plans")
	if err != nil {
		t.Fatalf("GET /savings-plans error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /savings-plans status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Compute Commitment Coverage",
		"Create Compute Savings Plan",
		"No Savings Plan purchases",
		"Owner account only",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /savings-plans missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/savings-plans/create", url.Values{
		"payer_account_id":      {persistence.AnyCompanyRetailManagementAccountID},
		"owner_account_id":      {"111122223333"},
		"reference_usage_type":  {"instance-hours:t3.medium"},
		"region_code":           {"us-east-1"},
		"sharing_scope":         {persistence.SavingsPlanSharingScopeOrganization},
		"hourly_commitment_usd": {"0.10"},
		"upfront_fee_usd":       {"0.09"},
		"term_start_time":       {"2026-02-01T00:00"},
		"term_end_time":         {"2026-02-01T03:00"},
		"description":           {"Shared compute Savings Plan"},
	})
	if err != nil {
		t.Fatalf("POST /savings-plans/create error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /savings-plans/create final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Created Savings Plan",
		"Shared compute Savings Plan",
		"Organization",
		"$0.10/hr",
		"Upfront $0.09",
		"No generated Savings Plan rows",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /savings-plans/create missing %q: %s", want, body)
		}
	}

	seedSavingsPlanWorkflowUsage(t, ctx, db)
	if _, err := persistence.NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}
	result, err := persistence.NewBillLineItemRepository(db).GenerateBillLineItems(ctx, persistence.BillLineItemGenerationRequest{})
	if err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}
	if result.ItemsCreated != 6 {
		t.Fatalf("GenerateBillLineItems() created %d, want two usage, two fees, and two negations", result.ItemsCreated)
	}

	resp, err = client.Get(server.URL + "/savings-plans")
	if err != nil {
		t.Fatalf("GET /savings-plans after pricing error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /savings-plans after pricing status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Generated Fees",
		"$0.39",
		"Negations",
		"-$0.1664",
		"Covered Usage",
		"$0.1664",
		"Amortized Source Cost",
		"$0.21632",
		"Upfront Fee",
		"Recurring Fee",
		"Negation",
		"Amazon EC2 instance-hours:t3.medium usage for savings-plan-storefront-web",
		"Amazon EC2 instance-hours:t3.medium usage for savings-plan-analytics-worker",
		"2 InstanceHour",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /savings-plans after pricing missing %q: %s", want, body)
		}
	}
}

func seedSavingsPlanWorkflowUsage(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	usageRepo := persistence.NewResourceUsageRepository(db)
	for _, request := range []persistence.ResourceCreateRequest{
		{
			ID:           "savings-plan-storefront-web",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  "AmazonEC2",
			ResourceType: "ec2_instance",
			ResourceName: "Savings Plan storefront web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
		},
		{
			ID:           "savings-plan-analytics-worker",
			AccountID:    "555566667777",
			RegionCode:   "us-east-1",
			ServiceCode:  "AmazonEC2",
			ResourceType: "ec2_instance",
			ResourceName: "Savings Plan analytics worker",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
		},
	} {
		if _, err := usageRepo.CreateResource(ctx, request); err != nil {
			t.Fatalf("CreateResource(%s) error = %v", request.ID, err)
		}
	}
	for _, request := range []persistence.UsageEventCreateRequest{
		{
			ID:                  "usage-savings-plan-storefront",
			ResourceID:          "savings-plan-storefront-web",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
		{
			ID:                  "usage-savings-plan-analytics",
			ResourceID:          "savings-plan-analytics-worker",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
	} {
		if _, err := usageRepo.RecordUsageEvent(ctx, request); err != nil {
			t.Fatalf("RecordUsageEvent(%s) error = %v", request.ID, err)
		}
	}
}
