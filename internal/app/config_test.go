package app

import "testing"

func TestDefaultConfigUsesAvailableLoopbackPort(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	if cfg.HTTPAddr != "127.0.0.1:0" {
		t.Fatalf("HTTPAddr = %q, want %q", cfg.HTTPAddr, "127.0.0.1:0")
	}
	if cfg.OpenBrowser {
		t.Fatal("OpenBrowser = true, want false for library/test startup")
	}
}

func TestConfigValidateRequiresLocalHTTPAddress(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		addr    string
		wantErr bool
	}{
		{name: "loopback", addr: "127.0.0.1:8080"},
		{name: "localhost", addr: "localhost:8080"},
		{name: "empty", addr: "", wantErr: true},
		{name: "missing port", addr: "127.0.0.1", wantErr: true},
		{name: "wildcard", addr: "0.0.0.0:8080", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := DefaultConfig()
			cfg.HTTPAddr = test.addr
			err := cfg.Validate()
			if (err != nil) != test.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, test.wantErr)
			}
		})
	}
}
