package protocol

import "strings"

// Trace sources describe where an outbound request's W3C trace context came
// from: written by the script author, continued from an active span, or
// synthesized by the protocol when no span was active.
const (
	TraceSourceAuthored   = "authored"
	TraceSourcePropagated = "propagated"
	TraceSourceGenerated  = "generated"
)

// SentTraceContext is the W3C trace context an outbound request carried. It is
// kept apart from Result.CorrelationID, which may name a gateway request ID
// chosen after the response. Only validated hex identifiers are retained, so
// the value is bounded and carries no script content.
type SentTraceContext struct {
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`
	Flags   string `json:"flags,omitempty"`
	Source  string `json:"source"`
	// Sent is false when the result was served without a network request,
	// such as a fresh HTTP cache hit; the context never reached a server.
	Sent bool `json:"sent"`
}

// NewSentTraceContext records a traceparent header value. It returns nil when
// no header was present. A malformed authored header is recorded with its
// source but without identifiers, so it can never be looked up as a trace.
func NewSentTraceContext(traceparent string, source string, sent bool) *SentTraceContext {
	traceparent = strings.TrimSpace(traceparent)
	if traceparent == "" {
		return nil
	}
	context := &SentTraceContext{Source: source, Sent: sent}
	if traceID, spanID, flags, ok := ParseTraceparent(traceparent); ok {
		context.TraceID, context.SpanID, context.Flags = traceID, spanID, flags
	}
	return context
}

// Sampled reports whether the sent context asked downstream tracers to record.
func (c *SentTraceContext) Sampled() bool {
	if c == nil || len(c.Flags) != 2 {
		return false
	}
	// The sampled flag is the lowest bit, so it is set on every odd hex digit.
	return strings.IndexByte("13579bdf", c.Flags[1]) >= 0
}

// ParseTraceparent validates a version-00-compatible W3C traceparent and
// returns its lowercase trace ID, parent span ID and flags.
func ParseTraceparent(value string) (traceID string, spanID string, flags string, ok bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(value)), "-")
	if len(parts) < 4 {
		return "", "", "", false
	}
	version := parts[0]
	if len(version) != 2 || !isLowerHexString(version) || version == "ff" || (version == "00" && len(parts) != 4) {
		return "", "", "", false
	}
	traceID, spanID, flags = parts[1], parts[2], parts[3]
	if len(traceID) != 32 || len(spanID) != 16 || len(flags) != 2 ||
		!isLowerHexString(traceID) || !isLowerHexString(spanID) || !isLowerHexString(flags) ||
		strings.Trim(traceID, "0") == "" || strings.Trim(spanID, "0") == "" {
		return "", "", "", false
	}
	return traceID, spanID, flags, true
}

func isLowerHexString(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return value != ""
}

// ValidTraceID reports whether id is a lowercase, non-zero W3C trace ID.
func ValidTraceID(id string) bool {
	return len(id) == 32 && isLowerHexString(id) && strings.Trim(id, "0") != ""
}

// LookupTraceID returns the trace ID a result's request actually sent, or ""
// when it sent none: exemplars and provider lookups only use these.
func (c *SentTraceContext) LookupTraceID() string {
	if c == nil || !c.Sent || !ValidTraceID(c.TraceID) {
		return ""
	}
	return c.TraceID
}
