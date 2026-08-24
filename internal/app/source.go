package app

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/slakje-nl/mqtt2prometheus/internal/broker"
	"github.com/slakje-nl/mqtt2prometheus/internal/config"
	"github.com/slakje-nl/mqtt2prometheus/internal/rules"
	"github.com/slakje-nl/mqtt2prometheus/internal/store"
)

type source struct {
	name              string
	subscribe         string
	qos               uint8
	brokerURL         string
	lastUpdatedMetric string
	labels            rules.Labels
	rules             []*rules.Rule
	prefix            string
}

func compileSources(cfg *config.Config) ([]*source, error) {
	out := make([]*source, 0, len(cfg.SourceList))

	for _, declared := range cfg.SourceList {
		compiled := &source{
			name:              declared.Name,
			subscribe:         declared.Subscribe,
			qos:               declared.EffectiveQoS(*cfg.MQTT.QoS),
			brokerURL:         declared.EffectiveBroker(cfg.MQTT.Broker),
			lastUpdatedMetric: declared.LastUpdatedMetric,
			labels:            rules.CompileLabels(declared.Labels),
			prefix:            topicPrefix(declared.Subscribe),
		}

		for i, rule := range declared.Rules {
			compiledRule, err := rules.Compile(declared, rule)
			if err != nil {
				return nil, fmt.Errorf("%s: rule %d: %w", declared.Path, i, err)
			}

			compiled.rules = append(compiled.rules, compiledRule)
		}

		out = append(out, compiled)
	}

	return out, nil
}

func topicPrefix(filter string) string {
	for i, r := range filter {
		if r == '+' || r == '#' {
			return filter[:i]
		}
	}

	return filter
}

func (s *source) owns(topic string) bool {
	return len(topic) >= len(s.prefix) && topic[:len(s.prefix)] == s.prefix
}

func (s *source) apply(samples *store.Store, now time.Time, msg broker.Message) ([]error, bool) {
	var (
		problems []error
		matched  bool
	)

	payload := rules.NewPayload(msg.Payload)

	for _, rule := range s.rules {
		result, hit, err := rule.Apply(msg.Topic, payload)
		if !hit {
			continue
		}

		matched = true

		if err != nil {
			problems = append(problems, err)

			continue
		}

		if result.Labels == nil {
			continue
		}

		samples.Set(rule.MetricName, kindOf(rule), rule.Help, result.Labels, result.Value)

		if rule.LastUpdatedMetric != "" {
			samples.Set(rule.LastUpdatedMetric, store.Gauge, config.HeartbeatHelp, result.Labels, float64(now.UnixNano())/1e9)
		}
	}

	if matched && s.lastUpdatedMetric != "" {
		labels, present, err := s.labels.Resolve(nil, payload)

		switch {
		case err != nil:
			problems = append(problems, err)
		case present:
			samples.Set(s.lastUpdatedMetric, store.Gauge, config.HeartbeatHelp, labels, float64(now.UnixNano())/1e9)
		}
	}

	return problems, matched
}

func kindOf(rule *rules.Rule) store.Kind {
	if rule.Counter {
		return store.Counter
	}

	return store.Gauge
}

func (s *source) metricNames() []string {
	names := make([]string, 0, len(s.rules)+1)

	for _, rule := range s.rules {
		names = append(names, rule.MetricName)

		if rule.LastUpdatedMetric != "" {
			names = append(names, rule.LastUpdatedMetric)
		}
	}

	if s.lastUpdatedMetric != "" {
		names = append(names, s.lastUpdatedMetric)
	}

	return names
}

func reasonOf(err error) string {
	switch {
	case errors.Is(err, rules.ErrBadJSON):
		return "json"
	case errors.Is(err, rules.ErrRegexNoMatch):
		return "regex"
	case errors.Is(err, rules.ErrNotNumeric):
		return "not_numeric"
	default:
		return "other"
	}
}

func logProblems(log *slog.Logger, name string, msg broker.Message, problems []error) {
	for _, problem := range problems {
		log.Debug("rule matched but produced no value",
			"source", name, "topic", msg.Topic, "reason", reasonOf(problem), "error", problem)
	}
}
