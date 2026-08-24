package app

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/slakje-nl/mqtt2prometheus/internal/broker"
	"github.com/slakje-nl/mqtt2prometheus/internal/config"
	"github.com/slakje-nl/mqtt2prometheus/internal/exporter"
	"github.com/slakje-nl/mqtt2prometheus/internal/store"
)

func newTestRouter(t *testing.T) (*Router, *store.Store, *exporter.Self) {
	t.Helper()

	router, samples, self, _ := newLoggingRouter(t, "error")

	return router, samples, self
}

func newLoggingRouter(t *testing.T, level string) (*Router, *store.Store, *exporter.Self, *syncBuffer) {
	t.Helper()

	samples := store.New()
	self := exporter.NewSelf(func() float64 { return float64(samples.Len()) })
	logs := &syncBuffer{}
	router := NewRouter(samples, self, newLogger(logs, level))
	router.now = func() time.Time { return fixedTime }

	return router, samples, self, logs
}

func TestRouter_LogsEachMessageAndItsOutcomeAtInfo(t *testing.T) {
	router, _, _, logs := newLoggingRouter(t, "info")
	router.Start(t.Context(), compiled(t, zwaveConfig()))

	defer router.Stop()

	router.Dispatch(broker.Message{
		Topic:   "zwave/example_sensor/lastActive",
		Payload: []byte(`{"value":1711922310552}`),
	})
	router.Dispatch(broker.Message{Topic: "zwave/example_sensor/unknown", Payload: []byte("13")})

	require.Eventually(t, func() bool {
		return strings.Count(logs.String(), `"msg":"message"`) == 2
	}, 2*time.Second, 5*time.Millisecond)

	written := logs.String()

	require.Contains(t, written, `"topic":"zwave/example_sensor/lastActive"`)
	require.Contains(t, written, `"payload":"{\"value\":1711922310552}"`)
	require.Contains(t, written, `"processed":true`)
	require.Contains(t, written, `"topic":"zwave/example_sensor/unknown"`)
	require.Contains(t, written, `"payload":"13"`)
	require.Contains(t, written, `"processed":false`)
	require.Contains(t, written, `"source":"zwave"`)
}

func TestRouter_LogsNothingAtWarnForANormalMessage(t *testing.T) {
	router, samples, _, logs := newLoggingRouter(t, "warn")
	router.Start(t.Context(), compiled(t, zwaveConfig()))

	defer router.Stop()

	router.Dispatch(broker.Message{
		Topic:   "zwave/example_sensor/lastActive",
		Payload: []byte(`{"value":1711922310552}`),
	})

	require.Eventually(t, func() bool { return samples.Len() == 2 }, 2*time.Second, 5*time.Millisecond)
	require.Empty(t, logs.String())
}

func compiled(t *testing.T, cfg *config.Config) []*source {
	t.Helper()

	sources, err := compileSources(cfg)
	require.NoError(t, err)

	return sources
}

func TestRouter_DispatchesToTheOwningSource(t *testing.T) {
	router, samples, self := newTestRouter(t)
	router.Start(t.Context(), compiled(t, zwaveConfig()))

	defer router.Stop()

	router.Dispatch(broker.Message{
		Topic:   "zwave/example_sensor/lastActive",
		Payload: []byte(`{"value":1711922310552}`),
	})

	require.Eventually(t, func() bool { return samples.Len() == 2 }, 2*time.Second, 5*time.Millisecond)
	require.InDelta(t, 1.0, testutil.ToFloat64(self.Received.WithLabelValues("zwave")), 1e-9)
}

func TestRouter_IgnoresATopicNoSourceOwns(t *testing.T) {
	router, samples, self := newTestRouter(t)
	router.Start(t.Context(), compiled(t, zwaveConfig()))

	defer router.Stop()

	router.Dispatch(broker.Message{Topic: "zigbee2mqtt/example device", Payload: []byte(`{}`)})

	require.Never(t, func() bool { return samples.Len() > 0 }, 200*time.Millisecond, 10*time.Millisecond)
	require.InDelta(t, 0.0, testutil.ToFloat64(self.Dropped), 1e-9)
}

func TestRouter_CountsAnErrorByReason(t *testing.T) {
	router, _, self := newTestRouter(t)
	router.Start(t.Context(), compiled(t, zwaveConfig()))

	defer router.Stop()

	router.Dispatch(broker.Message{Topic: "zwave/example_sensor/lastActive", Payload: []byte("nope")})

	require.Eventually(t, func() bool {
		return testutil.ToFloat64(self.Errors.WithLabelValues("zwave", "json")) == 1
	}, 2*time.Second, 5*time.Millisecond)
}

func TestRouter_DropsWhenTheBufferIsFull(t *testing.T) {
	router, _, self, logs := newLoggingRouter(t, "warn")

	sources := compiled(t, zwaveConfig())
	router.mu.Lock()
	router.feeds = []*feed{{source: sources[0], messages: make(chan broker.Message)}}
	router.mu.Unlock()

	router.Dispatch(broker.Message{Topic: "zwave/a/lastActive", Payload: []byte(`{"value":1}`)})

	require.InDelta(t, 1.0, testutil.ToFloat64(self.Dropped), 1e-9)
	require.InDelta(t, 0.0, testutil.ToFloat64(self.Received.WithLabelValues("zwave")), 1e-9)
	require.Contains(t, logs.String(), "message dropped, source buffer full")
	require.Contains(t, logs.String(), `"topic":"zwave/a/lastActive"`)
}

func TestRouter_Subscriptions(t *testing.T) {
	router, _, _ := newTestRouter(t)
	router.Start(t.Context(), compiled(t, zwaveConfig()))

	defer router.Stop()

	require.Equal(t, []broker.Subscription{{Filter: "zwave/#", QoS: 1}}, router.Subscriptions())
}

func TestRouter_StopIsSafeBeforeStart(t *testing.T) {
	router, _, _ := newTestRouter(t)

	router.Stop()

	require.Empty(t, router.Subscriptions())
}

func TestRouter_ConsumeExitsWhenTheContextIsDone(t *testing.T) {
	router, _, _ := newTestRouter(t)

	ctx, cancel := context.WithCancel(t.Context())
	router.Start(ctx, compiled(t, zwaveConfig()))
	cancel()

	router.Stop()
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}
