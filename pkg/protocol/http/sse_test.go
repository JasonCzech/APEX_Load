package http

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

func readSSEFromString(t *testing.T, payload string, params map[string]any) sseReadResult {
	t.Helper()
	body := io.NopCloser(strings.NewReader(payload))
	res, err := readSSEResponse(context.Background(), body, params, time.Now())
	if err != nil {
		t.Fatalf("readSSEResponse returned error: %v", err)
	}
	return res
}

func TestReadSSEResponseBoundsBufferBytes(t *testing.T) {
	// 500 small events, ~7500 bytes total, but a 150-byte cap should stop the
	// read long before the stream ends so an unbounded stream cannot OOM.
	payload := strings.Repeat("data: payload\n\n", 500)
	body := io.NopCloser(strings.NewReader(payload))
	res, err := readSSEResponse(context.Background(), body, map[string]any{"max_body_bytes": 150}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "exceeds 150 byte limit") {
		t.Fatalf("readSSEResponse error = %v, want byte-limit error", err)
	}

	if res.BytesRead >= int64(len(payload)) {
		t.Fatalf("BytesRead = %d, want bounded below full stream length %d", res.BytesRead, len(payload))
	}
	if res.BytesRead > 256 {
		t.Fatalf("BytesRead = %d, want close to the 150-byte cap", res.BytesRead)
	}
	if res.EventCount == 0 || res.EventCount >= 500 {
		t.Fatalf("EventCount = %d, want only complete events below 500", res.EventCount)
	}
	if !bytes.HasSuffix(res.Body, []byte("\n\n")) {
		t.Fatalf("body ends with partial SSE event: %q", res.Body[len(res.Body)-minInt(len(res.Body), 32):])
	}
}

func TestReadSSEResponseBoundsUnterminatedLine(t *testing.T) {
	payload := "data: " + strings.Repeat("x", 1<<20)
	body := io.NopCloser(strings.NewReader(payload))
	res, err := readSSEResponse(context.Background(), body, map[string]any{"max_body_bytes": 1024}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "exceeds 1024 byte limit") {
		t.Fatalf("readSSEResponse error = %v, want byte-limit error", err)
	}
	if res.BytesRead > 1025 {
		t.Fatalf("BytesRead = %d, want <= 1025 (cap plus overflow sentinel)", res.BytesRead)
	}
}

func TestReadSSEResponseBoundsCanonicalizedEventOutput(t *testing.T) {
	payload := strings.Repeat("data:x\n\n", 10)
	result, err := readSSEResponse(context.Background(), io.NopCloser(strings.NewReader(payload)), map[string]any{"max_body_bytes": len(payload)}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "retained event body") {
		t.Fatalf("canonical output error = %v", err)
	}
	if len(result.Body) > len(payload) {
		t.Fatalf("retained body = %d bytes, cap %d", len(result.Body), len(payload))
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestReadSSEResponseNonPositiveBodyLimitUsesHardCap(t *testing.T) {
	payload := strings.Repeat("data: payload\n\n", 50)
	// The finite stream fits below the hard cap. A negative script value cannot
	// disable the cap, but otherwise preserves normal behavior.
	res := readSSEFromString(t, payload, map[string]any{"max_body_bytes": -1})
	if res.EventCount != 50 {
		t.Fatalf("EventCount = %d, want all 50 events read when cap disabled", res.EventCount)
	}
	if res.BytesRead != int64(len(payload)) {
		t.Fatalf("BytesRead = %d, want full length %d", res.BytesRead, len(payload))
	}
}

func TestReadSSEResponseMultilineData(t *testing.T) {
	payload := "event: message\ndata: line one\ndata: line two\n\n"
	res := readSSEFromString(t, payload, nil)
	if res.EventCount != 1 {
		t.Fatalf("EventCount = %d, want 1", res.EventCount)
	}
	events := parseSSEEvents(res.Body)
	if len(events) != 1 {
		t.Fatalf("parsed events = %#v, want 1", events)
	}
	if events[0].Event != "message" {
		t.Fatalf("event name = %q, want message", events[0].Event)
	}
	if events[0].Data != "line one\nline two" {
		t.Fatalf("data = %q, want both lines joined with newline", events[0].Data)
	}
}

func TestReadSSEResponseStopOnEvent(t *testing.T) {
	payload := "event: progress\ndata: 1\n\nevent: done\ndata: ok\n\nevent: progress\ndata: 2\n\n"
	res := readSSEFromString(t, payload, map[string]any{"stop_on_event": "done"})
	// Reading stops once the "done" event is dispatched, so the trailing
	// progress event is never consumed.
	if res.EventCount != 2 {
		t.Fatalf("EventCount = %d, want 2 (stops after 'done')", res.EventCount)
	}
}

func TestReadSSEResponseMaxEvents(t *testing.T) {
	payload := strings.Repeat("data: x\n\n", 10)
	res := readSSEFromString(t, payload, map[string]any{"max_events": 3})
	if res.EventCount != 3 {
		t.Fatalf("EventCount = %d, want 3", res.EventCount)
	}
}

func TestReadSSEResponseStreamTimeoutAfterEventsSucceeds(t *testing.T) {
	// stream_timeout_ms is a bounded collection window: reaching it after
	// collecting at least one event is a successful end-of-collection, not a
	// failure (the standard SSE sampling pattern against an endpoint that
	// streams indefinitely).
	reader, writer := io.Pipe()
	defer writer.Close()
	go func() {
		_, _ = writer.Write([]byte("event: token\ndata: hello\n\n"))
		time.Sleep(150 * time.Millisecond)
		_ = writer.Close()
	}()

	res, err := readSSEResponse(context.Background(), reader, map[string]any{
		"stream_timeout_ms": float64(20),
		"max_body_bytes":    float64(-1),
	}, time.Now())
	if err != nil {
		t.Fatalf("readSSEResponse error = %v, want success (window elapsed with events)", err)
	}
	if res.EventCount != 1 {
		t.Fatalf("EventCount = %d, want collected event retained", res.EventCount)
	}
}

func TestReadSSEResponseStreamTimeoutWithNoEventsFails(t *testing.T) {
	// A window that captured nothing is still reported as a timeout error.
	reader, writer := io.Pipe()
	defer writer.Close()
	go func() {
		// Send only a comment/keep-alive (no dispatchable event), then stall.
		_, _ = writer.Write([]byte(": keep-alive\n"))
		time.Sleep(150 * time.Millisecond)
		_ = writer.Close()
	}()

	res, err := readSSEResponse(context.Background(), reader, map[string]any{
		"stream_timeout_ms": float64(20),
		"max_body_bytes":    float64(-1),
	}, time.Now())
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("readSSEResponse error = %v, want timeout (no events collected)", err)
	}
	if res.EventCount != 0 {
		t.Fatalf("EventCount = %d, want 0", res.EventCount)
	}
}

func TestReadSSEResponseClientTimeoutAfterEventsFails(t *testing.T) {
	// A premature CLIENT/transport read timeout (distinct from the intended
	// stream_timeout_ms window, which is 1000ms here and never fires) truncates
	// the sample unexpectedly and must surface as an error even though one event
	// was already collected — otherwise a mid-stream reset masquerades as success.
	body := &timeoutAfterPayloadReadCloser{reader: strings.NewReader("event: token\ndata: hello\n\n")}
	res, err := readSSEResponse(context.Background(), body, map[string]any{
		"stream_timeout_ms": float64(1000),
		"max_body_bytes":    float64(-1),
	}, time.Now())
	if err == nil {
		t.Fatalf("readSSEResponse error = nil, want a client/transport read-timeout error")
	}
	if res.EventCount != 1 {
		t.Fatalf("EventCount = %d, want the collected event retained on the result", res.EventCount)
	}
}

func TestSSEMaxBodyBytesDefault(t *testing.T) {
	if got := sseMaxBodyBytes(nil); got != defaultSSEMaxBodyBytes {
		t.Fatalf("default cap = %d, want %d", got, defaultSSEMaxBodyBytes)
	}
	if got := sseMaxBodyBytes(map[string]any{"max_body_bytes": 0}); got != hardSSEMaxBodyBytes {
		t.Fatalf("explicit 0 cap = %d, want hard cap %d", got, hardSSEMaxBodyBytes)
	}
	if got := sseMaxBodyBytes(map[string]any{"max_body_bytes": 4096}); got != 4096 {
		t.Fatalf("explicit cap = %d, want 4096", got)
	}
	if got := sseMaxBodyBytes(map[string]any{"max_body_bytes": hardSSEMaxBodyBytes + 1}); got != hardSSEMaxBodyBytes {
		t.Fatalf("oversized action cap = %d, want hard cap %d", got, hardSSEMaxBodyBytes)
	}
}

func TestExtractSSEEventOrdinal(t *testing.T) {
	payload := "event: tick\ndata: first\n\nevent: tick\ndata: second\n\n"
	res := readSSEFromString(t, payload, nil)
	got, err := extractSSEEvent("event=tick[2]", res.Body)
	if err != nil {
		t.Fatalf("extractSSEEvent: %v", err)
	}
	if got != "second" {
		t.Fatalf("ordinal[2] = %q, want second", got)
	}
}

type timeoutAfterPayloadReadCloser struct {
	reader *strings.Reader
}

func (r *timeoutAfterPayloadReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		return n, nil
	}
	if err == io.EOF {
		return 0, context.DeadlineExceeded
	}
	return n, err
}

func (r *timeoutAfterPayloadReadCloser) Close() error {
	return nil
}
