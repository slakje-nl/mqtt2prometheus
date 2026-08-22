package config

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type Problems []string

func (p Problems) Error() string {
	return fmt.Sprintf("%d configuration problems:\n  %s", len(p), strings.Join(p, "\n  "))
}

func (c *Config) Validate() error {
	var p Problems

	p = c.validateProcess(p)

	seenSource := map[string]string{}
	metricLabels := map[string]string{}

	for _, src := range c.SourceList {
		p = src.validate(p)

		if first, dup := seenSource[src.Name]; dup {
			p = append(p, fmt.Sprintf("%s: source name %q already used by %s", src.Path, src.Name, first))
		} else if src.Name != "" {
			seenSource[src.Name] = src.Path
		}

		p = src.validateMetricConsistency(p, metricLabels)
	}

	if len(p) > 0 {
		return p
	}

	return nil
}

func (c *Config) validateProcess(p Problems) Problems {
	if c.MQTT.Broker == "" {
		p = append(p, mainFile+": mqtt.broker is required")
	}

	if c.MQTT.ClientID == "" {
		p = append(p, mainFile+": mqtt.client_id is required")
	}

	if c.MQTT.Username == "" {
		p = append(p, mainFile+": mqtt.username is required")
	}

	if c.MQTT.Password == "" {
		p = append(p, mainFile+": mqtt.password is required")
	}

	switch {
	case c.MQTT.QoS == nil:
		p = append(p, mainFile+": mqtt.qos is required")
	case *c.MQTT.QoS > 2:
		p = append(p, fmt.Sprintf("%s: mqtt.qos must be 0, 1 or 2, got %d", mainFile, *c.MQTT.QoS))
	}

	if c.MQTT.CleanSession == nil {
		p = append(p, mainFile+": mqtt.clean_session is required")
	}

	if c.Server.Listen == "" {
		p = append(p, mainFile+": server.listen is required")
	}

	switch c.Log.Level {
	case "":
		p = append(p, mainFile+": log.level is required")
	case "debug", "info", "warn", "error":
	default:
		p = append(p, fmt.Sprintf("%s: log.level must be debug, info, warn or error, got %q", mainFile, c.Log.Level))
	}

	if len(c.SourceList) == 0 {
		p = append(p, mainFile+": no sources loaded")
	}

	return p
}

func (s Source) validate(p Problems) Problems {
	if s.Name == "" {
		p = append(p, s.Path+": name is required")
	}

	if s.Subscribe == "" {
		p = append(p, s.Path+": subscribe is required")
	}

	if s.QoS != nil && *s.QoS > 2 {
		p = append(p, fmt.Sprintf("%s: qos must be 0, 1 or 2, got %d", s.Path, *s.QoS))
	}

	if len(s.Rules) == 0 {
		p = append(p, s.Path+": at least one rule is required")
	}

	for i, rule := range s.Rules {
		p = rule.validate(p, fmt.Sprintf("%s: rule %d", s.Path, i))
	}

	return p
}

func (r Rule) validate(p Problems, where string) Problems {
	if r.MetricName == "" {
		p = append(p, where+": metric_name is required")
	} else if !metricNamePattern.MatchString(r.MetricName) {
		p = append(p, fmt.Sprintf("%s: metric_name %q is not a valid Prometheus metric name", where, r.MetricName))
	}

	if r.LastUpdatedMetric != "" && !metricNamePattern.MatchString(r.LastUpdatedMetric) {
		p = append(p, fmt.Sprintf("%s: last_updated_metric %q is not a valid Prometheus metric name", where, r.LastUpdatedMetric))
	}

	switch r.Type {
	case "":
		p = append(p, where+": type is required")
	case TypeGauge, TypeCounter:
	default:
		p = append(p, fmt.Sprintf("%s: type must be %q or %q, got %q", where, TypeGauge, TypeCounter, r.Type))
	}

	captures, problems := validateMatch(r.Match, where)
	p = append(p, problems...)
	p = r.Value.validate(p, where)

	return validateLabels(p, where, captures, r.Labels)
}

func validateMatch(match, where string) ([]string, Problems) {
	if match == "" {
		return nil, Problems{where + ": match is required"}
	}

	re, err := regexp.Compile(match)
	if err != nil {
		return nil, Problems{fmt.Sprintf("%s: match is not a valid regular expression: %v", where, err)}
	}

	var (
		captures []string
		p        Problems
	)

	for i, name := range re.SubexpNames() {
		if name == "" {
			continue
		}

		if !labelNamePattern.MatchString(name) {
			p = append(p, fmt.Sprintf("%s: capture group %d %q is not a valid Prometheus label name", where, i, name))

			continue
		}

		captures = append(captures, name)
	}

	return captures, p
}

func validateLabels(p Problems, where string, captures []string, labels map[string]string) Problems {
	captureSet := make(map[string]struct{}, len(captures))
	for _, name := range captures {
		captureSet[name] = struct{}{}
	}

	for _, name := range sortedKeys(labels) {
		if !labelNamePattern.MatchString(name) {
			p = append(p, fmt.Sprintf("%s: label %q is not a valid Prometheus label name", where, name))
		}

		if _, clash := captureSet[name]; clash {
			p = append(p, fmt.Sprintf("%s: label %q collides with a capture group of the same name", where, name))
		}

		for _, ref := range TemplateRefs(labels[name]) {
			if _, known := captureSet[ref]; !known {
				p = append(p, fmt.Sprintf("%s: label %q references {%s}, which is not a capture group of match", where, name, ref))
			}
		}
	}

	return p
}

func (v Value) validate(p Problems, where string) Problems {
	switch v.From {
	case "":
		return append(p, where+": value.from is required")
	case FromJSON:
		if v.Path == "" {
			p = append(p, where+": value.path is required when value.from is json")
		}
	case FromRaw:
		if v.Path != "" {
			p = append(p, where+": value.path is only valid when value.from is json")
		}
	default:
		return append(p, fmt.Sprintf("%s: value.from must be %q or %q, got %q", where, FromJSON, FromRaw, v.From))
	}

	if v.Regex != "" {
		re, err := regexp.Compile(v.Regex)
		if err != nil {
			p = append(p, fmt.Sprintf("%s: value.regex is not a valid regular expression: %v", where, err))
		} else if re.NumSubexp() != 1 {
			p = append(p, fmt.Sprintf("%s: value.regex must have exactly one capture group, got %d", where, re.NumSubexp()))
		}
	}

	if v.Scale != nil && *v.Scale == 0 {
		p = append(p, where+": value.scale must not be zero")
	}

	return p
}

func (s Source) validateMetricConsistency(p Problems, metricLabels map[string]string) Problems {
	for i, rule := range s.Rules {
		if rule.MetricName == "" || rule.Match == "" {
			continue
		}

		re, err := regexp.Compile(rule.Match)
		if err != nil {
			continue
		}

		names := strings.Join(LabelNames(CaptureNames(re), rule.Labels, s.Labels), " ")
		where := fmt.Sprintf("%s: rule %d", s.Path, i)

		if seen, ok := metricLabels[rule.MetricName]; ok && seen != names {
			p = append(p, fmt.Sprintf(
				"%s: metric_name %q is already used with labels [%s], this rule uses [%s]",
				where, rule.MetricName, seen, names))

			continue
		}

		metricLabels[rule.MetricName] = names
	}

	return p
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

var (
	metricNamePattern = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelNamePattern  = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)
