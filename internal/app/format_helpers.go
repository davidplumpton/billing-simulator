package app

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type positiveDecimalScaleOptions struct {
	RequiredMessage string
	NumericMessage  string
	FiniteMessage   string
	PositiveMessage string
	TooLargeMessage string
	Scale           float64
	MaxScaled       float64
}

// parsePositiveDecimalScaled converts a normalized positive decimal form value
// into a rounded scaled integer while rejecting non-finite floats before math.
func parsePositiveDecimalScaled(value string, options positiveDecimalScaleOptions) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, errors.New(options.RequiredMessage)
	}
	decimal, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", options.NumericMessage, err)
	}
	if math.IsNaN(decimal) || math.IsInf(decimal, 0) {
		return 0, errors.New(options.FiniteMessage)
	}
	if decimal <= 0 {
		return 0, errors.New(options.PositiveMessage)
	}
	scaled := math.Round(decimal * options.Scale)
	if math.IsNaN(scaled) || math.IsInf(scaled, 0) || scaled > options.MaxScaled {
		return 0, errors.New(options.TooLargeMessage)
	}
	return int64(scaled), nil
}

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
