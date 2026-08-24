package app

import (
	"strings"
	"sync"

	"github.com/slakje-nl/mqtt2prometheus/internal/broker"
)

type discovery struct {
	depth int
	out   *emitter

	mu   sync.Mutex
	seen map[string]struct{}
}

func newDiscovery(depth, limit int) *discovery {
	return &discovery{
		depth: depth,
		out:   newEmitter(limit),
		seen:  map[string]struct{}{},
	}
}

func (d *discovery) observe(msg broker.Message) {
	prefix := prefixOf(msg.Topic, d.depth)

	if d.known(prefix) {
		return
	}

	d.out.emit(prefix)
}

func (d *discovery) known(prefix string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()

	if _, seen := d.seen[prefix]; seen {
		return true
	}

	d.seen[prefix] = struct{}{}

	return false
}

func (d *discovery) close() {
	d.out.close()
}

func (d *discovery) lines() <-chan string {
	return d.out.lines()
}

func (d *discovery) done() <-chan struct{} {
	return d.out.done()
}

func (d *discovery) skipRetained() bool {
	return false
}

func prefixOf(topic string, depth int) string {
	segments := strings.Split(topic, "/")
	if len(segments) <= depth {
		return topic
	}

	return strings.Join(segments[:depth], "/")
}
