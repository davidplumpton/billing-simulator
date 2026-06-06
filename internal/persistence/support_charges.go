package persistence

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

const (
	serviceAWSSupport = "AWSSupport"

	supportBusinessUsageType = "support-business-eligible-usd"
	supportBusinessOperation = "BusinessSupport"
	supportRegionGlobal      = "global"
	supportUsageUnitUSD      = "USD"

	// supportBusinessMinimumCostMicros is a synthetic MVP minimum, not real AWS pricing.
	supportBusinessMinimumCostMicros = int64(1_000_000)
)

// SupportChargeGenerationRequest identifies a billing period whose support fee should be refreshed.
type SupportChargeGenerationRequest struct {
	PayerAccountID string
	PeriodStart    string
	PeriodEnd      string
	LineItemStatus string
}

// SupportChargeGenerationResult reports support fee line items created or refreshed.
type SupportChargeGenerationResult struct {
	ItemsCreated int
	ItemsUpdated int
	ItemsDeleted int
	Items        []BillLineItem
}

// SupportChargeSource links one support fee back to an eligible source bill line item.
type SupportChargeSource struct {
	SupportBillLineItemID string
	SourceBillLineItemID  string
	SourceCostMicros      int64
	CreatedAt             string
}

// SupportChargeRepository derives period-level AWS Support fees from eligible spend.
type SupportChargeRepository struct {
	db      *sql.DB
	catalog PriceCatalogRepository
}

// NewSupportChargeRepository creates a support-charge repository backed by a workspace database.
func NewSupportChargeRepository(db *sql.DB) SupportChargeRepository {
	return SupportChargeRepository{
		db:      db,
		catalog: NewPriceCatalogRepository(db),
	}
}

// GenerateSupportCharges refreshes support fees from eligible spend in one billing period.
func (r SupportChargeRepository) GenerateSupportCharges(ctx context.Context, request SupportChargeGenerationRequest) (SupportChargeGenerationResult, error) {
	if r.db == nil {
		return SupportChargeGenerationResult{}, fmt.Errorf("database handle is required")
	}
	request = normalizeSupportChargeGenerationRequest(request)
	period, err := validateSupportChargeGenerationRequest(request)
	if err != nil {
		return SupportChargeGenerationResult{}, err
	}
	catalogItem, err := r.supportCatalogItem(ctx, period)
	if err != nil {
		return SupportChargeGenerationResult{}, err
	}

	var result SupportChargeGenerationResult
	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		generated, err := r.generateSupportChargesInTx(ctx, tx, request, period, catalogItem)
		if err != nil {
			return err
		}
		if generated.ItemsCreated > 0 || generated.ItemsUpdated > 0 || generated.ItemsDeleted > 0 {
			if _, err := refreshCostCategoryAssignmentsInTx(ctx, tx, request.PeriodStart, request.PeriodEnd); err != nil {
				return err
			}
			if _, err := refreshCostExplorerSummariesInTx(ctx, tx, request.PeriodStart, request.PeriodEnd); err != nil {
				return err
			}
		}
		result = generated
		return nil
	})
	if err != nil {
		return SupportChargeGenerationResult{}, err
	}
	return result, nil
}

// ListSupportChargeSources reads the source line items used to compute one support fee.
func (r SupportChargeRepository) ListSupportChargeSources(ctx context.Context, supportBillLineItemID string) ([]SupportChargeSource, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	supportBillLineItemID = strings.TrimSpace(supportBillLineItemID)
	if supportBillLineItemID == "" {
		return nil, fmt.Errorf("support bill line item ID is required")
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			support_bill_line_item_id,
			source_bill_line_item_id,
			source_cost_micros,
			created_at
		 FROM support_charge_sources
		 WHERE support_bill_line_item_id = ?
		 ORDER BY source_bill_line_item_id`,
		supportBillLineItemID,
	)
	if err != nil {
		return nil, fmt.Errorf("list support charge sources: %w", err)
	}
	defer rows.Close()

	var sources []SupportChargeSource
	for rows.Next() {
		var source SupportChargeSource
		if err := rows.Scan(
			&source.SupportBillLineItemID,
			&source.SourceBillLineItemID,
			&source.SourceCostMicros,
			&source.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan support charge source: %w", err)
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate support charge sources: %w", err)
	}
	return sources, nil
}

func (r SupportChargeRepository) supportCatalogItem(ctx context.Context, period BillingPeriod) (PriceCatalogItem, error) {
	lookup, err := r.catalog.Lookup(ctx, PriceLookupRequest{
		ServiceCode:         serviceAWSSupport,
		UsageType:           supportBusinessUsageType,
		Operation:           supportBusinessOperation,
		RegionCode:          supportRegionGlobal,
		UsageUnit:           supportUsageUnitUSD,
		UsageQuantityMicros: 0,
		UsageDate:           period.Start,
		BillingPeriodDays:   period.Days,
	})
	if err != nil {
		return PriceCatalogItem{}, fmt.Errorf("lookup support catalog rate: %w", err)
	}
	return lookup.Item, nil
}

func (r SupportChargeRepository) generateSupportChargesInTx(
	ctx context.Context,
	tx supportChargeStore,
	request SupportChargeGenerationRequest,
	period BillingPeriod,
	catalogItem PriceCatalogItem,
) (SupportChargeGenerationResult, error) {
	spends, err := listEligibleSupportSpend(ctx, tx, request)
	if err != nil {
		return SupportChargeGenerationResult{}, err
	}

	result := SupportChargeGenerationResult{
		Items: make([]BillLineItem, 0, len(spends)),
	}
	keepIDs := map[string]struct{}{}
	for _, spend := range spends {
		item, err := supportLineItemFromSpend(request, period, catalogItem, spend)
		if err != nil {
			return SupportChargeGenerationResult{}, err
		}
		created, err := upsertSupportBillLineItem(ctx, tx, item)
		if err != nil {
			return SupportChargeGenerationResult{}, err
		}
		if err := replaceSupportChargeSources(ctx, tx, item.ID, spend.Sources); err != nil {
			return SupportChargeGenerationResult{}, err
		}
		if created {
			result.ItemsCreated++
		} else {
			result.ItemsUpdated++
		}
		result.Items = append(result.Items, item)
		keepIDs[item.ID] = struct{}{}
	}

	deleted, err := deleteStaleSupportCharges(ctx, tx, request, keepIDs)
	if err != nil {
		return SupportChargeGenerationResult{}, err
	}
	result.ItemsDeleted = deleted
	return result, nil
}

type supportChargeStore interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

type eligibleSupportSpend struct {
	PayerAccountID  string
	CurrencyCode    string
	TotalCostMicros int64
	Sources         []eligibleSupportSource
}

type eligibleSupportSource struct {
	ID         string
	CostMicros int64
}

func listEligibleSupportSpend(ctx context.Context, q supportChargeStore, request SupportChargeGenerationRequest) ([]eligibleSupportSpend, error) {
	rows, err := q.QueryContext(
		ctx,
		`SELECT
			id,
			payer_account_id,
			unblended_cost_micros,
			currency_code
		 FROM bill_line_items
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND line_item_status = ?
		   AND line_item_type = ?
		   AND service_code <> ?
		   AND unblended_cost_micros > 0
		   AND (? = '' OR payer_account_id = ?)
		 ORDER BY payer_account_id, usage_start_time, id`,
		request.PeriodStart,
		request.PeriodEnd,
		request.LineItemStatus,
		billLineItemTypeUsage,
		serviceAWSSupport,
		request.PayerAccountID,
		request.PayerAccountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list support-eligible spend: %w", err)
	}
	defer rows.Close()

	orderedPayers := []string{}
	spendByPayer := map[string]*eligibleSupportSpend{}
	for rows.Next() {
		var sourceID, payerAccountID, currencyCode string
		var costMicros int64
		if err := rows.Scan(&sourceID, &payerAccountID, &costMicros, &currencyCode); err != nil {
			return nil, fmt.Errorf("scan support-eligible spend: %w", err)
		}
		if currencyCode != defaultBillCurrencyCode {
			return nil, fmt.Errorf("support charges only support USD eligible spend, got %q for line item %q", currencyCode, sourceID)
		}
		spend := spendByPayer[payerAccountID]
		if spend == nil {
			spend = &eligibleSupportSpend{
				PayerAccountID: payerAccountID,
				CurrencyCode:   currencyCode,
			}
			spendByPayer[payerAccountID] = spend
			orderedPayers = append(orderedPayers, payerAccountID)
		}
		if spend.CurrencyCode != currencyCode {
			return nil, fmt.Errorf("support charges do not support mixed currencies for payer %s", payerAccountID)
		}
		spend.TotalCostMicros += costMicros
		spend.Sources = append(spend.Sources, eligibleSupportSource{
			ID:         sourceID,
			CostMicros: costMicros,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate support-eligible spend: %w", err)
	}

	spends := make([]eligibleSupportSpend, 0, len(orderedPayers))
	for _, payerAccountID := range orderedPayers {
		spend := spendByPayer[payerAccountID]
		if spend.TotalCostMicros > 0 {
			spends = append(spends, *spend)
		}
	}
	return spends, nil
}

func supportLineItemFromSpend(request SupportChargeGenerationRequest, period BillingPeriod, catalogItem PriceCatalogItem, spend eligibleSupportSpend) (BillLineItem, error) {
	costMicros, err := calculateCatalogCostMicros(spend.TotalCostMicros, catalogItem.RateMicros)
	if err != nil {
		return BillLineItem{}, fmt.Errorf("calculate support charge for payer %s: %w", spend.PayerAccountID, err)
	}
	if costMicros < supportBusinessMinimumCostMicros {
		costMicros = supportBusinessMinimumCostMicros
	}

	item := BillLineItem{
		ID:                    supportBillLineItemID(period.Start, period.End, spend.PayerAccountID),
		BillingPeriodStart:    period.Start,
		BillingPeriodEnd:      period.End,
		BillingPeriodDays:     period.Days,
		PayerAccountID:        spend.PayerAccountID,
		UsageAccountID:        spend.PayerAccountID,
		ServiceCode:           catalogItem.ServiceCode,
		ServiceName:           catalogItem.ServiceName,
		ProductFamily:         catalogItem.ProductFamily,
		UsageType:             catalogItem.UsageType,
		Operation:             catalogItem.Operation,
		RegionCode:            catalogItem.RegionCode,
		LineItemType:          billLineItemTypeFee,
		LineItemStatus:        request.LineItemStatus,
		UsageStartTime:        period.Start + "T00:00:00Z",
		UsageEndTime:          periodEndAsRFC3339(period),
		UsageQuantityMicros:   spend.TotalCostMicros,
		UsageUnit:             supportUsageUnitUSD,
		PricingUnit:           catalogItem.Unit,
		PricingQuantityMicros: spend.TotalCostMicros,
		UnblendedRateMicros:   catalogItem.RateMicros,
		UnblendedCostMicros:   costMicros,
		CurrencyCode:          catalogItem.CurrencyCode,
		PriceCatalogSKU:       catalogItem.SKU,
		PriceEffectiveDate:    catalogItem.EffectiveDate,
		TagSnapshot:           map[string]string{},
		Description:           supportLineItemDescription(spend),
	}
	if err := validateBillLineItem(item); err != nil {
		return BillLineItem{}, fmt.Errorf("build support line item for payer %s: %w", spend.PayerAccountID, err)
	}
	return item, nil
}

func upsertSupportBillLineItem(ctx context.Context, tx supportChargeStore, item BillLineItem) (bool, error) {
	exists, err := billLineItemExists(ctx, tx, item.ID)
	if err != nil {
		return false, err
	}
	tagSnapshotJSON, err := marshalStringMap(item.TagSnapshot)
	if err != nil {
		return false, fmt.Errorf("marshal support line item tag snapshot for %q: %w", item.ID, err)
	}
	if _, err := tx.ExecContext(
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
		ON CONFLICT(id) DO UPDATE SET
			metering_record_id = excluded.metering_record_id,
			usage_event_id = excluded.usage_event_id,
			resource_id = excluded.resource_id,
			billing_period_start = excluded.billing_period_start,
			billing_period_end = excluded.billing_period_end,
			billing_period_days = excluded.billing_period_days,
			payer_account_id = excluded.payer_account_id,
			usage_account_id = excluded.usage_account_id,
			service_code = excluded.service_code,
			service_name = excluded.service_name,
			product_family = excluded.product_family,
			usage_type = excluded.usage_type,
			operation = excluded.operation,
			region_code = excluded.region_code,
			line_item_type = excluded.line_item_type,
			line_item_status = excluded.line_item_status,
			usage_start_time = excluded.usage_start_time,
			usage_end_time = excluded.usage_end_time,
			usage_quantity_micros = excluded.usage_quantity_micros,
			usage_unit = excluded.usage_unit,
			pricing_unit = excluded.pricing_unit,
			pricing_quantity_micros = excluded.pricing_quantity_micros,
			unblended_rate_micros = excluded.unblended_rate_micros,
			unblended_cost_micros = excluded.unblended_cost_micros,
			currency_code = excluded.currency_code,
			price_catalog_sku = excluded.price_catalog_sku,
			price_effective_date = excluded.price_effective_date,
			tag_snapshot_json = excluded.tag_snapshot_json,
			description = excluded.description`,
		item.ID,
		nullStringArg(item.MeteringRecordID),
		nullStringArg(item.UsageEventID),
		nullStringArg(item.ResourceID),
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
	); err != nil {
		return false, fmt.Errorf("upsert support bill line item %q: %w", item.ID, err)
	}
	return !exists, nil
}

func billLineItemExists(ctx context.Context, q supportChargeStore, id string) (bool, error) {
	var found int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM bill_line_items WHERE id = ?`, id).Scan(&found)
	if err == nil {
		return true, nil
	}
	if err == sql.ErrNoRows {
		return false, nil
	}
	return false, fmt.Errorf("check bill line item %q: %w", id, err)
}

func replaceSupportChargeSources(ctx context.Context, tx supportChargeStore, supportBillLineItemID string, sources []eligibleSupportSource) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM support_charge_sources WHERE support_bill_line_item_id = ?`, supportBillLineItemID); err != nil {
		return fmt.Errorf("clear support charge sources for %q: %w", supportBillLineItemID, err)
	}
	for _, source := range sources {
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO support_charge_sources (
				support_bill_line_item_id,
				source_bill_line_item_id,
				source_cost_micros
			 ) VALUES (?, ?, ?)`,
			supportBillLineItemID,
			source.ID,
			source.CostMicros,
		); err != nil {
			return fmt.Errorf("insert support charge source %q for %q: %w", source.ID, supportBillLineItemID, err)
		}
	}
	return nil
}

func deleteStaleSupportCharges(ctx context.Context, tx supportChargeStore, request SupportChargeGenerationRequest, keepIDs map[string]struct{}) (int, error) {
	rows, err := tx.QueryContext(
		ctx,
		`SELECT id
		 FROM bill_line_items
		 WHERE billing_period_start = ?
		   AND billing_period_end = ?
		   AND line_item_status = ?
		   AND line_item_type = ?
		   AND service_code = ?
		   AND usage_type = ?
		   AND operation = ?
		   AND (? = '' OR payer_account_id = ?)
		 ORDER BY id`,
		request.PeriodStart,
		request.PeriodEnd,
		request.LineItemStatus,
		billLineItemTypeFee,
		serviceAWSSupport,
		supportBusinessUsageType,
		supportBusinessOperation,
		request.PayerAccountID,
		request.PayerAccountID,
	)
	if err != nil {
		return 0, fmt.Errorf("list stale support charges: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, fmt.Errorf("scan stale support charge: %w", err)
		}
		if _, keep := keepIDs[id]; !keep {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate stale support charges: %w", err)
	}

	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM support_charge_sources WHERE support_bill_line_item_id = ?`, id); err != nil {
			return 0, fmt.Errorf("delete stale support charge sources for %q: %w", id, err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM bill_line_items WHERE id = ?`, id); err != nil {
			return 0, fmt.Errorf("delete stale support charge %q: %w", id, err)
		}
	}
	return len(ids), nil
}

func normalizeSupportChargeGenerationRequest(request SupportChargeGenerationRequest) SupportChargeGenerationRequest {
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	request.PeriodStart = strings.TrimSpace(request.PeriodStart)
	request.PeriodEnd = strings.TrimSpace(request.PeriodEnd)
	request.LineItemStatus = strings.TrimSpace(request.LineItemStatus)
	if request.LineItemStatus == "" {
		request.LineItemStatus = defaultBillLineItemStatus
	}
	return request
}

func validateSupportChargeGenerationRequest(request SupportChargeGenerationRequest) (BillingPeriod, error) {
	if request.PeriodStart == "" || request.PeriodEnd == "" {
		return BillingPeriod{}, fmt.Errorf("support charge billing period start and end are required")
	}
	if !isBillLineItemStatus(request.LineItemStatus) {
		return BillingPeriod{}, fmt.Errorf("unsupported bill line item status %q", request.LineItemStatus)
	}
	return billingPeriodFromDateRange(request.PeriodStart, request.PeriodEnd)
}

func supportLineItemDescription(spend eligibleSupportSpend) string {
	sourceCount := len(spend.Sources)
	noun := "line items"
	if sourceCount == 1 {
		noun = "line item"
	}
	return fmt.Sprintf(
		"AWS Support Business Support for %d eligible %s totaling %d microdollars",
		sourceCount,
		noun,
		spend.TotalCostMicros,
	)
}

func supportBillLineItemID(periodStart, periodEnd, payerAccountID string) string {
	return stableBillingID("bli_support", periodStart, periodEnd, payerAccountID)
}
