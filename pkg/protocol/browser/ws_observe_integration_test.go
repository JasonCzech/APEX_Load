package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBrowserReplayWebSocketSession(t *testing.T) {
	upgrader := gorillawebsocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws":
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Logf("upgrade failed: %v", err)
				return
			}
			defer conn.Close()
			_ = conn.WriteMessage(gorillawebsocket.TextMessage, []byte(`{"type":"ready","seq":1}`))
			_, _, _ = conn.ReadMessage()
		default:
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html>
<html><body>
  <div id="status">opening</div>
  <script>
    const wsURL = (location.protocol === 'https:' ? 'wss://' : 'ws://') + location.host + '/ws';
    const ws = new WebSocket(wsURL);
    ws.onopen = () => ws.send(JSON.stringify({type: 'client-ready'}));
    ws.onmessage = event => { document.querySelector('#status').textContent = event.data; };
  </script>
</body></html>`))
		}
	}))
	defer server.Close()

	b := &Browser{}
	if err := b.Init(context.Background(), map[string]any{
		"base_url":         server.URL,
		"browser_headless": true,
	}); err != nil {
		lower := strings.ToLower(err.Error())
		if strings.Contains(lower, "executable") || strings.Contains(lower, "chrome") {
			t.Skipf("browser runtime unavailable: %v", err)
		}
		t.Fatalf("Init returned error: %v", err)
	}
	defer b.Teardown(context.Background())

	navigate, err := b.Execute(context.Background(), &protocol.Action{
		Name:   "Open WebSocket page",
		Type:   "browser_navigate",
		Target: "/",
	})
	if err != nil {
		t.Fatalf("navigate returned error: %v", err)
	}
	if !navigate.Success {
		t.Fatalf("navigate failed: %s", navigate.ErrorMessage)
	}

	wait, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Wait for ready frame",
		Type: "browser_ws_wait",
		Params: map[string]any{
			"url_contains": "/ws",
			"direction":    "received",
			"match":        `"ready"`,
			"match_mode":   "contains",
			"timeout_ms":   3000,
			"count":        1,
		},
	})
	if err != nil {
		t.Fatalf("ws wait returned error: %v", err)
	}
	if !wait.Success {
		t.Fatalf("ws wait failed: %s", wait.ErrorMessage)
	}
	if wait.ProtocolEvidence["browser_ws_matches"] == nil {
		t.Fatalf("ws wait missing evidence: %+v", wait.ProtocolEvidence)
	}

	b.recordWSEvent(server.URL+"/ws", "received", []byte(`{"type":"ready","seq":2}`))
	assertResult, err := b.Execute(context.Background(), &protocol.Action{
		Name: "Assert ready frame",
		Type: "browser_ws_assert",
		Params: map[string]any{
			"url_contains": "/ws",
			"direction":    "received",
			"match":        `"ready"`,
			"match_mode":   "contains",
			"count":        1,
		},
	})
	if err != nil {
		t.Fatalf("ws assert returned error: %v", err)
	}
	if !assertResult.Success {
		t.Fatalf("ws assert failed: %s", assertResult.ErrorMessage)
	}
}
