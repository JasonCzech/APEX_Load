package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func newFleetMembershipStore(t *testing.T, path string) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func saveTestPool(t *testing.T, store *SQLiteStore, id string) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.SaveFleetPool(&FleetPool{
		ID: id, Name: id, CloudProvider: "kubernetes", Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("SaveFleetPool(%s): %v", id, err)
	}
}

// TestGeneratorCannotJoinTwoPools is the invariant: a pool carries at most one
// domain, so a generator in two pools could be claimed by two domains.
func TestGeneratorCannotJoinTwoPools(t *testing.T) {
	store := newFleetMembershipStore(t, filepath.Join(t.TempDir(), "apexload.db"))
	saveTestPool(t, store, "pool-a")
	saveTestPool(t, store, "pool-b")

	now := time.Now().UTC()
	if err := store.SaveFleetPoolMembers("pool-a", []*FleetPoolMember{
		{ID: "m1", PoolID: "pool-a", GeneratorID: "gen-1", Target: "10.0.0.1:9000", JoinedAt: now},
	}); err != nil {
		t.Fatalf("SaveFleetPoolMembers(pool-a): %v", err)
	}
	err := store.SaveFleetPoolMembers("pool-b", []*FleetPoolMember{
		{ID: "m2", PoolID: "pool-b", GeneratorID: "gen-1", Target: "10.0.0.1:9000", JoinedAt: now},
	})
	if err == nil {
		t.Fatal("SaveFleetPoolMembers(pool-b) succeeded, want a unique-constraint rejection")
	}

	owner, err := store.FleetPoolIDForGenerator("gen-1")
	if err != nil {
		t.Fatalf("FleetPoolIDForGenerator: %v", err)
	}
	if owner != "pool-a" {
		t.Fatalf("owner = %q, want pool-a", owner)
	}
}

// TestPatchGeneratorMetadataMirrorsMembership proves the legacy metadata pointer
// and the authoritative members table stay in step, including on clear.
func TestPatchGeneratorMetadataMirrorsMembership(t *testing.T) {
	store := newFleetMembershipStore(t, filepath.Join(t.TempDir(), "apexload.db"))
	saveTestPool(t, store, "pool-a")
	saveTestPool(t, store, "pool-b")

	poolA := "pool-a"
	target := "10.0.0.9:9000"
	if _, err := store.PatchGeneratorMetadata("gen-1", GeneratorMetadataPatch{
		Target: &target, FleetPoolIDSet: true, FleetPoolID: &poolA,
	}); err != nil {
		t.Fatalf("PatchGeneratorMetadata(bind): %v", err)
	}
	owner, err := store.FleetPoolIDForGenerator("gen-1")
	if err != nil {
		t.Fatalf("FleetPoolIDForGenerator: %v", err)
	}
	if owner != "pool-a" {
		t.Fatalf("owner after bind = %q, want pool-a", owner)
	}
	members, err := store.ListFleetPoolMembers("pool-a")
	if err != nil {
		t.Fatalf("ListFleetPoolMembers: %v", err)
	}
	if len(members) != 1 || members[0].Target != target {
		t.Fatalf("members = %+v, want one row targeting %s", members, target)
	}

	// Re-patching the target updates the row in place rather than re-joining it,
	// so member ordering stays stable.
	joinedAt := members[0].JoinedAt
	movedTarget := "10.0.0.9:9100"
	if _, err := store.PatchGeneratorMetadata("gen-1", GeneratorMetadataPatch{
		Target: &movedTarget, FleetPoolIDSet: true, FleetPoolID: &poolA,
	}); err != nil {
		t.Fatalf("PatchGeneratorMetadata(retarget): %v", err)
	}
	members, err = store.ListFleetPoolMembers("pool-a")
	if err != nil {
		t.Fatalf("ListFleetPoolMembers: %v", err)
	}
	if len(members) != 1 || members[0].Target != movedTarget {
		t.Fatalf("members = %+v, want one row targeting %s", members, movedTarget)
	}
	if !members[0].JoinedAt.Equal(joinedAt) {
		t.Fatalf("joined_at = %v, want it preserved at %v", members[0].JoinedAt, joinedAt)
	}

	// A generator already owned by pool-a cannot be bound to pool-b.
	poolB := "pool-b"
	if _, err := store.PatchGeneratorMetadata("gen-1", GeneratorMetadataPatch{
		FleetPoolIDSet: true, FleetPoolID: &poolB,
	}); err != ErrGeneratorAlreadyInPool {
		t.Fatalf("PatchGeneratorMetadata(steal) = %v, want ErrGeneratorAlreadyInPool", err)
	}

	// Clearing the pointer releases the generator.
	if _, err := store.PatchGeneratorMetadata("gen-1", GeneratorMetadataPatch{FleetPoolIDSet: true}); err != nil {
		t.Fatalf("PatchGeneratorMetadata(clear): %v", err)
	}
	if owner, err = store.FleetPoolIDForGenerator("gen-1"); err != nil || owner != "" {
		t.Fatalf("owner after clear = %q (err %v), want empty", owner, err)
	}
	// Released, it can now join pool-b.
	if _, err := store.PatchGeneratorMetadata("gen-1", GeneratorMetadataPatch{
		FleetPoolIDSet: true, FleetPoolID: &poolB,
	}); err != nil {
		t.Fatalf("PatchGeneratorMetadata(rebind): %v", err)
	}
	if owner, err = store.FleetPoolIDForGenerator("gen-1"); err != nil || owner != "pool-b" {
		t.Fatalf("owner after rebind = %q (err %v), want pool-b", owner, err)
	}
}

// TestReplacingMembersReleasesTheMetadataPointer guards a drift the union
// readers used to hide: dropping a generator from a pool's member list must
// also release the legacy pointer, or the generator is simultaneously counted
// in that pool's targets and claimable by another pool.
func TestReplacingMembersReleasesTheMetadataPointer(t *testing.T) {
	store := newFleetMembershipStore(t, filepath.Join(t.TempDir(), "apexload.db"))
	saveTestPool(t, store, "pool-a")

	poolA := "pool-a"
	target := "10.0.0.1:9000"
	if _, err := store.PatchGeneratorMetadata("gen-1", GeneratorMetadataPatch{
		Target: &target, FleetPoolIDSet: true, FleetPoolID: &poolA,
	}); err != nil {
		t.Fatalf("PatchGeneratorMetadata: %v", err)
	}
	if err := store.SaveFleetPoolMembers("pool-a", nil); err != nil {
		t.Fatalf("SaveFleetPoolMembers(empty): %v", err)
	}

	owner, err := store.FleetPoolIDForGenerator("gen-1")
	if err != nil {
		t.Fatalf("FleetPoolIDForGenerator: %v", err)
	}
	metadata, err := store.GetGeneratorMetadata("gen-1")
	if err != nil {
		t.Fatalf("GetGeneratorMetadata: %v", err)
	}
	if owner != "" {
		t.Fatalf("owner = %q, want empty after the member row was removed", owner)
	}
	if metadata.FleetPoolID != nil {
		t.Fatalf("metadata pointer = %q, want nil so the two sources agree", *metadata.FleetPoolID)
	}
}

// TestConsolidateFleetPoolMembershipIsIdempotent covers the boot path: the
// consolidation runs on every open, so opening an already-consistent database
// twice must not change anything.
func TestConsolidateFleetPoolMembershipIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apexload.db")
	store := newFleetMembershipStore(t, path)
	saveTestPool(t, store, "pool-a")
	now := time.Now().UTC()
	if err := store.SaveFleetPoolMembers("pool-a", []*FleetPoolMember{
		{ID: "m1", PoolID: "pool-a", GeneratorID: "gen-1", Target: "10.0.0.1:9000", JoinedAt: now},
	}); err != nil {
		t.Fatalf("SaveFleetPoolMembers: %v", err)
	}
	if err := store.consolidateFleetPoolMembership(); err != nil {
		t.Fatalf("consolidate (second run): %v", err)
	}
	members, err := store.ListFleetPoolMembers("pool-a")
	if err != nil {
		t.Fatalf("ListFleetPoolMembers: %v", err)
	}
	if len(members) != 1 || members[0].GeneratorID != "gen-1" {
		t.Fatalf("members = %+v, want the single original row", members)
	}
}

// TestConsolidatePromotesMetadataOnlyMembership guards the migration risk: a
// generator bound only through the legacy pointer must not silently lose its
// pool when the members table becomes authoritative.
func TestConsolidatePromotesMetadataOnlyMembership(t *testing.T) {
	store := newFleetMembershipStore(t, filepath.Join(t.TempDir(), "apexload.db"))
	saveTestPool(t, store, "pool-a")

	// Write the legacy shape directly: metadata pointer with no member row.
	now := time.Now().UTC()
	if _, err := store.db.Exec(`INSERT INTO generator_metadata
		(generator_id, target, fleet_pool_id, region, provider_id, provider_status, cordoned, tags_json, created_at, updated_at)
		VALUES (?, ?, ?, '', '', '', 0, '', ?, ?)`,
		"gen-legacy", "10.0.0.5:9000", "pool-a", now, now); err != nil {
		t.Fatalf("seed legacy metadata: %v", err)
	}

	if err := store.consolidateFleetPoolMembership(); err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	owner, err := store.FleetPoolIDForGenerator("gen-legacy")
	if err != nil {
		t.Fatalf("FleetPoolIDForGenerator: %v", err)
	}
	if owner != "pool-a" {
		t.Fatalf("owner = %q, want pool-a (legacy binding must be promoted, not dropped)", owner)
	}
	members, err := store.ListFleetPoolMembers("pool-a")
	if err != nil {
		t.Fatalf("ListFleetPoolMembers: %v", err)
	}
	if len(members) != 1 || members[0].Target != "10.0.0.5:9000" {
		t.Fatalf("members = %+v, want the promoted row with its target", members)
	}
}

// TestTargetOnlyMembersAreNotTreatedAsOneGenerator guards a shape the exclusivity
// rule must not break: a member may be identified by target alone ("target or
// generator_id is required"), and every such row carries an empty generator_id.
// A total unique index would read them all as the same generator and reject the
// second pool; a total dedupe would silently delete one at boot.
func TestTargetOnlyMembersAreNotTreatedAsOneGenerator(t *testing.T) {
	store := newFleetMembershipStore(t, filepath.Join(t.TempDir(), "apexload.db"))
	saveTestPool(t, store, "pool-a")
	saveTestPool(t, store, "pool-b")

	now := time.Now().UTC()
	if err := store.SaveFleetPoolMembers("pool-a", []*FleetPoolMember{
		{ID: "m1", PoolID: "pool-a", Target: "10.0.0.1:9000", JoinedAt: now},
	}); err != nil {
		t.Fatalf("SaveFleetPoolMembers(pool-a): %v", err)
	}
	if err := store.SaveFleetPoolMembers("pool-b", []*FleetPoolMember{
		{ID: "m2", PoolID: "pool-b", Target: "10.0.0.2:9000", JoinedAt: now},
	}); err != nil {
		t.Fatalf("SaveFleetPoolMembers(pool-b): %v", err)
	}

	// Consolidation must leave both in place.
	if err := store.consolidateFleetPoolMembership(); err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	for _, poolID := range []string{"pool-a", "pool-b"} {
		members, err := store.ListFleetPoolMembers(poolID)
		if err != nil {
			t.Fatalf("ListFleetPoolMembers(%s): %v", poolID, err)
		}
		if len(members) != 1 {
			t.Fatalf("%s members = %+v, want the target-only row retained", poolID, members)
		}
	}
}

// TestConsolidateDedupesCrossPoolMembership is the case that would otherwise
// break the unique index on an existing installation: the same generator listed
// in two pools. The metadata pointer decides the winner.
func TestConsolidateDedupesCrossPoolMembership(t *testing.T) {
	store := newFleetMembershipStore(t, filepath.Join(t.TempDir(), "apexload.db"))
	saveTestPool(t, store, "pool-a")
	saveTestPool(t, store, "pool-b")

	now := time.Now().UTC()
	// Reproduce a pre-index installation: drop the unique index so the duplicate
	// rows an older build allowed can be seeded.
	if _, err := store.db.Exec(`DROP INDEX IF EXISTS idx_fleet_pool_members_generator`); err != nil {
		t.Fatalf("drop unique index: %v", err)
	}
	for _, row := range []struct{ id, pool string }{{"m1", "pool-a"}, {"m2", "pool-b"}} {
		if _, err := store.db.Exec(`INSERT INTO fleet_pool_members (id, pool_id, generator_id, target, status, joined_at)
			VALUES (?, ?, ?, ?, ?, ?)`, row.id, row.pool, "gen-1", "10.0.0.1:9000", "provisioned", now); err != nil {
			t.Fatalf("seed duplicate membership %s: %v", row.id, err)
		}
	}
	if _, err := store.db.Exec(`INSERT INTO generator_metadata
		(generator_id, target, fleet_pool_id, region, provider_id, provider_status, cordoned, tags_json, created_at, updated_at)
		VALUES (?, ?, ?, '', '', '', 0, '', ?, ?)`,
		"gen-1", "10.0.0.1:9000", "pool-b", now, now); err != nil {
		t.Fatalf("seed metadata pointer: %v", err)
	}

	if err := store.consolidateFleetPoolMembership(); err != nil {
		t.Fatalf("consolidate: %v", err)
	}
	owner, err := store.FleetPoolIDForGenerator("gen-1")
	if err != nil {
		t.Fatalf("FleetPoolIDForGenerator: %v", err)
	}
	if owner != "pool-b" {
		t.Fatalf("owner = %q, want pool-b (the pool the metadata pointer named)", owner)
	}
	// And the unique index can now be created on this database.
	if _, err := store.db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_fleet_pool_members_generator ON fleet_pool_members(generator_id)`); err != nil {
		t.Fatalf("create unique index after consolidation: %v", err)
	}
}
