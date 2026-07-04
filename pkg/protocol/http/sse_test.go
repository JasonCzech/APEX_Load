package http

import (
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
	res := readSSEFromString(t, payload, map[string]any{"max_body_bytes": 150})

	if res.BytesRead >= int64(len(payload)) {
		t.Fatalf("BytesRead = %d, want bounded below full stream length %d", res.BytesRead, len(payload))
	}
	if res.BytesRead > 256 {
		t.Fatalf("BytesRead = %d, want close to the 150-byte cap", res.BytesRead)
	}
	if res.EventCount == 0 || res.EventCount >= 500 {
		t.Fatalf("EventCount = %d, want a partial count below 500", res.EventCount)
	}
}

func TestReadSSEResponseMaxBodyBytesOptOut(t *testing.T) {
	payload := strings.Repeat("data: payload\n\n", 50)
	// A non-positive cap disables bounding: the whole finite stream is read.
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

func TestReadSSEResponseTimeoutAfterPartialEventFails(t *testing.T) {
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
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("readSSEResponse error = %v, want timeout", err)
	}
	if res.EventCount != 1 {
		t.Fatalf("EventCount = %d, want partial event count retained", res.EventCount)
	}
}

func TestReadSSEResponseClientTimeoutAfterPartialEventFails(t *testing.T) {
	body := &timeoutAfterPayloadReadCloser{reader: strings.NewReader("event: token\ndata: hello\n\n")}
	res, err := readSSEResponse(context.Background(), body, map[string]any{
		"stream_timeout_ms": float64(1000),
		"max_body_bytes":    float64(-1),
	}, time.Now())
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "deadline") {
		t.Fatalf("readSSEResponse error = %v, want client/body deadline", err)
	}
	if res.EventCount != 1 {
		t.Fatalf("EventCount = %d, want partial event count retained", res.EventCount)
	}
}

func TestSSEMaxBodyBytesDefault(t *testing.T) {
	if got := sseMaxBodyBytes(nil); got != defaultSSEMaxBodyBytes {
		t.Fatalf("default cap = %d, want %d", got, defaultSSEMaxBodyBytes)
	}
	if got := sseMaxBodyBytes(map[string]any{"max_body_bytes": 0}); got != 0 {
		t.Fatalf("explicit 0 cap = %d, want 0 (unbounded opt-out)", got)
	}
	if got := sseMaxBodyBytes(map[string]any{"max_body_bytes": 4096}); got != 4096 {
		t.Fatalf("explicit cap = %d, want 4096", got)
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
