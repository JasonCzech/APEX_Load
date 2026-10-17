package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/storage"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// All non-archive RPCs deliberately remain nil: any accidental execution or
// inventory call in the recovery worker fails the test instead of doing work.
type collectionRetryArchiveClient struct {
	generatorClient
	source        archiveArtifactClient
	reads, closes *atomic.Int32
}

type archiveFailureRepairProbeStore struct {
	SQLiteStore *storage.SQLiteStore
	calls       int
	fail        bool
}

func (s *archiveFailureRepairProbeStore) ReconcileExhaustedRunArchiveFailures(_, _ string, limit int) (int64, error) {
	s.calls++
	if limit != 100 {
		return 0, errors.New("unbounded archive failure repair")
	}
	if s.fail {
		return 0, errors.New("injected repair outage")
	}
	return 0, nil
}

func TestArchiveCollectionRetryRepairsHistoricalExhaustionBeforeListing(t *testing.T) {
	tr, store, _, backend := collectionCacheFixture(t, "a")
	tr.setExecutionBackend(backend)
	probe := &archiveFailureRepairProbeStore{SQLiteStore: store}
	tr.testStore = sqliteOverlay(probe)
	if err := tr.retryRunArchiveCollections(context.Background(), time.Now()); err != nil || probe.calls != 1 {
		t.Fatalf("repair pass = %d, %v", probe.calls, err)
	}
	probe.fail = true
	if err := tr.retryRunArchiveCollections(context.Background(), time.Now()); err == nil || probe.calls != 2 {
		t.Fatalf("repair outage = %d, %v", probe.calls, err)
	}
}

func (c *collectionRetryArchiveClient) GetArchiveArtifact(ctx context.Context, req generatorrpc.ArchiveArtifactRequest) ([]byte, error) {
	c.reads.Add(1)
	return c.source.GetArchiveArtifact(ctx, req)
}
func (c *collectionRetryArchiveClient) StreamArchiveArtifact(ctx context.Context, req generatorrpc.ArchiveArtifactRequest, destination io.Writer, max int64) (int64, error) {
	c.reads.Add(1)
	return c.source.(streamingArchiveGeneratorClient).StreamArchiveArtifact(ctx, req, destination, max)
}
func (c *collectionRetryArchiveClient) Close() error { c.closes.Add(1); return nil }

func TestArchiveCollectionRetrySurvivesControllerRestart(t *testing.T) {
	tr, store, run, _ := collectionCacheFixture(t, "a", "b")
	sources := run.remoteClientsByTarget
	now := time.Now().UTC()
	if _, err := store.UpdateTestStatusIfActive(run.ID, "FAILED", &now, "generator unavailable"); err != nil {
		t.Fatal(err)
	}
	var available atomic.Bool
	var reads, closes, dials atomic.Int32
	backend := newDistributedExecutionBackend([]string{"unrelated"}, time.Second, func(ctx context.Context, target string) (generatorClient, error) {
		dials.Add(1)
		if target == "b" && !available.Load() {
			return nil, errors.New("temporarily absent")
		}
		source := sources[target]
		if source == nil {
			t.Errorf("dialed unrecorded target %q", target)
			return nil, errors.New("unrecorded")
		}
		return &collectionRetryArchiveClient{source: source, reads: &reads, closes: &closes}, nil
	})
	tr.setExecutionBackend(backend)
	if err := tr.retryRunArchiveCollections(context.Background(), now); err == nil {
		t.Fatal("missing required peer accepted")
	}
	intent, err := store.GetRunArchiveCollection(run.ID)
	if err != nil || intent.Attempts != 1 || len(intent.CachedSources) != 1 {
		t.Fatalf("lost retry progress: %+v %v", intent, err)
	}
	if closes.Load() != 1 {
		t.Fatal("owned transport not closed")
	}
	available.Store(true)
	restarted := NewTestRunner(1, nil, nil)
	restarted.testStore, restarted.archiveStore = store, store
	restarted.setExecutionBackend(backend)
	if err := restarted.retryRunArchiveCollections(context.Background(), now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	job, err := store.GetArchiveFinalizationJob(run.ID)
	if err != nil || job == nil {
		t.Fatal("recovered source not queued", err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(&job.ArchiveRecord)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Status != "FAILED" || !manifest.CompletedAt.Equal(now) || manifest.SourceCoverage.Mode != "best_effort" || manifest.CSVExportIncomplete == "" {
		t.Fatalf("recovery upgraded/lost terminal evidence: %+v", manifest)
	}
	for _, source := range manifest.SourceCoverage.Sources {
		if !source.Collected {
			t.Fatal("available source omitted")
		}
	}
	if reads.Load() != 8 || closes.Load() != 3 || dials.Load() != 4 {
		t.Fatalf("reads=%d closes=%d dials=%d", reads.Load(), closes.Load(), dials.Load())
	}
	row, err := store.GetTest(run.ID)
	if err != nil || row.Status != "FAILED" || row.Error != "generator unavailable" {
		t.Fatal("recovery rewrote outcome", err)
	}
	intent, err = store.GetRunArchiveCollection(run.ID)
	if err != nil || !intent.Resolved || intent.Attempts != 2 {
		t.Fatal("retry not resolved", err)
	}
	if err := restarted.retryRunArchiveCollections(context.Background(), now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if dials.Load() != 4 {
		t.Fatal("resolved collection was dialed")
	}
}

func TestArchiveCollectionRetryExhaustsDownloadsThenUsesCacheOnly(t *testing.T) {
	for _, hasSource := range []bool{true, false} {
		t.Run(map[bool]string{true: "partial", false: "no sources"}[hasSource], func(t *testing.T) {
			tr, store, run, _ := collectionCacheFixture(t, "a", "lost")
			source := run.remoteClientsByTarget["a"]
			now := time.Now().UTC()
			if _, err := store.UpdateTestStatusIfActive(run.ID, "FAILED", &now, "failed"); err != nil {
				t.Fatal(err)
			}
			var dials, reads, closes atomic.Int32
			tr.setExecutionBackend(newDistributedExecutionBackend(nil, time.Second, func(ctx context.Context, target string) (generatorClient, error) {
				dials.Add(1)
				if target == "a" && hasSource {
					return &collectionRetryArchiveClient{source: source, reads: &reads, closes: &closes}, nil
				}
				return nil, errors.New("absent")
			}))
			for i := 0; i < 3; i++ {
				if err := tr.retryRunArchiveCollections(context.Background(), now.Add(time.Duration(i)*2*time.Minute)); err == nil {
					t.Fatal("missing source accepted")
				}
			}
			if dials.Load() != 6 {
				t.Fatalf("download attempts=%d", dials.Load())
			}
			if err := tr.retryRunArchiveCollections(context.Background(), now.Add(6*time.Minute)); err != nil {
				t.Fatal(err)
			}
			if dials.Load() != 6 {
				t.Fatal("final cache-only recovery dialed generators")
			}
			intent, err := store.GetRunArchiveCollection(run.ID)
			if err != nil || intent.Attempts != 3 {
				t.Fatal("attempt budget changed", err)
			}
			job, err := store.GetArchiveFinalizationJob(run.ID)
			if hasSource {
				if err != nil || job == nil || !intent.Resolved || intent.Exhausted {
					t.Fatal("partial source not queued", err)
				}
				manifest, err := loadRunArchiveManifestFromRecord(&job.ArchiveRecord)
				if err != nil {
					t.Fatal(err)
				}
				if manifest.Status != "FAILED" || len(manifest.SourceCoverage.Sources) != 2 || !manifest.SourceCoverage.Sources[0].Collected || manifest.SourceCoverage.Sources[1].Collected {
					t.Fatal("partial coverage not explicit")
				}
			} else if !errors.Is(err, sql.ErrNoRows) || !intent.Exhausted || intent.Resolved {
				t.Fatal("no-source recovery not exhausted", err)
			}
		})
	}
}

func TestArchiveCollectionRetryTerminalAgePreventsNetwork(t *testing.T) {
	tr, store, run, _ := collectionCacheFixture(t, "a")
	ended := time.Now().UTC()
	now := ended.Add(7 * time.Hour)
	if _, err := store.UpdateTestStatusIfActive(run.ID, "FAILED", &ended, ""); err != nil {
		t.Fatal(err)
	}
	tr.setExecutionBackend(newDistributedExecutionBackend(nil, time.Second, func(context.Context, string) (generatorClient, error) {
		t.Fatal("expired recovery dialed generator")
		return nil, nil
	}))
	if err := tr.retryRunArchiveCollections(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	intent, err := store.GetRunArchiveCollection(run.ID)
	if err != nil || !intent.Exhausted || intent.Attempts != 1 {
		t.Fatal("expired collection not exhausted", err)
	}
}

func TestArchiveCollectionRetryBusySourceDoesNotSpendAttempt(t *testing.T) {
	tr, store, run, _ := collectionCacheFixture(t, "a")
	now := time.Now().UTC()
	if _, err := store.UpdateTestStatusIfActive(run.ID, "FAILED", &now, ""); err != nil {
		t.Fatal(err)
	}
	tr.setExecutionBackend(newDistributedExecutionBackend(nil, time.Second, func(context.Context, string) (generatorClient, error) {
		t.Fatal("busy recovery dialed")
		return nil, nil
	}))
	release, err := lockArchivePublicationSource(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := tr.retryRunArchiveCollections(ctx, now); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	intent, err := store.GetRunArchiveCollection(run.ID)
	if err != nil || intent.Attempts != 0 {
		t.Fatal("busy fence spent attempt", err)
	}
}

func TestArchiveCollectionRetrySkipsStaleAndOwnedJobs(t *testing.T) {
	for _, mode := range []string{"active", "deleted", "new incarnation", "stale token", "stale attempt", "too soon", "pending", "catalog", "exhausted"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, run, collector := collectionCacheFixture(t, "a")
			now := time.Now().UTC()
			if mode != "active" {
				if _, err := store.UpdateTestStatusIfActive(run.ID, "FAILED", &now, ""); err != nil {
					t.Fatal(err)
				}
			}
			listed, err := store.GetRunArchiveCollection(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "deleted":
				if err := store.DeleteTest(run.ID); err != nil {
					t.Fatal(err)
				}
			case "new incarnation":
				row, err := store.GetTest(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				row.StartTime = row.StartTime.Add(time.Second)
				if err := store.SaveTest(row); err != nil {
					t.Fatal(err)
				}
			case "stale token":
				listed.IntentToken = "00000000-0000-4000-8000-000000000000"
			case "stale attempt", "too soon", "exhausted":
				if ok, err := store.ClaimRunArchiveCollectionAttempt(run.ID, listed.IntentToken, 0, "", ""); err != nil || !ok {
					t.Fatal(err)
				}
				if mode == "too soon" {
					listed, err = store.GetRunArchiveCollection(run.ID)
					if err != nil {
						t.Fatal(err)
					}
				}
				if mode == "exhausted" {
					if ok, err := store.ExhaustRunArchiveCollection(run.ID, listed.IntentToken, 1, "", ""); err != nil || !ok {
						t.Fatal(err)
					}
				}
			case "pending", "catalog":
				record, err := collector.stageRemoteArchive(tr, run, "FAILED", "", now, true)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "pending" {
					err = store.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: run.ID, ProjectID: run.ProjectID, ArchiveRecord: *record})
				} else {
					err = store.SaveRunArchive(record)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			backend := newDistributedExecutionBackend(nil, time.Second, func(context.Context, string) (generatorClient, error) {
				t.Errorf("ineligible %s dialed", mode)
				return nil, errors.New("unexpected")
			})
			if err := tr.retryRunArchiveCollection(context.Background(), backend, listed, now); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestArchiveCollectionRetryRecoversOrPreservesPreexistingSource(t *testing.T) {
	for _, mode := range []string{"valid", "corrupt", "different outcome"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, run, collector := collectionCacheFixture(t, "a")
			ended := time.Now().UTC()
			record, err := collector.stageRemoteArchive(tr, run, "COMPLETE", "", ended, true)
			if err != nil {
				t.Fatal(err)
			}
			status := "COMPLETE"
			if mode == "different outcome" {
				status = "FAILED"
			}
			if _, err := store.UpdateTestStatusIfActive(run.ID, status, &ended, ""); err != nil {
				t.Fatal(err)
			}
			if mode == "corrupt" {
				if err := os.WriteFile(record.ManifestPath, []byte("corrupt evidence"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			intent, err := store.GetRunArchiveCollection(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				if ok, err := store.ClaimRunArchiveCollectionAttempt(run.ID, intent.IntentToken, i, "", ""); err != nil || !ok {
					t.Fatal(err)
				}
			}
			tr.setExecutionBackend(newDistributedExecutionBackend(nil, time.Second, func(context.Context, string) (generatorClient, error) {
				t.Error("existing source dialed")
				return nil, errors.New("unexpected")
			}))
			err = tr.retryRunArchiveCollections(context.Background(), ended.Add(2*time.Minute))
			if (err != nil) != (mode != "valid") {
				t.Fatalf("%s: %v", mode, err)
			}
			after, err := os.ReadFile(record.ManifestPath)
			if err != nil || string(after) != string(before) {
				t.Fatal("preexisting source overwritten", err)
			}
			intent, err = store.GetRunArchiveCollection(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "valid" {
				if !intent.Resolved || intent.Exhausted {
					t.Fatal("valid orphan not queued")
				}
			} else if !intent.Exhausted || intent.Resolved {
				t.Fatal("invalid source not preserved as exhausted")
			}
		})
	}
}

func TestArchiveCollectionRetryHonorsDatabaseExecutionLease(t *testing.T) {
	for _, mode := range []string{"stale SQL lease", "follower"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, run, _ := collectionCacheFixture(t, "a")
			now := time.Now().UTC()
			if _, err := store.UpdateTestStatusIfActive(run.ID, "FAILED", &now, ""); err != nil {
				t.Fatal(err)
			}
			leader := newExecutionLeadership(store, "collection-retry")
			leader.tick()
			defer leader.relinquish()
			tr.leadership = leader
			tr.durableExecutionFence = true
			_, owner, _, err := tr.generatorExecutionClaim()
			if err != nil {
				t.Fatal(err)
			}
			if mode == "follower" {
				leader.relinquish()
			} else {
				if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
			}
			tr.setExecutionBackend(newDistributedExecutionBackend(nil, time.Second, func(context.Context, string) (generatorClient, error) {
				t.Error("unowned recovery dialed")
				return nil, errors.New("unexpected")
			}))
			err = tr.retryRunArchiveCollections(context.Background(), now)
			if mode == "follower" && !errors.Is(err, errNotExecutionLeader) {
				t.Fatal(err)
			}
			if mode != "follower" && err != nil {
				t.Fatal(err)
			}
			intent, err := store.GetRunArchiveCollection(run.ID)
			if err != nil || intent.Attempts != 0 {
				t.Fatal("unowned recovery spent budget", err)
			}
		})
	}
}

type collectionRetryBlockingClient struct {
	generatorClient
	entered chan struct{}
	closed  atomic.Int32
}

func (c *collectionRetryBlockingClient) StreamArchiveArtifact(ctx context.Context, _ generatorrpc.ArchiveArtifactRequest, _ io.Writer, _ int64) (int64, error) {
	close(c.entered)
	<-ctx.Done()
	return 0, ctx.Err()
}
func (c *collectionRetryBlockingClient) Close() error { c.closed.Add(1); return nil }
func TestArchiveCollectionRetryCancellationClosesOwnedTransports(t *testing.T) {
	tr, store, run, _ := collectionCacheFixture(t, "a")
	now := time.Now().UTC()
	if _, err := store.UpdateTestStatusIfActive(run.ID, "FAILED", &now, ""); err != nil {
		t.Fatal(err)
	}
	client := &collectionRetryBlockingClient{entered: make(chan struct{})}
	tr.setExecutionBackend(newDistributedExecutionBackend(nil, time.Second, func(context.Context, string) (generatorClient, error) { return client, nil }))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- tr.retryRunArchiveCollections(ctx, now) }()
	select {
	case <-client.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("download did not cancel")
	}
	if client.closed.Load() != 1 {
		t.Fatal("owned transport leaked")
	}
	intent, err := store.GetRunArchiveCollection(run.ID)
	if err != nil || intent.Attempts != 1 || intent.Resolved || intent.Exhausted {
		t.Fatal("cancellation committed final decision", err)
	}
}

func TestArchiveCollectionRetryFencesRunChangedBeforePublication(t *testing.T) {
	_, store, run, _ := collectionCacheFixture(t, "a")
	now := time.Now().UTC()
	if _, err := store.UpdateTestStatusIfActive(run.ID, "FAILED", &now, ""); err != nil {
		t.Fatal(err)
	}
	changed := &changedOrphanRunStore{SQLiteStore: store}
	tr := NewTestRunner(1, nil, nil)
	tr.testStore, tr.archiveStore = sqliteOverlay(changed), sqliteOverlay(changed)
	var reads, closes atomic.Int32
	tr.setExecutionBackend(newDistributedExecutionBackend(nil, time.Second, func(context.Context, string) (generatorClient, error) {
		return &collectionRetryArchiveClient{source: run.remoteClientsByTarget["a"], reads: &reads, closes: &closes}, nil
	}))
	if err := tr.retryRunArchiveCollections(context.Background(), now); err == nil {
		t.Fatal("changed run accepted publication")
	}
	if _, err := store.GetArchiveFinalizationJob(run.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("changed run left outbox", err)
	}
	if _, err := os.Stat(filepath.Join(resolveArchiveRoot(), "remote", run.ID, runArchiveManifestFileName)); err != nil {
		t.Fatal("fenced source was not preserved", err)
	}
}

func TestArchiveCollectionRetryDialAdmissionAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{}, remoteArchiveFetchConcurrency)
	var active, peak, closed, reads atomic.Int32
	backend := newDistributedExecutionBackend(nil, time.Minute, func(ctx context.Context, _ string) (generatorClient, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		entered <- struct{}{}
		<-ctx.Done()
		return &collectionRetryArchiveClient{reads: &reads, closes: &closed}, ctx.Err()
	})
	targets := make([]string, 50)
	for i := range targets {
		targets[i] = fmt.Sprint(i)
	}
	done := make(chan map[string]generatorClient, 1)
	go func() { done <- dialCollectionTargets(ctx, backend, targets) }()
	for i := 0; i < remoteArchiveFetchConcurrency; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("dial admission did not fill")
		}
	}
	cancel()
	select {
	case clients := <-done:
		if len(clients) != 0 {
			t.Fatal("failed dial retained transports")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dial waiters ignored cancellation")
	}
	if peak.Load() != remoteArchiveFetchConcurrency || closed.Load() != remoteArchiveFetchConcurrency || active.Load() != 0 {
		t.Fatalf("peak=%d closed=%d active=%d", peak.Load(), closed.Load(), active.Load())
	}
}
