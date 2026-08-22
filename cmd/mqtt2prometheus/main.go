package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/slakje-nl/mqtt2prometheus/internal/app"
	"github.com/slakje-nl/mqtt2prometheus/internal/config"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("mqtt2prometheus", flag.ContinueOnError)
	dir := flags.String("config", "config", "directory holding mqtt2prometheus.yaml and sources/")
	verify := flags.Bool("verify-config", false, "load and check the configuration, then exit")
	showVersion := flags.Bool("version", false, "print the version and exit")
	logLevel := flags.String("log-level", "", "override log.level from the configuration")
	healthcheck := flags.Bool("healthcheck", false, "probe the local health endpoint and exit")

	if err := flags.Parse(args); err != nil {
		return err
	}

	if *showVersion {
		_, err := fmt.Fprintf(out, "mqtt2prometheus %s (%s)\n", version, commit)

		return err
	}

	if *healthcheck {
		return app.Healthcheck(*dir)
	}

	if *verify {
		return app.Verify(*dir, out)
	}

	return serve(*dir, *logLevel)
}

func serve(dir, logLevel string) error {
	level := logLevel
	if level == "" {
		if cfg, err := config.Load(dir); err == nil {
			level = cfg.Log.Level
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return app.New(dir, app.Build{Version: version, Commit: commit}, app.NewLogger(level)).Run(ctx)
}
