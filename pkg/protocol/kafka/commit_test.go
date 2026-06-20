package kafka

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestKafkaCommitRecordsCommitCount verifies kafka_commit is no longer a no-op:
// it records a commit on the shared session and reports CommitCount telemetry.
func TestKafkaCommitRecordsCommitCount(t *testing.T) {
	k := newMockKafka(t, map[string]any{"kafka_topic": "events", "kafka_consumer_group": "cg-1"})
	res, err := k.Execute(context.Background(), &protocol.Action{Name: "commit", Type: "kafka_commit"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !res.Success {
		t.Fatalf("commit success = false: %s", res.ErrorMessage)
	}
	if res.ServerTelemetry == nil || res.ServerTelemetry.CommitCount != 1 {
		t.Fatalf("commit CommitCount = %+v, want 1", res.ServerTelemetry)
	}
	if k.session.Commits() != 1 {
		t.Fatalf("session commits = %d, want 1", k.session.Commits())
	}
}

// TestKafkaConsumerOffsetLagSurfaced verifies the broker offset backlog is
// reported on ServerTelemetry (P1.5) instead of only as a string extraction.
func TestKafkaConsumerOffsetLagSurfaced(t *testing.T) {
	k := newMockKafka(t, map[string]any{"kafka_topic": "lag.events"})
	for i := 0; i < 3; i++ {
		if _, err := k.Execute(context.Background(), &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte("x")}); err != nil {
			t.Fatalf("produce: %v", err)
		}
	}
	res, err := k.Execute(context.Background(), &protocol.Action{Name: "c", Type: "kafka_consume"})
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if !res.Success {
		t.Fatalf("consume failed: %s", res.ErrorMessage)
	}
	if res.ServerTelemetry == nil || res.ServerTelemetry.ConsumerOffsetLag != 2 {
		t.Fatalf("ConsumerOffsetLag = %+v, want 2 (3 produced, 1 consumed)", res.ServerTelemetry)
	}
}
