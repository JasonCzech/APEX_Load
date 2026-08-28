package storage

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestDomainFeaturePolicyDefaultsAndCAS(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	policy, err := store.GetDomainFeaturePolicy(DefaultDomainID)
	if err != nil {
		t.Fatal(err)
	}
	if policy.Revision != "0" || len(policy.DisabledFeatureIDs) != 0 {
		t.Fatalf("default policy = %#v", policy)
	}

	updated, ok, err := store.UpdateDomainFeaturePolicyIfRevision(DefaultDomainID, []string{"script_import.postman", "script_import.postman", "future.feature"}, "0")
	if err != nil || !ok {
		t.Fatalf("initial update = %#v, %t, %v", updated, ok, err)
	}
	if updated.Revision == "0" || len(updated.DisabledFeatureIDs) != 2 {
		t.Fatalf("updated policy = %#v", updated)
	}
	if _, ok, err := store.UpdateDomainFeaturePolicyIfRevision(DefaultDomainID, nil, "0"); err != nil || ok {
		t.Fatalf("stale create CAS = %t, %v", ok, err)
	}
	if _, ok, err := store.UpdateDomainFeaturePolicyIfRevision(DefaultDomainID, nil, "stale"); err != nil || ok {
		t.Fatalf("stale update CAS = %t, %v", ok, err)
	}
}

func TestDomainFeaturePolicyDeletedWithDomain(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	domain := &Domain{ID: "feature-domain", Name: "Feature Domain", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveDomain(domain); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.UpdateDomainFeaturePolicyIfRevision(domain.ID, []string{"script_import.postman"}, "0"); err != nil || !ok {
		t.Fatalf("save policy = %t, %v", ok, err)
	}
	if err := store.DeleteDomain(domain.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetDomainFeaturePolicy(domain.ID); err == nil {
		t.Fatal("expected deleted domain policy lookup to fail")
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM domain_feature_policies WHERE domain_id = ?`, domain.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("policy rows after delete = %d, %v", count, err)
	}
}

func TestDomainFeaturePolicyConcurrentCASReturnsWinningRevision(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	initial, ok, err := store.UpdateDomainFeaturePolicyIfRevision(DefaultDomainID, nil, "0")
	if err != nil || !ok {
		t.Fatalf("initial policy = %#v, %t, %v", initial, ok, err)
	}

	type result struct {
		policy *DomainFeaturePolicy
		ok     bool
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, disabled := range [][]string{{"script_import.postman"}, {"workload_shape.stress"}} {
		disabled := disabled
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			policy, updated, updateErr := store.UpdateDomainFeaturePolicyIfRevision(DefaultDomainID, disabled, initial.Revision)
			results <- result{policy: policy, ok: updated, err: updateErr}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	winners := 0
	var winner *DomainFeaturePolicy
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.ok {
			winners++
			winner = result.policy
		}
	}
	if winners != 1 || winner == nil {
		t.Fatalf("CAS winners = %d, policy = %#v", winners, winner)
	}
	persisted, err := store.GetDomainFeaturePolicy(DefaultDomainID)
	if err != nil {
		t.Fatal(err)
	}
	if winner.Revision != persisted.Revision || len(winner.DisabledFeatureIDs) != 1 || winner.DisabledFeatureIDs[0] != persisted.DisabledFeatureIDs[0] {
		t.Fatalf("returned winner %#v does not match persisted policy %#v", winner, persisted)
	}
}
