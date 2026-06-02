package app

import (
	"context"
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

	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", cfg.HTTPAddr, err)
	}

	server := &Server{
		httpServer: &http.Server{
			Handler:           newMux(),
			ReadHeaderTimeout: 5 * time.Second,
		},
		listener: listener,
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
	return s.httpServer.Shutdown(ctx)
}

// Wait blocks until the serving goroutine exits.
func (s *Server) Wait() error {
	return <-s.done
}

// newMux wires the first smoke-testable endpoints for the simulator process.
func newMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "AWS Billing Simulator")
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	return mux
}
