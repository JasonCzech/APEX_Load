package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// ImportedScriptIdentity is the provenance an importer recorded on a script,
// read without decoding the script's steps.
type ImportedScriptIdentity struct {
	ID         string
	Name       string
	RefKey     string
	FolderID   *string
	UpdatedAt  time.Time
	ArchivedAt *time.Time
	Import     *dsl.ImportMetadata
}

// ListImportedScriptIdentities lists the active and archived scripts in
// projectID whose import metadata names source. It reads only identity
// columns and metadata, so re-import matching does not decode every script in
// a large project. Rows whose metadata cannot be decoded are skipped.
func (s *SQLiteStore) ListImportedScriptIdentities(projectID, source string) ([]ImportedScriptIdentity, error) {
	projectID = normalizeProjectID(strings.TrimSpace(projectID))
	source = strings.TrimSpace(source)
	query := `SELECT id, name, COALESCE(ref_key, ''), folder_id, updated_at,
		(SELECT archived_at FROM object_archive_states a WHERE a.resource_type='script' AND a.resource_id=scripts.id AND ` +
		archiveIncarnationMatch(s.db.dialect, "a.incarnation", "scripts.created_at") + `),
		COALESCE(metadata_json, '')
		FROM scripts WHERE project_id = ? AND metadata_json LIKE ? ESCAPE '!'`
	// The LIKE is only a prefilter; each row's decoded metadata is checked.
	rows, err := s.db.Query(query, projectID, `%"source":"`+escapeLikePattern(source)+`"%`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var identities []ImportedScriptIdentity
	for rows.Next() {
		var identity ImportedScriptIdentity
		var metadataJSON string
		if err := rows.Scan(&identity.ID, &identity.Name, &identity.RefKey, &identity.FolderID,
			&identity.UpdatedAt, &identity.ArchivedAt, &metadataJSON); err != nil {
			return nil, err
		}
		var metadata dsl.ScriptMetadata
		if json.Unmarshal([]byte(metadataJSON), &metadata) != nil || metadata.Import == nil ||
			!strings.EqualFold(strings.TrimSpace(metadata.Import.Source), source) {
			continue
		}
		identity.Import = metadata.Import
		identities = append(identities, identity)
	}
	return identities, rows.Err()
}

// ImportBatchSavedAt returns, for each script ID, the updated_at the most
// recent import batch recorded when it saved that script. A script whose
// updated_at still equals that value has not been edited since the import.
// IDs with no receipt snapshot are absent from the result.
//
// Receipts are scanned newest first by their small script-ID lists, and each
// needed receipt's script snapshots are decoded once for all the IDs it holds.
func (s *SQLiteStore) ImportBatchSavedAt(projectID string, scriptIDs []string) (map[string]time.Time, error) {
	projectID = normalizeProjectID(strings.TrimSpace(projectID))
	pending := make(map[string]struct{}, len(scriptIDs))
	for _, rawID := range scriptIDs {
		if scriptID := strings.TrimSpace(rawID); scriptID != "" {
			pending[scriptID] = struct{}{}
		}
	}
	saved := make(map[string]time.Time, len(pending))
	if len(pending) == 0 {
		return saved, nil
	}
	rows, err := s.db.Query(`SELECT id, script_ids_json FROM bruno_native_import_batches
		WHERE project_id = ? ORDER BY created_at DESC, id`, projectID)
	if err != nil {
		return nil, err
	}
	receiptIDs := make(map[string][]string)
	var receiptOrder []string
	for rows.Next() && len(pending) > 0 {
		var receiptID, idsJSON string
		if err := rows.Scan(&receiptID, &idsJSON); err != nil {
			rows.Close()
			return nil, err
		}
		var ids []string
		if json.Unmarshal([]byte(idsJSON), &ids) != nil {
			continue
		}
		for _, rawID := range ids {
			scriptID := strings.TrimSpace(rawID)
			if _, wanted := pending[scriptID]; !wanted {
				continue
			}
			delete(pending, scriptID)
			if _, seen := receiptIDs[receiptID]; !seen {
				receiptOrder = append(receiptOrder, receiptID)
			}
			receiptIDs[receiptID] = append(receiptIDs[receiptID], scriptID)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, receiptID := range receiptOrder {
		var scriptsJSON string
		err := s.db.QueryRow(`SELECT scripts_json FROM bruno_native_import_batches WHERE id = ? AND project_id = ?`,
			receiptID, projectID).Scan(&scriptsJSON)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		var snapshots []struct {
			ID        string    `json:"id"`
			UpdatedAt time.Time `json:"updated_at"`
		}
		if json.Unmarshal([]byte(scriptsJSON), &snapshots) != nil {
			continue
		}
		wanted := make(map[string]struct{}, len(receiptIDs[receiptID]))
		for _, scriptID := range receiptIDs[receiptID] {
			wanted[scriptID] = struct{}{}
		}
		for _, snapshot := range snapshots {
			scriptID := strings.TrimSpace(snapshot.ID)
			if _, ok := wanted[scriptID]; ok && !snapshot.UpdatedAt.IsZero() {
				saved[scriptID] = snapshot.UpdatedAt
			}
		}
	}
	return saved, nil
}

// escapeLikePattern escapes value for a LIKE pattern that declares
// ESCAPE '!'.
func escapeLikePattern(value string) string {
	return strings.NewReplacer(`!`, `!!`, `%`, `!%`, `_`, `!_`).Replace(value)
}
