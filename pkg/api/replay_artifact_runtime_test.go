package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/recordbridge"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestReplayArtifactRuntimeRecoversNativePair(t *testing.T) {
	p, store, session, raw := replayPublisherFixture(t)
	ctx := context.Background()
	if err := p.Publish(ctx, session, bytes.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatal(err)
	}
	// Runtime is a new controller-facing adapter with no in-memory replay manager.
	runtime := newReplayArtifactRuntime(store, p.backend.backend)
	got, err := runtime.Session(ctx, p.session, p.project)
	if err != nil || got.ID != p.session || !got.WorkerSettled() {
		t.Fatal(got, err)
	}
	got, har, err := runtime.HAR(ctx, p.session, p.project)
	if err != nil {
		t.Fatal(err)
	}
	received, err := io.ReadAll(har.Reader())
	if err != nil || !bytes.Equal(received, raw) || har.Length() != int64(len(raw)) || har.Fidelity != "native" || got.ID != p.session {
		t.Fatal("recovered evidence differs", err)
	}
	if len(runtime.reads) != 1 {
		t.Fatal("download staging lost its admission")
	}
	har.Close()
	har.Close()
	if len(runtime.reads) != 0 {
		t.Fatal("download admission leaked")
	}
	for _, project := range []string{"", "foreign"} {
		if _, err := runtime.Session(ctx, p.session, project); !errors.Is(err, recordbridge.ErrRetainedReplayNotFound) {
			t.Fatal("unscoped read allowed", err)
		}
		if _, _, err := runtime.HAR(ctx, p.session, project); !errors.Is(err, recordbridge.ErrRetainedReplayNotFound) {
			t.Fatal("unscoped download allowed", err)
		}
	}
	receipt, err := store.GetReplayArtifactReceipt(ctx, p.session, p.project)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(filepath.Join(p.backend.backend.LocalRoot, p.backend.key(receipt.Spec, "replay.har"))); err != nil {
		t.Fatal(err)
	}
	if _, err = runtime.Session(ctx, p.session, p.project); err != nil {
		t.Fatal("missing HAR prevented bounded status", err)
	}
	if _, _, err = runtime.HAR(ctx, p.session, p.project); !errors.Is(err, recordbridge.ErrRetainedReplayUnavailable) {
		t.Fatal("missing HAR synthesized", err)
	}
	if len(runtime.reads) != 0 {
		t.Fatal("failure leaked admission")
	}
}

func TestReplayArtifactRuntimeAdmissionAndChangedBackend(t *testing.T) {
	p, store, session, raw := replayPublisherFixture(t)
	ctx := context.Background()
	if err := p.Publish(ctx, session, bytes.NewReader(raw), int64(len(raw))); err != nil {
		t.Fatal(err)
	}
	runtime := newReplayArtifactRuntime(store, p.backend.backend)
	releases := []func(){}
	for range 4 {
		release, err := takeReplayArtifactSlot(ctx, runtime.reads)
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, err := runtime.Session(ctx, p.session, p.project); !errors.Is(err, recordbridge.ErrRetainedReplayBusy) {
		t.Fatal(err)
	}
	if _, _, err := runtime.HAR(ctx, p.session, p.project); !errors.Is(err, recordbridge.ErrRetainedReplayBusy) {
		t.Fatal(err)
	}
	for _, release := range releases {
		release()
	}
	runtime.backend.LocalRoot = t.TempDir()
	if _, err := runtime.Session(ctx, p.session, p.project); !errors.Is(err, recordbridge.ErrRetainedReplayUnavailable) {
		t.Fatal("adopted replacement volume", err)
	}
	if len(runtime.reads) != 0 {
		t.Fatal("admission leaked")
	}
}

type replayArtifactRecheckStore struct {
	replayArtifactRuntimeStore
	calls int
	mode  string
}

func (s *replayArtifactRecheckStore) GetReplayArtifactReceipt(ctx context.Context, id, project string) (*storage.ReplayArtifactReceipt, error) {
	s.calls++
	receipt, err := s.replayArtifactRuntimeStore.GetReplayArtifactReceipt(ctx, id, project)
	if err != nil || s.calls != 2 {
		return receipt, err
	}
	switch s.mode {
	case "deleted":
		return nil, sql.ErrNoRows
	case "generation":
		receipt.Spec.Generation = strings.Repeat("f", 64)
	case "cleanup":
		receipt.State = "cleanup"
	}
	return receipt, nil
}
func TestReplayArtifactRuntimeRechecksAfterMaterialization(t *testing.T) {
	for _, download := range []bool{false, true} {
		for _, mode := range []string{"deleted", "generation", "cleanup"} {
			t.Run(mode+map[bool]string{false: "/status", true: "/download"}[download], func(t *testing.T) {
				p, store, session, raw := replayPublisherFixture(t)
				ctx := context.Background()
				if err := p.Publish(ctx, session, bytes.NewReader(raw), int64(len(raw))); err != nil {
					t.Fatal(err)
				}
				runtime := newReplayArtifactRuntime(&replayArtifactRecheckStore{replayArtifactRuntimeStore: store, mode: mode}, p.backend.backend)
				var err error
				if download {
					_, har, e := runtime.HAR(ctx, p.session, p.project)
					err = e
					har.Close()
				} else {
					_, err = runtime.Session(ctx, p.session, p.project)
				}
				if !errors.Is(err, recordbridge.ErrRetainedReplayUnavailable) {
					t.Fatal("lost receipt returned evidence", err)
				}
				if len(runtime.reads) != 0 {
					t.Fatal("recheck failure leaked admission")
				}
			})
		}
	}
}

func stageReplayRuntimeCleanupFixture(t *testing.T, p *replayArtifactPublisher, store *storage.SQLiteStore, session *recordbridge.BrowserReplaySession, har []byte, joined bool) storage.ReplayArtifactSpec {
	t.Helper()
	ctx := context.Background()
	report, err := json.Marshal(struct {
		Session *recordbridge.BrowserReplaySession `json:"session"`
	}{session})
	if err != nil {
		t.Fatal(err)
	}
	spec := storage.ReplayArtifactSpec{Version: 1, SessionID: p.session, ProjectID: p.project, Generation: sha256BytesHex([]byte(p.session)), SourceSHA256: p.backend.sourceSHA256(), HARSHA256: sha256BytesHex(har), HARBytes: int64(len(har)), ReportSHA256: sha256BytesHex(report), ReportBytes: int64(len(report))}
	if err = store.BeginReplayArtifactReceipt(ctx, spec, p.owner, time.Hour, p.limits); err != nil {
		t.Fatal(err)
	}
	if err = p.backend.Stage(ctx, spec, bytes.NewReader(har), bytes.NewReader(report)); err != nil {
		t.Fatal(err)
	}
	if joined {
		ok, err := store.JoinReplayArtifactWriter(ctx, p.session, p.project, spec.Generation, p.owner)
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
	}
	if ok, err := store.ReleaseRetainedReplaySessionLease(ctx, p.session, p.owner, p.project); err != nil || !ok {
		t.Fatal(ok, err)
	}
	return spec
}

func TestReplayArtifactRuntimeCleanupRequiresSourceAndJoin(t *testing.T) {
	for _, mode := range []string{"joined", "unjoined", "foreign-volume", "foreign-file"} {
		t.Run(mode, func(t *testing.T) {
			p, store, session, har := replayPublisherFixture(t)
			ctx := context.Background()
			spec := stageReplayRuntimeCleanupFixture(t, p, store, session, har, mode != "unjoined")
			runtime := newReplayArtifactRuntime(store, p.backend.backend)
			foreign := ""
			if mode == "foreign-volume" {
				runtime.backend.LocalRoot = t.TempDir()
			}
			if mode == "foreign-file" {
				foreign = filepath.Join(p.backend.backend.LocalRoot, filepath.Dir(p.backend.key(spec, "replay.har")), "unknown")
				if err := os.WriteFile(foreign, []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := runtime.Sweep(ctx)
			if (err != nil) != (mode == "foreign-file") {
				t.Fatal("unexpected cleanup result", err)
			}
			scope, err := store.LookupReplayArtifactScope(ctx, p.session)
			if mode == "joined" {
				if !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("joined cleanup retained receipt", scope, err)
				}
				return
			}
			if err != nil || scope == nil {
				t.Fatal("unsafe cleanup released receipt", scope, err)
			}
			if mode == "unjoined" {
				if scope.WriterJoined {
					t.Fatal("invented writer completion")
				}
				return
			}
			if mode == "foreign-file" {
				got, err := os.ReadFile(foreign)
				if err != nil || string(got) != "foreign" {
					t.Fatal("foreign file deleted", err)
				}
				if err = os.Remove(foreign); err != nil {
					t.Fatal(err)
				}
			}
			runtime.backend = p.backend.backend
			if err = runtime.Sweep(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err = store.LookupReplayArtifactScope(ctx, p.session); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("retry failed to finish exact cleanup", err)
			}
		})
	}
}

func TestBoundReplayArtifactPublisherAdmission(t *testing.T) {
	p, store, session, har := replayPublisherFixture(t)
	overlay := sqliteOverlay(&struct{ SQLiteStore *storage.SQLiteStore }{store})
	runtime := newReplayArtifactRuntime(overlay, p.backend.backend)
	if runtime == nil {
		t.Fatal("generated overlay lost replay receipt capabilities")
	}
	bound := &boundReplayArtifactPublisher{runtime: runtime, id: p.session, project: p.project, owner: p.owner}
	ctx := context.Background()
	first, err := takeReplayArtifactSlot(ctx, runtime.writes)
	if err != nil {
		t.Fatal(err)
	}
	second, err := takeReplayArtifactSlot(ctx, runtime.writes)
	if err != nil {
		t.Fatal(err)
	}
	if err = bound.Publish(ctx, session, bytes.NewReader(har), int64(len(har))); !errors.Is(err, recordbridge.ErrRetainedReplayBusy) {
		t.Fatal("unbounded publication admitted", err)
	}
	if _, err = store.LookupReplayArtifactScope(ctx, p.session); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("busy publication reserved bytes", err)
	}
	first()
	second()
	if err = bound.Publish(ctx, session, bytes.NewReader(har), int64(len(har))); err != nil {
		t.Fatal(err)
	}
	if len(runtime.writes) != 0 {
		t.Fatal("publication admission leaked")
	}
	if _, err = runtime.Session(ctx, p.session, p.project); err != nil {
		t.Fatal("published replay not recovered", err)
	}
	if err = bound.Publish(ctx, session, bytes.NewReader(har), int64(len(har))); err == nil {
		t.Fatal("repeat publication overwrote generation")
	}
	if len(runtime.writes) != 0 {
		t.Fatal("failed publication admission leaked")
	}
}

func TestReplayArtifactRuntimeCleanupTraversesForeignPage(t *testing.T) {
	p, store, session, har := replayPublisherFixture(t)
	foreign, _, _, _ := replayBackendFixture(t, false)
	ctx := context.Background()
	for i := range 33 {
		item := *p
		item.session = fmt.Sprintf("page-%02d", i)
		item.limits.GlobalReceipts = 100
		item.limits.ProjectReceipts = 100
		if i < 32 {
			item.backend = foreign
		}
		if acquired, err := store.TryAcquireReplaySessionLease(ctx, item.session, item.owner, item.project, time.Hour, 100, 100); err != nil || acquired != storage.ReplaySessionLeaseAcquired {
			t.Fatal(acquired, err)
		}
		if ok, err := store.RetainReplaySessionLease(ctx, item.session, item.owner, item.project, time.Hour); err != nil || !ok {
			t.Fatal(ok, err)
		}
		// Construct a detached terminal value rather than copying its private mutex.
		terminal := &recordbridge.BrowserReplaySession{ID: item.session, ProjectID: item.project, Status: session.Status, CompletedAt: session.CompletedAt, FinalReport: &engine.ReplayReport{ReplaySessionID: item.session}}
		stageReplayRuntimeCleanupFixture(t, &item, store, terminal, har, true)
	}
	runtime := newReplayArtifactRuntime(store, p.backend.backend)
	if err := runtime.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if runtime.after != "page-31" {
		t.Fatalf("foreign page did not advance: %q", runtime.after)
	}
	if _, err := store.LookupReplayArtifactScope(ctx, "page-32"); err != nil {
		t.Fatal("deleted outside bounded page", err)
	}
	if err := runtime.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if runtime.after != "" {
		t.Fatal("sweep did not wrap for retries")
	}
	if _, err := store.LookupReplayArtifactScope(ctx, "page-32"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("foreign page starved current volume", err)
	}
	if _, err := store.LookupReplayArtifactScope(ctx, "page-00"); err != nil {
		t.Fatal("foreign receipt released", err)
	}
}

func TestReplayArtifactRuntimeUnsupportedStoreAndCanceledAdmission(t *testing.T) {
	if newReplayArtifactRuntime(struct{}{}, archiveBackendRuntime{}) != nil {
		t.Fatal("unsupported store enabled durability")
	}
	slots := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if release, err := takeReplayArtifactSlot(ctx, slots); release != nil || !errors.Is(err, context.Canceled) || len(slots) != 0 {
		t.Fatal("canceled admission took a slot", err)
	}
	p, store, session, har := replayPublisherFixture(t)
	runtime := newReplayArtifactRuntime(store, p.backend.backend)
	if _, err := runtime.Session(ctx, p.session, p.project); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := runtime.HAR(ctx, p.session, p.project); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	bound := &boundReplayArtifactPublisher{runtime: runtime, id: p.session, project: p.project, owner: p.owner}
	if err := bound.Publish(ctx, session, bytes.NewReader(har), int64(len(har))); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(runtime.reads) != 0 || len(runtime.writes) != 0 {
		t.Fatal("canceled work leaked admission")
	}
}

func TestReplayArtifactRuntimeCleanupThroughStoreOverlay(t *testing.T) {
	p, store, session, har := replayPublisherFixture(t)
	stageReplayRuntimeCleanupFixture(t, p, store, session, har, true)
	overlay := sqliteOverlay(&struct{ SQLiteStore *storage.SQLiteStore }{store})
	runtime := newReplayArtifactRuntime(overlay, p.backend.backend)
	if runtime == nil {
		t.Fatal("generated overlay lost receipt capabilities")
	}
	if _, err := runtime.Session(context.Background(), p.session, p.project); !errors.Is(err, recordbridge.ErrRetainedReplayUnavailable) {
		t.Fatal("unpublished receipt did not report unavailable", err)
	}
	if err := runtime.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LookupReplayArtifactScope(context.Background(), p.session); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("overlay cleanup failed", err)
	}
}
