package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/slakje-nl/mqtt2prometheus/internal/broker"
	"github.com/slakje-nl/mqtt2prometheus/internal/exporter"
	"github.com/slakje-nl/mqtt2prometheus/internal/store"
)

const sourceBuffer = 1024

type clock func() time.Time

type Router struct {
	samples *store.Store
	self    *exporter.Self
	log     *slog.Logger
	now     clock

	mu      sync.RWMutex
	feeds   []*feed
	stop    context.CancelFunc
	stopped *sync.WaitGroup
}

type feed struct {
	source   *source
	messages chan broker.Message
}

func NewRouter(samples *store.Store, self *exporter.Self, log *slog.Logger) *Router {
	return &Router{samples: samples, self: self, log: log, now: time.Now}
}

func (r *Router) Dispatch(msg broker.Message) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, f := range r.feeds {
		if !f.source.owns(msg.Topic) {
			continue
		}

		select {
		case f.messages <- msg:
			r.self.Received.WithLabelValues(f.source.name).Inc()
		default:
			r.self.Dropped.Inc()
			r.log.Warn("message dropped, source buffer full",
				"source", f.source.name, "topic", msg.Topic)
		}
	}
}

func (r *Router) Start(ctx context.Context, sources []*source) {
	consumers, cancel := context.WithCancel(ctx)

	feeds := make([]*feed, 0, len(sources))
	for _, src := range sources {
		feeds = append(feeds, &feed{source: src, messages: make(chan broker.Message, sourceBuffer)})
	}

	var wg sync.WaitGroup

	for _, f := range feeds {
		wg.Add(1)

		go func() {
			defer wg.Done()

			r.consume(consumers, f)
		}()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.feeds = feeds
	r.stop = cancel
	r.stopped = &wg
}

func (r *Router) Stop() {
	r.mu.Lock()
	stop, stopped := r.stop, r.stopped
	r.feeds, r.stop, r.stopped = nil, nil, nil
	r.mu.Unlock()

	if stop == nil {
		return
	}

	stop()
	stopped.Wait()
}

func (r *Router) consume(ctx context.Context, f *feed) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-f.messages:
			r.handle(f, msg)
		}
	}
}

func (r *Router) handle(f *feed, msg broker.Message) {
	problems, matched := f.source.apply(r.samples, r.now(), msg)

	r.log.Info("message", "source", f.source.name, "topic", msg.Topic,
		"payload", string(msg.Payload), "processed", matched)

	logProblems(r.log, f.source.name, msg, problems)

	for _, problem := range problems {
		r.self.Errors.WithLabelValues(f.source.name, reasonOf(problem)).Inc()
	}
}

func (r *Router) Subscriptions() []broker.Subscription {
	r.mu.RLock()
	defer r.mu.RUnlock()

	subs := make([]broker.Subscription, 0, len(r.feeds))
	for _, f := range r.feeds {
		subs = append(subs, broker.Subscription{Filter: f.source.subscribe, QoS: f.source.qos})
	}

	return subs
}
