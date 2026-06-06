package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func TestWorkspaceStateStorePersistsLastWorkspacePath(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "state", "app.json")
	store := newWorkspaceStateStore(statePath)
	workspacePath := filepath.Join(t.TempDir(), "workspace")

	if err := store.Save(workspaceState{LastWorkspacePath: workspacePath}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	state, err := store.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.LastWorkspacePath != workspacePath {
		t.Fatalf("LastWorkspacePath = %q, want %q", state.LastWorkspacePath, workspacePath)
	}
}

func TestEmbeddedSharedTemplatesRenderPagePartials(t *testing.T) {
	t.Parallel()

	tmpl := newPageTemplate("embedded-template-test", `{{template "ui.notices" .Notices}}{{template "ui.empty-state" .WorkspaceEmptyState}}`)
	data := struct {
		Notices             []uiNoticeView
		WorkspaceEmptyState uiEmptyStateView
	}{
		Notices:             uiNotices("Saved", ""),
		WorkspaceEmptyState: uiWorkspaceRequiredState(),
	}
	var body strings.Builder
	if err := tmpl.Execute(&body, data); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	rendered := body.String()
	for _, want := range []string{
		`<div class="notice success">Saved</div>`,
		`<h2>Workspace Required</h2>`,
		`href="/workspaces"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered embedded partials missing %q: %s", want, rendered)
		}
	}
}

func TestStartOpensLastUsedWorkspacePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	workspacePath := filepath.Join(root, "remembered-workspace")
	writeWorkspaceState(t, statePath, workspacePath)

	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.StatePath = statePath
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

	if _, err := os.Stat(persistence.WorkspaceDBPath(workspacePath)); err != nil {
		t.Fatalf("remembered workspace database was not created: %v", err)
	}

	resp, err := http.Get(server.URL() + "/resources")
	if err != nil {
		t.Fatalf("GET /resources error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /resources status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if strings.Contains(body, "Workspace Required") || !strings.Contains(body, "Create Resource") {
		t.Fatalf("GET /resources did not render open workspace lab: %s", body)
	}
}

func TestStartPersistsConfiguredWorkspacePath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "state.json")
	workspacePath := filepath.Join(root, "configured-workspace")

	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.WorkspacePath = workspacePath
	cfg.StatePath = statePath
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

	state, err := newWorkspaceStateStore(statePath).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.LastWorkspacePath != workspacePath {
		t.Fatalf("LastWorkspacePath = %q, want %q", state.LastWorkspacePath, workspacePath)
	}
}

func TestWorkspaceUICreatesWorkspaceAndPersistsLastPath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "state", "app.json")
	workspacePath := filepath.Join(root, "browser-workspace")

	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.StatePath = statePath
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

	resp, err := http.Get(server.URL() + "/")
	if err != nil {
		t.Fatalf("GET / error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, "Workspaces") || !strings.Contains(body, "No workspace open") {
		t.Fatalf("GET / did not render workspace selector: %s", body)
	}

	client := http.Client{Timeout: time.Second}
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
	if !strings.Contains(body, "Opened workspace") || !strings.Contains(body, "Create Resource") {
		t.Fatalf("workspace open response missing flash or resource lab: %s", body)
	}

	if _, err := os.Stat(persistence.WorkspaceDBPath(workspacePath)); err != nil {
		t.Fatalf("workspace database was not created: %v", err)
	}

	state, err := newWorkspaceStateStore(statePath).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.LastWorkspacePath != workspacePath {
		t.Fatalf("LastWorkspacePath = %q, want %q", state.LastWorkspacePath, workspacePath)
	}

	db, err := sql.Open("sqlite", persistence.WorkspaceDBPath(workspacePath))
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if count != 30 {
		t.Fatalf("schema_migrations count = %d, want 30", count)
	}
}

func TestServerRenderedUIShellFeatureWorksInFreshWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	statePath := filepath.Join(root, "state", "app.json")
	workspacePath := filepath.Join(root, "ui-shell-workspace")

	cfg := DefaultConfig()
	cfg.HTTPAddr = "127.0.0.1:0"
	cfg.StatePath = statePath
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
	resp, err := client.Get(server.URL() + "/workspaces")
	if err != nil {
		t.Fatalf("GET /workspaces error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /workspaces status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Workspaces - AWS Billing Simulator</title>`,
		`<link rel="stylesheet" href="/assets/app.css">`,
		`<script src="/assets/app.js" defer></script>`,
		`<a class="active" aria-current="page" href="/workspaces">Workspaces</a>`,
		"No workspace open",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /workspaces missing %q: %s", want, body)
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
		t.Fatalf("POST /workspaces/open status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<title>Resources - AWS Billing Simulator</title>`,
		`<a class="active" aria-current="page" href="/resources">Resources</a>`,
		`data-partial-form="resources"`,
		`<table class="dense-table">`,
		"Create Resource",
		"Opened workspace",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("workspace open response missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Workspace Required") {
		t.Fatalf("workspace open response still requires a workspace: %s", body)
	}

	pages := []struct {
		path  string
		wants []string
	}{
		{
			path: "/organization",
			wants: []string{
				`<title>Organization - AWS Billing Simulator</title>`,
				`<a class="active" aria-current="page" href="/organization">Organization</a>`,
				"AnyCompany Retail",
				"Account Detail",
			},
		},
		{
			path: "/bills",
			wants: []string{
				`<title>Bills - AWS Billing Simulator</title>`,
				`<a class="active" aria-current="page" href="/bills">Bills</a>`,
				`data-partial-form="bills"`,
				"No issued bills to reconcile",
			},
		},
	}
	for _, page := range pages {
		resp, err := client.Get(server.URL() + page.path)
		if err != nil {
			t.Fatalf("GET %s error = %v", page.path, err)
		}
		body = readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d; body=%s", page.path, resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, `<main class="page`) {
			t.Fatalf("GET %s missing shared document shell: %s", page.path, body)
		}
		for _, want := range page.wants {
			if !strings.Contains(body, want) {
				t.Fatalf("GET %s missing %q: %s", page.path, want, body)
			}
		}
	}

	assets := []struct {
		path        string
		contentType string
		wants       []string
	}{
		{
			path:        "/assets/app.css",
			contentType: "text/css",
			wants:       []string{".table-wrap", "@media (max-width: 980px)"},
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

func TestReusableUITemplatePartialsRenderAcrossPages(t *testing.T) {
	t.Parallel()

	workspaceMux := httptest.NewServer(newWorkspaceMux(&workspaceSession{}))
	t.Cleanup(workspaceMux.Close)
	workspaceClient := workspaceMux.Client()

	resp, err := workspaceClient.Get(workspaceMux.URL + "/workspaces?flash=Saved")
	if err != nil {
		t.Fatalf("GET /workspaces error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /workspaces status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<div class="notice success">Saved</div>`,
		`<label class="form-row">Workspace Directory`,
		`<button type="submit">Open or Create Workspace</button>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /workspaces missing reusable UI fragment %q: %s", want, body)
		}
	}

	resp, err = workspaceClient.Get(workspaceMux.URL + "/resources")
	if err != nil {
		t.Fatalf("GET /resources without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /resources without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, `<section class="empty">`) ||
		!strings.Contains(body, `<a class="button-link" href="/workspaces">Open Workspace</a>`) {
		t.Fatalf("GET /resources missing shared empty state: %s", body)
	}

	resp, err = workspaceClient.Get(workspaceMux.URL + "/organization")
	if err != nil {
		t.Fatalf("GET /organization without workspace error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /organization without workspace status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, `<section class="empty">`) ||
		!strings.Contains(body, `<a class="button-link" href="/workspaces">Open Workspace</a>`) {
		t.Fatalf("GET /organization missing shared empty state: %s", body)
	}

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

	appMux := httptest.NewServer(newMux(db))
	t.Cleanup(appMux.Close)
	appClient := appMux.Client()

	resp, err = appClient.Get(appMux.URL + "/resources")
	if err != nil {
		t.Fatalf("GET /resources error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /resources status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	for _, want := range []string{
		`<label class="form-row">Amount`,
		`<table class="dense-table">`,
		`<th>Name</th>`,
		`colspan="8" class="empty-cell">No resources</td>`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET /resources missing reusable UI fragment %q: %s", want, body)
		}
	}

	resp, err = appClient.Get(appMux.URL + "/bills")
	if err != nil {
		t.Fatalf("GET /bills error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /bills status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if !strings.Contains(body, `<th>Rounding Residual</th>`) ||
		!strings.Contains(body, `colspan="16" class="empty-cell">No issued bills to reconcile</td>`) {
		t.Fatalf("GET /bills missing shared dense-table fragments: %s", body)
	}

	resp, err = appClient.Get(appMux.URL + "/invoices/SIM-INV-MISSING")
	if err != nil {
		t.Fatalf("GET missing invoice error = %v", err)
	}
	body = readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET missing invoice status = %d, want %d; body=%s", resp.StatusCode, http.StatusNotFound, body)
	}
	if !strings.Contains(body, `<div class="page-actions">`) ||
		!strings.Contains(body, `<a class="button-link" href="/bills">Bills</a>`) ||
		!strings.Contains(body, `<div class="notice error">Invoice not found.</div>`) {
		t.Fatalf("GET missing invoice missing shared action bar or validation notice: %s", body)
	}
}

func TestSharedLayoutNavigationAndEmbeddedStylesheet(t *testing.T) {
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

	server := httptest.NewServer(newWorkspaceMux(&workspaceSession{
		db:   db,
		path: workspacePath,
	}))
	t.Cleanup(server.Close)

	client := server.Client()
	pages := []struct {
		path       string
		title      string
		activeLink string
	}{
		{
			path:       "/workspaces",
			title:      "<title>Workspaces - AWS Billing Simulator</title>",
			activeLink: `<a class="active" aria-current="page" href="/workspaces">Workspaces</a>`,
		},
		{
			path:       "/organization",
			title:      "<title>Organization - AWS Billing Simulator</title>",
			activeLink: `<a class="active" aria-current="page" href="/organization">Organization</a>`,
		},
		{
			path:       "/resources",
			title:      "<title>Resources - AWS Billing Simulator</title>",
			activeLink: `<a class="active" aria-current="page" href="/resources">Resources</a>`,
		},
		{
			path:       "/cost-categories",
			title:      "<title>Cost Categories - AWS Billing Simulator</title>",
			activeLink: `<a class="active" aria-current="page" href="/cost-categories">Cost Categories</a>`,
		},
		{
			path:       "/cost-explorer",
			title:      "<title>Cost Explorer - AWS Billing Simulator</title>",
			activeLink: `<a class="active" aria-current="page" href="/cost-explorer">Cost Explorer</a>`,
		},
		{
			path:       "/bills",
			title:      "<title>Bills - AWS Billing Simulator</title>",
			activeLink: `<a class="active" aria-current="page" href="/bills">Bills</a>`,
		},
	}

	for _, page := range pages {
		resp, err := client.Get(server.URL + page.path)
		if err != nil {
			t.Fatalf("GET %s error = %v", page.path, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d; body=%s", page.path, resp.StatusCode, http.StatusOK, body)
		}
		if !strings.Contains(body, "<!doctype html>") || !strings.Contains(body, `<main class="page`) {
			t.Fatalf("GET %s missing shared document shell: %s", page.path, body)
		}
		if !strings.Contains(body, page.title) || !strings.Contains(body, page.activeLink) {
			t.Fatalf("GET %s missing title or active nav; body=%s", page.path, body)
		}
		if !strings.Contains(body, `<a href="/cost-explorer">Cost Explorer</a>`) &&
			!strings.Contains(body, `<a class="active" aria-current="page" href="/cost-explorer">Cost Explorer</a>`) {
			t.Fatalf("GET %s missing Cost Explorer navigation link: %s", page.path, body)
		}
		if !strings.Contains(body, `<span aria-disabled="true">Scenarios</span>`) {
			t.Fatalf("GET %s missing disabled future navigation item: %s", page.path, body)
		}
	}

	resp, err := client.Get(server.URL + "/assets/app.css")
	if err != nil {
		t.Fatalf("GET /assets/app.css error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /assets/app.css status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("GET /assets/app.css Content-Type = %q, want text/css", got)
	}
	if !strings.Contains(body, "--accent: #0f766e") || !strings.Contains(body, "@media (max-width: 980px)") {
		t.Fatalf("GET /assets/app.css missing embedded app styles: %s", body)
	}
}

func TestWorkspaceMuxRoutesInvoices(t *testing.T) {
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

	server := httptest.NewServer(newWorkspaceMux(&workspaceSession{db: db}))
	t.Cleanup(server.Close)

	resp, err := server.Client().Get(server.URL + "/invoices/SIM-INV-MISSING")
	if err != nil {
		t.Fatalf("GET /invoices/{id} error = %v", err)
	}
	body := readResponseBody(t, resp)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("GET /invoices/{id} status = %d, want %d; body=%s", resp.StatusCode, http.StatusNotFound, body)
	}
	if !strings.Contains(body, "Invoice not found.") {
		t.Fatalf("GET /invoices/{id} did not route to invoice handler: %s", body)
	}

	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		req, err := http.NewRequest(method, server.URL+"/invoices?viewer_role=finance", nil)
		if err != nil {
			t.Fatalf("NewRequest(%s /invoices) error = %v", method, err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("%s /invoices error = %v", method, err)
		}
		body := readResponseBody(t, resp)
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("%s /invoices status = %d, want %d; body=%s", method, resp.StatusCode, http.StatusSeeOther, body)
		}
		if location := resp.Header.Get("Location"); location != "/bills?viewer_role=finance" {
			t.Fatalf("%s /invoices Location = %q, want /bills?viewer_role=finance", method, location)
		}
	}
}

func writeWorkspaceState(t *testing.T, statePath, workspacePath string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("create state dir: %v", err)
	}
	data, err := json.Marshal(workspaceState{LastWorkspacePath: workspacePath})
	if err != nil {
		t.Fatalf("marshal workspace state: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}
}
