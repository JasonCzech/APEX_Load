package storage

import (
	"testing"
	"time"
)

func seedDomain(t *testing.T, store *SQLiteStore, id, name string) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.SaveDomain(&Domain{ID: id, Name: name, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveDomain: %v", err)
	}
}

func TestDomainDeploymentTargetRoundTrip(t *testing.T) {
	store := newClusterTestStore(t)
	seedDomain(t, store, "domain-1", "Payments")

	// An unconfigured domain is nil, not an error: "no constraint" is a normal
	// state, and every domain starts there.
	target, err := store.GetDomainDeploymentTarget("domain-1")
	if err != nil {
		t.Fatalf("GetDomainDeploymentTarget: %v", err)
	}
	if target != nil {
		t.Fatalf("unconfigured domain returned %+v, want nil", target)
	}

	created := &DomainDeploymentTarget{
		DomainID:         "domain-1",
		Provider:         DeploymentProviderAWSEKS,
		UpdatedBySubject: "admin@example.com",
	}
	// A first write must carry a ZERO concurrency token.
	saved, err := store.UpsertDomainDeploymentTargetIfUnchanged(created, time.Time{})
	if err != nil || !saved {
		t.Fatalf("first upsert = (%v, %v), want (true, nil)", saved, err)
	}

	loaded, err := store.GetDomainDeploymentTarget("domain-1")
	if err != nil || loaded == nil {
		t.Fatalf("GetDomainDeploymentTarget = (%v, %v)", loaded, err)
	}
	if loaded.Provider != DeploymentProviderAWSEKS || loaded.UpdatedBySubject != "admin@example.com" {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.CreatedAt.IsZero() || loaded.UpdatedAt.IsZero() {
		t.Fatalf("timestamps did not persist: %+v", loaded)
	}
}

// TestDomainDeploymentTargetPreservesCreatedAtAcrossEdits pins that an edit
// reports the ORIGINAL creation time rather than echoing the concurrency token.
func TestDomainDeploymentTargetPreservesCreatedAtAcrossEdits(t *testing.T) {
	store := newClusterTestStore(t)
	seedDomain(t, store, "domain-1", "Payments")

	target := &DomainDeploymentTarget{DomainID: "domain-1", Provider: DeploymentProviderAzureAKS}
	if _, err := store.UpsertDomainDeploymentTargetIfUnchanged(target, time.Time{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	original, err := store.GetDomainDeploymentTarget("domain-1")
	if err != nil {
		t.Fatalf("GetDomainDeploymentTarget: %v", err)
	}

	edit := &DomainDeploymentTarget{DomainID: "domain-1", Provider: DeploymentProviderAWSEKS}
	saved, err := store.UpsertDomainDeploymentTargetIfUnchanged(edit, original.UpdatedAt)
	if err != nil || !saved {
		t.Fatalf("edit = (%v, %v), want (true, nil)", saved, err)
	}
	if !edit.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("CreatedAt after edit = %v, want the original %v", edit.CreatedAt, original.CreatedAt)
	}
	if !edit.UpdatedAt.After(original.UpdatedAt) && !edit.UpdatedAt.Equal(original.UpdatedAt) {
		t.Errorf("UpdatedAt did not advance: %v", edit.UpdatedAt)
	}

	reloaded, err := store.GetDomainDeploymentTarget("domain-1")
	if err != nil {
		t.Fatalf("GetDomainDeploymentTarget: %v", err)
	}
	if reloaded.Provider != DeploymentProviderAWSEKS {
		t.Errorf("Provider = %q, want the edit to apply", reloaded.Provider)
	}
	if !reloaded.CreatedAt.Equal(original.CreatedAt) {
		t.Errorf("stored CreatedAt = %v, want %v", reloaded.CreatedAt, original.CreatedAt)
	}
}

func TestDomainDeploymentTargetRejectsStaleAndAmbiguousWrites(t *testing.T) {
	store := newClusterTestStore(t)
	seedDomain(t, store, "domain-1", "Payments")

	t.Run("a non-zero token against a domain with no row is refused", func(t *testing.T) {
		// The caller believed a target existed; resurrecting it under stale
		// intent would silently reinstate a constraint someone just cleared.
		target := &DomainDeploymentTarget{DomainID: "domain-1", Provider: DeploymentProviderAWSEKS}
		saved, err := store.UpsertDomainDeploymentTargetIfUnchanged(target, time.Now().UTC())
		if err != nil || saved {
			t.Fatalf("upsert = (%v, %v), want (false, nil)", saved, err)
		}
	})

	target := &DomainDeploymentTarget{DomainID: "domain-1", Provider: DeploymentProviderAWSEKS}
	if _, err := store.UpsertDomainDeploymentTargetIfUnchanged(target, time.Time{}); err != nil {
		t.Fatalf("create: %v", err)
	}
	stored, err := store.GetDomainDeploymentTarget("domain-1")
	if err != nil {
		t.Fatalf("GetDomainDeploymentTarget: %v", err)
	}

	t.Run("a stale token is refused", func(t *testing.T) {
		stale := &DomainDeploymentTarget{DomainID: "domain-1", Provider: DeploymentProviderKubernetes}
		saved, err := store.UpsertDomainDeploymentTargetIfUnchanged(stale, stored.UpdatedAt.Add(-time.Hour))
		if err != nil || saved {
			t.Fatalf("stale upsert = (%v, %v), want (false, nil)", saved, err)
		}
	})

	t.Run("a zero token against an existing row is refused", func(t *testing.T) {
		// Otherwise a client that never read the row could overwrite it.
		blind := &DomainDeploymentTarget{DomainID: "domain-1", Provider: DeploymentProviderKubernetes}
		saved, err := store.UpsertDomainDeploymentTargetIfUnchanged(blind, time.Time{})
		if err != nil || saved {
			t.Fatalf("blind upsert = (%v, %v), want (false, nil)", saved, err)
		}
	})

	t.Run("an unselectable provider is refused", func(t *testing.T) {
		for _, provider := range []string{DeploymentProviderGCPGKE, DeploymentProviderStatic, "", "nonsense"} {
			bad := &DomainDeploymentTarget{DomainID: "domain-1", Provider: provider}
			if _, err := store.UpsertDomainDeploymentTargetIfUnchanged(bad, stored.UpdatedAt); err == nil {
				t.Errorf("provider %q was accepted as a domain deployment target", provider)
			}
		}
	})
}

// TestDeleteClusterConnectionRefusesWhileADomainDefaultsToIt guards the
// reference the fleet-pool check alone would miss: without it the Postgres
// foreign key surfaces as a raw 500 instead of the existing 409.
func TestDeleteClusterConnectionRefusesWhileADomainDefaultsToIt(t *testing.T) {
	store := newClusterTestStore(t)
	seedDomain(t, store, "domain-1", "Payments")
	conn := eksConnection("cluster-eks", "us-east-1-perf")
	if err := store.CreateClusterConnection(conn); err != nil {
		t.Fatalf("CreateClusterConnection: %v", err)
	}

	target := &DomainDeploymentTarget{
		DomainID:         "domain-1",
		Provider:         DeploymentProviderAWSEKS,
		DefaultClusterID: "cluster-eks",
	}
	if _, err := store.UpsertDomainDeploymentTargetIfUnchanged(target, time.Time{}); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	deleted, err := store.DeleteClusterConnectionIfUnreferenced("cluster-eks")
	if err != nil {
		t.Fatalf("DeleteClusterConnectionIfUnreferenced: %v", err)
	}
	if deleted {
		t.Fatal("deleted a cluster a domain still defaults to")
	}

	// Clearing the constraint releases the reference.
	if err := store.DeleteDomainDeploymentTarget("domain-1"); err != nil {
		t.Fatalf("DeleteDomainDeploymentTarget: %v", err)
	}
	deleted, err = store.DeleteClusterConnectionIfUnreferenced("cluster-eks")
	if err != nil || !deleted {
		t.Fatalf("DeleteClusterConnectionIfUnreferenced = (%v, %v), want (true, nil)", deleted, err)
	}
}

// TestFleetPoolDomainBindingSurvivesUnrelatedEdits pins the five-call-site
// change in sqlite_fleet.go. Missing the UPDATE SET entry would silently reset
// a constrained pool to unscoped on every edit — a silent widening of blast
// radius that nothing else would report.
func TestFleetPoolDomainBindingSurvivesUnrelatedEdits(t *testing.T) {
	store := newClusterTestStore(t)
	seedDomain(t, store, "domain-1", "Payments")

	pool := &FleetPool{
		ID: "pool-1", Name: "east-http", CloudProvider: "aws_eks",
		Status: "active", DomainID: "domain-1", ClusterID: "cluster-eks",
	}
	if err := store.CreateFleetPoolWithMembers(pool, nil); err != nil {
		t.Fatalf("CreateFleetPoolWithMembers: %v", err)
	}

	stored, err := store.GetFleetPool("pool-1")
	if err != nil {
		t.Fatalf("GetFleetPool: %v", err)
	}
	if stored.DomainID != "domain-1" || stored.ClusterID != "cluster-eks" {
		t.Fatalf("bindings did not round-trip on create: %+v", stored)
	}

	stored.Name = "renamed"
	updated, err := store.UpdateFleetPoolIfUnchanged(stored, nil, false, stored.UpdatedAt)
	if err != nil || !updated {
		t.Fatalf("UpdateFleetPoolIfUnchanged = (%v, %v), want (true, nil)", updated, err)
	}

	reloaded, err := store.GetFleetPool("pool-1")
	if err != nil {
		t.Fatalf("GetFleetPool: %v", err)
	}
	if reloaded.DomainID != "domain-1" {
		t.Errorf("an unrelated edit cleared domain_id (%q); the pool silently became unscoped", reloaded.DomainID)
	}
	if reloaded.ClusterID != "cluster-eks" {
		t.Errorf("an unrelated edit cleared cluster_id (%q)", reloaded.ClusterID)
	}
}

// TestExistingFleetPoolsAreUnscoped pins the migration default. Backfilling to
// DefaultDomainID would silently subject every legacy pool to whatever target a
// superuser later sets on that domain.
func TestExistingFleetPoolsAreUnscoped(t *testing.T) {
	store := newClusterTestStore(t)
	pool := &FleetPool{ID: "pool-legacy", Name: "legacy", CloudProvider: "kubernetes", Status: "active"}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}
	stored, err := store.GetFleetPool("pool-legacy")
	if err != nil {
		t.Fatalf("GetFleetPool: %v", err)
	}
	if stored.DomainID != "" {
		t.Fatalf("domain_id = %q, want empty so the domain constraint does not apply", stored.DomainID)
	}
}
