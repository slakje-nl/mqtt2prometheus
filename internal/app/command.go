package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/slakje-nl/mqtt2prometheus/internal/broker"
	"github.com/slakje-nl/mqtt2prometheus/internal/config"
)

const ConfigDirEnv = "MQTT2PROMETHEUS_CONFIG_DIR"

const (
	defaultWindow = 30 * time.Second
	defaultDepth  = 1
)

var shortFlagName = regexp.MustCompile(`([ :])-([A-Za-z])`)

const usageText = `mqtt2prometheus exports selected MQTT messages as Prometheus metrics.

usage:
  mqtt2prometheus run
  mqtt2prometheus discover [--for DURATION] [--count N] [--depth N] [PREFIX]
  mqtt2prometheus capture [--for DURATION] [--count N] FILTER
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
	case "discover":
		return c.discover(ctx, args[1:])
	case "capture":
		return c.capture(ctx, args[1:])
	default:
		return c.usage(fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}

func (c Command) run(ctx context.Context, args []string) error {
	dir, err := c.settings(c.flagSet("run"), args, 0)
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
	dir, err := c.settings(c.flagSet("verify"), args, 0)
	if err != nil {
		return err
	}

	return Verify(dir, c.Out)
}

func (c Command) healthcheck(args []string) error {
	dir, err := c.settings(c.flagSet("healthcheck"), args, 0)
	if err != nil {
		return err
	}

	return Healthcheck(dir)
}

func (c Command) version(args []string) error {
	flags := c.flagSet("version")
	if err := c.parse(flags, args, 0); err != nil {
		return err
	}

	_, err := fmt.Fprintf(c.Out, "mqtt2prometheus %s (%s)\n", c.Build.Version, c.Build.Commit)

	return err
}

func (c Command) discover(ctx context.Context, args []string) error {
	flags := c.flagSet("discover")
	window := flags.Duration("for", defaultWindow, "how long to listen; 0 listens until interrupted")
	count := flags.Int("count", 0, "stop after this many prefixes; 0 keeps listening")
	depth := flags.Int("depth", defaultDepth, "how many topic segments make a prefix")

	if err := c.parse(flags, args, 1); err != nil {
		return err
	}

	if *depth < 1 {
		return &UsageError{Message: "discover: --depth must be at least 1"}
	}

	if *count < 0 {
		return &UsageError{Message: "discover: --count cannot be negative"}
	}

	cfg, err := c.configuration()
	if err != nil {
		return err
	}

	return c.follow(ctx, cfg, "-discover", topicFilter(flags.Arg(0)), *window,
		newDiscovery(*depth, *count))
}

func (c Command) capture(ctx context.Context, args []string) error {
	flags := c.flagSet("capture")
	window := flags.Duration("for", defaultWindow, "how long to listen; 0 listens until interrupted")
	count := flags.Int("count", 0, "stop after this many messages; 0 keeps listening")

	if err := c.parse(flags, args, 1); err != nil {
		return err
	}

	filter := flags.Arg(0)
	if filter == "" {
		return &UsageError{Message: "capture: a topic filter is required, for example 'dsmr/#'"}
	}

	if *count < 0 {
		return &UsageError{Message: "capture: --count cannot be negative"}
	}

	cfg, err := c.configuration()
	if err != nil {
		return err
	}

	return c.follow(ctx, cfg, "-capture", filter, *window, newCapturer(*count))
}

func (c Command) configuration() (*config.Config, error) {
	dir, err := configDir()
	if err != nil {
		return nil, err
	}

	return loadConfig(dir)
}

func (c Command) follow(ctx context.Context, cfg *config.Config, suffix, filter string,
	window time.Duration, target collector) error {
	if _, err := fmt.Fprintln(c.ErrOut, "waiting for messages"); err != nil {
		return err
	}

	listening, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, 1)

	go func() {
		errs <- listen(listening, cfg, suffix,
			broker.Subscription{Filter: filter, QoS: *cfg.MQTT.QoS, SkipRetained: target.skipRetained()},
			window, newLogger(c.ErrOut, cfg.Log.Level), target.observe)

		target.close()
	}()

	go func() {
		select {
		case <-target.done():
			cancel()
		case <-listening.Done():
		}
	}()

	writeErr := drainLines(c.Out, target.lines())
	cancel()

	if listenErr := <-errs; listenErr != nil {
		return listenErr
	}

	return errors.Join(writeErr, closingNotice(c.ErrOut))
}

func closingNotice(w io.Writer) error {
	_, err := fmt.Fprintln(w, "closing")

	return err
}

func (c Command) flagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}

	return flags
}

func (c Command) settings(flags *flag.FlagSet, args []string, maxArgs int) (string, error) {
	if err := c.parse(flags, args, maxArgs); err != nil {
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

func (c Command) parse(flags *flag.FlagSet, args []string, maxArgs int) error {
	if err := flags.Parse(args); err != nil {
		if usageErr := c.writeFlagUsage(flags); usageErr != nil {
			return usageErr
		}

		return doubleDashed(err)
	}

	if flags.NArg() > maxArgs {
		return &UsageError{Message: fmt.Sprintf("%s: unexpected argument %q", flags.Name(), flags.Arg(maxArgs))}
	}

	return nil
}

func (c Command) writeFlagUsage(flags *flag.FlagSet) error {
	if _, err := io.WriteString(c.ErrOut, flagUsage(flags)); err != nil {
		return fmt.Errorf("writing usage: %w", err)
	}

	return nil
}

func doubleDashed(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return err
	}

	return &UsageError{Message: shortFlagName.ReplaceAllString(err.Error(), "$1--$2")}
}

func flagUsage(flags *flag.FlagSet) string {
	lines := []string{fmt.Sprintf("Usage of %s:", flags.Name())}

	flags.VisitAll(func(f *flag.Flag) {
		kind, usage := flag.UnquoteUsage(f)
		lines = append(lines, fmt.Sprintf("  --%s %s", f.Name, kind), "    \t"+usage+shownDefault(f))
	})

	return strings.Join(lines, "\n") + "\n"
}

func shownDefault(f *flag.Flag) string {
	if f.DefValue == "" || f.DefValue == "0" || f.DefValue == "false" {
		return ""
	}

	return fmt.Sprintf(" (default %s)", f.DefValue)
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
