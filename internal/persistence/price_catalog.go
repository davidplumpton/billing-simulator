package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	priceQuantityMicros       = int64(1_000_000)
	maxPriceCalculationMicros = int64(1<<63 - 1)
)

var (
	// ErrPriceCatalogRateNotFound marks lookup failures where no catalog row matches a metering record.
	ErrPriceCatalogRateNotFound = errors.New("price catalog rate not found")

	// ErrUnsupportedUnitConversion marks lookup failures where the catalog cannot normalize the metered unit.
	ErrUnsupportedUnitConversion = errors.New("unsupported price catalog unit conversion")
)

// PriceCatalogItem stores one versioned synthetic catalog rate.
type PriceCatalogItem struct {
	SKU            string
	ServiceCode    string
	ServiceName    string
	ProductFamily  string
	UsageType      string
	Operation      string
	RegionCode     string
	Unit           string
	RateMicros     int64
	CurrencyCode   string
	EffectiveDate  string
	PriceSource    string
	PricingFormula string
	Notes          string
}

// PriceLookupRequest describes the metering fields required to price one usage record.
type PriceLookupRequest struct {
	ServiceCode         string
	UsageType           string
	Operation           string
	RegionCode          string
	UsageUnit           string
	UsageQuantityMicros int64
	UsageDate           string
	BillingPeriodDays   int
}

// PriceLookupResult contains the matched catalog item and normalized billing math.
type PriceLookupResult struct {
	Item                PriceCatalogItem
	UsageQuantityMicros int64
	CostMicros          int64
}

// PriceCatalogRepository reads catalog items seeded into a workspace database.
type PriceCatalogRepository struct {
	db *sql.DB
}

// NewPriceCatalogRepository creates a repository backed by a workspace database.
func NewPriceCatalogRepository(db *sql.DB) PriceCatalogRepository {
	return PriceCatalogRepository{db: db}
}

// Lookup maps a metering record to the best catalog item and computes unblended cost.
func (r PriceCatalogRepository) Lookup(ctx context.Context, request PriceLookupRequest) (PriceLookupResult, error) {
	request = normalizePriceLookupRequest(request)
	if err := validatePriceLookupRequest(request); err != nil {
		return PriceLookupResult{}, err
	}
	if r.db == nil {
		return PriceLookupResult{}, fmt.Errorf("database handle is required")
	}

	item, err := r.lookupCatalogItem(ctx, request)
	if errors.Is(err, sql.ErrNoRows) {
		return PriceLookupResult{}, formatPriceNotFoundError(request)
	}
	if err != nil {
		return PriceLookupResult{}, err
	}

	normalizedQuantity, err := convertUsageQuantityMicros(
		request.UsageQuantityMicros,
		request.UsageUnit,
		item.Unit,
		request.BillingPeriodDays,
	)
	if err != nil {
		return PriceLookupResult{}, fmt.Errorf("convert usage from %q to catalog unit %q for SKU %q: %w", request.UsageUnit, item.Unit, item.SKU, err)
	}

	costMicros, err := calculateCatalogCostMicros(normalizedQuantity, item.RateMicros)
	if err != nil {
		return PriceLookupResult{}, fmt.Errorf("calculate cost for SKU %q: %w", item.SKU, err)
	}

	return PriceLookupResult{
		Item:                item,
		UsageQuantityMicros: normalizedQuantity,
		CostMicros:          costMicros,
	}, nil
}

// List reads all price catalog items in deterministic service and SKU order.
func (r PriceCatalogRepository) List(ctx context.Context) ([]PriceCatalogItem, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}

	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			sku,
			service_code,
			service_name,
			product_family,
			usage_type,
			operation,
			region_code,
			unit,
			rate_micros,
			currency_code,
			effective_date,
			price_source,
			pricing_formula,
			notes
		 FROM price_catalog_items
		 ORDER BY service_code, sku`,
	)
	if err != nil {
		return nil, fmt.Errorf("list price catalog items: %w", err)
	}
	defer rows.Close()

	var items []PriceCatalogItem
	for rows.Next() {
		var item PriceCatalogItem
		if err := rows.Scan(
			&item.SKU,
			&item.ServiceCode,
			&item.ServiceName,
			&item.ProductFamily,
			&item.UsageType,
			&item.Operation,
			&item.RegionCode,
			&item.Unit,
			&item.RateMicros,
			&item.CurrencyCode,
			&item.EffectiveDate,
			&item.PriceSource,
			&item.PricingFormula,
			&item.Notes,
		); err != nil {
			return nil, fmt.Errorf("scan price catalog item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate price catalog items: %w", err)
	}
	return items, nil
}

func (r PriceCatalogRepository) lookupCatalogItem(ctx context.Context, request PriceLookupRequest) (PriceCatalogItem, error) {
	query := `SELECT
			sku,
			service_code,
			service_name,
			product_family,
			usage_type,
			operation,
			region_code,
			unit,
			rate_micros,
			currency_code,
			effective_date,
			price_source,
			pricing_formula,
			notes
		 FROM price_catalog_items
		 WHERE service_code = ?
		 	AND usage_type = ?
		 	AND operation = ?
		 	AND region_code IN (?, 'global')`
	args := []interface{}{
		request.ServiceCode,
		request.UsageType,
		request.Operation,
		request.RegionCode,
	}
	if request.UsageDate != "" {
		query += `
		 	AND effective_date <= ?`
		args = append(args, request.UsageDate)
	}
	query += `
		 ORDER BY
		 	CASE
		 		WHEN region_code = ? THEN 0
		 		WHEN region_code = 'global' THEN 1
		 		ELSE 2
		 	END,
		 	effective_date DESC,
		 	sku
		 LIMIT 1`
	args = append(args, request.RegionCode)

	var item PriceCatalogItem
	err := r.db.QueryRowContext(ctx, query, args...).Scan(
		&item.SKU,
		&item.ServiceCode,
		&item.ServiceName,
		&item.ProductFamily,
		&item.UsageType,
		&item.Operation,
		&item.RegionCode,
		&item.Unit,
		&item.RateMicros,
		&item.CurrencyCode,
		&item.EffectiveDate,
		&item.PriceSource,
		&item.PricingFormula,
		&item.Notes,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PriceCatalogItem{}, err
		}
		return PriceCatalogItem{}, fmt.Errorf("lookup price catalog item: %w", err)
	}
	return item, nil
}

func normalizePriceLookupRequest(request PriceLookupRequest) PriceLookupRequest {
	request.ServiceCode = strings.TrimSpace(request.ServiceCode)
	request.UsageType = strings.TrimSpace(request.UsageType)
	request.Operation = strings.TrimSpace(request.Operation)
	request.RegionCode = strings.TrimSpace(request.RegionCode)
	request.UsageUnit = strings.TrimSpace(request.UsageUnit)
	request.UsageDate = strings.TrimSpace(request.UsageDate)
	return request
}

func validatePriceLookupRequest(request PriceLookupRequest) error {
	if request.ServiceCode == "" {
		return fmt.Errorf("price lookup service code is required")
	}
	if request.UsageType == "" {
		return fmt.Errorf("price lookup usage type is required")
	}
	if request.Operation == "" {
		return fmt.Errorf("price lookup operation is required")
	}
	if request.RegionCode == "" {
		return fmt.Errorf("price lookup region code is required")
	}
	if request.UsageUnit == "" {
		return fmt.Errorf("price lookup usage unit is required")
	}
	if request.UsageQuantityMicros < 0 {
		return fmt.Errorf("price lookup usage quantity cannot be negative")
	}
	if request.BillingPeriodDays < 0 {
		return fmt.Errorf("price lookup billing period days cannot be negative")
	}
	if request.UsageDate != "" {
		if _, err := time.Parse(time.DateOnly, request.UsageDate); err != nil {
			return fmt.Errorf("price lookup usage date must use YYYY-MM-DD: %w", err)
		}
	}
	return nil
}

func formatPriceNotFoundError(request PriceLookupRequest) error {
	effectiveDate := "latest"
	if request.UsageDate != "" {
		effectiveDate = "<= " + request.UsageDate
	}
	return fmt.Errorf(
		"%w: service_code=%q usage_type=%q operation=%q region_code=%q effective_date=%s",
		ErrPriceCatalogRateNotFound,
		request.ServiceCode,
		request.UsageType,
		request.Operation,
		request.RegionCode,
		effectiveDate,
	)
}

func convertUsageQuantityMicros(quantityMicros int64, fromUnit, toUnit string, billingPeriodDays int) (int64, error) {
	from := canonicalPriceUnit(fromUnit)
	to := canonicalPriceUnit(toUnit)
	if from == "" || to == "" {
		return 0, fmt.Errorf("%w: from=%q to=%q", ErrUnsupportedUnitConversion, fromUnit, toUnit)
	}
	if from == to || equivalentCatalogUnit(from, to) {
		return quantityMicros, nil
	}

	switch {
	case from == "request" && to == "thousandrequests":
		return divideAndRoundMicros(quantityMicros, 1_000), nil
	case from == "request" && to == "millionrequests":
		return divideAndRoundMicros(quantityMicros, 1_000_000), nil
	case from == "gbday" && to == "gbmonth":
		if billingPeriodDays <= 0 {
			return 0, fmt.Errorf("%w: billing period days is required for %s to %s", ErrUnsupportedUnitConversion, fromUnit, toUnit)
		}
		return divideAndRoundMicros(quantityMicros, int64(billingPeriodDays)), nil
	default:
		return 0, fmt.Errorf("%w: from=%q to=%q", ErrUnsupportedUnitConversion, fromUnit, toUnit)
	}
}

func canonicalPriceUnit(unit string) string {
	normalized := strings.ToLower(strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.TrimSpace(unit)))
	switch normalized {
	case "requests", "requestcount":
		return "request"
	case "thousandrequest", "thousandrequests", "1000requests":
		return "thousandrequests"
	case "millionrequest", "millionrequests", "1000000requests":
		return "millionrequests"
	case "hours":
		return "hour"
	case "instancehours":
		return "instancehour"
	case "gatewayhours":
		return "gatewayhour"
	case "gigabyte", "gigabytes":
		return "gb"
	case "gbdays":
		return "gbday"
	case "gbmonths", "gbmo":
		return "gbmonth"
	case "gbseconds":
		return "gbsecond"
	case "months":
		return "month"
	case "subscriptionmonths":
		return "subscriptionmonth"
	case "dollar", "dollars", "usdollars":
		return "usd"
	default:
		return normalized
	}
}

func equivalentCatalogUnit(from, to string) bool {
	switch to {
	case "instancehour", "gatewayhour":
		return from == "hour"
	case "subscriptionmonth":
		return from == "month"
	case "eligibleusd":
		return from == "usd"
	default:
		return false
	}
}

func calculateCatalogCostMicros(quantityMicros, rateMicros int64) (int64, error) {
	if quantityMicros < 0 {
		return 0, fmt.Errorf("usage quantity cannot be negative")
	}
	if rateMicros < 0 {
		return 0, fmt.Errorf("rate cannot be negative")
	}
	if quantityMicros != 0 && rateMicros > maxPriceCalculationMicros/quantityMicros {
		return 0, fmt.Errorf("cost calculation overflows int64")
	}
	return divideAndRoundMicros(quantityMicros*rateMicros, priceQuantityMicros), nil
}

func divideAndRoundMicros(value, divisor int64) int64 {
	quotient := value / divisor
	remainder := value % divisor
	if remainder*2 >= divisor {
		return quotient + 1
	}
	return quotient
}
