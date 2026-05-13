package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

type Hub struct {
	clients    map[*Client]bool
	clientsMu  sync.RWMutex
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	done       chan struct{}
	droppedBroadcasts atomic.Int64
	droppedClientMsgs atomic.Int64
}

type HubStats struct {
	ConnectedClients      int   `json:"connected_clients"`
	DroppedBroadcasts     int64 `json:"dropped_broadcasts,omitempty"`
	DroppedClientMessages int64 `json:"dropped_client_messages,omitempty"`
}

type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	testID string // empty = all tests, set = specific test
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		done:       make(chan struct{}),
	}
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
			slog.Info("websocket client connected", "test_filter", client.testID, "clients", len(h.clients))

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
				if client.testID != "" {
					var envelope struct {
						TestID string `json:"test_id"`
					}
					if json.Unmarshal(msg, &envelope) == nil && envelope.TestID != client.testID {
						continue
					}
				}
				select {
				case client.send <- msg:
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
	close(h.done)
}

func (h *Hub) Broadcast(msg []byte) {
	select {
	case h.broadcast <- msg:
	default:
		h.droppedBroadcasts.Add(1)
	}
}

func (h *Hub) BroadcastJSON(v any) {
	data, err := json.Marshal(v)
	if err != nil {
		slog.Error("failed to marshal broadcast", "err", err)
		return
	}
	h.Broadcast(data)
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

func (h *Hub) serveWS(w http.ResponseWriter, r *http.Request, testID string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "err", err)
		return
	}

	client := &Client{
		hub:    h,
		conn:   conn,
		send:   make(chan []byte, 64),
		testID: testID,
	}

	h.register <- client

	go client.writePump()
	go client.readPump()
}

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
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
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
