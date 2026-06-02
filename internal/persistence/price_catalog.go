package persistence

import (
	"context"
	"database/sql"
	"fmt"
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

// PriceCatalogRepository reads catalog items seeded into a workspace database.
type PriceCatalogRepository struct {
	db *sql.DB
}

// NewPriceCatalogRepository creates a repository backed by a workspace database.
func NewPriceCatalogRepository(db *sql.DB) PriceCatalogRepository {
	return PriceCatalogRepository{db: db}
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
