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

func TestKafkaTraceCarrierRejectedRecordIsNotSent(t *testing.T) {
	k := newMockKafka(t, map[string]any{"kafka_topic": "orders", "kafka_timeout_ms": float64(250), "kafka_max_message_bytes": float64(16), protocol.ConfigMessagingTraceCarrier: "w3c"})
	result, err := k.Execute(context.Background(), &protocol.Action{Name: "produce", Type: "kafka_produce", Body: []byte("a body longer than the sixteen byte limit")})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || result.TraceContext == nil {
		t.Fatalf("result = %+v, want a rejected record that still records its context", result)
	}
	if result.TraceContext.Sent || result.TraceContext.LookupTraceID() != "" {
		t.Fatalf("TraceContext = %+v, want not sent for a record rejected before the client", result.TraceContext)
	}
}

func TestKafkaTraceCarrierBufferedTransactionalRecordIsNotSent(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{"kafka_topic": "tx.events", "kafka_transactional_id": "tx-trace", "kafka_timeout_ms": float64(40), protocol.ConfigMessagingTraceCarrier: "w3c"})
	if begin, err := k.Execute(ctx, &protocol.Action{Name: "b", Type: "kafka_begin_transaction"}); err != nil || !begin.Success {
		t.Fatalf("begin: %v %+v", err, begin)
	}
	buffered, err := k.Execute(ctx, &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte("t0")})
	if err != nil || !buffered.Success || buffered.TraceContext == nil {
		t.Fatalf("buffered produce = %+v %v", buffered, err)
	}
	// The record waits in the client for a commit that may never come.
	if buffered.TraceContext.Sent || buffered.TraceContext.LookupTraceID() != "" {
		t.Fatalf("buffered TraceContext = %+v, want not sent", buffered.TraceContext)
	}
	if abort, err := k.Execute(ctx, &protocol.Action{Name: "a", Type: "kafka_abort_transaction"}); err != nil || !abort.Success {
		t.Fatalf("abort: %v %+v", err, abort)
	}
}
