package broker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

type recordingObserver struct {
	mu         sync.Mutex
	up         []bool
	reconnects int
}

func (o *recordingObserver) Connected(state bool) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.up = append(o.up, state)
}

func (o *recordingObserver) Reconnected() {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.reconnects++
}

func (o *recordingObserver) everConnected() bool {
	o.mu.Lock()
	defer o.mu.Unlock()

	for _, state := range o.up {
		if state {
			return true
		}
	}

	return false
}

type stubSubscriber struct {
	ack  *paho.Suback
	err  error
	sent *paho.Subscribe
}

func (s *stubSubscriber) Subscribe(_ context.Context, sub *paho.Subscribe) (*paho.Suback, error) {
	s.sent = sub

	return s.ack, s.err
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestRun_RejectsAnUnparseableBrokerURL(t *testing.T) {
	p := NewPaho(Credentials{URL: "://nope"}, quietLogger(), &recordingObserver{})

	err := p.Run(t.Context(), nil, func(Message) {})

	require.ErrorContains(t, err, "broker url")
}

func TestRun_ReturnsWhenTheContextIsCancelled(t *testing.T) {
	observer := &recordingObserver{}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	p := NewPaho(Credentials{URL: "tcp://127.0.0.1:1", ClientID: "c"}, quietLogger(), observer)
	err := p.Run(ctx, []Subscription{{Filter: "zwave/#", QoS: 1}}, func(Message) {})

	require.NoError(t, err)
	require.Contains(t, observer.up, false)
}

func TestOnConnectionUp_SubscribeFailureIsLoggedNotFatal(t *testing.T) {
	observer := &recordingObserver{}
	p := NewPaho(Credentials{URL: "tcp://127.0.0.1:1"}, quietLogger(), observer)

	p.onConnectionUp(t.Context(), &stubSubscriber{err: errors.New("refused")},
		[]Subscription{{Filter: "zwave/#"}})

	require.Equal(t, []bool{true}, observer.up)
}

func TestOnConnectionUp_ReportsRefusedAndGrantedFilters(t *testing.T) {
	p := NewPaho(Credentials{URL: "tcp://127.0.0.1:1"}, quietLogger(), &recordingObserver{})
	sub := &stubSubscriber{ack: &paho.Suback{Reasons: []byte{0x87, 1, 0}}}

	p.onConnectionUp(t.Context(), sub, []Subscription{{Filter: "$SYS/#", QoS: 2}, {Filter: "zwave/#", QoS: 1}})

	require.Equal(t, "$SYS/#", sub.sent.Subscriptions[0].Topic)
	require.Equal(t, byte(2), sub.sent.Subscriptions[0].QoS)
}

func TestOnConnectionUp_SecondConnectionCountsAsAReconnect(t *testing.T) {
	observer := &recordingObserver{}
	p := NewPaho(Credentials{URL: "tcp://127.0.0.1:1"}, quietLogger(), observer)
	sub := &stubSubscriber{ack: &paho.Suback{}}

	subs := []Subscription{{Filter: "zwave/#"}}
	p.onConnectionUp(t.Context(), sub, subs)
	p.onConnectionUp(t.Context(), sub, subs)

	require.Equal(t, 1, observer.reconnects)
}

func TestOnConnectionUp_WithNoSubscriptionsDoesNotSubscribe(t *testing.T) {
	p := NewPaho(Credentials{URL: "tcp://127.0.0.1:1"}, quietLogger(), &recordingObserver{})
	sub := &stubSubscriber{ack: &paho.Suback{}}

	p.onConnectionUp(t.Context(), sub, nil)

	require.Nil(t, sub.sent)
}

func TestClientConfig_CarriesCredentialsAndErrorCallbacks(t *testing.T) {
	observer := &recordingObserver{}
	p := NewPaho(Credentials{
		URL: "tcp://broker.invalid:1883", ClientID: "id", Username: "user", Password: "pass",
	}, quietLogger(), observer)

	cfg, err := p.clientConfig(t.Context(), nil, func(Message) {})
	require.NoError(t, err)

	require.Equal(t, "id", cfg.ClientID)
	require.Equal(t, "user", cfg.ConnectUsername)
	require.Equal(t, []byte("pass"), cfg.ConnectPassword)
	require.Equal(t, "broker.invalid:1883", cfg.ServerUrls[0].Host)

	cfg.OnConnectError(errors.New("no route"))
	cfg.OnClientError(errors.New("broken pipe"))

	require.Equal(t, []bool{false, false}, observer.up)
}

func TestClientConfig_PublishHandlerForwardsTheMessage(t *testing.T) {
	p := NewPaho(Credentials{URL: "tcp://127.0.0.1:1"}, quietLogger(), &recordingObserver{})

	received := make(chan Message, 1)

	cfg, err := p.clientConfig(t.Context(), nil, func(m Message) { received <- m })
	require.NoError(t, err)

	done, err := cfg.OnPublishReceived[0](paho.PublishReceived{
		Packet: &paho.Publish{
			Topic:   "zwave/example_sensor/lastActive",
			Payload: []byte(`{"value":1711922310552}`),
		},
	})

	require.NoError(t, err)
	require.True(t, done)

	message := <-received
	require.Equal(t, "zwave/example_sensor/lastActive", message.Topic)
	require.JSONEq(t, `{"value":1711922310552}`, string(message.Payload))
}

type stubConnection struct {
	disconnectErr error
	disconnected  bool
}

func (c *stubConnection) Disconnect(context.Context) error {
	c.disconnected = true

	return c.disconnectErr
}

func TestRun_ReportsAConnectionThatCannotBeCreated(t *testing.T) {
	p := NewPaho(Credentials{URL: "tcp://127.0.0.1:1"}, quietLogger(), &recordingObserver{})
	p.connect = func(context.Context, autopaho.ClientConfig) (connection, error) {
		return nil, errors.New("no listener")
	}

	err := p.Run(t.Context(), nil, func(Message) {})

	require.ErrorContains(t, err, "mqtt connection: no listener")
}

func TestRun_LogsButToleratesADisconnectFailure(t *testing.T) {
	stub := &stubConnection{disconnectErr: errors.New("already closed")}
	p := NewPaho(Credentials{URL: "tcp://127.0.0.1:1"}, quietLogger(), &recordingObserver{})
	p.connect = func(context.Context, autopaho.ClientConfig) (connection, error) { return stub, nil }

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, p.Run(ctx, nil, func(Message) {}))
	require.True(t, stub.disconnected)
}

func TestRun_IgnoresACancelledDisconnect(t *testing.T) {
	stub := &stubConnection{disconnectErr: context.Canceled}
	p := NewPaho(Credentials{URL: "tcp://127.0.0.1:1"}, quietLogger(), &recordingObserver{})
	p.connect = func(context.Context, autopaho.ClientConfig) (connection, error) { return stub, nil }

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.NoError(t, p.Run(ctx, nil, func(Message) {}))
}

const mosquittoConf = "listener 1883\nallow_anonymous true\n"

func startMosquittoContainer(t *testing.T) string {
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

func TestRun_SubscribesThroughARealBroker(t *testing.T) {
	address := startMosquittoContainer(t)
	observer := &recordingObserver{}

	p := NewPaho(Credentials{URL: address, ClientID: "reader", Username: "u", Password: "p"},
		quietLogger(), observer)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)

	go func() { done <- p.Run(ctx, []Subscription{{Filter: "zwave/#", QoS: 1}}, func(Message) {}) }()

	require.Eventually(t, observer.everConnected, 30*time.Second, 50*time.Millisecond)

	cancel()
	require.NoError(t, <-done)
}

func TestSubscribeOptions_WithholdsRetainedOnlyWhenAsked(t *testing.T) {
	options := subscribeOptions([]Subscription{
		{Filter: "live/#", QoS: 1, SkipRetained: true},
		{Filter: "all/#", QoS: 2},
	})

	require.Len(t, options, 2)
	require.Equal(t, byte(withholdRetained), options[0].RetainHandling)
	require.Equal(t, "live/#", options[0].Topic)
	require.Equal(t, byte(sendRetained), options[1].RetainHandling)
}
