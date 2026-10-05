package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAnalysisDurableUploadDeleteRemovesExactOpaqueChunk(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "proj-a")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	server, store, in := nativeUploadCursorFixture(t)
	data := []byte("PAR1dataPAR1")
	abandoned, err := store.BeginAnalysisUploadWriter(t.Context(), storage.AnalysisUploadRequest{ProjectID: in.ProjectID, ImportID: in.ID, FileID: in.Files[0].ID, Revision: in.Revision, Offset: 0, Bytes: 12, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{abandoned.ObjectKey, abandoned.TemporaryKey} {
		if _, err = analysisObjectWrite(t.Context(), key, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	abandoned, err = store.CompleteAnalysisUploadWriter(t.Context(), in.ProjectID, abandoned.ID, false)
	if err != nil || !abandoned.PhysicallyClosed || abandoned.State != "abandoned" {
		t.Fatal("actual closed failed writer fixture", abandoned, err)
	}

	req := httptest.NewRequest("PUT", "/api/v1/analysis/imports/"+in.ID+"/files/"+in.Files[0].ID+"?offset=0", bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/octet-stream")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	if analytics.Available() {
		// The native profile exercises the complete real HTTP upload workflow.
		if response.Code != 200 {
			t.Fatalf("durable physical upload: %d %s", response.Code, response.Body.String())
		}
	} else {
		// The CGO-free profile intentionally cannot initiate analytical uploads.
		// Its deletion route must still reclaim existing durable accepted bytes.
		if response.Code != 501 {
			t.Fatalf("unavailable native profile accepted HTTP upload: %d %s", response.Code, response.Body.String())
		}
		current, err := store.GetAnalysisImport(in.ID)
		if err != nil || current == nil || len(current.Files) != 1 || current.Files[0].Uploaded != 0 {
			t.Fatal("capability refusal changed the upload cursor", current, err)
		}
		digest := sha256.Sum256(data)
		writer, err := store.BeginAnalysisUploadWriter(t.Context(), storage.AnalysisUploadRequest{ProjectID: in.ProjectID, ImportID: in.ID, FileID: in.Files[0].ID, Revision: current.Revision, Offset: 0, Bytes: int64(len(data)), SHA256: hex.EncodeToString(digest[:])})
		if err != nil {
			t.Fatal(err)
		}
		written, err := analysisObjectWrite(t.Context(), writer.ObjectKey, bytes.NewReader(data))
		if err != nil || written != writer.SHA256 {
			t.Fatal("trusted fixture physical write did not return actual nil Close proof", written, err)
		}
		// Only this fixture's actually returned Sync/Close permits durable proof;
		// neither the HTTP caller nor a timer supplies the completion boolean.
		writer, err = store.CompleteAnalysisUploadWriter(t.Context(), in.ProjectID, writer.ID, true)
		if err != nil || writer == nil || writer.State != "accepted" || !writer.PhysicallyClosed {
			t.Fatal("real durable accepted cursor fixture", writer, err)
		}
	}
	current, err := store.GetAnalysisImport(in.ID)
	if err != nil || current == nil || len(current.Files) != 1 || len(current.Files[0].Chunks) != 1 {
		t.Fatal("durable cursor", current, err)
	}
	key, err := storage.AnalysisUploadChunkObjectKey(current.ProjectID, current.ID, current.Files[0], current.Files[0].Chunks[0])
	if err != nil {
		t.Fatal(err)
	}
	legacy := analysisObjectKey(current.ProjectID, current.ID, analysisChunkName(current.Files[0], 0, current.Files[0].Chunks[0].SHA256))
	if key == legacy || current.Files[0].Chunks[0].ObjectKey != key {
		t.Fatal("fixture did not establish opaque source authority")
	}
	var original bytes.Buffer
	if _, err = analysisObjectRead(t.Context(), key, &original, int64(len(data))); err != nil || !bytes.Equal(original.Bytes(), data) {
		t.Fatal("uploaded exact object", err)
	}
	for range 2 {
		response = timeslotRequest(t, server, "DELETE", "/api/v1/analysis/imports/"+in.ID, nil)
		if response.Code != 204 {
			t.Fatalf("delete: %d %s", response.Code, response.Body.String())
		}
	}
	var retained bytes.Buffer
	if _, err = analysisObjectRead(t.Context(), key, &retained, 1); !errors.Is(err, os.ErrNotExist) || retained.Len() != 0 {
		t.Fatal("204 retained exact opaque source", err, retained.Len())
	}
	for _, key := range []string{abandoned.ObjectKey, abandoned.TemporaryKey} {
		var residual bytes.Buffer
		if _, err = analysisObjectRead(t.Context(), key, &residual, 1); !errors.Is(err, os.ErrNotExist) || residual.Len() != 0 {
			t.Fatal("204 retained closed abandoned writer object/temp", key, err)
		}
	}
	current, err = store.GetAnalysisImport(in.ID)
	if err != nil || current.State != "cancelled" {
		t.Fatal("uncommitted upload revocation lost", current, err)
	}
	ready, err := store.AnalysisDatasetCleanupReady(t.Context(), in.ProjectID, in.ID)
	if err != nil || !ready {
		t.Fatal("actual uploaded writer closure lost", ready, err)
	}
}
func TestAnalysisDatasetDeletionPlanValidatesEveryKeyBeforeDeleting(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	_, store, in := nativeUploadCursorFixture(t)
	writer, err := store.BeginAnalysisUploadWriter(t.Context(), storage.AnalysisUploadRequest{ProjectID: in.ProjectID, ImportID: in.ID, FileID: in.Files[0].ID, Revision: in.Revision, Offset: 0, Bytes: 12, SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := store.CompleteAnalysisUploadWriter(context.Background(), in.ProjectID, writer.ID, false); err != nil {
			t.Error(err)
		}
	})
	if _, err = analysisObjectWrite(t.Context(), writer.ObjectKey, bytes.NewReader([]byte("PAR1dataPAR1"))); err != nil {
		t.Fatal(err)
	}
	in.Files[0].Chunks = []storage.AnalysisChunk{{Offset: 0, Size: 12, SHA256: writer.SHA256, ObjectKey: writer.ObjectKey}}
	foreign := *in
	foreign.Normalized = []string{analysisObjectKey("other-project", in.ID, "part-0000-00.parquet")}
	if err = deleteAnalysisObjects(t.Context(), &foreign); !errors.Is(err, storage.ErrAnalysisAuthority) {
		t.Fatal("foreign deletion plan accepted", err)
	}
	var untouched bytes.Buffer
	if _, err = analysisObjectRead(t.Context(), writer.ObjectKey, &untouched, 12); err != nil || untouched.String() != "PAR1dataPAR1" {
		t.Fatal("later invalid key partially removed good source", err)
	}
	opaque, err := analysisDatasetDeletionKeys(in)
	if err != nil || len(opaque) != 2 || opaque[0] != writer.ObjectKey {
		t.Fatal("opaque plan", opaque, err)
	}
	in.Files[0].Chunks[0].ObjectKey = ""
	historical, err := analysisDatasetDeletionKeys(in)
	if err != nil || len(historical) != 2 || historical[0] != analysisObjectKey(in.ProjectID, in.ID, analysisChunkName(in.Files[0], 0, writer.SHA256)) {
		t.Fatal("historical key compatibility", historical, err)
	}
}

func TestAnalysisDatasetDeletionPlanBoundsEmptyChunkFileIDBeforeAllocation(t *testing.T) {
	_, _, in := nativeUploadCursorFixture(t)
	for _, id := range []string{strings.Repeat("x", 4<<20), "file", "AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA"} {
		bad := *in
		bad.Files = append([]storage.AnalysisImportFile{}, in.Files...)
		bad.Files[0].ID = id
		if keys, err := analysisDatasetDeletionKeys(&bad); !errors.Is(err, storage.ErrAnalysisAuthority) || len(keys) != 0 {
			t.Fatal("malformed empty-chunk ID admitted", err)
		}
		if len(id) > 36 {
			if allocations := testing.AllocsPerRun(20, func() { _, _ = analysisDatasetDeletionKeys(&bad) }); allocations != 0 {
				t.Fatal("oversized file ID allocated a derived object key", allocations)
			}
		}
	}
}

// The barrier is after the durable revocation write, before its caller can
// admit physical deletion. It does not replace the real SQL cleanup proof.
type analysisDeletionBackendBarrier struct {
	*storage.SQLiteStore
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *analysisDeletionBackendBarrier) SaveAnalysisImport(in storage.AnalysisImport, revision int64) (*storage.AnalysisImport, error) {
	saved, err := s.SQLiteStore.SaveAnalysisImport(in, revision)
	if err == nil && s.armed.Load() {
		s.once.Do(func() { close(s.entered); <-s.release })
	}
	return saved, err
}

func TestAnalysisDeletionPinsBackendBeforeMetadataMutation(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "filesystem")
	originalRoot, substitutedRoot := t.TempDir(), t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", originalRoot)
	var barrier *analysisDeletionBackendBarrier
	server, store, in := nativeUploadCursorFixture(t, func(store *storage.SQLiteStore) storage.Store {
		barrier = &analysisDeletionBackendBarrier{SQLiteStore: store, entered: make(chan struct{}), release: make(chan struct{})}
		return barrier
	})
	if err := ensureAnalysisBackend(server.store); err != nil {
		t.Fatal(err)
	}
	key := analysisObjectKey(in.ProjectID, in.ID, "original-"+in.Files[0].ID+".parquet")
	for _, root := range []string{originalRoot, substitutedRoot} {
		t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
		if _, err := analysisObjectWrite(t.Context(), key, bytes.NewBufferString("untouched")); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", originalRoot)
	barrier.armed.Store(true)
	release := sync.OnceFunc(func() { close(barrier.release) })
	joined := make(chan struct{})
	response := httptest.NewRecorder()
	t.Cleanup(func() {
		release()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Error("retaining exact metadata continuation until actual join")
			<-joined
		}
	})
	go func() {
		defer close(joined)
		server.mux.ServeHTTP(response, httptest.NewRequest("DELETE", "/api/v1/analysis/imports/"+in.ID, nil))
	}()
	select {
	case <-barrier.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("durable metadata revocation barrier did not enter")
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", substitutedRoot)
	release()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("exact deletion request did not return")
	}
	if response.Code != 503 {
		t.Fatal("backend substitution authorized deletion", response.Code, response.Body.String())
	}
	current, err := store.GetAnalysisImport(in.ID)
	if err != nil || current == nil || current.State != "cancelled" {
		t.Fatal("durable revocation lost", current, err)
	}
	for _, root := range []string{originalRoot, substitutedRoot} {
		data, err := os.ReadFile(filepath.Join(root, "_analysis", key))
		if err != nil || string(data) != "untouched" {
			t.Fatal("metadata-stage backend drift touched source", root, err)
		}
	}
}
