package exporter

import (
	"github.com/prometheus/client_golang/prometheus"
)

type Self struct {
	BuildInfo       *prometheus.GaugeVec
	Connected       prometheus.Gauge
	Reconnects      prometheus.Counter
	Received        *prometheus.CounterVec
	Dropped         prometheus.Counter
	Errors          *prometheus.CounterVec
	SeriesInStore   prometheus.GaugeFunc
	registerTargets []prometheus.Collector
}

func NewSelf(seriesCount func() float64) *Self {
	s := &Self{
		BuildInfo: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "mqtt2prom_build_info",
			Help: "Build information, always 1",
		}, []string{"version", "commit", "go_version"}),
		Connected: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "mqtt2prom_mqtt_connected",
			Help: "1 when the MQTT connection is up",
		}),
		Reconnects: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mqtt2prom_mqtt_reconnects_total",
			Help: "Number of times the MQTT connection was re-established",
		}),
		Received: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mqtt2prom_messages_received_total",
			Help: "MQTT messages handed to a source",
		}, []string{"source"}),
		Dropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "mqtt2prom_messages_dropped_total",
			Help: "Messages dropped because a source buffer was full",
		}),
		Errors: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "mqtt2prom_message_errors_total",
			Help: "Messages a rule matched but could not turn into a value",
		}, []string{"source", "reason"}),
		SeriesInStore: prometheus.NewGaugeFunc(prometheus.GaugeOpts{
			Name: "mqtt2prom_series",
			Help: "Series currently held in the sample store",
		}, seriesCount),
	}

	s.registerTargets = []prometheus.Collector{
		s.BuildInfo, s.Connected, s.Reconnects, s.Received,
		s.Dropped, s.Errors, s.SeriesInStore,
	}

	return s
}

func (s *Self) Register(reg prometheus.Registerer, extra ...prometheus.Collector) error {
	for _, collector := range append(s.registerTargets, extra...) {
		if err := reg.Register(collector); err != nil {
			return err
		}
	}

	return nil
}

func (s *Self) SetBuildInfo(version, commit, goVersion string) {
	s.BuildInfo.WithLabelValues(version, commit, goVersion).Set(1)
}

func (s *Self) SetConnected(up bool) {
	if up {
		s.Connected.Set(1)

		return
	}

	s.Connected.Set(0)
}
