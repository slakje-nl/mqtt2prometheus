package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/slakje-nl/mqtt2prometheus/internal/broker"
	"github.com/slakje-nl/mqtt2prometheus/internal/config"
)

const lineBuffer = 1024

type collector interface {
	observe(broker.Message)
	close()
	lines() <-chan string
	done() <-chan struct{}
}

type emitter struct {
	limit int
	out   chan string
	full  chan struct{}

	mu     sync.Mutex
	count  int
	closed bool
}

func newEmitter(limit int) *emitter {
	return &emitter{
		limit: limit,
		out:   make(chan string, lineBuffer),
		full:  make(chan struct{}),
	}
}

func (e *emitter) emit(line string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed || e.atLimit() {
		return
	}

	select {
	case e.out <- line:
		e.count++
	default:
		return
	}

	if e.atLimit() {
		close(e.full)
	}
}

func (e *emitter) atLimit() bool {
	return e.limit > 0 && e.count >= e.limit
}

func (e *emitter) close() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.closed {
		return
	}

	e.closed = true
	close(e.out)
}

func (e *emitter) lines() <-chan string {
	return e.out
}

func (e *emitter) done() <-chan struct{} {
	return e.full
}

type quietObserver struct{}

func (quietObserver) Connected(bool) {}

func (quietObserver) Reconnected() {}

func listen(ctx context.Context, cfg *config.Config, suffix string, sub broker.Subscription,
	window time.Duration, log *slog.Logger, handle broker.Handler) error {
	if window > 0 {
		timed, cancel := context.WithTimeout(ctx, window)
		defer cancel()

		ctx = timed
	}

	client := broker.NewPaho(broker.Credentials{
		URL:          cfg.MQTT.Broker,
		ClientID:     cfg.MQTT.ClientID + suffix,
		Username:     cfg.MQTT.Username,
		Password:     cfg.MQTT.Password,
		CleanSession: true,
	}, log, quietObserver{})

	return client.Run(ctx, []broker.Subscription{sub}, handle)
}

func drainLines(w io.Writer, lines <-chan string) error {
	for line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}

	return nil
}

func topicFilter(prefix string) string {
	if prefix == "" {
		return "#"
	}

	return strings.TrimSuffix(prefix, "/") + "/#"
}
