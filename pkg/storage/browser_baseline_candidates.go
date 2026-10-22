package storage

import (
	"context"
	"errors"
	"strings"
	"time"
)

// BrowserBaselineCandidateStore pages identities only. Archive inspection and
// browser compatibility are separate admission decisions, never inferred here.
type BrowserBaselineCandidateStore interface {
	ListBrowserBaselineCandidates(context.Context, BrowserBaselineCandidateQuery) ([]BrowserBaselineCandidate, bool, error)
}

type BrowserBaselineCandidate struct {
	TestID      string    `json:"test_id"`
	CompletedAt time.Time `json:"completed_at"`
}

type BrowserBaselineCandidateQuery struct {
	ProjectID, ApplicationID, ScenarioID, CurrentTestID string
	AfterID                                             string
	Limit                                               int
}

const MaxBrowserBaselineCandidatePage = 128

// Shared with the expression index; DefaultProjectID is a compile-time constant.
const browserBaselineProjectExpression = "COALESCE(NULLIF(project_id, ''), '" + DefaultProjectID + "')"

// ListBrowserBaselineCandidates pages the completed inventory by canonical ID.
// SQLite retains legacy timestamp offsets as text. Completion order and the
// current-run time fence therefore belong to the caller's exact time.Time
// comparisons, after reading its bounded inventory. A caller must disclose
// budget exhaustion and never claim a latest selection from a partial inventory.
func (s *SQLiteStore) ListBrowserBaselineCandidates(ctx context.Context, q BrowserBaselineCandidateQuery) ([]BrowserBaselineCandidate, bool, error) {
	if strings.TrimSpace(q.ProjectID) == "" || q.CurrentTestID == "" || q.Limit < 1 || q.Limit > MaxBrowserBaselineCandidatePage {
		return nil, false, errors.New("invalid browser baseline candidate page")
	}
	query := `SELECT id, end_time FROM tests WHERE ` + browserBaselineProjectExpression + `=? AND COALESCE(application_id, '')=? AND COALESCE(scenario_id, '')=? AND id<>? AND status='COMPLETE' AND end_time IS NOT NULL AND id>? ORDER BY id ASC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, normalizeProjectID(q.ProjectID), q.ApplicationID, q.ScenarioID, q.CurrentTestID, q.AfterID, q.Limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	candidates := make([]BrowserBaselineCandidate, 0, q.Limit+1)
	for rows.Next() {
		var c BrowserBaselineCandidate
		if err := rows.Scan(&c.TestID, &c.CompletedAt); err != nil {
			return nil, false, err
		}
		candidates = append(candidates, c)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(candidates) > q.Limit
	if more {
		candidates = candidates[:q.Limit]
	}
	return candidates, more, nil
}
