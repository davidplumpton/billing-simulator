package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

const (
	// SyntheticPriceCatalogID identifies the embedded deterministic catalog seeded into fresh workspaces.
	SyntheticPriceCatalogID = "synthetic-2026-01-01"
	// SyntheticPriceCatalogSourceURL is the local source URI for the embedded synthetic catalog manifest.
	SyntheticPriceCatalogSourceURL = "embedded://internal/persistence/seeds/synthetic_price_catalog.csv"
	// SyntheticPriceCatalogFetchDate records when the synthetic catalog snapshot was packaged.
	SyntheticPriceCatalogFetchDate = "2026-01-01"
	// SyntheticPriceCatalogEffectiveDate is the first usage date supported by the embedded catalog.
	SyntheticPriceCatalogEffectiveDate = "2026-01-01"

	PriceCatalogCompatibilityCompatible   = "compatible"
	PriceCatalogCompatibilityIncompatible = "incompatible"
)

// PriceCatalogManifest records catalog-level provenance that applies to many versioned rates.
type PriceCatalogManifest struct {
	ID                 string
	SourceURL          string
	FetchDate          string
	EffectiveDate      string
	SupportedRegions   []string
	CompatibilityKey   string
	CompatibilityNotes string
	IsActive           bool
	CreatedAt          string
}

// PriceCatalogScenarioCompatibility captures whether a scenario run can use the active catalog.
type PriceCatalogScenarioCompatibility struct {
	Catalog PriceCatalogManifest
	Status  string
	Message string
}

// ActiveManifest returns the one catalog manifest selected for new pricing and scenario runs.
func (r PriceCatalogRepository) ActiveManifest(ctx context.Context) (PriceCatalogManifest, error) {
	if r.db == nil {
		return PriceCatalogManifest{}, fmt.Errorf("database handle is required")
	}
	manifest, err := r.scanManifest(ctx, r.db.QueryRowContext(ctx, `
		SELECT id,
		       source_url,
		       fetch_date,
		       effective_date,
		       compatibility_key,
		       compatibility_notes,
		       is_active,
		       created_at
		  FROM price_catalog_manifests
		 WHERE is_active = 1
	`).Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return PriceCatalogManifest{}, fmt.Errorf("active price catalog manifest is required")
	}
	return manifest, err
}

// ListManifests returns catalog manifests in deterministic active/effective-date order.
func (r PriceCatalogRepository) ListManifests(ctx context.Context) ([]PriceCatalogManifest, error) {
	if r.db == nil {
		return nil, fmt.Errorf("database handle is required")
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id,
		       source_url,
		       fetch_date,
		       effective_date,
		       compatibility_key,
		       compatibility_notes,
		       is_active,
		       created_at
		  FROM price_catalog_manifests
		 ORDER BY is_active DESC, effective_date DESC, id
	`)
	if err != nil {
		return nil, fmt.Errorf("list price catalog manifests: %w", err)
	}
	defer rows.Close()

	manifests := []PriceCatalogManifest{}
	for rows.Next() {
		manifest, err := r.scanManifest(ctx, rows.Scan)
		if err != nil {
			return nil, err
		}
		manifests = append(manifests, manifest)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate price catalog manifests: %w", err)
	}
	return manifests, nil
}

// ScenarioCompatibility compares a scenario start date with the active catalog manifest.
func (r PriceCatalogRepository) ScenarioCompatibility(ctx context.Context, scenarioStart string) (PriceCatalogScenarioCompatibility, error) {
	manifest, err := r.ActiveManifest(ctx)
	if err != nil {
		return PriceCatalogScenarioCompatibility{}, err
	}
	scenarioDate, ok, err := scenarioStartDate(scenarioStart)
	if err != nil {
		return PriceCatalogScenarioCompatibility{}, err
	}
	if ok {
		effectiveDate, err := time.Parse(time.DateOnly, manifest.EffectiveDate)
		if err != nil {
			return PriceCatalogScenarioCompatibility{}, fmt.Errorf("parse price catalog effective date %q: %w", manifest.EffectiveDate, err)
		}
		if scenarioDate.Before(effectiveDate) {
			return PriceCatalogScenarioCompatibility{
				Catalog: manifest,
				Status:  PriceCatalogCompatibilityIncompatible,
				Message: fmt.Sprintf("Scenario starts on %s before catalog %s becomes effective on %s.", scenarioDate.Format(time.DateOnly), manifest.ID, manifest.EffectiveDate),
			}, nil
		}
	}
	return PriceCatalogScenarioCompatibility{
		Catalog: manifest,
		Status:  PriceCatalogCompatibilityCompatible,
		Message: manifest.CompatibilityNotes,
	}, nil
}

func (r PriceCatalogRepository) scanManifest(ctx context.Context, scan func(dest ...any) error) (PriceCatalogManifest, error) {
	var manifest PriceCatalogManifest
	var active int
	if err := scan(
		&manifest.ID,
		&manifest.SourceURL,
		&manifest.FetchDate,
		&manifest.EffectiveDate,
		&manifest.CompatibilityKey,
		&manifest.CompatibilityNotes,
		&active,
		&manifest.CreatedAt,
	); err != nil {
		return PriceCatalogManifest{}, err
	}
	regions, err := r.listManifestRegions(ctx, manifest.ID)
	if err != nil {
		return PriceCatalogManifest{}, err
	}
	manifest.IsActive = active == 1
	manifest.SupportedRegions = regions
	return manifest, nil
}

func (r PriceCatalogRepository) listManifestRegions(ctx context.Context, catalogID string) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT region_code
		  FROM price_catalog_manifest_regions
		 WHERE catalog_id = ?
		 ORDER BY region_code
	`, catalogID)
	if err != nil {
		return nil, fmt.Errorf("list price catalog manifest regions for %q: %w", catalogID, err)
	}
	defer rows.Close()

	regions := []string{}
	for rows.Next() {
		var region string
		if err := rows.Scan(&region); err != nil {
			return nil, fmt.Errorf("scan price catalog manifest region: %w", err)
		}
		regions = append(regions, region)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate price catalog manifest regions: %w", err)
	}
	return regions, nil
}

func validatePriceCatalogManifests(manifests []PriceCatalogManifest, items []PriceCatalogItem) error {
	if len(manifests) == 0 {
		return fmt.Errorf("price catalog validation failed: catalog manifest is required")
	}
	activeCount := 0
	var activeManifest PriceCatalogManifest
	var problems []string
	for _, manifest := range manifests {
		manifest = trimPriceCatalogManifest(manifest)
		label := priceCatalogManifestLabel(manifest)
		if manifest.ID == "" {
			problems = append(problems, "catalog manifest id is required")
		}
		if manifest.SourceURL == "" {
			problems = append(problems, fmt.Sprintf("%s source URL is required", label))
		}
		if manifest.FetchDate == "" {
			problems = append(problems, fmt.Sprintf("%s fetch date is required", label))
		} else if _, err := time.Parse(time.DateOnly, manifest.FetchDate); err != nil {
			problems = append(problems, fmt.Sprintf("%s fetch date %q must use YYYY-MM-DD", label, manifest.FetchDate))
		}
		if manifest.EffectiveDate == "" {
			problems = append(problems, fmt.Sprintf("%s effective date is required", label))
		} else if _, err := time.Parse(time.DateOnly, manifest.EffectiveDate); err != nil {
			problems = append(problems, fmt.Sprintf("%s effective date %q must use YYYY-MM-DD", label, manifest.EffectiveDate))
		}
		if manifest.CompatibilityKey == "" {
			problems = append(problems, fmt.Sprintf("%s compatibility key is required", label))
		}
		if manifest.CompatibilityNotes == "" {
			problems = append(problems, fmt.Sprintf("%s compatibility notes are required", label))
		}
		if len(manifest.SupportedRegions) == 0 {
			problems = append(problems, fmt.Sprintf("%s supported regions are required", label))
		}
		seenRegions := map[string]struct{}{}
		for _, region := range manifest.SupportedRegions {
			region = strings.TrimSpace(region)
			if region == "" {
				problems = append(problems, fmt.Sprintf("%s has blank supported region", label))
				continue
			}
			if _, ok := seenRegions[region]; ok {
				problems = append(problems, fmt.Sprintf("%s repeats supported region %q", label, region))
			}
			seenRegions[region] = struct{}{}
		}
		if manifest.IsActive {
			activeCount++
			activeManifest = manifest
		}
	}
	if activeCount != 1 {
		problems = append(problems, fmt.Sprintf("price catalog must have exactly one active manifest, found %d", activeCount))
	}
	if activeCount == 1 {
		supported := map[string]struct{}{}
		for _, region := range activeManifest.SupportedRegions {
			supported[region] = struct{}{}
		}
		for _, item := range items {
			if _, ok := supported[item.RegionCode]; !ok {
				problems = append(problems, fmt.Sprintf("active catalog manifest %q does not list item region %q", activeManifest.ID, item.RegionCode))
			}
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("price catalog validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func trimPriceCatalogManifest(manifest PriceCatalogManifest) PriceCatalogManifest {
	manifest.ID = strings.TrimSpace(manifest.ID)
	manifest.SourceURL = strings.TrimSpace(manifest.SourceURL)
	manifest.FetchDate = strings.TrimSpace(manifest.FetchDate)
	manifest.EffectiveDate = strings.TrimSpace(manifest.EffectiveDate)
	manifest.CompatibilityKey = strings.TrimSpace(manifest.CompatibilityKey)
	manifest.CompatibilityNotes = strings.TrimSpace(manifest.CompatibilityNotes)
	regions := make([]string, 0, len(manifest.SupportedRegions))
	for _, region := range manifest.SupportedRegions {
		if trimmed := strings.TrimSpace(region); trimmed != "" {
			regions = append(regions, trimmed)
		}
	}
	slices.Sort(regions)
	manifest.SupportedRegions = regions
	return manifest
}

func priceCatalogManifestLabel(manifest PriceCatalogManifest) string {
	if manifest.ID == "" {
		return "<blank catalog manifest>"
	}
	return fmt.Sprintf("catalog manifest %q", manifest.ID)
}

func scenarioStartDate(value string) (time.Time, bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false, nil
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed, true, nil
	}
	if parsed, err := time.Parse(time.DateOnly, value); err == nil {
		return parsed, true, nil
	}
	return time.Time{}, false, fmt.Errorf("scenario start date %q must use YYYY-MM-DD or RFC3339", value)
}
