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

// TestKafkaSeekRepositionsConsumer verifies kafka_seek is no longer a no-op:
// the offset-based mock broker lets a consumer replay from earliest and skip to
// latest. (Pre-P2.1 the mock was destructive FIFO and could not seek.)
func TestKafkaSeekRepositionsConsumer(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{"kafka_topic": "seek.events"})
	for _, body := range []string{"m0", "m1", "m2"} {
		if _, err := k.Execute(ctx, &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte(body)}); err != nil {
			t.Fatalf("produce %s: %v", body, err)
		}
	}
	// Consume m0 and m1 (cursor now at offset 2).
	for i := 0; i < 2; i++ {
		if _, err := k.Execute(ctx, &protocol.Action{Name: "c", Type: "kafka_consume"}); err != nil {
			t.Fatalf("consume: %v", err)
		}
	}
	// Seek back to earliest; the next consume replays from offset 0 (lag 2 again).
	sk, err := k.Execute(ctx, &protocol.Action{Name: "s", Type: "kafka_seek", Params: map[string]any{"offset": "earliest"}})
	if err != nil {
		t.Fatalf("seek: %v", err)
	}
	if !sk.Success {
		t.Fatalf("seek failed: %s", sk.ErrorMessage)
	}
	replay, err := k.Execute(ctx, &protocol.Action{Name: "r", Type: "kafka_consume"})
	if err != nil {
		t.Fatalf("replay consume: %v", err)
	}
	if replay.ServerTelemetry == nil || replay.ServerTelemetry.ConsumerOffsetLag != 2 {
		t.Fatalf("after seek-earliest the replay lag = %+v, want 2 (cursor reset to offset 0)", replay.ServerTelemetry)
	}

	// Seek to latest; the next consume finds nothing (caught up to the tail).
	if _, err := k.Execute(ctx, &protocol.Action{Name: "sl", Type: "kafka_seek", Params: map[string]any{"offset": "latest"}}); err != nil {
		t.Fatalf("seek latest: %v", err)
	}
	tail, err := k.Execute(ctx, &protocol.Action{Name: "ct", Type: "kafka_consume"})
	if err != nil {
		t.Fatalf("consume after seek latest: %v", err)
	}
	if tail.Success {
		t.Fatal("expected no record after seek to latest (consumer at tail)")
	}
}
