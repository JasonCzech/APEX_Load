package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestArchiveFinalizationRejectsChangedIdentityBeforeMutation(t *testing.T) {
	for _, mode := range []string{"start", "end", "status", "project", "record ID", "prepared hash", "prepared identity"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			test, record := createArchiveFinalizationTest(t, store, "identity-replaced", root)
			before, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			job := &storage.ArchiveFinalizationJob{TestID: test.ID, ProjectID: test.ProjectID, ArchiveRecord: *record, PersistArtifacts: true}
			if err := store.SaveArchiveFinalizationJob(job); err != nil {
				t.Fatal(err)
			}
			row, err := store.GetTest(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "start":
				row.StartTime = row.StartTime.Add(time.Second)
			case "end":
				end := row.EndTime.Add(time.Second)
				row.EndTime = &end
			case "status":
				row.Status = "FAILED"
			case "record ID":
				job.ArchiveRecord.TestID = "different-test"
				if _, err := NewTestRunner(1, NewHub(), store).inspectArchiveFinalizationSource(context.Background(), job, row, false); err == nil {
					t.Fatal("wrong record ID accepted")
				}
				return
			case "project":
				// Keep the durable test's project immutable; a stale job must be rejected.
				job.ProjectID = "wrong-project"
				if _, err := NewTestRunner(1, NewHub(), store).inspectArchiveFinalizationSource(context.Background(), job, row, false); err == nil {
					t.Fatal("wrong project accepted")
				}
				return
			case "prepared hash", "prepared identity":
				prepared := bytes.Replace(before, []byte(`"COMPLETE"`), []byte(`"FAILED"`), 1)
				if mode == "prepared identity" {
					job.ArchiveRecord.ManifestSHA256 = sha256BytesHex(prepared)
				}
				if mode == "prepared hash" {
					job.PreparedManifestJSON = string(prepared)
					if _, err := NewTestRunner(1, NewHub(), store).inspectArchiveFinalizationSource(context.Background(), job, row, false); err == nil {
						t.Fatal("untrusted prepared bytes accepted")
					}
					return
				}
				if ok, err := store.PrepareArchiveFinalizationManifestIfToken(job.TestID, job.IntentToken, &job.ArchiveRecord, string(prepared)); err != nil || !ok {
					t.Fatal("prepare", ok, err)
				}
			}
			if err := store.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			runner := NewTestRunner(1, NewHub(), store)
			if err := runner.processArchiveFinalizationJob(context.Background(), job); err == nil {
				t.Fatal("stale source accepted")
			}
			after, err := os.ReadFile(record.ManifestPath)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("source mutated", err)
			}
			for _, name := range []string{testNotesArchiveFileName, testRunManifestFileName} {
				if _, err := os.Stat(filepath.Join(record.ArchivePath, name)); !os.IsNotExist(err) {
					t.Fatal("adjunct created", name, err)
				}
			}
			if _, err := store.GetArchiveFinalizationJob(test.ID); err != nil {
				t.Fatal("pending work removed", err)
			}
			if _, err := store.GetRunArchive(test.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("stale catalog committed", err)
			}
		})
	}
}

type revokeCompletionLeaseStore struct{ SQLiteStore *storage.SQLiteStore }

func (s *revokeCompletionLeaseStore) CompleteArchiveFinalizationForRun(id, token, refresh string, record *storage.RunArchiveRecord, identity storage.ArchiveFinalizationRunIdentity) (bool, error) {
	if err := s.SQLiteStore.ReleaseSchedulerLease(identity.LeaseName, identity.LeaseOwner); err != nil {
		return false, err
	}
	return s.SQLiteStore.CompleteArchiveFinalizationForRun(id, token, refresh, record, identity)
}

func (s *revokeCompletionLeaseStore) CompleteArchiveFinalizationWithTelemetry(id, token, refresh string, record *storage.RunArchiveRecord, identity storage.ArchiveFinalizationRunIdentity, publication storage.TelemetryNotificationPublication) (bool, error) {
	if err := s.SQLiteStore.ReleaseSchedulerLease(identity.LeaseName, identity.LeaseOwner); err != nil {
		return false, err
	}
	return s.SQLiteStore.CompleteArchiveFinalizationWithTelemetry(id, token, refresh, record, identity, publication)
}

func TestArchiveFinalizationLostLeasePreservesCurrentSource(t *testing.T) {
	for _, mode := range []string{"legacy", "atomic telemetry"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			sqlite, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer sqlite.Close()
			store := &revokeCompletionLeaseStore{sqlite}
			test, record := createArchiveFinalizationTest(t, sqliteOverlay(store), "lease-expired-publication", root)
			runner := NewTestRunner(1, NewHub(), sqliteOverlay(store))
			if mode == "atomic telemetry" {
				runner.telemetryNotificationPublication = func(context.Context, string, *RunArchiveManifest) (storage.TelemetryNotificationPublication, error) {
					return storage.TelemetryNotificationPublication{Decision: "not_applicable"}, nil
				}
			}

			leader := newExecutionLeadership(sqliteOverlay(store), "publication-fence")
			leader.tick()
			defer leader.relinquish()
			runner.leadership = leader
			runner.durableExecutionFence = true
			err = runner.finalizeRunArchiveDurably(context.Background(), test, record, true)
			if err == nil || !strings.Contains(err.Error(), "current publication preserved") {
				t.Fatalf("completion error=%v", err)
			}
			if _, err := store.SQLiteStore.GetArchiveFinalizationJob(test.ID); err != nil {
				t.Fatal("pending intent lost", err)
			}
			if _, err := store.SQLiteStore.GetRunArchive(test.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatal("lost lease published", err)
			}
			jobs, err := store.SQLiteStore.ListArchiveVersionCleanupJobs(10)
			if err != nil || len(jobs) != 0 {
				t.Fatal("current source queued for deletion", err)
			}
			if _, err := os.Stat(record.ManifestPath); err != nil {
				t.Fatal("source lost", err)
			}
		})
	}
}

func TestSupersededCleanupPreservesPendingPublication(t *testing.T) {
	for _, mode := range []string{"same pointer", "same local path", "catalog current", "retired", "source locked"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			test, record := createArchiveFinalizationTest(t, store, "cleanup-pending", root)
			server := &Server{store: store}
			if mode == "same pointer" || mode == "same local path" {
				pending := *record
				if mode == "same local path" {
					// Object namespaces differ; shared staging must still block both deletions.
					record.ArchiveBackend = archiveBackendS3
					record.ArchiveURI = "s3://test/_versions/v1/old/cleanup-pending"
					pending.ArchiveBackend = archiveBackendS3
					pending.ArchiveURI = "s3://test/_versions/v1/new/cleanup-pending"
				}
				if err := store.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: test.ID, ProjectID: test.ProjectID, ArchiveRecord: pending}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "catalog current" {
				if err := store.SaveRunArchive(record); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			if mode == "source locked" {
				release, err := lockArchivePublicationSource(ctx, test.ID)
				if err != nil {
					t.Fatal(err)
				}
				defer release()
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
			}
			err = server.pruneSupersededArchiveVersion(ctx, record)
			switch mode {
			case "same pointer", "same local path":
				if err == nil || !strings.Contains(err.Error(), "pending publication") {
					t.Fatalf("cleanup error=%v", err)
				}
			case "source locked":
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			_, err = os.Stat(record.ManifestPath)
			if mode == "retired" {
				if !os.IsNotExist(err) {
					t.Fatal("retired source not removed", err)
				}
			} else if err != nil {
				t.Fatal("owned source removed", err)
			}
		})
	}
}
