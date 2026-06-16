package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Server owns the local HTTP listener and its shutdown lifecycle.
type Server struct {
	httpServer *http.Server
	listener   net.Listener
	workspace  *workspaceSession
	done       chan error
}

// Start validates configuration, binds the local listener, and serves handlers.
func Start(cfg Config, logger *slog.Logger) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}

	workspace, err := newWorkspaceSession(context.Background(), cfg, logger)
	if err != nil {
		return nil, err
	}

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		_ = workspace.Close()
		return nil, fmt.Errorf("listen on %s: %w", cfg.HTTPAddr, err)
	}

	server := &Server{
		httpServer: &http.Server{
			Handler:           newWorkspaceMux(workspace),
			ReadHeaderTimeout: 5 * time.Second,
		},
		listener:  listener,
		workspace: workspace,
		done:      make(chan error, 1),
	}

	logger.Info("starting simulator", "url", server.URL())
	go func() {
		err := server.httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		server.done <- err
	}()

	if cfg.OpenBrowser {
		if err := openBrowserURL(server.URL()); err != nil {
			logger.Warn("could not open browser", "url", server.URL(), "error", err)
		}
	}

	return server, nil
}

// Run starts the simulator and keeps it alive until the context is cancelled.
func Run(ctx context.Context, cfg Config, logger *slog.Logger) error {
	server, err := Start(cfg, logger)
	if err != nil {
		return err
	}
	return runStartedServer(ctx, server)
}

// runStartedServer owns shutdown after Start so every Run exit closes resources.
func runStartedServer(ctx context.Context, server *Server) error {
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			return err
		}
		return server.Wait()
	case err := <-server.done:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if closeErr := server.Close(shutdownCtx); closeErr != nil {
			if err == nil {
				return closeErr
			}
			return fmt.Errorf("%w; close server after serve exit: %v", err, closeErr)
		}
		return err
	}
}

// URL returns the browser URL for the bound local server.
func (s *Server) URL() string {
	return "http://" + s.listener.Addr().String()
}

// Close gracefully stops the local HTTP server.
func (s *Server) Close(ctx context.Context) error {
	err := s.httpServer.Shutdown(ctx)
	if s.workspace != nil {
		if dbErr := s.workspace.Close(); err == nil {
			err = dbErr
		}
	}
	return err
}

// Wait blocks until the serving goroutine exits.
func (s *Server) Wait() error {
	return <-s.done
}

// newMux wires the simulator's local browser UI and smoke-testable endpoints.
func newMux(db *sql.DB) http.Handler {
	resourceLab := newResourceLabHandler(db)
	mux := http.NewServeMux()
	mux.HandleFunc("/", resourceLab.handleRoot)
	registerDirectWorkspaceRoutes(mux)
	registerAppRoutes(mux, directAppRouteHandlers(db))
	return mux
}

// registerDirectWorkspaceRoutes makes workspace-only links explicit on the direct test mux.
func registerDirectWorkspaceRoutes(mux *http.ServeMux) {
	workspaces := directWorkspaceUnavailableHandler{}
	mux.HandleFunc("/workspaces", workspaces.handleWorkspaces)
	mux.HandleFunc("/workspaces/open", workspaces.handleWorkspaceAction)
	mux.HandleFunc("/workspaces/start", workspaces.handleWorkspaceAction)
}

type directWorkspaceUnavailableHandler struct{}

// handleWorkspaces renders an intentional direct-mux placeholder for workspace links.
func (directWorkspaceUnavailableHandler) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet, http.MethodHead)
		return
	}
	renderPage(w, http.StatusOK, pageLayoutOptions{
		Title:     "Workspaces - Billing Simulator",
		ActiveNav: "workspaces",
	}, directWorkspaceUnavailableTemplate, nil, "render direct workspace unavailable page")
}

// handleWorkspaceAction rejects workspace mutations on the direct mux without hiding the route.
func (directWorkspaceUnavailableHandler) handleWorkspaceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	http.Error(w, "workspace session unavailable on direct mux\n", http.StatusNotImplemented)
}

var directWorkspaceUnavailableTemplate = newPageTemplate("direct-workspace-unavailable", `<div class="page-heading">
	<div>
		<h1>Workspaces</h1>
		<p>Workspace selection is available from the runtime workspace session.</p>
	</div>
</div>
<section class="empty">
	<h2>Workspace Session Unavailable</h2>
	<p>The direct handler surface is for focused tests and does not own workspace lifecycle state.</p>
</section>`)

// handleHealthCheck serves the local readiness probe after route-level method enforcement.
func handleHealthCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintln(w, "ok")
}
