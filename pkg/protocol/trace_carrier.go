package protocol

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// Messaging trace carriers. A script opts in with config.trace_carrier; each
// protocol profile then writes the names its consumers' instrumentation reads.
const (
	TraceCarrierNone = "none"
	TraceCarrierW3C  = "w3c"

	// ConfigMessagingTraceCarrier is the protocol config key for the setting.
	ConfigMessagingTraceCarrier = "messaging_trace_carrier"

	TraceparentKey  = "traceparent"
	TracestateKey   = "tracestate"
	DiagnosticIDKey = "Diagnostic-Id"
)

// Trace link kinds archived on consumer and batch rows.
const (
	TraceLinkConsumed    = "consumed"
	TraceLinkRedelivery  = "redelivery"
	TraceLinkBatchMember = "batch_member"
	// MaxTraceLinks bounds the links one result keeps; more set TraceLinksTruncated.
	MaxTraceLinks = 8
)

// TraceLink names another trace context a result relates to without being its
// child: a consumed message's producer, a batch member, or a redelivery.
type TraceLink struct {
	TraceID string `json:"trace_id"`
	SpanID  string `json:"span_id"`
	Kind    string `json:"kind"`
	// DeliveryCount is set on redeliveries when the broker reports it.
	DeliveryCount int `json:"delivery_count,omitempty"`
}

// ValidateTraceCarrier accepts the supported trace_carrier values.
func ValidateTraceCarrier(value string) error {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", TraceCarrierNone, TraceCarrierW3C:
		return nil
	}
	return fmt.Errorf("trace_carrier must be %q or %q", TraceCarrierNone, TraceCarrierW3C)
}

// MessagingTraceCarrierEnabled reads the carrier from protocol config.
func MessagingTraceCarrierEnabled(cfg map[string]any) (bool, error) {
	value, _ := cfg[ConfigMessagingTraceCarrier].(string)
	if err := ValidateTraceCarrier(value); err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(value), TraceCarrierW3C), nil
}

// InjectMessageTraceContext writes a W3C context into a message's headers or
// properties: a child of the active transaction root when there is one,
// otherwise a new unsampled trace, with a fresh span ID per message. Azure SDK
// profiles also get Diagnostic-Id. A traceparent or Diagnostic-Id the script
// authored wins and is reported as authored. It returns what was sent.
func InjectMessageTraceContext(ctx context.Context, props map[string]string, diagnosticID bool) *SentTraceContext {
	if props == nil {
		return nil
	}
	if authored := firstPropertyFold(props, TraceparentKey, DiagnosticIDKey); authored != "" {
		return NewSentTraceContext(authored, TraceSourceAuthored, true)
	}
	traceparent, tracestate, source := childTraceparent(ctx)
	props[TraceparentKey] = traceparent
	if tracestate != "" {
		props[TracestateKey] = tracestate
	}
	if diagnosticID {
		props[DiagnosticIDKey] = traceparent
	}
	return NewSentTraceContext(traceparent, source, true)
}

// ExtractMessageTraceLink reads a received message's producer context.
func ExtractMessageTraceLink(props map[string]string, kind string, deliveryCount int) (TraceLink, bool) {
	value := firstPropertyFold(props, TraceparentKey, DiagnosticIDKey)
	traceID, spanID, _, ok := ParseTraceparent(value)
	if !ok {
		return TraceLink{}, false
	}
	link := TraceLink{TraceID: traceID, SpanID: spanID, Kind: kind}
	if kind == TraceLinkRedelivery && deliveryCount > 1 {
		link.DeliveryCount = deliveryCount
	}
	return link, true
}

// AppendTraceLink adds a link within MaxTraceLinks, flagging any overflow.
func AppendTraceLink(result *Result, link TraceLink) {
	if result == nil || link.TraceID == "" {
		return
	}
	if len(result.TraceLinks) >= MaxTraceLinks {
		result.TraceLinksTruncated = true
		return
	}
	result.TraceLinks = append(result.TraceLinks, link)
}

func childTraceparent(ctx context.Context) (traceparent, tracestate, source string) {
	if ctx != nil {
		if parent := trace.SpanContextFromContext(ctx); parent.IsValid() {
			return "00-" + parent.TraceID().String() + "-" + randomHex(8) + "-" + parent.TraceFlags().String(), parent.TraceState().String(), TraceSourcePropagated
		}
	}
	return "00-" + randomHex(16) + "-" + randomHex(8) + "-00", "", TraceSourceGenerated
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil || strings.Trim(hex.EncodeToString(buf), "0") == "" {
		buf[n-1] = 1
	}
	return hex.EncodeToString(buf)
}

func firstPropertyFold(props map[string]string, keys ...string) string {
	for _, want := range keys {
		for key, value := range props {
			if strings.EqualFold(strings.TrimSpace(key), want) && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}
