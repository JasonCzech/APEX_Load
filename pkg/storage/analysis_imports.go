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
	ObjectKey string `json:"object_key,omitempty"`
	Offset    int64  `json:"offset"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
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
	PartitionManifestVersion int                                   `json:"partition_manifest_version,omitempty"`
	PartitionManifest        *analysis.NormalizedPartitionManifest `json:"partition_manifest,omitempty"`
	UploadWriterVersion      int                                   `json:"upload_writer_version,omitempty"`
	ID                       string                                `json:"id"`
	ProjectID                string                                `json:"project_id"`
	Name                     string                                `json:"name"`
	Revision                 int64                                 `json:"revision"`
	State                    string                                `json:"state"`
	Files                    []AnalysisImportFile                  `json:"files"`
	Mapping                  *analysis.ParquetMapping              `json:"mapping,omitempty"`
	AllowRejected            bool                                  `json:"allow_rejected"`
	Accepted                 int64                                 `json:"accepted"`
	Rejected                 int64                                 `json:"rejected"`
	Rejections               []analysis.ParquetRejectionSummary    `json:"rejections,omitempty"`
	RejectionArtifacts       []string                              `json:"rejection_artifacts,omitempty"`
	RejectionSHA256          map[string]string                     `json:"rejection_sha256,omitempty"`
	Normalized               []string                              `json:"normalized,omitempty"`
	NormalizedSHA256         map[string]string                     `json:"normalized_sha256,omitempty"`
	ObsoleteObjects          []string                              `json:"obsolete_objects,omitempty"`
	MappingID                string                                `json:"mapping_id,omitempty"`
	MappingRevision          int64                                 `json:"mapping_revision,omitempty"`
	RunID                    string                                `json:"run_id,omitempty"`
	Error                    string                                `json:"error,omitempty"`
	Owner                    string                                `json:"-"`
	LeaseExpiresAt           time.Time                             `json:"-"`
	CreatedAt                time.Time                             `json:"created_at"`
	UpdatedAt                time.Time                             `json:"updated_at"`
	// ArtifactSHA256 identifies a committed dataset's normalized artifacts,
	// so a saved selection can pin the evidence it was saved against. It is
	// computed for API responses, never stored.
	ArtifactSHA256 string `json:"artifact_sha256,omitempty"`
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
	body, err := analysis.MarshalBoundedAnalysisJSON(in, 2<<20)
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
	// Administrative cancellation revokes publication centrally. Existing callers
	// need not know the internal worker token, and cannot acquire a new one here.
	if in.State == "cancelled" || in.State == "deleting" || in.State == "deleted" {
		in.Owner = ""
		in.LeaseExpiresAt = time.Time{}
	}
	if in.State == "normalizing" || in.Owner != "" || !in.LeaseExpiresAt.IsZero() {
		return nil, errors.New("worker ownership requires dedicated analysis attempt methods")
	}
	if expected == 0 && (in.PartitionManifestVersion != 0 || in.PartitionManifest != nil || len(in.Normalized) != 0 || len(in.NormalizedSHA256) != 0 || len(in.RejectionArtifacts) != 0 || len(in.RejectionSHA256) != 0 || in.Accepted != 0 || in.Rejected != 0 || len(in.Rejections) != 0 || len(in.ObsoleteObjects) != 0) {
		return nil, errors.New("unclaimed analytical output metadata")
	}
	if expected == 0 && in.UploadWriterVersion != 0 && in.UploadWriterVersion != AnalysisUploadWriterVersion {
		return nil, ErrAnalysisAuthority
	}
	if expected == 0 && in.UploadWriterVersion == AnalysisUploadWriterVersion {
		for _, file := range in.Files {
			if file.Uploaded != 0 || file.SHA256 != "" || len(file.Chunks) != 0 {
				return nil, ErrAnalysisAuthority
			}
		}
	}
	if expected == 0 && in.State != "uploading" {
		return nil, errors.New("imports must begin in uploading state")
	}
	if expected > 0 {
		var oldBody, oldOwner string
		var oldExpiry time.Time
		if err = tx.QueryRow(`SELECT payload,owner,lease_expires_at FROM analysis_imports WHERE id=? AND project_id=? AND revision=?`, in.ID, in.ProjectID, expected).Scan(&oldBody, &oldOwner, &oldExpiry); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, ErrAnalysisRevision
			}
			return nil, err
		}
		var old AnalysisImport
		if err = json.Unmarshal([]byte(oldBody), &old); err != nil {
			return nil, err
		}
		if old.UploadWriterVersion != in.UploadWriterVersion {
			return nil, ErrAnalysisAuthority
		}
		if old.UploadWriterVersion == AnalysisUploadWriterVersion && !reflect.DeepEqual(old.Files, in.Files) {
			return nil, errors.New("upload cursors require physical writer publication")
		}
		allowed := map[string]string{"uploading": " uploading mapped cancelled", "mapped": " mapped cancelled", "normalizing": " cancelled", "failed": " mapped cancelled", "ready": " ready deleting", "cancelled": " cancelled", "deleting": " deleting deleted", "deleted": " deleted"}
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
		if !analysisImportOwnedOutputsEqual(old, in) {
			return nil, errors.New("owner-managed analytical outputs are immutable outside attempt methods")
		}
		if old.State != "uploading" {
			for i, file := range in.Files {
				if file.SHA256 != old.Files[i].SHA256 || !reflect.DeepEqual(file.Chunks, old.Files[i].Chunks) {
					return nil, errors.New("sealed analytical input is immutable")
				}
			}
		}
		if oldOwner != "" || !oldExpiry.IsZero() {
			if in.State != "cancelled" && in.State != "deleting" && in.State != "deleted" {
				return nil, errors.New("active analytical ownership cannot be changed by generic save")
			}
		}
		if old.State == "ready" {
			a, _ := json.Marshal(old.Mapping)
			b, _ := json.Marshal(in.Mapping)
			if string(a) != string(b) || old.AllowRejected != in.AllowRejected || old.MappingID != in.MappingID || old.MappingRevision != in.MappingRevision || !analysisImportOwnedOutputsEqual(old, in) {
				return nil, errors.New("committed analytical data is immutable")
			}
		}
	}
	if in.State == "cancelled" || in.State == "deleting" || in.State == "deleted" {
		if err = queueAnalysisImportAttemptsCleanupTx(tx, in.ProjectID, in.ID); err != nil {
			return nil, err
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

// These collections are all omitted when empty in durable/public JSON. A
// claimed in-memory [] therefore reloads as nil without changing any evidence.
// Equivalence is restricted to that zero-field representation: nonempty plans,
// hashes, summary rows, ordering and counts retain exact existing equality.
func analysisImportOwnedOutputsEqual(a, b AnalysisImport) bool {
	return a.PartitionManifestVersion == b.PartitionManifestVersion && reflect.DeepEqual(a.PartitionManifest, b.PartitionManifest) &&
		a.Accepted == b.Accepted && a.Rejected == b.Rejected &&
		(len(a.Normalized) == 0 && len(b.Normalized) == 0 || reflect.DeepEqual(a.Normalized, b.Normalized)) &&
		(len(a.NormalizedSHA256) == 0 && len(b.NormalizedSHA256) == 0 || reflect.DeepEqual(a.NormalizedSHA256, b.NormalizedSHA256)) &&
		(len(a.RejectionArtifacts) == 0 && len(b.RejectionArtifacts) == 0 || reflect.DeepEqual(a.RejectionArtifacts, b.RejectionArtifacts)) &&
		(len(a.RejectionSHA256) == 0 && len(b.RejectionSHA256) == 0 || reflect.DeepEqual(a.RejectionSHA256, b.RejectionSHA256)) &&
		(len(a.Rejections) == 0 && len(b.Rejections) == 0 || reflect.DeepEqual(a.Rejections, b.Rejections)) &&
		(len(a.ObsoleteObjects) == 0 && len(b.ObsoleteObjects) == 0 || reflect.DeepEqual(a.ObsoleteObjects, b.ObsoleteObjects))
}
