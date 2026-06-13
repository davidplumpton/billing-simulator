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
	reservedInstanceProductFamily      = "Reserved Instance"
	reservedInstanceSharingOrg         = "organization"
	reservedInstanceSharingOwner       = "owner_account"
	reservedInstanceStatusActive       = "active"
	reservedInstanceStatusRetired      = "retired"
	reservedInstanceKindUpfrontFee     = "upfront_fee"
	reservedInstanceKindRecurringFee   = "recurring_fee"
	reservedInstanceKindCoverageCredit = "coverage_credit"
	reservedInstanceFeeUnit            = "Fee"
	reservedInstancePricingUnit        = "USD"
)

// ReservedInstancePurchase stores one simplified EC2 Reserved Instance commitment.
type ReservedInstancePurchase struct {
	ID                        string
	PayerAccountID            string
	OwnerAccountID            string
	ServiceCode               string
	ServiceName               string
	ProductFamily             string
	UsageType                 string
	Operation                 string
	RegionCode                string
	InstanceCount             int
	SharingScope              string
	TermStartTime             string
	TermEndTime               string
	UpfrontFeeMicros          int64
	MonthlyRecurringFeeMicros int64
	CurrencyCode              string
	PriceCatalogSKU           string
	PriceEffectiveDate        string
	Status                    string
	Description               string
	CreatedAt                 string
	UpdatedAt                 string
}

// ReservedInstanceLineItemSource links generated RI rows to their purchase and covered usage.
type ReservedInstanceLineItemSource struct {
	BillLineItemID        string
	ReservedInstanceID    string
	SourceBillLineItemID  string
	LineItemKind          string
	CoveredQuantityMicros int64
	CoveredCostMicros     int64
	CreatedAt             string
	UpdatedAt             string
}

// ReservedInstancePurchaseCreateRequest describes a simplified RI purchase to persist.
type ReservedInstancePurchaseCreateRequest struct {
	ID                        string
	PayerAccountID            string
	OwnerAccountID            string
	UsageType                 string
	Operation                 string
	RegionCode                string
	InstanceCount             int
	SharingScope              string
	TermStartTime             string
	TermEndTime               string
	UpfrontFeeMicros          int64
	MonthlyRecurringFeeMicros int64
	CurrencyCode              string
	Status                    string
	Description               string
}

// ReservedInstanceRepository manages simplified Reserved Instance purchases and generated rows.
type ReservedInstanceRepository struct {
	db      *sql.DB
	catalog PriceCatalogRepository
}

// NewReservedInstanceRepository creates a repository backed by a workspace database.
func NewReservedInstanceRepository(db *sql.DB) ReservedInstanceRepository {
	return ReservedInstanceRepository{
		db:      db,
		catalog: NewPriceCatalogRepository(db),
	}
}

// CreatePurchase stores one RI purchase after resolving its selected EC2 price-catalog lineage.
func (r ReservedInstanceRepository) CreatePurchase(ctx context.Context, request ReservedInstancePurchaseCreateRequest) (ReservedInstancePurchase, error) {
	if r.db == nil {
		return ReservedInstancePurchase{}, fmt.Errorf("database handle is required")
	}
	purchase, err := r.purchaseFromCreateRequest(ctx, request)
	if err != nil {
		return ReservedInstancePurchase{}, err
	}
	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		return insertReservedInstancePurchase(ctx, tx, purchase)
	})
	if err != nil {
		return ReservedInstancePurchase{}, err
	}
	return purchase, nil
}

// ListLineItemSources reads RI-generated line item links for one purchase.
func (r ReservedInstanceRepository) ListLineItemSources(ctx context.Context, reservedInstanceID string) ([]ReservedInstanceLineItemSource, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	reservedInstanceID = strings.TrimSpace(reservedInstanceID)
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			bill_line_item_id,
			reserved_instance_id,
			source_bill_line_item_id,
			line_item_kind,
			covered_quantity_micros,
			covered_cost_micros,
			created_at,
			updated_at
		 FROM reserved_instance_line_item_sources
		 WHERE (? = '' OR reserved_instance_id = ?)
		 ORDER BY reserved_instance_id, line_item_kind, bill_line_item_id`,
		reservedInstanceID,
		reservedInstanceID,
	)
	if err != nil {
		return nil, fmt.Errorf("list reserved instance line item sources: %w", err)
	}
	defer rows.Close()

	var sources []ReservedInstanceLineItemSource
	for rows.Next() {
		source, err := scanReservedInstanceLineItemSource(rows)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reserved instance line item sources: %w", err)
	}
	return sources, nil
}

func (r ReservedInstanceRepository) purchaseFromCreateRequest(ctx context.Context, request ReservedInstancePurchaseCreateRequest) (ReservedInstancePurchase, error) {
	request = normalizeReservedInstancePurchaseCreateRequest(request)
	if err := validateReservedInstancePurchaseCreateRequest(request); err != nil {
		return ReservedInstancePurchase{}, err
	}
	termStart, err := time.Parse(time.RFC3339, request.TermStartTime)
	if err != nil {
		return ReservedInstancePurchase{}, fmt.Errorf("reserved instance term start must use RFC3339: %w", err)
	}
	termEnd, err := time.Parse(time.RFC3339, request.TermEndTime)
	if err != nil {
		return ReservedInstancePurchase{}, fmt.Errorf("reserved instance term end must use RFC3339: %w", err)
	}
	request.TermStartTime = termStart.UTC().Format(time.RFC3339)
	request.TermEndTime = termEnd.UTC().Format(time.RFC3339)
	period, err := BillingPeriodForTime(termStart)
	if err != nil {
		return ReservedInstancePurchase{}, err
	}
	lookup, err := r.catalog.Lookup(ctx, PriceLookupRequest{
		ServiceCode:         serviceAmazonEC2,
		UsageType:           request.UsageType,
		Operation:           request.Operation,
		RegionCode:          request.RegionCode,
		UsageUnit:           "Hours",
		UsageQuantityMicros: priceQuantityMicros,
		UsageDate:           termStart.UTC().Format(time.DateOnly),
		BillingPeriodDays:   period.Days,
	})
	if err != nil {
		return ReservedInstancePurchase{}, fmt.Errorf("resolve reserved instance catalog lineage: %w", err)
	}
	purchase := ReservedInstancePurchase{
		ID:                        request.ID,
		PayerAccountID:            request.PayerAccountID,
		OwnerAccountID:            request.OwnerAccountID,
		ServiceCode:               lookup.Item.ServiceCode,
		ServiceName:               lookup.Item.ServiceName,
		ProductFamily:             reservedInstanceProductFamily,
		UsageType:                 request.UsageType,
		Operation:                 request.Operation,
		RegionCode:                request.RegionCode,
		InstanceCount:             request.InstanceCount,
		SharingScope:              request.SharingScope,
		TermStartTime:             request.TermStartTime,
		TermEndTime:               request.TermEndTime,
		UpfrontFeeMicros:          request.UpfrontFeeMicros,
		MonthlyRecurringFeeMicros: request.MonthlyRecurringFeeMicros,
		CurrencyCode:              request.CurrencyCode,
		PriceCatalogSKU:           lookup.Item.SKU,
		PriceEffectiveDate:        lookup.Item.EffectiveDate,
		Status:                    request.Status,
		Description:               request.Description,
	}
	if purchase.ID == "" {
		purchase.ID = reservedInstancePurchaseID(purchase)
	}
	if err := validateReservedInstancePurchase(purchase); err != nil {
		return ReservedInstancePurchase{}, err
	}
	return purchase, nil
}

func normalizeReservedInstancePurchaseCreateRequest(request ReservedInstancePurchaseCreateRequest) ReservedInstancePurchaseCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	request.OwnerAccountID = strings.TrimSpace(request.OwnerAccountID)
	request.UsageType = strings.TrimSpace(request.UsageType)
	request.Operation = strings.TrimSpace(request.Operation)
	if request.Operation == "" {
		request.Operation = "RunInstances"
	}
	request.RegionCode = strings.TrimSpace(request.RegionCode)
	request.SharingScope = strings.TrimSpace(request.SharingScope)
	if request.SharingScope == "" {
		request.SharingScope = reservedInstanceSharingOrg
	}
	request.TermStartTime = strings.TrimSpace(request.TermStartTime)
	request.TermEndTime = strings.TrimSpace(request.TermEndTime)
	request.CurrencyCode = strings.TrimSpace(request.CurrencyCode)
	if request.CurrencyCode == "" {
		request.CurrencyCode = defaultBillCurrencyCode
	}
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = reservedInstanceStatusActive
	}
	request.Description = strings.TrimSpace(request.Description)
	return request
}

func validateReservedInstancePurchaseCreateRequest(request ReservedInstancePurchaseCreateRequest) error {
	if request.PayerAccountID == "" {
		return fmt.Errorf("reserved instance payer account ID is required")
	}
	if request.OwnerAccountID == "" {
		return fmt.Errorf("reserved instance owner account ID is required")
	}
	if request.UsageType == "" || request.Operation == "" || request.RegionCode == "" {
		return fmt.Errorf("reserved instance price dimensions are required")
	}
	if request.InstanceCount <= 0 {
		return fmt.Errorf("reserved instance instance count must be greater than zero")
	}
	if !isReservedInstanceSharingScope(request.SharingScope) {
		return fmt.Errorf("unsupported reserved instance sharing scope %q", request.SharingScope)
	}
	termStart, err := time.Parse(time.RFC3339, request.TermStartTime)
	if err != nil {
		return fmt.Errorf("reserved instance term start must use RFC3339: %w", err)
	}
	termEnd, err := time.Parse(time.RFC3339, request.TermEndTime)
	if err != nil {
		return fmt.Errorf("reserved instance term end must use RFC3339: %w", err)
	}
	if !termStart.Before(termEnd) {
		return fmt.Errorf("reserved instance term start must be before end")
	}
	if request.UpfrontFeeMicros < 0 || request.MonthlyRecurringFeeMicros < 0 {
		return fmt.Errorf("reserved instance fees cannot be negative")
	}
	if request.CurrencyCode == "" {
		return fmt.Errorf("reserved instance currency is required")
	}
	if !isReservedInstanceStatus(request.Status) {
		return fmt.Errorf("unsupported reserved instance status %q", request.Status)
	}
	return nil
}

func validateReservedInstancePurchase(purchase ReservedInstancePurchase) error {
	if purchase.ID == "" {
		return fmt.Errorf("reserved instance ID is required")
	}
	if purchase.ServiceCode == "" || purchase.ServiceName == "" || purchase.ProductFamily == "" {
		return fmt.Errorf("reserved instance service metadata is required")
	}
	if purchase.PriceCatalogSKU == "" || purchase.PriceEffectiveDate == "" {
		return fmt.Errorf("reserved instance price catalog lineage is required")
	}
	return nil
}

func isReservedInstanceSharingScope(value string) bool {
	switch value {
	case reservedInstanceSharingOrg, reservedInstanceSharingOwner:
		return true
	default:
		return false
	}
}

func isReservedInstanceStatus(value string) bool {
	switch value {
	case reservedInstanceStatusActive, reservedInstanceStatusRetired:
		return true
	default:
		return false
	}
}

func insertReservedInstancePurchase(ctx context.Context, tx *sql.Tx, purchase ReservedInstancePurchase) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO reserved_instance_purchases (
			id,
			payer_account_id,
			owner_account_id,
			service_code,
			service_name,
			product_family,
			usage_type,
			operation,
			region_code,
			instance_count,
			sharing_scope,
			term_start_time,
			term_end_time,
			upfront_fee_micros,
			monthly_recurring_fee_micros,
			currency_code,
			price_catalog_sku,
			price_effective_date,
			status,
			description
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		purchase.ID,
		purchase.PayerAccountID,
		purchase.OwnerAccountID,
		purchase.ServiceCode,
		purchase.ServiceName,
		purchase.ProductFamily,
		purchase.UsageType,
		purchase.Operation,
		purchase.RegionCode,
		purchase.InstanceCount,
		purchase.SharingScope,
		purchase.TermStartTime,
		purchase.TermEndTime,
		purchase.UpfrontFeeMicros,
		purchase.MonthlyRecurringFeeMicros,
		purchase.CurrencyCode,
		purchase.PriceCatalogSKU,
		purchase.PriceEffectiveDate,
		purchase.Status,
		purchase.Description,
	)
	if err != nil {
		return fmt.Errorf("insert reserved instance purchase %q: %w", purchase.ID, err)
	}
	return nil
}

type reservedInstancePeriodRef struct {
	Start          string
	End            string
	Days           int
	PayerAccountID string
	LineItemStatus string
}

func (r ReservedInstanceRepository) generateLineItemsInTx(ctx context.Context, tx *sql.Tx, sourceItems []BillLineItem, lineItemStatus string) (BillLineItemGenerationResult, error) {
	periods := reservedInstancePeriodRefsForLineItems(sourceItems, lineItemStatus)
	result := BillLineItemGenerationResult{}
	for _, period := range periods {
		purchases, err := listActiveReservedInstancePurchases(ctx, tx, period)
		if err != nil {
			return BillLineItemGenerationResult{}, err
		}
		for _, purchase := range purchases {
			feeItems, err := reservedInstanceFeeLineItems(purchase, period)
			if err != nil {
				return BillLineItemGenerationResult{}, err
			}
			for _, item := range feeItems {
				created, err := upsertReservedInstanceBillLineItem(ctx, tx, item)
				if err != nil {
					return BillLineItemGenerationResult{}, err
				}
				source := ReservedInstanceLineItemSource{
					BillLineItemID:        item.ID,
					ReservedInstanceID:    purchase.ID,
					LineItemKind:          reservedInstanceLineItemKindForFee(item),
					CoveredQuantityMicros: 0,
					CoveredCostMicros:     0,
				}
				if err := upsertReservedInstanceLineItemSource(ctx, tx, source); err != nil {
					return BillLineItemGenerationResult{}, err
				}
				if created {
					result.ItemsCreated++
					result.Items = append(result.Items, item)
				}
			}
			coverageItems, err := reservedInstanceCoverageCreditLineItems(ctx, tx, purchase, period)
			if err != nil {
				return BillLineItemGenerationResult{}, err
			}
			for _, candidate := range coverageItems {
				created, err := upsertReservedInstanceBillLineItem(ctx, tx, candidate.Item)
				if err != nil {
					return BillLineItemGenerationResult{}, err
				}
				if err := upsertReservedInstanceLineItemSource(ctx, tx, candidate.Source); err != nil {
					return BillLineItemGenerationResult{}, err
				}
				if created {
					result.ItemsCreated++
					result.Items = append(result.Items, candidate.Item)
				}
			}
		}
	}
	return result, nil
}

func reservedInstancePeriodRefsForLineItems(items []BillLineItem, lineItemStatus string) []reservedInstancePeriodRef {
	seen := map[string]struct{}{}
	var periods []reservedInstancePeriodRef
	for _, item := range items {
		if item.LineItemType != billLineItemTypeUsage {
			continue
		}
		key := strings.Join([]string{item.BillingPeriodStart, item.BillingPeriodEnd, item.PayerAccountID, lineItemStatus}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		periods = append(periods, reservedInstancePeriodRef{
			Start:          item.BillingPeriodStart,
			End:            item.BillingPeriodEnd,
			Days:           item.BillingPeriodDays,
			PayerAccountID: item.PayerAccountID,
			LineItemStatus: lineItemStatus,
		})
	}
	return periods
}

func listActiveReservedInstancePurchases(ctx context.Context, q reservedInstanceStore, period reservedInstancePeriodRef) ([]ReservedInstancePurchase, error) {
	rows, err := q.QueryContext(
		ctx,
		`SELECT
			id,
			payer_account_id,
			owner_account_id,
			service_code,
			service_name,
			product_family,
			usage_type,
			operation,
			region_code,
			instance_count,
			sharing_scope,
			term_start_time,
			term_end_time,
			upfront_fee_micros,
			monthly_recurring_fee_micros,
			currency_code,
			price_catalog_sku,
			price_effective_date,
			status,
			description,
			created_at,
			updated_at
		 FROM reserved_instance_purchases
		 WHERE payer_account_id = ?
		   AND status = ?
		   AND term_start_time < ?
		   AND term_end_time > ?
		 ORDER BY term_start_time, owner_account_id, id`,
		period.PayerAccountID,
		reservedInstanceStatusActive,
		period.End+"T00:00:00Z",
		period.Start+"T00:00:00Z",
	)
	if err != nil {
		return nil, fmt.Errorf("list active reserved instance purchases: %w", err)
	}
	defer rows.Close()

	var purchases []ReservedInstancePurchase
	for rows.Next() {
		purchase, err := scanReservedInstancePurchase(rows)
		if err != nil {
			return nil, err
		}
		purchases = append(purchases, purchase)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active reserved instance purchases: %w", err)
	}
	return purchases, nil
}

type reservedInstanceStore interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func reservedInstanceFeeLineItems(purchase ReservedInstancePurchase, period reservedInstancePeriodRef) ([]BillLineItem, error) {
	var items []BillLineItem
	if purchase.UpfrontFeeMicros > 0 && reservedInstanceStartsInPeriod(purchase, period) {
		item, err := reservedInstanceFeeLineItem(purchase, period, reservedInstanceKindUpfrontFee, purchase.UpfrontFeeMicros)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if purchase.MonthlyRecurringFeeMicros > 0 && reservedInstanceOverlapsPeriod(purchase, period) {
		item, err := reservedInstanceFeeLineItem(purchase, period, reservedInstanceKindRecurringFee, purchase.MonthlyRecurringFeeMicros)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func reservedInstanceFeeLineItem(purchase ReservedInstancePurchase, period reservedInstancePeriodRef, kind string, amountMicros int64) (BillLineItem, error) {
	operation := "ReservedInstanceRecurringFee"
	description := fmt.Sprintf("Reserved Instance %s recurring charge for %s", purchase.ID, purchase.UsageType)
	if kind == reservedInstanceKindUpfrontFee {
		operation = "ReservedInstanceUpfrontFee"
		description = fmt.Sprintf("Reserved Instance %s upfront charge for %s", purchase.ID, purchase.UsageType)
	}
	item := BillLineItem{
		ID:                    reservedInstanceLineItemID(kind, purchase.ID, period.Start),
		BillingPeriodStart:    period.Start,
		BillingPeriodEnd:      period.End,
		BillingPeriodDays:     period.Days,
		PayerAccountID:        purchase.PayerAccountID,
		UsageAccountID:        purchase.OwnerAccountID,
		ServiceCode:           purchase.ServiceCode,
		ServiceName:           purchase.ServiceName,
		ProductFamily:         purchase.ProductFamily,
		UsageType:             "reserved-instance-" + strings.TrimSuffix(kind, "_fee") + ":" + purchase.UsageType,
		Operation:             operation,
		RegionCode:            purchase.RegionCode,
		LineItemType:          billLineItemTypeFee,
		LineItemStatus:        period.LineItemStatus,
		UsageStartTime:        period.Start + "T00:00:00Z",
		UsageEndTime:          periodEndAsRFC3339(BillingPeriod{Start: period.Start, End: period.End, Days: period.Days}),
		UsageQuantityMicros:   priceQuantityMicros,
		UsageUnit:             reservedInstanceFeeUnit,
		PricingUnit:           reservedInstancePricingUnit,
		PricingQuantityMicros: priceQuantityMicros,
		UnblendedRateMicros:   amountMicros,
		UnblendedCostMicros:   amountMicros,
		CurrencyCode:          purchase.CurrencyCode,
		PriceCatalogSKU:       purchase.PriceCatalogSKU,
		PriceEffectiveDate:    purchase.PriceEffectiveDate,
		TagSnapshot:           map[string]string{},
		Description:           description,
	}
	if err := validateBillLineItem(item); err != nil {
		return BillLineItem{}, fmt.Errorf("build reserved instance fee line item %q: %w", item.ID, err)
	}
	return item, nil
}

type reservedInstanceCoverageCandidate struct {
	Item   BillLineItem
	Source ReservedInstanceLineItemSource
}

type reservedInstanceEligibleUsage struct {
	BillLineItem
	AlreadyCoveredQuantityMicros int64
}

func reservedInstanceCoverageCreditLineItems(ctx context.Context, q reservedInstanceStore, purchase ReservedInstancePurchase, period reservedInstancePeriodRef) ([]reservedInstanceCoverageCandidate, error) {
	remaining, err := reservedInstanceRemainingCoverageQuantityMicros(ctx, q, purchase, period)
	if err != nil {
		return nil, err
	}
	if remaining <= 0 {
		return nil, nil
	}
	sources, err := listReservedInstanceEligibleUsage(ctx, q, purchase, period)
	if err != nil {
		return nil, err
	}

	var candidates []reservedInstanceCoverageCandidate
	for _, source := range sources {
		overlapQuantity, err := reservedInstanceSourceOverlapQuantityMicros(purchase, source.BillLineItem)
		if err != nil {
			return nil, err
		}
		sourceRemaining := overlapQuantity - source.AlreadyCoveredQuantityMicros
		if sourceRemaining <= 0 {
			continue
		}
		coveredQuantity := sourceRemaining
		if coveredQuantity > remaining {
			coveredQuantity = remaining
		}
		coveredCost, err := calculateCatalogCostMicros(coveredQuantity, source.UnblendedRateMicros)
		if err != nil {
			return nil, fmt.Errorf("calculate reserved instance coverage for line item %q: %w", source.ID, err)
		}
		if coveredCost <= 0 {
			continue
		}
		item := reservedInstanceCoverageCreditLineItem(purchase, period, source.BillLineItem, coveredQuantity, coveredCost)
		if err := validateBillLineItem(item); err != nil {
			return nil, fmt.Errorf("build reserved instance coverage line item %q: %w", item.ID, err)
		}
		candidates = append(candidates, reservedInstanceCoverageCandidate{
			Item: item,
			Source: ReservedInstanceLineItemSource{
				BillLineItemID:        item.ID,
				ReservedInstanceID:    purchase.ID,
				SourceBillLineItemID:  source.ID,
				LineItemKind:          reservedInstanceKindCoverageCredit,
				CoveredQuantityMicros: coveredQuantity,
				CoveredCostMicros:     coveredCost,
			},
		})
		remaining -= coveredQuantity
		if remaining <= 0 {
			break
		}
	}
	return candidates, nil
}

func listReservedInstanceEligibleUsage(ctx context.Context, q reservedInstanceStore, purchase ReservedInstancePurchase, period reservedInstancePeriodRef) ([]reservedInstanceEligibleUsage, error) {
	rows, err := q.QueryContext(
		ctx,
		`SELECT
			li.id,
			li.billing_period_start,
			li.billing_period_end,
			li.billing_period_days,
			li.payer_account_id,
			li.usage_account_id,
			li.service_code,
			li.service_name,
			li.product_family,
			li.usage_type,
			li.operation,
			li.region_code,
			li.line_item_type,
			li.line_item_status,
			li.usage_start_time,
			li.usage_end_time,
			li.usage_quantity_micros,
			li.usage_unit,
			li.pricing_unit,
			li.pricing_quantity_micros,
			li.unblended_rate_micros,
			li.unblended_cost_micros,
			li.currency_code,
			li.price_catalog_sku,
			li.price_effective_date,
			li.tag_snapshot_json,
			li.description,
			li.created_at,
			COALESCE((
				SELECT SUM(source.covered_quantity_micros)
				FROM reserved_instance_line_item_sources source
				WHERE source.source_bill_line_item_id = li.id
				  AND source.line_item_kind = ?
			), 0) AS already_covered_quantity_micros
		 FROM bill_line_items li
		 WHERE li.billing_period_start = ?
		   AND li.billing_period_end = ?
		   AND li.payer_account_id = ?
		   AND li.line_item_status = ?
		   AND li.line_item_type = ?
		   AND li.service_code = ?
		   AND li.usage_type = ?
		   AND li.operation = ?
		   AND li.region_code = ?
		   AND li.currency_code = ?
		   AND li.usage_start_time < ?
		   AND li.usage_end_time > ?
		   AND (? = ? OR li.usage_account_id = ?)
		 ORDER BY
			CASE WHEN li.usage_account_id = ? THEN 0 ELSE 1 END,
			li.usage_start_time,
			li.id`,
		reservedInstanceKindCoverageCredit,
		period.Start,
		period.End,
		period.PayerAccountID,
		period.LineItemStatus,
		billLineItemTypeUsage,
		purchase.ServiceCode,
		purchase.UsageType,
		purchase.Operation,
		purchase.RegionCode,
		purchase.CurrencyCode,
		purchase.TermEndTime,
		purchase.TermStartTime,
		purchase.SharingScope,
		reservedInstanceSharingOrg,
		purchase.OwnerAccountID,
		purchase.OwnerAccountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list reserved instance eligible usage: %w", err)
	}
	defer rows.Close()

	var usages []reservedInstanceEligibleUsage
	for rows.Next() {
		var usage reservedInstanceEligibleUsage
		var tagSnapshotJSON string
		if err := rows.Scan(
			&usage.ID,
			&usage.BillingPeriodStart,
			&usage.BillingPeriodEnd,
			&usage.BillingPeriodDays,
			&usage.PayerAccountID,
			&usage.UsageAccountID,
			&usage.ServiceCode,
			&usage.ServiceName,
			&usage.ProductFamily,
			&usage.UsageType,
			&usage.Operation,
			&usage.RegionCode,
			&usage.LineItemType,
			&usage.LineItemStatus,
			&usage.UsageStartTime,
			&usage.UsageEndTime,
			&usage.UsageQuantityMicros,
			&usage.UsageUnit,
			&usage.PricingUnit,
			&usage.PricingQuantityMicros,
			&usage.UnblendedRateMicros,
			&usage.UnblendedCostMicros,
			&usage.CurrencyCode,
			&usage.PriceCatalogSKU,
			&usage.PriceEffectiveDate,
			&tagSnapshotJSON,
			&usage.Description,
			&usage.CreatedAt,
			&usage.AlreadyCoveredQuantityMicros,
		); err != nil {
			return nil, fmt.Errorf("scan reserved instance eligible usage: %w", err)
		}
		tags, err := unmarshalStringMap(tagSnapshotJSON)
		if err != nil {
			return nil, fmt.Errorf("decode reserved instance eligible usage tags for %q: %w", usage.ID, err)
		}
		usage.TagSnapshot = tags
		usages = append(usages, usage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reserved instance eligible usage: %w", err)
	}
	return usages, nil
}

func reservedInstanceCoverageCreditLineItem(purchase ReservedInstancePurchase, period reservedInstancePeriodRef, source BillLineItem, coveredQuantityMicros, coveredCostMicros int64) BillLineItem {
	return BillLineItem{
		ID:                    reservedInstanceLineItemID(reservedInstanceKindCoverageCredit, purchase.ID, source.ID),
		BillingPeriodStart:    period.Start,
		BillingPeriodEnd:      period.End,
		BillingPeriodDays:     period.Days,
		PayerAccountID:        source.PayerAccountID,
		UsageAccountID:        source.UsageAccountID,
		ServiceCode:           source.ServiceCode,
		ServiceName:           source.ServiceName,
		ProductFamily:         source.ProductFamily,
		UsageType:             source.UsageType,
		Operation:             source.Operation,
		RegionCode:            source.RegionCode,
		LineItemType:          "Credit",
		LineItemStatus:        period.LineItemStatus,
		UsageStartTime:        source.UsageStartTime,
		UsageEndTime:          source.UsageEndTime,
		UsageQuantityMicros:   coveredQuantityMicros,
		UsageUnit:             source.UsageUnit,
		PricingUnit:           source.PricingUnit,
		PricingQuantityMicros: coveredQuantityMicros,
		UnblendedRateMicros:   source.UnblendedRateMicros,
		UnblendedCostMicros:   coveredCostMicros,
		CurrencyCode:          source.CurrencyCode,
		PriceCatalogSKU:       source.PriceCatalogSKU,
		PriceEffectiveDate:    source.PriceEffectiveDate,
		TagSnapshot:           normalizeStringMap(source.TagSnapshot),
		Description:           fmt.Sprintf("Reserved Instance %s coverage credit for source line item %s", purchase.ID, source.ID),
	}
}

func reservedInstanceSourceOverlapQuantityMicros(purchase ReservedInstancePurchase, source BillLineItem) (int64, error) {
	termStart, err := time.Parse(time.RFC3339, purchase.TermStartTime)
	if err != nil {
		return 0, fmt.Errorf("parse reserved instance term start: %w", err)
	}
	termEnd, err := time.Parse(time.RFC3339, purchase.TermEndTime)
	if err != nil {
		return 0, fmt.Errorf("parse reserved instance term end: %w", err)
	}
	sourceStart, err := time.Parse(time.RFC3339, source.UsageStartTime)
	if err != nil {
		return 0, fmt.Errorf("parse reserved instance source usage start for %q: %w", source.ID, err)
	}
	sourceEnd, err := time.Parse(time.RFC3339, source.UsageEndTime)
	if err != nil {
		return 0, fmt.Errorf("parse reserved instance source usage end for %q: %w", source.ID, err)
	}
	start := maxTime(termStart.UTC(), sourceStart.UTC())
	end := minTime(termEnd.UTC(), sourceEnd.UTC())
	if !start.Before(end) {
		return 0, nil
	}
	durationMicros := end.Sub(start).Microseconds()
	hourMicros := int64((time.Hour).Microseconds())
	quantityMicros := durationMicros * priceQuantityMicros / hourMicros
	if quantityMicros > source.PricingQuantityMicros {
		return source.PricingQuantityMicros, nil
	}
	return quantityMicros, nil
}

func reservedInstanceRemainingCoverageQuantityMicros(ctx context.Context, q reservedInstanceStore, purchase ReservedInstancePurchase, period reservedInstancePeriodRef) (int64, error) {
	available, err := reservedInstanceCoverageQuantityMicros(purchase, period)
	if err != nil {
		return 0, err
	}
	var used int64
	err = q.QueryRowContext(
		ctx,
		`SELECT COALESCE(SUM(source.covered_quantity_micros), 0)
		 FROM reserved_instance_line_item_sources source
		 JOIN bill_line_items li ON li.id = source.bill_line_item_id
		 WHERE source.reserved_instance_id = ?
		   AND source.line_item_kind = ?
		   AND li.billing_period_start = ?
		   AND li.billing_period_end = ?
		   AND li.payer_account_id = ?`,
		purchase.ID,
		reservedInstanceKindCoverageCredit,
		period.Start,
		period.End,
		period.PayerAccountID,
	).Scan(&used)
	if err != nil {
		return 0, fmt.Errorf("read reserved instance covered quantity: %w", err)
	}
	if available <= used {
		return 0, nil
	}
	return available - used, nil
}

func reservedInstanceCoverageQuantityMicros(purchase ReservedInstancePurchase, period reservedInstancePeriodRef) (int64, error) {
	termStart, err := time.Parse(time.RFC3339, purchase.TermStartTime)
	if err != nil {
		return 0, fmt.Errorf("parse reserved instance term start: %w", err)
	}
	termEnd, err := time.Parse(time.RFC3339, purchase.TermEndTime)
	if err != nil {
		return 0, fmt.Errorf("parse reserved instance term end: %w", err)
	}
	periodStart, periodEnd, err := reservedInstancePeriodTimes(period)
	if err != nil {
		return 0, err
	}
	start := maxTime(termStart.UTC(), periodStart)
	end := minTime(termEnd.UTC(), periodEnd)
	if !start.Before(end) {
		return 0, nil
	}
	durationMicros := end.Sub(start).Microseconds()
	hourMicros := int64((time.Hour).Microseconds())
	quantityMicros := durationMicros * priceQuantityMicros / hourMicros
	return int64(purchase.InstanceCount) * quantityMicros, nil
}

func reservedInstanceStartsInPeriod(purchase ReservedInstancePurchase, period reservedInstancePeriodRef) bool {
	termStart, err := time.Parse(time.RFC3339, purchase.TermStartTime)
	if err != nil {
		return false
	}
	periodStart, periodEnd, err := reservedInstancePeriodTimes(period)
	if err != nil {
		return false
	}
	termStart = termStart.UTC()
	return !termStart.Before(periodStart) && termStart.Before(periodEnd)
}

func reservedInstanceOverlapsPeriod(purchase ReservedInstancePurchase, period reservedInstancePeriodRef) bool {
	quantity, err := reservedInstanceCoverageQuantityMicros(purchase, period)
	return err == nil && quantity > 0
}

func reservedInstancePeriodTimes(period reservedInstancePeriodRef) (time.Time, time.Time, error) {
	start, err := time.Parse(time.DateOnly, period.Start)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse reserved instance period start: %w", err)
	}
	end, err := time.Parse(time.DateOnly, period.End)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse reserved instance period end: %w", err)
	}
	return start.UTC(), end.UTC(), nil
}

func upsertReservedInstanceBillLineItem(ctx context.Context, tx reservedInstanceStore, item BillLineItem) (bool, error) {
	return upsertGeneratedBillLineItem(ctx, tx, item, "reserved instance")
}

func upsertGeneratedBillLineItem(ctx context.Context, tx reservedInstanceStore, item BillLineItem, sourceLabel string) (bool, error) {
	exists, err := billLineItemExists(ctx, tx, item.ID)
	if err != nil {
		return false, err
	}
	tagSnapshotJSON, err := marshalStringMap(item.TagSnapshot)
	if err != nil {
		return false, fmt.Errorf("marshal %s line item tag snapshot for %q: %w", sourceLabel, item.ID, err)
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
		if closedErr := closedBillingPeriodMutationError(ctx, tx, item.BillingPeriodStart, item.BillingPeriodEnd, item.PayerAccountID, err); errors.Is(closedErr, ErrClosedBillingPeriod) {
			return false, closedErr
		}
		return false, fmt.Errorf("upsert %s bill line item %q: %w", sourceLabel, item.ID, err)
	}
	return !exists, nil
}

func upsertReservedInstanceLineItemSource(ctx context.Context, tx reservedInstanceStore, source ReservedInstanceLineItemSource) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO reserved_instance_line_item_sources (
			bill_line_item_id,
			reserved_instance_id,
			source_bill_line_item_id,
			line_item_kind,
			covered_quantity_micros,
			covered_cost_micros
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(bill_line_item_id, reserved_instance_id, line_item_kind) DO UPDATE SET
			source_bill_line_item_id = excluded.source_bill_line_item_id,
			covered_quantity_micros = excluded.covered_quantity_micros,
			covered_cost_micros = excluded.covered_cost_micros,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
		source.BillLineItemID,
		source.ReservedInstanceID,
		nullStringArg(source.SourceBillLineItemID),
		source.LineItemKind,
		source.CoveredQuantityMicros,
		source.CoveredCostMicros,
	)
	if err != nil {
		return fmt.Errorf("upsert reserved instance line item source for %q: %w", source.BillLineItemID, err)
	}
	return nil
}

func scanReservedInstancePurchase(row interface{ Scan(dest ...any) error }) (ReservedInstancePurchase, error) {
	var purchase ReservedInstancePurchase
	if err := row.Scan(
		&purchase.ID,
		&purchase.PayerAccountID,
		&purchase.OwnerAccountID,
		&purchase.ServiceCode,
		&purchase.ServiceName,
		&purchase.ProductFamily,
		&purchase.UsageType,
		&purchase.Operation,
		&purchase.RegionCode,
		&purchase.InstanceCount,
		&purchase.SharingScope,
		&purchase.TermStartTime,
		&purchase.TermEndTime,
		&purchase.UpfrontFeeMicros,
		&purchase.MonthlyRecurringFeeMicros,
		&purchase.CurrencyCode,
		&purchase.PriceCatalogSKU,
		&purchase.PriceEffectiveDate,
		&purchase.Status,
		&purchase.Description,
		&purchase.CreatedAt,
		&purchase.UpdatedAt,
	); err != nil {
		return ReservedInstancePurchase{}, fmt.Errorf("scan reserved instance purchase: %w", err)
	}
	return purchase, nil
}

func scanReservedInstanceLineItemSource(row interface{ Scan(dest ...any) error }) (ReservedInstanceLineItemSource, error) {
	var source ReservedInstanceLineItemSource
	var sourceBillLineItemID sql.NullString
	if err := row.Scan(
		&source.BillLineItemID,
		&source.ReservedInstanceID,
		&sourceBillLineItemID,
		&source.LineItemKind,
		&source.CoveredQuantityMicros,
		&source.CoveredCostMicros,
		&source.CreatedAt,
		&source.UpdatedAt,
	); err != nil {
		return ReservedInstanceLineItemSource{}, fmt.Errorf("scan reserved instance line item source: %w", err)
	}
	source.SourceBillLineItemID = nullStringValue(sourceBillLineItemID)
	return source, nil
}

func reservedInstanceLineItemKindForFee(item BillLineItem) string {
	if strings.Contains(item.Operation, "Upfront") {
		return reservedInstanceKindUpfrontFee
	}
	return reservedInstanceKindRecurringFee
}

func reservedInstancePurchaseID(purchase ReservedInstancePurchase) string {
	return reservedInstanceHashID("ri", purchase.PayerAccountID, purchase.OwnerAccountID, purchase.UsageType, purchase.Operation, purchase.RegionCode, purchase.TermStartTime)
}

func reservedInstanceLineItemID(kind string, parts ...string) string {
	values := append([]string{kind}, parts...)
	return reservedInstanceHashID("ri_li", values...)
}

func reservedInstanceHashID(prefix string, parts ...string) string {
	trimmed := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed = append(trimmed, strings.TrimSpace(part))
	}
	sum := sha256.Sum256([]byte(strings.Join(trimmed, "\x00")))
	return prefix + "_" + hex.EncodeToString(sum[:8])
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
