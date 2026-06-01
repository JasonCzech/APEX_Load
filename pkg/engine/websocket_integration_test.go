package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/dsl"
	_ "github.com/jasonczech/apexload/pkg/protocol/websocket"
)

func TestEngineRunsWebSocketScriptThroughProtocol(t *testing.T) {
	upgrader := gorillawebsocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Upgrade: %v", err)
			return
		}
		go func() {
			defer conn.Close()
			for {
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
	defer server.Close()

	script := &dsl.Script{
		Name:     "websocket engine",
		Protocol: "websocket",
		Config: dsl.ScriptConfig{
			BaseURL: server.URL,
			WebSocket: dsl.WebSocketConfig{
				ReadTimeoutMs:  1000,
				WriteTimeoutMs: 1000,
			},
			RunLogic: dsl.RunLogicConfig{Iterations: 1},
		},
		Init: []dsl.Action{{
			Name:   "connect",
			Type:   "ws_connect",
			Target: "/",
		}},
		Actions: []dsl.Action{{
			Name: "send",
			Type: "ws_send",
			Body: "hello token=abc123",
		}, {
			Name:   "receive",
			Type:   "ws_receive",
			Params: map[string]any{"timeout_ms": 1000, "match": "hello"},
			Extractors: []dsl.Extractor{{
				Name:       "token",
				Source:     "body",
				Type:       "regex",
				Expression: `token=([a-z0-9]+)`,
			}},
		}},
		Teardown: []dsl.Action{{
			Name: "close",
			Type: "ws_close",
		}},
	}

	engine, err := New(Config{Script: script, VUsers: 1, Duration: 2 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.TotalErrors != 0 {
		t.Fatalf("TotalErrors = %d, want 0", result.TotalErrors)
	}
	if result.TotalTransactions != 4 {
		t.Fatalf("TotalTransactions = %d, want 4", result.TotalTransactions)
	}
}
