package api

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/analysis"
	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

type nativeReconciliationFaultStore struct {
	*storage.SQLiteStore
	confirmOutage, confirmLostReply, resolveOutage atomic.Bool
	confirms, resolutions                          atomic.Int64
}

func (s *nativeReconciliationFaultStore) ConfirmAnalysisAttemptWritersQuiesced(ctx context.Context, id, project, owner string) error {
	s.confirms.Add(1)
	if s.confirmOutage.Load() {
		return errors.New("controlled database acknowledgement outage")
	}
	err := s.SQLiteStore.ConfirmAnalysisAttemptWritersQuiesced(ctx, id, project, owner)
	if err == nil && s.confirmLostReply.Load() {
		return storage.ErrAnalysisPublicationUnknown
	}
	return err
}
func (s *nativeReconciliationFaultStore) ResolveAnalysisAttemptPublication(ctx context.Context, id, project, owner, digest string) (storage.AnalysisAttemptResolution, error) {
	s.resolutions.Add(1)
	if s.resolveOutage.Load() {
		return storage.AnalysisAttemptResolution{}, storage.ErrAnalysisPublicationUnknown
	}
	return s.SQLiteStore.ResolveAnalysisAttemptPublication(ctx, id, project, owner, digest)
}

func nativeReconciliationFixture(t *testing.T) (*Server, *nativeReconciliationFaultStore, *storage.AnalysisImport) {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	store, err := storage.NewSQLiteStore(t.TempDir() + "/reconciliation.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UTC()
	if err = store.SaveProject(&storage.Project{ID: "proj-a", Name: "Reconciliation", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	in, err := store.SaveAnalysisImport(storage.AnalysisImport{ID: uuid.NewString(), ProjectID: "proj-a", Name: "owned native attempt", State: "uploading", Files: []storage.AnalysisImportFile{{ID: "file", RelativePath: "fixture.parquet", Size: 12, Uploaded: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	in.State = "mapped"
	in.Mapping = &analysis.ParquetMapping{Version: 1, Timestamp: analysis.ParquetColumnPath{"time"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: analysis.ParquetColumnPath{"value"}, Name: "value", Kind: analysis.KindGauge, Unit: "ms"}}}
	in, err = store.SaveAnalysisImport(*in, in.Revision)
	if err != nil {
		t.Fatal(err)
	}
	in, _, err = store.ClaimAnalysisImportAttempt(t.Context(), in.ID, in.ProjectID, in.Revision, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	fault := &nativeReconciliationFaultStore{SQLiteStore: store}
	// Core retention tests need no scheduler/recovery fixture. Startup integration
	// below separately constructs and joins a real Server.
	return &Server{store: fault, nativeReconciliationStore: fault}, fault, in
}
func nativeReconciliationPipeline(t *testing.T) (context.Context, *analytics.NativePipeline, string) {
	t.Helper()
	if !analytics.Available() {
		t.Skip("actual admitted native pipeline requires analytics_duckdb+cgo profile")
	}
	backend := currentArchiveBackend()
	root, err := nativeAnalysisCacheRootForBackend(backend)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), nativeAnalysisArtifactBackendKey{}, backend)
	ctx, pipeline, err := analytics.ReserveNativePipeline(ctx, root, analytics.PipelinePlan{InputBytes: 12, OutputBytes: 12, StagingBytes: 12, MemoryBytes: 64 << 20, SpillBytes: 8 << 20, Runtime: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := pipeline.CreateStagingDirectory(root)
	if err != nil {
		_ = pipeline.Release()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if value, ok := nativePendingWriterPipelines.Load(pipeline); ok {
			record := value.(*nativeWriterQuiescenceRecord)
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := waitNativeWriterPhysical(ctx, record.lease.PhysicalDone()); err != nil {
				t.Error("retaining unjoined physical test owner", err)
				return
			}
			attempts := record.scope.origin.(storage.AnalysisAttemptStore)
			// Clear only known fixture fault injection, never an actual close failure.
			if owner, ok := attempts.(*nativeReconciliationFaultStore); ok {
				owner.confirmOutage.Store(false)
				owner.confirmLostReply.Store(false)
			}
			if err := attempts.ConfirmAnalysisAttemptWritersQuiesced(ctx, record.claim.id, record.claim.project, record.claim.owner); err != nil {
				t.Error(err)
				return
			}
			if err := record.lease.Resolve(); err != nil {
				t.Error(err)
				return
			}
			nativePendingWriterPipelines.CompareAndDelete(pipeline, record)
		}
		if value, ok := nativePendingPublicationPipelines.Load(pipeline); ok {
			record := value.(*nativePublicationRecord)
			if owner, ok := record.scope.origin.(*nativeReconciliationFaultStore); ok {
				owner.resolveOutage.Store(false)
			}
			s := &Server{store: record.scope.origin, nativeReconciliationStore: record.scope.origin}
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if err := s.reconcileNativePublication(ctx, record.scope.origin.(storage.AnalysisAttemptStore), record); err != nil {
				t.Error("retaining unresolved physical test owner", err)
				return
			}
		}
		_ = pipeline.Release()
		select {
		case <-pipeline.Done():
		case <-time.After(5 * time.Second):
			t.Error("pipeline physical test owner did not join")
		}
	})
	return ctx, pipeline, dir
}
func assertNativeRetained(t *testing.T, pipeline *analytics.NativePipeline, dir string) {
	t.Helper()
	select {
	case <-pipeline.Done():
		t.Fatal("unresolved receipt released physical admission")
	default:
	}
	if !analytics.NativeBusy() {
		t.Fatal("unresolved receipt lost heavy gate")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("owned staging disappeared", err)
	}
}
func TestNativeQuiescenceDatabaseOutageRetainsExactProofAndRetries(t *testing.T) {
	for _, lostReply := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-commit", true: "after-commit"}[lostReply], func(t *testing.T) {
			s, store, in := nativeReconciliationFixture(t)
			ctx, pipeline, dir := nativeReconciliationPipeline(t)
			store.confirmOutage.Store(!lostReply)
			store.confirmLostReply.Store(lostReply)
			record, err := s.retainNativeWriterQuiescence(ctx, *in, pipeline, dir)
			if err != nil {
				t.Fatal(err)
			}
			call, stop := context.WithTimeout(context.Background(), 5*time.Second)
			err = s.confirmNativeWriterQuiescence(call, store, record)
			stop()
			if err == nil {
				t.Fatal("outage accepted")
			}
			if _, err = pipeline.AcquireIO(); err == nil {
				t.Fatal("failed acknowledgement reopened writer admission")
			}
			_ = pipeline.Release()
			assertNativeRetained(t, pipeline, dir)
			if value, ok := nativePendingWriterPipelines.Load(pipeline); !ok || value != record {
				t.Fatal("exact retry record lost")
			}
			// A new instance of the same process/store reuses the original opaque
			// physical lease. It does not reacquire or infer proof from gate completion.
			store.confirmOutage.Store(false)
			store.confirmLostReply.Store(false)
			second, err := storage.NewSQLiteStore(store.BackendInfo().DSN)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			resumed := &Server{store: second, nativeReconciliationStore: second}
			resumed.reconcileNativeAnalysisOnce(t.Context())
			select {
			case <-record.done:
			case <-time.After(5 * time.Second):
				t.Fatal("exact resumed writer reconciliation did not finish")
			}
			select {
			case <-pipeline.Done():
			case <-time.After(5 * time.Second):
				t.Fatal("exact resumed acknowledgement did not release")
			}
			if _, ok := nativePendingWriterPipelines.Load(pipeline); ok {
				t.Fatal("resolved receipt retained")
			}
		})
	}
}

type nativeReconciliationBlockedFile struct {
	file             *os.File
	entered, release chan struct{}
	once             sync.Once
}

func (f *nativeReconciliationBlockedFile) Close() error {
	f.once.Do(func() { close(f.entered) })
	<-f.release
	return f.file.Close()
}
func TestNativeQuiescenceCannotConfirmUntilActualArtifactCloseJoins(t *testing.T) {
	s, store, in := nativeReconciliationFixture(t)
	ctx, pipeline, dir := nativeReconciliationPipeline(t)
	io, err := pipeline.AcquireIO()
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(dir, "owned-source-")
	if err != nil {
		io.Joined()
		t.Fatal(err)
	}
	owner := &nativeReconciliationBlockedFile{file: file, entered: make(chan struct{}), release: make(chan struct{})}
	io.TrackOwner(owner)
	joined := make(chan struct{})
	var release sync.Once
	t.Cleanup(func() {
		release.Do(func() { close(owner.release) })
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("actual close invocation did not join")
		}
	})
	go func() { defer close(joined); _ = io.CloseOwner(owner); io.Joined() }()
	select {
	case <-owner.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("actual owned close not entered")
	}
	record, err := s.retainNativeWriterQuiescence(ctx, *in, pipeline, dir)
	if err != nil {
		t.Fatal(err)
	}
	call := nativeQueryDetachedDeadline{Context: context.Background(), deadline: time.Now().Add(20 * time.Millisecond)}
	if err = s.confirmNativeWriterQuiescence(call, store, record); err == nil {
		t.Fatal("blocked file supplied proof")
	}
	if store.confirms.Load() != 0 {
		t.Fatal("durable proof attempted before actual file close")
	}
	assertNativeRetained(t, pipeline, dir)
	release.Do(func() { close(owner.release) })
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("close caller not joined")
	}
	callCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err = s.confirmNativeWriterQuiescence(callCtx, store, record); err != nil {
		t.Fatal(err)
	}
	if store.confirms.Load() != 1 {
		t.Fatal("exact physical proof not acknowledged once")
	}
}
func TestNativePendingReceiptRefusesDifferentDatabaseAndArtifactNamespace(t *testing.T) {
	s, store, in := nativeReconciliationFixture(t)
	ctx, pipeline, dir := nativeReconciliationPipeline(t)
	store.confirmOutage.Store(true)
	record, err := s.retainNativeWriterQuiescence(ctx, *in, pipeline, dir)
	if err != nil {
		t.Fatal(err)
	}
	call, stop := context.WithTimeout(context.Background(), 5*time.Second)
	_ = s.confirmNativeWriterQuiescence(call, store, record)
	stop()
	foreign, err := storage.NewSQLiteStore(t.TempDir() + "/foreign.db")
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	other := &Server{store: foreign, nativeReconciliationStore: foreign}
	other.reconcileNativeAnalysisOnce(t.Context())
	if value, ok := nativePendingWriterPipelines.Load(pipeline); !ok || value != record {
		t.Fatal("foreign database resolved original receipt")
	}
	original := os.Getenv("APEXLOAD_ARCHIVE_DIR")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	if record.scope.matches(s) {
		t.Fatal("changed artifact namespace accepted")
	}
	if _, err = nativeAnalysisArtifactBackend(ctx); err == nil {
		t.Fatal("pinned artifact IO substituted current namespace")
	}
	s.reconcileNativeAnalysisOnce(t.Context())
	if store.confirms.Load() != 1 {
		t.Fatal("changed namespace attempted SQL acknowledgement")
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", original)
	store.confirmOutage.Store(false)
	s.reconcileNativeAnalysisOnce(t.Context())
	if _, ok := nativePendingWriterPipelines.Load(pipeline); ok {
		t.Fatal("restored exact namespace failed to resolve")
	}
}
func TestNativeUnknownPublicationRetainsCompleteReceiptAcrossRealServerReplacement(t *testing.T) {
	s, store, in := nativeReconciliationFixture(t)
	ctx, pipeline, dir := nativeReconciliationPipeline(t)
	proof, err := s.retainNativeWriterQuiescence(ctx, *in, pipeline, dir)
	if err != nil {
		t.Fatal(err)
	}
	call, stop := context.WithTimeout(context.Background(), 5*time.Second)
	if err = s.confirmNativeWriterQuiescence(call, store, proof); err != nil {
		stop()
		t.Fatal(err)
	}
	stop()
	publication, err := pipeline.RetainPublication()
	if err != nil {
		t.Fatal(err)
	}
	candidate := *in
	candidate.Files = append([]storage.AnalysisImportFile{}, in.Files...)
	for i := range candidate.Files {
		candidate.Files[i].SHA256 = strings.Repeat("a", 64)
	}
	candidate.NormalizedSHA256 = map[string]string{}
	for _, key := range candidate.Normalized {
		candidate.NormalizedSHA256[key] = strings.Repeat("b", 64)
	}
	candidate.Accepted = 1
	digest, err := storage.AnalysisAttemptResultDigest(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.PublishAnalysisImportAttempt(ctx, storage.AnalysisAttemptPublication{Import: candidate, AttemptID: in.Owner, Digest: digest}); err != nil {
		t.Fatal(err)
	}
	store.resolveOutage.Store(true)
	// Model the observed lost commit acknowledgement plus unavailable durable
	// resolution. The original immutable digest and exact publication lease stay.
	s.retainNativePublicationReconciliation(ctx, candidate, pipeline, publication, dir, digest)
	_ = pipeline.Release()
	assertNativeRetained(t, pipeline, dir)
	value, ok := nativePendingPublicationPipelines.Load(pipeline)
	if !ok {
		t.Fatal("publication record missing")
	}
	record := value.(*nativePublicationRecord)
	if record.claim != nativeClaim(candidate) || record.digest != digest || record.dir != dir || record.lease != publication {
		t.Fatal("immutable retry arguments lost")
	}
	// Configuration may be temporarily different during shutdown. The exact
	// record remains reachable and is resumed only by the original namespace.
	store.resolveOutage.Store(false)
	second, err := storage.NewSQLiteStore(store.BackendInfo().DSN)
	if err != nil {
		t.Fatal(err)
	}
	resumed := NewServer(ServerConfig{Store: second, MaxConcurrent: 2})
	t.Cleanup(func() {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := resumed.Shutdown(ctx); err != nil {
			t.Error("retaining replacement store", err)
			return
		}
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	})
	select {
	case <-record.done:
	case <-time.After(5 * time.Second):
		t.Fatal("new Server startup did not finish exact publication reconciliation")
	}
	select {
	case <-pipeline.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("resolved publication did not release physical pipeline")
	}
	if _, ok := nativePendingPublicationPipelines.Load(pipeline); ok {
		t.Fatal("resolved publication record retained")
	}
	current, err := second.GetAnalysisImport(in.ID)
	if err != nil || current.State != "ready" || current.Accepted != 1 {
		t.Fatal("published receipt was overwritten by cleanup", err)
	}
}

func TestNativeUnlaunchedJobFencesBeforeStopAndRetainsFailedConfirmation(t *testing.T) {
	s, store, in := nativeReconciliationFixture(t)
	ctx, pipeline, dir := nativeReconciliationPipeline(t)
	store.confirmOutage.Store(true)
	var stopped atomic.Bool
	stop := func() {
		stopped.Store(true)
		if value, ok := nativePendingWriterPipelines.Load(pipeline); !ok || value.(*nativeWriterQuiescenceRecord).lease == nil {
			t.Error("no-launch cancellation preceded exact physical hold")
		}
		if io, err := pipeline.AcquireIO(); err == nil {
			io.Joined()
			t.Error("no-launch stop admitted a replacement writer")
		}
	}
	s.failUnlaunchedAnalysisImport(ctx, t.Context(), store, *in, pipeline, dir, stop)
	if !stopped.Load() {
		t.Fatal("unlaunched lifetime not stopped")
	}
	assertNativeRetained(t, pipeline, dir)
	value, ok := nativePendingWriterPipelines.Load(pipeline)
	if !ok {
		t.Fatal("failed no-launch Confirm lost retry arguments")
	}
	record := value.(*nativeWriterQuiescenceRecord)
	if record.claim != nativeClaim(*in) || record.dir != dir {
		t.Fatal("no-launch retained a different claim")
	}
	current, err := store.GetAnalysisImport(in.ID)
	if err != nil || current.State != "failed" {
		t.Fatal("unlaunched attempt did not enter exact failed state", err)
	}
	store.confirmOutage.Store(false)
	s.reconcileNativeAnalysisOnce(t.Context())
	select {
	case <-pipeline.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("failed no-launch retry did not release physical owner")
	}
}

func TestNativePhysicalProofWaitKeepsBooleanAuthorityPredicateAndErrorBoundary(t *testing.T) {
	joined := make(chan struct{})
	close(joined)
	live := nativeQueryDetachedDeadline{Context: context.Background(), deadline: time.Now().Add(time.Minute)}
	if !analysisQueryContextLive(live) || waitNativeWriterPhysical(live, joined) != nil {
		t.Fatal("joined physical proof with live authority was refused")
	}
	expired := nativeQueryDetachedDeadline{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
	if analysisQueryContextLive(expired) || !errors.Is(waitNativeWriterPhysical(expired, joined), errNativeReconciliationRetained) {
		t.Fatal("closed physical channel upgraded expired authority")
	}
	if !errors.Is(waitNativeWriterPhysical(live, nil), errNativeReconciliationRetained) {
		t.Fatal("missing physical channel became proof")
	}
}

// Construct a real Server before deliberately replacing the legacy request-side
// fixture field. Stop all other background users first so this oracle tests only
// native recovery's immutable authority, without manufacturing unrelated races.
func TestNativeReconciliationPinsStoreAtRealServerConstruction(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	s, configured := newTimeslotServer(t)
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	if err := s.Shutdown(shutdown); err != nil {
		stop()
		t.Fatal(err)
	}
	stop()
	if !nativeSameStore(s.nativeReconciliationStore, configured) {
		t.Fatal("construction did not capture configured SQL authority")
	}
	foreign, err := storage.NewSQLiteStore(t.TempDir() + "/foreign.db")
	if err != nil {
		t.Fatal(err)
	}
	defer foreign.Close()
	scope, err := s.nativeReconciliationScope(t.Context())
	if err != nil || !nativeSameStore(scope.origin, configured) {
		t.Fatal("scope did not capture startup authority", err)
	}
	entered := make(chan struct{})
	joined := make(chan struct{})
	defer func() { <-joined; s.store = configured }()
	go func() {
		defer close(joined)
		close(entered)
		for range 1000 {
			captured, captureErr := s.nativeReconciliationScope(t.Context())
			if captureErr != nil || !nativeSameStore(captured.origin, configured) || !scope.matches(s) {
				t.Error("request-side replacement changed recovery authority", captureErr)
				return
			}
			s.reconcileNativeAnalysisOnce(t.Context())
		}
	}()
	<-entered
	for range 1000 {
		s.store = foreign
		s.store = nil
		s.store = configured
	}
	<-joined
	if !s.nativeReconciliationAttemptsMatch(configured.(storage.AnalysisAttemptStore)) || s.nativeReconciliationAttemptsMatch(foreign) {
		t.Fatal("attempt adapter escaped exact startup authority")
	}
	// No lazy fallback: a zero-value/unconfigured owner cannot claim a store that
	// is installed later into the mutable fixture/request field.
	unconfigured := &Server{store: configured}
	if _, err := unconfigured.nativeReconciliationScope(t.Context()); err == nil || scope.matches(unconfigured) {
		t.Fatal("unconfigured recovery borrowed mutable request authority")
	}
	unconfigured.reconcileNativeAnalysisOnce(t.Context())
}

func TestNativeQuiescenceRefusesForeignPassedAttemptAdapter(t *testing.T) {
	s, configured, in := nativeReconciliationFixture(t)
	ctx, pipeline, dir := nativeReconciliationPipeline(t)
	record, err := s.retainNativeWriterQuiescence(ctx, *in, pipeline, dir)
	if err != nil {
		t.Fatal(err)
	}
	foreign := &nativeReconciliationFaultStore{SQLiteStore: configured.SQLiteStore}
	if err = s.confirmNativeWriterQuiescence(ctx, foreign, record); !errors.Is(err, errNativeReconciliationRetained) || foreign.confirms.Load() != 0 {
		t.Fatal("foreign request adapter touched durable quiescence", err)
	}
	if value, ok := nativePendingWriterPipelines.Load(pipeline); !ok || value != record {
		t.Fatal("foreign attempt adapter released exact physical hold")
	}
	if err = s.confirmNativeWriterQuiescence(ctx, configured, record); err != nil {
		t.Fatal("configured startup adapter failed actual confirmation", err)
	}
}
