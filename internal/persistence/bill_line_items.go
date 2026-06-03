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
	defaultBillLineItemLimit = 25
	maxBillLineItemLimit     = 100
	billLineItemTypeUsage    = "Usage"
)

// BillLineItem stores a priced usage charge with lineage back to metering and catalog rows.
type BillLineItem struct {
	ID                    string
	MeteringRecordID      string
	UsageEventID          string
	ResourceID            string
	BillingPeriodStart    string
	BillingPeriodEnd      string
	BillingPeriodDays     int
	PayerAccountID        string
	UsageAccountID        string
	ServiceCode           string
	ServiceName           string
	ProductFamily         string
	UsageType             string
	Operation             string
	RegionCode            string
	LineItemType          string
	UsageStartTime        string
	UsageEndTime          string
	UsageQuantityMicros   int64
	UsageUnit             string
	PricingUnit           string
	PricingQuantityMicros int64
	UnblendedRateMicros   int64
	UnblendedCostMicros   int64
	CurrencyCode          string
	PriceCatalogSKU       string
	PriceEffectiveDate    string
	TagSnapshot           map[string]string
	Description           string
	CreatedAt             string
}

// BillLineItemGenerationRequest configures a pricing run for unpriced metering records.
type BillLineItemGenerationRequest struct {
	PayerAccountID string
}

// BillLineItemGenerationResult reports the bill line items created during one pricing run.
type BillLineItemGenerationResult struct {
	ItemsCreated int
	Items        []BillLineItem
}

// BillLineItemRepository prices metering records into bill line items.
type BillLineItemRepository struct {
	db      *sql.DB
	catalog PriceCatalogRepository
}

// NewBillLineItemRepository creates a repository backed by a workspace database.
func NewBillLineItemRepository(db *sql.DB) BillLineItemRepository {
	return BillLineItemRepository{
		db:      db,
		catalog: NewPriceCatalogRepository(db),
	}
}

// GenerateBillLineItems prices every unpriced metering record and persists stable usage line items.
func (r BillLineItemRepository) GenerateBillLineItems(ctx context.Context, request BillLineItemGenerationRequest) (BillLineItemGenerationResult, error) {
	if r.db == nil {
		return BillLineItemGenerationResult{}, fmt.Errorf("database handle is required")
	}
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)

	records, err := r.listUnpricedMeteringRecords(ctx)
	if err != nil {
		return BillLineItemGenerationResult{}, err
	}
	result := BillLineItemGenerationResult{
		Items: make([]BillLineItem, 0, len(records)),
	}
	if len(records) == 0 {
		return result, nil
	}

	candidates := make([]BillLineItem, 0, len(records))
	for _, record := range records {
		item, err := r.billLineItemFromMeteringRecord(ctx, request, record)
		if err != nil {
			return BillLineItemGenerationResult{}, err
		}
		candidates = append(candidates, item)
	}

	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		for _, item := range candidates {
			inserted, err := insertBillLineItem(ctx, tx, item)
			if err != nil {
				return err
			}
			if inserted {
				result.ItemsCreated++
				result.Items = append(result.Items, item)
			}
		}
		return nil
	})
	if err != nil {
		return BillLineItemGenerationResult{}, err
	}
	return result, nil
}

// ListBillLineItems reads recent bill line items in deterministic newest-first order.
func (r BillLineItemRepository) ListBillLineItems(ctx context.Context, limit int) ([]BillLineItem, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	if limit <= 0 {
		limit = defaultBillLineItemLimit
	}
	if limit > maxBillLineItemLimit {
		limit = maxBillLineItemLimit
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			metering_record_id,
			usage_event_id,
			resource_id,
			billing_period_start,
			billing_period_end,
			billing_period_days,
			payer_account_id,
			usage_account_id,
			service_code,
			service_name,
			product_family,
			usage_type,
			operation,
			region_code,
			line_item_type,
			usage_start_time,
			usage_end_time,
			usage_quantity_micros,
			usage_unit,
			pricing_unit,
			pricing_quantity_micros,
			unblended_rate_micros,
			unblended_cost_micros,
			currency_code,
			price_catalog_sku,
			price_effective_date,
			tag_snapshot_json,
			description,
			created_at
		 FROM bill_line_items
		 ORDER BY usage_start_time DESC, id DESC
		 LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list bill line items: %w", err)
	}
	defer rows.Close()

	var items []BillLineItem
	for rows.Next() {
		item, err := scanBillLineItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate bill line items: %w", err)
	}
	return items, nil
}

func (r BillLineItemRepository) listUnpricedMeteringRecords(ctx context.Context) ([]MeteringRecord, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			m.id,
			m.usage_event_id,
			m.resource_id,
			m.account_id,
			m.service_code,
			m.usage_type,
			m.operation,
			m.region_code,
			m.usage_start_time,
			m.usage_end_time,
			m.usage_quantity_micros,
			m.usage_unit,
			m.tag_snapshot_json,
			m.created_at
		 FROM metering_records m
		 LEFT JOIN bill_line_items b ON b.metering_record_id = m.id
		 WHERE b.id IS NULL
		 ORDER BY m.usage_start_time, m.id`,
	)
	if err != nil {
		return nil, fmt.Errorf("list unpriced metering records: %w", err)
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
		return nil, fmt.Errorf("iterate unpriced metering records: %w", err)
	}
	return records, nil
}

func (r BillLineItemRepository) billLineItemFromMeteringRecord(ctx context.Context, request BillLineItemGenerationRequest, record MeteringRecord) (BillLineItem, error) {
	period, err := billingPeriodForUsageStart(record.UsageStartTime)
	if err != nil {
		return BillLineItem{}, fmt.Errorf("price metering record %q: %w", record.ID, err)
	}
	lookupResult, err := r.catalog.Lookup(ctx, PriceLookupRequest{
		ServiceCode:         record.ServiceCode,
		UsageType:           record.UsageType,
		Operation:           record.Operation,
		RegionCode:          record.RegionCode,
		UsageUnit:           record.UsageUnit,
		UsageQuantityMicros: record.UsageQuantityMicros,
		UsageDate:           period.UsageDate,
		BillingPeriodDays:   period.Days,
	})
	if err != nil {
		return BillLineItem{}, fmt.Errorf("price metering record %q: %w", record.ID, err)
	}

	payerAccountID := request.PayerAccountID
	if payerAccountID == "" {
		payerAccountID = record.AccountID
	}
	item := BillLineItem{
		ID:                    billLineItemID(record.ID),
		MeteringRecordID:      record.ID,
		UsageEventID:          record.UsageEventID,
		ResourceID:            record.ResourceID,
		BillingPeriodStart:    period.Start,
		BillingPeriodEnd:      period.End,
		BillingPeriodDays:     period.Days,
		PayerAccountID:        payerAccountID,
		UsageAccountID:        record.AccountID,
		ServiceCode:           record.ServiceCode,
		ServiceName:           lookupResult.Item.ServiceName,
		ProductFamily:         lookupResult.Item.ProductFamily,
		UsageType:             record.UsageType,
		Operation:             record.Operation,
		RegionCode:            record.RegionCode,
		LineItemType:          billLineItemTypeUsage,
		UsageStartTime:        record.UsageStartTime,
		UsageEndTime:          record.UsageEndTime,
		UsageQuantityMicros:   record.UsageQuantityMicros,
		UsageUnit:             record.UsageUnit,
		PricingUnit:           lookupResult.Item.Unit,
		PricingQuantityMicros: lookupResult.UsageQuantityMicros,
		UnblendedRateMicros:   lookupResult.Item.RateMicros,
		UnblendedCostMicros:   lookupResult.CostMicros,
		CurrencyCode:          lookupResult.Item.CurrencyCode,
		PriceCatalogSKU:       lookupResult.Item.SKU,
		PriceEffectiveDate:    lookupResult.Item.EffectiveDate,
		TagSnapshot:           normalizeStringMap(record.TagSnapshot),
		Description:           billLineItemDescription(lookupResult.Item, record),
	}
	if err := validateBillLineItem(item); err != nil {
		return BillLineItem{}, fmt.Errorf("price metering record %q: %w", record.ID, err)
	}
	return item, nil
}

func validateBillLineItem(item BillLineItem) error {
	if item.ID == "" {
		return fmt.Errorf("bill line item ID is required")
	}
	if item.MeteringRecordID == "" {
		return fmt.Errorf("bill line item metering record ID is required")
	}
	if item.UsageEventID == "" {
		return fmt.Errorf("bill line item usage event ID is required")
	}
	if item.ResourceID == "" {
		return fmt.Errorf("bill line item resource ID is required")
	}
	if item.BillingPeriodStart == "" || item.BillingPeriodEnd == "" || item.BillingPeriodDays <= 0 {
		return fmt.Errorf("bill line item billing period is required")
	}
	if item.PayerAccountID == "" {
		return fmt.Errorf("bill line item payer account ID is required")
	}
	if item.UsageAccountID == "" {
		return fmt.Errorf("bill line item usage account ID is required")
	}
	if item.ServiceCode == "" || item.ServiceName == "" || item.ProductFamily == "" {
		return fmt.Errorf("bill line item service metadata is required")
	}
	if item.UsageType == "" || item.Operation == "" || item.RegionCode == "" {
		return fmt.Errorf("bill line item price dimensions are required")
	}
	if !isBillLineItemType(item.LineItemType) {
		return fmt.Errorf("unsupported bill line item type %q", item.LineItemType)
	}
	if item.UsageStartTime == "" || item.UsageEndTime == "" {
		return fmt.Errorf("bill line item usage window is required")
	}
	if item.UsageQuantityMicros <= 0 {
		return fmt.Errorf("bill line item usage quantity must be greater than zero")
	}
	if item.UsageUnit == "" || item.PricingUnit == "" {
		return fmt.Errorf("bill line item usage and pricing units are required")
	}
	if item.PricingQuantityMicros < 0 {
		return fmt.Errorf("bill line item pricing quantity cannot be negative")
	}
	if item.UnblendedRateMicros < 0 || item.UnblendedCostMicros < 0 {
		return fmt.Errorf("bill line item rate and cost cannot be negative")
	}
	if item.CurrencyCode == "" {
		return fmt.Errorf("bill line item currency is required")
	}
	if item.PriceCatalogSKU == "" || item.PriceEffectiveDate == "" {
		return fmt.Errorf("bill line item price catalog lineage is required")
	}
	if item.Description == "" {
		return fmt.Errorf("bill line item description is required")
	}
	return validateStringMap("bill line item tag snapshot", item.TagSnapshot)
}

func isBillLineItemType(value string) bool {
	switch value {
	case "Usage", "Credit", "Tax", "Fee", "Refund":
		return true
	default:
		return false
	}
}

func insertBillLineItem(ctx context.Context, tx *sql.Tx, item BillLineItem) (bool, error) {
	tagSnapshotJSON, err := marshalStringMap(item.TagSnapshot)
	if err != nil {
		return false, fmt.Errorf("marshal bill line item tag snapshot for metering record %q: %w", item.MeteringRecordID, err)
	}
	result, err := tx.ExecContext(
		ctx,
		`INSERT INTO bill_line_items (
			id,
			metering_record_id,
			usage_event_id,
			resource_id,
			billing_period_start,
			billing_period_end,
			billing_period_days,
			payer_account_id,
			usage_account_id,
			service_code,
			service_name,
			product_family,
			usage_type,
			operation,
			region_code,
			line_item_type,
			usage_start_time,
			usage_end_time,
			usage_quantity_micros,
			usage_unit,
			pricing_unit,
			pricing_quantity_micros,
			unblended_rate_micros,
			unblended_cost_micros,
			currency_code,
			price_catalog_sku,
			price_effective_date,
			tag_snapshot_json,
			description
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(metering_record_id) DO NOTHING`,
		item.ID,
		item.MeteringRecordID,
		item.UsageEventID,
		item.ResourceID,
		item.BillingPeriodStart,
		item.BillingPeriodEnd,
		item.BillingPeriodDays,
		item.PayerAccountID,
		item.UsageAccountID,
		item.ServiceCode,
		item.ServiceName,
		item.ProductFamily,
		item.UsageType,
		item.Operation,
		item.RegionCode,
		item.LineItemType,
		item.UsageStartTime,
		item.UsageEndTime,
		item.UsageQuantityMicros,
		item.UsageUnit,
		item.PricingUnit,
		item.PricingQuantityMicros,
		item.UnblendedRateMicros,
		item.UnblendedCostMicros,
		item.CurrencyCode,
		item.PriceCatalogSKU,
		item.PriceEffectiveDate,
		tagSnapshotJSON,
		item.Description,
	)
	if err != nil {
		return false, fmt.Errorf("insert bill line item for metering record %q: %w", item.MeteringRecordID, err)
	}
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read bill line item insert result for metering record %q: %w", item.MeteringRecordID, err)
	}
	return rowsAffected > 0, nil
}

type billLineItemRow interface {
	Scan(dest ...any) error
}

func scanBillLineItem(row billLineItemRow) (BillLineItem, error) {
	var item BillLineItem
	var tagSnapshotJSON string
	if err := row.Scan(
		&item.ID,
		&item.MeteringRecordID,
		&item.UsageEventID,
		&item.ResourceID,
		&item.BillingPeriodStart,
		&item.BillingPeriodEnd,
		&item.BillingPeriodDays,
		&item.PayerAccountID,
		&item.UsageAccountID,
		&item.ServiceCode,
		&item.ServiceName,
		&item.ProductFamily,
		&item.UsageType,
		&item.Operation,
		&item.RegionCode,
		&item.LineItemType,
		&item.UsageStartTime,
		&item.UsageEndTime,
		&item.UsageQuantityMicros,
		&item.UsageUnit,
		&item.PricingUnit,
		&item.PricingQuantityMicros,
		&item.UnblendedRateMicros,
		&item.UnblendedCostMicros,
		&item.CurrencyCode,
		&item.PriceCatalogSKU,
		&item.PriceEffectiveDate,
		&tagSnapshotJSON,
		&item.Description,
		&item.CreatedAt,
	); err != nil {
		return BillLineItem{}, fmt.Errorf("scan bill line item: %w", err)
	}

	var err error
	item.TagSnapshot, err = unmarshalStringMap(tagSnapshotJSON)
	if err != nil {
		return BillLineItem{}, fmt.Errorf("decode bill line item tag snapshot for %q: %w", item.ID, err)
	}
	return item, nil
}

type billLineItemBillingPeriod struct {
	Start     string
	End       string
	Days      int
	UsageDate string
}

func billingPeriodForUsageStart(value string) (billLineItemBillingPeriod, error) {
	usageStart, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return billLineItemBillingPeriod{}, fmt.Errorf("usage start time must use RFC3339: %w", err)
	}
	usageStart = usageStart.UTC()
	period, err := BillingPeriodForTime(usageStart)
	if err != nil {
		return billLineItemBillingPeriod{}, err
	}
	return billLineItemBillingPeriod{
		Start:     period.Start,
		End:       period.End,
		Days:      period.Days,
		UsageDate: usageStart.Format(time.DateOnly),
	}, nil
}

func billLineItemDescription(item PriceCatalogItem, record MeteringRecord) string {
	return fmt.Sprintf("%s %s usage for %s in %s", item.ServiceName, record.UsageType, record.ResourceID, record.RegionCode)
}

func billLineItemID(meteringRecordID string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(meteringRecordID)))
	return "bli_" + hex.EncodeToString(sum[:8])
}
