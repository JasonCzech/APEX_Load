package api

import "testing"

func TestHubStatsTracksDroppedBroadcastsAndClientMessages(t *testing.T) {
	hub := NewHub()
	hub.broadcast = make(chan []byte, 1)
	hub.Broadcast([]byte("one"))
	hub.Broadcast([]byte("two"))

	client := &Client{send: make(chan []byte, 1)}
	hub.clients[client] = true
	client.send <- []byte("occupied")

	hub.clientsMu.RLock()
	for client := range hub.clients {
		select {
		case client.send <- []byte(`{"test_id":"test-1"}`):
		default:
			hub.droppedClientMsgs.Add(1)
		}
	}
	hub.clientsMu.RUnlock()

	stats := hub.Stats()
	if stats.DroppedBroadcasts != 1 {
		t.Fatalf("DroppedBroadcasts = %d, want 1", stats.DroppedBroadcasts)
	}
	if stats.DroppedClientMessages != 1 {
		t.Fatalf("DroppedClientMessages = %d, want 1", stats.DroppedClientMessages)
	}
	if stats.ConnectedClients != 1 {
		t.Fatalf("ConnectedClients = %d, want 1", stats.ConnectedClients)
	}
}