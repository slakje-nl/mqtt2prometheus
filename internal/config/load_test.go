package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

const mainYAML = `
mqtt:
  broker: ${TEST_BROKER}
  client_id: mqtt2prometheus
  username: ${TEST_USER}
  password: ${TEST_PASS}
  qos: 1
  clean_session: false

server:
  listen: ":9000"

log:
  level: info

sources: sources/*.yaml
`

const sourceYAML = `
name: zwave
subscribe: 'zwave/#'
last_updated_metric: zwave_last_update

rules:
  - match: '^zwave/(?P<node>[^/]+)/lastActive$'
    metric_name: zwave_node_last_active
    type: gauge
    value: {from: json, path: value, scale: 0.001}
`

func writeConfig(t *testing.T, main string, sources map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, mainFile), []byte(main), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sources"), 0o750))

	for name, body := range sources {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sources", name), []byte(body), 0o600))
	}

	return dir
}

func setEnv(t *testing.T) {
	t.Helper()
	t.Setenv("TEST_BROKER", "tcp://broker:1883")
	t.Setenv("TEST_USER", "user")
	t.Setenv("TEST_PASS", "pass")
}

func TestLoad_ReadsMainAndSources(t *testing.T) {
	setEnv(t)
	dir := writeConfig(t, mainYAML, map[string]string{"zwave.yaml": sourceYAML})

	cfg, err := Load(dir)
	require.NoError(t, err)

	require.Equal(t, "tcp://broker:1883", cfg.MQTT.Broker)
	require.Equal(t, "user", cfg.MQTT.Username)
	require.Equal(t, "pass", cfg.MQTT.Password)
	require.Equal(t, uint8(1), *cfg.MQTT.QoS)
	require.False(t, *cfg.MQTT.CleanSession)
	require.Equal(t, ":9000", cfg.Server.Listen)
	require.Equal(t, "info", cfg.Log.Level)
	require.Len(t, cfg.SourceList, 1)

	src := cfg.SourceList[0]
	require.Equal(t, "zwave", src.Name)
	require.Equal(t, "zwave/#", src.Subscribe)
	require.Equal(t, "zwave_last_update", src.LastUpdatedMetric)
	require.Equal(t, filepath.Join(dir, "sources", "zwave.yaml"), src.Path)
	require.Len(t, src.Rules, 1)

	rule := src.Rules[0]
	require.Equal(t, "zwave_node_last_active", rule.MetricName)
	require.Equal(t, TypeGauge, rule.Type)
	require.Equal(t, FromJSON, rule.Value.From)
	require.Equal(t, "value", rule.Value.Path)
	require.InDelta(t, 0.001, *rule.Value.Scale, 1e-9)
}

func TestLoad_SourcesAreSortedByPath(t *testing.T) {
	setEnv(t)
	dir := writeConfig(t, mainYAML, map[string]string{
		"zwave.yaml":       sourceYAML,
		"mosquitto.yaml":   "name: mosquitto\nsubscribe: '$SYS/#'\nrules: []\n",
		"zigbee2mqtt.yaml": "name: zigbee2mqtt\nsubscribe: 'zigbee2mqtt/#'\nrules: []\n",
	})

	cfg, err := Load(dir)
	require.NoError(t, err)

	names := []string{cfg.SourceList[0].Name, cfg.SourceList[1].Name, cfg.SourceList[2].Name}
	require.Equal(t, []string{"mosquitto", "zigbee2mqtt", "zwave"}, names)
}

func TestLoad_MissingMainFile(t *testing.T) {
	_, err := Load(t.TempDir())
	require.ErrorContains(t, err, "reading config")
}

func TestLoad_MissingSourcesKey(t *testing.T) {
	dir := writeConfig(t, "server:\n  listen: \":9000\"\n", nil)

	_, err := Load(dir)
	require.ErrorContains(t, err, "sources is required")
}

func TestLoad_BadGlobPattern(t *testing.T) {
	dir := writeConfig(t, "sources: 'sources/[.yaml'\n", nil)

	_, err := Load(dir)
	require.ErrorContains(t, err, "syntax error in pattern")
}

func TestLoad_GlobMatchesNothing(t *testing.T) {
	dir := writeConfig(t, "sources: sources/*.yaml\n", nil)

	_, err := Load(dir)
	require.ErrorContains(t, err, "matched no files")
}

func TestLoad_UnreadableSourceFile(t *testing.T) {
	setEnv(t)
	dir := writeConfig(t, mainYAML, nil)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sources", "a.yaml"), 0o750))

	_, err := Load(dir)
	require.ErrorContains(t, err, "reading config")
}

func TestLoad_UnknownFieldIsRejected(t *testing.T) {
	setEnv(t)
	dir := writeConfig(t, mainYAML, map[string]string{
		"zwave.yaml": "name: zwave\nsubscribe: 'zwave/#'\ntypo_field: 1\nrules: []\n",
	})

	_, err := Load(dir)
	require.ErrorContains(t, err, "field typo_field not found")
}

func TestLoad_EmptySourceFile(t *testing.T) {
	setEnv(t)
	dir := writeConfig(t, mainYAML, map[string]string{"zwave.yaml": ""})

	_, err := Load(dir)
	require.ErrorContains(t, err, "file is empty")
}

func TestLoad_UnsetEnvVarsAreReportedOnce(t *testing.T) {
	dir := writeConfig(t, "mqtt:\n  broker: ${NOPE}\n  username: ${NOPE}\n  password: ${ALSO_NOPE}\n", nil)

	_, err := Load(dir)

	var missing *MissingEnvError
	require.ErrorAs(t, err, &missing)
	require.Equal(t, []string{"NOPE", "ALSO_NOPE"}, missing.Names)
	require.Equal(t, mainFile, missing.File)
	require.ErrorContains(t, err, "unset environment variables: NOPE, ALSO_NOPE")
}

func TestSource_EffectiveQoS(t *testing.T) {
	two := uint8(2)

	require.Equal(t, uint8(2), Source{QoS: &two}.EffectiveQoS(1))
	require.Equal(t, uint8(1), Source{}.EffectiveQoS(1))
}

func TestSource_EffectiveBroker(t *testing.T) {
	require.Equal(t, "tcp://other:1883", Source{Broker: "tcp://other:1883"}.EffectiveBroker("tcp://main:1883"))
	require.Equal(t, "tcp://main:1883", Source{}.EffectiveBroker("tcp://main:1883"))
}
