package app

import (
	"bytes"
	"context"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/slakje-nl/mqtt2prometheus/internal/broker"
	"github.com/stretchr/testify/require"
)

func publishRetained(t *testing.T, brokerURL, topic, payload string) {
	t.Helper()

	serverURL, err := url.Parse(brokerURL)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	mgr, err := autopaho.NewConnection(ctx, autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{serverURL},
		KeepAlive:                     20,
		CleanStartOnInitialConnection: true,
		ClientConfig:                  paho.ClientConfig{ClientID: "retainer-" + topic},
	})
	require.NoError(t, err)

	defer func() { _ = mgr.Disconnect(context.WithoutCancel(ctx)) }()

	require.NoError(t, mgr.AwaitConnection(ctx))

	_, err = mgr.Publish(ctx, &paho.Publish{Topic: topic, QoS: 1, Retain: true, Payload: []byte(payload)})
	require.NoError(t, err)
}

func followUntil(t *testing.T, args []string, contains ...string) (string, string) {
	t.Helper()

	out, errOut := &syncBuffer{}, &syncBuffer{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() {
		done <- Command{Out: out, ErrOut: errOut}.Execute(ctx, args)
	}()

	require.Eventually(t, func() bool {
		seen := out.String()
		for _, want := range contains {
			if !strings.Contains(seen, want) {
				return false
			}
		}

		return true
	}, 30*time.Second, 50*time.Millisecond)

	cancel()
	require.NoError(t, <-done)

	return out.String(), errOut.String()
}

func TestExecute_DiscoverPrintsEachPrefixOnceAsItAppears(t *testing.T) {
	brokerURL := startTestBroker(t)

	publishRetained(t, brokerURL, "dsmr/reading/power_delivered", "0.412")
	publishRetained(t, brokerURL, "dsmr/reading/gas_meter", "1234.567")
	publishRetained(t, brokerURL, "zigbee2mqtt/example device", `{"power":8.2}`)

	t.Setenv(ConfigDirEnv, writeConfigDir(t, brokerURL, ":0", map[string]string{"zwave.yaml": zwaveSource}))

	out, errOut := followUntil(t, []string{"discover", "-for", "0"}, "dsmr\n", "zigbee2mqtt\n")

	require.Equal(t, 1, strings.Count(out, "dsmr\n"))
	require.Equal(t, "waiting for messages\nclosing\n", errOut)

	deeper, _ := followUntil(t, []string{"discover", "-for", "0", "-depth", "2"},
		"dsmr/reading", "zigbee2mqtt/example device")
	require.Equal(t, 1, strings.Count(deeper, "dsmr/reading\n"))

	narrowed, _ := followUntil(t, []string{"discover", "-for", "0", "dsmr"}, "dsmr\n")
	require.NotContains(t, narrowed, "zigbee2mqtt")

	var single, singleErr bytes.Buffer

	err := testCommand(&single, &singleErr).Execute(t.Context(),
		[]string{"discover", "-for", "0", "-count", "1"})

	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(single.String(), "\n"))
}

func TestExecute_DiscoverWritesNothingWhenNobodyPublishes(t *testing.T) {
	brokerURL := startTestBroker(t)
	t.Setenv(ConfigDirEnv, writeConfigDir(t, brokerURL, ":0", map[string]string{"zwave.yaml": zwaveSource}))

	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"discover", "-for", "500ms"})

	require.NoError(t, err)
	require.Empty(t, out.String())
	require.Equal(t, "waiting for messages\nclosing\n", errOut.String())
}

func TestExecute_DiscoverRejectsBadArguments(t *testing.T) {
	var out, errOut bytes.Buffer

	command := testCommand(&out, &errOut)
	usage := &UsageError{}

	err := command.Execute(t.Context(), []string{"discover", "-depth", "0"})
	require.ErrorAs(t, err, &usage)
	require.ErrorContains(t, err, "-depth must be at least 1")

	err = command.Execute(t.Context(), []string{"discover", "-count", "-1"})
	require.ErrorAs(t, err, &usage)
	require.ErrorContains(t, err, "-count cannot be negative")

	err = command.Execute(t.Context(), []string{"discover", "-for", "-5m"})
	require.ErrorAs(t, err, &usage)
	require.ErrorContains(t, err, "-for cannot be negative")

	err = command.Execute(t.Context(), []string{"discover", "-nope"})
	require.ErrorContains(t, err, "not defined")

	t.Setenv(ConfigDirEnv, "")
	err = command.Execute(t.Context(), []string{"discover"})
	require.ErrorContains(t, err, ConfigDirEnv+" is not set")
}

func TestExecute_DiscoverReportsAnUnusableBrokerURL(t *testing.T) {
	t.Setenv(ConfigDirEnv, writeConfigDir(t, "tcp://%zz", ":0", map[string]string{"zwave.yaml": zwaveSource}))

	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"discover", "-for", "1s"})

	require.ErrorContains(t, err, "broker url")
}

func TestExecute_DiscoverReportsAnUnreadableConfig(t *testing.T) {
	t.Setenv(ConfigDirEnv, t.TempDir())

	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"discover"})

	require.Error(t, err)
}

func TestExecute_DiscoverReportsAFailedAnnouncement(t *testing.T) {
	t.Setenv(ConfigDirEnv, writeConfigDir(t, "tcp://broker:1883", ":0", map[string]string{"zwave.yaml": zwaveSource}))

	var out bytes.Buffer

	err := Command{Out: &out, ErrOut: failingWriterOnly{}}.Execute(t.Context(), []string{"discover"})

	require.ErrorContains(t, err, "disk full")
}

func TestDiscovery_KeepsWorkingWhenTheBufferFillsAndAfterClose(t *testing.T) {
	found := newDiscovery(1, 0)

	for i := range lineBuffer + 10 {
		found.observe(broker.Message{Topic: strings.Repeat("a", i+1) + "/leaf"})
	}

	found.close()
	found.close()

	drained := 0
	for range found.lines() {
		drained++
	}

	require.Equal(t, lineBuffer, drained)

	found.observe(broker.Message{Topic: "after/close"})
}

func TestDiscovery_StopsAtThePrefixCount(t *testing.T) {
	found := newDiscovery(1, 2)

	found.observe(broker.Message{Topic: "a/x"})
	found.observe(broker.Message{Topic: "a/y"})
	found.observe(broker.Message{Topic: "b/x"})

	<-found.done()

	found.observe(broker.Message{Topic: "c/x"})
	found.close()

	drained := []string{}
	for line := range found.lines() {
		drained = append(drained, line)
	}

	require.Equal(t, []string{"a", "b"}, drained)
}

func TestDrainLines_ReportsAWriteFailure(t *testing.T) {
	lines := make(chan string, 1)
	lines <- "dsmr"
	close(lines)

	err := drainLines(failingWriterOnly{}, lines)

	require.ErrorContains(t, err, "disk full")
}

func TestPrefixOf(t *testing.T) {
	require.Equal(t, "dsmr/reading", prefixOf("dsmr/reading/power", 2))
	require.Equal(t, "dsmr", prefixOf("dsmr/reading/power", 1))
	require.Equal(t, "dsmr/reading", prefixOf("dsmr/reading", 2))
	require.Equal(t, "example device", prefixOf("example device", 2))
	require.Equal(t, "$SYS/broker", prefixOf("$SYS/broker/uptime", 2))
}

func TestTopicFilter(t *testing.T) {
	require.Equal(t, "#", topicFilter(""))
	require.Equal(t, "dsmr/#", topicFilter("dsmr"))
	require.Equal(t, "dsmr/#", topicFilter("dsmr/"))
}

var _ io.Writer = failingWriterOnly{}
