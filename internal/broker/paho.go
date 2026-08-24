package broker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

const (
	keepAliveSeconds      = 20
	sessionExpirySeconds  = 3600
	disconnectGracePeriod = 5 * time.Second
	maxGrantedQoS         = 2
	sendRetained          = 0
	withholdRetained      = 2
)

type subscriber interface {
	Subscribe(ctx context.Context, s *paho.Subscribe) (*paho.Suback, error)
}

type connection interface {
	Disconnect(ctx context.Context) error
}

type connector func(context.Context, autopaho.ClientConfig) (connection, error)

func dial(ctx context.Context, cfg autopaho.ClientConfig) (connection, error) {
	return autopaho.NewConnection(ctx, cfg)
}

type Paho struct {
	creds    Credentials
	log      *slog.Logger
	observer Observer
	connect  connector

	connections atomic.Int64
}

func NewPaho(creds Credentials, log *slog.Logger, observer Observer) *Paho {
	return &Paho{creds: creds, log: log, observer: observer, connect: dial}
}

func (p *Paho) Run(ctx context.Context, subs []Subscription, handle Handler) error {
	cfg, err := p.clientConfig(ctx, subs, handle)
	if err != nil {
		return err
	}

	mgr, err := p.connect(ctx, cfg)
	if err != nil {
		return fmt.Errorf("mqtt connection: %w", err)
	}

	<-ctx.Done()
	p.observer.Connected(false)

	shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), disconnectGracePeriod)
	defer cancel()

	if err := mgr.Disconnect(shutdown); err != nil && !errors.Is(err, context.Canceled) {
		p.log.Warn("mqtt disconnect failed", "error", err)
	}

	return nil
}

func (p *Paho) clientConfig(ctx context.Context, subs []Subscription, handle Handler) (autopaho.ClientConfig, error) {
	serverURL, err := url.Parse(p.creds.URL)
	if err != nil {
		return autopaho.ClientConfig{}, fmt.Errorf("broker url: %w", err)
	}

	return autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{serverURL},
		KeepAlive:                     keepAliveSeconds,
		CleanStartOnInitialConnection: p.creds.CleanSession,
		SessionExpiryInterval:         sessionExpirySeconds,
		ConnectUsername:               p.creds.Username,
		ConnectPassword:               []byte(p.creds.Password),
		OnConnectionUp: func(mgr *autopaho.ConnectionManager, _ *paho.Connack) {
			p.onConnectionUp(ctx, mgr, subs)
		},
		OnConnectError: p.onConnectError,
		ClientConfig: paho.ClientConfig{
			ClientID:          p.creds.ClientID,
			OnPublishReceived: []func(paho.PublishReceived) (bool, error){publishHandler(handle)},
			OnClientError:     p.onClientError,
		},
	}, nil
}

func publishHandler(handle Handler) func(paho.PublishReceived) (bool, error) {
	return func(received paho.PublishReceived) (bool, error) {
		handle(Message{Topic: received.Packet.Topic, Payload: received.Packet.Payload})

		return true, nil
	}
}

func (p *Paho) onConnectionUp(ctx context.Context, sub subscriber, subs []Subscription) {
	p.observer.Connected(true)

	if p.connections.Add(1) > 1 {
		p.observer.Reconnected()
	}

	if len(subs) == 0 {
		return
	}

	ack, err := sub.Subscribe(ctx, &paho.Subscribe{Subscriptions: subscribeOptions(subs)})
	if err != nil {
		p.log.Error("mqtt subscribe failed", "error", err)

		return
	}

	p.reportSubscriptions(subs, ack)
}

func (p *Paho) onConnectError(err error) {
	p.observer.Connected(false)
	p.log.Warn("mqtt connection attempt failed", "error", err)
}

func (p *Paho) onClientError(err error) {
	p.observer.Connected(false)
	p.log.Warn("mqtt client error", "error", err)
}

func subscribeOptions(subs []Subscription) []paho.SubscribeOptions {
	options := make([]paho.SubscribeOptions, 0, len(subs))
	for _, sub := range subs {
		options = append(options, paho.SubscribeOptions{
			Topic:          sub.Filter,
			QoS:            sub.QoS,
			RetainHandling: retainHandling(sub.SkipRetained),
		})
	}

	return options
}

func retainHandling(skip bool) byte {
	if skip {
		return withholdRetained
	}

	return sendRetained
}

func (p *Paho) reportSubscriptions(subs []Subscription, ack *paho.Suback) {
	for i, reason := range ack.Reasons {
		if i >= len(subs) {
			return
		}

		if reason > maxGrantedQoS {
			p.log.Error("mqtt broker refused a subscription",
				"filter", subs[i].Filter, "reason_code", reason)

			continue
		}

		p.log.Info("subscribed", "filter", subs[i].Filter, "granted_qos", reason)
	}
}
