package api

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// Per-pool project quotas are an additive admission gate: a pool-assigned run is
// held when the project's footprint ON THAT POOL would exceed the configured
// quota, even if project-wide and global capacity are fine.
func TestPoolProjectQuotaBlocksAdmission(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 10, Addr: ":8080"})
	tr := server.runner

	if err := store.SaveProject(&storage.Project{
		ID:   "proj-pool",
		Name: "Pool Project",
		Settings: storage.ProjectSettings{
			PoolQuotas: map[string]storage.PoolProjectQuota{
				"pool-east": {MaxConcurrentTests: 1, MaxVUsers: 100},
			},
		},
	}); err != nil {
		t.Fatalf("SaveProject returned error: %v", err)
	}

	poolAssign := func(pool string) *GeneratorAssignmentSettings {
		return &GeneratorAssignmentSettings{Mode: "specific_hosts", PoolID: pool}
	}

	// One running test already on pool-east for this project (fills the 1-slot quota).
	tr.mu.Lock()
	tr.tests["running-1"] = &ManagedTest{ID: "running-1", ProjectID: "proj-pool", Status: "RUNNING", TotalVUsers: 40, Assignment: poolAssign("pool-east")}
	tr.mu.Unlock()

	eval := func(test *ManagedTest) (bool, string) {
		tr.mu.RLock()
		defer tr.mu.RUnlock()
		return tr.evaluateQuotaBlockLocked(test, "")
	}

	// A second pool-east run for the same project is blocked by the concurrency quota.
	blocked, reason := eval(&ManagedTest{ID: "c1", ProjectID: "proj-pool", Status: "QUEUED", TotalVUsers: 10, Assignment: poolAssign("pool-east")})
	if !blocked || !strings.Contains(reason, "pool test slot") {
		t.Fatalf("second pool-east run = (%v, %q), want blocked on pool test slot", blocked, reason)
	}

	// Raise the concurrency cap; now the VUser quota (100) binds: 40 active + 70 > 100.
	proj, _ := store.GetProject("proj-pool")
	proj.Settings.PoolQuotas["pool-east"] = storage.PoolProjectQuota{MaxConcurrentTests: 5, MaxVUsers: 100}
	if err := store.SaveProject(proj); err != nil {
		t.Fatalf("SaveProject (update) returned error: %v", err)
	}
	blocked, reason = eval(&ManagedTest{ID: "c2", ProjectID: "proj-pool", Status: "QUEUED", TotalVUsers: 70, Assignment: poolAssign("pool-east")})
	if !blocked || !strings.Contains(reason, "pool VUser capacity") {
		t.Fatalf("over-VUser pool run = (%v, %q), want blocked on pool VUser capacity", blocked, reason)
	}

	// A run on a DIFFERENT pool is unaffected by pool-east's quota.
	if blocked, reason := eval(&ManagedTest{ID: "c3", ProjectID: "proj-pool", Status: "QUEUED", TotalVUsers: 70, Assignment: poolAssign("pool-west")}); blocked {
		t.Fatalf("different-pool run should not be gated by pool-east quota, got %q", reason)
	}

	// A within-quota pool-east run is admitted (40 active + 50 = 90 <= 100, 1 < 5 slots).
	if blocked, reason := eval(&ManagedTest{ID: "c4", ProjectID: "proj-pool", Status: "QUEUED", TotalVUsers: 50, Assignment: poolAssign("pool-east")}); blocked {
		t.Fatalf("within-quota pool-east run should be admitted, got %q", reason)
	}

	// A non-pool-assigned run is unaffected by pool quotas.
	if blocked, reason := eval(&ManagedTest{ID: "c5", ProjectID: "proj-pool", Status: "QUEUED", TotalVUsers: 70}); blocked {
		t.Fatalf("non-pool run should not be gated by pool quota, got %q", reason)
	}
}
