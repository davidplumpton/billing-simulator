package app

import (
	"fmt"
	"strconv"
	"strings"
)

func formatQuantityMicros(value int64) string {
	if value%1_000_000 == 0 {
		return strconv.FormatInt(value/1_000_000, 10)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.6f", float64(value)/1_000_000), "0"), ".")
}

func formatUSDMicros(value int64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	whole := value / 1_000_000
	fraction := value % 1_000_000
	if fraction == 0 {
		return fmt.Sprintf("%s$%d.00", sign, whole)
	}
	fractionText := strings.TrimRight(fmt.Sprintf("%06d", fraction), "0")
	for len(fractionText) < 2 {
		fractionText += "0"
	}
	return fmt.Sprintf("%s$%d.%s", sign, whole, fractionText)
}
