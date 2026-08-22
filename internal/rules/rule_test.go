package rules

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/slakje-nl/mqtt2prometheus/internal/config"
)

func compile(t *testing.T, rule config.Rule, src ...config.Source) *Rule {
	t.Helper()

	source := config.Source{}
	if len(src) == 1 {
		source = src[0]
	}

	compiled, err := Compile(source, rule)
	require.NoError(t, err)

	return compiled
}

func TestApply_ExtractsCapturesAsLabels(t *testing.T) {
	rule := compile(t, config.Rule{
		Match:      `^zwave/(?P<meter>[^/]+)/meter/(?P<endpoint>endpoint_\d+)/value/66049$`,
		MetricName: "zwave_power_meter_power_watts",
		Type:       config.TypeGauge,
		Value:      config.Value{From: config.FromJSON, Path: "value"},
	})

	match, matched, err := rule.Apply(
		"zwave/example_outlet/meter/endpoint_1/value/66049",
		[]byte(`{"time":1735906853203,"value":3.395}`),
	)

	require.NoError(t, err)
	require.True(t, matched)
	require.InDelta(t, 3.395, match.Value, 1e-9)
	require.Equal(t, map[string]string{"meter": "example_outlet", "endpoint": "endpoint_1"}, match.Labels)
	require.Equal(t, []string{"endpoint", "meter"}, rule.LabelNames())
}

func TestApply_TopicWithASpace(t *testing.T) {
	rule := compile(t, config.Rule{
		Match:      `^zigbee2mqtt/(?P<device>[^/]+)$`,
		MetricName: "zigbee_power_meter_power",
		Type:       config.TypeGauge,
		Value:      config.Value{From: config.FromJSON, Path: "power"},
	})

	match, matched, err := rule.Apply("zigbee2mqtt/example device", []byte(`{"power":102}`))

	require.NoError(t, err)
	require.True(t, matched)
	require.Equal(t, map[string]string{"device": "example device"}, match.Labels)
	require.InDelta(t, 102.0, match.Value, 1e-9)
}

func TestApply_NonMatchingTopic(t *testing.T) {
	rule := compile(t, config.Rule{
		Match:      `^zwave/(?P<node>[^/]+)/lastActive$`,
		MetricName: "zwave_node_last_active",
		Type:       config.TypeGauge,
		Value:      config.Value{From: config.FromJSON, Path: "value"},
	})

	_, matched, err := rule.Apply("zwave/example_sensor/somethingElse", []byte(`{"value":1}`))

	require.NoError(t, err)
	require.False(t, matched)
}

func TestApply_LabelTemplateConsumesItsCapture(t *testing.T) {
	rule := compile(t, config.Rule{
		Match:      `^zwave/(?P<meter>[^/]+)/meter/endpoint_(?P<ep>\d+)$`,
		MetricName: "zwave_meter",
		Type:       config.TypeGauge,
		Value:      config.Value{From: config.FromRaw},
		Labels:     map[string]string{"endpoint": "endpoint_{ep}"},
	})

	match, matched, err := rule.Apply("zwave/example_outlet/meter/endpoint_2", []byte("7"))

	require.NoError(t, err)
	require.True(t, matched)
	require.Equal(t, map[string]string{"meter": "example_outlet", "endpoint": "endpoint_2"}, match.Labels)
}

func TestApply_SourceLabelsAreMergedAndOverriddenByRuleLabels(t *testing.T) {
	rule := compile(t,
		config.Rule{
			Match:      `^t$`,
			MetricName: "m",
			Type:       config.TypeGauge,
			Value:      config.Value{From: config.FromRaw},
			Labels:     map[string]string{"scope": "rule"},
		},
		config.Source{Labels: map[string]string{"instance": "home", "scope": "source"}},
	)

	match, _, err := rule.Apply("t", []byte("1"))

	require.NoError(t, err)
	require.Equal(t, map[string]string{"instance": "home", "scope": "rule"}, match.Labels)
}

func TestApply_MissingFieldSkipsWithoutError(t *testing.T) {
	rule := compile(t, config.Rule{
		Match:      `^zigbee2mqtt/(?P<device>[^/]+)$`,
		MetricName: "zigbee_power_meter_energy_total",
		Type:       config.TypeCounter,
		Value:      config.Value{From: config.FromJSON, Path: "energy"},
	})

	match, matched, err := rule.Apply("zigbee2mqtt/example device", []byte(`{"current":22222.0}`))

	require.NoError(t, err)
	require.True(t, matched)
	require.Nil(t, match.Labels)
}

func TestApply_PayloadErrorIsReportedAsMatched(t *testing.T) {
	rule := compile(t, config.Rule{
		Match:      `^t$`,
		MetricName: "m",
		Type:       config.TypeGauge,
		Value:      config.Value{From: config.FromJSON, Path: "value"},
	})

	_, matched, err := rule.Apply("t", []byte("not json"))

	require.True(t, matched)
	require.ErrorIs(t, err, ErrBadJSON)
}

func TestCompile_RejectsBadRegexes(t *testing.T) {
	_, err := Compile(config.Source{}, config.Rule{Match: "^(["})
	require.ErrorContains(t, err, "match:")

	_, err = Compile(config.Source{}, config.Rule{
		Match: "^t$",
		Value: config.Value{From: config.FromRaw, Regex: "^(["},
	})
	require.ErrorContains(t, err, "value.regex:")
}

func TestCompile_CounterFlag(t *testing.T) {
	require.True(t, compile(t, config.Rule{Match: "^t$", Type: config.TypeCounter}).Counter)
	require.False(t, compile(t, config.Rule{Match: "^t$", Type: config.TypeGauge}).Counter)
}
