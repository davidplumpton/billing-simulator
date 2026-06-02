package app

import (
	"log/slog"
	"testing"
)

func TestParseLogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		raw     string
		want    slog.Level
		wantErr bool
	}{
		{raw: "debug", want: slog.LevelDebug},
		{raw: "info", want: slog.LevelInfo},
		{raw: "warn", want: slog.LevelWarn},
		{raw: "warning", want: slog.LevelWarn},
		{raw: "error", want: slog.LevelError},
		{raw: "", want: slog.LevelInfo},
		{raw: "verbose", want: slog.LevelInfo, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			t.Parallel()

			got, err := ParseLogLevel(test.raw)
			if (err != nil) != test.wantErr {
				t.Fatalf("ParseLogLevel() error = %v, wantErr %v", err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("ParseLogLevel() = %v, want %v", got, test.want)
			}
		})
	}
}
