package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// The scripts, scenarios, run archives and live points a controller writes
// stay cached in memory, and their times are identities that recording
// bindings, authoring preconditions, archive cleanup and history merges
// compare with the stored row. PostgreSQL keeps microseconds and Go's clock
// has nanoseconds on Linux, so the cached copy must match what the store kept.
// This runs on SQLite here and on PostgreSQL in
// created_at_precision_postgres_integration_test.go.
func TestCreatedAtPrecisionAPI(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	exerciseCreatedAtPrecisionAPI(t, server, store)
}

// A published point's wall time is stamped at microseconds, but early-stop
// windows measure elapsed time from it, so it keeps the clock's monotonic
// reading.
func TestPublishedLivePointKeepsMonotonicClock(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	run := &ManagedTest{ID: "monotonic-publication", Status: "RUNNING"}
	runner.tests[run.ID] = run
	sampled := time.Now()
	point := &LiveMetrics{Timestamp: sampled}
	if !runner.publishLiveMetrics(run, point, nil) {
		t.Fatal("live point was not published")
	}
	if point.Timestamp.Nanosecond()%int(time.Microsecond) != 0 || sampled.Sub(point.Timestamp) >= time.Microsecond || point.Timestamp.After(sampled) {
		t.Fatalf("published at %s, sampled at %s: want the sample's microsecond", point.Timestamp.Format(time.RFC3339Nano), sampled.Format(time.RFC3339Nano))
	}
	if point.Timestamp == point.Timestamp.Round(0) {
		t.Fatal("the published point lost its monotonic clock reading")
	}
}

// Scenario schedules build their runs directly, so the builder stamps the
// run's creation time at the stored precision too.
func TestScheduledRunCreationTimeIsStoredPrecision(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	storePreflightScript(runner, "precision-schedule-script", storage.DefaultProjectID)
	req := CreateTestRequest{Name: "scheduled", ScriptID: "precision-schedule-script", VUsers: 1, Duration: "1m"}
	req.projectQuotaEvidence = runner.loadCreateTestProjectQuotaEvidence(req)
	runner.mu.Lock()
	run, err := runner.buildManagedTestLocked(req, "precision-scheduled-run", "QUEUED")
	runner.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if run.CreatedAt.Nanosecond()%int(time.Microsecond) != 0 || run.CreatedAt == run.CreatedAt.Round(0) {
		t.Fatalf("scheduled run created at %s: want microseconds with the monotonic reading kept", run.CreatedAt.Format(time.RFC3339Nano))
	}
}

func exerciseCreatedAtPrecisionAPI(t *testing.T, server *Server, store *storage.SQLiteStore) {
	t.Run("direct_import_recording_binding", func(t *testing.T) {
		script := libraryTestScript("dddddddd-dddd-4ddd-8ddd-dddddddddddd", "precision import", storage.DefaultProjectID, "/precision")
		transaction, err := newCreateImportTransaction(server, "har", []*dsl.Script{script}, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		stored, err := transaction.createScript(script)
		if err != nil {
			t.Fatal(err)
		}
		if err := transaction.commit(); err != nil {
			t.Fatal(err)
		}
		// A batch import with a recording session verifies the baseline it
		// bound against the stored script the import returned.
		baseline := &attestedRecordingBaseline{source: "proxy_recording", observations: []correlate.Observation{{ID: 0, RequestURL: "https://example.test/precision"}}}
		if err := server.persistOrVerifyGenericImportBatchBaselines([]*StoredScript{stored}, []*attestedRecordingBaseline{baseline}, []string{"precision-session"}, false); err != nil {
			t.Fatalf("verify the recording bound to the imported script: %v", err)
		}
		requireCachedScriptCreatedAt(t, store, stored)
	})
	t.Run("legacy_script_store", func(t *testing.T) {
		stored, err := server.runner.StoreScriptDurable(libraryTestScript("eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee", "precision store", storage.DefaultProjectID, "/store"))
		if err != nil {
			t.Fatal(err)
		}
		requireCachedScriptCreatedAt(t, store, stored)
	})
	t.Run("scenario_store", func(t *testing.T) {
		parent, err := server.runner.StoreScriptDurable(libraryTestScript("ffffffff-ffff-4fff-8fff-ffffffffffff", "precision scenario script", storage.DefaultProjectID, "/scenario"))
		if err != nil {
			t.Fatal(err)
		}
		scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
			Name: "precision scenario", Duration: "1m",
			Groups: []CreateGroupEntry{{Name: "API", ScriptID: parent.ID, VUsers: 1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		persisted, _, err := store.GetScenario(scenario.ID)
		if err != nil {
			t.Fatal(err)
		}
		requireAPIStoredTimestamp(t, "scenario created_at", scenario.CreatedAt, persisted.CreatedAt)
	})
	t.Run("archive_cleanup_intent", func(t *testing.T) {
		testID := "precision-archive-run"
		if err := store.SaveTest(&storage.TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: storage.DefaultProjectID, StartTime: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		// Archive records carry the run's clock, and a controller without HA
		// serves retention the record it published.
		createdAt := time.Date(2026, 10, 18, 12, 0, 0, 123456789, time.UTC)
		published := &storage.RunArchiveRecord{
			TestID: testID, ArchiveBackend: archiveBackendFilesystem, ArchivePath: "/archives/precision",
			ManifestPath: "/archives/precision/" + runArchiveManifestFileName, ManifestSHA256: "precision-sha",
			CreatedAt: createdAt, CompletedAt: createdAt.Add(time.Minute),
		}
		if err := store.SaveRunArchive(published); err != nil {
			t.Fatal(err)
		}
		if err := server.ensureArchiveCleanupIntent(published); err != nil {
			t.Fatalf("cleanup intent for the published record: %v", err)
		}
		// A retry after a restart holds the record read back from storage.
		reread, err := store.GetRunArchive(testID)
		if err != nil {
			t.Fatal(err)
		}
		if err := server.ensureArchiveCleanupIntent(reread); err != nil {
			t.Fatalf("cleanup intent for the stored record: %v", err)
		}
	})
	t.Run("run_created_at", func(t *testing.T) {
		// HA followers read a run back from PostgreSQL, and the Command Center
		// identifies a queued run by its created_at text on every read.
		storePreflightScript(server.runner, "precision-run-script", storage.DefaultProjectID)
		run, err := server.runner.CreateTest(CreateTestRequest{Name: "precision run", ScriptID: "precision-run-script", VUsers: 1, Duration: "1m"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			server.runner.mu.Lock()
			delete(server.runner.tests, run.ID)
			server.runner.mu.Unlock()
		})
		record, err := store.GetTest(run.ID)
		if err != nil || record == nil {
			t.Fatalf("read the run back = %+v, %v", record, err)
		}
		requireAPIStoredTimestamp(t, "run created_at", run.CreatedAt, record.StartTime)
		if run.CreatedAt == run.CreatedAt.Round(0) {
			t.Error("the run's creation time lost its monotonic clock reading")
		}
	})
	t.Run("published_live_point", func(t *testing.T) {
		testID := "precision-history-run"
		if err := store.SaveTest(&storage.TestRecord{ID: testID, Name: testID, Status: "RUNNING", ProjectID: storage.DefaultProjectID, StartTime: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
		run := &ManagedTest{ID: testID, Name: testID, Status: "RUNNING", ProjectID: storage.DefaultProjectID}
		server.runner.mu.Lock()
		server.runner.tests[testID] = run
		server.runner.mu.Unlock()
		// Nothing executes this run, so shutdown must not try to stop it.
		t.Cleanup(func() {
			server.runner.mu.Lock()
			delete(server.runner.tests, testID)
			server.runner.mu.Unlock()
		})
		// A local sample carries the run's clock. The controller keeps the
		// published point, stores it as history and archives it.
		point := &LiveMetrics{Timestamp: time.Date(2026, 10, 18, 12, 0, 0, 123456789, time.UTC), TotalTx: 7}
		archive := &archiveRecorderStub{}
		if !server.runner.publishLiveMetrics(run, point, &runArtifacts{recorder: archive}) {
			t.Fatal("live point was not published")
		}
		loaded := &ManagedTest{ID: testID}
		if err := server.runner.loadStoredTestHistoryLimit(loaded, 0); err != nil {
			t.Fatal(err)
		}
		if len(loaded.History) != 1 {
			t.Fatalf("stored history has %d points, want 1", len(loaded.History))
		}
		server.runner.mu.RLock()
		held := cloneLiveMetrics(run.LiveMetrics)
		server.runner.mu.RUnlock()
		requireAPIStoredTimestamp(t, "published point", held.Timestamp, loaded.History[0].Timestamp)
		if archive.recordedLive == nil {
			t.Fatal("live point was not archived")
		}
		requireAPIStoredTimestamp(t, "archived point", archive.recordedLive.Timestamp, loaded.History[0].Timestamp)
		// The final point of a finished run is merged once, not twice.
		if merged := mergeStoredHistoryWithCanonicalTail(loaded.History, held); len(merged) != 1 {
			t.Fatalf("final point appears %d times in the merged history, want once", len(merged))
		}
	})
}

func requireCachedScriptCreatedAt(t *testing.T, store storage.Store, stored *StoredScript) {
	t.Helper()
	persisted, err := store.GetScript(stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	requireAPIStoredTimestamp(t, "cached created_at", stored.CreatedAt, persisted.CreatedAt)
	requireAPIStoredTimestamp(t, "cached script created_at", stored.Script.CreatedAt, persisted.CreatedAt)
}

func requireAPIStoredTimestamp(t *testing.T, what string, held, stored time.Time) {
	t.Helper()
	if !held.Equal(stored) || !held.Equal(held.Truncate(time.Microsecond)) {
		t.Errorf("%s held %s, stored %s: want the stored value, at microseconds", what, held.Format(time.RFC3339Nano), stored.Format(time.RFC3339Nano))
	}
}
