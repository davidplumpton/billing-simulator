package app

import "testing"

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
