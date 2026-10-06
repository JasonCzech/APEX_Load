package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
)

const AnalysisUploadWriterVersion = 1
const analysisUploadMaxWriters = 16384
const analysisUploadChunkBytes = 4 << 20

var ErrAnalysisUploadInFlight = errors.New("upload cursor has an unjoined physical writer")

// Only an ordered project/import fence can prove an ambiguous Begin did not
// commit. A naked missing journal row supplies no such proof.
var ErrAnalysisUploadNotCommitted = errors.New("upload writer absence proved behind scope fence")

// A writer is an immutable physical-write intent, not a time-based lease. Its
// exact artifact plan and completion receipt outlive project/import deletion.
// No client endpoint accepts a physical completion assertion.
type AnalysisUploadWriter struct {
	ID, ProjectID, ImportID, FileID        string
	Revision, Offset, Bytes                int64
	SHA256, ObjectKey, TemporaryKey, State string
	CreatedAt                              time.Time
	PhysicallyClosed                       bool
	AppliedRevision, Uploaded              int64
	Rejection                              string
}
type AnalysisUploadRequest struct {
	ProjectID, ImportID, FileID string
	Revision, Offset, Bytes     int64
	SHA256                      string
}
type AnalysisUploadWriterStore interface {
	BeginAnalysisUploadWriter(context.Context, AnalysisUploadRequest) (*AnalysisUploadWriter, error)
	CompleteAnalysisUploadWriter(context.Context, string, string, bool) (*AnalysisUploadWriter, error)
	ResolveAnalysisUploadWriter(context.Context, string, string, string) (*AnalysisUploadWriter, error)
	AnalysisUploadWriterCleanupPlan(context.Context, AnalysisCleanupJob) (AnalysisAttemptCleanupPlan, error)
}

var analysisUploadDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

func analysisUploadObjectKey(w AnalysisUploadWriter) string {
	return analysisAttemptKey(w.ProjectID, w.ImportID, fmt.Sprintf("chunk-%s-%012d-%s-%s", w.FileID, w.Offset, w.SHA256, w.ID))
}
func analysisUploadValidate(w *AnalysisUploadWriter) error {
	if w == nil || len(w.ProjectID) < 1 || len(w.ProjectID) > 128 || w.Revision < 1 || w.Revision >= 1<<62 || w.Offset < 0 || w.Offset > (20<<30)-w.Bytes || w.Bytes < 1 || w.Bytes > analysisUploadChunkBytes || !analysisUploadDigest.MatchString(w.SHA256) || w.CreatedAt.IsZero() || len(w.Rejection) > 128 {
		return ErrAnalysisAuthority
	}
	for _, id := range []string{w.ID, w.ImportID, w.FileID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return ErrAnalysisAuthority
		}
	}
	if w.ObjectKey != analysisUploadObjectKey(*w) || w.TemporaryKey != analysisAttemptKey(w.ProjectID, w.ImportID, "upload-"+w.ID+".tmp") {
		return ErrAnalysisAuthority
	}
	switch w.State {
	case "pending":
		if w.PhysicallyClosed || w.AppliedRevision != 0 {
			return ErrAnalysisAuthority
		}
	case "accepted":
		if !w.PhysicallyClosed || w.AppliedRevision <= w.Revision || w.Uploaded != w.Offset+w.Bytes {
			return ErrAnalysisAuthority
		}
	case "abandoned":
	default:
		return ErrAnalysisAuthority
	}
	return nil
}
func analysisUploadLoad(ctx context.Context, tx *storeTx, project, id string) (*AnalysisUploadWriter, error) {
	var raw, importID, fileID string
	var offset int64
	q := `SELECT payload,import_id,file_id,chunk_offset FROM analysis_upload_writers WHERE id=? AND project_id=?`
	if tx.dialect == storeDialectPostgres {
		q += ` FOR UPDATE`
	}
	if err := tx.QueryRowContext(ctx, q, id, project).Scan(&raw, &importID, &fileID, &offset); err != nil {
		return nil, err
	}
	var w AnalysisUploadWriter
	if len(raw) > 4096 || json.Unmarshal([]byte(raw), &w) != nil || w.ID != id || w.ProjectID != project || w.ImportID != importID || w.FileID != fileID || w.Offset != offset || analysisUploadValidate(&w) != nil {
		return nil, ErrAnalysisAuthority
	}
	return &w, nil
}
func analysisUploadWrite(ctx context.Context, tx *storeTx, w *AnalysisUploadWriter, insert bool) error {
	if analysisUploadValidate(w) != nil {
		return ErrAnalysisAuthority
	}
	raw, err := json.Marshal(w)
	if err != nil || len(raw) > 4096 {
		return ErrAnalysisAuthority
	}
	q := `UPDATE analysis_upload_writers SET payload=? WHERE id=? AND project_id=? AND import_id=?`
	args := []any{string(raw), w.ID, w.ProjectID, w.ImportID}
	if insert {
		q = `INSERT INTO analysis_upload_writers(id,project_id,import_id,file_id,chunk_offset,payload) VALUES(?,?,?,?,?,?) ON CONFLICT(id) DO NOTHING`
		args = []any{w.ID, w.ProjectID, w.ImportID, w.FileID, w.Offset, string(raw)}
	}
	result, err := tx.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrAnalysisAuthority
	}
	return nil
}

func (s *SQLiteStore) BeginAnalysisUploadWriter(ctx context.Context, req AnalysisUploadRequest) (*AnalysisUploadWriter, error) {
	if len(req.ProjectID) < 1 || len(req.ProjectID) > 128 || req.Revision < 1 || req.Revision >= 1<<62 || req.Offset < 0 || req.Offset > (20<<30)-req.Bytes || req.Bytes < 1 || req.Bytes > analysisUploadChunkBytes || !analysisUploadDigest.MatchString(req.SHA256) {
		return nil, ErrAnalysisAuthority
	}
	for _, id := range []string{req.ImportID, req.FileID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return nil, ErrAnalysisAuthority
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	present, err := analysisUploadLockProject(ctx, tx, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, ErrAnalysisAuthority
	}
	in, err := analysisAttemptLoad(ctx, tx, req.ImportID, req.ProjectID)
	if err != nil {
		return nil, err
	}
	if in.State != "uploading" || in.Revision != req.Revision || in.UploadWriterVersion != AnalysisUploadWriterVersion {
		return nil, ErrAnalysisAuthority
	}
	var file *AnalysisImportFile
	for i := range in.Files {
		if in.Files[i].ID == req.FileID {
			file = &in.Files[i]
			break
		}
	}
	if file == nil || file.Uploaded != req.Offset || req.Bytes > file.Size-req.Offset || req.Bytes != analysisUploadChunkBytes && req.Offset+req.Bytes != file.Size {
		return nil, ErrAnalysisAuthority
	}
	// Project/import locks serialize contenders before any immutable object is
	// opened. An expired request or process owner never proves old writer closure.
	rows, err := tx.QueryContext(ctx, `SELECT id,payload FROM analysis_upload_writers WHERE project_id=? AND import_id=? AND file_id=? AND chunk_offset=?`, req.ProjectID, req.ImportID, req.FileID, req.Offset)
	if err != nil {
		return nil, err
	}
	blocked := false
	seen := 0
	for rows.Next() {
		seen++
		if seen > analysisUploadMaxWriters {
			rows.Close()
			return nil, ErrAnalysisAuthority
		}
		var rowID, raw string
		var old AnalysisUploadWriter
		if err = rows.Scan(&rowID, &raw); err != nil {
			rows.Close()
			return nil, err
		}
		if len(raw) > 4096 || json.Unmarshal([]byte(raw), &old) != nil || old.ID != rowID || old.ProjectID != req.ProjectID || old.ImportID != req.ImportID || old.FileID != req.FileID || old.Offset != req.Offset || analysisUploadValidate(&old) != nil {
			rows.Close()
			return nil, ErrAnalysisAuthority
		}
		if !old.PhysicallyClosed || old.State == "pending" {
			blocked = true
		}
	}
	scanErr, closeErr := rows.Err(), rows.Close()
	if err = errors.Join(scanErr, closeErr); err != nil {
		return nil, err
	}
	if blocked {
		return nil, ErrAnalysisUploadInFlight
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM analysis_upload_writers WHERE project_id=? AND import_id=?`, req.ProjectID, req.ImportID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= analysisUploadMaxWriters {
		return nil, ErrAnalysisAuthority
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return nil, err
	}
	w := &AnalysisUploadWriter{ID: uuid.NewString(), ProjectID: req.ProjectID, ImportID: req.ImportID, FileID: req.FileID, Revision: req.Revision, Offset: req.Offset, Bytes: req.Bytes, SHA256: req.SHA256, State: "pending", CreatedAt: now}
	w.ObjectKey = analysisUploadObjectKey(*w)
	w.TemporaryKey = analysisAttemptKey(w.ProjectID, w.ImportID, "upload-"+w.ID+".tmp")
	if err = analysisUploadWrite(ctx, tx, w, true); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return w, ErrAnalysisPublicationUnknown
	}
	return w, nil
}

// Complete is trusted parent physical proof, called after all sink/root/bucket
// owners joined. The exact intent seal and durable cursor become visible in one
// transaction; no cleanup can observe a seal-before-cursor publication gap.
func (s *SQLiteStore) CompleteAnalysisUploadWriter(ctx context.Context, project, writerID string, accepted bool) (*AnalysisUploadWriter, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	projectPresent, err := analysisUploadLockProject(ctx, tx, project)
	if err != nil {
		return nil, ErrAnalysisPublicationUnknown
	}
	var importID string
	if err = tx.QueryRowContext(ctx, `SELECT import_id FROM analysis_upload_writers WHERE id=? AND project_id=?`, writerID, project).Scan(&importID); err != nil {
		return nil, err
	}
	in, loadErr := analysisAttemptLoad(ctx, tx, importID, project)
	if loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows) {
		return nil, loadErr
	}
	w, err := analysisUploadLoad(ctx, tx, project, writerID)
	if err != nil {
		return nil, err
	}
	if w.PhysicallyClosed {
		if tx.Commit() != nil {
			return nil, ErrAnalysisPublicationUnknown
		}
		return w, nil
	}
	// Cancellation or deletion can revoke publication while leaving the physical
	// owner pending. The exact late owner can still supply closure proof.
	w.PhysicallyClosed = true
	eligible := projectPresent && accepted && w.State == "pending" && in != nil && in.State == "uploading" && in.Revision == w.Revision && in.UploadWriterVersion == AnalysisUploadWriterVersion
	index := -1
	if eligible {
		for i := range in.Files {
			if in.Files[i].ID == w.FileID && in.Files[i].Uploaded == w.Offset && w.Bytes <= in.Files[i].Size-w.Offset {
				index = i
				break
			}
		}
		eligible = index >= 0
	}
	oldRevision := int64(0)
	if eligible {
		oldRevision = in.Revision
		file := &in.Files[index]
		file.Chunks = append(file.Chunks, AnalysisChunk{Offset: w.Offset, Size: w.Bytes, SHA256: w.SHA256, ObjectKey: w.ObjectKey})
		file.Uploaded += w.Bytes
		in.Revision++
		in.UpdatedAt, err = preciseDatabaseTimeContext(ctx, tx, tx.dialect)
		if err != nil {
			return nil, err
		}
		// A known metadata-limit refusal must still durably seal the physically
		// joined writer and queue its exact plan; retrying publication cannot grow
		// a bounded metadata row and must not retain the parent forever.
		// Use the exact preflight of the durable metadata writer. Raw JSON
		// length can fit while its conservative escaped metadata budget does
		// not; that known refusal must still seal and queue the closed owner.
		if _, encodeErr := analysis.MarshalBoundedAnalysisJSON(in, 2<<20); encodeErr != nil {
			eligible = false
			w.Rejection = "metadata_limit"
		}
	}
	if eligible {
		w.State = "accepted"
		w.AppliedRevision = in.Revision
		w.Uploaded = in.Files[index].Uploaded
		if err = analysisUploadWrite(ctx, tx, w, false); err != nil {
			return nil, err
		}
		if err = analysisAttemptUpdate(ctx, tx, in, oldRevision, in.Owner, "uploading"); err != nil {
			return nil, err
		}
	} else {
		w.State = "abandoned"
		if err = analysisUploadWrite(ctx, tx, w, false); err != nil {
			return nil, err
		}
		if err = queueAnalysisCleanupTx(tx, "upload-writer", project, w.ID); err != nil {
			return nil, err
		}
	}
	if tx.Commit() != nil {
		return w, ErrAnalysisPublicationUnknown
	}
	return w, nil
}

// Resolution is private owner reconciliation, not a public project read. Its
// retained receipt is usable after deletion; public responses still check the
// current import/project and never expose an outliving journal as a dataset.
func analysisUploadLockProject(ctx context.Context, tx *storeTx, project string) (bool, error) {
	result, err := tx.ExecContext(ctx, `UPDATE projects SET updated_at=updated_at WHERE id=?`, project)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	if err != nil || changed > 1 {
		return false, ErrAnalysisPublicationUnknown
	}
	return changed == 1, nil
}

func (s *SQLiteStore) ResolveAnalysisUploadWriter(ctx context.Context, project, importID, id string) (*AnalysisUploadWriter, error) {
	if len(project) < 1 || len(project) > 128 {
		return nil, ErrAnalysisAuthority
	}
	for _, value := range []string{importID, id} {
		parsed, err := uuid.Parse(value)
		if err != nil || parsed.String() != value {
			return nil, ErrAnalysisAuthority
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	// Begin owns these same rows before its journal INSERT. An invisible INSERT
	// must therefore wait behind this fence rather than masquerade as rollback.
	// Completed deletion removes these rows only after earlier owners committed;
	// retained journals then remain independently readable under their own lock.
	if _, err = analysisUploadLockProject(ctx, tx, project); err != nil {
		return nil, ErrAnalysisPublicationUnknown
	}
	if _, err = analysisAttemptLoad(ctx, tx, importID, project); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	w, err := analysisUploadLoad(ctx, tx, project, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if tx.Commit() != nil {
				return nil, ErrAnalysisPublicationUnknown
			}
			return nil, ErrAnalysisUploadNotCommitted
		}
		return nil, err
	}
	if w.ImportID != importID {
		return nil, ErrAnalysisAuthority
	}
	if tx.Commit() != nil {
		return nil, ErrAnalysisPublicationUnknown
	}
	return w, nil
}

func analysisUploadQueueImportTx(tx *storeTx, project, id string) error {
	rows, err := tx.Query(`SELECT id FROM analysis_upload_writers WHERE project_id=? AND import_id=?`, project, id)
	if err != nil {
		return err
	}
	ids := []string{}
	for rows.Next() {
		var wid string
		if err = rows.Scan(&wid); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, wid)
		if len(ids) > analysisUploadMaxWriters {
			rows.Close()
			return ErrAnalysisAuthority
		}
	}
	scanErr, closeErr := rows.Err(), rows.Close()
	if err = errors.Join(scanErr, closeErr); err != nil {
		return err
	}
	for _, wid := range ids {
		w, err := analysisUploadLoad(context.Background(), tx, project, wid)
		if err != nil {
			return err
		}
		w.State = "abandoned"
		if err = analysisUploadWrite(context.Background(), tx, w, false); err != nil {
			return err
		}
		if err = queueAnalysisCleanupTx(tx, "upload-writer", project, w.ID); err != nil {
			return err
		}
	}
	return nil
}
func analysisUploadQuiescedTx(ctx context.Context, tx *storeTx, project, id string) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,file_id,chunk_offset,payload FROM analysis_upload_writers WHERE project_id=? AND import_id=?`, project, id)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	count := 0
	ready := true
	for rows.Next() {
		count++
		if count > analysisUploadMaxWriters {
			return false, ErrAnalysisAuthority
		}
		var rowID, fileID, raw string
		var offset int64
		var w AnalysisUploadWriter
		if err = rows.Scan(&rowID, &fileID, &offset, &raw); err != nil {
			return false, err
		}
		if len(raw) > 4096 || json.Unmarshal([]byte(raw), &w) != nil || w.ID != rowID || w.FileID != fileID || w.Offset != offset || w.ProjectID != project || w.ImportID != id || analysisUploadValidate(&w) != nil {
			return false, ErrAnalysisAuthority
		}
		if !w.PhysicallyClosed || w.State == "pending" {
			ready = false
		}
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	if err = rows.Close(); err != nil {
		return false, err
	}
	return ready, nil
}
func (s *SQLiteStore) AnalysisUploadWriterCleanupPlan(ctx context.Context, job AnalysisCleanupJob) (AnalysisAttemptCleanupPlan, error) {
	result := AnalysisAttemptCleanupPlan{Blocked: true}
	if job.Kind != "upload-writer" {
		return result, ErrAnalysisAuthority
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return result, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, job.ProjectID); err != nil {
		var count int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, job.ProjectID).Scan(&count); e != nil || count != 0 {
			return result, ErrAnalysisPublicationUnknown
		}
	}
	var importID string
	if err = tx.QueryRowContext(ctx, `SELECT import_id FROM analysis_upload_writers WHERE id=? AND project_id=?`, job.ResourceID, job.ProjectID).Scan(&importID); err != nil {
		return result, err
	}
	in, loadErr := analysisAttemptLoad(ctx, tx, importID, job.ProjectID)
	if loadErr != nil && !errors.Is(loadErr, sql.ErrNoRows) {
		return result, loadErr
	}
	w, err := analysisUploadLoad(ctx, tx, job.ProjectID, job.ResourceID)
	if err != nil {
		return result, err
	}
	if !w.PhysicallyClosed || w.State != "abandoned" {
		return result, nil
	}
	if in != nil && in.State != "cancelled" && in.State != "deleting" && in.State != "deleted" {
		for _, file := range in.Files {
			for _, chunk := range file.Chunks {
				if chunk.ObjectKey == w.ObjectKey {
					return result, nil
				}
			}
		}
	}
	result.Blocked = false
	result.Keys = []string{w.TemporaryKey, w.ObjectKey}
	if tx.Commit() != nil {
		return AnalysisAttemptCleanupPlan{Blocked: true}, ErrAnalysisPublicationUnknown
	}
	return result, nil
}

// This helper validates the new opaque owner key and the historical content
// addressed key before either is granted artifact read authority.
func AnalysisUploadChunkObjectKey(project, importID string, file AnalysisImportFile, chunk AnalysisChunk) (string, error) {
	for _, id := range []string{importID, file.ID} {
		parsed, err := uuid.Parse(id)
		if err != nil || parsed.String() != id {
			return "", ErrAnalysisAuthority
		}
	}
	if project == "" || len(project) > 128 {
		return "", ErrAnalysisAuthority
	}
	if chunk.Offset < 0 || chunk.Size < 1 || chunk.Size > analysisUploadChunkBytes || !analysisUploadDigest.MatchString(chunk.SHA256) {
		return "", ErrAnalysisAuthority
	}
	prefix := analysisAttemptKey(project, importID, "")
	legacy := prefix + fmt.Sprintf("chunk-%s-%012d-%s", file.ID, chunk.Offset, chunk.SHA256)
	if chunk.ObjectKey == "" {
		return legacy, nil
	}
	base := legacy + "-"
	if !strings.HasPrefix(chunk.ObjectKey, base) {
		return "", ErrAnalysisAuthority
	}
	id := strings.TrimPrefix(chunk.ObjectKey, base)
	parsed, err := uuid.Parse(id)
	if err != nil || parsed.String() != id {
		return "", ErrAnalysisAuthority
	}
	return chunk.ObjectKey, nil
}
