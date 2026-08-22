package store

import (
	"sort"
	"strings"
	"sync"
)

type Kind int

const (
	Gauge Kind = iota
	Counter
)

type Key struct {
	Name        string
	LabelNames  []string
	LabelValues []string
}

type Sample struct {
	Key   Key
	Kind  Kind
	Help  string
	Value float64
}

type series struct {
	kind   Kind
	help   string
	names  []string
	values []string
	name   string

	last   float64
	offset float64
	seen   bool
}

type Store struct {
	mu     sync.RWMutex
	series map[string]*series
}

func New() *Store {
	return &Store{series: map[string]*series{}}
}

func (s *Store) Set(name string, kind Kind, help string, labels map[string]string, value float64) {
	names, values := flatten(labels)
	id := identity(name, names, values)

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, found := s.series[id]
	if !found {
		existing = &series{kind: kind, help: help, names: names, values: values, name: name}
		s.series[id] = existing
	}

	existing.kind = kind
	existing.help = help

	if kind == Counter {
		existing.observeCounter(value)

		return
	}

	existing.last = value
	existing.seen = true
}

func (c *series) observeCounter(value float64) {
	if c.seen && value < c.last {
		c.offset += c.last
	}

	c.last = value
	c.seen = true
}

func (s *Store) Snapshot() []Sample {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Sample, 0, len(s.series))

	for _, entry := range s.series {
		out = append(out, Sample{
			Key:   Key{Name: entry.name, LabelNames: entry.names, LabelValues: entry.values},
			Kind:  entry.kind,
			Help:  entry.help,
			Value: entry.last + entry.offset,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.Name != out[j].Key.Name {
			return out[i].Key.Name < out[j].Key.Name
		}

		return strings.Join(out[i].Key.LabelValues, "\x00") < strings.Join(out[j].Key.LabelValues, "\x00")
	})

	return out
}

func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.series)
}

func flatten(labels map[string]string) ([]string, []string) {
	names := make([]string, 0, len(labels))
	for name := range labels {
		names = append(names, name)
	}

	sort.Strings(names)

	values := make([]string, 0, len(names))
	for _, name := range names {
		values = append(values, labels[name])
	}

	return names, values
}

func identity(name string, labelNames, labelValues []string) string {
	var b strings.Builder

	b.WriteString(name)

	for i, labelName := range labelNames {
		b.WriteByte(0)
		b.WriteString(labelName)
		b.WriteByte(0)
		b.WriteString(labelValues[i])
	}

	return b.String()
}
