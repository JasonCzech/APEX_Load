package api

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

var errNativeReconciliationRetained = errors.New("analytical reconciliation ownership retained")

type nativeAnalysisArtifactBackendKey struct{}

// Every admitted pipeline retains its artifact namespace. A later environment
// edit cannot substitute another source during I/O or release an old receipt.
func nativeAnalysisArtifactBackend(ctx context.Context) (archiveBackendRuntime, error) {
	current := currentArchiveBackend()
	if ctx != nil {
		if pinned, ok := ctx.Value(nativeAnalysisArtifactBackendKey{}).(archiveBackendRuntime); ok {
			if pinned != current {
				return archiveBackendRuntime{}, errNativeReconciliationRetained
			}
			return pinned, nil
		}
	}
	return current, nil
}

type nativeReconciliationScope struct {
	origin storage.Store
	// Private digest only; secret-bearing DSNs are never printed or disclosed.
	sqlNamespace [32]byte
	resumableSQL bool
	artifacts    archiveBackendRuntime
}

func nativeSQLReconciliationNamespace(store storage.Store) ([32]byte, bool) {
	provider, ok := store.(storage.BackendInfoProvider)
	if !ok {
		return [32]byte{}, false
	}
	info := provider.BackendInfo()
	if info.DSN == "" || len(info.DSN) > 64<<10 || info.Driver == "" || info.Type == "" {
		return [32]byte{}, false
	}
	// Separate in-memory databases may have identical locator strings. Only the
	// exact original store can resume those records, never a same-DSN replacement.
	if info.Type == storage.StoreBackendSQLite && (info.DSN == ":memory:" || strings.Contains(strings.ToLower(info.DSN), "mode=memory")) {
		return [32]byte{}, false
	}
	locator := info.DSN
	if info.Type == storage.StoreBackendSQLite {
		// Ordinary SQLite path locators are made absolute before capture; a
		// relative DSN under another working directory is a different namespace.
		if strings.HasPrefix(locator, "file:") || strings.ContainsAny(locator, "?\x00") {
			return [32]byte{}, false // ambiguous URI semantics retain exact origin only
		}
		var err error
		locator, err = filepath.Abs(locator)
		if err != nil {
			return [32]byte{}, false
		}
	}
	return sha256.Sum256([]byte(info.Type + "\x00" + info.Driver + "\x00" + locator)), true
}
func nativeSameStore(a, b storage.Store) bool {
	if a == nil || b == nil {
		return false
	}
	av, bv := reflect.ValueOf(a), reflect.ValueOf(b)
	return av.Type() == bv.Type() && av.Comparable() && bv.Comparable() && av.Equal(bv)
}
func (s *Server) nativeReconciliationScope(ctx context.Context) (nativeReconciliationScope, error) {
	// Registration always retains the originally pinned source, including when
	// current configuration has changed. Matching refuses until that exact source
	// is restored; configuration changes cannot make retry arguments unreachable.
	backend := currentArchiveBackend()
	if ctx != nil {
		if pinned, ok := ctx.Value(nativeAnalysisArtifactBackendKey{}).(archiveBackendRuntime); ok {
			backend = pinned
		}
	}
	if backend.validate() != nil || s == nil || s.nativeReconciliationStore == nil {
		return nativeReconciliationScope{}, errNativeReconciliationRetained
	}
	namespace, resumable := nativeSQLReconciliationNamespace(s.nativeReconciliationStore)
	return nativeReconciliationScope{origin: s.nativeReconciliationStore, sqlNamespace: namespace, resumableSQL: resumable, artifacts: backend}, nil
}
func (scope nativeReconciliationScope) matches(s *Server) bool {
	if s == nil || s.nativeReconciliationStore == nil || scope.artifacts != currentArchiveBackend() {
		return false
	}
	namespace, known := nativeSQLReconciliationNamespace(s.nativeReconciliationStore)
	if scope.resumableSQL {
		return known && scope.sqlNamespace == namespace
	}
	return nativeSameStore(scope.origin, s.nativeReconciliationStore)
}

// Request code passes its attempt adapter explicitly. Accept only the adapter
// captured at this Server's construction; a mutable request-store substitution
// must not retire physical ownership through a different SQL authority.
func (s *Server) nativeReconciliationAttemptsMatch(attempts storage.AnalysisAttemptStore) bool {
	if s == nil || s.nativeReconciliationStore == nil || attempts == nil {
		return false
	}
	store, ok := attempts.(storage.Store)
	return ok && nativeSameStore(s.nativeReconciliationStore, store)
}

type nativeReconciliationClaim struct {
	id, project, owner string
	revision           int64
}

func nativeClaim(in storage.AnalysisImport) nativeReconciliationClaim {
	return nativeReconciliationClaim{id: in.ID, project: in.ProjectID, owner: in.Owner, revision: in.Revision}
}
func (claim nativeReconciliationClaim) valid() bool {
	return claim.id != "" && claim.project != "" && claim.owner != "" && claim.revision > 0
}

type nativeWriterQuiescenceRecord struct {
	mu       sync.Mutex
	scope    nativeReconciliationScope
	claim    nativeReconciliationClaim
	pipeline *analytics.NativePipeline
	lease    *analytics.NativeQuiescenceLease
	dir      string
	resolved bool
	done     chan struct{}
}

type nativePublicationRecord struct {
	mu                                       sync.Mutex
	scope                                    nativeReconciliationScope
	claim                                    nativeReconciliationClaim
	pipeline                                 *analytics.NativePipeline
	lease                                    *analytics.NativePublicationLease
	dir, digest                              string
	publicationResolved, published, resolved bool
	done                                     chan struct{}
}

// These process-local roots retain every immutable retry argument and its exact
// physical lease across Server shutdown/no-launch. They are not crash evidence.
// The native heavy-job gate bounds their population. Pointer keys cannot merge
// independent pipelines that happen to present the same attempt locator.
var nativePendingWriterPipelines sync.Map      // *NativePipeline -> *nativeWriterQuiescenceRecord
var nativePendingPublicationPipelines sync.Map // *NativePipeline -> *nativePublicationRecord

func (s *Server) retainNativeWriterQuiescence(ctx context.Context, in storage.AnalysisImport, pipeline *analytics.NativePipeline, dir string) (*nativeWriterQuiescenceRecord, error) {
	claim := nativeClaim(in)
	if !claim.valid() || pipeline == nil {
		return nil, errNativeReconciliationRetained
	}
	if value, ok := nativePendingWriterPipelines.Load(pipeline); ok {
		record := value.(*nativeWriterQuiescenceRecord)
		if record.claim != claim || record.dir != dir || !record.scope.matches(s) {
			return nil, errNativeReconciliationRetained
		}
		return record, nil
	}
	scope, err := s.nativeReconciliationScope(ctx)
	if err != nil {
		return nil, err
	}
	lease, err := pipeline.RetainWriterQuiescence()
	if err != nil {
		return nil, err
	}
	record := &nativeWriterQuiescenceRecord{scope: scope, claim: claim, pipeline: pipeline, lease: lease, dir: dir, done: make(chan struct{})}
	// RetainWriterQuiescence permits one exact hold, so only its successful owner
	// reaches this registration; no failed duplicate can overwrite its receipt.
	nativePendingWriterPipelines.Store(pipeline, record)
	return record, nil
}

func waitNativeWriterPhysical(ctx context.Context, done <-chan struct{}) error {
	if !analysisQueryContextLive(ctx) || done == nil {
		return errNativeReconciliationRetained
	}
	var deadline <-chan time.Time
	var timer *time.Timer
	if end, ok := ctx.Deadline(); ok {
		timer = time.NewTimer(time.Until(end))
		defer timer.Stop()
		deadline = timer.C
	}
	select {
	case <-done:
		if !analysisQueryContextLive(ctx) {
			return errNativeReconciliationRetained
		}
		return nil
	case <-ctx.Done():
		return errNativeReconciliationRetained
	case <-deadline:
		return errNativeReconciliationRetained
	}
}
func (s *Server) confirmNativeWriterQuiescence(ctx context.Context, attempts storage.AnalysisAttemptStore, record *nativeWriterQuiescenceRecord) error {
	if record == nil || !s.nativeReconciliationAttemptsMatch(attempts) || !record.scope.matches(s) {
		return errNativeReconciliationRetained
	}
	if err := waitNativeWriterPhysical(ctx, record.lease.PhysicalDone()); err != nil {
		return err
	}
	// A blocked store call retains the record. Another Server/request does not
	// queue behind its mutex without a caller deadline or replace its retry owner.
	if !record.mu.TryLock() {
		return errNativeReconciliationRetained
	}
	defer record.mu.Unlock()
	if record.resolved {
		return nil
	}
	if !analysisQueryContextLive(ctx) || !record.scope.matches(s) {
		return errNativeReconciliationRetained
	}
	if err := attempts.ConfirmAnalysisAttemptWritersQuiesced(ctx, record.claim.id, record.claim.project, record.claim.owner); err != nil {
		return err
	}
	// The exact physical channel and successful durable confirmation jointly
	// authorize Resolve. Cancellation/absence/timeout never supplies that proof.
	if err := record.lease.Resolve(); err != nil {
		return err
	}
	record.resolved = true
	nativePendingWriterPipelines.CompareAndDelete(record.pipeline, record)
	close(record.done)
	return nil
}

func (s *Server) retainNativePublicationReconciliation(ctx context.Context, in storage.AnalysisImport, pipeline *analytics.NativePipeline, lease *analytics.NativePublicationLease, dir, digest string) {
	scope, err := s.nativeReconciliationScope(ctx)
	claim := nativeClaim(in)
	if err != nil || !claim.valid() || pipeline == nil || lease == nil || len(digest) != 64 {
		// The pipeline still owns its pending publication, and cannot release its
		// gate. This invalid adapter state is not repaired by inventing a receipt.
		return
	}
	record := &nativePublicationRecord{scope: scope, claim: claim, pipeline: pipeline, lease: lease, dir: dir, digest: digest, done: make(chan struct{})}
	nativePendingPublicationPipelines.LoadOrStore(pipeline, record)
}
func (s *Server) reconcileNativePublication(ctx context.Context, attempts storage.AnalysisAttemptStore, record *nativePublicationRecord) error {
	if record == nil || !s.nativeReconciliationAttemptsMatch(attempts) || !record.scope.matches(s) || !analysisQueryContextLive(ctx) || !record.mu.TryLock() {
		return errNativeReconciliationRetained
	}
	defer record.mu.Unlock()
	if record.resolved {
		return nil
	}
	if !record.publicationResolved {
		result, err := attempts.ResolveAnalysisAttemptPublication(ctx, record.claim.id, record.claim.project, record.claim.owner, record.digest)
		if err != nil {
			return err
		}
		record.publicationResolved, record.published = true, result.Published
	}
	if !record.published {
		err := attempts.FailAnalysisImportAttempt(ctx, record.claim.id, record.claim.project, record.claim.owner, record.claim.revision)
		if err != nil && !errors.Is(err, storage.ErrAnalysisAuthority) {
			return err
		}
	}
	record.lease.Resolve()
	if err := closeNativeAnalysisPipeline(record.pipeline, record.dir); err != nil {
		return err
	}
	record.resolved = true
	nativePendingPublicationPipelines.CompareAndDelete(record.pipeline, record)
	close(record.done)
	return nil
}

func (s *Server) reconcileNativeAnalysisOnce(ctx context.Context) {
	if s == nil {
		return
	}
	attempts, ok := s.nativeReconciliationStore.(storage.AnalysisAttemptStore)
	if !ok || !analysisQueryContextLive(ctx) {
		return
	}
	nativePendingWriterPipelines.Range(func(_, value any) bool {
		record := value.(*nativeWriterQuiescenceRecord)
		select {
		case <-record.lease.PhysicalDone():
			if record.scope.matches(s) {
				call, stop := context.WithTimeout(ctx, 5*time.Second)
				_ = s.confirmNativeWriterQuiescence(call, attempts, record)
				stop()
			}
		default:
		}
		return analysisQueryContextLive(ctx)
	})
	nativePendingPublicationPipelines.Range(func(_, value any) bool {
		record := value.(*nativePublicationRecord)
		if record.scope.matches(s) && analysisQueryContextLive(ctx) {
			call, stop := context.WithTimeout(ctx, 5*time.Second)
			_ = s.reconcileNativePublication(call, attempts, record)
			stop()
		}
		return analysisQueryContextLive(ctx)
	})
}
func (s *Server) startNativeAnalysisReconciliation() {
	s.launchBackground(func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			s.reconcileNativeAnalysisOnce(s.startupContext)
			select {
			case <-s.startupContext.Done():
				return
			case <-ticker.C:
			}
		}
	})
}

// Called only when the claimed job was not admitted by launchBackground. An
// idle snapshot is insufficient: retain the exact one-way hold before revoking
// its context, then persist only after the actual monitor/artifact owners join.
func (s *Server) failUnlaunchedAnalysisImport(pipelineContext, request context.Context, attempts storage.AnalysisAttemptStore, in storage.AnalysisImport, pipeline *analytics.NativePipeline, dir string, stop context.CancelFunc) {
	record, _ := s.retainNativeWriterQuiescence(pipelineContext, in, pipeline, dir)
	stop()
	failure, cancel := context.WithTimeout(context.WithoutCancel(request), time.Second)
	defer cancel()
	if record != nil {
		_ = s.confirmNativeWriterQuiescence(failure, attempts, record)
	}
	_ = attempts.FailAnalysisImportAttempt(failure, in.ID, in.ProjectID, in.Owner, in.Revision)
	_ = closeNativeAnalysisPipeline(pipeline, dir)
}
