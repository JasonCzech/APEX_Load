package browser

import (
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestConsoleEntriesAreByteAndCountBounded(t *testing.T) {
	b := &Browser{}
	for i := 0; i < browserConsoleEntryRingLimit+10; i++ {
		b.appendConsoleEntry(protocol.ConsoleEntry{
			Level:  strings.Repeat("l", browserConsoleLevelLimitBytes+100),
			Text:   strings.Repeat("x", browserConsoleTextLimitBytes+100),
			Source: strings.Repeat("s", browserConsoleSourceLimitBytes+100),
		})
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
	if len(b.consoleEntries[0].Source) != browserConsoleSourceLimitBytes || len(b.consoleEntries[0].Level) != browserConsoleLevelLimitBytes {
		t.Fatalf("console metadata = %d/%d bytes, want %d/%d", len(b.consoleEntries[0].Source), len(b.consoleEntries[0].Level), browserConsoleSourceLimitBytes, browserConsoleLevelLimitBytes)
	}
	if got := b.consoleEntriesSince(0); len(got) != browserConsoleEntryRingLimit {
		t.Fatalf("entries since evicted offset = %d, want retained ring", len(got))
	}
}

func TestNetworkTaskQueueDropsInsteadOfSpawningUnboundedWorkers(t *testing.T) {
	b := &Browser{
		networkTasks:      make(chan browserNetworkTask, 1),
		networkWorkerStop: make(chan struct{}),
		harLimitExceeded:  make(chan struct{}),
		harLimitOnce:      sync.Once{},
	}
	b.enqueueNetworkRequest(nil, true)
	b.enqueueNetworkRequest(nil, true)
	if b.networkTasksDropped != 1 {
		t.Fatalf("network tasks dropped = %d, want 1", b.networkTasksDropped)
	}
	select {
	case <-b.harLimitExceeded:
		t.Fatal("diagnostics queue pressure incorrectly signaled a HAR limit breach")
	default:
	}
}
