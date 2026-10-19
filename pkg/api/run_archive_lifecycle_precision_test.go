package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRunArchiveLifecyclePrecisionAPI(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "precision.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	exerciseRunArchiveLifecyclePrecisionAPI(t, store)
}

// A run's archive can be finalized before its in-memory terminal transition.
// Its timestamp must already have stored precision at the backend's stamp,
// before either the immutable manifest or the terminal row is written.
func exerciseRunArchiveLifecyclePrecisionAPI(t *testing.T, store *storage.SQLiteStore) {
	for _, mode := range []string{"local_complete", "local_build_failure", "distributed_complete", "distributed_failed", "distributed_aborted"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			runner := NewTestRunner(1, NewHub(), store)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			t.Cleanup(func() {
				cancel()
				runner.beginShutdown()
				// A failing pre-fix regression can leave a publication retry. Stop
				// it before its archive directory and the shared store are removed.
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					runner.terminalRetryMu.Lock()
					active := len(runner.terminalRetrying)
					runner.terminalRetryMu.Unlock()
					if active == 0 {
						return
					}
					time.Sleep(5 * time.Millisecond)
				}
				t.Error("archive publication retry did not stop")
			})
			createdAt := atMicrosecond(time.Now().Add(-time.Minute))
			run := &ManagedTest{ID: "precision-archive-lifecycle-" + mode, Name: mode, ProjectID: storage.DefaultProjectID,
				Status: "INITIALIZING", CreatedAt: createdAt, Duration: time.Minute, DurationStr: "1m", executionCtx: ctx, cancel: cancel}
			wantStatus := "COMPLETE"
			switch mode {
			case "local_complete", "local_build_failure":
				run.TerminationMode = dsl.TerminationIterations
				run.Duration, run.DurationStr = 0, ""
				target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
				t.Cleanup(target.Close)
				script := &dsl.Script{Name: "archive precision", Protocol: "http", Config: dsl.ScriptConfig{
					BaseURL: target.URL, RunLogic: dsl.RunLogicConfig{Iterations: 1},
				}, Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}}}
				vusers := 1
				if mode == "local_build_failure" {
					vusers = 0
					wantStatus = "FAILED"
				}
				run.TotalVUsers = vusers
				run.Groups = []TestGroup{{Name: "default", ScriptName: script.Name, VUsers: vusers}}
				run.groupSpecs = []engine.GroupConfig{{Name: "default", VUsers: vusers, Script: script}}
			default:
				run.Status = "RUNNING"
				startedAt := createdAt.Add(time.Second)
				run.StartedAt = &startedAt
				run.remoteFinalizing = true
				run.LiveMetrics = &LiveMetrics{Timestamp: startedAt}
				switch mode {
				case "distributed_failed":
					wantStatus = "FAILED"
				case "distributed_aborted":
					wantStatus = "ABORTED"
				}
				source := t.TempDir()
				artifacts := make(map[string]string)
				for _, name := range []string{"transactions.jsonl", "resources.jsonl", "interval_summaries.jsonl"} {
					path := filepath.Join(source, name)
					if err := os.WriteFile(path, nil, 0600); err != nil {
						t.Fatal(err)
					}
					artifacts[name] = path
				}
				manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: run.ID, TestName: run.Name,
					Status: wantStatus, CreatedAt: createdAt, CompletedAt: startedAt,
					TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "interval_summaries.jsonl"}
				artifacts[runArchiveManifestFileName] = filepath.Join(source, runArchiveManifestFileName)
				if err := writeIndentedJSONFile(artifacts[runArchiveManifestFileName], manifest); err != nil {
					t.Fatal(err)
				}
				run.remoteClientsByTarget = map[string]generatorClient{"generator": &archiveStreamingFakeClient{artifacts: artifacts}}
			}
			runner.tests[run.ID] = run
			if err := runner.saveManagedTestRecord(run); err != nil {
				t.Fatal(err)
			}
			if mode == "local_complete" || mode == "local_build_failure" {
				(localExecutionBackend{}).Run(runner, run)
			} else {
				newDistributedExecutionBackend([]string{"generator"}, time.Second, nil).completeDistributedTestRun(runner, run, wantStatus, "")
			}
			row, err := store.GetTest(run.ID)
			if err != nil || row == nil || row.EndTime == nil || run.EndedAt == nil || run.Status != wantStatus || row.Status != wantStatus {
				t.Fatalf("terminal run held=%s/%v, stored=%+v, err=%v; want %s", run.Status, run.EndedAt, row, err, wantStatus)
			}
			requireAPIStoredTimestamp(t, "archive run ended_at", *run.EndedAt, *row.EndTime)
			catalog, err := store.GetRunArchive(run.ID)
			if err != nil || catalog == nil || run.Archive == nil {
				t.Fatalf("terminal archive was not published and attached: catalog=%+v, held=%+v, err=%v", catalog, run.Archive, err)
			}
			manifest, err := loadRunArchiveManifestFromRecord(catalog)
			if err != nil {
				t.Fatal(err)
			}
			for what, stamp := range map[string]time.Time{"held archive completed_at": run.Archive.CompletedAt, "catalog completed_at": catalog.CompletedAt, "manifest completed_at": manifest.CompletedAt} {
				requireAPIStoredTimestamp(t, what, stamp, *row.EndTime)
			}
			// Revalidate against the final durable row, after the backend's second
			// terminal write. SQLite intentionally requires exact archive identity.
			data, err := os.ReadFile(catalog.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			job := &storage.ArchiveFinalizationJob{TestID: run.ID, ProjectID: run.ProjectID, ArchiveRecord: *catalog}
			if _, err := runner.validateArchiveFinalizationSource(job, catalog, row, data); err != nil {
				t.Fatalf("published archive no longer matches the terminal row: %v", err)
			}
		})
	}
}
