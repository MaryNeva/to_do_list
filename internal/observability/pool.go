package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type PoolStats struct {
	AcquiredConns     int32
	IdleConns         int32
	TotalConns        int32
	MaxConns          int32
	ConstructingConns int32

	AcquireCount         int64
	EmptyAcquireCount    int64
	CanceledAcquireCount int64
	AcquireDuration      time.Duration
}

type poolCollector struct {
	stats func() PoolStats

	acquired     *prometheus.Desc
	idle         *prometheus.Desc
	total        *prometheus.Desc
	max          *prometheus.Desc
	constructing *prometheus.Desc

	acquireCount    *prometheus.Desc
	emptyAcquire    *prometheus.Desc
	canceledAcquire *prometheus.Desc
	acquireSeconds  *prometheus.Desc
}

func NewPoolCollector(namespace string, stats func() PoolStats) prometheus.Collector {
	name := func(s string) string {
		return prometheus.BuildFQName(namespace, "db_pool", s)
	}

	return &poolCollector{
		stats: stats,

		acquired:     prometheus.NewDesc(name("acquired_connections"), "Connections currently checked out.", nil, nil),
		idle:         prometheus.NewDesc(name("idle_connections"), "Connections currently idle in the pool.", nil, nil),
		total:        prometheus.NewDesc(name("total_connections"), "Connections the pool currently holds.", nil, nil),
		max:          prometheus.NewDesc(name("max_connections"), "Upper bound on pool size.", nil, nil),
		constructing: prometheus.NewDesc(name("constructing_connections"), "Connections currently being established.", nil, nil),

		acquireCount:    prometheus.NewDesc(name("acquires_total"), "Successful acquires from the pool.", nil, nil),
		emptyAcquire:    prometheus.NewDesc(name("empty_acquires_total"), "Acquires that had to wait because the pool was empty.", nil, nil),
		canceledAcquire: prometheus.NewDesc(name("canceled_acquires_total"), "Acquires abandoned because their context ended first.", nil, nil),
		acquireSeconds:  prometheus.NewDesc(name("acquire_duration_seconds_total"), "Time spent waiting for a connection.", nil, nil),
	}
}

func (c *poolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.acquired
	ch <- c.idle
	ch <- c.total
	ch <- c.max
	ch <- c.constructing
	ch <- c.acquireCount
	ch <- c.emptyAcquire
	ch <- c.canceledAcquire
	ch <- c.acquireSeconds
}

func (c *poolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.stats()

	gauge := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v)
	}
	counter := func(d *prometheus.Desc, v float64) {
		ch <- prometheus.MustNewConstMetric(d, prometheus.CounterValue, v)
	}

	gauge(c.acquired, float64(s.AcquiredConns))
	gauge(c.idle, float64(s.IdleConns))
	gauge(c.total, float64(s.TotalConns))
	gauge(c.max, float64(s.MaxConns))
	gauge(c.constructing, float64(s.ConstructingConns))

	counter(c.acquireCount, float64(s.AcquireCount))
	counter(c.emptyAcquire, float64(s.EmptyAcquireCount))
	counter(c.canceledAcquire, float64(s.CanceledAcquireCount))
	counter(c.acquireSeconds, s.AcquireDuration.Seconds())
}
