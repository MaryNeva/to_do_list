package observability

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"to-do-list/internal/buildinfo"
)

type Metrics struct {
	registry *prometheus.Registry

	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
	httpInFlight prometheus.Gauge

	authAttempts     *prometheus.CounterVec
	refreshRotations *prometheus.CounterVec
	sessionsRevoked  *prometheus.CounterVec

	cleanupRuns     *prometheus.CounterVec
	cleanupRemoved  prometheus.Counter
	cleanupDuration prometheus.Histogram
}

func New(namespace string, build buildinfo.Info) *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),

		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "http",
			Name:      "requests_total",
			Help:      "Requests served, by method, route template and status code.",
		}, []string{"method", "route", "status"}),

		httpDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "Request latency, by method and route template.",
			Buckets:   prometheus.DefBuckets,
		}, []string{"method", "route"}),

		httpInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: namespace,
			Subsystem: "http",
			Name:      "requests_in_flight",
			Help:      "Requests currently being served.",
		}),

		authAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "auth",
			Name:      "attempts_total",
			Help:      "Authentication operations, by operation and outcome.",
		}, []string{"operation", "outcome"}),

		refreshRotations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "auth",
			Name:      "refresh_rotations_total",
			Help:      "Refresh token exchanges, by outcome. The reuse outcome means a consumed token was presented again.",
		}, []string{"outcome"}),

		sessionsRevoked: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "auth",
			Name:      "sessions_revoked_total",
			Help:      "Refresh sessions ended, by reason.",
		}, []string{"reason"}),

		cleanupRuns: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "cleanup",
			Name:      "runs_total",
			Help:      "Sweeps of the expired refresh token janitor, by outcome.",
		}, []string{"outcome"}),

		cleanupRemoved: prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "cleanup",
			Name:      "refresh_tokens_removed_total",
			Help:      "Expired refresh token rows deleted.",
		}),

		cleanupDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Namespace: namespace,
			Subsystem: "cleanup",
			Name:      "duration_seconds",
			Help:      "Duration of one janitor sweep.",
			Buckets:   prometheus.DefBuckets,
		}),
	}

	m.registry.MustRegister(
		m.httpRequests,
		m.httpDuration,
		m.httpInFlight,
		m.authAttempts,
		m.refreshRotations,
		m.sessionsRevoked,
		m.cleanupRuns,
		m.cleanupRemoved,
		m.cleanupDuration,
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		buildInfoCollector(namespace, build),
	)

	return m
}

// KnownLabels lists the label values that exist before anything has happened.
// The vocabulary lives in the use-case layer, so the caller supplies it.
type KnownLabels struct {
	AuthOperations    []string
	AuthOutcomes      []string
	RotationOutcomes  []string
	RevocationReasons []string
	CleanupOutcomes   []string
}

func (m *Metrics) Preload(known KnownLabels) {
	for _, operation := range known.AuthOperations {
		for _, outcome := range known.AuthOutcomes {
			m.authAttempts.WithLabelValues(operation, outcome)
		}
	}
	for _, outcome := range known.RotationOutcomes {
		m.refreshRotations.WithLabelValues(outcome)
	}
	for _, reason := range known.RevocationReasons {
		m.sessionsRevoked.WithLabelValues(reason)
	}
	for _, outcome := range known.CleanupOutcomes {
		m.cleanupRuns.WithLabelValues(outcome)
	}
}

func (m *Metrics) Registry() *prometheus.Registry { return m.registry }

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

func (m *Metrics) Register(c prometheus.Collector) error {
	return m.registry.Register(c)
}

func (m *Metrics) ObserveRequest(method, route string, status int, d time.Duration) {
	m.httpRequests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.httpDuration.WithLabelValues(method, route).Observe(d.Seconds())
}

func (m *Metrics) IncInFlight() { m.httpInFlight.Inc() }

func (m *Metrics) DecInFlight() { m.httpInFlight.Dec() }

func (m *Metrics) AuthAttempt(operation, outcome string) {
	m.authAttempts.WithLabelValues(operation, outcome).Inc()
}

func (m *Metrics) RefreshRotation(outcome string) {
	m.refreshRotations.WithLabelValues(outcome).Inc()
}

func (m *Metrics) SessionsRevoked(reason string) {
	m.sessionsRevoked.WithLabelValues(reason).Inc()
}

func (m *Metrics) CleanupRun(outcome string, removed int64, d time.Duration) {
	m.cleanupRuns.WithLabelValues(outcome).Inc()
	m.cleanupDuration.Observe(d.Seconds())
	if removed > 0 {
		m.cleanupRemoved.Add(float64(removed))
	}
}

func buildInfoCollector(namespace string, build buildinfo.Info) prometheus.Collector {
	gauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: namespace,
		Name:      "build_info",
		Help:      "Build identity of the running binary; the value is always 1.",
	}, []string{"version", "commit", "built_at", "go_version"})

	gauge.WithLabelValues(build.Version, build.Commit, build.BuiltAt, build.GoVersion).Set(1)

	return gauge
}
