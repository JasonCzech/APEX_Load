package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"time"
)

// RunAnnotationSet is revisioned independently of immutable run evidence. Deleted
// annotations remain in its audit history until the parent run is deleted.
type RunAnnotation struct {
	ID        string     `json:"id"`
	Timestamp time.Time  `json:"timestamp"`
	Label     string     `json:"label"`
	Text      string     `json:"text,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	CreatedBy string     `json:"created_by"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	DeletedBy string     `json:"deleted_by,omitempty"`
}
type RunAnnotationSet struct {
	Revision    int64           `json:"revision"`
	Annotations []RunAnnotation `json:"annotations"`
}

var ErrRunAnnotationLimit = errors.New("annotation audit history limit reached")

type RunAnnotationStore interface {
	GetRunAnnotations(project, run string) (RunAnnotationSet, error)
	SaveRunAnnotations(project, run string, set RunAnnotationSet, expected int64) (bool, error)
}

func (s *SQLiteStore) GetRunAnnotations(project, run string) (RunAnnotationSet, error) {
	out := RunAnnotationSet{Annotations: []RunAnnotation{}}
	var raw string
	err := s.db.QueryRow(`SELECT payload FROM run_annotations WHERE test_id = ? AND project_id = ?`, run, normalizeProjectID(project)).Scan(&raw)
	if err == sql.ErrNoRows {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	if len(raw) > 512<<10 {
		return out, errors.New("annotation history exceeds limit")
	}
	err = json.Unmarshal([]byte(raw), &out)
	return out, err
}
func (s *SQLiteStore) SaveRunAnnotations(project, run string, set RunAnnotationSet, expected int64) (bool, error) {
	if expected < 0 || expected == math.MaxInt64 || set.Revision != expected+1 {
		return false, errors.New("invalid annotation revision or history limit")
	}
	if len(set.Annotations) > 200 {
		return false, ErrRunAnnotationLimit
	}
	for _, a := range set.Annotations {
		if a.ID == "" || len(a.ID) > 128 || a.Timestamp.IsZero() || a.CreatedAt.IsZero() || a.Label == "" || len(a.Label) > 256 || len(a.Text) > 2048 || len(a.CreatedBy) > 256 || len(a.DeletedBy) > 256 {
			return false, errors.New("invalid annotation")
		}
	}
	raw, err := json.Marshal(set)
	if err != nil {
		return false, err
	}
	if len(raw) > 512<<10 {
		return false, ErrRunAnnotationLimit
	}
	var result sql.Result
	if expected == 0 {
		result, err = s.db.Exec(`INSERT INTO run_annotations(test_id, project_id, revision, payload)
   SELECT id, project_id, ?, ? FROM tests WHERE id = ? AND project_id = ?
   ON CONFLICT(test_id) DO NOTHING`, set.Revision, string(raw), run, normalizeProjectID(project))
	} else {
		result, err = s.db.Exec(`UPDATE run_annotations SET revision = ?, payload = ? WHERE test_id = ? AND project_id = ? AND revision = ?`, set.Revision, string(raw), run, normalizeProjectID(project), expected)
	}
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}
