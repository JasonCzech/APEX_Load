package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

type uploadReceiptOutageStore struct {
	*storage.SQLiteStore
	outage  atomic.Bool
	entered chan struct{}
	once    sync.Once
}

func (s *uploadReceiptOutageStore) ResolveAnalysisUploadWriter(ctx context.Context, project, importID, id string) (*storage.AnalysisUploadWriter, error) {
	if s.outage.Load() {
		s.once.Do(func() { close(s.entered) })
		return nil, storage.ErrAnalysisPublicationUnknown
	}
	return s.SQLiteStore.ResolveAnalysisUploadWriter(ctx, project, importID, id)
}

func shutdownUploadFixtureServer(t *testing.T, server *Server) {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAnalysisUploadExactCompletionSurvivesServerReplacementAndSealsIO(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	initial, base := newTimeslotServer(t)
	shutdownUploadFixtureServer(t, initial)
	fault := &uploadReceiptOutageStore{SQLiteStore: base.(*storage.SQLiteStore), entered: make(chan struct{})}
	fault.outage.Store(true)
	server := NewServer(ServerConfig{Store: fault, MaxConcurrent: 2})
	t.Cleanup(func() { shutdownUploadFixtureServer(t, server) })
	in, err := fault.SaveAnalysisImport(storage.AnalysisImport{ID: uuid.NewString(), ProjectID: "proj-a", Name: "retained upload", State: "uploading", UploadWriterVersion: storage.AnalysisUploadWriterVersion, Files: []storage.AnalysisImportFile{{ID: uuid.NewString(), RelativePath: "data.parquet", Size: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	owner, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("admission")
	}
	t.Cleanup(func() {
		fault.outage.Store(false)
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		server.reconcileAnalysisUploadWritersOnce(cleanupCtx)
		owner.endRequest()
		waitUploadOwnerReleased(t, owner)
	})
	ctx := context.WithValue(t.Context(), analysisUploadOwnerKey{}, owner)
	end, err := owner.beginOperation()
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	data := []byte("PAR1dataPAR1")
	digest := sha256.Sum256(data)
	server.runAnalysisUploadChunk(ctx, fault, owner, *in, in.Files[0], 0, data, hex.EncodeToString(digest[:]))
	end()
	owner.endRequest()
	select {
	case <-fault.entered:
	case <-time.After(time.Second):
		t.Fatal("completion did not enter retained SQL outage")
	}
	if end, err := owner.beginOperation(); err == nil {
		end()
		t.Fatal("ambiguous completion reopened body/artifact admission")
	}
	select {
	case <-owner.done:
		t.Fatal("SQL outage retired unresolved exact owner")
	default:
	}
	shutdownUploadFixtureServer(t, server)
	if _, ok := analysisUploadOwners.Load(owner.id); !ok {
		t.Fatal("Server shutdown discarded retry owner")
	}
	second, err := storage.NewSQLiteStore(fault.BackendInfo().DSN)
	if err != nil {
		t.Fatal(err)
	}
	resumed := NewServer(ServerConfig{Store: second, MaxConcurrent: 2})
	t.Cleanup(func() { shutdownUploadFixtureServer(t, resumed); _ = second.Close() })
	select {
	case <-owner.done:
	case <-time.After(5 * time.Second):
		t.Fatal("same-namespace Server startup did not resume exact receipt")
	}
	waitUploadOwnerReleased(t, owner)
	receipt, err := owner.outcome()
	if err != nil || receipt == nil || !receipt.PhysicallyClosed || receipt.State != "accepted" {
		t.Fatal("resumed publication lost exact physical decision", receipt, err)
	}
	current, err := second.GetAnalysisImport(in.ID)
	if err != nil || current.Revision != in.Revision+1 || current.Files[0].Uploaded != 12 || len(current.Files[0].Chunks) != 1 {
		t.Fatal("resumed cursor was not atomic", current, err)
	}
	if end, err := owner.beginOperation(); err == nil {
		end()
		t.Fatal("completed metadata resolution reopened writer admission")
	}
	var original bytes.Buffer
	if _, err = analysisObjectRead(t.Context(), receipt.ObjectKey, &original, 12); err != nil || !bytes.Equal(original.Bytes(), data) {
		t.Fatal("resumed source bytes changed", err)
	}
}

type uploadPanicBody struct {
	reads, closes atomic.Int32
}

func (b *uploadPanicBody) Read([]byte) (int, error) { b.reads.Add(1); panic("controlled body panic") }
func (b *uploadPanicBody) Close() error             { b.closes.Add(1); return nil }

// Tests know these injected panic callbacks allocate no hidden resource. This
// fixture-only registry retirement does not clear/forgive production's unknown
// flag, retry a Close, or supply durable physical-writer proof.
func retireUnknownUploadFixture(t *testing.T, owner *analysisUploadPhysicalOwner) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		owner.mu.Lock()
		knownJoined := owner.operations == 0
		for _, d := range owner.descriptors {
			knownJoined = knownJoined && d.joined
		}
		unknown := owner.operationUnknown
		owner.mu.Unlock()
		if unknown && knownJoined {
			analysisUploadOwners.Delete(owner.id)
			owner.releaseOnce.Do(func() { <-analysisUploadWriterSlots })
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Error("unknown fixture still has actual unjoined known owners")
}

func TestAnalysisUploadBodyPanicCannotBecomeEmptyCloseSetProof(t *testing.T) {
	owner, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("admission")
	}
	t.Cleanup(func() { retireUnknownUploadFixture(t, owner) })
	body := &uploadPanicBody{}
	_, err := readAnalysisUploadBody(t.Context(), owner, body)
	if !errors.Is(err, errAnalysisUploadPhysical) {
		t.Fatal("body panic was not bounded unknown ownership", err)
	}
	owner.endRequest()
	if owner.physicalJoined() || owner.sealPhysical() {
		t.Fatal("known body Close erased unknown allocation ownership")
	}
	if _, ok := analysisUploadOwners.Load(owner.id); !ok || body.reads.Load() != 1 || body.closes.Load() != 1 {
		t.Fatal("panic lost exact admission or duplicated known Close")
	}
}

type uploadCountingBody struct{ reads, closes atomic.Int32 }

func (b *uploadCountingBody) Read([]byte) (int, error) { b.reads.Add(1); return 0, io.EOF }
func (b *uploadCountingBody) Close() error             { b.closes.Add(1); return nil }

func TestAnalysisUploadActualDeadlineRefusesBodyBeforeAdmission(t *testing.T) {
	owner, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("admission")
	}
	defer owner.endRequest()
	body := &uploadCountingBody{}
	expired := nativeQueryDetachedDeadline{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
	if _, err := readAnalysisUploadBody(expired, owner, body); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("actual expired deadline accepted", err)
	}
	if body.reads.Load() != 0 || body.closes.Load() != 0 || !owner.physicalJoined() {
		t.Fatal("expired authority entered physical body operation")
	}
}

func TestAnalysisUploadDetachedFutureDeadlineBoundsCallerButRetainsActualRead(t *testing.T) {
	owner, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("admission")
	}
	body := &uploadBlockedBody{entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	release := sync.OnceFunc(func() { close(body.release) })
	t.Cleanup(func() { release(); owner.endRequest(); waitUploadOwnerReleased(t, owner) })
	ctx := nativeQueryDetachedDeadline{Context: context.Background(), deadline: time.Now().Add(30 * time.Millisecond)}
	done := make(chan error, 1)
	go func() { _, err := readAnalysisUploadBody(ctx, owner, body); done <- err }()
	select {
	case <-body.entered:
	case <-time.After(time.Second):
		t.Fatal("body did not enter")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("detached deadline left HTTP caller unbounded")
	}
	owner.endRequest()
	if owner.physicalJoined() {
		t.Fatal("deadline supplied blocked Read proof")
	}
	release()
	waitUploadOwnerReleased(t, owner)
}

func TestAnalysisUploadCursorLateDeadlineCannotPublishPrivateReceipt(t *testing.T) {
	server, store := newTimeslotServer(t)
	in, err := store.(storage.AnalysisImportStore).SaveAnalysisImport(storage.AnalysisImport{ID: uuid.NewString(), ProjectID: "proj-a", Name: "upload", State: "uploading", UploadWriterVersion: storage.AnalysisUploadWriterVersion, Files: []storage.AnalysisImportFile{{ID: uuid.NewString(), RelativePath: "data.parquet", Size: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	expired := nativeQueryDetachedDeadline{Context: context.Background(), deadline: time.Now().Add(-time.Second)}
	request := httptest.NewRequest("PUT", "/", nil).WithContext(expired)
	response := httptest.NewRecorder()
	if err = server.writeAnalysisUploadCursor(response, request, *in, in.Files[0].ID, 0, in.Revision); err == nil || response.Body.Len() != 0 {
		t.Fatal("private upload cursor bypassed actual return authority", err)
	}
}

func nativeUploadCursorFixture(t *testing.T, wrappers ...func(*storage.SQLiteStore) storage.Store) (*Server, *storage.SQLiteStore, *storage.AnalysisImport) {
	t.Helper()
	server, store, _ := nativeQueryAuthorityFixture(t, wrappers...)
	in, err := store.SaveAnalysisImport(storage.AnalysisImport{ID: uuid.NewString(), ProjectID: "proj-a", Name: "current upload cursor", State: "uploading", UploadWriterVersion: storage.AnalysisUploadWriterVersion, Files: []storage.AnalysisImportFile{{ID: uuid.NewString(), RelativePath: "data.parquet", Size: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return server, store, in
}

func TestAnalysisUploadCursorFinalAuthorityRefusesChangedScope(t *testing.T) {
	for _, change := range []string{"unchanged", "cancelled", "revision", "association", "project", "foreign-project", "artifact-namespace"} {
		t.Run(change, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			server, store, in := nativeUploadCursorFixture(t)
			ctx, _, err := server.bindAnalysisUploadContext(t.Context(), store)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "cancelled", "revision", "association":
				current := *in
				if change == "cancelled" {
					current.State = "cancelled"
				} else if change == "revision" {
					current.Name = "later metadata"
				} else {
					if err := store.SaveTest(&storage.TestRecord{ID: "upload-associated-run", ProjectID: in.ProjectID, Name: "upload association", Status: "COMPLETE", StartTime: time.Now().UTC()}); err != nil {
						t.Fatal(err)
					}
					current.RunID = "upload-associated-run"
				}
				if _, err = store.SaveAnalysisImport(current, current.Revision); err != nil {
					t.Fatal(err)
				}
			case "project":
				if err := store.DeleteProject(in.ProjectID); err != nil {
					t.Fatal(err)
				}
			case "artifact-namespace":
				t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			}
			r := httptest.NewRequest("PUT", "/", nil).WithContext(ctx)
			bindAuthorizedProjectToRequest(r, in.ProjectID)
			if change == "foreign-project" {
				bindAuthorizedProjectToRequest(r, "proj-b")
			}
			w := httptest.NewRecorder()
			err = server.writeAnalysisUploadCursor(w, r, *in, in.Files[0].ID, 0, in.Revision)
			if change == "unchanged" {
				if err != nil || w.Code != 200 || w.Body.Len() == 0 {
					t.Fatal("exact current upload cursor refused", err, w.Code)
				}
			} else if err == nil || w.Body.Len() != 0 {
				t.Fatal("private cursor bypassed final current authority", change, err, w.Body.String())
			}
		})
	}
}

func TestAnalysisUploadCursorMetadataBarrierRechecksRevisionAndActualDeadline(t *testing.T) {
	for _, stage := range []string{"revision", "project-deadline", "dataset-deadline"} {
		t.Run(stage, func(t *testing.T) {
			var barrier *nativeQueryMetadataBarrier
			server, store, in := nativeUploadCursorFixture(t, func(store *storage.SQLiteStore) storage.Store {
				barrier = &nativeQueryMetadataBarrier{SQLiteStore: store, entered: make(chan struct{}), release: make(chan struct{}), blockProject: stage == "project-deadline", blockDataset: stage != "project-deadline"}
				return barrier
			})
			ctx := context.Context(t.Context())
			if stage != "revision" {
				ctx = nativeQueryDetachedDeadline{Context: ctx, deadline: time.Now().Add(time.Second)}
			}
			barrier.armed.Store(true)
			r := httptest.NewRequest("PUT", "/", nil).WithContext(ctx)
			bindAuthorizedProjectToRequest(r, in.ProjectID)
			w := httptest.NewRecorder()
			done, joined := make(chan error, 1), make(chan struct{})
			release := sync.OnceFunc(func() { close(barrier.release) })
			t.Cleanup(func() {
				release()
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					t.Error("exact cursor metadata continuation did not join")
				}
			})
			go func() {
				defer close(joined)
				done <- server.writeAnalysisUploadCursor(w, r, *in, in.Files[0].ID, 0, in.Revision)
			}()
			select {
			case <-barrier.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("cursor metadata boundary not reached")
			}
			if stage == "revision" {
				current := *in
				current.Name = "concurrent upload metadata"
				if _, err := store.SaveAnalysisImport(current, current.Revision); err != nil {
					t.Fatal(err)
				}
			} else {
				deadline, _ := ctx.Deadline()
				timer := time.NewTimer(max(0, time.Until(deadline)) + time.Millisecond)
				<-timer.C
				timer.Stop()
			}
			release()
			select {
			case err := <-done:
				if err == nil || w.Body.Len() != 0 {
					t.Fatal("late cursor metadata return retained stale authority", err, w.Body.String())
				}
			case <-time.After(5 * time.Second):
				t.Fatal("released cursor metadata continuation did not return")
			}
		})
	}
}

type uploadResolveBarrierStore struct {
	*storage.SQLiteStore
	armed                atomic.Bool
	entered, release     chan struct{}
	completeCalls        atomic.Int32
	resolutionReturnOnce sync.Once
}

func (s *uploadResolveBarrierStore) ResolveAnalysisUploadWriter(ctx context.Context, project, importID, id string) (*storage.AnalysisUploadWriter, error) {
	writer, err := s.SQLiteStore.ResolveAnalysisUploadWriter(ctx, project, importID, id)
	if s.armed.Load() {
		s.resolutionReturnOnce.Do(func() { close(s.entered); <-s.release })
	}
	return writer, err
}
func (s *uploadResolveBarrierStore) CompleteAnalysisUploadWriter(ctx context.Context, project, id string, accepted bool) (*storage.AnalysisUploadWriter, error) {
	s.completeCalls.Add(1)
	return s.SQLiteStore.CompleteAnalysisUploadWriter(ctx, project, id, accepted)
}

func TestAnalysisUploadLateResolutionCannotAdmitNewCompletion(t *testing.T) {
	for _, boundary := range []string{"actual-deadline", "artifact-namespace", "actual-deadline-absent", "artifact-namespace-absent"} {
		t.Run(boundary, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
			originalRoot := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", originalRoot)
			var fault *uploadResolveBarrierStore
			server, store, in := nativeUploadCursorFixture(t, func(store *storage.SQLiteStore) storage.Store {
				fault = &uploadResolveBarrierStore{SQLiteStore: store, entered: make(chan struct{}), release: make(chan struct{})}
				return fault
			})
			// Stop automatic retry inventory before constructing this controlled
			// exact metadata owner. This fixture opens no body/artifact descriptor.
			shutdownUploadFixtureServer(t, server)
			absent := strings.HasSuffix(boundary, "-absent")
			var writer *storage.AnalysisUploadWriter
			var err error
			if absent {
				// Exact retained intent for an ambiguous uncommitted Begin. No
				// descriptor or writer was opened; the real SQLite resolver must
				// prove absence behind project/import transaction fences.
				writer = &storage.AnalysisUploadWriter{ID: uuid.NewString(), ProjectID: in.ProjectID, ImportID: in.ID, FileID: in.Files[0].ID, Revision: in.Revision, Bytes: 12, SHA256: strings.Repeat("a", 64), State: "pending", CreatedAt: time.Now().UTC()}
				writer.ObjectKey = analysisObjectKey(in.ProjectID, in.ID, analysisChunkName(in.Files[0], 0, writer.SHA256)+"-"+writer.ID)
				writer.TemporaryKey = analysisObjectKey(in.ProjectID, in.ID, "upload-"+writer.ID+".tmp")
			} else {
				writer, err = store.BeginAnalysisUploadWriter(t.Context(), storage.AnalysisUploadRequest{ProjectID: in.ProjectID, ImportID: in.ID, FileID: in.Files[0].ID, Revision: in.Revision, Bytes: 12, SHA256: strings.Repeat("a", 64)})
				if err != nil {
					t.Fatal(err)
				}
			}
			scope, err := server.nativeReconciliationScope(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			owner, ok := acquireAnalysisUploadOwner()
			if !ok {
				t.Fatal("physical admission")
			}
			owner.setIntent(writer)
			owner.mu.Lock()
			owner.reconciliation = &analysisUploadReconciliationRecord{scope: scope, intent: *writer, accepted: false}
			owner.mu.Unlock()
			fault.armed.Store(true)
			release := sync.OnceFunc(func() { close(fault.release) })
			joined := make(chan struct{})
			t.Cleanup(func() {
				release()
				select {
				case <-joined:
				case <-time.After(5 * time.Second):
					t.Error("exact late resolver did not join")
					return
				}
				fault.armed.Store(false)
				t.Setenv("APEXLOAD_ARCHIVE_DIR", originalRoot)
				cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				defer stop()
				server.reconcileAnalysisUploadOwner(cleanupCtx, owner)
				owner.endRequest()
				waitUploadOwnerReleased(t, owner)
			})
			ctx := context.Context(t.Context())
			if strings.HasPrefix(boundary, "actual-deadline") {
				ctx = nativeQueryDetachedDeadline{Context: context.Background(), deadline: time.Now().Add(time.Second)}
			}
			go func() { defer close(joined); server.reconcileAnalysisUploadOwner(ctx, owner) }()
			select {
			case <-fault.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("controlled resolver did not reach actual receipt return")
			}
			if strings.HasPrefix(boundary, "actual-deadline") {
				deadline, _ := ctx.Deadline()
				timer := time.NewTimer(max(0, time.Until(deadline)) + time.Millisecond)
				<-timer.C
				timer.Stop()
			} else {
				t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			}
			release()
			select {
			case <-joined:
			case <-time.After(5 * time.Second):
				t.Fatal("released metadata continuation did not join")
			}
			if fault.completeCalls.Load() != 0 {
				t.Fatal("late/foreign resolution admitted a new SQL completion")
			}
			select {
			case <-owner.done:
				t.Fatal("late/foreign resolution retired the sealed exact owner")
			default:
			}
			if end, err := owner.beginOperation(); err == nil {
				end()
				t.Fatal("retained late resolution reopened writer admission")
			}
			fault.armed.Store(false)
			t.Setenv("APEXLOAD_ARCHIVE_DIR", originalRoot)
			live, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			server.reconcileAnalysisUploadOwner(live, owner)
			owner.endRequest()
			waitUploadOwnerReleased(t, owner)
			receipt, err := owner.outcome()
			if absent {
				if !errors.Is(err, storage.ErrAnalysisAuthority) || receipt != nil || fault.completeCalls.Load() != 0 {
					t.Fatal("scoped committed absence did not reconcile without new completion", receipt, err)
				}
			} else if err != nil || receipt == nil || receipt.State != "abandoned" || !receipt.PhysicallyClosed || fault.completeCalls.Load() != 1 {
				t.Fatal("new live retry failed the exact retained completion", receipt, err)
			}
		})
	}
}

type uploadBeginPanicStore struct {
	*storage.SQLiteStore
	mu     sync.Mutex
	intent *storage.AnalysisUploadWriter
}

func (s *uploadBeginPanicStore) BeginAnalysisUploadWriter(ctx context.Context, request storage.AnalysisUploadRequest) (*storage.AnalysisUploadWriter, error) {
	writer, err := s.SQLiteStore.BeginAnalysisUploadWriter(ctx, request)
	if err != nil {
		return writer, err
	}
	s.mu.Lock()
	copy := *writer
	s.intent = &copy
	s.mu.Unlock()
	// A callback can panic after actual durable allocation before reporting its
	// owner. Production cannot infer rollback or an empty physical allocation.
	panic("controlled post-commit constructor panic")
}

func TestAnalysisUploadWriterPanicRetainsUnknownOuterOwnerAndJournal(t *testing.T) {
	var fault *uploadBeginPanicStore
	server, store, in := nativeUploadCursorFixture(t, func(store *storage.SQLiteStore) storage.Store {
		fault = &uploadBeginPanicStore{SQLiteStore: store}
		return fault
	})
	owner, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("admission")
	}
	t.Cleanup(func() {
		retireUnknownUploadFixture(t, owner)
		fault.mu.Lock()
		writer := fault.intent
		fault.mu.Unlock()
		if writer != nil {
			// This injected fixture has no artifact/body allocation; its known SQL
			// call returned before panic. Only fixture teardown can assert absence.
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			if _, err := store.CompleteAnalysisUploadWriter(ctx, writer.ProjectID, writer.ID, false); err != nil {
				t.Error(err)
			}
		}
	})
	data := []byte("PAR1dataPAR1")
	digest := sha256.Sum256(data)
	ctx := context.WithValue(t.Context(), analysisUploadOwnerKey{}, owner)
	w := httptest.NewRecorder()
	server.writeAnalysisUploadChunk(w, httptest.NewRequest("PUT", "/", nil).WithContext(ctx), fault, *in, in.Files[0], 0, data, hex.EncodeToString(digest[:]))
	owner.endRequest()
	if w.Code != 503 || owner.physicalJoined() || owner.sealPhysical() {
		t.Fatal("constructor panic became no-writer physical proof", w.Code, w.Body.String())
	}
	if _, ok := analysisUploadOwners.Load(owner.id); !ok {
		t.Fatal("constructor panic retired exact parent admission")
	}
	fault.mu.Lock()
	writer := fault.intent
	fault.mu.Unlock()
	if writer == nil {
		t.Fatal("controlled SQL allocation did not commit")
	}
	persisted, err := store.ResolveAnalysisUploadWriter(t.Context(), in.ProjectID, in.ID, writer.ID)
	if err != nil || persisted == nil || persisted.PhysicallyClosed || persisted.State != "pending" {
		t.Fatal("panic lost or forged durable writer evidence", persisted, err)
	}
	current, err := store.GetAnalysisImport(in.ID)
	if err != nil || current.Files[0].Uploaded != 0 || current.Revision != in.Revision {
		t.Fatal("unknown constructor published a cursor", current, err)
	}
	ready, err := store.AnalysisDatasetCleanupReady(t.Context(), in.ProjectID, in.ID)
	if err != nil || ready {
		t.Fatal("unknown constructor granted namespace deletion", ready, err)
	}
}

type uploadForeignAdapterStore struct {
	*storage.SQLiteStore
	begins atomic.Int32
}

func (s *uploadForeignAdapterStore) BeginAnalysisUploadWriter(ctx context.Context, request storage.AnalysisUploadRequest) (*storage.AnalysisUploadWriter, error) {
	s.begins.Add(1)
	return s.SQLiteStore.BeginAnalysisUploadWriter(ctx, request)
}

func TestAnalysisUploadForeignSameDatabaseAdapterRefusedBeforePhysicalAdmission(t *testing.T) {
	server, store, in := nativeUploadCursorFixture(t)
	foreign := &uploadForeignAdapterStore{SQLiteStore: store}
	owner, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("admission")
	}
	t.Cleanup(func() { owner.endRequest(); waitUploadOwnerReleased(t, owner) })
	ctx := context.WithValue(t.Context(), analysisUploadOwnerKey{}, owner)
	w := httptest.NewRecorder()
	data := []byte("PAR1dataPAR1")
	digest := sha256.Sum256(data)
	server.writeAnalysisUploadChunk(w, httptest.NewRequest("PUT", "/", nil).WithContext(ctx), foreign, *in, in.Files[0], 0, data, hex.EncodeToString(digest[:]))
	owner.mu.Lock()
	operations, descriptors, intent := owner.operations, len(owner.descriptors), owner.intent
	owner.mu.Unlock()
	if w.Code != 503 || foreign.begins.Load() != 0 || operations != 0 || descriptors != 0 || intent != nil {
		t.Fatal("same-DB foreign adapter admitted unowned SQL/artifact writes", w.Code, foreign.begins.Load(), operations, descriptors)
	}
}

type uploadWrongImportIDStore struct {
	*storage.SQLiteStore
	armed atomic.Bool
}

func (s *uploadWrongImportIDStore) GetAnalysisImport(id string) (*storage.AnalysisImport, error) {
	in, err := s.SQLiteStore.GetAnalysisImport(id)
	if in != nil && s.armed.Load() {
		copy := *in
		copy.ID = "00000000-0000-4000-8000-000000000099"
		return &copy, err
	}
	return in, err
}

func TestAnalysisUploadCursorRefusesForeignReturnedImportIdentity(t *testing.T) {
	var fault *uploadWrongImportIDStore
	server, _, in := nativeUploadCursorFixture(t, func(store *storage.SQLiteStore) storage.Store {
		fault = &uploadWrongImportIDStore{SQLiteStore: store}
		return fault
	})
	fault.armed.Store(true)
	r := httptest.NewRequest("PUT", "/", nil).WithContext(t.Context())
	bindAuthorizedProjectToRequest(r, in.ProjectID)
	w := httptest.NewRecorder()
	if err := server.writeAnalysisUploadCursor(w, r, *in, in.Files[0].ID, 0, in.Revision); err == nil || w.Body.Len() != 0 {
		t.Fatal("same-project/revision/file metadata returned a foreign import cursor", err, w.Body.String())
	}
}
