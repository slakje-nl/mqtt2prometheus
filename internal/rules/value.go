package rules

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/slakje-nl/mqtt2prometheus/internal/config"
)

var (
	ErrNotNumeric   = errors.New("value is not numeric")
	ErrRegexNoMatch = errors.New("value.regex did not match the payload")
	ErrBadJSON      = errors.New("payload is not valid JSON")
)

type reading struct {
	number float64
	skip   bool
}

var skipped = reading{skip: true}

type extractor struct {
	fromJSON bool
	path     []string
	regex    *regexp.Regexp
	scale    *float64
	mapping  map[string]float64
}

func newExtractor(v config.Value) (extractor, error) {
	e := extractor{
		fromJSON: v.From == config.FromJSON,
		scale:    v.Scale,
		mapping:  v.Map,
	}

	if e.fromJSON {
		e.path = strings.Split(v.Path, ".")
	}

	if v.Regex != "" {
		re, err := regexp.Compile(v.Regex)
		if err != nil {
			return extractor{}, fmt.Errorf("value.regex: %w", err)
		}

		e.regex = re
	}

	return e, nil
}

func (e extractor) extract(payload []byte) (reading, error) {
	raw, present, err := e.raw(payload)
	if err != nil {
		return skipped, err
	}

	if !present {
		return skipped, nil
	}

	result, err := e.toNumber(raw)
	if err != nil || result.skip {
		return skipped, err
	}

	if e.scale != nil {
		result.number *= *e.scale
	}

	return result, nil
}

func (e extractor) raw(payload []byte) (any, bool, error) {
	if !e.fromJSON {
		return string(payload), true, nil
	}

	var document any
	if err := json.Unmarshal(payload, &document); err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrBadJSON, err)
	}

	for _, segment := range e.path {
		object, isObject := document.(map[string]any)
		if !isObject {
			return nil, false, nil
		}

		next, present := object[segment]
		if !present {
			return nil, false, nil
		}

		document = next
	}

	if document == nil {
		return nil, false, nil
	}

	return document, true, nil
}

func (e extractor) toNumber(raw any) (reading, error) {
	switch typed := raw.(type) {
	case float64:
		return reading{number: typed}, nil
	case bool:
		if typed {
			return reading{number: 1}, nil
		}

		return reading{number: 0}, nil
	case string:
		return e.fromString(typed)
	default:
		return skipped, fmt.Errorf("%w: %T", ErrNotNumeric, raw)
	}
}

func (e extractor) fromString(s string) (reading, error) {
	if e.regex != nil {
		found := e.regex.FindStringSubmatch(s)
		if found == nil {
			return skipped, fmt.Errorf("%w: %q", ErrRegexNoMatch, s)
		}

		s = found[1]
	}

	if e.mapping != nil {
		number, mapped := e.mapping[s]
		if !mapped {
			return skipped, nil
		}

		return reading{number: number}, nil
	}

	number, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return skipped, fmt.Errorf("%w: %q", ErrNotNumeric, s)
	}

	return reading{number: number}, nil
}
