package api

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLocalArchiveFailurePersistsWithoutChangingExecution(t *testing.T) {
	for _, kind := range []string{"capture", "conversion", "backlog"} {
		for _, outcome := range []string{"COMPLETE", "ABORTED", "FAILED"} {
			t.Run(kind+outcome, func(t *testing.T) {
				t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
				t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
				t.Setenv(envArchiveCaptureMinFreeBytes, "1")
				t.Setenv(envArchiveConversionMinFreeBytes, "1")
				if kind == "capture" {
					t.Setenv(envArchiveCaptureMaxBytes, "4096")
				} else if kind == "backlog" {
					t.Setenv(envArchiveBacklogMaxBytes, "8192")
				} else {
					t.Setenv(envArchiveConversionMaxBytes, "1")
				}
				store, run := archiveFailureFixture(t)
				// Initialize the runner before introducing a live row: startup
				// reconciliation correctly fails orphaned pre-existing executions.
				tr := NewTestRunner(1, nil, store)
				run.Status = "RUNNING"
				run.EndedAt = nil
				if err := store.SaveTest(&storage.TestRecord{ID: run.ID, Name: run.Name, ProjectID: run.ProjectID, StartTime: run.CreatedAt, Status: run.Status}); err != nil {
					t.Fatal(err)
				}
				tr.tests[run.ID] = run
				if row, err := store.GetTest(run.ID); err != nil || row.Status != "RUNNING" || row.EndTime != nil {
					t.Fatalf("fixture is not live: %+v %v", row, err)
				}
				writer, err := newRunArchiveWriter(run, RunArchiveProvenance{})
				if err != nil {
					t.Fatal(err)
				}
				if err := writer.RecordResult(&protocol.Result{ActionName: "accepted"}); err != nil {
					t.Fatal(err)
				}
				if kind == "capture" || kind == "backlog" {
					for range 100 {
						err = writer.RecordResult(&protocol.Result{ActionName: strings.Repeat("x", 200)})
						if err != nil {
							break
						}
					}
					if kind == "capture" && !errors.Is(err, errArchiveCaptureLimit) {
						t.Fatal(err)
					}
				}
				end := time.Now().UTC()
				originalError := ""
				if outcome == "FAILED" {
					originalError = "fixture engine build failed"
				}
				unlock := tr.lockArchiveCommitFence(run.ID)
				defer unlock()
				// Abort can become durable before the recorder finishes flushing. Exercise
				// that path as well as normal finalization followed by terminal commit.
				if outcome == "ABORTED" {
					if err := tr.completeTestRunUnderArchiveFence(run, outcome, end, "", nil); err != nil {
						t.Fatal(err)
					}
				}
				artifacts := &runArtifacts{runner: tr, recorder: writer}
				record, status, why := artifacts.finalize(run, outcome, end, originalError)
				if record != nil || status != outcome || why != originalError {
					t.Fatal(record, status, why)
				}
				if outcome != "ABORTED" {
					if err := tr.completeTestRunUnderArchiveFence(run, status, end, why, record); err != nil {
						t.Fatal(err)
					}
				}
				failure, err := store.GetRunArchiveFailure(run.ID)
				if err != nil || failure.Reason != storage.RunArchiveFailureLocalFinalization || failure.Status != outcome {
					t.Fatalf("failure=%+v error=%v", failure, err)
				}
				row, err := store.GetTest(run.ID)
				if err != nil || row.Status != outcome || row.Error != originalError {
					t.Fatalf("outcome=%+v %v", row, err)
				}
				restarted := NewTestRunner(1, nil, store)
				got, ok := restarted.GetTest(run.ID)
				if !ok || got.ArchiveFailure == nil || got.ArchiveFailure.Reason != storage.RunArchiveFailureLocalFinalization {
					t.Fatalf("restart lost failure: %+v", got)
				}
				raw, err := os.ReadFile(filepath.Join(writer.rootDir, "transactions.jsonl"))
				if err != nil || !strings.Contains(string(raw), "accepted") {
					t.Fatal("accepted source was lost", err)
				}
				if _, err := os.Stat(writer.manifestPath); !os.IsNotExist(err) {
					t.Fatal("failed local archive has a complete manifest", err)
				}
			})
		}
	}
}

type localArchiveRetryStore struct {
	*scriptedTestPersistence
	failures *storage.SQLiteStore
}

func (s *localArchiveRetryStore) SaveRunArchiveFailure(f *storage.RunArchiveFailure, lease, owner string) (bool, error) {
	return s.failures.SaveRunArchiveFailure(f, lease, owner)
}
func (s *localArchiveRetryStore) GetRunArchiveFailure(id string) (*storage.RunArchiveFailure, error) {
	return s.failures.GetRunArchiveFailure(id)
}

func TestLocalArchiveFailureWaitsForTerminalRetryAndEmitsOnce(t *testing.T) {
	store, run := archiveFailureFixture(t)
	hub := NewHub()
	tr := NewTestRunner(1, hub, store)
	run.Status = "RUNNING"
	run.EndedAt = nil
	if err := store.SaveTest(&storage.TestRecord{ID: run.ID, Name: run.Name, ProjectID: run.ProjectID, StartTime: run.CreatedAt, Status: run.Status}); err != nil {
		t.Fatal(err)
	}
	tr.tests[run.ID] = run
	run.localArchiveFinalizationFailed = true
	outage := errors.New("temporary terminal-write outage")
	wrapper := &localArchiveRetryStore{scriptedTestPersistence: &scriptedTestPersistence{testPersistence: store, err: outage, failUpdateCall: failCalls(1, durableTransitionAttempts)}, failures: store}
	tr.testStore = wrapper
	t.Cleanup(func() {
		tr.mu.Lock()
		tr.shuttingDown = true
		tr.mu.Unlock()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			tr.terminalRetryMu.Lock()
			active := len(tr.terminalRetrying)
			tr.terminalRetryMu.Unlock()
			if active == 0 {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Error("terminal retry worker did not stop")
	})
	unlock := tr.lockArchiveCommitFence(run.ID)
	err := tr.completeTestRunUnderArchiveFence(run, "COMPLETE", time.Now().UTC(), "", nil)
	if !errors.Is(err, outage) {
		unlock()
		t.Fatal("terminal write did not fail", err)
	}
	row, err := store.GetTest(run.ID)
	if err != nil || row.Status != "RUNNING" {
		unlock()
		t.Fatal("failed write changed durable outcome", row, err)
	}
	if events := hubEventTypes(hub); containsEvent(events, "test_archive_failed") {
		unlock()
		t.Fatal("failure was advertised before terminal commit", events)
	}
	unlock()
	deadline := time.Now().Add(3 * time.Second)
	events := []string{}
	for {
		events = append(events, hubEventTypes(hub)...)
		tr.mu.RLock()
		complete := run.Status == "COMPLETE" && !run.localArchiveFinalizationFailed
		tr.mu.RUnlock()
		tr.terminalRetryMu.Lock()
		active := len(tr.terminalRetrying)
		tr.terminalRetryMu.Unlock()
		if complete && active == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("local failure was lost during terminal retry", events)
		}
		time.Sleep(5 * time.Millisecond)
	}
	events = append(events, hubEventTypes(hub)...)
	if err := tr.completeTestRun(run, "COMPLETE", time.Now().UTC(), "", nil); err != nil {
		t.Fatal(err)
	}
	events = append(events, hubEventTypes(hub)...)
	for _, kind := range []string{"test_complete", "test_archive_failed"} {
		count := 0
		for _, event := range events {
			if event == kind {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("%s emitted %d times: %v", kind, count, events)
		}
	}
	failure, err := store.GetRunArchiveFailure(run.ID)
	if err != nil || failure.Reason != storage.RunArchiveFailureLocalFinalization || failure.Status != "COMPLETE" {
		t.Fatal(failure, err)
	}
}
