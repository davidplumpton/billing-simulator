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
	if count != 18 {
		t.Fatalf("schema_migrations count = %d, want 18", count)
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
