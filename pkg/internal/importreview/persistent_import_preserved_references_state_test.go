package importreview

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestValidatePreservedImportCredentialEditsAllowsStateAndDataSourceEdits(t *testing.T) {
	prior := &dsl.Script{
		Name: "State references", Version: "1", Protocol: "http",
		DataSources: []dsl.DataSource{{
			Name: "users", Type: "csv", InlineData: "user,pass\nalice,synthetic-csv-secret\n", FirstRowHeader: true,
		}},
		Actions: []dsl.Action{{
			Name: "auth", Type: "POST", Method: "POST", Target: "https://origin.example.test/auth",
			Body: `{"user":"{{data.users.user}}","pass":"{{data.users.pass}}"}`,
		}},
	}
	MarkCredentialPreservingImportScript(prior)

	candidate := CloneScriptDefinition(prior)
	candidate.DataSources[0].InlineData = "user,pass\nalice,synthetic-csv-secret\nbob,synthetic-csv-secret-2\n"
	candidate.Actions[0].Target = "https://staging.example.test/auth"

	if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
		t.Fatalf("ValidatePreservedImportCredentialEdits returned unexpected error: %v", err)
	}
	saved, err := PreparePersistentLibraryScript(candidate, prior)
	if err != nil {
		t.Fatalf("PreparePersistentLibraryScript returned unexpected error: %v", err)
	}
	if saved.DataSources[0].InlineData != candidate.DataSources[0].InlineData {
		t.Fatalf("saved InlineData = %q, want %q", saved.DataSources[0].InlineData, candidate.DataSources[0].InlineData)
	}
}
