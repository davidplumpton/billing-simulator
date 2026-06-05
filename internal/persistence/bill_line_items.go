package persistence

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	defaultBillLineItemLimit    = 25
	maxBillLineItemLimit        = 100
	billLineItemTypeUsage       = "Usage"
	billLineItemTypeFee         = "Fee"
	billLineItemStatusEstimated = "estimated"
	billLineItemStatusFinal     = "final"
	defaultBillLineItemStatus   = billLineItemStatusEstimated
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
	LineItemStatus        string
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
	LineItemStatus string
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
	return r.generateBillLineItems(ctx, request, "")
}

// GenerateBillLineItemsThrough prices unpriced metering records that have ended by the given UTC time.
func (r BillLineItemRepository) GenerateBillLineItemsThrough(ctx context.Context, request BillLineItemGenerationRequest, throughTime string) (BillLineItemGenerationResult, error) {
	throughTime = strings.TrimSpace(throughTime)
	if throughTime != "" {
		parsed, err := time.Parse(time.RFC3339, throughTime)
		if err != nil {
			return BillLineItemGenerationResult{}, fmt.Errorf("bill line item through time must use RFC3339: %w", err)
		}
		throughTime = parsed.UTC().Format(time.RFC3339)
	}
	return r.generateBillLineItems(ctx, request, throughTime)
}

func (r BillLineItemRepository) generateBillLineItems(ctx context.Context, request BillLineItemGenerationRequest, throughTime string) (BillLineItemGenerationResult, error) {
	if r.db == nil {
		return BillLineItemGenerationResult{}, fmt.Errorf("database handle is required")
	}
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	request.LineItemStatus = strings.TrimSpace(request.LineItemStatus)
	if request.LineItemStatus == "" {
		request.LineItemStatus = defaultBillLineItemStatus
	}
	if !isBillLineItemStatus(request.LineItemStatus) {
		return BillLineItemGenerationResult{}, fmt.Errorf("unsupported bill line item status %q", request.LineItemStatus)
	}

	records, err := r.listUnpricedMeteringRecords(ctx, throughTime)
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
		if result.ItemsCreated > 0 {
			if _, err := refreshCostCategoryAssignmentsInTx(ctx, tx, "", ""); err != nil {
				return err
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
			line_item_status,
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

func (r BillLineItemRepository) listUnpricedMeteringRecords(ctx context.Context, throughTime string) ([]MeteringRecord, error) {
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
		   AND (? = '' OR m.usage_end_time <= ?)
		 ORDER BY m.usage_start_time, m.id`,
		throughTime,
		throughTime,
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
	period, err := billingPeriodForUsageWindow(record.UsageStartTime, record.UsageEndTime)
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
		payerAccountID, err = r.defaultPayerAccountID(ctx, record.AccountID)
		if err != nil {
			return BillLineItem{}, fmt.Errorf("resolve payer for metering record %q: %w", record.ID, err)
		}
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
		LineItemStatus:        request.LineItemStatus,
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

// defaultPayerAccountID maps a usage account to its organization payer when the caller omits an override.
func (r BillLineItemRepository) defaultPayerAccountID(ctx context.Context, usageAccountID string) (string, error) {
	usageAccountID = strings.TrimSpace(usageAccountID)
	if usageAccountID == "" {
		return "", nil
	}

	account, err := NewOrganizationRepository(r.db).GetAccount(ctx, usageAccountID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return usageAccountID, nil
		}
		return "", err
	}
	if strings.TrimSpace(account.PayerAccountID) == "" {
		return usageAccountID, nil
	}
	return strings.TrimSpace(account.PayerAccountID), nil
}

func validateBillLineItem(item BillLineItem) error {
	if item.ID == "" {
		return fmt.Errorf("bill line item ID is required")
	}
	if item.LineItemType == billLineItemTypeUsage {
		if item.MeteringRecordID == "" {
			return fmt.Errorf("bill line item metering record ID is required")
		}
		if item.UsageEventID == "" {
			return fmt.Errorf("bill line item usage event ID is required")
		}
		if item.ResourceID == "" {
			return fmt.Errorf("bill line item resource ID is required")
		}
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
	if !isBillLineItemStatus(item.LineItemStatus) {
		return fmt.Errorf("unsupported bill line item status %q", item.LineItemStatus)
	}
	if item.UsageStartTime == "" || item.UsageEndTime == "" {
		return fmt.Errorf("bill line item usage window is required")
	}
	if err := validateBillLineItemUsageWindow(item); err != nil {
		return err
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
	case billLineItemTypeUsage, "Credit", "Tax", billLineItemTypeFee, "Refund":
		return true
	default:
		return false
	}
}

func isBillLineItemStatus(value string) bool {
	switch value {
	case billLineItemStatusEstimated, billLineItemStatusFinal:
		return true
	default:
		return false
	}
}

// validateBillLineItemUsageWindow ensures a persisted charge stays inside its declared billing period.
func validateBillLineItemUsageWindow(item BillLineItem) error {
	periodStart, err := time.Parse(time.DateOnly, item.BillingPeriodStart)
	if err != nil {
		return fmt.Errorf("bill line item billing period start must use YYYY-MM-DD: %w", err)
	}
	periodEnd, err := time.Parse(time.DateOnly, item.BillingPeriodEnd)
	if err != nil {
		return fmt.Errorf("bill line item billing period end must use YYYY-MM-DD: %w", err)
	}
	if !periodStart.Before(periodEnd) {
		return fmt.Errorf("bill line item billing period start must be before end")
	}
	usageStart, err := time.Parse(time.RFC3339, item.UsageStartTime)
	if err != nil {
		return fmt.Errorf("bill line item usage start time must use RFC3339: %w", err)
	}
	usageEnd, err := time.Parse(time.RFC3339, item.UsageEndTime)
	if err != nil {
		return fmt.Errorf("bill line item usage end time must use RFC3339: %w", err)
	}
	if !usageStart.Before(usageEnd) {
		return fmt.Errorf("bill line item usage start time must be before end time")
	}
	if usageStart.UTC().Before(periodStart.UTC()) || usageEnd.UTC().After(periodEnd.UTC()) {
		return fmt.Errorf("bill line item usage window %s to %s crosses billing period %s to %s", usageStart.UTC().Format(time.RFC3339), usageEnd.UTC().Format(time.RFC3339), item.BillingPeriodStart, item.BillingPeriodEnd)
	}
	return nil
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
			line_item_status,
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
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		item.LineItemStatus,
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
	var meteringRecordID, usageEventID, resourceID sql.NullString
	if err := row.Scan(
		&item.ID,
		&meteringRecordID,
		&usageEventID,
		&resourceID,
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
		&item.LineItemStatus,
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

	item.MeteringRecordID = nullStringValue(meteringRecordID)
	item.UsageEventID = nullStringValue(usageEventID)
	item.ResourceID = nullStringValue(resourceID)

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

// billingPeriodForUsageWindow derives the usage-start period and rejects windows spilling past it.
func billingPeriodForUsageWindow(startValue, endValue string) (billLineItemBillingPeriod, error) {
	usageStart, err := time.Parse(time.RFC3339, strings.TrimSpace(startValue))
	if err != nil {
		return billLineItemBillingPeriod{}, fmt.Errorf("usage start time must use RFC3339: %w", err)
	}
	usageEnd, err := time.Parse(time.RFC3339, strings.TrimSpace(endValue))
	if err != nil {
		return billLineItemBillingPeriod{}, fmt.Errorf("usage end time must use RFC3339: %w", err)
	}
	usageStart = usageStart.UTC()
	usageEnd = usageEnd.UTC()
	if !usageStart.Before(usageEnd) {
		return billLineItemBillingPeriod{}, fmt.Errorf("usage start time must be before end time")
	}
	period, err := BillingPeriodForTime(usageStart)
	if err != nil {
		return billLineItemBillingPeriod{}, err
	}
	periodEnd, err := time.Parse(time.DateOnly, period.End)
	if err != nil {
		return billLineItemBillingPeriod{}, fmt.Errorf("parse billing period end: %w", err)
	}
	if usageEnd.After(periodEnd.UTC()) {
		return billLineItemBillingPeriod{}, fmt.Errorf("usage window %s to %s crosses billing period %s to %s", usageStart.Format(time.RFC3339), usageEnd.Format(time.RFC3339), period.Start, period.End)
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
