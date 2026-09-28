package storage

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunArchiveCollectionCache(t *testing.T) {
	checkRunArchiveCollectionCache(t, newHAFencingStore(t))
}
func checkRunArchiveCollectionCache(t *testing.T, s *SQLiteStore) {
	t.Helper()
	const lease, owner = "collection-cache", "current"
	if ok, err := s.TryAcquireSchedulerLease(lease, owner, time.Now(), time.Minute); err != nil || !ok {
		t.Fatal(err)
	}
	row := &TestRecord{ID: "cache-run", ProjectID: DefaultProjectID, Name: "cache", Status: "RUNNING", StartTime: time.Now().UTC()}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("a", 64)
	job, ok, err := s.PrepareRunArchiveCollection(row.ID, row.ProjectID, row.StartTime, map[string]string{"a": digest, "b": digest}, lease, owner)
	if err != nil || !ok {
		t.Fatal(err)
	}
	source := RunArchiveCollectionSource{ReceiptSHA256: digest, Bytes: 60}
	if ok, err := s.SaveRunArchiveCollectionSource(row.ID, job.IntentToken, "a", source, lease, owner); err == nil || ok {
		t.Fatal("unconfigured cache accepted")
	}
	for _, limit := range []int64{-1, 0, MaxRunArchiveCollectionCacheBytes + 1} {
		if _, ok, err := s.ConfigureRunArchiveCollectionCache(row.ID, job.IntentToken, limit, lease, owner); err == nil || ok {
			t.Fatal("invalid ceiling accepted")
		}
	}
	for _, who := range []string{"stale", owner} {
		configured, ok, err := s.ConfigureRunArchiveCollectionCache(row.ID, job.IntentToken, 100, lease, who)
		if err != nil || ok != (who == owner) {
			t.Fatalf("configure: %v %v", ok, err)
		}
		if ok && configured.CacheLimitBytes != 100 {
			t.Fatal("ceiling missing")
		}
	}
	again, ok, err := s.ConfigureRunArchiveCollectionCache(row.ID, job.IntentToken, 200, lease, owner)
	if err != nil || !ok || again.CacheLimitBytes != 100 {
		t.Fatal("ceiling changed")
	}
	for _, tc := range []struct {
		token, target, who string
		source             RunArchiveCollectionSource
		wantErr            bool
	}{
		{"stale", "a", owner, source, false}, {job.IntentToken, "a", "stale", source, false},
		{job.IntentToken, "unknown", owner, source, true}, {job.IntentToken, "a", owner, RunArchiveCollectionSource{digest, 0}, true},
		{job.IntentToken, "a", owner, RunArchiveCollectionSource{"bad", 1}, true},
	} {
		ok, err := s.SaveRunArchiveCollectionSource(row.ID, tc.token, tc.target, tc.source, lease, tc.who)
		if ok || (err != nil) != tc.wantErr {
			t.Fatalf("invalid save: %v %v", ok, err)
		}
	}
	// Competing sources cannot each spend the same remaining run capacity.
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for _, target := range []string{"a", "b"} {
		wg.Go(func() {
			ok, _ := s.SaveRunArchiveCollectionSource(row.ID, job.IntentToken, target, source, lease, owner)
			results <- ok
		})
	}
	wg.Wait()
	close(results)
	wins := 0
	for ok := range results {
		if ok {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("capacity winners=%d", wins)
	}
	got, err := s.GetRunArchiveCollection(row.ID)
	if err != nil || len(got.CachedSources) != 1 || !got.UpdatedAt.Truncate(time.Microsecond).Equal(job.UpdatedAt.Truncate(time.Microsecond)) {
		t.Fatalf("cache state=%+v %v", got, err)
	}
	for target := range got.CachedSources {
		if ok, err := s.SaveRunArchiveCollectionSource(row.ID, job.IntentToken, target, source, lease, owner); err != nil || !ok {
			t.Fatal("idempotent save", err)
		}
		if ok, err := s.SaveRunArchiveCollectionSource(row.ID, job.IntentToken, target, RunArchiveCollectionSource{strings.Repeat("b", 64), 60}, lease, owner); err == nil || ok {
			t.Fatal("receipt replaced")
		}
	}
	if err := s.SaveRunArchive(&RunArchiveRecord{TestID: row.ID, ArchivePath: "/run", ManifestPath: "/run/manifest.json", CreatedAt: row.StartTime, CompletedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SaveRunArchiveCollectionSource(row.ID, job.IntentToken, "b", source, lease, owner); err != nil || ok {
		t.Fatal("published source changed", err)
	}
	if _, ok, err := s.ConfigureRunArchiveCollectionCache(row.ID, job.IntentToken, 100, lease, owner); err != nil || ok {
		t.Fatal("published cache configured", err)
	}
}

func TestRunArchiveCollectionCacheUpgradeAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	row := &TestRecord{ID: "upgrade-cache", ProjectID: DefaultProjectID, Name: "upgrade", Status: "RUNNING", StartTime: time.Now().UTC()}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	job, ok, err := s.PrepareRunArchiveCollection(row.ID, row.ProjectID, row.StartTime, map[string]string{"a": strings.Repeat("a", 64)}, "", "")
	if err != nil || !ok {
		t.Fatal(err)
	}
	for _, query := range []string{"ALTER TABLE run_archive_collection_intents DROP COLUMN cache_limit_bytes", "ALTER TABLE run_archive_collection_intents DROP COLUMN cached_sources_json", "DELETE FROM schema_migrations WHERE version >= 20"} {
		if _, err := s.db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRunArchiveCollection(row.ID)
	if err != nil || got.IntentToken != job.IntentToken || got.CacheLimitBytes != 0 || len(got.CachedSources) != 0 {
		t.Fatalf("upgrade lost intent: %+v %v", got, err)
	}
	if _, ok, err := s.ConfigureRunArchiveCollectionCache(row.ID, job.IntentToken, 100, "", ""); err != nil || !ok {
		t.Fatal(err)
	}
	source := RunArchiveCollectionSource{strings.Repeat("c", 64), 100}
	if ok, err := s.SaveRunArchiveCollectionSource(row.ID, job.IntentToken, "a", source, "", ""); err != nil || !ok {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err = s.GetRunArchiveCollection(row.ID)
	if err != nil || got.CacheLimitBytes != 100 || got.CachedSources["a"] != source {
		t.Fatal("cache receipt lost on restart", err)
	}
}
