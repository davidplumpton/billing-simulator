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

	"aws-billing-simulator/internal/persistence"
)

// Server owns the local HTTP listener and its shutdown lifecycle.
type Server struct {
	httpServer *http.Server
	listener   net.Listener
	db         *sql.DB
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

	var db *sql.DB
	if cfg.WorkspacePath != "" {
		var err error
		db, err = persistence.OpenWorkspace(context.Background(), cfg.WorkspacePath)
		if err != nil {
			return nil, fmt.Errorf("open workspace: %w", err)
		}
	}

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		if db != nil {
			_ = db.Close()
		}
		return nil, fmt.Errorf("listen on %s: %w", cfg.HTTPAddr, err)
	}

	server := &Server{
		httpServer: &http.Server{
			Handler:           newMux(db),
			ReadHeaderTimeout: 5 * time.Second,
		},
		listener: listener,
		db:       db,
		done:     make(chan error, 1),
	}

	logger.Info("starting simulator", "url", server.URL())
	go func() {
		err := server.httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		server.done <- err
	}()

	return server, nil
}

// Run starts the simulator and keeps it alive until the context is cancelled.
func Run(ctx context.Context, cfg Config, logger *slog.Logger) error {
	server, err := Start(cfg, logger)
	if err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Close(shutdownCtx); err != nil {
			return err
		}
		return server.Wait()
	case err := <-server.done:
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
	if s.db != nil {
		if dbErr := s.db.Close(); err == nil {
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
	mux.HandleFunc("/assets/app.css", resourceLab.handleStylesheet)
	mux.HandleFunc("/resources", resourceLab.handleResources)
	mux.HandleFunc("/resources/create", resourceLab.handleCreateResource)
	mux.HandleFunc("/resources/tags", resourceLab.handleAddTag)
	mux.HandleFunc("/resources/usage", resourceLab.handleRecordUsage)
	mux.HandleFunc("/resources/generate", resourceLab.handleGenerateUsage)
	mux.HandleFunc("/resources/billing-pipeline", resourceLab.handleRunBillingPipeline)
	mux.HandleFunc("/resources/daily-metering", resourceLab.handleRunDailyMeteringJob)
	mux.HandleFunc("/clock/advance", resourceLab.handleAdvanceClock)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	return mux
}
