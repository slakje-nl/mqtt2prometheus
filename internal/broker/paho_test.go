package broker

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
	mqtt "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/stretchr/testify/require"
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

func startBroker(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

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

	return server2URL(address)
}

func server2URL(address string) string { return "tcp://" + address }

func TestRun_ReceivesAPublishedMessage(t *testing.T) {
	address := startBroker(t)
	observer := &recordingObserver{}

	p := NewPaho(Credentials{URL: address, ClientID: "reader", Username: "u", Password: "p"},
		quietLogger(), observer)

	received := make(chan Message, 1)
	ctx, cancel := context.WithCancel(t.Context())

	done := make(chan error, 1)
	go func() {
		done <- p.Run(ctx, []Subscription{{Filter: "zwave/#", QoS: 1}}, func(m Message) {
			select {
			case received <- m:
			default:
			}
		})
	}()

	require.Eventually(t, observer.everConnected, 5*time.Second, 20*time.Millisecond)

	publish(t, address, "zwave/example_sensor/lastActive", `{"value":1711922310552}`)

	select {
	case message := <-received:
		require.Equal(t, "zwave/example_sensor/lastActive", message.Topic)
		require.JSONEq(t, `{"value":1711922310552}`, string(message.Payload))
	case <-time.After(5 * time.Second):
		t.Fatal("no message received")
	}

	cancel()
	require.NoError(t, <-done)
}

func publish(t *testing.T, address, topic, payload string) {
	t.Helper()

	observer := &recordingObserver{}
	p := NewPaho(Credentials{URL: address, ClientID: "writer", Username: "u", Password: "p"},
		quietLogger(), observer)

	cfg, err := p.clientConfig(t.Context(), nil, func(Message) {})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	mgr, err := autopaho.NewConnection(ctx, cfg)
	require.NoError(t, err)

	defer func() { _ = mgr.Disconnect(context.WithoutCancel(ctx)) }()

	require.NoError(t, mgr.AwaitConnection(ctx))

	_, err = mgr.Publish(ctx, &paho.Publish{Topic: topic, QoS: 1, Payload: []byte(payload)})
	require.NoError(t, err)
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
