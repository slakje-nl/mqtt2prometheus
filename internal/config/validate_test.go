package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func qos(v uint8) *uint8 { return &v }

func boolPtr(v bool) *bool { return &v }

func validConfig() *Config {
	return &Config{
		MQTT: MQTT{
			Broker:       "tcp://broker:1883",
			ClientID:     "mqtt2prometheus",
			Username:     "user",
			Password:     "pass",
			QoS:          qos(1),
			CleanSession: boolPtr(false),
		},
		Server:  Server{Listen: ":9000"},
		Log:     Log{Level: "info"},
		Sources: "sources/*.yaml",
		SourceList: []Source{{
			Name:      "zwave",
			Subscribe: "zwave/#",
			Path:      "sources/zwave.yaml",
			Rules: []Rule{{
				Match:      `^zwave/(?P<node>[^/]+)/lastActive$`,
				MetricName: "zwave_node_last_active",
				Type:       TypeGauge,
				Value:      Value{From: FromJSON, Path: "value"},
			}},
		}},
	}
}

func TestValidate_AcceptsAValidConfig(t *testing.T) {
	require.NoError(t, validConfig().Validate())
}

func TestValidate_ProcessFieldsAreRequired(t *testing.T) {
	cfg := &Config{}

	err := cfg.Validate()
	require.Error(t, err)

	for _, want := range []string{
		"mqtt.broker is required",
		"mqtt.client_id is required",
		"mqtt.username is required",
		"mqtt.password is required",
		"mqtt.qos is required",
		"mqtt.clean_session is required",
		"server.listen is required",
		"log.level is required",
		"no sources loaded",
	} {
		require.ErrorContains(t, err, want)
	}
}

func TestValidate_ProcessFieldRanges(t *testing.T) {
	cfg := validConfig()
	cfg.MQTT.QoS = qos(3)
	cfg.Log.Level = "chatty"

	err := cfg.Validate()
	require.ErrorContains(t, err, "mqtt.qos must be 0, 1 or 2, got 3")
	require.ErrorContains(t, err, `log.level must be debug, info, warn or error, got "chatty"`)
}

func TestValidate_AcceptsEveryLogLevel(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		cfg := validConfig()
		cfg.Log.Level = level

		require.NoError(t, cfg.Validate(), level)
	}
}

func TestValidate_SourceFieldsAreRequired(t *testing.T) {
	cfg := validConfig()
	cfg.SourceList = []Source{{Path: "sources/broken.yaml"}}

	err := cfg.Validate()
	require.ErrorContains(t, err, "sources/broken.yaml: name is required")
	require.ErrorContains(t, err, "sources/broken.yaml: subscribe is required")
	require.ErrorContains(t, err, "sources/broken.yaml: at least one rule is required")
}

func TestValidate_SourceQoSRange(t *testing.T) {
	cfg := validConfig()
	cfg.SourceList[0].QoS = qos(9)

	require.ErrorContains(t, cfg.Validate(), "qos must be 0, 1 or 2, got 9")
}

func TestValidate_DuplicateSourceName(t *testing.T) {
	cfg := validConfig()
	second := cfg.SourceList[0]
	second.Path = "sources/other.yaml"
	second.Rules[0].MetricName = "other_metric"
	cfg.SourceList = append(cfg.SourceList, second)

	require.ErrorContains(t, cfg.Validate(), `source name "zwave" already used by sources/zwave.yaml`)
}

func TestValidate_RuleFieldsAreRequired(t *testing.T) {
	cfg := validConfig()
	cfg.SourceList[0].Rules = []Rule{{}}

	err := cfg.Validate()
	require.ErrorContains(t, err, "rule 0: metric_name is required")
	require.ErrorContains(t, err, "rule 0: type is required")
	require.ErrorContains(t, err, "rule 0: match is required")
	require.ErrorContains(t, err, "rule 0: value.from is required")
}

func TestValidate_RuleFieldValues(t *testing.T) {
	cases := map[string]struct {
		mutate func(*Rule)
		want   string
	}{
		"bad metric name":       {func(r *Rule) { r.MetricName = "has-a-dash" }, `metric_name "has-a-dash" is not a valid Prometheus metric name`},
		"bad heartbeat name":    {func(r *Rule) { r.LastUpdatedMetric = "no dashes here" }, `last_updated_metric "no dashes here" is not a valid`},
		"bad type":              {func(r *Rule) { r.Type = "histogram" }, `type must be "gauge" or "counter", got "histogram"`},
		"bad match regex":       {func(r *Rule) { r.Match = "^zwave/([" }, "match is not a valid regular expression"},
		"bad value from":        {func(r *Rule) { r.Value = Value{From: "xml"} }, `value.from must be "json" or "raw", got "xml"`},
		"json without path":     {func(r *Rule) { r.Value = Value{From: FromJSON} }, "value.path is required when value.from is json"},
		"raw with path":         {func(r *Rule) { r.Value = Value{From: FromRaw, Path: "value"} }, "value.path is only valid when value.from is json"},
		"bad value regex":       {func(r *Rule) { r.Value.Regex = "^([" }, "value.regex is not a valid regular expression"},
		"value regex no groups": {func(r *Rule) { r.Value.Regex = "^[0-9]+$" }, "value.regex must have exactly one capture group, got 0"},
		"value regex 2 groups":  {func(r *Rule) { r.Value.Regex = "^([0-9]+) ([a-z]+)$" }, "value.regex must have exactly one capture group, got 2"},
		"zero scale":            {func(r *Rule) { s := 0.0; r.Value.Scale = &s }, "value.scale must not be zero"},
		"bad label name": {func(r *Rule) {
			r.Labels = map[string]string{"not a label": "x"}
		}, `label "not a label" is not a valid Prometheus label name`},
		"label collides with capture": {func(r *Rule) {
			r.Labels = map[string]string{"node": "x"}
		}, `label "node" collides with a capture group of the same name`},
		"label references unknown capture": {func(r *Rule) {
			r.Labels = map[string]string{"where": "{nope}"}
		}, `label "where" references {nope}, which is not a capture group of match`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := validConfig()
			tc.mutate(&cfg.SourceList[0].Rules[0])

			require.ErrorContains(t, cfg.Validate(), tc.want)
		})
	}
}

func TestValidate_InvalidCaptureGroupName(t *testing.T) {
	cfg := validConfig()
	cfg.SourceList[0].Rules[0].Match = `^zwave/(?P<1node>[^/]+)$`

	require.ErrorContains(t, cfg.Validate(), `capture group 1 "1node" is not a valid Prometheus label name`)
}

func TestValidate_SameMetricNameWithDifferentLabelSets(t *testing.T) {
	cfg := validConfig()
	cfg.SourceList[0].Rules = append(cfg.SourceList[0].Rules, Rule{
		Match:      `^zwave/(?P<node>[^/]+)/(?P<endpoint>endpoint_\d+)/lastActive$`,
		MetricName: "zwave_node_last_active",
		Type:       TypeGauge,
		Value:      Value{From: FromJSON, Path: "value"},
	})

	require.ErrorContains(t, cfg.Validate(),
		`metric_name "zwave_node_last_active" is already used with labels [node], this rule uses [endpoint node]`)
}

func TestValidate_SameMetricNameWithSameLabelSetIsFine(t *testing.T) {
	cfg := validConfig()
	cfg.SourceList[0].Rules = append(cfg.SourceList[0].Rules, Rule{
		Match:      `^zwave/(?P<node>[^/]+)/alsoActive$`,
		MetricName: "zwave_node_last_active",
		Type:       TypeGauge,
		Value:      Value{From: FromJSON, Path: "value"},
	})

	require.NoError(t, cfg.Validate())
}

func TestValidate_ConsistencyCheckSkipsUnvalidatableRules(t *testing.T) {
	cfg := validConfig()
	cfg.SourceList[0].Rules = []Rule{
		{Match: "^a$", Type: TypeGauge, Value: Value{From: FromRaw}},
		{MetricName: "no_match", Type: TypeGauge, Value: Value{From: FromRaw}},
		{Match: "^([", MetricName: "bad_regex", Type: TypeGauge, Value: Value{From: FromRaw}},
	}

	err := cfg.Validate()
	require.ErrorContains(t, err, "metric_name is required")
	require.ErrorContains(t, err, "match is required")
	require.ErrorContains(t, err, "match is not a valid regular expression")
}

func TestValidate_LabelConsumesCapture(t *testing.T) {
	cfg := validConfig()
	cfg.SourceList[0].Rules[0].Labels = map[string]string{"who": "node-{node}"}

	require.NoError(t, cfg.Validate())
	require.Equal(t, []string{"who"}, LabelNames([]string{"node"}, map[string]string{"who": "node-{node}"}))
}

func TestProblems_ErrorFormatsEveryProblem(t *testing.T) {
	err := Problems{"first", "second"}

	require.EqualError(t, err, "2 configuration problems:\n  first\n  second")
}

func TestExpandTemplate(t *testing.T) {
	out := ExpandTemplate("endpoint_{ep}", map[string]string{"ep": "3"})

	require.Equal(t, "endpoint_3", out)
}

func TestExpandTemplate_UnknownRefBecomesEmpty(t *testing.T) {
	out := ExpandTemplate("{nope}", map[string]string{})

	require.Empty(t, out)
}
