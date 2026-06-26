package api

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestCapacityAdvisoryCacheHitCloneAndInvalidation(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	tr := server.runner

	groups := []TestGroup{{Name: "buyers", Protocol: "http", VUsers: 100}}
	// estimateCapacityAdvisoryForAssignment reads tr.settings + the generation, so
	// it must be called under tr.mu (the production callers hold it).
	call := func() *CapacityAdvisory {
		tr.mu.RLock()
		defer tr.mu.RUnlock()
		return tr.estimateCapacityAdvisoryForAssignment("proj-a", groups, 100, 5*time.Minute, false, nil, nil)
	}

	a1 := call() // miss
	if a1 == nil {
		t.Fatal("advisory should be non-nil for 100 VUsers")
	}
	a2 := call() // hit
	if a2 == nil {
		t.Fatal("second advisory nil")
	}
	if hits, misses := tr.capacityAdvisoryCacheStats(); hits != 1 || misses != 1 {
		t.Fatalf("after 2 identical calls hits=%d misses=%d, want hits=1 misses=1", hits, misses)
	}
	if a1.RecommendedGenerators != a2.RecommendedGenerators {
		t.Fatalf("cached advisory diverged: %d vs %d", a1.RecommendedGenerators, a2.RecommendedGenerators)
	}

	// Clone isolation: mutating a returned advisory must not corrupt the cache.
	a2.Warnings = append(a2.Warnings, "tampered-by-caller")
	a3 := call() // hit (still same inputs + generation)
	for _, w := range a3.Warnings {
		if w == "tampered-by-caller" {
			t.Fatal("cache returned an aliased advisory — caller mutation leaked into the cache")
		}
	}
	if hits, _ := tr.capacityAdvisoryCacheStats(); hits != 2 {
		t.Fatalf("third identical call hits=%d, want 2 (a cache hit)", hits)
	}

	// A settings change must invalidate the cache exactly (generation bump), and the
	// recomputed advisory must reflect the new settings.
	settings := tr.GetSystemSettings()
	// Pick a clearly-valid headroom percentage that differs from the current one.
	newHeadroom := 5.0
	if settings.CapacityPlanning.HeadroomPct == newHeadroom {
		newHeadroom = 10.0
	}
	settings.CapacityPlanning.HeadroomPct = newHeadroom
	if _, err := tr.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	a4 := call() // miss (cache was invalidated)
	if a4 == nil {
		t.Fatal("post-invalidation advisory nil")
	}
	if _, misses := tr.capacityAdvisoryCacheStats(); misses != 2 {
		t.Fatalf("after settings change misses=%d, want 2 (cache invalidated → recompute)", misses)
	}
	if a4.HeadroomPct != newHeadroom {
		t.Fatalf("recomputed advisory HeadroomPct=%v, want %v (new settings not applied)", a4.HeadroomPct, newHeadroom)
	}
}
