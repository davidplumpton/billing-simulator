package app

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aws-billing-simulator/internal/persistence"
	"aws-billing-simulator/internal/scenario"
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
	if count != 33 {
		t.Fatalf("schema_migrations count = %d, want 33", count)
	}

	var catalogCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM price_catalog_items`).Scan(&catalogCount); err != nil {
		t.Fatalf("count price_catalog_items: %v", err)
	}
	if catalogCount != 18 {
		t.Fatalf("price_catalog_items count = %d, want 18", catalogCount)
	}
}

func TestLocalServerSmokeFlowCreatesWorkspaceAndServesDashboard(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspacePath := filepath.Join(root, "smoke-workspace")

	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
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

	client := http.Client{Timeout: time.Second}

	resp, err := client.Get(server.URL() + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if strings.TrimSpace(body) != "ok" {
		t.Fatalf("GET /healthz body = %q, want ok", body)
	}

	resp, err = client.Get(server.URL() + "/")
	if err != nil {
		t.Fatalf("GET / error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Path; got != "/workspaces" {
		t.Fatalf("GET / final path = %q, want /workspaces", got)
	}
	for _, want := range []string{
		`<title>Workspaces - AWS Billing Simulator</title>`,
		`<link rel="stylesheet" href="/assets/app.css">`,
		`<script src="/assets/app.js" defer></script>`,
		"No workspace open",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET / workspace selector missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL()+"/workspaces/open", url.Values{
		"workspace_path": {workspacePath},
	})
	if err != nil {
		t.Fatalf("POST /workspaces/open error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /workspaces/open final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Path; got != "/resources" {
		t.Fatalf("POST /workspaces/open final path = %q, want /resources", got)
	}
	for _, want := range []string{
		`<title>Resources - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/resources">Resources</a>`,
		"Opened workspace",
		"Create Resource",
		"Simulator Clock",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /workspaces/open resource dashboard missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Workspace Required") {
		t.Fatalf("resource dashboard still requires a workspace: %s", body)
	}
	if _, err := os.Stat(persistence.WorkspaceDBPath(workspacePath)); err != nil {
		t.Fatalf("workspace database was not created: %v", err)
	}

	resp, err = client.Get(server.URL() + "/")
	if err != nil {
		t.Fatalf("GET / after workspace open error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / after workspace open final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Path; got != "/resources" {
		t.Fatalf("GET / after workspace open final path = %q, want /resources", got)
	}
	if !strings.Contains(body, "Create Resource") || strings.Contains(body, "Workspace Required") {
		t.Fatalf("GET / after workspace open did not render dashboard: %s", body)
	}

	assets := []struct {
		path        string
		contentType string
		wants       []string
	}{
		{
			path:        "/assets/app.css",
			contentType: "text/css",
			wants:       []string{"--accent: #0f766e", "@media (max-width: 980px)"},
		},
		{
			path:        "/assets/app.js",
			contentType: "text/javascript",
			wants:       []string{"data-partial-form", "X-AWS-Billing-Simulator-Fragment"},
		},
	}
	for _, asset := range assets {
		resp, err := client.Get(server.URL() + asset.path)
		if err != nil {
			t.Fatalf("GET %s error = %v", asset.path, err)
		}
		body = readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d; body=%s", asset.path, resp.StatusCode, http.StatusOK, body)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, asset.contentType) {
			t.Fatalf("GET %s Content-Type = %q, want prefix %q", asset.path, got, asset.contentType)
		}
		for _, want := range asset.wants {
			if !strings.Contains(body, want) {
				t.Fatalf("GET %s missing %q: %s", asset.path, want, body)
			}
		}
	}
}

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

	client := http.Client{Timeout: time.Second}
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
		"No AWS credentials",
		"No real payments",
		"Not tax-valid invoices",
		"Synthetic pricing",
		"organization/accounts create visibility context",
		"resources produce usage",
		"metering/pricing creates bill line items",
		"closes issue bills/invoices",
		"payments modify invoice state",
		"tags and Cost Categories affect reporting/allocation",
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

func TestOrganizationHierarchyEditorFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	workspacePath := filepath.Join(root, "organization-workspace")

	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
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

	client := http.Client{Timeout: time.Second}
	resp, err := client.Get(server.URL() + "/organization")
	if err != nil {
		t.Fatalf("GET /organization without workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Workspace Required") || !strings.Contains(body, `href="/workspaces"`) {
		t.Fatalf("GET /organization without workspace missing workspace empty state: %s", body)
	}

	resp, err = client.PostForm(server.URL()+"/workspaces/open", url.Values{
		"workspace_path": {workspacePath},
	})
	if err != nil {
		t.Fatalf("POST /workspaces/open error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /workspaces/open final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Path; got != "/resources" {
		t.Fatalf("POST /workspaces/open final path = %q, want /resources", got)
	}
	if _, err := os.Stat(persistence.WorkspaceDBPath(workspacePath)); err != nil {
		t.Fatalf("workspace database was not created: %v", err)
	}

	resp, err = client.Get(server.URL() + "/organization")
	if err != nil {
		t.Fatalf("GET /organization with workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization with workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Organization - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/organization">Organization</a>`,
		"AnyCompany Retail",
		"Root - ou_anycompany_root",
		"Storefront Prod",
		"Deprecated Prototype",
		"storefront-team",
		"4100-storefront",
		"shared-networking",
		"9900-deprecated",
		"13 accounts",
		"12 active, 1 suspended, 0 closed",
		`action="/organization/accounts/create"`,
		`action="/organization/accounts/move"`,
		`action="/organization/accounts/suspend"`,
		`action="/organization/accounts/close"`,
		`href="/resources?account_id=111122223333"`,
		`href="/bills?payer_account_id=999988887777&amp;usage_account_id=111122223333&amp;viewer_account_id=111122223333&amp;viewer_role=member-account"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /organization with workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL()+"/organization/accounts/create", url.Values{
		"organization_id": {persistence.AnyCompanyRetailOrganizationID},
		"account_id":      {"777788889901"},
		"parent_unit_id":  {"ou_anycompany_sandbox"},
		"account_name":    {"Feature Lab Account"},
		"account_email":   {"feature-lab@anycompany.example"},
		"effective_at":    {"2026-02-02T00:00"},
	})
	if err != nil {
		t.Fatalf("POST create account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Created account Feature Lab Account") {
		t.Fatalf("POST create account status/body = %d/%s, want success flash", resp.StatusCode, body)
	}

	resp, err = client.PostForm(server.URL()+"/organization/accounts/move", url.Values{
		"account_id":     {"777788889901"},
		"parent_unit_id": {"ou_anycompany_workloads"},
		"effective_at":   {"2026-02-05T00:00"},
	})
	if err != nil {
		t.Fatalf("POST move account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Moved Feature Lab Account to Root/Workloads") {
		t.Fatalf("POST move account status/body = %d/%s, want success flash", resp.StatusCode, body)
	}

	resp, err = client.PostForm(server.URL()+"/organization/accounts/suspend", url.Values{
		"account_id":   {"777788889901"},
		"effective_at": {"2026-02-10T00:00"},
	})
	if err != nil {
		t.Fatalf("POST suspend account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Suspended Feature Lab Account") {
		t.Fatalf("POST suspend account status/body = %d/%s, want success flash", resp.StatusCode, body)
	}

	resp, err = client.PostForm(server.URL()+"/organization/accounts/close", url.Values{
		"account_id":   {"777788889901"},
		"effective_at": {"2026-02-15T00:00"},
	})
	if err != nil {
		t.Fatalf("POST close account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST close account status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Closed Feature Lab Account",
		"14 accounts",
		"12 active, 1 suspended, 1 closed",
		"Feature Lab Account",
		"Root/Workloads",
		`<span class="status status-closed">Closed</span>`,
		"17 events",
		"Suspended -&gt; Closed",
		`href="/resources?account_id=777788889901"`,
		`href="/bills?payer_account_id=999988887777&amp;usage_account_id=777788889901&amp;viewer_account_id=777788889901&amp;viewer_role=member-account"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST close account body missing %q: %s", want, body)
		}
	}

	db := server.workspace.DB()
	if db == nil {
		t.Fatal("workspace database is nil after organization workflow")
	}
	repo := persistence.NewOrganizationRepository(db)
	account, err := repo.GetAccount(ctx, "777788889901")
	if err != nil {
		t.Fatalf("GetAccount(created) error = %v", err)
	}
	if account.Status != persistence.AccountStatusClosed ||
		account.OUPath != "Root/Workloads" ||
		account.LeftAt != "2026-02-15T00:00:00Z" {
		t.Fatalf("created account after browser workflow = %+v, want closed in Workloads with left_at", account)
	}
	events, err := repo.ListAccountLifecycleEvents(ctx, persistence.AnyCompanyRetailOrganizationID, 200)
	if err != nil {
		t.Fatalf("ListAccountLifecycleEvents() error = %v", err)
	}
	if count := organizationLifecycleEventCountForAccount(events, "777788889901"); count != 4 {
		t.Fatalf("created account lifecycle event count = %d, want 4", count)
	}
}

func TestAnyCompanySeedOrganizationFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	workspacePath := filepath.Join(root, "anycompany-seed-workspace")

	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.StatePath = filepath.Join(root, "state.json")
	cfg.WorkspacePath = workspacePath
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
	repo := persistence.NewOrganizationRepository(db)
	client := http.Client{Timeout: time.Second}

	resp, err := client.Get(server.URL() + "/organization")
	if err != nil {
		t.Fatalf("GET /organization error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"AnyCompany Retail",
		"anycompany-retail",
		"999988887777",
		"13 accounts",
		"12 active, 1 suspended, 0 closed",
		"Storefront Prod",
		"Root/Workloads",
		"storefront-team",
		"4100-storefront",
		"Deprecated Prototype",
		"9900-deprecated",
		`href="/resources?account_id=111122223333"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /organization seed body missing %q: %s", want, body)
		}
	}

	organization, err := repo.GetOrganizationByTemplate(ctx, persistence.AnyCompanyRetailTemplateKey)
	if err != nil {
		t.Fatalf("GetOrganizationByTemplate() error = %v", err)
	}
	if organization.ID != persistence.AnyCompanyRetailOrganizationID ||
		organization.ManagementAccountID != persistence.AnyCompanyRetailManagementAccountID {
		t.Fatalf("organization = %+v, want AnyCompany Retail seed identifiers", organization)
	}
	accounts, err := repo.ListAccounts(ctx, organization.ID)
	if err != nil {
		t.Fatalf("ListAccounts() error = %v", err)
	}
	if len(accounts) != 13 {
		t.Fatalf("seed account count = %d, want 13", len(accounts))
	}
	tags, err := repo.ListAccountTags(ctx, organization.ID)
	if err != nil {
		t.Fatalf("ListAccountTags() error = %v", err)
	}
	if len(tags) != 65 {
		t.Fatalf("seed account tag count = %d, want 65", len(tags))
	}
	events, err := repo.ListAccountLifecycleEvents(ctx, organization.ID, 200)
	if err != nil {
		t.Fatalf("ListAccountLifecycleEvents() error = %v", err)
	}
	if len(events) != 13 {
		t.Fatalf("seed lifecycle event count = %d, want 13", len(events))
	}

	if _, err := repo.CreateAccount(ctx, persistence.AccountCreateRequest{
		ID:             "777788889902",
		OrganizationID: organization.ID,
		ParentUnitID:   "ou_anycompany_sandbox",
		Name:           "Seed Drift Account",
		Email:          "seed-drift@anycompany.example",
		EffectiveAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateAccount(drift) error = %v", err)
	}
	if _, err := repo.MoveAccount(ctx, persistence.AccountMoveRequest{
		AccountID:    "111122223333",
		ParentUnitID: "ou_anycompany_sandbox",
		EffectiveAt:  "2026-02-02T00:00:00Z",
	}); err != nil {
		t.Fatalf("MoveAccount(Storefront Prod drift) error = %v", err)
	}
	if _, err := repo.SuspendAccount(ctx, persistence.AccountSuspendRequest{
		AccountID:   "111122223333",
		EffectiveAt: "2026-02-03T00:00:00Z",
	}); err != nil {
		t.Fatalf("SuspendAccount(Storefront Prod drift) error = %v", err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE account_tags SET tag_value = ? WHERE account_id = ? AND tag_key = ?`, "drifted-owner", "111122223333", "owner"); err != nil {
		t.Fatalf("update account tag drift: %v", err)
	}

	resp, err = client.Get(server.URL() + "/organization")
	if err != nil {
		t.Fatalf("GET /organization after drift error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization after drift status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Seed Drift Account") ||
		!strings.Contains(body, "14 accounts") ||
		!strings.Contains(body, "drifted-owner") {
		t.Fatalf("GET /organization after drift missing visible drift: %s", body)
	}

	definition, err := scenario.LoadSeedDefinition(scenario.UntaggedDataTransferSpikeSeedKey)
	if err != nil {
		t.Fatalf("LoadSeedDefinition() error = %v", err)
	}
	result, err := scenario.NewRunner(db).Run(ctx, definition)
	if err != nil {
		t.Fatalf("Run(packaged scenario) error = %v", err)
	}
	if result.Run.Status != "succeeded" ||
		result.ResourcesCreated != 2 ||
		result.UsageEventsCreated != 3 ||
		result.BillsIssued != 1 {
		t.Fatalf("Run(packaged scenario) result = %+v, want successful AnyCompany lab execution", result)
	}

	resp, err = client.Get(server.URL() + "/organization")
	if err != nil {
		t.Fatalf("GET /organization after scenario reset error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization after scenario reset status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"AnyCompany Retail",
		"13 accounts",
		"12 active, 1 suspended, 0 closed",
		"Storefront Prod",
		"Root/Workloads",
		"storefront-team",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /organization after scenario reset missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Seed Drift Account") || strings.Contains(body, "drifted-owner") {
		t.Fatalf("GET /organization after scenario reset retained drift: %s", body)
	}
	storefrontProd, err := repo.GetAccount(ctx, "111122223333")
	if err != nil {
		t.Fatalf("GetAccount(Storefront Prod) after reset error = %v", err)
	}
	if storefrontProd.ParentUnitID != "ou_anycompany_workloads" ||
		storefrontProd.Status != persistence.AccountStatusActive ||
		storefrontProd.Owner != "storefront-team" ||
		storefrontProd.PayerAccountID != persistence.AnyCompanyRetailManagementAccountID {
		t.Fatalf("Storefront Prod after scenario reset = %+v, want seed OU/status/owner/payer", storefrontProd)
	}
}

// TestOrganizationAccountSimulationEpicWorksInFreshWorkspace keeps the parent epic guarded across its browser-facing surfaces.
func TestOrganizationAccountSimulationEpicWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "organization-epic-workspace")
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
	client := http.Client{Timeout: time.Second}

	resp, err := client.Get(server.URL() + "/organization")
	if err != nil {
		t.Fatalf("GET /organization error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"AnyCompany Retail",
		"999988887777",
		"Storefront Prod",
		"Root/Workloads",
		"storefront-team",
		"4100-storefront",
		"12 active, 1 suspended, 0 closed",
		`action="/organization/accounts/create"`,
		`action="/organization/accounts/move"`,
		`action="/organization/accounts/suspend"`,
		`action="/organization/accounts/close"`,
		`href="/bills?payer_account_id=999988887777&amp;usage_account_id=111122223333&amp;viewer_account_id=111122223333&amp;viewer_role=member-account"`,
		`href="/bills?payer_account_id=999988887777&amp;viewer_account_id=999988887777&amp;viewer_role=management-account">Bills</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /organization body missing %q: %s", want, body)
		}
	}

	resp, err = client.Get(server.URL() + "/resources")
	if err != nil {
		t.Fatalf("GET /resources error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /resources status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, `name="account_id" value="111122223333"`) {
		t.Fatalf("GET /resources body missing Storefront Prod usage account default: %s", body)
	}
	if count := strings.Count(body, `name="payer_account_id" value="999988887777"`); count != 3 {
		t.Fatalf("GET /resources management payer defaults = %d, want 3: %s", count, body)
	}
	if strings.Contains(body, `name="payer_account_id" value="111122223333"`) {
		t.Fatalf("GET /resources still defaults payer forms to member account: %s", body)
	}

	seedFilterableUsage(t, ctx, db)

	var managementLineItems int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM bill_line_items WHERE payer_account_id = ?`, persistence.AnyCompanyRetailManagementAccountID).Scan(&managementLineItems); err != nil {
		t.Fatalf("count management bill_line_items: %v", err)
	}
	if managementLineItems != 2 {
		t.Fatalf("management bill_line_items = %d, want 2", managementLineItems)
	}

	resp, err = client.Get(server.URL() + "/bills?viewer_role=management-account&viewer_account_id=999988887777")
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
		"Filter bucket",
		"111122223333",
		"222233334444",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /bills management viewer body missing %q: %s", want, body)
		}
	}

	resp, err = client.Get(server.URL() + "/bills?viewer_role=member-account&viewer_account_id=111122223333")
	if err != nil {
		t.Fatalf("GET /bills member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<option value="member-account" selected>Member</option>`,
		`name="viewer_account_id" value="111122223333"`,
		"Filter web",
		"111122223333",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /bills member viewer body missing %q: %s", want, body)
		}
	}
	for _, leaked := range []string{
		"Filter bucket",
		"222233334444",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("GET /bills member viewer leaked %q: %s", leaked, body)
		}
	}
}

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
	client := http.Client{Timeout: time.Second}

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

func TestPaymentsUIResolvesFailedInvoiceAndProfileMethod(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "payments-ui-workspace")
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

	usageRepo := persistence.NewResourceUsageRepository(db)
	if _, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-payments-ui-web",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Payments UI web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-payments-ui-web",
		ResourceID:          "resource-payments-ui-web",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T03:00:00Z",
		UsageQuantityMicros: 3_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}
	closeResult, err := persistence.NewMonthEndCloseRepository(db).ClosePreviousPeriod(ctx, persistence.MonthEndCloseRequest{
		PayerAccountID: persistence.AnyCompanyRetailManagementAccountID,
		InvoiceDueDays: 14,
	})
	if err != nil {
		t.Fatalf("ClosePreviousPeriod() error = %v", err)
	}

	profileRepo := persistence.NewPaymentProfileRepository(db)
	failedMethod, err := profileRepo.CreatePaymentMethod(ctx, persistence.PaymentMethodCreateRequest{
		ID:               "paymeth_ui_failed_card",
		PaymentProfileID: "payprof_anycompany_retail_management",
		MethodType:       "card",
		DisplayName:      "Expired corporate card",
		Status:           "failed",
		CardBrand:        "Visa",
		AccountLast4:     "4242",
		ExpirationMonth:  2,
		ExpirationYear:   2026,
		FailureReason:    "card expired",
	})
	if err != nil {
		t.Fatalf("CreatePaymentMethod(failed card) error = %v", err)
	}
	if _, err := profileRepo.CreatePaymentMethod(ctx, persistence.PaymentMethodCreateRequest{
		ID:                      "paymeth_ui_advance_pay",
		PaymentProfileID:        "payprof_anycompany_retail_management",
		MethodType:              "advance_pay_balance",
		DisplayName:             "Advance Pay reserve",
		AdvancePayBalanceMicros: 3_500_000,
	}); err != nil {
		t.Fatalf("CreatePaymentMethod(Advance Pay) error = %v", err)
	}

	client := http.Client{Timeout: time.Second}
	resp, err := client.Get(server.URL() + "/payments")
	if err != nil {
		t.Fatalf("GET /payments error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /payments status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Payments - AWS Billing Simulator</title>`,
		`href="/payments">Payments</a>`,
		"Due Invoices",
		"Payment History",
		"Payment Setup",
		closeResult.InvoiceObligation.InvoiceID,
		"due",
		"Invoice remittance",
		"Expired corporate card",
		"card expired",
		"Advance Pay reserve",
		"$3.50",
		`action="/payments/action"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /payments body missing %q: %s", want, body)
		}
	}

	obligationID := closeResult.InvoiceObligation.ID
	financePaymentPath := "/payments?viewer_role=finance"
	resp, err = client.Get(server.URL() + financePaymentPath)
	if err != nil {
		t.Fatalf("GET %s error = %v", financePaymentPath, err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d; body=%s", financePaymentPath, resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<option value="finance" selected>Finance</option>`,
		`href="` + invoicePathForIDWithViewer(closeResult.InvoiceObligation.InvoiceID, exportViewerFields{Role: "finance"}) + `"`,
		`name="viewer_role" value="finance"`,
		closeResult.InvoiceObligation.InvoiceID,
		"Payment Setup",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET %s body missing %q: %s", financePaymentPath, want, body)
		}
	}

	memberPaymentPath := "/payments?viewer_role=member-account&viewer_account_id=111122223333"
	resp, err = client.Get(server.URL() + memberPaymentPath)
	if err != nil {
		t.Fatalf("GET %s error = %v", memberPaymentPath, err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET %s status = %d, want %d; body=%s", memberPaymentPath, resp.StatusCode, http.StatusForbidden, body)
	}
	if !strings.Contains(body, "cannot manage payments") ||
		strings.Contains(body, closeResult.InvoiceObligation.InvoiceID) ||
		strings.Contains(body, `action="/payments/action"`) {
		t.Fatalf("GET %s did not block member payment workflow cleanly: %s", memberPaymentPath, body)
	}

	assertObligationStatus := func(want string) {
		t.Helper()
		var got string
		if err := db.QueryRowContext(ctx, `SELECT status FROM invoice_payment_states WHERE invoice_obligation_id = ?`, obligationID).Scan(&got); err != nil {
			t.Fatalf("read invoice payment status: %v", err)
		}
		if got != want {
			t.Fatalf("invoice payment status = %q, want %q", got, want)
		}
	}
	resp, err = client.PostForm(server.URL()+"/payments/action", url.Values{
		"viewer_role":           {"member-account"},
		"viewer_account_id":     {"111122223333"},
		"invoice_obligation_id": {obligationID},
		"action":                {"schedule"},
	})
	if err != nil {
		t.Fatalf("POST /payments/action member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /payments/action member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
	assertObligationStatus("due")

	resp, err = client.PostForm(server.URL()+"/payments/action", url.Values{
		"viewer_role":           {"management-account"},
		"viewer_account_id":     {"000000000000"},
		"invoice_obligation_id": {obligationID},
		"action":                {"schedule"},
	})
	if err != nil {
		t.Fatalf("POST /payments/action cross-payer viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /payments/action cross-payer viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
	assertObligationStatus("due")

	postPaymentAction := func(values url.Values) string {
		t.Helper()
		resp, err := client.PostForm(server.URL()+"/payments/action", values)
		if err != nil {
			t.Fatalf("POST /payments/action error = %v", err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /payments/action final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
		}
		return body
	}

	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"schedule"},
	})
	if !strings.Contains(body, "Scheduled payment for "+closeResult.InvoiceObligation.InvoiceID) ||
		!strings.Contains(body, "scheduled") {
		t.Fatalf("schedule payment response missing scheduled state: %s", body)
	}
	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"process"},
	})
	if !strings.Contains(body, "Started payment processing for "+closeResult.InvoiceObligation.InvoiceID) ||
		!strings.Contains(body, "processing") {
		t.Fatalf("process payment response missing processing state: %s", body)
	}
	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"fail"},
		"reason":                {"card expired"},
	})
	if !strings.Contains(body, "Recorded failed payment for "+closeResult.InvoiceObligation.InvoiceID) ||
		!strings.Contains(body, "failed") ||
		!strings.Contains(body, "card expired") {
		t.Fatalf("fail payment response missing failed reason: %s", body)
	}
	body = postPaymentAction(url.Values{
		"method_id": {failedMethod.ID},
		"action":    {"fix_method"},
	})
	if !strings.Contains(body, "Fixed payment method Expired corporate card") ||
		!strings.Contains(body, "active") {
		t.Fatalf("fix method response missing active method: %s", body)
	}

	var methodStatus, failureReason string
	if err := db.QueryRowContext(ctx, `SELECT status, failure_reason FROM payment_methods WHERE id = ?`, failedMethod.ID).Scan(&methodStatus, &failureReason); err != nil {
		t.Fatalf("read fixed method: %v", err)
	}
	if methodStatus != "active" || failureReason != "" {
		t.Fatalf("fixed method state = %q/%q, want active with no failure", methodStatus, failureReason)
	}

	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"mark_past_due"},
		"occurred_at":           {"2026-03-23"},
	})
	if !strings.Contains(body, "Marked "+closeResult.InvoiceObligation.InvoiceID+" past due") ||
		!strings.Contains(body, "past-due") {
		t.Fatalf("mark past-due response missing past-due state: %s", body)
	}
	partialMicros := int64(500_000)
	remainingMicros := closeResult.InvoiceObligation.AmountDueMicros - partialMicros
	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"collect"},
		"amount":                {formatMicrosDecimal(partialMicros)},
	})
	if !strings.Contains(body, "Collected payment for "+closeResult.InvoiceObligation.InvoiceID) ||
		!strings.Contains(body, "Partially Paid") ||
		!strings.Contains(body, "partially-paid") ||
		!strings.Contains(body, "past-due") ||
		!strings.Contains(body, `value="mark_due"`) ||
		!strings.Contains(body, formatUSDMicros(remainingMicros)) {
		t.Fatalf("partial past-due collect response missing partial and past-due state: %s", body)
	}

	var partialBillState, partialPaymentStatus string
	var partialAmountDue int64
	if err := db.QueryRowContext(
		ctx,
		`SELECT b.bill_state, ps.status, ps.amount_due_micros
		   FROM bills b
		   JOIN invoice_payment_states ps ON ps.invoice_obligation_id = ?
		  WHERE b.id = ?`,
		obligationID,
		closeResult.Bill.ID,
	).Scan(&partialBillState, &partialPaymentStatus, &partialAmountDue); err != nil {
		t.Fatalf("read partial payment state: %v", err)
	}
	if partialBillState != "past_due" || partialPaymentStatus != "partially_paid" || partialAmountDue != remainingMicros {
		t.Fatalf("partial payment state = bill %q payment %q due %d, want past_due/partially_paid/%d", partialBillState, partialPaymentStatus, partialAmountDue, remainingMicros)
	}

	resp, err = client.Get(server.URL() + "/bills")
	if err != nil {
		t.Fatalf("GET /bills after partial past-due payment error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills after partial past-due payment status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "past-due") || !strings.Contains(body, "partially-paid") {
		t.Fatalf("GET /bills after partial past-due payment missing past-due partial invoice state: %s", body)
	}

	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"mark_due"},
	})
	if !strings.Contains(body, "Marked "+closeResult.InvoiceObligation.InvoiceID+" due") ||
		!strings.Contains(body, "due") ||
		!strings.Contains(body, formatUSDMicros(remainingMicros)) {
		t.Fatalf("mark partially paid due response missing due state: %s", body)
	}
	var dueBillState, duePaymentStatus string
	var dueAmountDue, dueAmountPaid int64
	if err := db.QueryRowContext(
		ctx,
		`SELECT b.bill_state, ps.status, ps.amount_due_micros, ps.amount_paid_micros
		   FROM bills b
		   JOIN invoice_payment_states ps ON ps.invoice_obligation_id = ?
		  WHERE b.id = ?`,
		obligationID,
		closeResult.Bill.ID,
	).Scan(&dueBillState, &duePaymentStatus, &dueAmountDue, &dueAmountPaid); err != nil {
		t.Fatalf("read marked-due partial payment state: %v", err)
	}
	if dueBillState != "issued" || duePaymentStatus != "due" || dueAmountDue != remainingMicros || dueAmountPaid != partialMicros {
		t.Fatalf("marked-due partial payment state = bill %q payment %q due %d paid %d, want issued/due/%d/%d", dueBillState, duePaymentStatus, dueAmountDue, dueAmountPaid, remainingMicros, partialMicros)
	}

	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"process"},
	})
	if !strings.Contains(body, "processing") {
		t.Fatalf("retry after method fix response missing processing state: %s", body)
	}
	body = postPaymentAction(url.Values{
		"invoice_obligation_id": {obligationID},
		"action":                {"collect"},
		"amount":                {formatMicrosDecimal(remainingMicros)},
	})
	if !strings.Contains(body, "Collected payment for "+closeResult.InvoiceObligation.InvoiceID) ||
		!strings.Contains(body, "succeeded") ||
		!strings.Contains(body, "Payment History") {
		t.Fatalf("collect payment response missing succeeded history: %s", body)
	}

	var billState, paymentStatus string
	if err := db.QueryRowContext(ctx, `SELECT bill_state FROM bills WHERE id = ?`, closeResult.Bill.ID).Scan(&billState); err != nil {
		t.Fatalf("read bill state: %v", err)
	}
	if err := db.QueryRowContext(ctx, `SELECT status FROM invoice_payment_states WHERE invoice_obligation_id = ?`, obligationID).Scan(&paymentStatus); err != nil {
		t.Fatalf("read payment state: %v", err)
	}
	if billState != "paid" || paymentStatus != "succeeded" {
		t.Fatalf("payment result = bill %q payment %q, want paid/succeeded", billState, paymentStatus)
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

func TestCostExplorerUIRequiresWorkspace(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/cost-explorer")
	if err != nil {
		t.Fatalf("GET /cost-explorer without workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Cost Explorer - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/cost-explorer">Cost Explorer</a>`,
		"Workspace Required",
		`href="/workspaces"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer without workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/cost-explorer/reports/save", url.Values{"report_name": {"Spend"}})
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/save without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /cost-explorer/reports/save without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before saving Cost Explorer reports.") {
		t.Fatalf("POST /cost-explorer/reports/save without workspace missing workspace message: %s", body)
	}
}

func TestScenariosUIRequiresWorkspace(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/scenarios")
	if err != nil {
		t.Fatalf("GET /scenarios without workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Scenarios - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/scenarios">Scenarios</a>`,
		"Workspace Required",
		`href="/workspaces"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /scenarios without workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.Get(server.URL + "/scenarios/editor")
	if err != nil {
		t.Fatalf("GET /scenarios/editor without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios/editor without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Scenario Editor - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/scenarios">Scenarios</a>`,
		"Workspace Required",
		`href="/workspaces"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /scenarios/editor without workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/scenarios/editor/validate", url.Values{"scenario_document": {"name: Draft"}})
	if err != nil {
		t.Fatalf("POST /scenarios/editor/validate without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /scenarios/editor/validate without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before validating scenario drafts.") {
		t.Fatalf("POST /scenarios/editor/validate without workspace missing workspace message: %s", body)
	}

	resp, err = client.PostForm(server.URL+"/scenarios/launch", url.Values{"scenario_key": {"first-consolidated-bill"}})
	if err != nil {
		t.Fatalf("POST /scenarios/launch without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /scenarios/launch without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before launching scenarios.") {
		t.Fatalf("POST /scenarios/launch without workspace missing workspace message: %s", body)
	}

	for _, action := range []struct {
		path string
		form url.Values
		want string
	}{
		{
			path: "/scenarios/reset",
			form: url.Values{"scenario_key": {"first-consolidated-bill"}},
			want: "Open a workspace before resetting scenarios.",
		},
		{
			path: "/scenarios/clone",
			form: url.Values{"clone_workspace_path": {filepath.Join(t.TempDir(), "clone")}},
			want: "Open a workspace before cloning scenarios.",
		},
		{
			path: "/scenarios/archive",
			form: url.Values{"scenario_run_id": {"run_missing"}},
			want: "Open a workspace before archiving scenarios.",
		},
	} {
		resp, err = client.PostForm(server.URL+action.path, action.form)
		if err != nil {
			t.Fatalf("POST %s without workspace error = %v", action.path, err)
		}
		body = readResponseBody(t, resp)
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("POST %s without workspace status = %d, want %d; body=%s", action.path, resp.StatusCode, http.StatusServiceUnavailable, body)
		}
		if !strings.Contains(body, action.want) {
			t.Fatalf("POST %s without workspace missing %q: %s", action.path, action.want, body)
		}
	}
}

func TestScenarioEditorValidationPreviewWorksInFreshWorkspace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "scenario-editor-workspace")
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
	client := http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(server.URL() + "/scenarios/editor")
	if err != nil {
		t.Fatalf("GET /scenarios/editor error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios/editor status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Scenario Editor - AWS Billing Simulator</title>`,
		`action="/scenarios/editor/validate"`,
		"Scenario YAML",
		"Validation Preview",
		"name: Draft scenario",
		"Validate Draft",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /scenarios/editor body missing %q: %s", want, body)
		}
	}

	validDraft := `
name: Browser-authored YAML scenario
clock:
  start: 2026-03-01
organization_template: anycompany-retail
events:
  - id: create-browser-web
    day: 1
    action: create_resource
    account: Storefront Prod
    service: Amazon EC2
    resource: browser-web
    resource_type: ec2_instance
    region: us-east-1
    tags:
      app: storefront
  - id: browser-web-hours
    day: 2
    action: add_usage
    account: Storefront Prod
    service: Amazon EC2
    resource: browser-web
    amount_hours: 12
checks:
  - type: saved_report_exists
    report_name: Browser spend review
`
	resp, err = client.PostForm(server.URL()+"/scenarios/editor/validate", url.Values{
		"scenario_document": {validDraft},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/editor/validate valid draft error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/editor/validate valid draft status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Scenario draft is valid.",
		"Valid",
		"Browser-authored YAML scenario",
		"2026-03-01",
		"2 events",
		"1 check",
		"create-browser-web",
		"browser-web-hours",
		"add_usage",
		"Day 2",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /scenarios/editor/validate valid body missing %q: %s", want, body)
		}
	}

	invalidDraft := `
name: ""
clock:
  start: March 2026
organization_template: anycompany-retail
events:
  - id: missing-quantity
    day: 1
    action: add_usage
    account: Storefront Prod
    service: Amazon EC2
`
	resp, err = client.PostForm(server.URL()+"/scenarios/editor/validate", url.Values{
		"scenario_document": {invalidDraft},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/editor/validate invalid draft error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/editor/validate invalid draft status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Invalid",
		"name is required",
		"clock.start must use YYYY-MM-DD",
		"events[0] must include amount_gb, amount_hours, quantity, or quantity_micros",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /scenarios/editor/validate invalid body missing %q: %s", want, body)
		}
	}

	var runCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_runs`).Scan(&runCount); err != nil {
		t.Fatalf("count scenario runs after editor validation: %v", err)
	}
	if runCount != 0 {
		t.Fatalf("scenario run count after editor validation = %d, want 0", runCount)
	}
}

func TestScenarioFeedbackReportUsesPersistedLearnerEvidence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "scenario-feedback-workspace")
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
	runID := "scenario-run-feedback"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO scenario_runs (
			id,
			definition_name,
			organization_template,
			random_seed,
			status,
			clock_start,
			current_event_id,
			events_total,
			events_succeeded,
			resources_created,
			usage_events_created,
			metering_records_created,
			bill_line_items_created,
			bills_issued,
			started_at,
			completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		runID,
		"Feedback Fixture",
		persistence.AnyCompanyRetailTemplateKey,
		7,
		"succeeded",
		"2026-03-01",
		"meter-march",
		2,
		2,
		1,
		3,
		3,
		5,
		1,
		"2026-03-01T00:00:00Z",
		"2026-04-01T00:00:00Z",
	); err != nil {
		t.Fatalf("insert scenario run: %v", err)
	}
	progressRepo := persistence.NewScenarioLearnerProgressRepository(db)
	if _, err := progressRepo.StartRun(ctx, persistence.ScenarioLearnerProgressStartRequest{
		ScenarioRunID:    runID,
		DefinitionName:   "Feedback Fixture",
		Objective:        "Investigate billing evidence",
		CurrentObjective: "Run scenario actions",
		ActionsTotal:     2,
		ChecksTotal:      1,
		StartedAt:        "2026-03-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("StartRun() error = %v", err)
	}
	if _, err := progressRepo.RecordAction(ctx, persistence.ScenarioLearnerActionRecordRequest{
		ScenarioRunID:  runID,
		ActionID:       "meter-march",
		ActionSequence: 1,
		ActionType:     "run_daily_metering",
		ActionStatus:   persistence.ScenarioLearnerActionStatusCompleted,
		CompletedAt:    "2026-03-31T00:00:00Z",
		Evidence:       "metering_records=3 bill_line_items=5",
	}); err != nil {
		t.Fatalf("RecordAction() error = %v", err)
	}
	if _, err := progressRepo.RecordAction(ctx, persistence.ScenarioLearnerActionRecordRequest{
		ScenarioRunID:  runID,
		ActionID:       "close-march",
		ActionSequence: 2,
		ActionType:     "close_billing_period",
		ActionStatus:   persistence.ScenarioLearnerActionStatusCompleted,
		CompletedAt:    "2026-04-01T00:00:00Z",
		Evidence:       "bill=bill-feedback",
	}); err != nil {
		t.Fatalf("RecordAction(close) error = %v", err)
	}
	if _, err := progressRepo.CompleteRun(ctx, persistence.ScenarioLearnerRunCompleteRequest{
		ScenarioRunID:         runID,
		RunStatus:             "succeeded",
		CurrentObjectiveState: persistence.ScenarioProgressStateInProgress,
		CurrentObjective:      "Run scenario assessment checks",
		CompletedAt:           "2026-04-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CompleteRun() error = %v", err)
	}
	if _, err := progressRepo.RecordCheckResults(ctx, persistence.ScenarioLearnerCheckResultRecordRequest{
		ScenarioRunID: runID,
		EvaluatedAt:   "2026-04-01T01:00:00Z",
		Results: []persistence.ScenarioLearnerCheckResult{{
			CheckID:       "check-top-driver",
			CheckSequence: 1,
			CheckType:     "identifies_top_driver",
			Status:        "passed",
			Expected:      "Amazon EC2",
			Actual:        "Amazon EC2 cost_micros=1230000",
			Message:       "Amazon EC2 is the top cost driver",
		}},
	}); err != nil {
		t.Fatalf("RecordCheckResults() error = %v", err)
	}

	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(server.URL() + "/scenarios")
	if err != nil {
		t.Fatalf("GET /scenarios error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, scenarioFeedbackPath(runID)) {
		t.Fatalf("GET /scenarios body missing feedback report link: %s", body)
	}

	resp, err = client.Get(server.URL() + scenarioFeedbackPath(runID))
	if err != nil {
		t.Fatalf("GET /scenarios/feedback error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios/feedback status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Scenario Feedback - AWS Billing Simulator</title>`,
		"Learner Feedback",
		"Feedback Fixture",
		"This run applied 2 of 2 scenario events",
		"scenario_learner_actions",
		"Run Daily Metering",
		"Converted eligible usage events into metering records and estimated bill line items.",
		"metering_records, bill_line_items",
		"Estimated billing turns usage into metered and priced line items before month end.",
		"Identifies Top Driver",
		"Amazon EC2 cost_micros=1230000",
		"Cost Explorer-style grouping identifies the dominant service or usage driver in bill line items.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /scenarios/feedback body missing %q: %s", want, body)
		}
	}
}

func TestScenariosListingAndLaunchUIWorksInFreshWorkspace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "scenarios-ui-workspace")
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
	client := http.Client{Timeout: 3 * time.Second}

	resp, err := client.Get(server.URL() + "/scenarios")
	if err != nil {
		t.Fatalf("GET /scenarios error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Scenarios - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/scenarios">Scenarios</a>`,
		"Available Scenarios",
		"First consolidated bill",
		"Missing Tags",
		"Payment Failure",
		"Shared Networking allocation",
		"Forecast and Budget Alert",
		"Find the untagged data-transfer spike",
		"Objective",
		"Estimated Duration",
		"Phase 1",
		"Phase 2",
		`action="/scenarios/launch"`,
		`name="scenario_key" value="first-consolidated-bill"`,
		`name="scenario_key" value="missing-tags"`,
		`name="scenario_key" value="payment-failure"`,
		`name="scenario_key" value="shared-networking-allocation"`,
		`name="scenario_key" value="forecast-budget-alert"`,
		`name="scenario_key" value="untagged-data-transfer-spike"`,
		"Start Lab",
		"Recent Runs",
		"No scenario runs",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /scenarios body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL()+"/scenarios/launch", url.Values{
		"scenario_key": {"first-consolidated-bill"},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/launch error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/launch final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Launched First consolidated bill: 8/8 events succeeded, 1 bill issued",
		"Start New Run",
		"Resume in Bills",
		`action="/scenarios/reset"`,
		`action="/scenarios/clone"`,
		`action="/scenarios/archive"`,
		"Reset to Seed",
		"Clone Workspace",
		"Archive Review Bundle",
		"Feedback Report",
		`/scenarios/feedback?scenario_run_id=`,
		"Succeeded",
		"Completed",
		"8/8 actions",
		"8/8",
		"Recent Runs",
		"close-march",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /scenarios/launch body missing %q: %s", want, body)
		}
	}

	var runID, status, progressState string
	var eventsSucceeded, billsIssued int
	if err := db.QueryRowContext(ctx, `
		SELECT id, status, events_succeeded, bills_issued
		FROM scenario_runs
		WHERE definition_name = ?
	`, "First consolidated bill").Scan(&runID, &status, &eventsSucceeded, &billsIssued); err != nil {
		t.Fatalf("read launched scenario run: %v", err)
	}
	if status != "succeeded" || eventsSucceeded != 8 || billsIssued != 1 {
		t.Fatalf("scenario run audit = %q/%d/%d, want succeeded/8/1", status, eventsSucceeded, billsIssued)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT current_objective_state
		FROM scenario_learner_progress
		WHERE scenario_run_id = ?
	`, runID).Scan(&progressState); err != nil {
		t.Fatalf("read launched scenario learner progress: %v", err)
	}
	if progressState != persistence.ScenarioProgressStateCompleted {
		t.Fatalf("scenario learner progress state = %q, want completed", progressState)
	}

	resp, err = client.Get(server.URL() + scenarioFeedbackPath(runID))
	if err != nil {
		t.Fatalf("GET /scenarios/feedback packaged run error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /scenarios/feedback packaged run status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"First consolidated bill",
		"This run applied 8 of 8 scenario events",
		"Create Account",
		"Close Billing Period",
		"Final bills and invoices tie payer obligations back to immutable source line items.",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /scenarios/feedback packaged run body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL()+"/scenarios/archive", url.Values{
		"scenario_run_id": {runID},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/archive error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/archive final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Archived review bundle to") || !strings.Contains(body, "with 2 export files") {
		t.Fatalf("POST /scenarios/archive body missing archive confirmation: %s", body)
	}
	archiveDir := filepath.Join(cfg.WorkspacePath, "review-archives")
	archiveEntries, err := os.ReadDir(archiveDir)
	if err != nil {
		t.Fatalf("read archive directory: %v", err)
	}
	if len(archiveEntries) != 1 {
		t.Fatalf("archive entries = %d, want 1", len(archiveEntries))
	}
	archivePath := filepath.Join(archiveDir, archiveEntries[0].Name())
	archiveReader, err := zip.OpenReader(archivePath)
	if err != nil {
		t.Fatalf("open review archive: %v", err)
	}
	defer archiveReader.Close()
	archiveNames := map[string]bool{}
	var manifestJSON []byte
	curExportCount := 0
	reconciliationCount := 0
	for _, file := range archiveReader.File {
		archiveNames[file.Name] = true
		if file.Name == "manifest.json" {
			manifestReader, err := file.Open()
			if err != nil {
				t.Fatalf("open archive manifest: %v", err)
			}
			manifestJSON, err = io.ReadAll(manifestReader)
			if closeErr := manifestReader.Close(); closeErr != nil {
				t.Fatalf("close archive manifest: %v", closeErr)
			}
			if err != nil {
				t.Fatalf("read archive manifest: %v", err)
			}
		}
		if strings.HasSuffix(file.Name, "-cur.csv") {
			curExportCount++
		}
		if strings.HasSuffix(file.Name, "-reconciliation.json") {
			reconciliationCount++
		}
	}
	if !archiveNames["manifest.json"] || !archiveNames["workspace/simulator.db"] || !archiveNames["feedback-report.json"] || curExportCount != 1 || reconciliationCount != 1 {
		t.Fatalf("archive entries = %+v, want manifest, feedback report, database, one CUR CSV, and one reconciliation JSON", archiveNames)
	}
	if len(manifestJSON) == 0 {
		t.Fatal("archive manifest was empty")
	}
	if strings.Contains(string(manifestJSON), cfg.WorkspacePath) || strings.Contains(string(manifestJSON), root) {
		t.Fatalf("archive manifest leaked local workspace root %q: %s", root, manifestJSON)
	}
	var manifest map[string]any
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		t.Fatalf("parse archive manifest: %v; body=%s", err, manifestJSON)
	}
	if _, ok := manifest["workspace_path"]; ok {
		t.Fatalf("archive manifest included deprecated workspace_path field: %s", manifestJSON)
	}
	workspaceLabel, ok := manifest["workspace_label"].(string)
	if !ok || workspaceLabel == "" {
		t.Fatalf("archive manifest workspace_label = %#v, want non-empty string", manifest["workspace_label"])
	}
	if strings.Contains(workspaceLabel, string(os.PathSeparator)) {
		t.Fatalf("archive manifest workspace_label contains path separator: %q", workspaceLabel)
	}
	if manifest["database_path"] != "workspace/simulator.db" || manifest["feedback_report_path"] != "feedback-report.json" {
		t.Fatalf("archive manifest paths = database:%#v feedback:%#v, want stable archive-relative paths", manifest["database_path"], manifest["feedback_report_path"])
	}

	resp, err = client.PostForm(server.URL()+"/scenarios/reset", url.Values{
		"scenario_key": {"first-consolidated-bill"},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/reset error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/reset final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Reset First consolidated bill to seed: 8/8 events succeeded, 1 bill issued") {
		t.Fatalf("POST /scenarios/reset body missing reset confirmation: %s", body)
	}
	resetDB := server.workspace.DB()
	if resetDB == nil {
		t.Fatal("workspace database is nil after reset")
	}
	var runCount int
	if err := resetDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_runs WHERE definition_name = ?`, "First consolidated bill").Scan(&runCount); err != nil {
		t.Fatalf("count scenario reset runs: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("scenario run count after reset = %d, want 1", runCount)
	}
	if err := resetDB.QueryRowContext(ctx, `SELECT current_objective_state FROM scenario_learner_progress WHERE definition_name = ?`, "First consolidated bill").Scan(&progressState); err != nil {
		t.Fatalf("read scenario reset learner progress: %v", err)
	}
	if progressState != persistence.ScenarioProgressStateCompleted {
		t.Fatalf("scenario reset learner progress state = %q, want completed", progressState)
	}

	clonePath := filepath.Join(root, "scenario-clone-workspace")
	resp, err = client.PostForm(server.URL()+"/scenarios/clone", url.Values{
		"clone_workspace_path": {clonePath},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/clone error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/clone final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Cloned workspace to "+clonePath) || !strings.Contains(body, "Recent Runs") {
		t.Fatalf("POST /scenarios/clone body missing clone confirmation or scenario page: %s", body)
	}
	if got := server.workspace.CurrentPath(); got != clonePath {
		t.Fatalf("current workspace path after clone = %q, want %q", got, clonePath)
	}
	if _, err := os.Stat(persistence.WorkspaceDBPath(clonePath)); err != nil {
		t.Fatalf("cloned workspace database missing: %v", err)
	}
	clonedDB := server.workspace.DB()
	if clonedDB == nil {
		t.Fatal("workspace database is nil after clone")
	}
	if err := clonedDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_runs WHERE definition_name = ?`, "First consolidated bill").Scan(&runCount); err != nil {
		t.Fatalf("count cloned scenario runs: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("cloned scenario run count = %d, want 1", runCount)
	}
	if err := clonedDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM scenario_learner_progress WHERE definition_name = ?`, "First consolidated bill").Scan(&runCount); err != nil {
		t.Fatalf("count cloned scenario learner progress: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("cloned scenario learner progress count = %d, want 1", runCount)
	}
}

func TestScenarioLaunchReportsClosedPeriodConflictBeforePartialSetup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "closed-period-scenario-workspace")
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
	client := http.Client{Timeout: 3 * time.Second}

	resp, err := client.PostForm(server.URL()+"/scenarios/launch", url.Values{
		"scenario_key": {"first-consolidated-bill"},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/launch first consolidated bill error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /scenarios/launch first consolidated bill status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}

	resp, err = client.PostForm(server.URL()+"/scenarios/launch", url.Values{
		"scenario_key": {"payment-failure"},
	})
	if err != nil {
		t.Fatalf("POST /scenarios/launch payment failure error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /scenarios/launch payment failure status = %d, want %d; body=%s", resp.StatusCode, http.StatusBadRequest, body)
	}
	wantMessage := "Cannot price March 2026 usage because billing period 2026-03-01 to 2026-04-01 is already closed for payer 999988887777. Reset or clone the workspace before launching this scenario."
	if !strings.Contains(body, wantMessage) {
		t.Fatalf("POST /scenarios/launch body missing closed-period message: %s", body)
	}
	for _, leaked := range []string{"constraint failed", "1811", "billing period is closed for payer"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("POST /scenarios/launch body leaked raw trigger detail %q: %s", leaked, body)
		}
	}

	var runID, status, errorMessage, progressState string
	if err := db.QueryRowContext(ctx, `
		SELECT id, status, error_message
		FROM scenario_runs
		WHERE definition_name = ?
	`, "Payment Failure").Scan(&runID, &status, &errorMessage); err != nil {
		t.Fatalf("read failed payment scenario run: %v", err)
	}
	if status != "failed" || !strings.Contains(errorMessage, wantMessage) {
		t.Fatalf("failed payment run = %q/%q, want learner-facing failed run", status, errorMessage)
	}
	if err := db.QueryRowContext(ctx, `
		SELECT current_objective_state
		FROM scenario_learner_progress
		WHERE scenario_run_id = ?
	`, runID).Scan(&progressState); err != nil {
		t.Fatalf("read failed payment learner progress: %v", err)
	}
	if progressState != persistence.ScenarioProgressStateFailed {
		t.Fatalf("failed payment progress state = %q, want failed", progressState)
	}
	for _, table := range []string{"scenario_run_events", "resources", "usage_events"} {
		var count int
		if err := db.QueryRowContext(ctx, fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE scenario_run_id = ?`, table), runID).Scan(&count); err != nil {
			t.Fatalf("count %s for failed run: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s rows for failed run = %d, want none", table, count)
		}
	}
}

func TestCostExplorerReportBuilderWorkflow(t *testing.T) {
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

	resp, err := client.Get(server.URL + "/cost-explorer")
	if err != nil {
		t.Fatalf("GET /cost-explorer error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Report Definition",
		"Time and Metric",
		"Filters",
		"Group By",
		"Run Report",
		"Save Report",
		"No saved reports",
		`<a class="active" aria-current="page" href="/cost-explorer">Cost Explorer</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer body missing %q: %s", want, body)
		}
	}

	query := url.Values{
		"date_range_start": {"2026-02-01"},
		"date_range_end":   {"2026-03-01"},
		"granularity":      {"daily"},
		"metric":           {"unblended_cost"},
		"chart_type":       {"line"},
		"service_values":   {"Amazon EC2"},
		"tag_key":          {"app"},
		"tag_values":       {"storefront"},
		"group_1_type":     {"dimension"},
		"group_1_key":      {"service"},
		"group_2_type":     {"tag"},
		"group_2_key":      {"app"},
		"run":              {"1"},
	}
	resp, err = client.Get(server.URL + "/cost-explorer?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer filtered report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer filtered report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Report Results",
		`class="report-chart report-chart-line"`,
		`<polyline class="chart-line"`,
		`<circle class="chart-point"`,
		"Period Start",
		"Group 1",
		"Group 2",
		"Service=AmazonEC2",
		"tag:app=storefront",
		"$0.0832",
		"Unblended Cost",
		"/cost-explorer/results.csv?",
		"/cost-explorer/line-items?",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer filtered report missing %q: %s", want, body)
		}
	}

	resp, err = client.Get(server.URL + "/cost-explorer/results.csv?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer/results.csv error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/results.csv status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("GET /cost-explorer/results.csv content type = %q, want text/csv", contentType)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, "cost-explorer-report.csv") {
		t.Fatalf("GET /cost-explorer/results.csv content disposition = %q, want report filename", disposition)
	}
	for _, want := range []string{
		"date_range_start,date_range_end,granularity,metric,period_start",
		"2026-02-01,2026-03-01,daily,unblended_cost,2026-02-01,2026-02-02,dimension,service,AmazonEC2,tag,app,storefront,0.083200,2.000000,0.083200,1,USD",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer/results.csv body missing %q: %s", want, body)
		}
	}

	drilldownQuery := url.Values{}
	for key, values := range query {
		drilldownQuery[key] = values
	}
	drilldownQuery.Set("period_start", "2026-02-01")
	drilldownQuery.Set("period_end", "2026-02-02")
	drilldownQuery.Set("group_1_value", "AmazonEC2")
	drilldownQuery.Set("group_2_value", "storefront")
	resp, err = client.Get(server.URL + "/cost-explorer/line-items?" + drilldownQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer/line-items error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/line-items status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Cost Explorer Bill Line Items",
		"Source Line Items",
		"resource-cost-category-web",
		"Amazon EC2",
		"instance-hours:t3.medium",
		"$0.0832",
		"tag:app=storefront",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer/line-items body missing %q: %s", want, body)
		}
	}

	stackedQuery := url.Values{
		"date_range_start": {"2026-02-01"},
		"date_range_end":   {"2026-03-01"},
		"granularity":      {"monthly"},
		"metric":           {"unblended_cost"},
		"chart_type":       {"stacked_bar"},
		"group_1_type":     {"dimension"},
		"group_1_key":      {"service"},
		"run":              {"1"},
	}
	resp, err = client.Get(server.URL + "/cost-explorer?" + stackedQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer stacked report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer stacked report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`class="report-chart report-chart-stacked_bar"`,
		`<rect class="chart-bar"`,
		"Service=AmazonEC2",
		"Service=AmazonS3",
		"Max $0.0907",
		"2026-02-01",
		"2026-03-01",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer stacked report missing %q: %s", want, body)
		}
	}

	saveForm := url.Values{}
	for key, values := range query {
		saveForm[key] = values
	}
	saveForm.Set("report_name", "Storefront EC2 daily")
	saveForm.Set("description", "Browser-created report definition")
	saveForm.Set("owner_account_id", persistence.AnyCompanyRetailManagementAccountID)
	saveForm.Set("owner_role", "management-account")

	resp, err = client.PostForm(server.URL+"/cost-explorer/reports/save", saveForm)
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/save error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cost-explorer/reports/save final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Saved report Storefront EC2 daily",
		"Storefront EC2 daily",
		"Browser-created report definition",
		"Loaded",
		"line",
		"Saved Reports",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /cost-explorer/reports/save body missing %q: %s", want, body)
		}
	}

	report, err := persistence.NewSavedReportRepository(db).GetByName(ctx, persistence.AnyCompanyRetailManagementAccountID, "management-account", "Storefront EC2 daily")
	if err != nil {
		t.Fatalf("GetByName(saved report) error = %v", err)
	}
	if report.DateRangeStart != "2026-02-01" ||
		report.DateRangeEnd != "2026-03-01" ||
		report.Granularity != "daily" ||
		report.ChartType != "line" ||
		len(report.Groupings) != 2 ||
		report.Groupings[0] != (persistence.SavedReportGrouping{Type: "dimension", Key: "service"}) ||
		report.Groupings[1] != (persistence.SavedReportGrouping{Type: "tag", Key: "app"}) ||
		report.Filters["service"][0] != "Amazon EC2" ||
		report.Filters["tag:app"][0] != "storefront" {
		t.Fatalf("saved report definition = %+v, want browser report filters and groupings", report)
	}
}

func TestCURCSVExportFilenameIncludesRequestVariantDimensions(t *testing.T) {
	t.Parallel()

	base := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
	})
	if want := "cur-2026-02-01-2026-03-01-payer-999988887777-usage-all-accounts-status-all-statuses-limit-default.csv"; base != want {
		t.Fatalf("curCSVExportFilename(base) = %q, want %q", base, want)
	}

	filtered := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "111122223333",
		LineItemStatus:     "final",
		Limit:              25,
	})
	if want := "cur-2026-02-01-2026-03-01-payer-999988887777-usage-111122223333-status-final-limit-25.csv"; filtered != want {
		t.Fatalf("curCSVExportFilename(filtered) = %q, want %q", filtered, want)
	}
	if filtered == base {
		t.Fatal("curCSVExportFilename() collapsed filtered and unfiltered exports to the same filename")
	}

	memberScoped := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "111122223333",
		LineItemStatus:     "final",
		Visibility:         persistence.BillingVisibilityFilter{UsageAccountID: "111122223333"},
		Limit:              25,
	})
	if want := "cur-2026-02-01-2026-03-01-payer-999988887777-usage-111122223333-status-final-limit-25-visibility-usage-111122223333.csv"; memberScoped != want {
		t.Fatalf("curCSVExportFilename(member scoped) = %q, want %q", memberScoped, want)
	}
	if memberScoped == filtered {
		t.Fatal("curCSVExportFilename() collapsed member-scoped and management-scoped usage exports to the same filename")
	}
}

func TestQueryLabPageShowsCURCSVExamples(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/query-lab")
	if err != nil {
		t.Fatalf("GET /query-lab error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /query-lab status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Query Lab",
		`href="/exports"`,
		`href="/scenarios"`,
		"/path/to/export.csv",
		"Linked Account Totals",
		"Untagged Spend",
		"Top Usage Types",
		"Invoice Reconciliation",
		"Allocated Cost Comparison",
		"read_csv_auto",
		"json_extract_string(tags_json",
		"json_extract_string(cost_categories_json",
		"source_bill_id",
		"Shared Networking",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /query-lab body missing %q: %s", want, body)
		}
	}
	for _, column := range persistence.CURCSVExportColumns() {
		if !strings.Contains(body, column) {
			t.Fatalf("GET /query-lab body missing CUR CSV column %q: %s", column, body)
		}
	}

	resp, err = client.Get(server.URL + "/exports")
	if err != nil {
		t.Fatalf("GET /exports error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, `href="/query-lab"`) {
		t.Fatalf("GET /exports body missing query-lab action: %s", body)
	}
}

func TestCURCSVExportDownloadIncludesBillMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	workspacePath := t.TempDir()
	db, err := persistence.OpenWorkspace(ctx, workspacePath)
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
		ID:           "resource-cur-export-ui",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "CUR export UI web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
		Tags: map[string]string{
			"app": "storefront",
		},
	}); err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-cur-export-ui",
		ResourceID:          "resource-cur-export-ui",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: 2_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
	}
	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-03-01T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock close time) error = %v", err)
	}
	closeResult, err := persistence.NewMonthEndCloseRepository(db).ClosePreviousPeriod(ctx, persistence.MonthEndCloseRequest{
		PayerAccountID: persistence.AnyCompanyRetailManagementAccountID,
		InvoiceDueDays: 14,
	})
	if err != nil {
		t.Fatalf("ClosePreviousPeriod() error = %v", err)
	}
	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-03-02T09:30:00Z"); err != nil {
		t.Fatalf("Set(clock export time) error = %v", err)
	}

	server := httptest.NewServer(newWorkspaceMux(&workspaceSession{
		db:   db,
		path: workspacePath,
	}))
	t.Cleanup(server.Close)
	client := server.Client()

	query := url.Values{
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"payer_account_id":     {persistence.AnyCompanyRetailManagementAccountID},
	}
	exportFilename := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
	})
	exportRepo := persistence.NewExportFileRepository(db, workspacePath)
	assertExportNotStored := func(filename string) {
		t.Helper()
		if _, err := exportRepo.GetByFilename(ctx, filename); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("GetByFilename(%s) after direct GET error = %v, want sql.ErrNoRows", filename, err)
		}
		if _, err := os.Stat(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), filename)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Stat(%s) after direct GET error = %v, want missing file", filename, err)
		}
	}
	resp, err := client.Get(server.URL + "/exports/cur.csv?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /exports/cur.csv error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/cur.csv status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("GET /exports/cur.csv content type = %q, want text/csv", contentType)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, exportFilename) {
		t.Fatalf("GET /exports/cur.csv content disposition = %q, want CUR filename", disposition)
	}
	if storedFilename := resp.Header.Get("X-Simulator-Export-Filename"); storedFilename != "" {
		t.Fatalf("GET /exports/cur.csv stored filename header = %q, want no persisted export header", storedFilename)
	}

	records, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("read CUR CSV response: %v\n%s", err, body)
	}
	initialCSVBody := body
	if len(records) != 3 {
		t.Fatalf("CUR CSV response records = %d (%+v), want header plus usage and support rows", len(records), records)
	}
	if got := strings.Join(records[0][:3], ","); got != "export_generated_at,source_bill_id,line_item_id" {
		t.Fatalf("CUR CSV header prefix = %q, want metadata then line_item_id", got)
	}
	assertExportNotStored(exportFilename)
	checksum := sha256.Sum256([]byte(body))
	wantChecksum := hex.EncodeToString(checksum[:])
	if got := resp.Header.Get("X-Simulator-Export-Checksum"); got != "" {
		t.Fatalf("GET /exports/cur.csv checksum header = %q, want no persisted export checksum", got)
	}
	resp, err = client.PostForm(server.URL+"/exports/generate-cur", query)
	if err != nil {
		t.Fatalf("POST /exports/generate-cur error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/generate-cur final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Path; got != "/exports" {
		t.Fatalf("POST /exports/generate-cur final path = %q, want /exports", got)
	}
	if !strings.Contains(body, "Generated "+exportFilename+" from 2 source rows") {
		t.Fatalf("POST /exports/generate-cur body missing flash: %s", body)
	}
	exportContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), exportFilename))
	if err != nil {
		t.Fatalf("read stored CUR CSV export: %v", err)
	}
	if string(exportContent) != initialCSVBody {
		t.Fatalf("stored CUR CSV export differs from direct response:\nfile=%s\nbody=%s", exportContent, initialCSVBody)
	}
	exportRecord, err := exportRepo.GetByFilename(ctx, exportFilename)
	if err != nil {
		t.Fatalf("GetByFilename(CUR export) error = %v", err)
	}
	if exportRecord.ExportType != persistence.ExportFileTypeCURCSV ||
		exportRecord.BillingPeriodStart != "2026-02-01" ||
		exportRecord.BillingPeriodEnd != "2026-03-01" ||
		exportRecord.PayerAccountID != persistence.AnyCompanyRetailManagementAccountID ||
		exportRecord.UsageAccountID != "" ||
		exportRecord.SizeBytes != int64(len(initialCSVBody)) ||
		exportRecord.ChecksumSHA256 != wantChecksum ||
		exportRecord.GenerationParameters["generated_at"] != "2026-03-02T09:30:00Z" ||
		exportRecord.GenerationParameters["source_bill_id"] != closeResult.Bill.ID ||
		exportRecord.GenerationParameters["rows_written"] != "2" {
		t.Fatalf("stored CUR export metadata = %+v, want response metadata", exportRecord)
	}

	filteredQuery := url.Values{
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"payer_account_id":     {persistence.AnyCompanyRetailManagementAccountID},
		"usage_account_id":     {"111122223333"},
		"line_item_status":     {"final"},
		"limit":                {"1"},
	}
	filteredExportFilename := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "111122223333",
		LineItemStatus:     "final",
		Limit:              1,
	})
	resp, err = client.Get(server.URL + "/exports/cur.csv?" + filteredQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/cur.csv filtered error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/cur.csv filtered status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	filteredCSVBody := body
	if storedFilename := resp.Header.Get("X-Simulator-Export-Filename"); storedFilename != "" {
		t.Fatalf("GET /exports/cur.csv filtered stored filename header = %q, want no persisted export header", storedFilename)
	}
	if filteredExportFilename == exportFilename {
		t.Fatalf("filtered export filename = base filename %q, want distinct request variants", filteredExportFilename)
	}
	filteredRecords, err := csv.NewReader(strings.NewReader(filteredCSVBody)).ReadAll()
	if err != nil {
		t.Fatalf("read filtered CUR CSV response: %v\n%s", err, filteredCSVBody)
	}
	if len(filteredRecords) != 2 {
		t.Fatalf("filtered CUR CSV records = %d (%+v), want header plus one usage row", len(filteredRecords), filteredRecords)
	}
	if filteredCSVBody == initialCSVBody {
		t.Fatalf("filtered CUR CSV body matched all-account body; filename variants should represent different content")
	}
	assertExportNotStored(filteredExportFilename)
	filteredChecksum := sha256.Sum256([]byte(filteredCSVBody))
	wantFilteredChecksum := hex.EncodeToString(filteredChecksum[:])
	resp, err = client.PostForm(server.URL+"/exports/generate-cur", filteredQuery)
	if err != nil {
		t.Fatalf("POST /exports/generate-cur filtered error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/generate-cur filtered final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Generated "+filteredExportFilename+" from 1 source rows") {
		t.Fatalf("POST /exports/generate-cur filtered body missing flash: %s", body)
	}
	filteredExportContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), filteredExportFilename))
	if err != nil {
		t.Fatalf("read filtered stored CUR CSV export: %v", err)
	}
	if string(filteredExportContent) != filteredCSVBody {
		t.Fatalf("filtered stored CUR CSV export differs from response:\nfile=%s\nbody=%s", filteredExportContent, filteredCSVBody)
	}
	filteredExportRecord, err := exportRepo.GetByFilename(ctx, filteredExportFilename)
	if err != nil {
		t.Fatalf("GetByFilename(filtered CUR export) error = %v", err)
	}
	if filteredExportRecord.ExportType != persistence.ExportFileTypeCURCSV ||
		filteredExportRecord.BillingPeriodStart != "2026-02-01" ||
		filteredExportRecord.BillingPeriodEnd != "2026-03-01" ||
		filteredExportRecord.PayerAccountID != persistence.AnyCompanyRetailManagementAccountID ||
		filteredExportRecord.UsageAccountID != "111122223333" ||
		filteredExportRecord.SizeBytes != int64(len(filteredCSVBody)) ||
		filteredExportRecord.ChecksumSHA256 != wantFilteredChecksum ||
		filteredExportRecord.GenerationParameters["usage_account_id"] != "111122223333" ||
		filteredExportRecord.GenerationParameters["line_item_status"] != "final" ||
		filteredExportRecord.GenerationParameters["limit"] != "1" ||
		filteredExportRecord.GenerationParameters["rows_written"] != "1" {
		t.Fatalf("filtered CUR export metadata = %+v, want request-specific metadata", filteredExportRecord)
	}
	storedExports, err := exportRepo.List(ctx, persistence.ExportFileListRequest{
		ExportType:         persistence.ExportFileTypeCURCSV,
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
	})
	if err != nil {
		t.Fatalf("List(CUR export variants) error = %v", err)
	}
	if len(storedExports) != 2 {
		t.Fatalf("List(CUR export variants) returned %d rows: %+v, want base and filtered exports", len(storedExports), storedExports)
	}
	storedExportNames := map[string]bool{}
	for _, storedExport := range storedExports {
		storedExportNames[storedExport.Filename] = true
	}
	if !storedExportNames[exportFilename] || !storedExportNames[filteredExportFilename] {
		t.Fatalf("stored export filenames = %+v, want %q and %q", storedExportNames, exportFilename, filteredExportFilename)
	}
	for _, row := range []struct {
		filename  string
		createdAt string
		updatedAt string
	}{
		{filename: exportFilename, createdAt: "2000-01-01T00:00:00.000Z", updatedAt: "2000-01-01T00:00:00.000Z"},
		{filename: filteredExportFilename, createdAt: "2001-01-01T00:00:00.000Z", updatedAt: "2001-01-01T00:00:00.000Z"},
	} {
		if _, err := db.ExecContext(ctx, `UPDATE workspace_export_files SET created_at = ?, updated_at = ? WHERE filename = ?`, row.createdAt, row.updatedAt, row.filename); err != nil {
			t.Fatalf("set deterministic export timestamps for %s: %v", row.filename, err)
		}
	}

	resp, err = client.Get(server.URL + "/exports")
	if err != nil {
		t.Fatalf("GET /exports error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Generated Exports",
		"Generate CUR Export",
		"2 files",
		"recently updated first",
		exportFilename,
		filteredExportFilename,
		"Download",
		"Regenerate",
		"Reconcile",
		closeResult.Bill.ID,
		"2 rows",
		"1 rows",
		"usage 111122223333",
		"final",
		shortChecksum(wantChecksum),
		shortChecksum(wantFilteredChecksum),
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /exports body missing %q: %s", want, body)
		}
	}
	baseExportIndex := strings.Index(body, exportFilename)
	filteredExportIndex := strings.Index(body, filteredExportFilename)
	if baseExportIndex == -1 || filteredExportIndex == -1 || filteredExportIndex > baseExportIndex {
		t.Fatalf("GET /exports order put base index %d and filtered index %d, want filtered newer export before base export: %s", baseExportIndex, filteredExportIndex, body)
	}

	downloadPath := exportFileDownloadPath(exportFilename)
	resp, err = client.Get(server.URL + downloadPath)
	if err != nil {
		t.Fatalf("GET %s error = %v", downloadPath, err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d; body=%s", downloadPath, resp.StatusCode, http.StatusOK, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("GET %s content type = %q, want text/csv", downloadPath, contentType)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, exportFilename) {
		t.Fatalf("GET %s content disposition = %q, want stored filename", downloadPath, disposition)
	}
	if got := resp.Header.Get("X-Simulator-Export-Checksum"); got != wantChecksum {
		t.Fatalf("GET %s checksum header = %q, want %q", downloadPath, got, wantChecksum)
	}
	if body != initialCSVBody {
		t.Fatalf("GET %s body differs from generated CSV:\ndownload=%s\ninitial=%s", downloadPath, body, initialCSVBody)
	}

	filteredDownloadPath := exportFileDownloadPath(filteredExportFilename)
	resp, err = client.Get(server.URL + filteredDownloadPath)
	if err != nil {
		t.Fatalf("GET %s error = %v", filteredDownloadPath, err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want %d; body=%s", filteredDownloadPath, resp.StatusCode, http.StatusOK, body)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, filteredExportFilename) {
		t.Fatalf("GET %s content disposition = %q, want filtered stored filename", filteredDownloadPath, disposition)
	}
	if got := resp.Header.Get("X-Simulator-Export-Checksum"); got != wantFilteredChecksum {
		t.Fatalf("GET %s checksum header = %q, want %q", filteredDownloadPath, got, wantFilteredChecksum)
	}
	if body != filteredCSVBody {
		t.Fatalf("GET %s body differs from generated filtered CSV:\ndownload=%s\ninitial=%s", filteredDownloadPath, body, filteredCSVBody)
	}

	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-03-02T10:15:00Z"); err != nil {
		t.Fatalf("Set(clock regeneration time) error = %v", err)
	}
	resp, err = client.PostForm(server.URL+"/exports/regenerate", url.Values{
		"filename": {exportFilename},
	})
	if err != nil {
		t.Fatalf("POST /exports/regenerate error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/regenerate final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Path; got != "/exports" {
		t.Fatalf("POST /exports/regenerate final path = %q, want /exports", got)
	}
	if !strings.Contains(body, "Regenerated "+exportFilename+" from 2 source rows") {
		t.Fatalf("POST /exports/regenerate body missing flash: %s", body)
	}
	exportRecord, err = persistence.NewExportFileRepository(db, workspacePath).GetByFilename(ctx, exportFilename)
	if err != nil {
		t.Fatalf("GetByFilename(regenerated CUR export) error = %v", err)
	}
	if exportRecord.GenerationParameters["generated_at"] != "2026-03-02T10:15:00Z" ||
		exportRecord.GenerationParameters["rows_written"] != "2" {
		t.Fatalf("regenerated CUR export metadata = %+v, want refreshed generation parameters", exportRecord)
	}
	regeneratedContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), exportFilename))
	if err != nil {
		t.Fatalf("read regenerated CUR CSV export: %v", err)
	}
	if !strings.Contains(string(regeneratedContent), "2026-03-02T10:15:00Z") {
		t.Fatalf("regenerated CUR CSV missing refreshed generated_at: %s", regeneratedContent)
	}
	resp, err = client.Get(server.URL + "/exports")
	if err != nil {
		t.Fatalf("GET /exports after regeneration error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports after regeneration status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	baseExportIndex = strings.Index(body, exportFilename)
	filteredExportIndex = strings.Index(body, filteredExportFilename)
	if baseExportIndex == -1 || filteredExportIndex == -1 || baseExportIndex > filteredExportIndex {
		t.Fatalf("GET /exports after regeneration put base index %d and filtered index %d, want regenerated base export first: %s", baseExportIndex, filteredExportIndex, body)
	}

	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-03-02T10:45:00Z"); err != nil {
		t.Fatalf("Set(clock filtered regeneration time) error = %v", err)
	}
	resp, err = client.PostForm(server.URL+"/exports/regenerate", url.Values{
		"filename": {filteredExportFilename},
	})
	if err != nil {
		t.Fatalf("POST /exports/regenerate filtered error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/regenerate filtered final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Regenerated "+filteredExportFilename+" from 1 source rows") {
		t.Fatalf("POST /exports/regenerate filtered body missing flash: %s", body)
	}
	filteredExportRecord, err = persistence.NewExportFileRepository(db, workspacePath).GetByFilename(ctx, filteredExportFilename)
	if err != nil {
		t.Fatalf("GetByFilename(regenerated filtered CUR export) error = %v", err)
	}
	if filteredExportRecord.GenerationParameters["generated_at"] != "2026-03-02T10:45:00Z" ||
		filteredExportRecord.GenerationParameters["rows_written"] != "1" ||
		filteredExportRecord.GenerationParameters["usage_account_id"] != "111122223333" ||
		filteredExportRecord.GenerationParameters["line_item_status"] != "final" ||
		filteredExportRecord.GenerationParameters["limit"] != "1" {
		t.Fatalf("regenerated filtered CUR export metadata = %+v, want preserved request dimensions and refreshed result metadata", filteredExportRecord)
	}
	filteredRegeneratedContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), filteredExportFilename))
	if err != nil {
		t.Fatalf("read regenerated filtered CUR CSV export: %v", err)
	}
	if !strings.Contains(string(filteredRegeneratedContent), "2026-03-02T10:45:00Z") {
		t.Fatalf("regenerated filtered CUR CSV missing refreshed generated_at: %s", filteredRegeneratedContent)
	}
	baseContentAfterFilteredRegeneration, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), exportFilename))
	if err != nil {
		t.Fatalf("read base CUR CSV export after filtered regeneration: %v", err)
	}
	if !strings.Contains(string(baseContentAfterFilteredRegeneration), "2026-03-02T10:15:00Z") ||
		strings.Contains(string(baseContentAfterFilteredRegeneration), "2026-03-02T10:45:00Z") {
		t.Fatalf("filtered regeneration overwrote base export content: %s", baseContentAfterFilteredRegeneration)
	}

	usage := requireCSVResponseRecord(t, records, "resource_id", "resource-cur-export-ui")
	for column, want := range map[string]string{
		"export_generated_at": "2026-03-02T09:30:00Z",
		"source_bill_id":      closeResult.Bill.ID,
		"payer_account_id":    persistence.AnyCompanyRetailManagementAccountID,
		"usage_account_id":    "111122223333",
		"usage_amount":        "2.000000",
		"unblended_cost":      "0.083200",
		"tags_json":           `{"app":"storefront"}`,
	} {
		if got := usage[csvResponseColumnIndex(t, records[0], column)]; got != want {
			t.Fatalf("CUR CSV usage column %s = %q, want %q in %v", column, got, want, usage)
		}
	}

	support := requireCSVResponseRecord(t, records, "service_code", "AWSSupport")
	if got := support[csvResponseColumnIndex(t, records[0], "source_bill_id")]; got != closeResult.Bill.ID {
		t.Fatalf("CUR CSV support source_bill_id = %q, want %q", got, closeResult.Bill.ID)
	}
	if got := support[csvResponseColumnIndex(t, records[0], "line_item_type")]; got != "Fee" {
		t.Fatalf("CUR CSV support line_item_type = %q, want Fee", got)
	}

	query.Set("line_item_status", "final")
	resp, err = client.Get(server.URL + "/exports/reconciliation?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /exports/reconciliation error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/reconciliation status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Export Reconciliation",
		"Bill and Invoice Comparison",
		"balanced",
		"CUR CSV",
		closeResult.Bill.ID,
		closeResult.InvoiceObligation.InvoiceID,
		"CUR-like CSV",
		"$1.0832",
		"$0.00",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /exports/reconciliation body missing %q: %s", want, body)
		}
	}

	query.Set("usage_account_id", "111122223333")
	resp, err = client.Get(server.URL + "/exports/reconciliation?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /exports/reconciliation filtered error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/reconciliation filtered status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"excluded-lines",
		"111122223333",
		"final",
		"$0.0832",
		"$1.00",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /exports/reconciliation filtered body missing %q: %s", want, body)
		}
	}

	limitedReconciliationQuery := url.Values{}
	for key, values := range query {
		limitedReconciliationQuery[key] = values
	}
	limitedReconciliationQuery.Set("limit", "1")
	resp, err = client.Get(server.URL + "/exports/reconciliation?" + limitedReconciliationQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/reconciliation limited error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/reconciliation limited status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"excluded-lines",
		`href="/exports/cur.csv?`,
		"limit=1",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /exports/reconciliation limited body missing %q: %s", want, body)
		}
	}

	viewerOnlyReconciliationQuery := url.Values{
		"viewer_role":       {"member-account"},
		"viewer_account_id": {"111122223333"},
	}
	resp, err = client.Get(server.URL + "/exports/reconciliation?" + viewerOnlyReconciliationQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/reconciliation viewer-only filter error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET /exports/reconciliation viewer-only filter status = %d, want %d; body=%s", resp.StatusCode, http.StatusBadRequest, body)
	}
	for _, want := range []string{
		`href="/exports/reconciliation">Clear</a>`,
		`name="viewer_account_id" value="111122223333"`,
		`value="member-account" selected`,
		"CUR-like export reconciliation billing period start and end are required",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /exports/reconciliation viewer-only filter body missing %q: %s", want, body)
		}
	}

	memberQuery := url.Values{
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"payer_account_id":     {persistence.AnyCompanyRetailManagementAccountID},
		"line_item_status":     {"final"},
		"limit":                {"2"},
		"viewer_role":          {"member-account"},
		"viewer_account_id":    {"111122223333"},
	}
	memberExportFilename := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "111122223333",
		LineItemStatus:     "final",
		Visibility:         persistence.BillingVisibilityFilter{UsageAccountID: "111122223333"},
		Limit:              2,
	})
	resp, err = client.Get(server.URL + "/exports/cur.csv?" + memberQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/cur.csv member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/cur.csv member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if storedFilename := resp.Header.Get("X-Simulator-Export-Filename"); storedFilename != "" {
		t.Fatalf("GET /exports/cur.csv member viewer stored filename = %q, want no persisted export header", storedFilename)
	}
	memberCSVBody := body
	memberRecords, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("read member CUR CSV response: %v\n%s", err, body)
	}
	if len(memberRecords) != 2 {
		t.Fatalf("member CUR CSV records = %d (%+v), want header plus own usage row", len(memberRecords), memberRecords)
	}
	memberUsage := requireCSVResponseRecord(t, memberRecords, "usage_account_id", "111122223333")
	if got := memberUsage[csvResponseColumnIndex(t, memberRecords[0], "source_bill_id")]; got != "" {
		t.Fatalf("member CUR CSV source_bill_id = %q, want payer document hidden", got)
	}
	if strings.Contains(body, "AWSSupport") || strings.Contains(body, "999988887777,999988887777") {
		t.Fatalf("member CUR CSV leaked payer-scoped support row: %s", body)
	}
	assertExportNotStored(memberExportFilename)
	resp, err = client.PostForm(server.URL+"/exports/generate-cur", memberQuery)
	if err != nil {
		t.Fatalf("POST /exports/generate-cur member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/generate-cur member viewer final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Query().Get("viewer_role"); got != "member-account" {
		t.Fatalf("POST /exports/generate-cur member viewer final viewer_role = %q, want preserved member-account", got)
	}
	if !strings.Contains(body, "Generated "+memberExportFilename+" from 1 source rows") ||
		strings.Contains(body, exportFilename) {
		t.Fatalf("POST /exports/generate-cur member viewer body = %s, want scoped flash and no all-account export", body)
	}
	memberExportContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), memberExportFilename))
	if err != nil {
		t.Fatalf("read member stored CUR CSV export: %v", err)
	}
	if string(memberExportContent) != memberCSVBody {
		t.Fatalf("member stored CUR CSV export differs from direct response:\nfile=%s\nbody=%s", memberExportContent, memberCSVBody)
	}
	memberRecord, err := exportRepo.GetByFilename(ctx, memberExportFilename)
	if err != nil {
		t.Fatalf("GetByFilename(member CUR export) error = %v", err)
	}
	if memberRecord.UsageAccountID != "111122223333" ||
		memberRecord.GenerationParameters["usage_account_id"] != "111122223333" ||
		memberRecord.GenerationParameters["visibility_scope"] != "usage-account" ||
		memberRecord.GenerationParameters["visibility_account_id"] != "111122223333" ||
		memberRecord.GenerationParameters["source_bill_id"] != "" ||
		memberRecord.GenerationParameters["rows_written"] != "1" {
		t.Fatalf("member CUR export metadata = %+v, want member-scoped export without payer bill ID", memberRecord)
	}

	managementSameShapeQuery := url.Values{}
	for key, values := range memberQuery {
		managementSameShapeQuery[key] = values
	}
	managementSameShapeQuery.Del("viewer_role")
	managementSameShapeQuery.Del("viewer_account_id")
	managementSameShapeQuery.Set("usage_account_id", "111122223333")
	managementSameShapeFilename := curCSVExportFilename(persistence.CURCSVExportRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		PayerAccountID:     persistence.AnyCompanyRetailManagementAccountID,
		UsageAccountID:     "111122223333",
		LineItemStatus:     "final",
		Limit:              2,
	})
	if managementSameShapeFilename == memberExportFilename {
		t.Fatalf("management and member stored CUR export filenames both = %q, want visibility-scoped variants", memberExportFilename)
	}
	resp, err = client.PostForm(server.URL+"/exports/generate-cur", managementSameShapeQuery)
	if err != nil {
		t.Fatalf("POST /exports/generate-cur matching management export error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/generate-cur matching management export status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Generated "+managementSameShapeFilename+" from 1 source rows") {
		t.Fatalf("POST /exports/generate-cur matching management export body missing flash: %s", body)
	}
	managementSameShapeContent, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), managementSameShapeFilename))
	if err != nil {
		t.Fatalf("read matching management CUR CSV export: %v", err)
	}
	if !strings.Contains(string(managementSameShapeContent), closeResult.Bill.ID) {
		t.Fatalf("matching management CUR CSV export missing payer bill ID metadata: %s", managementSameShapeContent)
	}
	memberExportContentAfterManagement, err := os.ReadFile(filepath.Join(persistence.WorkspaceExportsPath(workspacePath), memberExportFilename))
	if err != nil {
		t.Fatalf("read member CUR CSV export after matching management export: %v", err)
	}
	if string(memberExportContentAfterManagement) != memberCSVBody {
		t.Fatalf("matching management export overwrote member-scoped export:\nmember=%s\nwant=%s", memberExportContentAfterManagement, memberCSVBody)
	}

	crossAccountMemberQuery := url.Values{}
	for key, values := range memberQuery {
		crossAccountMemberQuery[key] = values
	}
	crossAccountMemberQuery.Set("usage_account_id", "444455556666")
	resp, err = client.Get(server.URL + "/exports/cur.csv?" + crossAccountMemberQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/cur.csv member cross-account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /exports/cur.csv member cross-account status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
	resp, err = client.PostForm(server.URL+"/exports/generate-cur", crossAccountMemberQuery)
	if err != nil {
		t.Fatalf("POST /exports/generate-cur member cross-account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /exports/generate-cur member cross-account status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}

	memberListQuery := url.Values{
		"viewer_role":       {"member-account"},
		"viewer_account_id": {"111122223333"},
	}
	resp, err = client.Get(server.URL + "/exports?" + memberListQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, memberExportFilename) {
		t.Fatalf("GET /exports member viewer missing own usage-account export: %s", body)
	}
	if strings.Contains(body, exportFilename) ||
		strings.Contains(body, filteredExportFilename) ||
		strings.Contains(body, managementSameShapeFilename) ||
		strings.Contains(body, "usage all accounts") {
		t.Fatalf("GET /exports member viewer leaked broader export: %s", body)
	}

	resp, err = client.Get(server.URL + exportFileDownloadPathWithViewer(exportFilename, exportViewerFields{Role: "member-account", AccountID: "111122223333"}))
	if err != nil {
		t.Fatalf("GET all-account export as member error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET all-account export as member status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
	resp, err = client.Get(server.URL + exportFileDownloadPathWithViewer(managementSameShapeFilename, exportViewerFields{Role: "member-account", AccountID: "111122223333"}))
	if err != nil {
		t.Fatalf("GET matching management export as member error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET matching management export as member status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
	resp, err = client.Get(server.URL + exportFileDownloadPathWithViewer(memberExportFilename, exportViewerFields{Role: "member-account", AccountID: "111122223333"}))
	if err != nil {
		t.Fatalf("GET member export as member error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET member export as member status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if strings.Contains(body, "AWSSupport") ||
		strings.Contains(body, closeResult.Bill.ID) ||
		!strings.Contains(body, "resource-cur-export-ui") {
		t.Fatalf("GET member export as member body = %s, want own usage row only", body)
	}

	resp, err = client.PostForm(server.URL+"/exports/regenerate", url.Values{
		"filename":          {exportFilename},
		"viewer_role":       {"member-account"},
		"viewer_account_id": {"111122223333"},
	})
	if err != nil {
		t.Fatalf("POST /exports/regenerate all-account as member error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("POST /exports/regenerate all-account as member status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
	resp, err = client.PostForm(server.URL+"/exports/regenerate", url.Values{
		"filename":          {memberExportFilename},
		"viewer_role":       {"member-account"},
		"viewer_account_id": {"111122223333"},
	})
	if err != nil {
		t.Fatalf("POST /exports/regenerate member export error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /exports/regenerate member export final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Query().Get("viewer_role"); got != "member-account" {
		t.Fatalf("POST /exports/regenerate member export final viewer_role = %q, want preserved member-account", got)
	}
	if !strings.Contains(body, "Regenerated "+memberExportFilename+" from 1 source rows") ||
		strings.Contains(body, exportFilename) {
		t.Fatalf("POST /exports/regenerate member export body = %s, want scoped flash and no all-account export", body)
	}

	memberReconciliationQuery := url.Values{}
	for key, values := range memberQuery {
		memberReconciliationQuery[key] = values
	}
	memberReconciliationQuery.Del("limit")
	resp, err = client.Get(server.URL + "/exports/reconciliation?" + memberReconciliationQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/reconciliation member viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /exports/reconciliation member viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"balanced",
		"111122223333",
		"$0.0832",
		"visible-line-items",
		"not-available",
		"viewer_role=member-account",
		`href="/exports/reconciliation">Clear</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /exports/reconciliation member viewer body missing %q: %s", want, body)
		}
	}
	for _, leaked := range []string{"$1.0832", "$1.00", "AWSSupport"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("GET /exports/reconciliation member viewer leaked %q: %s", leaked, body)
		}
	}

	resp, err = client.Get(server.URL + "/exports/reconciliation?" + crossAccountMemberQuery.Encode())
	if err != nil {
		t.Fatalf("GET /exports/reconciliation member cross-account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("GET /exports/reconciliation member cross-account status = %d, want %d; body=%s", resp.StatusCode, http.StatusForbidden, body)
	}
}

func TestCostExplorerSavedReportsAreScopedByOwnerContext(t *testing.T) {
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

	savedReportRepo := persistence.NewSavedReportRepository(db)
	managementReport, err := savedReportRepo.Create(ctx, persistence.SavedReportCreateRequest{
		ID:             "saved-report-ui-management-scope",
		Name:           "Management saved report",
		Description:    "Management owner only",
		OwnerAccountID: persistence.AnyCompanyRetailManagementAccountID,
		OwnerRole:      "management-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Groupings:      []persistence.SavedReportGrouping{{Type: "dimension", Key: "service"}},
	})
	if err != nil {
		t.Fatalf("Create(management saved report) error = %v", err)
	}
	memberReport, err := savedReportRepo.Create(ctx, persistence.SavedReportCreateRequest{
		ID:             "saved-report-ui-member-scope",
		Name:           "Member saved report",
		Description:    "Member owner only",
		OwnerAccountID: "111122223333",
		OwnerRole:      "member-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Groupings:      []persistence.SavedReportGrouping{{Type: "dimension", Key: "service"}},
	})
	if err != nil {
		t.Fatalf("Create(member saved report) error = %v", err)
	}

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	managementQuery := url.Values{
		"owner_account_id": {persistence.AnyCompanyRetailManagementAccountID},
		"owner_role":       {"management-account"},
	}
	resp, err := client.Get(server.URL + "/cost-explorer?" + managementQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer management owner error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer management owner status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Management saved report") || strings.Contains(body, "Member saved report") {
		t.Fatalf("management saved-report shelf body = %s, want only management report", body)
	}

	memberQuery := url.Values{
		"owner_account_id": {"111122223333"},
		"owner_role":       {"member-account"},
	}
	resp, err = client.Get(server.URL + "/cost-explorer?" + memberQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer member owner error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer member owner status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Member saved report") ||
		strings.Contains(body, "Management saved report") ||
		!strings.Contains(body, `name="owner_account_id" value="111122223333"`) ||
		!strings.Contains(body, `name="owner_role" value="member-account"`) {
		t.Fatalf("member saved-report shelf body = %s, want only member report and owner context fields", body)
	}

	memberLoadQuery := url.Values{
		"saved_report_id":  {memberReport.ID},
		"owner_account_id": {"111122223333"},
		"owner_role":       {"member-account"},
	}
	resp, err = client.Get(server.URL + "/cost-explorer?" + memberLoadQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer member saved report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer member saved report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Member saved report") ||
		!strings.Contains(body, "Loaded") ||
		strings.Contains(body, "Management saved report") {
		t.Fatalf("member saved-report load body = %s, want only loaded member report", body)
	}

	crossOwnerLoadQuery := url.Values{
		"saved_report_id":  {memberReport.ID},
		"owner_account_id": {persistence.AnyCompanyRetailManagementAccountID},
		"owner_role":       {"management-account"},
	}
	resp, err = client.Get(server.URL + "/cost-explorer?" + crossOwnerLoadQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer cross-owner saved report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET /cost-explorer cross-owner saved report status = %d, want %d; body=%s", resp.StatusCode, http.StatusBadRequest, body)
	}
	if strings.Contains(body, "Member saved report") {
		t.Fatalf("cross-owner saved-report load leaked member report: %s", body)
	}

	crossOwnerUpdate := url.Values{
		"saved_report_id":  {memberReport.ID},
		"report_name":      {"Member saved report takeover"},
		"owner_account_id": {persistence.AnyCompanyRetailManagementAccountID},
		"owner_role":       {"management-account"},
		"date_range_start": {"2026-02-01"},
		"date_range_end":   {"2026-03-01"},
		"granularity":      {"monthly"},
		"metric":           {"unblended_cost"},
		"chart_type":       {"table"},
		"group_1_type":     {"dimension"},
		"group_1_key":      {"service"},
	}
	resp, err = client.PostForm(server.URL+"/cost-explorer/reports/save", crossOwnerUpdate)
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/save cross-owner update error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /cost-explorer/reports/save cross-owner update status = %d, want %d; body=%s", resp.StatusCode, http.StatusBadRequest, body)
	}
	reloadedMemberReport, err := savedReportRepo.Get(ctx, memberReport.ID)
	if err != nil {
		t.Fatalf("Get(member report after cross-owner update) error = %v", err)
	}
	if reloadedMemberReport.Name != memberReport.Name || reloadedMemberReport.OwnerAccountID != memberReport.OwnerAccountID || reloadedMemberReport.OwnerRole != memberReport.OwnerRole {
		t.Fatalf("member report after cross-owner update = %+v, want unchanged %+v", reloadedMemberReport, memberReport)
	}

	resp, err = client.PostForm(server.URL+"/cost-explorer/reports/run", url.Values{
		"saved_report_id":  {memberReport.ID},
		"owner_account_id": {persistence.AnyCompanyRetailManagementAccountID},
		"owner_role":       {"management-account"},
	})
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/run cross-owner error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("POST /cost-explorer/reports/run cross-owner status = %d, want %d; body=%s", resp.StatusCode, http.StatusBadRequest, body)
	}
	reloadedMemberReport, err = savedReportRepo.Get(ctx, memberReport.ID)
	if err != nil {
		t.Fatalf("Get(member report after cross-owner run) error = %v", err)
	}
	if reloadedMemberReport.LastRunStatus != "never_run" || reloadedMemberReport.LastRunAt != "" {
		t.Fatalf("member report after cross-owner run = %+v, want no run metadata", reloadedMemberReport)
	}
	if managementReport.ID == memberReport.ID {
		t.Fatalf("test reports share ID: management=%q member=%q", managementReport.ID, memberReport.ID)
	}
}

func TestCostExplorerQueriesAreScopedByOwnerPolicy(t *testing.T) {
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

	memberQuery := url.Values{
		"owner_account_id": {"111122223333"},
		"owner_role":       {"member-account"},
		"date_range_start": {"2026-02-01"},
		"date_range_end":   {"2026-03-01"},
		"granularity":      {"monthly"},
		"metric":           {"unblended_cost"},
		"chart_type":       {"table"},
		"group_1_type":     {"dimension"},
		"group_1_key":      {"linked_account"},
		"run":              {"1"},
	}
	resp, err := client.Get(server.URL + "/cost-explorer?" + memberQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer member owner error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer member owner status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`name="owner_account_id" value="111122223333"`,
		`<option value="member-account" selected>Member</option>`,
		"Linked Account=111122223333",
		"$0.0416",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer member owner body missing %q: %s", want, body)
		}
	}
	for _, leaked := range []string{
		"Linked Account=222233334444",
		"$0.0491",
	} {
		if strings.Contains(body, leaked) {
			t.Fatalf("GET /cost-explorer member owner leaked %q: %s", leaked, body)
		}
	}

	resp, err = client.Get(server.URL + "/cost-explorer/results.csv?" + memberQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer/results.csv member owner error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/results.csv member owner status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"date_range_start,date_range_end,granularity,metric,period_start",
		"2026-02-01,2026-03-01,monthly,unblended_cost,2026-02-01,2026-03-01,dimension,linked_account,111122223333",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer/results.csv member owner body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "222233334444") {
		t.Fatalf("GET /cost-explorer/results.csv member owner leaked other account: %s", body)
	}

	drilldownQuery := url.Values{}
	for key, values := range memberQuery {
		drilldownQuery[key] = values
	}
	drilldownQuery.Set("period_start", "2026-02-01")
	drilldownQuery.Set("period_end", "2026-03-01")
	drilldownQuery.Set("group_1_value", "222233334444")
	resp, err = client.Get(server.URL + "/cost-explorer/line-items?" + drilldownQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer/line-items member owner cross-account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/line-items member owner cross-account status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if strings.Contains(body, "Filter bucket") || strings.Contains(body, "resource-filter-s3") || strings.Contains(body, "Amazon S3") {
		t.Fatalf("GET /cost-explorer/line-items member owner leaked cross-account line item: %s", body)
	}

	reportRepo := persistence.NewSavedReportRepository(db)
	report, err := reportRepo.Create(ctx, persistence.SavedReportCreateRequest{
		ID:             "saved-report-member-policy-scope",
		Name:           "Member policy scope",
		OwnerAccountID: "111122223333",
		OwnerRole:      "member-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Groupings:      []persistence.SavedReportGrouping{{Type: "dimension", Key: "linked_account"}},
	})
	if err != nil {
		t.Fatalf("Create(member policy scoped report) error = %v", err)
	}
	resp, err = client.PostForm(server.URL+"/cost-explorer/reports/run", url.Values{
		"saved_report_id":  {report.ID},
		"owner_account_id": {"111122223333"},
		"owner_role":       {"member-account"},
	})
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/run member owner error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cost-explorer/reports/run member owner status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	reloadedReport, err := reportRepo.Get(ctx, report.ID)
	if err != nil {
		t.Fatalf("Get(member policy scoped report after run) error = %v", err)
	}
	if reloadedReport.LastRunStatus != "succeeded" ||
		reloadedReport.LastRunRowCount != 1 ||
		reloadedReport.LastRunTotalUnblendedCostMicros != 41_600 {
		t.Fatalf("member report run metadata = %+v, want one scoped row totaling EC2 spend", reloadedReport)
	}
}

func TestBudgetsUIRequiresWorkspace(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/budgets")
	if err != nil {
		t.Fatalf("GET /budgets without workspace error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /budgets without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Budgets - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/budgets">Budgets</a>`,
		"Workspace Required",
		`href="/workspaces"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /budgets without workspace missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/budgets/create", url.Values{"name": {"Spend"}})
	if err != nil {
		t.Fatalf("POST /budgets/create without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /budgets/create without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before creating budgets.") {
		t.Fatalf("POST /budgets/create without workspace missing workspace message: %s", body)
	}

	resp, err = client.PostForm(server.URL+"/budgets/refresh", url.Values{})
	if err != nil {
		t.Fatalf("POST /budgets/refresh without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("POST /budgets/refresh without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusServiceUnavailable, body)
	}
	if !strings.Contains(body, "Open a workspace before refreshing budgets.") {
		t.Fatalf("POST /budgets/refresh without workspace missing workspace message: %s", body)
	}
}

func TestBudgetsPageCreatesAndEvaluatesBudget(t *testing.T) {
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
	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-02-11T00:00:00Z"); err != nil {
		t.Fatalf("Set simulator clock error = %v", err)
	}
	if _, err := persistence.NewResourceUsageRepository(db).RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                    "usage-budget-ui-scheduled",
		ResourceID:            "resource-cost-category-web",
		UsageType:             "instance-hours:t3.medium",
		Operation:             "RunInstances",
		UsageStartTime:        "2026-02-20T00:00:00Z",
		UsageEndTime:          "2026-02-20T02:00:00Z",
		UsageQuantityMicros:   2_000_000,
		UsageUnit:             "Hours",
		EventSource:           "scenario",
		ScenarioRunID:         "scenario-budget-ui",
		ScenarioEventID:       "future-scale-up",
		ScenarioEventSequence: 2,
	}); err != nil {
		t.Fatalf("RecordUsageEvent(future scenario) error = %v", err)
	}

	server := httptest.NewServer(newMux(db))
	t.Cleanup(server.Close)
	client := server.Client()

	resp, err := client.Get(server.URL + "/budgets")
	if err != nil {
		t.Fatalf("GET /budgets error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /budgets status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Budget Definition",
		"Month and Scope",
		"Thresholds",
		"Create Budget",
		"Refresh Forecasts and Alerts",
		"Alert Notifications",
		"Forecast Summaries",
		"No budget threshold checks",
		"No budget alert notifications",
		"No budget forecast summaries",
		`<a class="active" aria-current="page" href="/budgets">Budgets</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /budgets body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/budgets/create", url.Values{
		"name":                 {"Storefront Feb Budget"},
		"description":          {"Storefront account guardrail"},
		"billing_period_start": {"2026-02-01"},
		"billing_period_end":   {"2026-03-01"},
		"amount":               {"0.10"},
		"scope_type":           {persistence.BudgetScopeAccount},
		"scope_value":          {"111122223333"},
		"actual_threshold":     {"80"},
		"forecast_threshold":   {"400"},
	})
	if err != nil {
		t.Fatalf("POST /budgets/create error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /budgets/create final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Created budget Storefront Feb Budget",
		"Storefront Feb Budget",
		"Account 111122223333",
		"Actual",
		"Forecast",
		"80% / $0.08",
		"$0.0832",
		"83.2%",
		"Alert Notifications",
		"No budget alert notifications",
		"No budget forecast summaries",
		"Breached",
		"OK",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /budgets/create body missing %q: %s", want, body)
		}
	}

	budgets, err := persistence.NewBudgetRepository(db).ListBudgets(ctx, persistence.BudgetListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
		Status:             "active",
	})
	if err != nil {
		t.Fatalf("ListBudgets() error = %v", err)
	}
	if len(budgets) != 1 || len(budgets[0].Thresholds) != 2 {
		t.Fatalf("persisted budgets = %+v, want one budget with actual and forecast thresholds", budgets)
	}
	budgetRepo := persistence.NewBudgetRepository(db)
	forecasts, err := budgetRepo.ListForecastSummaries(ctx, persistence.BudgetForecastSummaryListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("ListForecastSummaries(after create) error = %v", err)
	}
	alerts, err := budgetRepo.ListAlertNotifications(ctx, persistence.BudgetAlertNotificationListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("ListAlertNotifications(after create) error = %v", err)
	}
	if len(forecasts) != 0 || len(alerts) != 0 {
		t.Fatalf("generated budget state after create redirect = forecasts %+v alerts %+v, want no refresh side effects", forecasts, alerts)
	}

	resp, err = client.PostForm(server.URL+"/budgets/refresh", url.Values{})
	if err != nil {
		t.Fatalf("POST /budgets/refresh error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /budgets/refresh final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Refreshed budget forecasts and alerts",
		"Storefront Feb Budget",
		"Forecast Summaries",
		"10/28",
		"$0.31616",
		"Scheduled Events",
		"Alert Notifications",
		"In-app",
		"actual threshold crossed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /budgets/refresh body missing %q: %s", want, body)
		}
	}

	alerts, err = budgetRepo.ListAlertNotifications(ctx, persistence.BudgetAlertNotificationListRequest{
		BillingPeriodStart: "2026-02-01",
		BillingPeriodEnd:   "2026-03-01",
	})
	if err != nil {
		t.Fatalf("ListAlertNotifications() error = %v", err)
	}
	if len(alerts) != 1 ||
		alerts[0].BudgetID != budgets[0].ID ||
		alerts[0].ThresholdType != persistence.BudgetThresholdTypeActual ||
		alerts[0].NotificationChannel != "in_app" {
		t.Fatalf("persisted alert notifications = %+v, want one in-app actual threshold alert", alerts)
	}

	beforeGET := readBudgetGeneratedStateFingerprint(t, ctx, db, "2026-02-01", "2026-03-01")
	time.Sleep(10 * time.Millisecond)
	for i := 0; i < 2; i++ {
		resp, err = client.Get(server.URL + "/budgets")
		if err != nil {
			t.Fatalf("GET /budgets idempotency check %d error = %v", i+1, err)
		}
		body = readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /budgets idempotency check %d status = %d, want %d; body=%s", i+1, resp.StatusCode, http.StatusOK, body)
		}
	}
	afterGET := readBudgetGeneratedStateFingerprint(t, ctx, db, "2026-02-01", "2026-03-01")
	if afterGET != beforeGET {
		t.Fatalf("GET /budgets changed generated budget state:\nbefore=%s\nafter=%s", beforeGET, afterGET)
	}
}

// TestCostExplorerReportUIFeatureWorksInFreshWorkspace keeps bd-1of.2 guarded through the browser-facing report builder, charts, saved reports, CSV, and drilldowns.
func TestCostExplorerReportUIFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "cost-explorer-report-feature-workspace")
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
	client := http.Client{Timeout: time.Second}

	resp, err := client.Get(server.URL() + "/cost-explorer")
	if err != nil {
		t.Fatalf("GET /cost-explorer error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Cost Explorer - AWS Billing Simulator</title>`,
		"Report Definition",
		"Time and Metric",
		"Filters",
		"Group By",
		"Run Report",
		"Save Report",
		"No saved reports",
		`<a class="active" aria-current="page" href="/cost-explorer">Cost Explorer</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer body missing %q: %s", want, body)
		}
	}

	createResource := func(name, accountID, appValue string) string {
		t.Helper()

		resp, err := client.PostForm(server.URL()+"/resources/create", url.Values{
			"account_id":     {accountID},
			"region_code":    {"us-east-1"},
			"service_preset": {"ec2_t3_medium"},
			"size":           {"t3.medium"},
			"resource_name":  {name},
			"status":         {"active"},
			"started_at":     {"2026-02-01T00:00"},
			"tags":           {"app=" + appValue + "\nowner=retail-finops"},
		})
		if err != nil {
			t.Fatalf("POST /resources/create %s error = %v", name, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /resources/create %s final status = %d, want %d; body=%s", name, resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, name) || !strings.Contains(body, "app="+appValue) {
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
		if !strings.Contains(body, "Generated "+days+" usage events") ||
			!strings.Contains(body, "instance-hours:t3.medium") ||
			!strings.Contains(body, "app=") {
			t.Fatalf("generator response for %s missing usage/tag snapshot: %s", resourceID, body)
		}
	}

	storefrontResourceID := createResource("Feature report storefront web", "111122223333", "storefront")
	paymentsResourceID := createResource("Feature report payments web", "444455556666", "payments")
	generateUsage(storefrontResourceID, "2")
	generateUsage(paymentsResourceID, "1")

	body = postClockAdvance(t, &client, server.URL(), "2", string(persistence.SimulatorClockAdvanceDays))
	if !strings.Contains(body, "Advanced clock to 2026-02-03T00:00:00Z") ||
		!strings.Contains(body, "daily metering created 3 metering records and 4 bill line items") ||
		!strings.Contains(body, "AWSSupport") {
		t.Fatalf("clock advance response missing Cost Explorer report line items: %s", body)
	}

	query := url.Values{
		"date_range_start": {"2026-02-01"},
		"date_range_end":   {"2026-03-01"},
		"granularity":      {"daily"},
		"metric":           {"unblended_cost"},
		"chart_type":       {"line"},
		"service_values":   {"Amazon EC2"},
		"tag_key":          {"app"},
		"tag_values":       {"storefront, payments"},
		"group_1_type":     {"tag"},
		"group_1_key":      {"app"},
		"run":              {"1"},
	}
	resp, err = client.Get(server.URL() + "/cost-explorer?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Report Results",
		`class="report-chart report-chart-line"`,
		`<polyline class="chart-line"`,
		`<circle class="chart-point"`,
		"tag:app=payments",
		"tag:app=storefront",
		"$0.9984",
		"Period Start",
		"Line Items",
		"/cost-explorer/results.csv?",
		"/cost-explorer/line-items?",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer report body missing %q: %s", want, body)
		}
	}

	resp, err = client.Get(server.URL() + "/cost-explorer/results.csv?" + query.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer/results.csv error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/results.csv status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("GET /cost-explorer/results.csv content type = %q, want text/csv", contentType)
	}
	for _, want := range []string{
		"date_range_start,date_range_end,granularity,metric,period_start",
		"tag,app,payments,,,,0.998400,24.000000,0.998400,1,USD",
		"tag,app,storefront,,,,0.998400,24.000000,0.998400,1,USD",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer/results.csv body missing %q: %s", want, body)
		}
	}

	drilldownQuery := url.Values{}
	for key, values := range query {
		drilldownQuery[key] = values
	}
	drilldownQuery.Set("period_start", "2026-02-01")
	drilldownQuery.Set("period_end", "2026-02-02")
	drilldownQuery.Set("group_1_value", "storefront")
	resp, err = client.Get(server.URL() + "/cost-explorer/line-items?" + drilldownQuery.Encode())
	if err != nil {
		t.Fatalf("GET /cost-explorer/line-items error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/line-items status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Cost Explorer Bill Line Items",
		"Source Line Items",
		storefrontResourceID,
		"Amazon EC2",
		"instance-hours:t3.medium",
		"$0.9984",
		"tag:app=storefront",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer/line-items body missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, paymentsResourceID) {
		t.Fatalf("GET /cost-explorer/line-items leaked payments resource into storefront drilldown: %s", body)
	}

	saveForm := url.Values{}
	for key, values := range query {
		saveForm[key] = values
	}
	saveForm.Set("report_name", "Daily App EC2 Spend")
	saveForm.Set("description", "Fresh-workspace report UI close-out")
	saveForm.Set("owner_account_id", persistence.AnyCompanyRetailManagementAccountID)
	saveForm.Set("owner_role", "management-account")

	resp, err = client.PostForm(server.URL()+"/cost-explorer/reports/save", saveForm)
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/save error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cost-explorer/reports/save final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Saved report Daily App EC2 Spend",
		"Daily App EC2 Spend",
		"Fresh-workspace report UI close-out",
		"Loaded",
		"line",
		"Saved Reports",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /cost-explorer/reports/save body missing %q: %s", want, body)
		}
	}

	savedReportRepo := persistence.NewSavedReportRepository(db)
	report, err := savedReportRepo.GetByName(ctx, persistence.AnyCompanyRetailManagementAccountID, "management-account", "Daily App EC2 Spend")
	if err != nil {
		t.Fatalf("GetByName(saved report) error = %v", err)
	}
	if report.DateRangeStart != "2026-02-01" ||
		report.DateRangeEnd != "2026-03-01" ||
		report.Granularity != "daily" ||
		report.ChartType != "line" ||
		len(report.Metrics) != 1 ||
		report.Metrics[0] != "unblended_cost" ||
		len(report.Groupings) != 1 ||
		report.Groupings[0] != (persistence.SavedReportGrouping{Type: "tag", Key: "app"}) ||
		report.Filters["service"][0] != "Amazon EC2" ||
		len(report.Filters["tag:app"]) != 2 {
		t.Fatalf("saved report definition = %+v, want daily app EC2 report definition", report)
	}

	resp, err = client.Get(server.URL() + "/cost-explorer?saved_report_id=" + url.QueryEscape(report.ID))
	if err != nil {
		t.Fatalf("GET /cost-explorer saved report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer saved report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Daily App EC2 Spend",
		"Loaded",
		`class="report-chart report-chart-line"`,
		"tag:app=payments",
		"tag:app=storefront",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer saved report body missing %q: %s", want, body)
		}
	}

	updateForm := url.Values{}
	for key, values := range saveForm {
		updateForm[key] = values
	}
	updateForm.Set("saved_report_id", report.ID)
	updateForm.Set("description", "Updated fresh-workspace report definition")
	updateForm.Set("metric", "usage_quantity")
	updateForm.Set("chart_type", "bar")

	resp, err = client.PostForm(server.URL()+"/cost-explorer/reports/save", updateForm)
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/save update error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cost-explorer/reports/save update final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Updated saved report Daily App EC2 Spend",
		"Updated fresh-workspace report definition",
		`class="report-chart report-chart-bar"`,
		`<rect class="chart-bar"`,
		"<strong>24</strong>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /cost-explorer/reports/save update body missing %q: %s", want, body)
		}
	}

	updatedReport, err := savedReportRepo.Get(ctx, report.ID)
	if err != nil {
		t.Fatalf("Get(updated saved report) error = %v", err)
	}
	if updatedReport.Description != "Updated fresh-workspace report definition" ||
		updatedReport.ChartType != "bar" ||
		len(updatedReport.Metrics) != 1 ||
		updatedReport.Metrics[0] != "usage_quantity" {
		t.Fatalf("updated saved report = %+v, want edited usage bar definition", updatedReport)
	}

	resp, err = client.Get(server.URL() + "/cost-explorer/results.csv?saved_report_id=" + url.QueryEscape(report.ID))
	if err != nil {
		t.Fatalf("GET /cost-explorer/results.csv saved report error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /cost-explorer/results.csv saved report status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if disposition := resp.Header.Get("Content-Disposition"); !strings.Contains(disposition, "Daily-App-EC2-Spend.csv") {
		t.Fatalf("GET /cost-explorer/results.csv saved report content disposition = %q, want saved report filename", disposition)
	}
	for _, want := range []string{
		"daily,usage_quantity",
		"tag,app,payments,,,,24.000000,24.000000,0.998400,1,USD",
		"tag,app,storefront,,,,24.000000,24.000000,0.998400,1,USD",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /cost-explorer/results.csv saved report body missing %q: %s", want, body)
		}
	}
}

func TestCostExplorerSavedReportRunRecordsLastRunMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	db, err := persistence.OpenWorkspace(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("OpenWorkspace() error = %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close(workspace) error = %v", err)
		}
	})
	if _, err := persistence.NewSimulatorClockRepository(db).Set(ctx, "2026-02-03T00:00:00Z"); err != nil {
		t.Fatalf("Set(clock) error = %v", err)
	}

	usageRepo := persistence.NewResourceUsageRepository(db)
	resource, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-saved-report-run",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Saved report run web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
		Tags:         map[string]string{"app": "storefront"},
	})
	if err != nil {
		t.Fatalf("CreateResource() error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-saved-report-run",
		ResourceID:          resource.ID,
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		RegionCode:          "us-east-1",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T02:00:00Z",
		UsageQuantityMicros: 2_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent() error = %v", err)
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

	saveForm := url.Values{
		"report_name":      {"Saved report run metadata"},
		"description":      {"Focused saved-report run test"},
		"owner_account_id": {persistence.AnyCompanyRetailManagementAccountID},
		"owner_role":       {"management-account"},
		"date_range_start": {"2026-02-01"},
		"date_range_end":   {"2026-03-01"},
		"granularity":      {"daily"},
		"metric":           {"unblended_cost"},
		"chart_type":       {"table"},
		"service_values":   {"Amazon EC2"},
		"tag_key":          {"app"},
		"tag_values":       {"storefront"},
		"group_1_type":     {"tag"},
		"group_1_key":      {"app"},
	}
	resp, err := client.PostForm(server.URL+"/cost-explorer/reports/save", saveForm)
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/save error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cost-explorer/reports/save status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Saved report run metadata") || !strings.Contains(body, "never_run") {
		t.Fatalf("saved report create response missing loaded never-run report: %s", body)
	}

	savedReportRepo := persistence.NewSavedReportRepository(db)
	report, err := savedReportRepo.GetByName(ctx, persistence.AnyCompanyRetailManagementAccountID, "management-account", "Saved report run metadata")
	if err != nil {
		t.Fatalf("GetByName(saved report) error = %v", err)
	}
	if report.LastRunStatus != "never_run" || report.LastRunAt != "" || report.LastRunRowCount != 0 || report.LastRunTotalUnblendedCostMicros != 0 {
		t.Fatalf("saved report after create = %+v, want no run metadata before explicit POST run", report)
	}

	resp, err = client.PostForm(server.URL+"/cost-explorer/reports/run", url.Values{
		"saved_report_id": {report.ID},
	})
	if err != nil {
		t.Fatalf("POST /cost-explorer/reports/run error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /cost-explorer/reports/run status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Ran saved report Saved report run metadata",
		"Saved report run metadata",
		"succeeded 2026-02-03T00:00:00Z",
		"tag:app=storefront",
		"$0.0832",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST /cost-explorer/reports/run body missing %q: %s", want, body)
		}
	}

	ranReport, err := savedReportRepo.Get(ctx, report.ID)
	if err != nil {
		t.Fatalf("Get(ran saved report) error = %v", err)
	}
	if ranReport.LastRunStatus != "succeeded" ||
		ranReport.LastRunAt != "2026-02-03T00:00:00Z" ||
		ranReport.LastRunRowCount != 1 ||
		ranReport.LastRunTotalUnblendedCostMicros != 83_200 ||
		ranReport.LastRunError != "" {
		t.Fatalf("saved report last-run metadata = %+v, want successful UI run summary", ranReport)
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
	client := http.Client{Timeout: time.Second}

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
	client := http.Client{Timeout: time.Second}

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
	client := http.Client{Timeout: time.Second}

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

// TestCostExplorerQueryEngineFeatureWorksInFreshWorkspace keeps bd-1of.1 guarded through the browser-facing billing setup and repository query surfaces.
func TestCostExplorerQueryEngineFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "cost-explorer-query-feature-workspace")
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
	client := http.Client{Timeout: time.Second}

	createResource := func(name, accountID, appValue string) string {
		t.Helper()

		resp, err := client.PostForm(server.URL()+"/resources/create", url.Values{
			"account_id":     {accountID},
			"region_code":    {"us-east-1"},
			"service_preset": {"ec2_t3_medium"},
			"size":           {"t3.medium"},
			"resource_name":  {name},
			"status":         {"active"},
			"started_at":     {"2026-02-01T00:00"},
			"tags":           {"app=" + appValue + "\nowner=retail-finops"},
		})
		if err != nil {
			t.Fatalf("POST /resources/create %s error = %v", name, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /resources/create %s final status = %d, want %d; body=%s", name, resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, name) || !strings.Contains(body, "app="+appValue) {
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
		if !strings.Contains(body, "Generated "+days+" usage events") ||
			!strings.Contains(body, "instance-hours:t3.medium") ||
			!strings.Contains(body, "app=") {
			t.Fatalf("generator response for %s missing usage/tag snapshot: %s", resourceID, body)
		}
	}

	storefrontResourceID := createResource("Feature explorer storefront web", "111122223333", "storefront")
	paymentsResourceID := createResource("Feature explorer payments web", "444455556666", "payments")
	generateUsage(storefrontResourceID, "2")
	generateUsage(paymentsResourceID, "1")

	body := postClockAdvance(t, &client, server.URL(), "2", string(persistence.SimulatorClockAdvanceDays))
	if !strings.Contains(body, "Advanced clock to 2026-02-03T00:00:00Z") ||
		!strings.Contains(body, "daily metering created 3 metering records and 4 bill line items") ||
		!strings.Contains(body, "AWSSupport") {
		t.Fatalf("clock advance response missing Cost Explorer feature line items: %s", body)
	}

	resp, err := client.PostForm(server.URL()+"/cost-categories/categories/create", url.Values{
		"name":          {"Product"},
		"default_value": {"Unmapped"},
		"description":   {"Cost Explorer query feature product grouping"},
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
			"tag_key":     {"app"},
			"description": {"Storefront application tag"},
		},
		{
			"category_id": {productID},
			"rule_order":  {"20"},
			"value":       {"Payments"},
			"dimension":   {persistence.CostCategoryRuleMatchTag},
			"operator":    {persistence.CostCategoryRuleOperatorIn},
			"values":      {"payments"},
			"tag_key":     {"app"},
			"description": {"Payments application tag"},
		},
		{
			"category_id": {productID},
			"rule_order":  {"30"},
			"value":       {"Shared Platform"},
			"dimension":   {persistence.CostCategoryRuleMatchService},
			"operator":    {persistence.CostCategoryRuleOperatorIn},
			"values":      {"AWSSupport"},
			"description": {"Support is a shared platform category"},
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

	costExplorerRepo := persistence.NewCostExplorerRepository(db)
	reportRequest := persistence.CostExplorerQueryRequest{
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Granularity:    "daily",
		Filters: map[string][]string{
			"service": {"Amazon EC2"},
			"tag:app": {"storefront"},
		},
		Groupings: []persistence.CostExplorerGrouping{
			{Type: "dimension", Key: "service"},
			{Type: "tag", Key: "app"},
		},
	}
	result, err := costExplorerRepo.Query(ctx, reportRequest)
	if err != nil {
		t.Fatalf("Query(storefront saved report request) error = %v", err)
	}
	if result.TotalLineItemCount != 2 ||
		result.TotalUsageQuantityMicros != 48_000_000 ||
		result.TotalUnblendedCostMicros != 1_996_800 ||
		len(result.Rows) != 2 {
		t.Fatalf("storefront query result = %+v, want two daily EC2 storefront rows totaling 1996800 micros", result)
	}
	for i, row := range result.Rows {
		wantDate := "2026-02-01"
		if i == 1 {
			wantDate = "2026-02-02"
		}
		if row.TimePeriodStart != wantDate ||
			row.UsageQuantityMicros != 24_000_000 ||
			row.UnblendedCostMicros != 998_400 ||
			row.LineItemCount != 1 ||
			len(row.GroupValues) != 2 ||
			row.GroupValues[0] != (persistence.CostExplorerGroupValue{Type: "dimension", Key: "service", Value: "AmazonEC2"}) ||
			row.GroupValues[1] != (persistence.CostExplorerGroupValue{Type: "tag", Key: "app", Value: "storefront"}) {
			t.Fatalf("storefront query row %d = %+v, want one daily EC2 storefront row", i, row)
		}
	}

	categoryResult, err := costExplorerRepo.Query(ctx, persistence.CostExplorerQueryRequest{
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Granularity:    "monthly",
		Filters: map[string][]string{
			"cost_category:Product": {"Storefront"},
		},
		Groupings: []persistence.CostExplorerGrouping{
			{Type: "cost_category", Key: "Product"},
			{Type: "dimension", Key: "linked_account"},
		},
	})
	if err != nil {
		t.Fatalf("Query(Product cost category) error = %v", err)
	}
	if categoryResult.TotalLineItemCount != 2 ||
		categoryResult.TotalUnblendedCostMicros != 1_996_800 ||
		len(categoryResult.Rows) != 1 {
		t.Fatalf("Product category query result = %+v, want Storefront EC2 rollup", categoryResult)
	}
	categoryRow := categoryResult.Rows[0]
	if categoryRow.TimePeriodStart != "2026-02-01" ||
		len(categoryRow.GroupValues) != 2 ||
		categoryRow.GroupValues[0] != (persistence.CostExplorerGroupValue{Type: "cost_category", Key: "Product", Value: "Storefront"}) ||
		categoryRow.GroupValues[1] != (persistence.CostExplorerGroupValue{Type: "dimension", Key: "linked_account", Value: "111122223333"}) {
		t.Fatalf("Product category query row = %+v, want Storefront linked-account grouping", categoryRow)
	}

	var monthlyLineItems int
	var monthlyUsageMicros, monthlyCostMicros int64
	if err := db.QueryRowContext(ctx, `
		SELECT line_item_count, usage_quantity_micros, unblended_cost_micros
		FROM monthly_account_service_summary
		WHERE billing_period_start = '2026-02-01'
		  AND billing_period_end = '2026-03-01'
		  AND usage_account_id = '111122223333'
		  AND service_code = 'AmazonEC2'
		  AND line_item_status = 'estimated'
	`).Scan(&monthlyLineItems, &monthlyUsageMicros, &monthlyCostMicros); err != nil {
		t.Fatalf("read Cost Explorer monthly account service summary: %v", err)
	}
	if monthlyLineItems != 2 || monthlyUsageMicros != 48_000_000 || monthlyCostMicros != 1_996_800 {
		t.Fatalf("monthly summary = lines %d usage %d cost %d, want storefront EC2 totals", monthlyLineItems, monthlyUsageMicros, monthlyCostMicros)
	}

	var categoryLineItems int
	var categoryCostMicros int64
	if err := db.QueryRowContext(ctx, `
		SELECT line_item_count, unblended_cost_micros
		FROM cost_category_summary
		WHERE billing_period_start = '2026-02-01'
		  AND billing_period_end = '2026-03-01'
		  AND cost_category_id = ?
		  AND assigned_value = 'Storefront'
	`, productID).Scan(&categoryLineItems, &categoryCostMicros); err != nil {
		t.Fatalf("read Cost Explorer cost category summary: %v", err)
	}
	if categoryLineItems != 2 || categoryCostMicros != 1_996_800 {
		t.Fatalf("cost category summary = lines %d cost %d, want Storefront summary totals", categoryLineItems, categoryCostMicros)
	}

	savedReportRepo := persistence.NewSavedReportRepository(db)
	savedReport, err := savedReportRepo.Create(ctx, persistence.SavedReportCreateRequest{
		ID:             "saved-report-cost-explorer-feature",
		Name:           "Daily storefront EC2 cost",
		Description:    "bd-1of.1 feature smoke report",
		OwnerAccountID: "999988887777",
		OwnerRole:      "management-account",
		DateRangeStart: reportRequest.DateRangeStart,
		DateRangeEnd:   reportRequest.DateRangeEnd,
		Granularity:    reportRequest.Granularity,
		Filters:        reportRequest.Filters,
		Groupings:      reportRequest.Groupings,
		Metrics:        []string{"unblended_cost", "usage_quantity"},
		ChartType:      "line",
	})
	if err != nil {
		t.Fatalf("Create(saved report) error = %v", err)
	}
	savedResult, err := costExplorerRepo.Query(ctx, persistence.CostExplorerQueryRequest{
		DateRangeStart: savedReport.DateRangeStart,
		DateRangeEnd:   savedReport.DateRangeEnd,
		Granularity:    savedReport.Granularity,
		Filters:        savedReport.Filters,
		Groupings:      savedReport.Groupings,
	})
	if err != nil {
		t.Fatalf("Query(saved report definition) error = %v", err)
	}
	if savedResult.TotalUnblendedCostMicros != result.TotalUnblendedCostMicros ||
		savedResult.TotalLineItemCount != result.TotalLineItemCount {
		t.Fatalf("saved report query = %+v, want same totals as direct query %+v", savedResult, result)
	}

	ranReport, err := savedReportRepo.RecordLastRun(ctx, persistence.SavedReportRunUpdate{
		ID:                       savedReport.ID,
		RunAt:                    "2026-02-03T00:00:00Z",
		Status:                   "succeeded",
		RowCount:                 len(savedResult.Rows),
		TotalUnblendedCostMicros: savedResult.TotalUnblendedCostMicros,
	})
	if err != nil {
		t.Fatalf("RecordLastRun(saved report) error = %v", err)
	}
	if ranReport.LastRunStatus != "succeeded" ||
		ranReport.LastRunRowCount != 2 ||
		ranReport.LastRunTotalUnblendedCostMicros != 1_996_800 ||
		ranReport.LastRunAt != "2026-02-03T00:00:00Z" {
		t.Fatalf("saved report last-run metadata = %+v, want successful query metadata", ranReport)
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
	client := http.Client{Timeout: time.Second}

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

func TestOrganizationUIRendersHierarchyAndBillingLinks(t *testing.T) {
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

	resp, err := server.Client().Get(server.URL + "/organization")
	if err != nil {
		t.Fatalf("GET /organization error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}

	for _, want := range []string{
		"<h1>Organization</h1>",
		"AnyCompany Retail",
		"Management account",
		"Root - ou_anycompany_root",
		"Security - ou_anycompany_security",
		"Workloads - ou_anycompany_workloads",
		"Storefront Prod",
		"Deprecated Prototype",
		"Cost Center",
		"storefront-team",
		"4100-storefront",
		"shared-networking",
		"9900-deprecated",
		`<span class="status status-suspended">Suspended</span>`,
		"13 accounts",
		"12 active, 1 suspended, 0 closed",
		"Account Detail",
		"Billing Role",
		"Simulator Clock",
		`name="effective_at" value="2026-02-01T00:00"`,
		`action="/organization/accounts/create"`,
		"Create Account",
		"Move Account",
		"Suspend Account",
		"Close Account",
		"Lifecycle History",
		"13 events",
		`href="/resources?account_id=111122223333"`,
		`href="/bills?payer_account_id=999988887777&amp;usage_account_id=111122223333&amp;viewer_account_id=111122223333&amp;viewer_role=member-account"`,
		`href="/bills?payer_account_id=999988887777&amp;viewer_account_id=999988887777&amp;viewer_role=management-account">Bills</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /organization body missing %q: %s", want, body)
		}
	}
}

func TestOrganizationUIAccountLifecycleWorkflow(t *testing.T) {
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

	resp, err := client.PostForm(server.URL+"/organization/accounts/create", url.Values{
		"organization_id": {persistence.AnyCompanyRetailOrganizationID},
		"account_id":      {"777788889997"},
		"parent_unit_id":  {"ou_anycompany_sandbox"},
		"account_name":    {"Partner Integration"},
		"account_email":   {"partner-integration@anycompany.example"},
		"effective_at":    {"2026-02-01T00:00"},
	})
	if err != nil {
		t.Fatalf("POST create account error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Created account Partner Integration") {
		t.Fatalf("POST create account status/body = %d/%s, want success flash", resp.StatusCode, body)
	}

	resp, err = client.PostForm(server.URL+"/organization/accounts/move", url.Values{
		"account_id":     {"777788889997"},
		"parent_unit_id": {"ou_anycompany_workloads"},
		"effective_at":   {"2026-02-05T00:00"},
	})
	if err != nil {
		t.Fatalf("POST move account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Moved Partner Integration to Root/Workloads") {
		t.Fatalf("POST move account status/body = %d/%s, want success flash", resp.StatusCode, body)
	}

	resp, err = client.PostForm(server.URL+"/organization/accounts/suspend", url.Values{
		"account_id":   {"777788889997"},
		"effective_at": {"2026-02-10T00:00"},
	})
	if err != nil {
		t.Fatalf("POST suspend account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Suspended Partner Integration") {
		t.Fatalf("POST suspend account status/body = %d/%s, want success flash", resp.StatusCode, body)
	}

	resp, err = client.PostForm(server.URL+"/organization/accounts/close", url.Values{
		"account_id":   {"777788889997"},
		"effective_at": {"2026-02-15T00:00"},
	})
	if err != nil {
		t.Fatalf("POST close account error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST close account status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		"Closed Partner Integration",
		"14 accounts",
		"12 active, 1 suspended, 1 closed",
		"Partner Integration",
		"Root/Workloads",
		`<span class="status status-closed">Closed</span>`,
		"17 events",
		"Suspended -&gt; Closed",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("POST close account body missing %q: %s", want, body)
		}
	}

	repo := persistence.NewOrganizationRepository(db)
	account, err := repo.GetAccount(ctx, "777788889997")
	if err != nil {
		t.Fatalf("GetAccount(created) error = %v", err)
	}
	if account.Status != persistence.AccountStatusClosed ||
		account.OUPath != "Root/Workloads" ||
		account.LeftAt != "2026-02-15T00:00:00Z" {
		t.Fatalf("created account after UI workflow = %+v, want closed in Workloads with left_at", account)
	}
	events, err := repo.ListAccountLifecycleEvents(ctx, persistence.AnyCompanyRetailOrganizationID, 200)
	if err != nil {
		t.Fatalf("ListAccountLifecycleEvents() error = %v", err)
	}
	if count := organizationLifecycleEventCountForAccount(events, "777788889997"); count != 4 {
		t.Fatalf("created account lifecycle event count = %d, want 4", count)
	}
}

func TestEmbeddedProgressiveEnhancementScriptServed(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)

	resp, err := server.Client().Get(server.URL + "/assets/app.js")
	if err != nil {
		t.Fatalf("GET /assets/app.js error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /assets/app.js status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/javascript") ||
		!strings.Contains(body, "data-partial-form") ||
		!strings.Contains(body, "X-AWS-Billing-Simulator-Fragment") {
		t.Fatalf("GET /assets/app.js missing partial-update script contract: %s", body)
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

func TestBillingVisibilityModelFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = filepath.Join(root, "billing-visibility-workspace")
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
	seedFilterableUsage(t, context.Background(), db)

	client := http.Client{Timeout: time.Second}
	tests := []struct {
		name  string
		path  string
		wants []string
		leaks []string
	}{
		{
			name: "management consolidated viewer sees linked account spend",
			path: "/bills?viewer_role=management-account&viewer_account_id=999988887777",
			wants: []string{
				`<option value="management-account" selected>Management</option>`,
				`name="viewer_account_id" value="999988887777"`,
				"Filter web",
				"Filter bucket",
				"Amazon EC2",
				"Amazon S3",
				"111122223333",
				"222233334444",
			},
		},
		{
			name: "finance viewer defaults to management payer scope",
			path: "/bills?viewer_role=finance",
			wants: []string{
				`<option value="finance" selected>Finance</option>`,
				"Filter web",
				"Filter bucket",
				"Amazon EC2",
				"Amazon S3",
				"111122223333",
				"222233334444",
			},
		},
		{
			name: "member viewer sees only its own informational charges",
			path: "/bills?viewer_role=member-account&viewer_account_id=111122223333",
			wants: []string{
				`<option value="member-account" selected>Member</option>`,
				`name="viewer_account_id" value="111122223333"`,
				"Filter web",
				"Amazon EC2",
				"111122223333",
			},
			leaks: []string{
				"Filter bucket",
				"Amazon S3",
				"222233334444",
			},
		},
		{
			name: "instructor viewer sees all local training data",
			path: "/bills?viewer_role=instructor",
			wants: []string{
				`<option value="instructor" selected>Instructor</option>`,
				"Filter web",
				"Filter bucket",
				"Amazon EC2",
				"Amazon S3",
				"111122223333",
				"222233334444",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := client.Get(server.URL() + tt.path)
			if err != nil {
				t.Fatalf("GET %s error = %v", tt.path, err)
			}
			body := readResponseBody(t, resp)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s status = %d, want %d; body=%s", tt.path, resp.StatusCode, http.StatusOK, body)
			}
			for _, want := range tt.wants {
				if !strings.Contains(body, want) {
					t.Fatalf("GET %s body missing %q: %s", tt.path, want, body)
				}
			}
			for _, leaked := range tt.leaks {
				if strings.Contains(body, leaked) {
					t.Fatalf("GET %s body leaked %q: %s", tt.path, leaked, body)
				}
			}
		})
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
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("GET /invoices/{id}/document.pdf status = %d, want %d; body=%s", resp.StatusCode, http.StatusNotImplemented, body)
	}
	if !strings.Contains(resp.Header.Get("X-Invoice-PDF-Implementation"), "html-to-pdf") ||
		!strings.Contains(body, "packaged HTML-to-PDF renderer") ||
		!strings.Contains(body, invoicePath) {
		t.Fatalf("GET /invoices/{id}/document.pdf missing implementation plan: headers=%v body=%s", resp.Header, body)
	}

	resp, err = client.Get(server.URL + invoicePDFPath + managementViewerQuery)
	if err != nil {
		t.Fatalf("GET /invoices/{id}/document.pdf management viewer error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("GET /invoices/{id}/document.pdf management viewer status = %d, want %d; body=%s", resp.StatusCode, http.StatusNotImplemented, body)
	}
	scopedManagementInvoicePath := invoicePath + "?" + managementInvoiceQuery
	if !strings.Contains(body, "packaged HTML-to-PDF renderer") ||
		!strings.Contains(body, scopedManagementInvoicePath) ||
		!strings.Contains(resp.Header.Get("Link"), "<"+scopedManagementInvoicePath+">") {
		t.Fatalf("GET /invoices/{id}/document.pdf management viewer missing scoped implementation plan: headers=%v body=%s", resp.Header, body)
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

func seedCostCategoryPreviewSpend(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	usageRepo := persistence.NewResourceUsageRepository(db)
	for _, request := range []persistence.ResourceCreateRequest{
		{
			ID:           "resource-cost-category-web",
			AccountID:    "111122223333",
			RegionCode:   "us-east-1",
			ServiceCode:  "AmazonEC2",
			ResourceType: "ec2_instance",
			ResourceName: "Cost category web",
			Status:       "active",
			StartedAt:    "2026-02-01T00:00:00Z",
			Tags: map[string]string{
				"app": "storefront",
			},
		},
		{
			ID:           "resource-cost-category-bucket",
			AccountID:    "222233334444",
			RegionCode:   "us-east-1",
			ServiceCode:  "AmazonS3",
			ResourceType: "s3_bucket",
			ResourceName: "Cost category bucket",
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
			ID:                  "usage-cost-category-web",
			ResourceID:          "resource-cost-category-web",
			UsageType:           "instance-hours:t3.medium",
			Operation:           "RunInstances",
			UsageStartTime:      "2026-02-01T00:00:00Z",
			UsageEndTime:        "2026-02-01T02:00:00Z",
			UsageQuantityMicros: 2_000_000,
			UsageUnit:           "Hours",
		},
		{
			ID:                  "usage-cost-category-bucket",
			ResourceID:          "resource-cost-category-bucket",
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
}

func readCostCategoryID(t *testing.T, db *sql.DB, name string) string {
	t.Helper()

	var id string
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM cost_categories WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatalf("read cost category %q ID: %v", name, err)
	}
	return id
}

func readBudgetGeneratedStateFingerprint(t *testing.T, ctx context.Context, db *sql.DB, periodStart, periodEnd string) string {
	t.Helper()

	repo := persistence.NewBudgetRepository(db)
	forecasts, err := repo.ListForecastSummaries(ctx, persistence.BudgetForecastSummaryListRequest{
		BillingPeriodStart: periodStart,
		BillingPeriodEnd:   periodEnd,
	})
	if err != nil {
		t.Fatalf("ListForecastSummaries(%s to %s) error = %v", periodStart, periodEnd, err)
	}
	alerts, err := repo.ListAlertNotifications(ctx, persistence.BudgetAlertNotificationListRequest{
		BillingPeriodStart: periodStart,
		BillingPeriodEnd:   periodEnd,
	})
	if err != nil {
		t.Fatalf("ListAlertNotifications(%s to %s) error = %v", periodStart, periodEnd, err)
	}
	return fmt.Sprintf("forecasts=%#v\nalerts=%#v", forecasts, alerts)
}

// requireCostCategoryAssignmentByLineItem returns the persisted category assignment for one billed line item.
func requireCostCategoryAssignmentByLineItem(t *testing.T, assignments []persistence.CostCategoryLineItemAssignment, lineItemID string) persistence.CostCategoryLineItemAssignment {
	t.Helper()

	for _, assignment := range assignments {
		if assignment.LineItemID == lineItemID {
			return assignment
		}
	}
	t.Fatalf("cost category assignments = %+v, want line item %q", assignments, lineItemID)
	return persistence.CostCategoryLineItemAssignment{}
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

// postTagDiscoveryRefresh runs the explicit tag discovery action and returns the rendered manager page.
func postTagDiscoveryRefresh(t *testing.T, client *http.Client, serverURL string) string {
	t.Helper()

	resp, err := client.PostForm(serverURL+"/tags/refresh", url.Values{})
	if err != nil {
		t.Fatalf("POST /tags/refresh error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /tags/refresh final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	return body
}

// readCostAllocationTagDiscoveryCounts reports persisted discovery rows without triggering refresh logic.
func readCostAllocationTagDiscoveryCounts(t *testing.T, ctx context.Context, db *sql.DB) (int, int) {
	t.Helper()

	var keyCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cost_allocation_tag_keys`).Scan(&keyCount); err != nil {
		t.Fatalf("count cost_allocation_tag_keys: %v", err)
	}
	var valueCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cost_allocation_tag_inventory`).Scan(&valueCount); err != nil {
		t.Fatalf("count cost_allocation_tag_inventory: %v", err)
	}
	return keyCount, valueCount
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

func requireCSVResponseRecord(t *testing.T, records [][]string, column, value string) []string {
	t.Helper()

	index := csvResponseColumnIndex(t, records[0], column)
	for _, record := range records[1:] {
		if record[index] == value {
			return record
		}
	}
	t.Fatalf("CSV response records = %+v, want %s=%q", records, column, value)
	return nil
}

func csvResponseColumnIndex(t *testing.T, header []string, column string) int {
	t.Helper()

	for idx, name := range header {
		if name == column {
			return idx
		}
	}
	t.Fatalf("CSV response header = %+v, missing %q", header, column)
	return -1
}

func readOnlyResourceID(t *testing.T, db *sql.DB) string {
	t.Helper()

	var resourceID string
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM resources LIMIT 1`).Scan(&resourceID); err != nil {
		t.Fatalf("read resource ID: %v", err)
	}
	return resourceID
}

// readOnlyResourceIDByName finds one test-created resource without mutating workspace state.
func readOnlyResourceIDByName(t *testing.T, db *sql.DB, resourceName string) string {
	t.Helper()

	var resourceID string
	if err := db.QueryRowContext(context.Background(), `SELECT id FROM resources WHERE resource_name = ?`, resourceName).Scan(&resourceID); err != nil {
		t.Fatalf("read resource ID for %q: %v", resourceName, err)
	}
	return resourceID
}

func organizationLifecycleEventCountForAccount(events []persistence.AccountLifecycleEvent, accountID string) int {
	count := 0
	for _, event := range events {
		if event.AccountID == accountID {
			count++
		}
	}
	return count
}

func seedFilterableUsage(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()

	usageRepo := persistence.NewResourceUsageRepository(db)
	if _, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-filter-ec2",
		AccountID:    "111122223333",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonEC2",
		ResourceType: "ec2_instance",
		ResourceName: "Filter web",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource(EC2) error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-filter-ec2",
		ResourceID:          "resource-filter-ec2",
		UsageType:           "instance-hours:t3.medium",
		Operation:           "RunInstances",
		UsageStartTime:      "2026-02-01T00:00:00Z",
		UsageEndTime:        "2026-02-01T01:00:00Z",
		UsageQuantityMicros: 1_000_000,
		UsageUnit:           "Hours",
	}); err != nil {
		t.Fatalf("RecordUsageEvent(EC2) error = %v", err)
	}
	if _, err := usageRepo.CreateResource(ctx, persistence.ResourceCreateRequest{
		ID:           "resource-filter-s3",
		AccountID:    "222233334444",
		RegionCode:   "us-east-1",
		ServiceCode:  "AmazonS3",
		ResourceType: "s3_bucket",
		ResourceName: "Filter bucket",
		Status:       "active",
		StartedAt:    "2026-02-01T00:00:00Z",
	}); err != nil {
		t.Fatalf("CreateResource(S3) error = %v", err)
	}
	if _, err := usageRepo.RecordUsageEvent(ctx, persistence.UsageEventCreateRequest{
		ID:                  "usage-filter-s3",
		ResourceID:          "resource-filter-s3",
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
