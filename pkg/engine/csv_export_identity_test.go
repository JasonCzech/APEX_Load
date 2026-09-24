package engine

import (
	"slices"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestCSVExportIdentityPrecedesNameRedaction(t *testing.T) {
	secret := "platform-credential-value"
	name := "sample-" + secret
	sink := &captureResultSink{}
	vu := &VUser{Script: &dsl.Script{Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{CSVExports: []dsl.CSVExport{{ID: "trace", ActionName: name}}}}}, Metrics: NewMetricsCollector(4), ResultSink: sink, Variables: map[string]string{}, RedactionConfig: WithSecretValueRedaction(nil, []string{secret})}
	for _, tc := range []struct {
		name string
		ids  []string
	}{{name, []string{"trace"}}, {"other", []string{}}} {
		result := &protocol.Result{ActionName: tc.name, Success: true}
		vu.recordResult(result, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
		if sink.result.CSVExportIDs == nil || !slices.Equal(sink.result.CSVExportIDs, tc.ids) {
			t.Fatalf("binding=%v want=%v", sink.result.CSVExportIDs, tc.ids)
		}
		if tc.name == name && sink.result.ActionName == name {
			t.Fatal("test did not exercise redaction")
		}
	}
}
