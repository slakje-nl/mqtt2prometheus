package store

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSet_GaugeKeepsTheLatestValue(t *testing.T) {
	s := New()

	s.Set("temp", Gauge, "help", map[string]string{"sensor": "a"}, 25.5)
	s.Set("temp", Gauge, "help", map[string]string{"sensor": "a"}, 21.0)

	snapshot := s.Snapshot()
	require.Len(t, snapshot, 1)
	require.InDelta(t, 21.0, snapshot[0].Value, 1e-9)
	require.Equal(t, "help", snapshot[0].Help)
	require.Equal(t, []string{"sensor"}, snapshot[0].Key.LabelNames)
	require.Equal(t, []string{"a"}, snapshot[0].Key.LabelValues)
}

func TestSet_GaugeMayDecrease(t *testing.T) {
	s := New()

	s.Set("clients", Gauge, "", nil, 10)
	s.Set("clients", Gauge, "", nil, 2)

	require.InDelta(t, 2.0, s.Snapshot()[0].Value, 1e-9)
}

func TestSet_CounterCarriesAnOffsetAcrossAReset(t *testing.T) {
	s := New()

	s.Set("messages", Counter, "", nil, 100)
	require.InDelta(t, 100.0, s.Snapshot()[0].Value, 1e-9)

	s.Set("messages", Counter, "", nil, 5)
	require.InDelta(t, 105.0, s.Snapshot()[0].Value, 1e-9)

	s.Set("messages", Counter, "", nil, 7)
	require.InDelta(t, 107.0, s.Snapshot()[0].Value, 1e-9)
}

func TestSet_CounterSurvivesRepeatedResets(t *testing.T) {
	s := New()

	for _, value := range []float64{100, 3, 50, 1} {
		s.Set("messages", Counter, "", nil, value)
	}

	require.InDelta(t, 151.0, s.Snapshot()[0].Value, 1e-9)
}

func TestSet_CounterDoesNotOffsetOnFirstObservation(t *testing.T) {
	s := New()

	s.Set("messages", Counter, "", nil, 0)

	require.InDelta(t, 0.0, s.Snapshot()[0].Value, 1e-9)
}

func TestSet_DifferentLabelValuesAreDifferentSeries(t *testing.T) {
	s := New()

	s.Set("temp", Gauge, "", map[string]string{"sensor": "a"}, 1)
	s.Set("temp", Gauge, "", map[string]string{"sensor": "b"}, 2)

	require.Equal(t, 2, s.Len())
}

func TestSet_LabelsAreOrderIndependent(t *testing.T) {
	s := New()

	s.Set("m", Gauge, "", map[string]string{"a": "1", "b": "2"}, 1)
	s.Set("m", Gauge, "", map[string]string{"b": "2", "a": "1"}, 9)

	require.Equal(t, 1, s.Len())
	require.InDelta(t, 9.0, s.Snapshot()[0].Value, 1e-9)
}

func TestSet_LabelNameAndValueCannotBeConfused(t *testing.T) {
	s := New()

	s.Set("m", Gauge, "", map[string]string{"ab": "c"}, 1)
	s.Set("m", Gauge, "", map[string]string{"a": "bc"}, 2)

	require.Equal(t, 2, s.Len())
}

func TestSnapshot_IsSortedByNameThenLabelValues(t *testing.T) {
	s := New()

	s.Set("b", Gauge, "", map[string]string{"x": "2"}, 1)
	s.Set("a", Gauge, "", map[string]string{"x": "9"}, 1)
	s.Set("b", Gauge, "", map[string]string{"x": "1"}, 1)

	snapshot := s.Snapshot()
	require.Equal(t, "a", snapshot[0].Key.Name)
	require.Equal(t, []string{"1"}, snapshot[1].Key.LabelValues)
	require.Equal(t, []string{"2"}, snapshot[2].Key.LabelValues)
}

func TestSnapshot_OfAnEmptyStore(t *testing.T) {
	require.Empty(t, New().Snapshot())
}

func TestRetain_DropsSeriesWhoseMetricIsGone(t *testing.T) {
	s := New()

	s.Set("keep", Gauge, "", map[string]string{"a": "1"}, 1)
	s.Set("keep", Gauge, "", map[string]string{"a": "2"}, 1)
	s.Set("drop", Gauge, "", nil, 1)

	s.Retain(func(name string) bool { return name == "keep" })

	require.Equal(t, 2, s.Len())
	require.Equal(t, "keep", s.Snapshot()[0].Key.Name)
}

func TestRetain_KeepsCounterOffsetsForSurvivingSeries(t *testing.T) {
	s := New()

	s.Set("messages", Counter, "", nil, 100)
	s.Set("messages", Counter, "", nil, 5)

	s.Retain(func(string) bool { return true })
	s.Set("messages", Counter, "", nil, 6)

	require.InDelta(t, 106.0, s.Snapshot()[0].Value, 1e-9)
}

func TestSet_KindAndHelpAreUpdatedInPlace(t *testing.T) {
	s := New()

	s.Set("m", Gauge, "old", nil, 1)
	s.Set("m", Counter, "new", nil, 2)

	snapshot := s.Snapshot()
	require.Equal(t, Counter, snapshot[0].Kind)
	require.Equal(t, "new", snapshot[0].Help)
}
