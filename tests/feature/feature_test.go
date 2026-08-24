//go:build feature

package feature

import (
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"net/url"

	"gopkg.in/yaml.v3"
)

type corpusCase struct {
	Topic           string   `yaml:"topic"`
	Payload         string   `yaml:"payload"`
	Expect          []string `yaml:"expect"`
	Reject          []string `yaml:"reject"`
	BrokerGenerated bool     `yaml:"broker_generated"`
}

const mosquittoConf = `listener 1883
allow_anonymous true
sys_interval 1
`

func repoRoot(t *testing.T) string {
	t.Helper()

	root, err := filepath.Abs(filepath.Join("..", ".."))
	require.NoError(t, err)

	return root
}

func startMosquitto(t *testing.T) string {
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

func freePort(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	return address
}

func startExporter(t *testing.T, brokerURL, listen string) {
	t.Helper()

	root := repoRoot(t)
	binary := filepath.Join(t.TempDir(), "mqtt2prometheus")

	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/mqtt2prometheus")
	build.Dir = root
	output, err := build.CombinedOutput()
	require.NoError(t, err, string(output))

	config := filepath.Join(t.TempDir(), "config")
	copyTree(t, filepath.Join(root, "config"), config)
	rewriteListen(t, filepath.Join(config, "mqtt2prometheus.yaml"), listen)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	cmd := exec.CommandContext(ctx, binary, "run")
	cmd.Env = append(os.Environ(),
		"MQTT2PROMETHEUS_CONFIG_DIR="+config,
		"MQTT2PROMETHEUS_LOG_LEVEL=warn",
		"MQTT_BROKER="+brokerURL,
		"MQTT_USERNAME=feature",
		"MQTT_PASSWORD=feature",
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr

	require.NoError(t, cmd.Start())

	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})

	require.Eventually(t, func() bool {
		status, _, err := probe(t, "http://"+listen+"/healthz")

		return err == nil && status == http.StatusOK
	}, 30*time.Second, 100*time.Millisecond)
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(to, 0o750))

	entries, err := os.ReadDir(from)
	require.NoError(t, err)

	for _, entry := range entries {
		source := filepath.Join(from, entry.Name())
		target := filepath.Join(to, entry.Name())

		if entry.IsDir() {
			copyTree(t, source, target)

			continue
		}

		body, err := os.ReadFile(source)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(target, body, 0o600))
	}
}

func rewriteListen(t *testing.T, path, listen string) {
	t.Helper()

	body, err := os.ReadFile(path)
	require.NoError(t, err)

	updated := strings.Replace(string(body), `listen: ":9000"`, `listen: "`+listen+`"`, 1)
	require.NoError(t, os.WriteFile(path, []byte(updated), 0o600))
}

func probe(t *testing.T, url string) (int, string, error) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		return 0, "", err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", err
	}

	return resp.StatusCode, string(body), nil
}

func connect(t *testing.T, brokerURL, clientID string) *autopaho.ConnectionManager {
	t.Helper()

	parsed, err := url.Parse(brokerURL)
	require.NoError(t, err)

	serverURL := []*url.URL{parsed}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)

	mgr, err := autopaho.NewConnection(ctx, autopaho.ClientConfig{
		ServerUrls:                    serverURL,
		KeepAlive:                     20,
		CleanStartOnInitialConnection: true,
		ClientConfig:                  paho.ClientConfig{ClientID: clientID},
	})
	require.NoError(t, err)
	require.NoError(t, mgr.AwaitConnection(ctx))

	t.Cleanup(func() { _ = mgr.Disconnect(context.WithoutCancel(ctx)) })

	return mgr
}

func loadCorpus(t *testing.T) []corpusCase {
	t.Helper()

	body, err := os.ReadFile(filepath.Join(repoRoot(t), "testdata", "corpus.yaml"))
	require.NoError(t, err)

	var cases []corpusCase
	require.NoError(t, yaml.Unmarshal(body, &cases))
	require.NotEmpty(t, cases)

	return cases
}

func TestEveryCapturedPayloadProducesItsMetrics(t *testing.T) {
	brokerURL := startMosquitto(t)
	listen := freePort(t)
	startExporter(t, brokerURL, listen)

	publisher := connect(t, brokerURL, "corpus-publisher")
	cases := loadCorpus(t)

	// publish every captured payload the three old exporters were tested against
	for _, c := range cases {
		if c.BrokerGenerated {
			continue
		}

		_, err := publisher.Publish(t.Context(), &paho.Publish{
			Topic: c.Topic, QoS: 1, Payload: []byte(c.Payload),
		})
		require.NoError(t, err, c.Topic)
	}

	var wanted []string
	for _, c := range cases {
		wanted = append(wanted, c.Expect...)
	}

	require.Eventually(t, func() bool {
		_, body, err := probe(t, "http://"+listen+"/metrics")
		if err != nil {
			return false
		}

		for _, line := range wanted {
			if !strings.Contains(body, line) {
				return false
			}
		}

		return true
	}, 30*time.Second, 250*time.Millisecond, "not every expected metric appeared")

	_, body, err := probe(t, "http://"+listen+"/metrics")
	require.NoError(t, err)

	// confirm the topics no rule covers produced nothing
	for _, c := range cases {
		for _, unwanted := range c.Reject {
			require.NotContains(t, body, unwanted, c.Topic)
		}
	}

	require.Contains(t, body, "zwave_last_update")
	require.Contains(t, body, "zigbee_last_update")
	require.Contains(t, body, "mosquitto_last_update")
	require.Contains(t, body, `zigbee_power_meter_last_update{device="example device"}`)
	require.Contains(t, body, "mqtt2prom_mqtt_connected 1")
}

func TestCounterResetCarriesAnOffset(t *testing.T) {
	brokerURL := startMosquitto(t)
	listen := freePort(t)
	startExporter(t, brokerURL, listen)

	publisher := connect(t, brokerURL, "counter-publisher")

	publish := func(value string) {
		_, err := publisher.Publish(t.Context(), &paho.Publish{
			Topic:   "zigbee2mqtt/counter device",
			QoS:     1,
			Payload: []byte(`{"energy":` + value + `}`),
		})
		require.NoError(t, err)
	}

	// a counter that goes backwards is a device reset, not a decrease
	publish("100")
	requireMetric(t, listen, `zigbee_power_meter_energy_total{device="counter device"} 100`)

	publish("5")
	requireMetric(t, listen, `zigbee_power_meter_energy_total{device="counter device"} 105`)
}

func requireMetric(t *testing.T, listen, line string) {
	t.Helper()

	require.Eventually(t, func() bool {
		_, body, err := probe(t, "http://"+listen+"/metrics")

		return err == nil && strings.Contains(body, line)
	}, 30*time.Second, 200*time.Millisecond, line)
}

func TestReadinessReflectsTheBroker(t *testing.T) {
	brokerURL := startMosquitto(t)
	listen := freePort(t)
	startExporter(t, brokerURL, listen)

	// $SYS traffic alone is enough to make the exporter ready
	require.Eventually(t, func() bool {
		status, _, err := probe(t, "http://"+listen+"/readyz")

		return err == nil && status == http.StatusOK
	}, 30*time.Second, 200*time.Millisecond)
}
