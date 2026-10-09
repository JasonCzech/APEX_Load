package engine

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type stopInterruptionSink struct {
	results      []*protocol.Result
	transactions []*protocol.Result
}

func (s *stopInterruptionSink) RecordResult(result *protocol.Result) error {
	s.results = append(s.results, result)
	return nil
}

func (s *stopInterruptionSink) RecordTransaction(result *protocol.Result) error {
	s.transactions = append(s.transactions, result)
	return nil
}

func TestWorkCutOffByAStopIsNotRecordedAsErrors(t *testing.T) {
	hard, stop := context.WithCancel(context.Background())
	sink := &stopInterruptionSink{}
	vu := &VUser{Script: &dsl.Script{}, Metrics: NewMetricsCollector(4), ResultSink: sink, Variables: map[string]string{}, HardStopCtx: hard}

	// Before the stop every failure counts.
	vu.recordResult(&protocol.Result{ActionName: "GET /cart", ErrorMessage: `Get "http://shop/cart": context canceled`}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	vu.recordTransaction("Add_To_Cart", 0, false, nil)
	if len(sink.results) != 1 || len(sink.transactions) != 1 {
		t.Fatalf("before stop: results=%d transactions=%d, want 1 and 1", len(sink.results), len(sink.transactions))
	}

	stop()
	// The request the stop cancelled, and the transaction it left incomplete, are left out.
	vu.recordResult(&protocol.Result{ActionName: "GET /cart", ErrorMessage: `Get "http://shop/cart": context canceled`}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if tx := vu.recordTransaction("Add_To_Cart", 0, false, &protocol.Result{ErrorMessage: "context canceled"}); tx != nil {
		t.Fatal("transaction cut off by the stop was recorded")
	}
	vu.recordTransaction("script_iteration", 0, false, nil)
	if len(sink.results) != 1 || len(sink.transactions) != 1 {
		t.Fatalf("after stop: results=%d transactions=%d, want the stop's cancellations left out", len(sink.results), len(sink.transactions))
	}

	// A genuine failure that lands during the stop still counts, and so does success.
	vu.recordResult(&protocol.Result{ActionName: "GET /checkout", StatusCode: 503, ErrorMessage: "expected status 200, got 503"}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	vu.recordTransaction("Checkout", 0, false, &protocol.Result{ErrorMessage: "expected status 200, got 503"})
	vu.recordResult(&protocol.Result{ActionName: "GET /", Success: true}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if len(sink.results) != 3 || len(sink.transactions) != 2 {
		t.Fatalf("genuine results during stop: results=%d transactions=%d, want 3 and 2", len(sink.results), len(sink.transactions))
	}
}

func TestReplayKeepsFailuresAfterAStop(t *testing.T) {
	hard, stop := context.WithCancel(context.Background())
	stop()
	sink := &stopInterruptionSink{}
	vu := &VUser{Script: &dsl.Script{}, Metrics: NewMetricsCollector(4), ResultSink: sink, Variables: map[string]string{}, HardStopCtx: hard, replayRuntime: &replayRuntimeState{rows: map[string]ReplayRuntimeRow{}}}
	vu.recordResult(&protocol.Result{ActionName: "GET /cart", ErrorMessage: "context canceled"}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if len(sink.results) != 1 {
		t.Fatalf("replay results=%d, want the debugger's stop kept", len(sink.results))
	}
}

func TestGracefulStopKeepsFailuresOfTheActionThatFinishes(t *testing.T) {
	hard, abort := context.WithCancel(context.Background())
	defer abort()
	boundary, gracefulStop := context.WithCancel(context.Background())
	gracefulStop()
	sink := &stopInterruptionSink{}
	vu := &VUser{Script: &dsl.Script{}, Metrics: NewMetricsCollector(4), ResultSink: sink, Variables: map[string]string{}, HardStopCtx: hard, ActionBoundaryCtx: boundary}

	// The current action runs to completion under HardStopCtx, so its failures are real.
	vu.recordTransaction("Checkout", 0, false, nil)
	vu.recordResult(&protocol.Result{ActionName: "GET /slow", ErrorMessage: `Get "http://shop/slow": context deadline exceeded (Client.Timeout exceeded while awaiting headers)`}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	vu.recordResult(&protocol.Result{ActionName: "GET /slow", ErrorMessage: "context deadline exceeded"}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if len(sink.transactions) != 1 || len(sink.results) != 2 {
		t.Fatalf("graceful stop: results=%d transactions=%d, want the finished action's failures kept", len(sink.results), len(sink.transactions))
	}
	// A cancellation is still the stop cutting a request off.
	vu.recordResult(&protocol.Result{ActionName: "GET /cart", ErrorMessage: "context canceled"}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if len(sink.results) != 2 {
		t.Fatalf("graceful stop cancellation recorded: results=%d", len(sink.results))
	}
}

func TestHardStopStillCountsTimeouts(t *testing.T) {
	hard, stop := context.WithCancel(context.Background())
	stop()
	sink := &stopInterruptionSink{}
	vu := &VUser{Script: &dsl.Script{}, Metrics: NewMetricsCollector(4), ResultSink: sink, Variables: map[string]string{}, HardStopCtx: hard}
	vu.recordResult(&protocol.Result{ActionName: "GET /slow", ErrorMessage: "net/http: request canceled (Client.Timeout exceeded while awaiting headers)"}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	vu.recordResult(&protocol.Result{ActionName: "GET /cart", ErrorMessage: "context deadline exceeded"}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if len(sink.results) != 1 || sink.results[0].ActionName != "GET /slow" {
		t.Fatalf("hard stop results=%v, want only the timeout kept", sink.results)
	}
}
