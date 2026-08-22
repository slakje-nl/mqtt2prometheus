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
	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/slakje-nl/mqtt2prometheus/internal/broker"
	"github.com/stretchr/testify/require"
)

const mainTemplate = `
mqtt:
  broker: BROKER_URL
  client_id: mqtt2prometheus-test
  username: user
  password: pass
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

func startTestBroker(t *testing.T) string {
	t.Helper()

	address := freeAddress(t)
	server := mqtt.New(&mqtt.Options{InlineClient: true})
	require.NoError(t, server.AddHook(new(auth.AllowHook), nil))
	require.NoError(t, server.AddListener(listeners.NewTCP(listeners.Config{ID: "t", Address: address})))

	go func() { _ = server.Serve() }()

	t.Cleanup(func() { _ = server.Close() })

	require.Eventually(t, func() bool {
		conn, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
		if err != nil {
			return false
		}

		return conn.Close() == nil
	}, 5*time.Second, 20*time.Millisecond)

	return "tcp://" + address
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
	reload := make(chan struct{}, 1)
	done := make(chan error, 1)

	go func() { done <- application.Run(ctx, reload) }()

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

	err := New(dir, Build{}, quietLogger()).Run(t.Context(), nil)

	require.ErrorContains(t, err, "at least one rule is required")
}

func TestRun_ReportsAMissingConfigDirectory(t *testing.T) {
	err := New(filepath.Join(t.TempDir(), "absent"), Build{}, quietLogger()).Run(t.Context(), nil)

	require.ErrorContains(t, err, "reading config")
}

func TestReload_SwapsRulesAndDropsRemovedSeries(t *testing.T) {
	brokerURL := startTestBroker(t)
	dir := writeConfigDir(t, brokerURL, freeAddress(t), map[string]string{"zwave.yaml": zwaveSource})

	application := New(dir, Build{}, quietLogger())
	_, sources, err := application.load()
	require.NoError(t, err)

	application.router.Start(t.Context(), sources)

	defer application.router.Stop()

	application.samples.Set("zwave_node_last_active", 0, "", map[string]string{"node": "a"}, 1)
	application.samples.Set("gone_metric", 0, "", nil, 1)

	renamed := strings.Replace(zwaveSource, "zwave_node_last_active", "zwave_renamed", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sources", "zwave.yaml"), []byte(renamed), 0o600))

	application.reload(t.Context())

	require.InDelta(t, 1.0, testutil.ToFloat64(application.self.Reloads), 1e-9)
	require.Zero(t, application.samples.Len())
}

func TestReload_KeepsTheRunningConfigWhenTheNewOneIsBroken(t *testing.T) {
	brokerURL := startTestBroker(t)
	dir := writeConfigDir(t, brokerURL, freeAddress(t), map[string]string{"zwave.yaml": zwaveSource})

	application := New(dir, Build{}, quietLogger())
	_, sources, err := application.load()
	require.NoError(t, err)

	application.router.Start(t.Context(), sources)

	defer application.router.Stop()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "sources", "zwave.yaml"), []byte("name: x\n"), 0o600))
	application.reload(t.Context())

	require.InDelta(t, 1.0, testutil.ToFloat64(application.self.ReloadFailures), 1e-9)
	require.Equal(t, []broker.Subscription{{Filter: "zwave/#", QoS: 1}}, application.router.Subscriptions())
}

func TestWatchReloads_RespondsToASignalAndToCancellation(t *testing.T) {
	brokerURL := startTestBroker(t)
	dir := writeConfigDir(t, brokerURL, freeAddress(t), map[string]string{"zwave.yaml": zwaveSource})

	application := New(dir, Build{}, quietLogger())
	_, sources, err := application.load()
	require.NoError(t, err)

	application.router.Start(t.Context(), sources)

	defer application.router.Stop()

	ctx, cancel := context.WithCancel(t.Context())
	reload := make(chan struct{}, 1)
	finished := make(chan struct{})

	go func() {
		application.watchReloads(ctx, reload)
		close(finished)
	}()

	reload <- struct{}{}

	require.Eventually(t, func() bool {
		return testutil.ToFloat64(application.self.Reloads) == 1
	}, 5*time.Second, 10*time.Millisecond)

	cancel()

	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("watchReloads did not return")
	}
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
	brokerURL := startTestBroker(t)
	dir := writeConfigDir(t, brokerURL, freeAddress(t), map[string]string{"zwave.yaml": zwaveSource})

	application := New(dir, Build{}, quietLogger())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, application.Run(ctx, nil))

	err := application.Run(ctx, nil)
	require.ErrorContains(t, err, "registering metrics")
}
