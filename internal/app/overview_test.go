package app

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOverviewIntroPageRendersWorkflowLinksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()

	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.StatePath = filepath.Join(root, "state.json")
	cfg.WorkspacePath = filepath.Join(root, "overview-workspace")
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

	client := appTestHTTPClient()
	resp, err := client.Get(server.URL() + "/overview")
	if err != nil {
		t.Fatalf("GET /overview error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /overview status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Path; got != "/overview" {
		t.Fatalf("GET /overview final path = %q, want /overview", got)
	}
	for _, want := range []string{
		`<title>Overview - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/overview">Overview</a>`,
		"Simulator Overview",
		"Core Interaction Flow",
		"Available Workflows",
		"Safe Starting Paths",
		"Start New Experience",
		`action="/workspaces/start"`,
		"Start a new experience",
		"No AWS credentials",
		"No real payments",
		"Not tax-valid invoices",
		"Synthetic pricing",
		"This project is not affiliated with, endorsed by, or sponsored by Amazon Web Services.",
		"organization/accounts create visibility context",
		"resources produce usage",
		"metering/pricing creates bill line items",
		"closes issue bills/invoices",
		"payments modify invoice state",
		"tags, Cost Categories, Savings Plans, and Pro Forma affect reporting/allocation",
		"exports/query lab consume generated billing data",
		"Scenario reset rebuilds the current workspace database around the selected lab seed",
		"Workspace clone copies the active workspace",
		`href="/workspaces"`,
		`href="/organization"`,
		`href="/resources"`,
		`href="/bills"`,
		`href="/invoices"`,
		`href="/payments"`,
		`href="/scenarios"`,
		`href="/tags"`,
		`href="/cost-categories"`,
		`href="/savings-plans"`,
		`href="/pro-forma"`,
		`href="/cost-explorer"`,
		`href="/budgets"`,
		`href="/exports"`,
		`href="/query-lab"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /overview body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Workspace Required") {
		t.Fatalf("GET /overview should not require a scenario or workspace workflow: %s", body)
	}
}
