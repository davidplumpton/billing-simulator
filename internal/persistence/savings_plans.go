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
	savingsPlanProductFamily      = "Savings Plan"
	savingsPlanPlanTypeCompute    = "compute"
	savingsPlanSharingOrg         = "organization"
	savingsPlanSharingOwner       = "owner_account"
	savingsPlanStatusActive       = "active"
	savingsPlanStatusRetired      = "retired"
	savingsPlanKindUpfrontFee     = "upfront_fee"
	savingsPlanKindRecurringFee   = "recurring_fee"
	savingsPlanKindNegation       = "negation"
	savingsPlanFeeUnit            = "Fee"
	savingsPlanPricingUnit        = "USD"
	savingsPlanSupportedOperation = "RunInstances"
)

// SavingsPlanPurchase stores one simplified Compute Savings Plan commitment.
type SavingsPlanPurchase struct {
	ID                     string
	PayerAccountID         string
	OwnerAccountID         string
	PlanType               string
	ServiceCode            string
	ServiceName            string
	ProductFamily          string
	ReferenceUsageType     string
	Operation              string
	RegionCode             string
	SharingScope           string
	TermStartTime          string
	TermEndTime            string
	HourlyCommitmentMicros int64
	UpfrontFeeMicros       int64
	CurrencyCode           string
	PriceCatalogSKU        string
	PriceEffectiveDate     string
	Status                 string
	Description            string
	CreatedAt              string
	UpdatedAt              string
}

// SavingsPlanLineItemSource links generated Savings Plan rows to a purchase and covered usage.
type SavingsPlanLineItemSource struct {
	BillLineItemID                string
	SavingsPlanID                 string
	SourceBillLineItemID          string
	LineItemKind                  string
	CoveredQuantityMicros         int64
	CoveredCostMicros             int64
	AmortizedCommitmentCostMicros int64
	CreatedAt                     string
	UpdatedAt                     string
}

// SavingsPlanPurchaseCreateRequest describes a simplified Savings Plan purchase to persist.
type SavingsPlanPurchaseCreateRequest struct {
	ID                     string
	PayerAccountID         string
	OwnerAccountID         string
	PlanType               string
	ReferenceUsageType     string
	Operation              string
	RegionCode             string
	SharingScope           string
	TermStartTime          string
	TermEndTime            string
	HourlyCommitmentMicros int64
	UpfrontFeeMicros       int64
	CurrencyCode           string
	Status                 string
	Description            string
}

// SavingsPlanRepository manages simplified Savings Plan purchases and generated rows.
type SavingsPlanRepository struct {
	db      *sql.DB
	catalog PriceCatalogRepository
}

// NewSavingsPlanRepository creates a repository backed by a workspace database.
func NewSavingsPlanRepository(db *sql.DB) SavingsPlanRepository {
	return SavingsPlanRepository{
		db:      db,
		catalog: NewPriceCatalogRepository(db),
	}
}

// CreatePurchase stores one Compute Savings Plan after resolving its EC2 catalog lineage.
func (r SavingsPlanRepository) CreatePurchase(ctx context.Context, request SavingsPlanPurchaseCreateRequest) (SavingsPlanPurchase, error) {
	if r.db == nil {
		return SavingsPlanPurchase{}, fmt.Errorf("database handle is required")
	}
	purchase, err := r.purchaseFromCreateRequest(ctx, request)
	if err != nil {
		return SavingsPlanPurchase{}, err
	}
	err = WithTransaction(ctx, r.db, func(tx *sql.Tx) error {
		return insertSavingsPlanPurchase(ctx, tx, purchase)
	})
	if err != nil {
		return SavingsPlanPurchase{}, err
	}
	return purchase, nil
}

// ListLineItemSources reads Savings Plan-generated line item links for one purchase.
func (r SavingsPlanRepository) ListLineItemSources(ctx context.Context, savingsPlanID string) ([]SavingsPlanLineItemSource, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	savingsPlanID = strings.TrimSpace(savingsPlanID)
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			bill_line_item_id,
			savings_plan_id,
			source_bill_line_item_id,
			line_item_kind,
			covered_quantity_micros,
			covered_cost_micros,
			amortized_commitment_cost_micros,
			created_at,
			updated_at
		 FROM savings_plan_line_item_sources
		 WHERE (? = '' OR savings_plan_id = ?)
		 ORDER BY savings_plan_id, line_item_kind, bill_line_item_id`,
		savingsPlanID,
		savingsPlanID,
	)
	if err != nil {
		return nil, fmt.Errorf("list savings plan line item sources: %w", err)
	}
	defer rows.Close()

	var sources []SavingsPlanLineItemSource
	for rows.Next() {
		source, err := scanSavingsPlanLineItemSource(rows)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate savings plan line item sources: %w", err)
	}
	return sources, nil
}

func (r SavingsPlanRepository) purchaseFromCreateRequest(ctx context.Context, request SavingsPlanPurchaseCreateRequest) (SavingsPlanPurchase, error) {
	request = normalizeSavingsPlanPurchaseCreateRequest(request)
	if err := validateSavingsPlanPurchaseCreateRequest(request); err != nil {
		return SavingsPlanPurchase{}, err
	}
	termStart, err := time.Parse(time.RFC3339, request.TermStartTime)
	if err != nil {
		return SavingsPlanPurchase{}, fmt.Errorf("savings plan term start must use RFC3339: %w", err)
	}
	termEnd, err := time.Parse(time.RFC3339, request.TermEndTime)
	if err != nil {
		return SavingsPlanPurchase{}, fmt.Errorf("savings plan term end must use RFC3339: %w", err)
	}
	request.TermStartTime = termStart.UTC().Format(time.RFC3339)
	request.TermEndTime = termEnd.UTC().Format(time.RFC3339)
	period, err := BillingPeriodForTime(termStart)
	if err != nil {
		return SavingsPlanPurchase{}, err
	}
	lookup, err := r.catalog.Lookup(ctx, PriceLookupRequest{
		ServiceCode:         serviceAmazonEC2,
		UsageType:           request.ReferenceUsageType,
		Operation:           request.Operation,
		RegionCode:          request.RegionCode,
		UsageUnit:           "Hours",
		UsageQuantityMicros: priceQuantityMicros,
		UsageDate:           termStart.UTC().Format(time.DateOnly),
		BillingPeriodDays:   period.Days,
	})
	if err != nil {
		return SavingsPlanPurchase{}, fmt.Errorf("resolve savings plan catalog lineage: %w", err)
	}
	purchase := SavingsPlanPurchase{
		ID:                     request.ID,
		PayerAccountID:         request.PayerAccountID,
		OwnerAccountID:         request.OwnerAccountID,
		PlanType:               request.PlanType,
		ServiceCode:            lookup.Item.ServiceCode,
		ServiceName:            lookup.Item.ServiceName,
		ProductFamily:          savingsPlanProductFamily,
		ReferenceUsageType:     request.ReferenceUsageType,
		Operation:              request.Operation,
		RegionCode:             request.RegionCode,
		SharingScope:           request.SharingScope,
		TermStartTime:          request.TermStartTime,
		TermEndTime:            request.TermEndTime,
		HourlyCommitmentMicros: request.HourlyCommitmentMicros,
		UpfrontFeeMicros:       request.UpfrontFeeMicros,
		CurrencyCode:           request.CurrencyCode,
		PriceCatalogSKU:        lookup.Item.SKU,
		PriceEffectiveDate:     lookup.Item.EffectiveDate,
		Status:                 request.Status,
		Description:            request.Description,
	}
	if purchase.ID == "" {
		purchase.ID = savingsPlanPurchaseID(purchase)
	}
	if err := validateSavingsPlanPurchase(purchase); err != nil {
		return SavingsPlanPurchase{}, err
	}
	return purchase, nil
}

func normalizeSavingsPlanPurchaseCreateRequest(request SavingsPlanPurchaseCreateRequest) SavingsPlanPurchaseCreateRequest {
	request.ID = strings.TrimSpace(request.ID)
	request.PayerAccountID = strings.TrimSpace(request.PayerAccountID)
	request.OwnerAccountID = strings.TrimSpace(request.OwnerAccountID)
	request.PlanType = strings.TrimSpace(request.PlanType)
	if request.PlanType == "" {
		request.PlanType = savingsPlanPlanTypeCompute
	}
	request.ReferenceUsageType = strings.TrimSpace(request.ReferenceUsageType)
	request.Operation = strings.TrimSpace(request.Operation)
	if request.Operation == "" {
		request.Operation = savingsPlanSupportedOperation
	}
	request.RegionCode = strings.TrimSpace(request.RegionCode)
	request.SharingScope = strings.TrimSpace(request.SharingScope)
	if request.SharingScope == "" {
		request.SharingScope = savingsPlanSharingOrg
	}
	request.TermStartTime = strings.TrimSpace(request.TermStartTime)
	request.TermEndTime = strings.TrimSpace(request.TermEndTime)
	request.CurrencyCode = strings.TrimSpace(request.CurrencyCode)
	if request.CurrencyCode == "" {
		request.CurrencyCode = defaultBillCurrencyCode
	}
	request.Status = strings.TrimSpace(request.Status)
	if request.Status == "" {
		request.Status = savingsPlanStatusActive
	}
	request.Description = strings.TrimSpace(request.Description)
	return request
}

func validateSavingsPlanPurchaseCreateRequest(request SavingsPlanPurchaseCreateRequest) error {
	if request.PayerAccountID == "" {
		return fmt.Errorf("savings plan payer account ID is required")
	}
	if request.OwnerAccountID == "" {
		return fmt.Errorf("savings plan owner account ID is required")
	}
	if request.PlanType != savingsPlanPlanTypeCompute {
		return fmt.Errorf("unsupported savings plan type %q; only compute savings plans are supported", request.PlanType)
	}
	if request.ReferenceUsageType == "" || request.Operation == "" || request.RegionCode == "" {
		return fmt.Errorf("savings plan EC2 reference price dimensions are required")
	}
	if request.Operation != savingsPlanSupportedOperation {
		return fmt.Errorf("unsupported savings plan operation %q; only EC2 RunInstances hourly usage is supported", request.Operation)
	}
	if !isSavingsPlanSharingScope(request.SharingScope) {
		return fmt.Errorf("unsupported savings plan sharing scope %q", request.SharingScope)
	}
	termStart, err := time.Parse(time.RFC3339, request.TermStartTime)
	if err != nil {
		return fmt.Errorf("savings plan term start must use RFC3339: %w", err)
	}
	termEnd, err := time.Parse(time.RFC3339, request.TermEndTime)
	if err != nil {
		return fmt.Errorf("savings plan term end must use RFC3339: %w", err)
	}
	if !termStart.Before(termEnd) {
		return fmt.Errorf("savings plan term start must be before end")
	}
	if request.HourlyCommitmentMicros <= 0 {
		return fmt.Errorf("savings plan hourly commitment must be greater than zero")
	}
	if request.UpfrontFeeMicros < 0 {
		return fmt.Errorf("savings plan upfront fee cannot be negative")
	}
	if request.CurrencyCode == "" {
		return fmt.Errorf("savings plan currency is required")
	}
	if !isSavingsPlanStatus(request.Status) {
		return fmt.Errorf("unsupported savings plan status %q", request.Status)
	}
	return nil
}

func validateSavingsPlanPurchase(purchase SavingsPlanPurchase) error {
	if purchase.ID == "" {
		return fmt.Errorf("savings plan ID is required")
	}
	if purchase.ServiceCode != serviceAmazonEC2 || purchase.Operation != savingsPlanSupportedOperation {
		return fmt.Errorf("savings plan coverage currently supports AmazonEC2 RunInstances hourly usage only")
	}
	if purchase.ServiceName == "" || purchase.ProductFamily == "" {
		return fmt.Errorf("savings plan service metadata is required")
	}
	if purchase.PriceCatalogSKU == "" || purchase.PriceEffectiveDate == "" {
		return fmt.Errorf("savings plan price catalog lineage is required")
	}
	return nil
}

func isSavingsPlanSharingScope(value string) bool {
	switch value {
	case savingsPlanSharingOrg, savingsPlanSharingOwner:
		return true
	default:
		return false
	}
}

func isSavingsPlanStatus(value string) bool {
	switch value {
	case savingsPlanStatusActive, savingsPlanStatusRetired:
		return true
	default:
		return false
	}
}

func insertSavingsPlanPurchase(ctx context.Context, tx *sql.Tx, purchase SavingsPlanPurchase) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO savings_plan_purchases (
			id,
			payer_account_id,
			owner_account_id,
			plan_type,
			service_code,
			service_name,
			product_family,
			reference_usage_type,
			operation,
			region_code,
			sharing_scope,
			term_start_time,
			term_end_time,
			hourly_commitment_micros,
			upfront_fee_micros,
			currency_code,
			price_catalog_sku,
			price_effective_date,
			status,
			description
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		purchase.ID,
		purchase.PayerAccountID,
		purchase.OwnerAccountID,
		purchase.PlanType,
		purchase.ServiceCode,
		purchase.ServiceName,
		purchase.ProductFamily,
		purchase.ReferenceUsageType,
		purchase.Operation,
		purchase.RegionCode,
		purchase.SharingScope,
		purchase.TermStartTime,
		purchase.TermEndTime,
		purchase.HourlyCommitmentMicros,
		purchase.UpfrontFeeMicros,
		purchase.CurrencyCode,
		purchase.PriceCatalogSKU,
		purchase.PriceEffectiveDate,
		purchase.Status,
		purchase.Description,
	)
	if err != nil {
		return fmt.Errorf("insert savings plan purchase %q: %w", purchase.ID, err)
	}
	return nil
}

type savingsPlanPeriodRef struct {
	Start          string
	End            string
	Days           int
	PayerAccountID string
	LineItemStatus string
}

func (r SavingsPlanRepository) generateLineItemsInTx(ctx context.Context, tx *sql.Tx, sourceItems []BillLineItem, lineItemStatus string) (BillLineItemGenerationResult, error) {
	periods := savingsPlanPeriodRefsForLineItems(sourceItems, lineItemStatus)
	result := BillLineItemGenerationResult{}
	for _, period := range periods {
		purchases, err := listActiveSavingsPlanPurchases(ctx, tx, period)
		if err != nil {
			return BillLineItemGenerationResult{}, err
		}
		for _, purchase := range purchases {
			feeItems, err := savingsPlanFeeLineItems(purchase, period)
			if err != nil {
				return BillLineItemGenerationResult{}, err
			}
			for _, item := range feeItems {
				created, err := upsertSavingsPlanBillLineItem(ctx, tx, item)
				if err != nil {
					return BillLineItemGenerationResult{}, err
				}
				source := SavingsPlanLineItemSource{
					BillLineItemID: item.ID,
					SavingsPlanID:  purchase.ID,
					LineItemKind:   savingsPlanLineItemKindForFee(item),
				}
				if err := upsertSavingsPlanLineItemSource(ctx, tx, source); err != nil {
					return BillLineItemGenerationResult{}, err
				}
				if created {
					result.ItemsCreated++
					result.Items = append(result.Items, item)
				}
			}
			negationItems, err := savingsPlanNegationLineItems(ctx, tx, purchase, period)
			if err != nil {
				return BillLineItemGenerationResult{}, err
			}
			for _, candidate := range negationItems {
				created, err := upsertSavingsPlanBillLineItem(ctx, tx, candidate.Item)
				if err != nil {
					return BillLineItemGenerationResult{}, err
				}
				if err := upsertSavingsPlanLineItemSource(ctx, tx, candidate.Source); err != nil {
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

func savingsPlanPeriodRefsForLineItems(items []BillLineItem, lineItemStatus string) []savingsPlanPeriodRef {
	seen := map[string]struct{}{}
	var periods []savingsPlanPeriodRef
	for _, item := range items {
		if item.LineItemType != billLineItemTypeUsage {
			continue
		}
		key := strings.Join([]string{item.BillingPeriodStart, item.BillingPeriodEnd, item.PayerAccountID, lineItemStatus}, "\x00")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		periods = append(periods, savingsPlanPeriodRef{
			Start:          item.BillingPeriodStart,
			End:            item.BillingPeriodEnd,
			Days:           item.BillingPeriodDays,
			PayerAccountID: item.PayerAccountID,
			LineItemStatus: lineItemStatus,
		})
	}
	return periods
}

func listActiveSavingsPlanPurchases(ctx context.Context, q reservedInstanceStore, period savingsPlanPeriodRef) ([]SavingsPlanPurchase, error) {
	rows, err := q.QueryContext(
		ctx,
		`SELECT
			id,
			payer_account_id,
			owner_account_id,
			plan_type,
			service_code,
			service_name,
			product_family,
			reference_usage_type,
			operation,
			region_code,
			sharing_scope,
			term_start_time,
			term_end_time,
			hourly_commitment_micros,
			upfront_fee_micros,
			currency_code,
			price_catalog_sku,
			price_effective_date,
			status,
			description,
			created_at,
			updated_at
		 FROM savings_plan_purchases
		 WHERE payer_account_id = ?
		   AND status = ?
		   AND term_start_time < ?
		   AND term_end_time > ?
		 ORDER BY term_start_time, owner_account_id, id`,
		period.PayerAccountID,
		savingsPlanStatusActive,
		period.End+"T00:00:00Z",
		period.Start+"T00:00:00Z",
	)
	if err != nil {
		return nil, fmt.Errorf("list active savings plan purchases: %w", err)
	}
	defer rows.Close()

	var purchases []SavingsPlanPurchase
	for rows.Next() {
		purchase, err := scanSavingsPlanPurchase(rows)
		if err != nil {
			return nil, err
		}
		purchases = append(purchases, purchase)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active savings plan purchases: %w", err)
	}
	return purchases, nil
}

func savingsPlanFeeLineItems(purchase SavingsPlanPurchase, period savingsPlanPeriodRef) ([]BillLineItem, error) {
	var items []BillLineItem
	if purchase.UpfrontFeeMicros > 0 && savingsPlanStartsInPeriod(purchase, period) {
		item, err := savingsPlanFeeLineItem(purchase, period, savingsPlanKindUpfrontFee, purchase.UpfrontFeeMicros)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	recurringFee, err := savingsPlanCommitmentMicros(purchase, period)
	if err != nil {
		return nil, err
	}
	if recurringFee > 0 {
		item, err := savingsPlanFeeLineItem(purchase, period, savingsPlanKindRecurringFee, recurringFee)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func savingsPlanFeeLineItem(purchase SavingsPlanPurchase, period savingsPlanPeriodRef, kind string, amountMicros int64) (BillLineItem, error) {
	operation := "SavingsPlanRecurringFee"
	description := fmt.Sprintf("Savings Plan %s recurring commitment charge", purchase.ID)
	if kind == savingsPlanKindUpfrontFee {
		operation = "SavingsPlanUpfrontFee"
		description = fmt.Sprintf("Savings Plan %s upfront commitment charge", purchase.ID)
	}
	item := BillLineItem{
		ID:                    savingsPlanLineItemID(kind, purchase.ID, period.Start),
		BillingPeriodStart:    period.Start,
		BillingPeriodEnd:      period.End,
		BillingPeriodDays:     period.Days,
		PayerAccountID:        purchase.PayerAccountID,
		UsageAccountID:        purchase.OwnerAccountID,
		ServiceCode:           purchase.ServiceCode,
		ServiceName:           purchase.ServiceName,
		ProductFamily:         purchase.ProductFamily,
		UsageType:             "savings-plan-" + strings.TrimSuffix(kind, "_fee") + ":" + purchase.ReferenceUsageType,
		Operation:             operation,
		RegionCode:            purchase.RegionCode,
		LineItemType:          billLineItemTypeFee,
		LineItemStatus:        period.LineItemStatus,
		UsageStartTime:        period.Start + "T00:00:00Z",
		UsageEndTime:          periodEndAsRFC3339(BillingPeriod{Start: period.Start, End: period.End, Days: period.Days}),
		UsageQuantityMicros:   priceQuantityMicros,
		UsageUnit:             savingsPlanFeeUnit,
		PricingUnit:           savingsPlanPricingUnit,
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
		return BillLineItem{}, fmt.Errorf("build savings plan fee line item %q: %w", item.ID, err)
	}
	return item, nil
}

type savingsPlanNegationCandidate struct {
	Item   BillLineItem
	Source SavingsPlanLineItemSource
}

type savingsPlanEligibleUsage struct {
	BillLineItem
	AlreadyCoveredQuantityMicros int64
	AlreadyCoveredCostMicros     int64
}

func savingsPlanNegationLineItems(ctx context.Context, q reservedInstanceStore, purchase SavingsPlanPurchase, period savingsPlanPeriodRef) ([]savingsPlanNegationCandidate, error) {
	remaining, err := savingsPlanRemainingCommitmentMicros(ctx, q, purchase, period)
	if err != nil {
		return nil, err
	}
	if remaining <= 0 {
		return nil, nil
	}
	amortizationNumerator, amortizationDenominator, err := savingsPlanAmortizationRatio(purchase, period)
	if err != nil {
		return nil, err
	}
	sources, err := listSavingsPlanEligibleUsage(ctx, q, purchase, period)
	if err != nil {
		return nil, err
	}

	var candidates []savingsPlanNegationCandidate
	for _, source := range sources {
		overlapQuantity, err := savingsPlanSourceOverlapQuantityMicros(purchase, source.BillLineItem)
		if err != nil {
			return nil, err
		}
		overlapCost, err := calculateCatalogCostMicros(overlapQuantity, source.UnblendedRateMicros)
		if err != nil {
			return nil, fmt.Errorf("calculate savings plan eligible source cost for line item %q: %w", source.ID, err)
		}
		sourceRemainingCost := overlapCost - source.AlreadyCoveredCostMicros
		sourceRemainingQuantity := overlapQuantity - source.AlreadyCoveredQuantityMicros
		if sourceRemainingCost <= 0 || sourceRemainingQuantity <= 0 {
			continue
		}
		coveredCost := sourceRemainingCost
		if coveredCost > remaining {
			coveredCost = remaining
		}
		coveredQuantity := sourceRemainingQuantity
		if coveredCost < sourceRemainingCost {
			coveredQuantity = sourceRemainingQuantity * coveredCost / sourceRemainingCost
		}
		if coveredQuantity <= 0 || coveredCost <= 0 {
			continue
		}
		amortizedCost, err := savingsPlanAmortizedCommitmentCostMicros(coveredCost, amortizationNumerator, amortizationDenominator)
		if err != nil {
			return nil, fmt.Errorf("calculate savings plan amortized commitment for line item %q: %w", source.ID, err)
		}
		item := savingsPlanNegationLineItem(purchase, period, source.BillLineItem, coveredQuantity, coveredCost)
		if err := validateBillLineItem(item); err != nil {
			return nil, fmt.Errorf("build savings plan negation line item %q: %w", item.ID, err)
		}
		candidates = append(candidates, savingsPlanNegationCandidate{
			Item: item,
			Source: SavingsPlanLineItemSource{
				BillLineItemID:                item.ID,
				SavingsPlanID:                 purchase.ID,
				SourceBillLineItemID:          source.ID,
				LineItemKind:                  savingsPlanKindNegation,
				CoveredQuantityMicros:         coveredQuantity,
				CoveredCostMicros:             coveredCost,
				AmortizedCommitmentCostMicros: amortizedCost,
			},
		})
		remaining -= coveredCost
		if remaining <= 0 {
			break
		}
	}
	return candidates, nil
}

func listSavingsPlanEligibleUsage(ctx context.Context, q reservedInstanceStore, purchase SavingsPlanPurchase, period savingsPlanPeriodRef) ([]savingsPlanEligibleUsage, error) {
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
			), 0) + COALESCE((
				SELECT SUM(source.covered_quantity_micros)
				FROM savings_plan_line_item_sources source
				WHERE source.source_bill_line_item_id = li.id
				  AND source.line_item_kind = ?
			), 0) AS already_covered_quantity_micros,
			COALESCE((
				SELECT SUM(source.covered_cost_micros)
				FROM reserved_instance_line_item_sources source
				WHERE source.source_bill_line_item_id = li.id
				  AND source.line_item_kind = ?
			), 0) + COALESCE((
				SELECT SUM(source.covered_cost_micros)
				FROM savings_plan_line_item_sources source
				WHERE source.source_bill_line_item_id = li.id
				  AND source.line_item_kind = ?
			), 0) AS already_covered_cost_micros
		 FROM bill_line_items li
		 WHERE li.billing_period_start = ?
		   AND li.billing_period_end = ?
		   AND li.payer_account_id = ?
		   AND li.line_item_status = ?
		   AND li.line_item_type = ?
		   AND li.service_code = ?
		   AND li.operation = ?
		   AND li.region_code = ?
		   AND li.currency_code = ?
		   AND li.usage_start_time < ?
		   AND li.usage_end_time > ?
		   AND (? = ? OR li.usage_account_id = ?)
		   AND li.unblended_cost_micros > 0
		 ORDER BY
			CASE WHEN li.usage_account_id = ? THEN 0 ELSE 1 END,
			li.usage_start_time,
			li.id`,
		reservedInstanceKindCoverageCredit,
		savingsPlanKindNegation,
		reservedInstanceKindCoverageCredit,
		savingsPlanKindNegation,
		period.Start,
		period.End,
		period.PayerAccountID,
		period.LineItemStatus,
		billLineItemTypeUsage,
		purchase.ServiceCode,
		purchase.Operation,
		purchase.RegionCode,
		purchase.CurrencyCode,
		purchase.TermEndTime,
		purchase.TermStartTime,
		purchase.SharingScope,
		savingsPlanSharingOrg,
		purchase.OwnerAccountID,
		purchase.OwnerAccountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list savings plan eligible usage: %w", err)
	}
	defer rows.Close()

	var usages []savingsPlanEligibleUsage
	for rows.Next() {
		var usage savingsPlanEligibleUsage
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
			&usage.AlreadyCoveredCostMicros,
		); err != nil {
			return nil, fmt.Errorf("scan savings plan eligible usage: %w", err)
		}
		tags, err := unmarshalStringMap(tagSnapshotJSON)
		if err != nil {
			return nil, fmt.Errorf("decode savings plan eligible usage tags for %q: %w", usage.ID, err)
		}
		usage.TagSnapshot = tags
		usages = append(usages, usage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate savings plan eligible usage: %w", err)
	}
	return usages, nil
}

func savingsPlanNegationLineItem(purchase SavingsPlanPurchase, period savingsPlanPeriodRef, source BillLineItem, coveredQuantityMicros, coveredCostMicros int64) BillLineItem {
	return BillLineItem{
		ID:                    savingsPlanLineItemID(savingsPlanKindNegation, purchase.ID, source.ID),
		BillingPeriodStart:    period.Start,
		BillingPeriodEnd:      period.End,
		BillingPeriodDays:     period.Days,
		PayerAccountID:        source.PayerAccountID,
		UsageAccountID:        source.UsageAccountID,
		ServiceCode:           source.ServiceCode,
		ServiceName:           source.ServiceName,
		ProductFamily:         source.ProductFamily,
		UsageType:             source.UsageType,
		Operation:             "SavingsPlanNegation",
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
		Description:           fmt.Sprintf("Savings Plan %s negation for source line item %s", purchase.ID, source.ID),
	}
}

func savingsPlanSourceOverlapQuantityMicros(purchase SavingsPlanPurchase, source BillLineItem) (int64, error) {
	termStart, err := time.Parse(time.RFC3339, purchase.TermStartTime)
	if err != nil {
		return 0, fmt.Errorf("parse savings plan term start: %w", err)
	}
	termEnd, err := time.Parse(time.RFC3339, purchase.TermEndTime)
	if err != nil {
		return 0, fmt.Errorf("parse savings plan term end: %w", err)
	}
	sourceStart, err := time.Parse(time.RFC3339, source.UsageStartTime)
	if err != nil {
		return 0, fmt.Errorf("parse savings plan source usage start for %q: %w", source.ID, err)
	}
	sourceEnd, err := time.Parse(time.RFC3339, source.UsageEndTime)
	if err != nil {
		return 0, fmt.Errorf("parse savings plan source usage end for %q: %w", source.ID, err)
	}
	start := maxTime(termStart.UTC(), sourceStart.UTC())
	end := minTime(termEnd.UTC(), sourceEnd.UTC())
	if !start.Before(end) {
		return 0, nil
	}
	durationMicros := end.Sub(start).Microseconds()
	sourceDurationMicros := sourceEnd.UTC().Sub(sourceStart.UTC()).Microseconds()
	if sourceDurationMicros <= 0 {
		return 0, nil
	}
	quantityMicros := source.PricingQuantityMicros * durationMicros / sourceDurationMicros
	if quantityMicros > source.PricingQuantityMicros {
		return source.PricingQuantityMicros, nil
	}
	return quantityMicros, nil
}

func savingsPlanRemainingCommitmentMicros(ctx context.Context, q reservedInstanceStore, purchase SavingsPlanPurchase, period savingsPlanPeriodRef) (int64, error) {
	available, err := savingsPlanCommitmentMicros(purchase, period)
	if err != nil {
		return 0, err
	}
	var used int64
	err = q.QueryRowContext(
		ctx,
		`SELECT COALESCE(SUM(source.covered_cost_micros), 0)
		 FROM savings_plan_line_item_sources source
		 JOIN bill_line_items li ON li.id = source.bill_line_item_id
		 WHERE source.savings_plan_id = ?
		   AND source.line_item_kind = ?
		   AND li.billing_period_start = ?
		   AND li.billing_period_end = ?
		   AND li.payer_account_id = ?`,
		purchase.ID,
		savingsPlanKindNegation,
		period.Start,
		period.End,
		period.PayerAccountID,
	).Scan(&used)
	if err != nil {
		return 0, fmt.Errorf("read savings plan covered cost: %w", err)
	}
	if available <= used {
		return 0, nil
	}
	return available - used, nil
}

func savingsPlanCommitmentMicros(purchase SavingsPlanPurchase, period savingsPlanPeriodRef) (int64, error) {
	overlapHours, err := savingsPlanOverlapHourMicros(purchase, period)
	if err != nil {
		return 0, err
	}
	if overlapHours <= 0 {
		return 0, nil
	}
	return calculateCatalogCostMicros(overlapHours, purchase.HourlyCommitmentMicros)
}

func savingsPlanAmortizationRatio(purchase SavingsPlanPurchase, period savingsPlanPeriodRef) (numerator int64, denominator int64, err error) {
	commitment, err := savingsPlanCommitmentMicros(purchase, period)
	if err != nil {
		return 0, 0, err
	}
	if commitment <= 0 {
		return 0, 0, nil
	}
	amortizedUpfront, err := savingsPlanAmortizedUpfrontMicros(purchase, period)
	if err != nil {
		return 0, 0, err
	}
	return commitment + amortizedUpfront, commitment, nil
}

func savingsPlanAmortizedCommitmentCostMicros(coveredCostMicros int64, numerator int64, denominator int64) (int64, error) {
	if coveredCostMicros <= 0 || numerator <= 0 || denominator <= 0 {
		return 0, nil
	}
	if coveredCostMicros > maxPriceCalculationMicros/numerator {
		return 0, fmt.Errorf("savings plan amortized cost calculation overflow")
	}
	return coveredCostMicros * numerator / denominator, nil
}

func savingsPlanAmortizedUpfrontMicros(purchase SavingsPlanPurchase, period savingsPlanPeriodRef) (int64, error) {
	if purchase.UpfrontFeeMicros <= 0 {
		return 0, nil
	}
	termStart, err := time.Parse(time.RFC3339, purchase.TermStartTime)
	if err != nil {
		return 0, fmt.Errorf("parse savings plan term start: %w", err)
	}
	termEnd, err := time.Parse(time.RFC3339, purchase.TermEndTime)
	if err != nil {
		return 0, fmt.Errorf("parse savings plan term end: %w", err)
	}
	termDurationMicros := termEnd.UTC().Sub(termStart.UTC()).Microseconds()
	if termDurationMicros <= 0 {
		return 0, nil
	}
	periodStart, periodEnd, err := savingsPlanPeriodTimes(period)
	if err != nil {
		return 0, err
	}
	start := maxTime(termStart.UTC(), periodStart)
	end := minTime(termEnd.UTC(), periodEnd)
	if !start.Before(end) {
		return 0, nil
	}
	overlapMicros := end.Sub(start).Microseconds()
	if purchase.UpfrontFeeMicros > maxPriceCalculationMicros/overlapMicros {
		return 0, fmt.Errorf("savings plan upfront amortization calculation overflow")
	}
	return purchase.UpfrontFeeMicros * overlapMicros / termDurationMicros, nil
}

func savingsPlanOverlapHourMicros(purchase SavingsPlanPurchase, period savingsPlanPeriodRef) (int64, error) {
	termStart, err := time.Parse(time.RFC3339, purchase.TermStartTime)
	if err != nil {
		return 0, fmt.Errorf("parse savings plan term start: %w", err)
	}
	termEnd, err := time.Parse(time.RFC3339, purchase.TermEndTime)
	if err != nil {
		return 0, fmt.Errorf("parse savings plan term end: %w", err)
	}
	periodStart, periodEnd, err := savingsPlanPeriodTimes(period)
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
	return durationMicros * priceQuantityMicros / hourMicros, nil
}

func savingsPlanStartsInPeriod(purchase SavingsPlanPurchase, period savingsPlanPeriodRef) bool {
	termStart, err := time.Parse(time.RFC3339, purchase.TermStartTime)
	if err != nil {
		return false
	}
	periodStart, periodEnd, err := savingsPlanPeriodTimes(period)
	if err != nil {
		return false
	}
	termStart = termStart.UTC()
	return !termStart.Before(periodStart) && termStart.Before(periodEnd)
}

func savingsPlanPeriodTimes(period savingsPlanPeriodRef) (time.Time, time.Time, error) {
	start, err := time.Parse(time.DateOnly, period.Start)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse savings plan period start: %w", err)
	}
	end, err := time.Parse(time.DateOnly, period.End)
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("parse savings plan period end: %w", err)
	}
	return start.UTC(), end.UTC(), nil
}

func upsertSavingsPlanBillLineItem(ctx context.Context, tx reservedInstanceStore, item BillLineItem) (bool, error) {
	return upsertGeneratedBillLineItem(ctx, tx, item, "savings plan")
}

func upsertSavingsPlanLineItemSource(ctx context.Context, tx reservedInstanceStore, source SavingsPlanLineItemSource) error {
	_, err := tx.ExecContext(
		ctx,
		`INSERT INTO savings_plan_line_item_sources (
			bill_line_item_id,
			savings_plan_id,
			source_bill_line_item_id,
			line_item_kind,
			covered_quantity_micros,
			covered_cost_micros,
			amortized_commitment_cost_micros
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(bill_line_item_id, savings_plan_id, line_item_kind) DO UPDATE SET
			source_bill_line_item_id = excluded.source_bill_line_item_id,
			covered_quantity_micros = excluded.covered_quantity_micros,
			covered_cost_micros = excluded.covered_cost_micros,
			amortized_commitment_cost_micros = excluded.amortized_commitment_cost_micros,
			updated_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now')`,
		source.BillLineItemID,
		source.SavingsPlanID,
		nullStringArg(source.SourceBillLineItemID),
		source.LineItemKind,
		source.CoveredQuantityMicros,
		source.CoveredCostMicros,
		source.AmortizedCommitmentCostMicros,
	)
	if err != nil {
		return fmt.Errorf("upsert savings plan line item source for %q: %w", source.BillLineItemID, err)
	}
	return nil
}

func scanSavingsPlanPurchase(row interface{ Scan(dest ...any) error }) (SavingsPlanPurchase, error) {
	var purchase SavingsPlanPurchase
	if err := row.Scan(
		&purchase.ID,
		&purchase.PayerAccountID,
		&purchase.OwnerAccountID,
		&purchase.PlanType,
		&purchase.ServiceCode,
		&purchase.ServiceName,
		&purchase.ProductFamily,
		&purchase.ReferenceUsageType,
		&purchase.Operation,
		&purchase.RegionCode,
		&purchase.SharingScope,
		&purchase.TermStartTime,
		&purchase.TermEndTime,
		&purchase.HourlyCommitmentMicros,
		&purchase.UpfrontFeeMicros,
		&purchase.CurrencyCode,
		&purchase.PriceCatalogSKU,
		&purchase.PriceEffectiveDate,
		&purchase.Status,
		&purchase.Description,
		&purchase.CreatedAt,
		&purchase.UpdatedAt,
	); err != nil {
		return SavingsPlanPurchase{}, fmt.Errorf("scan savings plan purchase: %w", err)
	}
	return purchase, nil
}

func scanSavingsPlanLineItemSource(row interface{ Scan(dest ...any) error }) (SavingsPlanLineItemSource, error) {
	var source SavingsPlanLineItemSource
	var sourceBillLineItemID sql.NullString
	if err := row.Scan(
		&source.BillLineItemID,
		&source.SavingsPlanID,
		&sourceBillLineItemID,
		&source.LineItemKind,
		&source.CoveredQuantityMicros,
		&source.CoveredCostMicros,
		&source.AmortizedCommitmentCostMicros,
		&source.CreatedAt,
		&source.UpdatedAt,
	); err != nil {
		return SavingsPlanLineItemSource{}, fmt.Errorf("scan savings plan line item source: %w", err)
	}
	source.SourceBillLineItemID = nullStringValue(sourceBillLineItemID)
	return source, nil
}

func savingsPlanLineItemKindForFee(item BillLineItem) string {
	if strings.Contains(item.Operation, "Upfront") {
		return savingsPlanKindUpfrontFee
	}
	return savingsPlanKindRecurringFee
}

func savingsPlanPurchaseID(purchase SavingsPlanPurchase) string {
	return savingsPlanHashID("sp", purchase.PayerAccountID, purchase.OwnerAccountID, purchase.ReferenceUsageType, purchase.Operation, purchase.RegionCode, purchase.TermStartTime)
}

func savingsPlanLineItemID(kind string, parts ...string) string {
	values := append([]string{kind}, parts...)
	return savingsPlanHashID("sp_li", values...)
}

func savingsPlanHashID(prefix string, parts ...string) string {
	trimmed := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed = append(trimmed, strings.TrimSpace(part))
	}
	sum := sha256.Sum256([]byte(strings.Join(trimmed, "\x00")))
	return prefix + "_" + hex.EncodeToString(sum[:8])
}
