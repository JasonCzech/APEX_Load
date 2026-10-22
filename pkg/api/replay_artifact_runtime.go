package api

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/recordbridge"
	"github.com/jasonczech/apexload/pkg/storage"
)

type replayArtifactRuntimeStore interface {
	replayArtifactPublicationStore
	LookupReplayArtifactScope(context.Context, string) (*storage.ReplayArtifactScope, error)
	GetReplayArtifactReceipt(context.Context, string, string) (*storage.ReplayArtifactReceipt, error)
	ClaimReplayArtifactCleanupAfter(context.Context, string, int) ([]storage.ReplayArtifactReceipt, error)
	FinishReplayArtifactCleanup(context.Context, string, string, string) (bool, error)
}

// Runtime owns bounded admission, not artifact lifetimes. Receipts retain those
// lifetimes across process shutdown, cache eviction and unknown writer failures.
type replayArtifactRuntime struct {
	store   replayArtifactRuntimeStore
	backend archiveBackendRuntime
	reads   chan struct{}
	writes  chan struct{}
	sweepMu sync.Mutex
	after   string
}

func newReplayArtifactRuntime(store any, backend archiveBackendRuntime) *replayArtifactRuntime {
	s, ok := store.(replayArtifactRuntimeStore)
	if !ok {
		return nil
	}
	return &replayArtifactRuntime{store: s, backend: backend, reads: make(chan struct{}, 4), writes: make(chan struct{}, 2)}
}
func (r *replayArtifactRuntime) open(ctx context.Context, create bool) (*replayArtifactBackend, error) {
	backend := r.backend
	if backend.Backend == archiveBackendFilesystem {
		root, err := resolveArchiveDirectoryRoot(backend.LocalRoot, create)
		if err != nil {
			return nil, err
		}
		backend.LocalRoot = root
	}
	return openReplayArtifactBackend(ctx, backend)
}
func takeReplayArtifactSlot(ctx context.Context, slots chan struct{}) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case slots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-slots }) }, nil
	default:
		return nil, recordbridge.ErrRetainedReplayBusy
	}
}
func (r *replayArtifactRuntime) receipt(ctx context.Context, id, project string) (*storage.ReplayArtifactReceipt, error) {
	if id == "" || project == "" {
		return nil, recordbridge.ErrRetainedReplayNotFound
	}
	receipt, err := r.store.GetReplayArtifactReceipt(ctx, id, project)
	if err == nil {
		return receipt, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, recordbridge.ErrRetainedReplayUnavailable
	}
	scope, scopeErr := r.store.LookupReplayArtifactScope(ctx, id)
	if errors.Is(scopeErr, sql.ErrNoRows) || (scopeErr == nil && scope.ProjectID != project) {
		return nil, recordbridge.ErrRetainedReplayNotFound
	}
	return nil, recordbridge.ErrRetainedReplayUnavailable
}
func (r *replayArtifactRuntime) recheck(ctx context.Context, original *storage.ReplayArtifactReceipt) error {
	current, err := r.store.GetReplayArtifactReceipt(ctx, original.Spec.SessionID, original.Spec.ProjectID)
	if err != nil || current == nil || current.Spec != original.Spec || current.State != "ready" || !current.WriterJoined || !current.ExpiresAt.Equal(original.ExpiresAt) {
		return recordbridge.ErrRetainedReplayUnavailable
	}
	return nil
}
func (r *replayArtifactRuntime) Session(ctx context.Context, id, project string) (*recordbridge.BrowserReplaySession, error) {
	release, err := takeReplayArtifactSlot(ctx, r.reads)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	receipt, err := r.receipt(ctx, id, project)
	if err != nil {
		return nil, err
	}
	backend, err := r.open(ctx, false)
	if err != nil {
		return nil, recordbridge.ErrRetainedReplayUnavailable
	}
	defer backend.Close()
	report, err := backend.ReadReport(ctx, *receipt)
	if err != nil {
		return nil, recordbridge.ErrRetainedReplayUnavailable
	}
	session, err := decodeRetainedReplayReport(report, id, project)
	if err != nil {
		return nil, err
	}
	if err = r.recheck(ctx, receipt); err != nil {
		return nil, err
	}
	return session, nil
}
func (r *replayArtifactRuntime) HAR(ctx context.Context, id, project string) (*recordbridge.BrowserReplaySession, recordbridge.ReplayHAR, error) {
	release, err := takeReplayArtifactSlot(ctx, r.reads)
	if err != nil {
		return nil, recordbridge.ReplayHAR{}, err
	}
	success := false
	defer func() {
		if !success {
			release()
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	receipt, err := r.receipt(ctx, id, project)
	if err != nil {
		return nil, recordbridge.ReplayHAR{}, err
	}
	backend, err := r.open(ctx, false)
	if err != nil {
		return nil, recordbridge.ReplayHAR{}, recordbridge.ErrRetainedReplayUnavailable
	}
	defer backend.Close()
	read, err := backend.Read(ctx, *receipt)
	if err != nil {
		return nil, recordbridge.ReplayHAR{}, recordbridge.ErrRetainedReplayUnavailable
	}
	defer func() {
		if !success {
			read.Close()
		}
	}()
	report, err := io.ReadAll(io.LimitReader(read.Report, 16<<20+1))
	if err != nil {
		return nil, recordbridge.ReplayHAR{}, recordbridge.ErrRetainedReplayUnavailable
	}
	session, err := decodeRetainedReplayReport(report, id, project)
	if err != nil {
		return nil, recordbridge.ReplayHAR{}, err
	}
	if err = r.recheck(ctx, receipt); err != nil {
		return nil, recordbridge.ReplayHAR{}, err
	}
	har, err := recordbridge.NewVerifiedReplayHAR(read.HAR, receipt.Spec.HARBytes, func() { read.Close(); release() })
	if err != nil {
		return nil, recordbridge.ReplayHAR{}, err
	}
	success = true
	return session, har, nil
}

type boundReplayArtifactPublisher struct {
	runtime            *replayArtifactRuntime
	id, project, owner string
}

func (p *boundReplayArtifactPublisher) Publish(ctx context.Context, session *recordbridge.BrowserReplaySession, har io.ReadSeeker, size int64) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	release, err := takeReplayArtifactSlot(ctx, p.runtime.writes)
	if err != nil {
		return err
	}
	defer release()
	backend, err := p.runtime.open(ctx, true)
	if err != nil {
		return err
	}
	defer backend.Close()
	publisher := replayArtifactPublisher{store: p.runtime.store, backend: backend, session: p.id, project: p.project, owner: p.owner, ttl: defaultReplaySessionTerminalTTL, limits: storage.ReplayArtifactLimits{GlobalReceipts: 250, ProjectReceipts: 100, GlobalBytes: 8 << 30, ProjectBytes: 2 << 30}}
	return publisher.Publish(ctx, session, har, size)
}

// Sweep advances over foreign backend receipts as well as failures. The cursor
// is scheduling state only; only exact joined cleanup can release reservations.
func (r *replayArtifactRuntime) Sweep(ctx context.Context) error {
	if !r.sweepMu.TryLock() {
		return nil
	}
	defer r.sweepMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	receipts, err := r.store.ClaimReplayArtifactCleanupAfter(ctx, r.after, 32)
	if err != nil {
		return err
	}
	if len(receipts) == 0 {
		r.after = ""
		return nil
	}
	backend, err := r.open(ctx, false)
	if err != nil {
		return err
	}
	defer backend.Close()
	var result error
	for _, receipt := range receipts {
		if err := ctx.Err(); err != nil {
			return errors.Join(result, err)
		}
		r.after = receipt.Spec.SessionID
		if receipt.Spec.SourceSHA256 != backend.sourceSHA256() {
			continue
		}
		if err := backend.Cleanup(ctx, receipt); err != nil {
			result = errors.Join(result, err)
			continue
		}
		_, err := r.store.FinishReplayArtifactCleanup(ctx, receipt.Spec.SessionID, receipt.Spec.ProjectID, receipt.Spec.Generation)
		result = errors.Join(result, err)
	}
	if len(receipts) < 32 {
		r.after = ""
	}
	return result
}
