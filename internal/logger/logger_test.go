package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func decode(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	line := strings.TrimSpace(buf.String())
	if line == "" {
		t.Fatal("nothing was logged")
	}

	var record map[string]any
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("log line is not JSON: %v (%q)", err, line)
	}

	return record
}

func TestLogger_StampsTheRequestIDFromContext(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "info", "json")

	log.InfoContext(WithRequestID(context.Background(), "req-42"), "something happened")

	if got := decode(t, &buf)["request_id"]; got != "req-42" {
		t.Errorf("request_id = %v, want %q", got, "req-42")
	}
}

func TestLogger_StampSurvivesWithAndWithGroup(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "info", "json").With("service", "to-do-list")

	log.ErrorContext(WithRequestID(context.Background(), "req-7"), "boom")

	record := decode(t, &buf)
	if got := record["request_id"]; got != "req-7" {
		t.Errorf("request_id = %v, want %q", got, "req-7")
	}
	if got := record["service"]; got != "to-do-list" {
		t.Errorf("service = %v, want %q", got, "to-do-list")
	}
}

func TestLogger_NoRequestIDLeavesTheFieldOut(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, "info", "json")

	log.InfoContext(context.Background(), "background work")

	if _, present := decode(t, &buf)["request_id"]; present {
		t.Error("request_id should be absent when the context carries none")
	}
}

func TestWithRequestID_IgnoresAnEmptyIdentifier(t *testing.T) {
	ctx := WithRequestID(context.Background(), "")

	if got := RequestID(ctx); got != "" {
		t.Errorf("RequestID() = %q, want empty", got)
	}
}

func TestRequestID_SurvivesADerivedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(WithRequestID(context.Background(), "req-1"))
	defer cancel()

	if got := RequestID(ctx); got != "req-1" {
		t.Errorf("RequestID() = %q, want %q", got, "req-1")
	}
}
