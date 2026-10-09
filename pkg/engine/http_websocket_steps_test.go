package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gorillawebsocket "github.com/gorilla/websocket"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	_ "github.com/jasonczech/apexload/pkg/protocol/websocket"
)

var registerHTTPWebSocketProtocol sync.Once

// An HTTP script logs in over HTTP, then opens a socket whose handshake
// carries the session cookie, exchanges messages, and uses a value a
// message callback saved in a later HTTP step.
type chatFixture struct {
	server          *httptest.Server
	mu              sync.Mutex
	handshakeCookie string
	resultQuery     string
}

func newChatFixture(t *testing.T) *chatFixture {
	t.Helper()
	fixture := &chatFixture{}
	upgrader := gorillawebsocket.Upgrader{}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "SESSION", Value: "s-42", Path: "/"})
			w.WriteHeader(http.StatusOK)
		case "/chat":
			fixture.mu.Lock()
			fixture.handshakeCookie = r.Header.Get("Cookie")
			fixture.mu.Unlock()
			if !strings.Contains(r.Header.Get("Cookie"), "SESSION=s-42") {
				http.Error(w, "no session", http.StatusUnauthorized)
				return
			}
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				for {
					kind, message, err := conn.ReadMessage()
					if err != nil {
						return
					}
					_ = conn.WriteMessage(kind, append([]byte("reply:"), message...))
				}
			}()
		case "/result":
			fixture.mu.Lock()
			fixture.resultQuery = r.URL.RawQuery
			fixture.mu.Unlock()
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fixture.server.Close)
	return fixture
}

func (fixture *chatFixture) script(protocolName string) *dsl.Script {
	return &dsl.Script{Name: "chat", Protocol: protocolName, Config: dsl.ScriptConfig{BaseURL: fixture.server.URL, RunLogic: dsl.RunLogicConfig{Iterations: 1}}, Actions: []dsl.Action{
		{Name: "login", Type: "POST", Method: "POST", Target: "/login"},
		{Name: "connect", Type: "ws_connect", Target: "/chat"},
		{Name: "on message", Type: "ws_on_message", Control: &dsl.ControlFlow{Steps: []dsl.Action{
			{Name: "keep", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"{{var.ws_message}}", "last_message"}}},
		}}},
		{Name: "send", Type: "ws_send", Body: "hello"},
		{Name: "wait", Type: "http_sync", Params: map[string]any{"variable": "last_message", "timeout_ms": 5000}},
		{Name: "result", Type: "GET", Method: "GET", Target: "/result?last={{var.last_message}}"},
		{Name: "close", Type: "ws_close"},
	}}
}

func (fixture *chatFixture) check(t *testing.T) {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if !strings.Contains(fixture.handshakeCookie, "SESSION=s-42") {
		t.Fatalf("the handshake must carry the HTTP session cookie, got %q", fixture.handshakeCookie)
	}
	if fixture.resultQuery != "last=reply:hello" {
		t.Fatalf("the message callback's value must reach the next HTTP step, got %q", fixture.resultQuery)
	}
}

func registerWebSocketStepsHTTP() string {
	const protocolName = "http-websocket-steps-test"
	registerHTTPWebSocketProtocol.Do(func() {
		protocol.Register(protocolName, func() protocol.Protocol { return &httpprotocol.HTTP{} })
	})
	return protocolName
}

// An HTTP script logs in over HTTP, then opens a socket whose handshake
// carries the session cookie, exchanges messages, and uses a value a
// message callback saved in a later HTTP step.
func TestHTTPScriptRunsWebSocketStepsWithItsSession(t *testing.T) {
	fixture := newChatFixture(t)
	script := fixture.script(registerWebSocketStepsHTTP())
	if issues := dsl.ValidateScriptIssues(&dsl.Script{Name: script.Name, Protocol: "http", Config: script.Config, Actions: script.Actions}); len(issues) != 0 {
		t.Fatalf("an HTTP script with WebSocket steps must validate: %v", issues)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report, err := ReplayScript(ctx, script, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success {
		t.Fatalf("replay failed: %s %+v", report.Error, report.Events)
	}
	fixture.check(t)
}

// The same script under load: the engine's virtual users dispatch the
// socket's callbacks too.
func TestHTTPScriptWebSocketStepsUnderLoad(t *testing.T) {
	fixture := newChatFixture(t)
	engine, err := New(Config{Script: fixture.script(registerWebSocketStepsHTTP()), VUsers: 1, Duration: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.TotalErrors != 0 {
		t.Fatalf("TotalErrors = %d", result.TotalErrors)
	}
	fixture.check(t)
}
