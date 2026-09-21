package main

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// listener stands in for a serving goroutine that reports once on its channel.
type listener struct {
	ch chan error
}

func newListener() *listener { return &listener{ch: make(chan error, 1)} }

func (l *listener) fail(err error) { l.ch <- err }

func (l *listener) stopsWhenDrained() { l.ch <- nil }

func TestAwaitStop_DrainsEvenWhenTheExitIsAFailure(t *testing.T) {
	for _, tc := range []struct {
		name     string
		arrange  func(api, metrics *listener)
		wantErr  string
		wantCode string
	}{
		{
			name:    "the API listener fails",
			arrange: func(api, _ *listener) { api.fail(errors.New("address already in use")) },
			wantErr: "listen: address already in use",
		},
		{
			name:    "the metrics listener fails",
			arrange: func(_, metrics *listener) { metrics.fail(errors.New("metrics port taken")) },
			wantErr: "metrics listener: metrics port taken",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, metrics := newListener(), newListener()
			signal := make(chan struct{}) // never closed: the failure is what ends this run

			drained := make(chan struct{})
			drain := func() error {
				close(drained)
				// Listeners still running stop only because of the drain.
				select {
				case api.ch <- nil:
				default:
				}
				select {
				case metrics.ch <- nil:
				default:
				}
				return nil
			}

			tc.arrange(api, metrics)

			err := awaitStopWithin(t, stopSignals{
				signal: signal, serve: api.ch, metrics: metrics.ch, hasMetrics: true,
			}, drain)

			select {
			case <-drained:
			default:
				t.Fatal("the run ended without draining: the other server was left serving and the pool was closed under it")
			}

			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want one containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestAwaitStop_WaitsForBothListenersAfterASignal(t *testing.T) {
	api, metrics := newListener(), newListener()
	signal := make(chan struct{})
	close(signal)

	drain := func() error {
		api.stopsWhenDrained()
		metrics.stopsWhenDrained()
		return nil
	}

	if err := awaitStopWithin(t, stopSignals{
		signal: signal, serve: api.ch, metrics: metrics.ch, hasMetrics: true,
	}, drain); err != nil {
		t.Fatalf("awaitStop() = %v, want nil", err)
	}
}

// Without a separate metrics listener there is nothing to wait for on that channel.
func TestAwaitStop_DoesNotWaitForAMetricsListenerThatWasNeverStarted(t *testing.T) {
	api := newListener()
	signal := make(chan struct{})
	close(signal)

	drain := func() error {
		api.stopsWhenDrained()
		return nil
	}

	if err := awaitStopWithin(t, stopSignals{
		signal: signal, serve: api.ch, metrics: make(chan error), hasMetrics: false,
	}, drain); err != nil {
		t.Fatalf("awaitStop() = %v, want nil", err)
	}
}

func TestAwaitStop_ReportsWorkThatOutlivedTheGracePeriod(t *testing.T) {
	api := newListener()
	signal := make(chan struct{})
	close(signal)

	drain := func() error {
		api.stopsWhenDrained()
		return errors.New("context deadline exceeded")
	}

	err := awaitStopWithin(t, stopSignals{
		signal: signal, serve: api.ch, metrics: make(chan error), hasMetrics: false,
	}, drain)
	if err == nil || !strings.Contains(err.Error(), "graceful shutdown: context deadline exceeded") {
		t.Fatalf("error = %v, want one naming the graceful shutdown", err)
	}
}

func TestAwaitStop_PrefersTheFailureThatStartedTheShutdown(t *testing.T) {
	api, metrics := newListener(), newListener()
	api.fail(errors.New("address already in use"))

	drain := func() error {
		metrics.stopsWhenDrained()
		return errors.New("context deadline exceeded")
	}

	err := awaitStopWithin(t, stopSignals{
		signal: make(chan struct{}), serve: api.ch, metrics: metrics.ch, hasMetrics: true,
	}, drain)
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("error = %v, want the listener failure", err)
	}
	if strings.Contains(err.Error(), "graceful shutdown") {
		t.Errorf("error = %v; the drain timeout replaced the cause", err)
	}
}

// awaitStopWithin fails the test instead of hanging when awaitStop never returns.
func awaitStopWithin(t *testing.T, stops stopSignals, drain func() error) error {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- awaitStop(stops, drain, time.Second, quietLogger()) }()

	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("awaitStop() never returned: it is waiting for a listener that already reported")
		return nil
	}
}
