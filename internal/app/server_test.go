package app

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func TestStartServesHealthCheck(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	client := http.Client{Timeout: time.Second}
	resp, err := client.Get(server.URL() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz error = %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /healthz body: %v", err)
	}
	if string(body) != "ok\n" {
		t.Fatalf("GET /healthz body = %q, want %q", string(body), "ok\n")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Close(shutdownCtx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := server.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestStartOpensBrowserAtDashboardURL(t *testing.T) {
	originalOpenBrowserURL := openBrowserURL
	t.Cleanup(func() {
		openBrowserURL = originalOpenBrowserURL
	})

	var openedURLs []string
	openBrowserURL = func(url string) error {
		openedURLs = append(openedURLs, url)
		return nil
	}

	cfg := DefaultConfig()
	cfg.OpenBrowser = true
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

	if len(openedURLs) != 1 {
		t.Fatalf("opened URLs = %v, want one dashboard URL", openedURLs)
	}
	if openedURLs[0] != server.URL() {
		t.Fatalf("opened URL = %q, want %q", openedURLs[0], server.URL())
	}
}

func TestStartAppliesWorkspaceMigrations(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Close(shutdownCtx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := server.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}

	db, err := sql.Open("sqlite", persistence.WorkspaceDBPath(cfg.WorkspacePath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 18 {
		t.Fatalf("schema_migrations count = %d, want 18", count)
	}

	var catalogCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM price_catalog_items`).Scan(&catalogCount); err != nil {
		t.Fatalf("count price_catalog_items: %v", err)
	}
	if catalogCount != 18 {
		t.Fatalf("price_catalog_items count = %d, want 18", catalogCount)
	}
}

func TestRunStartedServerClosesWorkspaceAfterUnexpectedServeExit(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = t.TempDir()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	server, err := Start(cfg, logger)
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Close(shutdownCtx)
	})

	workspaceDB := server.workspace.DB()
	if workspaceDB == nil {
		t.Fatal("Start() did not open workspace database")
	}

	runErr := make(chan error, 1)
	go func() {
		runErr <- runStartedServer(context.Background(), server)
	}()

	if err := server.listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	select {
	case err := <-runErr:
		if err == nil {
			t.Fatal("runStartedServer() error = nil, want unexpected serve error")
		}
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("runStartedServer() error = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runStartedServer() did not return after listener close")
	}

	if db := server.workspace.DB(); db != nil {
		t.Fatal("workspace database remained active after unexpected serve exit")
	}
	if err := workspaceDB.PingContext(context.Background()); err == nil {
		t.Fatal("closed workspace database still accepted PingContext")
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
	if !strings.Contains(body, invoicePath) {
		t.Fatalf("GET /bills after close missing printable invoice link %q: %s", invoiceID, body)
	}
	if !strings.Contains(body, invoiceCSVPath) || !strings.Contains(body, invoicePDFPath) {
		t.Fatalf("GET /bills after close missing invoice export links %q/%q: %s", invoiceCSVPath, invoicePDFPath, body)
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

	resp, err = client.Get(server.URL + invoicePDFPath)
	if err != nil {
		t.Fatalf("GET /invoices/{id}/document.pdf error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("GET /invoices/{id}/document.pdf status = %d, want %d; body=%s", resp.StatusCode, http.StatusNotImplemented, body)
	}
	if !strings.Contains(resp.Header.Get("X-Invoice-PDF-Implementation"), "html-to-pdf") ||
		!strings.Contains(body, "packaged HTML-to-PDF renderer") ||
		!strings.Contains(body, invoicePath) {
		t.Fatalf("GET /invoices/{id}/document.pdf missing implementation plan: headers=%v body=%s", resp.Header, body)
	}
}

func postClockAdvance(t *testing.T, client *http.Client, serverURL, amount, unit string) string {
	t.Helper()

	resp, err := client.PostForm(serverURL+"/clock/advance", url.Values{
		"clock_advance_amount": {amount},
		"clock_advance_unit":   {unit},
	})
	if err != nil {
		t.Fatalf("POST /clock/advance error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /clock/advance final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	return body
}

func readResponseBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(body)
}

func readOnlyResourceID(t *testing.T, db *sql.DB) string {
	t.Helper()

	var resourceID string
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM resources LIMIT 1`).Scan(&resourceID); err != nil {
		t.Fatalf("read resource ID: %v", err)
	}
	return resourceID
}

func insertBillsUIStoredBillState(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	periodStart string,
	periodEnd string,
	payerAccountID string,
	billState string,
	invoiceStatus string,
	usageChargeMicros int64,
	creditMicros int64,
	refundMicros int64,
	taxMicros int64,
) {
	t.Helper()

	totalMicros := usageChargeMicros + taxMicros - creditMicros - refundMicros
	if totalMicros < 0 {
		totalMicros = 0
	}
	periodKey := strings.ReplaceAll(periodStart, "-", "")
	stateKey := strings.ReplaceAll(billState, "_", "-")
	amountDueMicros := totalMicros
	amountPaidMicros := int64(0)
	if invoiceStatus == "paid" {
		amountDueMicros = 0
		amountPaidMicros = totalMicros
	}

	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO billing_period_closes (
			id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			status,
			metering_records_created,
			bill_line_items_created,
			finalized_line_item_count,
			finalized_cost_micros,
			currency_code,
			summaries_refreshed
		) VALUES (?, ?, ?, ?, 'closed', 0, 0, 1, ?, 'USD', 0)`,
		"close-ui-"+periodKey+"-"+stateKey,
		periodStart,
		periodEnd,
		payerAccountID,
		totalMicros,
	); err != nil {
		t.Fatalf("insert billing_period_closes: %v", err)
	}
	billID := "bill-ui-" + periodKey + "-" + stateKey
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO bills (
			id,
			close_id,
			billing_period_start,
			billing_period_end,
			payer_account_id,
			bill_state,
			currency_code,
			line_item_count,
			usage_charge_micros,
			credit_micros,
			refund_micros,
			tax_micros,
			total_micros
		) VALUES (?, ?, ?, ?, ?, ?, 'USD', 1, ?, ?, ?, ?, ?)`,
		billID,
		"close-ui-"+periodKey+"-"+stateKey,
		periodStart,
		periodEnd,
		payerAccountID,
		billState,
		usageChargeMicros,
		creditMicros,
		refundMicros,
		taxMicros,
		totalMicros,
	); err != nil {
		t.Fatalf("insert bills: %v", err)
	}
	if _, err := db.ExecContext(
		ctx,
		`INSERT INTO invoice_obligations (
			id,
			bill_id,
			invoice_id,
			status,
			amount_due_micros,
			amount_paid_micros,
			currency_code,
			invoice_date,
			due_date
		) VALUES (?, ?, ?, ?, ?, ?, 'USD', ?, ?)`,
		"iob-ui-"+periodKey+"-"+stateKey,
		billID,
		"SIM-INV-"+strings.ReplaceAll(periodStart[:7], "-", "")+"-"+strings.ToUpper(stateKey),
		invoiceStatus,
		amountDueMicros,
		amountPaidMicros,
		periodEnd,
		periodEnd,
	); err != nil {
		t.Fatalf("insert invoice_obligations: %v", err)
	}
}
