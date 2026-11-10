package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func traceExemplarID(seed string) string {
	return strings.Repeat(seed, 32)[:32]
}

func traceExemplarRequest(vu *VUser, name, seed string, duration time.Duration, sent bool) {
	_ = vu.recordResult(&protocol.Result{
		ActionName: name, Success: true, StatusCode: 200, Duration: duration, TTFB: duration / 2,
		TraceContext: &protocol.SentTraceContext{TraceID: traceExemplarID(seed), SpanID: strings.Repeat(seed, 16)[:16], Flags: "01", Source: "generated", Sent: sent},
	}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
}

// A marker transaction's synthesized result never goes on the wire, so it
// takes the trace context of its slowest child that really sent one: live
// exemplars and the archive's trace_id then point at a real trace.
func TestSourceTransactionTakesSlowestSentChildTrace(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "checkout")
	backdateSourceTransaction(vu, "checkout", time.Second)
	traceExemplarRequest(vu, "fast", "a", 20*time.Millisecond, true)
	traceExemplarRequest(vu, "slow", "b", 90*time.Millisecond, true)
	// Slower, but served from cache: its context never reached a server.
	traceExemplarRequest(vu, "cached", "c", 200*time.Millisecond, false)
	mustSourceTransaction(t, vu, "lr_end_transaction", "checkout", "LR_AUTO")
	if len(sink.txs) != 1 {
		t.Fatalf("expected one transaction, got %d", len(sink.txs))
	}
	got := sink.txs[0].TraceContext.LookupTraceID()
	if got != traceExemplarID("b") {
		t.Fatalf("transaction trace = %q, want the slowest sent child %q", got, traceExemplarID("b"))
	}
}

func TestSourceTransactionWithoutSentChildTraceHasNone(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "report")
	backdateSourceTransaction(vu, "report", time.Second)
	phaseRollupRequest(vu, "no-trace", 40*time.Millisecond)
	traceExemplarRequest(vu, "cached", "c", 50*time.Millisecond, false)
	mustSourceTransaction(t, vu, "lr_end_transaction", "report", "LR_AUTO")
	if tc := sink.txs[0].TraceContext; tc != nil {
		t.Fatalf("transaction got a trace context with no sent child: %+v", tc)
	}
}

// Work after lr_stop_transaction is held aside like the phase rollups: an end
// while stopped ignores it, a resume brings it back.
func TestSourceTransactionTraceRespectsStopAndResume(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "report")
	backdateSourceTransaction(vu, "report", time.Second)
	traceExemplarRequest(vu, "before-stop", "a", 30*time.Millisecond, true)
	mustSourceTransaction(t, vu, "lr_stop_transaction", "report")
	traceExemplarRequest(vu, "after-stop", "b", 300*time.Millisecond, true)
	mustSourceTransaction(t, vu, "lr_end_transaction", "report", "LR_AUTO")
	if got := sink.txs[0].TraceContext.LookupTraceID(); got != traceExemplarID("a") {
		t.Fatalf("stopped transaction trace = %q, want the pre-stop child", got)
	}

	vu, sink = phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "report")
	backdateSourceTransaction(vu, "report", time.Second)
	traceExemplarRequest(vu, "before-stop", "a", 30*time.Millisecond, true)
	mustSourceTransaction(t, vu, "lr_stop_transaction", "report")
	traceExemplarRequest(vu, "while-stopped", "b", 300*time.Millisecond, true)
	mustSourceTransaction(t, vu, "lr_resume_transaction", "report")
	mustSourceTransaction(t, vu, "lr_end_transaction", "report", "LR_AUTO")
	if got := sink.txs[0].TraceContext.LookupTraceID(); got != traceExemplarID("b") {
		t.Fatalf("resumed transaction trace = %q, want the slower held child", got)
	}
}

// Nested transactions each take the slowest child recorded while they were open.
func TestNestedSourceTransactionsEachTakeTheirSlowestChildTrace(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "outer")
	backdateSourceTransaction(vu, "outer", time.Second)
	traceExemplarRequest(vu, "outer-only", "a", 80*time.Millisecond, true)
	mustSourceTransaction(t, vu, "lr_start_transaction", "inner")
	backdateSourceTransaction(vu, "inner", 500*time.Millisecond)
	traceExemplarRequest(vu, "inner-child", "b", 40*time.Millisecond, true)
	mustSourceTransaction(t, vu, "lr_end_transaction", "inner", "LR_AUTO")
	mustSourceTransaction(t, vu, "lr_end_transaction", "outer", "LR_AUTO")
	traces := map[string]string{}
	for _, tx := range sink.txs {
		traces[tx.ActionName] = tx.TraceContext.LookupTraceID()
	}
	if traces["inner"] != traceExemplarID("b") || traces["outer"] != traceExemplarID("a") {
		t.Fatalf("nested traces = %v", traces)
	}
}

const traceExemplarParallelProtocolName = "trace-exemplar-parallel-test"

type traceExemplarParallelProtocol struct{ parallelTestProtocol }

func (*traceExemplarParallelProtocol) Name() string { return traceExemplarParallelProtocolName }
func (*traceExemplarParallelProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	duration, seed := 30*time.Millisecond, "a"
	if action.Name == "slow" {
		duration, seed = 120*time.Millisecond, "d"
	}
	return &protocol.Result{
		ActionName: action.Name, StartTime: time.Now(), Duration: duration, Success: true, TTFB: duration / 2,
		TraceContext: &protocol.SentTraceContext{TraceID: traceExemplarID(seed), SpanID: strings.Repeat(seed, 16), Source: "generated", Sent: true},
	}, nil
}

func init() {
	protocol.Register(traceExemplarParallelProtocolName, func() protocol.Protocol { return &traceExemplarParallelProtocol{} })
}

// Parallel branch requests reach the parent's open transaction, traces included.
func TestSourceTransactionTraceIncludesParallelBranches(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	vu.Group = "parallel"
	vu.Proto = &traceExemplarParallelProtocol{}
	vu.Script = &dsl.Script{Protocol: traceExemplarParallelProtocolName, Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}}}
	mustSourceTransaction(t, vu, "lr_start_transaction", "dashboard")
	backdateSourceTransaction(vu, "dashboard", time.Second)
	control := &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
		{Name: "one", Steps: []dsl.Action{{Name: "fast", Type: "GET", Target: "/one"}}},
		{Name: "two", Steps: []dsl.Action{{Name: "slow", Type: "GET", Target: "/two"}}},
	}}
	if !vu.runParallelBranches(context.Background(), control, "action") {
		t.Fatal("parallel branches failed")
	}
	mustSourceTransaction(t, vu, "lr_end_transaction", "dashboard", "LR_AUTO")
	if got := sink.txs[0].TraceContext.LookupTraceID(); got != traceExemplarID("d") {
		t.Fatalf("transaction trace = %q, want the slow branch request's", got)
	}
}

func traceExemplarFailedRequest(vu *VUser, name, seed string, duration time.Duration) {
	_ = vu.recordResult(&protocol.Result{
		ActionName: name, Success: false, StatusCode: 500, ErrorMessage: "server error", Duration: duration, TTFB: duration / 2,
		TraceContext: &protocol.SentTraceContext{TraceID: traceExemplarID(seed), SpanID: strings.Repeat(seed, 16)[:16], Flags: "01", Source: "generated", Sent: true},
	}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
}

// A failed transaction's trace is its error evidence (first_error exemplar,
// archive error exemplar, error-priority provider lookups): it must open the
// child that failed, not a slower healthy one.
func TestFailedSourceTransactionTakesFailedChildTrace(t *testing.T) {
	for _, tc := range []struct {
		name  string
		close func(t *testing.T, vu *VUser)
	}{
		{"lr_auto after failed child", func(t *testing.T, vu *VUser) {
			vu.noteSourceTransactionResult(dsl.Action{}, false)
			mustSourceTransaction(t, vu, "lr_end_transaction", "checkout", "LR_AUTO")
		}},
		{"lr_fail", func(t *testing.T, vu *VUser) {
			mustSourceTransaction(t, vu, "lr_end_transaction", "checkout", "LR_FAIL")
		}},
		{"failure close", func(t *testing.T, vu *VUser) {
			if vu.finalizeSourceTransaction("checkout", "transaction not ended") == nil {
				t.Fatal("expected a failed transaction result")
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vu, sink := phaseRollupVUser(t)
			mustSourceTransaction(t, vu, "lr_start_transaction", "checkout")
			backdateSourceTransaction(vu, "checkout", time.Second)
			traceExemplarRequest(vu, "cart", "a", 900*time.Millisecond, true)
			traceExemplarFailedRequest(vu, "pay", "b", 40*time.Millisecond)
			traceExemplarFailedRequest(vu, "retry-pay", "c", 60*time.Millisecond)
			tc.close(t, vu)
			if len(sink.txs) != 1 || sink.txs[0].Success {
				t.Fatalf("expected one failed transaction, got %+v", sink.txs)
			}
			if got := sink.txs[0].TraceContext.LookupTraceID(); got != traceExemplarID("b") {
				t.Fatalf("failed transaction trace = %q, want the first failed child %q", got, traceExemplarID("b"))
			}
		})
	}
}

// A 200 whose extractions cannot be retained fails in recordResult itself;
// the exemplar must be offered after that, so the failed transaction opens
// that request's trace (as it would inside a parallel branch), not a slower
// healthy one.
func TestFailedSourceTransactionTakesExtractionRetentionFailureTrace(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "checkout")
	backdateSourceTransaction(vu, "checkout", time.Second)
	traceExemplarRequest(vu, "cart", "a", 900*time.Millisecond, true)
	// The VUser's extraction budget is spent: the next extraction is refused.
	vu.extractedVariableBytes = maxVUserExtractionBytes
	pay := &protocol.Result{
		ActionName: "pay", Success: true, StatusCode: 200, Duration: 40 * time.Millisecond, TTFB: 20 * time.Millisecond,
		Extractions:  map[string]string{"order_id": "42"},
		TraceContext: &protocol.SentTraceContext{TraceID: traceExemplarID("b"), SpanID: strings.Repeat("b", 16), Flags: "01", Source: "generated", Sent: true},
	}
	if err := vu.recordResult(pay, dsl.ErrorHandlingConfig{}, dsl.LogConfig{}); err == nil {
		t.Fatal("expected the extraction retention failure")
	}
	if pay.Success {
		t.Fatal("extraction retention failure left the request successful")
	}
	vu.noteSourceTransactionResult(dsl.Action{}, false)
	mustSourceTransaction(t, vu, "lr_end_transaction", "checkout", "LR_AUTO")
	if len(sink.txs) != 1 || sink.txs[0].Success {
		t.Fatalf("expected one failed transaction, got %+v", sink.txs)
	}
	if got := sink.txs[0].TraceContext.LookupTraceID(); got != traceExemplarID("b") {
		t.Fatalf("failed transaction trace = %q, want the retention-failed child %q", got, traceExemplarID("b"))
	}
}

// A passing transaction (LR_PASS over a failed child) and a failed one whose
// failing children sent no trace both keep the slowest sent child.
func TestSourceTransactionTraceFallsBackToSlowestChild(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "checkout")
	backdateSourceTransaction(vu, "checkout", time.Second)
	traceExemplarRequest(vu, "cart", "a", 900*time.Millisecond, true)
	traceExemplarFailedRequest(vu, "pay", "b", 40*time.Millisecond)
	mustSourceTransaction(t, vu, "lr_end_transaction", "checkout", "LR_PASS")
	if got := sink.txs[0].TraceContext.LookupTraceID(); got != traceExemplarID("a") {
		t.Fatalf("passing transaction trace = %q, want the slowest child", got)
	}

	vu, sink = phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "checkout")
	backdateSourceTransaction(vu, "checkout", time.Second)
	traceExemplarRequest(vu, "cart", "a", 900*time.Millisecond, true)
	_ = vu.recordResult(&protocol.Result{ActionName: "pay", Success: false, StatusCode: 500, Duration: 40 * time.Millisecond}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	mustSourceTransaction(t, vu, "lr_end_transaction", "checkout", "LR_FAIL")
	if got := sink.txs[0].TraceContext.LookupTraceID(); got != traceExemplarID("a") {
		t.Fatalf("failed transaction without a failed traced child = %q, want the slowest child", got)
	}
}

// A failure while stopped joins the transaction only on resume; the earlier
// failure stays first.
func TestFailedSourceTransactionTraceRespectsStopAndResume(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "report")
	backdateSourceTransaction(vu, "report", time.Second)
	traceExemplarRequest(vu, "before-stop", "a", 300*time.Millisecond, true)
	mustSourceTransaction(t, vu, "lr_stop_transaction", "report")
	traceExemplarFailedRequest(vu, "while-stopped", "b", 30*time.Millisecond)
	mustSourceTransaction(t, vu, "lr_end_transaction", "report", "LR_FAIL")
	if got := sink.txs[0].TraceContext.LookupTraceID(); got != traceExemplarID("a") {
		t.Fatalf("stopped transaction trace = %q, want the pre-stop child", got)
	}

	vu, sink = phaseRollupVUser(t)
	mustSourceTransaction(t, vu, "lr_start_transaction", "report")
	backdateSourceTransaction(vu, "report", time.Second)
	traceExemplarRequest(vu, "before-stop", "a", 300*time.Millisecond, true)
	mustSourceTransaction(t, vu, "lr_stop_transaction", "report")
	traceExemplarFailedRequest(vu, "while-stopped", "b", 30*time.Millisecond)
	mustSourceTransaction(t, vu, "lr_resume_transaction", "report")
	traceExemplarFailedRequest(vu, "after-resume", "c", 20*time.Millisecond)
	mustSourceTransaction(t, vu, "lr_end_transaction", "report", "LR_FAIL")
	if got := sink.txs[0].TraceContext.LookupTraceID(); got != traceExemplarID("b") {
		t.Fatalf("resumed transaction trace = %q, want the held failed child", got)
	}
}

const traceExemplarFailingParallelProtocolName = "trace-exemplar-failing-parallel-test"

type traceExemplarFailingParallelProtocol struct{ parallelTestProtocol }

func (*traceExemplarFailingParallelProtocol) Name() string {
	return traceExemplarFailingParallelProtocolName
}
func (*traceExemplarFailingParallelProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	duration, seed, ok := 120*time.Millisecond, "d", true
	if action.Name == "fail" {
		duration, seed, ok = 15*time.Millisecond, "e", false
	}
	return &protocol.Result{
		ActionName: action.Name, StartTime: time.Now(), Duration: duration, Success: ok, TTFB: duration / 2,
		TraceContext: &protocol.SentTraceContext{TraceID: traceExemplarID(seed), SpanID: strings.Repeat(seed, 16), Source: "generated", Sent: true},
	}, nil
}

func init() {
	protocol.Register(traceExemplarFailingParallelProtocolName, func() protocol.Protocol { return &traceExemplarFailingParallelProtocol{} })
}

// A fast failing parallel branch request is the failed parent transaction's
// trace, ahead of a slower healthy branch.
func TestFailedSourceTransactionTraceIncludesFailedParallelBranch(t *testing.T) {
	vu, sink := phaseRollupVUser(t)
	vu.Group = "parallel"
	vu.Proto = &traceExemplarFailingParallelProtocol{}
	vu.Script = &dsl.Script{Protocol: traceExemplarFailingParallelProtocolName, Actions: []dsl.Action{{Name: "unused", Type: "GET", Target: "/"}}}
	mustSourceTransaction(t, vu, "lr_start_transaction", "dashboard")
	backdateSourceTransaction(vu, "dashboard", time.Second)
	control := &dsl.ControlFlow{Concurrency: 2, Branches: []dsl.ParallelBranch{
		{Name: "one", Steps: []dsl.Action{{Name: "slow", Type: "GET", Target: "/one"}}},
		{Name: "two", Steps: []dsl.Action{{Name: "fail", Type: "GET", Target: "/two"}}},
	}}
	_ = vu.runParallelBranches(context.Background(), control, "action")
	mustSourceTransaction(t, vu, "lr_end_transaction", "dashboard", "LR_FAIL")
	if len(sink.txs) != 1 || sink.txs[0].Success {
		t.Fatalf("expected one failed transaction, got %+v", sink.txs)
	}
	if got := sink.txs[0].TraceContext.LookupTraceID(); got != traceExemplarID("e") {
		t.Fatalf("transaction trace = %q, want the failed branch request's", got)
	}
}
