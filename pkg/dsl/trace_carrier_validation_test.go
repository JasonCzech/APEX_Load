package dsl

import (
	"strings"
	"testing"
)

func TestTraceCarrierValidation(t *testing.T) {
	script := &Script{Name: "events", Protocol: "kafka", Config: ScriptConfig{Observability: ObservabilityConfig{TraceCarrier: "b3"}}}
	found := false
	for _, issue := range ValidateScriptIssues(script) {
		found = found || strings.Contains(issue, "config.observability.trace_carrier")
	}
	if !found {
		t.Fatal("an unsupported trace_carrier was accepted")
	}
	script.Config.Observability.TraceCarrier = "w3c"
	for _, issue := range ValidateScriptIssues(script) {
		if strings.Contains(issue, "trace_carrier") {
			t.Fatalf("w3c rejected: %s", issue)
		}
	}
}
