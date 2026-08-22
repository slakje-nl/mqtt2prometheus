package exporter

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"github.com/slakje-nl/mqtt2prometheus/internal/store"
)

func TestCollect_RendersGaugesAndCounters(t *testing.T) {
	samples := store.New()
	samples.Set("zwave_sensor_temperature", store.Gauge, "Air temperature in celsius",
		map[string]string{"sensor": "example_sensor"}, 25.5)
	samples.Set("mosquitto_messages_total", store.Counter, "Messages since the broker started",
		map[string]string{"direction": "received"}, 123)

	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(NewCollector(samples)))

	expected := `
# HELP mosquitto_messages_total Messages since the broker started
# TYPE mosquitto_messages_total counter
mosquitto_messages_total{direction="received"} 123
# HELP zwave_sensor_temperature Air temperature in celsius
# TYPE zwave_sensor_temperature gauge
zwave_sensor_temperature{sensor="example_sensor"} 25.5
`

	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(expected)))
}

func TestCollect_MetricWithoutHelp(t *testing.T) {
	samples := store.New()
	samples.Set("no_help", store.Gauge, "", nil, 1)

	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(NewCollector(samples)))

	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(
		"# HELP no_help \n# TYPE no_help gauge\nno_help 1\n")))
}

func TestCollect_EmptyStore(t *testing.T) {
	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(NewCollector(store.New())))

	count, err := testutil.GatherAndCount(reg)
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestCollect_InvalidMetricSurfacesAsAnError(t *testing.T) {
	samples := store.New()
	samples.Set("bad", store.Gauge, "", map[string]string{"\xff\xfe": "x"}, 1)

	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(NewCollector(samples)))

	_, err := reg.Gather()
	require.Error(t, err)
}

func TestDescribe_IsUnchecked(t *testing.T) {
	out := make(chan *prometheus.Desc, 1)
	NewCollector(store.New()).Describe(out)
	close(out)

	require.Empty(t, out)
}
