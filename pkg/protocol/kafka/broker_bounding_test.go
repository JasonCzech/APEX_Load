package kafka

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestMemoryBrokerLogIsBounded verifies the in-memory mock broker caps retained
// records per topic (so a long-lived process does not grow unbounded) while
// keeping offsets absolute across eviction.
func TestMemoryBrokerLogIsBounded(t *testing.T) {
	b := newMemoryBroker()
	total := maxBrokerLogPerTopic + 100
	for i := 0; i < total; i++ {
		b.produce("t", kafkaRecord{Value: []byte("x")})
	}
	if got := len(b.logs["t"]); got > maxBrokerLogPerTopic {
		t.Fatalf("log not bounded: %d > %d", got, maxBrokerLogPerTopic)
	}
	// A fresh consumer reads from the earliest RETAINED offset, with correct
	// absolute offsets and lag.
	rec, lag, ok := b.consume("t", "g")
	if !ok {
		t.Fatal("expected a retained record")
	}
	wantBase := int64(total - maxBrokerLogPerTopic)
	if rec.Offset != wantBase {
		t.Fatalf("first retained offset = %d, want %d (base after eviction)", rec.Offset, wantBase)
	}
	if lag != int64(maxBrokerLogPerTopic)-1 {
		t.Fatalf("lag = %d, want %d", lag, maxBrokerLogPerTopic-1)
	}
}

func TestMemoryBrokerBoundsRetainedBytes(t *testing.T) {
	b := newMemoryBroker()
	payload := make([]byte, 1<<20)
	for i := 0; i < 100; i++ {
		b.produce("large", kafkaRecord{Value: payload})
	}
	if b.bytes["large"] > maxBrokerBytesPerTopic {
		t.Fatalf("retained bytes = %d, cap = %d", b.bytes["large"], maxBrokerBytesPerTopic)
	}
}

func TestMockKafkaNamespacesAreIsolatedAndReleased(t *testing.T) {
	b := newMemoryBroker()
	b.acquire("one")
	b.acquire("two")
	one := mockClient{broker: b, namespace: "one", release: &sync.Once{}}
	two := mockClient{broker: b, namespace: "two", release: &sync.Once{}}
	if _, err := one.Produce(context.Background(), "orders", kafkaRecord{Value: []byte("one")}); err != nil {
		t.Fatalf("produce: %v", err)
	}
	if _, _, ok := b.consume(mockTopicKey("two", "orders"), "g"); ok {
		t.Fatal("second namespace observed first namespace record")
	}
	_ = one.Teardown(context.Background())
	_ = two.Teardown(context.Background())
	for key := range b.logs {
		if strings.HasPrefix(key, "one\x00") || strings.HasPrefix(key, "two\x00") {
			t.Fatalf("namespace survived teardown: %q", key)
		}
	}
}

func TestMockKafkaTopicCapacityFailsWithoutEvictingLiveNamespace(t *testing.T) {
	b := newMemoryBroker()
	liveTopic := mockTopicKey("live-run", "orders")
	b.acquire("live-run")
	defer b.release("live-run")

	b.mu.Lock()
	b.logs[liveTopic] = []kafkaRecord{{Value: []byte("live")}}
	for i := 1; i < maxBrokerTopicKeys; i++ {
		b.logs[fmt.Sprintf("occupied-%d", i)] = nil
	}
	b.mu.Unlock()

	client := mockClient{broker: b, namespace: "new-run", release: &sync.Once{}}
	if _, err := client.Produce(context.Background(), "orders", kafkaRecord{Value: []byte("new")}); err == nil || !strings.Contains(err.Error(), "capacity") {
		t.Fatalf("Produce error = %v, want explicit capacity error", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.logs[mockTopicKey("new-run", "orders")]; exists {
		t.Fatal("capacity failure created the new namespace topic")
	}
	if got := b.logs[liveTopic]; len(got) != 1 || string(got[0].Value) != "live" {
		t.Fatalf("live namespace topic was changed or evicted: %#v", got)
	}
	if len(b.logs) != maxBrokerTopicKeys {
		t.Fatalf("topic keys = %d, want unchanged cap %d", len(b.logs), maxBrokerTopicKeys)
	}
}

func TestMockKafkaCursorCapacityFailsWithoutEviction(t *testing.T) {
	b := newMemoryBroker()
	topic := mockTopicKey("new-run", "orders")
	sentinel := cursorKey(mockTopicKey("live-run", "orders"), "sentinel")
	b.mu.Lock()
	b.logs[topic] = []kafkaRecord{{Value: []byte("new")}}
	b.cursors[sentinel] = 7
	for i := 1; i < maxBrokerCursorKeys; i++ {
		b.cursors[fmt.Sprintf("occupied-cursor-%d", i)] = int64(i)
	}
	b.mu.Unlock()

	client := mockClient{broker: b, namespace: "new-run", release: &sync.Once{}}
	if _, _, err := client.Consume(context.Background(), "orders", "new-group", time.Second); err == nil || !strings.Contains(err.Error(), "cursor capacity") {
		t.Fatalf("Consume error = %v, want explicit cursor-capacity error", err)
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.cursors[cursorKey(topic, "new-group")]; exists {
		t.Fatal("capacity failure created a new cursor")
	}
	if got := b.cursors[sentinel]; got != 7 {
		t.Fatalf("live namespace cursor = %d, want unchanged value 7", got)
	}
	if len(b.cursors) != maxBrokerCursorKeys {
		t.Fatalf("cursor keys = %d, want unchanged cap %d", len(b.cursors), maxBrokerCursorKeys)
	}
}
