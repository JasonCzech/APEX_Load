//go:build kafka_integration

// Integration test for the franz-go-backed real adapter. Excluded from default
// builds and CI; run against a live broker with:
//
//	KAFKA_BROKERS=localhost:9092 KAFKA_TOPIC=apexload.itest \
//	  go test -tags "kafka_real kafka_integration" ./pkg/protocol/kafka/...
//
// A local single-node Redpanda or Kafka is sufficient. The test produces a
// record and consumes it back, exercising the real produce/consume path.
package kafka

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestKafkaFranzProduceConsumeIntegration(t *testing.T) {
	brokers := os.Getenv("KAFKA_BROKERS")
	if brokers == "" {
		t.Skip("set KAFKA_BROKERS (and optionally KAFKA_TOPIC) to run the live Kafka integration test")
	}
	topic := os.Getenv("KAFKA_TOPIC")
	if topic == "" {
		topic = "apexload.itest"
	}

	ctx := context.Background()
	k := &Kafka{name: "kafka"}
	if err := k.Init(ctx, map[string]any{
		"kafka_brokers":           strings.Split(brokers, ","),
		"kafka_topic":             topic,
		"kafka_consumer_group":    "apexload-itest",
		"kafka_auto_offset_reset": "earliest",
		"kafka_timeout_ms":        float64(10000),
		"kafka_mock":              false,
	}); err != nil {
		t.Fatalf("Init returned error: %v", err)
	}
	defer func() { _ = k.Teardown(ctx) }()

	if err := k.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck failed (is the broker reachable?): %v", err)
	}

	marker := time.Now().UnixNano()
	produced, err := k.Execute(ctx, &protocol.Action{
		Name:   "produce",
		Type:   "kafka_produce",
		Body:   []byte(`{"marker":"` + time.Unix(0, marker).Format(time.RFC3339Nano) + `"}`),
		Params: map[string]any{"key": "itest"},
	})
	if err != nil || !produced.Success {
		t.Fatalf("produce failed: err=%v msg=%s", err, produced.ErrorMessage)
	}

	consumed, err := k.Execute(ctx, &protocol.Action{
		Name:       "consume",
		Type:       "kafka_consume",
		Params:     map[string]any{"timeout_ms": float64(10000)},
		Assertions: []protocol.Assertion{{Type: "message_received"}},
	})
	if err != nil || !consumed.Success {
		t.Fatalf("consume failed: err=%v msg=%s", err, consumed.ErrorMessage)
	}
	if consumed.Extractions["kafka_offset"] == "" {
		t.Fatal("expected a kafka_offset extraction from the consumed record")
	}
}
