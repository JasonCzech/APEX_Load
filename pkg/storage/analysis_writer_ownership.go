package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/internal/procidentity"
)

// Durable physical-writer ownership across controller restart and HA takeover.
//
// Every analytical physical writer generation (a normalization attempt minted
// by Claim, an upload chunk writer minted by Begin) records, in the same SQL
// transaction that mints it and before any artifact I/O, the exact controller
// process (its incarnation) that will perform its artifact writes. Artifact
// writes happen only in that process: the native worker writes private local
// staging, never the artifact store.
//
// The parent-issued quiescence/closure proof (ConfirmAnalysisAttemptWritersQuiesced,
// CompleteAnalysisUploadWriter) remains the ordinary proof. When that process
// dies first, its process-local retry records die with it, and only a second,
// independent proof can retire the generation: a controller on the same host
// proves the recorded incarnation exited (procidentity: the same identified
// machine booted again, or the same boot, machine identity and PID namespace
// with no process of that PID and start time). A lease expiry, a missing heartbeat or a replica that cannot judge the
// recorded host is never that proof, so a cold HA replica on another host, an
// unidentified platform and every pre-incarnation (legacy) writer keep their
// ownership until the recorded host proves the exit or an operator intervenes.
//
// Exit proves that no new artifact request will be issued. It proves that no
// later write can land only for the local filesystem backend, whose writes are
// synchronous system calls of the dead process. For S3, Azure Blob and GCS a
// commit request the process fully sent before exiting may still be applied
// afterwards: at that generation's own unique chunk or normalized key, or as
// a byte-identical copy of a source file (original-<file>.parquet, which every
// attempt of an import writes). Such a writer is retired from the live
// workflow (its import may be retried, its upload offset reused) but keeps
// its cleanup and the dataset's deletion blocked: remote commit unproven.

// AnalysisWriterProofIncarnationExited is the automatic exit termination proof kind.
const AnalysisWriterProofIncarnationExited = "incarnation_exited"

// AnalysisWriterProofAdminAttested is the administrator attestation proof kind.
const AnalysisWriterProofAdminAttested = "admin_attested"

// ErrAnalysisWriterActive refuses administrator attestation for a writer that
// is still active on the attesting controller process.
var ErrAnalysisWriterActive = errors.New("analytical writer is active on this controller")

// analysisWriterExitedReason is the import error after a proven exit.
const analysisWriterExitedReason = "Normalization stopped because the controller running it exited; commit the import again to retry."

// AnalysisWriterAttestation records an explicit administrator intervention
// attesting that a dataset's remote, unjudgeable or legacy writers have stopped.
type AnalysisWriterAttestation struct {
	Actor  string                `json:"actor"`
	Reason string                `json:"reason,omitempty"`
	At     time.Time             `json:"at"`
	Prover procidentity.Identity `json:"prover,omitempty"`
}

// AnalysisWriterVolume records the bound archive volume identity and locality
// observed when a writer generation was minted.
type AnalysisWriterVolume struct {
	Identity        string `json:"identity,omitempty"`
	LocalFilesystem bool   `json:"local_filesystem"`
}

// AnalysisWriterTermination is the durable record of a proven writer exit.
type AnalysisWriterTermination struct {
	Proof  string                `json:"proof"`
	Prover procidentity.Identity `json:"prover"`
	At     time.Time             `json:"at"`
	// RemoteCommitUnproven retains cleanup and deletion: the backend may still
	// apply a commit request the exited process had already sent.
	RemoteCommitUnproven bool                       `json:"remote_commit_unproven,omitempty"`
	Attestation          *AnalysisWriterAttestation `json:"attestation,omitempty"`
}

// AnalysisWriterExitScope is the prover's judgement authority. Exited must
// judge from the prover's own host; LocalFilesystem is true only when the
// pinned analytical artifact backend is the local filesystem.
type AnalysisWriterExitScope struct {
	Prover          procidentity.Identity
	Exited          func(procidentity.Identity) bool
	LocalFilesystem bool
	VolumeIdentity  string
}

// AnalysisWriterExitOutcome reports the generation's ownership after a
// resolution attempt.
type AnalysisWriterExitOutcome struct {
	// Proved is true once the writer's exit is durably recorded.
	Proved bool
	// Quiesced is true once its physical writers are durably proven stopped.
	Quiesced bool
	// RemoteCommitUnproven is true when exit is proven but a remote commit
	// may still land, so cleanup and deletion stay blocked.
	RemoteCommitUnproven bool
	// Recorded is true when this call wrote the proof or failed the import.
	Recorded bool
	// ImportFailed is true when this call failed the import it still owned.
	ImportFailed bool
}

// AnalysisWriterCandidate names one generation that may need exit proof.
type AnalysisWriterCandidate struct {
	Kind      string // "attempt" or "upload-writer"
	ProjectID string
	ID        string
}

// AnalysisWriterOwnershipStore resolves writers whose controller exited.
type AnalysisWriterOwnershipStore interface {
	ListAnalysisWriterOwnershipCandidates(ctx context.Context, after string, limit int) ([]AnalysisWriterCandidate, string, error)
	ResolveExitedAnalysisAttempt(ctx context.Context, project, attemptID string, scope AnalysisWriterExitScope) (AnalysisWriterExitOutcome, error)
	ResolveExitedAnalysisUploadWriter(ctx context.Context, project, writerID string, scope AnalysisWriterExitScope) (AnalysisWriterExitOutcome, error)
	ResolveExitedAnalysisUploadCursor(ctx context.Context, req AnalysisUploadRequest, scope AnalysisWriterExitScope) (int, error)
	AttestAnalysisImportWritersStopped(ctx context.Context, project, importID, actor, reason string) (*AnalysisImport, error)
}

type (
	analysisWriterIncarnationKey struct{}
	analysisWriterVolumeKey      struct{}
)

// WithAnalysisWriterVolume binds the archive volume identity and locality
// observed at writer mint time.
func WithAnalysisWriterVolume(ctx context.Context, v AnalysisWriterVolume) context.Context {
	if ctx == nil || v.Identity != "" && !analysisValidVolumeIdentity(v.Identity) {
		return ctx
	}
	return context.WithValue(ctx, analysisWriterVolumeKey{}, v)
}

func analysisWriterVolumeFrom(ctx context.Context) *AnalysisWriterVolume {
	if ctx == nil {
		return nil
	}
	v, ok := ctx.Value(analysisWriterVolumeKey{}).(AnalysisWriterVolume)
	if !ok || v.Identity != "" && !analysisValidVolumeIdentity(v.Identity) {
		return nil
	}
	return &v
}

// WithAnalysisWriterIncarnation binds the controller process that will perform
// a new generation's artifact writes. Claim and Begin record it durably; a
// context without one mints a generation that no later exit can retire.
func WithAnalysisWriterIncarnation(ctx context.Context, id procidentity.Identity) context.Context {
	if ctx == nil || !analysisWriterIdentityValid(id) {
		return ctx
	}
	return context.WithValue(ctx, analysisWriterIncarnationKey{}, id)
}

func analysisWriterIncarnationFrom(ctx context.Context) *procidentity.Identity {
	if ctx == nil {
		return nil
	}
	id, ok := ctx.Value(analysisWriterIncarnationKey{}).(procidentity.Identity)
	if !ok || !analysisWriterIdentityValid(id) {
		return nil
	}
	return &id
}

// Bounded so a writer payload with an incarnation and its prover stays well
// inside the 4 KiB upload-writer journal row.
func analysisWriterIdentityValid(id procidentity.Identity) bool {
	return id.PID > 0 && id.Boot != "" && id.Namespace != "" && id.Start != "" && len(id.Machine) <= 320 && len(id.Boot) <= 128 && len(id.Namespace) <= 128 && len(id.Start) <= 64
}

func analysisWriterTerminationValid(t *AnalysisWriterTermination, incarnation *procidentity.Identity) bool {
	if t == nil || t.At.IsZero() {
		return false
	}
	switch t.Proof {
	case AnalysisWriterProofIncarnationExited:
		return incarnation != nil && analysisWriterIdentityValid(t.Prover) && t.Prover != *incarnation
	case AnalysisWriterProofAdminAttested:
		if t.RemoteCommitUnproven || t.Attestation == nil || len(t.Attestation.Actor) < 1 || len(t.Attestation.Actor) > 256 || len(t.Attestation.Reason) < 3 || len(t.Attestation.Reason) > 1024 || t.Attestation.At.IsZero() {
			return false
		}
		if incarnation != nil && analysisWriterIdentityValid(t.Prover) && t.Prover == *incarnation {
			return false
		}
		return true
	default:
		return false
	}
}

func (scope AnalysisWriterExitScope) valid() bool {
	return scope.Exited != nil && analysisWriterIdentityValid(scope.Prover)
}

// judge is true only when the recorded incarnation is another process that
// the prover's host proves exited.
func (scope AnalysisWriterExitScope) judge(incarnation *procidentity.Identity) bool {
	return incarnation != nil && analysisWriterIdentityValid(*incarnation) && *incarnation != scope.Prover && scope.Exited(*incarnation)
}

func (scope AnalysisWriterExitScope) localFor(v *AnalysisWriterVolume) bool {
	if !scope.LocalFilesystem {
		return false
	}
	if v == nil {
		return true
	}
	if !v.LocalFilesystem {
		return false
	}
	if v.Identity != "" && scope.VolumeIdentity != "" && v.Identity != scope.VolumeIdentity {
		return false
	}
	return true
}

func (scope AnalysisWriterExitScope) termination(now time.Time, v *AnalysisWriterVolume) *AnalysisWriterTermination {
	return &AnalysisWriterTermination{Proof: AnalysisWriterProofIncarnationExited, Prover: scope.Prover, At: now, RemoteCommitUnproven: !scope.localFor(v)}
}

// analysisWriterSnapshot reads one bounded journal payload without locks.
func analysisWriterSnapshot(ctx context.Context, s *SQLiteStore, query, id, project string, into any) error {
	var body string
	if err := s.db.QueryRowContext(ctx, query, id, project).Scan(&body); err != nil {
		return err
	}
	if len(body) > 4<<20 || json.Unmarshal([]byte(body), into) != nil {
		return ErrAnalysisAuthority
	}
	return nil
}

// analysisWriterLockProject takes the project mutation lock. A deleted
// project still lets exact retained journals be resolved, as cleanup does.
func analysisWriterLockProject(ctx context.Context, tx *storeTx, project string) error {
	if _, err := lockProjectForOwnedMutation(tx, project); err != nil {
		var present int
		if e := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM projects WHERE id=?`, project).Scan(&present); e != nil || present != 0 {
			return ErrAnalysisPublicationUnknown
		}
	}
	return nil
}

func analysisAttemptExitOutcome(a *AnalysisAttempt) AnalysisWriterExitOutcome {
	proved := a.Termination != nil
	return AnalysisWriterExitOutcome{Proved: proved, Quiesced: a.WritersQuiesced, RemoteCommitUnproven: proved && a.Termination.RemoteCommitUnproven}
}

func analysisUploadExitOutcome(w *AnalysisUploadWriter) AnalysisWriterExitOutcome {
	proved := w.Termination != nil
	return AnalysisWriterExitOutcome{Proved: proved, Quiesced: w.PhysicallyClosed, RemoteCommitUnproven: proved && w.Termination.RemoteCommitUnproven && !w.PhysicallyClosed}
}

// ResolveExitedAnalysisAttempt records a proven exit of the attempt's
// controller. With the local filesystem backend the attempt's writers become
// quiesced, releasing its exact cleanup plan. An import it still owns,
// unpublished, fails in the same transaction (revision advanced, owner
// cleared), so no late publication or quiescence seal can adopt it.
func (s *SQLiteStore) ResolveExitedAnalysisAttempt(ctx context.Context, project, attemptID string, scope AnalysisWriterExitScope) (AnalysisWriterExitOutcome, error) {
	if !scope.valid() || project == "" || len(project) > 128 || attemptID == "" || len(attemptID) > 128 {
		return AnalysisWriterExitOutcome{}, ErrAnalysisAuthority
	}
	// An unlocked snapshot settles the common case, a generation this host
	// cannot judge, without the project lock; any change is re-decided below.
	var snapshot AnalysisAttempt
	if err := analysisWriterSnapshot(ctx, s, `SELECT payload FROM analysis_attempt_journal WHERE id=? AND project_id=?`, attemptID, project, &snapshot); err != nil {
		return AnalysisWriterExitOutcome{}, err
	}
	if snapshot.ID != attemptID || snapshot.ProjectID != project {
		return AnalysisWriterExitOutcome{}, ErrAnalysisAuthority
	}
	if snapshot.LegacyWriterUnknown || !scope.judge(snapshot.Incarnation) {
		return analysisAttemptExitOutcome(&snapshot), nil
	}
	// A settled generation (proven, quiesced or published) needs the lock
	// only while its import still names it as the normalizing owner.
	if snapshot.WritersQuiesced || snapshot.Termination != nil {
		var state, owner string
		err := s.db.QueryRowContext(ctx, `SELECT state,owner FROM analysis_imports WHERE id=? AND project_id=?`, snapshot.ImportID, project).Scan(&state, &owner)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return AnalysisWriterExitOutcome{}, err
		}
		if err != nil || state != "normalizing" || owner != attemptID {
			return analysisAttemptExitOutcome(&snapshot), nil
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AnalysisWriterExitOutcome{}, err
	}
	defer tx.Rollback()
	if err = analysisWriterLockProject(ctx, tx, project); err != nil {
		return AnalysisWriterExitOutcome{}, err
	}
	a, err := analysisAttemptJournalLoad(ctx, tx, attemptID, project)
	if err != nil {
		return AnalysisWriterExitOutcome{}, err
	}
	in, err := analysisAttemptLoad(ctx, tx, a.ImportID, project)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return AnalysisWriterExitOutcome{}, err
	}
	outcome := analysisAttemptExitOutcome(a)
	// A legacy sentinel or pre-incarnation attempt has no recorded process to
	// judge; its unknown writer stays retained.
	if a.LegacyWriterUnknown || a.Incarnation == nil {
		return outcome, tx.Commit()
	}
	owned := in != nil && in.State == "normalizing" && in.Owner == a.ID && in.Revision == a.Revision && a.PublicationDigest == ""
	prove := !a.WritersQuiesced && a.Termination == nil
	if !owned && !prove || !scope.judge(a.Incarnation) {
		return outcome, tx.Commit()
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return AnalysisWriterExitOutcome{}, err
	}
	// Failing an owned import always records the proof that allowed it; a
	// generation its own process had already quiesced has no remote doubt.
	local := scope.localFor(a.Volume)
	if prove || a.Termination == nil {
		a.Termination = scope.termination(now, a.Volume)
		a.Termination.RemoteCommitUnproven = !local && !a.WritersQuiesced
		a.WritersQuiesced = a.WritersQuiesced || local
	}
	if owned {
		oldRevision := in.Revision
		in.State, in.Owner, in.LeaseExpiresAt = "failed", "", time.Time{}
		in.Revision++
		in.UpdatedAt = now
		in.Error = analysisWriterExitedReason
		a.CleanupQueued = true
		if err = queueAnalysisCleanupTx(tx, "attempt", project, a.ID); err != nil {
			return AnalysisWriterExitOutcome{}, err
		}
		if err = analysisAttemptUpdate(ctx, tx, in, oldRevision, a.ID, "normalizing"); err != nil {
			return AnalysisWriterExitOutcome{}, err
		}
	}
	if err = analysisAttemptJournalWrite(ctx, tx, a); err != nil {
		return AnalysisWriterExitOutcome{}, err
	}
	if tx.Commit() != nil {
		return AnalysisWriterExitOutcome{}, ErrAnalysisPublicationUnknown
	}
	outcome = analysisAttemptExitOutcome(a)
	outcome.Recorded, outcome.ImportFailed = true, owned
	return outcome, nil
}

// resolveExitedAnalysisUploadWriterTx abandons one unclosed writer whose
// controller provably exited. Its exact keys stay its own; the durable cursor
// never referenced them.
func resolveExitedAnalysisUploadWriterTx(ctx context.Context, tx *storeTx, w *AnalysisUploadWriter, scope AnalysisWriterExitScope) (bool, error) {
	if w.PhysicallyClosed || w.Termination != nil || !scope.judge(w.Incarnation) {
		return false, nil
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return false, err
	}
	local := scope.localFor(w.Volume)
	w.Termination = scope.termination(now, w.Volume)
	w.State = "abandoned"
	w.PhysicallyClosed = local
	if err = analysisUploadWrite(ctx, tx, w, false); err != nil {
		return false, err
	}
	return true, queueAnalysisCleanupTx(tx, "upload-writer", w.ProjectID, w.ID)
}

// ResolveExitedAnalysisUploadWriter retires one upload writer whose controller
// provably exited before supplying its physical closure.
func (s *SQLiteStore) ResolveExitedAnalysisUploadWriter(ctx context.Context, project, writerID string, scope AnalysisWriterExitScope) (AnalysisWriterExitOutcome, error) {
	if !scope.valid() || project == "" || len(project) > 128 || writerID == "" || len(writerID) > 128 {
		return AnalysisWriterExitOutcome{}, ErrAnalysisAuthority
	}
	var snapshot AnalysisUploadWriter
	if err := analysisWriterSnapshot(ctx, s, `SELECT payload FROM analysis_upload_writers WHERE id=? AND project_id=?`, writerID, project, &snapshot); err != nil {
		return AnalysisWriterExitOutcome{}, err
	}
	if snapshot.ID != writerID || snapshot.ProjectID != project {
		return AnalysisWriterExitOutcome{}, ErrAnalysisAuthority
	}
	if snapshot.PhysicallyClosed || snapshot.Termination != nil || !scope.judge(snapshot.Incarnation) {
		return analysisUploadExitOutcome(&snapshot), nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AnalysisWriterExitOutcome{}, err
	}
	defer tx.Rollback()
	if err = analysisWriterLockProject(ctx, tx, project); err != nil {
		return AnalysisWriterExitOutcome{}, err
	}
	w, err := analysisUploadLoad(ctx, tx, project, writerID)
	if err != nil {
		return AnalysisWriterExitOutcome{}, err
	}
	recorded, err := resolveExitedAnalysisUploadWriterTx(ctx, tx, w, scope)
	if err != nil {
		return AnalysisWriterExitOutcome{}, err
	}
	if tx.Commit() != nil {
		return AnalysisWriterExitOutcome{}, ErrAnalysisPublicationUnknown
	}
	outcome := analysisUploadExitOutcome(w)
	outcome.Recorded = recorded
	return outcome, nil
}

// ResolveExitedAnalysisUploadCursor retires exited writers that block one
// upload cursor offset, so the client's retry at that offset can begin. It
// returns how many writers it retired.
func (s *SQLiteStore) ResolveExitedAnalysisUploadCursor(ctx context.Context, req AnalysisUploadRequest, scope AnalysisWriterExitScope) (int, error) {
	if !scope.valid() || req.ProjectID == "" || len(req.ProjectID) > 128 || req.Offset < 0 {
		return 0, ErrAnalysisAuthority
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	present, err := analysisUploadLockProject(ctx, tx, req.ProjectID)
	if err != nil || !present {
		return 0, ErrAnalysisAuthority
	}
	in, err := analysisAttemptLoad(ctx, tx, req.ImportID, req.ProjectID)
	if err != nil {
		return 0, err
	}
	if in.State != "uploading" || in.Revision != req.Revision {
		return 0, ErrAnalysisAuthority
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM analysis_upload_writers WHERE project_id=? AND import_id=? AND file_id=? AND chunk_offset=?`, req.ProjectID, req.ImportID, req.FileID, req.Offset)
	if err != nil {
		return 0, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		if ids = append(ids, id); len(ids) > analysisUploadMaxWriters {
			rows.Close()
			return 0, ErrAnalysisAuthority
		}
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return 0, err
	}
	resolved := 0
	for _, id := range ids {
		w, err := analysisUploadLoad(ctx, tx, req.ProjectID, id)
		if err != nil {
			return 0, err
		}
		changed, err := resolveExitedAnalysisUploadWriterTx(ctx, tx, w, scope)
		if err != nil {
			return 0, err
		}
		if changed {
			resolved++
		}
	}
	if tx.Commit() != nil {
		return 0, ErrAnalysisPublicationUnknown
	}
	return resolved, nil
}

// ListAnalysisWriterOwnershipCandidates pages through every generation that
// can still lack physical proof: owners of normalizing imports, then attempts
// and upload writers retained by the cleanup queue (failed, replaced,
// cancelled or deleted generations). A pending upload writer of a live upload
// is resolved when its offset is retried. Pass "" to start; an empty next
// cursor means the inventory wrapped. Each phase pages by key, so generations
// that stay unprovable cannot starve the rest.
func (s *SQLiteStore) ListAnalysisWriterOwnershipCandidates(ctx context.Context, after string, limit int) ([]AnalysisWriterCandidate, string, error) {
	phase, key := "i", ""
	if after != "" {
		if len(after) > 130 || !strings.HasPrefix(after, "i:") && !strings.HasPrefix(after, "j:") {
			return nil, "", ErrAnalysisAuthority
		}
		phase, key = after[:1], after[2:]
	}
	if limit < 1 || limit > 200 {
		return nil, "", ErrAnalysisAuthority
	}
	query := `SELECT 'attempt',project_id,owner FROM analysis_imports WHERE state='normalizing' AND owner<>'' AND owner>? ORDER BY owner LIMIT ?`
	if phase == "j" {
		query = `SELECT kind,project_id,resource_id FROM analysis_cleanup_jobs WHERE kind IN ('attempt','upload-writer') AND resource_id>? ORDER BY resource_id LIMIT ?`
	}
	rows, err := s.db.QueryContext(ctx, query, key, limit)
	if err != nil {
		return nil, "", err
	}
	out := []AnalysisWriterCandidate{}
	for rows.Next() {
		var c AnalysisWriterCandidate
		if err = rows.Scan(&c.Kind, &c.ProjectID, &c.ID); err != nil {
			rows.Close()
			return nil, "", err
		}
		out = append(out, c)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, "", err
	}
	switch {
	case len(out) == limit:
		return out, phase + ":" + out[len(out)-1].ID, nil
	case phase == "i":
		return out, "j:", nil
	default:
		return out, "", nil
	}
}

// AttestAnalysisImportWritersStopped records an explicit administrator
// attestation that all remote, unjudgeable, unrecorded or legacy writers of
// one dataset have stopped. It never attests a writer whose incarnation is the
// attesting controller itself while still active, and never touches any other
// dataset's state or cleanup plans.
func (s *SQLiteStore) AttestAnalysisImportWritersStopped(ctx context.Context, project, importID, actor, reason string) (*AnalysisImport, error) {
	actor = strings.TrimSpace(actor)
	reason = strings.TrimSpace(reason)
	if project == "" || len(project) > 128 || len(actor) < 1 || len(actor) > 256 || len(reason) < 3 || len(reason) > 1024 {
		return nil, ErrAnalysisAuthority
	}
	if parsed, err := uuid.Parse(importID); err != nil || parsed.String() != importID {
		return nil, ErrAnalysisAuthority
	}
	var prover procidentity.Identity
	if inc := analysisWriterIncarnationFrom(ctx); inc != nil {
		prover = *inc
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = analysisWriterLockProject(ctx, tx, project); err != nil {
		return nil, err
	}
	in, err := analysisAttemptLoad(ctx, tx, importID, project)
	if err != nil {
		return nil, err
	}
	if in.State != "cancelled" && in.State != "failed" && in.State != "deleting" {
		return nil, ErrAnalysisAuthority
	}
	now, err := preciseDatabaseTimeContext(ctx, tx, tx.dialect)
	if err != nil {
		return nil, err
	}
	attemptRows, err := tx.QueryContext(ctx, `SELECT payload FROM analysis_attempt_journal WHERE project_id=? AND import_id=? ORDER BY id`, project, importID)
	if err != nil {
		return nil, err
	}
	attempts := []AnalysisAttempt{}
	for attemptRows.Next() {
		if len(attempts) >= 4096 {
			attemptRows.Close()
			return nil, ErrAnalysisAuthority
		}
		var body string
		if err = attemptRows.Scan(&body); err != nil {
			attemptRows.Close()
			return nil, err
		}
		var a AnalysisAttempt
		if json.Unmarshal([]byte(body), &a) != nil || a.ProjectID != project || a.ImportID != importID {
			attemptRows.Close()
			return nil, ErrAnalysisAuthority
		}
		attempts = append(attempts, a)
	}
	if err = errors.Join(attemptRows.Err(), attemptRows.Close()); err != nil {
		return nil, err
	}
	uploadRows, err := tx.QueryContext(ctx, `SELECT id,file_id,chunk_offset,payload FROM analysis_upload_writers WHERE project_id=? AND import_id=? ORDER BY id`, project, importID)
	if err != nil {
		return nil, err
	}
	uploads := []AnalysisUploadWriter{}
	for uploadRows.Next() {
		if len(uploads) >= analysisUploadMaxWriters {
			uploadRows.Close()
			return nil, ErrAnalysisAuthority
		}
		var rowID, fileID, raw string
		var offset int64
		var w AnalysisUploadWriter
		if err = uploadRows.Scan(&rowID, &fileID, &offset, &raw); err != nil {
			uploadRows.Close()
			return nil, err
		}
		if len(raw) > 4096 || json.Unmarshal([]byte(raw), &w) != nil || w.ID != rowID || w.FileID != fileID || w.Offset != offset || w.ProjectID != project || w.ImportID != importID || analysisUploadValidate(&w) != nil {
			uploadRows.Close()
			return nil, ErrAnalysisAuthority
		}
		uploads = append(uploads, w)
	}
	if err = errors.Join(uploadRows.Err(), uploadRows.Close()); err != nil {
		return nil, err
	}
	if analysisWriterIdentityValid(prover) {
		for _, a := range attempts {
			if !a.WritersQuiesced && a.Termination == nil && a.Incarnation != nil && *a.Incarnation == prover {
				return nil, ErrAnalysisWriterActive
			}
		}
		for _, w := range uploads {
			if (!w.PhysicallyClosed || w.State == "pending") && w.Termination == nil && w.Incarnation != nil && *w.Incarnation == prover {
				return nil, ErrAnalysisWriterActive
			}
		}
	}
	att := &AnalysisWriterAttestation{
		Actor:  actor,
		Reason: reason,
		At:     now,
		Prover: prover,
	}
	term := &AnalysisWriterTermination{
		Proof:                AnalysisWriterProofAdminAttested,
		Prover:               prover,
		At:                   now,
		RemoteCommitUnproven: false,
		Attestation:          att,
	}
	for i := range attempts {
		a := &attempts[i]
		if !a.WritersQuiesced || a.LegacyWriterUnknown || a.Termination != nil && a.Termination.RemoteCommitUnproven {
			a.WritersQuiesced = true
			a.LegacyWriterUnknown = false
			a.Termination = term
			if in.State == "cancelled" || in.State == "deleting" || in.State == "deleted" || a.ID != in.Owner {
				a.CleanupQueued = true
			}
			if err = analysisAttemptJournalWrite(ctx, tx, a); err != nil {
				return nil, err
			}
			if a.CleanupQueued {
				if err = queueAnalysisCleanupTx(tx, "attempt", project, a.ID); err != nil {
					return nil, err
				}
			}
		}
	}
	for i := range uploads {
		w := &uploads[i]
		if !w.PhysicallyClosed || w.State == "pending" || w.Termination != nil && w.Termination.RemoteCommitUnproven {
			w.State = "abandoned"
			if w.Rejection == "" {
				w.Rejection = "writer_attested_stopped"
			}
			w.PhysicallyClosed = true
			w.Termination = term
			if err = analysisUploadWrite(ctx, tx, w, false); err != nil {
				return nil, err
			}
			if err = queueAnalysisCleanupTx(tx, "upload-writer", project, w.ID); err != nil {
				return nil, err
			}
		}
	}
	oldRevision, oldOwner, oldState := in.Revision, in.Owner, in.State
	in.WriterAttestation = att
	in.Revision++
	in.UpdatedAt = now
	if err = analysisAttemptUpdate(ctx, tx, in, oldRevision, oldOwner, oldState); err != nil {
		return nil, err
	}
	if in.State == "cancelled" || in.State == "deleting" {
		if err = queueAnalysisCleanupTx(tx, "dataset", project, in.ID); err != nil {
			return nil, err
		}
	}
	if tx.Commit() != nil {
		return nil, ErrAnalysisPublicationUnknown
	}
	return in, nil
}
