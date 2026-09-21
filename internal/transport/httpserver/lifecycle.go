package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Draining is a server that can stop accepting work and wait for in-flight
// requests. *fiber.App and *MetricsServer implement it.
type Draining interface {
	ShutdownWithContext(ctx context.Context) error
}

// Drain shuts all servers down concurrently within one timeout, then calls
// cancelInFlight to cancel whatever is still running. Request contexts must
// not derive from the signal context, or the signal would cancel them at once.
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

	if cancelInFlight != nil {
		cancelInFlight()
	}

	return errors.Join(problems...)
}

// MetricsServer serves the exporter on its own listener. The endpoint has no
// authentication, so it must be reachable only by the scraper.
type MetricsServer struct {
	srv *http.Server
	ln  net.Listener
}

// NewMetricsServer opens the listener immediately so a busy port fails startup.
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

// Addr returns the bound address, which differs from the requested one for port 0.
func (m *MetricsServer) Addr() string { return m.ln.Addr().String() }

// Serve blocks until shutdown and returns nil after a normal shutdown.
func (m *MetricsServer) Serve() error {
	if err := m.srv.Serve(m.ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ShutdownWithContext stops accepting and waits for open requests until ctx
// ends; connections still open then are closed, so a hung scrape cannot
// outlive the deadline. It is safe on a nil receiver, so Drain can be given a
// metrics server that was never started.
func (m *MetricsServer) ShutdownWithContext(ctx context.Context) error {
	if m == nil {
		return nil
	}
	err := m.srv.Shutdown(ctx)
	if err != nil {
		if closeErr := m.srv.Close(); closeErr != nil {
			err = errors.Join(err, closeErr)
		}
	}
	return err
}
