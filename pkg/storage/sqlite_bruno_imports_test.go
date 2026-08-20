package storage

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestCommitBrunoImportPersistsAtomicRevisionAndRejectsStaleWriter(t *testing.T) {
	store := newBrunoImportTestStore(t)
	first := brunoImportCommitFixture("script-bruno", "project-bruno", "revision-1", "artifact-raw-1", "artifact-sanitized-1", "sha-raw-1", "sha-sanitized-1")
	result, err := store.CommitBrunoImport(first)
	if err != nil {
		t.Fatalf("CommitBrunoImport(first): %v", err)
	}
	if !result.Created || result.Revision == nil || !result.Revision.Active {
		t.Fatalf("first commit result = %+v, want created active revision", result)
	}
	if got := result.Script.Config.Bruno.BundleRef; got != "artifact://artifact-sanitized-1" {
		t.Fatalf("bundle ref = %q", got)
	}

	active, err := store.GetActiveImportSourceRevision("script-bruno", "project-bruno")
	if err != nil {
		t.Fatalf("GetActiveImportSourceRevision: %v", err)
	}
	if active.ID != "revision-1" || active.CanonicalScript == nil {
		t.Fatalf("active revision = %+v", active)
	}
	if _, err := store.GetImportSourceArtifact(active.RawArtifactID, "another-project"); !errors.Is(err, ErrBrunoRevisionNotFound) && err == nil {
		t.Fatal("cross-project artifact lookup unexpectedly succeeded")
	}

	loaded, err := store.GetScript("script-bruno")
	if err != nil {
		t.Fatal(err)
	}
	staleExpected := loaded.UpdatedAt
	second := brunoImportCommitFixture("script-bruno", "project-bruno", "revision-2", "artifact-raw-2", "artifact-sanitized-2", "sha-raw-2", "sha-sanitized-2")
	second.ExpectedScriptUpdatedAt = &loaded.UpdatedAt
	second.ExpectedActiveRevisionID = "revision-1"
	second.Script.CreatedAt = loaded.CreatedAt
	second.Script.Config.Pacing = dsl.PacingConfig{Type: "fixed_delay", DelayMs: 250}
	second.Script.Config.Bruno.SecretBindings = map[string]string{"PASSWORD": "project/password"}
	if _, err := store.CommitBrunoImport(second); err != nil {
		t.Fatalf("CommitBrunoImport(second): %v", err)
	}

	stale := brunoImportCommitFixture("script-bruno", "project-bruno", "revision-stale", "artifact-raw-stale", "artifact-sanitized-stale", "sha-raw-stale", "sha-sanitized-stale")
	stale.ExpectedScriptUpdatedAt = &staleExpected
	stale.ExpectedActiveRevisionID = "revision-1"
	if _, err := store.CommitBrunoImport(stale); !errors.Is(err, ErrBrunoImportConcurrentChange) {
		t.Fatalf("stale commit error = %v, want ErrBrunoImportConcurrentChange", err)
	}
	revisions, err := store.ListImportSourceRevisions("script-bruno", "project-bruno")
	if err != nil {
		t.Fatal(err)
	}
	if len(revisions) != 2 || revisions[0].ID != "revision-2" || !revisions[0].Active || revisions[1].Active {
		t.Fatalf("revisions = %+v", revisions)
	}
}

func TestCommitBrunoImportCanExecuteProtectedRawArtifact(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := brunoImportCommitFixture(
		"script-bruno-raw-runtime",
		"project-bruno",
		"revision-raw-runtime",
		"artifact-raw-runtime",
		"artifact-sanitized-runtime",
		"sha-raw-runtime",
		"sha-sanitized-runtime",
	)
	commit.UseRawArtifact = true

	result, err := store.CommitBrunoImport(commit)
	if err != nil {
		t.Fatalf("CommitBrunoImport: %v", err)
	}
	if got := result.Script.Config.Bruno.BundleRef; got != "artifact://artifact-raw-runtime" {
		t.Fatalf("runtime bundle ref = %q", got)
	}
	if got := result.Script.Config.Bruno.BundleSHA256; got != "sha-raw-runtime" {
		t.Fatalf("runtime bundle digest = %q", got)
	}
	if result.Revision.RawArtifactID != "artifact-raw-runtime" ||
		result.Revision.SanitizedArtifactID != "artifact-sanitized-runtime" ||
		!result.Revision.SanitizedAvailable {
		t.Fatalf("revision artifacts = %#v", result.Revision)
	}
	policy, _ := result.Script.Metadata.Import.Preserved[dsl.ImportPreservedRuntimeArtifactKey].(string)
	if policy != "raw" {
		t.Fatalf("runtime artifact marker = %q", policy)
	}
}

func TestActivateImportSourceRevisionRollsBackBindingAndPreservesTuning(t *testing.T) {
	store := newBrunoImportTestStore(t)
	first := brunoImportCommitFixture("script-rollback", "project-bruno", "revision-1", "artifact-raw-1", "artifact-sanitized-1", "sha-raw-1", "sha-sanitized-1")
	if _, err := store.CommitBrunoImport(first); err != nil {
		t.Fatal(err)
	}
	current, err := store.GetScript("script-rollback")
	if err != nil {
		t.Fatal(err)
	}
	second := brunoImportCommitFixture("script-rollback", "project-bruno", "revision-2", "artifact-raw-2", "artifact-sanitized-2", "sha-raw-2", "sha-sanitized-2")
	second.ExpectedScriptUpdatedAt = &current.UpdatedAt
	second.ExpectedActiveRevisionID = "revision-1"
	second.Script.CreatedAt = current.CreatedAt
	second.Script.Config.Pacing = dsl.PacingConfig{Type: "fixed_delay", DelayMs: 900}
	second.Script.Config.Log = dsl.LogConfig{Level: "extended"}
	second.Script.Config.Bruno.SecretBindings = map[string]string{"TOKEN": "bound/token"}
	second.Script.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
		Source:    "bruno",
		Preserved: map[string]any{"operator_note": "retain"},
	}}
	second.UseRawArtifact = true
	if _, err := store.CommitBrunoImport(second); err != nil {
		t.Fatal(err)
	}

	revision, err := store.ActivateImportSourceRevision("script-rollback", "project-bruno", "revision-1", "revision-2")
	if err != nil {
		t.Fatalf("ActivateImportSourceRevision: %v", err)
	}
	if !revision.Active || revision.ID != "revision-1" {
		t.Fatalf("activated revision = %+v", revision)
	}
	rolledBack, err := store.GetScript("script-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Config.Bruno.BundleRef != "artifact://artifact-sanitized-1" {
		t.Fatalf("rolled-back bundle = %q", rolledBack.Config.Bruno.BundleRef)
	}
	if rolledBack.Config.Pacing.DelayMs != 900 || rolledBack.Config.Log.Level != "extended" {
		t.Fatalf("APEX tuning was not preserved: pacing=%+v log=%+v", rolledBack.Config.Pacing, rolledBack.Config.Log)
	}
	if got := rolledBack.Config.Bruno.SecretBindings["TOKEN"]; got != "bound/token" {
		t.Fatalf("secret binding = %q, want bound/token", got)
	}
	if rolledBack.Metadata == nil || rolledBack.Metadata.Import == nil {
		t.Fatalf("rolled-back metadata = %#v", rolledBack.Metadata)
	}
	if _, exists := rolledBack.Metadata.Import.Preserved[dsl.ImportPreservedRuntimeArtifactKey]; exists {
		t.Fatalf("sanitized revision retained raw runtime marker: %#v", rolledBack.Metadata.Import.Preserved)
	}
	if got := rolledBack.Metadata.Import.Preserved["operator_note"]; got != "retain" {
		t.Fatalf("unrelated import metadata = %#v, want operator note preserved", rolledBack.Metadata.Import.Preserved)
	}

	revision, err = store.ActivateImportSourceRevision("script-rollback", "project-bruno", "revision-2", "revision-1")
	if err != nil {
		t.Fatalf("ActivateImportSourceRevision(raw): %v", err)
	}
	if !revision.Active || revision.ID != "revision-2" {
		t.Fatalf("activated raw revision = %+v", revision)
	}
	restoredRaw, err := store.GetScript("script-rollback")
	if err != nil {
		t.Fatal(err)
	}
	if restoredRaw.Config.Bruno.BundleRef != "artifact://artifact-raw-2" {
		t.Fatalf("restored raw bundle = %q", restoredRaw.Config.Bruno.BundleRef)
	}
	if restoredRaw.Metadata == nil || restoredRaw.Metadata.Import == nil {
		t.Fatalf("restored raw metadata = %#v", restoredRaw.Metadata)
	}
	if got := restoredRaw.Metadata.Import.Preserved[dsl.ImportPreservedRuntimeArtifactKey]; got != "raw" {
		t.Fatalf("restored raw runtime marker = %#v, want raw", got)
	}
	if restoredRaw.Config.Pacing.DelayMs != 900 || restoredRaw.Config.Log.Level != "extended" {
		t.Fatalf("APEX tuning changed after raw restore: pacing=%+v log=%+v", restoredRaw.Config.Pacing, restoredRaw.Config.Log)
	}
}

func TestDeleteScriptRemovesBrunoRevisionsAndArtifactMetadata(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := brunoImportCommitFixture("script-delete", "project-bruno", "revision-delete", "artifact-raw-delete", "artifact-sanitized-delete", "sha-raw-delete", "sha-sanitized-delete")
	if _, err := store.CommitBrunoImport(commit); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteScript("script-delete"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetImportSourceRevision("revision-delete", "project-bruno"); err == nil {
		t.Fatal("revision remains after script deletion")
	}
	if _, err := store.GetImportSourceArtifact("artifact-raw-delete", "project-bruno"); err == nil {
		t.Fatal("artifact metadata remains after script deletion")
	}
}

func TestDeleteScriptIfUnchangedRemovesBrunoDependencies(t *testing.T) {
	store := newBrunoImportTestStore(t)
	commit := brunoImportCommitFixture("script-cas-delete", "project-bruno", "revision-cas-delete", "artifact-raw-cas", "artifact-sanitized-cas", "sha-raw-cas", "sha-sanitized-cas")
	if _, err := store.CommitBrunoImport(commit); err != nil {
		t.Fatal(err)
	}
	script, err := store.GetScript("script-cas-delete")
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := store.DeleteScriptIfUnchanged(script.ID, script.UpdatedAt)
	if err != nil || !deleted {
		t.Fatalf("DeleteScriptIfUnchanged deleted=%t error=%v", deleted, err)
	}
	if _, err := store.GetImportSourceRevision("revision-cas-delete", "project-bruno"); err == nil {
		t.Fatal("revision remains after conditional script deletion")
	}
	if _, err := store.GetImportSourceArtifact("artifact-raw-cas", "project-bruno"); err == nil {
		t.Fatal("artifact metadata remains after conditional script deletion")
	}
}

func newBrunoImportTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "bruno.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	if err := store.SaveProject(&Project{ID: "project-bruno", Name: "Bruno", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFolder(&Folder{
		ID:        "native-folder",
		ProjectID: "project-bruno",
		Name:      "Native imports",
		Category:  "scripts",
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	return store
}

func brunoImportCommitFixture(scriptID, projectID, revisionID, rawID, sanitizedID, rawSHA, sanitizedSHA string) *BrunoImportCommit {
	script := &dsl.Script{
		ID:          scriptID,
		Name:        "Bruno collection",
		Version:     "1",
		Protocol:    "bruno",
		ProjectID:   projectID,
		GeneratedBy: "bruno-import",
		Config: dsl.ScriptConfig{
			Bruno: dsl.BrunoConfig{
				BundleRef:            "artifact://" + sanitizedID,
				BundleSHA256:         sanitizedSHA,
				ManifestSHA256:       "manifest-sha",
				SourceFormat:         "yml",
				SourceRevision:       revisionID,
				CompatibilityVersion: "3.5.2",
				Sandbox:              "safe",
			},
		},
		Actions: []dsl.Action{{
			Name:   "Run Bruno collection",
			Type:   "bruno_run",
			Target: ".",
			Metadata: map[string]any{
				"selection": "collection",
			},
		}},
	}
	return &BrunoImportCommit{
		Script: script,
		RawArtifact: ImportSourceArtifact{
			ID:                 rawID,
			ArtifactKind:       "raw",
			BackendKey:         "objects/" + rawSHA,
			SHA256:             rawSHA,
			SizeBytes:          10,
			ManifestSHA256:     "manifest-sha",
			SourceFormat:       "yml",
			EncryptionMetadata: map[string]any{"algorithm": "AES-256-GCM"},
		},
		SanitizedArtifact: ImportSourceArtifact{
			ID:                 sanitizedID,
			ArtifactKind:       "sanitized",
			BackendKey:         "objects/" + sanitizedSHA,
			SHA256:             sanitizedSHA,
			SizeBytes:          9,
			ManifestSHA256:     "manifest-sha",
			SourceFormat:       "yml",
			EncryptionMetadata: map[string]any{"algorithm": "AES-256-GCM"},
		},
		Revision: ImportSourceRevision{
			ID:                   revisionID,
			SourceFormat:         "yml",
			CompatibilityVersion: "3.5.2",
			IdentityMap:          map[string]string{"request.yml": "uid-1"},
			ImportOptions:        map[string]any{"selection": "collection"},
			SecurityProfile:      map[string]any{"sandbox": "safe"},
			ChangeSummary:        map[string]any{"added": 1},
			SecretBindings:       map[string]string{},
			CreatedBy:            "test",
		},
	}
}
