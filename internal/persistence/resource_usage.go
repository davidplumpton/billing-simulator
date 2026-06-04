package persistence

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	defaultUsageEventLimit = 25
	maxUsageEventLimit     = 100
)

// Resource stores one synthetic billable resource in the learner workspace.
type Resource struct {
	ID                    string
	AccountID             string
	RegionCode            string
	ServiceCode           string
	ResourceType          string
	ResourceName          string
	Status                string
	CreatedAt             string
	StartedAt             string
	StoppedAt             string
	DeletedAt             string
	Attributes            map[string]string
	EventSource           string
	ScenarioRunID         string
	ScenarioEventID       string
	ScenarioEventSequence int
	Notes                 string
}

// ResourceCreateRequest describes a learner-created synthetic resource.
type ResourceCreateRequest struct {
	ID                    string
	AccountID             string
	RegionCode            string
	ServiceCode           string
	ResourceType          string
	ResourceName          string
	Status                string
	StartedAt             string
	StoppedAt             string
	DeletedAt             string
	Attributes            map[string]string
	Tags                  map[string]string
	Notes                 string
	EventSource           string
	ScenarioRunID         string
	ScenarioEventID       string
	ScenarioEventSequence int
}

// ResourceTag stores one active or historical resource tag value.
type ResourceTag struct {
	ID         string
	ResourceID string
	Key        string
	Value      string
	AppliedAt  string
	RemovedAt  string
}

// ResourceTagCreateRequest describes a learner tag added to a resource.
type ResourceTagCreateRequest struct {
	ID                    string
	ResourceID            string
	Key                   string
	Value                 string
	AppliedAt             string
	EventSource           string
	ScenarioRunID         string
	ScenarioEventID       string
	ScenarioEventSequence int
}

// UsageEvent stores one generated billable usage measurement.
type UsageEvent struct {
	ID                    string
	ResourceID            string
	AccountID             string
	ServiceCode           string
	UsageType             string
	Operation             string
	RegionCode            string
	UsageStartTime        string
	UsageEndTime          string
	UsageQuantityMicros   int64
	UsageUnit             string
	Attributes            map[string]string
	TagSnapshot           map[string]string
	EventSource           string
	ScenarioRunID         string
	ScenarioEventID       string
	ScenarioEventSequence int
	CreatedAt             string
}

// UsageEventCreateRequest describes a learner-generated usage event.
type UsageEventCreateRequest struct {
	ID                    string
	ResourceID            string
	ServiceCode           string
	UsageType             string
	Operation             string
	RegionCode            string
	UsageStartTime        string
	UsageEndTime          string
	UsageQuantityMicros   int64
	UsageUnit             string
	Attributes            map[string]string
	EventSource           string
	ScenarioRunID         string
	ScenarioEventID       string
	ScenarioEventSequence int
}

// ResourceSummary combines a resource with its active tags and usage rollup.
type ResourceSummary struct {
	Resource         Resource
	ActiveTags       map[string]string
	UsageEventCount  int
	LastUsageEndTime string
}

// ResourceUsageRepository reads and writes synthetic resources, tags, and usage events.
type ResourceUsageRepository struct {
	db *sql.DB
}

// NewResourceUsageRepository creates a repository backed by a workspace database.
func NewResourceUsageRepository(db *sql.DB) ResourceUsageRepository {
	return ResourceUsageRepository{db: db}
}

// CreateResource creates a learner resource and its initial active tags in one short transaction.
func (r ResourceUsageRepository) CreateResource(ctx context.Context, request ResourceCreateRequest) (Resource, error) {
	if r.db == nil {
		return Resource{}, fmt.Errorf("database handle is required")
	}
	request = normalizeResourceCreateRequest(request)
	if request.EventSource == "" {
		request.EventSource = "learner"
	}
	if err := validateResourceCreateRequest(request); err != nil {
		return Resource{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("res")
		if err != nil {
			return Resource{}, err
		}
		request.ID = id
	}

	attributesJSON, err := marshalStringMap(request.Attributes)
	if err != nil {
		return Resource{}, fmt.Errorf("marshal resource attributes: %w", err)
	}

	var resource Resource
	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(
			ctx,
			`INSERT INTO resources (
				id,
				account_id,
				region_code,
				service_code,
				resource_type,
				resource_name,
				status,
				started_at,
				stopped_at,
				deleted_at,
				attributes_json,
				event_source,
				scenario_run_id,
				scenario_event_id,
				scenario_event_sequence,
				notes
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			request.ID,
			request.AccountID,
			request.RegionCode,
			request.ServiceCode,
			request.ResourceType,
			request.ResourceName,
			request.Status,
			nullStringArg(request.StartedAt),
			nullStringArg(request.StoppedAt),
			nullStringArg(request.DeletedAt),
			attributesJSON,
			request.EventSource,
			nullStringArg(request.ScenarioRunID),
			nullStringArg(request.ScenarioEventID),
			nullIntArg(request.ScenarioEventSequence),
			request.Notes,
		)
		if err != nil {
			return fmt.Errorf("insert resource %q: %w", request.ID, err)
		}

		for key, value := range request.Tags {
			tagID, err := newRepositoryID("tag")
			if err != nil {
				return err
			}
			if err := insertResourceTag(ctx, tx, ResourceTagCreateRequest{
				ID:                    tagID,
				ResourceID:            request.ID,
				Key:                   key,
				Value:                 value,
				AppliedAt:             request.StartedAt,
				EventSource:           request.EventSource,
				ScenarioRunID:         request.ScenarioRunID,
				ScenarioEventID:       request.ScenarioEventID,
				ScenarioEventSequence: request.ScenarioEventSequence,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Resource{}, err
	}

	resource, err = r.GetResource(ctx, request.ID)
	if err != nil {
		return Resource{}, err
	}
	return resource, nil
}

// AddTag adds one active learner tag to an existing resource.
func (r ResourceUsageRepository) AddTag(ctx context.Context, request ResourceTagCreateRequest) (ResourceTag, error) {
	if r.db == nil {
		return ResourceTag{}, fmt.Errorf("database handle is required")
	}
	request = normalizeResourceTagCreateRequest(request)
	if request.EventSource == "" {
		request.EventSource = "learner"
	}
	if err := validateResourceTagCreateRequest(request); err != nil {
		return ResourceTag{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("tag")
		if err != nil {
			return ResourceTag{}, err
		}
		request.ID = id
	}

	if _, err := r.GetResource(ctx, request.ResourceID); err != nil {
		return ResourceTag{}, err
	}
	err := WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		return insertResourceTag(ctx, tx, request)
	})
	if err != nil {
		return ResourceTag{}, err
	}

	return r.getTag(ctx, request.ID)
}

// RecordUsageEvent creates a learner usage event from a resource snapshot and tags active at usage start.
func (r ResourceUsageRepository) RecordUsageEvent(ctx context.Context, request UsageEventCreateRequest) (UsageEvent, error) {
	return r.recordUsageEvent(ctx, request, "learner", false)
}

// RecordGeneratedUsageEvent creates or reuses a deterministic generator usage event.
func (r ResourceUsageRepository) RecordGeneratedUsageEvent(ctx context.Context, request UsageEventCreateRequest) (UsageEvent, error) {
	return r.recordUsageEvent(ctx, request, "generator", true)
}

func (r ResourceUsageRepository) recordUsageEvent(ctx context.Context, request UsageEventCreateRequest, eventSource string, ignoreDuplicateID bool) (UsageEvent, error) {
	if r.db == nil {
		return UsageEvent{}, fmt.Errorf("database handle is required")
	}
	request = normalizeUsageEventCreateRequest(request)
	if request.EventSource == "" {
		request.EventSource = eventSource
	}
	if err := validateUsageEventCreateRequest(request); err != nil {
		return UsageEvent{}, err
	}
	if request.ID == "" {
		id, err := newRepositoryID("use")
		if err != nil {
			return UsageEvent{}, err
		}
		request.ID = id
	}

	resource, err := r.GetResource(ctx, request.ResourceID)
	if err != nil {
		return UsageEvent{}, err
	}
	if request.ServiceCode != "" && request.ServiceCode != resource.ServiceCode {
		return UsageEvent{}, fmt.Errorf("usage service %q does not match resource service %q", request.ServiceCode, resource.ServiceCode)
	}
	regionCode := request.RegionCode
	if regionCode == "" {
		regionCode = resource.RegionCode
	}

	attributesJSON, err := marshalStringMap(request.Attributes)
	if err != nil {
		return UsageEvent{}, fmt.Errorf("marshal usage attributes: %w", err)
	}
	tagSnapshot, err := r.tagSnapshotAtUsageStart(ctx, resource.ID, request.UsageStartTime)
	if err != nil {
		return UsageEvent{}, err
	}
	tagSnapshotJSON, err := marshalStringMap(tagSnapshot)
	if err != nil {
		return UsageEvent{}, fmt.Errorf("marshal tag snapshot: %w", err)
	}

	query := `INSERT INTO usage_events (
			id,
			resource_id,
			account_id,
			service_code,
			usage_type,
			operation,
			region_code,
			usage_start_time,
			usage_end_time,
			usage_quantity_micros,
			usage_unit,
			attributes_json,
			tag_snapshot_json,
			event_source,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if ignoreDuplicateID {
		query += `
		ON CONFLICT(id) DO NOTHING`
	}

	_, err = r.db.ExecContext(
		ctx,
		query,
		request.ID,
		resource.ID,
		resource.AccountID,
		resource.ServiceCode,
		request.UsageType,
		request.Operation,
		regionCode,
		request.UsageStartTime,
		request.UsageEndTime,
		request.UsageQuantityMicros,
		request.UsageUnit,
		attributesJSON,
		tagSnapshotJSON,
		request.EventSource,
		nullStringArg(request.ScenarioRunID),
		nullStringArg(request.ScenarioEventID),
		nullIntArg(request.ScenarioEventSequence),
	)
	if err != nil {
		return UsageEvent{}, fmt.Errorf("insert usage event %q: %w", request.ID, err)
	}

	return r.getUsageEvent(ctx, request.ID)
}

// GetResource reads one resource by ID.
func (r ResourceUsageRepository) GetResource(ctx context.Context, id string) (Resource, error) {
	if r.db == nil {
		return Resource{}, fmt.Errorf("database handle is required")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Resource{}, fmt.Errorf("resource ID is required")
	}

	var resource Resource
	var startedAt, stoppedAt, deletedAt, scenarioRunID, scenarioEventID sql.NullString
	var scenarioEventSequence sql.NullInt64
	var attributesJSON string
	err := r.db.QueryRowContext(
		ctx,
		`SELECT
			id,
			account_id,
			region_code,
			service_code,
			resource_type,
			resource_name,
			status,
			created_at,
			started_at,
			stopped_at,
			deleted_at,
			attributes_json,
			event_source,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence,
			notes
		 FROM resources
		 WHERE id = ?`,
		id,
	).Scan(
		&resource.ID,
		&resource.AccountID,
		&resource.RegionCode,
		&resource.ServiceCode,
		&resource.ResourceType,
		&resource.ResourceName,
		&resource.Status,
		&resource.CreatedAt,
		&startedAt,
		&stoppedAt,
		&deletedAt,
		&attributesJSON,
		&resource.EventSource,
		&scenarioRunID,
		&scenarioEventID,
		&scenarioEventSequence,
		&resource.Notes,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return Resource{}, fmt.Errorf("resource %q not found", id)
		}
		return Resource{}, fmt.Errorf("get resource %q: %w", id, err)
	}
	resource.StartedAt = nullStringValue(startedAt)
	resource.StoppedAt = nullStringValue(stoppedAt)
	resource.DeletedAt = nullStringValue(deletedAt)
	resource.ScenarioRunID = nullStringValue(scenarioRunID)
	resource.ScenarioEventID = nullStringValue(scenarioEventID)
	resource.ScenarioEventSequence = nullIntValue(scenarioEventSequence)
	resource.Attributes, err = unmarshalStringMap(attributesJSON)
	if err != nil {
		return Resource{}, fmt.Errorf("decode resource attributes for %q: %w", id, err)
	}
	return resource, nil
}

// ListResources reads resources in deterministic newest-first order with active tags.
func (r ResourceUsageRepository) ListResources(ctx context.Context) ([]ResourceSummary, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			r.id,
			r.account_id,
			r.region_code,
			r.service_code,
			r.resource_type,
			r.resource_name,
			r.status,
			r.created_at,
			r.started_at,
			r.stopped_at,
			r.deleted_at,
			r.attributes_json,
			r.event_source,
			r.notes,
			(SELECT COUNT(*) FROM usage_events u WHERE u.resource_id = r.id) AS usage_event_count,
			COALESCE((SELECT MAX(u.usage_end_time) FROM usage_events u WHERE u.resource_id = r.id), '') AS last_usage_end_time
		 FROM resources r
		 ORDER BY r.created_at DESC, r.id DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	defer rows.Close()

	var summaries []ResourceSummary
	for rows.Next() {
		var summary ResourceSummary
		var startedAt, stoppedAt, deletedAt sql.NullString
		var attributesJSON string
		if err := rows.Scan(
			&summary.Resource.ID,
			&summary.Resource.AccountID,
			&summary.Resource.RegionCode,
			&summary.Resource.ServiceCode,
			&summary.Resource.ResourceType,
			&summary.Resource.ResourceName,
			&summary.Resource.Status,
			&summary.Resource.CreatedAt,
			&startedAt,
			&stoppedAt,
			&deletedAt,
			&attributesJSON,
			&summary.Resource.EventSource,
			&summary.Resource.Notes,
			&summary.UsageEventCount,
			&summary.LastUsageEndTime,
		); err != nil {
			return nil, fmt.Errorf("scan resource summary: %w", err)
		}
		summary.Resource.StartedAt = nullStringValue(startedAt)
		summary.Resource.StoppedAt = nullStringValue(stoppedAt)
		summary.Resource.DeletedAt = nullStringValue(deletedAt)
		summary.Resource.Attributes, err = unmarshalStringMap(attributesJSON)
		if err != nil {
			return nil, fmt.Errorf("decode resource attributes for %q: %w", summary.Resource.ID, err)
		}
		summary.ActiveTags, err = r.activeTags(ctx, summary.Resource.ID)
		if err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resource summaries: %w", err)
	}
	return summaries, nil
}

// ListUsageEvents reads recent usage events in deterministic newest-first order.
func (r ResourceUsageRepository) ListUsageEvents(ctx context.Context, limit int) ([]UsageEvent, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	if limit <= 0 {
		limit = defaultUsageEventLimit
	}
	if limit > maxUsageEventLimit {
		limit = maxUsageEventLimit
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			resource_id,
			account_id,
			service_code,
			usage_type,
			operation,
			region_code,
			usage_start_time,
			usage_end_time,
			usage_quantity_micros,
			usage_unit,
			attributes_json,
			tag_snapshot_json,
			event_source,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence,
			created_at
		 FROM usage_events
		 ORDER BY usage_start_time DESC, id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list usage events: %w", err)
	}
	defer rows.Close()

	var events []UsageEvent
	for rows.Next() {
		event, err := scanUsageEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate usage events: %w", err)
	}
	return events, nil
}

func (r ResourceUsageRepository) getTag(ctx context.Context, id string) (ResourceTag, error) {
	var tag ResourceTag
	var removedAt sql.NullString
	err := r.db.QueryRowContext(
		ctx,
		`SELECT id, resource_id, tag_key, tag_value, applied_at, removed_at
		 FROM resource_tags
		 WHERE id = ?`,
		id,
	).Scan(&tag.ID, &tag.ResourceID, &tag.Key, &tag.Value, &tag.AppliedAt, &removedAt)
	if err != nil {
		return ResourceTag{}, fmt.Errorf("get resource tag %q: %w", id, err)
	}
	tag.RemovedAt = nullStringValue(removedAt)
	return tag, nil
}

func (r ResourceUsageRepository) getUsageEvent(ctx context.Context, id string) (UsageEvent, error) {
	row := r.db.QueryRowContext(
		ctx,
		`SELECT
			id,
			resource_id,
			account_id,
			service_code,
			usage_type,
			operation,
			region_code,
			usage_start_time,
			usage_end_time,
			usage_quantity_micros,
			usage_unit,
			attributes_json,
			tag_snapshot_json,
			event_source,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence,
			created_at
		 FROM usage_events
		 WHERE id = ?`,
		id,
	)
	event, err := scanUsageEvent(row)
	if err != nil {
		return UsageEvent{}, fmt.Errorf("get usage event %q: %w", id, err)
	}
	return event, nil
}

func (r ResourceUsageRepository) activeTags(ctx context.Context, resourceID string) (map[string]string, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT tag_key, tag_value
		 FROM resource_tags
		 WHERE resource_id = ? AND removed_at IS NULL
		 ORDER BY tag_key`,
		resourceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list active tags for resource %q: %w", resourceID, err)
	}
	defer rows.Close()

	tags := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("scan active tag for resource %q: %w", resourceID, err)
		}
		tags[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active tags for resource %q: %w", resourceID, err)
	}
	return tags, nil
}

// tagSnapshotAtUsageStart returns tags that were active when a usage window began.
func (r ResourceUsageRepository) tagSnapshotAtUsageStart(ctx context.Context, resourceID, usageStartTime string) (map[string]string, error) {
	usageStart, err := time.Parse(time.RFC3339, usageStartTime)
	if err != nil {
		return nil, fmt.Errorf("parse usage start time for tag snapshot: %w", err)
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT tag_key, tag_value, applied_at, removed_at
		 FROM resource_tags
		 WHERE resource_id = ?
		 ORDER BY tag_key, applied_at, id`,
		resourceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list tag history for resource %q: %w", resourceID, err)
	}
	defer rows.Close()

	tags := map[string]string{}
	for rows.Next() {
		var key, value, appliedAtRaw string
		var removedAtRaw sql.NullString
		if err := rows.Scan(&key, &value, &appliedAtRaw, &removedAtRaw); err != nil {
			return nil, fmt.Errorf("scan tag history for resource %q: %w", resourceID, err)
		}
		appliedAt, err := time.Parse(time.RFC3339, appliedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("parse applied_at for resource tag %q on resource %q: %w", key, resourceID, err)
		}
		if appliedAt.After(usageStart) {
			continue
		}
		if removedAtRaw.Valid {
			removedAt, err := time.Parse(time.RFC3339, removedAtRaw.String)
			if err != nil {
				return nil, fmt.Errorf("parse removed_at for resource tag %q on resource %q: %w", key, resourceID, err)
			}
			if !removedAt.After(usageStart) {
				continue
			}
		}
		tags[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tag history for resource %q: %w", resourceID, err)
	}
	return tags, nil
}

func insertResourceTag(ctx context.Context, tx *sql.Tx, request ResourceTagCreateRequest) error {
	appliedAt := nullStringArg(request.AppliedAt)
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO resource_tags (
			id,
			resource_id,
			tag_key,
			tag_value,
			applied_at,
			event_source,
			scenario_run_id,
			scenario_event_id,
			scenario_event_sequence
		) VALUES (?, ?, ?, ?, COALESCE(?, strftime('%Y-%m-%dT%H:%M:%fZ', 'now')), ?, ?, ?, ?)`,
		request.ID,
		request.ResourceID,
		request.Key,
		request.Value,
		appliedAt,
		request.EventSource,
		nullStringArg(request.ScenarioRunID),
		nullStringArg(request.ScenarioEventID),
		nullIntArg(request.ScenarioEventSequence),
	)
	if err != nil {
		return fmt.Errorf("insert resource tag %q for resource %q: %w", request.Key, request.ResourceID, err)
	}
	return nil
}

type usageEventRow interface {
	Scan(dest ...any) error
}

func scanUsageEvent(row usageEventRow) (UsageEvent, error) {
	var event UsageEvent
	var attributesJSON, tagSnapshotJSON string
	var scenarioRunID, scenarioEventID sql.NullString
	var scenarioEventSequence sql.NullInt64
	if err := row.Scan(
		&event.ID,
		&event.ResourceID,
		&event.AccountID,
		&event.ServiceCode,
		&event.UsageType,
		&event.Operation,
		&event.RegionCode,
		&event.UsageStartTime,
		&event.UsageEndTime,
		&event.UsageQuantityMicros,
		&event.UsageUnit,
		&attributesJSON,
		&tagSnapshotJSON,
		&event.EventSource,
		&scenarioRunID,
		&scenarioEventID,
		&scenarioEventSequence,
		&event.CreatedAt,
	); err != nil {
		return UsageEvent{}, fmt.Errorf("scan usage event: %w", err)
	}

	var err error
	event.Attributes, err = unmarshalStringMap(attributesJSON)
	if err != nil {
		return UsageEvent{}, fmt.Errorf("decode usage attributes for %q: %w", event.ID, err)
	}
	event.TagSnapshot, err = unmarshalStringMap(tagSnapshotJSON)
	if err != nil {
		return UsageEvent{}, fmt.Errorf("decode usage tag snapshot for %q: %w", event.ID, err)
	}
	event.ScenarioRunID = nullStringValue(scenarioRunID)
	event.ScenarioEventID = nullStringValue(scenarioEventID)
	event.ScenarioEventSequence = nullIntValue(scenarioEventSequence)
	return event, nil
}

func normalizeResourceCreateRequest(request ResourceCreateRequest) ResourceCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.AccountID = strings.TrimSpace(request.AccountID)
	request.RegionCode = strings.TrimSpace(request.RegionCode)
	request.ServiceCode = strings.TrimSpace(request.ServiceCode)
	request.ResourceType = strings.TrimSpace(request.ResourceType)
	request.ResourceName = strings.TrimSpace(request.ResourceName)
	request.Status = strings.TrimSpace(request.Status)
	request.StartedAt = strings.TrimSpace(request.StartedAt)
	request.StoppedAt = strings.TrimSpace(request.StoppedAt)
	request.DeletedAt = strings.TrimSpace(request.DeletedAt)
	request.Notes = strings.TrimSpace(request.Notes)
	request.EventSource = strings.TrimSpace(request.EventSource)
	request.ScenarioRunID = strings.TrimSpace(request.ScenarioRunID)
	request.ScenarioEventID = strings.TrimSpace(request.ScenarioEventID)
	if request.Status == "" {
		request.Status = "active"
	}
	request.Attributes = normalizeStringMap(request.Attributes)
	request.Tags = normalizeStringMap(request.Tags)
	return request
}

func normalizeResourceTagCreateRequest(request ResourceTagCreateRequest) ResourceTagCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.ResourceID = strings.TrimSpace(request.ResourceID)
	request.Key = strings.TrimSpace(request.Key)
	request.Value = strings.TrimSpace(request.Value)
	request.AppliedAt = strings.TrimSpace(request.AppliedAt)
	request.EventSource = strings.TrimSpace(request.EventSource)
	request.ScenarioRunID = strings.TrimSpace(request.ScenarioRunID)
	request.ScenarioEventID = strings.TrimSpace(request.ScenarioEventID)
	return request
}

func normalizeUsageEventCreateRequest(request UsageEventCreateRequest) UsageEventCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.ResourceID = strings.TrimSpace(request.ResourceID)
	request.ServiceCode = strings.TrimSpace(request.ServiceCode)
	request.UsageType = strings.TrimSpace(request.UsageType)
	request.Operation = strings.TrimSpace(request.Operation)
	request.RegionCode = strings.TrimSpace(request.RegionCode)
	request.UsageStartTime = strings.TrimSpace(request.UsageStartTime)
	request.UsageEndTime = strings.TrimSpace(request.UsageEndTime)
	request.UsageUnit = strings.TrimSpace(request.UsageUnit)
	request.EventSource = strings.TrimSpace(request.EventSource)
	request.ScenarioRunID = strings.TrimSpace(request.ScenarioRunID)
	request.ScenarioEventID = strings.TrimSpace(request.ScenarioEventID)
	request.Attributes = normalizeStringMap(request.Attributes)
	return request
}

func validateResourceCreateRequest(request ResourceCreateRequest) error {
	if request.AccountID == "" {
		return fmt.Errorf("resource account ID is required")
	}
	if request.RegionCode == "" {
		return fmt.Errorf("resource region code is required")
	}
	if request.ServiceCode == "" {
		return fmt.Errorf("resource service code is required")
	}
	if request.ResourceType == "" {
		return fmt.Errorf("resource type is required")
	}
	if err := validateResourceStatus(request.Status); err != nil {
		return err
	}
	if err := validateOptionalTimestamp("resource started_at", request.StartedAt); err != nil {
		return err
	}
	if err := validateOptionalTimestamp("resource stopped_at", request.StoppedAt); err != nil {
		return err
	}
	if err := validateOptionalTimestamp("resource deleted_at", request.DeletedAt); err != nil {
		return err
	}
	if err := validateEventSourceProvenance("resource", request.EventSource, request.ScenarioRunID, request.ScenarioEventID, request.ScenarioEventSequence); err != nil {
		return err
	}
	return validateStringMap("resource tag", request.Tags)
}

func validateResourceTagCreateRequest(request ResourceTagCreateRequest) error {
	if request.ResourceID == "" {
		return fmt.Errorf("resource tag resource ID is required")
	}
	if request.Key == "" {
		return fmt.Errorf("resource tag key is required")
	}
	if err := validateEventSourceProvenance("resource tag", request.EventSource, request.ScenarioRunID, request.ScenarioEventID, request.ScenarioEventSequence); err != nil {
		return err
	}
	return validateOptionalTimestamp("resource tag applied_at", request.AppliedAt)
}

func validateUsageEventCreateRequest(request UsageEventCreateRequest) error {
	if request.ResourceID == "" {
		return fmt.Errorf("usage event resource ID is required")
	}
	if request.UsageType == "" {
		return fmt.Errorf("usage event usage type is required")
	}
	if request.Operation == "" {
		return fmt.Errorf("usage event operation is required")
	}
	if request.UsageStartTime == "" {
		return fmt.Errorf("usage event start time is required")
	}
	if request.UsageEndTime == "" {
		return fmt.Errorf("usage event end time is required")
	}
	start, err := time.Parse(time.RFC3339, request.UsageStartTime)
	if err != nil {
		return fmt.Errorf("usage event start time must use RFC3339: %w", err)
	}
	end, err := time.Parse(time.RFC3339, request.UsageEndTime)
	if err != nil {
		return fmt.Errorf("usage event end time must use RFC3339: %w", err)
	}
	if !start.Before(end) {
		return fmt.Errorf("usage event start time must be before end time")
	}
	if request.UsageQuantityMicros <= 0 {
		return fmt.Errorf("usage event quantity must be greater than zero")
	}
	if request.UsageUnit == "" {
		return fmt.Errorf("usage event unit is required")
	}
	if err := validateEventSourceProvenance("usage event", request.EventSource, request.ScenarioRunID, request.ScenarioEventID, request.ScenarioEventSequence); err != nil {
		return err
	}
	return nil
}

func validateEventSourceProvenance(label, eventSource, scenarioRunID, scenarioEventID string, scenarioEventSequence int) error {
	switch eventSource {
	case "learner", "scenario", "generator", "system":
	default:
		return fmt.Errorf("%s event source %q is not supported", label, eventSource)
	}
	if eventSource == "scenario" {
		if scenarioRunID == "" {
			return fmt.Errorf("%s scenario run ID is required for scenario event source", label)
		}
		if scenarioEventID == "" {
			return fmt.Errorf("%s scenario event ID is required for scenario event source", label)
		}
		if scenarioEventSequence <= 0 {
			return fmt.Errorf("%s scenario event sequence is required for scenario event source", label)
		}
		return nil
	}
	if scenarioRunID != "" || scenarioEventID != "" || scenarioEventSequence != 0 {
		return fmt.Errorf("%s scenario provenance requires scenario event source", label)
	}
	return nil
}

func validateResourceStatus(status string) error {
	switch status {
	case "planned", "active", "stopped", "deleted":
		return nil
	default:
		return fmt.Errorf("unsupported resource status %q", status)
	}
}

func validateOptionalTimestamp(label, value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return fmt.Errorf("%s must use RFC3339: %w", label, err)
	}
	return nil
}

func validateStringMap(label string, values map[string]string) error {
	for key := range values {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("%s key is required", label)
		}
	}
	return nil
}

func normalizeStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	normalized := map[string]string{}
	for key, value := range values {
		key = strings.TrimSpace(key)
		normalized[key] = strings.TrimSpace(value)
	}
	return normalized
}

func marshalStringMap(values map[string]string) (string, error) {
	if len(values) == 0 {
		return "{}", nil
	}
	if err := validateStringMap("JSON object", values); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func unmarshalStringMap(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]string{}, nil
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, err
	}
	if values == nil {
		return map[string]string{}, nil
	}
	return values, nil
}

func nullStringArg(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullStringValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func nullIntArg(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullIntValue(value sql.NullInt64) int {
	if !value.Valid {
		return 0
	}
	return int(value.Int64)
}

func newRepositoryID(prefix string) (string, error) {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", fmt.Errorf("generate %s ID: %w", prefix, err)
	}
	return prefix + "_" + hex.EncodeToString(bytes[:]), nil
}
