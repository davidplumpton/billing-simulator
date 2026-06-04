package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const (
	defaultMeteringRecordLimit = 25
	maxMeteringRecordLimit     = 100
)

// MeteringRecord stores the normalized billable dimensions derived from one usage event.
type MeteringRecord struct {
	ID                  string
	UsageEventID        string
	ResourceID          string
	AccountID           string
	ServiceCode         string
	UsageType           string
	Operation           string
	RegionCode          string
	UsageStartTime      string
	UsageEndTime        string
	UsageQuantityMicros int64
	UsageUnit           string
	TagSnapshot         map[string]string
	CreatedAt           string
}

// MeteringGenerationResult reports the records created during one metering run.
type MeteringGenerationResult struct {
	RecordsCreated int
	Records        []MeteringRecord
}

// MeteringRepository converts usage events into normalized metering records.
type MeteringRepository struct {
	db *sql.DB
}

// NewMeteringRepository creates a repository backed by a workspace database.
func NewMeteringRepository(db *sql.DB) MeteringRepository {
	return MeteringRepository{db: db}
}

// GenerateMeteringRecords creates one stable metering record for every unmetered usage event.
func (r MeteringRepository) GenerateMeteringRecords(ctx context.Context) (MeteringGenerationResult, error) {
	return r.generateMeteringRecords(ctx, "")
}

// GenerateMeteringRecordsThrough creates metering records for usage that has ended by the given UTC time.
func (r MeteringRepository) GenerateMeteringRecordsThrough(ctx context.Context, throughTime string) (MeteringGenerationResult, error) {
	throughTime = strings.TrimSpace(throughTime)
	if throughTime != "" {
		parsed, err := time.Parse(time.RFC3339, throughTime)
		if err != nil {
			return MeteringGenerationResult{}, fmt.Errorf("metering through time must use RFC3339: %w", err)
		}
		throughTime = parsed.UTC().Format(time.RFC3339)
	}
	return r.generateMeteringRecords(ctx, throughTime)
}

func (r MeteringRepository) generateMeteringRecords(ctx context.Context, throughTime string) (MeteringGenerationResult, error) {
	if r.db == nil {
		return MeteringGenerationResult{}, fmt.Errorf("database handle is required")
	}

	events, err := r.listUnmeteredUsageEvents(ctx, throughTime)
	if err != nil {
		return MeteringGenerationResult{}, err
	}
	result := MeteringGenerationResult{
		Records: make([]MeteringRecord, 0, len(events)),
	}
	if len(events) == 0 {
		return result, nil
	}

	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		for _, event := range events {
			record, err := meteringRecordFromUsageEvent(event)
			if err != nil {
				return err
			}
			inserted, err := insertMeteringRecord(ctx, tx, record)
			if err != nil {
				return err
			}
			if inserted {
				result.RecordsCreated++
				result.Records = append(result.Records, record)
			}
		}
		return nil
	})
	if err != nil {
		return MeteringGenerationResult{}, err
	}
	return result, nil
}

// ListMeteringRecords reads recent metering records in deterministic newest-first order.
func (r MeteringRepository) ListMeteringRecords(ctx context.Context, limit int) ([]MeteringRecord, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	if limit <= 0 {
		limit = defaultMeteringRecordLimit
	}
	if limit > maxMeteringRecordLimit {
		limit = maxMeteringRecordLimit
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			usage_event_id,
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
			tag_snapshot_json,
			created_at
		 FROM metering_records
		 ORDER BY usage_start_time DESC, id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list metering records: %w", err)
	}
	defer rows.Close()

	var records []MeteringRecord
	for rows.Next() {
		record, err := scanMeteringRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate metering records: %w", err)
	}
	return records, nil
}

func (r MeteringRepository) listUnmeteredUsageEvents(ctx context.Context, throughTime string) ([]UsageEvent, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			u.id,
			u.resource_id,
			u.account_id,
			u.service_code,
			u.usage_type,
			u.operation,
			u.region_code,
			u.usage_start_time,
			u.usage_end_time,
			u.usage_quantity_micros,
			u.usage_unit,
			u.attributes_json,
			u.tag_snapshot_json,
			u.event_source,
			u.scenario_run_id,
			u.scenario_event_id,
			u.scenario_event_sequence,
			u.created_at
		 FROM usage_events u
		 LEFT JOIN metering_records m ON m.usage_event_id = u.id
		 WHERE m.id IS NULL
		   AND (? = '' OR u.usage_end_time <= ?)
		 ORDER BY u.usage_start_time, u.id`,
		throughTime,
		throughTime,
	)
	if err != nil {
		return nil, fmt.Errorf("list unmetered usage events: %w", err)
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
		return nil, fmt.Errorf("iterate unmetered usage events: %w", err)
	}
	return events, nil
}

func meteringRecordFromUsageEvent(event UsageEvent) (MeteringRecord, error) {
	start, err := time.Parse(time.RFC3339, strings.TrimSpace(event.UsageStartTime))
	if err != nil {
		return MeteringRecord{}, fmt.Errorf("usage event %q start time must use RFC3339: %w", event.ID, err)
	}
	end, err := time.Parse(time.RFC3339, strings.TrimSpace(event.UsageEndTime))
	if err != nil {
		return MeteringRecord{}, fmt.Errorf("usage event %q end time must use RFC3339: %w", event.ID, err)
	}
	if !start.Before(end) {
		return MeteringRecord{}, fmt.Errorf("usage event %q start time must be before end time", event.ID)
	}

	record := MeteringRecord{
		ID:                  meteringRecordID(event.ID),
		UsageEventID:        strings.TrimSpace(event.ID),
		ResourceID:          strings.TrimSpace(event.ResourceID),
		AccountID:           strings.TrimSpace(event.AccountID),
		ServiceCode:         strings.TrimSpace(event.ServiceCode),
		UsageType:           strings.TrimSpace(event.UsageType),
		Operation:           strings.TrimSpace(event.Operation),
		RegionCode:          strings.TrimSpace(event.RegionCode),
		UsageStartTime:      start.Format(time.RFC3339),
		UsageEndTime:        end.Format(time.RFC3339),
		UsageQuantityMicros: event.UsageQuantityMicros,
		UsageUnit:           strings.TrimSpace(event.UsageUnit),
		TagSnapshot:         normalizeStringMap(event.TagSnapshot),
	}
	if err := validateMeteringRecord(record); err != nil {
		return MeteringRecord{}, err
	}
	return record, nil
}

func validateMeteringRecord(record MeteringRecord) error {
	if record.ID == "" {
		return fmt.Errorf("metering record ID is required")
	}
	if record.UsageEventID == "" {
		return fmt.Errorf("metering record usage event ID is required")
	}
	if record.ResourceID == "" {
		return fmt.Errorf("metering record resource ID is required")
	}
	if record.AccountID == "" {
		return fmt.Errorf("metering record account ID is required")
	}
	if record.ServiceCode == "" {
		return fmt.Errorf("metering record service code is required")
	}
	if record.UsageType == "" {
		return fmt.Errorf("metering record usage type is required")
	}
	if record.Operation == "" {
		return fmt.Errorf("metering record operation is required")
	}
	if record.RegionCode == "" {
		return fmt.Errorf("metering record region code is required")
	}
	if record.UsageStartTime == "" {
		return fmt.Errorf("metering record start time is required")
	}
	if record.UsageEndTime == "" {
		return fmt.Errorf("metering record end time is required")
	}
	if record.UsageQuantityMicros <= 0 {
		return fmt.Errorf("metering record quantity must be greater than zero")
	}
	if record.UsageUnit == "" {
		return fmt.Errorf("metering record unit is required")
	}
	return validateStringMap("metering tag snapshot", record.TagSnapshot)
}

func insertMeteringRecord(ctx context.Context, tx *sql.Tx, record MeteringRecord) (bool, error) {
	tagSnapshotJSON, err := marshalStringMap(record.TagSnapshot)
	if err != nil {
		return false, fmt.Errorf("marshal metering tag snapshot for usage event %q: %w", record.UsageEventID, err)
	}
	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO metering_records (
			id,
			usage_event_id,
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
			tag_snapshot_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(usage_event_id) DO NOTHING`,
		record.ID,
		record.UsageEventID,
		record.ResourceID,
		record.AccountID,
		record.ServiceCode,
		record.UsageType,
		record.Operation,
		record.RegionCode,
		record.UsageStartTime,
		record.UsageEndTime,
		record.UsageQuantityMicros,
		record.UsageUnit,
		tagSnapshotJSON,
	)
	if err != nil {
		return false, fmt.Errorf("insert metering record for usage event %q: %w", record.UsageEventID, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read metering insert result for usage event %q: %w", record.UsageEventID, err)
	}
	return rowsAffected > 0, nil
}

type meteringRecordRow interface {
	Scan(dest ...any) error
}

func scanMeteringRecord(row meteringRecordRow) (MeteringRecord, error) {
	var record MeteringRecord
	var tagSnapshotJSON string
	if err := row.Scan(
		&record.ID,
		&record.UsageEventID,
		&record.ResourceID,
		&record.AccountID,
		&record.ServiceCode,
		&record.UsageType,
		&record.Operation,
		&record.RegionCode,
		&record.UsageStartTime,
		&record.UsageEndTime,
		&record.UsageQuantityMicros,
		&record.UsageUnit,
		&tagSnapshotJSON,
		&record.CreatedAt,
	); err != nil {
		return MeteringRecord{}, fmt.Errorf("scan metering record: %w", err)
	}

	var err error
	record.TagSnapshot, err = unmarshalStringMap(tagSnapshotJSON)
	if err != nil {
		return MeteringRecord{}, fmt.Errorf("decode metering tag snapshot for %q: %w", record.ID, err)
	}
	return record, nil
}

func meteringRecordID(usageEventID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(usageEventID)))
	return "mtr_" + hex.EncodeToString(sum[:8])
}
