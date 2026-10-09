package http

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// WebSocket steps inside an HTTP script (ws_connect, ws_send, ws_on_message,
// ...) run on a WebSocket client the script's HTTP protocol owns. The client
// is created from the protocol registry on the first WebSocket step and
// shares the HTTP cookie jar and TLS settings, so a socket opened after an
// HTTP login carries the session cookie. Per-version clones share it.
type webSocketSession struct {
	mu     sync.Mutex
	config map[string]any
	client protocol.Protocol
}

// httpSessionSharer is implemented by the websocket protocol.
type httpSessionSharer interface {
	ShareHTTPSession(jar http.CookieJar, tlsConfig *tls.Config)
}

// IsWebSocketStep reports whether an HTTP-script action is a WebSocket step.
func IsWebSocketStep(actionType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(actionType)), "ws_")
}

func (h *HTTP) executeWebSocketStep(ctx context.Context, action *protocol.Action) (*protocol.Result, error) {
	client, err := h.webSocketClient(ctx, true)
	if err != nil {
		return &protocol.Result{ActionName: protocol.ActionName(action), StartTime: time.Now(), DependencyType: "websocket", ErrorMessage: err.Error()}, nil
	}
	return client.Execute(ctx, action)
}

// webSocketClient returns the session's client, creating it when create is
// set.
func (h *HTTP) webSocketClient(ctx context.Context, create bool) (protocol.Protocol, error) {
	session := h.webSockets
	if session == nil {
		return nil, fmt.Errorf("WebSocket steps need an initialized HTTP protocol")
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.client != nil || !create {
		return session.client, nil
	}
	client, err := protocol.Get("websocket")
	if err != nil {
		return nil, fmt.Errorf("WebSocket steps are not available: %w", err)
	}
	if err := client.Init(ctx, session.config); err != nil {
		return nil, fmt.Errorf("initialize WebSocket client: %w", err)
	}
	if sharer, ok := client.(httpSessionSharer); ok {
		var jar http.CookieJar
		if h.client != nil {
			jar = h.client.Jar
		}
		var tlsConfig *tls.Config
		if h.transport != nil {
			tlsConfig = h.transport.TLSClientConfig
		}
		sharer.ShareHTTPSession(jar, tlsConfig)
	}
	session.client = client
	return client, nil
}

// DrainMessageCallbacks implements protocol.MessageCallbackSource for the
// script's WebSocket steps; it returns nothing until a socket is used.
func (h *HTTP) DrainMessageCallbacks(ctx context.Context, wait bool) ([]protocol.CallbackMessage, error) {
	client, _ := h.webSocketClient(ctx, false)
	source, ok := client.(protocol.MessageCallbackSource)
	if !ok {
		return nil, nil
	}
	return source.DrainMessageCallbacks(ctx, wait)
}

// CallbackGeneration implements protocol.MessageCallbackSource.
func (h *HTTP) CallbackGeneration() uint64 {
	client, _ := h.webSocketClient(context.Background(), false)
	source, ok := client.(protocol.MessageCallbackSource)
	if !ok {
		return 0
	}
	return source.CallbackGeneration()
}

// teardownWebSockets closes the script's socket at virtual-user end.
func (h *HTTP) teardownWebSockets(ctx context.Context) {
	if h.webSockets == nil {
		return
	}
	h.webSockets.mu.Lock()
	client := h.webSockets.client
	h.webSockets.client = nil
	h.webSockets.mu.Unlock()
	if client != nil {
		_ = client.Teardown(ctx)
	}
}
