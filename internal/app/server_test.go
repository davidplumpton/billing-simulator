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
	if count != 22 {
		t.Fatalf("schema_migrations count = %d, want 22", count)
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

	resp, err := client.Get(server.URL + "/tags")
	if err != nil {
		t.Fatalf("GET /tags error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tags status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
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
		`action="/tags/activate"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /tags body missing %q: %s", want, body)
		}
	}

	resp, err = client.PostForm(server.URL+"/tags/activate", url.Values{"tag_key": {"app"}})
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

	resp, err = client.Get(server.URL() + "/tags")
	if err != nil {
		t.Fatalf("GET /tags with billed spend error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /tags with billed spend status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
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
			t.Fatalf("GET /tags with billed spend body missing %q: %s", want, body)
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
	managementViewerQuery := "?viewer_role=management-account&viewer_account_id=999988887777"
	memberViewerQuery := "?viewer_role=member-account&viewer_account_id=111122223333"
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
	if !strings.Contains(body, "packaged HTML-to-PDF renderer") || !strings.Contains(body, invoicePath) {
		t.Fatalf("GET /invoices/{id}/document.pdf management viewer missing implementation plan: %s", body)
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
