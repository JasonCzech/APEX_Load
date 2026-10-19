package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRunLifecyclePrecisionAPI(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	exerciseRunLifecyclePrecisionAPI(t, server, store)
}

// The same exercise runs against PostgreSQL on Linux. The annotation uses the
// durable start a follower would serve, while the handler sees the live run.
func exerciseRunLifecyclePrecisionAPI(t *testing.T, server *Server, store *storage.SQLiteStore) {
	for _, mode := range []string{"local", "distributed", "distributed_supplied_start", "distributed_fresh_barrier"} {
		t.Run(mode, func(t *testing.T) {
			runner := server.runner
			script := &dsl.Script{Name: "precision", Protocol: "http", Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "https://example.test/"}}}
			ctx, cancel := context.WithCancel(context.Background())
			run := &ManagedTest{
				ID: "precision-start-" + mode, Name: "Precision start", ProjectID: storage.DefaultProjectID,
				Status: "INITIALIZING", CreatedAt: atMicrosecond(time.Now()), Duration: time.Minute, DurationStr: "1m",
				Groups: []TestGroup{{Name: "default", ScriptName: script.Name, VUsers: 1}}, TotalVUsers: 1,
				groupSpecs: []engine.GroupConfig{{Name: "default", Script: script, VUsers: 1}}, executionCtx: ctx, cancel: cancel,
			}
			runner.mu.Lock()
			runner.tests[run.ID] = run
			runner.mu.Unlock()
			t.Cleanup(func() {
				cancel()
				runner.mu.Lock()
				delete(runner.tests, run.ID)
				runner.mu.Unlock()
			})
			if err := runner.saveManagedTestRecord(run); err != nil {
				t.Fatal(err)
			}
			var expectedZone *time.Location
			switch mode {
			case "local":
				mge, err := runner.buildRunEngine(run, nil)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if err := mge.ClosePreparedRuntime(); err != nil {
						t.Error(err)
					}
				})
				_, done, stopped, started, err := runner.startManagedTestRun(run, mge, nil)
				if err != nil || !started {
					t.Fatalf("local start: started=%v err=%v", started, err)
				}
				// No engine is run: stop the publisher before reading the live copy.
				close(done)
				select {
				case <-stopped:
				case <-time.After(5 * time.Second):
					t.Fatal("metrics publisher did not stop")
				}
				expectedZone = time.Local
			case "distributed", "distributed_fresh_barrier":
				backend := &distributedExecutionBackend{}
				started, _, err := backend.prepareDistributedTestRun(runner, run, nil, nil, nil)
				if err != nil || !started {
					t.Fatalf("distributed start: started=%v err=%v", started, err)
				}
				t.Cleanup(run.remoteLifecycleCancel)
				if mode == "distributed_fresh_barrier" {
					barrier, err := persistFreshDistributedScheduledStart(runner, run, time.Second)
					if err != nil {
						t.Fatal(err)
					}
					if !barrier.Equal(*run.StartedAt) {
						t.Fatalf("generator barrier %v differs from controller start %v", barrier, run.StartedAt)
					}
				}
				expectedZone = time.Local
			case "distributed_supplied_start":
				// A deterministic sub-microsecond instant also verifies that supplied
				// clocks retain their original zone when stamped for storage.
				expectedZone = time.FixedZone("precision", -5*60*60)
				stamp := time.Now().Add(-time.Second).In(expectedZone)
				stamp = stamp.Truncate(time.Microsecond).Add(789 * time.Nanosecond)
				backend := &distributedExecutionBackend{}
				started, _, err := backend.prepareDistributedTestRunWithLifecycle(runner, run, nil, nil, nil, ctx, cancel, stamp)
				if err != nil || !started {
					t.Fatalf("supplied distributed start: started=%v err=%v", started, err)
				}
			}
			record, err := store.GetTest(run.ID)
			if err != nil || record == nil || record.StartedAt == nil || run.StartedAt == nil {
				t.Fatalf("started run = %+v, err=%v", record, err)
			}
			requireAPIStoredTimestamp(t, "run started_at", *run.StartedAt, *record.StartedAt)
			if run.StartedAt.Location() != expectedZone {
				t.Errorf("start zone = %v, want %v", run.StartedAt.Location(), expectedZone)
			}
			if mode != "distributed_supplied_start" && *run.StartedAt == run.StartedAt.Round(0) {
				t.Error("run start lost its monotonic reading")
			}
			if mode != "distributed_fresh_barrier" {
				if response := precisionAnnotationRequest(t, server, run.ID, *record.StartedAt, 0); response.Code != http.StatusCreated {
					t.Errorf("elapsed-zero follower annotation: %d %s", response.Code, response.Body.String())
				}
				if response := precisionAnnotationRequest(t, server, run.ID, record.StartedAt.Add(-time.Microsecond), 1); response.Code != http.StatusBadRequest {
					t.Errorf("annotation before start: %d %s", response.Code, response.Body.String())
				}
			}
		})
	}
}

func precisionAnnotationRequest(t *testing.T, server *Server, runID string, at time.Time, revision int64) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(runAnnotationRequest{ExpectedRevision: revision, Timestamp: at, Label: "Boundary"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	request.SetPathValue("id", runID)
	response := httptest.NewRecorder()
	server.handleCreateRunAnnotation(response, request)
	return response
}
