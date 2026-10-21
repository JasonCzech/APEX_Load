package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"time"
)

// RCAAutoAdmission is a retry intent, not an analysis. Evidence is frozen only
// when admission succeeds. Raw is an opaque compare-delete token.
type RCAAutoAdmission struct {
	Version     int       `json:"version"`
	TestID      string    `json:"test_id"`
	ProjectID   string    `json:"project_id"`
	Incarnation string    `json:"incarnation"`
	StartTime   time.Time `json:"start_time"`
	EndTime     time.Time `json:"end_time"`
	Token       string    `json:"token"`
	Raw         string    `json:"-"`
	Key         string    `json:"-"`
}

const rcaAutoAdmissionSuffix = "rca-auto-admission:v1"

func rcaAutoAdmissionKey(id string) string {
	return RunNotificationClaimPrefix(id) + rcaAutoAdmissionSuffix
}

// The notification namespace uses the existing run/project deletion graph.
func (s *SQLiteStore) rcaAutoAdmissionIdentity(tx *storeTx, id string) (*RCAAutoAdmission, error) {
	if s.db.dialect == storeDialectSQLite {
		if _, err := tx.Exec(`UPDATE tests SET id=id WHERE id=?`, id); err != nil {
			return nil, err
		}
	}
	q := `SELECT id,COALESCE(NULLIF(project_id,''),?),start_time,end_time,COALESCE(initial_provenance_json,'') FROM tests WHERE id=? AND status IN ('COMPLETE','FAILED','ABORTED') AND EXISTS(SELECT 1 FROM projects p WHERE p.id=COALESCE(NULLIF(tests.project_id,''),?)) AND NOT EXISTS(SELECT 1 FROM archive_cleanup_jobs c WHERE c.test_id=tests.id)`
	if s.db.dialect == storeDialectPostgres {
		q += ` FOR UPDATE`
	}
	var in RCAAutoAdmission
	var provenance string
	var end sql.NullTime
	if err := tx.QueryRow(q, DefaultProjectID, id, DefaultProjectID).Scan(&in.TestID, &in.ProjectID, &in.StartTime, &end, &provenance); err != nil {
		return nil, err
	}
	incarnation, err := testRunIncarnationJSON(provenance)
	if err != nil {
		return nil, err
	}
	in.Version = 1
	in.Incarnation = incarnation
	if end.Valid {
		in.EndTime = end.Time
	}
	return &in, nil
}
func sameRCAAutoAdmission(a, b *RCAAutoAdmission) bool {
	return a.TestID == b.TestID && a.ProjectID == b.ProjectID && a.Incarnation == b.Incarnation && a.StartTime.Equal(b.StartTime) && a.EndTime.Equal(b.EndTime)
}
func (s *SQLiteStore) EnsureRCAAutoAdmission(ctx context.Context, id string) (*RCAAutoAdmission, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	in, err := s.rcaAutoAdmissionIdentity(tx, id)
	if err != nil {
		return nil, err
	}
	key := rcaAutoAdmissionKey(in.TestID)
	var old string
	err = tx.QueryRow(`SELECT value FROM app_settings WHERE key=?`, key).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		var prior RCAAutoAdmission
		if len(old) > 4096 || json.Unmarshal([]byte(old), &prior) != nil || prior.Version != 1 {
			return nil, errors.New("unsupported RCA auto-admission intent")
		}
	}
	// Each attachment replaces the acknowledgement token, even for the same
	// incarnation: an older admission must not acknowledge a newer refresh.
	in.Token = uuid.NewString()
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if len(raw) > 4096 {
		return nil, errors.New("RCA admission identity too large")
	}
	if _, err = tx.Exec(`INSERT INTO app_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, key, string(raw), time.Now().UTC()); err != nil {
		return nil, err
	}
	in.Raw = string(raw)
	in.Key = key
	return in, tx.Commit()
}

// Keyset progress includes unsupported records, so they cannot starve later runs.
func (s *SQLiteStore) ListRCAAutoAdmissions(ctx context.Context, after string, limit int) ([]RCAAutoAdmission, string, error) {
	if limit < 1 || limit > 32 {
		return nil, "", errors.New("invalid RCA admission scan limit")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT key,value FROM app_settings WHERE key>? AND key LIKE 'run:notifications:%:rca-auto-admission:v1' ORDER BY key LIMIT ?`, after, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []RCAAutoAdmission
	next := ""
	for rows.Next() {
		var key, raw string
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, "", err
		}
		next = key
		var in RCAAutoAdmission
		if len(raw) > 4096 || json.Unmarshal([]byte(raw), &in) != nil || in.Version != 1 || in.Token == "" || key != rcaAutoAdmissionKey(in.TestID) {
			continue
		}
		in.Key = key
		in.Raw = raw
		out = append(out, in)
	}
	return out, next, rows.Err()
}
func (s *SQLiteStore) RCAAutoAdmissionCurrent(ctx context.Context, in RCAAutoAdmission) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	current, err := s.rcaAutoAdmissionIdentity(tx, in.TestID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var raw string
	err = tx.QueryRow(`SELECT value FROM app_settings WHERE key=?`, in.Key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return sameRCAAutoAdmission(current, &in) && raw == in.Raw, tx.Commit()
}
func (s *SQLiteStore) FinishRCAAutoAdmission(ctx context.Context, in RCAAutoAdmission) error {
	if in.Key != rcaAutoAdmissionKey(in.TestID) || in.Raw == "" {
		return errors.New("invalid RCA admission completion")
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM app_settings WHERE key=? AND value=?`, in.Key, in.Raw)
	return err
}

// ErrRCAAutoAdmissionSuperseded refuses publication from a stale retry observer.
var ErrRCAAutoAdmissionSuperseded = errors.New("RCA automatic admission was superseded")

func (s *SQLiteStore) lockRCAAutoAdmissionTx(tx *storeTx, in *RCAAutoAdmission) error {
	if in == nil {
		return nil
	}
	current, err := s.rcaAutoAdmissionIdentity(tx, in.TestID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRCAAutoAdmissionSuperseded
	}
	if err != nil {
		return err
	}
	var raw string
	err = tx.QueryRow(`SELECT value FROM app_settings WHERE key=?`, in.Key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRCAAutoAdmissionSuperseded
	}
	if err != nil {
		return err
	}
	if in.Key != rcaAutoAdmissionKey(in.TestID) || !sameRCAAutoAdmission(current, in) || raw != in.Raw {
		return ErrRCAAutoAdmissionSuperseded
	}
	return nil
}
func (s *SQLiteStore) AppendRCAEvidenceForAutoAdmission(ctx context.Context, in RCAAutoAdmission, payload []byte, now time.Time, reuse func(*RCAEvidenceRevision) bool) (*RCAEvidenceRevision, error) {
	return s.appendRCAEvidenceRevision(ctx, in.ProjectID, in.TestID, payload, now, reuse, &in)
}
func (s *SQLiteStore) CreateRCAAnalysisForAutoAdmission(ctx context.Context, in RCAAutoAdmission, digest string, payload []byte, now time.Time) (*RCAAnalysis, bool, error) {
	return s.createRCAAnalysisWithOrigin(ctx, in.ProjectID, in.TestID, digest, RCAOriginAutomatic, payload, now, &in)
}
