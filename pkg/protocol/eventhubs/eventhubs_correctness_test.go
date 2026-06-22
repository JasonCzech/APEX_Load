package eventhubs

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestEventHubsTimeoutHonorsNumericParam covers eventhubs-1: timeout_ms is a
// catalog int and arrives as float64 from JSON, so the override must be honored
// (the old string-only read silently dropped it).
func TestEventHubsTimeoutHonorsNumericParam(t *testing.T) {
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "h"})
	if got := e.timeout(&protocol.Action{Params: map[string]any{"timeout_ms": float64(750)}}); got != 750*time.Millisecond {
		t.Fatalf("timeout(float64 750) = %s, want 750ms", got)
	}
	// A string value still works via the fallback path.
	if got := e.timeout(&protocol.Action{Params: map[string]any{"timeout_ms": "600"}}); got != 600*time.Millisecond {
		t.Fatalf("timeout(string 600) = %s, want 600ms", got)
	}
}

// TestEventHubsBuildEventPartitionMutualExclusion covers eventhubs-2: when the
// action sets one routing control, the config default for the other must not be
// applied (the real adapter rejects an event carrying both).
func TestEventHubsBuildEventPartitionMutualExclusion(t *testing.T) {
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "h", "eventhubs_partition_key": "cfgkey"})

	ev := e.buildEvent(&protocol.Action{Params: map[string]any{"partition_id": "3"}})
	if ev.PartitionID != "3" || ev.PartitionKey != "" {
		t.Fatalf("action partition_id + config default key: got key=%q id=%q, want key=\"\" id=\"3\"", ev.PartitionKey, ev.PartitionID)
	}

	// With no action override, the config default applies.
	evDefault := e.buildEvent(&protocol.Action{})
	if evDefault.PartitionKey != "cfgkey" {
		t.Fatalf("no override: PartitionKey = %q, want cfgkey", evDefault.PartitionKey)
	}
}

// TestEventHubsBuildEventDecodesBase64Body covers eventhubs-3 (runtime half):
// a base64-encoded body round-trips back to its raw bytes for lossless binary.
func TestEventHubsBuildEventDecodesBase64Body(t *testing.T) {
	e := newMockEventHubs(t, map[string]any{"eventhubs_event_hub": "h"})
	raw := []byte{0xff, 0x00, 0x42, 0xfe}
	ev := e.buildEvent(&protocol.Action{
		Body:         []byte(base64.StdEncoding.EncodeToString(raw)),
		BodyEncoding: "base64",
	})
	if string(ev.Body) != string(raw) {
		t.Fatalf("decoded body = %v, want raw bytes %v", ev.Body, raw)
	}
}
