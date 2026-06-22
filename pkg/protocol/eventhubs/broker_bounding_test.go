package eventhubs

import "testing"

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
