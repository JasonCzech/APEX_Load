package browser

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// appendNetworkEntryForTest mirrors the accounting recordNetworkRequest performs
// (append + body-byte tally + cap), without needing a live playwright.Request.
func (b *Browser) appendNetworkEntryForTest(body string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.networkEntries = append(b.networkEntries, protocol.NetworkEntry{RequestBody: body})
	b.networkBodyBytes += len(body)
	b.trimNetworkEntriesLocked()
}

// TestNetworkEntriesRingAndByteBudget guards B6: the per-session network buffer
// must stay bounded by both entry count and total retained body bytes, evicting
// the oldest entries rather than growing for the whole session.
func TestNetworkEntriesRingAndByteBudget(t *testing.T) {
	b := &Browser{}
	// Far more entries than the ring limit, each with a sizeable body.
	bigBody := strings.Repeat("x", 256*1024)
	total := browserNetworkEntryRingLimit + 5000
	for i := 0; i < total; i++ {
		b.appendNetworkEntryForTest(bigBody)
	}

	if len(b.networkEntries) > browserNetworkEntryRingLimit {
		t.Fatalf("entry count = %d, want <= %d", len(b.networkEntries), browserNetworkEntryRingLimit)
	}
	if b.networkBodyBytes > browserNetworkBodyBudgetBytes {
		t.Fatalf("retained body bytes = %d, want <= %d", b.networkBodyBytes, browserNetworkBodyBudgetBytes)
	}
	if b.networkEntriesDropped != total-len(b.networkEntries) {
		t.Fatalf("dropped count = %d, want %d", b.networkEntriesDropped, total-len(b.networkEntries))
	}
	// networkBodyBytes must equal the sum of retained bodies (no drift).
	wantBytes := len(b.networkEntries) * len(bigBody)
	if b.networkBodyBytes != wantBytes {
		t.Fatalf("body byte tally = %d, want %d", b.networkBodyBytes, wantBytes)
	}
}

// TestNetworkEntriesSinceSurvivesEviction guards that the absolute offset used by
// finalizeActionResult still slices correctly after older entries are evicted.
func TestNetworkEntriesSinceSurvivesEviction(t *testing.T) {
	b := &Browser{}
	// Seed a few small entries, capture an offset, then flood past the cap.
	for i := 0; i < 10; i++ {
		b.appendNetworkEntryForTest("seed")
	}
	_, networkStart := b.eventOffsets()
	const after = 7
	for i := 0; i < after; i++ {
		b.appendNetworkEntryForTest("post")
	}
	got := b.networkEntriesSince(networkStart)
	if len(got) != after {
		t.Fatalf("entries since offset = %d, want %d", len(got), after)
	}
	for _, e := range got {
		if e.RequestBody != "post" {
			t.Fatalf("entries-since returned a pre-offset entry: %q", e.RequestBody)
		}
	}

	// Now evict everything captured up to the offset and confirm we clamp to the
	// retained set instead of panicking or returning stale indices.
	bigBody := strings.Repeat("y", 256*1024)
	for i := 0; i < browserNetworkEntryRingLimit+10; i++ {
		b.appendNetworkEntryForTest(bigBody)
	}
	got = b.networkEntriesSince(networkStart)
	if len(got) > len(b.networkEntries) {
		t.Fatalf("entries-since after eviction = %d, exceeds retained %d", len(got), len(b.networkEntries))
	}
}
