package rules

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/slakje-nl/mqtt2prometheus/internal/config"
)

var ErrNotALabel = errors.New("value cannot be used as a label")

type labelExtractor struct {
	static   string
	fromJSON bool
	path     []string
	mapping  map[string]string
}

func newLabelExtractor(declared config.Label) labelExtractor {
	e := labelExtractor{
		static:   declared.Value,
		fromJSON: declared.From == config.FromJSON,
		mapping:  declared.Map,
	}

	if e.fromJSON {
		e.path = strings.Split(declared.Path, ".")
	}

	return e
}

func (e labelExtractor) resolve(captured map[string]string, payload []byte) (string, bool, error) {
	value, present, err := e.read(captured, payload)
	if err != nil || !present {
		return "", false, err
	}

	if e.mapping == nil {
		return value, true, nil
	}

	mapped, found := e.mapping[value]

	return mapped, found, nil
}

func (e labelExtractor) read(captured map[string]string, payload []byte) (string, bool, error) {
	if !e.fromJSON {
		return config.ExpandTemplate(e.static, captured), true, nil
	}

	raw, present, err := walkJSON(payload, e.path)
	if err != nil || !present {
		return "", false, err
	}

	return asLabel(raw)
}

func asLabel(raw any) (string, bool, error) {
	switch typed := raw.(type) {
	case string:
		return typed, true, nil
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true, nil
	case bool:
		return strconv.FormatBool(typed), true, nil
	default:
		return "", false, fmt.Errorf("%w: %T", ErrNotALabel, raw)
	}
}

type Labels struct {
	names      []string
	extractors map[string]labelExtractor
}

func CompileLabels(declared map[string]config.Label) Labels {
	compiled := Labels{
		names:      make([]string, 0, len(declared)),
		extractors: make(map[string]labelExtractor, len(declared)),
	}

	for name, label := range declared {
		compiled.names = append(compiled.names, name)
		compiled.extractors[name] = newLabelExtractor(label)
	}

	sort.Strings(compiled.names)

	return compiled
}

func (l Labels) Resolve(captured map[string]string, payload []byte) (map[string]string, bool, error) {
	resolved := make(map[string]string, len(l.names))

	for _, name := range l.names {
		value, present, err := l.extractors[name].resolve(captured, payload)
		if err != nil {
			return nil, false, fmt.Errorf("label %q: %w", name, err)
		}

		if !present {
			return nil, false, nil
		}

		resolved[name] = value
	}

	return resolved, true, nil
}
