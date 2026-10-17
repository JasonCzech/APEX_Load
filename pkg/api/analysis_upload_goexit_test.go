package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

type uploadGoexitBody struct{ reads, closes atomic.Int32 }

func (b *uploadGoexitBody) Read([]byte) (int, error) {
	b.reads.Add(1)
	runtime.Goexit()
	return 0, nil // unreachable; required by the Read signature
}
func (b *uploadGoexitBody) Close() error { b.closes.Add(1); return nil }

func TestAnalysisUploadBodyGoexitRetainsUnknownPhysicalOwnership(t *testing.T) {
	before := len(analysisUploadWriterSlots)
	owner, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("admission")
	}
	// The controlled callback is known to allocate no physical resource. Only
	// this fixture retirement can reclaim its test quota; no production flag,
	// metadata, Close outcome or durable writer proof is changed.
	t.Cleanup(func() { retireUnknownUploadFixture(t, owner) })
	body := &uploadGoexitBody{}
	data, err := readAnalysisUploadBody(t.Context(), owner, body)
	if data != nil || !errors.Is(err, errAnalysisUploadPhysical) {
		t.Fatal("Goexit became a successful empty body", data, err)
	}
	owner.endRequest()
	if body.reads.Load() != 1 || body.closes.Load() != 1 || owner.physicalJoined() || owner.sealPhysical() {
		t.Fatal("known Close erased entered Read's unknown return")
	}
	if got, ok := analysisUploadOwners.Load(owner.id); !ok || got != owner || len(analysisUploadWriterSlots) != before+1 {
		t.Fatal("Goexit lost the exact registered admission")
	}
	if end, err := owner.beginOperation(); err == nil {
		end()
		t.Fatal("unknown physical operation admitted a replacement writer")
	}
	if receipt, err := owner.outcome(); receipt != nil || !errors.Is(err, errAnalysisUploadPhysical) {
		t.Fatal("Goexit produced successful cursor authority", receipt, err)
	}
}

// This boundary returns neither success nor error after the actual durable
// Begin commits. Its exact record exists; the caller must not invent proof of
// the interrupted operation's physical allocations from a nil recover value.
type uploadGoexitBeginStore struct {
	SQLiteStore *storage.SQLiteStore
	mu          sync.Mutex
	intent      *storage.AnalysisUploadWriter
	completes   atomic.Int32
}

func (s *uploadGoexitBeginStore) BeginAnalysisUploadWriter(ctx context.Context, request storage.AnalysisUploadRequest) (*storage.AnalysisUploadWriter, error) {
	intent, err := s.SQLiteStore.BeginAnalysisUploadWriter(ctx, request)
	if err != nil {
		return intent, err
	}
	s.mu.Lock()
	copy := *intent
	s.intent = &copy
	s.mu.Unlock()
	runtime.Goexit()
	return nil, nil // unreachable
}
func (s *uploadGoexitBeginStore) CompleteAnalysisUploadWriter(ctx context.Context, project, id string, accepted bool) (*storage.AnalysisUploadWriter, error) {
	s.completes.Add(1)
	return s.SQLiteStore.CompleteAnalysisUploadWriter(ctx, project, id, accepted)
}

func TestAnalysisUploadBackgroundGoexitRetainsSlotAndNeverPublishesCursor(t *testing.T) {
	initial, base := newTimeslotServer(t)
	shutdownUploadFixtureServer(t, initial)
	fault := &uploadGoexitBeginStore{SQLiteStore: base.(*storage.SQLiteStore)}
	server := NewServer(ServerConfig{Store: sqliteOverlay(fault), MaxConcurrent: 2})
	t.Cleanup(func() { shutdownUploadFixtureServer(t, server) })
	in, err := fault.SQLiteStore.SaveAnalysisImport(storage.AnalysisImport{ID: uuid.NewString(), ProjectID: "proj-a", Name: "Goexit writer", State: "uploading", UploadWriterVersion: storage.AnalysisUploadWriterVersion, Files: []storage.AnalysisImportFile{{ID: uuid.NewString(), RelativePath: "data.parquet", Size: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	before := len(analysisUploadWriterSlots)
	owner, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("admission")
	}
	// The controlled Begin callback owns only the actual SQLite transaction,
	// which returned before Goexit; it creates no hidden descriptor/worker. Its
	// pending journal is deliberately not completed or given physical proof.
	t.Cleanup(func() { retireUnknownUploadFixture(t, owner) })
	ctx := context.WithValue(t.Context(), analysisUploadOwnerKey{}, owner)
	data := []byte("PAR1dataPAR1")
	sum := sha256.Sum256(data)
	response := httptest.NewRecorder()
	server.writeAnalysisUploadChunk(response, httptest.NewRequest("PUT", "/", nil).WithContext(ctx), sqliteOverlay(fault), *in, in.Files[0], 0, data, hex.EncodeToString(sum[:]))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("abnormally returned writer published a cursor", response.Code, response.Body.String())
	}
	owner.endRequest()
	server.reconcileAnalysisUploadWritersOnce(t.Context())
	if fault.completes.Load() != 0 || owner.physicalJoined() || owner.sealPhysical() {
		t.Fatal("Goexit authorized durable physical closure/completion")
	}
	if got, ok := analysisUploadOwners.Load(owner.id); !ok || got != owner || len(analysisUploadWriterSlots) != before+1 {
		t.Fatal("interrupted writer lost its exact charged parent")
	}
	current, err := fault.SQLiteStore.GetAnalysisImport(in.ID)
	if err != nil || current == nil || current.Revision != in.Revision || len(current.Files) != 1 || current.Files[0].Uploaded != 0 || len(current.Files[0].Chunks) != 0 {
		t.Fatal("Goexit changed durable cursor", current, err)
	}
	fault.mu.Lock()
	intent := fault.intent
	fault.mu.Unlock()
	if intent == nil {
		t.Fatal("controlled Begin did not commit its actual intent")
	}
	retained, err := fault.SQLiteStore.ResolveAnalysisUploadWriter(t.Context(), in.ProjectID, in.ID, intent.ID)
	if err != nil || !analysisSameUploadIntent(intent, retained) || retained.State != "pending" || retained.PhysicallyClosed {
		t.Fatal("Goexit manufactured a completed/abandoned writer receipt", retained, err)
	}
	if receipt, err := owner.outcome(); receipt != nil || !errors.Is(err, errAnalysisUploadPhysical) {
		t.Fatal("interrupted writer exposed successful cursor authority", receipt, err)
	}
}
