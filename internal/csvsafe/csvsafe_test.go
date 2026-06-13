package csvsafe

import "testing"

func TestSpreadsheetStringNeutralizesFormulaPrefixes(t *testing.T) {
	t.Parallel()

	for value, want := range map[string]string{
		"=SUM(1,1)": "'=SUM(1,1)",
		"+SUM(1,1)": "'+SUM(1,1)",
		"-SUM(1,1)": "'-SUM(1,1)",
		"@SUM(1,1)": "'@SUM(1,1)",
		"plain":     "plain",
		"":          "",
	} {
		if got := SpreadsheetString(value); got != want {
			t.Fatalf("SpreadsheetString(%q) = %q, want %q", value, got, want)
		}
	}
}
