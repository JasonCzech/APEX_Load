package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestWebSocketDiagnosticsObserveOnlyMatchedPong(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&gorilla.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetPingHandler(func(payload string) error {
			if err := conn.WriteControl(gorilla.PongMessage, []byte("unmatched"), time.Now().Add(time.Second)); err != nil {
				return err
			}
			if err := conn.WriteControl(gorilla.PongMessage, []byte(payload), time.Now().Add(time.Second)); err != nil {
				return err
			}
			return conn.WriteMessage(gorilla.TextMessage, []byte("ready"))
		})
		for {
			kind, body, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err = conn.WriteMessage(kind, body); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	w := &WebSocket{}
	if err := w.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	defer w.Teardown(context.Background())
	execute := func(a *protocol.Action) *protocol.WebSocketDiagnostics {
		t.Helper()
		result, err := w.Execute(context.Background(), a)
		if err != nil {
			t.Fatal(err)
		}
		d := result.ServerTelemetry.WebSocket
		if !d.Valid() {
			t.Fatalf("invalid diagnostics %+v", d)
		}
		return d
	}
	connected := execute(&protocol.Action{Type: "ws_connect", Target: "ws" + strings.TrimPrefix(server.URL, "http")})
	if connected.HandshakeMs == nil || connected.ReceiveMs != nil {
		t.Fatalf("handshake %+v", connected)
	}
	ping := execute(&protocol.Action{Type: "ws_ping", Body: []byte("authored")})
	if len(ping.PingRTTMs) != 0 {
		t.Fatal("ping write invented RTT")
	}
	received := execute(&protocol.Action{Type: "ws_receive"})
	if len(received.PingRTTMs) != 1 || received.ReceiveMs == nil || received.MessageType != "text" {
		t.Fatalf("receive %+v", received)
	}
	sent := execute(&protocol.Action{Type: "ws_send", Body: []byte("message")})
	if sent.SendMs == nil || sent.WriteWaitMs == nil || sent.MessagesSent != 1 || sent.BytesSent != 7 {
		t.Fatalf("send %+v", sent)
	}
	received = execute(&protocol.Action{Type: "ws_receive"})
	if len(received.PingRTTMs) != 0 {
		t.Fatal("unmatched application message invented RTT")
	}
}

func TestWebSocketDiagnosticCloneAndMerge(t *testing.T) {
	zero := 0.0
	d := &protocol.WebSocketDiagnostics{Operation: "ws_receive", MessageType: "text", ReceiveMs: &zero, MessagesReceived: 1, PingRTTMs: []float64{1, 3}}
	cloned := d.Clone()
	*cloned.ReceiveMs = 9
	cloned.PingRTTMs[0] = 8
	if *d.ReceiveMs != 0 || d.PingRTTMs[0] != 1 {
		t.Fatal("mutable diagnostics alias")
	}
	var summary *protocol.WebSocketSummary
	protocol.MergeWebSocketDiagnostic(&summary, d)
	protocol.MergeWebSocketDiagnostic(&summary, &protocol.WebSocketDiagnostics{Operation: "ws_connect"})
	if summary.Samples != 2 || summary.Receive.Count != 1 || summary.PingRTT.Count != 2 || summary.Handshake.Count != 0 {
		t.Fatalf("optional measurements %+v", summary)
	}
}
