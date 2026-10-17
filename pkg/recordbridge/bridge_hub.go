package recordbridge

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// bridgeHub is the bounded, process-local event sink for paired recording
// clients. Controller fanout and account subscriptions belong to its API hub.
type bridgeHub struct {
	mu      sync.Mutex
	clients map[*bridgeClient]struct{}
	origins map[string]struct{}
	stopped bool
	workers sync.WaitGroup
}
type bridgeClient struct {
	conn    *websocket.Conn
	send    chan []byte
	done    chan struct{}
	project string
	test    string
}

func newBridgeHub() *bridgeHub {
	return &bridgeHub{clients: map[*bridgeClient]struct{}{}, origins: map[string]struct{}{}}
}
func (h *bridgeHub) SetAllowedOrigins(origins []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.origins = map[string]struct{}{}
	for _, origin := range origins {
		h.origins[origin] = struct{}{}
	}
}
func (h *bridgeHub) ClientCount() int { h.mu.Lock(); defer h.mu.Unlock(); return len(h.clients) }
func (h *bridgeHub) BroadcastScopedJSON(test string, project string, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopped {
		return
	}
	for client := range h.clients {
		if client.test != "" && client.test != strings.TrimSpace(test) {
			continue
		}
		if client.project != "" && client.project != strings.TrimSpace(project) {
			continue
		}
		select {
		case client.send <- raw:
		default:
		}
	}
}
func (h *bridgeHub) remove(client *bridgeClient) {
	h.mu.Lock()
	if _, ok := h.clients[client]; ok {
		delete(h.clients, client)
		close(client.done)
	}
	h.mu.Unlock()
	_ = client.conn.Close()
}
func (h *bridgeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	if h.stopped || len(h.clients) >= 1024 {
		h.mu.Unlock()
		http.Error(w, "websocket client limit reached or hub stopped", http.StatusServiceUnavailable)
		return
	}
	// Reserve admission while upgrading, so simultaneous handshakes cannot exceed
	// the same bounded population as established clients.
	client := &bridgeClient{send: make(chan []byte, 64), done: make(chan struct{}), project: strings.TrimSpace(r.URL.Query().Get("project_id")), test: strings.TrimSpace(r.URL.Query().Get("test_id"))}
	h.clients[client] = struct{}{}
	h.mu.Unlock()
	up := websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 4096, CheckOrigin: func(r *http.Request) bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		origin := strings.TrimSpace(r.Header.Get("Origin"))
		if origin == "" {
			return true
		}
		_, ok := h.origins[origin]
		return ok
	}}
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		h.mu.Lock()
		delete(h.clients, client)
		h.mu.Unlock()
		return
	}
	h.mu.Lock()
	client.conn = conn
	if h.stopped {
		delete(h.clients, client)
		h.mu.Unlock()
		_ = conn.Close()
		return
	}
	h.workers.Add(2)
	h.mu.Unlock()
	go func() {
		defer h.workers.Done()
		defer h.remove(client)
		conn.SetReadLimit(4096)
		_ = conn.SetReadDeadline(time.Now().Add(time.Minute))
		conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(time.Minute)) })
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	go func() {
		defer h.workers.Done()
		defer h.remove(client)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-client.done:
				return
			case raw := <-client.send:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if conn.WriteMessage(websocket.TextMessage, raw) != nil {
					return
				}
			case <-ticker.C:
				_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if conn.WriteMessage(websocket.PingMessage, nil) != nil {
					return
				}
			}
		}
	}()
}
func (h *bridgeHub) Stop() {
	h.mu.Lock()
	h.stopped = true
	for client := range h.clients {
		delete(h.clients, client)
		close(client.done)
		if client.conn != nil {
			_ = client.conn.Close()
		}
	}
	h.mu.Unlock()
	h.workers.Wait()
}
