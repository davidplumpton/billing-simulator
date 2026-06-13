package app

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
	"aws-billing-simulator/internal/scenario"
)

var (
	openWorkspaceForReset          = persistence.OpenWorkspace
	stageWorkspaceDatabaseForReset = stageWorkspaceDatabaseReset
)

type scenarioArchiveResult struct {
	Path        string
	ExportCount int
}

type scenarioArchiveBill struct {
	ID                 string `json:"id"`
	BillingPeriodStart string `json:"billing_period_start"`
	BillingPeriodEnd   string `json:"billing_period_end"`
	PayerAccountID     string `json:"payer_account_id"`
	CurrencyCode       string `json:"currency_code"`
	CURCSVPath         string `json:"cur_csv_path"`
	CURRowsWritten     int    `json:"cur_rows_written"`
	ReconciliationPath string `json:"reconciliation_path"`
}

type scenarioArchiveManifest struct {
	ArchivedAt         string                `json:"archived_at"`
	WorkspaceLabel     string                `json:"workspace_label"`
	DatabasePath       string                `json:"database_path"`
	FeedbackReportPath string                `json:"feedback_report_path"`
	ScenarioRun        scenarioRunAudit      `json:"scenario_run"`
	Bills              []scenarioArchiveBill `json:"bills"`
}

// currentWorkspacePath returns the active workspace path when scenario actions have session context.
func (h scenarioHandler) currentWorkspacePath() string {
	if h.workspace == nil {
		return ""
	}
	return h.workspace.CurrentPath()
}

// handleResetScenario reruns a packaged seed as an explicit instructor reset action.
func (h scenarioHandler) handleResetScenario(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		http.Error(w, "Open a workspace before resetting scenarios.", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderScenarios(w, r, http.StatusBadRequest, "parse scenario reset form: "+err.Error(), "")
		return
	}

	key := strings.TrimSpace(r.PostForm.Get("scenario_key"))
	definition, err := scenario.LoadSeedDefinition(key)
	if err != nil {
		h.renderScenarios(w, r, http.StatusBadRequest, "reset scenario: "+err.Error(), "")
		return
	}
	result, err := h.resetPackagedScenario(r.Context(), definition)
	if err != nil {
		h.scenarioHandlerAfterWorkspaceAction().renderScenarios(w, r, http.StatusBadRequest, "reset scenario: "+err.Error(), "")
		return
	}

	flash := fmt.Sprintf(
		"Reset %s to seed: %d/%d events succeeded, %s",
		definition.Name,
		result.Run.EventsSucceeded,
		result.Run.EventsTotal,
		scenarioBillsIssuedLabel(result.Run.BillsIssued),
	)
	http.Redirect(w, r, "/scenarios?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

// handleCloneWorkspace copies the active workspace and switches the session to the clone.
func (h scenarioHandler) handleCloneWorkspace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		http.Error(w, "Open a workspace before cloning scenarios.", http.StatusServiceUnavailable)
		return
	}
	if h.workspace == nil || h.currentWorkspacePath() == "" {
		h.renderScenarios(w, r, http.StatusServiceUnavailable, "Open a workspace before cloning scenarios.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderScenarios(w, r, http.StatusBadRequest, "parse scenario clone form: "+err.Error(), "")
		return
	}

	clonedPath, err := h.workspace.CloneTo(r.Context(), r.PostForm.Get("clone_workspace_path"))
	if err != nil {
		h.renderScenarios(w, r, http.StatusBadRequest, "clone workspace: "+err.Error(), "")
		return
	}

	http.Redirect(w, r, "/scenarios?flash="+urlQueryEscape("Cloned workspace to "+clonedPath), http.StatusSeeOther)
}

// handleArchiveScenario writes a local review bundle for one durable scenario run.
func (h scenarioHandler) handleArchiveScenario(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if h.db == nil {
		http.Error(w, "Open a workspace before archiving scenarios.", http.StatusServiceUnavailable)
		return
	}
	if h.currentWorkspacePath() == "" {
		h.renderScenarios(w, r, http.StatusServiceUnavailable, "Open a workspace before archiving scenarios.", "")
		return
	}
	if err := r.ParseForm(); err != nil {
		h.renderScenarios(w, r, http.StatusBadRequest, "parse scenario archive form: "+err.Error(), "")
		return
	}

	runID := strings.TrimSpace(r.PostForm.Get("scenario_run_id"))
	result, err := h.archiveScenarioRun(r.Context(), runID)
	if err != nil {
		h.renderScenarios(w, r, http.StatusBadRequest, "archive scenario: "+err.Error(), "")
		return
	}

	flash := fmt.Sprintf("Archived review bundle to %s with %d export files", result.Path, result.ExportCount)
	http.Redirect(w, r, "/scenarios?flash="+urlQueryEscape(flash), http.StatusSeeOther)
}

// runPackagedScenario loads and executes one embedded seed definition.
func (h scenarioHandler) runPackagedScenario(ctx context.Context, key string) (scenario.Definition, scenario.RunResult, error) {
	definition, err := scenario.LoadSeedDefinition(strings.TrimSpace(key))
	if err != nil {
		return scenario.Definition{}, scenario.RunResult{}, err
	}
	result, err := scenario.NewRunner(h.db).Run(ctx, definition)
	if err != nil {
		return scenario.Definition{}, scenario.RunResult{}, err
	}
	if _, err := scenario.NewEvaluator(h.db).EvaluateRun(ctx, result.Run.ID, definition); err != nil {
		return scenario.Definition{}, scenario.RunResult{}, fmt.Errorf("evaluate scenario checks: %w", err)
	}
	return definition, result, nil
}

// resetPackagedScenario rebuilds the active workspace database before applying one seed.
func (h scenarioHandler) resetPackagedScenario(ctx context.Context, definition scenario.Definition) (scenario.RunResult, error) {
	if h.workspace != nil && h.currentWorkspacePath() != "" {
		return h.workspace.ResetToScenarioSeed(ctx, definition)
	}
	result, err := scenario.NewRunner(h.db).Run(ctx, definition)
	if err != nil {
		return scenario.RunResult{}, err
	}
	if _, err := scenario.NewEvaluator(h.db).EvaluateRun(ctx, result.Run.ID, definition); err != nil {
		return scenario.RunResult{}, fmt.Errorf("evaluate scenario checks: %w", err)
	}
	return result, nil
}

// scenarioHandlerAfterWorkspaceAction refreshes the database handle after session-level mutations.
func (h scenarioHandler) scenarioHandlerAfterWorkspaceAction() scenarioHandler {
	if h.workspace == nil {
		return h
	}
	return newWorkspaceScenarioHandler(h.workspace)
}

// CloneTo copies the current workspace directory and opens the clone for subsequent requests.
func (s *workspaceSession) CloneTo(ctx context.Context, rawTargetPath string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("workspace session is required")
	}
	targetPath, err := normalizeWorkspacePath(rawTargetPath)
	if err != nil {
		return "", err
	}

	s.swapMu.Lock()
	defer s.swapMu.Unlock()

	s.mu.Lock()
	sourcePath := s.path
	db := s.db
	s.mu.Unlock()
	if db == nil || sourcePath == "" {
		return "", fmt.Errorf("open workspace is required")
	}
	if err := cloneWorkspaceDirectory(ctx, db, sourcePath, targetPath); err != nil {
		return "", err
	}

	clonedDB, err := persistence.OpenWorkspace(ctx, targetPath)
	if err != nil {
		return "", fmt.Errorf("open cloned workspace: %w", err)
	}
	if err := s.store.Save(workspaceState{LastWorkspacePath: targetPath}); err != nil {
		closeErr := clonedDB.Close()
		if closeErr != nil {
			return "", fmt.Errorf("%w; close cloned workspace database: %v", err, closeErr)
		}
		return "", err
	}

	s.mu.Lock()
	oldDB := s.db
	s.db = clonedDB
	s.path = targetPath
	s.lastPath = targetPath
	s.mu.Unlock()

	if oldDB != nil {
		if err := oldDB.Close(); err != nil {
			return "", fmt.Errorf("close previous workspace database: %w", err)
		}
	}
	return targetPath, nil
}

// ResetToScenarioSeed replaces the current workspace database and runs one packaged scenario seed.
func (s *workspaceSession) ResetToScenarioSeed(ctx context.Context, definition scenario.Definition) (scenario.RunResult, error) {
	if s == nil {
		return scenario.RunResult{}, fmt.Errorf("workspace session is required")
	}

	s.swapMu.Lock()
	defer s.swapMu.Unlock()

	s.mu.Lock()
	workspacePath := s.path
	oldDB := s.db
	s.mu.Unlock()

	if workspacePath == "" {
		return scenario.RunResult{}, fmt.Errorf("open workspace is required")
	}
	s.clearActiveWorkspaceForReset()
	if oldDB != nil {
		if err := oldDB.Close(); err != nil {
			return scenario.RunResult{}, s.recoverWorkspaceAfterResetFailure(ctx, workspacePath, fmt.Errorf("close workspace before reset: %w", err))
		}
	}
	staging, err := stageWorkspaceDatabaseForReset(workspacePath)
	if err != nil {
		return scenario.RunResult{}, s.recoverWorkspaceAfterResetFailure(ctx, workspacePath, err)
	}

	db, err := openWorkspaceForReset(ctx, workspacePath)
	if err != nil {
		restoreErr := staging.Restore()
		return scenario.RunResult{}, s.recoverWorkspaceAfterResetFailure(ctx, workspacePath, appendResetRecoveryError(fmt.Errorf("open reset workspace: %w", err), restoreErr))
	}
	if err := s.store.Save(workspaceState{LastWorkspacePath: workspacePath}); err != nil {
		closeErr := db.Close()
		restoreErr := staging.Restore()
		return scenario.RunResult{}, s.recoverWorkspaceAfterResetFailure(ctx, workspacePath, appendResetRecoveryError(err, closeErr, restoreErr))
	}
	s.activateWorkspaceAfterReset(db, workspacePath)
	if err := staging.Commit(); err != nil {
		return scenario.RunResult{}, err
	}

	result, err := scenario.NewRunner(db).Run(ctx, definition)
	if err != nil {
		return result, err
	}
	if _, err := scenario.NewEvaluator(db).EvaluateRun(ctx, result.Run.ID, definition); err != nil {
		return result, fmt.Errorf("evaluate scenario checks: %w", err)
	}
	return result, nil
}

// ArchiveScenarioRun serializes review archive snapshotting against DB-backed requests.
func (s *workspaceSession) ArchiveScenarioRun(ctx context.Context, runID string) (scenarioArchiveResult, error) {
	if s == nil {
		return scenarioArchiveResult{}, fmt.Errorf("workspace session is required")
	}

	s.swapMu.Lock()
	defer s.swapMu.Unlock()

	s.mu.Lock()
	db := s.db
	workspacePath := s.path
	s.mu.Unlock()
	if db == nil || workspacePath == "" {
		return scenarioArchiveResult{}, fmt.Errorf("open workspace is required")
	}
	return archiveScenarioRunWithSnapshot(ctx, db, workspacePath, runID)
}

// clearActiveWorkspaceForReset prevents new request handlers from observing a handle being closed.
func (s *workspaceSession) clearActiveWorkspaceForReset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db = nil
	s.path = ""
}

// activateWorkspaceAfterReset publishes a reopened workspace after reset staging has completed.
func (s *workspaceSession) activateWorkspaceAfterReset(db *sql.DB, workspacePath string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db = db
	s.path = workspacePath
	s.lastPath = workspacePath
}

// recoverWorkspaceAfterResetFailure reopens the workspace path so reset errors leave the session usable.
func (s *workspaceSession) recoverWorkspaceAfterResetFailure(ctx context.Context, workspacePath string, cause error) error {
	db, err := openWorkspaceForReset(ctx, workspacePath)
	if err != nil {
		return appendResetRecoveryError(cause, fmt.Errorf("recover workspace session: %w", err))
	}
	s.activateWorkspaceAfterReset(db, workspacePath)
	return cause
}

func appendResetRecoveryError(primary error, extras ...error) error {
	err := primary
	for _, extra := range extras {
		if extra != nil {
			if err == nil {
				err = extra
			} else {
				err = fmt.Errorf("%w; %v", err, extra)
			}
		}
	}
	return err
}

type workspaceResetStaging struct {
	backupDir string
	files     []workspaceResetStagedFile
}

type workspaceResetStagedFile struct {
	originalPath string
	backupPath   string
}

// stageWorkspaceDatabaseReset moves current SQLite files aside before opening a fresh reset database.
func stageWorkspaceDatabaseReset(workspacePath string) (workspaceResetStaging, error) {
	staging := workspaceResetStaging{}
	dbPath := persistence.WorkspaceDBPath(workspacePath)
	paths := []string{dbPath, dbPath + "-wal", dbPath + "-shm"}
	existingPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		if _, err := os.Lstat(path); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return staging, fmt.Errorf("inspect workspace database file %q: %w", path, err)
		}
		existingPaths = append(existingPaths, path)
	}
	if len(existingPaths) == 0 {
		return staging, nil
	}

	backupDir, err := os.MkdirTemp(workspacePath, ".reset-backup-")
	if err != nil {
		return staging, fmt.Errorf("create reset backup directory: %w", err)
	}
	staging.backupDir = backupDir
	for _, path := range existingPaths {
		backupPath := filepath.Join(backupDir, filepath.Base(path))
		if err := os.Rename(path, backupPath); err != nil {
			restoreErr := staging.Restore()
			return staging, appendResetRecoveryError(fmt.Errorf("stage workspace database file %q: %w", path, err), restoreErr)
		}
		staging.files = append(staging.files, workspaceResetStagedFile{
			originalPath: path,
			backupPath:   backupPath,
		})
	}
	return staging, nil
}

// Restore puts staged database files back after a reset setup failure.
func (s workspaceResetStaging) Restore() error {
	var result error
	for i := len(s.files) - 1; i >= 0; i-- {
		file := s.files[i]
		if err := os.Remove(file.originalPath); err != nil && !os.IsNotExist(err) {
			result = appendResetRecoveryError(result, fmt.Errorf("remove replacement workspace database file %q: %w", file.originalPath, err))
			continue
		}
		if err := os.Rename(file.backupPath, file.originalPath); err != nil {
			result = appendResetRecoveryError(result, fmt.Errorf("restore workspace database file %q: %w", file.originalPath, err))
		}
	}
	if s.backupDir != "" {
		if err := os.Remove(s.backupDir); err != nil && !os.IsNotExist(err) {
			result = appendResetRecoveryError(result, fmt.Errorf("remove reset backup directory %q: %w", s.backupDir, err))
		}
	}
	return result
}

// Commit discards staged backup files once the reset workspace has been safely published.
func (s workspaceResetStaging) Commit() error {
	if s.backupDir == "" {
		return nil
	}
	if err := os.RemoveAll(s.backupDir); err != nil {
		return fmt.Errorf("remove reset backup directory %q: %w", s.backupDir, err)
	}
	return nil
}

// archiveScenarioRun creates a ZIP with a database snapshot plus run-specific export files.
func (h scenarioHandler) archiveScenarioRun(ctx context.Context, runID string) (result scenarioArchiveResult, err error) {
	if h.workspace != nil {
		return h.workspace.ArchiveScenarioRun(ctx, runID)
	}
	return archiveScenarioRunWithSnapshot(ctx, h.db, h.currentWorkspacePath(), runID)
}

// archiveScenarioRunWithSnapshot builds a review ZIP after callers have stabilized workspace DB access.
func archiveScenarioRunWithSnapshot(ctx context.Context, db *sql.DB, workspacePath, runID string) (result scenarioArchiveResult, err error) {
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return scenarioArchiveResult{}, fmt.Errorf("scenario run ID is required")
	}
	if workspacePath == "" {
		return scenarioArchiveResult{}, fmt.Errorf("workspace path is required")
	}
	if db == nil {
		return scenarioArchiveResult{}, fmt.Errorf("database handle is required")
	}

	reader := scenarioHandler{db: db}
	run, err := reader.scenarioRunByID(ctx, runID)
	if err != nil {
		return scenarioArchiveResult{}, err
	}
	bills, err := reader.scenarioBillsForRun(ctx, runID)
	if err != nil {
		return scenarioArchiveResult{}, err
	}
	if err := checkpointWorkspaceDB(ctx, db); err != nil {
		return scenarioArchiveResult{}, err
	}
	feedbackReport, err := reader.loadScenarioFeedbackReport(ctx, runID)
	if err != nil {
		return scenarioArchiveResult{}, fmt.Errorf("build feedback report: %w", err)
	}

	archivedAt := time.Now().UTC().Format(time.RFC3339)
	archiveDir := filepath.Join(workspacePath, "review-archives")
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return scenarioArchiveResult{}, fmt.Errorf("create review archive directory: %w", err)
	}
	archivePath := filepath.Join(
		archiveDir,
		fmt.Sprintf("scenario-%s-%s.zip", safeCSVFilenamePart(runID, "run"), time.Now().UTC().Format("20060102T150405Z")),
	)
	archiveFile, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return scenarioArchiveResult{}, fmt.Errorf("create review archive: %w", err)
	}

	closed := false
	defer func() {
		if !closed {
			_ = archiveFile.Close()
		}
		if err != nil {
			_ = os.Remove(archivePath)
		}
	}()

	zipWriter := zip.NewWriter(archiveFile)
	if err = addFileToZip(zipWriter, "workspace/simulator.db", persistence.WorkspaceDBPath(workspacePath)); err != nil {
		return scenarioArchiveResult{}, err
	}

	curRepo := persistence.NewCURLineItemRepository(db)
	exportCount := 0
	for i := range bills {
		bill := &bills[i]
		request := persistence.CURCSVExportRequest{
			BillingPeriodStart: bill.BillingPeriodStart,
			BillingPeriodEnd:   bill.BillingPeriodEnd,
			PayerAccountID:     bill.PayerAccountID,
			GeneratedAt:        archivedAt,
		}
		curPath := "exports/" + safeCSVFilenamePart(bill.ID, "bill") + "-cur.csv"
		curWriter, createErr := zipWriter.Create(curPath)
		if createErr != nil {
			err = fmt.Errorf("create CUR export in archive: %w", createErr)
			return scenarioArchiveResult{}, err
		}
		curResult, writeErr := curRepo.WriteCSVExport(ctx, curWriter, request)
		if writeErr != nil {
			err = fmt.Errorf("write CUR export for bill %q: %w", bill.ID, writeErr)
			return scenarioArchiveResult{}, err
		}
		bill.CURCSVPath = curPath
		bill.CURRowsWritten = curResult.RowsWritten
		exportCount++

		report, reportErr := curRepo.GetReconciliationReport(ctx, persistence.CURExportReconciliationRequest{
			BillingPeriodStart: bill.BillingPeriodStart,
			BillingPeriodEnd:   bill.BillingPeriodEnd,
			PayerAccountID:     bill.PayerAccountID,
		})
		if reportErr != nil {
			err = fmt.Errorf("build reconciliation report for bill %q: %w", bill.ID, reportErr)
			return scenarioArchiveResult{}, err
		}
		reconciliationPath := "exports/" + safeCSVFilenamePart(bill.ID, "bill") + "-reconciliation.json"
		reportJSON, marshalErr := json.MarshalIndent(report, "", "  ")
		if marshalErr != nil {
			err = fmt.Errorf("encode reconciliation report for bill %q: %w", bill.ID, marshalErr)
			return scenarioArchiveResult{}, err
		}
		reportJSON = append(reportJSON, '\n')
		if err = addBytesToZip(zipWriter, reconciliationPath, reportJSON); err != nil {
			return scenarioArchiveResult{}, err
		}
		bill.ReconciliationPath = reconciliationPath
		exportCount++
	}

	feedbackReportPath := "feedback-report.json"
	feedbackReportJSON, err := json.MarshalIndent(feedbackReport, "", "  ")
	if err != nil {
		return scenarioArchiveResult{}, fmt.Errorf("encode feedback report: %w", err)
	}
	feedbackReportJSON = append(feedbackReportJSON, '\n')
	if err = addBytesToZip(zipWriter, feedbackReportPath, feedbackReportJSON); err != nil {
		return scenarioArchiveResult{}, err
	}

	manifest := scenarioArchiveManifest{
		ArchivedAt:         archivedAt,
		WorkspaceLabel:     scenarioArchiveWorkspaceLabel(workspacePath),
		DatabasePath:       "workspace/simulator.db",
		FeedbackReportPath: feedbackReportPath,
		ScenarioRun:        run,
		Bills:              bills,
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return scenarioArchiveResult{}, fmt.Errorf("encode archive manifest: %w", err)
	}
	manifestJSON = append(manifestJSON, '\n')
	if err = addBytesToZip(zipWriter, "manifest.json", manifestJSON); err != nil {
		return scenarioArchiveResult{}, err
	}
	if err = zipWriter.Close(); err != nil {
		return scenarioArchiveResult{}, fmt.Errorf("close review archive: %w", err)
	}
	if err = archiveFile.Close(); err != nil {
		return scenarioArchiveResult{}, fmt.Errorf("close review archive file: %w", err)
	}
	closed = true

	return scenarioArchiveResult{Path: archivePath, ExportCount: exportCount}, nil
}

// scenarioRunByID reads one durable scenario run by its stable ID.
func (h scenarioHandler) scenarioRunByID(ctx context.Context, runID string) (scenarioRunAudit, error) {
	run, err := scanScenarioRun(h.db.QueryRowContext(ctx, `
		SELECT id,
		       definition_name,
		       status,
		       clock_start,
		       current_event_id,
		       events_total,
		       events_succeeded,
		       resources_created,
		       usage_events_created,
		       metering_records_created,
		       bill_line_items_created,
		       bills_issued,
		       error_message,
		       started_at,
		       completed_at
		  FROM scenario_runs
		 WHERE id = ?
	`, runID).Scan)
	if err == sql.ErrNoRows {
		return scenarioRunAudit{}, fmt.Errorf("scenario run %q was not found: %w", runID, sql.ErrNoRows)
	}
	if err != nil {
		return scenarioRunAudit{}, fmt.Errorf("read scenario run %q: %w", runID, err)
	}
	return run, nil
}

// scenarioBillsForRun returns bills issued directly by scenario close events.
func (h scenarioHandler) scenarioBillsForRun(ctx context.Context, runID string) ([]scenarioArchiveBill, error) {
	rows, err := h.db.QueryContext(ctx, `
		SELECT DISTINCT b.id,
		       b.billing_period_start,
		       b.billing_period_end,
		       b.payer_account_id,
		       b.currency_code
		  FROM scenario_run_events e
		  JOIN bills b ON b.id = e.bill_id
		 WHERE e.scenario_run_id = ?
		   AND trim(e.bill_id) <> ''
		 ORDER BY b.billing_period_start, b.billing_period_end, b.payer_account_id, b.id
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("list scenario bills for run %q: %w", runID, err)
	}
	defer rows.Close()

	bills := []scenarioArchiveBill{}
	for rows.Next() {
		var bill scenarioArchiveBill
		if err := rows.Scan(
			&bill.ID,
			&bill.BillingPeriodStart,
			&bill.BillingPeriodEnd,
			&bill.PayerAccountID,
			&bill.CurrencyCode,
		); err != nil {
			return nil, fmt.Errorf("scan scenario bill: %w", err)
		}
		bills = append(bills, bill)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scenario bills: %w", err)
	}
	return bills, nil
}

// cloneWorkspaceDirectory copies a checkpointed workspace into a new empty directory.
func cloneWorkspaceDirectory(ctx context.Context, db *sql.DB, sourcePath, targetPath string) error {
	sourcePath = filepath.Clean(sourcePath)
	targetPath = filepath.Clean(targetPath)
	if sourcePath == targetPath {
		return fmt.Errorf("clone target must be different from the current workspace")
	}
	if pathIsInside(targetPath, sourcePath) {
		return fmt.Errorf("clone target cannot be inside the current workspace")
	}
	if err := ensureEmptyDirectoryTarget(targetPath); err != nil {
		return err
	}
	if err := checkpointWorkspaceDB(ctx, db); err != nil {
		return err
	}
	return copyWorkspaceTree(ctx, sourcePath, targetPath)
}

// removeWorkspaceDatabaseFiles deletes the SQLite database and transient sidecars for a seed reset.
func removeWorkspaceDatabaseFiles(workspacePath string) error {
	dbPath := persistence.WorkspaceDBPath(workspacePath)
	for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove workspace database file %q: %w", path, err)
		}
	}
	return nil
}

// checkpointWorkspaceDB flushes WAL pages so filesystem snapshots include the latest database state.
func checkpointWorkspaceDB(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("database handle is required")
	}
	var busy, logFrames, checkpointedFrames int
	if err := db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointedFrames); err != nil {
		return fmt.Errorf("checkpoint workspace database: %w", err)
	}
	if busy != 0 {
		return fmt.Errorf("checkpoint workspace database: %d connection(s) still busy", busy)
	}
	return nil
}

// ensureEmptyDirectoryTarget rejects clone targets that could overwrite learner data.
func ensureEmptyDirectoryTarget(targetPath string) error {
	entries, err := os.ReadDir(targetPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect clone target: %w", err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("clone target directory must be empty")
	}
	return nil
}

// copyWorkspaceTree copies regular workspace files while omitting transient SQLite sidecars.
func copyWorkspaceTree(ctx context.Context, sourcePath, targetPath string) error {
	return filepath.WalkDir(sourcePath, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(sourcePath, path)
		if err != nil {
			return fmt.Errorf("resolve workspace clone path: %w", err)
		}
		if rel == "." {
			return os.MkdirAll(targetPath, 0o755)
		}
		if entry.Name() == "simulator.db-wal" || entry.Name() == "simulator.db-shm" {
			return nil
		}

		destination := filepath.Join(targetPath, rel)
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect workspace file %q: %w", path, err)
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("workspace clone only supports regular files: %s", path)
		}
		return copyRegularFile(path, destination, info.Mode().Perm())
	})
}

// copyRegularFile writes one file into a clone without preserving unsupported metadata.
func copyRegularFile(sourcePath, targetPath string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return fmt.Errorf("create clone file directory: %w", err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open workspace source file: %w", err)
	}
	defer source.Close()

	target, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create workspace clone file: %w", err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = target.Close()
		}
	}()
	if _, err := io.Copy(target, source); err != nil {
		return fmt.Errorf("copy workspace file: %w", err)
	}
	if err := target.Close(); err != nil {
		return fmt.Errorf("close workspace clone file: %w", err)
	}
	closed = true
	return nil
}

// addFileToZip stores one local file under a stable archive path.
func addFileToZip(zipWriter *zip.Writer, archivePath, sourcePath string) error {
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		return fmt.Errorf("read archive source file %q: %w", sourcePath, err)
	}
	return addBytesToZip(zipWriter, archivePath, data)
}

// addBytesToZip writes a byte slice into the review archive.
func addBytesToZip(zipWriter *zip.Writer, archivePath string, data []byte) error {
	writer, err := zipWriter.Create(archivePath)
	if err != nil {
		return fmt.Errorf("create archive entry %q: %w", archivePath, err)
	}
	if _, err := io.Copy(writer, bytes.NewReader(data)); err != nil {
		return fmt.Errorf("write archive entry %q: %w", archivePath, err)
	}
	return nil
}

// scenarioArchiveWorkspaceLabel returns a display label without preserving local path components.
func scenarioArchiveWorkspaceLabel(workspacePath string) string {
	label := filepath.Base(filepath.Clean(strings.TrimSpace(workspacePath)))
	if label == "." || label == string(filepath.Separator) {
		return "workspace"
	}
	return safeCSVFilenamePart(label, "workspace")
}

// defaultScenarioClonePath suggests a sibling workspace directory for a scenario clone.
func defaultScenarioClonePath(currentPath, scenarioKey string) string {
	currentPath = strings.TrimSpace(currentPath)
	if currentPath == "" {
		return ""
	}
	return filepath.Join(
		filepath.Dir(currentPath),
		filepath.Base(currentPath)+"-"+safeCSVFilenamePart(scenarioKey, "scenario")+"-clone",
	)
}

// pathIsInside reports whether child is nested below parent.
func pathIsInside(child, parent string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
