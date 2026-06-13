package app

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

func costExplorerQueryRequestFromBuilder(builder costExplorerBuilderView) (persistence.CostExplorerQueryRequest, error) {
	filters, err := costExplorerFiltersFromBuilder(builder)
	if err != nil {
		return persistence.CostExplorerQueryRequest{}, err
	}
	groupings, err := costExplorerGroupingsFromBuilder(builder)
	if err != nil {
		return persistence.CostExplorerQueryRequest{}, err
	}
	return persistence.CostExplorerQueryRequest{
		DateRangeStart: builder.DateRangeStart,
		DateRangeEnd:   builder.DateRangeEnd,
		Granularity:    builder.Granularity,
		Metric:         builder.Metric,
		Filters:        filters,
		Groupings:      groupings,
	}, nil
}

func costExplorerDefaultBuilder() costExplorerBuilderView {
	return costExplorerBuilderView{
		OwnerAccountID: persistence.AnyCompanyRetailManagementAccountID,
		OwnerRole:      "management-account",
		DateRangeStart: "2026-02-01",
		DateRangeEnd:   "2026-03-01",
		Granularity:    "monthly",
		Metric:         "unblended_cost",
		ChartType:      "table",
		Group1Type:     "dimension",
		Group1Key:      "service",
	}
}

type costExplorerSavedReportOwnerScope struct {
	OwnerAccountID string
	OwnerRole      string
}

// costExplorerSavedReportOwnerScopeFromValues derives the simulated saved-report shelf from request values.
func costExplorerSavedReportOwnerScopeFromValues(values url.Values, defaults costExplorerBuilderView) (costExplorerSavedReportOwnerScope, error) {
	builder, err := costExplorerBuilderFromValues(values, defaults)
	if err != nil {
		return costExplorerSavedReportOwnerScope{}, err
	}
	return costExplorerSavedReportOwnerScopeFromBuilder(builder), nil
}

// costExplorerSavedReportOwnerScopeFromBuilder extracts the owner shelf from a normalized builder.
func costExplorerSavedReportOwnerScopeFromBuilder(builder costExplorerBuilderView) costExplorerSavedReportOwnerScope {
	return costExplorerSavedReportOwnerScope{
		OwnerAccountID: strings.TrimSpace(builder.OwnerAccountID),
		OwnerRole:      strings.TrimSpace(builder.OwnerRole),
	}
}

// listRequest converts the UI owner shelf into the persistence list filter.
func (s costExplorerSavedReportOwnerScope) listRequest(limit int) persistence.SavedReportListRequest {
	return persistence.SavedReportListRequest{
		OwnerAccountID: s.OwnerAccountID,
		OwnerRole:      s.OwnerRole,
		Limit:          limit,
	}
}

func costExplorerBuilderFromValues(values url.Values, defaults costExplorerBuilderView) (costExplorerBuilderView, error) {
	builder := defaults
	builder.SavedReportID = firstValue(values, "saved_report_id")
	builder.ReportName = firstValue(values, "report_name")
	builder.Description = firstValue(values, "description")
	builder.OwnerAccountID = defaultString(firstValue(values, "owner_account_id"), builder.OwnerAccountID)
	builder.OwnerRole = defaultString(firstValue(values, "owner_role"), builder.OwnerRole)
	builder.DateRangeStart = defaultString(firstValue(values, "date_range_start"), builder.DateRangeStart)
	builder.DateRangeEnd = defaultString(firstValue(values, "date_range_end"), builder.DateRangeEnd)
	builder.Granularity = defaultString(firstValue(values, "granularity"), builder.Granularity)
	builder.Metric = defaultString(firstValue(values, "metric"), builder.Metric)
	builder.ChartType = defaultString(firstValue(values, "chart_type"), builder.ChartType)
	builder.ServiceValues = firstValue(values, "service_values")
	builder.LinkedAccountValues = firstValue(values, "linked_account_values")
	builder.RegionValues = firstValue(values, "region_values")
	builder.UsageTypeValues = firstValue(values, "usage_type_values")
	builder.LineItemTypeValues = firstValue(values, "line_item_type_values")
	builder.TagKey = firstValue(values, "tag_key")
	builder.TagValues = firstValue(values, "tag_values")
	builder.CostCategoryKey = firstValue(values, "cost_category_key")
	builder.CostCategoryValues = firstValue(values, "cost_category_values")
	builder.Group1Type = defaultString(firstValue(values, "group_1_type"), builder.Group1Type)
	builder.Group1Key = defaultString(firstValue(values, "group_1_key"), builder.Group1Key)
	builder.Group2Type = firstValue(values, "group_2_type")
	builder.Group2Key = firstValue(values, "group_2_key")
	if builder.Metric == "" {
		builder.Metric = "unblended_cost"
	}
	if builder.ChartType == "" {
		builder.ChartType = "table"
	}
	return builder, nil
}

func costExplorerBuilderFromSavedReport(report persistence.SavedReport, defaults costExplorerBuilderView) costExplorerBuilderView {
	builder := defaults
	builder.SavedReportID = report.ID
	builder.ReportName = report.Name
	builder.Description = report.Description
	builder.OwnerAccountID = report.OwnerAccountID
	builder.OwnerRole = report.OwnerRole
	builder.DateRangeStart = report.DateRangeStart
	builder.DateRangeEnd = report.DateRangeEnd
	builder.Granularity = report.Granularity
	if len(report.Metrics) > 0 {
		builder.Metric = report.Metrics[0]
	}
	builder.ChartType = report.ChartType
	builder.ServiceValues = strings.Join(report.Filters["service"], ", ")
	builder.LinkedAccountValues = strings.Join(report.Filters["linked_account"], ", ")
	builder.RegionValues = strings.Join(report.Filters["region"], ", ")
	builder.UsageTypeValues = strings.Join(report.Filters["usage_type"], ", ")
	builder.LineItemTypeValues = strings.Join(report.Filters["line_item_type"], ", ")
	for key, values := range report.Filters {
		if strings.HasPrefix(key, "tag:") {
			builder.TagKey = strings.TrimPrefix(key, "tag:")
			builder.TagValues = strings.Join(values, ", ")
		}
		if strings.HasPrefix(key, "cost_category:") {
			builder.CostCategoryKey = strings.TrimPrefix(key, "cost_category:")
			builder.CostCategoryValues = strings.Join(values, ", ")
		}
	}
	if len(report.Groupings) > 0 {
		builder.Group1Type = report.Groupings[0].Type
		builder.Group1Key = report.Groupings[0].Key
	}
	if len(report.Groupings) > 1 {
		builder.Group2Type = report.Groupings[1].Type
		builder.Group2Key = report.Groupings[1].Key
	}
	return builder
}

func costExplorerRequestHasBuilderFields(r *http.Request) bool {
	query := r.URL.Query()
	for _, key := range []string{
		"report_name",
		"date_range_start",
		"date_range_end",
		"granularity",
		"metric",
		"service_values",
		"linked_account_values",
		"region_values",
		"usage_type_values",
		"line_item_type_values",
		"tag_key",
		"tag_values",
		"cost_category_key",
		"cost_category_values",
		"group_1_type",
		"group_1_key",
		"group_2_type",
		"group_2_key",
	} {
		if _, ok := query[key]; ok {
			return true
		}
	}
	return false
}

func costExplorerBuilderHasFilters(builder costExplorerBuilderView) bool {
	return builder.ServiceValues != "" ||
		builder.LinkedAccountValues != "" ||
		builder.RegionValues != "" ||
		builder.UsageTypeValues != "" ||
		builder.LineItemTypeValues != "" ||
		builder.TagKey != "" ||
		builder.TagValues != "" ||
		builder.CostCategoryKey != "" ||
		builder.CostCategoryValues != ""
}

func costExplorerFiltersFromBuilder(builder costExplorerBuilderView) (map[string][]string, error) {
	filters := map[string][]string{}
	addFilterValues(filters, "service", builder.ServiceValues)
	addFilterValues(filters, "linked_account", builder.LinkedAccountValues)
	addFilterValues(filters, "region", builder.RegionValues)
	addFilterValues(filters, "usage_type", builder.UsageTypeValues)
	addFilterValues(filters, "line_item_type", builder.LineItemTypeValues)
	tagValues := splitRuleValues(builder.TagValues)
	if builder.TagKey != "" || len(tagValues) > 0 {
		if builder.TagKey == "" {
			return nil, fmt.Errorf("tag filter key is required when tag values are set")
		}
		if len(tagValues) == 0 {
			return nil, fmt.Errorf("tag filter values are required when tag key is set")
		}
		filters["tag:"+builder.TagKey] = tagValues
	}
	categoryValues := splitRuleValues(builder.CostCategoryValues)
	if builder.CostCategoryKey != "" || len(categoryValues) > 0 {
		if builder.CostCategoryKey == "" {
			return nil, fmt.Errorf("Cost Category filter key is required when values are set")
		}
		if len(categoryValues) == 0 {
			return nil, fmt.Errorf("Cost Category filter values are required when key is set")
		}
		filters["cost_category:"+builder.CostCategoryKey] = categoryValues
	}
	return filters, nil
}

func costExplorerGroupingsFromBuilder(builder costExplorerBuilderView) ([]persistence.CostExplorerGrouping, error) {
	groupings := []persistence.CostExplorerGrouping{}
	for idx, input := range []struct {
		groupType string
		key       string
	}{
		{builder.Group1Type, builder.Group1Key},
		{builder.Group2Type, builder.Group2Key},
	} {
		groupType := strings.TrimSpace(input.groupType)
		key := strings.TrimSpace(input.key)
		if groupType == "" {
			continue
		}
		if key == "" {
			return nil, fmt.Errorf("group %d key is required", idx+1)
		}
		groupings = append(groupings, persistence.CostExplorerGrouping{Type: groupType, Key: key})
	}
	return groupings, nil
}

func costExplorerDrilldownGroupValuesFromValues(values url.Values, groupings []persistence.CostExplorerGrouping) ([]persistence.CostExplorerGroupValue, error) {
	groupValues := make([]persistence.CostExplorerGroupValue, 0, len(groupings))
	for i, grouping := range groupings {
		value := firstValue(values, fmt.Sprintf("group_%d_value", i+1))
		if value == "" {
			return nil, fmt.Errorf("Cost Explorer drilldown group %d value is required", i+1)
		}
		groupValues = append(groupValues, persistence.CostExplorerGroupValue{
			Type:  grouping.Type,
			Key:   grouping.Key,
			Value: value,
		})
	}
	return groupValues, nil
}

func addFilterValues(filters map[string][]string, key, raw string) {
	values := splitRuleValues(raw)
	if len(values) > 0 {
		filters[key] = values
	}
}

func costExplorerBuilderQueryValues(builder costExplorerBuilderView) url.Values {
	values := url.Values{}
	setQueryValue := func(key, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			values.Set(key, value)
		}
	}
	setQueryValue("saved_report_id", builder.SavedReportID)
	setQueryValue("report_name", builder.ReportName)
	setQueryValue("description", builder.Description)
	setQueryValue("owner_account_id", builder.OwnerAccountID)
	setQueryValue("owner_role", builder.OwnerRole)
	setQueryValue("date_range_start", builder.DateRangeStart)
	setQueryValue("date_range_end", builder.DateRangeEnd)
	setQueryValue("granularity", builder.Granularity)
	setQueryValue("metric", builder.Metric)
	setQueryValue("chart_type", builder.ChartType)
	setQueryValue("service_values", builder.ServiceValues)
	setQueryValue("linked_account_values", builder.LinkedAccountValues)
	setQueryValue("region_values", builder.RegionValues)
	setQueryValue("usage_type_values", builder.UsageTypeValues)
	setQueryValue("line_item_type_values", builder.LineItemTypeValues)
	setQueryValue("tag_key", builder.TagKey)
	setQueryValue("tag_values", builder.TagValues)
	setQueryValue("cost_category_key", builder.CostCategoryKey)
	setQueryValue("cost_category_values", builder.CostCategoryValues)
	setQueryValue("group_1_type", builder.Group1Type)
	setQueryValue("group_1_key", builder.Group1Key)
	setQueryValue("group_2_type", builder.Group2Type)
	setQueryValue("group_2_key", builder.Group2Key)
	values.Set("run", "1")
	return values
}

func costExplorerPath(builder costExplorerBuilderView) string {
	return "/cost-explorer?" + costExplorerBuilderQueryValues(builder).Encode()
}

// costExplorerNewReportPath clears the report definition while keeping the current owner shelf.
func costExplorerNewReportPath(builder costExplorerBuilderView) string {
	values := url.Values{}
	setQueryValue := func(key, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			values.Set(key, value)
		}
	}
	setQueryValue("owner_account_id", builder.OwnerAccountID)
	setQueryValue("owner_role", builder.OwnerRole)
	if len(values) == 0 {
		return "/cost-explorer"
	}
	return "/cost-explorer?" + values.Encode()
}

func costExplorerResultsCSVPath(builder costExplorerBuilderView) string {
	return "/cost-explorer/results.csv?" + costExplorerBuilderQueryValues(builder).Encode()
}

func costExplorerDrilldownPath(builder costExplorerBuilderView, row persistence.CostExplorerQueryRow) string {
	values := costExplorerBuilderQueryValues(builder)
	values.Set("period_start", row.TimePeriodStart)
	values.Set("period_end", row.TimePeriodEnd)
	for i, group := range row.GroupValues {
		values.Set(fmt.Sprintf("group_%d_value", i+1), group.Value)
	}
	return "/cost-explorer/line-items?" + values.Encode()
}
