package storage

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestImportedScriptIdentitiesAndReceiptSavedAt(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := brunoNativeImportCommitFixture("native-session-identity", "request-sha-identity")
	commit.Scripts[0].Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
		Source:    "bruno",
		Preserved: map[string]any{"native_importer": "v1", "candidate_id": "candidate-1"},
	}}
	commit.Scripts[1].Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "har"}}
	result, err := store.CommitBrunoNativeImport(commit)
	if err != nil {
		t.Fatalf("commit: %v", err)
	}

	identities, err := store.ListImportedScriptIdentities("project-bruno", "bruno")
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 || identities[0].ID != "native-script-1" || identities[0].Import == nil ||
		identities[0].Import.Preserved["candidate_id"] != "candidate-1" || identities[0].ArchivedAt != nil {
		t.Fatalf("identities = %#v", identities)
	}

	saved, err := store.ImportBatchSnapshots("project-bruno", []string{"native-script-1", "native-script-2", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	if !saved["native-script-1"].UpdatedAt.Equal(result.Scripts[0].UpdatedAt) || !saved["native-script-2"].UpdatedAt.Equal(result.Scripts[1].UpdatedAt) {
		t.Fatalf("saved-at = %#v, committed = %v / %v", saved, result.Scripts[0].UpdatedAt, result.Scripts[1].UpdatedAt)
	}
	if script := saved["native-script-1"].Script; script == nil || script.ID != "native-script-1" {
		t.Fatalf("snapshot script = %#v, want the script the import saved", script)
	}
	if _, found := saved["missing"]; found {
		t.Fatalf("saved-at reported a script no receipt holds: %#v", saved)
	}

	// An edit after the import moves updated_at past the receipt's value.
	edited, err := store.GetScript("native-script-1")
	if err != nil {
		t.Fatal(err)
	}
	edited.Name = "Edited after import"
	edited.UpdatedAt = time.Now().UTC().Add(time.Second)
	if err := store.SaveScript(edited); err != nil {
		t.Fatal(err)
	}
	identities, err = store.ListImportedScriptIdentities("project-bruno", "bruno")
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 || identities[0].UpdatedAt.Equal(saved["native-script-1"].UpdatedAt) {
		t.Fatalf("edited identity = %#v, receipt saved-at = %v", identities, saved["native-script-1"].UpdatedAt)
	}
}
