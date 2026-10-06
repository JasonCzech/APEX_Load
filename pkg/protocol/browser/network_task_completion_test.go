package browser

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func networkCompletionTestBrowser() *Browser {
	return &Browser{
		documentMetricsEnabled: true,
		evidence:               browserEvidenceState{generation: strings.Repeat("a", 32)},
		networkTasks:           make(chan browserNetworkTask, browserNetworkTaskQueueSize),
		networkWorkerStop:      make(chan struct{}),
		networkWorkers:         newBrowserNetworkWorkerState(),
	}
}

func admitNetworkCompletionTestTask(t *testing.T, b *Browser) browserNetworkTask {
	t.Helper()
	b.mu.Lock()
	admitted := b.admitNetworkTaskLocked(browserNetworkTask{generation: b.evidence.generation})
	b.mu.Unlock()
	if !admitted {
		t.Fatal("task was not admitted")
	}
	return <-b.networkTasks
}

func completeNetworkCompletionTestTask(b *Browser, task browserNetworkTask, entry protocol.NetworkEntry) {
	b.mu.Lock()
	if b.networkTaskCurrentLocked(task) {
		b.appendNetworkEntryLocked(entry)
	}
	b.mu.Unlock()
	b.finishNetworkTaskReceipt(task.receipt, true)
}

type networkCompletionTestResult struct {
	entries []protocol.NetworkEntry
	reason  string
}

func runNetworkCompletionTestWait(t *testing.T, b *Browser, snapshot browserNetworkWorkSnapshot, offset int) (<-chan networkCompletionTestResult, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	results := make(chan networkCompletionTestResult, 1)
	joined := make(chan struct{})
	started := make(chan struct{})
	go func() {
		defer close(joined)
		close(started)
		entries, reason := b.settleNetworkWorkSnapshot(ctx, snapshot, offset)
		results <- networkCompletionTestResult{entries: entries, reason: reason}
	}()
	<-started
	t.Cleanup(func() {
		cancel()
		select {
		case <-joined:
		case <-time.After(3 * time.Second):
			t.Error("owned metadata waiter did not join")
		}
	})
	return results, cancel
}

func requireNetworkCompletionTestBlocked(t *testing.T, results <-chan networkCompletionTestResult) {
	t.Helper()
	select {
	case result := <-results:
		t.Fatalf("metadata finalized while an accepted receipt remained open: %#v", result)
	case <-time.After(20 * time.Millisecond):
	}
}

func awaitNetworkCompletionTestResult(t *testing.T, results <-chan networkCompletionTestResult) networkCompletionTestResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("metadata waiter did not finish")
		return networkCompletionTestResult{}
	}
}

func TestNetworkCompletionWaitsForAcceptedMetadataAndClonesSnapshot(t *testing.T) {
	b := networkCompletionTestBrowser()
	task := admitNetworkCompletionTestTask(t, b)
	_, offset, owner := b.actionEvidenceOffsets()
	snapshot := b.snapshotAcceptedNetworkWork(owner)
	results, _ := runNetworkCompletionTestWait(t, b, snapshot, offset)
	requireNetworkCompletionTestBlocked(t, results)
	serviceWorker := false
	requestID := owner.generation + "-r1"
	completeNetworkCompletionTestTask(b, task, protocol.NetworkEntry{
		URL: "/accepted", RequestHeaders: map[string]string{"x-request": "original"},
		ResponseHeaders: map[string]string{"x-response": "original"},
		BrowserEvidence: &protocol.BrowserResourceEvidence{
			Version: protocol.BrowserEvidenceVersion, SourceGeneration: owner.generation, RequestID: requestID,
			ObservedAt: time.Unix(1, 0).UTC(), TimingSource: "playwright", State: "finished", ServiceWorker: &serviceWorker,
		},
	})
	result := awaitNetworkCompletionTestResult(t, results)
	if result.reason != "" || len(result.entries) != 1 || result.entries[0].URL != "/accepted" {
		t.Fatalf("completed metadata = %#v", result)
	}
	if result.entries[0].BrowserEvidence == nil || result.entries[0].BrowserEvidence.ServiceWorker == nil {
		t.Fatal("validated resource evidence was discarded from the detached snapshot")
	}
	result.entries[0].RequestHeaders["x-request"] = "changed"
	result.entries[0].ResponseHeaders["x-response"] = "changed"
	result.entries[0].BrowserEvidence.RequestID = "changed"
	*result.entries[0].BrowserEvidence.ServiceWorker = true
	b.mu.Lock()
	defer b.mu.Unlock()
	stored := b.networkEntries[0]
	if stored.RequestHeaders["x-request"] != "original" || stored.ResponseHeaders["x-response"] != "original" || stored.BrowserEvidence == nil || stored.BrowserEvidence.ServiceWorker == nil || stored.BrowserEvidence.RequestID != requestID || *stored.BrowserEvidence.ServiceWorker {
		t.Fatal("finalized metadata aliased mutable browser storage")
	}
	if len(b.networkWorkers.pending) != 0 || b.evidence.incomplete {
		t.Fatal("completed metadata retained a pending obligation or became incomplete")
	}
}

func TestNetworkCompletionOutOfOrderCannotDischargeEarlierReceipt(t *testing.T) {
	b := networkCompletionTestBrowser()
	first := admitNetworkCompletionTestTask(t, b)
	second := admitNetworkCompletionTestTask(t, b)
	_, offset, owner := b.actionEvidenceOffsets()
	snapshot := b.snapshotAcceptedNetworkWork(owner)
	results, _ := runNetworkCompletionTestWait(t, b, snapshot, offset)
	completeNetworkCompletionTestTask(b, second, protocol.NetworkEntry{URL: "/second"})
	requireNetworkCompletionTestBlocked(t, results)
	completeNetworkCompletionTestTask(b, first, protocol.NetworkEntry{URL: "/first"})
	result := awaitNetworkCompletionTestResult(t, results)
	if result.reason != "" || len(result.entries) != 2 || result.entries[0].URL != "/second" || result.entries[1].URL != "/first" {
		t.Fatalf("out-of-order completion = %#v", result)
	}
}

func TestNetworkCompletionDoesNotWaitForTasksAcceptedAfterSnapshot(t *testing.T) {
	b := networkCompletionTestBrowser()
	first := admitNetworkCompletionTestTask(t, b)
	_, offset, owner := b.actionEvidenceOffsets()
	snapshot := b.snapshotAcceptedNetworkWork(owner)
	later := admitNetworkCompletionTestTask(t, b)
	defer b.finishNetworkTaskReceipt(later.receipt, true)
	completeNetworkCompletionTestTask(b, first, protocol.NetworkEntry{URL: "/first"})
	results, _ := runNetworkCompletionTestWait(t, b, snapshot, offset)
	result := awaitNetworkCompletionTestResult(t, results)
	if result.reason != "" || len(result.entries) != 1 {
		t.Fatalf("fixed accepted set = %#v", result)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.networkWorkers.pending) != 1 || later.receipt.finished {
		t.Fatal("future work was incorrectly reported completed")
	}
}

func TestNetworkCompletionParentCancellationRetainsTruthfulPartialEvidence(t *testing.T) {
	b := networkCompletionTestBrowser()
	completed := admitNetworkCompletionTestTask(t, b)
	pending := admitNetworkCompletionTestTask(t, b)
	defer b.finishNetworkTaskReceipt(pending.receipt, true)
	_, offset, owner := b.actionEvidenceOffsets()
	completeNetworkCompletionTestTask(b, completed, protocol.NetworkEntry{URL: "/completed"})
	results, cancel := runNetworkCompletionTestWait(t, b, b.snapshotAcceptedNetworkWork(owner), offset)
	requireNetworkCompletionTestBlocked(t, results)
	cancel()
	result := awaitNetworkCompletionTestResult(t, results)
	if result.reason != "cancelled" || len(result.entries) != 1 || result.entries[0].URL != "/completed" {
		t.Fatalf("cancelled evidence = %#v", result)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.evidence.incomplete || b.networkWorkers.incompleteGeneration != owner.generation || pending.receipt.finished {
		t.Fatal("cancelled acquisition lost its exact incomplete/pending state")
	}
}

func TestNetworkCompletionParentDeadlineShortensFiveSecondBudget(t *testing.T) {
	if browserNetworkMetadataSettleTimeout != 5*time.Second {
		t.Fatalf("metadata settling budget = %v, want 5s", browserNetworkMetadataSettleTimeout)
	}
	b := networkCompletionTestBrowser()
	pending := admitNetworkCompletionTestTask(t, b)
	defer b.finishNetworkTaskReceipt(pending.receipt, true)
	_, offset, owner := b.actionEvidenceOffsets()
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(20*time.Millisecond))
	defer cancel()
	entries, reason := b.settleNetworkWorkSnapshot(ctx, b.snapshotAcceptedNetworkWork(owner), offset)
	if reason != "metadata_timeout" || len(entries) != 0 || ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("parent deadline = %q/%d/%v", reason, len(entries), ctx.Err())
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.evidence.incomplete || b.networkWorkers.incompleteGeneration != owner.generation || pending.receipt.finished {
		t.Fatal("deadline incorrectly completed metadata or omitted incompleteness")
	}
}

func TestNetworkCompletionCancellationWinsEvenAfterReceiptIsReady(t *testing.T) {
	b := networkCompletionTestBrowser()
	task := admitNetworkCompletionTestTask(t, b)
	_, offset, owner := b.actionEvidenceOffsets()
	snapshot := b.snapshotAcceptedNetworkWork(owner)
	completeNetworkCompletionTestTask(b, task, protocol.NetworkEntry{URL: "/completed"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	entries, reason := b.settleNetworkWorkSnapshot(ctx, snapshot, offset)
	if reason != "cancelled" || len(entries) != 1 {
		t.Fatalf("ready cancellation = %q/%d", reason, len(entries))
	}
}

func TestNetworkCompletionRetirementWakesWithoutPhysicalWorkerJoin(t *testing.T) {
	b := networkCompletionTestBrowser()
	task := admitNetworkCompletionTestTask(t, b)
	defer b.finishNetworkTaskReceipt(task.receipt, true)
	_, offset, owner := b.actionEvidenceOffsets()
	results, _ := runNetworkCompletionTestWait(t, b, b.snapshotAcceptedNetworkWork(owner), offset)
	requireNetworkCompletionTestBlocked(t, results)
	b.mu.Lock()
	b.closing = true
	b.retireNetworkWorkersLocked()
	b.mu.Unlock()
	result := awaitNetworkCompletionTestResult(t, results)
	if result.reason != "lifecycle_closed" || result.entries != nil || task.receipt.finished {
		t.Fatalf("retired lifecycle = %#v, completed = %v", result, task.receipt.finished)
	}
}

func TestNetworkCompletionRechecksExactGenerationAndWorkerIdentity(t *testing.T) {
	for _, changeGeneration := range []bool{false, true} {
		name := "same_generation_new_worker"
		if changeGeneration {
			name = "new_generation"
		}
		t.Run(name, func(t *testing.T) {
			b := networkCompletionTestBrowser()
			oldTask := admitNetworkCompletionTestTask(t, b)
			_, offset, oldOwner := b.actionEvidenceOffsets()
			snapshot := b.snapshotAcceptedNetworkWork(oldOwner)
			b.mu.Lock()
			b.retireNetworkWorkersLocked()
			b.networkWorkers = newBrowserNetworkWorkerState()
			if changeGeneration {
				b.evidence.generation = strings.Repeat("b", 32)
			}
			b.evidence.incomplete = false
			b.networkEntries = []protocol.NetworkEntry{{URL: "/replacement"}}
			b.mu.Unlock()
			newTask := admitNetworkCompletionTestTask(t, b)
			defer b.finishNetworkTaskReceipt(newTask.receipt, true)
			// Rejected stale work must not reach the nil native request, append a
			// replacement entry, or steal the replacement receipt obligation.
			b.processNetworkTask(oldTask)
			b.finishNetworkTaskReceipt(oldTask.receipt, false)
			entries, reason := b.settleNetworkWorkSnapshot(context.Background(), snapshot, offset)
			if reason != "lifecycle_changed" || entries != nil {
				t.Fatalf("replacement lifecycle = %q/%#v", reason, entries)
			}
			b.mu.Lock()
			defer b.mu.Unlock()
			if b.evidence.incomplete || b.networkWorkers.incompleteGeneration != "" || len(b.networkWorkers.pending) != 1 || newTask.receipt.finished || b.networkEntries[0].URL != "/replacement" {
				t.Fatal("old acquisition mutated replacement lifecycle")
			}
		})
	}
}

func TestNetworkCompletionGenerationComesFromActionNotWorkerStartup(t *testing.T) {
	b := networkCompletionTestBrowser()
	b.evidence.generation = ""
	worker := b.networkWorkers
	b.evidence.generation = strings.Repeat("b", 32)
	old := admitNetworkCompletionTestTask(t, b)
	defer b.finishNetworkTaskReceipt(old.receipt, false)
	b.evidence.generation = strings.Repeat("c", 32)
	current := admitNetworkCompletionTestTask(t, b)
	_, offset, owner := b.actionEvidenceOffsets()
	snapshot := b.snapshotAcceptedNetworkWork(owner)
	if owner.workers != worker || owner.generation != b.evidence.generation || len(snapshot.receipts) != 1 || snapshot.receipts[0] != current.receipt {
		t.Fatalf("action-time owner = %#v, snapshot = %#v", owner, snapshot)
	}
	// A stale failed task from the same worker state cannot poison this epoch.
	b.finishNetworkTaskReceipt(old.receipt, false)
	completeNetworkCompletionTestTask(b, current, protocol.NetworkEntry{URL: "/current"})
	entries, reason := b.settleNetworkWorkSnapshot(context.Background(), snapshot, offset)
	if reason != "" || len(entries) != 1 || b.evidence.incomplete || worker.incompleteGeneration != "" {
		t.Fatalf("action-time generation = %q/%#v, incomplete = %v/%q", reason, entries, b.evidence.incomplete, worker.incompleteGeneration)
	}
}

func TestNetworkCompletionChangedGenerationRejectsPendingSnapshotBeforeWait(t *testing.T) {
	b := networkCompletionTestBrowser()
	task := admitNetworkCompletionTestTask(t, b)
	defer b.finishNetworkTaskReceipt(task.receipt, true)
	_, offset, owner := b.actionEvidenceOffsets()
	snapshot := b.snapshotAcceptedNetworkWork(owner)
	b.mu.Lock()
	b.evidence.generation = strings.Repeat("b", 32)
	b.evidence.incomplete = false
	b.networkEntries = []protocol.NetworkEntry{{URL: "/new-generation"}}
	b.mu.Unlock()
	results, _ := runNetworkCompletionTestWait(t, b, snapshot, offset)
	result := awaitNetworkCompletionTestResult(t, results)
	if result.reason != "lifecycle_changed" || result.entries != nil || b.evidence.incomplete || task.receipt.finished {
		t.Fatalf("changed generation = %#v, current incomplete = %v, old completed = %v", result, b.evidence.incomplete, task.receipt.finished)
	}
}

func TestNetworkCompletionPendingReceiptsAreQueueAndWorkerBounded(t *testing.T) {
	b := networkCompletionTestBrowser()
	defer func() {
		b.mu.Lock()
		receipts := make([]*browserNetworkTaskReceipt, 0, len(b.networkWorkers.pending))
		for receipt := range b.networkWorkers.pending {
			receipts = append(receipts, receipt)
		}
		b.mu.Unlock()
		for _, receipt := range receipts {
			b.finishNetworkTaskReceipt(receipt, true)
		}
	}()
	admit := func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.admitNetworkTaskLocked(browserNetworkTask{generation: b.evidence.generation})
	}
	for range browserNetworkTaskQueueSize {
		if !admit() {
			t.Fatal("bounded queue did not accept available space")
		}
	}
	if admit() {
		t.Fatal("full queue accepted task")
	}
	if len(b.networkWorkers.pending) != browserNetworkTaskQueueSize {
		t.Fatal("failed admission left phantom pending receipt")
	}
	for range browserNetworkWorkerCount {
		<-b.networkTasks
	}
	for range browserNetworkWorkerCount {
		if !admit() {
			t.Fatal("queue plus active worker bound rejected legal task")
		}
	}
	if len(b.networkWorkers.pending) != browserNetworkTaskQueueSize+browserNetworkWorkerCount || admit() {
		t.Fatal("pending receipts exceeded queue plus worker bound")
	}
	if b.networkTasksDropped != 2 || !b.evidence.incomplete || b.networkWorkers.incompleteGeneration != b.evidence.generation {
		t.Fatal("bounded drops were not reflected in current evidence")
	}
}

func TestNetworkCompletionSmallQueueRollbackAndStoppedAdmission(t *testing.T) {
	b := networkCompletionTestBrowser()
	b.networkTasks = make(chan browserNetworkTask, 1)
	b.mu.Lock()
	if !b.admitNetworkTaskLocked(browserNetworkTask{generation: b.evidence.generation}) || b.admitNetworkTaskLocked(browserNetworkTask{generation: b.evidence.generation}) || len(b.networkWorkers.pending) != 1 {
		b.mu.Unlock()
		t.Fatal("small nonblocking queue admission/rollback failed")
	}
	b.mu.Unlock()
	task := <-b.networkTasks
	b.finishNetworkTaskReceipt(task.receipt, true)
	close(b.networkWorkerStop)
	b.mu.Lock()
	admitted := b.admitNetworkTaskLocked(browserNetworkTask{generation: b.evidence.generation})
	pending := len(b.networkWorkers.pending)
	b.mu.Unlock()
	if admitted || pending != 0 {
		t.Fatal("stopped worker accepted an obligation")
	}
}

func TestNetworkCompletionDropIsVisibleToLaterSnapshot(t *testing.T) {
	b := networkCompletionTestBrowser()
	b.mu.Lock()
	b.noteNetworkTaskDropLocked(b.networkWorkers)
	b.networkEntries = []protocol.NetworkEntry{{URL: "/partial"}}
	b.mu.Unlock()
	_, _, owner := b.actionEvidenceOffsets()
	entries, reason := b.acquireActionNetworkEntries(context.Background(), owner, 0)
	if reason != "metadata_incomplete" || len(entries) != 1 || entries[0].URL != "/partial" {
		t.Fatalf("dropped acquisition = %q/%#v", reason, entries)
	}
}

func TestNetworkCompletionPanicRetiresReceiptAndPreservesPanic(t *testing.T) {
	b := networkCompletionTestBrowser()
	task := admitNetworkCompletionTestTask(t, b)
	panicked := false
	func() {
		defer func() { panicked = recover() != nil }()
		b.processNetworkTask(task) // nil native request preserves its original panic.
	}()
	if !panicked {
		t.Fatal("native worker panic was swallowed")
	}
	select {
	case <-task.receipt.done:
	default:
		t.Fatal("panic did not complete private receipt")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.networkWorkers.pending) != 0 || !b.evidence.incomplete || b.networkWorkers.incompleteGeneration != task.generation {
		t.Fatal("panic retained receipt or hid incomplete metadata")
	}
}

func TestNetworkCompletionDisabledCaptureKeepsImmediateLegacySnapshot(t *testing.T) {
	b := networkCompletionTestBrowser()
	b.documentMetricsEnabled = false
	task := admitNetworkCompletionTestTask(t, b)
	if task.receipt != nil || len(b.networkWorkers.pending) != 0 {
		t.Fatal("disabled document capture allocated a receipt")
	}
	b.networkEntries = []protocol.NetworkEntry{{URL: "/legacy"}}
	_, _, owner := b.actionEvidenceOffsets()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	entries, reason := b.acquireActionNetworkEntries(ctx, owner, 0)
	if reason != "" || len(entries) != 1 || entries[0].URL != "/legacy" || b.evidence.incomplete {
		t.Fatalf("disabled capture changed legacy timing/semantics: %q/%#v", reason, entries)
	}
}

func TestNetworkCompletionUnavailableOwnerIsExplicitAndBounded(t *testing.T) {
	b := networkCompletionTestBrowser()
	b.networkWorkers = nil
	_, _, owner := b.actionEvidenceOffsets()
	entries, reason := b.acquireActionNetworkEntries(context.Background(), owner, 0)
	if reason != "worker_unavailable" || entries != nil || !b.evidence.incomplete {
		t.Fatalf("missing worker = %q/%#v/%v", reason, entries, b.evidence.incomplete)
	}
	result := &protocol.Result{Success: true, Duration: 7 * time.Millisecond}
	recordNetworkAcquisitionWarning(result, reason)
	if !result.Success || result.Duration != 7*time.Millisecond || result.ErrorMessage != "" || len(result.WarningCodes) != 1 || result.WarningCodes[0] != "browser_resource_acquisition_worker_unavailable" {
		t.Fatalf("incompleteness became a target failure: %#v", result)
	}
}

func TestNetworkCompletionSnapshotPreservesAbsoluteEvictionOffsets(t *testing.T) {
	b := networkCompletionTestBrowser()
	b.networkEntriesDropped = 2
	b.networkEntries = []protocol.NetworkEntry{{URL: "/retained-2"}, {URL: "/retained-3"}}
	_, _, owner := b.actionEvidenceOffsets()
	for _, fixture := range []struct {
		offset int
		want   int
	}{{0, 2}, {2, 2}, {3, 1}, {4, 0}} {
		entries, reason := b.acquireActionNetworkEntries(context.Background(), owner, fixture.offset)
		if reason != "" || len(entries) != fixture.want {
			t.Fatalf("absolute offset %d = %q/%d, want %d", fixture.offset, reason, len(entries), fixture.want)
		}
	}
}

// Cancel immediately after sampling the real parent status. The first sample
// remains nil; the following wait must observe the parent's cancellation.
type cancelAfterStatusSampleContext struct {
	context.Context
	cancel      context.CancelFunc
	firstSample sync.Once
}

func (ctx *cancelAfterStatusSampleContext) Err() error {
	err := ctx.Context.Err()
	ctx.firstSample.Do(ctx.cancel)
	return err
}

func TestNetworkCompletionCancellationAfterInitialStatusSamplingIsCancelled(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAfterStatusSampleContext{Context: parent, cancel: cancel}
	unfinished := &browserNetworkTaskReceipt{done: make(chan struct{})}
	reason := waitAcceptedNetworkWork(ctx, make(chan struct{}), []*browserNetworkTaskReceipt{unfinished})
	if reason != "cancelled" || parent.Err() != context.Canceled {
		t.Fatalf("cancellation between status sampling and wait = %q/%v, want cancelled", reason, parent.Err())
	}
	select {
	case <-unfinished.done:
		t.Fatal("cancelled wait incorrectly discharged unfinished metadata")
	default:
	}
}
