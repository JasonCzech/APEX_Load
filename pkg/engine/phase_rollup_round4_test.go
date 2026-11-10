package engine

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func phaseRollupRecord(vu *VUser, result *protocol.Result) *protocol.Result {
	_ = vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	return result
}

func phaseRollupEpoch(t *testing.T, result *protocol.Result) int64 {
	t.Helper()
	if result == nil || result.ProtocolEvidence == nil {
		return 0
	}
	value, present := result.ProtocolEvidence[asyncDispatchEpochEvidence]
	if !present {
		return 0
	}
	// recordResult stamps an int64; the evidence bounds normalize evidence
	// to its JSON form (a float64), as the archive stores it.
	switch epoch := value.(type) {
	case int64:
		return epoch
	case float64:
		if epoch != math.Trunc(epoch) {
			t.Fatalf("async_dispatch_epoch %v is not an integer", epoch)
		}
		return int64(epoch)
	default:
		t.Fatalf("async_dispatch_epoch is %T, want an integer", value)
		return 0
	}
}

// Round 4 #0: a fresh browser-cache hit and a locally resolved HTML step
// inside an authored transaction have no first byte to miss; they used to
// mark the transaction unsplit and zero its Server/Download time.
func TestSourceTransactionRollupLocalChildrenKeepTTFB(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "home")
	backdateSourceTransaction(vu, "home", 500*time.Millisecond)
	phaseRollupRecord(vu, &protocol.Result{
		ActionName: "page", Success: true, StatusCode: 200, DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody,
		Duration: 320 * time.Millisecond, ConnectTime: 10 * time.Millisecond, TTFB: 300 * time.Millisecond, HTTPVersionSource: "wire",
		ProtocolEvidence: map[string]any{"response_content_source": "wire"},
	})
	for range 10 {
		phaseRollupRecord(vu, &protocol.Result{
			ActionName: "static", Success: true, StatusCode: 200, DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody,
			Duration: 40 * time.Microsecond, HTTPVersionSource: "cache",
			ProtocolEvidence: map[string]any{"response_content_source": "cache"},
		})
	}
	// An HTML step resolved against the page context dispatches nothing.
	phaseRollupRecord(vu, &protocol.Result{
		ActionName: "html-step", Success: true, DependencyType: "http", ByteEvidence: protocol.ByteEvidenceNone,
		Duration: 2 * time.Millisecond, ProtocolEvidence: map[string]any{"page_context_url": "https://example.test/"},
	})
	mustSourceTransaction(t, vu, "lr_end_transaction", "home", "LR_AUTO")
	if len(sink.txs) != 1 {
		t.Fatalf("expected one transaction, got %d", len(sink.txs))
	}
	tx := sink.txs[0]
	if tx.TTFB != 300*time.Millisecond || tx.ConnectTime != 10*time.Millisecond {
		t.Fatalf("ttfb=%v connect=%v, want the answered request's 300ms TTFB kept", tx.TTFB, tx.ConnectTime)
	}
	server, download, noResponse := phaseRollupTxSplit(t, vu, tx)
	if server <= 0 || download <= 0 || noResponse != 0 {
		t.Fatalf("server=%v download=%v noTTFB=%v, want a server/download split", server, download, noResponse)
	}
}

// The local-result rule leaves real no-response children unsplit: a
// WebSocket handshake (ByteEvidenceNone, a network exchange without a TTFB)
// still marks the transaction unsplit.
func TestSourceTransactionRollupWebSocketNoneStaysUnsplit(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "chat")
	backdateSourceTransaction(vu, "chat", time.Second)
	phaseRollupRequest(vu, "page", 50*time.Millisecond)
	phaseRollupRecord(vu, &protocol.Result{ActionName: "ws-connect", Success: true, DependencyType: "websocket", ByteEvidence: protocol.ByteEvidenceNone, Duration: 400 * time.Millisecond})
	mustSourceTransaction(t, vu, "lr_end_transaction", "chat", "LR_AUTO")
	if tx := sink.txs[0]; tx.TTFB != 0 {
		t.Fatalf("ttfb=%v, want no TTFB (the handshake has no first byte)", tx.TTFB)
	}
}

// Round 4 #5/#7: an http_sequence aggregate with a timed-out child used to
// sum the answered children's TTFB and chart the timeout as Download.
func TestRecordTransactionSequenceTimedOutChildIsUnsplit(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	aggregate := &protocol.Result{ActionName: "group", Success: false, BusinessTransaction: true}
	children := []*protocol.Result{
		{DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody, Success: true, Duration: 150 * time.Millisecond, TTFB: 100 * time.Millisecond},
		{DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody, Success: true, Duration: 150 * time.Millisecond, TTFB: 100 * time.Millisecond},
		{DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody, Success: false, ErrorMessage: "timeout", Duration: 30 * time.Second, ConnectTime: 20 * time.Millisecond},
	}
	var duration time.Duration
	for _, child := range children {
		copySequenceSourceResult(aggregate, child)
		duration += child.Duration
	}
	aggregate.Duration = duration
	vu.recordTransaction("group", duration, false, aggregate)
	if len(sink.txs) != 1 {
		t.Fatalf("expected one transaction, got %d", len(sink.txs))
	}
	tx := sink.txs[0]
	if tx.TTFB != 0 || tx.ConnectTime != 20*time.Millisecond {
		t.Fatalf("ttfb=%v connect=%v, want no TTFB and the children's setup", tx.TTFB, tx.ConnectTime)
	}
	if _, leaked := tx.ProtocolEvidence[sequenceUnsplitChildrenEvidence]; leaked {
		t.Fatalf("transaction row carries the aggregate's bookkeeping: %+v", tx.ProtocolEvidence)
	}
	server, download, noResponse := phaseRollupTxSplit(t, vu, tx)
	if server != 0 || download != 0 {
		t.Fatalf("server=%v download=%v, want 0 (the timeout is not download time)", server, download)
	}
	if want := durationToMilliseconds(tx.Duration) - 20; math.Abs(noResponse-want) > 1e-6 {
		t.Fatalf("no-TTFB remainder %v, want %v", noResponse, want)
	}
}

// A sequence child served from the cache does not make the aggregate unsplit.
func TestRecordTransactionSequenceCacheHitKeepsTTFB(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	aggregate := &protocol.Result{ActionName: "group", Success: true, BusinessTransaction: true}
	copySequenceSourceResult(aggregate, &protocol.Result{DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody, Success: true, Duration: 150 * time.Millisecond, TTFB: 100 * time.Millisecond})
	copySequenceSourceResult(aggregate, &protocol.Result{DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody, Success: true, Duration: time.Millisecond, HTTPVersionSource: "cache", ProtocolEvidence: map[string]any{"response_content_source": "cache"}})
	vu.recordTransaction("group", 151*time.Millisecond, true, aggregate)
	if tx := sink.txs[0]; tx.TTFB != 100*time.Millisecond {
		t.Fatalf("ttfb=%v, want 100ms", tx.TTFB)
	}
}

// Shared decision 1: dispatch results carry the dispatch's epoch; a
// transaction opened in the dispatch carries the same epoch on its row; a
// transaction opened before it (even when closed inside it) carries none;
// each dispatch has its own epoch.
func TestAsyncDispatchEpochStampedOnResultsAndTransactions(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "outer")
	backdateSourceTransaction(vu, "outer", time.Second)
	plain := phaseRollupRecord(vu, &protocol.Result{ActionName: "page", Success: true, DependencyType: "http", Duration: 20 * time.Millisecond, TTFB: 10 * time.Millisecond})

	outer := vu.beginAsyncDispatch()
	mustSourceTransaction(t, vu, "lr_start_transaction", "poll")
	backdateSourceTransaction(vu, "poll", 500*time.Millisecond)
	first := phaseRollupRecord(vu, &protocol.Result{ActionName: "poll-req", Success: true, DependencyType: "http", Duration: 90 * time.Millisecond, TTFB: 80 * time.Millisecond})
	mustSourceTransaction(t, vu, "lr_end_transaction", "poll", "LR_AUTO")
	// The transaction opened before the dispatch closes inside it.
	mustSourceTransaction(t, vu, "lr_end_transaction", "outer", "LR_AUTO")
	vu.inAsyncDispatch = outer

	outer = vu.beginAsyncDispatch()
	second := phaseRollupRecord(vu, &protocol.Result{ActionName: "later", Success: true, DependencyType: "http", Duration: 9 * time.Millisecond, TTFB: 8 * time.Millisecond})
	vu.inAsyncDispatch = outer

	if epoch := phaseRollupEpoch(t, plain); epoch != 0 {
		t.Fatalf("ordinary request stamped epoch %d", epoch)
	}
	firstEpoch, secondEpoch := phaseRollupEpoch(t, first), phaseRollupEpoch(t, second)
	if firstEpoch <= 0 || secondEpoch <= 0 || firstEpoch == secondEpoch {
		t.Fatalf("dispatch epochs %d, %d: want two distinct positive epochs", firstEpoch, secondEpoch)
	}
	byName := map[string]*protocol.Result{}
	for _, tx := range sink.txs {
		byName[tx.ActionName] = tx
	}
	if epoch := phaseRollupEpoch(t, byName["poll"]); epoch != firstEpoch {
		t.Fatalf("callback transaction epoch %d, want %d", epoch, firstEpoch)
	}
	if epoch := phaseRollupEpoch(t, byName["outer"]); epoch != 0 {
		t.Fatalf("transaction opened outside the dispatch stamped epoch %d", epoch)
	}
}

// A parallel block run from a callback: its branch requests carry the
// callback's epoch (the branch shares it), as does the transaction the
// callback opened; a branch's own nested dispatch keeps that epoch.
func TestAsyncDispatchEpochParallelBranchesShareDispatch(t *testing.T) {
	vu, _ := phaseRollupVUser(t)
	sink := &phaseRollupRecordingSink{}
	vu.ResultSink = sink
	vu.Group = "parallel"
	vu.Proto = &phaseRollupParallelProtocol{}
	vu.Script = &dsl.Script{Protocol: phaseRollupParallelProtocolName, Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}}}
	// Earlier dispatches move the counter so the epoch is not trivially 1.
	for range 2 {
		outer := vu.beginAsyncDispatch()
		vu.inAsyncDispatch = outer
	}
	outer := vu.beginAsyncDispatch()
	epoch := int64(vu.asyncDispatchEpoch)
	mustSourceTransaction(t, vu, "lr_start_transaction", "fanout")
	backdateSourceTransaction(vu, "fanout", time.Second)
	control := &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
		{Name: "one", Steps: []dsl.Action{{Name: "one", Type: "GET", Target: "/one"}}},
		{Name: "two", Steps: []dsl.Action{{Name: "two", Type: "GET", Target: "/two"}}},
	}}
	if !vu.runParallelBranches(context.Background(), control, "action") {
		t.Fatal("parallel branches failed")
	}
	mustSourceTransaction(t, vu, "lr_end_transaction", "fanout", "LR_AUTO")
	vu.inAsyncDispatch = outer

	requests := 0
	for _, result := range sink.results {
		if result.BusinessTransaction {
			continue
		}
		requests++
		if got := phaseRollupEpoch(t, result); got != epoch {
			t.Fatalf("branch request %q epoch %d, want the callback's %d", result.ActionName, got, epoch)
		}
	}
	if requests != 2 {
		t.Fatalf("branch requests = %d", requests)
	}
	var fanout *protocol.Result
	for _, tx := range sink.txs {
		if tx.ActionName == "fanout" {
			fanout = tx
		}
	}
	if got := phaseRollupEpoch(t, fanout); got != epoch {
		t.Fatalf("fanout epoch %d, want %d", got, epoch)
	}

	branch := &VUser{asyncDispatchBranch: true, asyncDispatchEpoch: uint64(epoch), asyncDispatchEpochs: vu.asyncDispatchEpochs}
	branch.beginAsyncDispatch()
	if int64(branch.asyncDispatchEpoch) != epoch {
		t.Fatalf("dispatch branch's own dispatch moved its epoch to %d", branch.asyncDispatchEpoch)
	}
}

// Branches share the parent's epoch counter, so a branch's own dispatch
// cannot reuse an epoch the parent issues; and the branch's own dispatch
// work never reaches the parent's open transactions (all opened outside
// that dispatch), as the report never lets them claim it.
func TestAsyncDispatchEpochBranchOwnDispatch(t *testing.T) {
	parent := &VUser{}
	parent.asyncDispatchEpochCounter()
	branch := &VUser{asyncDispatchEpochs: parent.asyncDispatchEpochs}
	branch.beginAsyncDispatch()
	branchEpoch := branch.asyncDispatchEpoch
	parent.beginAsyncDispatch()
	if branchEpoch == 0 || parent.asyncDispatchEpoch == branchEpoch {
		t.Fatalf("branch epoch %d, parent epoch %d: want distinct epochs", branchEpoch, parent.asyncDispatchEpoch)
	}

	sink := &parallelResultSink{}
	sink.notePhases(&protocol.Result{DependencyType: "http", Duration: 20 * time.Millisecond, TTFB: 10 * time.Millisecond, ProtocolEvidence: map[string]any{"async_dispatch": true, asyncDispatchEpochEvidence: int64(branchEpoch)}})
	if got := sink.phaseTotals(); got.ttfb != 0 {
		t.Fatalf("branch's own dispatch work rolled toward the parent's transactions: %+v", got)
	}
	sink.notePhases(&protocol.Result{DependencyType: "http", Duration: 20 * time.Millisecond, TTFB: 10 * time.Millisecond})
	if got := sink.phaseTotals(); got.ttfb != 10*time.Millisecond {
		t.Fatalf("ordinary branch request lost: %+v", got)
	}
	dispatchSink := &parallelResultSink{dispatchBranch: true}
	dispatchSink.notePhases(&protocol.Result{DependencyType: "http", Duration: 20 * time.Millisecond, TTFB: 10 * time.Millisecond, ProtocolEvidence: map[string]any{"async_dispatch": true}})
	if got := dispatchSink.phaseTotals(); got.ttfb != 10*time.Millisecond {
		t.Fatalf("dispatch branch's work lost: %+v", got)
	}
}

// A sequence aggregate recorded during a dispatch carries the epoch, so its
// own dispatch-stamped children can be claimed by it.
func TestAsyncDispatchEpochOnSequenceTransactionInDispatch(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	outer := vu.beginAsyncDispatch()
	vu.recordTransaction("group", 100*time.Millisecond, true, &protocol.Result{ActionName: "group", BusinessTransaction: true, Duration: 100 * time.Millisecond, TTFB: 50 * time.Millisecond})
	vu.inAsyncDispatch = outer
	vu.recordTransaction("after", 100*time.Millisecond, true, &protocol.Result{ActionName: "after", BusinessTransaction: true, Duration: 100 * time.Millisecond})
	if got := phaseRollupEpoch(t, sink.txs[0]); got != int64(vu.asyncDispatchEpoch) || got <= 0 {
		t.Fatalf("sequence transaction in dispatch epoch %d, want %d", got, vu.asyncDispatchEpoch)
	}
	if got := phaseRollupEpoch(t, sink.txs[1]); got != 0 {
		t.Fatalf("transaction outside dispatch stamped %d", got)
	}
}
