package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	now := time.Now().In(time.FixedZone("EDT", -4*60*60))
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
	requestedAt := row.StartTime.UTC()
	record.CompletedAt = record.CompletedAt.UTC()
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

func TestArchiveOrphanPreparedManifestTravelsWithFencedJob(t *testing.T) {
	checkArchiveOrphanPreparedManifest(t, newHAFencingStore(t))
}

// Failed-over recovery commits a restamped manifest with the fenced job. Only
// bytes matching the record's trust anchor are accepted, and they survive the
// insert for finalization to restore.
func checkArchiveOrphanPreparedManifest(t *testing.T, s *SQLiteStore) {
	t.Helper()
	now := time.Now().UTC()
	row := &TestRecord{ID: "orphan-prepared", Name: "orphan", ProjectID: DefaultProjectID, StartTime: now.Add(-time.Minute), EndTime: &now, Status: "FAILED"}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	prepared := `{"test_id":"orphan-prepared","status":"FAILED"}`
	sum := sha256.Sum256([]byte(prepared))
	job := func(manifest, digest string) *ArchiveFinalizationJob {
		return &ArchiveFinalizationJob{TestID: row.ID, ProjectID: row.ProjectID, PreparedManifestJSON: manifest, ArchiveRecord: RunArchiveRecord{
			TestID: row.ID, CreatedAt: row.StartTime, CompletedAt: now, ManifestSHA256: digest, ArchivePath: "/archive/run", ManifestPath: "/archive/run/manifest.json",
		}}
	}
	for _, bad := range []*ArchiveFinalizationJob{
		job(prepared, strings.Repeat("0", 64)),
		job(`{"test_id":"another-run"}`, hex.EncodeToString(sum[:])),
	} {
		if ok, err := s.SaveArchiveFinalizationJobIfOrphanForRun(bad, row.StartTime, "FAILED", "", ""); err == nil || ok {
			t.Fatalf("unanchored prepared manifest accepted: %v %v", ok, err)
		}
	}
	if _, err := s.GetArchiveFinalizationJob(row.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("rejected prepared manifest left outbox", err)
	}
	good := job(prepared, hex.EncodeToString(sum[:]))
	if ok, err := s.SaveArchiveFinalizationJobIfOrphanForRun(good, row.StartTime, "FAILED", "", ""); err != nil || !ok || good.PreparedManifestJSON != prepared {
		t.Fatalf("anchored prepared manifest = %v %v", ok, err)
	}
	stored, err := s.GetArchiveFinalizationJob(row.ID)
	if err != nil || stored.PreparedManifestJSON != prepared || stored.ArchiveRecord.ManifestSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("prepared manifest was not committed with the job: %+v %v", stored, err)
	}
}
