package kafka

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestKafkaConsumeDetectsDuplicateAfterSeek covers messaging-shared-1 for Kafka:
// in-order consumption reports no duplicate / out-of-order, while re-consuming an
// already-seen offset (after a seek back) is flagged as a duplicate.
func TestKafkaConsumeDetectsDuplicateAfterSeek(t *testing.T) {
	ctx := context.Background()
	k := newMockKafka(t, map[string]any{"kafka_topic": "dup.events"})
	for _, body := range []string{"m0", "m1", "m2"} {
		if _, err := k.Execute(ctx, &protocol.Action{Name: "p", Type: "kafka_produce", Body: []byte(body)}); err != nil {
			t.Fatalf("produce: %v", err)
		}
	}

	// Consume offsets 0,1,2 in order: no duplicate, no out-of-order.
	for i := 0; i < 3; i++ {
		res, err := k.Execute(ctx, &protocol.Action{Name: "c", Type: "kafka_consume"})
		if err != nil || !res.Success {
			t.Fatalf("consume %d: err=%v ok=%v", i, err, res != nil && res.Success)
		}
		if res.ServerTelemetry != nil && (res.ServerTelemetry.DuplicateCount != 0 || res.ServerTelemetry.OutOfOrderCount != 0) {
			t.Fatalf("in-order consume %d flagged dup/ooo: %+v", i, res.ServerTelemetry)
		}
	}

	// Seek back to earliest and re-consume offset 0 -> duplicate.
	if _, err := k.Execute(ctx, &protocol.Action{Name: "s", Type: "kafka_seek", Params: map[string]any{"offset": "earliest"}}); err != nil {
		t.Fatalf("seek: %v", err)
	}
	replay, err := k.Execute(ctx, &protocol.Action{Name: "r", Type: "kafka_consume"})
	if err != nil || !replay.Success {
		t.Fatalf("replay consume: err=%v ok=%v", err, replay != nil && replay.Success)
	}
	if replay.ServerTelemetry == nil || replay.ServerTelemetry.DuplicateCount != 1 {
		t.Fatalf("re-consumed offset 0 DuplicateCount = %+v, want 1", replay.ServerTelemetry)
	}
}
