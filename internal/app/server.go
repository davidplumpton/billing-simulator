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
	bills := newBillsHandler(db)
	organization := newOrganizationHandler(db)
	tags := newCostAllocationTagsHandler(db)
	costCategories := newCostCategoriesHandler(db)
	costExplorer := newCostExplorerHandler(db)
	budgets := newBudgetHandler(db)
	payments := newPaymentsHandler(db)
	mux := http.NewServeMux()
	mux.HandleFunc("/", resourceLab.handleRoot)
	mux.HandleFunc("/assets/app.css", serveAppStylesheet)
	mux.HandleFunc("/assets/app.js", serveAppScript)
	mux.HandleFunc("/organization", organization.handleOrganization)
	mux.HandleFunc("/organization/accounts/create", organization.handleCreateAccount)
	mux.HandleFunc("/organization/accounts/move", organization.handleMoveAccount)
	mux.HandleFunc("/organization/accounts/suspend", organization.handleSuspendAccount)
	mux.HandleFunc("/organization/accounts/close", organization.handleCloseAccount)
	mux.HandleFunc("/resources", resourceLab.handleResources)
	mux.HandleFunc("/resources/create", resourceLab.handleCreateResource)
	mux.HandleFunc("/resources/tags", resourceLab.handleAddTag)
	mux.HandleFunc("/resources/usage", resourceLab.handleRecordUsage)
	mux.HandleFunc("/resources/generate", resourceLab.handleGenerateUsage)
	mux.HandleFunc("/resources/billing-pipeline", resourceLab.handleRunBillingPipeline)
	mux.HandleFunc("/resources/daily-metering", resourceLab.handleRunDailyMeteringJob)
	mux.HandleFunc("/resources/month-close", resourceLab.handleRunMonthEndClose)
	mux.HandleFunc("/clock/advance", resourceLab.handleAdvanceClock)
	mux.HandleFunc("/tags", tags.handleTags)
	mux.HandleFunc("/tags/activate", tags.handleActivateTag)
	mux.HandleFunc("/tags/deactivate", tags.handleDeactivateTag)
	mux.HandleFunc("/cost-categories", costCategories.handleCostCategories)
	mux.HandleFunc("/cost-categories/categories/create", costCategories.handleCreateCostCategory)
	mux.HandleFunc("/cost-categories/rules/create", costCategories.handleCreateCostCategoryRule)
	mux.HandleFunc("/cost-categories/splits/create", costCategories.handleCreateCostCategorySplitRule)
	mux.HandleFunc("/cost-explorer", costExplorer.handleCostExplorer)
	mux.HandleFunc("/cost-explorer/results.csv", costExplorer.handleCostExplorerResultsCSV)
	mux.HandleFunc("/cost-explorer/line-items", costExplorer.handleCostExplorerLineItems)
	mux.HandleFunc("/cost-explorer/reports/save", costExplorer.handleSaveCostExplorerReport)
	mux.HandleFunc("/cost-explorer/reports/run", costExplorer.handleRunCostExplorerReport)
	mux.HandleFunc("/budgets", budgets.handleBudgets)
	mux.HandleFunc("/budgets/create", budgets.handleCreateBudget)
	mux.HandleFunc("/bills", bills.handleBills)
	mux.HandleFunc("/invoices/", bills.handleInvoice)
	mux.HandleFunc("/payments", payments.handlePayments)
	mux.HandleFunc("/payments/action", payments.handlePaymentAction)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "ok")
	})
	return mux
}
