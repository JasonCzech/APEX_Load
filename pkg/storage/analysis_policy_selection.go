package storage

import "encoding/json"

// AnalysisPolicySelectionStore resolves an exact revision and its latest
// policy tombstone in one database snapshot. It does not mutate either row.
// The immutable selected revision may precede the current live revision.
type AnalysisPolicySelectionStore interface {
	GetAnalysisPolicyRevision(project, id string, revision int64) (selected, latest *AnalysisDefinition, err error)
}

func (s *SQLiteStore) GetAnalysisPolicyRevision(project, id string, revision int64) (*AnalysisDefinition, *AnalysisDefinition, error) {
	var selectedBody, latestBody string
	err := s.db.QueryRow(`SELECT selected.payload, latest.payload
 FROM analysis_definitions selected JOIN analysis_definitions latest
 ON latest.project_id = selected.project_id AND latest.id = selected.id
 AND latest.revision = (SELECT MAX(current_revision.revision) FROM analysis_definitions current_revision WHERE current_revision.project_id=selected.project_id AND current_revision.id=selected.id)
 WHERE selected.project_id=? AND selected.id=? AND selected.revision=?`, project, id, revision).Scan(&selectedBody, &latestBody)
	if err != nil {
		return nil, nil, err
	}
	var selected, latest AnalysisDefinition
	if err := json.Unmarshal([]byte(selectedBody), &selected); err != nil {
		return nil, nil, err
	}
	if err := json.Unmarshal([]byte(latestBody), &latest); err != nil {
		return nil, nil, err
	}
	return &selected, &latest, nil
}
