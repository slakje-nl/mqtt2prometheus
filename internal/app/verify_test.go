package app

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVerify_ReportsTheResolvedConfiguration(t *testing.T) {
	dir := writeConfigDir(t, "tcp://broker.example:1883", ":9000",
		map[string]string{"zwave.yaml": zwaveSource})

	var out bytes.Buffer
	require.NoError(t, Verify(dir, &out))

	report := out.String()
	require.Contains(t, report, "broker      tcp://broker.example:1883")
	require.Contains(t, report, "client id   mqtt2prometheus-test")
	require.Contains(t, report, "username    (set, redacted)")
	require.Contains(t, report, "password    (set, redacted)")
	require.Contains(t, report, "listen      :9000")
	require.Contains(t, report, "zwave           1 rules  subscribes zwave/#            qos 1")
	require.Contains(t, report, "1 sources, 1 rules, 2 metrics, no problems")
	require.NotContains(t, report, "broker-account")
	require.NotContains(t, report, "s3cr3t-value")
}

func TestVerify_ReportsAProblem(t *testing.T) {
	dir := writeConfigDir(t, "tcp://broker.example:1883", ":9000",
		map[string]string{"broken.yaml": "name: broken\nsubscribe: 'x/#'\nrules: []\n"})

	err := Verify(dir, &bytes.Buffer{})

	require.ErrorContains(t, err, "at least one rule is required")
}

func TestVerify_ReportsAFailedWrite(t *testing.T) {
	dir := writeConfigDir(t, "tcp://broker.example:1883", ":9000",
		map[string]string{"zwave.yaml": zwaveSource})

	err := Verify(dir, failingWriterOnly{})

	require.ErrorContains(t, err, "writing report")
}

type failingWriterOnly struct{}

func (failingWriterOnly) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestRedact(t *testing.T) {
	require.Equal(t, "(empty)", redact(""))
	require.Equal(t, "(set, redacted)", redact("hunter2"))
}

func TestRedactURL(t *testing.T) {
	require.Equal(t, "tcp://broker:1883", redactURL("tcp://broker:1883"))
	require.Equal(t, "tcp://(redacted)@broker:1883", redactURL("tcp://user:pass@broker:1883"))
	require.Equal(t, "(redacted)@broker:1883", redactURL("user:pass@broker:1883"))
}

func TestVerify_CountsRulesAcrossSources(t *testing.T) {
	second := "name: other\nsubscribe: 'other/#'\nrules:\n" +
		"  - match: '^other/(?P<thing>[^/]+)$'\n    metric_name: other_thing\n" +
		"    type: gauge\n    value: {from: raw}\n"

	dir := writeConfigDir(t, "tcp://broker.example:1883", ":9000",
		map[string]string{"zwave.yaml": zwaveSource, "other.yaml": second})

	var out bytes.Buffer
	require.NoError(t, Verify(dir, &out))

	require.Contains(t, out.String(), "2 sources, 2 rules, 3 metrics, no problems")
}

func TestVerify_ReportsAMissingDirectory(t *testing.T) {
	err := Verify(filepath.Join(t.TempDir(), "absent"), &bytes.Buffer{})

	require.ErrorContains(t, err, "reading config")
}
