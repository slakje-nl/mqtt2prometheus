package app

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/slakje-nl/mqtt2prometheus/internal/broker"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const mainTemplate = `
mqtt:
  broker: BROKER_URL
  client_id: mqtt2prometheus-test
  username: broker-account
  password: s3cr3t-value
  qos: 1
  clean_session: true

server:
  listen: LISTEN_ADDRESS

log:
  level: error

sources: sources/*.yaml
`

const zwaveSource = `
name: zwave
subscribe: 'zwave/#'
last_updated_metric: zwave_last_update

rules:
  - match: '^zwave/(?P<node>[^/]+)/lastActive$'
    metric_name: zwave_node_last_active
    type: gauge
    value: {from: json, path: value, scale: 0.001}
`

const mosquittoConf = "listener 1883\nallow_anonymous true\n"

func startTestBroker(t *testing.T) string {
	t.Helper()

	conf := filepath.Join(t.TempDir(), "mosquitto.conf")
	require.NoError(t, os.WriteFile(conf, []byte(mosquittoConf), 0o644))

	container, err := testcontainers.GenericContainer(t.Context(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "eclipse-mosquitto:2",
			ExposedPorts: []string{"1883/tcp"},
			Files: []testcontainers.ContainerFile{{
				HostFilePath:      conf,
				ContainerFilePath: "/mosquitto/config/mosquitto.conf",
				FileMode:          0o644,
			}},
			WaitingFor: wait.ForListeningPort("1883/tcp").WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)

	t.Cleanup(func() { _ = container.Terminate(context.WithoutCancel(t.Context())) })

	host, err := container.Host(t.Context())
	require.NoError(t, err)

	port, err := container.MappedPort(t.Context(), "1883/tcp")
	require.NoError(t, err)

	return "tcp://" + net.JoinHostPort(host, port.Port())
}

func writeConfigDir(t *testing.T, brokerURL, listen string, sources map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	main := strings.NewReplacer("BROKER_URL", brokerURL, "LISTEN_ADDRESS", listen).Replace(mainTemplate)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "mqtt2prometheus.yaml"), []byte(main), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sources"), 0o750))

	for name, body := range sources {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "sources", name), []byte(body), 0o600))
	}

	return dir
}

func publishTo(t *testing.T, brokerURL, topic, payload string) {
	t.Helper()

	serverURL, err := url.Parse(brokerURL)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)

	defer cancel()

	mgr, err := autopaho.NewConnection(ctx, autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{serverURL},
		KeepAlive:                     20,
		CleanStartOnInitialConnection: true,
		ClientConfig:                  paho.ClientConfig{ClientID: "publisher"},
	})
	require.NoError(t, err)

	defer func() { _ = mgr.Disconnect(context.WithoutCancel(ctx)) }()

	require.NoError(t, mgr.AwaitConnection(ctx))

	_, err = mgr.Publish(ctx, &paho.Publish{Topic: topic, QoS: 1, Payload: []byte(payload)})
	require.NoError(t, err)
}

func TestRun_EndToEnd(t *testing.T) {
	brokerURL := startTestBroker(t)
	listen := freeAddress(t)
	dir := writeConfigDir(t, brokerURL, listen, map[string]string{"zwave.yaml": zwaveSource})

	application := New(dir, Build{Version: "test", Commit: "abc"}, quietLogger())

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- application.Run(ctx) }()

	require.Eventually(t, func() bool {
		_, _, err := tryGet(t, "http://"+listen+"/healthz")

		return err == nil
	}, 10*time.Second, 20*time.Millisecond)

	publishTo(t, brokerURL, "zwave/example_sensor/lastActive", `{"time":1711922310802,"value":1711922310552}`)

	require.Eventually(t, func() bool {
		_, body, err := tryGet(t, "http://"+listen+"/metrics")

		return err == nil && strings.Contains(body, `zwave_node_last_active{node="example_sensor"} 1.711922310552e+09`)
	}, 15*time.Second, 50*time.Millisecond)

	_, body := get(t, "http://"+listen+"/metrics")
	require.Contains(t, body, "zwave_last_update")
	require.Contains(t, body, `mqtt2prom_build_info{commit="abc",go_version=`)
	require.Contains(t, body, "mqtt2prom_mqtt_connected 1")

	status, _ := get(t, "http://"+listen+"/readyz")
	require.Equal(t, http.StatusOK, status)

	cancel()
	require.NoError(t, <-done)
}

func TestRun_RejectsABrokenConfig(t *testing.T) {
	dir := writeConfigDir(t, "tcp://127.0.0.1:1", ":0", map[string]string{
		"broken.yaml": "name: broken\nsubscribe: 'x/#'\nrules: []\n",
	})

	err := New(dir, Build{}, quietLogger()).Run(t.Context())

	require.ErrorContains(t, err, "at least one rule is required")
}

func TestRun_ReportsAMissingConfigDirectory(t *testing.T) {
	err := New(filepath.Join(t.TempDir(), "absent"), Build{}, quietLogger()).Run(t.Context())

	require.ErrorContains(t, err, "reading config")
}

func TestObserverCallbacks(t *testing.T) {
	application := New(t.TempDir(), Build{}, quietLogger())

	application.Connected(true)
	require.True(t, application.connected.Load())
	require.False(t, application.ready())

	application.dispatch(broker.Message{Topic: "zwave/x"})
	require.True(t, application.ready())

	application.Reconnected()
	require.InDelta(t, 1.0, testutil.ToFloat64(application.self.Reconnects), 1e-9)

	application.Connected(false)
	require.False(t, application.ready())
}

func TestNewLogger_ParsesEveryLevel(t *testing.T) {
	require.Equal(t, slog.LevelDebug, parseLevel("debug"))
	require.Equal(t, slog.LevelWarn, parseLevel("warn"))
	require.Equal(t, slog.LevelError, parseLevel("error"))
	require.Equal(t, slog.LevelInfo, parseLevel("info"))
	require.Equal(t, slog.LevelInfo, parseLevel("nonsense"))
	require.True(t, NewLogger("debug").Enabled(t.Context(), slog.LevelDebug))
}

func TestRun_ReportsDuplicateMetricRegistration(t *testing.T) {
	dir := writeConfigDir(t, "tcp://127.0.0.1:1", freeAddress(t), map[string]string{"zwave.yaml": zwaveSource})

	application := New(dir, Build{}, quietLogger())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, application.Run(ctx))

	err := application.Run(ctx)
	require.ErrorContains(t, err, "registering metrics")
}
