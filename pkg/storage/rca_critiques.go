package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// MaxRCACritiquesPerAnalysis bounds the reviews one analysis keeps.
const MaxRCACritiquesPerAnalysis = 100

// ErrRCACritiqueLimit means an analysis already holds the most critiques it
// keeps.
var ErrRCACritiqueLimit = errors.New("analysis critique limit reached")

// RCACritique is a review of a root-cause analysis: an agent's or an
// analyst's narrative about it. It is stored apart from the analysis and
// never changes its assessment.
type RCACritique struct {
	ID         string          `json:"id"`
	ProjectID  string          `json:"project_id"`
	TestID     string          `json:"test_id"`
	AnalysisID string          `json:"analysis_id"`
	CreatedAt  time.Time       `json:"created_at"`
	CreatedBy  string          `json:"created_by"`
	Payload    json.RawMessage `json:"payload"`
}

type RCACritiqueStore interface {
	// CreateRCACritique stores a critique of an existing analysis; it
	// returns ErrRCACritiqueLimit when the analysis holds the most it keeps.
	CreateRCACritique(RCACritique) (*RCACritique, error)
	ListRCACritiques(project, test, analysis string) ([]RCACritique, error)
}

func (s *SQLiteStore) CreateRCACritique(c RCACritique) (*RCACritique, error) {
	if c.ID == "" || c.ProjectID == "" || c.TestID == "" || c.AnalysisID == "" || !json.Valid(c.Payload) || len(c.Payload) > 64<<10 || len(c.CreatedBy) > 256 {
		return nil, errors.New("invalid root-cause critique")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var owner string
	if err := tx.QueryRow(`SELECT test_id FROM rca_analyses WHERE id = ? AND project_id = ?`, c.AnalysisID, c.ProjectID).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, errors.New("root-cause analysis not found")
		}
		return nil, err
	}
	if owner != c.TestID {
		return nil, errors.New("root-cause analysis not found")
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM rca_critiques WHERE analysis_id = ?`, c.AnalysisID).Scan(&n); err != nil {
		return nil, err
	}
	if n >= MaxRCACritiquesPerAnalysis {
		return nil, ErrRCACritiqueLimit
	}
	c.CreatedAt = c.CreatedAt.UTC()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	if _, err := tx.Exec(`INSERT INTO rca_critiques(id, project_id, test_id, analysis_id, created_at, created_by, payload) VALUES(?,?,?,?,?,?,?)`,
		c.ID, c.ProjectID, c.TestID, c.AnalysisID, c.CreatedAt, strings.TrimSpace(c.CreatedBy), string(c.Payload)); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *SQLiteStore) ListRCACritiques(project, test, analysis string) ([]RCACritique, error) {
	rows, err := s.db.Query(`SELECT id, project_id, test_id, analysis_id, created_at, created_by, payload FROM rca_critiques WHERE project_id = ? AND test_id = ? AND analysis_id = ? ORDER BY created_at, id`, project, test, analysis)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RCACritique{}
	for rows.Next() {
		var c RCACritique
		var payload string
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.TestID, &c.AnalysisID, &c.CreatedAt, &c.CreatedBy, &payload); err != nil {
			return nil, err
		}
		c.CreatedAt, c.Payload = c.CreatedAt.UTC(), json.RawMessage(payload)
		out = append(out, c)
	}
	return out, rows.Err()
}
