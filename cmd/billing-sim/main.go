package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"aws-billing-simulator/internal/app"
)

func main() {
	cfg := app.DefaultConfig()
	cfg.StatePath = app.DefaultStatePath()
	cfg.OpenBrowser = true
	var logLevel string

	flags := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flags.StringVar(&cfg.HTTPAddr, "http", cfg.HTTPAddr, "local HTTP bind address")
	flags.StringVar(&cfg.WorkspacePath, "workspace", cfg.WorkspacePath, "workspace directory")
	flags.StringVar(&cfg.StatePath, "state", cfg.StatePath, "app state file")
	flags.BoolVar(&cfg.OpenBrowser, "browser", cfg.OpenBrowser, "open the dashboard in the default browser")
	flags.StringVar(&logLevel, "log-level", "info", "log level: debug, info, warn, or error")
	if err := flags.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "parse flags: %v\n", err)
		os.Exit(2)
	}

	level, err := app.ParseLogLevel(logLevel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse log level: %v\n", err)
		os.Exit(2)
	}
	logger := app.NewLogger(os.Stderr, level)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx, cfg, logger); err != nil {
		logger.Error("simulator stopped", "error", err)
		os.Exit(1)
	}
}
