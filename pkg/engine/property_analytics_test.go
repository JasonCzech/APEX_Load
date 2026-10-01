package engine

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"testing"
	"time"
)

func TestPropertyPromotionIsFiniteAndPreservesMissing(t *testing.T) {
	script := &dsl.Script{Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{PropertyAnalytics: []dsl.PropertyAnalytics{{Name: "operation", Extraction: "operation_type", Values: []string{"checkout", "search"}}}}}}
	for _, tc := range []struct {
		in   map[string]string
		want string
	}{{map[string]string{"operation_type": "checkout"}, "checkout"}, {map[string]string{"operation_type": "unapproved-private-value"}, "other"}, {nil, "missing"}} {
		got := promotedPropertyDimensions(script, tc.in)
		if got["operation"] != tc.want {
			t.Fatalf("%v", got)
		}
	}
	script.Config.Results.PropertyAnalytics[0].Extraction = "password"
	if got := promotedPropertyDimensions(script, map[string]string{"password": "checkout"}); got != nil {
		t.Fatal("sensitive property promoted")
	}
}
func TestPropertyRollupsUseObservations(t *testing.T) {
	mc := NewMetricsCollector(10)
	snap := mc.computeSnapshot([]*protocol.Result{
		{ActionName: "checkout", BusinessTransaction: false, Duration: 10 * time.Millisecond, Success: true, PropertyDimensions: map[string]string{"operation": "checkout"}},
		{ActionName: "checkout", BusinessTransaction: false, Duration: 30 * time.Millisecond, Success: false, PropertyDimensions: map[string]string{"operation": "checkout"}},
	}, time.Second)
	got := snap.ByProperty["operation=checkout"]
	if got == nil || got.Count != 2 || got.Errors != 1 || got.AvgDuration != 20*time.Millisecond {
		t.Fatalf("bad property metrics: %+v", got)
	}
}

func TestPropertyPromotionUsesScrubbedEvidenceAndReplacesAdapterLabels(t *testing.T) {
	sink := &captureResultSink{}
	vu := &VUser{Script: &dsl.Script{Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{PropertyAnalytics: []dsl.PropertyAnalytics{{Name: "operation", Extraction: "operation_type", Values: []string{"checkout", "search"}}}}}}, Metrics: NewMetricsCollector(4), ResultSink: sink, Variables: map[string]string{}, RedactionConfig: WithSecretValueRedaction(nil, []string{"checkout"})}
	for _, tc := range []struct{ extracted, want string }{{"checkout", "other"}, {"search", "search"}} {
		result := &protocol.Result{ActionName: "request", Success: true, Extractions: map[string]string{"operation_type": tc.extracted}, PropertyDimensions: map[string]string{"unapproved": "private-value"}}
		vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
		if len(sink.result.PropertyDimensions) != 1 || sink.result.PropertyDimensions["operation"] != tc.want {
			t.Fatalf("unsafe promoted evidence: %+v", sink.result.PropertyDimensions)
		}
	}
}
