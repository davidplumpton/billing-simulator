package csvsafe

// SpreadsheetString prefixes text cells that spreadsheet apps may treat as formulas.
// Callers should use it only for string fields in downloadable CSVs; numeric fields
// stay raw so query tools and aggregations can read them as numbers.
func SpreadsheetString(value string) string {
	if value == "" {
		return value
	}
	switch value[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}
