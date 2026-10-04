package salesforce

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestCometdNativePreservesLeafHTTPVersionAndSourceGate(t *testing.T) {
	for _, version := range []string{"auto", "1.1", "2"} {
		leaf := dsl.Action{Name: "connect", Type: "POST", Target: "/cometd/60.0", Body: `[{"id":"1","channel":"/meta/connect","clientId":"synthetic"}]`, Metadata: map[string]any{"salesforce": map[string]any{"classification": "cometd"}}}
		leaf.HTTPVersion, _ = dsl.NewHTTPVersionOverride(version)
		folded := collapseCometdConnectRun([]dsl.Action{leaf, leaf})
		if len(folded) != 1 || folded[0].Control.Steps[0].HTTPVersion.Value() != version {
			t.Fatal("native long-poll loop lost explicit leaf policy")
		}
		other := leaf
		if version == "2" {
			other.HTTPVersion, _ = dsl.NewHTTPVersionOverride("1.1")
		} else {
			other.HTTPVersion, _ = dsl.NewHTTPVersionOverride("2")
		}
		if len(collapseCometdConnectRun([]dsl.Action{leaf, other})) != 2 {
			t.Fatal("native folding erased a transport policy boundary")
		}
		requestimport.ApplyHTTPVersionCapture(&leaf, requestimport.ReviewHTTPVersionCapture(nil, false, nil, false, nil, false, false), requestimport.SourceRef{Format: "har", Pointer: "request.httpVersion"})
		cloned, err := cloneScript(&dsl.Script{Actions: []dsl.Action{leaf}})
		if err != nil || cloned.Actions[0].HTTPVersion.Value() != version || cloned.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
			t.Fatal("native clone lost policy or unresolved source gate")
		}
	}
}
