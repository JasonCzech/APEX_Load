package kafka

import "testing"

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
