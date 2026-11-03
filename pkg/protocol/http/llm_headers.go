package http

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// parseRetryAfterHeader extracts a retry wait duration from HTTP response headers,
// preferring millisecond headers (x-ms-retry-after-ms, retry-after-ms) over
// Retry-After (integer seconds or HTTP-date).
func parseRetryAfterHeader(headers http.Header, now time.Time) time.Duration {
	if len(headers) == 0 {
		return 0
	}
	for _, key := range []string{"x-ms-retry-after-ms", "retry-after-ms"} {
		if raw := strings.TrimSpace(headers.Get(key)); raw != "" {
			if ms, err := strconv.ParseFloat(raw, 64); err == nil && ms > 0 {
				return time.Duration(ms * float64(time.Millisecond))
			}
		}
	}
	raw := strings.TrimSpace(headers.Get("Retry-After"))
	if raw == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(raw, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	if parsed, err := http.ParseTime(raw); err == nil {
		if now.IsZero() {
			now = time.Now()
		}
		if d := parsed.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// applyLLMResponseHeaders populates rate-limit, region, and server timing headers
// onto diag.
func applyLLMResponseHeaders(diag *protocol.LLMDiagnostics, headers http.Header) {
	if diag == nil || len(headers) == 0 {
		return
	}
	for _, key := range []string{"x-ratelimit-remaining-requests", "x-ms-ratelimit-remaining-requests"} {
		if raw := strings.TrimSpace(headers.Get(key)); raw != "" {
			if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v >= 0 {
				diag.RateLimitRemainingRequests = &v
				break
			}
		}
	}
	for _, key := range []string{"x-ratelimit-remaining-tokens", "x-ms-ratelimit-remaining-tokens"} {
		if raw := strings.TrimSpace(headers.Get(key)); raw != "" {
			if v, err := strconv.ParseInt(raw, 10, 64); err == nil && v >= 0 {
				diag.RateLimitRemainingTokens = &v
				break
			}
		}
	}
	if region := strings.TrimSpace(headers.Get("x-ms-region")); region != "" && len(region) <= 64 {
		diag.Region = region
	}
	if v, ok := parseHeaderFloat(headers, "x-apexllm-queue-ms", "x-queue-time-ms"); ok {
		diag.ServerQueueMs = &v
	}
	if v, ok := parseHeaderFloat(headers, "x-apexllm-prefill-ms", "x-prefill-time-ms"); ok {
		diag.ServerPrefillMs = &v
	}
	if v, ok := parseHeaderFloat(headers, "x-apexllm-decode-ms", "x-decode-time-ms"); ok {
		diag.ServerDecodeMs = &v
	}
}

func parseHeaderFloat(headers http.Header, keys ...string) (float64, bool) {
	for _, key := range keys {
		if raw := strings.TrimSpace(headers.Get(key)); raw != "" {
			if v, err := strconv.ParseFloat(raw, 64); err == nil && v >= 0 {
				return v, true
			}
		}
	}
	return 0, false
}
