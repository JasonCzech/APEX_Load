package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestWebSocketInitRejectsOversizedMessageLimit(t *testing.T) {
	ws := &WebSocket{}
	err := ws.Init(context.Background(), map[string]any{
		"base_url":                   "ws://example.test",
		"websocket_max_message_size": int64(dsl.MaxWebSocketMessageBytes + 1),
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Init error = %v, want hard message limit rejection", err)
	}
}

func TestWebSocketWriteMessageRejectsOversizedRuntimePayload(t *testing.T) {
	ws := &WebSocket{writeTimeout: time.Second}
	err := ws.writeMessage(nil, gorillawebsocket.BinaryMessage, make([]byte, dsl.MaxWebSocketMessageBytes+1))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("writeMessage error = %v, want hard message limit rejection", err)
	}
}

func TestWebSocketExecuteConnectSendReceiveClose(t *testing.T) {
	server := newEchoServer(t, "")
	defer server.Close()

	ws := &WebSocket{}
	if err := ws.Init(context.Background(), map[string]any{
		"base_url":                   server.URL,
		"websocket_read_timeout_ms":  float64(1000),
		"websocket_write_timeout_ms": float64(1000),
		"websocket_max_message_size": float64(4096),
		"default_headers":            map[string]string{"X-Test": "apexload"},
		"auth_type":                  "bearer",
		"auth_token":                 "runtime-token",
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	connect, err := ws.Execute(context.Background(), &protocol.Action{Name: "connect", Type: "ws_connect", Target: "/ws"})
	if err != nil {
		t.Fatalf("connect Execute returned error: %v", err)
	}
	if !connect.Success {
		t.Fatalf("connect failed: %s", connect.ErrorMessage)
	}
	if connect.DependencyType != "websocket" {
		t.Fatalf("DependencyType = %q, want websocket", connect.DependencyType)
	}
	if connect.DependencyTarget == "" {
		t.Fatal("DependencyTarget should be populated")
	}
	if connect.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("StatusCode = %d, want %d", connect.StatusCode, http.StatusSwitchingProtocols)
	}

	send, err := ws.Execute(context.Background(), &protocol.Action{
		Name: "send",
		Type: "ws_send",
		Body: []byte(`hello apexload token=abc123`),
	})
	if err != nil {
		t.Fatalf("send Execute returned error: %v", err)
	}
	if !send.Success {
		t.Fatalf("send failed: %s", send.ErrorMessage)
	}
	if send.BytesSent != int64(len(`hello apexload token=abc123`)) {
		t.Fatalf("BytesSent = %d", send.BytesSent)
	}
	if send.ServerTelemetry == nil || send.ServerTelemetry.MessagesSent != 1 {
		t.Fatalf("send ServerTelemetry = %#v", send.ServerTelemetry)
	}

	receive, err := ws.Execute(context.Background(), &protocol.Action{
		Name:   "receive",
		Type:   "ws_receive",
		Params: map[string]any{"timeout_ms": float64(1000), "match": "apexload"},
		Extractors: []protocol.Extractor{{
			Name:       "token",
			Source:     "body",
			Type:       "regex",
			Expression: `token=([a-z0-9]+)`,
		}},
		Assertions: []protocol.Assertion{{
			Type:  "message_contains",
			Value: "hello",
		}},
	})
	if err != nil {
		t.Fatalf("receive Execute returned error: %v", err)
	}
	if !receive.Success {
		t.Fatalf("receive failed: %s", receive.ErrorMessage)
	}
	if receive.ResponseBody != `hello apexload token=abc123` {
		t.Fatalf("ResponseBody = %q", receive.ResponseBody)
	}
	if receive.Extractions["token"] != "abc123" {
		t.Fatalf("token extraction = %q", receive.Extractions["token"])
	}
	if receive.ServerTelemetry == nil || receive.ServerTelemetry.MessagesReceived != 1 {
		t.Fatalf("receive ServerTelemetry = %#v", receive.ServerTelemetry)
	}

	closeResult, err := ws.Execute(context.Background(), &protocol.Action{
		Name:   "close",
		Type:   "ws_close",
		Params: map[string]any{"reason": "done"},
	})
	if err != nil {
		t.Fatalf("close Execute returned error: %v", err)
	}
	if !closeResult.Success {
		t.Fatalf("close failed: %s", closeResult.ErrorMessage)
	}
}

func TestWebSocketSubprotocolOriginAndPing(t *testing.T) {
	server := newEchoServer(t, "graphql-ws")
	defer server.Close()

	ws := &WebSocket{}
	if err := ws.Init(context.Background(), map[string]any{
		"base_url":                   server.URL,
		"websocket_subprotocol":      "graphql-ws",
		"websocket_origin":           "https://apexload.example",
		"websocket_compression":      true,
		"websocket_write_timeout_ms": 1000,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	connect, err := ws.Execute(context.Background(), &protocol.Action{Name: "connect", Type: "ws_connect", Target: "/ws"})
	if err != nil {
		t.Fatalf("connect Execute returned error: %v", err)
	}
	if !connect.Success {
		t.Fatalf("connect failed: %s", connect.ErrorMessage)
	}

	ping, err := ws.Execute(context.Background(), &protocol.Action{Name: "ping", Type: "ws_ping", Body: []byte("probe")})
	if err != nil {
		t.Fatalf("ping Execute returned error: %v", err)
	}
	if !ping.Success {
		t.Fatalf("ping failed: %s", ping.ErrorMessage)
	}
	if ping.BytesSent != int64(len("probe")) {
		t.Fatalf("BytesSent = %d", ping.BytesSent)
	}
}

func TestWebSocketReceiveMatchFailure(t *testing.T) {
	server := newEchoServer(t, "")
	defer server.Close()

	ws := &WebSocket{}
	if err := ws.Init(context.Background(), map[string]any{"base_url": server.URL}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if result, _ := ws.Execute(context.Background(), &protocol.Action{Name: "connect", Type: "ws_connect", Target: "/ws"}); !result.Success {
		t.Fatalf("connect failed: %s", result.ErrorMessage)
	}
	if result, _ := ws.Execute(context.Background(), &protocol.Action{Name: "send", Type: "ws_send", Body: []byte("actual message")}); !result.Success {
		t.Fatalf("send failed: %s", result.ErrorMessage)
	}

	receive, err := ws.Execute(context.Background(), &protocol.Action{
		Name:   "receive",
		Type:   "ws_receive",
		Params: map[string]any{"timeout_ms": 1000, "match": "missing"},
	})
	if err != nil {
		t.Fatalf("receive Execute returned error: %v", err)
	}
	if receive.Success {
		t.Fatal("receive should fail when match text is absent")
	}
	if !strings.Contains(receive.ErrorMessage, "missing") {
		t.Fatalf("ErrorMessage = %q", receive.ErrorMessage)
	}
	if receive.Snapshot == nil {
		t.Fatal("Snapshot = nil, want populated websocket snapshot")
	}
	if receive.Snapshot.RequestMethod != "ws_receive" {
		t.Fatalf("Snapshot.RequestMethod = %q, want ws_receive", receive.Snapshot.RequestMethod)
	}
	if receive.Snapshot.RequestURL == "" || !strings.Contains(receive.Snapshot.RequestURL, "/ws") {
		t.Fatalf("Snapshot.RequestURL = %q, want websocket URL", receive.Snapshot.RequestURL)
	}
	if receive.Snapshot.ResponseBody != "actual message" {
		t.Fatalf("Snapshot.ResponseBody = %q, want actual message", receive.Snapshot.ResponseBody)
	}
}

func TestWebSocketExtractors(t *testing.T) {
	body := []byte(`{"payload":{"items":[{"id":"first"},{"id":"second"}],"token":"abc123"},"text":"left-VALUE-right"}`)
	tests := []struct {
		name      string
		extractor protocol.Extractor
		want      string
	}{
		{
			name:      "jsonpath",
			extractor: protocol.Extractor{Source: "body", Type: "jsonpath", Expression: "$.payload.items[1].id"},
			want:      "second",
		},
		{
			name:      "json alias",
			extractor: protocol.Extractor{Source: "message", Type: "json", Expression: "$.payload.token"},
			want:      "abc123",
		},
		{
			name:      "jmespath",
			extractor: protocol.Extractor{Source: "body", Type: "jmespath", Expression: "payload.items[0].id"},
			want:      "first",
		},
		{
			name:      "boundary",
			extractor: protocol.Extractor{Source: "body", Type: "boundary", Expression: `left-|||-right`},
			want:      "VALUE",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extract(tt.extractor, body)
			if err != nil {
				t.Fatalf("extract: %v", err)
			}
			if got != tt.want {
				t.Fatalf("extract = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWebSocketSendRequiresOpenConnection(t *testing.T) {
	ws := &WebSocket{}
	if err := ws.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	result, err := ws.Execute(context.Background(), &protocol.Action{Name: "send", Type: "ws_send", Body: []byte("hello")})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.Success {
		t.Fatal("send should fail without an open connection")
	}
	if !strings.Contains(result.ErrorMessage, "not open") {
		t.Fatalf("ErrorMessage = %q", result.ErrorMessage)
	}
}

func TestWebSocketSendReconnectsAfterClosedConnection(t *testing.T) {
	server := newEchoServer(t, "")
	defer server.Close()

	ws := &WebSocket{}
	if err := ws.Init(context.Background(), map[string]any{
		"base_url":                   server.URL,
		"websocket_reconnect":        true,
		"websocket_read_timeout_ms":  float64(1000),
		"websocket_write_timeout_ms": float64(1000),
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if result, _ := ws.Execute(context.Background(), &protocol.Action{Name: "connect", Type: "ws_connect", Target: "/ws"}); !result.Success {
		t.Fatalf("connect failed: %s", result.ErrorMessage)
	}

	ws.mu.Lock()
	stale := ws.conn
	ws.mu.Unlock()
	if stale == nil {
		t.Fatal("expected open connection")
	}
	_ = stale.Close()

	send, err := ws.Execute(context.Background(), &protocol.Action{Name: "send", Type: "ws_send", Body: []byte("reconnected")})
	if err != nil {
		t.Fatalf("send Execute returned error: %v", err)
	}
	if !send.Success {
		t.Fatalf("send failed after reconnect: %s", send.ErrorMessage)
	}

	receive, err := ws.Execute(context.Background(), &protocol.Action{Name: "receive", Type: "ws_receive", Params: map[string]any{"timeout_ms": float64(1000)}})
	if err != nil {
		t.Fatalf("receive Execute returned error: %v", err)
	}
	if !receive.Success {
		t.Fatalf("receive failed after reconnect: %s", receive.ErrorMessage)
	}
	if receive.ResponseBody != "reconnected" {
		t.Fatalf("ResponseBody = %q, want reconnected", receive.ResponseBody)
	}
}

func BenchmarkWebSocketEchoRoundTrip(b *testing.B) {
	server := newEchoServer(b, "")
	defer server.Close()

	ws := &WebSocket{}
	if err := ws.Init(context.Background(), map[string]any{
		"base_url":                   server.URL,
		"websocket_read_timeout_ms":  float64(5000),
		"websocket_write_timeout_ms": float64(5000),
		"websocket_max_message_size": float64(4096),
	}); err != nil {
		b.Fatalf("Init: %v", err)
	}
	defer ws.Teardown(context.Background())
	if result, _ := ws.Execute(context.Background(), &protocol.Action{Name: "connect", Type: "ws_connect", Target: "/ws"}); !result.Success {
		b.Fatalf("connect failed: %s", result.ErrorMessage)
	}

	send := &protocol.Action{Name: "send", Type: "ws_send", Body: []byte(`{"type":"ping","id":"benchmark"}`)}
	receive := &protocol.Action{Name: "receive", Type: "ws_receive", Params: map[string]any{"timeout_ms": float64(5000)}}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if result, _ := ws.Execute(context.Background(), send); !result.Success {
			b.Fatalf("send failed: %s", result.ErrorMessage)
		}
		if result, _ := ws.Execute(context.Background(), receive); !result.Success {
			b.Fatalf("receive failed: %s", result.ErrorMessage)
		}
	}
}

func newEchoServer(t testing.TB, subprotocol string) *httptest.Server {
	t.Helper()
	upgrader := gorillawebsocket.Upgrader{
		EnableCompression: true,
		CheckOrigin: func(r *http.Request) bool {
			if r.Header.Get("Origin") == "" {
				return true
			}
			return r.Header.Get("Origin") == "https://apexload.example"
		},
	}
	if subprotocol != "" {
		upgrader.Subprotocols = []string{subprotocol}
	}

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ws" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Test") != "" && r.Header.Get("X-Test") != "apexload" {
			t.Errorf("X-Test header = %q", r.Header.Get("X-Test"))
		}
		if r.Header.Get("Authorization") != "" && r.Header.Get("Authorization") != "Bearer runtime-token" {
			t.Errorf("Authorization header = %q", r.Header.Get("Authorization"))
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			return
		}
		if subprotocol != "" && conn.Subprotocol() != subprotocol {
			t.Errorf("Subprotocol = %q, want %q", conn.Subprotocol(), subprotocol)
		}
		conn.SetPongHandler(func(string) error { return nil })
		go func() {
			defer conn.Close()
			for {
				_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				messageType, message, err := conn.ReadMessage()
				if err != nil {
					return
				}
				if err := conn.WriteMessage(messageType, message); err != nil {
					return
				}
			}
		}()
	}))
}
