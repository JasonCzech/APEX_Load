package engine

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"go.opentelemetry.io/otel/trace"
)

func TestGroupVUserCorrelation(t *testing.T) {
	unsampled := false
	tests := []struct {
		name  string
		group GroupConfig
		want  vuserCorrelation
	}{
		{name: "no origins", group: GroupConfig{CorrelationCarriers: []string{CorrelationCarrierTraceparent}}},
		{name: "run marker only", group: GroupConfig{RunCorrelationOrigins: []string{"https://a.test"}}, want: vuserCorrelation{sampled: true}},
		{name: "traceparent", group: GroupConfig{RunCorrelationOrigins: []string{"https://a.test"}, CorrelationCarriers: []string{CorrelationCarrierTraceparent}}, want: vuserCorrelation{traceRoots: true, sampled: true}},
		{name: "marker", group: GroupConfig{RunCorrelationOrigins: []string{"https://a.test"}, CorrelationCarriers: []string{CorrelationCarrierTransactionMarker}}, want: vuserCorrelation{scope: true, sampled: true}},
		{
			name:  "script unsampled preference",
			group: GroupConfig{RunCorrelationOrigins: []string{"https://a.test"}, CorrelationCarriers: []string{CorrelationCarrierTraceparent}, Script: &dsl.Script{Config: dsl.ScriptConfig{HTTP: dsl.HTTPConfig{TraceparentSampled: &unsampled}}}},
			want:  vuserCorrelation{traceRoots: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := groupVUserCorrelation(tt.group); got != tt.want {
				t.Fatalf("correlation = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCorrelationContextSharesEnclosingResolution(t *testing.T) {
	vu := &VUser{ID: 4, correlation: vuserCorrelation{scope: true, traceRoots: true, sampled: true}}
	outer, endOuter := vu.correlationContext(context.Background(), &protocol.Action{Name: "sequence"})
	defer endOuter()
	inner, endInner := vu.correlationContext(outer, &protocol.Action{Name: "child"})
	defer endInner()
	if trace.SpanContextFromContext(inner).TraceID() != trace.SpanContextFromContext(outer).TraceID() {
		t.Fatal("a sequence child started its own trace instead of sharing the sequence's")
	}
	if scope, _ := protocol.CorrelationScopeFromContext(inner); scope.Transaction != "sequence" || scope.VUserID != 4 {
		t.Fatalf("child scope = %+v, want the sequence's scope", scope)
	}
	disabled := &VUser{}
	ctx := context.Background()
	if got, _ := disabled.correlationContext(ctx, &protocol.Action{Name: "x"}); got != ctx {
		t.Fatal("a run without carriers changed the action context")
	}
}

type tracePathRecorder struct {
	mu   sync.Mutex
	seen []struct{ path, traceparent, runMarker string }
}

func (r *tracePathRecorder) handler(w stdhttp.ResponseWriter, req *stdhttp.Request) {
	r.mu.Lock()
	r.seen = append(r.seen, struct{ path, traceparent, runMarker string }{req.URL.Path, req.Header.Get("Traceparent"), req.Header.Get("X-Apex-Test-Run")})
	r.mu.Unlock()
	w.WriteHeader(stdhttp.StatusOK)
}

func TestTraceContextGivesEachTransactionInstanceItsOwnTrace(t *testing.T) {
	recorder := &tracePathRecorder{}
	server := httptest.NewServer(stdhttp.HandlerFunc(recorder.handler))
	defer server.Close()
	disabled := false
	script := &dsl.Script{
		Name:     "checkout_flow",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL:  server.URL,
			RunLogic: dsl.RunLogicConfig{Iterations: 2},
			// Request-file imports persist traceparent: false; the run opt-in wins.
			HTTP: dsl.HTTPConfig{Traceparent: &disabled},
		},
		Actions: []dsl.Action{
			{Name: "Checkout", Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction"}},
			{Name: "cart", Type: "GET", Method: "GET", Target: "/cart"},
			{Name: "pay", Type: "GET", Method: "GET", Target: "/pay"},
			{Name: "Checkout", Type: "source_transaction", Params: map[string]any{"function": "lr_end_transaction"}},
			{Name: "home", Type: "GET", Method: "GET", Target: "/home"},
		},
	}
	mge, err := NewMultiGroup(MultiGroupConfig{
		Groups: []GroupConfig{{
			Name:                  "checkout",
			Script:                script,
			VUsers:                1,
			RunID:                 "run-trace",
			RunCorrelationOrigins: []string{server.URL},
			CorrelationCarriers:   []string{CorrelationCarrierTraceparent},
		}},
		Duration: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("NewMultiGroup: %v", err)
	}
	if _, err := mge.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	recorder.mu.Lock()
	seen := append(recorder.seen[:0:0], recorder.seen...)
	recorder.mu.Unlock()
	if len(seen) != 6 {
		t.Fatalf("requests = %+v, want three per iteration", seen)
	}
	traceOf := func(i int) string {
		traceID, _, _, ok := protocol.ParseTraceparent(seen[i].traceparent)
		if !ok {
			t.Fatalf("request %s carried traceparent %q", seen[i].path, seen[i].traceparent)
		}
		if seen[i].runMarker != "" {
			t.Fatalf("request %s carried the run marker without the run_marker carrier", seen[i].path)
		}
		return traceID
	}
	firstCart, firstPay, firstHome := traceOf(0), traceOf(1), traceOf(2)
	secondCart, secondPay := traceOf(3), traceOf(4)
	if firstCart != firstPay || secondCart != secondPay {
		t.Fatal("requests inside one transaction instance did not share its trace")
	}
	if firstCart == secondCart {
		t.Fatal("two transaction instances shared one trace")
	}
	if firstHome == firstCart {
		t.Fatal("an action outside the transaction reused the transaction's trace")
	}
	if _, cartSpan, _, _ := protocol.ParseTraceparent(seen[0].traceparent); cartSpan == "" {
		t.Fatal("missing span id")
	} else if _, paySpan, _, _ := protocol.ParseTraceparent(seen[1].traceparent); paySpan == cartSpan {
		t.Fatal("two requests in one transaction claimed the same parent span")
	}
}
