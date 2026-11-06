package kafka

import (
	"context"
	"strings"
	"testing"
)

func TestKafkaInitValidatesMaxInFlightAgainstIdempotence(t *testing.T) {
	if err := (&Kafka{name: "kafka"}).Init(context.Background(), map[string]any{"kafka_mock": true, "kafka_producer_max_in_flight_requests": float64(6)}); err == nil || !strings.Contains(err.Error(), "max_in_flight_requests above 5") {
		t.Fatalf("idempotent default accepted six in-flight requests: %v", err)
	}
	k := newMockKafka(t, map[string]any{"kafka_producer_max_in_flight_requests": float64(5), "kafka_idempotence": "disabled"})
	if got := k.cfg.Producer.MaxInFlightRequests; got == nil || *got != 5 {
		t.Fatalf("max in-flight not carried into the runtime config: %v", got)
	}
	if got := newMockKafka(t, nil).cfg.Producer.MaxInFlightRequests; got != nil {
		t.Fatalf("unset max in-flight must keep the adapter default, got %d", *got)
	}
}
