package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func TestBillsUIShowsBillStatesAndTotals(t *testing.T) {
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
		ID:           "resource-bills-ui-february",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "February bill web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource(February) error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-bills-ui-february",
		ResourceID:          "resource-bills-ui-february",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: 2_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(February) error = %v", err)
	}
	if _, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-bills-ui-march",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "March bill web",
		Status:       "active",
		StartedAt:    "2026-03-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource(March) error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-bills-ui-march",
		ResourceID:          "resource-bills-ui-march",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-03-02T00:00:00Z",
		UsageEndTime:        "2026-03-02T01:00:00Z",
		UsageQuantityMicros: 1_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(March) error = %v", err)
	}
	if _, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-bills-ui-s3",
		AccountID:    "222233334444",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonS3",
		ResourceType: "s3_bucket",
		ResourceName: "Receipts bucket",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource(S3) error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-bills-ui-s3-put",
		ResourceID:          "resource-bills-ui-s3",
		UsageType:           "requests:put-1k",
		Operation:           "PutObject",
		UsageStartTime:      "2026-02-02T00:00:00Z",
		UsageEndTime:        "2026-02-03T00:00:00Z",
		UsageQuantityMicros: 1_500_000_000,
		UsageUnit:           "Request",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(S3) error = %v", err)
	}
	if _, err := persistence.NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
		t.Fatalf("GenerateMeteringRecords() error = %v", err)
	}
	if _, err := persistence.NewBillLineItemRepository(db).GenerateBillLineItems(ctx, persistence.BillLineItemGenerationRequest{}); err != nil {
		t.Fatalf("GenerateBillLineItems() error = %v", err)
	}
	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-03-15T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}

	insertBillsUIStoredBillState(t, ctx, db, "2025-10-01", "2025-11-01", "111122223333", "issued", "due", 1_000_000, 0, 0, 0)
	insertBillsUIStoredBillState(t, ctx, db, "2025-11-01", "2025-12-01", "111122223333", "adjusted", "due", 3_000_000, 500_000, 0, 200_000)
	insertBillsUIStoredBillState(t, ctx, db, "2025-12-01", "2026-01-01", "111122223333", "paid", "paid", 4_000_000, 0, 0, 0)
	insertBillsUIStoredBillState(t, ctx, db, "2026-01-01", "2026-02-01", "111122223333", "past_due", "past_due", 5_000_000, 0, 0, 0)

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/bills")
	if err != nil {
		t.Fatalf("GET /bills error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Bill States",
		"Open",
		"Pending Close",
		"Issued",
		"Adjusted",
		"Paid",
		"Past Due",
		"Bill Reconciliation",
		"Source Total",
		"Rounding Residual",
		"Charges by Service and Account",
		"Resource Charge Drilldown",
		"open",
		"pending-close",
		"issued",
		"adjusted",
		"paid",
		"past-due",
		"residual",
		"Charges",
		"Credits",
		"Tax",
		"Total",
		"$0.0416",
		"$0.0832",
		"$0.0075",
		"$2.70",
		"Amazon S3",
		"requests:put-1k",
		"222233334444",
		"Receipts bucket",
		"February bill web",
		"not issued",
		"SIM-INV-202511-ADJUSTED",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /bills body missing %q: %s", want, body)
		}
	}
}

func TestBillsUIShowsCompleteStateCardsPastSummaryDisplayLimit(t *testing.T) {
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

	var wantTotal int64
	for i := 0; i < 60; i++ {
		start := time.Date(2020, time.January, 1, 0, 0, 0, 0, time.UTC).AddDate(0, i, 0)
		end := start.AddDate(0, 1, 0)
		usageMicros := int64(i+1) * 1_000_000
		wantTotal += usageMicros
		insertBillsUIStoredBillState(
			t,
			ctx,
			db,
			start.Format(time.DateOnly),
			end.Format(time.DateOnly),
			"111122223333",
			"paid",
			"paid",
			usageMicros,
			0,
			0,
			0,
		)
	}

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	resp, err := server.Client().Get(server.URL + "/bills")
	if err != nil {
		t.Fatalf("GET /bills error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"51 of 61 summaries shown",
		"50 of 60 bills shown",
		"60 bills",
		formatUSDMicros(wantTotal),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /bills over limit body missing %q: %s", want, body)
		}
	}
	if oldLimitedTotal := formatUSDMicros(1_775_000_000); strings.Contains(body, oldLimitedTotal) {
		t.Fatalf("GET /bills over limit body includes old limited-card total %q: %s", oldLimitedTotal, body)
	}
}

func TestBillsUIFiltersAndPartialRefresh(t *testing.T) {
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
	seedFilterableUsage(t, ctx, db)

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/bills?usage_account_id=222233334444&service_code=AmazonS3")
	if err != nil {
		t.Fatalf("GET /bills filtered error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills filtered status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<main class="page">`,
		`<script src="/assets/app.js" defer></script>`,
		`data-partial-form="bills"`,
		`name="usage_account_id" value="222233334444"`,
		`name="service_code" value="AmazonS3"`,
		"Filter bucket",
		"Amazon S3",
		"222233334444",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /bills filtered body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Filter web") {
		t.Fatalf("GET /bills filtered body included EC2 resource: %s", body)
	}

	req, err := http.NewRequest(http.MethodGet, server.URL+"/bills?payer_account_id=999988887777&usage_account_id=111122223333", nil)
	if err != nil {
		t.Fatalf("NewRequest(/bills fragment) error = %v", err)
	}
	req.Header.Set("X-AWS-Billing-Simulator-Fragment", "bills")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET /bills fragment error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills fragment status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if strings.Contains(body, "<main") || strings.Contains(body, `<script src="/assets/app.js"`) {
		t.Fatalf("GET /bills fragment returned full layout: %s", body)
	}
	for _, want := range []string{
		`data-partial-target="#bills-refresh"`,
		`name="payer_account_id" value="999988887777"`,
		`name="usage_account_id" value="111122223333"`,
		"Filter web",
		"Amazon EC2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /bills fragment body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Filter bucket") {
		t.Fatalf("GET /bills usage-account fragment included S3 resource: %s", body)
	}
}

func TestBillsUIFiltersBySimulatedViewer(t *testing.T) {
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
	seedFilterableUsage(t, ctx, db)

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/bills?viewer_role=member-account&viewer_account_id=111122223333")
	if err != nil {
		t.Fatalf("GET /bills member viewer error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<option value="member-account" selected>Member</option>`,
		`name="viewer_account_id" value="111122223333"`,
		"Filter web",
		"Amazon EC2",
		"111122223333",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /bills member viewer body missing %q: %s", want, body)
		}
	}
	for _, leaked := range []string{
		"Filter bucket",
		"Amazon S3",
		"222233334444",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("GET /bills member viewer leaked %q: %s", leaked, body)
		}
	}

	resp, err = client.Get(server.URL + "/bills?viewer_role=management-account&viewer_account_id=999988887777")
	if err != nil {
		t.Fatalf("GET /bills management viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills management viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<option value="management-account" selected>Management</option>`,
		`name="viewer_account_id" value="999988887777"`,
		"Filter web",
		"Amazon EC2",
		"Filter bucket",
		"Amazon S3",
		"222233334444",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /bills management viewer body missing %q: %s", want, body)
		}
	}
}
