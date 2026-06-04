package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const (
	costAllocationTagTypeUserDefined    = "user-defined"
	costAllocationTagStatusDiscovered   = "discovered"
	costAllocationTagStatusActive       = "active"
	costAllocationTagStatusDeactivated  = "deactivated"
	costAllocationTagActionActivate     = "activate"
	costAllocationTagActionDeactivate   = "deactivate"
	costAllocationTagVisibilityDelay    = 24 * time.Hour
	defaultCostAllocationTagEventSource = "learner"
	defaultCostAllocationTagKeySource   = "system"
)

// CostAllocationTagKey stores billing-side discovery and activation state for one tag key.
type CostAllocationTagKey struct {
	Key                   string
	Type                  string
	FirstSeenAt           string
	LastSeenAt            string
	DiscoveredAt          string
	ActivationStatus      string
	ActivatedAt           string
	DeactivatedAt         string
	CostExplorerVisibleAt string
	CURExportVisibleAt    string
	EventSource           string
	ScenarioRunID         string
	ScenarioEventID       string
	ScenarioEventSequence int
}

// CostAllocationTagInventoryRow stores current active resource coverage for one key/value pair.
type CostAllocationTagInventoryRow struct {
	Key                   string
	Value                 string
	FirstSeenAt           string
	LastSeenAt            string
	ResourceCount         int
	ActivationStatus      string
	CostExplorerVisibleAt string
	CURExportVisibleAt    string
}

// CostAllocationTagActivationEvent records one learner-visible activation or deactivation transition.
type CostAllocationTagActivationEvent struct {
	ID                    string
	Key                   string
	Action                string
	RequestedAt           string
	EffectiveAt           string
	CostExplorerVisibleAt string
	CURExportVisibleAt    string
	EventSource           string
	ScenarioRunID         string
	ScenarioEventID       string
	ScenarioEventSequence int
}

// CostAllocationTagActivationRequest describes an activation lifecycle operation.
type CostAllocationTagActivationRequest struct {
	ID                    string
	Key                   string
	RequestedAt           string
	EventSource           string
	ScenarioRunID         string
	ScenarioEventID       string
	ScenarioEventSequence int
}

// CostAllocationTagRefreshResult summarizes the current billing tag inventory after discovery.
type CostAllocationTagRefreshResult struct {
	DiscoveredKeyCount  int
	InventoryValueCount int
}

// CostAllocationTagRepository manages billing-visible cost allocation tag state.
type CostAllocationTagRepository struct {
	db *sql.DB
}

// NewCostAllocationTagRepository creates a repository backed by a workspace database.
func NewCostAllocationTagRepository(db *sql.DB) CostAllocationTagRepository {
	return CostAllocationTagRepository{db: db}
}

// RefreshDiscoveredTags rebuilds billing tag discovery and key/value inventory from active resource tags.
func (r CostAllocationTagRepository) RefreshDiscoveredTags(ctx context.Context, discoveredAt string) (CostAllocationTagRefreshResult, error) {
	if r.db == nil {
		return CostAllocationTagRefreshResult{}, fmt.Errorf("database handle is required")
	}
	_, discoveredAt, err := normalizedRepositoryTimestamp("cost allocation tag discovery time", discoveredAt)
	if err != nil {
		return CostAllocationTagRefreshResult{}, err
	}

	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO cost_allocation_tag_keys (
				tag_key,
				tag_type,
				first_seen_at,
				last_seen_at,
				discovered_at,
				event_source
			)
			SELECT
				tag_key,
				?,
				MIN(applied_at),
				MAX(applied_at),
				?,
				?
			FROM resource_tags
			WHERE removed_at IS NULL
			GROUP BY tag_key
			ON CONFLICT(tag_key) DO UPDATE SET
				first_seen_at = min(cost_allocation_tag_keys.first_seen_at, excluded.first_seen_at),
				last_seen_at = max(cost_allocation_tag_keys.last_seen_at, excluded.last_seen_at),
				discovered_at = min(cost_allocation_tag_keys.discovered_at, excluded.discovered_at)`,
			costAllocationTagTypeUserDefined,
			discoveredAt,
			defaultCostAllocationTagKeySource,
		); err != nil {
			return fmt.Errorf("refresh discovered cost allocation tag keys: %w", err)
		}

		if _, err := tx.ExecContext(
			ctx,
			`DELETE FROM cost_allocation_tag_inventory
			WHERE NOT EXISTS (
				SELECT 1
				FROM resource_tags rt
				WHERE rt.removed_at IS NULL
				  AND rt.tag_key = cost_allocation_tag_inventory.tag_key
				  AND rt.tag_value = cost_allocation_tag_inventory.tag_value
			)`,
		); err != nil {
			return fmt.Errorf("remove stale cost allocation tag inventory: %w", err)
		}

		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO cost_allocation_tag_inventory (
				tag_key,
				tag_value,
				first_seen_at,
				last_seen_at,
				resource_count
			)
			SELECT
				tag_key,
				tag_value,
				MIN(applied_at),
				MAX(applied_at),
				COUNT(DISTINCT resource_id)
			FROM resource_tags
			WHERE removed_at IS NULL
			GROUP BY tag_key, tag_value
			ON CONFLICT(tag_key, tag_value) DO UPDATE SET
				first_seen_at = min(cost_allocation_tag_inventory.first_seen_at, excluded.first_seen_at),
				last_seen_at = max(cost_allocation_tag_inventory.last_seen_at, excluded.last_seen_at),
				resource_count = excluded.resource_count`,
		); err != nil {
			return fmt.Errorf("refresh cost allocation tag inventory: %w", err)
		}
		return nil
	}); err != nil {
		return CostAllocationTagRefreshResult{}, err
	}

	var result CostAllocationTagRefreshResult
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cost_allocation_tag_keys`).Scan(&result.DiscoveredKeyCount); err != nil {
		return CostAllocationTagRefreshResult{}, fmt.Errorf("count discovered cost allocation tags: %w", err)
	}
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM cost_allocation_tag_inventory`).Scan(&result.InventoryValueCount); err != nil {
		return CostAllocationTagRefreshResult{}, fmt.Errorf("count cost allocation tag inventory: %w", err)
	}
	return result, nil
}

// ActivateTag marks a discovered tag key active and sets delayed billing visibility timestamps.
func (r CostAllocationTagRepository) ActivateTag(ctx context.Context, request CostAllocationTagActivationRequest) (CostAllocationTagKey, error) {
	return r.transitionTag(ctx, request, costAllocationTagActionActivate)
}

// DeactivateTag marks a discovered tag key inactive and records the lifecycle event.
func (r CostAllocationTagRepository) DeactivateTag(ctx context.Context, request CostAllocationTagActivationRequest) (CostAllocationTagKey, error) {
	return r.transitionTag(ctx, request, costAllocationTagActionDeactivate)
}

// ListDiscoveredKeys returns every billing-discovered tag key in stable display order.
func (r CostAllocationTagRepository) ListDiscoveredKeys(ctx context.Context) ([]CostAllocationTagKey, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			tag_key,
			tag_type,
			first_seen_at,
			last_seen_at,
			discovered_at,
			activation_status,
			activated_at,
			deactivated_at,
			cost_explorer_visible_at,
			cur_export_visible_at,
			event_source,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence
		 FROM cost_allocation_tag_keys
		 ORDER BY lower(tag_key), tag_key`,
	)
	if err != nil {
		return nil, fmt.Errorf("list discovered cost allocation tag keys: %w", err)
	}
	defer rows.Close()
	return scanCostAllocationTagKeys(rows)
}

// ListActiveKeys returns tag keys currently activated for cost allocation, including pending visibility.
func (r CostAllocationTagRepository) ListActiveKeys(ctx context.Context) ([]CostAllocationTagKey, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			tag_key,
			tag_type,
			first_seen_at,
			last_seen_at,
			discovered_at,
			activation_status,
			activated_at,
			deactivated_at,
			cost_explorer_visible_at,
			cur_export_visible_at,
			event_source,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence
		 FROM cost_allocation_tag_keys
		 WHERE activation_status = ?
		 ORDER BY lower(tag_key), tag_key`,
		costAllocationTagStatusActive,
	)
	if err != nil {
		return nil, fmt.Errorf("list active cost allocation tag keys: %w", err)
	}
	defer rows.Close()
	return scanCostAllocationTagKeys(rows)
}

// ListBillingVisibleKeys returns active tag keys visible to simulated Cost Explorer at the supplied time.
func (r CostAllocationTagRepository) ListBillingVisibleKeys(ctx context.Context, visibleAt string) ([]CostAllocationTagKey, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	_, visibleAt, err := normalizedRepositoryTimestamp("cost allocation tag visibility time", visibleAt)
	if err != nil {
		return nil, err
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			tag_key,
			tag_type,
			first_seen_at,
			last_seen_at,
			discovered_at,
			activation_status,
			activated_at,
			deactivated_at,
			cost_explorer_visible_at,
			cur_export_visible_at,
			event_source,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence
		 FROM cost_allocation_tag_keys
		 WHERE activation_status = ?
		   AND cost_explorer_visible_at <= ?
		 ORDER BY lower(tag_key), tag_key`,
		costAllocationTagStatusActive,
		visibleAt,
	)
	if err != nil {
		return nil, fmt.Errorf("list billing-visible cost allocation tag keys: %w", err)
	}
	defer rows.Close()
	return scanCostAllocationTagKeys(rows)
}

// ListInventory returns current key/value resource-tag inventory with billing activation state attached.
func (r CostAllocationTagRepository) ListInventory(ctx context.Context) ([]CostAllocationTagInventoryRow, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			i.tag_key,
			i.tag_value,
			i.first_seen_at,
			i.last_seen_at,
			i.resource_count,
			k.activation_status,
			k.cost_explorer_visible_at,
			k.cur_export_visible_at
		 FROM cost_allocation_tag_inventory i
		 JOIN cost_allocation_tag_keys k ON k.tag_key = i.tag_key
		 ORDER BY lower(i.tag_key), i.tag_key, i.resource_count DESC, i.tag_value`,
	)
	if err != nil {
		return nil, fmt.Errorf("list cost allocation tag inventory: %w", err)
	}
	defer rows.Close()

	var inventory []CostAllocationTagInventoryRow
	for rows.Next() {
		var row CostAllocationTagInventoryRow
		var costExplorerVisibleAt, curExportVisibleAt sql.NullString
		if err := rows.Scan(
			&row.Key,
			&row.Value,
			&row.FirstSeenAt,
			&row.LastSeenAt,
			&row.ResourceCount,
			&row.ActivationStatus,
			&costExplorerVisibleAt,
			&curExportVisibleAt,
		); err != nil {
			return nil, fmt.Errorf("scan cost allocation tag inventory: %w", err)
		}
		row.CostExplorerVisibleAt = nullStringValue(costExplorerVisibleAt)
		row.CURExportVisibleAt = nullStringValue(curExportVisibleAt)
		inventory = append(inventory, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost allocation tag inventory: %w", err)
	}
	return inventory, nil
}

// ListActivationEvents returns activation lifecycle events for one tag key, newest first.
func (r CostAllocationTagRepository) ListActivationEvents(ctx context.Context, key string) ([]CostAllocationTagActivationEvent, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("cost allocation tag key is required")
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			tag_key,
			action,
			requested_at,
			effective_at,
			cost_explorer_visible_at,
			cur_export_visible_at,
			event_source,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence
		 FROM cost_allocation_tag_activation_events
		 WHERE tag_key = ?
		 ORDER BY requested_at DESC, id DESC`,
		key,
	)
	if err != nil {
		return nil, fmt.Errorf("list cost allocation tag activation events for %q: %w", key, err)
	}
	defer rows.Close()

	var events []CostAllocationTagActivationEvent
	for rows.Next() {
		event, err := scanCostAllocationTagActivationEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost allocation tag activation events for %q: %w", key, err)
	}
	return events, nil
}

func (r CostAllocationTagRepository) transitionTag(ctx context.Context, request CostAllocationTagActivationRequest, action string) (CostAllocationTagKey, error) {
	if r.db == nil {
		return CostAllocationTagKey{}, fmt.Errorf("database handle is required")
	}
	request = normalizeCostAllocationTagActivationRequest(request)
	if request.EventSource == "" {
		request.EventSource = defaultCostAllocationTagEventSource
	}
	if err := validateCostAllocationTagActivationRequest(request); err != nil {
		return CostAllocationTagKey{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("cat_evt")
		if err != nil {
			return CostAllocationTagKey{}, err
		}
		request.ID = id
	}
	requestedTime, requestedAt, err := normalizedRepositoryTimestamp("cost allocation tag requested_at", request.RequestedAt)
	if err != nil {
		return CostAllocationTagKey{}, err
	}

	effectiveAt := requestedAt
	costExplorerVisibleAt := ""
	curExportVisibleAt := ""
	nextStatus := costAllocationTagStatusDeactivated
	if action == costAllocationTagActionActivate {
		nextStatus = costAllocationTagStatusActive
		visibleAt := requestedTime.Add(costAllocationTagVisibilityDelay).UTC().Format(time.RFC3339)
		costExplorerVisibleAt = visibleAt
		curExportVisibleAt = visibleAt
	} else if action != costAllocationTagActionDeactivate {
		return CostAllocationTagKey{}, fmt.Errorf("unsupported cost allocation tag action %q", action)
	}

	if err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		var currentStatus string
		if err := tx.QueryRowContext(
			ctx,
			`SELECT activation_status FROM cost_allocation_tag_keys WHERE tag_key = ?`,
			request.Key,
		).Scan(&currentStatus); err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("cost allocation tag key %q has not been discovered", request.Key)
			}
			return fmt.Errorf("check cost allocation tag key %q: %w", request.Key, err)
		}
		if action == costAllocationTagActionDeactivate && currentStatus != costAllocationTagStatusActive {
			return fmt.Errorf("cost allocation tag key %q is not active", request.Key)
		}

		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO cost_allocation_tag_activation_events (
				id,
				tag_key,
				action,
				requested_at,
				effective_at,
				cost_explorer_visible_at,
				cur_export_visible_at,
				event_source,
				scenario_run_id,
				scenario_event_id,
				scenario_event_sequence
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			request.ID,
			request.Key,
			action,
			requestedAt,
			effectiveAt,
			nullStringArg(costExplorerVisibleAt),
			nullStringArg(curExportVisibleAt),
			request.EventSource,
			nullStringArg(request.ScenarioRunID),
			nullStringArg(request.ScenarioEventID),
			nullIntArg(request.ScenarioEventSequence),
		); err != nil {
			return fmt.Errorf("insert cost allocation tag %s event for %q: %w", action, request.Key, err)
		}

		if action == costAllocationTagActionActivate {
			_, err = tx.ExecContext(
				ctx,
				`UPDATE cost_allocation_tag_keys
				 SET activation_status = ?,
				     activated_at = ?,
				     deactivated_at = NULL,
				     cost_explorer_visible_at = ?,
				     cur_export_visible_at = ?,
				     event_source = ?,
				     scenario_run_id = ?,
				     scenario_event_id = ?,
				     scenario_event_sequence = ?
				 WHERE tag_key = ?`,
				nextStatus,
				requestedAt,
				costExplorerVisibleAt,
				curExportVisibleAt,
				request.EventSource,
				nullStringArg(request.ScenarioRunID),
				nullStringArg(request.ScenarioEventID),
				nullIntArg(request.ScenarioEventSequence),
				request.Key,
			)
		} else {
			_, err = tx.ExecContext(
				ctx,
				`UPDATE cost_allocation_tag_keys
				 SET activation_status = ?,
				     deactivated_at = ?,
				     cost_explorer_visible_at = NULL,
				     cur_export_visible_at = NULL,
				     event_source = ?,
				     scenario_run_id = ?,
				     scenario_event_id = ?,
				     scenario_event_sequence = ?
				 WHERE tag_key = ?`,
				nextStatus,
				requestedAt,
				request.EventSource,
				nullStringArg(request.ScenarioRunID),
				nullStringArg(request.ScenarioEventID),
				nullIntArg(request.ScenarioEventSequence),
				request.Key,
			)
		}
		if err != nil {
			return fmt.Errorf("update cost allocation tag key %q: %w", request.Key, err)
		}
		return nil
	}); err != nil {
		return CostAllocationTagKey{}, err
	}

	return r.getKey(ctx, request.Key)
}

func (r CostAllocationTagRepository) getKey(ctx context.Context, key string) (CostAllocationTagKey, error) {
	row := r.db.QueryRowContext(
		ctx,
		`SELECT
			tag_key,
			tag_type,
			first_seen_at,
			last_seen_at,
			discovered_at,
			activation_status,
			activated_at,
			deactivated_at,
			cost_explorer_visible_at,
			cur_export_visible_at,
			event_source,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence
		 FROM cost_allocation_tag_keys
		 WHERE tag_key = ?`,
		key,
	)
	return scanCostAllocationTagKey(row)
}

type costAllocationTagKeyRow interface {
	Scan(dest ...any) error
}

func scanCostAllocationTagKeys(rows *sql.Rows) ([]CostAllocationTagKey, error) {
	var keys []CostAllocationTagKey
	for rows.Next() {
		key, err := scanCostAllocationTagKey(rows)
		if err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost allocation tag keys: %w", err)
	}
	return keys, nil
}

func scanCostAllocationTagKey(row costAllocationTagKeyRow) (CostAllocationTagKey, error) {
	var key CostAllocationTagKey
	var activatedAt, deactivatedAt, costExplorerVisibleAt, curExportVisibleAt sql.NullString
	var scenarioRunID, scenarioEventID sql.NullString
	var scenarioEventSequence sql.NullInt64
	if err := row.Scan(
		&key.Key,
		&key.Type,
		&key.FirstSeenAt,
		&key.LastSeenAt,
		&key.DiscoveredAt,
		&key.ActivationStatus,
		&activatedAt,
		&deactivatedAt,
		&costExplorerVisibleAt,
		&curExportVisibleAt,
		&key.EventSource,
		&scenarioRunID,
		&scenarioEventID,
		&scenarioEventSequence,
	); err != nil {
		return CostAllocationTagKey{}, fmt.Errorf("scan cost allocation tag key: %w", err)
	}
	key.ActivatedAt = nullStringValue(activatedAt)
	key.DeactivatedAt = nullStringValue(deactivatedAt)
	key.CostExplorerVisibleAt = nullStringValue(costExplorerVisibleAt)
	key.CURExportVisibleAt = nullStringValue(curExportVisibleAt)
	key.ScenarioRunID = nullStringValue(scenarioRunID)
	key.ScenarioEventID = nullStringValue(scenarioEventID)
	key.ScenarioEventSequence = nullIntValue(scenarioEventSequence)
	return key, nil
}

type costAllocationTagActivationEventRow interface {
	Scan(dest ...any) error
}

func scanCostAllocationTagActivationEvent(row costAllocationTagActivationEventRow) (CostAllocationTagActivationEvent, error) {
	var event CostAllocationTagActivationEvent
	var costExplorerVisibleAt, curExportVisibleAt sql.NullString
	var scenarioRunID, scenarioEventID sql.NullString
	var scenarioEventSequence sql.NullInt64
	if err := row.Scan(
		&event.ID,
		&event.Key,
		&event.Action,
		&event.RequestedAt,
		&event.EffectiveAt,
		&costExplorerVisibleAt,
		&curExportVisibleAt,
		&event.EventSource,
		&scenarioRunID,
		&scenarioEventID,
		&scenarioEventSequence,
	); err != nil {
		return CostAllocationTagActivationEvent{}, fmt.Errorf("scan cost allocation tag activation event: %w", err)
	}
	event.CostExplorerVisibleAt = nullStringValue(costExplorerVisibleAt)
	event.CURExportVisibleAt = nullStringValue(curExportVisibleAt)
	event.ScenarioRunID = nullStringValue(scenarioRunID)
	event.ScenarioEventID = nullStringValue(scenarioEventID)
	event.ScenarioEventSequence = nullIntValue(scenarioEventSequence)
	return event, nil
}

func normalizeCostAllocationTagActivationRequest(request CostAllocationTagActivationRequest) CostAllocationTagActivationRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.Key = strings.TrimSpace(request.Key)
	request.RequestedAt = strings.TrimSpace(request.RequestedAt)
	request.EventSource = strings.TrimSpace(request.EventSource)
	request.ScenarioRunID = strings.TrimSpace(request.ScenarioRunID)
	request.ScenarioEventID = strings.TrimSpace(request.ScenarioEventID)
	return request
}

func validateCostAllocationTagActivationRequest(request CostAllocationTagActivationRequest) error {
	if request.Key == "" {
		return fmt.Errorf("cost allocation tag key is required")
	}
	return validateEventSourceProvenance("cost allocation tag", request.EventSource, request.ScenarioRunID, request.ScenarioEventID, request.ScenarioEventSequence)
}

func normalizedRepositoryTimestamp(label, value string) (time.Time, string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		now := time.Now().UTC()
		return now, now.Format(time.RFC3339), nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, "", fmt.Errorf("%s must use RFC3339: %w", label, err)
	}
	parsed = parsed.UTC()
	return parsed, parsed.Format(time.RFC3339), nil
}
