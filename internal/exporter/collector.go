package exporter

import (
	"github.com/prometheus/client_golang/prometheus"

	"github.com/slakje-nl/mqtt2prometheus/internal/store"
)

type Collector struct {
	samples *store.Store
}

func NewCollector(samples *store.Store) *Collector {
	return &Collector{samples: samples}
}

func (c *Collector) Describe(chan<- *prometheus.Desc) {}

func (c *Collector) Collect(out chan<- prometheus.Metric) {
	for _, sample := range c.samples.Snapshot() {
		desc := prometheus.NewDesc(sample.Key.Name, sample.Help, sample.Key.LabelNames, nil)

		metric, err := prometheus.NewConstMetric(desc, valueType(sample.Kind), sample.Value, sample.Key.LabelValues...)
		if err != nil {
			out <- prometheus.NewInvalidMetric(desc, err)

			continue
		}

		out <- metric
	}
}

func valueType(kind store.Kind) prometheus.ValueType {
	if kind == store.Counter {
		return prometheus.CounterValue
	}

	return prometheus.GaugeValue
}
