package rules

import (
	"fmt"
	"regexp"

	"github.com/slakje-nl/mqtt2prometheus/internal/config"
)

type Rule struct {
	MetricName        string
	Help              string
	Counter           bool
	LastUpdatedMetric string

	match      *regexp.Regexp
	captures   []string
	labels     Labels
	autoLabels []string
	value      extractor
}

type Match struct {
	Labels map[string]string
	Value  float64
}

func Compile(src config.Source, rule config.Rule) (*Rule, error) {
	match, err := regexp.Compile(rule.Match)
	if err != nil {
		return nil, fmt.Errorf("match: %w", err)
	}

	value, err := newExtractor(rule.Value)
	if err != nil {
		return nil, err
	}

	declared := map[string]config.Label{}
	for name, label := range src.Labels {
		declared[name] = label
	}

	for name, label := range rule.Labels {
		declared[name] = label
	}

	captures := config.CaptureNames(match)

	return &Rule{
		MetricName:        rule.MetricName,
		Help:              rule.Help,
		Counter:           rule.Type == config.TypeCounter,
		LastUpdatedMetric: rule.LastUpdatedMetric,
		match:             match,
		captures:          captures,
		labels:            CompileLabels(declared),
		autoLabels:        config.LabelNames(captures, declared),
		value:             value,
	}, nil
}

func (r *Rule) Apply(topic string, payload []byte) (Match, bool, error) {
	found := r.match.FindStringSubmatch(topic)
	if found == nil {
		return Match{}, false, nil
	}

	captured := make(map[string]string, len(r.captures))

	for i, name := range r.match.SubexpNames() {
		if name != "" {
			captured[name] = found[i]
		}
	}

	value, err := r.value.extract(payload)
	if err != nil {
		return Match{}, true, err
	}

	if value.skip {
		return Match{}, true, nil
	}

	labels, present, err := r.resolveLabels(captured, payload)
	if err != nil {
		return Match{}, true, err
	}

	if !present {
		return Match{}, true, nil
	}

	return Match{Labels: labels, Value: value.number}, true, nil
}

func (r *Rule) resolveLabels(captured map[string]string, payload []byte) (map[string]string, bool, error) {
	labels := make(map[string]string, len(r.autoLabels))

	for _, name := range r.autoLabels {
		extractor, declared := r.labels.extractors[name]
		if !declared {
			labels[name] = captured[name]

			continue
		}

		value, present, err := extractor.resolve(captured, payload)
		if err != nil {
			return nil, false, fmt.Errorf("label %q: %w", name, err)
		}

		if !present {
			return nil, false, nil
		}

		labels[name] = value
	}

	return labels, true, nil
}

func (r *Rule) LabelNames() []string {
	return r.autoLabels
}
