package persistence

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	defaultSavedReportLimit       = 25
	maxSavedReportLimit           = 100
	defaultSavedReportOwnerRole   = "management-account"
	defaultSavedReportGranularity = "monthly"
	defaultSavedReportMetric      = "unblended_cost"
	defaultSavedReportChartType   = "table"
	savedReportStatusNeverRun     = "never_run"
	savedReportStatusSucceeded    = "succeeded"
	savedReportStatusFailed       = "failed"
)

// SavedReport stores a reusable Cost Explorer-style report definition and its latest run metadata.
type SavedReport struct {
	ID                              string
	Name                            string
	Description                     string
	OwnerAccountID                  string
	OwnerRole                       string
	DateRangeStart                  string
	DateRangeEnd                    string
	Granularity                     string
	Filters                         map[string][]string
	Groupings                       []SavedReportGrouping
	Metrics                         []string
	ChartType                       string
	LastRunAt                       string
	LastRunStatus                   string
	LastRunRowCount                 int
	LastRunTotalUnblendedCostMicros int64
	LastRunError                    string
	CreatedAt                       string
	UpdatedAt                       string
}

// SavedReportGrouping describes one Cost Explorer grouping dimension, tag, or cost category.
type SavedReportGrouping struct {
	Type string `json:"type"`
	Key  string `json:"key"`
}

// SavedReportCreateRequest describes a report definition the learner wants to save.
type SavedReportCreateRequest struct {
	ID             string
	Name           string
	Description    string
	OwnerAccountID string
	OwnerRole      string
	DateRangeStart string
	DateRangeEnd   string
	Granularity    string
	Filters        map[string][]string
	Groupings      []SavedReportGrouping
	Metrics        []string
	ChartType      string
}

// SavedReportUpdateRequest describes a complete replacement for an existing saved report definition.
type SavedReportUpdateRequest struct {
	ID             string
	Name           string
	Description    string
	OwnerAccountID string
	OwnerRole      string
	DateRangeStart string
	DateRangeEnd   string
	Granularity    string
	Filters        map[string][]string
	Groupings      []SavedReportGrouping
	Metrics        []string
	ChartType      string
}

// SavedReportListRequest filters saved reports for a report owner.
type SavedReportListRequest struct {
	OwnerAccountID string
	OwnerRole      string
	Limit          int
}

// SavedReportRunUpdate records the latest execution metadata for a saved report.
type SavedReportRunUpdate struct {
	ID                       string
	RunAt                    string
	Status                   string
	RowCount                 int
	TotalUnblendedCostMicros int64
	ErrorMessage             string
}

// SavedReportRepository manages persisted Cost Explorer saved report definitions.
type SavedReportRepository struct {
	db *sql.DB
}

// NewSavedReportRepository creates a repository backed by a workspace database.
func NewSavedReportRepository(db *sql.DB) SavedReportRepository {
	return SavedReportRepository{db: db}
}

// Create saves a new Cost Explorer report definition for a simulated owner.
func (r SavedReportRepository) Create(ctx context.Context, request SavedReportCreateRequest) (SavedReport, error) {
	if r.db == nil {
		return SavedReport{}, fmt.Errorf("database handle is required")
	}
	request = normalizeSavedReportCreateRequest(request)
	if err := validateSavedReportDefinition(savedReportDefinition{
		ID:             request.ID,
		Name:           request.Name,
		OwnerAccountID: request.OwnerAccountID,
		OwnerRole:      request.OwnerRole,
		DateRangeStart: request.DateRangeStart,
		DateRangeEnd:   request.DateRangeEnd,
		Granularity:    request.Granularity,
		Filters:        request.Filters,
		Groupings:      request.Groupings,
		Metrics:        request.Metrics,
		ChartType:      request.ChartType,
	}); err != nil {
		return SavedReport{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("sr")
		if err != nil {
			return SavedReport{}, err
		}
		request.ID = id
	}

	filtersJSON, groupingsJSON, metricsJSON, err := marshalSavedReportDefinitionJSON(request.Filters, request.Groupings, request.Metrics)
	if err != nil {
		return SavedReport{}, err
	}
	if _, err := r.db.ExecContext(
		ctx,
		`INSERT INTO saved_reports (
			id,
			name,
			description,
			owner_account_id,
			owner_role,
			date_range_start,
			date_range_end,
			granularity,
			filters_json,
			groupings_json,
			metrics_json,
			chart_type
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		request.ID,
		request.Name,
		request.Description,
		request.OwnerAccountID,
		request.OwnerRole,
		request.DateRangeStart,
		request.DateRangeEnd,
		request.Granularity,
		filtersJSON,
		groupingsJSON,
		metricsJSON,
		request.ChartType,
	); err != nil {
		return SavedReport{}, fmt.Errorf("insert saved report %q: %w", request.ID, err)
	}
	return r.Get(ctx, request.ID)
}

// Update replaces the saved definition fields while preserving run history and creation metadata.
func (r SavedReportRepository) Update(ctx context.Context, request SavedReportUpdateRequest) (SavedReport, error) {
	if r.db == nil {
		return SavedReport{}, fmt.Errorf("database handle is required")
	}
	request = normalizeSavedReportUpdateRequest(request)
	if request.ID == "" {
		return SavedReport{}, fmt.Errorf("saved report ID is required")
	}
	if err := validateSavedReportDefinition(savedReportDefinition{
		ID:             request.ID,
		Name:           request.Name,
		OwnerAccountID: request.OwnerAccountID,
		OwnerRole:      request.OwnerRole,
		DateRangeStart: request.DateRangeStart,
		DateRangeEnd:   request.DateRangeEnd,
		Granularity:    request.Granularity,
		Filters:        request.Filters,
		Groupings:      request.Groupings,
		Metrics:        request.Metrics,
		ChartType:      request.ChartType,
	}); err != nil {
		return SavedReport{}, err
	}

	filtersJSON, groupingsJSON, metricsJSON, err := marshalSavedReportDefinitionJSON(request.Filters, request.Groupings, request.Metrics)
	if err != nil {
		return SavedReport{}, err
	}
	result, err := r.db.ExecContext(
		ctx,
		`UPDATE saved_reports
		 SET name = ?,
		     description = ?,
		     owner_account_id = ?,
		     owner_role = ?,
		     date_range_start = ?,
		     date_range_end = ?,
		     granularity = ?,
		     filters_json = ?,
		     groupings_json = ?,
		     metrics_json = ?,
		     chart_type = ?,
		     updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE id = ?`,
		request.Name,
		request.Description,
		request.OwnerAccountID,
		request.OwnerRole,
		request.DateRangeStart,
		request.DateRangeEnd,
		request.Granularity,
		filtersJSON,
		groupingsJSON,
		metricsJSON,
		request.ChartType,
		request.ID,
	)
	if err != nil {
		return SavedReport{}, fmt.Errorf("update saved report %q: %w", request.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return SavedReport{}, fmt.Errorf("read saved report update result for %q: %w", request.ID, err)
	}
	if affected == 0 {
		return SavedReport{}, fmt.Errorf("saved report %q not found", request.ID)
	}
	return r.Get(ctx, request.ID)
}

// RecordLastRun updates the latest execution metadata without changing the saved definition.
func (r SavedReportRepository) RecordLastRun(ctx context.Context, request SavedReportRunUpdate) (SavedReport, error) {
	if r.db == nil {
		return SavedReport{}, fmt.Errorf("database handle is required")
	}
	request = normalizeSavedReportRunUpdate(request)
	if err := validateSavedReportRunUpdate(request); err != nil {
		return SavedReport{}, err
	}
	_, runAt, err := normalizedRepositoryTimestamp("saved report run time", request.RunAt)
	if err != nil {
		return SavedReport{}, err
	}

	result, err := r.db.ExecContext(
		ctx,
		`UPDATE saved_reports
		 SET last_run_at = ?,
		     last_run_status = ?,
		     last_run_row_count = ?,
		     last_run_total_unblended_cost_micros = ?,
		     last_run_error = ?,
		     updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')
		 WHERE id = ?`,
		runAt,
		request.Status,
		request.RowCount,
		request.TotalUnblendedCostMicros,
		request.ErrorMessage,
		request.ID,
	)
	if err != nil {
		return SavedReport{}, fmt.Errorf("record saved report run %q: %w", request.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return SavedReport{}, fmt.Errorf("read saved report run update result for %q: %w", request.ID, err)
	}
	if affected == 0 {
		return SavedReport{}, fmt.Errorf("saved report %q not found", request.ID)
	}
	return r.Get(ctx, request.ID)
}

// Get reads one saved report by ID.
func (r SavedReportRepository) Get(ctx context.Context, id string) (SavedReport, error) {
	if r.db == nil {
		return SavedReport{}, fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return SavedReport{}, fmt.Errorf("saved report ID is required")
	}

	row := r.db.QueryRowContext(ctx, savedReportSelectSQL+` WHERE id = ?`, id)
	report, err := scanSavedReport(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return SavedReport{}, fmt.Errorf("saved report %q not found", id)
		}
		return SavedReport{}, fmt.Errorf("get saved report %q: %w", id, err)
	}
	return report, nil
}

// GetForOwner reads one saved report by ID after applying the simulated owner scope.
func (r SavedReportRepository) GetForOwner(ctx context.Context, id, ownerAccountID, ownerRole string) (SavedReport, error) {
	if r.db == nil {
		return SavedReport{}, fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	ownerAccountID = strings.TrimSpace(ownerAccountID)
	ownerRole = strings.TrimSpace(ownerRole)
	if id == "" {
		return SavedReport{}, fmt.Errorf("saved report ID is required")
	}
	if ownerAccountID == "" {
		return SavedReport{}, fmt.Errorf("saved report owner account ID is required")
	}
	if err := validateSavedReportOwnerRole(ownerRole); err != nil {
		return SavedReport{}, err
	}

	row := r.db.QueryRowContext(
		ctx,
		savedReportSelectSQL+`
		 WHERE id = ?
		   AND owner_account_id = ?
		   AND owner_role = ?`,
		id,
		ownerAccountID,
		ownerRole,
	)
	report, err := scanSavedReport(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return SavedReport{}, fmt.Errorf("saved report %q not found for owner %q/%q", id, ownerRole, ownerAccountID)
		}
		return SavedReport{}, fmt.Errorf("get saved report %q for owner %q/%q: %w", id, ownerRole, ownerAccountID, err)
	}
	return report, nil
}

// GetByName reads one saved report for an owner by case-insensitive report name.
func (r SavedReportRepository) GetByName(ctx context.Context, ownerAccountID, name string) (SavedReport, error) {
	if r.db == nil {
		return SavedReport{}, fmt.Errorf("database handle is required")
	}
	ownerAccountID = strings.TrimSpace(ownerAccountID)
	name = strings.TrimSpace(name)
	if ownerAccountID == "" {
		return SavedReport{}, fmt.Errorf("saved report owner account ID is required")
	}
	if name == "" {
		return SavedReport{}, fmt.Errorf("saved report name is required")
	}

	row := r.db.QueryRowContext(
		ctx,
		savedReportSelectSQL+`
		 WHERE owner_account_id = ?
		   AND lower(name) = lower(?)`,
		ownerAccountID,
		name,
	)
	report, err := scanSavedReport(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return SavedReport{}, fmt.Errorf("saved report %q for owner %q not found", name, ownerAccountID)
		}
		return SavedReport{}, fmt.Errorf("get saved report %q for owner %q: %w", name, ownerAccountID, err)
	}
	return report, nil
}

// List returns saved reports in stable newest-first order, optionally filtered by owner.
func (r SavedReportRepository) List(ctx context.Context, request SavedReportListRequest) ([]SavedReport, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeSavedReportListRequest(request)
	if request.Limit <= 0 {
		request.Limit = defaultSavedReportLimit
	}
	if request.Limit > maxSavedReportLimit {
		request.Limit = maxSavedReportLimit
	}
	if request.OwnerRole != "" {
		if err := validateSavedReportOwnerRole(request.OwnerRole); err != nil {
			return nil, err
		}
	}

	query := savedReportSelectSQL
	args := []any{}
	clauses := []string{}
	if request.OwnerAccountID != "" {
		clauses = append(clauses, "owner_account_id = ?")
		args = append(args, request.OwnerAccountID)
	}
	if request.OwnerRole != "" {
		clauses = append(clauses, "owner_role = ?")
		args = append(args, request.OwnerRole)
	}
	if len(clauses) > 0 {
		query += " WHERE " + strings.Join(clauses, " AND ")
	}
	query += `
		 ORDER BY updated_at DESC, lower(name), id DESC
		 LIMIT ?`
	args = append(args, request.Limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list saved reports: %w", err)
	}
	defer rows.Close()

	var reports []SavedReport
	for rows.Next() {
		report, err := scanSavedReport(rows)
		if err != nil {
			return nil, err
		}
		reports = append(reports, report)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate saved reports: %w", err)
	}
	return reports, nil
}

// Delete removes a saved report definition and its latest run metadata.
func (r SavedReportRepository) Delete(ctx context.Context, id string) error {
	if r.db == nil {
		return fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("saved report ID is required")
	}
	result, err := r.db.ExecContext(ctx, `DELETE FROM saved_reports WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete saved report %q: %w", id, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read saved report delete result for %q: %w", id, err)
	}
	if affected == 0 {
		return fmt.Errorf("saved report %q not found", id)
	}
	return nil
}

type savedReportDefinition struct {
	ID             string
	Name           string
	OwnerAccountID string
	OwnerRole      string
	DateRangeStart string
	DateRangeEnd   string
	Granularity    string
	Filters        map[string][]string
	Groupings      []SavedReportGrouping
	Metrics        []string
	ChartType      string
}

const savedReportSelectSQL = `SELECT
	id,
	name,
	description,
	owner_account_id,
	owner_role,
	date_range_start,
	date_range_end,
	granularity,
	filters_json,
	groupings_json,
	metrics_json,
	chart_type,
	last_run_at,
	last_run_status,
	last_run_row_count,
	last_run_total_unblended_cost_micros,
	last_run_error,
	created_at,
	updated_at
FROM saved_reports`

type savedReportScanner interface {
	Scan(dest ...any) error
}

func scanSavedReport(scanner savedReportScanner) (SavedReport, error) {
	var report SavedReport
	var filtersJSON, groupingsJSON, metricsJSON string
	var lastRunAt sql.NullString
	if err := scanner.Scan(
		&report.ID,
		&report.Name,
		&report.Description,
		&report.OwnerAccountID,
		&report.OwnerRole,
		&report.DateRangeStart,
		&report.DateRangeEnd,
		&report.Granularity,
		&filtersJSON,
		&groupingsJSON,
		&metricsJSON,
		&report.ChartType,
		&lastRunAt,
		&report.LastRunStatus,
		&report.LastRunRowCount,
		&report.LastRunTotalUnblendedCostMicros,
		&report.LastRunError,
		&report.CreatedAt,
		&report.UpdatedAt,
	); err != nil {
		return SavedReport{}, err
	}
	var err error
	report.Filters, err = unmarshalSavedReportFilters(filtersJSON)
	if err != nil {
		return SavedReport{}, fmt.Errorf("decode saved report filters for %q: %w", report.ID, err)
	}
	report.Groupings, err = unmarshalSavedReportGroupings(groupingsJSON)
	if err != nil {
		return SavedReport{}, fmt.Errorf("decode saved report groupings for %q: %w", report.ID, err)
	}
	report.Metrics, err = unmarshalSavedReportMetrics(metricsJSON)
	if err != nil {
		return SavedReport{}, fmt.Errorf("decode saved report metrics for %q: %w", report.ID, err)
	}
	report.LastRunAt = nullStringValue(lastRunAt)
	return report, nil
}

func normalizeSavedReportCreateRequest(request SavedReportCreateRequest) SavedReportCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	request.OwnerAccountID = strings.TrimSpace(request.OwnerAccountID)
	request.OwnerRole = normalizeSavedReportDefault(request.OwnerRole, defaultSavedReportOwnerRole)
	request.DateRangeStart = strings.TrimSpace(request.DateRangeStart)
	request.DateRangeEnd = strings.TrimSpace(request.DateRangeEnd)
	request.Granularity = normalizeSavedReportDefault(request.Granularity, defaultSavedReportGranularity)
	request.Filters = normalizeSavedReportFilters(request.Filters)
	request.Groupings = normalizeSavedReportGroupings(request.Groupings)
	request.Metrics = normalizeSavedReportMetrics(request.Metrics)
	request.ChartType = normalizeSavedReportDefault(request.ChartType, defaultSavedReportChartType)
	return request
}

func normalizeSavedReportUpdateRequest(request SavedReportUpdateRequest) SavedReportUpdateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.Name = strings.TrimSpace(request.Name)
	request.Description = strings.TrimSpace(request.Description)
	request.OwnerAccountID = strings.TrimSpace(request.OwnerAccountID)
	request.OwnerRole = normalizeSavedReportDefault(request.OwnerRole, defaultSavedReportOwnerRole)
	request.DateRangeStart = strings.TrimSpace(request.DateRangeStart)
	request.DateRangeEnd = strings.TrimSpace(request.DateRangeEnd)
	request.Granularity = normalizeSavedReportDefault(request.Granularity, defaultSavedReportGranularity)
	request.Filters = normalizeSavedReportFilters(request.Filters)
	request.Groupings = normalizeSavedReportGroupings(request.Groupings)
	request.Metrics = normalizeSavedReportMetrics(request.Metrics)
	request.ChartType = normalizeSavedReportDefault(request.ChartType, defaultSavedReportChartType)
	return request
}

func normalizeSavedReportListRequest(request SavedReportListRequest) SavedReportListRequest {
	request.OwnerAccountID = strings.TrimSpace(request.OwnerAccountID)
	request.OwnerRole = strings.TrimSpace(request.OwnerRole)
	return request
}

func normalizeSavedReportRunUpdate(request SavedReportRunUpdate) SavedReportRunUpdate {
	request.ID = strings.TrimSpace(request.ID)
	request.RunAt = strings.TrimSpace(request.RunAt)
	request.Status = strings.TrimSpace(request.Status)
	request.ErrorMessage = strings.TrimSpace(request.ErrorMessage)
	return request
}

func normalizeSavedReportDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func normalizeSavedReportFilters(filters map[string][]string) map[string][]string {
	normalized := map[string][]string{}
	for key, values := range filters {
		key = strings.TrimSpace(key)
		normalizedValues := make([]string, 0, len(values))
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				normalizedValues = append(normalizedValues, value)
			}
		}
		normalized[key] = normalizedValues
	}
	return normalized
}

func normalizeSavedReportGroupings(groupings []SavedReportGrouping) []SavedReportGrouping {
	normalized := make([]SavedReportGrouping, 0, len(groupings))
	for _, grouping := range groupings {
		normalized = append(normalized, SavedReportGrouping{
			Type: strings.TrimSpace(grouping.Type),
			Key:  strings.TrimSpace(grouping.Key),
		})
	}
	return normalized
}

func normalizeSavedReportMetrics(metrics []string) []string {
	if len(metrics) == 0 {
		return []string{defaultSavedReportMetric}
	}
	normalized := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		metric = strings.TrimSpace(metric)
		if metric != "" {
			normalized = append(normalized, metric)
		}
	}
	if len(normalized) == 0 {
		return []string{defaultSavedReportMetric}
	}
	return normalized
}

func validateSavedReportDefinition(definition savedReportDefinition) error {
	if definition.Name == "" {
		return fmt.Errorf("saved report name is required")
	}
	if definition.OwnerAccountID == "" {
		return fmt.Errorf("saved report owner account ID is required")
	}
	if err := validateSavedReportOwnerRole(definition.OwnerRole); err != nil {
		return err
	}
	if err := validateSavedReportDateRange(definition.DateRangeStart, definition.DateRangeEnd); err != nil {
		return err
	}
	if err := validateSavedReportGranularity(definition.Granularity); err != nil {
		return err
	}
	if err := validateSavedReportFilters(definition.Filters); err != nil {
		return err
	}
	if err := validateSavedReportGroupings(definition.Groupings); err != nil {
		return err
	}
	if err := validateSavedReportMetrics(definition.Metrics); err != nil {
		return err
	}
	return validateSavedReportChartType(definition.ChartType)
}

func validateSavedReportOwnerRole(role string) error {
	switch role {
	case "management-account", "member-account", "finance", "instructor":
		return nil
	default:
		return fmt.Errorf("saved report owner role %q is not supported", role)
	}
}

func validateSavedReportDateRange(startDate, endDate string) error {
	start, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		return fmt.Errorf("saved report date range start must use YYYY-MM-DD: %w", err)
	}
	end, err := time.Parse("2006-01-02", endDate)
	if err != nil {
		return fmt.Errorf("saved report date range end must use YYYY-MM-DD: %w", err)
	}
	if !start.Before(end) {
		return fmt.Errorf("saved report date range start must be before end")
	}
	return nil
}

func validateSavedReportGranularity(granularity string) error {
	switch granularity {
	case "daily", "monthly", "hourly":
		return nil
	default:
		return fmt.Errorf("saved report granularity %q is not supported", granularity)
	}
}

func validateSavedReportFilters(filters map[string][]string) error {
	for key, values := range filters {
		if key == "" {
			return fmt.Errorf("saved report filter key is required")
		}
		if len(values) == 0 {
			return fmt.Errorf("saved report filter %q needs at least one value", key)
		}
		seen := map[string]bool{}
		for _, value := range values {
			if value == "" {
				return fmt.Errorf("saved report filter %q value is required", key)
			}
			if seen[value] {
				return fmt.Errorf("saved report filter %q has duplicate value %q", key, value)
			}
			seen[value] = true
		}
	}
	return nil
}

func validateSavedReportGroupings(groupings []SavedReportGrouping) error {
	if len(groupings) > 2 {
		return fmt.Errorf("saved report supports at most two groupings")
	}
	seen := map[string]bool{}
	for i, grouping := range groupings {
		if grouping.Key == "" {
			return fmt.Errorf("saved report grouping %d key is required", i)
		}
		switch grouping.Type {
		case "dimension", "tag", "cost_category":
		default:
			return fmt.Errorf("saved report grouping %d type %q is not supported", i, grouping.Type)
		}
		identity := grouping.Type + ":" + grouping.Key
		if seen[identity] {
			return fmt.Errorf("saved report grouping %q is duplicated", identity)
		}
		seen[identity] = true
	}
	return nil
}

func validateSavedReportMetrics(metrics []string) error {
	if len(metrics) == 0 {
		return fmt.Errorf("saved report needs at least one metric")
	}
	seen := map[string]bool{}
	for _, metric := range metrics {
		if metric == "" {
			return fmt.Errorf("saved report metric is required")
		}
		switch metric {
		case "unblended_cost", "blended_cost", "amortized_cost", "usage_quantity", "net_cost":
		default:
			return fmt.Errorf("saved report metric %q is not supported", metric)
		}
		if seen[metric] {
			return fmt.Errorf("saved report metric %q is duplicated", metric)
		}
		seen[metric] = true
	}
	return nil
}

func validateSavedReportChartType(chartType string) error {
	switch chartType {
	case "table", "line", "bar", "stacked_bar":
		return nil
	default:
		return fmt.Errorf("saved report chart type %q is not supported", chartType)
	}
}

func validateSavedReportRunUpdate(request SavedReportRunUpdate) error {
	if request.ID == "" {
		return fmt.Errorf("saved report ID is required")
	}
	switch request.Status {
	case savedReportStatusSucceeded, savedReportStatusFailed:
	default:
		return fmt.Errorf("saved report run status %q is not supported", request.Status)
	}
	if request.RowCount < 0 {
		return fmt.Errorf("saved report run row count must be non-negative")
	}
	if request.TotalUnblendedCostMicros < 0 {
		return fmt.Errorf("saved report run total unblended cost must be non-negative")
	}
	return nil
}

func marshalSavedReportDefinitionJSON(filters map[string][]string, groupings []SavedReportGrouping, metrics []string) (string, string, string, error) {
	filtersJSON, err := marshalSavedReportFilters(filters)
	if err != nil {
		return "", "", "", err
	}
	groupingsJSON, err := json.Marshal(groupings)
	if err != nil {
		return "", "", "", fmt.Errorf("marshal saved report groupings: %w", err)
	}
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		return "", "", "", fmt.Errorf("marshal saved report metrics: %w", err)
	}
	return filtersJSON, string(groupingsJSON), string(metricsJSON), nil
}

func marshalSavedReportFilters(filters map[string][]string) (string, error) {
	if len(filters) == 0 {
		return "{}", nil
	}
	encoded, err := json.Marshal(filters)
	if err != nil {
		return "", fmt.Errorf("marshal saved report filters: %w", err)
	}
	return string(encoded), nil
}

func unmarshalSavedReportFilters(raw string) (map[string][]string, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string][]string{}, nil
	}
	var filters map[string][]string
	if err := json.Unmarshal([]byte(raw), &filters); err != nil {
		return nil, err
	}
	if filters == nil {
		return map[string][]string{}, nil
	}
	return filters, nil
}

func unmarshalSavedReportGroupings(raw string) ([]SavedReportGrouping, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var groupings []SavedReportGrouping
	if err := json.Unmarshal([]byte(raw), &groupings); err != nil {
		return nil, err
	}
	return groupings, nil
}

func unmarshalSavedReportMetrics(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var metrics []string
	if err := json.Unmarshal([]byte(raw), &metrics); err != nil {
		return nil, err
	}
	return metrics, nil
}
