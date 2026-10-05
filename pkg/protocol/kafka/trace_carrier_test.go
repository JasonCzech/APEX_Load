package kafka

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"go.opentelemetry.io/otel/trace"
)

func TestKafkaTraceCarrierPropagatesAndLinks(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled}))
	k := newMockKafka(t, map[string]any{"kafka_topic": "orders", "kafka_timeout_ms": float64(250), protocol.ConfigMessagingTraceCarrier: "w3c"})
	produced, err := k.Execute(ctx, &protocol.Action{Name: "produce", Type: "kafka_produce", Body: []byte("a"), Headers: map[string]string{"source": "checkout"}})
	if err != nil || !produced.Success || produced.TraceContext == nil || produced.TraceContext.Source != protocol.TraceSourcePropagated || produced.TraceContext.LookupTraceID() == "" {
		t.Fatalf("produce = %+v %v", produced, err)
	}
	consumed, err := k.Execute(context.Background(), &protocol.Action{Name: "consume", Type: "kafka_consume", Extractors: []protocol.Extractor{{Name: "tp", Source: "header", Expression: protocol.TraceparentKey}}})
	if err != nil || !consumed.Success {
		t.Fatalf("consume = %+v %v", consumed, err)
	}
	// The consumer links to the producer's span; it does not claim to continue its trace.
	if consumed.TraceContext != nil || len(consumed.TraceLinks) != 1 {
		t.Fatalf("consumer trace context %+v links %+v", consumed.TraceContext, consumed.TraceLinks)
	}
	link := consumed.TraceLinks[0]
	if link.Kind != protocol.TraceLinkConsumed || link.SpanID != produced.TraceContext.SpanID || link.TraceID != produced.TraceContext.TraceID {
		t.Fatalf("link %+v does not name the produced message %+v", link, produced.TraceContext)
	}

	plain := newMockKafka(t, map[string]any{"kafka_topic": "plain", "kafka_timeout_ms": float64(250)})
	unchanged, _ := plain.Execute(ctx, &protocol.Action{Name: "produce", Type: "kafka_produce", Body: []byte("a")})
	if unchanged.TraceContext != nil {
		t.Fatal("a script without trace_carrier sent trace context")
	}
	if err := (&Kafka{name: "kafka"}).Init(context.Background(), map[string]any{"kafka_mock": true, protocol.ConfigMessagingTraceCarrier: "b3"}); err == nil {
		t.Fatal("an unsupported carrier was accepted")
	}
}
