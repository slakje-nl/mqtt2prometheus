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
	labels     map[string]string
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

	labels := map[string]string{}
	for name, template := range src.Labels {
		labels[name] = template
	}

	for name, template := range rule.Labels {
		labels[name] = template
	}

	captures := config.CaptureNames(match)

	return &Rule{
		MetricName:        rule.MetricName,
		Help:              rule.Help,
		Counter:           rule.Type == config.TypeCounter,
		LastUpdatedMetric: rule.LastUpdatedMetric,
		match:             match,
		captures:          captures,
		labels:            labels,
		autoLabels:        config.LabelNames(captures, labels),
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

	return Match{Labels: r.resolveLabels(captured), Value: value.number}, true, nil
}

func (r *Rule) resolveLabels(captured map[string]string) map[string]string {
	labels := make(map[string]string, len(r.autoLabels))

	for _, name := range r.autoLabels {
		if template, declared := r.labels[name]; declared {
			labels[name] = config.ExpandTemplate(template, captured)

			continue
		}

		labels[name] = captured[name]
	}

	return labels
}

func (r *Rule) LabelNames() []string {
	return r.autoLabels
}
