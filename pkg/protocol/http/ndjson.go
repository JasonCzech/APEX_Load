package http

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/contentdecode"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func shouldReadNDJSON(contentType string, params map[string]any) bool {
	if isNDJSONContentType(contentType) {
		return true
	}
	if params == nil {
		return false
	}
	for _, key := range []string{"stream", "stream_mode"} {
		if v, ok := params[key].(string); ok {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "ndjson", "jsonl", "application/x-ndjson", "application/ndjson", "application/jsonl":
				return true
			}
		}
	}
	return false
}

func isNDJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		mediaType = strings.TrimSpace(strings.Split(value, ";")[0])
	}
	switch strings.ToLower(mediaType) {
	case "application/x-ndjson", "application/ndjson", "application/jsonl", "application/x-jsonlines":
		return true
	default:
		return false
	}
}

func readNDJSONResponse(ctx context.Context, body io.ReadCloser, params map[string]any, start time.Time, leases ...*responseCaptureLease) (sseReadResult, error) {
	opts, err := resolveSSEReadOptions(params, defaultSSEStreamTimeout)
	if err != nil {
		return sseReadResult{EndReason: EndReasonError}, err
	}
	return readNDJSONResponseWithOptions(ctx, body, opts, start, leases...)
}

func readNDJSONResponseWithOptions(ctx context.Context, body io.ReadCloser, opts sseReadOptions, start time.Time, leases ...*responseCaptureLease) (sseReadResult, error) {
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

	stream := io.LimitReader(body, maxBodyBytes+1)
	var captureLease *responseCaptureLease
	if len(leases) > 0 {
		captureLease = leases[0]
		stream = &responseCaptureReader{reader: stream, lease: captureLease}
	}
	reader := bufio.NewReader(stream)
	var out bytes.Buffer
	var result sseReadResult
	observe := protocol.StreamChunkObserver(ctx)

	appendLine := func(line string) error {
		encodedSize := int64(len(line) + 1)
		if opts.MaxRetainedBytes > 0 {
			if encodedSize > opts.MaxRetainedBytes {
				out.Reset()
				tail := line[int64(len(line))-(opts.MaxRetainedBytes-1):]
				out.WriteString(tail)
				out.WriteByte('\n')
				return nil
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
			out.WriteString(line)
			out.WriteByte('\n')
			return nil
		}
		if encodedSize > maxBodyBytes-int64(out.Len()) {
			return fmt.Errorf("NDJSON retained body exceeds %d byte limit", maxBodyBytes)
		}
		if !captureLease.reserve(encodedSize) {
			return captureLease.exhaustedError()
		}
		out.WriteString(line)
		out.WriteByte('\n')
		return nil
	}

	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			result.BytesRead += int64(len(line))
			if result.BytesRead > maxBodyBytes {
				result.Body = out.Bytes()
				result.EndReason = EndReasonError
				return result, fmt.Errorf("NDJSON response exceeds %d byte limit", maxBodyBytes)
			}
			trimmed := strings.TrimRight(line, "\r\n")
			if strings.TrimSpace(trimmed) != "" {
				if appendErr := appendLine(trimmed); appendErr != nil {
					result.Body = out.Bytes()
					result.EndReason = EndReasonError
					return result, appendErr
				}
				if observe != nil {
					observe(trimmed)
				}
				now := time.Now()
				result.EventCount++
				if result.FirstEvent == 0 {
					result.FirstEvent = now.Sub(start)
				}
				ev := sseEvent{Data: trimmed}
				stop := false
				if opts.OnEvent != nil {
					cbStop, cbErr := opts.OnEvent(ev, now)
					if cbErr != nil {
						result.Body = out.Bytes()
						result.EndReason = EndReasonError
						return result, cbErr
					}
					if cbStop {
						stop = true
						result.EndReason = EndReasonCallbackStop
						result.StoppedCleanly = true
					}
				}
				if !stop && opts.StopOnData != "" && (trimmed == opts.StopOnData || strings.TrimSpace(trimmed) == opts.StopOnData) {
					stop = true
					result.EndReason = EndReasonStopOnData
					result.StoppedCleanly = true
				}
				if !stop && opts.MaxEvents > 0 && result.EventCount >= opts.MaxEvents {
					stop = true
					result.EndReason = EndReasonMaxEvents
					result.StoppedCleanly = !opts.RequireStopEvent
				}
				if stop {
					result.Body = out.Bytes()
					if opts.RequireStopEvent && !result.StoppedCleanly {
						return result, fmt.Errorf("NDJSON stream stopped at %s before required stop event", result.EndReason)
					}
					return result, nil
				}
			}
		}
		if err != nil {
			result.Body = out.Bytes()
			if result.StoppedCleanly {
				return result, nil
			}
			switch {
			case errors.Is(err, io.EOF):
				result.EndReason = EndReasonEOF
				if opts.RequireStopEvent {
					return result, fmt.Errorf("NDJSON stream ended before required stop event")
				}
				result.StoppedCleanly = true
				return result, nil
			case timedOut.Load():
				result.EndReason = EndReasonTimeout
				if opts.RequireStopEvent {
					return result, fmt.Errorf("NDJSON stream timed out after %s before required stop event", timeout)
				}
				if result.EventCount > 0 {
					return result, nil
				}
				return result, fmt.Errorf("NDJSON stream timed out after %s", timeout)
			case ctx.Err() != nil:
				result.EndReason = EndReasonContextCanceled
				return result, ctx.Err()
			case contentdecode.Kind(err) != "":
				result.EndReason = EndReasonError
				return result, err
			default:
				if isSSEPrematureReadTimeout(err) || opts.RequireStopEvent {
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
