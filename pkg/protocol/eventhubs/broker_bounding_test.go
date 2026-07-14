package eventhubs

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMemoryBrokerLogIsBounded verifies the in-memory mock broker caps retained
// events per hub while keeping sequence numbers/offsets absolute across eviction.
func TestMemoryBrokerLogIsBounded(t *testing.T) {
	b := newMemoryBroker()
	total := maxBrokerLogPerHub + 100
	for i := 0; i < total; i++ {
		b.send("h", event{Body: []byte("x")})
	}
	if got := len(b.logs["h"]); got > maxBrokerLogPerHub {
		t.Fatalf("log not bounded: %d > %d", got, maxBrokerLogPerHub)
	}
	ev, lag, ok := b.receive("h", "g")
	if !ok {
		t.Fatal("expected a retained event")
	}
	wantBase := int64(total - maxBrokerLogPerHub)
	if ev.SequenceNum != wantBase {
		t.Fatalf("first retained sequence = %d, want %d (base after eviction)", ev.SequenceNum, wantBase)
	}
	if lag != int64(maxBrokerLogPerHub)-1 {
		t.Fatalf("lag = %d, want %d", lag, maxBrokerLogPerHub-1)
	}
}

func TestMemoryBrokerBoundsRetainedBytes(t *testing.T) {
	b := newMemoryBroker()
	payload := make([]byte, 1<<20)
	for i := 0; i < 100; i++ {
		b.send("large", event{Body: payload})
	}
	if b.bytes["large"] > maxBrokerBytesPerHub {
		t.Fatalf("retained bytes = %d, cap = %d", b.bytes["large"], maxBrokerBytesPerHub)
	}
}

func TestMockEventHubNamespacesAreIsolatedAndReleased(t *testing.T) {
	b := newMemoryBroker()
	b.acquire("one")
	b.acquire("two")
	one := mockClient{broker: b, namespace: "one", release: &sync.Once{}}
	two := mockClient{broker: b, namespace: "two", release: &sync.Once{}}
	if _, err := one.Send(context.Background(), "orders", event{Body: []byte("one")}); err != nil {
		t.Fatalf("send: %v", err)
	}
	if _, _, ok := b.receive(mockHubKey("two", "orders"), "g"); ok {
		t.Fatal("second namespace observed first namespace event")
	}
	_ = one.Teardown(context.Background())
	_ = two.Teardown(context.Background())
	for key := range b.logs {
		if strings.HasPrefix(key, "one\x00") || strings.HasPrefix(key, "two\x00") {
			t.Fatalf("namespace survived teardown: %q", key)
		}
	}
}

func TestMockEventHubCapacityFailsWithoutEvictingLiveNamespace(t *testing.T) {
	b := newMemoryBroker()
	liveHub := mockHubKey("live-run", "orders")
	b.acquire("live-run")
	defer b.release("live-run")

	b.mu.Lock()
	b.logs[liveHub] = []event{{Body: []byte("live")}}
	for i := 1; i < maxBrokerHubKeys; i++ {
		b.logs[fmt.Sprintf("occupied-%d", i)] = nil
	}
	b.mu.Unlock()

	client := mockClient{broker: b, namespace: "new-run", release: &sync.Once{}}
	if _, err := client.Send(context.Background(), "orders", event{Body: []byte("new")}); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("Send error = %v, want explicit capacity error", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.logs[mockHubKey("new-run", "orders")]; exists {
		t.Fatal("capacity failure created the new namespace hub")
	}
	if got := b.logs[liveHub]; len(got) != 1 || string(got[0].Body) != "live" {
		t.Fatalf("live namespace hub was changed or evicted: %#v", got)
	}
	if len(b.logs) != maxBrokerHubKeys {
		t.Fatalf("hub keys = %d, want unchanged cap %d", len(b.logs), maxBrokerHubKeys)
	}
}

func TestMockEventHubCursorCapacityFailsWithoutEviction(t *testing.T) {
	b := newMemoryBroker()
	hub := mockHubKey("new-run", "orders")
	sentinel := cursorKey(mockHubKey("live-run", "orders"), "sentinel")
	b.mu.Lock()
	b.logs[hub] = []event{{Body: []byte("new")}}
	b.cursors[sentinel] = 7
	for i := 1; i < maxBrokerCursorKeys; i++ {
		b.cursors[fmt.Sprintf("occupied-cursor-%d", i)] = int64(i)
	}
	b.mu.Unlock()

	client := mockClient{broker: b, namespace: "new-run", release: &sync.Once{}}
	if _, _, err := client.Receive(context.Background(), "orders", "new-group", time.Second); err == nil || !strings.Contains(err.Error(), "cursor capacity") {
		t.Fatalf("Receive error = %v, want explicit cursor-capacity error", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.cursors[cursorKey(hub, "new-group")]; exists {
		t.Fatal("capacity failure created a new cursor")
	}
	if got := b.cursors[sentinel]; got != 7 {
		t.Fatalf("live namespace cursor = %d, want unchanged value 7", got)
	}
	if len(b.cursors) != maxBrokerCursorKeys {
		t.Fatalf("cursor keys = %d, want unchanged cap %d", len(b.cursors), maxBrokerCursorKeys)
	}
}
