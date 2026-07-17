package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func (s *SQLiteStore) CommitBrunoImport(commit *BrunoImportCommit) (*BrunoImportCommitResult, error) {
	if commit == nil || commit.Script == nil {
		return nil, errors.New("Bruno import script is required")
	}
	script := commit.Script
	script.ID = strings.TrimSpace(script.ID)
	script.ProjectID = normalizeProjectID(strings.TrimSpace(script.ProjectID))
	if script.ID == "" {
		return nil, errors.New("Bruno import script id is required")
	}
	if strings.TrimSpace(script.Protocol) != "bruno" {
		return nil, errors.New("Bruno import script protocol must be bruno")
	}
	if strings.TrimSpace(commit.Revision.ID) == "" {
		commit.Revision.ID = uuid.NewString()
	}
	commit.Revision.ScriptID = script.ID
	commit.Revision.ProjectID = script.ProjectID
	commit.Revision.Active = true
	if commit.Revision.CreatedAt.IsZero() {
		commit.Revision.CreatedAt = time.Now().UTC()
	}
	if err := normalizeBrunoArtifact(&commit.RawArtifact, script.ProjectID, "raw"); err != nil {
		return nil, err
	}
	if err := normalizeBrunoArtifact(&commit.SanitizedArtifact, script.ProjectID, "sanitized"); err != nil {
		return nil, err
	}
	commit.Revision.RawArtifactID = commit.RawArtifact.ID
	commit.Revision.SanitizedArtifactID = commit.SanitizedArtifact.ID

	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := validateScriptFolderProjectTx(tx, script.ProjectID, script.FolderID); err != nil {
		return nil, err
	}
	if err := validateScriptObjectRepositoryProjectsTx(tx, script.ProjectID, script); err != nil {
		return nil, err
	}
	rawArtifact, err := saveBrunoArtifactTx(tx, &commit.RawArtifact)
	if err != nil {
		return nil, err
	}
	sanitizedArtifact, err := saveBrunoArtifactTx(tx, &commit.SanitizedArtifact)
	if err != nil {
		return nil, err
	}
	commit.Revision.RawArtifactID = rawArtifact.ID
	commit.Revision.SanitizedArtifactID = sanitizedArtifact.ID
	script.Config.Bruno.BundleRef = "artifact://" + sanitizedArtifact.ID
	script.Config.Bruno.BundleSHA256 = sanitizedArtifact.SHA256
	script.Config.Bruno.ManifestSHA256 = sanitizedArtifact.ManifestSHA256
	script.Config.Bruno.SourceFormat = firstNonEmptyStorage(script.Config.Bruno.SourceFormat, commit.Revision.SourceFormat)
	script.Config.Bruno.SourceRevision = commit.Revision.ID
	script.Config.Bruno.CompatibilityVersion = firstNonEmptyStorage(script.Config.Bruno.CompatibilityVersion, commit.Revision.CompatibilityVersion)
	commit.Revision.CanonicalScript = cloneBrunoScript(script)
	encodedScript, err := marshalScriptForStorage(script)
	if err != nil {
		return nil, err
	}
	created, err := upsertBrunoImportScriptTx(tx, script, encodedScript, commit.ExpectedScriptUpdatedAt)
	if err != nil {
		return nil, err
	}
	encodedRevision, err := marshalBrunoRevision(&commit.Revision)
	if err != nil {
		return nil, err
	}

	var activeRevisionID string
	activeErr := tx.QueryRow(`SELECT id FROM bruno_source_revisions WHERE script_id = ? AND project_id = ? AND active_flag = 1`,
		script.ID, script.ProjectID).Scan(&activeRevisionID)
	if activeErr != nil && !errors.Is(activeErr, sql.ErrNoRows) {
		return nil, activeErr
	}
	expectedActive := strings.TrimSpace(commit.ExpectedActiveRevisionID)
	if expectedActive != "" && (errors.Is(activeErr, sql.ErrNoRows) || activeRevisionID != expectedActive) {
		return nil, ErrBrunoImportConcurrentChange
	}
	parentID := strings.TrimSpace(commit.Revision.ParentRevisionID)
	if parentID == "" && activeErr == nil {
		parentID = activeRevisionID
	}
	if parentID != "" {
		var parentScriptID, parentProjectID string
		if err := tx.QueryRow(`SELECT script_id, project_id FROM bruno_source_revisions WHERE id = ?`, parentID).Scan(&parentScriptID, &parentProjectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrBrunoRevisionNotFound
			}
			return nil, err
		}
		if parentScriptID != script.ID || normalizeProjectID(parentProjectID) != script.ProjectID {
			return nil, errors.New("Bruno parent revision belongs to another script or project")
		}
	}
	commit.Revision.ParentRevisionID = parentID
	if _, err := tx.Exec(`UPDATE bruno_source_revisions SET active_flag = 0 WHERE script_id = ? AND project_id = ?`,
		script.ID, script.ProjectID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO bruno_source_revisions (
		id, script_id, project_id, parent_revision_id, active_flag, raw_artifact_id, sanitized_artifact_id,
		source_format, compatibility_version, identity_map_json, import_options_json, dependency_snapshot_ref,
		security_profile_json, change_summary_json, secret_bindings_json, canonical_script_json, created_by, created_at
	) VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		commit.Revision.ID, script.ID, script.ProjectID, nullableString(parentID),
		commit.Revision.RawArtifactID, commit.Revision.SanitizedArtifactID,
		commit.Revision.SourceFormat, commit.Revision.CompatibilityVersion,
		encodedRevision.identityMap, encodedRevision.importOptions, nullableString(commit.Revision.DependencySnapshotRef),
		encodedRevision.securityProfile, encodedRevision.changeSummary, encodedRevision.secretBindings,
		encodedRevision.canonicalScript, nullableString(commit.Revision.CreatedBy), commit.Revision.CreatedAt); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &BrunoImportCommitResult{
		Script:   cloneBrunoScript(script),
		Revision: cloneBrunoRevision(&commit.Revision),
		Created:  created,
	}, nil
}

func upsertBrunoImportScriptTx(tx *storeTx, script *dsl.Script, encoded storedScriptJSON, expectedUpdatedAt *time.Time) (bool, error) {
	var currentCreatedAt, currentUpdatedAt time.Time
	var currentUpdatedRaw, currentProjectID string
	err := tx.QueryRow(`SELECT created_at, updated_at, CAST(updated_at AS TEXT), COALESCE(project_id, '') FROM scripts WHERE id = ?`, script.ID).
		Scan(&currentCreatedAt, &currentUpdatedAt, &currentUpdatedRaw, &currentProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		if expectedUpdatedAt != nil && !expectedUpdatedAt.IsZero() {
			return false, ErrBrunoImportConcurrentChange
		}
		now := time.Now().UTC()
		if script.CreatedAt.IsZero() {
			script.CreatedAt = now
		}
		script.UpdatedAt = now
		if _, err := tx.Exec(`INSERT INTO scripts (
			id, ref_key, name, protocol, version, project_id, folder_id, generated_by, generated_from,
			metadata_json, config, data_sources, parameters, init, actions, teardown, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			script.ID, script.RefKey, script.Name, script.Protocol, script.Version, script.ProjectID, script.FolderID,
			script.GeneratedBy, script.GeneratedFrom, encoded.metadata, encoded.config, encoded.dataSources,
			encoded.parameters, encoded.init, encoded.actions, encoded.teardown, script.CreatedAt, script.UpdatedAt); err != nil {
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if normalizeProjectID(currentProjectID) != script.ProjectID {
		return false, errors.New("Bruno import target script belongs to another project")
	}
	if expectedUpdatedAt == nil || expectedUpdatedAt.IsZero() || !currentUpdatedAt.Equal(expectedUpdatedAt.UTC()) {
		return false, ErrBrunoImportConcurrentChange
	}
	script.CreatedAt = currentCreatedAt
	script.UpdatedAt = time.Now().UTC()
	result, err := tx.Exec(`UPDATE scripts SET
		ref_key = CASE WHEN TRIM(COALESCE(?, '')) = '' THEN ref_key ELSE ? END,
		name = ?, protocol = ?, version = ?, folder_id = ?, generated_by = ?, generated_from = ?,
		metadata_json = ?, config = ?, data_sources = ?, parameters = ?, init = ?, actions = ?, teardown = ?, updated_at = ?
		WHERE id = ? AND project_id = ? AND CAST(updated_at AS TEXT) = ?`,
		script.RefKey, script.RefKey, script.Name, script.Protocol, script.Version, script.FolderID,
		script.GeneratedBy, script.GeneratedFrom, encoded.metadata, encoded.config, encoded.dataSources,
		encoded.parameters, encoded.init, encoded.actions, encoded.teardown, script.UpdatedAt,
		script.ID, script.ProjectID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, ErrBrunoImportConcurrentChange
	}
	return false, nil
}

func normalizeBrunoArtifact(artifact *BrunoSourceArtifact, projectID, kind string) error {
	if artifact == nil {
		return errors.New("Bruno source artifact is required")
	}
	artifact.ProjectID = normalizeProjectID(strings.TrimSpace(projectID))
	artifact.ArtifactKind = firstNonEmptyStorage(strings.TrimSpace(artifact.ArtifactKind), kind)
	artifact.ID = strings.TrimSpace(artifact.ID)
	if artifact.ID == "" {
		artifact.ID = uuid.NewString()
	}
	artifact.BackendKey = strings.TrimSpace(artifact.BackendKey)
	artifact.SHA256 = strings.ToLower(strings.TrimSpace(artifact.SHA256))
	if artifact.BackendKey == "" || artifact.SHA256 == "" || artifact.SizeBytes < 0 {
		return errors.New("Bruno artifact backend key, digest, and non-negative size are required")
	}
	if artifact.LifecycleState == "" {
		artifact.LifecycleState = "active"
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now().UTC()
	}
	return nil
}

func saveBrunoArtifactTx(tx *storeTx, artifact *BrunoSourceArtifact) (*BrunoSourceArtifact, error) {
	var existingID string
	err := tx.QueryRow(`SELECT id FROM bruno_source_artifacts WHERE project_id = ? AND artifact_kind = ? AND sha256 = ?`,
		artifact.ProjectID, artifact.ArtifactKind, artifact.SHA256).Scan(&existingID)
	if err == nil {
		existing, err := getBrunoSourceArtifactTx(tx, existingID, artifact.ProjectID)
		if err != nil {
			return nil, err
		}
		if existing.SizeBytes != artifact.SizeBytes || existing.BackendKey != artifact.BackendKey {
			return nil, errors.New("Bruno content-addressed artifact metadata conflicts with an existing object")
		}
		return existing, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	encryptionJSON, err := marshalStorageJSON(artifact.EncryptionMetadata)
	if err != nil {
		return nil, fmt.Errorf("marshal Bruno artifact encryption metadata: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO bruno_source_artifacts (
		id, project_id, artifact_kind, backend_key, sha256, size_bytes, manifest_sha256,
		source_format, encryption_json, lifecycle_state, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		artifact.ID, artifact.ProjectID, artifact.ArtifactKind, artifact.BackendKey, artifact.SHA256,
		artifact.SizeBytes, nullableString(artifact.ManifestSHA256), nullableString(artifact.SourceFormat),
		encryptionJSON, artifact.LifecycleState, artifact.CreatedAt); err != nil {
		return nil, err
	}
	return cloneBrunoArtifact(artifact), nil
}

func (s *SQLiteStore) GetBrunoSourceArtifact(id string, projectID string) (*BrunoSourceArtifact, error) {
	return getBrunoSourceArtifactDB(s.db, strings.TrimSpace(id), normalizeProjectID(strings.TrimSpace(projectID)))
}

func (s *SQLiteStore) IsBrunoArtifactBackendKeyReferenced(backendKey string) (bool, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM bruno_source_artifacts WHERE backend_key = ?`, strings.TrimSpace(backendKey)).Scan(&count)
	return count > 0, err
}

func getBrunoSourceArtifactTx(tx *storeTx, id, projectID string) (*BrunoSourceArtifact, error) {
	return scanBrunoArtifact(tx.QueryRow(`SELECT id, project_id, artifact_kind, backend_key, sha256, size_bytes,
		COALESCE(manifest_sha256, ''), COALESCE(source_format, ''), encryption_json, lifecycle_state, created_at
		FROM bruno_source_artifacts WHERE id = ? AND project_id = ?`, id, projectID))
}

func getBrunoSourceArtifactDB(db *storeDB, id, projectID string) (*BrunoSourceArtifact, error) {
	return scanBrunoArtifact(db.QueryRow(`SELECT id, project_id, artifact_kind, backend_key, sha256, size_bytes,
		COALESCE(manifest_sha256, ''), COALESCE(source_format, ''), encryption_json, lifecycle_state, created_at
		FROM bruno_source_artifacts WHERE id = ? AND project_id = ?`, id, projectID))
}

func scanBrunoArtifact(row rowScanner) (*BrunoSourceArtifact, error) {
	var artifact BrunoSourceArtifact
	var encryptionJSON string
	if err := row.Scan(&artifact.ID, &artifact.ProjectID, &artifact.ArtifactKind, &artifact.BackendKey,
		&artifact.SHA256, &artifact.SizeBytes, &artifact.ManifestSHA256, &artifact.SourceFormat,
		&encryptionJSON, &artifact.LifecycleState, &artifact.CreatedAt); err != nil {
		return nil, err
	}
	if err := unmarshalStorageJSON(encryptionJSON, &artifact.EncryptionMetadata); err != nil {
		return nil, fmt.Errorf("decode Bruno artifact %s encryption metadata: %w", artifact.ID, err)
	}
	return &artifact, nil
}

func (s *SQLiteStore) GetBrunoSourceRevision(id string, projectID string) (*BrunoSourceRevision, error) {
	return scanBrunoRevision(s.db.QueryRow(brunoRevisionSelect+` WHERE id = ? AND project_id = ?`,
		strings.TrimSpace(id), normalizeProjectID(strings.TrimSpace(projectID))))
}

func (s *SQLiteStore) GetActiveBrunoSourceRevision(scriptID string, projectID string) (*BrunoSourceRevision, error) {
	return scanBrunoRevision(s.db.QueryRow(brunoRevisionSelect+` WHERE script_id = ? AND project_id = ? AND active_flag = 1`,
		strings.TrimSpace(scriptID), normalizeProjectID(strings.TrimSpace(projectID))))
}

func (s *SQLiteStore) ListBrunoSourceRevisions(scriptID string, projectID string) ([]*BrunoSourceRevision, error) {
	rows, err := s.db.Query(brunoRevisionSelect+` WHERE script_id = ? AND project_id = ? ORDER BY created_at DESC`,
		strings.TrimSpace(scriptID), normalizeProjectID(strings.TrimSpace(projectID)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var revisions []*BrunoSourceRevision
	for rows.Next() {
		revision, err := scanBrunoRevision(rows)
		if err != nil {
			return nil, err
		}
		revisions = append(revisions, revision)
	}
	return revisions, rows.Err()
}

func (s *SQLiteStore) ActivateBrunoSourceRevision(scriptID string, projectID string, revisionID string, expectedActiveRevisionID string) (*BrunoSourceRevision, error) {
	scriptID = strings.TrimSpace(scriptID)
	projectID = normalizeProjectID(strings.TrimSpace(projectID))
	revisionID = strings.TrimSpace(revisionID)
	expectedActiveRevisionID = strings.TrimSpace(expectedActiveRevisionID)
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var currentActive string
	err = tx.QueryRow(`SELECT id FROM bruno_source_revisions WHERE script_id = ? AND project_id = ? AND active_flag = 1`,
		scriptID, projectID).Scan(&currentActive)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if expectedActiveRevisionID != "" && (errors.Is(err, sql.ErrNoRows) || currentActive != expectedActiveRevisionID) {
		return nil, ErrBrunoImportConcurrentChange
	}
	revision, err := scanBrunoRevision(tx.QueryRow(brunoRevisionSelect+` WHERE id = ? AND script_id = ? AND project_id = ?`,
		revisionID, scriptID, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrBrunoRevisionNotFound
	}
	if err != nil {
		return nil, err
	}
	var currentScript dsl.Script
	var configJSON, currentUpdatedRaw string
	if err := tx.QueryRow(`SELECT id, name, protocol, version, project_id, config, CAST(updated_at AS TEXT)
		FROM scripts WHERE id = ? AND project_id = ?`, scriptID, projectID).
		Scan(&currentScript.ID, &currentScript.Name, &currentScript.Protocol, &currentScript.Version,
			&currentScript.ProjectID, &configJSON, &currentUpdatedRaw); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(configJSON), &currentScript.Config); err != nil {
		return nil, fmt.Errorf("decode current Bruno script config: %w", err)
	}
	if revision.CanonicalScript == nil {
		return nil, errors.New("Bruno revision has no canonical script snapshot")
	}
	// Preserve APEX pacing, logging, observability, policy, secret-binding names,
	// and the user's selected bruno_run roots. Only the immutable source binding
	// changes during rollback.
	secretBindings := currentScript.Config.Bruno.SecretBindings
	currentScript.Config.Bruno = revision.CanonicalScript.Config.Bruno
	if len(secretBindings) > 0 {
		currentScript.Config.Bruno.SecretBindings = secretBindings
	}
	encodedConfig, err := json.Marshal(currentScript.Config)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	result, err := tx.Exec(`UPDATE scripts SET config = ?, updated_at = ? WHERE id = ? AND project_id = ? AND CAST(updated_at AS TEXT) = ?`,
		string(encodedConfig), now, scriptID, projectID, currentUpdatedRaw)
	if err != nil {
		return nil, err
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		if err != nil {
			return nil, err
		}
		return nil, ErrBrunoImportConcurrentChange
	}
	if _, err := tx.Exec(`UPDATE bruno_source_revisions SET active_flag = 0 WHERE script_id = ? AND project_id = ?`, scriptID, projectID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`UPDATE bruno_source_revisions SET active_flag = 1 WHERE id = ? AND script_id = ? AND project_id = ?`, revisionID, scriptID, projectID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	revision.Active = true
	return revision, nil
}

const brunoRevisionSelect = `SELECT id, script_id, project_id, COALESCE(parent_revision_id, ''), active_flag,
	raw_artifact_id, sanitized_artifact_id, source_format, compatibility_version, identity_map_json,
	import_options_json, COALESCE(dependency_snapshot_ref, ''), security_profile_json, change_summary_json,
	secret_bindings_json, canonical_script_json, COALESCE(created_by, ''), created_at
	FROM bruno_source_revisions`

func scanBrunoRevision(row rowScanner) (*BrunoSourceRevision, error) {
	var revision BrunoSourceRevision
	var active int
	var identityMapJSON, importOptionsJSON, securityProfileJSON, changeSummaryJSON, secretBindingsJSON, canonicalScriptJSON string
	if err := row.Scan(&revision.ID, &revision.ScriptID, &revision.ProjectID, &revision.ParentRevisionID, &active,
		&revision.RawArtifactID, &revision.SanitizedArtifactID, &revision.SourceFormat, &revision.CompatibilityVersion,
		&identityMapJSON, &importOptionsJSON, &revision.DependencySnapshotRef, &securityProfileJSON,
		&changeSummaryJSON, &secretBindingsJSON, &canonicalScriptJSON, &revision.CreatedBy, &revision.CreatedAt); err != nil {
		return nil, err
	}
	revision.Active = active != 0
	for _, field := range []struct {
		name string
		raw  string
		dst  any
	}{
		{"identity_map_json", identityMapJSON, &revision.IdentityMap},
		{"import_options_json", importOptionsJSON, &revision.ImportOptions},
		{"security_profile_json", securityProfileJSON, &revision.SecurityProfile},
		{"change_summary_json", changeSummaryJSON, &revision.ChangeSummary},
		{"secret_bindings_json", secretBindingsJSON, &revision.SecretBindings},
		{"canonical_script_json", canonicalScriptJSON, &revision.CanonicalScript},
	} {
		if err := unmarshalStorageJSON(field.raw, field.dst); err != nil {
			return nil, fmt.Errorf("decode Bruno revision %s %s: %w", revision.ID, field.name, err)
		}
	}
	return &revision, nil
}

type storedBrunoRevisionJSON struct {
	identityMap     string
	importOptions   string
	securityProfile string
	changeSummary   string
	secretBindings  string
	canonicalScript string
}

func marshalBrunoRevision(revision *BrunoSourceRevision) (storedBrunoRevisionJSON, error) {
	if revision == nil || revision.CanonicalScript == nil {
		return storedBrunoRevisionJSON{}, errors.New("Bruno revision canonical script is required")
	}
	var result storedBrunoRevisionJSON
	fields := []struct {
		name  string
		value any
		dst   *string
	}{
		{"identity_map_json", revision.IdentityMap, &result.identityMap},
		{"import_options_json", revision.ImportOptions, &result.importOptions},
		{"security_profile_json", revision.SecurityProfile, &result.securityProfile},
		{"change_summary_json", revision.ChangeSummary, &result.changeSummary},
		{"secret_bindings_json", revision.SecretBindings, &result.secretBindings},
		{"canonical_script_json", revision.CanonicalScript, &result.canonicalScript},
	}
	for _, field := range fields {
		encoded, err := marshalStorageJSON(field.value)
		if err != nil {
			return storedBrunoRevisionJSON{}, fmt.Errorf("marshal Bruno revision %s: %w", field.name, err)
		}
		*field.dst = encoded
	}
	return result, nil
}

func marshalStorageJSON(value any) (string, error) {
	if value == nil {
		return "{}", nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if string(encoded) == "null" {
		return "{}", nil
	}
	return string(encoded), nil
}

func unmarshalStorageJSON(raw string, target any) error {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	return json.Unmarshal([]byte(raw), target)
}

func cloneBrunoScript(script *dsl.Script) *dsl.Script {
	if script == nil {
		return nil
	}
	payload, _ := json.Marshal(script)
	var clone dsl.Script
	_ = json.Unmarshal(payload, &clone)
	return &clone
}

func cloneBrunoRevision(revision *BrunoSourceRevision) *BrunoSourceRevision {
	if revision == nil {
		return nil
	}
	payload, _ := json.Marshal(revision)
	var clone BrunoSourceRevision
	_ = json.Unmarshal(payload, &clone)
	return &clone
}

func cloneBrunoArtifact(artifact *BrunoSourceArtifact) *BrunoSourceArtifact {
	if artifact == nil {
		return nil
	}
	payload, _ := json.Marshal(artifact)
	var clone BrunoSourceArtifact
	_ = json.Unmarshal(payload, &clone)
	return &clone
}

func firstNonEmptyStorage(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
