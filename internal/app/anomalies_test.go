package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func TestAnomaliesUIRequiresWorkspace(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/anomalies")
	if err != nil {
		t.Fatalf("GET /anomalies without workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /anomalies without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Anomalies - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/anomalies">Anomalies</a>`,
		"Workspace Required",
		`href="/workspaces"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /anomalies without workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/anomalies/refresh", url.Values{})
	if err != nil {
		t.Fatalf("POST /anomalies/refresh without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /anomalies/refresh without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before refreshing anomaly alerts.") {
		t.Fatalf("POST /anomalies/refresh without workspace missing workspace message: %s", body)
	}
}

func TestAnomaliesPageRefreshesAndListsAlerts(t *testing.T) {
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
	seedAnomalyComparisonSpend(t, ctx, db)

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/anomalies")
	if err != nil {
		t.Fatalf("GET /anomalies error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /anomalies status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Anomalies",
		"Refresh Alerts",
		"Cost Anomaly Alerts",
		"No cost anomaly alerts",
		`<a class="active" aria-current="page" href="/anomalies">Anomalies</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /anomalies body missing %q: %s", want, body)
		}
	}

	beforeGET := readAnomalyGeneratedStateFingerprint(t, ctx, db, "2026-02-01", "2026-03-01", "2026-01-01", "2026-02-01")
	resp, err = client.PostForm(server.URL+"/anomalies/refresh", url.Values{
		"billing_period_start":  {"2026-02-01"},
		"billing_period_end":    {"2026-03-01"},
		"baseline_period_start": {"2026-01-01"},
		"baseline_period_end":   {"2026-02-01"},
		"threshold_percent":     {"200"},
		"minimum_current_cost":  {"0.05"},
	})
	if err != nil {
		t.Fatalf("POST /anomalies/refresh error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /anomalies/refresh final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Refreshed 4 cost anomaly alerts",
		"Service",
		"Account",
		"Tag",
		"Cost Category",
		"Amazon EC2",
		"Storefront",
		"app=storefront",
		"Product=Storefront",
		"$0.0832",
		"$0.0416",
		"200%",
		"Spike",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /anomalies/refresh body missing %q: %s", want, body)
		}
	}

	afterRefresh := readAnomalyGeneratedStateFingerprint(t, ctx, db, "2026-02-01", "2026-03-01", "2026-01-01", "2026-02-01")
	if afterRefresh == beforeGET {
		t.Fatalf("POST /anomalies/refresh did not persist alerts: %s", afterRefresh)
	}

	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 2; i++ {
		resp, err = client.Get(server.URL + "/anomalies?billing_period_start=2026-02-01&billing_period_end=2026-03-01&baseline_period_start=2026-01-01&baseline_period_end=2026-02-01&threshold_percent=200&minimum_current_cost=0.05")
		if err != nil {
			t.Fatalf("GET /anomalies idempotency check %d error = %v", i+1, err)
		}
		body = readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /anomalies idempotency check %d status = %d, want %d; body=%s", i+1, resp.StatusCode, http.StatusOK, body)
		}
	}
	afterGET := readAnomalyGeneratedStateFingerprint(t, ctx, db, "2026-02-01", "2026-03-01", "2026-01-01", "2026-02-01")
	if afterGET != afterRefresh {
		t.Fatalf("GET /anomalies changed anomaly state:\nbefore=%s\nafter=%s", afterRefresh, afterGET)
	}
}

func seedAnomalyComparisonSpend(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	usageRepo := persistence.NewResourceUsageRepository(db)
	if _, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-anomaly-baseline-web",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Anomaly baseline web",
		Status:       "active",
		StartedAt:    "2026-01-01T00:00:00Z",
		Tags: map[string]string{
			"app": "storefront",
		},
	}); err != nil {
		t.Fatalf("CreateResource(baseline web) error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-anomaly-baseline-web",
		ResourceID:          "resource-anomaly-baseline-web",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-01-10T00:00:00Z",
		UsageEndTime:        "2026-01-10T01:00:00Z",
		UsageQuantityMicros: 1_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(baseline web) error = %v", err)
	}
	if _, err := persistence.NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords(baseline) error = %v", err)
	}
	if _, err := persistence.NewBillLineItemRepository(db).GenerateBillLineItems(ctx, persistence.BillLineItemGenerationRequest{}); err != nil {
		t.Fatalf("GenerateBillLineItems(baseline) error = %v", err)
	}

	seedCostCategoryPreviewSpend(t, ctx, db)

	categoryRepo := persistence.NewCostCategoryRepository(db)
	product, err := categoryRepo.CreateCategory(ctx, persistence.CostCategoryCreateRequest{
		Name:         "Product",
		Description:  "Product anomaly grouping",
		DefaultValue: "Unallocated",
	})
	if err != nil {
		t.Fatalf("CreateCategory(Product) error = %v", err)
	}
	if _, err := categoryRepo.CreateRule(ctx, persistence.CostCategoryRuleCreateRequest{
		CostCategoryID: product.ID,
		RuleOrder:      10,
		Value:          "Storefront",
		Conditions: []persistence.CostCategoryRuleCondition{
			{
				Dimension: persistence.CostCategoryRuleMatchTag,
				TagKey:    "app",
				Values:    []string{"storefront"},
			},
		},
	}); err != nil {
		t.Fatalf("CreateRule(Product=Storefront) error = %v", err)
	}
	if _, err := categoryRepo.RefreshAssignmentsForOpenPeriods(ctx); err != nil {
		t.Fatalf("RefreshAssignmentsForOpenPeriods() error = %v", err)
	}
}

func readAnomalyGeneratedStateFingerprint(t *testing.T, ctx context.Context, db *sql.DB, periodStart, periodEnd, baselineStart, baselineEnd string) string {
	t.Helper()

	alerts, err := persistence.NewCostAnomalyRepository(db).ListAlerts(ctx, persistence.CostAnomalyListRequest{
		BillingPeriodStart:  periodStart,
		BillingPeriodEnd:    periodEnd,
		BaselinePeriodStart: baselineStart,
		BaselinePeriodEnd:   baselineEnd,
	})
	if err != nil {
		t.Fatalf("ListAlerts(%s to %s) error = %v", periodStart, periodEnd, err)
	}
	return fmt.Sprintf("alerts=%#v", alerts)
}
