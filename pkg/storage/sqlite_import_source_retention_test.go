package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func retentionTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "retention.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	now := time.Now().UTC()
	if _, err := store.db.Exec(`INSERT INTO scripts (id, name, protocol, version, project_id, created_at, updated_at)
		VALUES ('script-1', 'imported', 'http', 1, ?, ?, ?)`, DefaultProjectID, now, now); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	return store
}

func retentionInput(scriptID, projectID, sha string) *ImportSourceRetention {
	return &ImportSourceRetention{
		ScriptID:  scriptID,
		ProjectID: projectID,
		Artifact: ImportSourceArtifact{
			ArtifactKind: "raw",
			BackendKey:   "key/" + sha,
			SHA256:       sha,
			SizeBytes:    128,
		},
		SourceFormat:    "openapi",
		CanonicalScript: &dsl.Script{ID: scriptID, Name: "imported", Protocol: "http"},
	}
}

// TestRetainImportSourceStoresRawOnlyRevision pins the shape a format without a
// sanitizer produces: one artifact, referenced by both columns, and the
// availability flag left false so the sanitized download stays closed.
func TestRetainImportSourceStoresRawOnlyRevision(t *testing.T) {
	store := retentionTestStore(t)

	revision, err := store.RetainImportSource(retentionInput("script-1", DefaultProjectID, "sha-one"))
	if err != nil {
		t.Fatalf("retain: %v", err)
	}
	if revision.RawArtifactID == "" || revision.RawArtifactID != revision.SanitizedArtifactID {
		t.Fatalf("raw-only revision should reference one artifact twice, got raw=%q sanitized=%q",
			revision.RawArtifactID, revision.SanitizedArtifactID)
	}
	if revision.SanitizedAvailable {
		t.Fatal("raw-only retention must not claim a sanitized artifact is available")
	}
	if !revision.Active {
		t.Fatal("retained revision should become the active one")
	}

	active, err := store.GetActiveBrunoSourceRevision("script-1", DefaultProjectID)
	if err != nil {
		t.Fatalf("read active revision: %v", err)
	}
	if active.ID != revision.ID {
		t.Fatalf("active revision = %q, want %q", active.ID, revision.ID)
	}
	if active.SanitizedAvailable {
		t.Fatal("sanitized availability must survive the round trip as false")
	}
}

// TestRetainImportSourceRejectsCrossProjectScript is the tenant-isolation check.
//
// The second project must genuinely exist for this to test anything. Against a
// made-up project id the foreign key rejects the write on its own, and the test
// passes whether or not the explicit ownership comparison is present -- which is
// exactly what an earlier version of this test did, and it was worthless.
// With a real project, the FK is satisfied and only the ownership check stands
// between an importer and attaching retained source to another tenant's script.
func TestRetainImportSourceRejectsCrossProjectScript(t *testing.T) {
	store := retentionTestStore(t)

	now := time.Now().UTC()
	if _, err := store.db.Exec(`INSERT INTO projects (id, domain_id, category_id, name, created_at, updated_at)
		VALUES ('other-project', ?, ?, 'Other', ?, ?)`, DefaultDomainID, DefaultRepositoryCategoryID, now, now); err != nil {
		t.Fatalf("seed second project: %v", err)
	}

	_, err := store.RetainImportSource(retentionInput("script-1", "other-project", "sha-two"))
	if err == nil {
		t.Fatal("expected retention to refuse a script owned by another project")
	}

	// Nothing may be left behind by the refusal.
	var revisions int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM import_source_revisions`).Scan(&revisions); err != nil {
		t.Fatalf("count revisions: %v", err)
	}
	if revisions != 0 {
		t.Fatalf("refused retention still wrote %d revision(s)", revisions)
	}
	var artifacts int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM import_source_artifacts`).Scan(&artifacts); err != nil {
		t.Fatalf("count artifacts: %v", err)
	}
	if artifacts != 0 {
		t.Fatalf("refused retention still wrote %d artifact(s)", artifacts)
	}
}

// TestRetainImportSourceSupersedesPriorRevision pins that a re-import chains
// rather than accumulating multiple active revisions, which the partial unique
// index would otherwise reject at some later, less obvious point.
func TestRetainImportSourceSupersedesPriorRevision(t *testing.T) {
	store := retentionTestStore(t)

	first, err := store.RetainImportSource(retentionInput("script-1", DefaultProjectID, "sha-first"))
	if err != nil {
		t.Fatalf("first retain: %v", err)
	}
	second, err := store.RetainImportSource(retentionInput("script-1", DefaultProjectID, "sha-second"))
	if err != nil {
		t.Fatalf("second retain: %v", err)
	}
	if second.ParentRevisionID != first.ID {
		t.Fatalf("second revision parent = %q, want %q", second.ParentRevisionID, first.ID)
	}

	revisions, err := store.ListBrunoSourceRevisions("script-1", DefaultProjectID)
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	active := 0
	for _, revision := range revisions {
		if revision.Active {
			active++
		}
	}
	if len(revisions) != 2 || active != 1 {
		t.Fatalf("got %d revisions with %d active, want 2 and 1", len(revisions), active)
	}
}

// TestRetainImportSourceRequiresSourceFormat pins that retention refuses to
// record a revision it cannot attribute to a format. source_format is what a
// later reader uses to decide how to interpret the retained bytes.
func TestRetainImportSourceRequiresSourceFormat(t *testing.T) {
	store := retentionTestStore(t)

	input := retentionInput("script-1", DefaultProjectID, "sha-three")
	input.SourceFormat = "  "
	if _, err := store.RetainImportSource(input); err == nil {
		t.Fatal("expected retention to require a source format")
	}
}
