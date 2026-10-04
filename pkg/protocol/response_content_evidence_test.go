package protocol

import "testing"

func TestResponseContentEvidenceWrapperCopiesOnlyClosedEnums(t *testing.T) {
	for _, tc := range []struct {
		state, basis string
		accepted     bool
	}{{"complete", "decoded", true}, {"collection_window", "identity", true}, {"unavailable", "decoded", true}, {"private-state", "private-basis", false}} {
		result := &Result{DependencyType: "soap", ProtocolEvidence: map[string]any{"response_content_state": tc.state, "response_content_basis": tc.basis, "private_extra": "private-payload", "response_content_source": map[bool]string{true: "cache", false: "private-source"}[tc.accepted]}}
		evidence := BuildProtocolEvidence(&Action{SOAP: &SOAPActionConfig{Operation: "Read"}}, result)
		if _, ok := evidence["response_content_state"]; ok != tc.accepted {
			t.Fatal("wrapper copied invalid state or lost a closed receipt")
		}
		if _, ok := evidence["response_content_basis"]; ok != (tc.accepted && tc.state != "unavailable") {
			t.Fatal("wrapper copied invalid basis or lost a closed receipt")
		}
		if _, ok := evidence["response_content_source"]; ok != tc.accepted {
			t.Fatal("wrapper copied invalid source or lost fixed content provenance")
		}
		if evidence["private_extra"] != nil {
			t.Fatal("wrapper widened native evidence projection")
		}
	}
}
