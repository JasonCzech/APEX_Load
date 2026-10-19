package requestimport

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
)

func TestReviewCredentialProjectionExplainsXMLJSONHeaderMismatch(t *testing.T) {
	t.Parallel()
	_, result, err := credentialpolicy.ProjectRequestBody([]byte("<request/>"), "application/json", credentialpolicy.Options{
		Mode:   credentialpolicy.Preserve,
		Source: credentialpolicy.SourceRef{Format: "enterprise_script_archive", Pointer: "Action.c:7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	review := ReviewCredentialProjection(result, "imported HTTP request")
	for _, finding := range review.Findings {
		if finding.Code == "request_body_xml_json_content_type_mismatch" {
			if finding.Detail != importreview.XMLBodyJSONContentTypeDetail || finding.SourceRef != "Action.c:7" || finding.Gate != importreview.GateExecution || finding.Severity != diag.Blocker {
				t.Fatalf("mismatch guidance = %#v", finding)
			}
			return
		}
	}
	t.Fatal("missing Content-Type mismatch finding")
}
