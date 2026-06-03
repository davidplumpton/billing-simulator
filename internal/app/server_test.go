package app

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
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
	if count != 10 {
		t.Fatalf("schema_migrations count = %d, want 10", count)
	}

	var catalogCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM price_catalog_items`).Scan(&catalogCount); err != nil {
		t.Fatalf("count price_catalog_items: %v", err)
	}
	if catalogCount != 18 {
		t.Fatalf("price_catalog_items count = %d, want 18", catalogCount)
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
	if !strings.Contains(body, "Daily metering created 1 metering records, 1 bill line items, and refreshed 1 summaries") ||
		!strings.Contains(body, "Current Billing Summary") ||
		!strings.Contains(body, "Daily Metering Jobs") ||
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
