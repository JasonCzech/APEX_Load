package storage

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CreateExternalResultImportIfAbsent reserves an immutable imported-result
// identity. Library evidence uses deterministic IDs, so an UPSERT here would
// let a retry or another controller silently replace already-imported evidence.
func (s *SQLiteStore) CreateExternalResultImportIfAbsent(result *ExternalResultImport) (bool, error) {
	summaryJSON, rawJSON, archiveRecordJSON, err := prepareExternalResultImportInsert(result)
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	exists, err := validateExternalResultImportPlacement(tx, result, false)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	created, err := tx.Exec(`INSERT INTO external_result_imports (id, provider, name, project_id, application_id, release_id, scenario_id, source, summary_json, raw_json, archive_record_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO NOTHING`,
		result.ID, result.Provider, result.Name, result.ProjectID, nullableString(result.ApplicationID), result.ReleaseID, result.ScenarioID,
		result.Source, summaryJSON, rawJSON, archiveRecordJSON, result.CreatedAt)
	if err != nil {
		return false, err
	}
	rows, err := created.RowsAffected()
	if err != nil || rows != 1 {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// GetExternalResultImport returns one immutable evidence row by identity.
func (s *SQLiteStore) GetExternalResultImport(id string) (*ExternalResultImport, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, errors.New("external result import id is required")
	}
	return scanExternalResultImport(s.db.QueryRow(`SELECT id, provider, name, COALESCE(project_id, ''), COALESCE(application_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(source, ''), COALESCE(summary_json, ''), COALESCE(raw_json, ''), COALESCE(archive_record_json, ''), created_at
		FROM external_result_imports WHERE id = ?`, id))
}

// DeleteExternalResultImportIfRawResult compensates a newly-created bundle
// item only if no other replica has replaced or repaired the evidence payload.
func (s *SQLiteStore) DeleteExternalResultImportIfRawResult(id string, expectedRaw map[string]any) (bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, errors.New("external result import id is required")
	}
	var currentRawJSON string
	if err := s.db.QueryRow(`SELECT raw_json FROM external_result_imports WHERE id = ?`, id).Scan(&currentRawJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	expectedJSON, err := json.Marshal(expectedRaw)
	if err != nil {
		return false, fmt.Errorf("marshal expected external raw result: %w", err)
	}
	equal, err := semanticJSONEqual([]byte(currentRawJSON), expectedJSON)
	if err != nil {
		return false, fmt.Errorf("compare external raw result: %w", err)
	}
	if !equal {
		return false, nil
	}
	result, err := s.db.Exec(`DELETE FROM external_result_imports WHERE id = ? AND raw_json = ?`, id, currentRawJSON)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

// DeleteExternalResultImportIfUnchanged compensates immutable evidence only
// while every persisted field still matches the row created by this import.
// The exact SQL predicates close the compare-to-delete race as well as guarding
// metadata when the raw evidence happens to be identical.
func (s *SQLiteStore) DeleteExternalResultImportIfUnchanged(expected *ExternalResultImport) (bool, error) {
	if expected == nil || strings.TrimSpace(expected.ID) == "" {
		return false, errors.New("expected external result import is required")
	}
	var provider, name, projectID, applicationID, releaseID, scenarioID, source string
	var summaryJSON, rawJSON, archiveRecordJSON, createdAtRaw string
	var createdAt time.Time
	if err := s.db.QueryRow(`SELECT provider, name, COALESCE(project_id, ''), COALESCE(application_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(source, ''), COALESCE(summary_json, ''), COALESCE(raw_json, ''), COALESCE(archive_record_json, ''), created_at, CAST(created_at AS TEXT)
		FROM external_result_imports WHERE id = ?`, strings.TrimSpace(expected.ID)).Scan(
		&provider, &name, &projectID, &applicationID, &releaseID, &scenarioID, &source, &summaryJSON, &rawJSON, &archiveRecordJSON, &createdAt, &createdAtRaw,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	expectedSummaryJSON, err := json.Marshal(expected.Summary)
	if err != nil {
		return false, fmt.Errorf("marshal expected external result summary: %w", err)
	}
	expectedRawJSON, err := json.Marshal(expected.RawResult)
	if err != nil {
		return false, fmt.Errorf("marshal expected external raw result: %w", err)
	}
	expectedArchiveRecordJSON, err := json.Marshal(expected.ArchiveRecord)
	if err != nil {
		return false, fmt.Errorf("marshal expected external archive record: %w", err)
	}
	summaryEqual, err := semanticJSONEqual([]byte(summaryJSON), expectedSummaryJSON)
	if err != nil {
		return false, fmt.Errorf("compare external result summary: %w", err)
	}
	rawEqual, err := semanticJSONEqual([]byte(rawJSON), expectedRawJSON)
	if err != nil {
		return false, fmt.Errorf("compare external raw result: %w", err)
	}
	archiveRecordEqual, err := semanticJSONEqual([]byte(firstNonEmptyStorageJSON(archiveRecordJSON)), expectedArchiveRecordJSON)
	if err != nil {
		return false, fmt.Errorf("compare external archive record: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(provider), strings.TrimSpace(expected.Provider)) ||
		strings.TrimSpace(name) != strings.TrimSpace(expected.Name) ||
		normalizeProjectID(projectID) != normalizeProjectID(expected.ProjectID) ||
		strings.TrimSpace(applicationID) != strings.TrimSpace(expected.ApplicationID) ||
		strings.TrimSpace(releaseID) != strings.TrimSpace(expected.ReleaseID) ||
		strings.TrimSpace(scenarioID) != strings.TrimSpace(expected.ScenarioID) ||
		strings.TrimSpace(source) != strings.TrimSpace(expected.Source) ||
		!summaryEqual || !rawEqual || !archiveRecordEqual || !createdAt.Equal(expected.CreatedAt) {
		return false, nil
	}
	result, err := s.db.Exec(`DELETE FROM external_result_imports
		WHERE id = ? AND provider = ? AND name = ? AND COALESCE(project_id, '') = ?
		AND COALESCE(application_id, '') = ? AND COALESCE(release_id, '') = ? AND COALESCE(scenario_id, '') = ? AND COALESCE(source, '') = ?
		AND COALESCE(summary_json, '') = ? AND COALESCE(raw_json, '') = ? AND COALESCE(archive_record_json, '') = ? AND CAST(created_at AS TEXT) = ?`,
		strings.TrimSpace(expected.ID), provider, name, projectID, applicationID, releaseID, scenarioID, source, summaryJSON, rawJSON, archiveRecordJSON, createdAtRaw)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func semanticJSONEqual(left, right []byte) (bool, error) {
	normalize := func(payload []byte) ([]byte, error) {
		decoder := json.NewDecoder(bytes.NewReader(payload))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	leftCanonical, err := normalize(left)
	if err != nil {
		return false, err
	}
	rightCanonical, err := normalize(right)
	if err != nil {
		return false, err
	}
	return bytes.Equal(leftCanonical, rightCanonical), nil
}

func prepareExternalResultImportInsert(result *ExternalResultImport) (string, string, string, error) {
	if result == nil {
		return "", "", "", errors.New("external result import is required")
	}
	result.ID = strings.TrimSpace(result.ID)
	result.Provider = strings.ToLower(strings.TrimSpace(result.Provider))
	result.Name = strings.TrimSpace(result.Name)
	result.ApplicationID = strings.TrimSpace(result.ApplicationID)
	result.ReleaseID = strings.TrimSpace(result.ReleaseID)
	result.ScenarioID = strings.TrimSpace(result.ScenarioID)
	result.Source = strings.TrimSpace(result.Source)
	if result.ID == "" {
		return "", "", "", errors.New("external result import id is required")
	}
	if result.Provider == "" {
		return "", "", "", errors.New("external result import provider is required")
	}
	if result.Name == "" {
		result.Name = result.Provider + " import"
	}
	result.ProjectID = normalizeProjectID(result.ProjectID)
	if result.CreatedAt.IsZero() {
		result.CreatedAt = time.Now().UTC()
	}
	summaryJSON, err := json.Marshal(result.Summary)
	if err != nil {
		return "", "", "", fmt.Errorf("marshal external result summary: %w", err)
	}
	rawJSON, err := json.Marshal(result.RawResult)
	if err != nil {
		return "", "", "", fmt.Errorf("marshal external raw result: %w", err)
	}
	archiveRecordJSON, err := json.Marshal(result.ArchiveRecord)
	if err != nil {
		return "", "", "", fmt.Errorf("marshal external archive record: %w", err)
	}
	return string(summaryJSON), string(rawJSON), string(archiveRecordJSON), nil
}

func firstNonEmptyStorageJSON(value string) string {
	if strings.TrimSpace(value) == "" {
		return "null"
	}
	return value
}

// validateExternalResultImportPlacement locks every tenant-bearing parent and
// prevents a legacy upsert from moving an existing evidence identity between
// projects. Create-only callers preserve their collision semantics by returning
// immediately when the durable identity already exists.
func validateExternalResultImportPlacement(executor testSaveExecutor, result *ExternalResultImport, rejectRelocation bool) (bool, error) {
	if result == nil {
		return false, errors.New("external result import is required")
	}
	const owner = "external result import"
	if err := lockRequiredPlacementParent(executor,
		`UPDATE projects SET updated_at = updated_at WHERE id = ?`, result.ProjectID, owner, "project"); err != nil {
		return false, err
	}

	locked, err := executor.Exec(`UPDATE external_result_imports SET project_id = project_id WHERE id = ?`, result.ID)
	if err != nil {
		return false, err
	}
	affected, err := locked.RowsAffected()
	if err != nil {
		return false, err
	}
	exists := affected == 1
	if exists {
		var existingProjectID string
		if err := executor.QueryRow(`SELECT COALESCE(project_id, '') FROM external_result_imports WHERE id = ?`, result.ID).Scan(&existingProjectID); err != nil {
			return false, err
		}
		if rejectRelocation && normalizeProjectID(existingProjectID) != result.ProjectID {
			return false, fmt.Errorf("external result import %q belongs to project %q, not %q", result.ID, normalizeProjectID(existingProjectID), result.ProjectID)
		}
		if !rejectRelocation {
			return true, nil
		}
	}

	if result.ApplicationID != "" {
		if err := lockRequiredPlacementParent(executor,
			`UPDATE applications SET updated_at = updated_at WHERE id = ?`, result.ApplicationID, owner, "application"); err != nil {
			return false, err
		}
		var applicationProjectID string
		if err := executor.QueryRow(`SELECT COALESCE(project_id, '') FROM applications WHERE id = ?`, result.ApplicationID).Scan(&applicationProjectID); err != nil {
			return false, err
		}
		if normalizeProjectID(applicationProjectID) != result.ProjectID {
			return false, fmt.Errorf("external result import application %q belongs to project %q, not %q", result.ApplicationID, normalizeProjectID(applicationProjectID), result.ProjectID)
		}
	}
	if result.ReleaseID != "" {
		if err := lockRequiredPlacementParent(executor,
			`UPDATE releases SET updated_at = updated_at WHERE id = ?`, result.ReleaseID, owner, "release"); err != nil {
			return false, err
		}
		var releaseProjectID, releaseApplicationID string
		if err := executor.QueryRow(`SELECT COALESCE(project_id, ''), COALESCE(application_id, '') FROM releases WHERE id = ?`, result.ReleaseID).Scan(&releaseProjectID, &releaseApplicationID); err != nil {
			return false, err
		}
		if normalizeProjectID(releaseProjectID) != result.ProjectID {
			return false, fmt.Errorf("external result import release %q belongs to project %q, not %q", result.ReleaseID, normalizeProjectID(releaseProjectID), result.ProjectID)
		}
		if result.ApplicationID != "" && strings.TrimSpace(releaseApplicationID) != result.ApplicationID {
			return false, fmt.Errorf("external result import release %q belongs to application %q, not %q", result.ReleaseID, releaseApplicationID, result.ApplicationID)
		}
	}
	if result.ScenarioID != "" {
		if err := validateScenarioProjectPlacement(executor, result.ScenarioID, result.ProjectID, owner); err != nil {
			return false, err
		}
	}
	return exists, nil
}

var _ interface {
	CreateExternalResultImportIfAbsent(*ExternalResultImport) (bool, error)
	GetExternalResultImport(string) (*ExternalResultImport, error)
	DeleteExternalResultImportIfRawResult(string, map[string]any) (bool, error)
	DeleteExternalResultImportIfUnchanged(*ExternalResultImport) (bool, error)
} = (*SQLiteStore)(nil)
