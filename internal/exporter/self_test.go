package exporter

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"
)

func TestSelf_RegistersAndReports(t *testing.T) {
	self := NewSelf(func() float64 { return 42 })

	reg := prometheus.NewRegistry()
	require.NoError(t, self.Register(reg))

	self.SetBuildInfo("v1", "abc123", "go1.27.0")
	self.SetConnected(true)
	self.Reconnects.Inc()
	self.Received.WithLabelValues("zwave").Add(3)
	self.Dropped.Inc()
	self.Errors.WithLabelValues("zwave", "json").Inc()
	self.Reloads.Inc()
	self.ReloadFailures.Inc()

	require.InDelta(t, 1.0, testutil.ToFloat64(self.Connected), 1e-9)
	require.InDelta(t, 42.0, testutil.ToFloat64(self.SeriesInStore), 1e-9)
	require.InDelta(t, 3.0, testutil.ToFloat64(self.Received.WithLabelValues("zwave")), 1e-9)

	require.NoError(t, testutil.GatherAndCompare(reg, strings.NewReader(`
# HELP mqtt2prom_build_info Build information, always 1
# TYPE mqtt2prom_build_info gauge
mqtt2prom_build_info{commit="abc123",go_version="go1.27.0",version="v1"} 1
`), "mqtt2prom_build_info"))
}

func TestSelf_SetConnectedDown(t *testing.T) {
	self := NewSelf(func() float64 { return 0 })

	self.SetConnected(false)

	require.InDelta(t, 0.0, testutil.ToFloat64(self.Connected), 1e-9)
}

func TestSelf_RegisterReportsADuplicate(t *testing.T) {
	reg := prometheus.NewRegistry()
	require.NoError(t, NewSelf(func() float64 { return 0 }).Register(reg))

	err := NewSelf(func() float64 { return 0 }).Register(reg)
	require.Error(t, err)
}
