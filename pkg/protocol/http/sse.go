package http

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const defaultSSEStreamTimeout = 30 * time.Second

// defaultSSEMaxBodyBytes bounds how much of an SSE stream is buffered when the
// script sets neither max_events nor stop_on_event. Without a bound a long-lived
// or never-terminating stream grows the in-memory buffer without limit, and a
// VUser looping over such a read exhausts memory. Authors can raise it (or set
// max_body_bytes <= 0 to opt out) via the action params.
const defaultSSEMaxBodyBytes = 8 << 20 // 8 MiB

type sseEvent struct {
	Event string
	Data  string
	ID    string
}

type sseReadResult struct {
	Body       []byte
	BytesRead  int64
	FirstEvent time.Duration
	EventCount int
}

func shouldReadSSE(contentType string, params map[string]any) bool {
	if isSSEContentType(contentType) {
		return true
	}
	value, ok := params["stream"]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "sse", "event_stream", "text/event-stream":
			return true
		}
	}
	return false
}

func isSSEContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		mediaType = strings.TrimSpace(strings.Split(value, ";")[0])
	}
	return strings.EqualFold(mediaType, "text/event-stream")
}

func readSSEResponse(ctx context.Context, body io.ReadCloser, params map[string]any, start time.Time) (sseReadResult, error) {
	maxEvents := sseParamInt(params, "max_events", 0)
	stopOnEvent := strings.TrimSpace(sseParamString(params, "stop_on_event"))
	maxBodyBytes := sseMaxBodyBytes(params)
	timeout := time.Duration(sseParamInt(params, "stream_timeout_ms", int(defaultSSEStreamTimeout/time.Millisecond))) * time.Millisecond

	var timedOut atomic.Bool
	done := make(chan struct{})
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		go func() {
			select {
			case <-ctx.Done():
				_ = body.Close()
			case <-timer.C:
				timedOut.Store(true)
				_ = body.Close()
			case <-done:
			}
		}()
	} else {
		go func() {
			select {
			case <-ctx.Done():
				_ = body.Close()
			case <-done:
			}
		}()
	}
	defer close(done)

	reader := bufio.NewReader(body)
	var out bytes.Buffer
	var result sseReadResult
	var current sseEvent
	var dataLines []string
	dispatch := func() bool {
		if current.Event == "" && current.ID == "" && len(dataLines) == 0 {
			return false
		}
		current.Data = strings.Join(dataLines, "\n")
		writeSSEEvent(&out, current)
		result.EventCount++
		if result.FirstEvent == 0 {
			result.FirstEvent = time.Since(start)
		}
		stop := false
		if maxEvents > 0 && result.EventCount >= maxEvents {
			stop = true
		}
		if stopOnEvent != "" && current.Event == stopOnEvent {
			stop = true
		}
		current = sseEvent{}
		dataLines = nil
		return stop
	}

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			result.BytesRead += int64(len(line))
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if dispatch() {
					result.Body = out.Bytes()
					return result, nil
				}
			case strings.HasPrefix(line, ":"):
				continue
			case strings.HasPrefix(line, "event:"):
				current.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			case strings.HasPrefix(line, "id:"):
				current.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			}
			if maxBodyBytes > 0 && result.BytesRead >= maxBodyBytes {
				// Buffer bound reached: flush any in-progress event and stop
				// reading so an unbounded stream cannot exhaust memory.
				dispatch()
				result.Body = out.Bytes()
				return result, nil
			}
		}
		if err != nil {
			if len(dataLines) > 0 || current.Event != "" || current.ID != "" {
				dispatch()
			}
			result.Body = out.Bytes()
			switch {
			case errors.Is(err, io.EOF):
				return result, nil
			case timedOut.Load():
				// stream_timeout_ms is a bounded collection window: reaching it
				// after collecting events is a successful end-of-collection (the
				// standard SSE sampling pattern against an endpoint that streams
				// indefinitely), not a failure. Only a window that captured
				// nothing is reported as a timeout error.
				if result.EventCount > 0 {
					return result, nil
				}
				return result, fmt.Errorf("SSE stream timed out after %s", timeout)
			case ctx.Err() != nil:
				return result, ctx.Err()
			default:
				// A premature CLIENT/transport read timeout (http.Client.Timeout, a
				// read deadline, a mid-stream connection reset) is NOT the intended
				// stream_timeout_ms collection window (handled above) — it truncates
				// the sample unexpectedly, so it must surface as an error even when
				// some events were collected. Only after ruling that out does an
				// events-collected result count as a successful read.
				if isSSEPrematureReadTimeout(err) {
					return result, err
				}
				if result.EventCount > 0 {
					return result, nil
				}
				return result, err
			}
		}
	}
}

func isSSEPrematureReadTimeout(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "client.timeout") ||
		strings.Contains(lower, "context deadline exceeded") ||
		strings.Contains(lower, "timeout awaiting response")
}

func writeSSEEvent(out *bytes.Buffer, event sseEvent) {
	if event.Event != "" {
		out.WriteString("event: ")
		out.WriteString(event.Event)
		out.WriteByte('\n')
	}
	if event.ID != "" {
		out.WriteString("id: ")
		out.WriteString(event.ID)
		out.WriteByte('\n')
	}
	if event.Data != "" {
		for _, line := range strings.Split(event.Data, "\n") {
			out.WriteString("data: ")
			out.WriteString(line)
			out.WriteByte('\n')
		}
	}
	out.WriteByte('\n')
}

func parseSSEEvents(body []byte) []sseEvent {
	lines := strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n")
	events := make([]sseEvent, 0)
	var current sseEvent
	var dataLines []string
	dispatch := func() {
		if current.Event == "" && current.ID == "" && len(dataLines) == 0 {
			return
		}
		current.Data = strings.Join(dataLines, "\n")
		events = append(events, current)
		current = sseEvent{}
		dataLines = nil
	}
	for _, line := range lines {
		switch {
		case line == "":
			dispatch()
		case strings.HasPrefix(line, ":"):
			continue
		case strings.HasPrefix(line, "event:"):
			current.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case strings.HasPrefix(line, "id:"):
			current.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		}
	}
	dispatch()
	return events
}

func extractSSEConcat(expr string, body []byte) (string, error) {
	eventName, _ := parseSSEExtractorExpression(expr)
	events := parseSSEEvents(body)
	values := make([]string, 0, len(events))
	for _, event := range events {
		if eventName == "" || event.Event == eventName {
			values = append(values, event.Data)
		}
	}
	if len(values) == 0 {
		return "", fmt.Errorf("no SSE events matched %q", expr)
	}
	return strings.Join(values, "\n"), nil
}

func extractSSEEvent(expr string, body []byte) (string, error) {
	eventName, ordinal := parseSSEExtractorExpression(expr)
	if ordinal <= 0 {
		ordinal = 1
	}
	seen := 0
	for _, event := range parseSSEEvents(body) {
		if eventName != "" && event.Event != eventName {
			continue
		}
		seen++
		if seen == ordinal {
			return event.Data, nil
		}
	}
	return "", fmt.Errorf("SSE event %q ordinal %d not found", eventName, ordinal)
}

func parseSSEExtractorExpression(expr string) (string, int) {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return "", 1
	}
	if strings.HasPrefix(trimmed, "event=") {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "event="))
	}
	ordinal := 1
	if open := strings.LastIndex(trimmed, "["); open >= 0 && strings.HasSuffix(trimmed, "]") {
		if parsed, err := strconv.Atoi(strings.TrimSuffix(trimmed[open+1:], "]")); err == nil {
			ordinal = parsed
			trimmed = strings.TrimSpace(trimmed[:open])
		}
	}
	return trimmed, ordinal
}

// sseMaxBodyBytes resolves the SSE buffer cap. When max_body_bytes is unset the
// default cap applies; a value <= 0 disables the cap (unbounded, opt-in).
func sseMaxBodyBytes(params map[string]any) int64 {
	if params != nil {
		if _, ok := params["max_body_bytes"]; ok {
			return int64(sseParamInt(params, "max_body_bytes", 0))
		}
	}
	return defaultSSEMaxBodyBytes
}

func sseParamString(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	raw, ok := params[key]
	if !ok || raw == nil {
		return ""
	}
	switch value := raw.(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return fmt.Sprint(value)
	}
}

func sseParamInt(params map[string]any, key string, fallback int) int {
	if params == nil {
		return fallback
	}
	switch value := params[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, err := strconv.Atoi(value.String())
		if err == nil {
			return parsed
		}
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			return parsed
		}
	}
	return fallback
}
