package observability

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestPoolCollector_RendersTheCurrentSnapshot(t *testing.T) {
	stats := PoolStats{
		AcquiredConns:        3,
		IdleConns:            5,
		TotalConns:           8,
		MaxConns:             10,
		ConstructingConns:    1,
		AcquireCount:         120,
		EmptyAcquireCount:    4,
		CanceledAcquireCount: 2,
		AcquireDuration:      1500 * time.Millisecond,
	}

	registry := prometheus.NewRegistry()
	registry.MustRegister(NewPoolCollector("todo", func() PoolStats { return stats }))

	expected := `
# HELP todo_db_pool_acquired_connections Connections currently checked out.
# TYPE todo_db_pool_acquired_connections gauge
todo_db_pool_acquired_connections 3
# HELP todo_db_pool_max_connections Upper bound on pool size.
# TYPE todo_db_pool_max_connections gauge
todo_db_pool_max_connections 10
# HELP todo_db_pool_empty_acquires_total Acquires that had to wait because the pool was empty.
# TYPE todo_db_pool_empty_acquires_total counter
todo_db_pool_empty_acquires_total 4
# HELP todo_db_pool_acquire_duration_seconds_total Time spent waiting for a connection.
# TYPE todo_db_pool_acquire_duration_seconds_total counter
todo_db_pool_acquire_duration_seconds_total 1.5
`

	err := testutil.GatherAndCompare(registry, strings.NewReader(expected),
		"todo_db_pool_acquired_connections",
		"todo_db_pool_max_connections",
		"todo_db_pool_empty_acquires_total",
		"todo_db_pool_acquire_duration_seconds_total",
	)
	if err != nil {
		t.Error(err)
	}
}

func TestPoolCollector_ReadsThePoolOnEveryScrape(t *testing.T) {
	var calls atomic.Int64

	registry := prometheus.NewRegistry()
	registry.MustRegister(NewPoolCollector("todo", func() PoolStats {
		n := calls.Add(1)
		return PoolStats{AcquiredConns: int32(n)}
	}))

	first := testutil.ToFloat64(mustGauge(t, registry, "todo_db_pool_acquired_connections"))
	second := testutil.ToFloat64(mustGauge(t, registry, "todo_db_pool_acquired_connections"))

	if first != 1 || second != 2 {
		t.Errorf("acquired = %v then %v, want 1 then 2 - the pool was not re-read", first, second)
	}
	if calls.Load() != 2 {
		t.Errorf("the pool was read %d times, want 2", calls.Load())
	}
}

func mustGauge(t *testing.T, registry *prometheus.Registry, name string) prometheus.Gauge {
	t.Helper()

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: name})
		gauge.Set(family.GetMetric()[0].GetGauge().GetValue())
		return gauge
	}

	t.Fatalf("metric %q was not collected", name)
	return nil
}
