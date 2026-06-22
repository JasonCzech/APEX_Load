package eventhubs

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestEventHubsConsumeDetectsDuplicate covers messaging-shared-1 for Event Hubs:
// in-order observation reports no duplicate / out-of-order, while re-observing an
// already-seen sequence (a second consumer group re-reading the same offset) is
// flagged as a duplicate.
func TestEventHubsConsumeDetectsDuplicate(t *testing.T) {
	ctx := context.Background()
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "h"})
	for i := 0; i < 3; i++ {
		if _, err := e.Execute(ctx, &protocol.Action{Name: "p", Type: "eventhubs_produce", Body: []byte("x")}); err != nil {
			t.Fatalf("produce: %v", err)
		}
	}

	// Group g1 observes sequences 0,1,2 in order: no duplicate / out-of-order.
	for i := 0; i < 3; i++ {
		res, err := e.Execute(ctx, &protocol.Action{Name: "c", Type: "eventhubs_consume", Params: map[string]any{"consumer_group": "g1"}})
		if err != nil || !res.Success {
			t.Fatalf("g1 consume %d: err=%v ok=%v", i, err, res != nil && res.Success)
		}
		if res.ServerTelemetry != nil && (res.ServerTelemetry.DuplicateCount != 0 || res.ServerTelemetry.OutOfOrderCount != 0) {
			t.Fatalf("g1 in-order consume %d flagged dup/ooo: %+v", i, res.ServerTelemetry)
		}
	}

	// Group g2 re-reads sequence 0 (its own cursor); the observer already saw it via
	// g1, so it is flagged as a duplicate.
	res, err := e.Execute(ctx, &protocol.Action{Name: "c2", Type: "eventhubs_consume", Params: map[string]any{"consumer_group": "g2"}})
	if err != nil || !res.Success {
		t.Fatalf("g2 consume: err=%v ok=%v", err, res != nil && res.Success)
	}
	if res.ServerTelemetry == nil || res.ServerTelemetry.DuplicateCount != 1 {
		t.Fatalf("g2 re-read of sequence 0 DuplicateCount = %+v, want 1", res.ServerTelemetry)
	}
}
