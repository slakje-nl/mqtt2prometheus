package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/slakje-nl/mqtt2prometheus/internal/config"
)

const ConfigDirEnv = "MQTT2PROMETHEUS_CONFIG_DIR"

const usageText = `mqtt2prometheus exports selected MQTT messages as Prometheus metrics.

usage:
  mqtt2prometheus run
  mqtt2prometheus verify
  mqtt2prometheus healthcheck
  mqtt2prometheus version

The configuration directory is read from ` + ConfigDirEnv + `.
`

type UsageError struct {
	Message string
}

func (e *UsageError) Error() string {
	return e.Message
}

type Command struct {
	Build  Build
	Out    io.Writer
	ErrOut io.Writer
}

func (c Command) Execute(ctx context.Context, args []string) error {
	err := c.dispatch(ctx, args)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}

	return err
}

func (c Command) dispatch(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return c.usage("no subcommand given")
	}

	switch args[0] {
	case "run":
		return c.run(ctx, args[1:])
	case "verify":
		return c.verify(args[1:])
	case "healthcheck":
		return c.healthcheck(args[1:])
	case "version":
		return c.version(args[1:])
	default:
		return c.usage(fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

func (c Command) run(ctx context.Context, args []string) error {
	dir, err := c.settings(c.flagSet("run"), args)
	if err != nil {
		return err
	}

	cfg, err := loadConfig(dir)
	if err != nil {
		return err
	}

	return New(dir, c.Build, NewLogger(cfg.Log.Level)).Run(ctx)
}

func (c Command) verify(args []string) error {
	dir, err := c.settings(c.flagSet("verify"), args)
	if err != nil {
		return err
	}

	return Verify(dir, c.Out)
}

func (c Command) healthcheck(args []string) error {
	dir, err := c.settings(c.flagSet("healthcheck"), args)
	if err != nil {
		return err
	}

	return Healthcheck(dir)
}

func (c Command) version(args []string) error {
	flags := c.flagSet("version")
	if err := parse(flags, args); err != nil {
		return err
	}

	_, err := fmt.Fprintf(c.Out, "mqtt2prometheus %s (%s)\n", c.Build.Version, c.Build.Commit)

	return err
}

func (c Command) flagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(c.ErrOut)

	return flags
}

func (c Command) settings(flags *flag.FlagSet, args []string) (string, error) {
	if err := parse(flags, args); err != nil {
		return "", err
	}

	return configDir()
}

func (c Command) usage(message string) error {
	if _, err := io.WriteString(c.ErrOut, usageText); err != nil {
		return err
	}

	return &UsageError{Message: message}
}

func parse(flags *flag.FlagSet, args []string) error {
	if err := flags.Parse(args); err != nil {
		return err
	}

	if flags.NArg() > 0 {
		return &UsageError{Message: fmt.Sprintf("%s: unexpected argument %q", flags.Name(), flags.Arg(0))}
	}

	return nil
}

func configDir() (string, error) {
	dir := os.Getenv(ConfigDirEnv)
	if dir == "" {
		return "", fmt.Errorf("%s is not set", ConfigDirEnv)
	}

	return dir, nil
}

func loadConfig(dir string) (*config.Config, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}
