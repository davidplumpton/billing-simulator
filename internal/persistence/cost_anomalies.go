package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	CostAnomalyDimensionService      = "service"
	CostAnomalyDimensionAccount      = "account"
	CostAnomalyDimensionTag          = "tag"
	CostAnomalyDimensionCostCategory = "cost_category"

	CostAnomalySpikeIncrease = "increase"
	CostAnomalySpikeNewSpend = "new_spend"

	DefaultCostAnomalyThresholdBasisPoints     = 20_000
	DefaultCostAnomalyMinimumCurrentCostMicros = 50_000
	DefaultCostAnomalyAlertLimit               = 500
)

// CostAnomalyAlert stores one reviewable cost spike detected between two periods.
type CostAnomalyAlert struct {
	ID                       string
	BillingPeriodStart       string
	BillingPeriodEnd         string
	BaselinePeriodStart      string
	BaselinePeriodEnd        string
	DimensionType            string
	DimensionKey             string
	DimensionValue           string
	DimensionLabel           string
	PayerAccountID           string
	LineItemStatus           string
	CurrencyCode             string
	SpikeKind                string
	CurrentCostMicros        int64
	BaselineCostMicros       int64
	IncreaseCostMicros       int64
	CurrentCostBasisPoints   int64
	CurrentLineItemCount     int
	BaselineLineItemCount    int
	ThresholdBasisPoints     int
	MinimumCurrentCostMicros int64
	Message                  string
	FirstDetectedAt          string
	LastObservedAt           string
	CreatedAt                string
	UpdatedAt                string
}

// CostAnomalyRefreshRequest selects a current period, baseline period, and spike threshold.
type CostAnomalyRefreshRequest struct {
	BillingPeriodStart       string
	BillingPeriodEnd         string
	BaselinePeriodStart      string
	BaselinePeriodEnd        string
	ThresholdBasisPoints     int
	MinimumCurrentCostMicros int64
}

// CostAnomalyRefreshResult reports the anomalies persisted by one refresh.
type CostAnomalyRefreshResult struct {
	BillingPeriodStart  string
	BillingPeriodEnd    string
	BaselinePeriodStart string
	BaselinePeriodEnd   string
	Alerts              []CostAnomalyAlert
}

// CostAnomalyListRequest selects persisted anomaly alerts for display.
type CostAnomalyListRequest struct {
	BillingPeriodStart  string
	BillingPeriodEnd    string
	BaselinePeriodStart string
	BaselinePeriodEnd   string
	Limit               int
}

// CostAnomalyRepository detects and lists cost spikes from persisted billing data.
type CostAnomalyRepository struct {
	db *sql.DB
}

// NewCostAnomalyRepository creates a repository backed by a workspace database.
func NewCostAnomalyRepository(db *sql.DB) CostAnomalyRepository {
	return CostAnomalyRepository{db: db}
}

// RefreshAlerts compares current and baseline period aggregates and stores reviewable alerts.
func (r CostAnomalyRepository) RefreshAlerts(ctx context.Context, request CostAnomalyRefreshRequest) (CostAnomalyRefreshResult, error) {
	if r.db == nil {
		return CostAnomalyRefreshResult{}, fmt.Errorf("database handle is required")
	}
	request = normalizeCostAnomalyRefreshRequest(request)
	if err := validateCostAnomalyRefreshRequest(request); err != nil {
		return CostAnomalyRefreshResult{}, err
	}

	current, err := r.listCostAnomalyAggregates(ctx, request.BillingPeriodStart, request.BillingPeriodEnd)
	if err != nil {
		return CostAnomalyRefreshResult{}, err
	}
	baseline, err := r.listCostAnomalyAggregates(ctx, request.BaselinePeriodStart, request.BaselinePeriodEnd)
	if err != nil {
		return CostAnomalyRefreshResult{}, err
	}
	alerts, err := costAnomalyAlertsFromAggregates(request, current, baseline)
	if err != nil {
		return CostAnomalyRefreshResult{}, err
	}
	if err := r.replaceCostAnomalyAlerts(ctx, request, alerts); err != nil {
		return CostAnomalyRefreshResult{}, err
	}
	persisted, err := r.ListAlerts(ctx, CostAnomalyListRequest{
		BillingPeriodStart:  request.BillingPeriodStart,
		BillingPeriodEnd:    request.BillingPeriodEnd,
		BaselinePeriodStart: request.BaselinePeriodStart,
		BaselinePeriodEnd:   request.BaselinePeriodEnd,
	})
	if err != nil {
		return CostAnomalyRefreshResult{}, err
	}
	return CostAnomalyRefreshResult{
		BillingPeriodStart:  request.BillingPeriodStart,
		BillingPeriodEnd:    request.BillingPeriodEnd,
		BaselinePeriodStart: request.BaselinePeriodStart,
		BaselinePeriodEnd:   request.BaselinePeriodEnd,
		Alerts:              persisted,
	}, nil
}

// ListAlerts returns persisted cost anomaly alerts for the selected period comparison.
func (r CostAnomalyRepository) ListAlerts(ctx context.Context, request CostAnomalyListRequest) ([]CostAnomalyAlert, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeCostAnomalyListRequest(request)
	if err := validateCostAnomalyListRequest(request); err != nil {
		return nil, err
	}
	limit := request.Limit
	if limit <= 0 {
		limit = DefaultCostAnomalyAlertLimit
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			billing_period_start,
			billing_period_end,
			baseline_period_start,
			baseline_period_end,
			dimension_type,
			dimension_key,
			dimension_value,
			dimension_label,
			payer_account_id,
			line_item_status,
			currency_code,
			spike_kind,
			current_cost_micros,
			baseline_cost_micros,
			increase_cost_micros,
			current_cost_basis_points,
			current_line_item_count,
			baseline_line_item_count,
			threshold_basis_points,
			minimum_current_cost_micros,
			message,
			first_detected_at,
			last_observed_at,
			created_at,
			updated_at
		 FROM cost_anomaly_alerts
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND baseline_period_start = ?
		   AND baseline_period_end = ?
		 ORDER BY current_cost_micros DESC, dimension_type, lower(dimension_label)
		 LIMIT ?`,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		request.BaselinePeriodStart,
		request.BaselinePeriodEnd,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list cost anomaly alerts: %w", err)
	}
	defer rows.Close()

	alerts := []CostAnomalyAlert{}
	for rows.Next() {
		alert, err := scanCostAnomalyAlert(rows)
		if err != nil {
			return nil, err
		}
		alerts = append(alerts, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost anomaly alerts: %w", err)
	}
	return alerts, nil
}

type costAnomalyAggregate struct {
	DimensionType  string
	DimensionKey   string
	DimensionValue string
	DimensionLabel string
	PayerAccountID string
	LineItemStatus string
	CurrencyCode   string
	LineItemCount  int
	CostMicros     int64
}

type costAnomalyIdentity struct {
	DimensionType  string
	DimensionKey   string
	DimensionValue string
	PayerAccountID string
	LineItemStatus string
	CurrencyCode   string
}

func (r CostAnomalyRepository) listCostAnomalyAggregates(ctx context.Context, periodStart, periodEnd string) ([]costAnomalyAggregate, error) {
	queries := []struct {
		name string
		sql  string
	}{
		{
			name: CostAnomalyDimensionService,
			sql: `SELECT
					'service',
					service_code,
					service_code,
					service_name,
					payer_account_id,
					line_item_status,
					currency_code,
					COUNT(*),
					COALESCE(SUM(unblended_cost_micros), 0)
				FROM bill_line_items
				WHERE billing_period_start = ?
				  AND billing_period_end = ?
				  AND line_item_type = 'Usage'
				GROUP BY service_code, service_name, payer_account_id, line_item_status, currency_code`,
		},
		{
			name: CostAnomalyDimensionAccount,
			sql: `SELECT
					'account',
					'usage_account_id',
					li.usage_account_id,
					COALESCE(a.name, li.usage_account_id),
					li.payer_account_id,
					li.line_item_status,
					li.currency_code,
					COUNT(*),
					COALESCE(SUM(li.unblended_cost_micros), 0)
				FROM bill_line_items li
				LEFT JOIN accounts a ON a.id = li.usage_account_id
				WHERE li.billing_period_start = ?
				  AND li.billing_period_end = ?
				  AND li.line_item_type = 'Usage'
				GROUP BY li.usage_account_id, a.name, li.payer_account_id, li.line_item_status, li.currency_code`,
		},
		{
			name: CostAnomalyDimensionTag,
			sql: `SELECT
					'tag',
					CAST(tag.key AS TEXT),
					CAST(tag.value AS TEXT),
					CAST(tag.key AS TEXT) || '=' || CAST(tag.value AS TEXT),
					li.payer_account_id,
					li.line_item_status,
					li.currency_code,
					COUNT(*),
					COALESCE(SUM(li.unblended_cost_micros), 0)
				FROM bill_line_items li, json_each(li.tag_snapshot_json) tag
				WHERE li.billing_period_start = ?
				  AND li.billing_period_end = ?
				  AND li.line_item_type = 'Usage'
				  AND TRIM(CAST(tag.key AS TEXT)) <> ''
				  AND TRIM(CAST(tag.value AS TEXT)) <> ''
				GROUP BY CAST(tag.key AS TEXT), CAST(tag.value AS TEXT), li.payer_account_id, li.line_item_status, li.currency_code`,
		},
		{
			name: CostAnomalyDimensionCostCategory,
			sql: `SELECT
					'cost_category',
					cost_category_id,
					assigned_value,
					cost_category_name || '=' || assigned_value,
					payer_account_id,
					line_item_status,
					currency_code,
					COUNT(*),
					COALESCE(SUM(unblended_cost_micros), 0)
				FROM cost_category_line_item_assignments
				WHERE billing_period_start = ?
				  AND billing_period_end = ?
				GROUP BY cost_category_id, cost_category_name, assigned_value, payer_account_id, line_item_status, currency_code`,
		},
	}

	var aggregates []costAnomalyAggregate
	for _, query := range queries {
		rows, err := r.db.QueryContext(ctx, query.sql, periodStart, periodEnd)
		if err != nil {
			return nil, fmt.Errorf("query %s cost anomaly aggregates: %w", query.name, err)
		}
		for rows.Next() {
			var aggregate costAnomalyAggregate
			if err := rows.Scan(
				&aggregate.DimensionType,
				&aggregate.DimensionKey,
				&aggregate.DimensionValue,
				&aggregate.DimensionLabel,
				&aggregate.PayerAccountID,
				&aggregate.LineItemStatus,
				&aggregate.CurrencyCode,
				&aggregate.LineItemCount,
				&aggregate.CostMicros,
			); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("scan %s cost anomaly aggregate: %w", query.name, err)
			}
			aggregate = normalizeCostAnomalyAggregate(aggregate)
			if aggregate.CostMicros > 0 && aggregate.LineItemCount > 0 {
				aggregates = append(aggregates, aggregate)
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("iterate %s cost anomaly aggregates: %w", query.name, err)
		}
		if err := rows.Close(); err != nil {
			return nil, fmt.Errorf("close %s cost anomaly aggregate rows: %w", query.name, err)
		}
	}
	return aggregates, nil
}

func costAnomalyAlertsFromAggregates(request CostAnomalyRefreshRequest, current, baseline []costAnomalyAggregate) ([]CostAnomalyAlert, error) {
	baselineByID := map[costAnomalyIdentity]costAnomalyAggregate{}
	for _, aggregate := range baseline {
		baselineByID[costAnomalyIdentityForAggregate(aggregate)] = aggregate
	}

	alerts := []CostAnomalyAlert{}
	for _, aggregate := range current {
		if aggregate.CostMicros < request.MinimumCurrentCostMicros {
			continue
		}
		baselineAggregate := baselineByID[costAnomalyIdentityForAggregate(aggregate)]
		currentBasisPoints := costAnomalyCurrentBasisPoints(aggregate.CostMicros, baselineAggregate.CostMicros)
		spikeKind := CostAnomalySpikeIncrease
		if baselineAggregate.CostMicros == 0 {
			spikeKind = CostAnomalySpikeNewSpend
		} else if currentBasisPoints < int64(request.ThresholdBasisPoints) || aggregate.CostMicros <= baselineAggregate.CostMicros {
			continue
		}

		id, err := newRepositoryID("anom")
		if err != nil {
			return nil, err
		}
		alert := CostAnomalyAlert{
			ID:                       id,
			BillingPeriodStart:       request.BillingPeriodStart,
			BillingPeriodEnd:         request.BillingPeriodEnd,
			BaselinePeriodStart:      request.BaselinePeriodStart,
			BaselinePeriodEnd:        request.BaselinePeriodEnd,
			DimensionType:            aggregate.DimensionType,
			DimensionKey:             aggregate.DimensionKey,
			DimensionValue:           aggregate.DimensionValue,
			DimensionLabel:           aggregate.DimensionLabel,
			PayerAccountID:           aggregate.PayerAccountID,
			LineItemStatus:           aggregate.LineItemStatus,
			CurrencyCode:             aggregate.CurrencyCode,
			SpikeKind:                spikeKind,
			CurrentCostMicros:        aggregate.CostMicros,
			BaselineCostMicros:       baselineAggregate.CostMicros,
			IncreaseCostMicros:       aggregate.CostMicros - baselineAggregate.CostMicros,
			CurrentCostBasisPoints:   currentBasisPoints,
			CurrentLineItemCount:     aggregate.LineItemCount,
			BaselineLineItemCount:    baselineAggregate.LineItemCount,
			ThresholdBasisPoints:     request.ThresholdBasisPoints,
			MinimumCurrentCostMicros: request.MinimumCurrentCostMicros,
		}
		alert.Message = costAnomalyMessage(alert)
		alerts = append(alerts, alert)
	}

	sort.Slice(alerts, func(i, j int) bool {
		if alerts[i].CurrentCostMicros != alerts[j].CurrentCostMicros {
			return alerts[i].CurrentCostMicros > alerts[j].CurrentCostMicros
		}
		if alerts[i].DimensionType != alerts[j].DimensionType {
			return alerts[i].DimensionType < alerts[j].DimensionType
		}
		return alerts[i].DimensionLabel < alerts[j].DimensionLabel
	})
	return alerts, nil
}

func (r CostAnomalyRepository) replaceCostAnomalyAlerts(ctx context.Context, request CostAnomalyRefreshRequest, alerts []CostAnomalyAlert) error {
	return WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		existing, err := listExistingCostAnomalyAlertIdentities(ctx, tx, request)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM cost_anomaly_alerts
			 WHERE billing_period_start = ?
			   AND billing_period_end = ?
			   AND baseline_period_start = ?
			   AND baseline_period_end = ?`,
			request.BillingPeriodStart,
			request.BillingPeriodEnd,
			request.BaselinePeriodStart,
			request.BaselinePeriodEnd,
		); err != nil {
			return fmt.Errorf("clear cost anomaly alerts: %w", err)
		}
		for _, alert := range alerts {
			if prior, ok := existing[costAnomalyIdentityForAlert(alert)]; ok {
				alert.ID = prior.ID
				alert.FirstDetectedAt = prior.FirstDetectedAt
			}
			if err := insertCostAnomalyAlert(ctx, tx, alert); err != nil {
				return err
			}
		}
		return nil
	})
}

type costAnomalyPriorAlert struct {
	ID              string
	FirstDetectedAt string
}

func listExistingCostAnomalyAlertIdentities(ctx context.Context, tx *sql.Tx, request CostAnomalyRefreshRequest) (map[costAnomalyIdentity]costAnomalyPriorAlert, error) {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT
			id,
			dimension_type,
			dimension_key,
			dimension_value,
			payer_account_id,
			line_item_status,
			currency_code,
			first_detected_at
		 FROM cost_anomaly_alerts
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND baseline_period_start = ?
		   AND baseline_period_end = ?`,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		request.BaselinePeriodStart,
		request.BaselinePeriodEnd,
	)
	if err != nil {
		return nil, fmt.Errorf("list existing cost anomaly alerts: %w", err)
	}
	defer rows.Close()

	existing := map[costAnomalyIdentity]costAnomalyPriorAlert{}
	for rows.Next() {
		var prior costAnomalyPriorAlert
		var identity costAnomalyIdentity
		if err := rows.Scan(
			&prior.ID,
			&identity.DimensionType,
			&identity.DimensionKey,
			&identity.DimensionValue,
			&identity.PayerAccountID,
			&identity.LineItemStatus,
			&identity.CurrencyCode,
			&prior.FirstDetectedAt,
		); err != nil {
			return nil, fmt.Errorf("scan existing cost anomaly alert: %w", err)
		}
		existing[identity] = prior
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate existing cost anomaly alerts: %w", err)
	}
	return existing, nil
}

func insertCostAnomalyAlert(ctx context.Context, tx *sql.Tx, alert CostAnomalyAlert) error {
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO cost_anomaly_alerts (
			id,
			billing_period_start,
			billing_period_end,
			baseline_period_start,
			baseline_period_end,
			dimension_type,
			dimension_key,
			dimension_value,
			dimension_label,
			payer_account_id,
			line_item_status,
			currency_code,
			spike_kind,
			current_cost_micros,
			baseline_cost_micros,
			increase_cost_micros,
			current_cost_basis_points,
			current_line_item_count,
			baseline_line_item_count,
			threshold_basis_points,
			minimum_current_cost_micros,
			message,
			first_detected_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE(NULLIF(?, ''), strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))`,
		alert.ID,
		alert.BillingPeriodStart,
		alert.BillingPeriodEnd,
		alert.BaselinePeriodStart,
		alert.BaselinePeriodEnd,
		alert.DimensionType,
		alert.DimensionKey,
		alert.DimensionValue,
		alert.DimensionLabel,
		alert.PayerAccountID,
		alert.LineItemStatus,
		alert.CurrencyCode,
		alert.SpikeKind,
		alert.CurrentCostMicros,
		alert.BaselineCostMicros,
		alert.IncreaseCostMicros,
		alert.CurrentCostBasisPoints,
		alert.CurrentLineItemCount,
		alert.BaselineLineItemCount,
		alert.ThresholdBasisPoints,
		alert.MinimumCurrentCostMicros,
		alert.Message,
		alert.FirstDetectedAt,
	); err != nil {
		return fmt.Errorf("insert cost anomaly alert for %s %s: %w", alert.DimensionType, alert.DimensionLabel, err)
	}
	return nil
}

func scanCostAnomalyAlert(row interface {
	Scan(dest ...any) error
}) (CostAnomalyAlert, error) {
	var alert CostAnomalyAlert
	if err := row.Scan(
		&alert.ID,
		&alert.BillingPeriodStart,
		&alert.BillingPeriodEnd,
		&alert.BaselinePeriodStart,
		&alert.BaselinePeriodEnd,
		&alert.DimensionType,
		&alert.DimensionKey,
		&alert.DimensionValue,
		&alert.DimensionLabel,
		&alert.PayerAccountID,
		&alert.LineItemStatus,
		&alert.CurrencyCode,
		&alert.SpikeKind,
		&alert.CurrentCostMicros,
		&alert.BaselineCostMicros,
		&alert.IncreaseCostMicros,
		&alert.CurrentCostBasisPoints,
		&alert.CurrentLineItemCount,
		&alert.BaselineLineItemCount,
		&alert.ThresholdBasisPoints,
		&alert.MinimumCurrentCostMicros,
		&alert.Message,
		&alert.FirstDetectedAt,
		&alert.LastObservedAt,
		&alert.CreatedAt,
		&alert.UpdatedAt,
	); err != nil {
		return CostAnomalyAlert{}, fmt.Errorf("scan cost anomaly alert: %w", err)
	}
	return alert, nil
}

func normalizeCostAnomalyRefreshRequest(request CostAnomalyRefreshRequest) CostAnomalyRefreshRequest {
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	request.BaselinePeriodStart = strings.TrimSpace(request.BaselinePeriodStart)
	request.BaselinePeriodEnd = strings.TrimSpace(request.BaselinePeriodEnd)
	if request.BaselinePeriodStart == "" || request.BaselinePeriodEnd == "" {
		request.BaselinePeriodStart, request.BaselinePeriodEnd = defaultCostAnomalyBaselinePeriod(request.BillingPeriodStart, request.BillingPeriodEnd)
	}
	if request.ThresholdBasisPoints <= 0 {
		request.ThresholdBasisPoints = DefaultCostAnomalyThresholdBasisPoints
	}
	if request.MinimumCurrentCostMicros <= 0 {
		request.MinimumCurrentCostMicros = DefaultCostAnomalyMinimumCurrentCostMicros
	}
	return request
}

func normalizeCostAnomalyListRequest(request CostAnomalyListRequest) CostAnomalyListRequest {
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	request.BaselinePeriodStart = strings.TrimSpace(request.BaselinePeriodStart)
	request.BaselinePeriodEnd = strings.TrimSpace(request.BaselinePeriodEnd)
	if request.BaselinePeriodStart == "" || request.BaselinePeriodEnd == "" {
		request.BaselinePeriodStart, request.BaselinePeriodEnd = defaultCostAnomalyBaselinePeriod(request.BillingPeriodStart, request.BillingPeriodEnd)
	}
	if request.Limit <= 0 {
		request.Limit = DefaultCostAnomalyAlertLimit
	}
	return request
}

func validateCostAnomalyRefreshRequest(request CostAnomalyRefreshRequest) error {
	if err := validateCostAnomalyPeriods(request.BillingPeriodStart, request.BillingPeriodEnd, request.BaselinePeriodStart, request.BaselinePeriodEnd); err != nil {
		return err
	}
	if request.ThresholdBasisPoints <= 0 || request.ThresholdBasisPoints > 1_000_000 {
		return fmt.Errorf("cost anomaly threshold percent must be greater than zero and at most 10000%%")
	}
	if request.MinimumCurrentCostMicros < 0 {
		return fmt.Errorf("cost anomaly minimum current cost cannot be negative")
	}
	return nil
}

func validateCostAnomalyListRequest(request CostAnomalyListRequest) error {
	return validateCostAnomalyPeriods(request.BillingPeriodStart, request.BillingPeriodEnd, request.BaselinePeriodStart, request.BaselinePeriodEnd)
}

func validateCostAnomalyPeriods(periodStart, periodEnd, baselineStart, baselineEnd string) error {
	if err := validateBillingPeriodDateRange(periodStart, periodEnd); err != nil {
		return err
	}
	if err := validateBillingPeriodDateRange(baselineStart, baselineEnd); err != nil {
		return fmt.Errorf("baseline %w", err)
	}
	currentStart, _ := time.Parse(time.DateOnly, periodStart)
	previousEnd, _ := time.Parse(time.DateOnly, baselineEnd)
	if previousEnd.After(currentStart) {
		return fmt.Errorf("cost anomaly baseline period must end on or before the billing period start")
	}
	return nil
}

func defaultCostAnomalyBaselinePeriod(periodStart, periodEnd string) (string, string) {
	start, startErr := time.Parse(time.DateOnly, strings.TrimSpace(periodStart))
	end, endErr := time.Parse(time.DateOnly, strings.TrimSpace(periodEnd))
	if startErr != nil || endErr != nil || !start.Before(end) {
		return "", ""
	}
	if start.Day() == 1 && start.AddDate(0, 1, 0).Equal(end) {
		return start.AddDate(0, -1, 0).Format(time.DateOnly), start.Format(time.DateOnly)
	}
	durationDays := int(end.Sub(start).Hours() / 24)
	if durationDays <= 0 {
		durationDays = 1
	}
	return start.AddDate(0, 0, -durationDays).Format(time.DateOnly), start.Format(time.DateOnly)
}

func normalizeCostAnomalyAggregate(aggregate costAnomalyAggregate) costAnomalyAggregate {
	aggregate.DimensionType = strings.TrimSpace(aggregate.DimensionType)
	aggregate.DimensionKey = strings.TrimSpace(aggregate.DimensionKey)
	aggregate.DimensionValue = strings.TrimSpace(aggregate.DimensionValue)
	aggregate.DimensionLabel = strings.TrimSpace(aggregate.DimensionLabel)
	aggregate.PayerAccountID = strings.TrimSpace(aggregate.PayerAccountID)
	aggregate.LineItemStatus = strings.TrimSpace(aggregate.LineItemStatus)
	aggregate.CurrencyCode = strings.TrimSpace(aggregate.CurrencyCode)
	return aggregate
}

func costAnomalyIdentityForAggregate(aggregate costAnomalyAggregate) costAnomalyIdentity {
	return costAnomalyIdentity{
		DimensionType:  aggregate.DimensionType,
		DimensionKey:   aggregate.DimensionKey,
		DimensionValue: aggregate.DimensionValue,
		PayerAccountID: aggregate.PayerAccountID,
		LineItemStatus: aggregate.LineItemStatus,
		CurrencyCode:   aggregate.CurrencyCode,
	}
}

func costAnomalyIdentityForAlert(alert CostAnomalyAlert) costAnomalyIdentity {
	return costAnomalyIdentity{
		DimensionType:  alert.DimensionType,
		DimensionKey:   alert.DimensionKey,
		DimensionValue: alert.DimensionValue,
		PayerAccountID: alert.PayerAccountID,
		LineItemStatus: alert.LineItemStatus,
		CurrencyCode:   alert.CurrencyCode,
	}
}

func costAnomalyCurrentBasisPoints(currentCostMicros, baselineCostMicros int64) int64 {
	if baselineCostMicros <= 0 {
		return 0
	}
	if currentCostMicros > math.MaxInt64/10_000 {
		return math.MaxInt64
	}
	return divideAndRoundMicros(currentCostMicros*10_000, baselineCostMicros)
}

func costAnomalyMessage(alert CostAnomalyAlert) string {
	if alert.SpikeKind == CostAnomalySpikeNewSpend {
		return fmt.Sprintf("%s has new spend with no baseline in the previous period", alert.DimensionLabel)
	}
	return fmt.Sprintf("%s increased to %d basis points of the previous period", alert.DimensionLabel, alert.CurrentCostBasisPoints)
}
