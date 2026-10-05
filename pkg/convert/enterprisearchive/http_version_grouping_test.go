package enterprisearchive

import (
	"encoding/json"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSourceSequenceChildPreservesOptionalHTTPVersion(t *testing.T) {
	for _, version := range []string{"", "auto", "1.1", "2"} {
		action := dsl.Action{Name: "leaf", Type: "GET", Target: "/"}
		if version != "" {
			action.HTTPVersion, _ = dsl.NewHTTPVersionOverride(version)
		}
		// A malformed observation carries the durable gate the wrapper must keep.
		requestimport.ApplyHTTPVersionCapture(&action, requestimport.ReviewHTTPVersionCapture(42, true, nil, false, nil, false, false), requestimport.SourceRef{Format: "har", Pointer: "request.httpVersion"})
		child := sourceSequenceChild(action)
		_, present := child["http_version"]
		if present != (version != "") {
			t.Fatal("sequence wrapper changed scalar presence")
		}
		encoded, _ := json.Marshal(child)
		var reopened dsl.Action
		if err := json.Unmarshal(encoded, &reopened); err != nil {
			t.Fatal(err)
		}
		if reopened.HTTPVersion.Value() != version || reopened.Metadata["execution_blocker"] != "import_review_required" {
			t.Fatal("sequence wrapper lost policy or source gate")
		}
	}
}
