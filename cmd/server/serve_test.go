package main

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"

	"to-do-list/internal/transport/httpserver"
)

func TestServeAPI_LogsListeningOnlyAfterTheAddressIsBound(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer busy.Close()

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	served, err := serveAPI(fiber.New(fiber.Config{DisableStartupMessage: true}), busy.Addr().String(), log)
	if err == nil {
		t.Fatal("serveAPI() on a busy port returned no error")
	}
	if served != nil {
		t.Error("serveAPI() returned a serve channel although nothing is served")
	}
	if strings.Contains(logs.String(), "listening") {
		t.Errorf("logged listening for a port it could not bind:\n%s", logs.String())
	}
}

func TestServeAPI_LogsTheBoundAddressAndServes(t *testing.T) {
	app := fiber.New(fiber.Config{DisableStartupMessage: true})
	app.Get("/ping", func(c *fiber.Ctx) error { return c.SendString("pong") })

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))

	served, err := serveAPI(app, "127.0.0.1:0", log, "extra", "attr")
	if err != nil {
		t.Fatalf("serveAPI(): %v", err)
	}
	defer func() {
		_ = app.ShutdownWithContext(context.Background())
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("serve returned %v after shutdown", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the server did not stop after shutdown")
		}
	}()

	line := logs.String()
	if !strings.Contains(line, "msg=listening") || !strings.Contains(line, "extra=attr") {
		t.Fatalf("unexpected log: %s", line)
	}
	if strings.Contains(line, "address=127.0.0.1:0 ") {
		t.Fatalf("logged the requested port 0 instead of the bound one: %s", line)
	}

	addr := line[strings.Index(line, "address=")+len("address="):]
	addr = strings.Fields(addr)[0]

	resp, err := http.Get("http://" + addr + "/ping")
	if err != nil {
		t.Fatalf("GET the logged address: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "pong" {
		t.Errorf("body = %q, want pong", body)
	}
}

// The API port is taken while a scrape hangs on the metrics listener: startup
// must still fail within the grace period and drop the hung connection.
func TestStartAPI_StopsTheMetricsServerWithinTheGracePeriod(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	defer close(release)

	hang := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
	})
	metrics, err := httpserver.NewMetricsServer("127.0.0.1:0", "/metrics", hang)
	if err != nil {
		t.Fatalf("NewMetricsServer(): %v", err)
	}
	go func() { _ = metrics.Serve() }()

	scrape := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + metrics.Addr() + "/metrics")
		if err == nil {
			resp.Body.Close()
		}
		scrape <- err
	}()
	<-entered

	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("occupy a port: %v", err)
	}
	defer busy.Close()

	const grace = 200 * time.Millisecond
	started := time.Now()
	_, err = startAPI(fiber.New(fiber.Config{DisableStartupMessage: true}), busy.Addr().String(),
		metrics, grace, slog.New(slog.NewTextHandler(io.Discard, nil)))
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("startAPI() on a busy port returned no error")
	}
	if elapsed > grace+time.Second {
		t.Errorf("startAPI() took %v, want about the %v grace period", elapsed, grace)
	}

	select {
	case err := <-scrape:
		if err == nil {
			t.Error("the hung scrape completed normally; its connection should have been closed")
		}
	case <-time.After(2 * time.Second):
		t.Error("the hung scrape's connection is still open after the grace period")
	}
}
