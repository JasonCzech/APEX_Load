package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRunArchiveCollectionDurability(t *testing.T) {
	checkRunArchiveCollectionDurability(t, newHAFencingStore(t))
}
func checkRunArchiveCollectionDurability(t *testing.T, s *SQLiteStore) {
	t.Helper()
	const lease, owner = "archive-collection-execution", "collection-owner"
	if held, err := s.TryAcquireSchedulerLease(lease, owner, time.Now(), time.Minute); err != nil || !held {
		t.Fatal(err)
	}
	row := &TestRecord{ID: "collection-run", ProjectID: DefaultProjectID, Name: "collection", Status: "RUNNING", StartTime: time.Now().UTC(), Groups: []any{}}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	identity := strings.Repeat("a", 64)
	prepare := func(source map[string]string, who string) (*RunArchiveCollectionIntent, bool, error) {
		return s.PrepareRunArchiveCollection(row.ID, row.ProjectID, row.StartTime, source, lease, who)
	}
	if _, ok, err := prepare(map[string]string{"a:9091": identity}, "stale"); err != nil || ok {
		t.Fatalf("stale owner=%v %v", ok, err)
	}
	first, ok, err := prepare(map[string]string{"a:9091": identity}, owner)
	if err != nil || !ok {
		t.Fatalf("prepare=%v %v", ok, err)
	}
	again, ok, err := prepare(map[string]string{"a:9091": identity, "b:9091": strings.Repeat("b", 64)}, owner)
	if err != nil || !ok || again.IntentToken != first.IntentToken || len(again.Sources) != 2 {
		t.Fatalf("merge=%+v %v", again, err)
	}
	if _, ok, err := prepare(map[string]string{"a:9091": strings.Repeat("c", 64)}, owner); err == nil || ok {
		t.Fatal("rebound source identity")
	}
	if jobs, err := s.ListRunArchiveCollections(10, time.Now()); err != nil || len(jobs) != 0 {
		t.Fatal("active source became runnable")
	}
	if held, err := s.ClaimRunArchiveCollectionAttempt(row.ID, first.IntentToken, 0, lease, owner); err != nil || held {
		t.Fatal("active attempt claimed")
	}
	end := time.Now().UTC()
	if _, err := s.UpdateTestStatusIfActive(row.ID, "FAILED", &end, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := prepare(map[string]string{"c:9091": identity}, owner); err != nil || ok {
		t.Fatal("terminal source mutated")
	}
	jobs, err := s.ListRunArchiveCollections(10, time.Now())
	if err != nil || len(jobs) != 1 || jobs[0].IntentToken != first.IntentToken {
		t.Fatalf("pending=%+v %v", jobs, err)
	}
	var wg sync.WaitGroup
	results := make(chan bool, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.ClaimRunArchiveCollectionAttempt(row.ID, first.IntentToken, 0, lease, owner)
			results <- ok
			errs <- err
		}()
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
		t.Fatalf("attempt winners=%d", wins)
	}
	if jobs, err := s.ListRunArchiveCollections(10, time.Now().Add(-time.Minute)); err != nil || len(jobs) != 0 {
		t.Fatal("retry not delayed")
	}
	got, err := s.GetRunArchiveCollection(row.ID)
	if err != nil || got.Attempts != 1 || len(got.Sources) != 2 {
		t.Fatalf("durable=%+v %v", got, err)
	}
	for _, args := range []struct {
		token, who string
		attempt    int
	}{{"stale", owner, 1}, {first.IntentToken, "stale", 1}, {first.IntentToken, owner, 0}} {
		if ok, err := s.ExhaustRunArchiveCollection(row.ID, args.token, args.attempt, lease, args.who); ok || (args.attempt > 0 && err != nil) {
			t.Fatalf("stale exhausted=%v %v", ok, err)
		}
	}
	if ok, err := s.ExhaustRunArchiveCollection(row.ID, first.IntentToken, 1, lease, owner); err != nil || !ok {
		t.Fatalf("exhaust=%v %v", ok, err)
	}
	if jobs, err := s.ListRunArchiveCollections(10, time.Now().Add(time.Hour)); err != nil || len(jobs) != 0 {
		t.Fatal("exhausted job retried")
	}
	got, err = s.GetRunArchiveCollection(row.ID)
	if err != nil || !got.Exhausted {
		t.Fatal("exhaustion was not durable")
	}
	if err := s.DeleteTest(row.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetRunArchiveCollection(row.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("delete did not remove intent")
	}
	row.StartTime = time.Now().UTC()
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimRunArchiveCollectionAttempt(row.ID, first.IntentToken, 1, lease, owner); err != nil || ok {
		t.Fatal("old incarnation claimed replacement")
	}
}

func TestRunArchiveCollectionRejectsMalformedSources(t *testing.T) {
	for _, sources := range []map[string]string{nil, {}, {"": strings.Repeat("a", 64)}, {"a\n": strings.Repeat("a", 64)}, {"a": "short"}, {"a": strings.Repeat("A", 64)}} {
		if err := validateArchiveCollectionSources(sources); err == nil {
			t.Fatal("invalid sources accepted")
		}
	}
	sources := make(map[string]string)
	for i := 0; i < MaxRunArchiveCollectionSources; i++ {
		sources[fmt.Sprintf("source-%d", i)] = strings.Repeat("a", 64)
	}
	if err := validateArchiveCollectionSources(sources); err != nil {
		t.Fatal(err)
	}
	sources["too-many"] = strings.Repeat("b", 64)
	if err := validateArchiveCollectionSources(sources); err == nil {
		t.Fatal("unbounded sources")
	}
}

func TestRunArchiveCollectionRejectsCorruptStoredMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, column string
		value        any
	}{
		{"invalid JSON", "sources_json", "{"},
		{"negative cache ceiling", "cache_limit_bytes", -1},
		{"unbounded cache ceiling", "cache_limit_bytes", MaxRunArchiveCollectionCacheBytes + 1},
		{"invalid cache JSON", "cached_sources_json", "{"},
		{"oversized cache JSON", "cached_sources_json", strings.Repeat(" ", (512<<10)+1)},
		{"unconfigured cache receipt", "cached_sources_json", `{"a":{"receipt_sha256":"` + strings.Repeat("a", 64) + `","bytes":1}}`},

		{"empty sources", "sources_json", "{}"},
		{"oversized sources", "sources_json", strings.Repeat(" ", (512<<10)+1)},
		{"invalid token", "intent_token", "../../other"},
		{"negative attempts", "attempts", -1},
		{"unbounded attempts", "attempts", 101},
		{"invalid exhaustion", "exhausted", 2},
		{"invalid resolution", "resolved", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newHAFencingStore(t)
			row := &TestRecord{ID: "corrupt-collection", ProjectID: DefaultProjectID, Name: "collection", Status: "RUNNING", StartTime: time.Now().UTC(), Groups: []any{}}
			if err := s.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := s.PrepareRunArchiveCollection(row.ID, row.ProjectID, row.StartTime, map[string]string{"a": strings.Repeat("a", 64)}, "", ""); err != nil || !ok {
				t.Fatal(err)
			}
			// Column names come only from this fixed test table; values remain parameters.
			if _, err := s.db.Exec("UPDATE run_archive_collection_intents SET "+tc.column+" = ? WHERE test_id = ?", tc.value, row.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := s.GetRunArchiveCollection(row.ID); err == nil {
				t.Fatal("corrupt durable collection metadata accepted")
			}
			if _, ok, err := s.PrepareRunArchiveCollection(row.ID, row.ProjectID, row.StartTime, map[string]string{"a": strings.Repeat("a", 64)}, "", ""); err == nil || ok {
				t.Fatal("corrupt collection metadata reused by dispatch")
			}

		})
	}
}

func TestRunArchiveCollectionPublicationCannotResurrectAfterRetention(t *testing.T) {
	checkRunArchiveCollectionPublicationPrecedence(t, newHAFencingStore(t))
}
func checkRunArchiveCollectionPublicationPrecedence(t *testing.T, s *SQLiteStore) {
	for _, publication := range []string{"catalog", "outbox", "orphan"} {
		t.Run(publication, func(t *testing.T) {
			row := &TestRecord{ID: "collection-retention-" + publication, ProjectID: DefaultProjectID, Name: "collection", Status: "RUNNING", StartTime: time.Now().UTC(), Groups: []any{}}
			if err := s.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			intent, ok, err := s.PrepareRunArchiveCollection(row.ID, row.ProjectID, row.StartTime, map[string]string{"a": strings.Repeat("a", 64)}, "", "")
			if err != nil || !ok {
				t.Fatalf("prepare: %v %v", ok, err)
			}
			end := time.Now().UTC()
			if _, err := s.UpdateTestStatusIfActive(row.ID, "COMPLETE", &end, ""); err != nil {
				t.Fatal(err)
			}
			if ok, err := s.ClaimRunArchiveCollectionAttempt(row.ID, intent.IntentToken, 0, "", ""); err != nil || !ok {
				t.Fatalf("claim: %v %v", ok, err)
			}
			record := RunArchiveRecord{TestID: row.ID, ArchivePath: "/archive/run", ManifestPath: "/archive/run/manifest.json", CreatedAt: row.StartTime, CompletedAt: end}
			switch publication {
			case "catalog":
				err = s.SaveRunArchive(&record)
			case "outbox":
				err = s.SaveArchiveFinalizationJob(&ArchiveFinalizationJob{TestID: row.ID, ProjectID: row.ProjectID, ArchiveRecord: record})
			case "orphan":
				ok, err = s.SaveArchiveFinalizationJobIfOrphan(&ArchiveFinalizationJob{TestID: row.ID, ProjectID: row.ProjectID, ArchiveRecord: record})
				if !ok && err == nil {
					t.Fatal("orphan intent not saved")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec("DELETE FROM run_archives WHERE test_id = ?", row.ID); err != nil {
				t.Fatal(err)
			}
			if err = s.DeleteArchiveFinalizationJob(row.ID); err != nil {
				t.Fatal(err)
			}
			got, err := s.GetRunArchiveCollection(row.ID)
			if err != nil || !got.Resolved {
				t.Fatalf("resolved: %+v %v", got, err)
			}
			if jobs, err := s.ListRunArchiveCollections(100, time.Now().Add(time.Hour)); err != nil || len(jobs) != 0 {
				t.Fatalf("retained collection reappeared: %+v %v", jobs, err)
			}
			if ok, err := s.ClaimRunArchiveCollectionAttempt(row.ID, intent.IntentToken, 1, "", ""); err != nil || ok {
				t.Fatalf("retained collection claimed: %v %v", ok, err)
			}
			if ok, err := s.ExhaustRunArchiveCollection(row.ID, intent.IntentToken, 1, "", ""); err != nil || ok {
				t.Fatalf("resolved collection exhausted: %v %v", ok, err)
			}
		})
	}
}

func TestRunArchiveCollectionUpgradeFromPriorRelease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.db")
	s, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("DROP TABLE run_archive_collection_intents"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("DELETE FROM schema_migrations WHERE version >= 19"); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	checkRunArchiveCollectionDurability(t, s)
	checkRunArchiveCollectionPublicationPrecedence(t, s)
}

func TestRunArchiveCollectionBoundsEncodedMetadata(t *testing.T) {
	s := newHAFencingStore(t)
	row := &TestRecord{ID: "encoded-limit", ProjectID: DefaultProjectID, Name: "collection", Status: "RUNNING", StartTime: time.Now().UTC()}
	if err := s.SaveTest(row); err != nil {
		t.Fatal(err)
	}
	sources := make(map[string]string)
	for i := 0; i < MaxRunArchiveCollectionSources; i++ {
		sources[fmt.Sprintf("%03d", i)+strings.Repeat("\"", 1020)] = strings.Repeat("a", 64)
	}
	if err := validateArchiveCollectionSources(sources); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.PrepareRunArchiveCollection(row.ID, row.ProjectID, row.StartTime, sources, "", ""); err == nil || ok {
		t.Fatal("oversized encoded sources persisted")
	}
	if _, err := s.GetRunArchiveCollection(row.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rejected intent persisted: %v", err)
	}
}
