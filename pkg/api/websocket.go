package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type Hub struct {
	clients           map[*Client]bool
	clientsMu         sync.RWMutex
	broadcast         chan hubBroadcast
	register          chan *Client
	unregister        chan *Client
	done              chan struct{}
	stopOnce          sync.Once
	droppedBroadcasts atomic.Int64
	droppedClientMsgs atomic.Int64
	fanoutMu          sync.RWMutex
	fanout            FanoutBus
	fanoutChannel     string
	fanoutOrigin      string
	allowedOriginsMu  sync.RWMutex
	allowedOrigins    []string
}

// SetAllowedOrigins configures the Origin allow-list enforced during the
// WebSocket upgrade. It shares originAllowed with the CORS middleware so the
// two policies cannot drift.
func (h *Hub) SetAllowedOrigins(origins []string) {
	if h == nil {
		return
	}
	h.allowedOriginsMu.Lock()
	defer h.allowedOriginsMu.Unlock()
	h.allowedOrigins = append([]string(nil), origins...)
}

func (h *Hub) upgrader() websocket.Upgrader {
	return websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 4096,
		CheckOrigin: func(r *http.Request) bool {
			h.allowedOriginsMu.RLock()
			allowedOrigins := append([]string(nil), h.allowedOrigins...)
			h.allowedOriginsMu.RUnlock()
			return originAllowed(allowedOrigins, r)
		},
	}
}

type HubStats struct {
	ConnectedClients      int   `json:"connected_clients"`
	DroppedBroadcasts     int64 `json:"dropped_broadcasts,omitempty"`
	DroppedClientMessages int64 `json:"dropped_client_messages,omitempty"`
}

type Client struct {
	hub        *Hub
	conn       *websocket.Conn
	send       chan []byte
	testID     string // empty = all tests, set = specific test
	projectID  string // empty = all projects, set = specific project
	authorized func() bool
}

type websocketFanoutEnvelope struct {
	Origin       string          `json:"origin"`
	SentAt       time.Time       `json:"sent_at"`
	TestID       string          `json:"test_id,omitempty"`
	ProjectID    string          `json:"project_id,omitempty"`
	BroadcastAll bool            `json:"broadcast_all,omitempty"`
	Payload      json.RawMessage `json:"payload"`
}

type hubBroadcast struct {
	payload      []byte
	testID       string
	projectID    string
	broadcastAll bool
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan hubBroadcast, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		done:       make(chan struct{}),
	}
}

func (h *Hub) SetFanout(bus FanoutBus, origin string, channel string) {
	if h == nil {
		return
	}
	h.fanoutMu.Lock()
	defer h.fanoutMu.Unlock()
	h.fanout = bus
	h.fanoutOrigin = origin
	h.fanoutChannel = channel
}

func (h *Hub) Run() {
	for {
		select {
		case <-h.done:
			h.clientsMu.Lock()
			for c := range h.clients {
				close(c.send)
				delete(h.clients, c)
			}
			h.clientsMu.Unlock()
			return

		case client := <-h.register:
			h.clientsMu.Lock()
			h.clients[client] = true
			h.clientsMu.Unlock()
			slog.Info("websocket client connected", "test_filter", client.testID, "project_filter", client.projectID, "clients", len(h.clients))

		case client := <-h.unregister:
			h.clientsMu.Lock()
			if _, ok := h.clients[client]; ok {
				close(client.send)
				delete(h.clients, client)
			}
			h.clientsMu.Unlock()
			slog.Info("websocket client disconnected", "clients", len(h.clients))

		case msg := <-h.broadcast:
			h.clientsMu.RLock()
			for client := range h.clients {
				if !msg.broadcastAll {
					if client.testID != "" && msg.testID != client.testID {
						continue
					}
					if client.projectID != "" && msg.projectID != client.projectID {
						continue
					}
				}
				select {
				case client.send <- msg.payload:
				default:
					h.droppedClientMsgs.Add(1)
					// slow client — drop message
				}
			}
			h.clientsMu.RUnlock()
		}
	}
}

func (h *Hub) Stop() {
	if h == nil {
		return
	}
	h.stopOnce.Do(func() { close(h.done) })
}

func (h *Hub) Broadcast(msg []byte) {
	broadcast := newHubBroadcast(msg)
	h.broadcastLocal(broadcast)
	h.publishFanout(broadcast)
}

func (h *Hub) broadcastLocal(msg hubBroadcast) {
	select {
	case h.broadcast <- msg:
	default:
		h.droppedBroadcasts.Add(1)
	}
}

func (h *Hub) publishFanout(msg hubBroadcast) {
	h.fanoutMu.RLock()
	bus := h.fanout
	channel := h.fanoutChannel
	origin := h.fanoutOrigin
	h.fanoutMu.RUnlock()
	if bus == nil || channel == "" || origin == "" || len(msg.payload) == 0 {
		return
	}
	payload, err := json.Marshal(websocketFanoutEnvelope{
		Origin:       origin,
		SentAt:       time.Now().UTC(),
		TestID:       msg.testID,
		ProjectID:    msg.projectID,
		BroadcastAll: msg.broadcastAll,
		Payload:      append([]byte(nil), msg.payload...),
	})
	if err != nil {
		slog.Warn("failed to marshal websocket fanout envelope", "err", err)
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), defaultCacheWriteTimeout)
		defer cancel()
		if err := bus.Publish(ctx, channel, payload); err != nil {
			slog.Warn("failed to publish websocket fanout envelope", "err", err)
		}
	}()
}

func (h *Hub) BroadcastJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		slog.Error("failed to marshal broadcast", "err", err)
		return
	}
	h.Broadcast(data)
}

func (h *Hub) BroadcastScopedJSON(testID string, projectID string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		slog.Error("failed to marshal broadcast", "err", err)
		return
	}
	broadcast := hubBroadcast{
		payload:   data,
		testID:    strings.TrimSpace(testID),
		projectID: strings.TrimSpace(projectID),
	}
	h.broadcastLocal(broadcast)
	h.publishFanout(broadcast)
}

func newHubBroadcast(payload []byte) hubBroadcast {
	var envelope struct {
		TestID       string `json:"test_id"`
		ProjectID    string `json:"project_id"`
		BroadcastAll bool   `json:"broadcast_all"`
	}
	_ = json.Unmarshal(payload, &envelope)
	// Honor the bypass only for a genuinely global, scope-free message. If a payload
	// sets broadcast_all but ALSO carries a test_id/project_id (a misuse that would
	// otherwise leak scoped data to every client), fall back to normal filtering.
	broadcastAll := envelope.BroadcastAll && envelope.TestID == "" && envelope.ProjectID == ""
	return hubBroadcast{
		payload:      payload,
		testID:       envelope.TestID,
		projectID:    envelope.ProjectID,
		broadcastAll: broadcastAll,
	}
}

func (h *Hub) ClientCount() int {
	h.clientsMu.RLock()
	defer h.clientsMu.RUnlock()
	return len(h.clients)
}

func (h *Hub) Stats() HubStats {
	if h == nil {
		return HubStats{}
	}
	return HubStats{
		ConnectedClients:      h.ClientCount(),
		DroppedBroadcasts:     h.droppedBroadcasts.Load(),
		DroppedClientMessages: h.droppedClientMsgs.Load(),
	}
}

func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request, testID string, projectID string, authorized ...func() bool) {
	up := h.upgrader()
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "err", err)
		return
	}

	client := &Client{
		hub:       h,
		conn:      conn,
		send:      make(chan []byte, 64),
		testID:    testID,
		projectID: projectID,
	}
	if len(authorized) > 0 {
		client.authorized = authorized[0]
	}

	select {
	case h.register <- client:
	case <-h.done:
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	case <-r.Context().Done():
		_ = conn.Close()
		return
	}

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		select {
		case c.hub.unregister <- c:
		case <-c.hub.done:
		}
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(4096)
	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case <-c.hub.done:
			return
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}

		case <-ticker.C:
			if c.authorized != nil && !c.authorized() {
				_ = c.conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "authorization expired or revoked"), time.Now().Add(time.Second))
				return
			}
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
