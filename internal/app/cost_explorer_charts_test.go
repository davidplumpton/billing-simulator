package app

import (
	"testing"

	"aws-billing-simulator/internal/persistence"
)

func TestCostExplorerChartViewKeepsPositiveOnlyBarScale(t *testing.T) {
	t.Parallel()

	result := persistence.CostExplorerQueryResult{
		Rows: []persistence.CostExplorerQueryRow{{
			TimePeriodStart:     "2026-02-01",
			UnblendedCostMicros: 2_000_000,
			UsageQuantityMicros: 1_000_000,
		}},
	}

	chart := costExplorerChartViewFromResult(result, "unblended_cost", "bar")
	if !chart.HasChart {
		t.Fatalf("HasChart = false, want true")
	}
	if chart.ZeroY != costExplorerChartPlotY+costExplorerChartPlotHeight {
		t.Fatalf("ZeroY = %d, want positive-only baseline %d", chart.ZeroY, costExplorerChartPlotY+costExplorerChartPlotHeight)
	}
	if chart.YAxisLabel != "Max $2.00" {
		t.Fatalf("YAxisLabel = %q, want %q", chart.YAxisLabel, "Max $2.00")
	}
	for _, want := range []string{"$2.00", "$1.00", "$0.00"} {
		if !chartHasTickLabel(chart.Ticks, want) {
			t.Fatalf("ticks missing %q: %#v", want, chart.Ticks)
		}
	}
	if len(chart.Bars) != 1 {
		t.Fatalf("len(Bars) = %d, want 1", len(chart.Bars))
	}
	bar := chart.Bars[0]
	if bar.Y != costExplorerChartPlotY || bar.Height != costExplorerChartPlotHeight {
		t.Fatalf("bar geometry = y %d height %d, want y %d height %d", bar.Y, bar.Height, costExplorerChartPlotY, costExplorerChartPlotHeight)
	}
}

func TestCostExplorerChartViewRendersNegativeNetCostLine(t *testing.T) {
	t.Parallel()

	result := persistence.CostExplorerQueryResult{
		Rows: []persistence.CostExplorerQueryRow{
			{TimePeriodStart: "2026-02-01", NetCostMicros: -2_000_000},
			{TimePeriodStart: "2026-02-02", NetCostMicros: 1_000_000},
		},
	}

	chart := costExplorerChartViewFromResult(result, "net_cost", "line")
	if !chart.HasChart {
		t.Fatalf("HasChart = false, want true")
	}
	if chart.ZeroY <= costExplorerChartPlotY || chart.ZeroY >= costExplorerChartPlotY+costExplorerChartPlotHeight {
		t.Fatalf("ZeroY = %d, want baseline inside signed plot", chart.ZeroY)
	}
	if chart.YAxisLabel != "Range -$2.00 to $1.00" {
		t.Fatalf("YAxisLabel = %q, want signed range", chart.YAxisLabel)
	}
	for _, want := range []string{"$1.00", "$0.00", "-$2.00"} {
		if !chartHasTickLabel(chart.Ticks, want) {
			t.Fatalf("ticks missing %q: %#v", want, chart.Ticks)
		}
	}
	if len(chart.Lines) != 1 || len(chart.Lines[0].Nodes) != 2 {
		t.Fatalf("line nodes = %#v, want one two-point line", chart.Lines)
	}
	negativeNode := chart.Lines[0].Nodes[0]
	positiveNode := chart.Lines[0].Nodes[1]
	if negativeNode.ValueLabel != "-$2.00" || negativeNode.Y <= chart.ZeroY {
		t.Fatalf("negative node = %#v, want below zero axis %d", negativeNode, chart.ZeroY)
	}
	if positiveNode.ValueLabel != "$1.00" || positiveNode.Y >= chart.ZeroY {
		t.Fatalf("positive node = %#v, want above zero axis %d", positiveNode, chart.ZeroY)
	}
}

func TestCostExplorerChartViewRendersNegativeGroupedBar(t *testing.T) {
	t.Parallel()

	result := persistence.CostExplorerQueryResult{
		Rows: []persistence.CostExplorerQueryRow{
			{
				TimePeriodStart: "2026-02-01",
				GroupValues: []persistence.CostExplorerGroupValue{{
					Type:  "dimension",
					Key:   "service",
					Value: "AmazonEC2",
				}},
				NetCostMicros: 3_000_000,
			},
			{
				TimePeriodStart: "2026-02-01",
				GroupValues: []persistence.CostExplorerGroupValue{{
					Type:  "dimension",
					Key:   "service",
					Value: "Refunds",
				}},
				NetCostMicros: -1_500_000,
			},
		},
	}

	chart := costExplorerChartViewFromResult(result, "net_cost", "bar")
	positiveBar := mustFindChartBar(t, chart.Bars, "$3.00")
	negativeBar := mustFindChartBar(t, chart.Bars, "-$1.50")
	if positiveBar.Y >= chart.ZeroY || positiveBar.Y+positiveBar.Height != chart.ZeroY {
		t.Fatalf("positive bar = %#v, want bar ending at zero axis %d", positiveBar, chart.ZeroY)
	}
	if negativeBar.Y != chart.ZeroY || negativeBar.Height <= 0 || negativeBar.Y+negativeBar.Height <= chart.ZeroY {
		t.Fatalf("negative bar = %#v, want bar starting at zero axis %d and extending downward", negativeBar, chart.ZeroY)
	}
}

func TestCostExplorerChartViewRendersNegativeStackedBar(t *testing.T) {
	t.Parallel()

	result := persistence.CostExplorerQueryResult{
		Rows: []persistence.CostExplorerQueryRow{
			{
				TimePeriodStart: "2026-02-01",
				GroupValues: []persistence.CostExplorerGroupValue{{
					Type:  "dimension",
					Key:   "line_item_type",
					Value: "Usage",
				}},
				NetCostMicros: 4_000_000,
			},
			{
				TimePeriodStart: "2026-02-01",
				GroupValues: []persistence.CostExplorerGroupValue{{
					Type:  "dimension",
					Key:   "line_item_type",
					Value: "Credit",
				}},
				NetCostMicros: -1_000_000,
			},
			{
				TimePeriodStart: "2026-02-01",
				GroupValues: []persistence.CostExplorerGroupValue{{
					Type:  "dimension",
					Key:   "line_item_type",
					Value: "Refund",
				}},
				NetCostMicros: -2_000_000,
			},
		},
	}

	chart := costExplorerChartViewFromResult(result, "net_cost", "stacked_bar")
	positiveBar := mustFindChartBar(t, chart.Bars, "$4.00")
	creditBar := mustFindChartBar(t, chart.Bars, "-$1.00")
	refundBar := mustFindChartBar(t, chart.Bars, "-$2.00")
	if positiveBar.Y >= chart.ZeroY || positiveBar.Y+positiveBar.Height != chart.ZeroY {
		t.Fatalf("positive stacked bar = %#v, want bar ending at zero axis %d", positiveBar, chart.ZeroY)
	}
	if creditBar.Y != chart.ZeroY || creditBar.Height <= 0 {
		t.Fatalf("first negative stacked bar = %#v, want start at zero axis %d", creditBar, chart.ZeroY)
	}
	if refundBar.Y != creditBar.Y+creditBar.Height || refundBar.Height <= 0 {
		t.Fatalf("second negative stacked bar = %#v, want stacked below first negative bar %#v", refundBar, creditBar)
	}
}

func chartHasTickLabel(ticks []costExplorerChartTickView, label string) bool {
	for _, tick := range ticks {
		if tick.Label == label {
			return true
		}
	}
	return false
}

func mustFindChartBar(t *testing.T, bars []costExplorerChartBarView, valueLabel string) costExplorerChartBarView {
	t.Helper()

	for _, bar := range bars {
		if bar.ValueLabel == valueLabel {
			return bar
		}
	}
	t.Fatalf("missing chart bar with value %q in %#v", valueLabel, bars)
	return costExplorerChartBarView{}
}
