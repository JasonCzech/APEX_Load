package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// A guided repair rebinds only a step's executable fields, so the import
// evidence in its metadata still names the reference it replaced. Replay keeps
// that evidence as recorded and sends the request; every other metadata value
// is still interpolated and still refused while a {{param.*}} in it is
// unresolved.
func TestExecuteKeepsImportEvidenceMetadataVerbatim(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, nil)
	vu.Script.Parameters = []dsl.Parameter{{Name: "existing", Type: "list", Values: []string{"value"}, Update: "once"}}
	vu.Parameters = NewParameterRuntime(vu.Script.Parameters, nil, 0)
	evidence := map[string]any{
		"source_evidence": "{{param.missing}}",
		"original_url":    "https://example.test/{{param.missing_url}}?token={{secret.never_resolved}}",
		"source_path":     map[string]any{"file": "{{param.nested}}"},
	}
	resolved := vu.resolveAction(dsl.Action{Name: "consumer", Type: "POST", Method: "POST", Target: "https://example.test/", Body: "{{param.existing}}", Metadata: evidence})
	if _, err := vu.executeWithRetry(context.Background(), resolved, dsl.ErrorHandlingConfig{}); err != nil {
		t.Fatalf("execute error = %v, want the rebound request sent", err)
	}
	if len(proto.names) != 1 || string(resolved.Body) != "value" {
		t.Fatalf("protocol calls = %v, body = %q", proto.names, resolved.Body)
	}
	if resolved.Metadata["source_evidence"] != "{{param.missing}}" || resolved.Metadata["original_url"] != evidence["original_url"] {
		t.Fatalf("evidence was rewritten: %#v", resolved.Metadata)
	}

	refused := vu.resolveAction(dsl.Action{Name: "annotated", Type: "POST", Method: "POST", Target: "https://example.test/", Body: "{{param.existing}}", Metadata: map[string]any{"source_evidence": "kept", "note": "{{param.missing}}"}})
	if _, err := vu.executeWithRetry(context.Background(), refused, dsl.ErrorHandlingConfig{}); err == nil || !strings.Contains(err.Error(), "unresolved {{param.*}}") {
		t.Fatalf("execute error = %v, want the unresolved metadata parameter refused", err)
	}
	if len(proto.names) != 1 {
		t.Fatalf("protocol calls = %v, want the annotated request refused", proto.names)
	}
}

func TestActionUnresolvedParameterScanSkipsImportEvidenceMetadata(t *testing.T) {
	for _, test := range []struct {
		metadata map[string]any
		want     bool
	}{
		{metadata: map[string]any{"source_evidence": "{{param.x}}", " Original_URL ": "{{param.x}}", "asyncapi_operation": "{{param.x}}"}, want: false},
		{metadata: map[string]any{"source_evidence": "{{param.x}}", "raw_source": "{{param.x}}"}, want: true},
		{metadata: map[string]any{"raw": map[string]any{"source_evidence": "{{param.x}}"}}, want: true},
		{metadata: map[string]any{"{{param.x}}": "evidence"}, want: true},
	} {
		if got := actionContainsUnresolvedParameter(&protocol.Action{Name: "a", Metadata: test.metadata}); got != test.want {
			t.Errorf("metadata %#v: unresolved = %v, want %v", test.metadata, got, test.want)
		}
	}
}
