package eventhubs

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestEventHubsConsumeReliabilityIsPerStream covers messaging-shared-1 for Event
// Hubs: in-order observation reports no duplicate / out-of-order, and — because
// sequence numbers are scoped per (hub, consumer-group, partition) — a second
// consumer group independently re-reading the same sequences from its own cursor
// is NOT mis-flagged as a broker duplicate (consumer groups read independently).
func TestEventHubsConsumeReliabilityIsPerStream(t *testing.T) {
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

	// Group g2 re-reads the same sequences from its own cursor. As an independent
	// consumer group it must NOT be flagged as duplicate/out-of-order.
	for i := 0; i < 3; i++ {
		res, err := e.Execute(ctx, &protocol.Action{Name: "c2", Type: "eventhubs_consume", Params: map[string]any{"consumer_group": "g2"}})
		if err != nil || !res.Success {
			t.Fatalf("g2 consume %d: err=%v ok=%v", i, err, res != nil && res.Success)
		}
		if res.ServerTelemetry != nil && (res.ServerTelemetry.DuplicateCount != 0 || res.ServerTelemetry.OutOfOrderCount != 0) {
			t.Fatalf("g2 independent re-read %d wrongly flagged dup/ooo: %+v", i, res.ServerTelemetry)
		}
	}
}

// TestEventHubsEmptyProbeIsNoContentSuccess covers the read-only observer probe:
// when no event is available within the timeout, the probe is a no-content
// success (204) rather than a failed transaction, so it does not inflate the
// error rate / trip SLA gates on this producer-first protocol.
func TestEventHubsEmptyProbeIsNoContentSuccess(t *testing.T) {
	ctx := context.Background()
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "h", "eventhubs_timeout_ms": float64(20)})
	// No events were produced, so the probe finds an empty tail.
	res, err := e.Execute(ctx, &protocol.Action{Name: "c", Type: "eventhubs_consume"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Success {
		t.Fatalf("empty probe scored as failure: status=%d msg=%q", res.StatusCode, res.ErrorMessage)
	}
	if res.StatusCode != 204 {
		t.Fatalf("empty probe status = %d, want 204", res.StatusCode)
	}
	if res.Extractions["eventhubs_empty_probe"] != "1" {
		t.Fatalf("empty probe extraction = %q, want \"1\"", res.Extractions["eventhubs_empty_probe"])
	}
}

func TestEventHubsCallerDeadlineIsNotReportedAsEmptyProbe(t *testing.T) {
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "h", "eventhubs_timeout_ms": float64(200)})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	res, err := e.Execute(ctx, &protocol.Action{Name: "c", Type: "eventhubs_consume"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Success || res.StatusCode != 502 {
		t.Fatalf("caller deadline result = %#v, want transport failure", res)
	}
	if res.Extractions["eventhubs_empty_probe"] != "" {
		t.Fatalf("caller deadline was mislabeled empty probe: %#v", res.Extractions)
	}
}
