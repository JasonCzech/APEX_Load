package storage

import (
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Root-cause analysis states. An analysis is queued when requested, running
// while a controller holds its lease, and ends completed, failed or
// cancelled.
const (
	RCAStateQueued    = "queued"
	RCAStateRunning   = "running"
	RCAStateCompleted = "completed"
	RCAStateFailed    = "failed"
	RCAStateCancelled = "cancelled"
)

// Root-cause analysis origins. A requested analysis was asked for by a
// person or client; an automatic one was queued when the run's archive was
// finalized. Requested analyses are claimed first, and at most one automatic
// analysis runs at a time, so a request always finds a free worker slot.
const (
	RCAOriginRequested = "requested"
	RCAOriginAutomatic = "automatic"
)

// Bounds on a run's analytical history: 50 live analyses (queued, running or
// completed) of at most 2 MiB each keep a run's stored assessments near 100
// MiB at worst. Failed and cancelled analyses do not count against the cap;
// the oldest beyond MaxRCARevisionsPerRun are pruned when a new one is
// queued. Evidence revisions are capped too, but a revision no live analysis
// cites is pruned before the cap refuses a new one.
const (
	MaxRCARevisionsPerRun = 50
	MaxRCAPayloadBytes    = 2 << 20
)

// ErrRCALimit means a run already holds the maximum live analyses or cited
// evidence revisions.
var ErrRCALimit = errors.New("run root-cause revision limit reached: cancel or wait for its queued analyses, or delete older runs' analyses")

// RCAEvidenceRevision is one frozen set of evidence an analysis can read.
// Revisions are append-only per run.
type RCAEvidenceRevision struct {
	ID        string          `json:"id"`
	ProjectID string          `json:"project_id"`
	TestID    string          `json:"test_id"`
	Revision  int64           `json:"revision"`
	CreatedAt time.Time       `json:"created_at"`
	Payload   json.RawMessage `json:"payload"`
}

// RCAAnalysis is one root-cause analysis job and, once completed, its
// assessment.
type RCAAnalysis struct {
	ID             string          `json:"id"`
	ProjectID      string          `json:"project_id"`
	TestID         string          `json:"test_id"`
	Revision       int64           `json:"revision"`
	State          string          `json:"state"`
	CreatedAt      time.Time       `json:"created_at"`
	CompletedAt    *time.Time      `json:"completed_at,omitempty"`
	InputDigest    string          `json:"input_digest"`
	Origin         string          `json:"origin"`
	Payload        json.RawMessage `json:"payload"`
	Owner          string          `json:"-"`
	LeaseExpiresAt *time.Time      `json:"-"`
}

// RCAStore keeps a run's evidence revisions and analyses.
type RCAStore interface {
	AppendRCAEvidenceRevision(project, test string, payload []byte, now time.Time, reuse func(latest *RCAEvidenceRevision) bool) (*RCAEvidenceRevision, error)
	LatestRCAEvidenceRevision(project, test string) (*RCAEvidenceRevision, error)
	GetRCAEvidenceRevision(project, test string, revision int64) (*RCAEvidenceRevision, error)
	CreateRCAAnalysis(project, test, inputDigest string, payload []byte, now time.Time) (*RCAAnalysis, bool, error)
	CreateRCAAnalysisWithOrigin(project, test, inputDigest, origin string, payload []byte, now time.Time) (*RCAAnalysis, bool, error)
	LatestRCAAnalyses(project string, tests []string) (map[string]RCARunAnalyses, error)
	GetRCAAnalysis(project, test, id string) (*RCAAnalysis, error)
	LatestCompletedRCAAnalysis(project, test string) (*RCAAnalysis, error)
	ClaimRCAAnalysis(owner string, now time.Time, lease time.Duration) (*RCAAnalysis, error)
	RenewRCAAnalysis(id, owner string, now time.Time, lease time.Duration) (bool, error)
	CompleteRCAAnalysisFenced(id, owner, state string, payload []byte, now time.Time, leaseName, leaseOwner string, generation uint64) (bool, error)
	CancelRCAAnalysis(project, test, id string, now time.Time) (bool, error)
}

func validRCAPayload(payload []byte) bool {
	return len(payload) > 0 && len(payload) <= MaxRCAPayloadBytes && json.Valid(payload)
}

func validRCADigest(digest string) bool {
	decoded, err := hex.DecodeString(digest)
	return err == nil && len(decoded) == 32
}

// lockRCARunTx serializes a run's root-cause writes with its project's other
// owned mutations and confirms the run belongs to the project.
func lockRCARunTx(tx *storeTx, project, test string) error {
	if _, err := lockProjectForOwnedMutation(tx, project); err != nil {
		return err
	}
	var owner string
	if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM tests WHERE id = ?`, test).Scan(&owner); err != nil {
		return err
	}
	// Runs stored before project scoping have no project ID and belong to the
	// default project.
	if normalizeProjectID(owner) != normalizeProjectID(project) {
		return errors.New("run belongs to another project")
	}
	return nil
}

// AppendRCAEvidenceRevision freezes a new evidence revision for a run. When
// reuse accepts the run's latest revision, that one is returned instead; the
// check runs under the run's lock, so concurrent captures of the same
// evidence share one revision.
func (s *SQLiteStore) AppendRCAEvidenceRevision(project, test string, payload []byte, now time.Time, reuse func(latest *RCAEvidenceRevision) bool) (*RCAEvidenceRevision, error) {
	project, test = strings.TrimSpace(project), strings.TrimSpace(test)
	if project == "" || test == "" || !validRCAPayload(payload) {
		return nil, errors.New("invalid root-cause evidence revision")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockRCARunTx(tx, project, test); err != nil {
		return nil, err
	}
	if reuse != nil {
		current, err := scanRCAEvidence(tx.QueryRow(`SELECT `+rcaEvidenceColumns+` FROM rca_evidence_revisions WHERE test_id = ? ORDER BY revision DESC LIMIT 1`, test))
		if err == nil && reuse(current) {
			return current, nil
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
	}
	var latest int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(revision), 0) FROM rca_evidence_revisions WHERE test_id = ?`, test).Scan(&latest); err != nil {
		return nil, err
	}
	if err := pruneRCAEvidenceRevisionsTx(tx, test); err != nil {
		return nil, err
	}
	revision := &RCAEvidenceRevision{ID: uuid.NewString(), ProjectID: project, TestID: test, Revision: latest + 1, CreatedAt: now.UTC(), Payload: append(json.RawMessage(nil), payload...)}
	if _, err := tx.Exec(`INSERT INTO rca_evidence_revisions(id, project_id, test_id, revision, created_at, payload) VALUES(?,?,?,?,?,?)`,
		revision.ID, project, test, revision.Revision, revision.CreatedAt, string(payload)); err != nil {
		return nil, err
	}
	return revision, tx.Commit()
}

// rcaLiveStates are the analysis states that hold their inputs: queued and
// running ones will read them, completed ones cite them.
var rcaLiveStates = []any{RCAStateQueued, RCAStateRunning, RCAStateCompleted}

// pruneRCAEvidenceRevisionsTx makes room for one more evidence revision when
// the run holds MaxRCARevisionsPerRun: it deletes the oldest revisions that
// no live analysis cites (the api freezes the cited revision at
// payload.inputs.evidence_revision), and returns ErrRCALimit when every
// revision is cited.
func pruneRCAEvidenceRevisionsTx(tx *storeTx, test string) error {
	var count int64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM rca_evidence_revisions WHERE test_id = ?`, test).Scan(&count); err != nil {
		return err
	}
	if count < MaxRCARevisionsPerRun {
		return nil
	}
	cited, err := rcaCitedEvidenceRevisionsTx(tx, test)
	if err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT revision FROM rca_evidence_revisions WHERE test_id = ? ORDER BY revision ASC`, test)
	if err != nil {
		return err
	}
	var prunable []int64
	for rows.Next() {
		var revision int64
		if err := rows.Scan(&revision); err != nil {
			rows.Close()
			return err
		}
		if !cited[revision] {
			prunable = append(prunable, revision)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, revision := range prunable {
		if count < MaxRCARevisionsPerRun {
			break
		}
		if _, err := tx.Exec(`DELETE FROM rca_evidence_revisions WHERE test_id = ? AND revision = ?`, test, revision); err != nil {
			return err
		}
		count--
	}
	if count >= MaxRCARevisionsPerRun {
		return ErrRCALimit
	}
	return nil
}

// rcaCitedEvidenceRevisionsTx lists the evidence revisions a run's live
// analyses cite.
func rcaCitedEvidenceRevisionsTx(tx *storeTx, test string) (map[int64]bool, error) {
	rows, err := tx.Query(`SELECT payload FROM rca_analyses WHERE test_id = ? AND state IN (?, ?, ?)`, append([]any{test}, rcaLiveStates...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cited := map[int64]bool{}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var frozen struct {
			Inputs struct {
				EvidenceRevision int64 `json:"evidence_revision"`
			} `json:"inputs"`
		}
		if json.Unmarshal([]byte(payload), &frozen) == nil {
			cited[frozen.Inputs.EvidenceRevision] = true
		}
	}
	return cited, rows.Err()
}

func scanRCAEvidence(row interface{ Scan(...any) error }) (*RCAEvidenceRevision, error) {
	var revision RCAEvidenceRevision
	var payload string
	if err := row.Scan(&revision.ID, &revision.ProjectID, &revision.TestID, &revision.Revision, &revision.CreatedAt, &payload); err != nil {
		return nil, err
	}
	revision.CreatedAt = revision.CreatedAt.UTC()
	revision.Payload = json.RawMessage(payload)
	return &revision, nil
}

const rcaEvidenceColumns = `id, project_id, test_id, revision, created_at, payload`

// LatestRCAEvidenceRevision returns a run's newest evidence revision, or nil
// when it has none.
func (s *SQLiteStore) LatestRCAEvidenceRevision(project, test string) (*RCAEvidenceRevision, error) {
	revision, err := scanRCAEvidence(s.db.QueryRow(`SELECT `+rcaEvidenceColumns+` FROM rca_evidence_revisions WHERE project_id = ? AND test_id = ? ORDER BY revision DESC LIMIT 1`, project, test))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return revision, err
}

// GetRCAEvidenceRevision returns one evidence revision of a run.
func (s *SQLiteStore) GetRCAEvidenceRevision(project, test string, revision int64) (*RCAEvidenceRevision, error) {
	return scanRCAEvidence(s.db.QueryRow(`SELECT `+rcaEvidenceColumns+` FROM rca_evidence_revisions WHERE project_id = ? AND test_id = ? AND revision = ?`, project, test, revision))
}

const rcaAnalysisColumns = `id, project_id, test_id, revision, state, created_at, completed_at, input_digest, payload, owner, lease_expires_at, origin`

func scanRCAAnalysis(row interface{ Scan(...any) error }) (*RCAAnalysis, error) {
	var analysis RCAAnalysis
	var completed, lease sql.NullTime
	var payload string
	if err := row.Scan(&analysis.ID, &analysis.ProjectID, &analysis.TestID, &analysis.Revision, &analysis.State, &analysis.CreatedAt, &completed, &analysis.InputDigest, &payload, &analysis.Owner, &lease, &analysis.Origin); err != nil {
		return nil, err
	}
	analysis.CreatedAt = analysis.CreatedAt.UTC()
	if completed.Valid {
		at := completed.Time.UTC()
		analysis.CompletedAt = &at
	}
	if lease.Valid {
		at := lease.Time.UTC()
		analysis.LeaseExpiresAt = &at
	}
	analysis.Payload = json.RawMessage(payload)
	return &analysis, nil
}

// CreateRCAAnalysis queues an analysis of a run's frozen inputs. It is
// idempotent: when a queued, running or completed analysis has the same
// input digest it is returned instead, with created false.
func (s *SQLiteStore) CreateRCAAnalysis(project, test, inputDigest string, payload []byte, now time.Time) (*RCAAnalysis, bool, error) {
	return s.CreateRCAAnalysisWithOrigin(project, test, inputDigest, RCAOriginRequested, payload, now)
}

// CreateRCAAnalysisWithOrigin is CreateRCAAnalysis for an analysis of the
// given origin. An existing analysis of the same inputs keeps its own origin,
// except that a request for the inputs of a still-queued automatic analysis
// makes it a requested one, so it is claimed ahead of the automatic queue.
func (s *SQLiteStore) CreateRCAAnalysisWithOrigin(project, test, inputDigest, origin string, payload []byte, now time.Time) (*RCAAnalysis, bool, error) {
	project, test = strings.TrimSpace(project), strings.TrimSpace(test)
	if project == "" || test == "" || !validRCADigest(inputDigest) || !validRCAPayload(payload) || (origin != RCAOriginRequested && origin != RCAOriginAutomatic) {
		return nil, false, errors.New("invalid root-cause analysis")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	if err := lockRCARunTx(tx, project, test); err != nil {
		return nil, false, err
	}
	existing, err := scanRCAAnalysis(tx.QueryRow(`SELECT `+rcaAnalysisColumns+` FROM rca_analyses WHERE test_id = ? AND input_digest = ? AND state IN (?, ?, ?) ORDER BY revision DESC LIMIT 1`,
		test, inputDigest, RCAStateQueued, RCAStateRunning, RCAStateCompleted))
	if err == nil {
		if origin != RCAOriginRequested || existing.State != RCAStateQueued || existing.Origin != RCAOriginAutomatic {
			return existing, false, nil
		}
		result, err := tx.Exec(`UPDATE rca_analyses SET origin = ? WHERE id = ? AND state = ? AND origin = ?`, RCAOriginRequested, existing.ID, RCAStateQueued, RCAOriginAutomatic)
		if err != nil {
			return nil, false, err
		}
		if n, err := result.RowsAffected(); err == nil && n == 1 {
			existing.Origin = RCAOriginRequested
		}
		return existing, false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	// The cap counts the analyses that hold their inputs; failed and cancelled
	// ones are history, bounded by pruning the oldest.
	var live int64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM rca_analyses WHERE test_id = ? AND state IN (?, ?, ?)`, append([]any{test}, rcaLiveStates...)...).Scan(&live); err != nil {
		return nil, false, err
	}
	if live >= MaxRCARevisionsPerRun {
		return nil, false, ErrRCALimit
	}
	if _, err := tx.Exec(`DELETE FROM rca_analyses WHERE test_id = ? AND state IN (?, ?) AND id NOT IN (
			SELECT id FROM rca_analyses WHERE test_id = ? AND state IN (?, ?) ORDER BY revision DESC LIMIT ?)`,
		test, RCAStateFailed, RCAStateCancelled, test, RCAStateFailed, RCAStateCancelled, MaxRCARevisionsPerRun-1); err != nil {
		return nil, false, err
	}
	var latest int64
	if err := tx.QueryRow(`SELECT COALESCE(MAX(revision), 0) FROM rca_analyses WHERE test_id = ?`, test).Scan(&latest); err != nil {
		return nil, false, err
	}
	analysis := &RCAAnalysis{ID: uuid.NewString(), ProjectID: project, TestID: test, Revision: latest + 1, State: RCAStateQueued, CreatedAt: now.UTC(), InputDigest: inputDigest, Origin: origin, Payload: append(json.RawMessage(nil), payload...)}
	if _, err := tx.Exec(`INSERT INTO rca_analyses(id, project_id, test_id, revision, state, created_at, input_digest, payload, owner, origin) VALUES(?,?,?,?,?,?,?,?, '', ?)`,
		analysis.ID, project, test, analysis.Revision, analysis.State, analysis.CreatedAt, inputDigest, string(payload), origin); err != nil {
		return nil, false, err
	}
	return analysis, true, tx.Commit()
}

// GetRCAAnalysis returns one analysis of a run.
func (s *SQLiteStore) GetRCAAnalysis(project, test, id string) (*RCAAnalysis, error) {
	return scanRCAAnalysis(s.db.QueryRow(`SELECT `+rcaAnalysisColumns+` FROM rca_analyses WHERE project_id = ? AND test_id = ? AND id = ?`, project, test, id))
}

// LatestCompletedRCAAnalysis returns a run's newest completed analysis, or
// nil when it has none.
func (s *SQLiteStore) LatestCompletedRCAAnalysis(project, test string) (*RCAAnalysis, error) {
	analysis, err := scanRCAAnalysis(s.db.QueryRow(`SELECT `+rcaAnalysisColumns+` FROM rca_analyses WHERE project_id = ? AND test_id = ? AND state = ? ORDER BY revision DESC LIMIT 1`, project, test, RCAStateCompleted))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return analysis, err
}

// RCARunAnalyses are a run's newest analysis of any state and its newest
// completed one (the same analysis when the newest has completed). Neither
// carries its payload: a caller reads a completed one's assessment with
// GetRCAAnalysis when it needs it, which a completed analysis's immutability
// lets it cache.
type RCARunAnalyses struct {
	Latest    *RCAAnalysis
	Completed *RCAAnalysis
}

// maxRCABatchRuns bounds one LatestRCAAnalyses call.
const maxRCABatchRuns = 200

// LatestRCAAnalyses returns, for each of a project's runs that has analyses,
// its newest analysis and its newest completed one, in two queries, without
// their payloads.
func (s *SQLiteStore) LatestRCAAnalyses(project string, tests []string) (map[string]RCARunAnalyses, error) {
	project = strings.TrimSpace(project)
	out := map[string]RCARunAnalyses{}
	ids := make([]string, 0, len(tests))
	seen := map[string]bool{}
	for _, test := range tests {
		if test = strings.TrimSpace(test); test != "" && !seen[test] {
			seen[test] = true
			ids = append(ids, test)
		}
	}
	if project == "" || len(ids) == 0 {
		return out, nil
	}
	if len(ids) > maxRCABatchRuns {
		return nil, errors.New("too many runs in one root-cause batch")
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args := []any{project}
	for _, id := range ids {
		args = append(args, id)
	}
	latestColumns := strings.Replace(rcaAnalysisColumns, "payload", "'{}' AS payload", 1)
	latest, err := s.db.Query(`SELECT `+latestColumns+` FROM rca_analyses a WHERE project_id = ? AND test_id IN (`+placeholders+`)
		AND revision = (SELECT MAX(revision) FROM rca_analyses b WHERE b.test_id = a.test_id)`, args...)
	if err != nil {
		return nil, err
	}
	for latest.Next() {
		analysis, err := scanRCAAnalysis(latest)
		if err != nil {
			latest.Close()
			return nil, err
		}
		entry := out[analysis.TestID]
		entry.Latest = analysis
		out[analysis.TestID] = entry
	}
	if err := latest.Err(); err != nil {
		latest.Close()
		return nil, err
	}
	if err := latest.Close(); err != nil {
		return nil, err
	}
	completedArgs := append(append([]any{}, args...), RCAStateCompleted, RCAStateCompleted)
	completed, err := s.db.Query(`SELECT `+latestColumns+` FROM rca_analyses a WHERE project_id = ? AND test_id IN (`+placeholders+`) AND state = ?
		AND revision = (SELECT MAX(revision) FROM rca_analyses b WHERE b.test_id = a.test_id AND b.state = ?)`, completedArgs...)
	if err != nil {
		return nil, err
	}
	defer completed.Close()
	for completed.Next() {
		analysis, err := scanRCAAnalysis(completed)
		if err != nil {
			return nil, err
		}
		entry := out[analysis.TestID]
		entry.Completed = analysis
		if entry.Latest != nil && entry.Latest.ID == analysis.ID {
			entry.Latest = analysis
		}
		out[analysis.TestID] = entry
	}
	return out, completed.Err()
}

// ClaimRCAAnalysis leases the next analysis to owner: a running one whose
// owner's lease expired, or a queued one, requested analyses before
// automatic ones and each oldest first. A queued automatic analysis waits
// while another automatic one holds a live lease, which keeps a worker slot
// for requests. It returns nil when there is none.
func (s *SQLiteStore) ClaimRCAAnalysis(owner string, now time.Time, lease time.Duration) (*RCAAnalysis, error) {
	if strings.TrimSpace(owner) == "" || lease <= 0 {
		return nil, errors.New("invalid root-cause analysis claim")
	}
	now = now.UTC()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id string
	err = tx.QueryRow(`SELECT id FROM rca_analyses WHERE
			(state = ? AND (origin <> ? OR NOT EXISTS (SELECT 1 FROM rca_analyses running WHERE running.state = ? AND running.origin = ? AND running.lease_expires_at > ?)))
			OR (state = ? AND lease_expires_at <= ?)
		ORDER BY CASE WHEN state = ? THEN 0 WHEN origin = ? THEN 2 ELSE 1 END, created_at, id LIMIT 1`,
		RCAStateQueued, RCAOriginAutomatic, RCAStateRunning, RCAOriginAutomatic, now,
		RCAStateRunning, now,
		RCAStateRunning, RCAOriginAutomatic).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	result, err := tx.Exec(`UPDATE rca_analyses SET state = ?, owner = ?, lease_expires_at = ? WHERE id = ? AND (state = ? OR (state = ? AND lease_expires_at <= ?))`,
		RCAStateRunning, owner, now.Add(lease), id, RCAStateQueued, RCAStateRunning, now)
	if err != nil {
		return nil, err
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		return nil, err
	}
	claimed, err := scanRCAAnalysis(tx.QueryRow(`SELECT `+rcaAnalysisColumns+` FROM rca_analyses WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	return claimed, tx.Commit()
}

// RenewRCAAnalysis extends a running analysis's lease while owner holds it.
func (s *SQLiteStore) RenewRCAAnalysis(id, owner string, now time.Time, lease time.Duration) (bool, error) {
	result, err := s.db.Exec(`UPDATE rca_analyses SET lease_expires_at = ? WHERE id = ? AND owner = ? AND state = ?`, now.UTC().Add(lease), id, owner, RCAStateRunning)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// CompleteRCAAnalysisFenced records a running analysis's outcome when owner
// still holds it and, with a lease name, while the execution term holds; a
// stale owner or term cannot add a second outcome.
func (s *SQLiteStore) CompleteRCAAnalysisFenced(id, owner, state string, payload []byte, now time.Time, leaseName, leaseOwner string, generation uint64) (bool, error) {
	if owner == "" || (state != RCAStateCompleted && state != RCAStateFailed) || !validRCAPayload(payload) {
		return false, errors.New("invalid root-cause analysis completion")
	}
	var tx *storeTx
	var err error
	if leaseName != "" {
		tx, err = s.beginExecutionLeaseTermTransaction(leaseName, leaseOwner, generation)
		if err == nil && tx == nil {
			return false, nil
		}
	} else {
		tx, err = s.db.Begin()
	}
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE rca_analyses SET state = ?, payload = ?, completed_at = ?, lease_expires_at = NULL WHERE id = ? AND state = ? AND owner = ?`,
		state, string(payload), now.UTC(), id, RCAStateRunning, owner)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		return false, err
	}
	return true, tx.Commit()
}

// CancelRCAAnalysis cancels a queued or running analysis.
func (s *SQLiteStore) CancelRCAAnalysis(project, test, id string, now time.Time) (bool, error) {
	result, err := s.db.Exec(`UPDATE rca_analyses SET state = ?, completed_at = ?, lease_expires_at = NULL WHERE project_id = ? AND test_id = ? AND id = ? AND state IN (?, ?)`,
		RCAStateCancelled, now.UTC(), project, test, id, RCAStateQueued, RCAStateRunning)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// endRCAForArchiveTx ends the root-cause work a deleted archive leaves
// without evidence: its queued and running analyses are cancelled. Completed
// assessments, the critiques analysts wrote about them and the evidence
// revisions they cite are the run's history and stay until the run goes.
func endRCAForArchiveTx(tx *storeTx, test string, now time.Time) error {
	_, err := tx.Exec(`UPDATE rca_analyses SET state = ?, completed_at = ?, lease_expires_at = NULL WHERE test_id = ? AND state IN (?, ?)`,
		RCAStateCancelled, now.UTC(), test, RCAStateQueued, RCAStateRunning)
	return err
}

// deleteRCAForTestTx removes a run's root-cause history; it follows the run
// and its project.
func deleteRCAForTestTx(tx *storeTx, test string) error {
	if _, err := tx.Exec(`DELETE FROM rca_critiques WHERE test_id = ?`, test); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM rca_analyses WHERE test_id = ?`, test); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM rca_evidence_revisions WHERE test_id = ?`, test)
	return err
}
