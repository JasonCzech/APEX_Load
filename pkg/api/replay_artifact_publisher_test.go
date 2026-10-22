package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

func replayPublisherFixture(t *testing.T) (*replayArtifactPublisher, *storage.SQLiteStore, *recordbridge.BrowserReplaySession, []byte) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "replay.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	backend, _, har, _ := replayBackendFixture(t, false)
	ctx := context.Background()
	acquired, err := store.TryAcquireReplaySessionLease(ctx, "publisher", "owner", storage.DefaultProjectID, time.Hour, 10, 10)
	if err != nil || acquired != storage.ReplaySessionLeaseAcquired {
		t.Fatal(acquired, err)
	}
	retained, err := store.RetainReplaySessionLease(ctx, "publisher", "owner", storage.DefaultProjectID, time.Hour)
	if err != nil || !retained {
		t.Fatal(retained, err)
	}
	p := &replayArtifactPublisher{store: store, backend: backend, session: "publisher", project: storage.DefaultProjectID, owner: "owner", ttl: time.Hour, limits: storage.ReplayArtifactLimits{GlobalReceipts: 10, ProjectReceipts: 10, GlobalBytes: 1 << 20, ProjectBytes: 1 << 20}}
	now := time.Now()
	session := &recordbridge.BrowserReplaySession{ID: p.session, ProjectID: p.project, Status: recordbridge.BrowserReplayCompleted, CompletedAt: &now, FinalReport: &engine.ReplayReport{Success: true, ReplaySessionID: p.session}}
	return p, store, session, har
}

func TestReplayArtifactPublisherRoundTripAndNoOverwrite(t *testing.T) {
	p, store, session, har := replayPublisherFixture(t)
	ctx := context.Background()
	if err := p.Publish(ctx, session, strings.NewReader(string(har)), int64(len(har))); err != nil {
		t.Fatal(err)
	}
	receipt, err := store.GetReplayArtifactReceipt(ctx, p.session, p.project)
	if err != nil || receipt == nil {
		t.Fatal(receipt, err)
	}
	if !receipt.WriterJoined || receipt.State != "ready" {
		t.Fatal(receipt)
	}
	read, err := p.backend.Read(ctx, *receipt)
	if err != nil {
		t.Fatal(err)
	}
	defer read.Close()
	got, err := io.ReadAll(read.HAR)
	if err != nil || string(got) != string(har) {
		t.Fatal("native bytes changed", err)
	}
	var envelope struct {
		Session recordbridge.BrowserReplaySession `json:"session"`
	}
	if err = json.NewDecoder(read.Report).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Session.ID != p.session || !envelope.Session.FinalReport.Success {
		t.Fatal("terminal report changed")
	}
	if err = p.Publish(ctx, session, strings.NewReader(string(har)), int64(len(har))); !errors.Is(err, storage.ErrReplayArtifactFence) {
		t.Fatal("publication retried existing writer", err)
	}
	again, err := store.GetReplayArtifactReceipt(ctx, p.session, p.project)
	if err != nil || again.Spec.Generation != receipt.Spec.Generation {
		t.Fatal("generation changed", err)
	}
}

func TestReplayArtifactPublisherRefusesBeforeReservation(t *testing.T) {
	for _, scenario := range []string{"session", "project", "active", "report", "report-identity", "report-size", "completion", "zero-completion", "length", "overflow", "canceled", "owner", "capacity"} {
		t.Run(scenario, func(t *testing.T) {
			p, store, session, har := replayPublisherFixture(t)
			size := int64(len(har))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "session":
				session.ID = "foreign"
			case "project":
				session.ProjectID = "foreign"
			case "active":
				session.Status = recordbridge.BrowserReplayRunning
			case "report":
				session.FinalReport = nil
			case "report-identity":
				session.FinalReport.ReplaySessionID = "foreign"
			case "report-size":
				session.FinalReport.Error = strings.Repeat("x", 16<<20)
			case "completion":
				session.CompletedAt = nil
			case "zero-completion":
				session.CompletedAt = &time.Time{}
			case "length":
				size++
			case "overflow":
				size--
			case "canceled":
				cancel()
			case "owner":
				p.owner = "foreign"
			case "capacity":
				p.limits.GlobalBytes = 1
				p.limits.ProjectBytes = 1
			}
			if err := p.Publish(ctx, session, strings.NewReader(string(har)), size); err == nil {
				t.Fatal("invalid publication accepted")
			}
			receipt, err := store.GetReplayArtifactReceipt(context.Background(), p.session, p.project)
			if !errors.Is(err, sql.ErrNoRows) || receipt != nil {
				t.Fatal("refusal published", receipt, err)
			}
			if _, err := os.Stat(filepath.Join(p.backend.backend.LocalRoot, archiveReplayDirectory, "v1")); !os.IsNotExist(err) {
				t.Fatal("refusal wrote bytes", err)
			}
		})
	}
}

// These ports inject errors around real receipt transactions, preserving the
// actual admission, ownership and cleanup implementation underneath.
type replayPublisherStoreHook struct {
	replayArtifactPublicationStore
	afterBegin func() error
	beforeJoin func() error
}

func (s replayPublisherStoreHook) BeginReplayArtifactReceipt(ctx context.Context, spec storage.ReplayArtifactSpec, owner string, ttl time.Duration, limits storage.ReplayArtifactLimits) error {
	if err := s.replayArtifactPublicationStore.BeginReplayArtifactReceipt(ctx, spec, owner, ttl, limits); err != nil {
		return err
	}
	if s.afterBegin != nil {
		return s.afterBegin()
	}
	return nil
}
func (s replayPublisherStoreHook) JoinReplayArtifactWriter(ctx context.Context, session, project, generation, owner string) (bool, error) {
	if s.beforeJoin != nil {
		if err := s.beforeJoin(); err != nil {
			return false, err
		}
	}
	return s.replayArtifactPublicationStore.JoinReplayArtifactWriter(ctx, session, project, generation, owner)
}
func TestReplayArtifactPublisherLateOwnerCannotPublish(t *testing.T) {
	p, store, session, har := replayPublisherFixture(t)
	ctx := context.Background()
	p.store = replayPublisherStoreHook{replayArtifactPublicationStore: store, beforeJoin: func() error {
		ok, err := store.ReleaseRetainedReplaySessionLease(ctx, p.session, p.owner, p.project)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("lease did not release")
		}
		return nil
	}}
	if err := p.Publish(ctx, session, strings.NewReader(string(har)), int64(len(har))); !errors.Is(err, storage.ErrReplayArtifactFence) {
		t.Fatal(err)
	}
	cleanup, err := store.ClaimReplayArtifactCleanup(ctx, 10)
	if err != nil || len(cleanup) != 1 {
		t.Fatal(cleanup, err)
	}
	if !cleanup[0].WriterJoined {
		t.Fatal("verified writer lost completion proof")
	}
	if err = p.backend.Cleanup(ctx, cleanup[0]); err != nil {
		t.Fatal(err)
	}
	removed, err := store.FinishReplayArtifactCleanup(ctx, p.session, p.project, cleanup[0].Spec.Generation)
	if err != nil || !removed {
		t.Fatal(removed, err)
	}
}
func TestReplayArtifactPublisherCanceledStageRetainsUnknownWriter(t *testing.T) {
	p, store, session, har := replayPublisherFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.store = replayPublisherStoreHook{replayArtifactPublicationStore: store, afterBegin: func() error { cancel(); return nil }}
	if err := p.Publish(ctx, session, strings.NewReader(string(har)), int64(len(har))); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cleanCtx := context.Background()
	if _, err := store.ReleaseRetainedReplaySessionLease(cleanCtx, p.session, p.owner, p.project); err != nil {
		t.Fatal(err)
	}
	cleanup, err := store.ClaimReplayArtifactCleanup(cleanCtx, 10)
	if err != nil || len(cleanup) != 0 {
		t.Fatal("unknown writer was cleanable", cleanup, err)
	}
	acquired, err := store.TryAcquireReplaySessionLease(cleanCtx, p.session, "successor", p.project, time.Hour, 10, 10)
	if err != nil || acquired == storage.ReplaySessionLeaseAcquired {
		t.Fatal("reservation lost namespace", acquired, err)
	}
}

func TestReplayArtifactPublisherJoinFailurePreservesReservation(t *testing.T) {
	p, store, session, har := replayPublisherFixture(t)
	ctx := context.Background()
	failure := errors.New("join acknowledgement unavailable")
	p.store = replayPublisherStoreHook{replayArtifactPublicationStore: store, beforeJoin: func() error { return failure }}
	if err := p.Publish(ctx, session, strings.NewReader(string(har)), int64(len(har))); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(p.backend.backend.LocalRoot, archiveReplayDirectory, "v1"))
	if err != nil || len(entries) != 1 {
		t.Fatal("verified physical generation missing", entries, err)
	}
	if _, err = store.ReleaseRetainedReplaySessionLease(ctx, p.session, p.owner, p.project); err != nil {
		t.Fatal(err)
	}
	cleanup, err := store.ClaimReplayArtifactCleanup(ctx, 10)
	if err != nil || len(cleanup) != 0 {
		t.Fatal("unknown join was reclaimed", cleanup, err)
	}
	acquired, err := store.TryAcquireReplaySessionLease(ctx, p.session, "successor", p.project, time.Hour, 10, 10)
	if err != nil || acquired == storage.ReplaySessionLeaseAcquired {
		t.Fatal("unknown join released namespace", acquired, err)
	}
}

func TestReplayArtifactPublisherCancellationAfterStageRecordsJoin(t *testing.T) {
	p, store, session, har := replayPublisherFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.store = replayPublisherStoreHook{replayArtifactPublicationStore: store, beforeJoin: func() error { cancel(); return nil }}
	if err := p.Publish(ctx, session, strings.NewReader(string(har)), int64(len(har))); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	cleanCtx := context.Background()
	if _, err := store.ReleaseRetainedReplaySessionLease(cleanCtx, p.session, p.owner, p.project); err != nil {
		t.Fatal(err)
	}
	cleanup, err := store.ClaimReplayArtifactCleanup(cleanCtx, 10)
	if err != nil || len(cleanup) != 1 || !cleanup[0].WriterJoined {
		t.Fatal("verified join lost after cancellation", cleanup, err)
	}
	if err = p.backend.Cleanup(cleanCtx, cleanup[0]); err != nil {
		t.Fatal(err)
	}
	removed, err := store.FinishReplayArtifactCleanup(cleanCtx, p.session, p.project, cleanup[0].Spec.Generation)
	if err != nil || !removed {
		t.Fatal(removed, err)
	}
}
