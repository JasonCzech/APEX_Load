package storage

import (
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestArchiveOrphanIncarnationFence(t *testing.T) {
	checkArchiveOrphanIncarnationFence(t, newHAFencingStore(t))
}
func checkArchiveOrphanIncarnationFence(t *testing.T, s *SQLiteStore) {
	t.Helper()
	const lease, owner = "orphan-recovery", "current"
	if held, err := s.TryAcquireSchedulerLease(lease, owner, time.Now(), time.Minute); err != nil || !held {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	row := &TestRecord{ID: "orphan-fenced", Name: "orphan", ProjectID: DefaultProjectID, StartTime: now.Add(-time.Minute), Status: "RUNNING"}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	intent, ok, err := s.PrepareRunArchiveCollection(row.ID, row.ProjectID, row.StartTime, map[string]string{"a": strings.Repeat("a", 64)}, lease, owner)
	if err != nil || !ok {
		t.Fatal(err)
	}
	record := RunArchiveRecord{TestID: row.ID, CreatedAt: row.StartTime, CompletedAt: now, ArchivePath: "/archive/run", ManifestPath: "/archive/run/manifest.json"}
	job := func(record RunArchiveRecord) *ArchiveFinalizationJob {
		return &ArchiveFinalizationJob{TestID: row.ID, ProjectID: row.ProjectID, ArchiveRecord: record}
	}
	if ok, err := s.SaveArchiveFinalizationJobIfOrphanForRun(job(record), record.CreatedAt, "COMPLETE", lease, owner); err != nil || ok {
		t.Fatal("active run published", err)
	}
	if _, err := s.UpdateTestStatusIfActive(row.ID, "COMPLETE", &now, ""); err != nil {
		t.Fatal(err)
	}
	wrongStart, wrongEnd := record, record
	wrongStart.CreatedAt = wrongStart.CreatedAt.Add(time.Second)
	wrongEnd.CompletedAt = wrongEnd.CompletedAt.Add(time.Second)
	for _, tc := range []struct {
		record      RunArchiveRecord
		status, who string
	}{{wrongStart, "COMPLETE", owner}, {wrongEnd, "COMPLETE", owner}, {record, "FAILED", owner}, {record, "COMPLETE", "stale"}} {
		if ok, err := s.SaveArchiveFinalizationJobIfOrphanForRun(job(tc.record), tc.record.CreatedAt, tc.status, lease, tc.who); err != nil || ok {
			t.Fatalf("stale recovery accepted: %v %v", ok, err)
		}
	}
	if _, err := s.GetArchiveFinalizationJob(row.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("rejected recovery left outbox")
	}
	// The payload creation time is not the controller request identity.
	record.CreatedAt = record.CreatedAt.Add(2 * time.Second)
	requestedAt := row.StartTime
	var wg sync.WaitGroup
	results := make(chan bool, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Go(func() {
			ok, err := s.SaveArchiveFinalizationJobIfOrphanForRun(job(record), requestedAt, "COMPLETE", lease, owner)
			results <- ok
			errs <- err
		})
	}
	wg.Wait()
	close(results)
	close(errs)
	wins := 0
	for ok := range results {
		if ok {
			wins++
		}
	}
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if wins != 1 {
		t.Fatalf("recovery winners=%d", wins)
	}
	got, err := s.GetRunArchiveCollection(row.ID)
	if err != nil || !got.Resolved || got.IntentToken != intent.IntentToken {
		t.Fatal("recovery did not resolve exact collection", err)
	}
	if err := s.DeleteArchiveFinalizationJob(row.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SaveArchiveFinalizationJobIfOrphanForRun(job(record), requestedAt, "COMPLETE", lease, owner); err != nil || ok {
		t.Fatal("resolved collection resurrected", err)
	}
	if ok, err := s.SaveArchiveFinalizationJobIfOrphan(job(record)); err != nil || ok {
		t.Fatal("legacy producer resurrected resolved collection", err)
	}
	if err := s.DeleteTest(row.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SaveArchiveFinalizationJobIfOrphanForRun(job(record), requestedAt, "COMPLETE", lease, owner); err != nil || ok {
		t.Fatal("deleted run resurrected", err)
	}
	row.StartTime = row.StartTime.Add(time.Second)
	row.Status = "COMPLETE"
	row.EndTime = &now
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SaveArchiveFinalizationJobIfOrphanForRun(job(record), requestedAt, "COMPLETE", lease, owner); err != nil || ok {
		t.Fatal("replacement run inherited orphan", err)
	}
}

func TestArchiveOrphanIncarnationRequiresIdentity(t *testing.T) {
	s := newHAFencingStore(t)
	for _, job := range []*ArchiveFinalizationJob{nil, {}, {ArchiveRecord: RunArchiveRecord{CreatedAt: time.Now()}}, {ArchiveRecord: RunArchiveRecord{CompletedAt: time.Now()}}} {
		if ok, err := s.SaveArchiveFinalizationJobIfOrphanForRun(job, time.Now(), "COMPLETE", "", ""); err == nil || ok {
			t.Fatal("missing recovery identity accepted")
		}
	}
	job := &ArchiveFinalizationJob{ArchiveRecord: RunArchiveRecord{CreatedAt: time.Now(), CompletedAt: time.Now()}}
	if ok, err := s.SaveArchiveFinalizationJobIfOrphanForRun(job, time.Time{}, "COMPLETE", "", ""); err == nil || ok {
		t.Fatal("zero request time accepted")
	}
	for _, tc := range [][3]string{{"RUNNING", "", ""}, {"COMPLETE", "lease", ""}, {"COMPLETE", "", "owner"}} {
		if ok, err := s.SaveArchiveFinalizationJobIfOrphanForRun(job, time.Now(), tc[0], tc[1], tc[2]); err == nil || ok {
			t.Fatal("invalid recovery fence accepted")
		}
	}
}
