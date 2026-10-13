package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ScenarioDraft is scenario-builder progress saved to a project: the builder's
// own draft document, kept until it is complete enough to save as a scenario.
// It is not a scenario, so no run, plan or schedule can use it.
type ScenarioDraft struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	// ScenarioID names the scenario an edit draft changes; that scenario keeps
	// its saved version until the changes are saved complete.
	ScenarioID string `json:"scenario_id,omitempty"`
	Name       string `json:"name"`
	// Document is the builder's draft (JSON), empty in summaries.
	Document  string    `json:"-"`
	Revision  int64     `json:"revision"`
	CreatedBy string    `json:"created_by,omitempty"`
	UpdatedBy string    `json:"updated_by,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

var (
	// ErrScenarioDraftNotFound is a draft that does not exist in the project.
	ErrScenarioDraftNotFound = errors.New("scenario draft not found")
	// ErrScenarioDraftProjectNotFound is a draft saved to a project that does not exist.
	ErrScenarioDraftProjectNotFound = errors.New("project not found")
)

// CreateScenarioDraft saves a new draft at revision 1. The project row is
// locked in the same transaction, so a concurrent project deletion cannot
// leave a draft behind.
func (s *SQLiteStore) CreateScenarioDraft(draft *ScenarioDraft) error {
	if draft == nil || strings.TrimSpace(draft.ID) == "" || strings.TrimSpace(draft.Name) == "" || draft.Document == "" {
		return errors.New("scenario draft id, name and document are required")
	}
	draft.ProjectID = normalizeProjectID(draft.ProjectID)
	now := time.Now().UTC()
	draft.CreatedAt, draft.UpdatedAt, draft.Revision = now, now, 1
	draft.UpdatedBy = draft.CreatedBy
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	locked, err := tx.Exec(`UPDATE projects SET updated_at = updated_at WHERE id = ?`, draft.ProjectID)
	if err != nil {
		return err
	}
	if affected, err := locked.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return ErrScenarioDraftProjectNotFound
	}
	if _, err := tx.Exec(`INSERT INTO scenario_drafts (id, project_id, scenario_id, name, document_json, revision, created_by, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		draft.ID, draft.ProjectID, nullableString(draft.ScenarioID), draft.Name, draft.Document, draft.Revision, draft.CreatedBy, draft.UpdatedBy, draft.CreatedAt, draft.UpdatedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateScenarioDraftIfRevision replaces a draft's name, scenario and
// document when it is still at expectedRevision, and advances the revision.
// It reports false when the draft is missing or another save came first.
func (s *SQLiteStore) UpdateScenarioDraftIfRevision(draft *ScenarioDraft, expectedRevision int64) (bool, error) {
	if draft == nil || strings.TrimSpace(draft.ID) == "" || strings.TrimSpace(draft.Name) == "" || draft.Document == "" {
		return false, errors.New("scenario draft id, name and document are required")
	}
	draft.ProjectID = normalizeProjectID(draft.ProjectID)
	now := time.Now().UTC()
	result, err := s.db.Exec(`UPDATE scenario_drafts SET scenario_id = ?, name = ?, document_json = ?, revision = revision + 1, updated_by = ?, updated_at = ?
		WHERE id = ? AND project_id = ? AND revision = ?`,
		nullableString(draft.ScenarioID), draft.Name, draft.Document, draft.UpdatedBy, now, draft.ID, draft.ProjectID, expectedRevision)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	stored, err := s.GetScenarioDraft(draft.ProjectID, draft.ID)
	if err != nil {
		return false, err
	}
	*draft = *stored
	return true, nil
}

const scenarioDraftColumns = `id, project_id, COALESCE(scenario_id, ''), name, %s, revision, COALESCE(created_by, ''), COALESCE(updated_by, ''), created_at, updated_at`

func scanScenarioDraft(row interface{ Scan(...any) error }) (*ScenarioDraft, error) {
	var draft ScenarioDraft
	if err := row.Scan(&draft.ID, &draft.ProjectID, &draft.ScenarioID, &draft.Name, &draft.Document, &draft.Revision, &draft.CreatedBy, &draft.UpdatedBy, &draft.CreatedAt, &draft.UpdatedAt); err != nil {
		return nil, err
	}
	return &draft, nil
}

// GetScenarioDraft reads one of a project's drafts with its document.
func (s *SQLiteStore) GetScenarioDraft(projectID, id string) (*ScenarioDraft, error) {
	draft, err := scanScenarioDraft(s.db.QueryRow(`SELECT `+fmt.Sprintf(scenarioDraftColumns, `document_json`)+` FROM scenario_drafts WHERE id = ? AND project_id = ?`, id, normalizeProjectID(projectID)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrScenarioDraftNotFound
	}
	return draft, err
}

// ListScenarioDrafts lists a project's drafts, most recently saved first,
// without their documents.
func (s *SQLiteStore) ListScenarioDrafts(projectID string) ([]*ScenarioDraft, error) {
	rows, err := s.db.Query(`SELECT `+fmt.Sprintf(scenarioDraftColumns, `''`)+` FROM scenario_drafts WHERE project_id = ? ORDER BY updated_at DESC, id`, normalizeProjectID(projectID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	drafts := []*ScenarioDraft{}
	for rows.Next() {
		draft, err := scanScenarioDraft(rows)
		if err != nil {
			return nil, err
		}
		drafts = append(drafts, draft)
	}
	return drafts, rows.Err()
}

// DeleteScenarioDraft deletes one of a project's drafts, reporting whether it
// was deleted. A positive expectedRevision deletes it only at that revision, so
// work saved to it since is kept.
func (s *SQLiteStore) DeleteScenarioDraft(projectID, id string, expectedRevision int64) (bool, error) {
	query, args := `DELETE FROM scenario_drafts WHERE id = ? AND project_id = ?`, []any{id, normalizeProjectID(projectID)}
	if expectedRevision > 0 {
		query, args = query+` AND revision = ?`, append(args, expectedRevision)
	}
	result, err := s.db.Exec(query, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}
