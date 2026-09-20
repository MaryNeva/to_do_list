package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Draining is anything that stops accepting new work and waits for what is
// already running. Both *fiber.App and MetricsServer satisfy it.
type Draining interface {
	ShutdownWithContext(ctx context.Context) error
}

// Drain performs the shutdown in the order that makes it graceful: stop
// accepting, wait for the work already in flight, and only then cancel what
// is left. The request contexts must NOT come from the signal context - a
// signal cancels that one immediately, and handlers would be interrupted by
// the very signal that asked for a clean stop.
//
// Every server gets the same budget and they drain at the same time, so a
// slow scrape cannot spend the time the API needed.
func Drain(timeout time.Duration, cancelInFlight context.CancelFunc, servers ...Draining) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	errs := make(chan error, len(servers))
	for _, server := range servers {
		if server == nil {
			errs <- nil
			continue
		}
		go func(server Draining) { errs <- server.ShutdownWithContext(ctx) }(server)
	}

	var problems []error
	for range servers {
		if err := <-errs; err != nil {
			problems = append(problems, err)
		}
	}

	// Whatever outlived the grace period is abandoned here, not before.
	if cancelInFlight != nil {
		cancelInFlight()
	}

	return errors.Join(problems...)
}

// MetricsServer serves the exporter on a listener of its own. The endpoint
// has no authentication, so a deployment binds it somewhere only the scraper
// can reach and publishes nothing but the API port.
type MetricsServer struct {
	srv *http.Server
	ln  net.Listener
}

// NewMetricsServer opens the listener immediately. A port already in use has
// to fail here, while the process can still refuse to start: reported later
// from a goroutine it would leave a service that passes every health check
// and exports nothing.
func NewMetricsServer(addr, path string, exporter http.Handler) (*MetricsServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("metrics listener on %q: %w", addr, err)
	}

	mux := http.NewServeMux()
	mux.Handle(path, exporter)

	return &MetricsServer{
		srv: &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
		},
		ln: ln,
	}, nil
}

// Addr is the address actually bound, which differs from the requested one
// when the port was left to the operating system.
func (m *MetricsServer) Addr() string { return m.ln.Addr().String() }

// Serve blocks until the server is shut down, and reports nil in that case.
func (m *MetricsServer) Serve() error {
	if err := m.srv.Serve(m.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ShutdownWithContext tolerates a nil receiver, so a deployment that serves
// metrics on the API port can be handed to Drain unconditionally.
func (m *MetricsServer) ShutdownWithContext(ctx context.Context) error {
	if m == nil {
		return nil
	}
	return m.srv.Shutdown(ctx)
}
