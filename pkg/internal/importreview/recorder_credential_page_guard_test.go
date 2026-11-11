package importreview

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestRecorderPageFlowEditsAreAllowedWithLiteralCredentials(t *testing.T) {
	prior := recorderCredentialScript()
	prior.Actions = append([]dsl.Action{
		{Name: "Open login", Type: "browser_navigate", Target: "https://app.example.test/login"},
	}, prior.Actions...)
	EnsureScriptImportActionUIDs(prior)

	for _, tc := range []struct {
		name string
		edit func(*dsl.Script)
	}{
		{
			name: "navigate retargeted",
			edit: func(s *dsl.Script) {
				s.Actions[0].Target = "https://staging.example.test/login"
			},
		},
		{
			name: "navigate deleted",
			edit: func(s *dsl.Script) {
				s.Actions = s.Actions[1:]
			},
		},
		{
			name: "page script inserted before fill",
			edit: func(s *dsl.Script) {
				s.Actions = append([]dsl.Action{
					s.Actions[0],
					{Name: "Prepare state", Type: "browser_evaluate_js", Target: "window.__testReady = true"},
				}, s.Actions[1:]...)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			tc.edit(candidate)
			if err := ValidatePreservedImportCredentialEditsWithOptions(candidate, prior, PreservedImportEditOptions{}); err != nil {
				t.Fatalf("ValidatePreservedImportCredentialEditsWithOptions returned unexpected error: %v", err)
			}
			saved, err := PreparePersistentLibraryScript(candidate, prior)
			if err != nil {
				t.Fatalf("PreparePersistentLibraryScript returned unexpected error: %v", err)
			}
			code := saved.Actions[recorderActionIndex(t, saved.Actions, "Fill code")]
			if code.Value != recorderKeptValue {
				t.Fatalf("saved Fill code value = %q, want %q", code.Value, recorderKeptValue)
			}
		})
	}
}
