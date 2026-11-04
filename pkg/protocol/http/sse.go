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

	"github.com/jasonczech/apexload/pkg/internal/contentdecode"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const defaultSSEStreamTimeout = 30 * time.Second

// defaultSSEMaxBodyBytes bounds how much of an SSE stream is buffered when the
// script sets neither max_events nor stop_on_event. The hard ceiling is not
// script-controlled: an unterminated line must remain bounded even when a
// script supplies zero, a negative value, or an arbitrarily large value.
const (
	defaultSSEMaxBodyBytes int64 = 8 << 20 // 8 MiB
	hardSSEMaxBodyBytes    int64 = 8 << 20 // 8 MiB
)

// StreamEndReason classifies why an SSE or NDJSON stream reader stopped.
type StreamEndReason string

const (
	EndReasonEOF             StreamEndReason = "eof"
	EndReasonStopOnEvent     StreamEndReason = "stop_on_event"
	EndReasonStopOnData      StreamEndReason = "stop_on_data"
	EndReasonMaxEvents       StreamEndReason = "max_events"
	EndReasonCallbackStop    StreamEndReason = "callback_stop"
	EndReasonTimeout         StreamEndReason = "timeout"
	EndReasonContextCanceled StreamEndReason = "context_canceled"
	EndReasonError           StreamEndReason = "error"
)

var errStreamStopRequested = errors.New("stream stop requested by observer")

type sseEvent struct {
	Event string
	Data  string
	ID    string
}

type sseReadResult struct {
	Body           []byte
	BytesRead      int64
	FirstEvent     time.Duration
	EventCount     int
	EndReason      StreamEndReason
	StoppedCleanly bool
}

type sseReadOptions struct {
	MaxEvents        int
	StopOnEvent      string
	StopOnData       string
	RequireStopEvent bool
	MaxBodyBytes     int64
	MaxRetainedBytes int64 // if > 0, caps retained Body bytes (sliding tail) while allowing BytesRead up to MaxBodyBytes
	Timeout          time.Duration
	OnEvent          func(event sseEvent, now time.Time) (stop bool, err error)
}

func resolveSSEReadOptions(params map[string]any, defaultTimeout time.Duration) (sseReadOptions, error) {
	if defaultTimeout <= 0 {
		defaultTimeout = defaultSSEStreamTimeout
	}
	timeout, timeoutErr := protocol.BoundedMilliseconds(params["stream_timeout_ms"], defaultTimeout, MaxConfiguredTimeout)
	if timeoutErr != nil {
		return sseReadOptions{}, fmt.Errorf("stream_timeout_ms %w", timeoutErr)
	}
	return sseReadOptions{
		MaxEvents:        sseParamInt(params, "max_events", 0),
		StopOnEvent:      strings.TrimSpace(sseParamString(params, "stop_on_event")),
		StopOnData:       strings.TrimSpace(sseParamString(params, "stop_on_data")),
		RequireStopEvent: sseParamBool(params, "require_stop_event", false),
		MaxBodyBytes:     sseMaxBodyBytes(params),
		Timeout:          timeout,
	}, nil
}

func shouldReadSSE(contentType string, params map[string]any) bool {
	if isSSEContentType(contentType) {
		return true
	}
	if params == nil {
		return false
	}
	if mode, ok := params["stream_mode"].(string); ok {
		switch strings.ToLower(strings.TrimSpace(mode)) {
		case "none", "false", "0", "no":
			return false
		case "sse", "event_stream", "text/event-stream":
			if b, hasStream := params["stream"].(bool); hasStream && !b {
				return false
			}
			return true
		}
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

func sseRequestedByParams(params map[string]any) bool {
	if params == nil {
		return false
	}
	if _, present := params["stream_timeout_ms"]; present {
		return true
	}
	return shouldReadSSE("", params) || shouldReadNDJSON("", params)
}

func isSSEContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		mediaType = strings.TrimSpace(strings.Split(value, ";")[0])
	}
	return strings.EqualFold(mediaType, "text/event-stream")
}

// trimSSEFieldValue strips at most one leading U+0020 SPACE after the colon,
// per the WHATWG Server-Sent Events specification.
func trimSSEFieldValue(raw string) string {
	if len(raw) > 0 && raw[0] == ' ' {
		return raw[1:]
	}
	return raw
}

func readSSEResponse(ctx context.Context, body io.ReadCloser, params map[string]any, start time.Time, leases ...*responseCaptureLease) (sseReadResult, error) {
	opts, err := resolveSSEReadOptions(params, defaultSSEStreamTimeout)
	if err != nil {
		return sseReadResult{EndReason: EndReasonError}, err
	}
	return readSSEResponseWithOptions(ctx, body, opts, start, leases...)
}

func readSSEResponseWithOptions(ctx context.Context, body io.ReadCloser, opts sseReadOptions, start time.Time, leases ...*responseCaptureLease) (sseReadResult, error) {
	maxBodyBytes := opts.MaxBodyBytes
	if maxBodyBytes <= 0 || maxBodyBytes > hardSSEMaxBodyBytes {
		maxBodyBytes = defaultSSEMaxBodyBytes
	}
	timeout := opts.Timeout

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

	// Bound the reader before ReadString can allocate a complete unterminated
	// SSE line. The normalized limit is always positive and is also the maximum
	// retained stream body.
	stream := io.LimitReader(body, maxBodyBytes+1)
	var captureLease *responseCaptureLease
	if len(leases) > 0 {
		captureLease = leases[0]
		stream = &responseCaptureReader{reader: stream, lease: captureLease}
	}
	reader := bufio.NewReader(stream)
	var out bytes.Buffer
	var result sseReadResult
	var current sseEvent
	dataLines := make([]string, 0, 4)
	var dispatchErr error
	observe := protocol.StreamChunkObserver(ctx)

	appendRetainedEvent := func(ev sseEvent, encodedSize int64) bool {
		if opts.MaxRetainedBytes > 0 {
			// Sliding tail retention for bounded raw tail (e.g. 64 KiB in LLM mode).
			if encodedSize > opts.MaxRetainedBytes {
				out.Reset()
				var tmp bytes.Buffer
				writeSSEEvent(&tmp, ev)
				b := tmp.Bytes()
				out.Write(b[int64(len(b))-opts.MaxRetainedBytes:])
				return true
			}
			if int64(out.Len())+encodedSize > opts.MaxRetainedBytes {
				keep := int(opts.MaxRetainedBytes - encodedSize)
				existing := out.Bytes()
				if keep <= 0 {
					out.Reset()
				} else if keep < len(existing) {
					shifted := append([]byte(nil), existing[len(existing)-keep:]...)
					out.Reset()
					out.Write(shifted)
				}
			}
			writeSSEEvent(&out, ev)
			return true
		}
		if encodedSize > maxBodyBytes-int64(out.Len()) {
			dispatchErr = fmt.Errorf("SSE retained event body exceeds %d byte limit", maxBodyBytes)
			return false
		}
		if !captureLease.reserve(encodedSize) {
			dispatchErr = captureLease.exhaustedError()
			return false
		}
		writeSSEEvent(&out, ev)
		return true
	}

	dispatch := func() bool {
		if current.Event == "" && current.ID == "" && len(dataLines) == 0 {
			return false
		}
		if len(dataLines) == 1 {
			current.Data = dataLines[0]
		} else if len(dataLines) > 1 {
			current.Data = strings.Join(dataLines, "\n")
		}
		encodedSize := sseEncodedEventSize(current)
		if !appendRetainedEvent(current, encodedSize) {
			result.EndReason = EndReasonError
			return true
		}
		if observe != nil {
			observe(current.Data)
		}
		now := time.Now()
		result.EventCount++
		if result.FirstEvent == 0 {
			result.FirstEvent = now.Sub(start)
		}
		stop := false
		if opts.OnEvent != nil {
			cbStop, cbErr := opts.OnEvent(current, now)
			if cbErr != nil {
				dispatchErr = cbErr
				result.EndReason = EndReasonError
				current = sseEvent{}
				dataLines = dataLines[:0]
				return true
			}
			if cbStop {
				stop = true
				result.EndReason = EndReasonCallbackStop
				result.StoppedCleanly = true
			}
		}
		if !stop && opts.StopOnData != "" && (current.Data == opts.StopOnData || strings.TrimSpace(current.Data) == opts.StopOnData) {
			stop = true
			result.EndReason = EndReasonStopOnData
			result.StoppedCleanly = true
		}
		if !stop && opts.StopOnEvent != "" && current.Event == opts.StopOnEvent {
			stop = true
			result.EndReason = EndReasonStopOnEvent
			result.StoppedCleanly = true
		}
		if !stop && opts.MaxEvents > 0 && result.EventCount >= opts.MaxEvents {
			stop = true
			result.EndReason = EndReasonMaxEvents
			result.StoppedCleanly = !opts.RequireStopEvent
		}
		current = sseEvent{}
		dataLines = dataLines[:0]
		return stop
	}

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			result.BytesRead += int64(len(line))
			if result.BytesRead > maxBodyBytes {
				result.Body = out.Bytes()
				result.EndReason = EndReasonError
				return result, fmt.Errorf("SSE response exceeds %d byte limit", maxBodyBytes)
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if dispatch() {
					result.Body = out.Bytes()
					if dispatchErr != nil {
						return result, dispatchErr
					}
					if opts.RequireStopEvent && !result.StoppedCleanly {
						return result, fmt.Errorf("SSE stream stopped at %s before required stop event", result.EndReason)
					}
					return result, nil
				}
			case strings.HasPrefix(line, ":"):
				continue
			case strings.HasPrefix(line, "event:"):
				current.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				dataLines = append(dataLines, trimSSEFieldValue(strings.TrimPrefix(line, "data:")))
			case strings.HasPrefix(line, "id:"):
				current.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			}
		}
		if err != nil {
			if len(dataLines) > 0 || current.Event != "" || current.ID != "" {
				dispatch()
			}
			result.Body = out.Bytes()
			if dispatchErr != nil {
				return result, dispatchErr
			}
			if result.StoppedCleanly {
				return result, nil
			}
			switch {
			case errors.Is(err, io.EOF):
				result.EndReason = EndReasonEOF
				if opts.RequireStopEvent {
					return result, fmt.Errorf("SSE stream ended before required stop event")
				}
				result.StoppedCleanly = true
				return result, nil
			case timedOut.Load():
				result.EndReason = EndReasonTimeout
				if opts.RequireStopEvent {
					return result, fmt.Errorf("SSE stream timed out after %s before required stop event", timeout)
				}
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
				result.EndReason = EndReasonContextCanceled
				return result, ctx.Err()
			case contentdecode.Kind(err) != "":
				result.EndReason = EndReasonError
				return result, err // A malformed compressed trailer is not a collection window.
			default:
				if isSSEPrematureReadTimeout(err) {
					result.EndReason = EndReasonError
					return result, err
				}
				if opts.RequireStopEvent {
					result.EndReason = EndReasonError
					return result, err
				}
				if result.EventCount > 0 {
					result.EndReason = EndReasonEOF
					return result, nil
				}
				result.EndReason = EndReasonError
				return result, err
			}
		}
	}
}

// sseEncodedEventSize computes the canonical serialized byte length of event
// without allocating a slice via strings.Split.
func sseEncodedEventSize(event sseEvent) int64 {
	size := int64(1) // terminating blank line
	if event.Event != "" {
		size += int64(len("event: ") + len(event.Event) + 1)
	}
	if event.ID != "" {
		size += int64(len("id: ") + len(event.ID) + 1)
	}
	if event.Data != "" {
		lines := int64(1 + strings.Count(event.Data, "\n"))
		size += lines*int64(len("data: ")) + int64(len(event.Data)) + 1
	}
	return size
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

// writeSSEEvent writes event in canonical SSE format without allocating a slice via strings.Split.
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
		rest := event.Data
		for {
			nl := strings.IndexByte(rest, '\n')
			if nl < 0 {
				out.WriteString("data: ")
				out.WriteString(rest)
				out.WriteByte('\n')
				break
			}
			out.WriteString("data: ")
			out.WriteString(rest[:nl])
			out.WriteByte('\n')
			rest = rest[nl+1:]
		}
	}
	out.WriteByte('\n')
}

func parseSSEEvents(body []byte) []sseEvent {
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
	rest := string(body)
	for len(rest) > 0 {
		var line string
		if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
			line = rest[:nl]
			rest = rest[nl+1:]
		} else {
			line = rest
			rest = ""
		}
		line = strings.TrimSuffix(line, "\r")
		switch {
		case line == "":
			dispatch()
		case strings.HasPrefix(line, ":"):
			continue
		case strings.HasPrefix(line, "event:"):
			current.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			dataLines = append(dataLines, trimSSEFieldValue(strings.TrimPrefix(line, "data:")))
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

// sseMaxBodyBytes resolves the SSE buffer cap. The action can lower the cap but
// cannot disable or exceed the operator-owned hard ceiling.
func sseMaxBodyBytes(params map[string]any) int64 {
	if params != nil {
		if _, ok := params["max_body_bytes"]; ok {
			requested := int64(sseParamInt(params, "max_body_bytes", 0))
			if requested > 0 && requested < hardSSEMaxBodyBytes {
				return requested
			}
			return hardSSEMaxBodyBytes
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

func sseParamBool(params map[string]any, key string, fallback bool) bool {
	if params == nil {
		return fallback
	}
	switch value := params[key].(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "1", "yes":
			return true
		case "false", "0", "no":
			return false
		}
	}
	return fallback
}
