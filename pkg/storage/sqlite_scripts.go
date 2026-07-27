package storage

// SQLite store — scripts. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func (s *SQLiteStore) SaveScript(script *dsl.Script) error {
	if script == nil {
		return errors.New("script is required")
	}
	if strings.TrimSpace(script.ID) == "" {
		return errors.New("script id is required")
	}
	script.ProjectID = normalizeProjectID(strings.TrimSpace(script.ProjectID))
	encoded, err := marshalScriptForStorage(script)
	if err != nil {
		return err
	}

	if script.CreatedAt.IsZero() {
		script.CreatedAt = time.Now().UTC()
	}
	script.UpdatedAt = time.Now().UTC()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateScriptFolderProjectTx(tx, script.ProjectID, script.FolderID); err != nil {
		return err
	}
	if err := validateScriptObjectRepositoryProjectsTx(tx, script.ProjectID, script); err != nil {
		return err
	}
	var existingProjectID string
	if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM scripts WHERE id = ?`, script.ID).Scan(&existingProjectID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	} else if err == nil && normalizeProjectID(existingProjectID) != script.ProjectID {
		return fmt.Errorf("script %q belongs to another project", script.ID)
	}

	query := `INSERT INTO scripts (id, ref_key, name, protocol, version, project_id, folder_id, generated_by, generated_from, metadata_json, config, data_sources, parameters, init, actions, teardown, created_at, updated_at)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(id) DO UPDATE SET
			  ref_key=CASE WHEN TRIM(COALESCE(excluded.ref_key, '')) = '' THEN scripts.ref_key ELSE excluded.ref_key END,
			  name=excluded.name, protocol=excluded.protocol, version=excluded.version,
			  folder_id=excluded.folder_id,
			  generated_by=excluded.generated_by, generated_from=excluded.generated_from,
			  metadata_json=excluded.metadata_json,
			  config=excluded.config, data_sources=excluded.data_sources, parameters=excluded.parameters,
			  init=excluded.init, actions=excluded.actions, teardown=excluded.teardown,
			  updated_at=excluded.updated_at
			  WHERE scripts.project_id=excluded.project_id`

	result, err := tx.Exec(query,
		script.ID, script.RefKey, script.Name, script.Protocol, script.Version, script.ProjectID, script.FolderID,
		script.GeneratedBy, script.GeneratedFrom,
		encoded.metadata, encoded.config, encoded.dataSources, encoded.parameters, encoded.init, encoded.actions, encoded.teardown,
		script.CreatedAt, script.UpdatedAt)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return fmt.Errorf("script %q changed concurrently", script.ID)
	}
	return tx.Commit()
}

// CreateScriptIfAbsent atomically reserves a script ID without permitting an
// UPSERT to rewrite an existing library row. Approval flows use this create-only
// primitive because their deterministic ID is observable and may be targeted
// by a concurrent controller or client.
func (s *SQLiteStore) CreateScriptIfAbsent(script *dsl.Script) (bool, error) {
	if script == nil {
		return false, errors.New("script is required")
	}
	if strings.TrimSpace(script.ID) == "" {
		return false, errors.New("script id is required")
	}
	script.ProjectID = normalizeProjectID(strings.TrimSpace(script.ProjectID))
	encoded, err := marshalScriptForStorage(script)
	if err != nil {
		return false, err
	}
	if script.CreatedAt.IsZero() {
		script.CreatedAt = time.Now().UTC()
	}
	script.UpdatedAt = time.Now().UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := validateScriptFolderProjectTx(tx, script.ProjectID, script.FolderID); err != nil {
		return false, err
	}
	if err := validateScriptObjectRepositoryProjectsTx(tx, script.ProjectID, script); err != nil {
		return false, err
	}
	result, err := tx.Exec(`INSERT INTO scripts (id, ref_key, name, protocol, version, project_id, folder_id, generated_by, generated_from, metadata_json, config, data_sources, parameters, init, actions, teardown, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		script.ID, script.RefKey, script.Name, script.Protocol, script.Version, script.ProjectID, script.FolderID,
		script.GeneratedBy, script.GeneratedFrom,
		encoded.metadata, encoded.config, encoded.dataSources, encoded.parameters, encoded.init, encoded.actions, encoded.teardown,
		script.CreatedAt, script.UpdatedAt)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// UpdateScriptIfUnchanged atomically replaces a script only when the caller's
// durable version is still current. Library synchronization uses this boundary
// to avoid overwriting an editor's concurrent update after conflict preflight.
func (s *SQLiteStore) UpdateScriptIfUnchanged(script *dsl.Script, expectedUpdatedAt time.Time) (bool, error) {
	return s.updateScriptIfUnchanged(script, expectedUpdatedAt, "", "", time.Time{})
}

// UpdateScriptIfUnchangedAndCorrelationRulesetCurrent atomically fences a
// derived script update on both the script revision and the correlation
// ruleset revision that produced it. The ruleset row is locked in the same
// transaction as the script CAS, closing the re-read/commit window where a
// concurrent editor could otherwise change or replace the ruleset after apply
// preview work had completed.
func (s *SQLiteStore) UpdateScriptIfUnchangedAndCorrelationRulesetCurrent(
	script *dsl.Script,
	expectedUpdatedAt time.Time,
	rulesetID string,
	expectedRulesetProjectID string,
	expectedRulesetUpdatedAt time.Time,
) (bool, error) {
	rulesetID = strings.TrimSpace(rulesetID)
	if rulesetID == "" || expectedRulesetUpdatedAt.IsZero() {
		return false, errors.New("correlation ruleset id and expected updated_at are required")
	}
	return s.updateScriptIfUnchanged(
		script,
		expectedUpdatedAt,
		rulesetID,
		normalizeProjectID(expectedRulesetProjectID),
		expectedRulesetUpdatedAt,
	)
}

func (s *SQLiteStore) updateScriptIfUnchanged(
	script *dsl.Script,
	expectedUpdatedAt time.Time,
	rulesetID string,
	expectedRulesetProjectID string,
	expectedRulesetUpdatedAt time.Time,
) (bool, error) {
	if script == nil {
		return false, errors.New("script is required")
	}
	if strings.TrimSpace(script.ID) == "" || expectedUpdatedAt.IsZero() {
		return false, errors.New("script id and expected updated_at are required")
	}
	script.ProjectID = normalizeProjectID(strings.TrimSpace(script.ProjectID))
	if rulesetID != "" && expectedRulesetProjectID != script.ProjectID {
		return false, errors.New("correlation ruleset belongs to another project")
	}
	encoded, err := marshalScriptForStorage(script)
	if err != nil {
		return false, err
	}
	if script.CreatedAt.IsZero() {
		return false, errors.New("script created_at is required for conditional update")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := validateScriptFolderProjectTx(tx, script.ProjectID, script.FolderID); err != nil {
		return false, err
	}
	if err := validateScriptObjectRepositoryProjectsTx(tx, script.ProjectID, script); err != nil {
		return false, err
	}
	if rulesetID != "" {
		rulesetQuery := `SELECT COALESCE(project_id, ''), updated_at
			FROM correlation_rulesets WHERE id = ?`
		if s.db.dialect == storeDialectPostgres {
			rulesetQuery += ` FOR UPDATE`
		}
		var currentRulesetProjectID string
		var currentRulesetUpdatedAt time.Time
		if err := tx.QueryRow(rulesetQuery, rulesetID).Scan(
			&currentRulesetProjectID,
			&currentRulesetUpdatedAt,
		); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return false, err
		}
		if normalizeProjectID(currentRulesetProjectID) != expectedRulesetProjectID ||
			!currentRulesetUpdatedAt.Equal(expectedRulesetUpdatedAt) {
			return false, nil
		}
	}
	var currentUpdatedAt time.Time
	var currentUpdatedRaw, currentProjectID string
	if err := tx.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT), project_id FROM scripts WHERE id = ?`, script.ID).Scan(&currentUpdatedAt, &currentUpdatedRaw, &currentProjectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if normalizeProjectID(currentProjectID) != script.ProjectID || !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	script.UpdatedAt = time.Now().UTC()
	result, err := tx.Exec(`UPDATE scripts SET
		ref_key = ?, name = ?, protocol = ?, version = ?, folder_id = ?,
		generated_by = ?, generated_from = ?, metadata_json = ?, config = ?, data_sources = ?, parameters = ?, init = ?, actions = ?, teardown = ?, updated_at = ?
		WHERE id = ? AND project_id = ? AND CAST(updated_at AS TEXT) = ?`,
		script.RefKey, script.Name, script.Protocol, script.Version, script.FolderID,
		script.GeneratedBy, script.GeneratedFrom,
		encoded.metadata, encoded.config, encoded.dataSources, encoded.parameters, encoded.init, encoded.actions, encoded.teardown,
		script.UpdatedAt, script.ID, script.ProjectID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func validateScriptFolderProjectTx(executor testSaveExecutor, projectID string, folderID *string) error {
	projectID = normalizeProjectID(strings.TrimSpace(projectID))
	if err := lockRequiredPlacementParent(executor,
		`UPDATE projects SET updated_at = updated_at WHERE id = ?`, projectID, "script", "project"); err != nil {
		return err
	}
	if folderID == nil {
		return nil
	}
	id := strings.TrimSpace(*folderID)
	if id == "" {
		return errors.New("script folder id must be non-empty when set")
	}
	if err := lockRequiredPlacementParent(executor,
		`UPDATE folders SET updated_at = updated_at WHERE id = ?`, id, "script", "folder"); err != nil {
		return err
	}
	var folderProjectID string
	if err := executor.QueryRow(`SELECT project_id FROM folders WHERE id = ?`, id).Scan(&folderProjectID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("script folder %q not found", id)
		}
		return err
	}
	if normalizeProjectID(folderProjectID) != normalizeProjectID(projectID) {
		return fmt.Errorf("script folder %q belongs to another project", id)
	}
	return nil
}

func validateScriptObjectRepositoryProjectsTx(tx *storeTx, projectID string, script *dsl.Script) error {
	if script == nil {
		return nil
	}
	repositoryIDs := qualifiedObjectRepositoryIDs(script)
	orderedIDs := make([]string, 0, len(repositoryIDs))
	for repositoryID := range repositoryIDs {
		orderedIDs = append(orderedIDs, repositoryID)
	}
	sort.Strings(orderedIDs)
	for _, repositoryID := range orderedIDs {
		if err := lockRequiredPlacementParent(tx,
			`UPDATE object_repositories SET updated_at = updated_at WHERE id = ?`, repositoryID, "script", "object repository"); err != nil {
			return err
		}
		var repositoryProjectID string
		if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM object_repositories WHERE id = ?`, repositoryID).Scan(&repositoryProjectID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("object repository %q referenced by script does not exist", repositoryID)
			}
			return err
		}
		if normalizeProjectID(repositoryProjectID) != normalizeProjectID(projectID) {
			return fmt.Errorf("object repository %q belongs to another project", repositoryID)
		}
	}
	return nil
}

func qualifiedObjectRepositoryIDs(script *dsl.Script) map[string]struct{} {
	out := map[string]struct{}{}
	var visit func([]dsl.Action)
	visit = func(actions []dsl.Action) {
		for i := range actions {
			ref := strings.TrimSpace(actions[i].ObjectRef)
			if repositoryID, _, qualified := strings.Cut(ref, ":"); qualified {
				repositoryID = strings.TrimSpace(repositoryID)
				if strings.HasPrefix(repositoryID, "object-repo-") {
					out[repositoryID] = struct{}{}
				}
			}
			if actions[i].Control != nil {
				visit(actions[i].Control.Steps)
				visit(actions[i].Control.Else)
			}
		}
	}
	visit(script.Init)
	visit(script.Actions)
	visit(script.Teardown)
	return out
}

// DeleteScriptIfUnchanged conditionally removes a script and its dependent
// rows. It is used only to compensate a failed multi-item library import.
func (s *SQLiteStore) DeleteScriptIfUnchanged(id string, expectedUpdatedAt time.Time) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" || expectedUpdatedAt.IsZero() {
		return false, errors.New("script id and expected updated_at are required")
	}
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := s.db.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT) FROM scripts WHERE id = ?`, id).Scan(&currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Lock the parent scripts row before touching dependent rows so this
	// transaction and SaveScriptRecording (which locks the parent first, then
	// upserts script_recordings) acquire row locks in the same order on
	// PostgreSQL; the opposite order can deadlock. See DeleteScript.
	lockQuery := `SELECT CAST(updated_at AS TEXT) FROM scripts WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		lockQuery += ` FOR UPDATE`
	}
	var lockedUpdatedRaw string
	if err := tx.QueryRow(lockQuery, id).Scan(&lockedUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if lockedUpdatedRaw != currentUpdatedRaw {
		return false, nil
	}
	if _, err := tx.Exec(`DELETE FROM script_recordings WHERE script_id = ? AND EXISTS (
		SELECT 1 FROM scripts WHERE id = ? AND CAST(updated_at AS TEXT) = ?
	)`, id, id, currentUpdatedRaw); err != nil {
		return false, err
	}
	result, err := tx.Exec(`DELETE FROM import_source_revisions WHERE script_id = ? AND EXISTS (
		SELECT 1 FROM scripts WHERE id = ? AND CAST(updated_at AS TEXT) = ?
	)`, id, id, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	_ = result
	result, err = tx.Exec(`DELETE FROM scripts WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, id, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	if _, err := tx.Exec(deleteUnreferencedImportArtifactsSQL); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

type storedScriptJSON struct {
	metadata    string
	config      string
	dataSources string
	parameters  string
	init        string
	actions     string
	teardown    string
}

func marshalScriptForStorage(script *dsl.Script) (storedScriptJSON, error) {
	fields := []struct {
		name  string
		value any
		dst   *string
	}{
		{name: "metadata_json", value: script.Metadata},
		{name: "config", value: script.Config},
		{name: "data_sources", value: script.DataSources},
		{name: "parameters", value: script.Parameters},
		{name: "init", value: script.Init},
		{name: "actions", value: script.Actions},
		{name: "teardown", value: script.Teardown},
	}
	var encoded storedScriptJSON
	fields[0].dst = &encoded.metadata
	fields[1].dst = &encoded.config
	fields[2].dst = &encoded.dataSources
	fields[3].dst = &encoded.parameters
	fields[4].dst = &encoded.init
	fields[5].dst = &encoded.actions
	fields[6].dst = &encoded.teardown
	for _, field := range fields {
		payload, err := json.Marshal(field.value)
		if err != nil {
			return storedScriptJSON{}, fmt.Errorf("marshal script %s: %w", field.name, err)
		}
		*field.dst = string(payload)
	}
	return encoded, nil
}

func (s *SQLiteStore) GetScript(id string) (*dsl.Script, error) {
	var script dsl.Script
	var metadataJSON, configJSON, dataSourcesJSON, parametersJSON, initJSON, actionsJSON, teardownJSON string

	query := `SELECT id, COALESCE(ref_key, ''), name, protocol, version, COALESCE(project_id, ''), folder_id, generated_by, generated_from, COALESCE(metadata_json, ''), config, data_sources, COALESCE(parameters, ''), init, actions, teardown, created_at, updated_at
			  FROM scripts WHERE id = ?`
	err := s.db.QueryRow(query, id).Scan(
		&script.ID, &script.RefKey, &script.Name, &script.Protocol, &script.Version,
		&script.ProjectID, &script.FolderID,
		&script.GeneratedBy, &script.GeneratedFrom,
		&metadataJSON, &configJSON, &dataSourcesJSON, &parametersJSON, &initJSON, &actionsJSON, &teardownJSON,
		&script.CreatedAt, &script.UpdatedAt)
	if err != nil {
		return nil, err
	}

	if err := decodeStoredScriptJSON(&script, metadataJSON, configJSON, dataSourcesJSON, parametersJSON, initJSON, actionsJSON, teardownJSON); err != nil {
		return nil, fmt.Errorf("decode script %s: %w", script.ID, err)
	}

	return &script, nil
}

func (s *SQLiteStore) ListScripts(projectID string, folderID *string) ([]*dsl.Script, error) {
	args := []any{}
	query := `SELECT id, COALESCE(ref_key, ''), name, protocol, version, COALESCE(project_id, ''), folder_id, generated_by, generated_from, COALESCE(metadata_json, ''), config, data_sources, COALESCE(parameters, ''), init, actions, teardown, created_at, updated_at FROM scripts`
	var filters []string
	if projectID != "" {
		filters = append(filters, `project_id = ?`)
		args = append(args, projectID)
	}
	if folderID != nil {
		if *folderID == "" {
			filters = append(filters, `folder_id IS NULL`)
		} else {
			filters = append(filters, `folder_id = ?`)
			args = append(args, *folderID)
		}
	}
	if len(filters) > 0 {
		query += ` WHERE ` + strings.Join(filters, ` AND `)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scripts []*dsl.Script
	for rows.Next() {
		var script dsl.Script
		var metadataJSON, configJSON, dataSourcesJSON, parametersJSON, initJSON, actionsJSON, teardownJSON string
		err := rows.Scan(
			&script.ID, &script.RefKey, &script.Name, &script.Protocol, &script.Version,
			&script.ProjectID, &script.FolderID,
			&script.GeneratedBy, &script.GeneratedFrom,
			&metadataJSON, &configJSON, &dataSourcesJSON, &parametersJSON, &initJSON, &actionsJSON, &teardownJSON,
			&script.CreatedAt, &script.UpdatedAt)
		if err != nil {
			return nil, err
		}
		if err := decodeStoredScriptJSON(&script, metadataJSON, configJSON, dataSourcesJSON, parametersJSON, initJSON, actionsJSON, teardownJSON); err != nil {
			return nil, fmt.Errorf("decode script %s: %w", script.ID, err)
		}
		scripts = append(scripts, &script)
	}
	return scripts, rows.Err()
}

func (s *SQLiteStore) DeleteScript(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Lock the parent before removing dependent integration rows. A concurrent
	// GitHub link save takes the same row lock, so it cannot insert a new link
	// after this transaction has already passed its dependent-row cleanup.
	lockQuery := `SELECT id FROM scripts WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		lockQuery += ` FOR UPDATE`
	}
	var lockedID string
	if err := tx.QueryRow(lockQuery, id).Scan(&lockedID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	for _, deletion := range []struct {
		query string
		args  []any
	}{
		{query: `DELETE FROM github_check_links WHERE script_id = ?`, args: []any{id}},
		{query: `DELETE FROM github_script_links WHERE script_id = ?`, args: []any{id}},
		{query: `DELETE FROM script_recordings WHERE script_id = ?`, args: []any{id}},
		{query: `DELETE FROM import_source_revisions WHERE script_id = ?`, args: []any{id}},
		{query: deleteUnreferencedImportArtifactsSQL},
		{query: `DELETE FROM scripts WHERE id = ?`, args: []any{id}},
	} {
		if _, err := tx.Exec(deletion.query, deletion.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func decodeStoredScriptJSON(script *dsl.Script, metadataJSON, configJSON, dataSourcesJSON, parametersJSON, initJSON, actionsJSON, teardownJSON string) error {
	if script == nil {
		return errors.New("script is required")
	}
	fields := []struct {
		name     string
		raw      string
		dst      any
		optional bool
	}{
		{name: "metadata_json", raw: metadataJSON, dst: &script.Metadata, optional: true},
		{name: "config", raw: configJSON, dst: &script.Config},
		{name: "data_sources", raw: dataSourcesJSON, dst: &script.DataSources},
		{name: "parameters", raw: parametersJSON, dst: &script.Parameters, optional: true},
		{name: "init", raw: initJSON, dst: &script.Init},
		{name: "actions", raw: actionsJSON, dst: &script.Actions},
		{name: "teardown", raw: teardownJSON, dst: &script.Teardown},
	}
	for _, field := range fields {
		if field.optional && (field.raw == "" || field.raw == "null") {
			continue
		}
		if err := json.Unmarshal([]byte(field.raw), field.dst); err != nil {
			return fmt.Errorf("%s: %w", field.name, err)
		}
	}
	return nil
}

func (s *SQLiteStore) MoveScript(id string, folderID *string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var projectID string
	if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM scripts WHERE id = ?`, id).Scan(&projectID); err != nil {
		return err
	}
	if err := validateScriptFolderProjectTx(tx, projectID, folderID); err != nil {
		return err
	}
	result, err := tx.Exec(`UPDATE scripts SET folder_id = ?, updated_at = ? WHERE id = ? AND project_id = ?`, folderID, time.Now(), id, projectID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveAuthoringSession(session *AuthoringSession) error {
	if session == nil {
		return errors.New("authoring session is required")
	}
	session.ID = strings.TrimSpace(session.ID)
	if session.ID == "" {
		return errors.New("authoring session id is required")
	}
	session.Status = strings.ToLower(strings.TrimSpace(session.Status))
	session.ProjectID = normalizeProjectID(strings.TrimSpace(session.ProjectID))
	if session.Draft != nil {
		if strings.TrimSpace(session.Draft.ProjectID) == "" {
			session.Draft.ProjectID = session.ProjectID
		} else if normalizeProjectID(strings.TrimSpace(session.Draft.ProjectID)) != session.ProjectID {
			return errors.New("authoring session draft belongs to another project")
		}
	}
	now := authoringSessionTimestamp(time.Now())
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	} else {
		session.CreatedAt = authoringSessionTimestamp(session.CreatedAt)
	}
	session.UpdatedAt = now
	if session.LastAccessedAt.IsZero() {
		session.LastAccessedAt = now
	} else {
		session.LastAccessedAt = authoringSessionTimestamp(session.LastAccessedAt)
	}
	draftJSON, err := json.Marshal(session.Draft)
	if err != nil {
		return fmt.Errorf("marshal authoring draft: %w", err)
	}
	reviewStateJSON, err := json.Marshal(session.ReviewState)
	if err != nil {
		return fmt.Errorf("marshal authoring review state: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if session.ProjectID, err = lockProjectForOwnedMutation(tx, session.ProjectID); err != nil {
		return err
	}
	result, err := tx.Exec(`INSERT INTO authoring_sessions (id, project_id, source, name, status, draft_json, review_state_json, created_at, updated_at, last_accessed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			source=excluded.source,
			name=excluded.name,
			status=excluded.status,
			draft_json=excluded.draft_json,
			review_state_json=excluded.review_state_json,
			updated_at=excluded.updated_at,
			last_accessed_at=excluded.last_accessed_at
		WHERE COALESCE(NULLIF(authoring_sessions.project_id, ''), ?) = excluded.project_id
			AND LOWER(TRIM(authoring_sessions.status)) NOT IN ('approving', 'approved')`,
		session.ID, session.ProjectID, session.Source, session.Name, session.Status, string(draftJSON), string(reviewStateJSON), session.CreatedAt, session.UpdatedAt, session.LastAccessedAt, DefaultProjectID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		var currentProjectID, currentStatus string
		if err := tx.QueryRow(`SELECT COALESCE(project_id, ''), status FROM authoring_sessions WHERE id = ?`, session.ID).Scan(&currentProjectID, &currentStatus); err != nil {
			return err
		}
		if normalizeProjectID(currentProjectID) != session.ProjectID {
			return fmt.Errorf("authoring session %q belongs to another project", session.ID)
		}
		switch strings.ToLower(strings.TrimSpace(currentStatus)) {
		case "approving":
			return fmt.Errorf("authoring session %q approval is in progress", session.ID)
		case "approved":
			return fmt.Errorf("approved authoring session %q is immutable", session.ID)
		default:
			return fmt.Errorf("authoring session %q was not saved", session.ID)
		}
	}
	return tx.Commit()
}

// CreateAuthoringSession inserts one authoring session without replacing an
// existing row. API idempotency derives a stable session ID from the
// project-scoped request key; this insert-only primitive makes concurrent
// controller replicas converge on the first durable review artifact.
func (s *SQLiteStore) CreateAuthoringSession(session *AuthoringSession) (bool, error) {
	if session == nil {
		return false, errors.New("authoring session is required")
	}
	session.ID = strings.TrimSpace(session.ID)
	if session.ID == "" {
		return false, errors.New("authoring session id is required")
	}
	session.Status = strings.ToLower(strings.TrimSpace(session.Status))
	session.ProjectID = normalizeProjectID(strings.TrimSpace(session.ProjectID))
	if session.Draft != nil {
		if strings.TrimSpace(session.Draft.ProjectID) == "" {
			session.Draft.ProjectID = session.ProjectID
		} else if normalizeProjectID(strings.TrimSpace(session.Draft.ProjectID)) != session.ProjectID {
			return false, errors.New("authoring session draft belongs to another project")
		}
	}
	now := authoringSessionTimestamp(time.Now())
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	} else {
		session.CreatedAt = authoringSessionTimestamp(session.CreatedAt)
	}
	session.UpdatedAt = now
	if session.LastAccessedAt.IsZero() {
		session.LastAccessedAt = now
	} else {
		session.LastAccessedAt = authoringSessionTimestamp(session.LastAccessedAt)
	}
	draftJSON, err := json.Marshal(session.Draft)
	if err != nil {
		return false, fmt.Errorf("marshal authoring draft: %w", err)
	}
	reviewStateJSON, err := json.Marshal(session.ReviewState)
	if err != nil {
		return false, fmt.Errorf("marshal authoring review state: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if session.ProjectID, err = lockProjectForOwnedMutation(tx, session.ProjectID); err != nil {
		return false, err
	}
	result, err := tx.Exec(`INSERT INTO authoring_sessions (id, project_id, source, name, status, draft_json, review_state_json, created_at, updated_at, last_accessed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		session.ID, session.ProjectID, session.Source, session.Name, session.Status, string(draftJSON), string(reviewStateJSON), session.CreatedAt, session.UpdatedAt, session.LastAccessedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return affected == 1, nil
}

// CompareAndSwapAuthoringSession persists a session only while the durable row
// still has the version and status the caller read. Authoring approval is a
// multi-step saga; this CAS prevents a concurrent edit, delete, or second
// approval from replacing its durable intent with a stale session snapshot.
func (s *SQLiteStore) CompareAndSwapAuthoringSession(session *AuthoringSession, expectedUpdatedAt time.Time, expectedStatus string) (bool, error) {
	if session == nil {
		return false, errors.New("authoring session is required")
	}
	session.ID = strings.TrimSpace(session.ID)
	if session.ID == "" {
		return false, errors.New("authoring session id is required")
	}
	if strings.EqualFold(strings.TrimSpace(expectedStatus), "approved") {
		return false, nil
	}
	session.Status = strings.ToLower(strings.TrimSpace(session.Status))
	session.ProjectID = normalizeProjectID(strings.TrimSpace(session.ProjectID))
	if session.Draft != nil {
		if strings.TrimSpace(session.Draft.ProjectID) == "" {
			session.Draft.ProjectID = session.ProjectID
		} else if normalizeProjectID(strings.TrimSpace(session.Draft.ProjectID)) != session.ProjectID {
			return false, errors.New("authoring session draft belongs to another project")
		}
	}
	now := authoringSessionTimestamp(time.Now())
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	} else {
		session.CreatedAt = authoringSessionTimestamp(session.CreatedAt)
	}
	session.UpdatedAt = now
	if session.LastAccessedAt.IsZero() {
		session.LastAccessedAt = now
	} else {
		session.LastAccessedAt = authoringSessionTimestamp(session.LastAccessedAt)
	}
	draftJSON, err := json.Marshal(session.Draft)
	if err != nil {
		return false, fmt.Errorf("marshal authoring draft: %w", err)
	}
	reviewStateJSON, err := json.Marshal(session.ReviewState)
	if err != nil {
		return false, fmt.Errorf("marshal authoring review state: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if session.ProjectID, err = lockProjectForOwnedMutation(tx, session.ProjectID); err != nil {
		return false, err
	}
	result, err := tx.Exec(`UPDATE authoring_sessions SET
		source = ?, name = ?, status = ?, draft_json = ?, review_state_json = ?,
		updated_at = ?, last_accessed_at = ?
		WHERE id = ? AND updated_at = ? AND status = ?
		AND COALESCE(NULLIF(project_id, ''), ?) = ?`,
		session.Source, session.Name, session.Status, string(draftJSON), string(reviewStateJSON),
		session.UpdatedAt, session.LastAccessedAt, session.ID, expectedUpdatedAt, expectedStatus, DefaultProjectID, session.ProjectID)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func authoringSessionTimestamp(value time.Time) time.Time {
	return value.UTC().Truncate(time.Microsecond)
}

// DeleteAuthoringSessionUnlessApproving fences deletion against the durable
// approval intent and its finalized approval evidence. A caller that loses an
// in-progress race may retry after approval is rolled back; approved sessions
// remain retained for approval verification.
func (s *SQLiteStore) DeleteAuthoringSessionUnlessApproving(id string) (bool, error) {
	result, err := s.db.Exec(`DELETE FROM authoring_sessions WHERE id = ? AND LOWER(TRIM(status)) NOT IN ('approving', 'approved')`, id)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (s *SQLiteStore) GetAuthoringSession(id string) (*AuthoringSession, error) {
	var session AuthoringSession
	var draftJSON, reviewStateJSON string
	err := s.db.QueryRow(`SELECT id, COALESCE(project_id, ''), source, name, status, COALESCE(draft_json, ''), COALESCE(review_state_json, ''), created_at, updated_at, last_accessed_at FROM authoring_sessions WHERE id = ?`, id).Scan(
		&session.ID, &session.ProjectID, &session.Source, &session.Name, &session.Status, &draftJSON, &reviewStateJSON, &session.CreatedAt, &session.UpdatedAt, &session.LastAccessedAt)
	if err != nil {
		return nil, err
	}
	if err := decodeAuthoringSessionJSON(&session, draftJSON, reviewStateJSON); err != nil {
		return nil, fmt.Errorf("decode authoring session %s: %w", id, err)
	}
	return &session, nil
}

// GetAuthoringSessionProjectID is a side-effect-free ownership lookup for
// authorization middleware. In particular, it must not advance last_accessed_at
// before the caller has been authorized for the session's project.
func (s *SQLiteStore) GetAuthoringSessionProjectID(id string) (string, error) {
	var projectID string
	if err := s.db.QueryRow(`SELECT COALESCE(project_id, '') FROM authoring_sessions WHERE id = ?`, strings.TrimSpace(id)).Scan(&projectID); err != nil {
		return "", err
	}
	return normalizeProjectID(projectID), nil
}

// TouchAuthoringSessionAccess records access only after the API handler has
// passed project authorization. Ownership resolution uses the read-only method
// above and therefore cannot create an unauthorized durable side effect.
func (s *SQLiteStore) TouchAuthoringSessionAccess(id string, accessedAt time.Time) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, errors.New("authoring session id is required")
	}
	accessedAt = authoringSessionTimestamp(accessedAt)
	result, err := s.db.Exec(`UPDATE authoring_sessions SET last_accessed_at = ? WHERE id = ?`, accessedAt, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *SQLiteStore) ListAuthoringSessions(projectID string) ([]*AuthoringSession, error) {
	return s.listAuthoringSessions(projectID, 0, 0)
}

func (s *SQLiteStore) ListAuthoringSessionsPage(projectID string, limit, offset int) ([]*AuthoringSession, error) {
	if limit <= 0 {
		return []*AuthoringSession{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	return s.listAuthoringSessions(projectID, limit, offset)
}

func (s *SQLiteStore) listAuthoringSessions(projectID string, limit, offset int) ([]*AuthoringSession, error) {
	args := []any{}
	query := `SELECT id, COALESCE(project_id, ''), source, name, status, COALESCE(draft_json, ''), COALESCE(review_state_json, ''), created_at, updated_at, last_accessed_at FROM authoring_sessions`
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, normalizeProjectID(projectID))
	}
	query += ` ORDER BY updated_at DESC`
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := make([]*AuthoringSession, 0)
	for rows.Next() {
		var session AuthoringSession
		var draftJSON, reviewStateJSON string
		if err := rows.Scan(&session.ID, &session.ProjectID, &session.Source, &session.Name, &session.Status, &draftJSON, &reviewStateJSON, &session.CreatedAt, &session.UpdatedAt, &session.LastAccessedAt); err != nil {
			return nil, err
		}
		if err := decodeAuthoringSessionJSON(&session, draftJSON, reviewStateJSON); err != nil {
			return nil, fmt.Errorf("decode authoring session %s: %w", session.ID, err)
		}
		sessions = append(sessions, &session)
	}
	return sessions, nil
}

func (s *SQLiteStore) DeleteAuthoringSession(id string) error {
	// Final approval evidence is immutable even for direct storage callers.
	// Project deletion remains the explicit administrative lifecycle boundary
	// and removes dependent sessions through its transaction.
	result, err := s.db.Exec(`DELETE FROM authoring_sessions WHERE id = ? AND LOWER(TRIM(status)) NOT IN ('approving', 'approved')`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 1 {
		return err
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM authoring_sessions WHERE id = ?`, id).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "approving":
		return fmt.Errorf("authoring session %q approval is in progress", id)
	case "approved":
		return fmt.Errorf("approved authoring session %q is immutable", id)
	default:
		return fmt.Errorf("authoring session %q was not deleted because it changed concurrently", id)
	}
}

func decodeAuthoringSessionJSON(session *AuthoringSession, draftJSON string, reviewStateJSON string) error {
	if draftJSON != "" && draftJSON != "null" {
		var script dsl.Script
		if err := json.Unmarshal([]byte(draftJSON), &script); err != nil {
			return fmt.Errorf("decode draft: %w", err)
		}
		session.Draft = &script
	}
	if reviewStateJSON != "" && reviewStateJSON != "null" {
		if err := json.Unmarshal([]byte(reviewStateJSON), &session.ReviewState); err != nil {
			return fmt.Errorf("decode review state: %w", err)
		}
	}
	return nil
}

// deleteUnreferencedImportArtifactsSQL removes encrypted source artifacts that
// no longer have a referrer, and is shared by every script-delete path.
//
// It is a single constant on purpose. The statement used to be written out at
// each delete site, so a table added to one copy and not the other would let a
// script delete silently remove artifacts a live import batch still points at —
// the failure would surface much later as an unreadable source or a failed
// rollback, with nothing tying it back to the delete. Any new table that
// references import_source_artifacts must be added to this UNION, and
// TestScriptDeleteKeepsArtifactsReferencedByAnImportBatch covers it.
const deleteUnreferencedImportArtifactsSQL = `DELETE FROM import_source_artifacts WHERE id NOT IN (
	SELECT raw_artifact_id FROM import_source_revisions
	UNION SELECT sanitized_artifact_id FROM import_source_revisions
	UNION SELECT raw_artifact_id FROM import_batches
	UNION SELECT sanitized_artifact_id FROM import_batches
)`
