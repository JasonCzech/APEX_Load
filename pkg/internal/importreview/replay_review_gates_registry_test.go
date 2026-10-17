package importreview

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"testing"
)

// TestDurableReviewGateRegistryIsTheOnlyHardeningSwitch proves the registry is
// actually the extension point: an importer becomes durable by adding an entry,
// and nothing else in the file needs to change.
func TestDurableReviewGateRegistryIsTheOnlyHardeningSwitch(t *testing.T) {
	action := dsl.Action{
		Name:     "request",
		Type:     "GET",
		Metadata: map[string]any{"review_gate": "postman_unsupported_auth"},
	}
	if DurableImportReviewGateAction(action) {
		t.Fatal("an unregistered gate prefix is already durable")
	}

	original := DurableReviewGateSources
	t.Cleanup(func() { DurableReviewGateSources = original })
	DurableReviewGateSources = append(append([]DurableReviewGateSource(nil), original...),
		DurableReviewGateSource{gatePrefix: "postman_"})

	if !DurableImportReviewGateAction(action) {
		t.Fatal("registering a gate prefix did not make it durable")
	}
	// Registering one importer must not harden another's markers.
	trueclient := dsl.Action{Name: "js", Type: "browser_evaluate_js",
		Metadata: map[string]any{"review_gate": "javascript"}}
	if DurableImportReviewGateAction(trueclient) {
		t.Fatal("registering postman hardened TrueClient's gate")
	}
}
