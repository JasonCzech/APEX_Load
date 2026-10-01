package generatorrpc

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestSnapshotJSONReturnsValidTypedTruncation(t *testing.T) {
	raw := snapshotJSON(&protocol.ErrorSnapshot{
		RequestMethod:  "GET",
		RequestURL:     "https://example.test/large",
		ResponseStatus: 500,
		ResponseBody:   strings.Repeat("x", 64<<10),
	})
	var snapshot protocol.ErrorSnapshot
	if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
		t.Fatalf("truncated snapshot is invalid JSON: %v; raw=%q", err, raw)
	}
	if snapshot.ResponseStatus != 500 || !strings.Contains(snapshot.ResponseBody, "truncated") {
		t.Fatalf("truncated snapshot = %+v", snapshot)
	}
}

func TestCloneServerTelemetryPreservesWebSocket(t *testing.T) {
	zero := 0.0
	code := int32(1000)
	input := &protocol.ServerTelemetry{WebSocket: &protocol.WebSocketDiagnostics{
		Operation: "receive", ReceiveMs: &zero, CloseCode: &code, PingRTTMs: []float64{2},
	}}
	got := cloneServerTelemetry(input)
	if got.WebSocket == nil || got.WebSocket.ReceiveMs == nil || *got.WebSocket.ReceiveMs != 0 || got.WebSocket.Operation != "receive" {
		t.Fatal("WebSocket evidence lost")
	}
	*got.WebSocket.ReceiveMs = 3
	*got.WebSocket.CloseCode = 1001
	got.WebSocket.PingRTTMs[0] = 4
	if *input.WebSocket.ReceiveMs != 0 || *input.WebSocket.CloseCode != 1000 || input.WebSocket.PingRTTMs[0] != 2 {
		t.Fatal("clone aliases source diagnostics")
	}
	if cloneServerTelemetry(&protocol.ServerTelemetry{}).WebSocket != nil {
		t.Fatal("missing diagnostics invented")
	}
}
