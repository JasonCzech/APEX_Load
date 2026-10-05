package protocol

import (
	"context"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

func transactionRoot(t *testing.T) (context.Context, trace.SpanContext) {
	t.Helper()
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	state, _ := trace.ParseTraceState("vendor=value")
	root := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled, TraceState: state})
	return trace.ContextWithSpanContext(context.Background(), root), root
}

func TestInjectMessageTraceContext(t *testing.T) {
	ctx, root := transactionRoot(t)
	props := map[string]string{"order": "A-1"}
	sent := InjectMessageTraceContext(ctx, props, false)
	if sent == nil || sent.Source != TraceSourcePropagated || !sent.Sent || sent.LookupTraceID() == "" {
		t.Fatalf("sent = %+v", sent)
	}
	// A child of the transaction root, with its own span and the root's state.
	if sent.TraceID != root.TraceID().String() || sent.SpanID == root.SpanID().String() || props[TracestateKey] != "vendor=value" || !sent.Sampled() {
		t.Fatalf("child context %+v props %v", sent, props)
	}
	if _, ok := props[DiagnosticIDKey]; ok {
		t.Fatal("Diagnostic-Id written for a non-Azure profile")
	}
	again := InjectMessageTraceContext(ctx, map[string]string{}, true)
	if again.SpanID == sent.SpanID {
		t.Fatal("two messages shared a span ID")
	}

	azure := map[string]string{}
	InjectMessageTraceContext(context.Background(), azure, true)
	if azure[DiagnosticIDKey] == "" || azure[DiagnosticIDKey] != azure[TraceparentKey] {
		t.Fatalf("Azure profile carriers = %v", azure)
	}
	if _, _, flags, ok := ParseTraceparent(azure[TraceparentKey]); !ok || flags != "00" {
		t.Fatalf("a root-less message must start an unsampled trace: %q", azure[TraceparentKey])
	}

	authored := map[string]string{"Traceparent": "00-11111111111111111111111111111111-2222222222222222-01"}
	if got := InjectMessageTraceContext(ctx, authored, true); got.Source != TraceSourceAuthored || len(authored) != 1 {
		t.Fatalf("authored carrier changed: %+v %v", got, authored)
	}
}

func TestExtractAndBoundTraceLinks(t *testing.T) {
	link, ok := ExtractMessageTraceLink(map[string]string{"diagnostic-id": "00-11111111111111111111111111111111-2222222222222222-01"}, TraceLinkRedelivery, 3)
	if !ok || link.TraceID != strings.Repeat("1", 32) || link.Kind != TraceLinkRedelivery || link.DeliveryCount != 3 {
		t.Fatalf("link = %+v %v", link, ok)
	}
	if _, ok := ExtractMessageTraceLink(map[string]string{"traceparent": "garbage"}, TraceLinkConsumed, 0); ok {
		t.Fatal("malformed carrier produced a link")
	}
	result := &Result{}
	for i := 0; i < MaxTraceLinks+2; i++ {
		AppendTraceLink(result, link)
	}
	if len(result.TraceLinks) != MaxTraceLinks || !result.TraceLinksTruncated {
		t.Fatalf("links = %d truncated %v", len(result.TraceLinks), result.TraceLinksTruncated)
	}
	if ValidateTraceCarrier("W3C") != nil || ValidateTraceCarrier("") != nil || ValidateTraceCarrier("b3") == nil {
		t.Fatal("trace_carrier validation")
	}
}
