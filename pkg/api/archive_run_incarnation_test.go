package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRunIncarnationAdmissionIsImmutableAndControllerOwned(t *testing.T) {
	runner, store, test := newPassiveTransitionTestRunner(t, "incarnation-admission")
	runner.mu.Lock()
	initial, err := runner.telemetryAdmissionProvenanceLocked(test)
	runner.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := uuid.Parse(initial.RunIncarnation); err != nil {
		t.Fatalf("admission did not mint an identity: %v", err)
	}
	if saved, err := store.SaveTestInitialProvenance(test.ID, test.ProjectID, initialProvenanceForStorage(initial)); err != nil || !saved {
		t.Fatalf("save identity: %t %v", saved, err)
	}
	// A stale/fresh local candidate cannot replace the committed admission.
	attempted := *initial
	attempted.RunIncarnation = uuid.NewString()
	if saved, err := store.SaveTestInitialProvenance(test.ID, test.ProjectID, initialProvenanceForStorage(&attempted)); err != nil || !saved {
		t.Fatal(err)
	}
	row, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	restored := managedTestFromRecord(row)
	runner.mu.Lock()
	readmitted, err := runner.telemetryAdmissionProvenanceLocked(restored)
	runner.mu.Unlock()
	if err != nil || readmitted.RunIncarnation != initial.RunIncarnation {
		t.Fatalf("reload changed incarnation: %+v %v", readmitted, err)
	}
	if got := buildRunArchiveProvenanceFromSnapshot(restored); got.RunIncarnation != initial.RunIncarnation {
		t.Fatal("runtime provenance lost incarnation")
	}
	replacement := &ManagedTest{ID: test.ID, ProjectID: test.ProjectID, CreatedAt: test.CreatedAt}
	runner.mu.Lock()
	next, err := runner.telemetryAdmissionProvenanceLocked(replacement)
	runner.mu.Unlock()
	if err != nil || next.RunIncarnation == "" || next.RunIncarnation == initial.RunIncarnation {
		t.Fatalf("new logical run reused identity: %+v %v", next, err)
	}
	// Old immutable receipts remain legacy instead of acquiring invented proof.
	replacement.initialArchiveProvenance = &RunArchiveProvenance{}
	runner.mu.Lock()
	legacy, err := runner.telemetryAdmissionProvenanceLocked(replacement)
	runner.mu.Unlock()
	if err != nil || legacy.RunIncarnation != "" {
		t.Fatal("legacy run acquired a new identity", err)
	}
}

type replaceOrphanIncarnationStore struct {
	base    *storage.SQLiteStore
	replace func()
}

func (s *replaceOrphanIncarnationStore) SaveArchiveFinalizationJobIfOrphanForRun(job *storage.ArchiveFinalizationJob, requestedAt time.Time, status, lease, owner string) (bool, error) {
	s.replace()
	return s.base.SaveArchiveFinalizationJobIfOrphanForRun(job, requestedAt, status, lease, owner)
}

func TestOrphanRecoveryExplicitRunIncarnation(t *testing.T) {
	for _, mode := range []string{"same incarnation", "different incarnation", "legacy exact", "legacy different end", "archive token only", "durable token only", "conflicting projections", "corrupt artifact", "replacement before CAS"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			const id = "incarnation-run"
			saveTerminalOrphanTest(t, store, id, storage.DefaultProjectID)
			record := writeOrphanArchiveFixture(t, root, id, id, storage.DefaultProjectID)
			manifest, err := loadRunArchiveManifest(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			row, err := store.GetTest(id)
			if err != nil {
				t.Fatal(err)
			}
			token, durableToken := "current-incarnation", "current-incarnation"
			end := row.EndTime.Add(time.Second)
			switch mode {
			case "different incarnation":
				token = "prior-incarnation"
				end = *row.EndTime
			case "legacy exact":
				token, durableToken = "", ""
				end = *row.EndTime
			case "legacy different end":
				token, durableToken = "", ""
			case "archive token only":
				durableToken = ""
			case "durable token only":
				token = ""
			}
			row.InitialProvenance = map[string]any{"run_incarnation": durableToken}
			row.EndTime = &end
			if err := store.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			manifest.Provenance.RunIncarnation = token
			initial := manifest.Provenance
			manifest.InitialProvenance = &initial
			if mode == "conflicting projections" {
				initial.RunIncarnation = "different"
			}
			if mode == "corrupt artifact" {
				payload := []byte("verified before corruption")
				manifest.Artifacts = []RunArchiveArtifact{{Name: "evidence.txt", SizeBytes: int64(len(payload)), SHA256: sha256BytesHex(payload)}}
				if err := os.WriteFile(filepath.Join(record.ArchivePath, "evidence.txt"), []byte("corrupt"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := writeIndentedJSONFile(record.ManifestPath, manifest); err != nil {
				t.Fatal(err)
			}
			identity, err := os.Stat(record.ArchivePath)
			if err != nil {
				t.Fatal(err)
			}
			runner := NewTestRunner(1, nil, store)
			var orphanStore incarnationOrphanArchiveFinalizationStore = store
			if mode == "replacement before CAS" {
				orphanStore = &replaceOrphanIncarnationStore{base: store, replace: func() {
					if err := store.DeleteTest(id); err != nil {
						t.Fatal(err)
					}
					row.InitialProvenance = map[string]any{"run_incarnation": "replacement"}
					if err := store.SaveTest(row); err != nil {
						t.Fatal(err)
					}
				}}
			}
			err = runner.recoverOrphanedLocalRunArchive(context.Background(), orphanStore, orphanArchiveCandidate{path: record.ArchivePath, identity: identity})
			accepted := mode == "same incarnation" || mode == "legacy exact"
			if accepted && err != nil {
				t.Fatal(err)
			}
			if !accepted && mode != "replacement before CAS" && err == nil {
				t.Fatal("invalid archive accepted")
			}
			current, err := store.GetTest(id)
			if err != nil {
				t.Fatal(err)
			}
			wantEnd := end
			if accepted {
				wantEnd = manifest.CompletedAt
			}
			if current.EndTime == nil || !current.EndTime.Equal(wantEnd) {
				t.Fatalf("durable end=%v want=%v", current.EndTime, wantEnd)
			}
			runner.mu.RLock()
			live := runner.tests[id]
			liveEnd := cloneTimePtr(live.EndedAt)
			runner.mu.RUnlock()
			if liveEnd == nil || !liveEnd.Equal(wantEnd) {
				t.Fatalf("live end=%v want=%v", liveEnd, wantEnd)
			}
			job, err := store.GetArchiveFinalizationJob(id)
			if accepted {
				if err != nil {
					t.Fatal(err)
				}
				proof, err := runner.inspectArchiveFinalizationIdentity(context.Background(), job, current, false)
				if err != nil || proof.RunIncarnation != token {
					t.Fatalf("finalization lost identity: %+v %v", proof, err)
				}
				if err := (&Server{runner: runner, store: store}).retryArchiveFinalizationJobs(context.Background()); err != nil {
					t.Fatal(err)
				}
				if _, err := store.GetRunArchive(id); err != nil {
					t.Fatalf("valid archive not published: %v", err)
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("rejected archive created work: %v", err)
			}
		})
	}
}

func TestArchiveReadRejectsDifferentRunIncarnation(t *testing.T) {
	for _, mode := range []string{"matching", "different", "durable token only", "archive token only", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.Close() })
			const id = "read-incarnation"
			saveTerminalOrphanTest(t, store, id, storage.DefaultProjectID)
			record := writeOrphanArchiveFixture(t, root, id, id, storage.DefaultProjectID)
			manifest, err := loadRunArchiveManifest(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			token, durableToken := "current", "current"
			switch mode {
			case "different":
				token = "previous"
			case "durable token only":
				token = ""
			case "archive token only":
				durableToken = ""
			case "legacy":
				token, durableToken = "", ""
			}
			manifest.Provenance.RunIncarnation = token
			manifest.Provenance.RequestedAt = manifest.CreatedAt
			// This read-path fixture also exercises a real ZIP download, so it
			// needs the datasets that a published archive normally contains.
			manifest.TransactionsFile = "transactions.jsonl"
			manifest.ResourceSamplesFile = "resources.jsonl"
			manifest.IntervalSummariesFile = "interval_summaries.jsonl"
			for _, name := range []string{manifest.TransactionsFile, manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
				if err := os.WriteFile(filepath.Join(record.ArchivePath, name), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := updateRunArchiveArtifactIntegrity(record.ArchivePath, manifest); err != nil {
				t.Fatal(err)
			}
			if err := writeIndentedJSONFile(record.ManifestPath, manifest); err != nil {
				t.Fatal(err)
			}
			row, err := store.GetTest(id)
			if err != nil {
				t.Fatal(err)
			}
			row.InitialProvenance = map[string]any{"run_incarnation": durableToken}
			if err := store.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			inspected, _, err := orphanArchiveRecordFromDirectoryWithIdentity(context.Background(), record.ArchivePath, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.SaveRunArchive(inspected); err != nil {
				t.Fatal(err)
			}
			runner := NewTestRunner(1, nil, store, WithExecutionLeadership(newExecutionLeadership(store, "reader")))
			got, ok := runner.GetRunArchiveContext(context.Background(), id)
			accepted := mode == "matching" || mode == "legacy"
			if ok != accepted || (got != nil) != accepted {
				t.Fatalf("archive read accepted=%t want=%t record=%+v", ok, accepted, got)
			}
			runner.mu.RLock()
			attached := runner.tests[id].Archive
			runner.mu.RUnlock()
			if !accepted && attached != nil {
				t.Fatal("rejected incarnation retained live archive")
			}
			request := httptest.NewRequest(http.MethodGet, "/api/v1/library/results/"+id+"/archive", nil)
			request.SetPathValue("id", id)
			response := httptest.NewRecorder()
			(&Server{store: store, runner: runner}).handleLibraryResultArchive(response, request)
			wantStatus := http.StatusNotFound
			if accepted {
				wantStatus = http.StatusOK
			}
			if response.Code != wantStatus {
				t.Fatalf("library archive status=%d want=%d body=%s", response.Code, wantStatus, response.Body.String())
			}
		})
	}
}
