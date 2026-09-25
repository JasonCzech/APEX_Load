package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestComparisonPoliciesCASAndImmutableSnapshots(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "comparison.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.SaveScenario(&Scenario{ID: "scenario", Name: "Scenario", Duration: "2m", CreatedAt: time.Now()}, nil); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveTest(&TestRecord{ID: "run", Name: "Run", Status: "COMPLETE", StartTime: time.Now(), Groups: []any{}}); err != nil {
		t.Fatal(err)
	}
	revision, err := store.SaveComparisonPolicy("scenario", DefaultProjectID, 0, []byte(`{"threshold":20}`))
	if err != nil || revision != 1 {
		t.Fatalf("initial policy=%d %v", revision, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.SaveComparisonPolicy("scenario", DefaultProjectID, 1, []byte(`{"threshold":30}`))
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrComparisonPolicyConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("CAS successes=%d conflicts=%d", successes, conflicts)
	}
	if _, _, err = store.GetComparisonPolicy("scenario", "foreign"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-project policy=%v", err)
	}
	if err = store.SaveBaselineComparison("comparison", DefaultProjectID, "run", []byte(`{"immutable":true}`)); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveBaselineComparison("comparison", DefaultProjectID, "run", []byte(`{"immutable":false}`)); err == nil {
		t.Fatal("snapshot overwritten")
	}
	if _, err = store.GetBaselineComparison("comparison", "foreign", "run"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cross-project snapshot=%v", err)
	}
	if _, err = store.GetBaselineComparison("comparison", DefaultProjectID, "other"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("wrong run snapshot=%v", err)
	}
	body, err := store.GetBaselineComparison("comparison", DefaultProjectID, "run")
	if err != nil || string(body) != `{"immutable":true}` {
		t.Fatalf("snapshot=%s %v", body, err)
	}
}
