package app

import (
	"context"
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

	samples := store.New()
	self := exporter.NewSelf(func() float64 { return float64(samples.Len()) })
	router := NewRouter(samples, self, quietLogger())
	router.now = func() time.Time { return fixedTime }

	return router, samples, self
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
	router, _, self := newTestRouter(t)

	sources := compiled(t, zwaveConfig())
	router.mu.Lock()
	router.feeds = []*feed{{source: sources[0], messages: make(chan broker.Message)}}
	router.mu.Unlock()

	router.Dispatch(broker.Message{Topic: "zwave/a/lastActive", Payload: []byte(`{"value":1}`)})

	require.InDelta(t, 1.0, testutil.ToFloat64(self.Dropped), 1e-9)
	require.InDelta(t, 0.0, testutil.ToFloat64(self.Received.WithLabelValues("zwave")), 1e-9)
}

func TestRouter_SubscriptionsAndMetricNames(t *testing.T) {
	router, _, _ := newTestRouter(t)
	router.Start(t.Context(), compiled(t, zwaveConfig()))

	defer router.Stop()

	require.Equal(t, []broker.Subscription{{Filter: "zwave/#", QoS: 1}}, router.Subscriptions())
	require.Equal(t, map[string]struct{}{
		"zwave_node_last_active": {},
		"zwave_last_update":      {},
	}, router.MetricNames())
}

func TestRouter_StopIsSafeBeforeStart(t *testing.T) {
	router, _, _ := newTestRouter(t)

	router.Stop()

	require.Empty(t, router.Subscriptions())
}

func TestRouter_ReloadSwapsTheRules(t *testing.T) {
	router, samples, _ := newTestRouter(t)
	router.Start(t.Context(), compiled(t, zwaveConfig()))

	defer router.Stop()

	replacement := zwaveConfig()
	replacement.SourceList[0].Rules[0].MetricName = "zwave_renamed"
	router.Reload(t.Context(), compiled(t, replacement))

	router.Dispatch(broker.Message{
		Topic: "zwave/example_sensor/lastActive", Payload: []byte(`{"value":1000}`),
	})

	require.Eventually(t, func() bool {
		for _, sample := range samples.Snapshot() {
			if sample.Key.Name == "zwave_renamed" {
				return true
			}
		}

		return false
	}, 2*time.Second, 5*time.Millisecond)
}

func TestRouter_ConsumeExitsWhenTheContextIsDone(t *testing.T) {
	router, _, _ := newTestRouter(t)

	ctx, cancel := context.WithCancel(t.Context())
	router.Start(ctx, compiled(t, zwaveConfig()))
	cancel()

	router.Stop()
}
