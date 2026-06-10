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

// TestUsagePricingBillingEngineEpicWorksInFreshWorkspace keeps bd-zaw guarded through the browser-facing billing pipeline.
func TestUsagePricingBillingEngineEpicWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "billing-engine-epic-workspace")
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

	resp, err := client.Get(server.URL() + "/resources")
	if err != nil {
		t.Fatalf("GET /resources error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /resources status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Resources - AWS Billing Simulator</title>`,
		"Create Resource",
		"Generate Usage",
		"Run Daily Metering",
		"Close Previous Period",
		"Price Dimensions",
		`name="account_id" value="111122223333"`,
		`name="payer_account_id" value="999988887777"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /resources body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL()+"/resources/create", url.Values{
		"account_id":     {"111122223333"},
		"region_code":    {"us-east-1"},
		"service_preset": {"ec2_t3_medium"},
		"size":           {"t3.medium"},
		"resource_name":  {"Epic billing web"},
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
	if !strings.Contains(body, "Epic billing web") || !strings.Contains(body, "app=storefront") {
		t.Fatalf("resource create response missing created resource/tag: %s", body)
	}

	resourceID := readOnlyResourceID(t, db)
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
		!strings.Contains(body, "2026-02-02T00:00:00Z") {
		t.Fatalf("generator response missing generated usage details: %s", body)
	}

	resp, err = client.PostForm(server.URL()+"/resources/generate", url.Values{
		"resource_id":           {resourceID},
		"generation_pattern":    {"daily_instance_hours"},
		"generation_start_date": {"2026-02-01"},
		"generation_days":       {"1"},
	})
	if err != nil {
		t.Fatalf("repeat POST /resources/generate error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("repeat POST /resources/generate final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Reused 1 existing usage events") {
		t.Fatalf("repeat generator response missing reuse flash: %s", body)
	}

	var generatedUsage int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events WHERE resource_id = ? AND event_source = 'generator'`, resourceID).Scan(&generatedUsage); err != nil {
		t.Fatalf("count generated usage_events: %v", err)
	}
	if generatedUsage != 1 {
		t.Fatalf("generated usage event count = %d, want 1", generatedUsage)
	}

	body = postClockAdvance(t, &client, server.URL(), "2", string(persistence.SimulatorClockAdvanceDays))
	if !strings.Contains(body, "Advanced clock to 2026-02-03T00:00:00Z") ||
		!strings.Contains(body, "daily metering created 1 metering records and 2 bill line items") ||
		!strings.Contains(body, "Current Billing Summary") ||
		!strings.Contains(body, "AWSSupport") ||
		!strings.Contains(body, "estimated") ||
		!strings.Contains(body, "999988887777") {
		t.Fatalf("clock-advanced daily metering response missing estimated billing details: %s", body)
	}

	var meteringRecords, estimatedLineItems int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metering_records`).Scan(&meteringRecords); err != nil {
		t.Fatalf("count metering_records: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bill_line_items WHERE line_item_status = 'estimated' AND payer_account_id = ?`, "999988887777").Scan(&estimatedLineItems); err != nil {
		t.Fatalf("count estimated bill_line_items: %v", err)
	}
	if meteringRecords != 1 || estimatedLineItems != 2 {
		t.Fatalf("estimated pipeline counts = metering %d line items %d, want 1/2", meteringRecords, estimatedLineItems)
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
		"Closed Billing Periods",
		"Issued Bills",
		"SIM-INV-202602-",
		"$1.9984",
		"999988887777",
		"final",
		"due",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("month-end close response missing %q: %s", want, body)
		}
	}

	var finalLineItems, issuedBills, invoiceDocuments int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bill_line_items WHERE line_item_status = 'final' AND payer_account_id = ?`, "999988887777").Scan(&finalLineItems); err != nil {
		t.Fatalf("count final bill_line_items: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bills WHERE bill_state = 'issued' AND payer_account_id = ?`, "999988887777").Scan(&issuedBills); err != nil {
		t.Fatalf("count issued bills: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoice_documents`).Scan(&invoiceDocuments); err != nil {
		t.Fatalf("count invoice_documents: %v", err)
	}
	if finalLineItems != 2 || issuedBills != 1 || invoiceDocuments != 1 {
		t.Fatalf("final close counts = line items %d bills %d invoice docs %d, want 2/1/1", finalLineItems, issuedBills, invoiceDocuments)
	}

	resp, err = client.Get(server.URL() + "/bills?viewer_role=management-account&viewer_account_id=999988887777")
	if err != nil {
		t.Fatalf("GET /bills after close error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills after close status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Bill Reconciliation",
		"balanced",
		"Epic billing web",
		"AWSSupport",
		"$1.9984",
		"$0.00",
		"SIM-INV-202602-",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /bills after close missing %q: %s", want, body)
		}
	}
}

func TestResourcesUICreatesResourceAndUsage(t *testing.T) {
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

	resp, err := client.Get(server.URL + "/resources")
	if err != nil {
		t.Fatalf("GET /resources error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /resources status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Create Resource") || !strings.Contains(body, "Price Dimensions") {
		t.Fatalf("GET /resources body missing resource lab UI: %s", body)
	}
	if !strings.Contains(body, "Simulator Clock") || !strings.Contains(body, "2026-02-01T00:00:00Z") {
		t.Fatalf("GET /resources body missing simulator clock UI: %s", body)
	}
	if !strings.Contains(body, `name="account_id" value="111122223333"`) {
		t.Fatalf("GET /resources body missing Storefront Prod usage account default: %s", body)
	}
	if count := strings.Count(body, `name="payer_account_id" value="999988887777"`); count != 3 {
		t.Fatalf("GET /resources payer defaults = %d, want billing pipeline, daily metering, and month close defaults to management account: %s", count, body)
	}
	if strings.Contains(body, `name="payer_account_id" value="111122223333"`) {
		t.Fatalf("GET /resources still defaults payer forms to member account: %s", body)
	}

	resp, err = client.PostForm(server.URL+"/resources/create", url.Values{
		"account_id":     {"111122223333"},
		"region_code":    {"us-east-1"},
		"service_preset": {"ec2_t3_medium"},
		"size":           {"t3.medium"},
		"resource_name":  {"Storefront web"},
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
	if !strings.Contains(body, "Storefront web") || !strings.Contains(body, "app=storefront") {
		t.Fatalf("resource create response missing created resource/tag: %s", body)
	}

	resourceID := readOnlyResourceID(t, db)
	resp, err = client.PostForm(server.URL+"/resources/usage", url.Values{
		"resource_id":      {resourceID},
		"usage_preset":     {"ec2_hours"},
		"quantity":         {"2"},
		"usage_start_time": {"2026-02-01T00:00"},
		"usage_end_time":   {"2026-02-01T02:00"},
	})
	if err != nil {
		t.Fatalf("POST /resources/usage error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/usage final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "instance-hours:t3.medium") || !strings.Contains(body, "$0.0832") {
		t.Fatalf("usage response missing billable dimensions or estimate: %s", body)
	}

	resp, err = client.PostForm(server.URL+"/resources/generate", url.Values{
		"resource_id":           {resourceID},
		"generation_pattern":    {"daily_instance_hours"},
		"generation_start_date": {"2026-02-02"},
		"generation_days":       {"2"},
	})
	if err != nil {
		t.Fatalf("POST /resources/generate error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/generate final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Generated 2 usage events") || !strings.Contains(body, "2026-02-03T00:00:00Z") {
		t.Fatalf("generator response missing flash or deterministic usage window: %s", body)
	}

	var usageCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events WHERE resource_id = ?`, resourceID).Scan(&usageCount); err != nil {
		t.Fatalf("count usage events: %v", err)
	}
	if usageCount != 3 {
		t.Fatalf("usage event count = %d, want 3", usageCount)
	}

	var generatorCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events WHERE resource_id = ? AND event_source = 'generator'`, resourceID).Scan(&generatorCount); err != nil {
		t.Fatalf("count generated usage events: %v", err)
	}
	if generatorCount != 2 {
		t.Fatalf("generated usage event count = %d, want 2", generatorCount)
	}

	resp, err = client.PostForm(server.URL+"/resources/generate", url.Values{
		"resource_id":           {resourceID},
		"generation_pattern":    {"daily_instance_hours"},
		"generation_start_date": {"2026-02-02"},
		"generation_days":       {"2"},
	})
	if err != nil {
		t.Fatalf("repeat POST /resources/generate error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("repeat POST /resources/generate final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Reused 2 existing usage events") || !strings.Contains(body, "2026-02-03T00:00:00Z") {
		t.Fatalf("repeat generator response missing reuse flash or deterministic usage window: %s", body)
	}
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM usage_events WHERE resource_id = ?`, resourceID).Scan(&usageCount); err != nil {
		t.Fatalf("count usage events after repeat generation: %v", err)
	}
	if usageCount != 3 {
		t.Fatalf("usage event count after repeat generation = %d, want 3", usageCount)
	}

	resp, err = client.PostForm(server.URL+"/resources/billing-pipeline", url.Values{
		"payer_account_id": {"999988887777"},
	})
	if err != nil {
		t.Fatalf("POST /resources/billing-pipeline error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/billing-pipeline final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Created 3 metering records and 3 bill line items") ||
		!strings.Contains(body, "Metering Records") ||
		!strings.Contains(body, "Bill Line Items") ||
		!strings.Contains(body, "SIM-EC2-T3-MEDIUM-HR") ||
		!strings.Contains(body, "999988887777") {
		t.Fatalf("billing pipeline response missing metering or bill line item details: %s", body)
	}

	var meteringCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM metering_records`).Scan(&meteringCount); err != nil {
		t.Fatalf("count metering_records: %v", err)
	}
	if meteringCount != 3 {
		t.Fatalf("metering record count = %d, want 3", meteringCount)
	}

	var billLineItemCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bill_line_items`).Scan(&billLineItemCount); err != nil {
		t.Fatalf("count bill_line_items: %v", err)
	}
	if billLineItemCount != 3 {
		t.Fatalf("bill line item count = %d, want 3", billLineItemCount)
	}
}

func TestResourcesUIFiltersAndPartialRefresh(t *testing.T) {
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

	resp, err := client.Get(server.URL + "/resources?account_id=111122223333")
	if err != nil {
		t.Fatalf("GET /resources filtered error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /resources filtered status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<main class="page">`,
		`<script src="/assets/app.js" defer></script>`,
		`data-partial-form="resources"`,
		`name="account_id" value="111122223333"`,
		"Filter web",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /resources filtered body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Filter bucket") {
		t.Fatalf("GET /resources account filter included S3 resource: %s", body)
	}

	req, err := http.NewRequest(http.MethodGet, server.URL+"/resources?service_code=AmazonS3", nil)
	if err != nil {
		t.Fatalf("NewRequest(/resources fragment) error = %v", err)
	}
	req.Header.Set("X-AWS-Billing-Simulator-Fragment", "resources")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("GET /resources fragment error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /resources fragment status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if strings.Contains(body, "<main") || strings.Contains(body, `<script src="/assets/app.js"`) {
		t.Fatalf("GET /resources fragment returned full layout: %s", body)
	}
	for _, want := range []string{
		`data-partial-target="#resources-refresh"`,
		`name="service_code" value="AmazonS3"`,
		"Filter bucket",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /resources fragment body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Filter web") {
		t.Fatalf("GET /resources service fragment included EC2 resource: %s", body)
	}
}

func TestResourcesUIStorageEstimatesUseBillingPeriodDays(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		usageID        string
		usageStartTime string
		usageEndTime   string
		quantityMicros int64
		wantDays       int
	}{
		{
			name:           "February",
			usageID:        "usage-ui-storage-february",
			usageStartTime: "2026-02-10T00:00:00Z",
			usageEndTime:   "2026-02-11T00:00:00Z",
			quantityMicros: 280_000_000,
			wantDays:       28,
		},
		{
			name:           "March",
			usageID:        "usage-ui-storage-march",
			usageStartTime: "2026-03-10T00:00:00Z",
			usageEndTime:   "2026-03-11T00:00:00Z",
			quantityMicros: 310_000_000,
			wantDays:       31,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
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
			resource, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
				ID:           "resource-" + tt.usageID,
				AccountID:    "111122223333",
				RegionCode:   "us-east-1",
				ServiceCode:  "AmazonEBS",
				ResourceType: "ebs_volume",
				ResourceName: tt.name + " volume",
				Status:       "active",
				StartedAt:    "2026-02-01T00:00:00Z",
			})
			if err != nil {
				t.Fatalf("CreateResource() error = %v", err)
			}
			event, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
				ID:                  tt.usageID,
				ResourceID:          resource.ID,
				UsageType:           "storage:gp3-gb-month",
				Operation:           "VolumeStorage",
				UsageStartTime:      tt.usageStartTime,
				UsageEndTime:        tt.usageEndTime,
				UsageQuantityMicros: tt.quantityMicros,
				UsageUnit:           "GBDay",
			})
			if err != nil {
				t.Fatalf("RecordUsageEvent() error = %v", err)
			}

			view := newResourceLabHandler(db).usageEventView(ctx, event, resource.ResourceName)
			if view.EstimatedCost == "unpriced" {
				t.Fatalf("usageEventView() estimate = %q, want priced storage estimate", view.EstimatedCost)
			}

			if _, err := persistence.NewMeteringRepository(db).GenerateMeteringRecords(ctx); err != nil {
				t.Fatalf("GenerateMeteringRecords() error = %v", err)
			}
			result, err := persistence.NewBillLineItemRepository(db).GenerateBillLineItems(ctx, persistence.BillLineItemGenerationRequest{})
			if err != nil {
				t.Fatalf("GenerateBillLineItems() error = %v", err)
			}
			if result.ItemsCreated != 1 {
				t.Fatalf("GenerateBillLineItems() created %d, want 1", result.ItemsCreated)
			}
			item := result.Items[0]
			if item.BillingPeriodDays != tt.wantDays {
				t.Fatalf("bill line item billing period days = %d, want %d", item.BillingPeriodDays, tt.wantDays)
			}
			if want := formatUSDMicros(item.UnblendedCostMicros); view.EstimatedCost != want {
				t.Fatalf("usageEventView() estimate = %q, want persisted bill line item cost %q", view.EstimatedCost, want)
			}
		})
	}
}

func TestResourcesUIAdvancesSimulatorClock(t *testing.T) {
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

	resp, err := client.Get(server.URL + "/resources")
	if err != nil {
		t.Fatalf("GET /resources error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /resources status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "2026-02-01 to 2026-03-01 (28 days)") {
		t.Fatalf("GET /resources body missing initial billing period: %s", body)
	}
	if !strings.Contains(body, "100 GB-day $0.285714") {
		t.Fatalf("GET /resources body missing February storage price-dimension estimate: %s", body)
	}

	body = postClockAdvance(t, client, server.URL, "6", string(persistence.SimulatorClockAdvanceHours))
	if !strings.Contains(body, "Advanced clock to 2026-02-01T06:00:00Z") ||
		!strings.Contains(body, `value="2026-02-01T06:00"`) ||
		!strings.Contains(body, `value="2026-02-01T07:00"`) {
		t.Fatalf("hour advance response missing updated clock defaults: %s", body)
	}

	body = postClockAdvance(t, client, server.URL, "2", string(persistence.SimulatorClockAdvanceDays))
	if !strings.Contains(body, "Advanced clock to 2026-02-03T06:00:00Z") ||
		!strings.Contains(body, `value="2026-02-03"`) {
		t.Fatalf("day advance response missing updated clock defaults: %s", body)
	}

	body = postClockAdvance(t, client, server.URL, "1", string(persistence.SimulatorClockAdvanceBillingPeriods))
	if !strings.Contains(body, "Advanced clock to 2026-03-01T00:00:00Z") ||
		!strings.Contains(body, "2026-03-01 to 2026-04-01 (31 days)") ||
		!strings.Contains(body, "100 GB-day $0.258065") ||
		!strings.Contains(body, `value="2026-03-01T00:00"`) {
		t.Fatalf("billing-period advance response missing updated clock state: %s", body)
	}

	resp, err = client.PostForm(server.URL+"/resources/create", url.Values{
		"account_id":     {"111122223333"},
		"region_code":    {"us-east-1"},
		"service_preset": {"ec2_t3_medium"},
		"size":           {"t3.medium"},
		"resource_name":  {"Clock default web"},
		"status":         {"active"},
	})
	if err != nil {
		t.Fatalf("POST /resources/create error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/create final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}

	var startedAt string
	if err := db.QueryRowContext(ctx, `SELECT started_at FROM resources WHERE resource_name = ?`, "Clock default web").Scan(&startedAt); err != nil {
		t.Fatalf("read created resource started_at: %v", err)
	}
	if startedAt != "2026-03-01T00:00:00Z" {
		t.Fatalf("created resource started_at = %q, want simulator clock default", startedAt)
	}
}

func TestResourcesUIDailyMeteringRunsOnDemandAndAfterClockAdvance(t *testing.T) {
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

	body := postClockAdvance(t, client, server.URL, "2", string(persistence.SimulatorClockAdvanceHours))
	if !strings.Contains(body, "daily metering created 0 metering records and 0 bill line items") {
		t.Fatalf("initial clock advance response missing daily metering job flash: %s", body)
	}

	resp, err := client.PostForm(server.URL+"/resources/create", url.Values{
		"account_id":     {"111122223333"},
		"region_code":    {"us-east-1"},
		"service_preset": {"ec2_t3_medium"},
		"size":           {"t3.medium"},
		"resource_name":  {"Daily metered web"},
		"status":         {"active"},
		"started_at":     {"2026-02-01T00:00"},
	})
	if err != nil {
		t.Fatalf("POST /resources/create error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/create final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}

	resourceID := readOnlyResourceID(t, db)
	resp, err = client.PostForm(server.URL+"/resources/usage", url.Values{
		"resource_id":      {resourceID},
		"usage_preset":     {"ec2_hours"},
		"quantity":         {"1"},
		"usage_start_time": {"2026-02-01T00:00"},
		"usage_end_time":   {"2026-02-01T01:00"},
	})
	if err != nil {
		t.Fatalf("POST /resources/usage ready error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/usage ready final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	resp, err = client.PostForm(server.URL+"/resources/usage", url.Values{
		"resource_id":      {resourceID},
		"usage_preset":     {"ec2_hours"},
		"quantity":         {"1"},
		"usage_start_time": {"2026-02-01T02:00"},
		"usage_end_time":   {"2026-02-01T03:00"},
	})
	if err != nil {
		t.Fatalf("POST /resources/usage future error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/usage future final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}

	resp, err = client.PostForm(server.URL+"/resources/daily-metering", url.Values{
		"payer_account_id": {"999988887777"},
	})
	if err != nil {
		t.Fatalf("POST /resources/daily-metering error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/daily-metering final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Daily metering created 1 metering records, 2 bill line items, and refreshed 2 summaries") ||
		!strings.Contains(body, "Current Billing Summary") ||
		!strings.Contains(body, "Daily Metering Jobs") ||
		!strings.Contains(body, "AWSSupport") ||
		!strings.Contains(body, "estimated") ||
		!strings.Contains(body, "999988887777") {
		t.Fatalf("daily metering response missing summary/job details: %s", body)
	}

	body = postClockAdvance(t, client, server.URL, "1", string(persistence.SimulatorClockAdvanceHours))
	if !strings.Contains(body, "daily metering created 1 metering records and 1 bill line items") ||
		!strings.Contains(body, "clock_advance") ||
		!strings.Contains(body, "on_demand") {
		t.Fatalf("clock advance response missing triggered daily metering details: %s", body)
	}

	var jobRunCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM daily_metering_job_runs`).Scan(&jobRunCount); err != nil {
		t.Fatalf("count daily_metering_job_runs: %v", err)
	}
	if jobRunCount != 3 {
		t.Fatalf("daily metering job run count = %d, want 3", jobRunCount)
	}
}

func TestResourcesUIMonthEndCloseIssuesBill(t *testing.T) {
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
	clockRepo := persistence.NewSimulatorClockRepository(db)
	resource, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-ui-month-close",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Closeable web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-ui-month-close",
		ResourceID:          resource.ID,
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: 2_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if _, err := clockRepo.Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.PostForm(server.URL+"/resources/month-close", url.Values{
		"payer_account_id": {"999988887777"},
		"invoice_due_days": {"10"},
	})
	if err != nil {
		t.Fatalf("POST /resources/month-close error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/month-close final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Month-end close finalized 2 line items") ||
		!strings.Contains(body, "Closed Billing Periods") ||
		!strings.Contains(body, "Issued Bills") ||
		!strings.Contains(body, "AWSSupport") ||
		!strings.Contains(body, "SIM-INV-202602-") ||
		!strings.Contains(body, "999988887777") ||
		!strings.Contains(body, "2026-03-11") ||
		!strings.Contains(body, "final") ||
		!strings.Contains(body, "due") {
		t.Fatalf("month-end close response missing close, bill, or invoice details: %s", body)
	}
}

func TestResourcesUIBillingPeriodWorkflowClosesFreshWorkspace(t *testing.T) {
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

	resp, err := client.PostForm(server.URL+"/resources/create", url.Values{
		"account_id":     {"111122223333"},
		"region_code":    {"us-east-1"},
		"service_preset": {"ec2_t3_medium"},
		"size":           {"t3.medium"},
		"resource_name":  {"Workflow web"},
		"status":         {"active"},
	})
	if err != nil {
		t.Fatalf("POST /resources/create error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/create final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}

	resourceID := readOnlyResourceID(t, db)
	resp, err = client.PostForm(server.URL+"/resources/usage", url.Values{
		"resource_id":      {resourceID},
		"usage_preset":     {"ec2_hours"},
		"quantity":         {"2"},
		"usage_start_time": {"2026-02-01T00:00"},
		"usage_end_time":   {"2026-02-01T02:00"},
	})
	if err != nil {
		t.Fatalf("POST /resources/usage error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /resources/usage final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}

	body = postClockAdvance(t, client, server.URL, "3", string(persistence.SimulatorClockAdvanceHours))
	if !strings.Contains(body, "Advanced clock to 2026-02-01T03:00:00Z") ||
		!strings.Contains(body, "daily metering created 1 metering records and 2 bill line items") ||
		!strings.Contains(body, "Current Billing Summary") ||
		!strings.Contains(body, "AWSSupport") ||
		!strings.Contains(body, "estimated") ||
		!strings.Contains(body, "999988887777") {
		t.Fatalf("clock-advanced daily metering response missing estimated billing summary: %s", body)
	}
	var estimatedManagementItems int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bill_line_items WHERE line_item_status = 'estimated' AND payer_account_id = ?`, "999988887777").Scan(&estimatedManagementItems); err != nil {
		t.Fatalf("count estimated management bill_line_items: %v", err)
	}
	if estimatedManagementItems != 2 {
		t.Fatalf("estimated management bill line item count = %d, want usage plus Support", estimatedManagementItems)
	}

	body = postClockAdvance(t, client, server.URL, "1", string(persistence.SimulatorClockAdvanceBillingPeriods))
	if !strings.Contains(body, "Advanced clock to 2026-03-01T00:00:00Z") ||
		!strings.Contains(body, "2026-03-01 to 2026-04-01 (31 days)") {
		t.Fatalf("billing-period advance response missing March clock state: %s", body)
	}

	resp, err = client.PostForm(server.URL+"/resources/month-close", url.Values{
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
	if !strings.Contains(body, "Month-end close finalized 2 line items into bill") ||
		!strings.Contains(body, "Closed Billing Periods") ||
		!strings.Contains(body, "Issued Bills") ||
		!strings.Contains(body, "AWSSupport") ||
		!strings.Contains(body, "SIM-INV-202602-") ||
		!strings.Contains(body, "$1.0832") ||
		!strings.Contains(body, "999988887777") ||
		!strings.Contains(body, "final") ||
		!strings.Contains(body, "due") {
		t.Fatalf("month-end close response missing final bill workflow details: %s", body)
	}

	var finalLineItems int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bill_line_items WHERE line_item_status = 'final' AND payer_account_id = ?`, "999988887777").Scan(&finalLineItems); err != nil {
		t.Fatalf("count final bill_line_items: %v", err)
	}
	if finalLineItems != 2 {
		t.Fatalf("final management bill line item count = %d, want 2", finalLineItems)
	}
	var issuedBills int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bills WHERE bill_state = 'issued' AND payer_account_id = ?`, "999988887777").Scan(&issuedBills); err != nil {
		t.Fatalf("count issued bills: %v", err)
	}
	if issuedBills != 1 {
		t.Fatalf("issued bill count = %d, want 1", issuedBills)
	}
	var dueInvoices int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM invoice_obligations WHERE status = 'due'`).Scan(&dueInvoices); err != nil {
		t.Fatalf("count due invoice obligations: %v", err)
	}
	if dueInvoices != 1 {
		t.Fatalf("due invoice count = %d, want 1", dueInvoices)
	}

	resp, err = client.Get(server.URL + "/bills")
	if err != nil {
		t.Fatalf("GET /bills after close error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills after close status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Bill Reconciliation") ||
		!strings.Contains(body, "balanced") ||
		!strings.Contains(body, "$1.0832") ||
		!strings.Contains(body, "$0.00") ||
		!strings.Contains(body, "Rounding Residual") {
		t.Fatalf("GET /bills after close missing balanced reconciliation: %s", body)
	}

	var invoiceID string
	if err := db.QueryRowContext(ctx, `SELECT invoice_id FROM invoice_documents LIMIT 1`).Scan(&invoiceID); err != nil {
		t.Fatalf("read invoice document ID: %v", err)
	}
	invoicePath := invoicePathForID(invoiceID)
	invoiceCSVPath := invoiceCSVPathForID(invoiceID)
	invoicePDFPath := invoicePDFPathForID(invoiceID)
	curCSVPath := curCSVExportPath(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     "999988887777",
		LineItemStatus:     "final",
	})
	curReconcilePath := curExportReconciliationPath(persistence.CURExportReconciliationRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     "999988887777",
		LineItemStatus:     "final",
	})
	managementViewerQuery := "?viewer_role=management-account&viewer_account_id=999988887777"
	managementInvoiceQuery := "viewer_account_id=999988887777&viewer_role=management-account"
	escapedManagementInvoiceQuery := "viewer_account_id=999988887777&amp;viewer_role=management-account"
	memberViewerQuery := "?viewer_role=member-account&viewer_account_id=111122223333"
	if !strings.Contains(body, invoicePath) {
		t.Fatalf("GET /bills after close missing printable invoice link %q: %s", invoiceID, body)
	}
	if !strings.Contains(body, invoiceCSVPath) || !strings.Contains(body, invoicePDFPath) {
		t.Fatalf("GET /bills after close missing invoice export links %q/%q: %s", invoiceCSVPath, invoicePDFPath, body)
	}
	escapedCURCSVPath := strings.ReplaceAll(curCSVPath, "&", "&amp;")
	escapedCURReconcilePath := strings.ReplaceAll(curReconcilePath, "&", "&amp;")
	if !strings.Contains(body, escapedCURCSVPath) || !strings.Contains(body, escapedCURReconcilePath) {
		t.Fatalf("GET /bills after close missing CUR export links %q/%q: %s", curCSVPath, curReconcilePath, body)
	}
	resp, err = client.Get(server.URL + "/bills" + managementViewerQuery)
	if err != nil {
		t.Fatalf("GET /bills management viewer after close error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills management viewer after close status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`href="` + invoicePath + `?` + escapedManagementInvoiceQuery + `"`,
		`href="` + invoiceCSVPath + `?` + escapedManagementInvoiceQuery + `"`,
		`href="` + invoicePDFPath + `?` + escapedManagementInvoiceQuery + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /bills management viewer after close missing scoped invoice link %q: %s", want, body)
		}
	}
	resp, err = client.Get(server.URL + "/bills" + memberViewerQuery)
	if err != nil {
		t.Fatalf("GET /bills member viewer after close error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills member viewer after close status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	memberCURCSVPath := curCSVExportPathWithViewer(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     "999988887777",
		UsageAccountID:     "111122223333",
		LineItemStatus:     "final",
	}, exportViewerFields{Role: "member-account", AccountID: "111122223333"})
	memberCURReconcilePath := curExportReconciliationPathWithViewer(persistence.CURExportReconciliationRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     "999988887777",
		UsageAccountID:     "111122223333",
		LineItemStatus:     "final",
	}, exportViewerFields{Role: "member-account", AccountID: "111122223333"})
	for _, want := range []string{
		"Workflow web",
		"invoice restricted",
		strings.ReplaceAll(memberCURCSVPath, "&", "&amp;"),
		strings.ReplaceAll(memberCURReconcilePath, "&", "&amp;"),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /bills member viewer after close missing %q: %s", want, body)
		}
	}
	for _, leaked := range []string{
		invoiceID,
		invoicePath,
		invoiceCSVPath,
		invoicePDFPath,
		"due $1.0832 paid $0.00",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("GET /bills member viewer after close leaked invoice document detail %q: %s", leaked, body)
		}
	}
	resp, err = client.Get(server.URL + invoicePath)
	if err != nil {
		t.Fatalf("GET /invoices/{id} error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /invoices/{id} status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Invoice " + invoiceID,
		"AnyCompany Retail",
		"Service Detail",
		"Account Detail",
		"Invoice Lines",
		"Workflow web",
		"AWSSupport",
		"AWS Support Business",
		"Usage",
		"Fee",
		"$1.0832",
		"$1.00",
		"due",
		invoiceCSVPath,
		invoicePDFPath,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /invoices/{id} body missing %q: %s", want, body)
		}
	}

	resp, err = client.Get(server.URL + invoicePath + memberViewerQuery)
	if err != nil {
		t.Fatalf("GET /invoices/{id} member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /invoices/{id} member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}

	resp, err = client.Get(server.URL + invoicePath + managementViewerQuery)
	if err != nil {
		t.Fatalf("GET /invoices/{id} management viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /invoices/{id} management viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Invoice "+invoiceID) || !strings.Contains(body, "Workflow web") {
		t.Fatalf("GET /invoices/{id} management viewer missing invoice details: %s", body)
	}
	for _, want := range []string{
		`href="` + invoiceCSVPath + `?` + escapedManagementInvoiceQuery + `"`,
		`href="` + invoicePDFPath + `?` + escapedManagementInvoiceQuery + `"`,
		`href="/bills?` + escapedManagementInvoiceQuery + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /invoices/{id} management viewer missing scoped action link %q: %s", want, body)
		}
	}
	crossPayerViewerQuery := "?viewer_role=management-account&viewer_account_id=000000000000"
	resp, err = client.Get(server.URL + invoicePath + crossPayerViewerQuery)
	if err != nil {
		t.Fatalf("GET /invoices/{id} cross-payer management viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /invoices/{id} cross-payer management viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}

	resp, err = client.Get(server.URL + invoiceCSVPath)
	if err != nil {
		t.Fatalf("GET /invoices/{id}/line-items.csv error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /invoices/{id}/line-items.csv status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("GET /invoices/{id}/line-items.csv content type = %q, want text/csv", contentType)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, invoiceID+"-line-items.csv") {
		t.Fatalf("GET /invoices/{id}/line-items.csv content disposition = %q, want invoice filename", disposition)
	}
	assertHEADDownloadMatchesGET(t, newMux(db), invoiceCSVPath, resp.Header, "Content-Type", "Content-Disposition")
	for _, want := range []string{
		"invoice_id,bill_id,document_status,payment_status",
		invoiceID,
		"Workflow web",
		"AWSSupport",
		"AWS Support Business",
		"Usage",
		"Fee",
		"0.083200",
		"1.000000",
		"999988887777",
		"111122223333",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /invoices/{id}/line-items.csv body missing %q: %s", want, body)
		}
	}

	resp, err = client.Get(server.URL + invoiceCSVPath + memberViewerQuery)
	if err != nil {
		t.Fatalf("GET /invoices/{id}/line-items.csv member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /invoices/{id}/line-items.csv member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}

	resp, err = client.Get(server.URL + invoiceCSVPath + managementViewerQuery)
	if err != nil {
		t.Fatalf("GET /invoices/{id}/line-items.csv management viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /invoices/{id}/line-items.csv management viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, invoiceID) || !strings.Contains(body, "Workflow web") || !strings.Contains(body, "999988887777") {
		t.Fatalf("GET /invoices/{id}/line-items.csv management viewer missing export details: %s", body)
	}
	resp, err = client.Get(server.URL + invoiceCSVPath + crossPayerViewerQuery)
	if err != nil {
		t.Fatalf("GET /invoices/{id}/line-items.csv cross-payer management viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /invoices/{id}/line-items.csv cross-payer management viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}

	resp, err = client.Get(server.URL + invoicePDFPath + memberViewerQuery)
	if err != nil {
		t.Fatalf("GET /invoices/{id}/document.pdf member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /invoices/{id}/document.pdf member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}

	resp, err = client.Get(server.URL + invoicePDFPath)
	if err != nil {
		t.Fatalf("GET /invoices/{id}/document.pdf error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /invoices/{id}/document.pdf status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "application/pdf") {
		t.Fatalf("GET /invoices/{id}/document.pdf content type = %q, want application/pdf", contentType)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, invoiceID+"-document.pdf") {
		t.Fatalf("GET /invoices/{id}/document.pdf content disposition = %q, want invoice PDF filename", disposition)
	}
	assertHEADDownloadMatchesGET(t, newMux(db), invoicePDFPath, resp.Header, "Content-Type", "Content-Disposition", "Link")
	if !strings.HasPrefix(body, "%PDF-1.4") ||
		!strings.Contains(body, "Invoice "+invoiceID) ||
		!strings.Contains(body, "AnyCompany Retail") ||
		!strings.Contains(body, "Workflow web") ||
		!strings.Contains(body, "AWSSupport") ||
		!strings.Contains(body, "%%EOF") {
		t.Fatalf("GET /invoices/{id}/document.pdf missing rendered invoice PDF content: headers=%v body=%s", resp.Header, body)
	}

	resp, err = client.Get(server.URL + invoicePDFPath + managementViewerQuery)
	if err != nil {
		t.Fatalf("GET /invoices/{id}/document.pdf management viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /invoices/{id}/document.pdf management viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	scopedManagementInvoicePath := invoicePath + "?" + managementInvoiceQuery
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/pdf") ||
		!strings.Contains(body, "Invoice "+invoiceID) ||
		!strings.Contains(body, "Workflow web") ||
		!strings.Contains(resp.Header.Get("Link"), "<"+scopedManagementInvoicePath+">") {
		t.Fatalf("GET /invoices/{id}/document.pdf management viewer missing scoped PDF response: headers=%v body=%s", resp.Header, body)
	}
	resp, err = client.Get(server.URL + invoicePDFPath + crossPayerViewerQuery)
	if err != nil {
		t.Fatalf("GET /invoices/{id}/document.pdf cross-payer management viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /invoices/{id}/document.pdf cross-payer management viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
}
