package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
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
	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/storage"
)

type uploadBlockedBody struct {
	entered, release chan struct{}
	once             sync.Once
	closed           chan struct{}
}

func (b *uploadBlockedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return 0, io.EOF
}
func (b *uploadBlockedBody) Close() error {
	select {
	case <-b.closed:
	default:
		close(b.closed)
	}
	return nil
}
func waitUploadOwnerReleased(t *testing.T, o *analysisUploadPhysicalOwner) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := analysisUploadOwners.Load(o.id); !ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("exact upload admission did not release")
}
func TestAnalysisUploadCancellationReturnsWhileActualBodyReadRemainsOwned(t *testing.T) {
	o, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("admission")
	}
	b := &uploadBlockedBody{entered: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
	release := sync.OnceFunc(func() { close(b.release) })
	t.Cleanup(func() { release(); o.endRequest(); waitUploadOwnerReleased(t, o) })
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { _, err := readAnalysisUploadBody(ctx, o, b); done <- err }()
	select {
	case <-b.entered:
	case <-time.After(time.Second):
		t.Fatal("read did not enter")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP caller waited for blocked physical read")
	}
	o.endRequest()
	if o.physicalJoined() {
		t.Fatal("body Close invented Read join")
	}
	if _, ok := analysisUploadOwners.Load(o.id); !ok {
		t.Fatal("blocked actual body lost exact parent")
	}
	release()
	waitUploadOwnerReleased(t, o)
}

type uploadBlockedCloser struct {
	entered, release chan struct{}
	calls            int
	mu               sync.Mutex
}

func (c *uploadBlockedCloser) Close() error {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	close(c.entered)
	<-c.release
	return nil
}
func TestAnalysisUploadLateActualCloseAloneStartsExactPublicationContinuation(t *testing.T) {
	o, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("admission")
	}
	c := &uploadBlockedCloser{entered: make(chan struct{}), release: make(chan struct{})}
	unblock := sync.OnceFunc(func() { close(c.release) })
	t.Cleanup(func() { unblock(); o.endRequest(); waitUploadOwnerReleased(t, o) })
	end, err := o.beginOperation()
	if err != nil {
		t.Fatal(err)
	}
	o.TrackOwner(c)
	o.setIntent(&storage.AnalysisUploadWriter{ID: uuid.NewString()})
	published := make(chan struct{})
	o.afterPhysicalJoin(func() { close(published); o.finish(nil, nil) })
	err = o.CloseOwner(c)
	if !errors.Is(err, errAnalysisUploadPhysical) {
		t.Fatal("blocked close did not return bounded unknown", err)
	}
	end()
	o.endRequest()
	select {
	case <-published:
		t.Fatal("timed out close authorized publication")
	default:
	}
	o.mu.Lock()
	exact := o.descriptors[c].closer
	o.mu.Unlock()
	if exact != c {
		t.Fatal("failed physical owner replaced")
	}
	if _, ok := analysisUploadOwners.Load(o.id); !ok {
		t.Fatal("unjoined descriptor released admission")
	}
	unblock()
	select {
	case <-published:
	case <-time.After(time.Second):
		t.Fatal("actual close did not resume exact parent")
	}
	waitUploadOwnerReleased(t, o)
	c.mu.Lock()
	calls := c.calls
	c.mu.Unlock()
	if calls != 1 {
		t.Fatal("concurrent owner Close duplicated", calls)
	}
}
func TestAnalysisUploadRegisteredBeforeBodyAllocationAndGlobalSlotLimit(t *testing.T) {
	before := len(analysisUploadWriterSlots)
	var owners []*analysisUploadPhysicalOwner
	t.Cleanup(func() {
		for _, o := range owners {
			o.endRequest()
		}
	})
	for len(analysisUploadWriterSlots) < cap(analysisUploadWriterSlots) {
		o, ok := acquireAnalysisUploadOwner()
		if !ok {
			t.Fatal("early admission refusal")
		}
		owners = append(owners, o)
		if _, ok := analysisUploadOwners.Load(o.id); !ok {
			t.Fatal("admission lacked exact parent registry")
		}
	}
	if o, ok := acquireAnalysisUploadOwner(); ok || o != nil {
		t.Fatal("unbounded upload body admission")
	}
	for _, o := range owners {
		o.endRequest()
	}
	owners = nil
	if len(analysisUploadWriterSlots) != before {
		t.Fatal("released requests retained body quota")
	}
}
func TestAnalysisUploadPipelinePublishesDurableCursorAfterExactObjectClosure(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	server, store := newTimeslotServer(t)
	imports := store.(storage.AnalysisImportStore)
	writers := store.(storage.AnalysisUploadWriterStore)
	in, err := imports.SaveAnalysisImport(storage.AnalysisImport{ID: uuid.NewString(), ProjectID: "proj-a", Name: "upload", State: "uploading", UploadWriterVersion: storage.AnalysisUploadWriterVersion, Files: []storage.AnalysisImportFile{{ID: uuid.NewString(), RelativePath: "data.parquet", Size: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	owner, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("admission")
	}
	defer owner.endRequest()
	ctx := context.WithValue(t.Context(), analysisUploadOwnerKey{}, owner)
	data := []byte("PAR1dataPAR1")
	digest := strings.Repeat("a", 64)
	// The mismatched actual digest must seal an abandoned exact plan, without
	// exposing a cursor or allowing the same writer to become accepted later.
	req := httptest.NewRequest("PUT", "/", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	server.writeAnalysisUploadChunk(response, req, writers, *in, in.Files[0], 0, data, digest)
	if response.Code != 409 {
		t.Fatal("invalid digest accepted", response.Code, response.Body.String())
	}
	current, err := imports.GetAnalysisImport(in.ID)
	if err != nil || current.Files[0].Uploaded != 0 || current.Revision != in.Revision {
		t.Fatal("failed bytes published cursor", current, err)
	}
	receipt, _ := owner.outcome()
	if receipt == nil || receipt.State != "abandoned" || !receipt.PhysicallyClosed {
		t.Fatal("failed immutable object lost exact seal", receipt)
	}
	if !owner.physicalJoined() {
		t.Fatal("metadata published before physical sink/root closure")
	}
	var got bytes.Buffer
	actual, err := analysisObjectRead(t.Context(), receipt.ObjectKey, &got, int64(len(data)))
	if err != nil || actual == digest || !bytes.Equal(got.Bytes(), data) {
		t.Fatal("source bytes changed", err)
	}
	next, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("retry admission")
	}
	defer next.endRequest()
	nextCtx := context.WithValue(t.Context(), analysisUploadOwnerKey{}, next)
	sum := sha256.Sum256(data)
	approved := hex.EncodeToString(sum[:])
	retried := httptest.NewRecorder()
	server.writeAnalysisUploadChunk(retried, httptest.NewRequest("PUT", "/", nil).WithContext(nextCtx), writers, *in, in.Files[0], 0, data, approved)
	if retried.Code != 200 {
		t.Fatal("approved bytes failed durable upload", retried.Code, retried.Body.String())
	}
	current, err = imports.GetAnalysisImport(in.ID)
	if err != nil || current.Files[0].Uploaded != 12 || current.Revision != in.Revision+1 || len(current.Files[0].Chunks) != 1 || current.Files[0].Chunks[0].SHA256 != approved {
		t.Fatal("accepted seal/cursor diverged", current, err)
	}
	accepted, _ := next.outcome()
	if accepted == nil || !accepted.PhysicallyClosed || accepted.State != "accepted" || accepted.ObjectKey == receipt.ObjectKey {
		t.Fatal("retry did not use distinct exact physical plan", accepted)
	}

}

// This store-boundary fault represents an ambiguous Begin followed by a naked
// missing-row response. The real store still owns the committed journal; only
// its later scope-fenced resolution can release that exact owner.
type uploadUnfencedMissingStore struct {
	SQLiteStore     *storage.SQLiteStore
	calls           atomic.Int32
	entered, permit chan struct{}
	enteredOnce     sync.Once
}

func (s *uploadUnfencedMissingStore) BeginAnalysisUploadWriter(ctx context.Context, req storage.AnalysisUploadRequest) (*storage.AnalysisUploadWriter, error) {
	w, err := s.SQLiteStore.BeginAnalysisUploadWriter(ctx, req)
	if err != nil {
		return w, err
	}
	return w, storage.ErrAnalysisPublicationUnknown
}
func (s *uploadUnfencedMissingStore) ResolveAnalysisUploadWriter(ctx context.Context, project, importID, id string) (*storage.AnalysisUploadWriter, error) {
	if s.calls.Add(1) == 1 {
		return nil, sql.ErrNoRows
	}
	s.enteredOnce.Do(func() { close(s.entered) })
	select {
	case <-s.permit:
		return s.SQLiteStore.ResolveAnalysisUploadWriter(ctx, project, importID, id)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestAnalysisUploadUnfencedMissingRowCannotReleaseAmbiguousBeginOwner(t *testing.T) {
	server, store := newTimeslotServer(t)
	imports := store.(storage.AnalysisImportStore)
	in, err := imports.SaveAnalysisImport(storage.AnalysisImport{ID: uuid.NewString(), ProjectID: "proj-a", Name: "ambiguous upload", State: "uploading", UploadWriterVersion: storage.AnalysisUploadWriterVersion, Files: []storage.AnalysisImportFile{{ID: uuid.NewString(), RelativePath: "data.parquet", Size: 12}}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	writers := &uploadUnfencedMissingStore{SQLiteStore: store.(*storage.SQLiteStore), entered: make(chan struct{}), permit: make(chan struct{})}
	shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
	if err := server.Shutdown(shutdown); err != nil {
		stop()
		t.Fatal(err)
	}
	stop()
	server = NewServer(ServerConfig{Store: sqliteOverlay(writers), MaxConcurrent: 2})
	t.Cleanup(func() {
		ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	permit := sync.OnceFunc(func() { close(writers.permit) })
	owner, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("admission")
	}
	t.Cleanup(func() { permit(); owner.endRequest(); waitUploadOwnerReleased(t, owner) })
	end, err := owner.beginOperation()
	if err != nil {
		t.Fatal(err)
	}
	server.runAnalysisUploadChunk(t.Context(), sqliteOverlay(writers), owner, *in, in.Files[0], 0, []byte("PAR1dataPAR1"), strings.Repeat("a", 64))
	end()
	owner.endRequest()
	select {
	case <-writers.entered:
	case <-time.After(time.Second):
		t.Fatal("ambiguous Begin did not enter owned reconciliation")
	}
	select {
	case <-owner.done:
		t.Fatal("naked missing journal row was treated as rollback proof")
	default:
	}
	if _, ok := analysisUploadOwners.Load(owner.id); !ok {
		t.Fatal("unfenced missing row released exact parent admission")
	}
	permit()
	select {
	case <-owner.done:
	case <-time.After(time.Second):
		t.Fatal("fenced committed receipt did not reconcile")
	}
	receipt, err := owner.outcome()
	if err != nil || receipt == nil || receipt.State != "abandoned" || !receipt.PhysicallyClosed {
		t.Fatal("unknown Begin did not retain and seal exact plan", receipt, err)
	}
	current, err := imports.GetAnalysisImport(in.ID)
	if err != nil || current.Files[0].Uploaded != 0 || current.Revision != in.Revision {
		t.Fatal("unwritten ambiguous Begin published a cursor", current, err)
	}
}

func TestAnalysisUploadKnownPreIntentFailurePermanentlyStopsNewOperation(t *testing.T) {
	owner, ok := acquireAnalysisUploadOwner()
	if !ok {
		t.Fatal("admission")
	}
	t.Cleanup(func() { owner.endRequest(); waitUploadOwnerReleased(t, owner) })
	end, err := owner.beginOperation()
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	owner.finish(nil, storage.ErrAnalysisAuthority)
	if next, err := owner.beginOperation(); err == nil {
		next()
		t.Fatal("terminal pre-intent refusal reopened body/artifact admission")
	}
	// Terminal metadata does not pretend the previously entered operation joined.
	if owner.physicalJoined() {
		t.Fatal("terminal refusal manufactured physical quiescence")
	}
	end()
	owner.endRequest()
	waitUploadOwnerReleased(t, owner)
}
