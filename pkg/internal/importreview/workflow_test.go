package importreview

import (
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestSharedImportReviewPreservesExactDraftAcrossPreparationAndBinding(t *testing.T) {
	original := &dsl.Script{Name: "Recorded login", Version: "1", Protocol: "http", ProjectID: "project-a", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "Login", Type: "request", Method: "POST", Target: "/login", Headers: map[string]string{"Authorization": "Bearer recorded-token-9ab2", "Content-Type": "application/json"}, Body: `{ "password" : "recorded-password-2bf8", "customer" : "keep  spacing" }`}}}
	EnsureCredentialPreservingImportMetadata(original, "har")
	original.Metadata.Import.Preserved[ImportDraftAttestationMetadataKey] = "draft-token"
	prepared, err := PreparePersistentImportedLibraryScript(original, nil, "browser_recording_har")
	if err != nil {
		t.Fatal(err)
	}
	if prepared == original || prepared.Actions[0].Body != original.Actions[0].Body || prepared.Actions[0].Headers["Authorization"] != original.Actions[0].Headers["Authorization"] {
		t.Fatal("preparation changed imported bytes or reused the mutable input")
	}
	if ImportDraftAttestationToken(prepared) != "" || ImportDraftAttestationToken(original) != "draft-token" {
		t.Fatal("preparation must strip only the candidate capability")
	}
	if prepared.Metadata.Import.Source != "browser_recording_har" {
		t.Fatal("server profile was not applied")
	}
	projected := MaskScriptSecrets(prepared)
	requirements := DiscoverSecretBindingRequirements(projected, 0)
	var auth *SecretBindingRequirement
	for i := range requirements {
		if strings.Contains(requirements[i].Path, "Authorization") {
			auth = &requirements[i]
		}
	}
	if auth == nil {
		t.Fatalf("missing authorization occurrence: %#v", requirements)
	}
	bound, _, err := ApplyCredentialPreservingSecretBindings(prepared, 0, []SecretBindingSelection{{RequirementID: auth.ID, SecretName: "LOGIN_TOKEN"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bound.Actions[0].Headers["Authorization"], "{{secret.LOGIN_TOKEN}}") {
		t.Fatal("chosen occurrence was not rebound")
	}
	if bound.Actions[0].Body != original.Actions[0].Body {
		t.Fatal("binding changed unrelated imported body bytes")
	}
	if prepared.Actions[0].Headers["Authorization"] != original.Actions[0].Headers["Authorization"] {
		t.Fatal("binding changed caller snapshot")
	}
	if _, _, err := ApplyCredentialPreservingSecretBindings(prepared, 0, []SecretBindingSelection{{RequirementID: "stale", SecretName: "LOGIN_TOKEN"}}); err == nil {
		t.Fatal("stale occurrence accepted")
	}
}

func TestSharedReviewGateTraversesNestedActionsAndReturnsOwnedIssues(t *testing.T) {
	script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Name: "Outer", Type: "transaction", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Name: "Untranslated", Type: "request", Metadata: map[string]any{"execution_blocker": "unsupported_source_behavior"}}}}}}}
	err := ReplayReviewGateError(script)
	if !errors.Is(err, ErrScriptReplayBlocked) {
		t.Fatalf("nested gate missing: %v", err)
	}
	issues := ReplayBlockedIssues(err)
	if len(issues) != 1 || !strings.Contains(issues[0], "Untranslated") {
		t.Fatalf("issues=%v", issues)
	}
	issues[0] = "changed"
	if ReplayBlockedIssues(err)[0] == "changed" {
		t.Fatal("published issues mutated stored error")
	}
}
