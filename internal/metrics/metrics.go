package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// Metrics 自身可观测性指标集合
type Metrics struct {
	registry *prometheus.Registry

	Received       *prometheus.CounterVec
	Flushed        *prometheus.CounterVec
	Dropped        *prometheus.CounterVec
	MemoryDepth    *prometheus.GaugeVec
	DiskDepth      *prometheus.GaugeVec
	DiskSize       *prometheus.GaugeVec
	FlushDuration  *prometheus.HistogramVec
	BackendLatency *prometheus.HistogramVec
}

// New 创建并注册所有指标
func New() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		registry: reg,
		Received: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "metric_gw_received_total",
			Help: "Total number of metrics received via HTTP API.",
		}, []string{}),
		Flushed: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "metric_gw_flushed_total",
			Help: "Total number of metrics flushed to backends.",
		}, []string{"backend", "status"}),
		Dropped: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "metric_gw_dropped_total",
			Help: "Total number of metrics dropped.",
		}, []string{"backend", "reason"}),
		MemoryDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "metric_gw_buffer_memory_depth",
			Help: "Current number of metrics in memory queue.",
		}, []string{"backend"}),
		DiskDepth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "metric_gw_buffer_disk_depth",
			Help: "Current number of metrics in disk queue.",
		}, []string{"backend"}),
		DiskSize: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "metric_gw_buffer_disk_size_bytes",
			Help: "Current disk queue size in bytes.",
		}, []string{"backend"}),
		FlushDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "metric_gw_flush_duration_seconds",
			Help:    "Time spent flushing a batch to a backend.",
			Buckets: prometheus.DefBuckets,
		}, []string{"backend"}),
		BackendLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "metric_gw_backend_latency_seconds",
			Help:    "Backend response latency.",
			Buckets: prometheus.DefBuckets,
		}, []string{"backend"}),
	}

	reg.MustRegister(m.Received)
	reg.MustRegister(m.Flushed)
	reg.MustRegister(m.Dropped)
	reg.MustRegister(m.MemoryDepth)
	reg.MustRegister(m.DiskDepth)
	reg.MustRegister(m.DiskSize)
	reg.MustRegister(m.FlushDuration)
	reg.MustRegister(m.BackendLatency)

	return m
}

// Registry 返回 Prometheus registry
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}
