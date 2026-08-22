package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/slakje-nl/mqtt2prometheus/internal/broker"
	"github.com/slakje-nl/mqtt2prometheus/internal/config"
	"github.com/slakje-nl/mqtt2prometheus/internal/exporter"
	"github.com/slakje-nl/mqtt2prometheus/internal/store"
)

type Build struct {
	Version string
	Commit  string
}

type App struct {
	dir     string
	build   Build
	log     *slog.Logger
	samples *store.Store
	self    *exporter.Self
	router  *Router
	reg     *prometheus.Registry

	connected atomic.Bool
	seen      atomic.Bool
}

func New(dir string, build Build, log *slog.Logger) *App {
	samples := store.New()
	self := exporter.NewSelf(func() float64 { return float64(samples.Len()) })

	return &App{
		dir:     dir,
		build:   build,
		log:     log,
		samples: samples,
		self:    self,
		router:  NewRouter(samples, self, log),
		reg:     prometheus.NewRegistry(),
	}
}

func (a *App) Run(ctx context.Context) error {
	cfg, sources, err := a.load()
	if err != nil {
		return err
	}

	if err := a.self.Register(a.reg, exporter.NewCollector(a.samples)); err != nil {
		return fmt.Errorf("registering metrics: %w", err)
	}

	a.self.SetBuildInfo(a.build.Version, a.build.Commit, runtime.Version())
	a.router.Start(ctx, sources)

	defer a.router.Stop()

	return a.runComponents(ctx, cfg)
}

func (a *App) runComponents(ctx context.Context, cfg *config.Config) error {
	server := newServer(cfg.Server.Listen, a.reg, a.ready)

	components, cancel := context.WithCancel(ctx)
	defer cancel()

	errs := make(chan error, 2)

	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		defer wg.Done()

		a.log.Info("listening", "address", cfg.Server.Listen)
		errs <- serve(components, server)
	}()

	go func() {
		defer wg.Done()

		errs <- a.connect(components, cfg)
	}()

	err := <-errs
	cancel()
	wg.Wait()

	if second := <-errs; err == nil {
		err = second
	}

	return err
}

func (a *App) connect(ctx context.Context, cfg *config.Config) error {
	client := broker.NewPaho(broker.Credentials{
		URL:          cfg.MQTT.Broker,
		ClientID:     cfg.MQTT.ClientID,
		Username:     cfg.MQTT.Username,
		Password:     cfg.MQTT.Password,
		CleanSession: *cfg.MQTT.CleanSession,
	}, a.log, a)

	return client.Run(ctx, a.router.Subscriptions(), a.dispatch)
}

func (a *App) dispatch(msg broker.Message) {
	a.seen.Store(true)
	a.router.Dispatch(msg)
}

func (a *App) Connected(up bool) {
	a.connected.Store(up)
	a.self.SetConnected(up)
}

func (a *App) Reconnected() {
	a.self.Reconnects.Inc()
}

func (a *App) ready() bool {
	return a.connected.Load() && a.seen.Load()
}

func (a *App) load() (*config.Config, []*source, error) {
	cfg, err := config.Load(a.dir)
	if err != nil {
		return nil, nil, err
	}

	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}

	sources, err := compileSources(cfg)

	return cfg, sources, err
}

func NewLogger(level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLevel(level)}))
}

func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
