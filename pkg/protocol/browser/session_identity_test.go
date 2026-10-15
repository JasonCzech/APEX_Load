package browser

import (
	"fmt"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// An observer's first step names its session identity; later steps and
// sessions without one do not.
func TestBrowserIdentityEvidenceOnce(t *testing.T) {
	b := &Browser{}
	b.applyConfigOverrides(map[string]any{"browser_identity_index": float64(3), "browser_identity_username": "perf3@acme.test"})
	first := &protocol.Result{}
	b.applyEvidenceOutcome(first)
	if got := fmt.Sprint(first.ProtocolEvidence[BrowserIdentityEvidenceKey]); got != "map[index:3 username:perf3@acme.test]" {
		t.Fatalf("first step evidence = %s", got)
	}
	second := &protocol.Result{}
	b.applyEvidenceOutcome(second)
	if second.ProtocolEvidence != nil {
		t.Fatalf("reported again: %v", second.ProtocolEvidence)
	}
	anonymous := &Browser{}
	anonymous.applyConfigOverrides(map[string]any{})
	none := &protocol.Result{}
	anonymous.applyEvidenceOutcome(none)
	if none.ProtocolEvidence != nil {
		t.Fatalf("a session without an identity reported one: %v", none.ProtocolEvidence)
	}
}
