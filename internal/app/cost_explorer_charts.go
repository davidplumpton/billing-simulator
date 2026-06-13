package app

import (
	"fmt"
	"strings"

	"aws-billing-simulator/internal/persistence"
)

type costExplorerChartView struct {
	Title       string
	Type        string
	MetricLabel string
	HasRows     bool
	HasChart    bool
	Width       int
	Height      int
	PlotX       int
	PlotY       int
	PlotWidth   int
	PlotHeight  int
	YAxisLabel  string
	ZeroY       int
	Ticks       []costExplorerChartTickView
	XLabels     []costExplorerChartAxisLabelView
	Lines       []costExplorerChartLineView
	Bars        []costExplorerChartBarView
	Legend      []costExplorerChartLegendView
}

type costExplorerChartTickView struct {
	Y     int
	Label string
}

type costExplorerChartAxisLabelView struct {
	X     int
	Label string
}

type costExplorerChartLineView struct {
	Label  string
	Color  string
	Points string
	Nodes  []costExplorerChartPointView
}

type costExplorerChartPointView struct {
	X          int
	Y          int
	Period     string
	Label      string
	ValueLabel string
}

type costExplorerChartBarView struct {
	X          int
	Y          int
	Width      int
	Height     int
	Color      string
	Period     string
	Label      string
	ValueLabel string
}

type costExplorerChartLegendView struct {
	Label string
	Color string
}

const (
	costExplorerChartWidth      = 760
	costExplorerChartHeight     = 300
	costExplorerChartPlotX      = 58
	costExplorerChartPlotY      = 28
	costExplorerChartPlotWidth  = 650
	costExplorerChartPlotHeight = 194
)

var costExplorerChartColors = []string{
	"#0f766e",
	"#2563eb",
	"#b45309",
	"#7c3aed",
	"#b42318",
	"#147d3f",
	"#4b5563",
	"#0e7490",
}

type costExplorerChartSeries struct {
	Label  string
	Color  string
	Values map[string]int64
}

type costExplorerChartDomain struct {
	MinValue int64
	MaxValue int64
}

// costExplorerChartViewFromResult converts aggregate report rows into server-rendered SVG primitives.
func costExplorerChartViewFromResult(result persistence.CostExplorerQueryResult, metric, chartType string) costExplorerChartView {
	chart := costExplorerChartView{
		Title:       "Cost Explorer report chart",
		Type:        chartType,
		MetricLabel: costExplorerMetricLabel(metric),
		HasRows:     len(result.Rows) > 0,
		Width:       costExplorerChartWidth,
		Height:      costExplorerChartHeight,
		PlotX:       costExplorerChartPlotX,
		PlotY:       costExplorerChartPlotY,
		PlotWidth:   costExplorerChartPlotWidth,
		PlotHeight:  costExplorerChartPlotHeight,
		ZeroY:       costExplorerChartPlotY + costExplorerChartPlotHeight,
	}
	if len(result.Rows) == 0 || chartType == "table" {
		return chart
	}
	if chartType != "line" && chartType != "bar" && chartType != "stacked_bar" {
		return chart
	}

	buckets, series := costExplorerChartBucketsAndSeries(result, metric)
	if len(buckets) == 0 || len(series) == 0 {
		return chart
	}
	stacked := chartType == "stacked_bar"
	domain := costExplorerChartDomainFor(buckets, series, stacked)

	chart.HasChart = true
	chart.ZeroY = costExplorerChartY(0, domain)
	chart.YAxisLabel = costExplorerChartYAxisLabel(domain, metric)
	chart.Ticks = costExplorerChartTicks(domain, metric)
	chart.XLabels = costExplorerChartXLabels(buckets)
	chart.Legend = costExplorerChartLegend(series)
	switch chartType {
	case "line":
		chart.Lines = costExplorerChartLines(buckets, series, metric, domain)
	case "bar":
		chart.Bars = costExplorerChartBars(buckets, series, metric, domain, false)
	case "stacked_bar":
		chart.Bars = costExplorerChartBars(buckets, series, metric, domain, true)
	}
	return chart
}

// costExplorerChartBucketsAndSeries keeps report bucket and grouping order stable for chart rendering.
func costExplorerChartBucketsAndSeries(result persistence.CostExplorerQueryResult, metric string) ([]string, []costExplorerChartSeries) {
	buckets := []string{}
	bucketSeen := map[string]bool{}
	series := []costExplorerChartSeries{}
	seriesIndex := map[string]int{}
	for _, row := range result.Rows {
		if !bucketSeen[row.TimePeriodStart] {
			bucketSeen[row.TimePeriodStart] = true
			buckets = append(buckets, row.TimePeriodStart)
		}
		label := costExplorerChartSeriesLabel(row)
		index, ok := seriesIndex[label]
		if !ok {
			index = len(series)
			seriesIndex[label] = index
			series = append(series, costExplorerChartSeries{
				Label:  label,
				Color:  costExplorerChartColors[index%len(costExplorerChartColors)],
				Values: map[string]int64{},
			})
		}
		series[index].Values[row.TimePeriodStart] += costExplorerMetricMicros(metric, row)
	}
	return buckets, series
}

// costExplorerChartSeriesLabel formats one grouping combination for legends and tooltips.
func costExplorerChartSeriesLabel(row persistence.CostExplorerQueryRow) string {
	if len(row.GroupValues) == 0 {
		return "All spend"
	}
	labels := make([]string, 0, len(row.GroupValues))
	for _, group := range row.GroupValues {
		labels = append(labels, costExplorerGroupLabel(group))
	}
	return strings.Join(labels, " / ")
}

// costExplorerMetricMicros returns the raw metric value used for chart scaling.
func costExplorerMetricMicros(metric string, row persistence.CostExplorerQueryRow) int64 {
	switch metric {
	case "usage_quantity":
		return row.UsageQuantityMicros
	case "blended_cost":
		return row.BlendedCostMicros
	case "net_cost":
		return row.NetCostMicros
	case "amortized_cost":
		return row.AmortizedCostMicros
	default:
		return row.UnblendedCostMicros
	}
}

// costExplorerChartDomainFor finds the value range needed to render grouped or stacked charts around zero.
func costExplorerChartDomainFor(buckets []string, series []costExplorerChartSeries, stacked bool) costExplorerChartDomain {
	domain := costExplorerChartDomain{}
	seen := false
	for _, bucket := range buckets {
		var positiveTotal int64
		var negativeTotal int64
		for _, item := range series {
			value := item.Values[bucket]
			if stacked {
				if value >= 0 {
					positiveTotal += value
				} else {
					negativeTotal += value
				}
				continue
			}
			if !seen || value < domain.MinValue {
				domain.MinValue = value
			}
			if !seen || value > domain.MaxValue {
				domain.MaxValue = value
			}
			seen = true
		}
		if stacked {
			if !seen || negativeTotal < domain.MinValue {
				domain.MinValue = negativeTotal
			}
			if !seen || positiveTotal > domain.MaxValue {
				domain.MaxValue = positiveTotal
			}
			seen = true
		}
	}
	if domain.MinValue > 0 {
		domain.MinValue = 0
	}
	if domain.MaxValue < 0 {
		domain.MaxValue = 0
	}
	if domain.MinValue == 0 && domain.MaxValue == 0 {
		domain.MaxValue = 1
	}
	return domain
}

// costExplorerChartYAxisLabel summarizes the positive-only ceiling or the signed chart range.
func costExplorerChartYAxisLabel(domain costExplorerChartDomain, metric string) string {
	if domain.MinValue >= 0 {
		return "Max " + costExplorerChartValueLabel(metric, domain.MaxValue)
	}
	return fmt.Sprintf("Range %s to %s", costExplorerChartValueLabel(metric, domain.MinValue), costExplorerChartValueLabel(metric, domain.MaxValue))
}

// costExplorerChartTicks renders a compact vertical scale for positive-only and signed charts.
func costExplorerChartTicks(domain costExplorerChartDomain, metric string) []costExplorerChartTickView {
	values := []int64{}
	switch {
	case domain.MinValue >= 0:
		mid := domain.MaxValue / 2
		if mid == 0 && domain.MaxValue > 1 {
			mid = 1
		}
		values = []int64{domain.MaxValue, mid, 0}
	case domain.MaxValue <= 0:
		mid := domain.MinValue / 2
		if mid == 0 && domain.MinValue < -1 {
			mid = -1
		}
		values = []int64{0, mid, domain.MinValue}
	default:
		values = []int64{domain.MaxValue, 0, domain.MinValue}
	}
	ticks := make([]costExplorerChartTickView, 0, len(values))
	seen := map[int64]bool{}
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		ticks = append(ticks, costExplorerChartTickView{
			Y:     costExplorerChartY(value, domain),
			Label: costExplorerChartValueLabel(metric, value),
		})
	}
	return ticks
}

// costExplorerChartXLabels chooses stable bucket labels without crowding daily charts.
func costExplorerChartXLabels(buckets []string) []costExplorerChartAxisLabelView {
	if len(buckets) == 0 {
		return nil
	}
	step := 1
	if len(buckets) > 8 {
		step = (len(buckets) + 6) / 7
	}
	labels := []costExplorerChartAxisLabelView{}
	for index, bucket := range buckets {
		if index%step != 0 && index != len(buckets)-1 {
			continue
		}
		labels = append(labels, costExplorerChartAxisLabelView{
			X:     costExplorerChartX(index, len(buckets)),
			Label: costExplorerChartPeriodLabel(bucket),
		})
	}
	return labels
}

// costExplorerChartLegend maps series colors to labels for learners comparing groups.
func costExplorerChartLegend(series []costExplorerChartSeries) []costExplorerChartLegendView {
	legend := make([]costExplorerChartLegendView, 0, len(series))
	for _, item := range series {
		legend = append(legend, costExplorerChartLegendView{
			Label: item.Label,
			Color: item.Color,
		})
	}
	return legend
}

// costExplorerChartLines renders line chart polylines and point tooltips.
func costExplorerChartLines(buckets []string, series []costExplorerChartSeries, metric string, domain costExplorerChartDomain) []costExplorerChartLineView {
	lines := make([]costExplorerChartLineView, 0, len(series))
	for _, item := range series {
		nodes := make([]costExplorerChartPointView, 0, len(buckets))
		pointParts := make([]string, 0, len(buckets))
		for bucketIndex, bucket := range buckets {
			value := item.Values[bucket]
			x := costExplorerChartX(bucketIndex, len(buckets))
			y := costExplorerChartY(value, domain)
			nodes = append(nodes, costExplorerChartPointView{
				X:          x,
				Y:          y,
				Period:     bucket,
				Label:      item.Label,
				ValueLabel: costExplorerChartValueLabel(metric, value),
			})
			pointParts = append(pointParts, fmt.Sprintf("%d,%d", x, y))
		}
		lines = append(lines, costExplorerChartLineView{
			Label:  item.Label,
			Color:  item.Color,
			Points: strings.Join(pointParts, " "),
			Nodes:  nodes,
		})
	}
	return lines
}

// costExplorerChartBars renders grouped or stacked bars with one tooltip per visible segment.
func costExplorerChartBars(buckets []string, series []costExplorerChartSeries, metric string, domain costExplorerChartDomain, stacked bool) []costExplorerChartBarView {
	if len(buckets) == 0 {
		return nil
	}
	bucketWidth := costExplorerChartPlotWidth / len(buckets)
	if bucketWidth < 1 {
		bucketWidth = 1
	}
	zeroY := costExplorerChartY(0, domain)
	plotBottom := costExplorerChartPlotY + costExplorerChartPlotHeight
	bars := []costExplorerChartBarView{}
	for bucketIndex, bucket := range buckets {
		bucketStart := costExplorerChartPlotX + bucketIndex*bucketWidth
		if stacked {
			barWidth := clampInt(bucketWidth-14, 8, 54)
			x := bucketStart + (bucketWidth-barWidth)/2
			positiveCumulative := int64(0)
			negativeCumulative := int64(0)
			for _, item := range series {
				value := item.Values[bucket]
				if value == 0 {
					continue
				}
				cumulative := positiveCumulative
				if value < 0 {
					cumulative = negativeCumulative
				}
				next := cumulative + value
				y := costExplorerChartY(next, domain)
				previousY := costExplorerChartY(cumulative, domain)
				barY := y
				height := previousY - y
				if value < 0 {
					barY = previousY
					height = y - previousY
				}
				if value < 0 && height < 2 {
					height = 2
					if barY+height > plotBottom {
						height = plotBottom - barY
					}
				}
				if height < 0 {
					height = 0
				}
				bars = append(bars, costExplorerChartBarView{
					X:          x,
					Y:          barY,
					Width:      barWidth,
					Height:     height,
					Color:      item.Color,
					Period:     bucket,
					Label:      item.Label,
					ValueLabel: costExplorerChartValueLabel(metric, value),
				})
				if value >= 0 {
					positiveCumulative = next
				} else {
					negativeCumulative = next
				}
			}
			continue
		}

		availableWidth := bucketWidth - 12
		if availableWidth < 8 {
			availableWidth = 8
		}
		barWidth := clampInt(availableWidth/len(series), 4, 34)
		totalWidth := barWidth * len(series)
		x := bucketStart + (bucketWidth-totalWidth)/2
		for seriesIndex, item := range series {
			value := item.Values[bucket]
			y := costExplorerChartY(value, domain)
			barY := y
			height := zeroY - y
			if value < 0 {
				barY = zeroY
				height = y - zeroY
			}
			if value != 0 && height < 2 {
				height = 2
				if value > 0 {
					barY = zeroY - height
					if barY < costExplorerChartPlotY {
						barY = costExplorerChartPlotY
						height = zeroY - barY
					}
				} else if barY+height > plotBottom {
					height = plotBottom - barY
				}
			}
			if height < 0 {
				height = 0
			}
			bars = append(bars, costExplorerChartBarView{
				X:          x + seriesIndex*barWidth,
				Y:          barY,
				Width:      barWidth,
				Height:     height,
				Color:      item.Color,
				Period:     bucket,
				Label:      item.Label,
				ValueLabel: costExplorerChartValueLabel(metric, value),
			})
		}
	}
	return bars
}

// costExplorerChartX maps a bucket index to the horizontal plot coordinate.
func costExplorerChartX(index, bucketCount int) int {
	if bucketCount <= 1 {
		return costExplorerChartPlotX + costExplorerChartPlotWidth/2
	}
	return costExplorerChartPlotX + (index*costExplorerChartPlotWidth)/(bucketCount-1)
}

// costExplorerChartY maps a metric value to the vertical plot coordinate.
func costExplorerChartY(value int64, domain costExplorerChartDomain) int {
	if domain.MinValue >= 0 {
		if value < 0 {
			value = 0
		}
		if domain.MaxValue <= 0 {
			domain.MaxValue = 1
		}
		scaled := int((value*int64(costExplorerChartPlotHeight) + domain.MaxValue/2) / domain.MaxValue)
		if scaled > costExplorerChartPlotHeight {
			scaled = costExplorerChartPlotHeight
		}
		return costExplorerChartPlotY + costExplorerChartPlotHeight - scaled
	}
	if value < domain.MinValue {
		value = domain.MinValue
	}
	if value > domain.MaxValue {
		value = domain.MaxValue
	}
	span := domain.MaxValue - domain.MinValue
	if span <= 0 {
		span = 1
	}
	offset := int(((domain.MaxValue-value)*int64(costExplorerChartPlotHeight) + span/2) / span)
	if offset < 0 {
		offset = 0
	}
	if offset > costExplorerChartPlotHeight {
		offset = costExplorerChartPlotHeight
	}
	return costExplorerChartPlotY + offset
}

// costExplorerChartValueLabel formats chart values with the selected metric's display rules.
func costExplorerChartValueLabel(metric string, value int64) string {
	switch metric {
	case "usage_quantity":
		return formatQuantityMicros(value)
	default:
		return formatUSDMicros(value)
	}
}

// costExplorerChartPeriodLabel shortens ISO bucket labels for the SVG axis.
func costExplorerChartPeriodLabel(bucket string) string {
	if len(bucket) >= len("2006-01-02T15:04") && strings.Contains(bucket, "T") {
		return bucket[5:10] + " " + bucket[11:16]
	}
	if len(bucket) >= len("2006-01-02") {
		return bucket[5:10]
	}
	return bucket
}

// clampInt limits chart dimensions so tiny and wide result sets stay legible.
func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
