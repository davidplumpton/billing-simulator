package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	defaultBudgetAlertNotificationChannel = "in_app"
	defaultBudgetAlertNotificationLimit   = 500
)

// BudgetAlertNotification stores one in-app notification created by a budget threshold crossing.
type BudgetAlertNotification struct {
	ID                     string
	BudgetID               string
	BudgetThresholdID      string
	BudgetName             string
	BillingPeriodStart     string
	BillingPeriodEnd       string
	BudgetAmountMicros     int64
	CurrencyCode           string
	ThresholdType          string
	ThresholdBasisPoints   int
	ThresholdAmountMicros  int64
	SpendMicros            int64
	PercentUsedBasisPoints int64
	LineItemCount          int
	NotificationChannel    string
	Message                string
	FirstTriggeredAt       string
	LastObservedAt         string
	CreatedAt              string
	UpdatedAt              string
}

// BudgetAlertNotificationListRequest selects persisted alert notifications for one budget month.
type BudgetAlertNotificationListRequest struct {
	BillingPeriodStart string
	BillingPeriodEnd   string
	Limit              int
}

// RecordAlertNotifications records in-app notifications for breached budget threshold checks.
func (r BudgetRepository) RecordAlertNotifications(ctx context.Context, evaluations []BudgetEvaluation) ([]BudgetAlertNotification, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	notifications := []BudgetAlertNotification{}
	periodStart := ""
	periodEnd := ""
	for _, evaluation := range evaluations {
		if periodStart == "" && periodEnd == "" {
			periodStart = evaluation.BillingPeriodStart
			periodEnd = evaluation.BillingPeriodEnd
		}
		if evaluation.BillingPeriodStart != periodStart || evaluation.BillingPeriodEnd != periodEnd {
			return nil, fmt.Errorf("budget evaluations must belong to one billing period")
		}
		for _, check := range evaluation.ThresholdChecks {
			if !check.Breached {
				continue
			}
			notification, err := budgetAlertNotificationFromEvaluation(evaluation, check)
			if err != nil {
				return nil, err
			}
			notifications = append(notifications, notification)
		}
	}
	if periodStart == "" || periodEnd == "" {
		return notifications, nil
	}
	if err := validateMonthlyBudgetPeriod(periodStart, periodEnd); err != nil {
		return nil, err
	}

	if len(notifications) > 0 {
		if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
			for _, notification := range notifications {
				if err := upsertBudgetAlertNotification(ctx, tx, notification); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return r.ListAlertNotifications(ctx, BudgetAlertNotificationListRequest{
		BillingPeriodStart: periodStart,
		BillingPeriodEnd:   periodEnd,
	})
}

// ListAlertNotifications returns persisted in-app budget alert notifications for one month.
func (r BudgetRepository) ListAlertNotifications(ctx context.Context, request BudgetAlertNotificationListRequest) ([]BudgetAlertNotification, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	request = normalizeBudgetAlertNotificationListRequest(request)
	if err := validateMonthlyBudgetPeriod(request.BillingPeriodStart, request.BillingPeriodEnd); err != nil {
		return nil, err
	}
	limit := request.Limit
	if limit <= 0 {
		limit = defaultBudgetAlertNotificationLimit
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			budget_id,
			budget_threshold_id,
			budget_name,
			billing_period_start,
			billing_period_end,
			budget_amount_micros,
			currency_code,
			threshold_type,
			threshold_basis_points,
			threshold_amount_micros,
			spend_micros,
			percent_used_basis_points,
			line_item_count,
			notification_channel,
			message,
			first_triggered_at,
			last_observed_at,
			created_at,
			updated_at
		 FROM budget_alert_notifications
		 WHERE billing_period_start = ? AND billing_period_end = ?
		 ORDER BY first_triggered_at DESC, lower(budget_name), threshold_type, threshold_basis_points
		 LIMIT ?`,
		request.BillingPeriodStart,
		request.BillingPeriodEnd,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list budget alert notifications: %w", err)
	}
	defer rows.Close()

	var notifications []BudgetAlertNotification
	for rows.Next() {
		notification, err := scanBudgetAlertNotification(rows)
		if err != nil {
			return nil, err
		}
		notifications = append(notifications, notification)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate budget alert notifications: %w", err)
	}
	return notifications, nil
}

func budgetAlertNotificationFromEvaluation(evaluation BudgetEvaluation, check BudgetThresholdCheck) (BudgetAlertNotification, error) {
	id, err := newRepositoryID("budalert")
	if err != nil {
		return BudgetAlertNotification{}, err
	}
	if strings.TrimSpace(check.ThresholdID) == "" {
		return BudgetAlertNotification{}, fmt.Errorf("budget threshold ID is required for alert notification")
	}
	return BudgetAlertNotification{
		ID:                     id,
		BudgetID:               evaluation.Budget.ID,
		BudgetThresholdID:      check.ThresholdID,
		BudgetName:             evaluation.Budget.Name,
		BillingPeriodStart:     evaluation.BillingPeriodStart,
		BillingPeriodEnd:       evaluation.BillingPeriodEnd,
		BudgetAmountMicros:     evaluation.Budget.BudgetAmountMicros,
		CurrencyCode:           evaluation.CurrencyCode,
		ThresholdType:          check.ThresholdType,
		ThresholdBasisPoints:   check.ThresholdBasisPoints,
		ThresholdAmountMicros:  check.ThresholdAmountMicros,
		SpendMicros:            check.SpendMicros,
		PercentUsedBasisPoints: check.PercentUsedBasisPoints,
		LineItemCount:          evaluation.LineItemCount,
		NotificationChannel:    defaultBudgetAlertNotificationChannel,
		Message:                budgetAlertNotificationMessage(evaluation.Budget.Name, check),
	}, nil
}

func budgetAlertNotificationMessage(budgetName string, check BudgetThresholdCheck) string {
	return fmt.Sprintf("%s threshold crossed for %s at %d basis points", check.ThresholdType, budgetName, check.ThresholdBasisPoints)
}

func upsertBudgetAlertNotification(ctx context.Context, tx *sql.Tx, notification BudgetAlertNotification) error {
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO budget_alert_notifications (
			id,
			budget_id,
			budget_threshold_id,
			budget_name,
			billing_period_start,
			billing_period_end,
			budget_amount_micros,
			currency_code,
			threshold_type,
			threshold_basis_points,
			threshold_amount_micros,
			spend_micros,
			percent_used_basis_points,
			line_item_count,
			notification_channel,
			message
		 ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (budget_threshold_id, billing_period_start, billing_period_end)
		 DO UPDATE SET
			budget_id = excluded.budget_id,
			budget_name = excluded.budget_name,
			budget_amount_micros = excluded.budget_amount_micros,
			currency_code = excluded.currency_code,
			threshold_type = excluded.threshold_type,
			threshold_basis_points = excluded.threshold_basis_points,
			threshold_amount_micros = excluded.threshold_amount_micros,
			spend_micros = excluded.spend_micros,
			percent_used_basis_points = excluded.percent_used_basis_points,
			line_item_count = excluded.line_item_count,
			notification_channel = excluded.notification_channel,
			message = excluded.message,
			last_observed_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
		notification.ID,
		notification.BudgetID,
		notification.BudgetThresholdID,
		notification.BudgetName,
		notification.BillingPeriodStart,
		notification.BillingPeriodEnd,
		notification.BudgetAmountMicros,
		notification.CurrencyCode,
		notification.ThresholdType,
		notification.ThresholdBasisPoints,
		notification.ThresholdAmountMicros,
		notification.SpendMicros,
		notification.PercentUsedBasisPoints,
		notification.LineItemCount,
		notification.NotificationChannel,
		notification.Message,
	); err != nil {
		return fmt.Errorf("record budget alert notification for %q: %w", notification.BudgetName, err)
	}
	return nil
}

func scanBudgetAlertNotification(row budgetAlertNotificationRow) (BudgetAlertNotification, error) {
	var notification BudgetAlertNotification
	if err := row.Scan(
		&notification.ID,
		&notification.BudgetID,
		&notification.BudgetThresholdID,
		&notification.BudgetName,
		&notification.BillingPeriodStart,
		&notification.BillingPeriodEnd,
		&notification.BudgetAmountMicros,
		&notification.CurrencyCode,
		&notification.ThresholdType,
		&notification.ThresholdBasisPoints,
		&notification.ThresholdAmountMicros,
		&notification.SpendMicros,
		&notification.PercentUsedBasisPoints,
		&notification.LineItemCount,
		&notification.NotificationChannel,
		&notification.Message,
		&notification.FirstTriggeredAt,
		&notification.LastObservedAt,
		&notification.CreatedAt,
		&notification.UpdatedAt,
	); err != nil {
		return BudgetAlertNotification{}, fmt.Errorf("scan budget alert notification: %w", err)
	}
	return notification, nil
}

type budgetAlertNotificationRow interface {
	Scan(dest ...any) error
}

func normalizeBudgetAlertNotificationListRequest(request BudgetAlertNotificationListRequest) BudgetAlertNotificationListRequest {
	request.BillingPeriodStart = strings.TrimSpace(request.BillingPeriodStart)
	request.BillingPeriodEnd = strings.TrimSpace(request.BillingPeriodEnd)
	return request
}
