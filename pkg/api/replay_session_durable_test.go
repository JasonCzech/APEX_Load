package api

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestNonHAReplaySessionsShareDurableOwnership(t *testing.T) {
	firstStore, secondStore := openReplaySessionHAStores(t)
	first := NewServer(ServerConfig{Store: firstStore})
	second := NewServer(ServerConfig{Store: secondStore})
	closeBatchTestServer(t, first)
	closeBatchTestServer(t, second)
	id := uuid.NewString()
	_, release, signal, err := first.reserveReplaySessionID(id, storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, _, _, err := second.reserveReplaySessionID(id, "other-project"); !errors.Is(err, errReplaySessionIDConflict) {
		t.Fatal("cross-controller owner not fenced", err)
	}
	if err := signal.retainForTerminal(time.Minute); err != nil {
		t.Fatal(err)
	}
	defer signal.releaseAll()
	release()
	if _, _, _, err := second.reserveReplaySessionID(id, storage.DefaultProjectID); !errors.Is(err, errReplaySessionIDConflict) {
		t.Fatal("terminal namespace not retained", err)
	}
	signal.releaseRetainedID()
	_, nextRelease, _, err := second.reserveReplaySessionID(id, storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	defer nextRelease()
	release()
	if _, _, _, err := first.reserveReplaySessionID(id, storage.DefaultProjectID); !errors.Is(err, errReplaySessionIDConflict) {
		t.Fatal("stale callback removed successor", err)
	}
}

func TestNonHAReplayReceiptSurvivesProcessLeasePruning(t *testing.T) {
	for _, published := range []bool{false, true} {
		name := "unjoined staging"
		if published {
			name = "published"
		}
		t.Run(name, func(t *testing.T) {
			first, second := openReplaySessionHAStores(t)
			ctx := context.Background()
			id := uuid.NewString()
			owner := "original-owner"
			project := "receipt-project"
			if err := first.SaveProject(&storage.Project{ID: project, Name: project, CreatedAt: time.Now(), UpdatedAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			if got, err := first.TryAcquireReplaySessionLease(ctx, id, owner, project, time.Minute, 10, 10); err != nil || got != storage.ReplaySessionLeaseAcquired {
				t.Fatal(got, err)
			}
			if ok, err := first.RetainReplaySessionLease(ctx, id, owner, project, time.Minute); err != nil || !ok {
				t.Fatal(ok, err)
			}
			spec := storage.ReplayArtifactSpec{Version: 1, SessionID: id, ProjectID: project, Generation: strings.Repeat("d", 64), SourceSHA256: strings.Repeat("a", 64), HARSHA256: strings.Repeat("b", 64), HARBytes: 10, ReportSHA256: strings.Repeat("c", 64), ReportBytes: 10}
			limits := storage.ReplayArtifactLimits{GlobalReceipts: 10, ProjectReceipts: 10, GlobalBytes: 1000, ProjectBytes: 1000}
			if err := first.BeginReplayArtifactReceipt(ctx, spec, owner, time.Minute, limits); err != nil {
				t.Fatal(err)
			}
			if published {
				if ok, err := first.JoinReplayArtifactWriter(ctx, id, project, spec.Generation, owner); err != nil || !ok {
					t.Fatal(ok, err)
				}
				if ok, err := first.PublishReplayArtifactReceipt(ctx, id, project, spec.Generation, owner); err != nil || !ok {
					t.Fatal(ok, err)
				}
			}
			if ok, err := first.ReleaseRetainedReplaySessionLease(ctx, id, owner, project); err != nil || !ok {
				t.Fatal(ok, err)
			}
			server := NewServer(ServerConfig{Store: second})
			closeBatchTestServer(t, server)
			if _, _, _, err := server.reserveReplaySessionID(id, project); !errors.Is(err, errReplaySessionIDConflict) {
				t.Fatal("receipt namespace bypassed after restart", err)
			}
			server.replaySessionMu.Lock()
			pending := len(server.replaySessionReservations)
			server.replaySessionMu.Unlock()
			if pending != 0 {
				t.Fatal("failed claim leaked local reservation", pending)
			}
			if !published {
				if got, err := second.ClaimReplayArtifactCleanup(ctx, 10); err != nil || len(got) != 0 {
					t.Fatal("unjoined writer became reclaimable", got, err)
				}
				if ok, err := first.JoinReplayArtifactWriter(ctx, id, project, spec.Generation, owner); err != nil || !ok {
					t.Fatal(ok, err)
				}
			}
			if err := first.DeleteProject(project); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := server.reserveReplaySessionID(id, storage.DefaultProjectID); !errors.Is(err, errReplaySessionIDConflict) {
				t.Fatal("cleanup tombstone bypassed", err)
			}
			got, err := second.ClaimReplayArtifactCleanup(ctx, 10)
			if err != nil || len(got) != 1 {
				t.Fatal(got, err)
			}
			// Simulate the adapter's confirmed exact-generation physical deletion.
			if ok, err := second.FinishReplayArtifactCleanup(ctx, id, project, spec.Generation); err != nil || !ok {
				t.Fatal(ok, err)
			}
			_, release, _, err := server.reserveReplaySessionID(id, storage.DefaultProjectID)
			if err != nil {
				t.Fatal(err)
			}
			release()
		})
	}
}

type unavailableNonHAReplayStore struct{ *storage.SQLiteStore }

func (s *unavailableNonHAReplayStore) TryAcquireReplaySessionLease(context.Context, string, string, string, time.Duration, int, int) (storage.ReplaySessionLeaseAcquireResult, error) {
	return "", errors.New("injected durable admission failure")
}

func TestNonHAReplayDurableAdmissionFailureDoesNotFallBack(t *testing.T) {
	first, _ := openReplaySessionHAStores(t)
	server := NewServer(ServerConfig{Store: &unavailableNonHAReplayStore{first}})
	closeBatchTestServer(t, server)
	if _, _, _, err := server.reserveReplaySessionID(uuid.NewString(), storage.DefaultProjectID); !errors.Is(err, errReplaySessionLeaseUnavailable) {
		t.Fatal("durable failure did not fail closed", err)
	}
	server.replaySessionMu.Lock()
	defer server.replaySessionMu.Unlock()
	if len(server.replaySessionReservations) != 0 {
		t.Fatal("failed durable claim left local ownership")
	}
}

func TestNonHAReplayRenewalLossTerminatesActiveBrowserReplay(t *testing.T) {
	testReplaySessionRenewalLossTerminatesActiveBrowserReplay(t, false)
}

type canceledNonHAReplayAcquireStore struct {
	*storage.SQLiteStore
	cancel context.CancelFunc
}

func (s *canceledNonHAReplayAcquireStore) TryAcquireReplaySessionLease(ctx context.Context, id, owner, project string, ttl time.Duration, global, perProject int) (storage.ReplaySessionLeaseAcquireResult, error) {
	result, err := s.SQLiteStore.TryAcquireReplaySessionLease(ctx, id, owner, project, ttl, global, perProject)
	s.cancel()
	return result, err
}
func TestNonHAReplayCancellationAfterAcquireReleasesDurableOwner(t *testing.T) {
	first, second := openReplaySessionHAStores(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := NewServer(ServerConfig{Store: &canceledNonHAReplayAcquireStore{first, cancel}})
	closeBatchTestServer(t, server)
	id := uuid.NewString()
	if _, _, _, err := server.reserveReplaySessionIDContext(ctx, id, storage.DefaultProjectID); !errors.Is(err, context.Canceled) {
		t.Fatal("non-HA cancellation was misreported", err)
	}
	server.replaySessionMu.Lock()
	pending := len(server.replaySessionReservations)
	server.replaySessionMu.Unlock()
	if pending != 0 {
		t.Fatal("canceled admission leaked local ownership", pending)
	}
	got, err := second.TryAcquireReplaySessionLease(context.Background(), id, "successor", storage.DefaultProjectID, time.Minute, 10, 10)
	if err != nil || got != storage.ReplaySessionLeaseAcquired {
		t.Fatal("canceled admission leaked durable ownership", got, err)
	}
	if _, err := second.ReleaseReplaySessionLease(context.Background(), id, "successor", storage.DefaultProjectID); err != nil {
		t.Fatal(err)
	}
}
