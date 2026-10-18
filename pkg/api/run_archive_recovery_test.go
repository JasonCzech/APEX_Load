package api

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRunArchiveRecoveryProjectsDurableStateWithoutReadingSources(t *testing.T) {
	for _, mode := range []string{"pending", "cache", "expired", "exhausted", "resolved", "catalog unavailable", "publication", "untrusted publication", "available", "active", "changed start", "changed end", "changed status", "other project"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, run, backend := collectionCacheFixture(t, "private-generator.example:9091", "missing-generator.example:9091")
			delete(run.remoteClientsByTarget, "missing-generator.example:9091")
			if _, err := backend.stageRemoteArchive(tr, run, "FAILED", "", time.Now(), true); err == nil {
				t.Fatal("missing source accepted")
			}
			intent, err := store.GetRunArchiveCollection(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(intent.CachedSources) != 1 {
				t.Fatal("fixture missing receipt")
			}
			// Historical receipt counts survive physical loss; projection must not scan files.
			if err := os.RemoveAll(filepath.Join(resolveArchiveRoot(), "_collection")); err != nil {
				t.Fatal(err)
			}
			end := time.Now().UTC()
			if mode == "expired" {
				end = end.Add(-archiveCollectionTerminalDeadline - time.Minute)
			}
			if mode != "active" {
				if _, err := store.UpdateTestStatusIfActive(run.ID, "FAILED", &end, ""); err != nil {
					t.Fatal(err)
				}
				run.Status = "FAILED"
				run.EndedAt = &end
			}
			want := "collection_pending"
			switch mode {
			case "cache", "exhausted":
				for i := 0; i < archiveCollectionNetworkAttempts; i++ {
					if ok, err := store.ClaimRunArchiveCollectionAttempt(run.ID, intent.IntentToken, i, "", ""); err != nil || !ok {
						t.Fatal("claim", ok, err)
					}
				}
				want = "cache_recovery_pending"
				if mode == "exhausted" {
					if ok, err := store.ExhaustRunArchiveCollection(run.ID, intent.IntentToken, archiveCollectionNetworkAttempts, "", ""); err != nil || !ok {
						t.Fatal("exhaust", ok, err)
					}
					want = "exhausted"
				}
			case "expired":
				want = "cache_recovery_pending"
			case "resolved", "catalog unavailable", "publication", "untrusted publication":
				manifest := RunArchiveManifest{TestID: run.ID, CreatedAt: run.CreatedAt, CompletedAt: end, Status: run.Status}
				data, err := json.Marshal(manifest)
				if err != nil {
					t.Fatal(err)
				}
				record := storage.RunArchiveRecord{TestID: run.ID, CreatedAt: run.CreatedAt, CompletedAt: end, ManifestSHA256: sha256BytesHex(data), ArchivePath: "/private/source", ManifestPath: "/private/source/manifest.json"}
				job := &storage.ArchiveFinalizationJob{TestID: run.ID, ProjectID: run.ProjectID, ArchiveRecord: record}
				if err := store.SaveArchiveFinalizationJob(job); err != nil {
					t.Fatal(err)
				}
				want = "publication_pending"
				if mode == "publication" {
					if ok, err := store.PrepareArchiveFinalizationManifestIfToken(run.ID, job.IntentToken, &record, string(data)); err != nil || !ok {
						t.Fatal("prepare", ok, err)
					}
				}
				if mode == "untrusted publication" {
					want = ""
				}
				if mode == "resolved" || mode == "catalog unavailable" {
					if err := store.DeleteArchiveFinalizationJob(run.ID); err != nil {
						t.Fatal(err)
					}
					want = "unavailable"
					if mode == "catalog unavailable" {
						if err := store.SaveRunArchive(&record); err != nil {
							t.Fatal(err)
						}
						want = ""
					}
				}
			case "available":
				run.Archive = &storage.RunArchiveRecord{TestID: run.ID}
				want = "available"
			case "active":
				want = ""
			case "changed start":
				run.CreatedAt = run.CreatedAt.Add(time.Second)
				want = ""
			case "changed end":
				changed := end.Add(time.Second)
				run.EndedAt = &changed
				want = ""
			case "changed status":
				run.Status = "COMPLETE"
				want = ""
			case "other project":
				run.ProjectID = "different-project"
				want = ""
			}
			tr.attachRunArchiveRecovery(run)
			if want == "" {
				if run.ArchiveRecovery != nil {
					t.Fatal("unproven state exposed")
				}
				return
			}
			status := run.ArchiveRecovery
			if status == nil || status.State != want {
				t.Fatalf("state=%+v want %s", status, want)
			}
			if mode != "available" && (status.ExpectedSources == nil || *status.ExpectedSources != 2 || status.VerifiedSources == nil || *status.VerifiedSources != 1) {
				t.Fatal("historical source counts differ")
			}
			data, err := json.Marshal(status)
			if err != nil {
				t.Fatal(err)
			}
			for _, private := range []string{"private-generator", "missing-generator", intent.IntentToken, "/private/", "ReceiptSHA", "last_error"} {
				if strings.Contains(string(data), private) {
					t.Fatalf("private metadata leaked: %s", data)
				}
			}
			var fields map[string]any
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			allowed := map[string]bool{"state": true, "recovery_attempts": true, "expected_sources": true, "verified_sources": true, "updated_at": true, "retry_window_ends_at": true}
			for key := range fields {
				if !allowed[key] {
					t.Fatal("unexpected public recovery field", key)
				}
			}
			clone := cloneRunArchiveRecovery(status)
			if clone.RecoveryAttempts != nil {
				*clone.RecoveryAttempts = 99
				if *status.RecoveryAttempts == 99 {
					t.Fatal("aliased attempt projection")
				}
			}
			if clone.UpdatedAt != nil {
				*clone.UpdatedAt = time.Time{}
				if status.UpdatedAt.IsZero() {
					t.Fatal("aliased timestamp projection")
				}
			}
		})
	}
}

type recoveryReadStore struct {
	SQLiteStore *storage.SQLiteStore
	reads       int
	failure     bool
	afterRead   func()
}

func (s *recoveryReadStore) GetRunArchiveCollection(id string) (*storage.RunArchiveCollectionIntent, error) {
	s.reads++
	if s.failure {
		return nil, errors.New("private storage path")
	}
	intent, err := s.SQLiteStore.GetRunArchiveCollection(id)
	if s.afterRead != nil {
		fn := s.afterRead
		s.afterRead = nil
		fn()
	}
	return intent, err
}

func TestRunArchiveRecoverySingleRunReadsAndIncarnationRecheck(t *testing.T) {
	for _, mode := range []string{"read", "store failure", "replaced during read", "replaced incarnation during read"} {
		t.Run(mode, func(t *testing.T) {
			_, sqlite, run, _ := collectionCacheFixture(t, "private-target")
			end := time.Now().UTC()
			if _, err := sqlite.UpdateTestStatusIfActive(run.ID, "FAILED", &end, ""); err != nil {
				t.Fatal(err)
			}
			run.Status = "FAILED"
			run.EndedAt = &end
			if mode == "replaced incarnation during read" {
				row, err := sqlite.GetTest(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				row.InitialProvenance = map[string]any{"run_incarnation": "prior"}
				if err := sqlite.SaveTest(row); err != nil {
					t.Fatal(err)
				}
			}
			store := &recoveryReadStore{SQLiteStore: sqlite, failure: mode == "store failure"}
			tr := NewTestRunner(1, NewHub(), sqliteOverlay(store))
			if mode == "replaced during read" || mode == "replaced incarnation during read" {
				store.afterRead = func() {
					row, err := sqlite.GetTest(run.ID)
					if err != nil {
						t.Fatal(err)
					}
					if mode == "replaced incarnation during read" {
						if err := sqlite.DeleteTest(run.ID); err != nil {
							t.Fatal(err)
						}
						row.InitialProvenance = map[string]any{"run_incarnation": "replacement"}
					} else {
						row.StartTime = row.StartTime.Add(time.Second)
					}
					if err := sqlite.SaveTest(row); err != nil {
						t.Fatal(err)
					}
				}
			}
			for _, read := range []func() (*ManagedTest, bool){func() (*ManagedTest, bool) { return tr.GetTest(run.ID) }, func() (*ManagedTest, bool) { return tr.GetTestSummary(run.ID) }, func() (*ManagedTest, bool) { return tr.GetTestMetricsWindow(run.ID, nil, 10) }} {
				got, ok := read()
				if !ok {
					t.Fatal("run disappeared")
				}
				if mode == "read" {
					if got.ArchiveRecovery == nil || got.ArchiveRecovery.State != "collection_pending" {
						t.Fatal("single read missing status")
					}
				} else if got.ArchiveRecovery != nil {
					t.Fatal("uncertain/replaced state exposed")
				}
			}
			before := store.reads
			tr.ListTests()
			tr.ListTestSummaries()
			tr.ListTestsFiltered(run.ProjectID, nil)
			tr.ListTestSummariesFiltered(run.ProjectID, nil)
			if store.reads != before {
				t.Fatal("list added per-run collection queries")
			}
		})
	}
}
