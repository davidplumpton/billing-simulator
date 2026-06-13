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

	client := appTestHTTPClient()
	assertHealthCheckMethods(t, &client, server.URL())

	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := server.Close(shutdownCtx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := server.Wait(); err != nil {
		t.Fatalf("Wait() error = %v", err)
	}
}

func TestMethodNotAllowedResponsesIncludeAllowHeader(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newMux(nil))
	t.Cleanup(server.Close)

	tests := []struct {
		name      string
		method    string
		path      string
		wantAllow string
	}{
		{
			name:      "GET and HEAD route",
			method:    http.MethodPost,
			path:      "/overview",
			wantAllow: "GET, HEAD",
		},
		{
			name:      "health check route",
			method:    http.MethodPost,
			path:      "/healthz",
			wantAllow: "GET, HEAD",
		},
		{
			name:      "POST route",
			method:    http.MethodGet,
			path:      "/resources/create",
			wantAllow: "POST",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req, err := http.NewRequest(tt.method, server.URL+tt.path, nil)
			if err != nil {
				t.Fatalf("NewRequest(%s %s) error = %v", tt.method, tt.path, err)
			}
			resp, err := server.Client().Do(req)
			if err != nil {
				t.Fatalf("%s %s error = %v", tt.method, tt.path, err)
			}
			body := readResponseBody(t, resp)
			if resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("%s %s status = %d, want %d; body=%s", tt.method, tt.path, resp.StatusCode, http.StatusMethodNotAllowed, body)
			}
			if allow := resp.Header.Get("Allow"); allow != tt.wantAllow {
				t.Fatalf("%s %s Allow = %q, want %q", tt.method, tt.path, allow, tt.wantAllow)
			}
		})
	}
}

func TestWorkspaceMuxHealthCheckMethodGuard(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(newWorkspaceMux(&workspaceSession{}))
	t.Cleanup(server.Close)

	assertHealthCheckMethods(t, server.Client(), server.URL)
}

// assertHealthCheckMethods verifies the complete method contract for /healthz.
func assertHealthCheckMethods(t *testing.T, client *http.Client, serverURL string) {
	t.Helper()

	tests := []struct {
		name       string
		method     string
		wantStatus int
		wantAllow  string
		wantBody   string
	}{
		{
			name:       "GET",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantBody:   "ok\n",
		},
		{
			name:       "HEAD",
			method:     http.MethodHead,
			wantStatus: http.StatusOK,
		},
		{
			name:       "POST",
			method:     http.MethodPost,
			wantStatus: http.StatusMethodNotAllowed,
			wantAllow:  "GET, HEAD",
			wantBody:   "method not allowed\n",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, serverURL+"/healthz", nil)
			if err != nil {
				t.Fatalf("NewRequest(%s /healthz) error = %v", tt.method, err)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s /healthz error = %v", tt.method, err)
			}
			body := readResponseBody(t, resp)
			if resp.StatusCode != tt.wantStatus {
				t.Fatalf("%s /healthz status = %d, want %d; body=%s", tt.method, resp.StatusCode, tt.wantStatus, body)
			}
			if allow := resp.Header.Get("Allow"); allow != tt.wantAllow {
				t.Fatalf("%s /healthz Allow = %q, want %q", tt.method, allow, tt.wantAllow)
			}
			if body != tt.wantBody {
				t.Fatalf("%s /healthz body = %q, want %q", tt.method, body, tt.wantBody)
			}
		})
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
	if count != 38 {
		t.Fatalf("schema_migrations count = %d, want 38", count)
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

	client := appTestHTTPClient()

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
