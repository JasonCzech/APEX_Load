package storage

import (
	"path/filepath"
	"testing"
	"time"
)

// seedRevisionPrerequisites creates the script and the two artifacts that a
// revision's foreign keys require. The project comes from the default hierarchy
// that init() already established.
func seedRevisionPrerequisites(t *testing.T, store *SQLiteStore) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := store.db.Exec(`INSERT INTO scripts (id, name, protocol, version, project_id, created_at, updated_at)
		VALUES ('script-1', 'seed', 'bruno', 1, ?, ?, ?)`, DefaultProjectID, now, now); err != nil {
		t.Fatalf("seed script: %v", err)
	}
	for _, artifact := range []struct{ id, kind string }{{"artifact-raw", "raw"}, {"artifact-sanitized", "sanitized"}} {
		if _, err := store.db.Exec(`INSERT INTO import_source_artifacts
			(id, project_id, artifact_kind, backend_key, sha256, size_bytes, encryption_json, lifecycle_state, created_at)
			VALUES (?, ?, ?, ?, ?, 1, '{}', 'active', ?)`,
			artifact.id, DefaultProjectID, artifact.kind, "key/"+artifact.id, "sha-"+artifact.id, now); err != nil {
			t.Fatalf("seed artifact %s: %v", artifact.id, err)
		}
	}
}

// seedRevisionRow inserts one revision directly, bypassing CommitBrunoImport so
// the test can construct states the production writer would not produce -- in
// particular a raw-only revision whose two artifact columns are the same id.
func seedRevisionRow(t *testing.T, store *SQLiteStore, id, rawArtifactID, sanitizedArtifactID string, sanitizedAvailable *int) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	columns := `id, script_id, project_id, active_flag, raw_artifact_id, sanitized_artifact_id,
		source_format, compatibility_version, identity_map_json, import_options_json,
		security_profile_json, change_summary_json, secret_bindings_json, canonical_script_json, created_at`
	values := `?, 'script-1', ?, 0, ?, ?, 'bruno_native', 'v1', '{}', '{}', '{}', '{}', '{}', '{}', ?`
	args := []any{id, DefaultProjectID, rawArtifactID, sanitizedArtifactID, now}
	if sanitizedAvailable != nil {
		columns += ", sanitized_available"
		values += ", ?"
		args = append(args, *sanitizedAvailable)
	}
	if _, err := store.db.Exec(`INSERT INTO import_source_revisions (`+columns+`) VALUES (`+values+`)`, args...); err != nil {
		t.Fatalf("seed revision %s: %v", id, err)
	}
}

func sanitizedAvailableFor(t *testing.T, store *SQLiteStore, id string) int {
	t.Helper()
	var value int
	if err := store.db.QueryRow(`SELECT sanitized_available FROM import_source_revisions WHERE id = ?`, id).Scan(&value); err != nil {
		t.Fatalf("read sanitized_available for %s: %v", id, err)
	}
	return value
}

// TestSanitizedAvailabilityBackfillDistinguishesRawOnlyRevisions is the property
// that lets this migration run on every start instead of exactly once.
//
// A revision written before the column existed always came from Bruno, which
// produces two distinct artifacts. A raw-only revision points both columns at
// the same artifact. If the backfill keyed on "column is 0" alone it would
// promote raw-only revisions into editor-visible sanitized downloads on the
// next restart -- silently, and for every such row.
func TestSanitizedAvailabilityBackfillDistinguishesRawOnlyRevisions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backfill.db")

	first, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	seedRevisionPrerequisites(t, first)
	// Legacy Bruno shape: distinct raw and sanitized artifacts, flag not set.
	seedRevisionRow(t, first, "legacy-bruno", "artifact-raw", "artifact-sanitized", nil)
	// Raw-only shape: both columns reference the same artifact.
	seedRevisionRow(t, first, "raw-only", "artifact-raw", "artifact-raw", nil)
	if err := first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer second.Close()

	if got := sanitizedAvailableFor(t, second, "legacy-bruno"); got != 1 {
		t.Fatalf("legacy Bruno revision sanitized_available = %d, want 1 (backfill should have marked it)", got)
	}
	if got := sanitizedAvailableFor(t, second, "raw-only"); got != 0 {
		t.Fatalf("raw-only revision sanitized_available = %d, want 0 — the backfill promoted a raw-only revision", got)
	}
}

// TestSanitizedAvailabilityBackfillIsIdempotent runs the migration repeatedly.
// A raw-only revision must never drift to 1 no matter how many times the process
// restarts.
func TestSanitizedAvailabilityBackfillIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "idempotent.db")

	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	seedRevisionPrerequisites(t, store)
	seedRevisionRow(t, store, "raw-only", "artifact-raw", "artifact-raw", nil)
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	for attempt := 0; attempt < 3; attempt++ {
		reopened, err := NewSQLiteStore(path)
		if err != nil {
			t.Fatalf("reopen %d: %v", attempt, err)
		}
		if got := sanitizedAvailableFor(t, reopened, "raw-only"); got != 0 {
			t.Fatalf("after restart %d, raw-only sanitized_available = %d, want 0", attempt+1, got)
		}
		if err := reopened.Close(); err != nil {
			t.Fatalf("close %d: %v", attempt, err)
		}
	}
}

// TestSanitizedAvailabilityDefaultsToWithholding pins the direction of the
// column default. A row inserted by code that does not know about this column
// must default to "no sanitized document", so the failure mode of an
// unconverted writer is a withheld download rather than a raw-source
// disclosure.
func TestSanitizedAvailabilityDefaultsToWithholding(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "default.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	seedRevisionPrerequisites(t, store)
	seedRevisionRow(t, store, "unknowing-writer", "artifact-raw", "artifact-sanitized", nil)
	if got := sanitizedAvailableFor(t, store, "unknowing-writer"); got != 0 {
		t.Fatalf("column default = %d, want 0 so an unconverted writer withholds rather than discloses", got)
	}
}

// TestSanitizedAvailabilityRoundTripsThroughScan pins that the flag survives the
// read path. If the column were added to the schema but dropped from the SELECT
// or the scan, every revision would read back as raw-only and the sanitized
// download would break for Bruno -- a loud failure, but worth catching here
// rather than in a handler test.
func TestSanitizedAvailabilityRoundTripsThroughScan(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "roundtrip.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	seedRevisionPrerequisites(t, store)
	available := 1
	seedRevisionRow(t, store, "with-sanitized", "artifact-raw", "artifact-sanitized", &available)
	withheld := 0
	seedRevisionRow(t, store, "without-sanitized", "artifact-raw", "artifact-raw", &withheld)

	revisions, err := store.ListBrunoSourceRevisions("script-1", DefaultProjectID)
	if err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	got := map[string]bool{}
	for _, revision := range revisions {
		got[revision.ID] = revision.SanitizedAvailable
	}
	if !got["with-sanitized"] {
		t.Fatal("revision with a sanitized artifact read back as raw-only")
	}
	if got["without-sanitized"] {
		t.Fatal("raw-only revision read back as having a sanitized artifact")
	}
}
