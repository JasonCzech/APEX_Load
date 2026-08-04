package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	maxHubClients             = 1024
	maxHubFanoutPublishes     = 32
	websocketClientSendBuffer = 64
)

type Hub struct {
	clients            map[*Client]bool
	clientsMu          sync.RWMutex
	broadcast          chan hubBroadcast
	register           chan *Client
	unregister         chan *Client
	done               chan struct{}
	stopOnce           sync.Once
	clientSlots        chan struct{}
	droppedBroadcasts  atomic.Int64
	droppedClientMsgs  atomic.Int64
	rejectedClients    atomic.Int64
	fanoutMu           sync.RWMutex
	fanout             FanoutBus
	fanoutChannel      string
	fanoutOrigin       string
	fanoutCtx          context.Context
	fanoutCancel       context.CancelFunc
	fanoutLifecycleMu  sync.Mutex
	fanoutStopping     bool
	fanoutWG           sync.WaitGroup
	fanoutSlots        chan struct{}
	droppedFanout      atomic.Int64
	allowedOriginsMu   sync.RWMutex
	allowedOrigins     []string
	originAuthProvider func() AuthSettings
}

func (h *Hub) SetOriginAuthProvider(provider func() AuthSettings) {
	if h == nil {
		return
	}
	h.allowedOriginsMu.Lock()
	defer h.allowedOriginsMu.Unlock()
	h.originAuthProvider = provider
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
			authProvider := h.originAuthProvider
			h.allowedOriginsMu.RUnlock()
			var auth *AuthSettings
			if authProvider != nil {
				settings := authProvider()
				auth = &settings
			}
			decision := evaluateOriginPolicyWithAuth(allowedOrigins, r, auth)
			// As with HTTP CORS, a wildcard must not authorize ambient browser
			// credentials. Otherwise any site could open a cross-origin WebSocket
			// carrying the user's session cookie.
			return decision.allowed && !(decision.wildcard && strings.TrimSpace(r.Header.Get("Cookie")) != "")
		},
	}
}

type HubStats struct {
	ConnectedClients       int   `json:"connected_clients"`
	DroppedBroadcasts      int64 `json:"dropped_broadcasts,omitempty"`
	DroppedClientMessages  int64 `json:"dropped_client_messages,omitempty"`
	RejectedClients        int64 `json:"rejected_clients,omitempty"`
	DroppedFanoutPublishes int64 `json:"dropped_fanout_publishes,omitempty"`
}

type Client struct {
	hub        *Hub
	conn       *websocket.Conn
	send       chan []byte
	testID     string // empty = all tests, set = specific test
	projectID  string // empty = all projects, set = specific project
	authorized func() bool
	slotHeld   bool
	slotOnce   sync.Once
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
	return newHubWithLimits(maxHubClients, maxHubFanoutPublishes)
}

func newHubWithLimits(maxClients int, maxFanoutPublishes int) *Hub {
	if maxClients <= 0 {
		maxClients = maxHubClients
	}
	if maxFanoutPublishes <= 0 {
		maxFanoutPublishes = maxHubFanoutPublishes
	}
	fanoutCtx, fanoutCancel := context.WithCancel(context.Background())
	return &Hub{
		clients:      make(map[*Client]bool),
		broadcast:    make(chan hubBroadcast, 256),
		register:     make(chan *Client),
		unregister:   make(chan *Client),
		done:         make(chan struct{}),
		clientSlots:  make(chan struct{}, maxClients),
		fanoutCtx:    fanoutCtx,
		fanoutCancel: fanoutCancel,
		fanoutSlots:  make(chan struct{}, maxFanoutPublishes),
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
				c.releaseSlot()
			}
			h.clientsMu.Unlock()
			return

		case client := <-h.register:
			h.clientsMu.Lock()
			h.clients[client] = true
			clientCount := len(h.clients)
			h.clientsMu.Unlock()
			slog.Info("websocket client connected", "test_filter", client.testID, "project_filter", client.projectID, "clients", clientCount)

		case client := <-h.unregister:
			h.clientsMu.Lock()
			if _, ok := h.clients[client]; ok {
				close(client.send)
				delete(h.clients, client)
				client.releaseSlot()
			}
			clientCount := len(h.clients)
			h.clientsMu.Unlock()
			slog.Info("websocket client disconnected", "clients", clientCount)

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
	h.stopOnce.Do(func() {
		h.fanoutLifecycleMu.Lock()
		h.fanoutStopping = true
		if h.fanoutCancel != nil {
			h.fanoutCancel()
		}
		h.fanoutLifecycleMu.Unlock()
		close(h.done)
		h.fanoutWG.Wait()
	})
}

func (h *Hub) Broadcast(msg []byte) {
	broadcast := newHubBroadcast(msg)
	h.broadcastLocal(broadcast)
	h.publishFanout(broadcast)
}

func (h *Hub) broadcastLocal(msg hubBroadcast) {
	select {
	case <-h.done:
		h.droppedBroadcasts.Add(1)
		return
	default:
	}
	select {
	case h.broadcast <- msg:
	case <-h.done:
		h.droppedBroadcasts.Add(1)
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
	h.fanoutLifecycleMu.Lock()
	if h.fanoutStopping {
		h.fanoutLifecycleMu.Unlock()
		h.droppedFanout.Add(1)
		return
	}
	select {
	case h.fanoutSlots <- struct{}{}:
	default:
		h.fanoutLifecycleMu.Unlock()
		h.droppedFanout.Add(1)
		return
	}
	h.fanoutWG.Add(1)
	parentCtx := h.fanoutCtx
	h.fanoutLifecycleMu.Unlock()
	go func() {
		defer h.fanoutWG.Done()
		defer func() { <-h.fanoutSlots }()
		ctx, cancel := context.WithTimeout(parentCtx, defaultCacheWriteTimeout)
		defer cancel()
		if err := bus.Publish(ctx, channel, payload); err != nil {
			if !errors.Is(err, context.Canceled) {
				slog.Warn("failed to publish websocket fanout envelope", "err", err)
			}
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
		ConnectedClients:       h.ClientCount(),
		DroppedBroadcasts:      h.droppedBroadcasts.Load(),
		DroppedClientMessages:  h.droppedClientMsgs.Load(),
		RejectedClients:        h.rejectedClients.Load(),
		DroppedFanoutPublishes: h.droppedFanout.Load(),
	}
}

func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request, testID string, projectID string, authorized ...func() bool) {
	select {
	case <-h.done:
		http.Error(w, "websocket hub is shutting down", http.StatusServiceUnavailable)
		return
	default:
	}
	select {
	case h.clientSlots <- struct{}{}:
	default:
		h.rejectedClients.Add(1)
		http.Error(w, "websocket client limit reached", http.StatusServiceUnavailable)
		return
	}
	up := h.upgrader()
	conn, err := up.Upgrade(w, r, nil)
	if err != nil {
		<-h.clientSlots
		slog.Error("websocket upgrade failed", "err", err)
		return
	}

	client := &Client{
		hub:       h,
		conn:      conn,
		send:      make(chan []byte, websocketClientSendBuffer),
		testID:    testID,
		projectID: projectID,
		slotHeld:  true,
	}
	if len(authorized) > 0 {
		client.authorized = authorized[0]
	}

	select {
	case h.register <- client:
	case <-h.done:
		client.releaseSlot()
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"), time.Now().Add(time.Second))
		_ = conn.Close()
		return
	case <-r.Context().Done():
		client.releaseSlot()
		_ = conn.Close()
		return
	}

	go client.writePump()
	go client.readPump()
}

func (c *Client) releaseSlot() {
	if c == nil || c.hub == nil || !c.slotHeld {
		return
	}
	c.slotOnce.Do(func() {
		<-c.hub.clientSlots
	})
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
