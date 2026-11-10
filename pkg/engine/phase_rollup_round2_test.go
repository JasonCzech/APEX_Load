package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// Six overlapping requests that each open a TLS connection (100/100/100 ms
// setup, 900 ms TTFB) in a 1000 ms transaction: the serial sums scale by one
// factor, so setup stays about a third of TTFB and TLS and server time stay
// visible. Clamping in phase order gave DNS 600 + connect 400, TLS 0, server 0.
func TestSourceTransactionPhaseRollupBoundedToScalesOverlap(t *testing.T) {
	r := &sourceTransactionPhaseRollup{}
	for range 6 {
		r.add(100*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond, 900*time.Millisecond)
	}
	duration := time.Second
	dns, connect, tls, ttfb := r.boundedTo(duration)
	if ttfb != duration {
		t.Fatalf("ttfb = %v, want %v", ttfb, duration)
	}
	setup := dns + connect + tls
	if setup > ttfb {
		t.Fatalf("setup %v exceeds ttfb %v", setup, ttfb)
	}
	want := 111 * time.Millisecond
	for name, got := range map[string]time.Duration{"dns": dns, "connect": connect, "tls": tls} {
		if got < want || got > want+time.Millisecond {
			t.Fatalf("%s = %v, want about %v (its share of the scaled TTFB)", name, got, want)
		}
	}
	if server := ttfb - setup; server < 660*time.Millisecond || server > 670*time.Millisecond {
		t.Fatalf("server = %v, want about 667ms", server)
	}
}

// Setup that exceeds a bounded TTFB (requests that set up but got no first
// byte) scales into it as a whole, and the invariant setup <= TTFB <= duration
// holds.
func TestSourceTransactionPhaseRollupBoundedToSetupOverTTFB(t *testing.T) {
	r := &sourceTransactionPhaseRollup{dns: 300 * time.Millisecond, connect: 300 * time.Millisecond, tls: 300 * time.Millisecond, ttfb: 600 * time.Millisecond}
	dns, connect, tls, ttfb := r.boundedTo(time.Second)
	if ttfb != 600*time.Millisecond {
		t.Fatalf("ttfb = %v", ttfb)
	}
	if dns+connect+tls > ttfb || tls < 199*time.Millisecond || dns < 199*time.Millisecond {
		t.Fatalf("setup %v/%v/%v not scaled into ttfb %v", dns, connect, tls, ttfb)
	}
}

// A transaction left open and closed as failed (finalizeSourceTransaction,
// e.g. abort on error) keeps its child phase rollup instead of reporting a
// zero-TTFB, "no first byte" sample.
func TestFinalizeSourceTransactionKeepsPhaseRollup(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "checkout")
	backdateSourceTransaction(vu, "checkout", time.Second)
	phaseRollupRequest(vu, "cart", 50*time.Millisecond)
	phaseRollupRequest(vu, "pay", 70*time.Millisecond)
	if vu.finalizeSourceTransaction("checkout", "transaction not ended") == nil {
		t.Fatal("expected a failed transaction result")
	}
	if len(sink.txs) != 1 {
		t.Fatalf("expected one transaction, got %d", len(sink.txs))
	}
	tx := sink.txs[0]
	if tx.Success {
		t.Fatal("finalized transaction should be failed")
	}
	if tx.TTFB != 120*time.Millisecond || tx.ConnectTime != 10*time.Millisecond {
		t.Fatalf("finalized transaction lost its rollup: ttfb=%v connect=%v", tx.TTFB, tx.ConnectTime)
	}
}

// A resumed transaction's duration includes the stopped interval (LoadRunner
// reports start to end), so requests made while it was stopped join its
// rollup on resume.
func TestSourceTransactionPhaseRollupResumeKeepsStoppedRequests(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "report")
	backdateSourceTransaction(vu, "report", time.Second)
	phaseRollupRequest(vu, "first", 60*time.Millisecond)
	mustSourceTransaction(t, vu, "lr_stop_transaction", "report")
	phaseRollupRequest(vu, "while-stopped", 100*time.Millisecond)
	mustSourceTransaction(t, vu, "lr_resume_transaction", "report")
	phaseRollupRequest(vu, "after-resume", 40*time.Millisecond)
	mustSourceTransaction(t, vu, "lr_end_transaction", "report", "LR_AUTO")
	if tx := sink.txs[0]; tx.TTFB != 200*time.Millisecond || tx.ConnectTime != 15*time.Millisecond {
		t.Fatalf("resumed transaction rollup: ttfb=%v connect=%v, want 200ms/15ms", tx.TTFB, tx.ConnectTime)
	}
}

// A stop then resume carries LLM hops made while stopped into the rollup too.
func TestSourceTransactionLLMRollupResumeKeepsStoppedHops(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "chat")
	mustSourceTransaction(t, vu, "lr_stop_transaction", "chat")
	_ = vu.recordResult(&protocol.Result{
		ActionName: "hop", Success: true, Duration: 10 * time.Millisecond,
		ServerTelemetry: &protocol.ServerTelemetry{LLM: &protocol.LLMDiagnostics{PromptTokens: 7, CompletionTokens: 3}},
	}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	mustSourceTransaction(t, vu, "lr_resume_transaction", "chat")
	mustSourceTransaction(t, vu, "lr_end_transaction", "chat", "LR_AUTO")
	tx := sink.txs[0]
	if tx.ServerTelemetry == nil || tx.ServerTelemetry.LLM == nil || tx.ServerTelemetry.LLM.PromptTokens != 7 {
		t.Fatalf("resumed transaction lost the stopped LLM hop: %+v", tx.ServerTelemetry)
	}
}

// A parallel block run from an async callback is async-conversation work: its
// branch requests stay out of the enclosing transaction, like the callback's
// own requests.
func TestSourceTransactionPhaseRollupSkipsParallelBranchesInAsyncDispatch(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	vu.Group = "parallel"
	vu.Proto = &phaseRollupParallelProtocol{}
	vu.Script = &dsl.Script{Protocol: phaseRollupParallelProtocolName, Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}}}
	mustSourceTransaction(t, vu, "lr_start_transaction", "dashboard")
	backdateSourceTransaction(vu, "dashboard", time.Second)
	control := &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
		{Name: "one", Steps: []dsl.Action{{Name: "one", Type: "GET", Target: "/one"}}},
		{Name: "two", Steps: []dsl.Action{{Name: "two", Type: "GET", Target: "/two"}}},
	}}
	vu.inAsyncDispatch = true
	ok := vu.runParallelBranches(context.Background(), control, "action")
	vu.inAsyncDispatch = false
	if !ok {
		t.Fatal("parallel branches failed")
	}
	mustSourceTransaction(t, vu, "lr_end_transaction", "dashboard", "LR_AUTO")
	if tx := sink.txs[0]; tx.TTFB != 0 || tx.ConnectTime != 0 {
		t.Fatalf("async-callback parallel branches rolled into the transaction: ttfb=%v connect=%v", tx.TTFB, tx.ConnectTime)
	}
}
