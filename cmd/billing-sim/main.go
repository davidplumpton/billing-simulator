package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"aws-billing-simulator/internal/app"
	"aws-billing-simulator/internal/buildinfo"
)

func main() {
	cfg := app.DefaultConfig()
	cfg.StatePath = app.DefaultStatePath()
	cfg.OpenBrowser = true
	var logLevel string
	var showVersion bool

	flags := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	flags.StringVar(&cfg.HTTPAddr, "http", cfg.HTTPAddr, "local HTTP bind address")
	flags.StringVar(&cfg.WorkspacePath, "workspace", cfg.WorkspacePath, "workspace directory")
	flags.StringVar(&cfg.StatePath, "state", cfg.StatePath, "app state file")
	flags.BoolVar(&cfg.OpenBrowser, "browser", cfg.OpenBrowser, "open the dashboard in the default browser")
	flags.StringVar(&logLevel, "log-level", "info", "log level: debug, info, warn, or error")
	flags.BoolVar(&showVersion, "version", false, "print the billing-sim release version and exit")
	if err := flags.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "parse flags: %v\n", err)
		os.Exit(2)
	}
	if showVersion {
		fmt.Fprintf(os.Stdout, "billing-sim %s\n", buildinfo.Current().Version)
		return
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
