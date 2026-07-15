package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type blockingHubFanoutBus struct {
	active  atomic.Int32
	maximum atomic.Int32
	started chan struct{}
}

func (b *blockingHubFanoutBus) Publish(ctx context.Context, _ string, _ []byte) error {
	active := b.active.Add(1)
	for {
		maximum := b.maximum.Load()
		if active <= maximum || b.maximum.CompareAndSwap(maximum, active) {
			break
		}
	}
	select {
	case b.started <- struct{}{}:
	default:
	}
	defer b.active.Add(-1)
	<-ctx.Done()
	return ctx.Err()
}

func (*blockingHubFanoutBus) Subscribe(context.Context, string) (FanoutSubscription, error) {
	return nil, errors.New("not implemented")
}

func TestHubBoundsFanoutConcurrencyAndCancelsOnStop(t *testing.T) {
	const fanoutLimit = 2
	hub := newHubWithLimits(1, fanoutLimit)
	bus := &blockingHubFanoutBus{started: make(chan struct{}, 16)}
	hub.SetFanout(bus, "controller-a", "events")

	for index := 0; index < 10; index++ {
		hub.BroadcastJSON(map[string]any{"type": "metrics_update", "sequence": index})
	}
	for index := 0; index < fanoutLimit; index++ {
		select {
		case <-bus.started:
		case <-time.After(time.Second):
			t.Fatalf("fanout publish %d did not start", index)
		}
	}
	if maximum := bus.maximum.Load(); maximum > fanoutLimit {
		t.Fatalf("maximum concurrent fanout publishes = %d, want <= %d", maximum, fanoutLimit)
	}
	if dropped := hub.Stats().DroppedFanoutPublishes; dropped != 8 {
		t.Fatalf("dropped fanout publishes = %d, want 8", dropped)
	}

	stopped := make(chan struct{})
	go func() {
		hub.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Hub.Stop did not cancel and join fanout publishers")
	}
	if active := bus.active.Load(); active != 0 {
		t.Fatalf("active fanout publishers after Stop = %d, want 0", active)
	}
}

func TestHubRejectsWebsocketConnectionsBeyondLimit(t *testing.T) {
	hub := newHubWithLimits(1, 1)
	go hub.Run()
	defer hub.Stop()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.serveWS(w, r, "", "")
	}))
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	first, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("first websocket dial: %v", err)
	}
	waitForHubClientCount(t, hub, 1)
	_, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("second websocket connection exceeded the configured limit")
	}
	if response == nil || response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("second websocket response = %#v, want 503", response)
	}
	if rejected := hub.Stats().RejectedClients; rejected != 1 {
		t.Fatalf("rejected clients = %d, want 1", rejected)
	}

	_ = first.Close()
	waitForHubClientCount(t, hub, 0)
	third, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("websocket slot was not released after disconnect: %v", err)
	}
	_ = third.Close()
}

func waitForHubClientCount(t *testing.T, hub *Hub, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if hub.ClientCount() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("hub client count = %d, want %d", hub.ClientCount(), want)
}

func TestHubBroadcastAllReachesScopedClientsButGuardsScopedPayloads(t *testing.T) {
	hub := NewHub()
	go hub.Run()
	defer hub.Stop()

	projectClient := &Client{hub: hub, send: make(chan []byte, 2), projectID: "project-1"}
	hub.register <- projectClient

	// A content-free broadcast_all ping must reach the project-scoped client despite its filter.
	hub.BroadcastJSON(map[string]any{"type": "access_review_changed", "broadcast_all": true})
	select {
	case msg := <-projectClient.send:
		var payload map[string]any
		if err := json.Unmarshal(msg, &payload); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if payload["type"] != "access_review_changed" {
			t.Fatalf("type = %#v, want access_review_changed", payload["type"])
		}
	case <-time.After(time.Second):
		t.Fatal("broadcast_all ping did not reach the project-scoped client")
	}

	// A misused broadcast_all on a message carrying a foreign project_id must NOT bypass the
	// filter (the scoped client must not receive cross-project data).
	hub.BroadcastJSON(map[string]any{"type": "test_started", "project_id": "project-2", "broadcast_all": true})
	select {
	case msg := <-projectClient.send:
		t.Fatalf("broadcast_all leaked a project-scoped message across tenants: %s", string(msg))
	case <-time.After(50 * time.Millisecond):
	}
}

func TestHubStatsTracksDroppedBroadcastsAndClientMessages(t *testing.T) {
	hub := NewHub()
	hub.broadcast = make(chan hubBroadcast, 1)
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

func TestHubBroadcastScopedJSONCarriesRoutingMetadata(t *testing.T) {
	hub := NewHub()
	hub.broadcast = make(chan hubBroadcast, 1)

	hub.BroadcastScopedJSON(" test-1 ", " project-1 ", map[string]any{
		"type":       "metrics_update",
		"test_id":    "test-1",
		"project_id": "project-1",
	})

	select {
	case msg := <-hub.broadcast:
		if msg.testID != "test-1" || msg.projectID != "project-1" || msg.broadcastAll {
			t.Fatalf("routing metadata = %#v, want scoped test/project without broadcast_all", msg)
		}
		var payload map[string]any
		if err := json.Unmarshal(msg.payload, &payload); err != nil {
			t.Fatalf("json.Unmarshal returned error: %v", err)
		}
		if payload["type"] != "metrics_update" {
			t.Fatalf("type = %#v, want metrics_update", payload["type"])
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for scoped broadcast")
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
