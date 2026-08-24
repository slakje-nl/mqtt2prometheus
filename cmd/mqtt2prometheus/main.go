package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/slakje-nl/mqtt2prometheus/internal/app"
)

var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		stop()
	}()

	command := app.Command{
		Build:  app.Build{Version: version, Commit: commit},
		Out:    os.Stdout,
		ErrOut: os.Stderr,
	}

	if err := command.Execute(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err.Error())

		return exitCode(err)
	}

	return 0
}

func exitCode(err error) int {
	usage := &app.UsageError{}
	if errors.As(err, &usage) {
		return 2
	}

	return 1
}
