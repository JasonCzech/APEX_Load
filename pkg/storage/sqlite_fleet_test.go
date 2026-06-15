package storage

import (
	"testing"
	"time"
)

func TestFleetPoolRoundTripAndDeleteClearsRefs(t *testing.T) {
	store := openTimeslotStore(t)

	pool := &FleetPool{
		ID:            "pool-1",
		Name:          "east-http",
		CloudProvider: "kubernetes",
		Region:        "eastus",
		MinSize:       1,
		MaxSize:       10,
		DesiredSize:   3,
		Tags:          map[string]string{"team": "perf", "tier": "gold"},
	}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatalf("save pool: %v", err)
	}
	if err := store.SaveFleetPoolMembers(pool.ID, []*FleetPoolMember{
		{ID: "m1", PoolID: pool.ID, GeneratorID: "gen-1", Target: "10.0.0.1:7070", JoinedAt: time.Now()},
	}); err != nil {
		t.Fatalf("save members: %v", err)
	}
	// A generator that points at the pool — deletion must detach it, not orphan it.
	poolID := pool.ID
	if err := store.SaveGeneratorMetadata(&GeneratorMetadata{GeneratorID: "gen-1", Target: "10.0.0.1:7070", FleetPoolID: &poolID}); err != nil {
		t.Fatalf("save generator metadata: %v", err)
	}

	got, err := store.GetFleetPool("pool-1")
	if err != nil {
		t.Fatalf("get pool: %v", err)
	}
	if got.Status != "active" || got.DesiredSize != 3 || got.Tags["team"] != "perf" {
		t.Fatalf("pool not preserved: %+v", got)
	}
	if len(got.Members) != 1 || got.Members[0].GeneratorID != "gen-1" {
		t.Fatalf("members not preserved: %+v", got.Members)
	}

	pools, err := store.ListFleetPools(10, 0)
	if err != nil || len(pools) != 1 {
		t.Fatalf("list pools = %d (err %v), want 1", len(pools), err)
	}
	if len(pools[0].Members) != 1 {
		t.Fatalf("ListFleetPools should populate members like GetFleetPool, got %d", len(pools[0].Members))
	}

	if err := store.DeleteFleetPool("pool-1"); err != nil {
		t.Fatalf("delete pool: %v", err)
	}
	if members, _ := store.ListFleetPoolMembers("pool-1"); len(members) != 0 {
		t.Fatalf("members not removed on pool delete: %d", len(members))
	}
	gen, err := store.GetGeneratorMetadata("gen-1")
	if err != nil {
		t.Fatalf("get generator after pool delete: %v", err)
	}
	if gen.FleetPoolID != nil {
		t.Fatalf("generator pool ref not cleared on pool delete: %v", *gen.FleetPoolID)
	}
}

func TestGeneratorMetadataUpsert(t *testing.T) {
	store := openTimeslotStore(t)
	if err := store.SaveGeneratorMetadata(&GeneratorMetadata{
		GeneratorID: "gen-x", Target: "host:1", Region: "eu", Cordoned: false,
		Tags: map[string]string{"role": "soak"},
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Upsert: flip cordoned + change region.
	if err := store.SaveGeneratorMetadata(&GeneratorMetadata{
		GeneratorID: "gen-x", Target: "host:1", Region: "us", Cordoned: true,
		Tags: map[string]string{"role": "soak"},
	}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := store.GetGeneratorMetadata("gen-x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Cordoned || got.Region != "us" || got.Tags["role"] != "soak" {
		t.Fatalf("upsert not applied: %+v", got)
	}
	list, err := store.ListGeneratorMetadata(10, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %d (err %v), want 1", len(list), err)
	}
	if err := store.DeleteGeneratorMetadata("gen-x"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetGeneratorMetadata("gen-x"); err == nil {
		t.Fatal("expected error after delete")
	}
}

func TestProvisioningRequestRoundTrip(t *testing.T) {
	store := openTimeslotStore(t)
	req := &ProvisioningRequest{ID: "req-1", FleetPoolID: "pool-1", DesiredCount: 5, Status: "not_configured"}
	if err := store.SaveProvisioningRequest(req); err != nil {
		t.Fatalf("save req: %v", err)
	}
	for _, et := range []string{"requested", "not_configured"} {
		if err := store.SaveProvisioningEvent(&ProvisioningEvent{ID: "ev-" + et, RequestID: "req-1", EventType: et, Timestamp: time.Now()}); err != nil {
			t.Fatalf("save event %s: %v", et, err)
		}
	}
	got, err := store.GetProvisioningRequest("req-1")
	if err != nil {
		t.Fatalf("get req: %v", err)
	}
	if got.DesiredCount != 5 || got.Status != "not_configured" {
		t.Fatalf("req not preserved: %+v", got)
	}
	if len(got.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(got.Events))
	}
	list, err := store.ListProvisioningRequests("pool-1", 10, 0)
	if err != nil || len(list) != 1 {
		t.Fatalf("list by pool = %d (err %v), want 1", len(list), err)
	}
	if other, _ := store.ListProvisioningRequests("pool-other", 10, 0); len(other) != 0 {
		t.Fatalf("pool filter leaked: %d", len(other))
	}
}
