package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

var ErrAnalysisRevision = errors.New("analysis import revision conflict")

type AnalysisChunk struct {
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
type AnalysisImportFile struct {
	ID           string          `json:"id"`
	RelativePath string          `json:"relative_path"`
	Size         int64           `json:"size"`
	Uploaded     int64           `json:"uploaded"`
	SHA256       string          `json:"sha256,omitempty"`
	Chunks       []AnalysisChunk `json:"chunks,omitempty"`
}
type AnalysisImport struct {
	ID                 string                             `json:"id"`
	ProjectID          string                             `json:"project_id"`
	Name               string                             `json:"name"`
	Revision           int64                              `json:"revision"`
	State              string                             `json:"state"`
	Files              []AnalysisImportFile               `json:"files"`
	Mapping            *analysis.ParquetMapping           `json:"mapping,omitempty"`
	AllowRejected      bool                               `json:"allow_rejected"`
	Accepted           int64                              `json:"accepted"`
	Rejected           int64                              `json:"rejected"`
	Rejections         []analysis.ParquetRejectionSummary `json:"rejections,omitempty"`
	RejectionArtifacts []string                           `json:"rejection_artifacts,omitempty"`
	RejectionSHA256    map[string]string                  `json:"rejection_sha256,omitempty"`
	Normalized         []string                           `json:"normalized,omitempty"`
	NormalizedSHA256   map[string]string                  `json:"normalized_sha256,omitempty"`
	ObsoleteObjects    []string                           `json:"obsolete_objects,omitempty"`
	MappingID          string                             `json:"mapping_id,omitempty"`
	MappingRevision    int64                              `json:"mapping_revision,omitempty"`
	RunID              string                             `json:"run_id,omitempty"`
	Error              string                             `json:"error,omitempty"`
	Owner              string                             `json:"-"`
	LeaseExpiresAt     time.Time                          `json:"-"`
	CreatedAt          time.Time                          `json:"created_at"`
	UpdatedAt          time.Time                          `json:"updated_at"`
}

type AnalysisImportStore interface {
	GetAnalysisImport(string) (*AnalysisImport, error)
	ListAnalysisImports(string, int, int) ([]AnalysisImport, error)
	SaveAnalysisImport(AnalysisImport, int64) (*AnalysisImport, error)
}

// Durable metadata is authoritative; native databases and staging directories
// are rebuildable caches. CAS protects uploads, cancellation and publication.
func (s *SQLiteStore) SaveAnalysisImport(in AnalysisImport, expected int64) (*AnalysisImport, error) {
	if strings.TrimSpace(in.ID) == "" || len(in.ID) > 128 || strings.TrimSpace(in.ProjectID) == "" || len(in.Name) < 1 || len(in.Name) > 256 || expected < 0 || expected >= 1<<62 || len(in.Files) < 1 || len(in.Files) > 1024 || len(in.Normalized) > 65536 {
		return nil, errors.New("invalid analysis import metadata")
	}
	switch in.State {
	case "uploading", "mapped", "normalizing", "ready", "failed", "cancelled", "deleting", "deleted":
	default:
		return nil, errors.New("invalid analysis import state")
	}
	in.Revision = expected + 1
	in.UpdatedAt = time.Now().UTC()
	if expected == 0 {
		in.CreatedAt = in.UpdatedAt
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if len(body) > 2<<20 {
		return nil, errors.New("analysis metadata exceeds 2 MiB")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, in.ProjectID); err != nil {
		return nil, err
	}
	if in.RunID != "" {
		var owner string
		if err = tx.QueryRow(`SELECT project_id FROM tests WHERE id=?`, in.RunID).Scan(&owner); err != nil || owner != in.ProjectID {
			return nil, errors.New("dataset association requires a run in the same project")
		}
	}
	if expected == 0 && in.State != "uploading" {
		return nil, errors.New("imports must begin in uploading state")
	}
	if expected > 0 {
		var oldBody string
		if err = tx.QueryRow(`SELECT payload FROM analysis_imports WHERE id=? AND project_id=? AND revision=?`, in.ID, in.ProjectID, expected).Scan(&oldBody); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrAnalysisRevision
			}
			return nil, err
		}
		var old AnalysisImport
		if err = json.Unmarshal([]byte(oldBody), &old); err != nil {
			return nil, err
		}
		allowed := map[string]string{"uploading": " uploading mapped cancelled", "mapped": " mapped normalizing cancelled", "normalizing": " normalizing ready failed cancelled", "failed": " mapped normalizing cancelled", "ready": " ready deleting", "cancelled": " cancelled", "deleting": " deleting deleted", "deleted": " deleted"}
		if !strings.Contains(allowed[old.State]+" ", " "+in.State+" ") {
			return nil, errors.New("invalid analysis lifecycle transition")
		}
		if !in.CreatedAt.Equal(old.CreatedAt) || len(in.Files) != len(old.Files) {
			return nil, errors.New("immutable import identity changed")
		}
		for i, file := range in.Files {
			prior := old.Files[i]
			if file.ID != prior.ID || file.RelativePath != prior.RelativePath || file.Size != prior.Size || file.Uploaded < prior.Uploaded || file.Uploaded > file.Size {
				return nil, errors.New("immutable import file or durable cursor changed")
			}
		}
		if old.State == "ready" {
			a, _ := json.Marshal(old.Mapping)
			b, _ := json.Marshal(in.Mapping)
			if string(a) != string(b) || old.Accepted != in.Accepted || old.Rejected != in.Rejected || old.AllowRejected != in.AllowRejected || old.MappingID != in.MappingID || old.MappingRevision != in.MappingRevision || !reflect.DeepEqual(old.Normalized, in.Normalized) || !reflect.DeepEqual(old.NormalizedSHA256, in.NormalizedSHA256) || !reflect.DeepEqual(old.Rejections, in.Rejections) || !reflect.DeepEqual(old.RejectionArtifacts, in.RejectionArtifacts) || !reflect.DeepEqual(old.RejectionSHA256, in.RejectionSHA256) {
				return nil, errors.New("committed analytical data is immutable")
			}
		}
	}
	var result sql.Result
	if expected == 0 {
		var n int
		if err = tx.QueryRow(`SELECT COUNT(*) FROM analysis_imports WHERE project_id=? AND state!='deleted'`, in.ProjectID).Scan(&n); err != nil {
			return nil, err
		}
		if n >= 1000 {
			return nil, errors.New("project dataset limit reached")
		}
		result, err = tx.Exec(`INSERT INTO analysis_imports(id,project_id,revision,state,payload,owner,lease_expires_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, in.ID, in.ProjectID, in.Revision, in.State, string(body), in.Owner, in.LeaseExpiresAt)
	} else {
		result, err = tx.Exec(`UPDATE analysis_imports SET revision=?,state=?,payload=?,owner=?,lease_expires_at=? WHERE id=? AND project_id=? AND revision=?`, in.Revision, in.State, string(body), in.Owner, in.LeaseExpiresAt, in.ID, in.ProjectID, expected)
	}
	if err != nil {
		return nil, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, ErrAnalysisRevision
	}
	if in.State == "cancelled" || in.State == "deleted" || in.State == "deleting" {
		if err = queueAnalysisCleanupTx(tx, "dataset", in.ProjectID, in.ID); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &in, nil
}
func (s *SQLiteStore) GetAnalysisImport(id string) (*AnalysisImport, error) {
	var body string
	var in AnalysisImport
	if err := s.db.QueryRow(`SELECT payload,owner,lease_expires_at FROM analysis_imports WHERE id=?`, id).Scan(&body, &in.Owner, &in.LeaseExpiresAt); err != nil {
		return nil, err
	}
	err := json.Unmarshal([]byte(body), &in)
	return &in, err
}
func (s *SQLiteStore) ListAnalysisImports(project string, limit, offset int) ([]AnalysisImport, error) {
	if limit < 1 || limit > 100 || offset < 0 {
		return nil, errors.New("invalid analysis page")
	}
	rows, err := s.db.Query(`SELECT payload FROM analysis_imports WHERE project_id=? AND state!='deleted' ORDER BY id LIMIT ? OFFSET ?`, project, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AnalysisImport{}
	for rows.Next() {
		var body string
		var in AnalysisImport
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &in); err != nil {
			return nil, err
		}
		result = append(result, in)
	}
	return result, rows.Err()
}
