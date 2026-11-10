package engine

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestPhaseObservationsCountOnlyPhasesThatTookTime(t *testing.T) {
	var observed PhaseObservations
	observed.Observe(5*time.Millisecond, 10*time.Millisecond, 20*time.Millisecond, 50*time.Millisecond, 60*time.Millisecond) // new TLS connection
	observed.Observe(0, 0, 0, 5*time.Millisecond, 6*time.Millisecond)                                                        // reused connection
	observed.ObserveMs(0, 3, 0, 4, 9)                                                                                        // cached DNS, cleartext
	if observed != (PhaseObservations{DNS: 1, Connect: 2, TLS: 1}) {
		t.Fatalf("observed = %+v", observed)
	}
}

func TestPhaseObservationMergeIsUnknownWhenAPartPredatesIt(t *testing.T) {
	var merge PhaseObservationMerge
	merge.Add(&PhaseObservations{DNS: 1, Connect: 2, TLS: 2}, 10)
	merge.Add(&PhaseObservations{Connect: 1}, 5)
	merge.Add(nil, 0) // an empty part never decides
	if got := merge.Result(); got == nil || *got != (PhaseObservations{DNS: 1, Connect: 3, TLS: 2}) {
		t.Fatalf("merged = %+v", got)
	}
	merge.Add(nil, 4) // an older generator with samples
	if got := merge.Result(); got != nil {
		t.Fatalf("expected unknown after a part without observations, got %+v", got)
	}
	var empty PhaseObservationMerge
	if empty.Result() != nil {
		t.Fatal("a merge of nothing should be unknown")
	}
}

func TestMergeActionResultCountsObservedPhases(t *testing.T) {
	metrics := &ActionMetrics{}
	mergeActionResult(metrics, &protocol.Result{Success: true, DNSTime: time.Millisecond, ConnectTime: 2 * time.Millisecond, TLSTime: 3 * time.Millisecond, TTFB: 10 * time.Millisecond})
	mergeActionResult(metrics, &protocol.Result{Success: true, TTFB: 4 * time.Millisecond})
	if metrics.PhaseObserved == nil || *metrics.PhaseObserved != (PhaseObservations{DNS: 1, Connect: 1, TLS: 1}) {
		t.Fatalf("observed = %+v", metrics.PhaseObserved)
	}
}

func TestSourceTransactionRollsUpChildRequestPhases(t *testing.T) {
	sink := &llmTestTxSink{}
	mc := NewMetricsCollector(100)
	defer mc.Stop()
	vu := &VUser{ID: 1, Variables: make(map[string]string), Metrics: mc, ResultSink: sink}
	sourceAction := func(function string, args ...any) dsl.Action {
		return dsl.Action{Name: "checkout", Type: "source_transaction", Params: map[string]any{"function": function, "args": args}}
	}
	if err := vu.handleSourceTransactionAction(sourceAction("lr_start_transaction", "checkout")); err != nil {
		t.Fatalf("start: %v", err)
	}
	vu.sourceTransactions["checkout"].startedAt = time.Now().Add(-300 * time.Millisecond)
	_ = vu.recordResult(&protocol.Result{
		ActionName: "cart", Success: true, StatusCode: 200, Duration: 120 * time.Millisecond,
		DNSTime: 5 * time.Millisecond, ConnectTime: 10 * time.Millisecond, TLSTime: 25 * time.Millisecond, TTFB: 100 * time.Millisecond,
	}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	_ = vu.recordResult(&protocol.Result{
		ActionName: "pay", Success: true, StatusCode: 200, Duration: 80 * time.Millisecond, TTFB: 60 * time.Millisecond, // reused connection
	}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if err := vu.handleSourceTransactionAction(sourceAction("lr_end_transaction", "checkout", "LR_AUTO")); err != nil {
		t.Fatalf("end: %v", err)
	}
	if len(sink.txs) != 1 {
		t.Fatalf("expected one business transaction, got %d", len(sink.txs))
	}
	tx := sink.txs[0]
	if tx.DNSTime != 5*time.Millisecond || tx.ConnectTime != 10*time.Millisecond || tx.TLSTime != 25*time.Millisecond || tx.TTFB != 160*time.Millisecond {
		// The transaction is backdated to 300 ms, so the serial 160 ms of
		// TTFB fits inside it and is not clamped.
		t.Fatalf("rolled-up phases dns=%v connect=%v tls=%v ttfb=%v", tx.DNSTime, tx.ConnectTime, tx.TLSTime, tx.TTFB)
	}
}
