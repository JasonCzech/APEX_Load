package storage

// SQLite store — scripts. Split from sqlite.go (same package, behavior-preserving).

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func (s *SQLiteStore) SaveScript(script *dsl.Script) error {
	script.ProjectID = normalizeProjectID(script.ProjectID)
	metadataJSON, _ := json.Marshal(script.Metadata)
	configJSON, _ := json.Marshal(script.Config)
	dataSourcesJSON, _ := json.Marshal(script.DataSources)
	parametersJSON, _ := json.Marshal(script.Parameters)
	initJSON, _ := json.Marshal(script.Init)
	actionsJSON, _ := json.Marshal(script.Actions)
	teardownJSON, _ := json.Marshal(script.Teardown)

	if script.CreatedAt.IsZero() {
		script.CreatedAt = time.Now()
	}
	script.UpdatedAt = time.Now()

	query := `INSERT INTO scripts (id, ref_key, name, protocol, version, project_id, folder_id, generated_by, generated_from, metadata_json, config, data_sources, parameters, init, actions, teardown, created_at, updated_at)
			  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			  ON CONFLICT(id) DO UPDATE SET
			  ref_key=excluded.ref_key,
			  name=excluded.name, protocol=excluded.protocol, version=excluded.version,
			  project_id=excluded.project_id, folder_id=excluded.folder_id,
			  generated_by=excluded.generated_by, generated_from=excluded.generated_from,
			  metadata_json=excluded.metadata_json,
			  config=excluded.config, data_sources=excluded.data_sources, parameters=excluded.parameters,
			  init=excluded.init, actions=excluded.actions, teardown=excluded.teardown,
			  updated_at=excluded.updated_at`

	_, err := s.db.Exec(query,
		script.ID, script.RefKey, script.Name, script.Protocol, script.Version, script.ProjectID, script.FolderID,
		script.GeneratedBy, script.GeneratedFrom,
		string(metadataJSON), string(configJSON), string(dataSourcesJSON), string(parametersJSON), string(initJSON), string(actionsJSON), string(teardownJSON),
		script.CreatedAt, script.UpdatedAt)
	return err
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

	if metadataJSON != "" && metadataJSON != "null" {
		json.Unmarshal([]byte(metadataJSON), &script.Metadata)
	}
	json.Unmarshal([]byte(configJSON), &script.Config)
	json.Unmarshal([]byte(dataSourcesJSON), &script.DataSources)
	if parametersJSON != "" && parametersJSON != "null" {
		json.Unmarshal([]byte(parametersJSON), &script.Parameters)
	}
	json.Unmarshal([]byte(initJSON), &script.Init)
	json.Unmarshal([]byte(actionsJSON), &script.Actions)
	json.Unmarshal([]byte(teardownJSON), &script.Teardown)

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
		if metadataJSON != "" && metadataJSON != "null" {
			json.Unmarshal([]byte(metadataJSON), &script.Metadata)
		}
		json.Unmarshal([]byte(configJSON), &script.Config)
		json.Unmarshal([]byte(dataSourcesJSON), &script.DataSources)
		if parametersJSON != "" && parametersJSON != "null" {
			json.Unmarshal([]byte(parametersJSON), &script.Parameters)
		}
		json.Unmarshal([]byte(initJSON), &script.Init)
		json.Unmarshal([]byte(actionsJSON), &script.Actions)
		json.Unmarshal([]byte(teardownJSON), &script.Teardown)
		scripts = append(scripts, &script)
	}
	return scripts, nil
}

func (s *SQLiteStore) DeleteScript(id string) error {
	_, err := s.db.Exec(`DELETE FROM scripts WHERE id = ?`, id)
	return err
}

func (s *SQLiteStore) MoveScript(id string, folderID *string) error {
	_, err := s.db.Exec(`UPDATE scripts SET folder_id = ?, updated_at = ? WHERE id = ?`, folderID, time.Now(), id)
	return err
}

func (s *SQLiteStore) SaveAuthoringSession(session *AuthoringSession) error {
	if session == nil {
		return errors.New("authoring session is required")
	}
	session.ProjectID = normalizeProjectID(session.ProjectID)
	now := time.Now()
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	session.UpdatedAt = now
	if session.LastAccessedAt.IsZero() {
		session.LastAccessedAt = now
	}
	draftJSON, err := json.Marshal(session.Draft)
	if err != nil {
		return fmt.Errorf("marshal authoring draft: %w", err)
	}
	reviewStateJSON, err := json.Marshal(session.ReviewState)
	if err != nil {
		return fmt.Errorf("marshal authoring review state: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO authoring_sessions (id, project_id, source, name, status, draft_json, review_state_json, created_at, updated_at, last_accessed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			project_id=excluded.project_id,
			source=excluded.source,
			name=excluded.name,
			status=excluded.status,
			draft_json=excluded.draft_json,
			review_state_json=excluded.review_state_json,
			updated_at=excluded.updated_at,
			last_accessed_at=excluded.last_accessed_at`,
		session.ID, session.ProjectID, session.Source, session.Name, session.Status, string(draftJSON), string(reviewStateJSON), session.CreatedAt, session.UpdatedAt, session.LastAccessedAt)
	return err
}

func (s *SQLiteStore) GetAuthoringSession(id string) (*AuthoringSession, error) {
	var session AuthoringSession
	var draftJSON, reviewStateJSON string
	err := s.db.QueryRow(`SELECT id, COALESCE(project_id, ''), source, name, status, COALESCE(draft_json, ''), COALESCE(review_state_json, ''), created_at, updated_at, last_accessed_at FROM authoring_sessions WHERE id = ?`, id).Scan(
		&session.ID, &session.ProjectID, &session.Source, &session.Name, &session.Status, &draftJSON, &reviewStateJSON, &session.CreatedAt, &session.UpdatedAt, &session.LastAccessedAt)
	if err != nil {
		return nil, err
	}
	decodeAuthoringSessionJSON(&session, draftJSON, reviewStateJSON)
	now := time.Now()
	if _, err := s.db.Exec(`UPDATE authoring_sessions SET last_accessed_at = ? WHERE id = ?`, now, id); err == nil {
		session.LastAccessedAt = now
	}
	return &session, nil
}

func (s *SQLiteStore) ListAuthoringSessions(projectID string) ([]*AuthoringSession, error) {
	args := []any{}
	query := `SELECT id, COALESCE(project_id, ''), source, name, status, COALESCE(draft_json, ''), COALESCE(review_state_json, ''), created_at, updated_at, last_accessed_at FROM authoring_sessions`
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, normalizeProjectID(projectID))
	}
	query += ` ORDER BY updated_at DESC`
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
		decodeAuthoringSessionJSON(&session, draftJSON, reviewStateJSON)
		sessions = append(sessions, &session)
	}
	return sessions, nil
}

func (s *SQLiteStore) DeleteAuthoringSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM authoring_sessions WHERE id = ?`, id)
	return err
}

func decodeAuthoringSessionJSON(session *AuthoringSession, draftJSON string, reviewStateJSON string) {
	if draftJSON != "" && draftJSON != "null" {
		var script dsl.Script
		if err := json.Unmarshal([]byte(draftJSON), &script); err == nil {
			session.Draft = &script
		}
	}
	if reviewStateJSON != "" && reviewStateJSON != "null" {
		_ = json.Unmarshal([]byte(reviewStateJSON), &session.ReviewState)
	}
}
