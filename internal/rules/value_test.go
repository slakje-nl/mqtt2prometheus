package rules

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/slakje-nl/mqtt2prometheus/internal/config"
)

func extract(t *testing.T, v config.Value, payload string) (reading, error) {
	t.Helper()

	e, err := newExtractor(v)
	require.NoError(t, err)

	return e.extract(NewPayload([]byte(payload)))
}

func TestExtract_JSONNumber(t *testing.T) {
	got, err := extract(t, config.Value{From: config.FromJSON, Path: "value"}, `{"value":25.5}`)

	require.NoError(t, err)
	require.False(t, got.skip)
	require.InDelta(t, 25.5, got.number, 1e-9)
}

func TestExtract_JSONNestedPath(t *testing.T) {
	got, err := extract(t, config.Value{From: config.FromJSON, Path: "update.state"},
		`{"update":{"state":42}}`)

	require.NoError(t, err)
	require.InDelta(t, 42.0, got.number, 1e-9)
}

func TestExtract_JSONScale(t *testing.T) {
	scale := 0.001
	got, err := extract(t, config.Value{From: config.FromJSON, Path: "value", Scale: &scale},
		`{"time":1711922310802,"value":1711922310552}`)

	require.NoError(t, err)
	require.InDelta(t, 1711922310.552, got.number, 1e-6)
}

func TestExtract_JSONBool(t *testing.T) {
	on, err := extract(t, config.Value{From: config.FromJSON, Path: "contact"}, `{"contact":true}`)
	require.NoError(t, err)
	require.InDelta(t, 1.0, on.number, 1e-9)

	off, err := extract(t, config.Value{From: config.FromJSON, Path: "contact"}, `{"contact":false}`)
	require.NoError(t, err)
	require.InDelta(t, 0.0, off.number, 1e-9)
}

func TestExtract_JSONStringMapping(t *testing.T) {
	value := config.Value{From: config.FromJSON, Path: "state", Map: map[string]float64{"ON": 1, "OFF": 0}}

	on, err := extract(t, value, `{"state":"ON"}`)
	require.NoError(t, err)
	require.InDelta(t, 1.0, on.number, 1e-9)

	unmapped, err := extract(t, value, `{"state":"UNKNOWN"}`)
	require.NoError(t, err)
	require.True(t, unmapped.skip)
}

func TestExtract_JSONSkipsWhatIsNotThere(t *testing.T) {
	cases := map[string]struct {
		path    string
		payload string
	}{
		"absent key":           {"energy", `{"current":1}`},
		"absent nested key":    {"update.state", `{"update":{"other":1}}`},
		"path through scalar":  {"update.state", `{"update":5}`},
		"explicit null":        {"identify", `{"identify":null}`},
		"path through no root": {"a.b", `5`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := extract(t, config.Value{From: config.FromJSON, Path: tc.path}, tc.payload)

			require.NoError(t, err)
			require.True(t, got.skip)
		})
	}
}

func TestExtract_JSONErrors(t *testing.T) {
	_, err := extract(t, config.Value{From: config.FromJSON, Path: "value"}, `not json`)
	require.ErrorIs(t, err, ErrBadJSON)

	_, err = extract(t, config.Value{From: config.FromJSON, Path: "value"}, `{"value":["a"]}`)
	require.ErrorIs(t, err, ErrNotNumeric)

	_, err = extract(t, config.Value{From: config.FromJSON, Path: "value"}, `{"value":"twelve"}`)
	require.ErrorIs(t, err, ErrNotNumeric)
}

func TestExtract_RawFloat(t *testing.T) {
	got, err := extract(t, config.Value{From: config.FromRaw}, "123")

	require.NoError(t, err)
	require.InDelta(t, 123.0, got.number, 1e-9)
}

func TestExtract_RawWithRegex(t *testing.T) {
	value := config.Value{From: config.FromRaw, Regex: `^([0-9]+) seconds$`}

	got, err := extract(t, value, "9284 seconds")
	require.NoError(t, err)
	require.InDelta(t, 9284.0, got.number, 1e-9)

	_, err = extract(t, value, "forever")
	require.ErrorIs(t, err, ErrRegexNoMatch)
}

func TestExtract_RawNotNumeric(t *testing.T) {
	_, err := extract(t, config.Value{From: config.FromRaw}, "1.6.1")

	require.ErrorIs(t, err, ErrNotNumeric)
}

func TestNewExtractor_BadRegex(t *testing.T) {
	_, err := newExtractor(config.Value{From: config.FromRaw, Regex: "^(["})

	require.ErrorContains(t, err, "value.regex")
}
