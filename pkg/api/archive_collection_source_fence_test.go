package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestArchiveSourceFenceAppliesWithoutRetention(t *testing.T) {
	for _, backend := range []string{archiveBackendFilesystem, archiveBackendS3} {
		t.Run(backend, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", backend)
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "source-fence")
			t.Setenv("APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL", "")
			release, err := lockArchivePublicationSource(context.Background(), "same-run")
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			blocked, err := lockArchivePublicationSource(ctx, "same-run")
			cancel()
			if err == nil {
				blocked()
				t.Fatal("concurrent source writer bypassed file fence")
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			other, err := lockArchivePublicationSource(context.Background(), "another-run")
			if err != nil {
				t.Fatal(err)
			}
			other()
		})
	}
}

func TestArchiveSourceFenceCanceledBeforeFilesystemIO(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	release, err := lockArchivePublicationSource(ctx, "run")
	if err == nil {
		release()
		t.Fatal("canceled operation acquired source fence")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("canceled lock created source root: %v", err)
	}
}

func TestArchiveCollectionStopsOnLeadershipLoss(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL", "")
	leader := newExecutionLeadership(&fakeLeaseStore{dfltOK: true}, "source-fence-leader")
	leader.tick()
	defer leader.relinquish()
	runner := &TestRunner{leadership: leader}
	started := make(chan time.Time, 1)
	client := &deadlineArchiveClient{started: started}
	run := &ManagedTest{ID: "loss-during-collection", remoteClientsByTarget: map[string]generatorClient{"source": client}}
	dir := filepath.Join(root, "remote", run.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(dir, "original")
	if err := os.WriteFile(original, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	backend := newDistributedExecutionBackend([]string{"source"}, time.Second, nil)
	done := make(chan error, 1)
	go func() {
		record, err := backend.stageRemoteArchive(runner, run, "FAILED", "", time.Now(), false)
		if record != nil {
			err = errors.New("lost leader produced a source")
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("source RPC did not start")
	}
	leader.mu.Lock()
	leader.transitionToFollowerLocked()
	leader.mu.Unlock()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("lost leader completed collection")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("collection ignored leadership loss")
	}
	content, err := os.ReadFile(original)
	if err != nil || string(content) != "preserve" {
		t.Fatalf("old source changed: %q %v", content, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	release, err := lockArchivePublicationSource(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestArchiveCollectionReusesExistingPublicationSource(t *testing.T) {
	for _, mode := range []string{"catalog", "outbox"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			runner := NewTestRunner(1, nil, store)
			run, record := createArchiveFinalizationTest(t, store, "already-collected", filepath.Join(root, "remote"))
			if mode == "catalog" {
				err = store.SaveRunArchive(record)
			} else {
				err = store.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: run.ID, ProjectID: run.ProjectID, ArchiveRecord: *record})
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			// No live transport exists. Returning a source proves collection did not
			// attempt to rediscover or overwrite the current publication.
			backend := newDistributedExecutionBackend(nil, time.Second, nil)
			got, err := backend.stageRemoteArchive(runner, run, "FAILED", "stale completion", time.Now(), true)
			if err != nil || got == nil || got.ManifestSHA256 != record.ManifestSHA256 {
				t.Fatalf("reuse: %+v %v", got, err)
			}
			after, err := os.ReadFile(record.ManifestPath)
			if err != nil || string(after) != string(before) {
				t.Fatalf("existing source changed: %v", err)
			}
		})
	}
}

func TestArchiveCollectionSourceGuardRejectsDeletedReplacedAndRetainedRuns(t *testing.T) {
	for _, mode := range []string{"deleted", "replaced", "replaced incarnation", "changed generation", "cleanup", "resolved"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			runner, store, run := collectionIntentFixture(t)
			if err := runner.prepareRunArchiveSources(run, []string{"source"}, 0, bytes.Repeat([]byte{1}, 32)); err != nil {
				t.Fatal(err)
			}
			record := storage.RunArchiveRecord{TestID: run.ID, ArchivePath: filepath.Join(root, "remote", run.ID), ManifestPath: filepath.Join(root, "remote", run.ID, "manifest.json"), CreatedAt: run.CreatedAt, CompletedAt: time.Now().UTC()}
			switch mode {
			case "deleted":
				if err := store.DeleteTest(run.ID); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				run.CreatedAt = run.CreatedAt.Add(time.Second)
			case "replaced incarnation":
				row, err := store.GetTest(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				row.InitialProvenance = map[string]any{"run_incarnation": "replacement"}
				if err := store.SaveTest(row); err != nil {
					t.Fatal(err)
				}
				run.initialArchiveProvenance = &RunArchiveProvenance{RunIncarnation: "prior"}
			case "changed generation":
				stampOrphanRunExecutionGeneration(t, store, run.ID)
			case "cleanup":
				if err := store.SaveArchiveCleanupJob(&storage.ArchiveCleanupJob{TestID: run.ID, ProjectID: run.ProjectID, ArchiveRecord: record}); err != nil {
					t.Fatal(err)
				}
			case "resolved":
				if err := store.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: run.ID, ProjectID: run.ProjectID, ArchiveRecord: record}); err != nil {
					t.Fatal(err)
				}
				if err := store.DeleteArchiveFinalizationJob(run.ID); err != nil {
					t.Fatal(err)
				}
			}
			b := newDistributedExecutionBackend(nil, time.Second, nil)
			got, err := b.stageRemoteArchive(runner, run, "FAILED", "", time.Now(), false)
			if err == nil || got != nil {
				t.Fatalf("unsafe collection accepted: %+v %v", got, err)
			}
			if _, err := os.Stat(filepath.Join(root, "remote", run.ID)); !os.IsNotExist(err) {
				t.Fatalf("rejected collection wrote source: %v", err)
			}
		})
	}
}

type collectionInterleavingClient struct {
	*archiveStreamingFakeClient
	once   sync.Once
	during func()
}

func (c *collectionInterleavingClient) StreamArchiveArtifact(ctx context.Context, req generatorrpc.ArchiveArtifactRequest, w io.Writer, maximum int64) (int64, error) {
	c.once.Do(c.during)
	return c.archiveStreamingFakeClient.StreamArchiveArtifact(ctx, req, w, maximum)
}

func TestArchiveCollectionRechecksPublicationAndDeletionAfterDownload(t *testing.T) {
	for _, mode := range []string{"catalog", "outbox", "deleted", "replaced", "replaced incarnation", "changed generation"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "test.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			runner := NewTestRunner(1, nil, store)
			run, record := createArchiveFinalizationTest(t, store, "collection-race", filepath.Join(root, "remote"))
			if mode == "replaced incarnation" {
				row, err := store.GetTest(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				row.InitialProvenance = map[string]any{"run_incarnation": "prior"}
				if err := store.SaveTest(row); err != nil {
					t.Fatal(err)
				}
				run.initialArchiveProvenance = &RunArchiveProvenance{RunIncarnation: "prior"}
			}
			before, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var mutationErr error
			client := &collectionInterleavingClient{archiveStreamingFakeClient: coverageArchiveClient(t, run.ID), during: func() {
				switch mode {
				case "changed generation":
					stampOrphanRunExecutionGeneration(t, store, run.ID)
				case "catalog":
					mutationErr = store.SaveRunArchive(record)
				case "outbox":
					mutationErr = store.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: run.ID, ProjectID: run.ProjectID, ArchiveRecord: *record})
				case "deleted", "replaced", "replaced incarnation":
					mutationErr = store.DeleteTest(run.ID)
					if mutationErr == nil && mode == "replaced" {
						mutationErr = store.SaveTest(&storage.TestRecord{ID: run.ID, ProjectID: run.ProjectID, Name: "replacement", Status: "RUNNING", StartTime: run.CreatedAt.Add(time.Second)})
					} else if mutationErr == nil && mode == "replaced incarnation" {
						mutationErr = store.SaveTest(&storage.TestRecord{ID: run.ID, ProjectID: run.ProjectID, Name: "replacement", Status: "RUNNING", StartTime: run.CreatedAt, InitialProvenance: map[string]any{"run_incarnation": "replacement"}})
					}
				}
			}}
			run.remoteClientsByTarget = map[string]generatorClient{"source": client}
			backend := newDistributedExecutionBackend([]string{"source"}, time.Second, nil)
			got, err := backend.stageRemoteArchive(runner, run, "FAILED", "late collector", time.Now(), true)
			if mutationErr != nil {
				t.Fatal(mutationErr)
			}
			if mode == "deleted" || mode == "replaced" || mode == "replaced incarnation" || mode == "changed generation" {
				if err == nil || got != nil {
					t.Fatalf("stale collection committed: %+v %v", got, err)
				}
			} else if err != nil || got == nil || got.ManifestSHA256 != record.ManifestSHA256 {
				t.Fatalf("current publication not reused: %+v %v", got, err)
			}
			after, readErr := os.ReadFile(record.ManifestPath)
			if readErr != nil || !bytes.Equal(before, after) {
				t.Fatalf("existing source overwritten: %v", readErr)
			}
		})
	}
}
