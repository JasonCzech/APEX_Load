package engine

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type phaseRollupRecordingSink struct {
	mu      sync.Mutex
	results []*protocol.Result
	txs     []*protocol.Result
}

func (s *phaseRollupRecordingSink) RecordResult(r *protocol.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r != nil {
		s.results = append(s.results, r)
	}
	return nil
}

func (s *phaseRollupRecordingSink) RecordTransaction(r *protocol.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r != nil {
		cp := *r
		s.txs = append(s.txs, &cp)
	}
	return nil
}

func phaseRollupTxSplit(t *testing.T, vu *VUser, tx *protocol.Result) (server, download, noResponse float64) {
	t.Helper()
	snap := vu.Metrics.computeSnapshot([]*protocol.Result{tx}, time.Second)
	server, download, noResponse, ok := snap.PhaseObserved.ResponseSplit(1, durationToMilliseconds(tx.Duration), snap.AvgDNSMs, snap.AvgConnectMs, snap.AvgTLSMs, snap.AvgTTFBMs)
	if !ok {
		t.Fatalf("no phase observations: %+v", snap.PhaseObserved)
	}
	return server, download, noResponse
}

// Round 3 #3: an authored transaction with two answered requests and one
// that timed out (TTFB 0 after 30 s) charted the timeout's 30 s as Download.
// A child without a first byte makes the transaction's time after setup
// unsplit (no-TTFB), never download or server.
func TestSourceTransactionRollupTimeoutChildIsNotDownload(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "checkout")
	backdateSourceTransaction(vu, "checkout", 30200*time.Millisecond)
	for _, name := range []string{"cart", "price"} {
		_ = vu.recordResult(&protocol.Result{
			ActionName: name, Success: true, StatusCode: 200, DependencyType: "http",
			Duration: 110 * time.Millisecond, TTFB: 100 * time.Millisecond,
		}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	}
	_ = vu.recordResult(&protocol.Result{
		ActionName: "pay", Success: false, DependencyType: "http", ErrorMessage: "timeout",
		Duration: 30 * time.Second, ConnectTime: 20 * time.Millisecond,
	}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	mustSourceTransaction(t, vu, "lr_end_transaction", "checkout", "LR_AUTO")
	if len(sink.txs) != 1 {
		t.Fatalf("expected one transaction, got %d", len(sink.txs))
	}
	tx := sink.txs[0]
	if tx.TTFB != 0 || tx.ConnectTime != 20*time.Millisecond {
		t.Fatalf("transaction with a timed-out child: ttfb=%v connect=%v, want no TTFB and the children's setup", tx.TTFB, tx.ConnectTime)
	}
	server, download, noResponse := phaseRollupTxSplit(t, vu, tx)
	if server != 0 || download != 0 {
		t.Fatalf("server=%v download=%v, want 0 (the timeout is not server or download time)", server, download)
	}
	if want := durationToMilliseconds(tx.Duration) - 20; math.Abs(noResponse-want) > 1e-6 {
		t.Fatalf("no-TTFB remainder %v, want %v", noResponse, want)
	}
}

// Round 3 #3/#14: a non-HTTP child (no TTFB recorded by its protocol) inside
// a transaction that also made an HTTP request is not charted as download.
func TestSourceTransactionRollupNonHTTPChildIsUnsplit(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "order")
	backdateSourceTransaction(vu, "order", 2*time.Second)
	phaseRollupRequest(vu, "page", 50*time.Millisecond)
	_ = vu.recordResult(&protocol.Result{ActionName: "query", Success: true, DependencyType: "database", Duration: 1500 * time.Millisecond}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	mustSourceTransaction(t, vu, "lr_end_transaction", "order", "LR_AUTO")
	if tx := sink.txs[0]; tx.TTFB != 0 || tx.ConnectTime != 5*time.Millisecond {
		t.Fatalf("ttfb=%v connect=%v, want no TTFB split and the HTTP setup kept", tx.TTFB, tx.ConnectTime)
	}
}

// A transaction whose only children are TTFB-less protocol steps reports what
// a transaction without a rollup does (no synthesized phases).
func TestSourceTransactionRollupOnlyUnsplitChildren(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "ws")
	backdateSourceTransaction(vu, "ws", time.Second)
	_ = vu.recordResult(&protocol.Result{ActionName: "send", Success: true, DependencyType: "websocket", Duration: 900 * time.Millisecond}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	mustSourceTransaction(t, vu, "lr_end_transaction", "ws", "LR_AUTO")
	if tx := sink.txs[0]; tx.TTFB != 0 || tx.DNSTime+tx.ConnectTime+tx.TLSTime != 0 {
		t.Fatalf("phases %+v", tx)
	}
}

// Round 3 #4: a concurrent (non-transparent) http_sequence aggregate sums its
// overlapping children's phases; recorded as a business transaction they are
// scaled to the group's wall time, so Server cannot exceed the transaction.
func TestRecordTransactionBoundsConcurrentSequencePhases(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	aggregate := &protocol.Result{ActionName: "group", Duration: time.Second, Success: true, BusinessTransaction: true}
	for range 6 {
		copySequenceSourceResult(aggregate, &protocol.Result{DependencyType: "http", Success: true, Duration: 950 * time.Millisecond, TLSTime: 100 * time.Millisecond, TTFB: 900 * time.Millisecond})
	}
	if aggregate.TTFB != 5400*time.Millisecond {
		t.Fatalf("test setup: aggregate TTFB %v", aggregate.TTFB)
	}
	vu.recordTransaction("group", aggregate.Duration, true, aggregate)
	if len(sink.txs) != 1 {
		t.Fatalf("expected one transaction, got %d", len(sink.txs))
	}
	tx := sink.txs[0]
	if tx.TTFB != time.Second {
		t.Fatalf("ttfb = %v, want the 1s wall time", tx.TTFB)
	}
	if ratio := float64(tx.TLSTime) / float64(tx.TTFB); math.Abs(ratio-100.0/900.0) > 0.01 {
		t.Fatalf("TLS/TTFB = %.3f, want its 1/9 share kept", ratio)
	}
	server, download, _ := phaseRollupTxSplit(t, vu, tx)
	if stack := durationToMilliseconds(tx.TLSTime) + server + download; stack > durationToMilliseconds(tx.Duration)+1e-6 {
		t.Fatalf("phase stack %v exceeds the %v transaction", stack, tx.Duration)
	}
	// A single request already fits and is copied unchanged.
	vu.recordTransaction("single", 200*time.Millisecond, true, &protocol.Result{DNSTime: 3 * time.Millisecond, ConnectTime: 4 * time.Millisecond, TLSTime: 5 * time.Millisecond, TTFB: 60 * time.Millisecond, Duration: 200 * time.Millisecond})
	if tx := sink.txs[1]; tx.DNSTime != 3*time.Millisecond || tx.ConnectTime != 4*time.Millisecond || tx.TLSTime != 5*time.Millisecond || tx.TTFB != 60*time.Millisecond {
		t.Fatalf("single-request phases changed: %+v", tx)
	}
}

// Round 3 #13: a transaction opened inside an async callback rolls up the
// callback's own requests; the transaction that was open when the dispatch
// began still takes none. Dispatch work is stamped async_dispatch.
func TestSourceTransactionOpenedInAsyncCallbackRollsUpItsRequests(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "outer")
	backdateSourceTransaction(vu, "outer", time.Second)
	phaseRollupRequest(vu, "page", 40*time.Millisecond)

	outer := vu.beginAsyncDispatch()
	mustSourceTransaction(t, vu, "lr_start_transaction", "poll")
	backdateSourceTransaction(vu, "poll", 500*time.Millisecond)
	callbackRequest := &protocol.Result{ActionName: "poll-req", Success: true, DependencyType: "http", Duration: 90 * time.Millisecond, ConnectTime: 5 * time.Millisecond, TTFB: 80 * time.Millisecond}
	_ = vu.recordResult(callbackRequest, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	mustSourceTransaction(t, vu, "lr_end_transaction", "poll", "LR_AUTO")
	vu.inAsyncDispatch = outer

	mustSourceTransaction(t, vu, "lr_end_transaction", "outer", "LR_AUTO")
	if len(sink.txs) != 2 {
		t.Fatalf("expected two transactions, got %d", len(sink.txs))
	}
	byName := map[string]*protocol.Result{}
	for _, tx := range sink.txs {
		byName[tx.ActionName] = tx
	}
	if poll := byName["poll"]; poll == nil || poll.TTFB != 80*time.Millisecond || poll.ConnectTime != 5*time.Millisecond {
		t.Fatalf("callback transaction lost its request's phases: %+v", poll)
	}
	if outerTx := byName["outer"]; outerTx == nil || outerTx.TTFB != 40*time.Millisecond {
		t.Fatalf("callback request rolled into the transaction open before the dispatch: %+v", outerTx)
	}
	if dispatch, _ := callbackRequest.ProtocolEvidence["async_dispatch"].(bool); !dispatch {
		t.Fatalf("callback request not stamped async_dispatch: %+v", callbackRequest.ProtocolEvidence)
	}
}

// A transaction opened in one dispatch and left open takes no dispatch work
// from a later dispatch, but takes ordinary requests in between.
func TestSourceTransactionAsyncDispatchEpochScopesCallbackWork(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	outer := vu.beginAsyncDispatch()
	mustSourceTransaction(t, vu, "lr_start_transaction", "session")
	backdateSourceTransaction(vu, "session", time.Second)
	phaseRollupRequest(vu, "first-callback-request", 10*time.Millisecond)
	vu.inAsyncDispatch = outer

	phaseRollupRequest(vu, "main-flow", 20*time.Millisecond)

	outer = vu.beginAsyncDispatch()
	phaseRollupRequest(vu, "later-callback-request", 400*time.Millisecond)
	vu.inAsyncDispatch = outer

	mustSourceTransaction(t, vu, "lr_end_transaction", "session", "LR_AUTO")
	if tx := sink.txs[0]; tx.TTFB != 30*time.Millisecond {
		t.Fatalf("ttfb = %v, want 30ms (its own callback request and the main flow, not a later dispatch)", tx.TTFB)
	}
}

// A parallel block inside a callback: its branch requests roll into a
// transaction the callback opened, and each is stamped async_dispatch.
func TestSourceTransactionInAsyncCallbackTakesParallelBranches(t *testing.T) {
	vu, _ := phaseRollupVUser(t)
	sink := &phaseRollupRecordingSink{}
	vu.ResultSink = sink
	vu.Group = "parallel"
	vu.Proto = &phaseRollupParallelProtocol{}
	vu.Script = &dsl.Script{Protocol: phaseRollupParallelProtocolName, Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}}}
	mustSourceTransaction(t, vu, "lr_start_transaction", "before")
	backdateSourceTransaction(vu, "before", time.Second)

	outer := vu.beginAsyncDispatch()
	mustSourceTransaction(t, vu, "lr_start_transaction", "fanout")
	backdateSourceTransaction(vu, "fanout", time.Second)
	control := &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
		{Name: "one", Steps: []dsl.Action{{Name: "one", Type: "GET", Target: "/one"}}},
		{Name: "two", Steps: []dsl.Action{{Name: "two", Type: "GET", Target: "/two"}}},
	}}
	ok := vu.runParallelBranches(context.Background(), control, "action")
	mustSourceTransaction(t, vu, "lr_end_transaction", "fanout", "LR_AUTO")
	vu.inAsyncDispatch = outer
	if !ok {
		t.Fatal("parallel branches failed")
	}
	mustSourceTransaction(t, vu, "lr_end_transaction", "before", "LR_AUTO")

	byName := map[string]*protocol.Result{}
	for _, tx := range sink.txs {
		byName[tx.ActionName] = tx
	}
	if fanout := byName["fanout"]; fanout == nil || fanout.TTFB != 40*time.Millisecond || fanout.ConnectTime != 8*time.Millisecond {
		t.Fatalf("callback transaction lost its parallel branches: %+v", fanout)
	}
	if before := byName["before"]; before == nil || before.TTFB != 0 {
		t.Fatalf("branch requests rolled into the transaction open before the dispatch: %+v", before)
	}
	branchRequests := 0
	for _, result := range sink.results {
		if result.BusinessTransaction {
			continue
		}
		branchRequests++
		if dispatch, _ := result.ProtocolEvidence["async_dispatch"].(bool); !dispatch {
			t.Fatalf("branch request %q not stamped async_dispatch: %+v", result.ActionName, result.ProtocolEvidence)
		}
	}
	if branchRequests != 2 {
		t.Fatalf("branch requests recorded = %d", branchRequests)
	}
}

// Ordinary requests are not stamped.
func TestRecordResultOutsideDispatchIsNotStamped(t *testing.T) {
	vu, _ := phaseRollupVUser(t)
	result := &protocol.Result{ActionName: "page", Success: true, Duration: time.Millisecond}
	_ = vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if _, ok := result.ProtocolEvidence["async_dispatch"]; ok {
		t.Fatalf("ordinary request stamped: %+v", result.ProtocolEvidence)
	}
}
