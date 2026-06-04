package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"aws-billing-simulator/internal/persistence"
)

type workspaceState struct {
	LastWorkspacePath string `json:"last_workspace_path"`
}

type workspaceStateStore struct {
	path string
}

// newWorkspaceStateStore creates the file-backed state store for app startup settings.
func newWorkspaceStateStore(path string) workspaceStateStore {
	return workspaceStateStore{path: strings.TrimSpace(path)}
}

// Load reads the last-used workspace path from the app state file.
func (s workspaceStateStore) Load() (workspaceState, error) {
	if s.path == "" {
		return workspaceState{}, nil
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return workspaceState{}, nil
		}
		return workspaceState{}, fmt.Errorf("read app state: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return workspaceState{}, nil
	}

	var state workspaceState
	if err := json.Unmarshal(data, &state); err != nil {
		return workspaceState{}, fmt.Errorf("parse app state: %w", err)
	}
	state.LastWorkspacePath = strings.TrimSpace(state.LastWorkspacePath)
	return state, nil
}

// Save persists the last-used workspace path for the next simulator launch.
func (s workspaceStateStore) Save(state workspaceState) error {
	if s.path == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create app state directory: %w", err)
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode app state: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		return fmt.Errorf("write app state: %w", err)
	}
	return nil
}

type workspaceSession struct {
	mu       sync.Mutex
	store    workspaceStateStore
	db       *sql.DB
	path     string
	lastPath string
}

// newWorkspaceSession loads the remembered workspace and opens the configured workspace when available.
func newWorkspaceSession(ctx context.Context, cfg Config, logger *slog.Logger) (*workspaceSession, error) {
	if logger == nil {
		logger = slog.Default()
	}

	session := &workspaceSession{
		store: newWorkspaceStateStore(cfg.StatePath),
	}

	state, err := session.store.Load()
	if err != nil {
		logger.Warn("could not load app state", "error", err)
	}
	session.lastPath = state.LastWorkspacePath

	path := strings.TrimSpace(cfg.WorkspacePath)
	explicitWorkspace := path != ""
	if path == "" {
		path = session.lastPath
	}
	if path == "" {
		return session, nil
	}

	if err := session.Open(ctx, path); err != nil {
		if explicitWorkspace {
			return nil, fmt.Errorf("open workspace: %w", err)
		}
		logger.Warn("could not open last-used workspace", "path", path, "error", err)
	}
	return session, nil
}

// Open creates or opens a workspace directory and makes it active for future requests.
func (s *workspaceSession) Open(ctx context.Context, rawPath string) error {
	workspacePath, err := normalizeWorkspacePath(rawPath)
	if err != nil {
		return err
	}

	db, err := persistence.OpenWorkspace(ctx, workspacePath)
	if err != nil {
		return err
	}
	if err := s.store.Save(workspaceState{LastWorkspacePath: workspacePath}); err != nil {
		closeErr := db.Close()
		if closeErr != nil {
			return fmt.Errorf("%w; close workspace database: %v", err, closeErr)
		}
		return err
	}

	s.mu.Lock()
	oldDB := s.db
	s.db = db
	s.path = workspacePath
	s.lastPath = workspacePath
	s.mu.Unlock()

	if oldDB != nil {
		if err := oldDB.Close(); err != nil {
			return fmt.Errorf("close previous workspace database: %w", err)
		}
	}
	return nil
}

// DB returns the active workspace database, or nil when no workspace is open.
func (s *workspaceSession) DB() *sql.DB {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db
}

// CurrentPath returns the active workspace directory path.
func (s *workspaceSession) CurrentPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.path
}

// LastPath returns the most recent workspace path known to the session.
func (s *workspaceSession) LastPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastPath
}

// Close closes the active workspace database.
func (s *workspaceSession) Close() error {
	s.mu.Lock()
	db := s.db
	s.db = nil
	s.path = ""
	s.mu.Unlock()

	if db == nil {
		return nil
	}
	return db.Close()
}

// normalizeWorkspacePath expands user input into a stable absolute workspace directory path.
func normalizeWorkspacePath(rawPath string) (string, error) {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return "", fmt.Errorf("workspace path is required")
	}
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		if path == "~" {
			path = home
		} else {
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	return filepath.Clean(absPath), nil
}
