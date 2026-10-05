package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// AnalysisDefinition revisions are append-only. Deletion hides the latest
// definition while preserving the revision that an import or comparison used.
type AnalysisDefinition struct {
	ID         string          `json:"id"`
	ProjectID  string          `json:"project_id"`
	Kind       string          `json:"kind"`
	Name       string          `json:"name"`
	Revision   int64           `json:"revision"`
	Definition json.RawMessage `json:"definition"`
	Deleted    bool            `json:"deleted"`
	CreatedAt  time.Time       `json:"created_at"`
}

// AnalysisDefinitionKindConnectionLinks holds one connection's deep link
// settings; it is not a user analytical definition.
const AnalysisDefinitionKindConnectionLinks = "connection_links"

type AnalysisDefinitionStore interface {
	SaveAnalysisDefinition(AnalysisDefinition, int64) (*AnalysisDefinition, error)
	GetAnalysisDefinition(string, string, int64) (*AnalysisDefinition, error)
	ListAnalysisDefinitions(string, string, int, int) ([]AnalysisDefinition, error)
}

func (s *SQLiteStore) SaveAnalysisDefinition(in AnalysisDefinition, expected int64) (*AnalysisDefinition, error) {
	if in.ID == "" || len(in.ID) > 128 || in.ProjectID == "" || strings.TrimSpace(in.Name) == "" || len(in.Name) > 256 || expected < 0 || expected >= 1<<62 || (in.Kind != "mapping" && in.Kind != "comparison" && in.Kind != AnalysisDefinitionKindConnectionLinks) || !json.Valid(in.Definition) || len(in.Definition) > 128<<10 {
		return nil, errors.New("invalid analytical definition")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, in.ProjectID); err != nil {
		return nil, err
	}
	var revision int64
	var project, kind string
	err = tx.QueryRow(`SELECT project_id,kind,revision FROM analysis_definitions WHERE id=? ORDER BY revision DESC LIMIT 1`, in.ID).Scan(&project, &kind, &revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if revision != expected || revision > 0 && (project != in.ProjectID || kind != in.Kind) {
		return nil, ErrAnalysisRevision
	}
	// Connection link settings are one per connection and stay out of the
	// mappings' and comparisons' budget.
	if expected == 0 && in.Kind != AnalysisDefinitionKindConnectionLinks {
		var n int
		if err = tx.QueryRow(`SELECT COUNT(DISTINCT id) FROM analysis_definitions WHERE project_id=? AND kind<>?`, in.ProjectID, AnalysisDefinitionKindConnectionLinks).Scan(&n); err != nil {
			return nil, err
		}
		if n >= 1000 {
			return nil, errors.New("project analytical definition limit reached")
		}
	}
	if expected >= 1000 {
		return nil, errors.New("analytical definition revision limit reached")
	}
	in.Revision = expected + 1
	in.CreatedAt = time.Now().UTC()
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO analysis_definitions(id,project_id,kind,revision,payload) VALUES(?,?,?,?,?)`, in.ID, in.ProjectID, in.Kind, in.Revision, string(body)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &in, nil
}

func (s *SQLiteStore) GetAnalysisDefinition(project, id string, revision int64) (*AnalysisDefinition, error) {
	var body string
	var err error
	if revision == 0 {
		err = s.db.QueryRow(`SELECT payload FROM analysis_definitions WHERE project_id=? AND id=? ORDER BY revision DESC LIMIT 1`, project, id).Scan(&body)
	} else {
		err = s.db.QueryRow(`SELECT payload FROM analysis_definitions WHERE project_id=? AND id=? AND revision=?`, project, id, revision).Scan(&body)
	}
	if err != nil {
		return nil, err
	}
	var in AnalysisDefinition
	err = json.Unmarshal([]byte(body), &in)
	return &in, err
}

func (s *SQLiteStore) ListAnalysisDefinitions(project, kind string, limit, offset int) ([]AnalysisDefinition, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, errors.New("invalid analytical definitions page")
	}
	rows, err := s.db.Query(`SELECT d.payload FROM analysis_definitions d WHERE d.project_id=? AND d.kind=? AND d.revision=(SELECT MAX(x.revision) FROM analysis_definitions x WHERE x.id=d.id) ORDER BY d.id LIMIT ? OFFSET ?`, project, kind, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AnalysisDefinition{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var in AnalysisDefinition
		if err = json.Unmarshal([]byte(body), &in); err != nil {
			return nil, err
		}
		result = append(result, in)
	}
	return result, rows.Err()
}
