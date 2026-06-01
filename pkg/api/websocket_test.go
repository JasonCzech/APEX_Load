package api

import (
	"encoding/json"
	"testing"
	"time"
)

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

func TestHubFiltersBroadcastsByProjectID(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	projectClient := &Client{hub: hub, send: make(chan []byte, 1), projectID: "project-1"}
	globalClient := &Client{hub: hub, send: make(chan []byte, 1)}
	hub.register <- projectClient
	hub.register <- globalClient

	hub.BroadcastJSON(map[string]any{"type": "test_started", "test_id": "test-1", "project_id": "project-1"})

	select {
	case msg := <-projectClient.send:
		var payload map[string]any
		if err := json.Unmarshal(msg, &payload); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if payload["project_id"] != "project-1" {
			t.Fatalf("project_id = %#v, want %q", payload["project_id"], "project-1")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for project-scoped message")
	}

	select {
	case <-globalClient.send:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for global client message")
	}

	hub.BroadcastJSON(map[string]any{"type": "test_started", "test_id": "test-2", "project_id": "project-2"})

	select {
	case msg := <-projectClient.send:
		t.Fatalf("unexpected cross-project message delivered: %s", string(msg))
	case <-time.After(50 * time.Millisecond):
	}
}
