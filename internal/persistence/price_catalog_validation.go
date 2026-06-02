package persistence

import (
	"context"
	"fmt"
	"strings"
	"time"
)

var supportedPriceCatalogRegions = map[string]struct{}{
	"global":    {},
	"us-east-1": {},
}

var supportedPriceCatalogUnits = map[string]struct{}{
	"EligibleUSD":       {},
	"GB":                {},
	"GBMonth":           {},
	"GBSecond":          {},
	"GatewayHour":       {},
	"InstanceHour":      {},
	"MillionRequests":   {},
	"SubscriptionMonth": {},
	"ThousandRequests":  {},
}

var supportedPriceCatalogSources = map[string]struct{}{
	"aws_price_list_snapshot": {},
	"instructor_override":     {},
	"synthetic":               {},
}

var supportedPriceCatalogFormulas = map[string]map[string]struct{}{
	"(request_count / 1000) * rate": {
		"ThousandRequests": {},
	},
	"(request_count / 1000000) * rate": {
		"MillionRequests": {},
	},
	"daily_gb_month_quantity * rate": {
		"GBMonth": {},
	},
	"eligible_monthly_cost * rate_with_minimum": {
		"EligibleUSD": {},
	},
	"gateway_hours * rate": {
		"GatewayHour": {},
	},
	"gb_seconds * rate": {
		"GBSecond": {},
	},
	"ingested_gb * rate": {
		"GB": {},
	},
	"processed_gb * rate": {
		"GB": {},
	},
	"retrieved_gb * rate": {
		"GB": {},
	},
	"storage_gb_month_quantity * rate": {
		"GBMonth": {},
	},
	"stored_gb_month_quantity * rate": {
		"GBMonth": {},
	},
	"subscription_months * rate": {
		"SubscriptionMonth": {},
	},
	"transferred_gb * rate": {
		"GB": {},
	},
	"usage_quantity * rate": {
		"InstanceHour": {},
	},
}

// Validate checks that all catalog rows use supported simulator dimensions.
func (r PriceCatalogRepository) Validate(ctx context.Context) error {
	items, err := r.List(ctx)
	if err != nil {
		return err
	}
	return validatePriceCatalogItems(items)
}

func validatePriceCatalogItems(items []PriceCatalogItem) error {
	if len(items) == 0 {
		return fmt.Errorf("price catalog validation failed: catalog has no items")
	}

	seenVersions := map[string]struct{}{}
	seenLookupIdentities := map[priceCatalogLookupIdentity]string{}
	var problems []string
	for _, item := range items {
		item = trimPriceCatalogItem(item)
		label := priceCatalogItemLabel(item)
		versionKey := item.SKU + "\x00" + item.EffectiveDate
		if item.SKU != "" {
			if _, ok := seenVersions[versionKey]; ok {
				problems = append(problems, fmt.Sprintf("duplicate SKU %q at effective_date %q", item.SKU, item.EffectiveDate))
			}
			seenVersions[versionKey] = struct{}{}
		}
		if identity, ok := priceCatalogLookupIdentityFor(item); ok {
			if previousSKU, ok := seenLookupIdentities[identity]; ok && previousSKU != item.SKU {
				problems = append(problems, fmt.Sprintf(
					"ambiguous lookup identity %s is shared by SKUs %q and %q",
					formatPriceCatalogLookupIdentity(identity),
					previousSKU,
					item.SKU,
				))
			} else {
				seenLookupIdentities[identity] = item.SKU
			}
		}

		if item.SKU == "" {
			problems = append(problems, "SKU is required")
		}
		if item.ServiceCode == "" || item.ServiceName == "" || item.ProductFamily == "" || item.UsageType == "" || item.Operation == "" {
			problems = append(problems, fmt.Sprintf("%s has incomplete service metadata", label))
		}
		if item.RegionCode == "" {
			problems = append(problems, fmt.Sprintf("%s region is required", label))
		} else if _, ok := supportedPriceCatalogRegions[item.RegionCode]; !ok {
			problems = append(problems, fmt.Sprintf("%s uses unsupported region %q", label, item.RegionCode))
		}
		unitSupported := true
		if item.Unit == "" {
			unitSupported = false
			problems = append(problems, fmt.Sprintf("%s unit is required", label))
		} else if _, ok := supportedPriceCatalogUnits[item.Unit]; !ok {
			unitSupported = false
			problems = append(problems, fmt.Sprintf("%s uses unsupported unit %q", label, item.Unit))
		}
		if item.CurrencyCode != "USD" {
			problems = append(problems, fmt.Sprintf("%s currency = %q, want USD", label, item.CurrencyCode))
		}
		if item.EffectiveDate == "" {
			problems = append(problems, fmt.Sprintf("%s effective date is required", label))
		} else if _, err := time.Parse(time.DateOnly, item.EffectiveDate); err != nil {
			problems = append(problems, fmt.Sprintf("%s effective date %q must use YYYY-MM-DD", label, item.EffectiveDate))
		}
		if item.PriceSource == "" {
			problems = append(problems, fmt.Sprintf("%s price source is required", label))
		} else if _, ok := supportedPriceCatalogSources[item.PriceSource]; !ok {
			problems = append(problems, fmt.Sprintf("%s uses unsupported price source %q", label, item.PriceSource))
		}
		if item.PricingFormula == "" {
			problems = append(problems, fmt.Sprintf("%s pricing formula is required", label))
		} else if supportedUnits, ok := supportedPriceCatalogFormulas[item.PricingFormula]; !ok {
			problems = append(problems, fmt.Sprintf("%s uses unsupported pricing formula %q", label, item.PricingFormula))
		} else if unitSupported {
			if _, ok := supportedUnits[item.Unit]; !ok {
				problems = append(problems, fmt.Sprintf("%s pricing formula %q does not cover unit %q", label, item.PricingFormula, item.Unit))
			}
		}
		if item.Notes == "" {
			problems = append(problems, fmt.Sprintf("%s notes are required", label))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("price catalog validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func trimPriceCatalogItem(item PriceCatalogItem) PriceCatalogItem {
	item.SKU = strings.TrimSpace(item.SKU)
	item.ServiceCode = strings.TrimSpace(item.ServiceCode)
	item.ServiceName = strings.TrimSpace(item.ServiceName)
	item.ProductFamily = strings.TrimSpace(item.ProductFamily)
	item.UsageType = strings.TrimSpace(item.UsageType)
	item.Operation = strings.TrimSpace(item.Operation)
	item.RegionCode = strings.TrimSpace(item.RegionCode)
	item.Unit = strings.TrimSpace(item.Unit)
	item.CurrencyCode = strings.TrimSpace(item.CurrencyCode)
	item.EffectiveDate = strings.TrimSpace(item.EffectiveDate)
	item.PriceSource = strings.TrimSpace(item.PriceSource)
	item.PricingFormula = strings.TrimSpace(item.PricingFormula)
	item.Notes = strings.TrimSpace(item.Notes)
	return item
}

func priceCatalogItemLabel(item PriceCatalogItem) string {
	if item.SKU == "" {
		return "<blank SKU>"
	}
	return fmt.Sprintf("SKU %q", item.SKU)
}

type priceCatalogLookupIdentity struct {
	serviceCode   string
	usageType     string
	operation     string
	regionCode    string
	effectiveDate string
}

// priceCatalogLookupIdentityFor returns the exact catalog dimensions Lookup can select before fallback ordering.
func priceCatalogLookupIdentityFor(item PriceCatalogItem) (priceCatalogLookupIdentity, bool) {
	if item.ServiceCode == "" || item.UsageType == "" || item.Operation == "" || item.RegionCode == "" || item.EffectiveDate == "" {
		return priceCatalogLookupIdentity{}, false
	}
	return priceCatalogLookupIdentity{
		serviceCode:   item.ServiceCode,
		usageType:     item.UsageType,
		operation:     item.Operation,
		regionCode:    item.RegionCode,
		effectiveDate: item.EffectiveDate,
	}, true
}

// formatPriceCatalogLookupIdentity formats duplicate lookup dimensions for validation errors.
func formatPriceCatalogLookupIdentity(identity priceCatalogLookupIdentity) string {
	return fmt.Sprintf(
		`service_code=%q usage_type=%q operation=%q region_code=%q effective_date=%q`,
		identity.serviceCode,
		identity.usageType,
		identity.operation,
		identity.regionCode,
		identity.effectiveDate,
	)
}
