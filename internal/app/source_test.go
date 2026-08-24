package app

import (
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/slakje-nl/mqtt2prometheus/internal/broker"
	"github.com/slakje-nl/mqtt2prometheus/internal/config"
	"github.com/slakje-nl/mqtt2prometheus/internal/rules"
	"github.com/slakje-nl/mqtt2prometheus/internal/store"
)

var fixedTime = time.Unix(1755400000, 0)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func zwaveConfig() *config.Config {
	return &config.Config{
		MQTT:   config.MQTT{Broker: "tcp://broker:1883", QoS: ptr(uint8(1))},
		Server: config.Server{Listen: ":9000"},
		SourceList: []config.Source{{
			Name:              "zwave",
			Subscribe:         "zwave/#",
			Path:              "sources/zwave.yaml",
			LastUpdatedMetric: "zwave_last_update",
			Rules: []config.Rule{{
				Match:      `^zwave/(?P<node>[^/]+)/lastActive$`,
				MetricName: "zwave_node_last_active",
				Type:       config.TypeGauge,
				Value:      config.Value{From: config.FromJSON, Path: "value", Scale: ptr(0.001)},
			}},
		}},
	}
}

func ptr[T any](v T) *T { return &v }

func TestCompileSources_CarriesEffectiveQoSAndBroker(t *testing.T) {
	cfg := zwaveConfig()
	cfg.SourceList[0].QoS = ptr(uint8(2))
	cfg.SourceList[0].Broker = "tcp://other:1883"

	sources, err := compileSources(cfg)
	require.NoError(t, err)

	require.Equal(t, uint8(2), sources[0].qos)
	require.Equal(t, "tcp://other:1883", sources[0].brokerURL)
	require.Equal(t, "zwave/", sources[0].prefix)
}

func TestCompileSources_InheritsFromTheProcessConfig(t *testing.T) {
	sources, err := compileSources(zwaveConfig())
	require.NoError(t, err)

	require.Equal(t, uint8(1), sources[0].qos)
	require.Equal(t, "tcp://broker:1883", sources[0].brokerURL)
}

func TestCompileSources_ReportsABadRule(t *testing.T) {
	cfg := zwaveConfig()
	cfg.SourceList[0].Rules[0].Match = "^(["

	_, err := compileSources(cfg)

	require.ErrorContains(t, err, "sources/zwave.yaml: rule 0: match")
}

func TestTopicPrefix(t *testing.T) {
	require.Equal(t, "zwave/", topicPrefix("zwave/#"))
	require.Equal(t, "$SYS/", topicPrefix("$SYS/#"))
	require.Equal(t, "a/", topicPrefix("a/+/b"))
	require.Equal(t, "exact/topic", topicPrefix("exact/topic"))
}

func TestOwns(t *testing.T) {
	s := &source{prefix: "zwave/"}

	require.True(t, s.owns("zwave/example_sensor/lastActive"))
	require.False(t, s.owns("zigbee2mqtt/example device"))
	require.False(t, s.owns("zwav"))
}

func TestApply_WritesMetricAndBothHeartbeats(t *testing.T) {
	cfg := zwaveConfig()
	cfg.SourceList[0].Rules[0].LastUpdatedMetric = "zwave_node_last_seen"

	sources, err := compileSources(cfg)
	require.NoError(t, err)

	samples := store.New()
	problems, _ := sources[0].apply(samples, fixedTime, broker.Message{
		Topic:   "zwave/example_sensor/lastActive",
		Payload: []byte(`{"time":1711922310802,"value":1711922310552}`),
	})

	require.Empty(t, problems)

	snapshot := samples.Snapshot()
	require.Len(t, snapshot, 3)
	require.Equal(t, "zwave_last_update", snapshot[0].Key.Name)
	require.InDelta(t, float64(fixedTime.Unix()), snapshot[0].Value, 1e-6)
	require.Equal(t, "zwave_node_last_active", snapshot[1].Key.Name)
	require.InDelta(t, 1711922310.552, snapshot[1].Value, 1e-6)
	require.Equal(t, "zwave_node_last_seen", snapshot[2].Key.Name)
	require.Equal(t, []string{"node"}, snapshot[2].Key.LabelNames)
}

func TestApply_UnmatchedTopicWritesNothing(t *testing.T) {
	sources, err := compileSources(zwaveConfig())
	require.NoError(t, err)

	samples := store.New()
	problems, _ := sources[0].apply(samples, fixedTime, broker.Message{
		Topic: "zwave/example_sensor/unknownThing", Payload: []byte(`{"value":1}`),
	})

	require.Empty(t, problems)
	require.Zero(t, samples.Len())
}

func TestApply_SkippedValueWritesNoHeartbeatForThatRule(t *testing.T) {
	cfg := zwaveConfig()
	cfg.SourceList[0].Rules[0].Value = config.Value{From: config.FromJSON, Path: "missing"}
	cfg.SourceList[0].Rules[0].LastUpdatedMetric = "zwave_node_last_seen"

	sources, err := compileSources(cfg)
	require.NoError(t, err)

	samples := store.New()
	_, _ = sources[0].apply(samples, fixedTime, broker.Message{
		Topic: "zwave/example_sensor/lastActive", Payload: []byte(`{"other":1}`),
	})

	require.Equal(t, 1, samples.Len())
	require.Equal(t, "zwave_last_update", samples.Snapshot()[0].Key.Name)
}

func TestApply_ReportsAPayloadProblem(t *testing.T) {
	sources, err := compileSources(zwaveConfig())
	require.NoError(t, err)

	problems, _ := sources[0].apply(store.New(), fixedTime, broker.Message{
		Topic: "zwave/example_sensor/lastActive", Payload: []byte("not json"),
	})

	require.Len(t, problems, 1)
	require.ErrorIs(t, problems[0], rules.ErrBadJSON)
}

func TestApply_CounterKind(t *testing.T) {
	cfg := zwaveConfig()
	cfg.SourceList[0].Rules[0].Type = config.TypeCounter
	cfg.SourceList[0].Rules[0].Value = config.Value{From: config.FromJSON, Path: "value"}

	sources, err := compileSources(cfg)
	require.NoError(t, err)

	samples := store.New()
	_, _ = sources[0].apply(samples, fixedTime, broker.Message{
		Topic: "zwave/example_sensor/lastActive", Payload: []byte(`{"value":5}`),
	})

	require.Equal(t, store.Counter, samples.Snapshot()[1].Kind)
}

func TestApply_SourceWithoutHeartbeat(t *testing.T) {
	cfg := zwaveConfig()
	cfg.SourceList[0].LastUpdatedMetric = ""

	sources, err := compileSources(cfg)
	require.NoError(t, err)

	samples := store.New()
	_, _ = sources[0].apply(samples, fixedTime, broker.Message{
		Topic: "zwave/example_sensor/lastActive", Payload: []byte(`{"value":1000}`),
	})

	require.Equal(t, 1, samples.Len())
}

func TestMetricNames(t *testing.T) {
	cfg := zwaveConfig()
	cfg.SourceList[0].Rules[0].LastUpdatedMetric = "zwave_node_last_seen"

	sources, err := compileSources(cfg)
	require.NoError(t, err)

	require.ElementsMatch(t,
		[]string{"zwave_node_last_active", "zwave_node_last_seen", "zwave_last_update"},
		sources[0].metricNames())
}

func TestMetricNames_WithoutHeartbeats(t *testing.T) {
	cfg := zwaveConfig()
	cfg.SourceList[0].LastUpdatedMetric = ""

	sources, err := compileSources(cfg)
	require.NoError(t, err)

	require.Equal(t, []string{"zwave_node_last_active"}, sources[0].metricNames())
}

func TestReasonOf(t *testing.T) {
	require.Equal(t, "json", reasonOf(rules.ErrBadJSON))
	require.Equal(t, "regex", reasonOf(rules.ErrRegexNoMatch))
	require.Equal(t, "not_numeric", reasonOf(rules.ErrNotNumeric))
	require.Equal(t, "other", reasonOf(errors.New("surprise")))
}

func TestLogProblems(t *testing.T) {
	logProblems(quietLogger(), "zwave", broker.Message{Topic: "t"}, []error{rules.ErrBadJSON})
}
