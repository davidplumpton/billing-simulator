package persistence

import (
	"bytes"
	"embed"
	"encoding/csv"
	"fmt"
	"strconv"
)

// seedAssetsFS contains packaged fixture manifests used to verify embedded seed data.
//
//go:embed seeds/synthetic_price_catalog.csv
var seedAssetsFS embed.FS

// SyntheticPriceCatalogSeedItems returns the packaged synthetic catalog manifest.
func SyntheticPriceCatalogSeedItems() ([]PriceCatalogItem, error) {
	raw, err := seedAssetsFS.ReadFile("seeds/synthetic_price_catalog.csv")
	if err != nil {
		return nil, fmt.Errorf("read synthetic price catalog seed manifest: %w", err)
	}
	reader := csv.NewReader(bytes.NewReader(raw))
	reader.FieldsPerRecord = 12
	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("parse synthetic price catalog seed manifest: %w", err)
	}
	if len(records) < 2 {
		return nil, fmt.Errorf("synthetic price catalog seed manifest is empty")
	}

	items := make([]PriceCatalogItem, 0, len(records)-1)
	for idx, record := range records[1:] {
		rateMicros, err := strconv.ParseInt(record[8], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse synthetic price catalog row %d rate_micros: %w", idx+2, err)
		}
		items = append(items, PriceCatalogItem{
			SKU:           record[0],
			ServiceCode:   record[1],
			ServiceName:   record[2],
			ProductFamily: record[3],
			UsageType:     record[4],
			Operation:     record[5],
			RegionCode:    record[6],
			Unit:          record[7],
			RateMicros:    rateMicros,
			CurrencyCode:  record[9],
			EffectiveDate: record[10],
			PriceSource:   record[11],
		})
	}
	return items, nil
}
