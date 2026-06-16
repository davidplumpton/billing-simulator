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

func TestProFormaUIRequiresWorkspace(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/pro-forma")
	if err != nil {
		t.Fatalf("GET /pro-forma without workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /pro-forma without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Pro Forma - Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/pro-forma">Pro Forma</a>`,
		"Workspace Required",
		`href="/workspaces"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /pro-forma without workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/pro-forma/pricing-plans/create", url.Values{"name": {"Retail Showback"}})
	if err != nil {
		t.Fatalf("POST /pro-forma/pricing-plans/create without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /pro-forma/pricing-plans/create without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before creating pro forma pricing plans.") {
		t.Fatalf("POST /pro-forma/pricing-plans/create without workspace missing workspace message: %s", body)
	}
}

func TestProFormaWorkflow(t *testing.T) {
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
	sourceCountBefore, sourceCostBefore := readProFormaSourceBillTotals(t, ctx, db)

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/pro-forma")
	if err != nil {
		t.Fatalf("GET /pro-forma error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /pro-forma status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Internal Showback",
		"New Pricing Plan",
		"Pricing Plans",
		"No pricing plans",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /pro-forma missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/pro-forma/pricing-plans/create", url.Values{
		"name":        {"Retail Showback"},
		"description": {"Internal rates for product teams"},
	})
	if err != nil {
		t.Fatalf("POST create pricing plan error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create pricing plan final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Created pricing plan Retail Showback") || !strings.Contains(body, "Service Rate") {
		t.Fatalf("POST create pricing plan response missing plan state: %s", body)
	}
	planID := readProFormaPricingPlanID(t, db, "Retail Showback")

	resp, err = client.PostForm(server.URL+"/pro-forma/pricing-rules/create", url.Values{
		"pricing_plan_id":    {planID},
		"service_code":       {"AmazonEC2"},
		"multiplier_percent": {"150"},
		"description":        {"Compute margin"},
	})
	if err != nil {
		t.Fatalf("POST create pricing rule error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create pricing rule final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Saved 150% multiplier for AmazonEC2") || !strings.Contains(body, "Compute margin") {
		t.Fatalf("POST create pricing rule response missing rule state: %s", body)
	}

	resp, err = client.PostForm(server.URL+"/pro-forma/billing-groups/create", url.Values{
		"name":             {"Storefront Showback"},
		"description":      {"Storefront internal bill"},
		"payer_account_id": {"999988887777"},
		"pricing_plan_id":  {planID},
	})
	if err != nil {
		t.Fatalf("POST create billing group error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create billing group final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Created billing group Storefront Showback") || !strings.Contains(body, "Assign Account") {
		t.Fatalf("POST create billing group response missing group state: %s", body)
	}
	groupID := readProFormaBillingGroupID(t, db, "Storefront Showback")

	resp, err = client.PostForm(server.URL+"/pro-forma/accounts/assign", url.Values{
		"billing_group_id": {groupID},
		"account_id":       {"111122223333"},
	})
	if err != nil {
		t.Fatalf("POST assign account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST assign account final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Assigned account 111122223333") || !strings.Contains(body, "Storefront Prod") {
		t.Fatalf("POST assign account response missing assignment state: %s", body)
	}

	resp, err = client.PostForm(server.URL+"/pro-forma/refresh", url.Values{
		"billing_group_id":     {groupID},
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"payer_account_id":     {"999988887777"},
	})
	if err != nil {
		t.Fatalf("POST refresh pro forma rows error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST refresh pro forma rows final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Refreshed 1 pro forma rows",
		"Showback Summary",
		"Generated Rows",
		"Custom Items",
		"Storefront Showback",
		"Amazon EC2",
		"150%",
		"$0.0832",
		"$0.1248",
		"$0.0416",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST refresh pro forma rows missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/pro-forma/custom-line-items/create", url.Values{
		"billing_group_id":     {groupID},
		"line_item_type":       {"markup"},
		"name":                 {"Shared tooling markup"},
		"amount_usd":           {"0.10"},
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"description":          {"Internal tooling recovery"},
	})
	if err != nil {
		t.Fatalf("POST create markup custom line item error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create markup custom line item final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Added custom Markup Shared tooling markup",
		"Shared tooling markup",
		"Internal tooling recovery",
		"$0.10",
		"$0.2248",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST create markup custom line item missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/pro-forma/custom-line-items/create", url.Values{
		"billing_group_id":     {groupID},
		"line_item_type":       {"credit"},
		"name":                 {"Training credit"},
		"amount_usd":           {"0.05"},
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"description":          {"Instructor approved"},
	})
	if err != nil {
		t.Fatalf("POST create credit custom line item error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST create credit custom line item final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Added custom Credit Training credit",
		"Training credit",
		"Instructor approved",
		"-$0.05",
		"$0.1748",
		"$0.0916",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST create credit custom line item missing %q: %s", want, body)
		}
	}

	sourceCountAfter, sourceCostAfter := readProFormaSourceBillTotals(t, ctx, db)
	if sourceCountAfter != sourceCountBefore || sourceCostAfter != sourceCostBefore {
		t.Fatalf("bill line item source changed from count/cost %d/%d to %d/%d", sourceCountBefore, sourceCostBefore, sourceCountAfter, sourceCostAfter)
	}
}

func readProFormaPricingPlanID(t *testing.T, db *sql.DB, name string) string {
	t.Helper()

	var id string
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM pro_forma_pricing_plans WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatalf("read pro forma pricing plan %q ID: %v", name, err)
	}
	return id
}

func readProFormaBillingGroupID(t *testing.T, db *sql.DB, name string) string {
	t.Helper()

	var id string
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM pro_forma_billing_groups WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatalf("read pro forma billing group %q ID: %v", name, err)
	}
	return id
}

func readProFormaSourceBillTotals(t *testing.T, ctx context.Context, db *sql.DB) (int, int64) {
	t.Helper()

	var count int
	var cost int64
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(unblended_cost_micros), 0)
		FROM bill_line_items
		WHERE billing_period_start = '2026-02-01'
		  AND billing_period_end = '2026-03-01'
	`).Scan(&count, &cost); err != nil {
		t.Fatalf("read source bill totals: %v", err)
	}
	return count, cost
}
