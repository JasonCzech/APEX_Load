package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var (
	ErrScriptAuthoringConflict             = errors.New("script authored content changed; reload before applying changes")
	ErrScriptAuthoringNotFound             = errors.New("script authored content not found")
	ErrScriptAuthoringUnavailable          = errors.New("script authored content revision unavailable")
	ErrScriptAuthoringPreconditionRequired = errors.New("script authored content precondition required")
	ErrScriptAuthoringInvalidRevision      = errors.New("invalid script authored content revision")
	// ErrScriptAuthoringOutcomeUnknown wraps a failed commit: the change may
	// have been applied. Every other error is a definite refusal.
	ErrScriptAuthoringOutcomeUnknown = errors.New("script authored content commit outcome unknown")
	// ErrScriptInUse refuses deleting a script that a scenario still uses.
	ErrScriptInUse = errors.New("script is used by a scenario")
	// ErrScriptRefKeyTaken refuses creating a script whose reference key
	// another script already holds (a copy carries its original's key).
	ErrScriptRefKeyTaken = errors.New("script reference key is already in use")
)

// ScriptAuthoringSnapshot pairs persisted executable content and authored
// placement with the nonce observed in the same database transaction.
// ContentRevision is response metadata: it never enters executable DSL JSON.
type ScriptAuthoringSnapshot struct {
	Script            *dsl.Script
	ContentRevision   string
	LibraryAssignment *LibraryAssignment
}

type ScriptAuthoringPrecondition struct {
	ProjectID          string
	ContentRevision    string
	CreatedAt          time.Time
	LegacyOrdinarySave bool `json:"-"` // Server-owned PUT compatibility admission; never copied from DSL.
}

type ScriptAuthoringMutationResult struct {
	Snapshot *ScriptAuthoringSnapshot
	Applied  bool
}

// ScriptAuthoringStore is optional so a store without durable revision tracking
// can explicitly refuse a requested precondition rather than invent a token.
type ScriptAuthoringStore interface {
	GetScriptAuthoringSnapshot(string) (*ScriptAuthoringSnapshot, error)
	ListScriptAuthoringSnapshots(string, *string) ([]*ScriptAuthoringSnapshot, error)
	CreateScriptAuthoringIfAbsent(*dsl.Script) (*ScriptAuthoringMutationResult, error)
	UpdateScriptAuthoringIfCurrent(*dsl.Script, ScriptAuthoringPrecondition) (*ScriptAuthoringMutationResult, error)
	UpdateScriptAuthoringIfCurrentAndCorrelationRulesetCurrent(*dsl.Script, ScriptAuthoringPrecondition, string, string, time.Time) (*ScriptAuthoringMutationResult, error)
	MoveScriptAuthoringIfCurrent(string, *string, ScriptAuthoringPrecondition) (*ScriptAuthoringMutationResult, error)
	DeleteScriptAuthoringIfCurrent(string, ScriptAuthoringPrecondition) (bool, error)
	ReplaceScriptLibraryAssignmentIfCurrent(string, ScriptAuthoringPrecondition, *string, []string, int64) (*LibraryAssignment, *ScriptAuthoringMutationResult, error)
}

// ScriptAuthoringReferenceStore resolves a canonical reference through durable
// storage without consulting a replica's display cache or scanning the library.
// Resolution and the returned content/placement share the same read transaction.
type ScriptAuthoringReferenceStore interface {
	GetScriptAuthoringSnapshotByRefKey(string) (*ScriptAuthoringSnapshot, error)
}

var _ ScriptAuthoringStore = (*SQLiteStore)(nil)
var _ ScriptAuthoringReferenceStore = (*SQLiteStore)(nil)

// ValidScriptContentRevision accepts only the persisted v1 nonce grammar.
func ValidScriptContentRevision(value string) bool {
	if len(value) != len("scr-v1-")+64 || !strings.HasPrefix(value, "scr-v1-") {
		return false
	}
	for _, c := range value[len("scr-v1-"):] {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validateScriptAuthoringPrecondition(expected ScriptAuthoringPrecondition) error {
	if !ValidScriptContentRevision(expected.ContentRevision) {
		return ErrScriptAuthoringInvalidRevision
	}
	if strings.TrimSpace(expected.ProjectID) == "" || expected.CreatedAt.IsZero() {
		return errors.New("script project and created_at are required for conditional mutation")
	}
	return nil
}

func scriptSelectColumns(dialect storeDialect) string {
	return `id, COALESCE(ref_key, ''), name, protocol, version, COALESCE(project_id, ''), folder_id, generated_by, generated_from,
 COALESCE(metadata_json, ''), COALESCE(goal_config_json, ''), config, data_sources, COALESCE(parameters, ''), init, actions, teardown,
 created_at, updated_at, (SELECT archived_at FROM object_archive_states a WHERE a.resource_type='script' AND a.resource_id=scripts.id AND ` + archiveIncarnationMatch(dialect, "a.incarnation", "scripts.created_at") + `), COALESCE(content_revision, '')`
}

type scriptRowScanner interface{ Scan(...any) error }

func scanScriptAuthoringRow(row scriptRowScanner) (*ScriptAuthoringSnapshot, error) {
	var script dsl.Script
	var metadata, goals, config, data, parameters, init, actions, teardown, revision string
	if err := row.Scan(&script.ID, &script.RefKey, &script.Name, &script.Protocol, &script.Version,
		&script.ProjectID, &script.FolderID, &script.GeneratedBy, &script.GeneratedFrom,
		&metadata, &goals, &config, &data, &parameters, &init, &actions, &teardown,
		&script.CreatedAt, &script.UpdatedAt, &script.ArchivedAt, &revision); err != nil {
		return nil, err
	}
	if !ValidScriptContentRevision(revision) {
		return nil, ErrScriptAuthoringUnavailable
	}
	if err := decodeStoredScriptJSON(&script, metadata, goals, config, data, parameters, init, actions, teardown); err != nil {
		return nil, fmt.Errorf("decode script %s: %w", script.ID, err)
	}
	recognizeLegacyCredentialPreservingImport(&script)
	return &ScriptAuthoringSnapshot{Script: &script, ContentRevision: revision}, nil
}

func readScriptAuthoringSnapshotTx(tx *storeTx, id string) (*ScriptAuthoringSnapshot, error) {
	snapshot, err := scanScriptAuthoringRow(tx.QueryRow(`SELECT `+scriptSelectColumns(tx.dialect)+` FROM scripts WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	assignments, err := readLibraryAssignments(tx, "script_library_assignments", []string{id})
	if err != nil {
		return nil, err
	}
	snapshot.LibraryAssignment = assignments[id]
	return snapshot, nil
}

func (s *SQLiteStore) beginScriptAuthoringRead() (*storeTx, error) {
	opts := &sql.TxOptions{ReadOnly: true}
	if s.db.dialect == storeDialectPostgres {
		opts.Isolation = sql.LevelRepeatableRead
	}
	return s.db.BeginTx(context.Background(), opts)
}

func (s *SQLiteStore) GetScriptAuthoringSnapshot(id string) (*ScriptAuthoringSnapshot, error) {
	tx, err := s.beginScriptAuthoringRead()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	snapshot, err := readScriptAuthoringSnapshotTx(tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScriptAuthoringNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func readScriptAuthoringSnapshotByRefKeyTx(tx *storeTx, refKey string) (*ScriptAuthoringSnapshot, error) {
	// Two rows suffice to reject an ambiguous legacy/corrupt reference. Never
	// choose an arbitrary owner, even if the normal unique index is unavailable.
	if strings.TrimSpace(refKey) == "" {
		return nil, ErrScriptAuthoringNotFound
	}
	rows, err := tx.Query(`SELECT id FROM scripts WHERE ref_key=? ORDER BY id LIMIT 2`, refKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, ErrScriptAuthoringNotFound
	}
	if len(ids) != 1 {
		return nil, ErrScriptAuthoringUnavailable
	}
	snapshot, err := readScriptAuthoringSnapshotTx(tx, ids[0])
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScriptAuthoringNotFound
	}
	return snapshot, err
}

func (s *SQLiteStore) GetScriptAuthoringSnapshotByRefKey(refKey string) (*ScriptAuthoringSnapshot, error) {
	tx, err := s.beginScriptAuthoringRead()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	snapshot, err := readScriptAuthoringSnapshotByRefKeyTx(tx, refKey)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return snapshot, nil
}

func (s *SQLiteStore) ListScriptAuthoringSnapshots(projectID string, folderID *string) ([]*ScriptAuthoringSnapshot, error) {
	tx, err := s.beginScriptAuthoringRead()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	query := `SELECT ` + scriptSelectColumns(tx.dialect) + ` FROM scripts`
	var filters []string
	var args []any
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
	query += ` ORDER BY created_at DESC, id`
	rows, err := tx.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var snapshots []*ScriptAuthoringSnapshot
	var ids []string
	for rows.Next() {
		snapshot, err := scanScriptAuthoringRow(rows)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
		ids = append(ids, snapshot.Script.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	assignments, err := readLibraryAssignments(tx, "script_library_assignments", ids)
	if err != nil {
		return nil, err
	}
	for _, snapshot := range snapshots {
		snapshot.LibraryAssignment = assignments[snapshot.Script.ID]
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return snapshots, nil
}

// claimScriptAuthoringTx locks the parent before any authored child write.
// Parent identity/ownership are checked independently of the nonce.
func claimScriptAuthoringTx(tx *storeTx, id string, expected ScriptAuthoringPrecondition) error {
	query := `SELECT COALESCE(project_id, ''), created_at, COALESCE(content_revision, '') FROM scripts WHERE id = ?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var project, revision string
	var created time.Time
	if err := tx.QueryRow(query, id).Scan(&project, &created, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrScriptAuthoringNotFound
		}
		return err
	}
	if normalizeProjectID(project) != normalizeProjectID(expected.ProjectID) || !created.Equal(expected.CreatedAt) {
		return ErrScriptAuthoringNotFound
	}
	if !ValidScriptContentRevision(revision) {
		return ErrScriptAuthoringUnavailable
	}
	if revision != expected.ContentRevision {
		return ErrScriptAuthoringConflict
	}
	return nil
}

func (s *SQLiteStore) MoveScriptAuthoringIfCurrent(id string, folderID *string, expected ScriptAuthoringPrecondition) (*ScriptAuthoringMutationResult, error) {
	if err := validateScriptAuthoringPrecondition(expected); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := validateScriptFolderProjectTx(tx, expected.ProjectID, folderID); err != nil {
		return nil, err
	}
	if err := claimScriptAuthoringTx(tx, id, expected); err != nil {
		return nil, err
	}
	if err := ensureObjectActiveTx(tx, "script", id); err != nil {
		return nil, err
	}
	result, err := tx.Exec(`UPDATE scripts SET folder_id=?, updated_at=? WHERE id=? AND project_id=? AND content_revision=?`, folderID, time.Now().UTC(), id, normalizeProjectID(expected.ProjectID), expected.ContentRevision)
	if err != nil {
		return nil, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if count != 1 {
		return nil, ErrScriptAuthoringConflict
	}
	snapshot, err := readScriptAuthoringSnapshotTx(tx, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &ScriptAuthoringMutationResult{Snapshot: snapshot, Applied: true}, nil
}

func (s *SQLiteStore) DeleteScriptAuthoringIfCurrent(id string, expected ScriptAuthoringPrecondition) (bool, error) {
	if err := validateScriptAuthoringPrecondition(expected); err != nil {
		return false, err
	}
	return s.deleteScriptConditional(id, time.Time{}, &expected)
}

func (s *SQLiteStore) deleteScriptConditional(id string, expectedUpdatedAt time.Time, expected *ScriptAuthoringPrecondition) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var project string
	if err := tx.QueryRow(`SELECT COALESCE(project_id,'') FROM scripts WHERE id=?`, id).Scan(&project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrScriptAuthoringNotFound
		}
		return false, err
	}
	if expected != nil && normalizeProjectID(project) != normalizeProjectID(expected.ProjectID) {
		return false, ErrScriptAuthoringNotFound
	}
	if err := lockRequiredPlacementParent(tx, `UPDATE projects SET updated_at=updated_at WHERE id=?`, normalizeProjectID(project), "script", "project"); err != nil {
		return false, err
	}
	query := `SELECT COALESCE(project_id,''),updated_at FROM scripts WHERE id=?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var lockedProject string
	var lockedUpdatedAt time.Time
	if err := tx.QueryRow(query, id).Scan(&lockedProject, &lockedUpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrScriptAuthoringNotFound
		}
		return false, err
	}
	if normalizeProjectID(lockedProject) != normalizeProjectID(project) {
		return false, ErrScriptAuthoringNotFound
	}
	if expected != nil {
		if err := claimScriptAuthoringTx(tx, id, *expected); err != nil {
			return false, err
		}
	} else if !expectedUpdatedAt.IsZero() && !lockedUpdatedAt.Equal(expectedUpdatedAt) {
		return false, ErrScriptAuthoringConflict
	}
	if err := deleteScriptDependenciesTx(tx, id); err != nil {
		return false, err
	}
	// Child cleanup may advance the nonce. The precondition was already claimed
	// under the parent lock; do not reapply it and self-conflict on final DELETE.
	result, err := tx.Exec(`DELETE FROM scripts WHERE id=? AND project_id=?`, id, project)
	if err != nil {
		if isForeignKeyConstraintErr(err) {
			return false, ErrScriptInUse
		}
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if count != 1 {
		return false, ErrScriptAuthoringNotFound
	}
	if _, err := tx.Exec(deleteUnreferencedImportArtifactsSQL); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("%w: %v", ErrScriptAuthoringOutcomeUnknown, err)
	}
	return true, nil
}

func deleteScriptDependenciesTx(tx *storeTx, id string) error {
	if err := rejectActiveGitHubScriptSyncClaimTx(tx, id); err != nil {
		return err
	}
	if err := detachOrDeleteGitHubCheckLinksForScriptTx(tx, id); err != nil {
		return err
	}
	for _, query := range []string{
		`DELETE FROM github_script_sync_target_claims WHERE script_id=?`,
		`DELETE FROM github_script_sync_claims WHERE script_id=?`,
		`DELETE FROM github_script_sync_target_owners WHERE script_id=?`,
		`DELETE FROM github_script_links WHERE script_id=?`,
		`DELETE FROM script_recordings WHERE script_id=?`,
		`DELETE FROM bruno_source_revisions WHERE script_id=?`,
		`DELETE FROM script_library_assignments_releases WHERE item_id=?`,
		`DELETE FROM script_library_assignments WHERE item_id=?`,
	} {
		if _, err := tx.Exec(query, id); err != nil {
			return err
		}
	}
	return nil
}
