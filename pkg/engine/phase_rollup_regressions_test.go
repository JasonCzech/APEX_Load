package engine

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func phaseRollupSourceAction(name, function string, args ...any) dsl.Action {
	return dsl.Action{Name: name, Type: "source_transaction", Params: map[string]any{"function": function, "args": args}}
}

func phaseRollupVUser(t *testing.T) (*VUser, *llmTestTxSink) {
	t.Helper()
	sink := &llmTestTxSink{}
	mc := NewMetricsCollector(100)
	t.Cleanup(mc.Stop)
	return &VUser{ID: 1, Variables: make(map[string]string), Metrics: mc, ResultSink: sink}, sink
}

func phaseRollupRequest(vu *VUser, name string, ttfb time.Duration) {
	_ = vu.recordResult(&protocol.Result{
		ActionName: name, Success: true, StatusCode: 200, Duration: ttfb + 10*time.Millisecond,
		ConnectTime: 5 * time.Millisecond, TTFB: ttfb,
	}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
}

func mustSourceTransaction(t *testing.T, vu *VUser, function string, args ...any) {
	t.Helper()
	if err := vu.handleSourceTransactionAction(phaseRollupSourceAction("tx", function, args...)); err != nil {
		t.Fatalf("%s: %v", function, err)
	}
}

func backdateSourceTransaction(vu *VUser, name string, by time.Duration) {
	vu.sourceTransactions[name].startedAt = time.Now().Add(-by)
}

// A passing authored transaction with a phase rollup kept the VUser's region
// and network profile; the synthesized rollup source used to blank them.
func TestSourceTransactionPhaseRollupKeepsRegionAndProfile(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	vu.Region = "eu-west"
	vu.resultPhase = "steady"
	vu.Script = &dsl.Script{}
	vu.Script.Config.WebRuntime.NetworkSimulation = dsl.NetworkSimulationConfig{Mode: "preset", Preset: "4g"}
	want := networkProfileLabel(vu.Script.Config.WebRuntime.NetworkSimulation)
	if want == "" {
		t.Fatal("test setup: expected a network profile label")
	}
	mustSourceTransaction(t, vu, "lr_start_transaction", "login")
	backdateSourceTransaction(vu, "login", time.Second)
	phaseRollupRequest(vu, "login-page", 40*time.Millisecond)
	mustSourceTransaction(t, vu, "lr_end_transaction", "login", "LR_AUTO")
	if len(sink.txs) != 1 {
		t.Fatalf("expected one transaction, got %d", len(sink.txs))
	}
	tx := sink.txs[0]
	if tx.TTFB != 40*time.Millisecond {
		t.Fatalf("rollup TTFB = %v", tx.TTFB)
	}
	if tx.Region != "eu-west" || tx.NetworkProfile != want || tx.Phase != "steady" {
		t.Fatalf("transaction lost its identity: region=%q profile=%q phase=%q", tx.Region, tx.NetworkProfile, tx.Phase)
	}
	snap := vu.Metrics.computeSnapshot([]*protocol.Result{tx}, time.Second)
	if region, ok := snap.ByRegion["eu-west"]; !ok || region == nil {
		t.Fatalf("transaction missing from the eu-west breakdown: %+v", snap.ByRegion)
	}
}

// Multi-region: two VUsers in different regions each keep their own region.
func TestSourceTransactionPhaseRollupMultiRegion(t *testing.T) {
	for _, region := range []string{"us-east", "ap-south"} {
		vu, sink := phaseRollupVUser(t)
		vu.Region = region
		mustSourceTransaction(t, vu, "lr_start_transaction", "search")
		backdateSourceTransaction(vu, "search", time.Second)
		phaseRollupRequest(vu, "search-api", 30*time.Millisecond)
		mustSourceTransaction(t, vu, "lr_end_transaction", "search", "LR_FAIL")
		if len(sink.txs) != 1 || sink.txs[0].Region != region {
			t.Fatalf("region %s: transactions %+v", region, sink.txs)
		}
	}
}

// Async-conversation responses and background requests are not part of the
// enclosing transaction.
func TestSourceTransactionPhaseRollupSkipsAsyncResults(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "checkout")
	backdateSourceTransaction(vu, "checkout", time.Second)
	phaseRollupRequest(vu, "cart", 50*time.Millisecond)
	_ = vu.recordResult(&protocol.Result{
		ActionName: "poll", Success: true, Duration: 300 * time.Millisecond, TTFB: 290 * time.Millisecond, ConnectTime: 20 * time.Millisecond,
		ProtocolEvidence: map[string]any{"background_request": true},
	}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	vu.inAsyncDispatch = true
	phaseRollupRequest(vu, "async status", 200*time.Millisecond)
	vu.inAsyncDispatch = false
	_ = vu.recordResult(&protocol.Result{
		ActionName: "async status", Success: true, Duration: 100 * time.Millisecond, TTFB: 90 * time.Millisecond,
		ProtocolEvidence: map[string]any{"async_conversation": "status"},
	}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	mustSourceTransaction(t, vu, "lr_end_transaction", "checkout", "LR_AUTO")
	tx := sink.txs[0]
	if tx.TTFB != 50*time.Millisecond || tx.ConnectTime != 5*time.Millisecond {
		t.Fatalf("async traffic rolled into the transaction: ttfb=%v connect=%v", tx.TTFB, tx.ConnectTime)
	}
}

// Requests made while a transaction is stopped are outside its duration.
func TestSourceTransactionPhaseRollupSkipsStoppedTransaction(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "report")
	backdateSourceTransaction(vu, "report", time.Second)
	phaseRollupRequest(vu, "first", 60*time.Millisecond)
	mustSourceTransaction(t, vu, "lr_stop_transaction", "report")
	phaseRollupRequest(vu, "after-stop-1", 100*time.Millisecond)
	phaseRollupRequest(vu, "after-stop-2", 100*time.Millisecond)
	mustSourceTransaction(t, vu, "lr_end_transaction", "report", "LR_AUTO")
	if tx := sink.txs[0]; tx.TTFB != 60*time.Millisecond || tx.ConnectTime != 5*time.Millisecond {
		t.Fatalf("stopped transaction took post-stop phases: ttfb=%v connect=%v", tx.TTFB, tx.ConnectTime)
	}
}

// Serial sums of overlapping requests are bounded to the transaction's own
// duration so its live phase stack cannot exceed it.
func TestSourceTransactionPhaseRollupIsBoundedByDuration(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "dashboard")
	backdateSourceTransaction(vu, "dashboard", 100*time.Millisecond)
	for _, name := range []string{"a", "b", "c"} { // three concurrent ~90 ms requests
		_ = vu.recordResult(&protocol.Result{
			ActionName: name, Success: true, Duration: 95 * time.Millisecond,
			DNSTime: 10 * time.Millisecond, ConnectTime: 20 * time.Millisecond, TLSTime: 30 * time.Millisecond, TTFB: 90 * time.Millisecond,
		}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	}
	mustSourceTransaction(t, vu, "lr_end_transaction", "dashboard", "LR_AUTO")
	tx := sink.txs[0]
	if tx.TTFB <= 0 || tx.TTFB > tx.Duration {
		t.Fatalf("TTFB %v not within duration %v", tx.TTFB, tx.Duration)
	}
	if setup := tx.DNSTime + tx.ConnectTime + tx.TLSTime; setup > tx.TTFB {
		t.Fatalf("setup %v exceeds TTFB %v", setup, tx.TTFB)
	}
	// Each phase keeps its share of the overlapped work (scaled, not clamped
	// in phase order): setup is 60/90 of TTFB, TLS is half of setup.
	setup := tx.DNSTime + tx.ConnectTime + tx.TLSTime
	if ratio := float64(setup) / float64(tx.TTFB); math.Abs(ratio-60.0/90.0) > 0.01 {
		t.Fatalf("setup/TTFB = %.3f, want 0.667", ratio)
	}
	if ratio := float64(tx.TLSTime) / float64(setup); math.Abs(ratio-0.5) > 0.01 {
		t.Fatalf("TLS/setup = %.3f, want 0.5", ratio)
	}
}

func TestSourceTransactionPhaseRollupBoundedTo(t *testing.T) {
	r := &sourceTransactionPhaseRollup{dns: 30 * time.Millisecond, connect: 60 * time.Millisecond, tls: 90 * time.Millisecond}
	dns, connect, tls, ttfb := r.boundedTo(100 * time.Millisecond)
	// Setup with no first byte scales to the duration, keeping each share.
	if ttfb != 0 || dns+connect+tls > 100*time.Millisecond || dns < 16*time.Millisecond || connect < 33*time.Millisecond || tls < 49*time.Millisecond {
		t.Fatalf("no-TTFB rollup bounded to %v/%v/%v/%v", dns, connect, tls, ttfb)
	}
}

const phaseRollupParallelProtocolName = "phase-rollup-parallel-test"

type phaseRollupParallelProtocol struct{ parallelTestProtocol }

func (*phaseRollupParallelProtocol) Name() string { return phaseRollupParallelProtocolName }
func (*phaseRollupParallelProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{
		ActionName: action.Name, StartTime: time.Now(), Duration: 30 * time.Millisecond, Success: true,
		ConnectTime: 4 * time.Millisecond, TTFB: 20 * time.Millisecond,
	}, nil
}

func init() {
	protocol.Register(phaseRollupParallelProtocolName, func() protocol.Protocol { return &phaseRollupParallelProtocol{} })
}

// Requests inside a parallel block roll into the parent's open transaction.
func TestSourceTransactionPhaseRollupIncludesParallelBranches(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	vu.Group = "parallel"
	vu.Proto = &phaseRollupParallelProtocol{}
	vu.Script = &dsl.Script{Protocol: phaseRollupParallelProtocolName, Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}}}
	mustSourceTransaction(t, vu, "lr_start_transaction", "dashboard")
	backdateSourceTransaction(vu, "dashboard", time.Second)
	control := &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
		{Name: "one", Steps: []dsl.Action{{Name: "one", Type: "GET", Target: "/one"}}},
		{Name: "two", Steps: []dsl.Action{{Name: "two", Type: "GET", Target: "/two"}, {Name: "three", Type: "GET", Target: "/three"}}},
	}}
	if !vu.runParallelBranches(context.Background(), control, "action") {
		t.Fatal("parallel branches failed")
	}
	mustSourceTransaction(t, vu, "lr_end_transaction", "dashboard", "LR_AUTO")
	if len(sink.txs) != 1 {
		t.Fatalf("expected one transaction, got %d", len(sink.txs))
	}
	if tx := sink.txs[0]; tx.TTFB != 60*time.Millisecond || tx.ConnectTime != 12*time.Millisecond {
		t.Fatalf("parallel branch phases missing: ttfb=%v connect=%v", tx.TTFB, tx.ConnectTime)
	}
}

// Mixed timeouts and successes: the split stacks to the average duration, and
// download counts only samples that got a first byte.
func TestPhaseObservationsResponseSplitStacksToAverageWithTimeouts(t *testing.T) {
	type sample struct{ dns, connect, tls, ttfb, dur float64 }
	samples := []sample{
		{dns: 5, connect: 10, tls: 20, ttfb: 135, dur: 185}, // full new connection
		{ttfb: 60, dur: 80},       // reused connection
		{connect: 30, dur: 30000}, // connect then timeout, no first byte
		{dur: 5000},               // non-HTTP / no TTFB
	}
	var observed PhaseObservations
	var sumDur, sumDNS, sumConnect, sumTLS, sumTTFB, sumDownload float64
	for _, s := range samples {
		observed.ObserveMs(s.dns, s.connect, s.tls, s.ttfb, s.dur)
		sumDur += s.dur
		sumDNS += s.dns
		sumConnect += s.connect
		sumTLS += s.tls
		sumTTFB += s.ttfb
		if s.ttfb > 0 {
			sumDownload += s.dur - s.ttfb
		}
	}
	n := float64(len(samples))
	server, download, noResponse, ok := observed.ResponseSplit(int64(len(samples)), sumDur/n, sumDNS/n, sumConnect/n, sumTLS/n, sumTTFB/n)
	if !ok {
		t.Fatal("known observations should split")
	}
	if math.Abs(download-sumDownload/n) > 1e-9 {
		t.Fatalf("download %v, want %v (first-byte samples only)", download, sumDownload/n)
	}
	if want := (100.0 + 60.0) / n; math.Abs(server-want) > 1e-9 {
		t.Fatalf("server %v, want %v", server, want)
	}
	if want := (29970.0 + 5000.0) / n; math.Abs(noResponse-want) > 1e-9 {
		t.Fatalf("no response %v, want %v", noResponse, want)
	}
	stack := sumDNS/n + sumConnect/n + sumTLS/n + server + download + noResponse
	if math.Abs(stack-sumDur/n) > 1e-9 {
		t.Fatalf("stack %v != average duration %v", stack, sumDur/n)
	}
	if observed.NoTTFB != 2 {
		t.Fatalf("no-TTFB samples = %d", observed.NoTTFB)
	}
	var unknown *PhaseObservations
	if _, _, _, ok := unknown.ResponseSplit(4, 1, 0, 0, 0, 0); ok {
		t.Fatal("unknown observations must not split")
	}
}

// The engine's own snapshot leaves a timeout's time out of the download tail.
func TestSnapshotWaitCountsOnlyFirstByteSamples(t *testing.T) {
	mc := NewMetricsCollector(10)
	defer mc.Stop()
	now := time.Now()
	snap := mc.computeSnapshot([]*protocol.Result{
		{ActionName: "t", BusinessTransaction: true, Success: true, StartTime: now, Duration: 100 * time.Millisecond, TTFB: 60 * time.Millisecond},
		{ActionName: "t", BusinessTransaction: true, Success: false, StartTime: now, Duration: 900 * time.Millisecond, ErrorMessage: "timeout"},
	}, time.Second)
	if math.Abs(snap.AvgWaitMs-20) > 1e-6 {
		t.Fatalf("avg wait %v, want 20 (40 ms download / 2 samples)", snap.AvgWaitMs)
	}
	if snap.PhaseObserved == nil || snap.PhaseObserved.NoTTFB != 1 || math.Abs(snap.PhaseObserved.NoTTFBRemainderMs-900) > 1e-6 {
		t.Fatalf("phase observed = %+v", snap.PhaseObserved)
	}
}
