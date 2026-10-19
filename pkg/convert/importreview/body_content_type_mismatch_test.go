package importreview

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestFindingsFromScriptRetainsXMLJSONHeaderGuidance(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ code, detail string }{
		{"request_body_xml_json_content_type_mismatch", XMLBodyJSONContentTypeDetail},
		{"credential_json_malformed", "imported behavior has an unresolved execution blocker"},
	} {
		t.Run(test.code, func(t *testing.T) {
			script := &dsl.Script{Actions: []dsl.Action{{Name: "request"}}}
			ApplyToAction(&script.Actions[0], []Finding{{Code: test.code, SourceRef: "Action.c:7", Gate: GateExecution, Severity: diag.Blocker}})
			findings, err := FindingsFromScript(script)
			if err != nil || len(findings) != 1 {
				t.Fatalf("findings = %#v / %v", findings, err)
			}
			if findings[0].Detail != test.detail || findings[0].SourceRef != "Action.c:7" || findings[0].Gate != GateExecution {
				t.Fatalf("durable diagnostic = %#v", findings[0])
			}
		})
	}
}
