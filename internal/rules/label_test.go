package rules

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/slakje-nl/mqtt2prometheus/internal/config"
)

func jsonLabelRule(labels map[string]config.Label) config.Rule {
	return config.Rule{
		Match:      `^dsmr/json$`,
		MetricName: "dsmr_power_delivered_kw",
		Type:       config.TypeGauge,
		Value:      config.Value{From: config.FromJSON, Path: "PowerDelivered_total"},
		Labels:     labels,
	}
}

func TestApply_TakesLabelValuesFromThePayload(t *testing.T) {
	rule := compile(t, jsonLabelRule(map[string]config.Label{
		"mac_address": {From: config.FromJSON, Path: "mac_address"},
		"phase":       {From: config.FromStatic, Value: "l1"},
	}))

	match, matched, err := rule.Apply("dsmr/json",
		NewPayload([]byte(`{"mac_address":"00_00_00_00_00_00","PowerDelivered_total":"1.723"}`)))

	require.NoError(t, err)
	require.True(t, matched)
	require.Equal(t, map[string]string{"mac_address": "00_00_00_00_00_00", "phase": "l1"}, match.Labels)
	require.InDelta(t, 1.723, match.Value, 0.0001)
}

func TestApply_MapsALabelValueOnTheWayOut(t *testing.T) {
	rule := compile(t, jsonLabelRule(map[string]config.Label{
		"tariff": {From: config.FromJSON, Path: "ElectricityTariff", Map: map[string]string{"0001": "low", "0002": "normal"}},
	}))

	match, _, err := rule.Apply("dsmr/json", NewPayload([]byte(`{"ElectricityTariff":"0002","PowerDelivered_total":"1.723"}`)))
	require.NoError(t, err)
	require.Equal(t, map[string]string{"tariff": "normal"}, match.Labels)

	unmapped, matched, err := rule.Apply("dsmr/json", NewPayload([]byte(`{"ElectricityTariff":"0003","PowerDelivered_total":"1.723"}`)))
	require.NoError(t, err)
	require.True(t, matched)
	require.Nil(t, unmapped.Labels)
}

func TestApply_SkipsWhenALabelPathIsMissing(t *testing.T) {
	rule := compile(t, jsonLabelRule(map[string]config.Label{
		"mac_address": {From: config.FromJSON, Path: "mac_address"},
	}))

	match, matched, err := rule.Apply("dsmr/json", NewPayload([]byte(`{"PowerDelivered_total":"1.723"}`)))

	require.NoError(t, err)
	require.True(t, matched)
	require.Nil(t, match.Labels)
}

func TestApply_ReportsALabelThatIsNotAScalar(t *testing.T) {
	rule := compile(t, jsonLabelRule(map[string]config.Label{
		"gateway": {From: config.FromJSON, Path: "gateway"},
	}))

	_, matched, err := rule.Apply("dsmr/json", NewPayload([]byte(`{"gateway":{"model":"x"},"PowerDelivered_total":"1.723"}`)))

	require.True(t, matched)
	require.ErrorIs(t, err, ErrNotALabel)
	require.ErrorContains(t, err, `label "gateway"`)
}

func TestLabels_ReadsEveryJSONType(t *testing.T) {
	labels := CompileLabels(map[string]config.Label{
		"text":   {From: config.FromJSON, Path: "text"},
		"number": {From: config.FromJSON, Path: "number"},
		"flag":   {From: config.FromJSON, Path: "flag"},
		"nested": {From: config.FromJSON, Path: "outer.inner"},
	})

	resolved, present, err := labels.Resolve(nil, NewPayload([]byte(`{"text":"a","number":7.5,"flag":true,"outer":{"inner":"deep"}}`)))

	require.NoError(t, err)
	require.True(t, present)
	require.Equal(t, map[string]string{"text": "a", "number": "7.5", "flag": "true", "nested": "deep"}, resolved)
}

func TestLabels_ResolvesNothingWhenAPathIsMissing(t *testing.T) {
	labels := CompileLabels(map[string]config.Label{"mac": {From: config.FromJSON, Path: "mac"}})

	resolved, present, err := labels.Resolve(nil, NewPayload([]byte(`{"other":1}`)))

	require.NoError(t, err)
	require.False(t, present)
	require.Nil(t, resolved)
}

func TestLabels_ReportsAPayloadThatIsNotJSON(t *testing.T) {
	labels := CompileLabels(map[string]config.Label{"mac": {From: config.FromJSON, Path: "mac"}})

	_, present, err := labels.Resolve(nil, NewPayload([]byte("not json")))

	require.False(t, present)
	require.ErrorIs(t, err, ErrBadJSON)
}

func TestLabels_ResolvesNothingWhenNoneAreDeclared(t *testing.T) {
	resolved, present, err := CompileLabels(nil).Resolve(nil, NewPayload([]byte("{}")))

	require.NoError(t, err)
	require.True(t, present)
	require.Empty(t, resolved)
}
