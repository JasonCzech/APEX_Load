package engine

import (
	"fmt"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func tracedResult(n int, durationMs int, success bool, sent bool) *protocol.Result {
	traceparent := fmt.Sprintf("00-%032x-%016x-01", n, n)
	return &protocol.Result{
		Duration: time.Duration(durationMs) * time.Millisecond, Success: success, StartTime: time.Unix(int64(n), 0),
		TraceContext: protocol.NewSentTraceContext(traceparent, protocol.TraceSourceGenerated, sent),
	}
}

func TestRecordTraceExemplarKeepsSlowestFirstErrorAndASample(t *testing.T) {
	metrics := &ActionMetrics{}
	for i, d := range []int{100, 900, 300} {
		recordTraceExemplar(metrics, tracedResult(i+1, d, true, true))
	}
	recordTraceExemplar(metrics, tracedResult(10, 50, false, true))
	recordTraceExemplar(metrics, tracedResult(11, 60, false, true))
	// A cache hit sent nothing and an untraced request has no context: neither qualifies.
	recordTraceExemplar(metrics, tracedResult(20, 5000, true, false))
	recordTraceExemplar(metrics, &protocol.Result{Duration: 6 * time.Second, Success: true})
	byReason := map[string]TraceExemplar{}
	for _, e := range metrics.Exemplars {
		byReason[e.Reason] = e
	}
	if len(metrics.Exemplars) != MaxTraceExemplars || byReason[ExemplarSlowestSuccess].TraceID != fmt.Sprintf("%032x", 2) || byReason[ExemplarSlowestSuccess].DurationMs != 900 {
		t.Fatalf("exemplars = %+v", metrics.Exemplars)
	}
	if byReason[ExemplarFirstError].TraceID != fmt.Sprintf("%032x", 10) || byReason[ExemplarFirstError].Success {
		t.Fatalf("first error = %+v", byReason[ExemplarFirstError])
	}
	sample := byReason[ExemplarSample].TraceID
	if sample == fmt.Sprintf("%032x", 20) || sample == "" || metrics.exemplarCandidates != 5 {
		t.Fatalf("sample %q drawn from %d candidates", sample, metrics.exemplarCandidates)
	}
}

func TestMergeTraceExemplarsAcrossSources(t *testing.T) {
	early, late := time.Unix(10, 0), time.Unix(20, 0)
	merged := MergeTraceExemplars(
		[]TraceExemplar{{TraceID: "a", Reason: ExemplarSlowestSuccess, DurationMs: 100}, {TraceID: "b", Reason: ExemplarFirstError, Timestamp: late}},
		[]TraceExemplar{{TraceID: "c", Reason: ExemplarSlowestSuccess, DurationMs: 400}, {TraceID: "d", Reason: ExemplarFirstError, Timestamp: early}, {TraceID: "e", Reason: ExemplarSample}},
	)
	if len(merged) != 3 || merged[0].TraceID != "c" || merged[1].TraceID != "d" || merged[2].TraceID != "e" {
		t.Fatalf("merged = %+v", merged)
	}
}
