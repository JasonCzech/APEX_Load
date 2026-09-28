package storage

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestIdleExecutionLeaseRelease(t *testing.T) {
	checkIdleExecutionLeaseRelease(t, newHAFencingStore(t))
}

func checkIdleExecutionLeaseRelease(t *testing.T, s *SQLiteStore) {
	t.Helper()
	const name = "idle-recovery"
	acquire := func(owner string) *SchedulerLease {
		t.Helper()
		ok, lease, err := s.TryAcquireSchedulerLeaseTerm(name, owner, time.Now(), time.Minute)
		if err != nil || !ok {
			t.Fatalf("acquire: %v %v", ok, err)
		}
		return lease
	}
	for i, status := range []string{"RUNNING", "INITIALIZING", "SHAKEOUT", "PAUSED", "STOPPING", "STARTING", "unknown", "", "DRAFT", "PENDING", "QUEUED", "SHAKEOUT_REVIEW", "COMPLETE", "FAILED", "ABORTED"} {
		t.Run(status, func(t *testing.T) {
			owner := fmt.Sprintf("owner-%d", i)
			term := acquire(owner)
			record := &TestRecord{ID: "idle-test", Name: "idle-test", Status: status, StartTime: time.Now(), Groups: []any{}}
			if err := s.SaveTest(record); err != nil {
				t.Fatal(err)
			}
			// Even an otherwise-idle database cannot release the wrong term.
			for _, stale := range []struct {
				owner      string
				generation uint64
			}{{"stale", term.Generation}, {owner, term.Generation + 1}} {
				ok, err := s.ReleaseSchedulerLeaseTermIfTestsIdle(name, stale.owner, stale.generation)
				if err != nil || ok {
					t.Fatalf("stale release: %v %v", ok, err)
				}
			}
			released, err := s.ReleaseSchedulerLeaseTermIfTestsIdle(" "+name+" ", " "+owner+" ", term.Generation)
			want := i >= 8
			if err != nil || released != want {
				t.Fatalf("release %s: %v %v, want %v", status, released, err, want)
			}
			lease, err := s.GetSchedulerLease(name)
			if err != nil || lease.Generation != term.Generation || lease.OwnerID != owner {
				t.Fatalf("term changed: %#v %v", lease, err)
			}
			if lease.ExpiresAt.After(lease.UpdatedAt) == want {
				t.Fatalf("wrong expiry: %#v", lease)
			}
			if err := s.ReleaseSchedulerLease(name, owner); err != nil {
				t.Fatal(err)
			}
		})
	}
	if ok, err := s.ReleaseSchedulerLeaseTermIfTestsIdle(name, "invalid", 0); err == nil || ok {
		t.Fatalf("zero generation: %v %v", ok, err)
	}
	if err := s.DeleteTest("idle-test"); err != nil {
		t.Fatal(err)
	}
	// Compete with actual fenced start commits. There must never be both a
	// released term and a committed executable test under that term.
	for i := 0; i < 12; i++ {
		owner := fmt.Sprintf("race-%d", i)
		term := acquire(owner)
		gate := make(chan struct{})
		var wg sync.WaitGroup
		var started, released bool
		var startErr, releaseErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-gate
			started, startErr = s.CreateTestIfAbsentIfLeaseTermHeld(&TestRecord{ID: "racing-start", Name: "racing-start", Status: "INITIALIZING", StartTime: time.Now(), Groups: []any{}}, name, owner, term.Generation)
		}()
		go func() {
			defer wg.Done()
			<-gate
			released, releaseErr = s.ReleaseSchedulerLeaseTermIfTestsIdle(name, owner, term.Generation)
		}()
		close(gate)
		wg.Wait()
		if startErr != nil || releaseErr != nil || started == released {
			t.Fatalf("start=%v/%v release=%v/%v", started, startErr, released, releaseErr)
		}
		if err := s.DeleteTest("racing-start"); err != nil {
			t.Fatal(err)
		}
		if err := s.ReleaseSchedulerLease(name, owner); err != nil {
			t.Fatal(err)
		}
	}
	old := acquire("old")
	if err := s.ReleaseSchedulerLease(name, "old"); err != nil {
		t.Fatal(err)
	}
	newTerm := acquire("successor")
	if newTerm.Generation <= old.Generation {
		t.Fatal("generation did not advance")
	}
	if ok, err := s.ReleaseSchedulerLeaseTermIfTestsIdle(name, "old", old.Generation); ok || err != nil {
		t.Fatalf("stale successor release: %v %v", ok, err)
	}
}

func TestIdleExecutionLeaseArchiveGuards(t *testing.T) {
	checkIdleExecutionLeaseArchiveGuards(t, newHAFencingStore(t))
}

func checkIdleExecutionLeaseArchiveGuards(t *testing.T, s *SQLiteStore) {
	t.Helper()
	for _, mode := range []string{"collection", "publication", "resolved", "exhausted", "stale-incarnation"} {
		t.Run(mode, func(t *testing.T) {
			id := "idle-archive-" + mode
			owner := "owner-" + mode
			ok, term, err := s.TryAcquireSchedulerLeaseTerm(id, owner, time.Now(), time.Minute)
			if err != nil || !ok {
				t.Fatal(ok, err)
			}
			now := time.Now().UTC()
			start := now.Add(-time.Minute)
			row := &TestRecord{ID: id, Name: id, ProjectID: DefaultProjectID, Status: "RUNNING", StartTime: start}
			if err := s.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			if mode != "publication" {
				_, prepared, err := s.PrepareRunArchiveCollection(id, row.ProjectID, start, map[string]string{"source": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, id, owner)
				if err != nil || !prepared {
					t.Fatal(prepared, err)
				}
			}
			if err := s.UpdateTestStatus(id, "COMPLETE", &now, ""); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "publication":
				job := &ArchiveFinalizationJob{TestID: id, ProjectID: row.ProjectID, ArchiveRecord: RunArchiveRecord{TestID: id, CreatedAt: start, CompletedAt: now, ArchivePath: "/archive/" + id, ManifestPath: "/archive/" + id + "/manifest.json"}}
				if err := s.SaveArchiveFinalizationJob(job); err != nil {
					t.Fatal(err)
				}
			case "resolved":
				if _, err := s.db.Exec(`UPDATE run_archive_collection_intents SET resolved=1 WHERE test_id=?`, id); err != nil {
					t.Fatal(err)
				}
			case "exhausted":
				if _, err := s.db.Exec(`UPDATE run_archive_collection_intents SET exhausted=1 WHERE test_id=?`, id); err != nil {
					t.Fatal(err)
				}
			case "stale-incarnation":
				if _, err := s.db.Exec(`UPDATE run_archive_collection_intents SET test_start_time=? WHERE test_id=?`, start.Add(-time.Hour), id); err != nil {
					t.Fatal(err)
				}
			}
			released, err := s.ReleaseSchedulerLeaseTermIfTestsIdle(id, owner, term.Generation)
			want := mode != "collection" && mode != "publication"
			if err != nil || released != want {
				t.Fatalf("release=%v %v, want %v", released, err, want)
			}
			if !want {
				// Retiring the bookkeeping after publication permits the next attempt.
				if err := s.DeleteArchiveFinalizationJob(id); err != nil {
					t.Fatal(err)
				}
				if _, err := s.db.Exec(`UPDATE run_archive_collection_intents SET resolved=1 WHERE test_id=?`, id); err != nil {
					t.Fatal(err)
				}
				if released, err := s.ReleaseSchedulerLeaseTermIfTestsIdle(id, owner, term.Generation); err != nil || !released {
					t.Fatalf("finished archive blocked release: %v %v", released, err)
				}
			}
			if err := s.DeleteTest(id); err != nil {
				t.Fatal(err)
			}
		})
	}
}
