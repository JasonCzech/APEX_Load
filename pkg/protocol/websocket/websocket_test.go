package websocket

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestWebSocketJSONPathRejectsMalformedIndexesAndClassifiesMisses(t *testing.T) {
	body := []byte(`{"items":[{"id":"one"}]}`)
	for _, expression := range []string{
		"$.items[0", "$.items[]", "$.items[-1]", "$.items[0]junk",
		"$.", ".items", "$.items..id", "$.[0]", "$.items.[0]",
	} {
		t.Run(expression, func(t *testing.T) {
			if _, err := extractJSONPath(expression, body); err == nil || errors.Is(err, protocol.ErrExtractionMiss) {
				t.Fatalf("extractJSONPath(%q) error = %v, want configuration error", expression, err)
			}
		})
	}
	if _, err := extractJSONPath("$.missing", body); !errors.Is(err, protocol.ErrExtractionMiss) {
		t.Fatalf("missing field error = %v, want ErrExtractionMiss", err)
	}
}

func TestWebSocketLocalPayloadErrorsDoNotTriggerReconnect(t *testing.T) {
	ws := &WebSocket{
		reconnect:    true,
		lastTarget:   "ws://127.0.0.1:1/unreachable",
		dialer:       gorillawebsocket.DefaultDialer,
		writeTimeout: time.Second,
	}
	result := &protocol.Result{}

	oversizedMessage := &protocol.Action{Type: "ws_send", Body: make([]byte, dsl.MaxWebSocketMessageBytes+1)}
	if err := ws.send(context.Background(), oversizedMessage, result); err == nil || !strings.Contains(err.Error(), "message exceeds") {
		t.Fatalf("oversized send error = %v, want local message-size error", err)
	}
	oversizedPing := &protocol.Action{Type: "ws_ping", Body: make([]byte, maxWebSocketControlPayloadBytes+1)}
	if err := ws.ping(context.Background(), oversizedPing, result); err == nil || !strings.Contains(err.Error(), "ping payload exceeds") {
		t.Fatalf("oversized ping error = %v, want local ping-size error", err)
	}
	oversizedClose := &protocol.Action{
		Type:   "ws_close",
		Params: map[string]any{"reason": strings.Repeat("x", maxWebSocketCloseReasonBytes+1)},
	}
	if err := ws.close(oversizedClose, result); err == nil || !strings.Contains(err.Error(), "close reason exceeds") {
		t.Fatalf("oversized close error = %v, want local close-reason-size error", err)
	}
	invalidUTF8Close := &protocol.Action{Type: "ws_close", Params: map[string]any{"reason": string([]byte{0xff})}}
	if err := ws.close(invalidUTF8Close, result); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("invalid UTF-8 close error = %v, want local UTF-8 error", err)
	}
	ws.mu.Lock()
	closedByAction := ws.closedByAction
	ws.mu.Unlock()
	if closedByAction {
		t.Fatal("invalid local close payload changed connection state")
	}
}

func TestWebSocketCloseRejectsInvalidCodesBeforeStateMutation(t *testing.T) {
	for _, code := range []any{
		999, 1004, 1005, 1006, 1014, 1015, 1016, 2999, 5000, -1,
		1000.5, "1000", math.NaN(), math.Inf(1),
	} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			ws := &WebSocket{
				reconnect:  true,
				lastTarget: "ws://127.0.0.1:1/unreachable",
			}
			err := ws.close(&protocol.Action{
				Type:   "ws_close",
				Params: map[string]any{"code": code},
			}, &protocol.Result{})
			if err == nil || !strings.Contains(err.Error(), "close code") {
				t.Fatalf("close code %v error = %v, want local configuration error", code, err)
			}
			ws.mu.Lock()
			closedByAction := ws.closedByAction
			ws.mu.Unlock()
			if closedByAction {
				t.Fatalf("invalid close code %v changed connection state", code)
			}
		})
	}
}

func TestWebSocketCloseAcceptsStandardAndApplicationCodes(t *testing.T) {
	for _, code := range []any{1000, 1013, 3000, 4999, float64(1001)} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			ws := &WebSocket{}
			if err := ws.close(&protocol.Action{
				Type:   "ws_close",
				Params: map[string]any{"code": code},
			}, &protocol.Result{}); err != nil {
				t.Fatalf("close code %v rejected: %v", code, err)
			}
			ws.mu.Lock()
			closedByAction := ws.closedByAction
			ws.mu.Unlock()
			if !closedByAction {
				t.Fatalf("valid close code %v did not close connection state", code)
			}
		})
	}
}

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

func TestWebSocketEnforcesResolvedActionTargetPolicy(t *testing.T) {
	ws := &WebSocket{}
	if err := ws.Init(context.Background(), map[string]any{
		"base_url":                     "wss://allowed.example.test/socket",
		protocol.TargetPolicyConfigKey: []string{"allowed.example.test"},
	}); err != nil {
		t.Fatal(err)
	}
	result, err := ws.Execute(context.Background(), &protocol.Action{
		Name: "blocked", Type: "ws_connect", Target: "wss://attacker.example/socket",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "attacker.example") {
		t.Fatalf("WebSocket policy result = %#v", result)
	}
}

func TestWebSocketRejectsUnboundedTimeouts(t *testing.T) {
	ws := &WebSocket{}
	err := ws.Init(context.Background(), map[string]any{
		"websocket_read_timeout_ms": dsl.MaxConfiguredProtocolTimeout.Milliseconds() + 1,
	})
	if err == nil || !strings.Contains(err.Error(), "between 0") {
		t.Fatalf("Init error = %v, want timeout cap rejection", err)
	}
}

func TestWebSocketReceiveHonorsContextCancellation(t *testing.T) {
	server := newEchoServer(t, "")
	defer server.Close()
	ws := &WebSocket{}
	if err := ws.Init(context.Background(), map[string]any{
		"base_url":                  server.URL,
		"websocket_read_timeout_ms": dsl.MaxConfiguredProtocolTimeoutMS,
	}); err != nil {
		t.Fatal(err)
	}
	defer ws.Teardown(context.Background())
	if result, _ := ws.Execute(context.Background(), &protocol.Action{Name: "connect", Type: "ws_connect", Target: "/ws"}); !result.Success {
		t.Fatalf("connect failed: %s", result.ErrorMessage)
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	result, err := ws.Execute(ctx, &protocol.Action{Name: "receive", Type: "ws_receive"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || time.Since(start) > time.Second {
		t.Fatalf("receive ignored cancellation: success=%v elapsed=%s error=%q", result.Success, time.Since(start), result.ErrorMessage)
	}
}

func TestWebSocketActionRejectsUnboundedTimeout(t *testing.T) {
	server := newEchoServer(t, "")
	defer server.Close()
	ws := &WebSocket{}
	if err := ws.Init(context.Background(), map[string]any{"base_url": server.URL}); err != nil {
		t.Fatal(err)
	}
	defer ws.Teardown(context.Background())
	if result, _ := ws.Execute(context.Background(), &protocol.Action{Name: "connect", Type: "ws_connect", Target: "/ws"}); !result.Success {
		t.Fatalf("connect failed: %s", result.ErrorMessage)
	}
	result, err := ws.Execute(context.Background(), &protocol.Action{
		Name: "receive", Type: "ws_receive",
		Params: map[string]any{"timeout_ms": dsl.MaxConfiguredProtocolTimeout.Milliseconds() + 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Success || !strings.Contains(result.ErrorMessage, "between 0") {
		t.Fatalf("unbounded action timeout result = %#v", result)
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
	if result.ByteEvidence != protocol.ByteEvidenceNone {
		t.Fatalf("send ByteEvidence without connection = %q, want %q", result.ByteEvidence, protocol.ByteEvidenceNone)
	}
	recvResult, err := ws.Execute(context.Background(), &protocol.Action{Name: "recv", Type: "ws_receive"})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if recvResult.Success || recvResult.ByteEvidence != protocol.ByteEvidenceNone {
		t.Fatalf("recv without connection = success:%v byte_evidence:%q, want false / %q", recvResult.Success, recvResult.ByteEvidence, protocol.ByteEvidenceNone)
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

func TestWebSocketSendReconnectsAfterReceiveTimeout(t *testing.T) {
	server := newEchoServer(t, "")
	defer server.Close()

	ws := &WebSocket{}
	if err := ws.Init(context.Background(), map[string]any{
		"base_url":                   server.URL,
		"websocket_reconnect":        true,
		"websocket_read_timeout_ms":  float64(25),
		"websocket_write_timeout_ms": float64(1000),
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if result, _ := ws.Execute(context.Background(), &protocol.Action{Name: "connect", Type: "ws_connect", Target: "/ws"}); !result.Success {
		t.Fatalf("connect failed: %s", result.ErrorMessage)
	}

	timedOut, err := ws.Execute(context.Background(), &protocol.Action{
		Name: "timeout", Type: "ws_receive", Params: map[string]any{"timeout_ms": float64(25)},
	})
	if err != nil {
		t.Fatalf("receive Execute returned error: %v", err)
	}
	if timedOut.Success {
		t.Fatal("receive unexpectedly succeeded without a message")
	}

	sent, err := ws.Execute(context.Background(), &protocol.Action{Name: "send", Type: "ws_send", Body: []byte("after-timeout")})
	if err != nil {
		t.Fatalf("send Execute returned error: %v", err)
	}
	if !sent.Success {
		t.Fatalf("send did not reconnect after receive timeout: %s", sent.ErrorMessage)
	}
	received, _ := ws.Execute(context.Background(), &protocol.Action{
		Name: "receive", Type: "ws_receive", Params: map[string]any{"timeout_ms": float64(1000)},
	})
	if !received.Success || received.ResponseBody != "after-timeout" {
		t.Fatalf("receive after reconnect = success:%t body:%q error:%q", received.Success, received.ResponseBody, received.ErrorMessage)
	}
}

func TestWebSocketExplicitCloseDoesNotReconnect(t *testing.T) {
	server := newEchoServer(t, "")
	defer server.Close()

	ws := &WebSocket{}
	if err := ws.Init(context.Background(), map[string]any{
		"base_url":            server.URL,
		"websocket_reconnect": true,
	}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if result, _ := ws.Execute(context.Background(), &protocol.Action{Name: "connect", Type: "ws_connect", Target: "/ws"}); !result.Success {
		t.Fatalf("connect failed: %s", result.ErrorMessage)
	}
	if result, _ := ws.Execute(context.Background(), &protocol.Action{Name: "close", Type: "ws_close"}); !result.Success {
		t.Fatalf("close failed: %s", result.ErrorMessage)
	}
	sent, _ := ws.Execute(context.Background(), &protocol.Action{Name: "send", Type: "ws_send", Body: []byte("must-not-reopen")})
	if sent.Success || !strings.Contains(sent.ErrorMessage, "not open") {
		t.Fatalf("send after explicit close = success:%t error:%q", sent.Success, sent.ErrorMessage)
	}
}

func TestWebSocketExplicitCloseWinsAgainstInflightReconnect(t *testing.T) {
	reconnectStarted := make(chan struct{})
	releaseReconnect := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		close(reconnectStarted)
		<-releaseReconnect
		conn, err := (&gorillawebsocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(writer, request, nil)
		if err == nil {
			defer conn.Close()
			_, _, _ = conn.ReadMessage()
		}
	}))
	defer server.Close()

	ws := &WebSocket{
		reconnect:  true,
		lastTarget: strings.Replace(server.URL, "http://", "ws://", 1),
		dialer:     &gorillawebsocket.Dialer{HandshakeTimeout: time.Second},
	}
	reconnectDone := make(chan error, 1)
	go func() {
		_, err := ws.reconnectConn(context.Background(), &protocol.Result{})
		reconnectDone <- err
	}()
	select {
	case <-reconnectStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect handshake did not start")
	}
	if err := ws.close(&protocol.Action{Type: "ws_close"}, &protocol.Result{}); err != nil {
		t.Fatalf("explicit close: %v", err)
	}
	close(releaseReconnect)
	select {
	case err := <-reconnectDone:
		if err == nil || !strings.Contains(err.Error(), "closed while reconnecting") {
			t.Fatalf("reconnect error = %v, want explicit-close fence", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconnect did not finish")
	}
	ws.mu.Lock()
	conn, closedByAction := ws.conn, ws.closedByAction
	ws.mu.Unlock()
	if conn != nil || !closedByAction {
		t.Fatalf("connection state after close/reconnect race = conn:%v closed:%t", conn, closedByAction)
	}
}

func TestWebSocketInvalidExtractorIsNotAnOnMiss(t *testing.T) {
	extractor := protocol.Extractor{Name: "token", Type: "regex", Expression: "[", OnMiss: "ignore"}
	value, extractionErr := extract(extractor, []byte("token=value"))
	result := &protocol.Result{}
	if err := protocol.ApplyExtraction(result, extractor, value, extractionErr); err == nil || !strings.Contains(err.Error(), "invalid regex") {
		t.Fatalf("ApplyExtraction error = %v, want invalid regex failure", err)
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

func TestWebSocketPreDialFailureMarksAttemptedHeaders(t *testing.T) {
	ws := &WebSocket{unredactedDebugSnapshots: true, defaultHeaders: http.Header{"X-Default": {"default"}}, origin: "https://example.test"}
	result, err := ws.Execute(context.Background(), &protocol.Action{Type: "ws_connect", Target: "://bad", HeaderList: []protocol.HeaderEntry{{Name: "X-Multi", Value: "one"}, {Name: "X-Multi", Value: "two"}}})
	if err != nil || result.Success || result.Snapshot == nil || !result.Snapshot.RequestWireHeadersUnavailable || result.Snapshot.RequestHeaders["X-Multi"] != "one\ntwo" || result.Snapshot.RequestHeaders["X-Default"] != "default" || result.Snapshot.RequestHeaders["Origin"] != "https://example.test" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

func TestWebSocketEpochFencePreservesActualHandshakeEvidence(t *testing.T) {
	for _, reconnect := range []bool{false, true} {
		for _, debug := range []bool{false, true} {
			t.Run(fmt.Sprintf("reconnect=%t/debug=%t", reconnect, debug), func(t *testing.T) {
				started := make(chan *http.Request, 1)
				release := make(chan struct{})
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					started <- r.Clone(context.Background())
					<-release
					conn, err := (&gorillawebsocket.Upgrader{}).Upgrade(w, r, nil)
					if err == nil {
						defer conn.Close()
						_, _, _ = conn.ReadMessage()
					}
				}))
				defer server.Close()
				target := strings.Replace(server.URL, "http://", "ws://", 1) + "/socket"
				ws := &WebSocket{unredactedDebugSnapshots: debug, reconnect: true, lastTarget: target, lastHeaders: http.Header{"X-Secret": {"secret"}, "X-Multi": {"one", "two"}}, defaultHeaders: http.Header{"X-Secret": {"secret"}}, dialer: &gorillawebsocket.Dialer{HandshakeTimeout: time.Second}, writeTimeout: time.Second}
				action := &protocol.Action{Type: "ws_connect", Target: target, Headers: map[string]string{"X-Secret": "secret"}, RedactedHeaders: map[string]string{"X-Secret": "[REDACTED]"}, HeaderList: []protocol.HeaderEntry{{Name: "X-Multi", Value: "one"}, {Name: "X-Multi", Value: "two"}}}
				if reconnect {
					action.Type = "ws_send"
					action.Target = "/different"
				}
				done := make(chan *protocol.Result, 1)
				go func() { result, _ := ws.Execute(context.Background(), action); done <- result }()
				var actual *http.Request
				select {
				case actual = <-started:
				case <-time.After(2 * time.Second):
					t.Fatal("handshake did not start")
				}
				if err := ws.Teardown(context.Background()); err != nil {
					t.Fatal(err)
				}
				close(release)
				var result *protocol.Result
				select {
				case result = <-done:
				case <-time.After(2 * time.Second):
					t.Fatal("handshake did not finish")
				}
				if result.Success || result.Snapshot == nil {
					t.Fatalf("result=%#v", result)
				}
				if debug {
					method := "ws_connect"
					if reconnect {
						method = "ws_reconnect"
					}
					snapshot := result.Snapshot
					if snapshot.RequestMethod != method || snapshot.RequestURL != target || snapshot.ResponseStatus != http.StatusSwitchingProtocols || snapshot.RequestWireHeadersUnavailable || snapshot.RequestHeaders["X-Secret"] != "secret" || snapshot.RequestHeaders["X-Multi"] != "one\ntwo" || snapshot.RequestHeaders["Sec-WebSocket-Key"] != actual.Header.Get("Sec-Websocket-Key") {
						t.Fatalf("snapshot=%#v", snapshot)
					}
				} else if result.Snapshot.RequestHeaders["X-Secret"] == "secret" {
					t.Fatal("ordinary snapshot leaked secret")
				}
			})
		}
	}
}

func TestHandshakeWireHeadersReportsInvalidUTF8AtBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		omitted bool
	}{
		{"valid-boundary", strings.Repeat("a", 16<<10), false},
		{"invalid-equal-length", strings.Repeat("a", (16<<10)-3) + string([]byte{0xff}) + "xy", true},
		{"oversize", strings.Repeat("b", (16<<10)+1), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &handshakeWireHeaders{headers: make(map[string]string), remaining: 256 << 10}
			h.add("X-Value", []string{tc.value})
			if h.omitted != tc.omitted || len(h.headers["X-Value"]) > 16<<10 {
				t.Fatalf("omitted=%t length=%d", h.omitted, len(h.headers["X-Value"]))
			}
		})
	}
}

func TestHandshakeFallbackReportsUTF8Normalization(t *testing.T) {
	snapshot := &protocol.ErrorSnapshot{}
	captureHandshakeHeaders(snapshot, http.Header{"X-Invalid": {"a" + string([]byte{0xff}) + "b"}}, nil, nil)
	if !snapshot.RequestWireHeadersUnavailable || !snapshot.RequestHeadersOmitted || snapshot.RequestHeaders["X-Invalid"] != "a�b" {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}
