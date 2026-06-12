package app

import (
	"context"
	"strings"
	"time"

	"aws-billing-simulator/internal/persistence"
)

func resourceViewFromSummary(summary persistence.ResourceSummary) resourceView {
	resource := summary.Resource
	name := displayResourceName(resource)
	return resourceView{
		ID:               resource.ID,
		Name:             name,
		AccountID:        resource.AccountID,
		RegionCode:       resource.RegionCode,
		ServiceCode:      resource.ServiceCode,
		ResourceType:     resource.ResourceType,
		Size:             resource.Attributes["size"],
		Status:           resource.Status,
		CreatedAt:        resource.CreatedAt,
		UsageEventCount:  summary.UsageEventCount,
		LastUsageEndTime: summary.LastUsageEndTime,
		Tags:             keyValueViews(summary.ActiveTags),
		Attributes:       keyValueViews(resource.Attributes),
	}
}

func (h resourceLabHandler) usageEventView(ctx context.Context, event persistence.UsageEvent, resourceName string) usageEventView {
	if resourceName == "" {
		resourceName = event.ResourceID
	}
	costEstimate := "unpriced"
	usageDate, billingPeriodDays, ok := usageEstimatePeriod(event.UsageStartTime)
	if ok {
		lookupResult, err := h.billing.catalog.Lookup(ctx, persistence.PriceLookupRequest{
			ServiceCode:         event.ServiceCode,
			UsageType:           event.UsageType,
			Operation:           event.Operation,
			RegionCode:          event.RegionCode,
			UsageUnit:           event.UsageUnit,
			UsageQuantityMicros: event.UsageQuantityMicros,
			UsageDate:           usageDate,
			BillingPeriodDays:   billingPeriodDays,
		})
		if err == nil {
			costEstimate = formatUSDMicros(lookupResult.CostMicros)
		}
	}

	return usageEventView{
		ID:                 event.ID,
		ResourceID:         event.ResourceID,
		ResourceName:       resourceName,
		AccountID:          event.AccountID,
		ServiceCode:        event.ServiceCode,
		UsageType:          event.UsageType,
		Operation:          event.Operation,
		RegionCode:         event.RegionCode,
		Window:             event.UsageStartTime + " to " + event.UsageEndTime,
		Quantity:           formatQuantityMicros(event.UsageQuantityMicros),
		Unit:               event.UsageUnit,
		EstimatedCost:      costEstimate,
		BillableDimensions: billableDimensions(event.ServiceCode, event.UsageType, event.Operation, event.RegionCode),
		Tags:               keyValueViews(event.TagSnapshot),
	}
}

func displayResourceName(resource persistence.Resource) string {
	if strings.TrimSpace(resource.ResourceName) != "" {
		return resource.ResourceName
	}
	return resource.ID
}

// usageEstimatePeriod returns the lookup date and calendar-month days used for UI-only price estimates.
func usageEstimatePeriod(value string) (string, int, bool) {
	parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return "", 0, false
	}
	period, err := persistence.BillingPeriodForTime(parsed)
	if err != nil {
		return "", 0, false
	}
	return parsed.UTC().Format(time.DateOnly), period.Days, true
}

func billableDimensions(serviceCode, usageType, operation, regionCode string) string {
	return serviceCode + " / " + usageType + " / " + operation + " / " + regionCode
}
