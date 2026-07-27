package storage

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// ImportSourceRetention records the source a non-Bruno importer wants kept
// alongside a script that has already been created.
//
// It is deliberately separate from BrunoImportCommit rather than a mode of it.
// CommitBrunoImport creates the script, rewrites script.Config.Bruno, and
// requires protocol "bruno"; retention here does none of those. Folding the two
// together would mean a retaining OpenAPI import started carrying Bruno bundle
// configuration, which the engine flattens into bruno_* keys for every script.
type ImportSourceRetention struct {
	ScriptID  string
	ProjectID string
	// Artifact is the raw, unredacted source. Formats that cannot produce a
	// separately sanitized document retain only this; see SanitizedAvailable on
	// ImportSourceRevision for what that costs at download time.
	Artifact             ImportSourceArtifact
	SourceFormat         string
	CompatibilityVersion string
	CanonicalScript      *dsl.Script
	CreatedBy            string
}

// ImportSourceRetentionStore is optional, matching BrunoImportStore, so
// lightweight test stores stay source-compatible.
type ImportSourceRetentionStore interface {
	RetainImportSource(retention *ImportSourceRetention) (*BrunoSourceRevision, error)
}

// RetainImportSource stores one raw source artifact and makes it the active
// revision of an existing script.
//
// Both artifact columns point at the same raw artifact. The revision schema
// requires sanitized_artifact_id, and this format has no sanitized document to
// put there. SanitizedAvailable stays false, which is what stops the source
// download from serving these bytes under the editor-visible sanitized gate --
// the column being populated is deliberately not what authorizes that.
func (s *SQLiteStore) RetainImportSource(retention *ImportSourceRetention) (*BrunoSourceRevision, error) {
	if retention == nil || retention.CanonicalScript == nil {
		return nil, errors.New("import source retention canonical script is required")
	}
	scriptID := strings.TrimSpace(retention.ScriptID)
	if scriptID == "" {
		return nil, errors.New("import source retention script id is required")
	}
	projectID := normalizeProjectID(strings.TrimSpace(retention.ProjectID))
	sourceFormat := strings.TrimSpace(retention.SourceFormat)
	if sourceFormat == "" {
		return nil, errors.New("import source retention source format is required")
	}
	if err := normalizeBrunoArtifact(&retention.Artifact, projectID, "raw"); err != nil {
		return nil, err
	}

	revision := BrunoSourceRevision{
		ID:                   uuid.NewString(),
		ScriptID:             scriptID,
		ProjectID:            projectID,
		Active:               true,
		SourceFormat:         sourceFormat,
		CompatibilityVersion: firstNonEmptyStorage(strings.TrimSpace(retention.CompatibilityVersion), "v1"),
		CanonicalScript:      cloneBrunoScript(retention.CanonicalScript),
		CreatedBy:            strings.TrimSpace(retention.CreatedBy),
		CreatedAt:            time.Now().UTC(),
	}

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Confirm the script exists in this project before writing a revision for
	// it. The foreign key would catch a missing script, but not one that lives
	// in a different project, which would silently attach retained source to
	// another tenant's script.
	var storedProjectID string
	if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM scripts WHERE id = ?`, scriptID).Scan(&storedProjectID); err != nil {
		return nil, errors.New("import source retention target script not found")
	}
	if normalizeProjectID(storedProjectID) != projectID {
		return nil, errors.New("import source retention target script belongs to another project")
	}

	artifact, err := saveBrunoArtifactTx(tx, &retention.Artifact)
	if err != nil {
		return nil, err
	}
	revision.RawArtifactID = artifact.ID
	revision.SanitizedArtifactID = artifact.ID
	revision.SanitizedAvailable = false

	var previousActiveID string
	if err := tx.QueryRow(`SELECT id FROM import_source_revisions WHERE script_id = ? AND project_id = ? AND active_flag = 1`,
		scriptID, projectID).Scan(&previousActiveID); err == nil {
		revision.ParentRevisionID = previousActiveID
	}
	if _, err := tx.Exec(`UPDATE import_source_revisions SET active_flag = 0 WHERE script_id = ? AND project_id = ?`,
		scriptID, projectID); err != nil {
		return nil, err
	}

	encoded, err := marshalBrunoRevision(&revision)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO import_source_revisions (
		id, script_id, project_id, parent_revision_id, active_flag, raw_artifact_id, sanitized_artifact_id,
		sanitized_available, source_format, compatibility_version, identity_map_json, import_options_json, dependency_snapshot_ref,
		security_profile_json, change_summary_json, secret_bindings_json, canonical_script_json, created_by, created_at
	) VALUES (?, ?, ?, ?, 1, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		revision.ID, scriptID, projectID, nullableString(revision.ParentRevisionID),
		revision.RawArtifactID, revision.SanitizedArtifactID,
		revision.SourceFormat, revision.CompatibilityVersion,
		encoded.identityMap, encoded.importOptions, nullableString(revision.DependencySnapshotRef),
		encoded.securityProfile, encoded.changeSummary, encoded.secretBindings,
		encoded.canonicalScript, nullableString(revision.CreatedBy), revision.CreatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return cloneBrunoRevision(&revision), nil
}
