package browser

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestConsoleEntriesAreByteAndCountBounded(t *testing.T) {
	b := &Browser{}
	for i := 0; i < browserConsoleEntryRingLimit+10; i++ {
		b.appendConsoleEntry(protocol.ConsoleEntry{Text: strings.Repeat("x", browserConsoleTextLimitBytes+100)})
	}
	if len(b.consoleEntries) != browserConsoleEntryRingLimit {
		t.Fatalf("console entries = %d, want %d", len(b.consoleEntries), browserConsoleEntryRingLimit)
	}
	if b.consoleDropped != 10 {
		t.Fatalf("console dropped = %d, want 10", b.consoleDropped)
	}
	if len(b.consoleEntries[0].Text) != browserConsoleTextLimitBytes {
		t.Fatalf("console text = %d bytes, want %d", len(b.consoleEntries[0].Text), browserConsoleTextLimitBytes)
	}
	if got := b.consoleEntriesSince(0); len(got) != browserConsoleEntryRingLimit {
		t.Fatalf("entries since evicted offset = %d, want retained ring", len(got))
	}
}

func TestNetworkTaskQueueDropsInsteadOfSpawningUnboundedWorkers(t *testing.T) {
	b := &Browser{
		networkTasks:      make(chan browserNetworkTask, 1),
		networkWorkerStop: make(chan struct{}),
	}
	b.enqueueNetworkRequest(nil, true)
	b.enqueueNetworkRequest(nil, true)
	if b.networkTasksDropped != 1 {
		t.Fatalf("network tasks dropped = %d, want 1", b.networkTasksDropped)
	}
}
