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

type MetricsServer struct {
	srv *http.Server
	ln  net.Listener
}

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
