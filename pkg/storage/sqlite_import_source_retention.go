package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const ImportSourceCommitFenceMetadataKey = "protected_source_commit_fence_v1"

var ErrImportSourceScriptChanged = errors.New("import source retention target script changed")

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
	// ExpectedScriptFingerprint fences source activation to the exact
	// committed script content that produced CanonicalScript. Callers may omit
	// it for compatibility; RetainImportSource derives the same fingerprint
	// from CanonicalScript and still enforces the live-row comparison.
	ExpectedScriptFingerprint string
	CreatedBy                 string
}

// ImportSourceRetentionStore is optional, matching ImportSourceStore, so
// lightweight test stores stay source-compatible.
type ImportSourceRetentionStore interface {
	RetainImportSource(retention *ImportSourceRetention) (*ImportSourceRevision, error)
}

// RetainImportSource stores one raw source artifact and makes it the active
// revision of an existing script.
//
// Both artifact columns point at the same raw artifact. The revision schema
// requires sanitized_artifact_id, and this format has no sanitized document to
// put there. SanitizedAvailable stays false, which is what stops the source
// download from serving these bytes under the editor-visible sanitized gate --
// the column being populated is deliberately not what authorizes that.
func (s *SQLiteStore) RetainImportSource(retention *ImportSourceRetention) (*ImportSourceRevision, error) {
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
	canonicalFingerprint, err := ImportSourceScriptFingerprint(retention.CanonicalScript)
	if err != nil {
		return nil, fmt.Errorf("fingerprint retained import source script: %w", err)
	}
	expectedFingerprint := strings.ToLower(strings.TrimSpace(retention.ExpectedScriptFingerprint))
	if expectedFingerprint == "" {
		expectedFingerprint = canonicalFingerprint
	} else if expectedFingerprint != canonicalFingerprint {
		return nil, errors.New("import source retention fingerprint does not match canonical script")
	}

	revision := ImportSourceRevision{
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

	// Retention is a second durable boundary after the script commit. Lock the
	// live script before reading it, then keep that lock through revision
	// activation so delayed publication A cannot become active after script B
	// has replaced it. SQLite's no-op update obtains the write reservation
	// before the read; PostgreSQL uses a row lock.
	if s.db.dialect != storeDialectPostgres {
		result, lockErr := tx.Exec(`UPDATE scripts SET id = id WHERE id = ?`, scriptID)
		if lockErr != nil {
			return nil, lockErr
		}
		if affected, rowsErr := result.RowsAffected(); rowsErr != nil {
			return nil, rowsErr
		} else if affected != 1 {
			return nil, errors.New("import source retention target script not found")
		}
	}
	liveScript, err := loadImportSourceRetentionScriptTx(tx, scriptID, s.db.dialect == storeDialectPostgres)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("import source retention target script not found")
		}
		return nil, err
	}
	if normalizeProjectID(liveScript.ProjectID) != projectID {
		return nil, errors.New("import source retention target script belongs to another project")
	}
	liveFingerprint, err := ImportSourceScriptFingerprint(liveScript)
	if err != nil {
		return nil, fmt.Errorf("fingerprint live import source script: %w", err)
	}
	if liveFingerprint != expectedFingerprint {
		return nil, ErrImportSourceScriptChanged
	}

	artifact, err := saveBrunoArtifactTx(tx, &retention.Artifact)
	if err != nil {
		return nil, err
	}
	revision.RawArtifactID = artifact.ID
	revision.SanitizedArtifactID = artifact.ID
	revision.SanitizedAvailable = false

	previousActiveID, err := importSourceParentRevisionID(tx.QueryRow(
		`SELECT id FROM bruno_source_revisions WHERE script_id = ? AND project_id = ? AND active_flag = 1`,
		scriptID,
		projectID,
	))
	if err != nil {
		return nil, err
	}
	if previousActiveID != "" {
		revision.ParentRevisionID = previousActiveID
	}
	if _, err := tx.Exec(`UPDATE bruno_source_revisions SET active_flag = 0 WHERE script_id = ? AND project_id = ?`,
		scriptID, projectID); err != nil {
		return nil, err
	}

	encoded, err := marshalBrunoRevision(&revision)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO bruno_source_revisions (
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

func importSourceParentRevisionID(row rowScanner) (string, error) {
	var id string
	if err := row.Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("load active import source parent revision: %w", err)
	}
	return id, nil
}

// ImportSourceScriptFingerprint returns the stable content identity used to
// bind a retained source revision to its committed script. Volatile storage
// fields are excluded, as is the self-describing commit fence itself. Every
// executable and review-relevant script field remains covered.
func ImportSourceScriptFingerprint(script *dsl.Script) (string, error) {
	if script == nil {
		return "", errors.New("script is required")
	}
	source, err := json.Marshal(script)
	if err != nil {
		return "", fmt.Errorf("marshal script for fingerprint: %w", err)
	}
	var clone dsl.Script
	if err := json.Unmarshal(source, &clone); err != nil {
		return "", fmt.Errorf("clone script for fingerprint: %w", err)
	}
	clone.RefKey = ""
	clone.CreatedAt = time.Time{}
	clone.UpdatedAt = time.Time{}
	if clone.Metadata != nil && clone.Metadata.Import != nil &&
		clone.Metadata.Import.Preserved != nil {
		delete(clone.Metadata.Import.Preserved, ImportSourceCommitFenceMetadataKey)
	}
	payload, err := json.Marshal(&clone)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:]), nil
}

func loadImportSourceRetentionScriptTx(tx *storeTx, scriptID string, lock bool) (*dsl.Script, error) {
	query := `SELECT id, COALESCE(ref_key, ''), name, protocol, version, COALESCE(project_id, ''), folder_id, COALESCE(generated_by, ''), COALESCE(generated_from, ''), COALESCE(metadata_json, '{}'), COALESCE(goal_config_json, ''), COALESCE(config, '{}'), COALESCE(data_sources, '[]'), COALESCE(parameters, '[]'), COALESCE(init, '[]'), COALESCE(actions, '[]'), COALESCE(teardown, '[]'), created_at, updated_at
		FROM scripts WHERE id = ?`
	if lock {
		query += ` FOR UPDATE`
	}
	var script dsl.Script
	var metadataJSON, goalsJSON, configJSON, dataSourcesJSON, parametersJSON, initJSON, actionsJSON, teardownJSON string
	err := tx.QueryRow(query, scriptID).Scan(
		&script.ID, &script.RefKey, &script.Name, &script.Protocol, &script.Version,
		&script.ProjectID, &script.FolderID,
		&script.GeneratedBy, &script.GeneratedFrom,
		&metadataJSON, &goalsJSON, &configJSON, &dataSourcesJSON, &parametersJSON, &initJSON, &actionsJSON, &teardownJSON,
		&script.CreatedAt, &script.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := decodeStoredScriptJSON(
		&script,
		metadataJSON,
		goalsJSON,
		configJSON,
		dataSourcesJSON,
		parametersJSON,
		initJSON,
		actionsJSON,
		teardownJSON,
	); err != nil {
		return nil, fmt.Errorf("decode import source retention script %s: %w", scriptID, err)
	}
	return &script, nil
}
