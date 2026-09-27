package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestFailClosedEvacuationRecordsArchiveOutcome(t *testing.T) {
	for _, mode := range []string{"missing", "collection error", "collected"} {
		t.Run(mode, func(t *testing.T) {
			s, run := archiveFailureFixture(t)
			run.Status, run.EndedAt = "STOPPING", nil
			run.Error = "generator evacuation state is uncertain"
			if err := s.SaveTest(&storage.TestRecord{ID: run.ID, Name: run.Name, ProjectID: run.ProjectID, StartTime: run.CreatedAt, Status: run.Status}); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			if mode == "collection error" {
				root = filepath.Join(root, "not-directory")
				if err := os.WriteFile(root, []byte("file"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			var client generatorClient = &startupLifecycleFakeClient{accept: true}
			if mode == "collected" {
				client = coverageArchiveClient(t, run.ID)
			}
			run.remoteClientsByTarget = map[string]generatorClient{"generator-a": client}
			runner := NewTestRunner(1, nil, s)
			runner.tests[run.ID] = run
			backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, nil)
			err := backend.finalizeFailClosedEvacuation(runner, run)
			if (err != nil) != (mode == "collection error") {
				t.Fatalf("finalize: %v", err)
			}
			stored, err := s.GetTest(run.ID)
			if err != nil || stored.Status != "FAILED" || stored.Error != run.Error {
				t.Fatalf("terminal outcome: %+v %v", stored, err)
			}
			restarted := NewTestRunner(1, nil, s)
			got, ok := restarted.GetTestSummary(run.ID)
			if !ok {
				t.Fatal("missing restarted run")
			}
			if mode == "collected" {
				if got.Archive == nil || got.ArchiveFailure != nil {
					t.Fatalf("published archive: %+v", got)
				}
				if _, err := s.GetRunArchiveFailure(run.ID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("unexpected failure: %v", err)
				}
				return
			}
			reason := storage.RunArchiveFailureRemoteUnavailable
			if mode == "collection error" {
				reason = storage.RunArchiveFailureRemoteCollection
			}
			if got.ArchiveFailure == nil || got.ArchiveFailure.Reason != reason {
				t.Fatalf("failure after restart: %+v", got.ArchiveFailure)
			}
			if got.Status != "FAILED" || got.Error != run.Error {
				t.Fatalf("changed terminal outcome: %+v", got)
			}
		})
	}
}

func TestFailClosedEvacuationPreservesCommittedArchiveIdentity(t *testing.T) {
	s, run := archiveFailureFixture(t)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	originalEnd := *run.EndedAt
	run.Error = "original terminal reason"
	if err := s.SaveTest(&storage.TestRecord{ID: run.ID, Name: run.Name, ProjectID: run.ProjectID, StartTime: run.CreatedAt, EndTime: run.EndedAt, Status: run.Status, Error: run.Error}); err != nil {
		t.Fatal(err)
	}
	client := coverageArchiveClient(t, run.ID)
	run.remoteClientsByTarget = map[string]generatorClient{"generator-a": client}
	runner := NewTestRunner(1, nil, s)
	runner.tests[run.ID] = run
	backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, nil)
	if err := backend.finalizeFailClosedEvacuation(runner, run); err != nil {
		t.Fatal(err)
	}
	if run.Archive == nil {
		t.Fatal("no archive")
	}
	report, err := buildRunArchiveReport(run.Archive)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Manifest.CompletedAt.Equal(originalEnd) || report.Manifest.Error != run.Error || report.Manifest.Status != "FAILED" {
		t.Fatalf("manifest changed committed outcome: %+v", report.Manifest)
	}
	client.mu.Lock()
	before := client.streamCalls
	client.mu.Unlock()
	if err := backend.finalizeFailClosedEvacuation(runner, run); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	after := client.streamCalls
	client.mu.Unlock()
	if before != after {
		t.Fatal("duplicate callback recollected a published archive")
	}
	if !run.EndedAt.Equal(originalEnd) {
		t.Fatal("completion timestamp changed")
	}
}

func TestFailClosedEvacuationRejectsSupersededRun(t *testing.T) {
	for _, mode := range []string{"replaced", "deleted", "aborted", "complete", "running"} {
		t.Run(mode, func(t *testing.T) {
			s, run := archiveFailureFixture(t)
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			client := coverageArchiveClient(t, run.ID)
			run.remoteClientsByTarget = map[string]generatorClient{"generator-a": client}
			runner := NewTestRunner(1, nil, s)
			runner.tests[run.ID] = run
			switch mode {
			case "replaced":
				runner.tests[run.ID] = &ManagedTest{ID: run.ID, Status: "RUNNING"}
			case "deleted":
				delete(runner.tests, run.ID)
			case "aborted":
				run.Status = "ABORTED"
			case "complete":
				run.Status = "COMPLETE"
			case "running":
				run.Status = "RUNNING"
			}
			backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, nil)
			if err := backend.finalizeFailClosedEvacuation(runner, run); err != nil {
				t.Fatal(err)
			}
			client.mu.Lock()
			calls := client.streamCalls
			client.mu.Unlock()
			if calls != 0 {
				t.Fatal("superseded callback collected an archive")
			}
			if _, err := s.GetRunArchive(run.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("superseded callback published archive: %v", err)
			}
			if _, err := s.GetRunArchiveFailure(run.ID); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("superseded callback recorded failure: %v", err)
			}
		})
	}
}

type evacuationTerminalGateStore struct {
	*storage.SQLiteStore
	blocked atomic.Bool
}

func (s *evacuationTerminalGateStore) UpdateTestStatusIfActive(id, status string, end *time.Time, reason string) (storage.TestStatusTransitionResult, error) {
	if s.blocked.Load() {
		return "", errors.New("terminal database unavailable")
	}
	return s.SQLiteStore.UpdateTestStatusIfActive(id, status, end, reason)
}

func TestFailClosedEvacuationRetainsSourcesUntilTerminalCommit(t *testing.T) {
	s, run := archiveFailureFixture(t)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	run.Status, run.EndedAt = "STOPPING", nil
	if err := s.SaveTest(&storage.TestRecord{ID: run.ID, Name: run.Name, ProjectID: run.ProjectID, StartTime: run.CreatedAt, Status: run.Status}); err != nil {
		t.Fatal(err)
	}
	gate := &evacuationTerminalGateStore{SQLiteStore: s}
	gate.blocked.Store(true)
	client := coverageArchiveClient(t, run.ID)
	// The first interval must retain its own target after terminal compaction
	// drops all but the last live-history point.
	interval := IntervalSummaryRecord{Timestamp: time.Now().UTC().Add(-2 * time.Second).Truncate(time.Second), TestID: run.ID, TotalTx: 1, TPS: 1}
	data, err := json.Marshal(interval)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(client.artifacts["interval_summaries.jsonl"], append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	run.History = []*LiveMetrics{{Timestamp: interval.Timestamp, TargetTPS: 17}, {Timestamp: interval.Timestamp.Add(time.Second), TargetTPS: 29}}
	run.remoteClientsByTarget = map[string]generatorClient{"generator-a": client}
	runner := NewTestRunner(1, nil, gate)
	runner.tests[run.ID] = run
	t.Cleanup(func() {
		gate.blocked.Store(false)
		runner.mu.Lock()
		runner.shuttingDown = true
		runner.mu.Unlock()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			runner.terminalRetryMu.Lock()
			pending := len(runner.terminalRetrying)
			runner.terminalRetryMu.Unlock()
			if pending == 0 {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Error("terminal retries did not finish")
	})
	backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, nil)
	if err := backend.finalizeFailClosedEvacuation(runner, run); err == nil {
		t.Fatal("expected terminal persistence error")
	}
	client.mu.Lock()
	calls := client.streamCalls
	client.mu.Unlock()
	client.startupLifecycleFakeClient.mu.Lock()
	closed := client.closed
	client.startupLifecycleFakeClient.mu.Unlock()
	if calls != 0 || closed != 0 {
		t.Fatalf("sources consumed before commit: calls=%d closed=%d", calls, closed)
	}
	if _, err := s.GetArchiveFinalizationJob(run.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("premature publication: %v", err)
	}
	if err := backend.finalizeFailClosedEvacuation(runner, run); err != nil {
		t.Fatalf("duplicate callback should leave collection to the existing retry: %v", err)
	}
	gate.blocked.Store(false)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		runner.mu.RLock()
		archive := run.Archive
		runner.mu.RUnlock()
		if archive != nil {
			stored, err := s.GetTest(run.ID)
			if err != nil || stored.Status != "FAILED" || stored.EndTime == nil {
				t.Fatalf("terminal row: %+v %v", stored, err)
			}
			report, err := buildRunArchiveReport(archive)
			if err != nil {
				t.Fatal(err)
			}
			if !report.Manifest.CompletedAt.Equal(*stored.EndTime) {
				t.Fatal("retry archive does not match durable terminal identity")
			}
			file, err := os.Open(filepath.Join(filepath.Dir(archive.ManifestPath), report.Manifest.IntervalSummariesFile))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			var merged IntervalSummaryRecord
			if err := json.NewDecoder(file).Decode(&merged); err != nil {
				t.Fatal(err)
			}
			if merged.TargetTPS != 17 {
				t.Fatalf("lost pre-compaction interval target: %+v", merged)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("archive collection did not resume after terminal commit")
}

func TestFailClosedEvacuationDoesNotRecollectPublishedEvidence(t *testing.T) {
	for _, mode := range []string{"outbox", "catalog"} {
		t.Run(mode, func(t *testing.T) {
			s, run := archiveFailureFixture(t)
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			record := storage.RunArchiveRecord{TestID: run.ID, CreatedAt: run.CreatedAt, CompletedAt: *run.EndedAt, TransactionCount: 37}
			if mode == "outbox" {
				if err := s.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: run.ID, ProjectID: run.ProjectID, ArchiveRecord: record}); err != nil {
					t.Fatal(err)
				}
			} else if err := s.SaveRunArchive(&record); err != nil {
				t.Fatal(err)
			}
			client := coverageArchiveClient(t, run.ID)
			run.remoteClientsByTarget = map[string]generatorClient{"generator-a": client}
			runner := NewTestRunner(1, nil, s)
			runner.tests[run.ID] = run
			backend := newDistributedExecutionBackend([]string{"generator-a"}, time.Second, nil)
			if err := backend.finalizeFailClosedEvacuation(runner, run); err != nil {
				t.Fatal(err)
			}
			client.mu.Lock()
			calls := client.streamCalls
			client.mu.Unlock()
			if calls != 0 {
				t.Fatal("recollected evidence already owned by publication")
			}
			if mode == "outbox" {
				job, err := s.GetArchiveFinalizationJob(run.ID)
				if err != nil || job.ArchiveRecord.TransactionCount != 37 {
					t.Fatalf("publication job replaced: %+v %v", job, err)
				}
			} else {
				got, err := s.GetRunArchive(run.ID)
				if err != nil || got.TransactionCount != 37 {
					t.Fatalf("archive catalog replaced: %+v %v", got, err)
				}
			}
		})
	}
}
