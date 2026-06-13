package app

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

func costExplorerResultsCSVBytes(result persistence.CostExplorerQueryResult, builder costExplorerBuilderView) ([]byte, error) {
	var body bytes.Buffer
	writer := csv.NewWriter(&body)
	if err := writer.Write(costExplorerResultsCSVHeader()); err != nil {
		return nil, err
	}
	for _, row := range result.Rows {
		if err := writer.Write(costExplorerResultsCSVRecord(result, builder, row)); err != nil {
			return nil, err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, err
	}
	return body.Bytes(), nil
}

func costExplorerResultsCSVHeader() []string {
	return []string{
		"date_range_start",
		"date_range_end",
		"granularity",
		"metric",
		"period_start",
		"period_end",
		"group_1_type",
		"group_1_key",
		"group_1_value",
		"group_2_type",
		"group_2_key",
		"group_2_value",
		"metric_value",
		"usage_quantity",
		"unblended_cost",
		"blended_cost",
		"net_cost",
		"amortized_cost",
		"line_item_count",
		"currency_code",
	}
}

func costExplorerResultsCSVRecord(result persistence.CostExplorerQueryResult, builder costExplorerBuilderView, row persistence.CostExplorerQueryRow) []string {
	group1 := costExplorerCSVGroup(row, 0)
	group2 := costExplorerCSVGroup(row, 1)
	return []string{
		result.DateRangeStart,
		result.DateRangeEnd,
		result.Granularity,
		builder.Metric,
		row.TimePeriodStart,
		row.TimePeriodEnd,
		group1.Type,
		group1.Key,
		group1.Value,
		group2.Type,
		group2.Key,
		group2.Value,
		costExplorerMetricCSVValue(builder.Metric, row),
		formatMicrosDecimal(row.UsageQuantityMicros),
		formatMicrosDecimal(row.UnblendedCostMicros),
		formatMicrosDecimal(row.BlendedCostMicros),
		formatMicrosDecimal(row.NetCostMicros),
		formatMicrosDecimal(row.AmortizedCostMicros),
		fmt.Sprintf("%d", row.LineItemCount),
		row.CurrencyCode,
	}
}

func costExplorerCSVGroup(row persistence.CostExplorerQueryRow, index int) persistence.CostExplorerGroupValue {
	if index >= 0 && index < len(row.GroupValues) {
		return row.GroupValues[index]
	}
	return persistence.CostExplorerGroupValue{}
}

func costExplorerMetricCSVValue(metric string, row persistence.CostExplorerQueryRow) string {
	switch metric {
	case "usage_quantity":
		return formatMicrosDecimal(row.UsageQuantityMicros)
	case "blended_cost":
		return formatMicrosDecimal(row.BlendedCostMicros)
	case "net_cost":
		return formatMicrosDecimal(row.NetCostMicros)
	case "amortized_cost":
		return formatMicrosDecimal(row.AmortizedCostMicros)
	default:
		return formatMicrosDecimal(row.UnblendedCostMicros)
	}
}

func costExplorerResultsCSVFilename(builder costExplorerBuilderView) string {
	name := strings.TrimSpace(builder.ReportName)
	if name == "" {
		name = "cost-explorer-report"
	}
	var safe strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			safe.WriteRune(r)
		} else {
			safe.WriteByte('-')
		}
	}
	filename := strings.Trim(safe.String(), "-")
	if filename == "" {
		filename = "cost-explorer-report"
	}
	return filename + ".csv"
}
