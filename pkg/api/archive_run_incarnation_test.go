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
	for _, mode := range []string{"same incarnation", "different incarnation", "legacy exact", "legacy different end", "archive token only", "durable token only", "conflicting projections", "corrupt artifact", "replacement before CAS", "same incarnation without execution term", "same incarnation exact end", "different generation exact end", "archive generation only", "durable generation only", "generation changed before CAS", "end changed before CAS", "pending job without generation"} {
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
			case "same incarnation exact end", "different generation exact end", "pending job without generation":
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
			var generation uint64
			if mode != "legacy exact" && mode != "legacy different end" && mode != "same incarnation without execution term" && mode != "archive generation only" {
				generation = stampOrphanRunExecutionGeneration(t, store, id)
			}
			manifest.Provenance.ExecutionGeneration = generation
			switch mode {
			case "different generation exact end", "archive generation only":
				manifest.Provenance.ExecutionGeneration = generation + 1
			case "durable generation only":
				manifest.Provenance.ExecutionGeneration = 0
			}
			manifest.Provenance.RunIncarnation = token
			initial := manifest.Provenance
			initial.ExecutionGeneration = 0 // Admission predates execution ownership.
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
					stampOrphanRunExecutionGeneration(t, store, id)
				}}
			}
			if mode == "generation changed before CAS" || mode == "end changed before CAS" {
				orphanStore = &replaceOrphanIncarnationStore{base: store, replace: func() {
					if mode == "generation changed before CAS" {
						advanceOrphanFixtureGeneration(t, store, id)
					} else {
						end = end.Add(time.Second)
						row.EndTime = &end
						if err := store.SaveTest(row); err != nil {
							t.Fatal(err)
						}
					}
				}}
			}
			original, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			err = runner.recoverOrphanedLocalRunArchive(context.Background(), orphanStore, orphanArchiveCandidate{path: record.ArchivePath, identity: identity})
			accepted := mode == "same incarnation" || mode == "same incarnation exact end" || mode == "legacy exact" || mode == "pending job without generation"
			if accepted && err != nil {
				t.Fatal(err)
			}
			if !accepted && mode != "replacement before CAS" && mode != "generation changed before CAS" && mode != "end changed before CAS" && err == nil {
				t.Fatal("invalid archive accepted")
			}
			current, err := store.GetTest(id)
			if err != nil {
				t.Fatal(err)
			}
			// Even successful recovery must preserve the durable end, and no
			// source bytes change before the fenced job is processed.
			wantEnd := end
			unchanged, readErr := os.ReadFile(record.ManifestPath)
			if readErr != nil || string(unchanged) != string(original) {
				t.Fatalf("recovery rewrote source before finalization: %v", readErr)
			}
			if current.EndTime == nil || !current.EndTime.Equal(wantEnd) {
				t.Fatalf("durable end=%v want=%v", current.EndTime, wantEnd)
			}
			runner.mu.RLock()
			live := runner.tests[id]
			liveEnd := cloneTimePtr(live.EndedAt)
			runner.mu.RUnlock()
			if mode != "end changed before CAS" && (liveEnd == nil || !liveEnd.Equal(wantEnd)) {
				t.Fatalf("live end=%v want=%v", liveEnd, wantEnd)
			}
			job, err := store.GetArchiveFinalizationJob(id)
			if accepted {
				if err != nil {
					t.Fatal(err)
				}
				if mode == "pending job without generation" {
					// Upgrade compatibility: the old outbox record omits the new
					// field; verified manifest provenance supplies the CAS proof.
					job.ArchiveRecord.ExecutionGeneration = 0
					if err := store.SaveArchiveFinalizationJob(job); err != nil {
						t.Fatal(err)
					}
					job, err = store.GetArchiveFinalizationJob(id)
					if err != nil || job.ArchiveRecord.ExecutionGeneration != 0 {
						t.Fatalf("pending legacy record: %+v %v", job, err)
					}
				}
				inspection, err := runner.inspectArchiveFinalizationSource(context.Background(), job, current, false)
				if err != nil || inspection == nil || inspection.identity.RunIncarnation != token || inspection.identity.ExecutionGeneration != generation {
					t.Fatalf("finalization lost identity: %+v %v", inspection, err)
				}
				if err := (&Server{runner: runner, store: store}).retryArchiveFinalizationJobs(context.Background()); err != nil {
					t.Fatal(err)
				}
				published, err := store.GetRunArchive(id)
				if err != nil {
					t.Fatalf("valid archive not published: %v", err)
				}
				final, err := loadRunArchiveManifestFromRecord(published)
				if err != nil || !final.CompletedAt.Equal(end) || !published.CompletedAt.Equal(end) {
					t.Fatalf("publication did not preserve durable end: %+v %v", final, err)
				}
				if mode == "same incarnation" && (final.ExecutorCompletedAt == nil || !final.ExecutorCompletedAt.Equal(manifest.CompletedAt)) {
					t.Fatal("restamping lost executor completion time")
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("rejected archive created work: %v", err)
			}
		})
	}
}

// Move execution ownership while preserving the public run identity and times.
// This is the interleaving between verified manifest I/O and its storage CAS.
func advanceOrphanFixtureGeneration(t *testing.T, store *storage.SQLiteStore, id string) {
	t.Helper()
	row, err := store.GetTest(id)
	if err != nil {
		t.Fatal(err)
	}
	status, ended := row.Status, row.EndTime
	row.Status, row.EndTime = "RUNNING", nil
	if err := store.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	const lease = "orphan-generation-fixture"
	// Lease admission uses the database clock. Release the fixture owner while
	// preserving its durable generation instead of supplying a future clock.
	if err := store.ReleaseSchedulerLease(lease, "fixture-term"); err != nil {
		t.Fatal(err)
	}
	held, next, err := store.TryAcquireSchedulerLeaseWithGeneration(lease, "successor-term", time.Now(), time.Minute)
	if err != nil || !held || next <= row.ExecutionGeneration {
		t.Fatal("acquire successor term", held, next, err)
	}
	if ok, err := store.AdvanceTestExecutionGenerationIfLeaseTermHeld(id, lease, "successor-term", next); err != nil || !ok {
		t.Fatal("advance execution term", ok, err)
	}
	row.Status, row.EndTime = status, ended
	if err := store.SaveTest(row); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveReadRejectsDifferentRunIncarnation(t *testing.T) {
	for _, mode := range []string{"matching", "different", "durable token only", "archive token only", "legacy", "matching generation", "different generation", "archive generation only", "durable generation only"} {
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
			if mode == "matching generation" || mode == "different generation" || mode == "durable generation only" {
				manifest.Provenance.ExecutionGeneration = stampOrphanRunExecutionGeneration(t, store, id)
			}
			switch mode {
			case "different generation", "archive generation only":
				manifest.Provenance.ExecutionGeneration++
			case "durable generation only":
				manifest.Provenance.ExecutionGeneration = 0
			}
			if err := writeIndentedJSONFile(record.ManifestPath, manifest); err != nil {
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
			accepted := mode == "matching" || mode == "legacy" || mode == "matching generation"
			if ok != accepted || (got != nil) != accepted {
				t.Fatalf("archive read accepted=%t want=%t record=%+v", ok, accepted, got)
			}
			runner.mu.RLock()
			attached := runner.tests[id].Archive
			runner.mu.RUnlock()
			if !accepted && attached != nil {
				t.Fatal("rejected incarnation retained live archive")
			}
			summary, exists := runner.GetTestSummary(id)
			if !exists || (summary.Archive != nil) != accepted {
				t.Fatalf("catalog summary attached wrong run identity: %+v", summary)
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
