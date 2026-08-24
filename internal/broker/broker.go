package broker

import (
	"context"
)

type Message struct {
	Topic   string
	Payload []byte
}

type Subscription struct {
	Filter       string
	QoS          uint8
	SkipRetained bool
}

type Handler func(Message)

type Broker interface {
	Run(ctx context.Context, subs []Subscription, handle Handler) error
}

type Credentials struct {
	URL          string
	ClientID     string
	Username     string
	Password     string
	CleanSession bool
}

type Observer interface {
	Connected(up bool)
	Reconnected()
}
