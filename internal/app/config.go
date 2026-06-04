package app

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

// Config holds process-level settings needed to start the local simulator.
type Config struct {
	HTTPAddr      string
	WorkspacePath string
	StatePath     string
}

// DefaultConfig returns conservative local-only defaults for the simulator.
func DefaultConfig() Config {
	return Config{
		HTTPAddr: "127.0.0.1:8080",
	}
}

// DefaultStatePath returns the per-user app state file used by the CLI.
func DefaultStatePath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "aws-billing-simulator", "state.json")
}

// Validate rejects unsafe or incomplete runtime configuration before startup.
func (c Config) Validate() error {
	if strings.TrimSpace(c.HTTPAddr) == "" {
		return fmt.Errorf("http address is required")
	}

	host, _, err := net.SplitHostPort(c.HTTPAddr)
	if err != nil {
		return fmt.Errorf("http address must be host:port: %w", err)
	}
	if host != "127.0.0.1" && host != "localhost" {
		return fmt.Errorf("http address must bind to 127.0.0.1 or localhost")
	}

	return nil
}
