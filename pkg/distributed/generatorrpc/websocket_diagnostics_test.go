package generatorrpc

import (
	"github.com/jasonczech/apexload/pkg/protocol"
	"reflect"
	"testing"
)

func TestWebSocketDiagnosticsWirePreservesOptionalEvidence(t *testing.T) {
	zero := 0.0
	code := int32(1006)
	d := &protocol.WebSocketDiagnostics{Operation: "ws_receive", ReceiveMs: &zero, CloseCode: &code, DisconnectReason: "peer_close", PingRTTMs: []float64{1, 2}, CallbackMode: true}
	got := websocketDiagnosticsFromProto(websocketDiagnosticsToProto(d))
	if !reflect.DeepEqual(d, got) {
		t.Fatalf("round trip %+v != %+v", d, got)
	}
	*got.ReceiveMs = 1
	if *d.ReceiveMs != 0 {
		t.Fatal("aliased measurement")
	}
}
