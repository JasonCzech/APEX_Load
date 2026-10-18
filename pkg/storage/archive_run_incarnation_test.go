package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func TestArchiveOrphanExplicitIncarnation(t *testing.T) {
	checkArchiveOrphanExplicitIncarnation(t, newHAFencingStore(t))
}

func checkArchiveOrphanExplicitIncarnation(t *testing.T, s *SQLiteStore) {
	t.Helper()
	for _, mode := range []string{"same", "different", "missing archive token", "missing durable token", "legacy exact", "legacy different end", "pending job", "changed start", "changed status", "expired owner"} {
		t.Run(mode, func(t *testing.T) {
			id := fmt.Sprintf("explicit-orphan-%d", time.Now().UnixNano())
			start := time.Now().UTC().Add(-time.Minute)
			durableEnd, archiveEnd := start.Add(30*time.Second), start.Add(29*time.Second)
			durableToken, archiveToken := "current-run", "current-run"
			switch mode {
			case "different":
				archiveToken = "older-run"
				archiveEnd = durableEnd
			case "missing archive token":
				archiveToken = ""
			case "missing durable token":
				durableToken = ""
			case "legacy exact":
				durableToken, archiveToken = "", ""
				archiveEnd = durableEnd
			case "legacy different end":
				durableToken, archiveToken = "", ""
			}
			row := &TestRecord{ID: id, Name: id, ProjectID: DefaultProjectID, Status: "FAILED", StartTime: start, EndTime: &durableEnd, InitialProvenance: map[string]any{"run_incarnation": durableToken}}
			if err := s.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			record := RunArchiveRecord{TestID: row.ID, RunIncarnation: archiveToken, CreatedAt: start.Add(time.Second), CompletedAt: archiveEnd, ArchivePath: "/archive/run", ManifestPath: "/archive/run/manifest.json"}
			job := &ArchiveFinalizationJob{TestID: row.ID, ProjectID: row.ProjectID, ArchiveRecord: record}
			requestedAt, status, lease, owner := start, row.Status, "", ""
			switch mode {
			case "pending job":
				existing := *job
				existing.ArchiveRecord.CompletedAt = durableEnd
				if err := s.SaveArchiveFinalizationJob(&existing); err != nil {
					t.Fatal(err)
				}
			case "changed start":
				requestedAt = start.Add(time.Second)
			case "changed status":
				status = "COMPLETE"
			case "expired owner":
				lease, owner = "execution", "old"
				if ok, err := s.TryAcquireSchedulerLease(lease, "current", time.Now(), time.Minute); err != nil || !ok {
					t.Fatal(err)
				}
			}
			beforeRow, err := s.GetTest(row.ID)
			if err != nil {
				t.Fatal(err)
			}
			var beforeJob *ArchiveFinalizationJob
			if mode == "pending job" {
				beforeJob, err = s.GetArchiveFinalizationJob(row.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			accepted := mode == "same" || mode == "legacy exact"
			ok, err := s.SaveArchiveFinalizationJobIfOrphanForRun(job, requestedAt, status, lease, owner)
			if err != nil || ok != accepted {
				t.Fatalf("accepted=%t want=%t err=%v", ok, accepted, err)
			}
			current, err := s.GetTest(row.ID)
			if err != nil {
				t.Fatal(err)
			}
			wantEnd := durableEnd
			if accepted {
				wantEnd = archiveEnd
			}
			if current.EndTime == nil || !archiveIdentityTimestampEqual(s.db.dialect, *current.EndTime, wantEnd) {
				t.Fatalf("durable end=%v want=%v", current.EndTime, wantEnd)
			}
			if !accepted && !reflect.DeepEqual(beforeRow, current) {
				t.Fatalf("rejected recovery mutated run: before=%+v after=%+v", beforeRow, current)
			}
			stored, err := s.GetArchiveFinalizationJob(row.ID)
			if accepted {
				if err != nil || stored.ArchiveRecord.RunIncarnation != archiveToken {
					t.Fatalf("outbox lost incarnation: %+v %v", stored, err)
				}
			} else if mode == "pending job" {
				if err != nil || !reflect.DeepEqual(beforeJob, stored) {
					t.Fatalf("rejected recovery mutated pending intent: before=%+v after=%+v err=%v", beforeJob, stored, err)
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("rejected recovery created job: %v", err)
			}
		})
	}
}

func TestArchiveFinalizationCASRejectsReplacedIncarnation(t *testing.T) {
	checkArchiveFinalizationCASRejectsReplacedIncarnation(t, newHAFencingStore(t))
}

func checkArchiveFinalizationCASRejectsReplacedIncarnation(t *testing.T, s *SQLiteStore) {
	t.Helper()
	now := time.Now().UTC()
	row := &TestRecord{ID: fmt.Sprintf("explicit-replaced-%d", now.UnixNano()), Name: "same-id", ProjectID: DefaultProjectID, Status: "COMPLETE", StartTime: now.Add(-time.Minute), EndTime: &now, InitialProvenance: map[string]any{"run_incarnation": "first"}}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	record := RunArchiveRecord{TestID: row.ID, CreatedAt: row.StartTime, CompletedAt: now, ArchivePath: "/archive/run", ManifestPath: "/archive/run/manifest.json"}
	job := &ArchiveFinalizationJob{TestID: row.ID, ProjectID: row.ProjectID, ArchiveRecord: record}
	if err := s.SaveArchiveFinalizationJob(job); err != nil {
		t.Fatal(err)
	}
	identity := ArchiveFinalizationRunIdentity{RunIncarnation: "first", ProjectID: row.ProjectID, StartTime: row.StartTime, EndTime: now, Status: row.Status}
	// Simulate replacement between inspected manifest and commit, with every
	// timestamp preserved so only the immutable token distinguishes the run.
	if _, err := s.db.Exec(`UPDATE tests SET initial_provenance_json = ? WHERE id = ?`, `{"run_incarnation":"replacement"}`, row.ID); err != nil {
		t.Fatal(err)
	}
	beforeRow, err := s.GetTest(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	beforeJob, err := s.GetArchiveFinalizationJob(row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.CompleteArchiveFinalizationForRun(row.ID, job.IntentToken, "", &record, identity); err != nil || ok {
		t.Fatalf("stale publication committed: %t %v", ok, err)
	}
	if _, err := s.GetRunArchive(row.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale catalog: %v", err)
	}
	afterJob, err := s.GetArchiveFinalizationJob(row.ID)
	if err != nil || !reflect.DeepEqual(beforeJob, afterJob) {
		t.Fatalf("rejected publication mutated pending intent: before=%+v after=%+v err=%v", beforeJob, afterJob, err)
	}
	afterRow, err := s.GetTest(row.ID)
	if err != nil || !reflect.DeepEqual(beforeRow, afterRow) {
		t.Fatalf("rejected publication mutated run: before=%+v after=%+v err=%v", beforeRow, afterRow, err)
	}
}

func TestTestRunIncarnationRejectsInvalidIdentity(t *testing.T) {
	for _, value := range []any{map[string]any{"run_incarnation": 42}, map[string]any{"run_incarnation": []string{"bad"}}, map[string]any{"run_incarnation": map[string]any{}}, []string{"bad"}} {
		if _, err := TestRunIncarnation(value); err == nil {
			t.Fatalf("accepted invalid identity: %#v", value)
		}
	}
	for _, value := range []any{nil, map[string]any{}, map[string]any{"unrelated": true}} {
		if token, err := TestRunIncarnation(value); err != nil || token != "" {
			t.Fatalf("legacy identity=%q %v", token, err)
		}
	}
	// Unrelated receipt fields must not be serialized by the scalar map reader.
	if token, err := TestRunIncarnation(map[string]any{"run_incarnation": "admitted", "unrelated": make(chan struct{})}); err != nil || token != "admitted" {
		t.Fatalf("scalar identity read traversed unrelated provenance: %q %v", token, err)
	}
}
