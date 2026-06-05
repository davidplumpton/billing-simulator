package main

import (
	"bufio"
	"context"
	"database/sql"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func TestPackagedCommandBuildsAndRunsFreshWorkspaceSmoke(t *testing.T) {
	t.Parallel()

	binaryPath := buildCommandBinary(t)
	root := t.TempDir()
	workspacePath := filepath.Join(root, "fresh-workspace")
	statePath := filepath.Join(root, "state", "app.json")

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(
		ctx,
		binaryPath,
		"-http", "127.0.0.1:0",
		"-workspace", workspacePath,
		"-state", statePath,
		"-browser=false",
	)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("StderrPipe() error = %v", err)
	}

	urls, logs := collectStartupURLs(stderr)
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start built billing-sim command: %v", err)
	}
	waitErr := make(chan error, 1)
	go func() {
		waitErr <- cmd.Wait()
	}()
	t.Cleanup(func() {
		stopCommand(t, cancel, cmd, waitErr)
	})

	var serverURL string
	select {
	case serverURL = <-urls:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatalf("built billing-sim command did not log a startup URL; stderr:\n%s", logs())
	}

	client := http.Client{Timeout: time.Second}
	body := getCommandSmokeBody(t, client, serverURL+"/healthz", http.StatusOK)
	if strings.TrimSpace(body) != "ok" {
		t.Fatalf("GET /healthz body = %q, want ok", body)
	}

	resp, err := client.Get(serverURL + "/")
	if err != nil {
		t.Fatalf("GET / from built command error = %v", err)
	}
	body = readCommandSmokeBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / final status = %d, want %d; body=%s", resp.StatusCode, http.StatusOK, body)
	}
	if got := resp.Request.URL.Path; got != "/resources" {
		t.Fatalf("GET / final path = %q, want /resources", got)
	}
	for _, want := range []string{
		`<title>Resources - AWS Billing Simulator</title>`,
		`<link rel="stylesheet" href="/assets/app.css">`,
		`<script src="/assets/app.js" defer></script>`,
		"Create Resource",
		"Simulator Clock",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("GET / from built command missing %q: %s", want, body)
		}
	}
	if strings.Contains(body, "Workspace Required") {
		t.Fatalf("built command did not open configured workspace: %s", body)
	}

	assetChecks := []struct {
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
	for _, check := range assetChecks {
		resp, err := client.Get(serverURL + check.path)
		if err != nil {
			t.Fatalf("GET %s from built command error = %v", check.path, err)
		}
		body = readCommandSmokeBody(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d, want %d; body=%s", check.path, resp.StatusCode, http.StatusOK, body)
		}
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, check.contentType) {
			t.Fatalf("GET %s Content-Type = %q, want prefix %q", check.path, got, check.contentType)
		}
		for _, want := range check.wants {
			if !strings.Contains(body, want) {
				t.Fatalf("GET %s missing %q: %s", check.path, want, body)
			}
		}
	}

	db, err := sql.Open("sqlite", persistence.WorkspaceDBPath(workspacePath))
	if err != nil {
		t.Fatalf("sql.Open() migrated workspace error = %v", err)
	}
	defer db.Close()

	var migrationCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatalf("count schema_migrations in command-created workspace: %v", err)
	}
	if migrationCount != 23 {
		t.Fatalf("schema_migrations count = %d, want 23", migrationCount)
	}

	var catalogCount int
	if err := db.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM price_catalog_items`).Scan(&catalogCount); err != nil {
		t.Fatalf("count price_catalog_items in command-created workspace: %v", err)
	}
	if catalogCount != 18 {
		t.Fatalf("price_catalog_items count = %d, want 18", catalogCount)
	}
}

// buildCommandBinary builds the CLI from the module root the same way a local user would.
func buildCommandBinary(t *testing.T) string {
	t.Helper()

	outputPath := filepath.Join(t.TempDir(), "billing-sim")
	cmd := exec.Command("go", "build", "-o", outputPath, "./cmd/billing-sim")
	cmd.Dir = repoRoot(t)
	combined, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build ./cmd/billing-sim error = %v\n%s", err, string(combined))
	}
	if info, err := os.Stat(outputPath); err != nil {
		t.Fatalf("built command missing at %s: %v", outputPath, err)
	} else if info.Size() == 0 {
		t.Fatalf("built command at %s is empty", outputPath)
	}
	return outputPath
}

// repoRoot locates the module root without relying on the current working directory.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() could not locate test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

// collectStartupURLs drains command stderr and returns startup URLs as they are logged.
func collectStartupURLs(stderr io.Reader) (<-chan string, func() string) {
	urls := make(chan string, 1)
	var mu sync.Mutex
	var lines []string

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			mu.Lock()
			lines = append(lines, line)
			mu.Unlock()

			if url, ok := startupURLFromLogLine(line); ok {
				select {
				case urls <- url:
				default:
				}
			}
		}
		if err := scanner.Err(); err != nil {
			mu.Lock()
			lines = append(lines, "stderr scan error: "+err.Error())
			mu.Unlock()
		}
	}()

	logs := func() string {
		mu.Lock()
		defer mu.Unlock()
		return strings.Join(lines, "\n")
	}
	return urls, logs
}

// startupURLFromLogLine extracts the local app URL from slog text output.
func startupURLFromLogLine(line string) (string, bool) {
	for _, field := range strings.Fields(line) {
		value, ok := strings.CutPrefix(field, "url=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"`)
		if strings.HasPrefix(value, "http://127.0.0.1:") {
			return value, true
		}
	}
	return "", false
}

// stopCommand asks the child process to shut down cleanly and cancels it if needed.
func stopCommand(t *testing.T, cancel context.CancelFunc, cmd *exec.Cmd, waitErr <-chan error) {
	t.Helper()

	if cmd.Process == nil {
		cancel()
		return
	}
	_ = cmd.Process.Signal(os.Interrupt)
	select {
	case err := <-waitErr:
		cancel()
		if err != nil {
			t.Errorf("built billing-sim command exited with error: %v", err)
		}
	case <-time.After(3 * time.Second):
		cancel()
		select {
		case err := <-waitErr:
			if err != nil {
				t.Errorf("built billing-sim command required cancellation: %v", err)
			}
		case <-time.After(time.Second):
			t.Errorf("built billing-sim command did not exit after cancellation")
		}
	}
}

// getCommandSmokeBody fetches a smoke-test URL and returns its response body.
func getCommandSmokeBody(t *testing.T, client http.Client, url string, wantStatus int) string {
	t.Helper()

	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s error = %v", url, err)
	}
	body := readCommandSmokeBody(t, resp)
	if resp.StatusCode != wantStatus {
		t.Fatalf("GET %s status = %d, want %d; body=%s", url, resp.StatusCode, wantStatus, body)
	}
	return body
}

// readCommandSmokeBody closes and returns an HTTP response body for command smoke checks.
func readCommandSmokeBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(body)
}
