package app

import (
	"strings"

	"github.com/slakje-nl/mqtt2prometheus/internal/broker"
)

var payloadEscaper = strings.NewReplacer("\n", `\n`, "\r", `\r`, "\t", `\t`)

type capturer struct {
	out *emitter
}

func newCapturer(limit int) *capturer {
	return &capturer{out: newEmitter(limit)}
}

func (c *capturer) observe(msg broker.Message) {
	c.out.emit(captureLine(msg.Topic, msg.Payload))
}

func (c *capturer) close() {
	c.out.close()
}

func (c *capturer) lines() <-chan string {
	return c.out.lines()
}

func (c *capturer) done() <-chan struct{} {
	return c.out.done()
}

func (c *capturer) skipRetained() bool {
	return true
}

func captureLine(topic string, payload []byte) string {
	return topic + "\t" + payloadEscaper.Replace(string(payload))
}
