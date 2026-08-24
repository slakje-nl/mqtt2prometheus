package app

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/slakje-nl/mqtt2prometheus/internal/broker"
	"github.com/stretchr/testify/require"
)

func newPublisher(t *testing.T, brokerURL string) func(topic, payload string) {
	t.Helper()

	serverURL, err := url.Parse(brokerURL)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	mgr, err := autopaho.NewConnection(ctx, autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{serverURL},
		KeepAlive:                     20,
		CleanStartOnInitialConnection: true,
		ClientConfig:                  paho.ClientConfig{ClientID: "live-publisher"},
	})
	require.NoError(t, err)
	require.NoError(t, mgr.AwaitConnection(ctx))

	t.Cleanup(func() { _ = mgr.Disconnect(context.WithoutCancel(ctx)) })

	return func(topic, payload string) {
		_, err := mgr.Publish(ctx, &paho.Publish{Topic: topic, QoS: 1, Payload: []byte(payload)})
		require.NoError(t, err)
	}
}

func captureWhilePublishing(t *testing.T, args []string, publish func(), until func(string) bool) (string, string) {
	t.Helper()

	out, errOut := &syncBuffer{}, &syncBuffer{}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() {
		done <- Command{Out: out, ErrOut: errOut}.Execute(ctx, args)
	}()

	require.Eventually(t, func() bool {
		publish()

		return until(out.String())
	}, 30*time.Second, 200*time.Millisecond)

	cancel()
	require.NoError(t, <-done)

	return out.String(), errOut.String()
}

func TestExecute_CapturePrintsOneLinePerMessage(t *testing.T) {
	brokerURL := startTestBroker(t)
	publish := newPublisher(t, brokerURL)

	t.Setenv(ConfigDirEnv, writeConfigDir(t, brokerURL, ":0", map[string]string{"zwave.yaml": zwaveSource}))

	out, errOut := captureWhilePublishing(t, []string{"capture", "--for", "0", "dsmr/#"},
		func() {
			publish("dsmr/reading/power_delivered", "0.412")
			publish("dsmr/consumption", `{"power":412,"tariff":"0002"}`)
			publish("zigbee2mqtt/example device", "offline")
		},
		func(seen string) bool {
			return strings.Contains(seen, "dsmr/reading/power_delivered\t0.412\n") &&
				strings.Contains(seen, "dsmr/consumption\t{\"power\":412,\"tariff\":\"0002\"}\n")
		})

	require.NotContains(t, out, "zigbee2mqtt")
	require.Equal(t, "waiting for messages\nclosing\n", errOut)

	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		require.Len(t, strings.SplitN(line, "\t", 2), 2)
	}
}

func TestExecute_CaptureIgnoresRetainedMessages(t *testing.T) {
	brokerURL := startTestBroker(t)

	publishRetained(t, brokerURL, "dsmr/stale", "from before you started")

	publish := newPublisher(t, brokerURL)

	t.Setenv(ConfigDirEnv, writeConfigDir(t, brokerURL, ":0", map[string]string{"zwave.yaml": zwaveSource}))

	out, _ := captureWhilePublishing(t, []string{"capture", "--for", "0", "dsmr/#"},
		func() { publish("dsmr/fresh", "published while watching") },
		func(seen string) bool { return strings.Contains(seen, "dsmr/fresh") })

	require.NotContains(t, out, "dsmr/stale")
	require.NotContains(t, out, "from before you started")
}

func TestExecute_CaptureStopsAtTheMessageCount(t *testing.T) {
	brokerURL := startTestBroker(t)
	publish := newPublisher(t, brokerURL)

	t.Setenv(ConfigDirEnv, writeConfigDir(t, brokerURL, ":0", map[string]string{"zwave.yaml": zwaveSource}))

	out, errOut := captureWhilePublishing(t,
		[]string{"capture", "--for", "0", "--count", "1", "dsmr/#"},
		func() { publish("dsmr/a", "1") },
		func(seen string) bool { return strings.Count(seen, "\n") == 1 })

	require.Equal(t, 1, strings.Count(out, "\n"))
	require.Equal(t, "waiting for messages\nclosing\n", errOut)
}

func TestExecute_CaptureWritesNothingWhenNobodyPublishes(t *testing.T) {
	brokerURL := startTestBroker(t)
	t.Setenv(ConfigDirEnv, writeConfigDir(t, brokerURL, ":0", map[string]string{"zwave.yaml": zwaveSource}))

	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(),
		[]string{"capture", "--for", "500ms", "dsmr/#"})

	require.NoError(t, err)
	require.Empty(t, out.String())
}

func TestExecute_CaptureRejectsBadArguments(t *testing.T) {
	var out, errOut bytes.Buffer

	command := testCommand(&out, &errOut)
	usage := &UsageError{}

	err := command.Execute(t.Context(), []string{"capture"})
	require.ErrorAs(t, err, &usage)
	require.ErrorContains(t, err, "a topic filter is required")

	err = command.Execute(t.Context(), []string{"capture", "--count", "-1", "dsmr/#"})
	require.ErrorAs(t, err, &usage)
	require.ErrorContains(t, err, "--count cannot be negative")

	err = command.Execute(t.Context(), []string{"capture", "--nope"})
	require.ErrorContains(t, err, "not defined")

	t.Setenv(ConfigDirEnv, "")
	err = command.Execute(t.Context(), []string{"capture", "dsmr/#"})
	require.ErrorContains(t, err, ConfigDirEnv+" is not set")
}

func TestExecute_CaptureReportsAnUnusableBrokerURL(t *testing.T) {
	t.Setenv(ConfigDirEnv, writeConfigDir(t, "tcp://%zz", ":0", map[string]string{"zwave.yaml": zwaveSource}))

	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"capture", "--for", "1s", "dsmr/#"})

	require.ErrorContains(t, err, "broker url")
}

func TestExecute_CaptureReportsAnUnreadableConfig(t *testing.T) {
	t.Setenv(ConfigDirEnv, t.TempDir())

	var out, errOut bytes.Buffer

	err := testCommand(&out, &errOut).Execute(t.Context(), []string{"capture", "dsmr/#"})

	require.Error(t, err)
}

func TestCapturer_AsksTheBrokerToWithholdRetainedMessages(t *testing.T) {
	require.True(t, newCapturer(0).skipRetained())
	require.False(t, newDiscovery(1, 0).skipRetained())
}

func TestCaptureLine_KeepsOneMessageOnOneLine(t *testing.T) {
	require.Equal(t, "dsmr/a\t0.412", captureLine("dsmr/a", []byte("0.412")))
	require.Equal(t, "example device\t{\"a\":1}", captureLine("example device", []byte(`{"a":1}`)))
	require.Equal(t, "t\t"+`\nb\rc\td`, captureLine("t", []byte("\nb\rc\td")))
	require.NotContains(t, captureLine("t", []byte("a\nb")), "\n")
}

func TestCapturer_StopsAtTheMessageCountAndAfterClose(t *testing.T) {
	taken := newCapturer(2)

	taken.observe(broker.Message{Topic: "a", Payload: []byte("1")})
	taken.observe(broker.Message{Topic: "a", Payload: []byte("2")})

	<-taken.done()

	taken.observe(broker.Message{Topic: "a", Payload: []byte("3")})
	taken.close()
	taken.close()
	taken.observe(broker.Message{Topic: "a", Payload: []byte("4")})

	drained := []string{}
	for line := range taken.lines() {
		drained = append(drained, line)
	}

	require.Equal(t, []string{"a\t1", "a\t2"}, drained)
}

func TestCapturer_DropsWhenTheBufferIsFull(t *testing.T) {
	taken := newCapturer(0)

	for i := range lineBuffer + 5 {
		taken.observe(broker.Message{Topic: "a", Payload: []byte(strings.Repeat("x", i%3+1))})
	}

	taken.close()

	drained := 0
	for range taken.lines() {
		drained++
	}

	require.Equal(t, lineBuffer, drained)
}
