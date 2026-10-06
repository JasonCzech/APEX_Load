package browser

import (
	"context"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const browserNetworkMetadataSettleTimeout = 5 * time.Second

// Only the existing bounded queue and workers can own pending receipts. A
// receipt is retired individually, so out-of-order completions never require
// an unbounded sequence-hole history. All mutable fields are guarded by b.mu.
type browserNetworkWorkerState struct {
	stop                 chan struct{}
	retired              bool
	incompleteGeneration string
	pending              map[*browserNetworkTaskReceipt]struct{}
}

type browserNetworkTaskReceipt struct {
	owner      *browserNetworkWorkerState
	generation string
	done       chan struct{}
	finished   bool
}

type browserNetworkActionOwner struct {
	enabled    bool
	generation string
	workers    *browserNetworkWorkerState
}

type browserNetworkWorkSnapshot struct {
	owner    browserNetworkActionOwner
	receipts []*browserNetworkTaskReceipt
	reason   string
}

func newBrowserNetworkWorkerState() *browserNetworkWorkerState {
	// Generation authority comes from action/admission time, paired with this
	// exact worker-state identity, rather than being pinned at worker startup.
	return &browserNetworkWorkerState{stop: make(chan struct{}), pending: make(map[*browserNetworkTaskReceipt]struct{})}
}

func (b *Browser) actionEvidenceOffsets() (int, int, browserNetworkActionOwner) {
	b.mu.Lock()
	defer b.mu.Unlock()
	owner := browserNetworkActionOwner{enabled: b.documentMetricsEnabled, generation: b.evidence.generation, workers: b.networkWorkers}
	// Absolute offsets survive ring eviction during the action and settling.
	return b.consoleDropped + len(b.consoleEntries), b.networkEntriesDropped + len(b.networkEntries), owner
}

// Retiring an epoch wakes evidence waiters without pretending its abandoned
// metadata work completed. The existing native/HAR worker joins still own
// physical cleanup. Callers hold b.mu.
func (b *Browser) retireNetworkWorkersLocked() {
	if workers := b.networkWorkers; workers != nil && !workers.retired {
		workers.retired = true
		close(workers.stop)
	}
	b.networkWorkers = nil
}

// Admission and receipt publication share one lock. A worker cannot finish a
// task before its receipt exists, and a rejected send leaves no obligation.
// Callers hold b.mu; the queue send is always nonblocking.
func (b *Browser) admitNetworkTaskLocked(task browserNetworkTask) bool {
	if b.closing || b.networkTasks == nil || b.networkWorkerStop == nil || task.generation != b.evidence.generation {
		return false
	}
	select {
	case <-b.networkWorkerStop:
		return false
	default:
	}
	workers := b.networkWorkers
	if b.documentMetricsEnabled {
		limit := min(cap(b.networkTasks)+browserNetworkWorkerCount, browserNetworkTaskQueueSize+browserNetworkWorkerCount)
		if workers == nil || workers.retired || len(workers.pending) >= limit {
			b.noteNetworkTaskDropLocked(workers)
			return false
		}
		task.receipt = &browserNetworkTaskReceipt{owner: workers, generation: task.generation, done: make(chan struct{})}
		workers.pending[task.receipt] = struct{}{}
	}
	select {
	case b.networkTasks <- task:
		return true
	default:
		if task.receipt != nil {
			delete(workers.pending, task.receipt)
		}
		b.noteNetworkTaskDropLocked(workers)
		return false
	}
}

func (b *Browser) noteNetworkTaskDropLocked(workers *browserNetworkWorkerState) {
	b.networkTasksDropped++
	b.evidence.incomplete = true
	if workers != nil && workers == b.networkWorkers {
		workers.incompleteGeneration = b.evidence.generation
	}
}

// A per-task wrapper keeps defers bounded by the worker count, including on a
// stale-generation return or panic. Panic propagation is deliberately unchanged.
func (b *Browser) networkTaskCurrentLocked(task browserNetworkTask) bool {
	if task.generation == "" && task.receipt == nil {
		return true // Preserve the direct legacy recording helper's contract.
	}
	if b.closing || task.generation != b.evidence.generation {
		return false
	}
	return task.receipt == nil || task.receipt.owner == b.networkWorkers && !task.receipt.owner.retired && task.receipt.generation == task.generation
}

func (b *Browser) processNetworkTask(task browserNetworkTask) {
	completed := false
	defer func() { b.finishNetworkTaskReceipt(task.receipt, completed) }()
	b.recordNetworkTask(task)
	completed = true
}

func (b *Browser) finishNetworkTaskReceipt(receipt *browserNetworkTaskReceipt, completed bool) {
	if receipt == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if receipt.finished {
		return
	}
	receipt.finished = true
	workers := receipt.owner
	delete(workers.pending, receipt)
	if !completed && workers == b.networkWorkers && receipt.generation == b.evidence.generation {
		workers.incompleteGeneration = receipt.generation
		b.evidence.incomplete = true
	}
	close(receipt.done)
}

func (b *Browser) networkOwnerReasonLocked(owner browserNetworkActionOwner) string {
	if owner.workers == nil || owner.generation == "" {
		return "worker_unavailable"
	}
	if owner.workers != b.networkWorkers || owner.generation != b.evidence.generation {
		if b.closing && owner.workers.retired {
			return "lifecycle_closed"
		}
		return "lifecycle_changed"
	}
	if b.closing || owner.workers.retired {
		return "lifecycle_closed"
	}
	return ""
}

func (b *Browser) snapshotAcceptedNetworkWork(owner browserNetworkActionOwner) browserNetworkWorkSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	snapshot := browserNetworkWorkSnapshot{owner: owner, reason: b.networkOwnerReasonLocked(owner)}
	if snapshot.reason == "" {
		snapshot.receipts = make([]*browserNetworkTaskReceipt, 0, len(owner.workers.pending))
		for receipt := range owner.workers.pending {
			if receipt.generation == owner.generation {
				snapshot.receipts = append(snapshot.receipts, receipt)
			}
		}
	}
	return snapshot
}

func networkMetadataContextReason(ctx context.Context) string {
	switch ctx.Err() {
	case nil:
		return ""
	case context.Canceled:
		return "cancelled"
	default:
		return "metadata_timeout"
	}
}

func waitAcceptedNetworkWork(ctx context.Context, stop <-chan struct{}, receipts []*browserNetworkTaskReceipt) string {
	for _, receipt := range receipts {
		if reason := networkMetadataContextReason(ctx); reason != "" {
			return reason
		}
		select {
		case <-stop:
			return "lifecycle_closed"
		default:
		}
		select {
		case <-receipt.done:
		case <-stop:
			return "lifecycle_closed"
		case <-ctx.Done():
			return networkMetadataContextReason(ctx)
		}
	}
	// Cancellation/close win even when completion becomes ready concurrently.
	if reason := networkMetadataContextReason(ctx); reason != "" {
		return reason
	}
	select {
	case <-stop:
		return "lifecycle_closed"
	default:
		return ""
	}
}

func (b *Browser) settleNetworkWorkSnapshot(ctx context.Context, snapshot browserNetworkWorkSnapshot, offset int) ([]protocol.NetworkEntry, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	reason := snapshot.reason
	waitCtx := ctx
	if reason == "" {
		// An owner invalidated before settling must not spend the metadata
		// budget waiting for work that cannot be published into this result.
		b.mu.Lock()
		reason = b.networkOwnerReasonLocked(snapshot.owner)
		b.mu.Unlock()
	}
	if reason == "" {
		bounded, cancel := context.WithTimeout(ctx, browserNetworkMetadataSettleTimeout)
		defer cancel()
		waitCtx = bounded
		reason = waitAcceptedNetworkWork(bounded, snapshot.owner.workers.stop, snapshot.receipts)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	ownerReason := b.networkOwnerReasonLocked(snapshot.owner)
	if ownerReason != "" {
		reason = ownerReason
	}
	if reason == "" {
		// Completion can race cancellation or waiting for the snapshot lock.
		// Keep the settling deadline alive through the final ownership check.
		reason = networkMetadataContextReason(waitCtx)
	}
	if reason == "" && snapshot.owner.workers.incompleteGeneration == snapshot.owner.generation {
		reason = "metadata_incomplete"
	}
	if reason != "" {
		// Never poison a replacement epoch, including one with an identical
		// generation string but a different private worker-state identity.
		if snapshot.owner.workers == b.networkWorkers && snapshot.owner.generation == b.evidence.generation {
			b.evidence.incomplete = true
			if snapshot.owner.workers != nil {
				snapshot.owner.workers.incompleteGeneration = snapshot.owner.generation
			}
		}
	}
	if ownerReason != "" {
		return nil, reason
	}
	return b.networkEntriesSinceLocked(offset), reason
}

func (b *Browser) acquireActionNetworkEntries(ctx context.Context, owner browserNetworkActionOwner, offset int) ([]protocol.NetworkEntry, string) {
	if !owner.enabled {
		return b.networkEntriesSince(offset), ""
	}
	return b.settleNetworkWorkSnapshot(ctx, b.snapshotAcceptedNetworkWork(owner), offset)
}

func recordNetworkAcquisitionWarning(result *protocol.Result, reason string) {
	if reason != "" {
		// Evidence incompleteness is advisory, not an invented target failure.
		// WarningCodes already flow through bounded live/archive projections.
		result.WarningCodes = append(result.WarningCodes, "browser_resource_acquisition_"+reason)
	}
}
