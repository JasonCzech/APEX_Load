//go:build eventhubs_real

package eventhubs

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestEventHubsRealRoundTrip exercises a live produce->observe round trip against
// a real Azure Event Hubs namespace. It is skipped unless EVENTHUBS_CONNECTION_STRING
// (and EVENTHUBS_EVENT_HUB) are set, since Event Hubs has no GA local emulator.
//
// The connection string should carry an EntityPath or EVENTHUBS_EVENT_HUB must
// name the hub. The probe reads from partition EVENTHUBS_PARTITION_ID (default
// "0") at the partition tail, so the producer must target that partition.
func TestEventHubsRealRoundTrip(t *testing.T) {
	conn := os.Getenv("EVENTHUBS_CONNECTION_STRING")
	if conn == "" {
		t.Skip("EVENTHUBS_CONNECTION_STRING not set; skipping live Event Hubs round trip")
	}
	hub := os.Getenv("EVENTHUBS_EVENT_HUB")
	partition := os.Getenv("EVENTHUBS_PARTITION_ID")
	if partition == "" {
		partition = "0"
	}

	ctx := context.Background()
	e := &EventHubs{name: eventHubsProtocolName}
	if err := e.Init(ctx, map[string]any{
		"eventhubs_connection_string": conn,
		"eventhubs_event_hub":         hub,
		"eventhubs_partition_id":      partition,
		"eventhubs_auth_type":         "connection_string",
		"eventhubs_timeout_ms":        float64(15000),
		// Round-trip: read from the partition start so the just-produced event is
		// observed reliably (a "latest" probe opened after the produce skips it).
		"eventhubs_start_position": "earliest",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer e.Teardown(ctx)

	// Produce to the partition, then observe from the start so the event is seen.
	produced, err := e.Execute(ctx, &protocol.Action{
		Name:   "produce",
		Type:   "eventhubs_produce",
		Body:   []byte(`{"probe":"round-trip"}`),
		Params: map[string]any{"partition_id": partition},
	})
	if err != nil || !produced.Success {
		t.Fatalf("produce failed: err=%v msg=%s", err, produced.ErrorMessage)
	}

	// Observe from the partition start (set above). Retry a few times to absorb
	// ingestion propagation latency between produce and the event being readable.
	var consumed *protocol.Result
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		consumed, err = e.Execute(ctx, &protocol.Action{Name: "observe", Type: "eventhubs_consume"})
		if err == nil && consumed.Success {
			break
		}
	}
	if consumed == nil || !consumed.Success {
		t.Fatalf("observe failed: err=%v msg=%v", err, consumed)
	}
	if consumed.ServerTelemetry == nil || consumed.ServerTelemetry.MessagesReceived != 1 {
		t.Fatalf("expected one received message, got telemetry %+v", consumed.ServerTelemetry)
	}
}
