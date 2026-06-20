package kafka

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func newMockKafka(t *testing.T, cfg map[string]any) *Kafka {
	t.Helper()
	// Isolate every test from the package-level shared broker.
	k := &Kafka{name: "kafka"}
	if cfg == nil {
		cfg = map[string]any{}
	}
	cfg["kafka_mock"] = true
	if err := k.Init(context.Background(), cfg); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	k.client = mockClient{broker: newMemoryBroker()}
	return k
}

func TestKafkaProtocolIsRegistered(t *testing.T) {
	p, err := protocol.Get("kafka")
	if err != nil {
		t.Fatalf("protocol.Get(kafka) returned error: %v", err)
	}
	if p.Name() != "kafka" {
		t.Fatalf("Name() = %q, want kafka", p.Name())
	}
}

func TestKafkaProduceConsumeWithExtractionAndAssertion(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{
		"kafka_topic":      "orders.events",
		"kafka_timeout_ms": float64(250),
	})

	produced, err := k.Execute(ctx, &protocol.Action{
		Name:    "produce-order",
		Type:    "kafka_produce",
		Body:    []byte(`{"orderId":"A-100","status":"created"}`),
		Headers: map[string]string{"source": "checkout"},
		Params:  map[string]any{"key": "A-100"},
		Assertions: []protocol.Assertion{
			{Type: "message_received"},
			{Type: "key_equals", Expected: "A-100"},
			{Type: "offset_advanced"},
		},
	})
	if err != nil {
		t.Fatalf("produce returned error: %v", err)
	}
	if !produced.Success {
		t.Fatalf("produce success = false: %s", produced.ErrorMessage)
	}
	if produced.BytesSent == 0 {
		t.Fatal("produce BytesSent = 0, want payload bytes")
	}
	if produced.Extractions["kafka_offset"] != "0" {
		t.Fatalf("kafka_offset = %q, want 0", produced.Extractions["kafka_offset"])
	}
	if produced.ServerTelemetry == nil || produced.ServerTelemetry.MessagesSent != 1 {
		t.Fatalf("MessagesSent telemetry not set: %+v", produced.ServerTelemetry)
	}

	consumed, err := k.Execute(ctx, &protocol.Action{
		Name: "consume-order",
		Type: "kafka_consume",
		Extractors: []protocol.Extractor{
			{Name: "order_id", Source: "body", Type: "jsonpath", Expression: "$.orderId"},
			{Name: "src", Source: "header", Expression: "source"},
		},
		Assertions: []protocol.Assertion{
			{Type: "message_received"},
			{Type: "body_contains", Value: "created"},
			{Type: "header_equals", Value: "source", Expected: "checkout"},
			{Type: "consumer_lag_below", Max: 10},
		},
	})
	if err != nil {
		t.Fatalf("consume returned error: %v", err)
	}
	if !consumed.Success {
		t.Fatalf("consume success = false: %s", consumed.ErrorMessage)
	}
	if consumed.Extractions["order_id"] != "A-100" {
		t.Fatalf("order_id = %q, want A-100", consumed.Extractions["order_id"])
	}
	if consumed.Extractions["src"] != "checkout" {
		t.Fatalf("src = %q, want checkout", consumed.Extractions["src"])
	}
	if consumed.Extractions["kafka_consumer_lag"] != "0" {
		t.Fatalf("kafka_consumer_lag = %q, want 0", consumed.Extractions["kafka_consumer_lag"])
	}
	if consumed.BytesReceived == 0 {
		t.Fatal("consume BytesReceived = 0, want payload bytes")
	}
	if consumed.ServerTelemetry == nil || consumed.ServerTelemetry.MessagesReceived != 1 {
		t.Fatalf("MessagesReceived telemetry not set: %+v", consumed.ServerTelemetry)
	}
	if consumed.ServerTelemetry.ResponseSizeClass != "small" {
		t.Fatalf("ResponseSizeClass = %q, want small", consumed.ServerTelemetry.ResponseSizeClass)
	}
}

func TestKafkaRequestReply(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{"kafka_topic": "rpc.requests"})

	result, err := k.Execute(ctx, &protocol.Action{
		Name:   "rpc",
		Type:   "kafka_request_reply",
		Body:   []byte(`{"ping":true}`),
		Params: map[string]any{"reply_to": "rpc.replies", "timeout_ms": float64(500)},
		Assertions: []protocol.Assertion{
			{Type: "message_received"},
			{Type: "body_contains", Value: "ping"},
		},
	})
	if err != nil {
		t.Fatalf("request_reply returned error: %v", err)
	}
	if !result.Success {
		t.Fatalf("request_reply success = false: %s", result.ErrorMessage)
	}
	if result.BytesSent == 0 || result.BytesReceived == 0 {
		t.Fatalf("expected non-zero bytes sent/received, got sent=%d received=%d", result.BytesSent, result.BytesReceived)
	}
	if result.CorrelationID == "" {
		t.Fatal("expected a correlation id on the reply")
	}
	if result.Extractions["kafka_operation"] != "request_reply" {
		t.Fatalf("kafka_operation = %q, want request_reply", result.Extractions["kafka_operation"])
	}
}

func TestKafkaPartitionStrategies(t *testing.T) {
	ctx := context.Background()

	// Explicit partition param wins regardless of strategy.
	k := newMockKafka(t, map[string]any{"kafka_topic": "t", "kafka_partition_strategy": "key-hash"})
	res, err := k.Execute(ctx, &protocol.Action{
		Name: "p", Type: "kafka_produce", Body: []byte("x"),
		Params:     map[string]any{"partition": float64(3)},
		Assertions: []protocol.Assertion{{Type: "partition_equals", Expected: 3}},
	})
	if err != nil || !res.Success {
		t.Fatalf("explicit partition failed: err=%v msg=%s", err, res.ErrorMessage)
	}

	// key-hash is deterministic for the same key.
	k2 := newMockKafka(t, map[string]any{"kafka_topic": "t", "kafka_partition_strategy": "key-hash", "kafka_partitions": float64(4)})
	first := k2.resolvePartition(&protocol.Action{Params: map[string]any{"key": "abc"}}, "abc")
	second := k2.resolvePartition(&protocol.Action{Params: map[string]any{"key": "abc"}}, "abc")
	if first != second {
		t.Fatalf("key-hash not deterministic: %d != %d", first, second)
	}
	if first < 0 || first >= 4 {
		t.Fatalf("partition %d out of range [0,4)", first)
	}

	// round-robin advances across calls.
	k3 := newMockKafka(t, map[string]any{"kafka_topic": "t", "kafka_partition_strategy": "round-robin", "kafka_partitions": float64(3)})
	got := []int32{
		k3.resolvePartition(&protocol.Action{}, ""),
		k3.resolvePartition(&protocol.Action{}, ""),
		k3.resolvePartition(&protocol.Action{}, ""),
		k3.resolvePartition(&protocol.Action{}, ""),
	}
	want := []int32{0, 1, 2, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("round-robin[%d] = %d, want %d (seq %v)", i, got[i], want[i], got)
		}
	}
}

func TestKafkaConsumeTimeout(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{"kafka_topic": "empty.topic", "kafka_timeout_ms": float64(40)})
	res, err := k.Execute(ctx, &protocol.Action{Name: "c", Type: "kafka_consume"})
	if err != nil {
		t.Fatalf("Execute should not return a transport error: %v", err)
	}
	if res.Success {
		t.Fatal("expected consume to fail on empty topic")
	}
	if res.StatusCode != 204 {
		t.Fatalf("StatusCode = %d, want 204", res.StatusCode)
	}
}

func TestKafkaUnsupportedActionType(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{"kafka_topic": "t"})
	res, err := k.Execute(ctx, &protocol.Action{Name: "bad", Type: "kafka_explode"})
	if err != nil {
		t.Fatalf("Execute should not return error for unsupported type: %v", err)
	}
	if res.Success {
		t.Fatal("expected failure for unsupported action type")
	}
	if res.StatusCode != 500 {
		t.Fatalf("StatusCode = %d, want 500", res.StatusCode)
	}
}

func TestKafkaMissingTopic(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, nil) // no default topic, no target
	res, err := k.Execute(ctx, &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte("x")})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if res.Success {
		t.Fatal("expected failure when topic is missing")
	}
}

func TestKafkaHealthCheckMock(t *testing.T) {
	k := newMockKafka(t, map[string]any{"kafka_topic": "t"})
	if err := k.HealthCheck(context.Background()); err != nil {
		t.Fatalf("mock HealthCheck returned error: %v", err)
	}
}

func TestKafkaConsumerGroupDependencyType(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{"kafka_topic": "t", "kafka_consumer_group": "cg-1"})
	_, _ = k.Execute(ctx, &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte("x")})
	res, err := k.Execute(ctx, &protocol.Action{Name: "c", Type: "kafka_consume", Params: map[string]any{"timeout_ms": float64(200)}})
	if err != nil {
		t.Fatalf("consume returned error: %v", err)
	}
	if res.DependencyType != "kafka_consumer_group" {
		t.Fatalf("DependencyType = %q, want kafka_consumer_group", res.DependencyType)
	}
}
