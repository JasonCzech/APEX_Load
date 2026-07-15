package storage

import (
	"database/sql"
	"errors"
	"sync"
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

func TestFleetPoolAtomicCreateAndOptimisticMemberUpdate(t *testing.T) {
	store := openTimeslotStore(t)
	bad := &FleetPool{ID: "pool-bad", Name: "bad", CloudProvider: "kubernetes"}
	if err := store.CreateFleetPoolWithMembers(bad, []*FleetPoolMember{
		{ID: "duplicate-member", GeneratorID: "gen-a"},
		{ID: "duplicate-member", GeneratorID: "gen-b"},
	}); err == nil {
		t.Fatal("CreateFleetPoolWithMembers accepted duplicate member primary keys")
	}
	if _, err := store.GetFleetPool(bad.ID); err == nil {
		t.Fatal("failed atomic create left a partial fleet pool")
	}

	pool := &FleetPool{ID: "pool-cas", Name: "original", CloudProvider: "kubernetes"}
	if err := store.CreateFleetPoolWithMembers(pool, []*FleetPoolMember{{ID: "member-original", GeneratorID: "gen-original"}}); err != nil {
		t.Fatalf("CreateFleetPoolWithMembers: %v", err)
	}
	first, err := store.GetFleetPool(pool.ID)
	if err != nil {
		t.Fatalf("GetFleetPool first: %v", err)
	}
	stale, err := store.GetFleetPool(pool.ID)
	if err != nil {
		t.Fatalf("GetFleetPool stale: %v", err)
	}
	first.Name = "first writer"
	updated, err := store.UpdateFleetPoolIfUnchanged(first, []*FleetPoolMember{{ID: "member-first", GeneratorID: "gen-first"}}, true, first.UpdatedAt)
	if err != nil || !updated {
		t.Fatalf("first update = (%v, %v), want success", updated, err)
	}
	stale.Name = "stale writer"
	updated, err = store.UpdateFleetPoolIfUnchanged(stale, nil, false, stale.UpdatedAt)
	if err != nil || updated {
		t.Fatalf("stale update = (%v, %v), want conflict", updated, err)
	}
	fresh, err := store.GetFleetPool(pool.ID)
	if err != nil || fresh.Name != "first writer" || len(fresh.Members) != 1 || fresh.Members[0].GeneratorID != "gen-first" {
		t.Fatalf("fresh fleet pool = %#v, err=%v", fresh, err)
	}

	expected := fresh.UpdatedAt
	fresh.Name = "must roll back"
	updated, err = store.UpdateFleetPoolIfUnchanged(fresh, []*FleetPoolMember{
		{ID: "duplicate-update-member", GeneratorID: "gen-a"},
		{ID: "duplicate-update-member", GeneratorID: "gen-b"},
	}, true, expected)
	if err == nil || updated {
		t.Fatalf("invalid member update = (%v, %v), want atomic failure", updated, err)
	}
	fresh, err = store.GetFleetPool(pool.ID)
	if err != nil || fresh.Name != "first writer" || len(fresh.Members) != 1 || fresh.Members[0].GeneratorID != "gen-first" {
		t.Fatalf("failed member update changed pool = %#v, err=%v", fresh, err)
	}

	if err := store.UpdateFleetPoolDesiredSize(pool.ID, 7, time.Now().UTC()); err != nil {
		t.Fatalf("UpdateFleetPoolDesiredSize: %v", err)
	}
	fresh, err = store.GetFleetPool(pool.ID)
	if err != nil || fresh.DesiredSize != 7 || fresh.Name != "first writer" {
		t.Fatalf("targeted desired-size update changed other fields: %#v, err=%v", fresh, err)
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

func TestGeneratorMetadataPatchesMergeConcurrentFields(t *testing.T) {
	store := openTimeslotStore(t)
	target := "host:7070"
	if _, err := store.PatchGeneratorMetadata("gen-patch", GeneratorMetadataPatch{Target: &target}); err != nil {
		t.Fatalf("initial PatchGeneratorMetadata: %v", err)
	}
	region := "eastus"
	cordoned := true
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, patch := range []GeneratorMetadataPatch{{Region: &region}, {Cordoned: &cordoned}} {
		patch := patch
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := store.PatchGeneratorMetadata("gen-patch", patch)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent PatchGeneratorMetadata: %v", err)
		}
	}
	got, err := store.GetGeneratorMetadata("gen-patch")
	if err != nil {
		t.Fatalf("GetGeneratorMetadata: %v", err)
	}
	if got.Target != target || got.Region != region || !got.Cordoned {
		t.Fatalf("merged metadata = %#v, want target, region, and cordon preserved", got)
	}
}

func TestGeneratorMetadataPatchRejectsDeletedFleetPool(t *testing.T) {
	store := openTimeslotStore(t)
	pool := &FleetPool{ID: "pool-deleted-before-patch", Name: "deleted", CloudProvider: "static"}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatalf("SaveFleetPool: %v", err)
	}
	if err := store.DeleteFleetPool(pool.ID); err != nil {
		t.Fatalf("DeleteFleetPool: %v", err)
	}
	if _, err := store.PatchGeneratorMetadata("gen-dangling", GeneratorMetadataPatch{
		FleetPoolIDSet: true,
		FleetPoolID:    &pool.ID,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("PatchGeneratorMetadata error = %v, want sql.ErrNoRows", err)
	}
	if _, err := store.GetGeneratorMetadata("gen-dangling"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("GetGeneratorMetadata error = %v, want no dangling metadata row", err)
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

func TestFleetAlertAckRoundTrip(t *testing.T) {
	store := openTimeslotStore(t)
	ack := &FleetAlertAck{
		ID:                    "alert-1",
		AlertType:             "saturation",
		Target:                "apexload-generator-0",
		Region:                "eastus",
		Severity:              "danger",
		Summary:               "generator is saturated",
		Note:                  "paging platform team",
		AcknowledgedBySubject: "ops@example.com",
		AcknowledgedAt:        time.Now().UTC().Add(-time.Minute),
	}
	if err := store.SaveFleetAlertAck(ack); err != nil {
		t.Fatalf("SaveFleetAlertAck returned error: %v", err)
	}
	acks, err := store.ListFleetAlertAcks()
	if err != nil {
		t.Fatalf("ListFleetAlertAcks returned error: %v", err)
	}
	if len(acks) != 1 {
		t.Fatalf("acks = %d, want 1", len(acks))
	}
	if acks[0].ID != "alert-1" || acks[0].Note != "paging platform team" || acks[0].AcknowledgedBySubject != "ops@example.com" {
		t.Fatalf("ack not preserved: %+v", acks[0])
	}
	ack.Note = "handled"
	if err := store.SaveFleetAlertAck(ack); err != nil {
		t.Fatalf("update SaveFleetAlertAck returned error: %v", err)
	}
	acks, err = store.ListFleetAlertAcks()
	if err != nil {
		t.Fatalf("ListFleetAlertAcks after update returned error: %v", err)
	}
	if len(acks) != 1 || acks[0].Note != "handled" {
		t.Fatalf("updated acks = %+v, want one handled ack", acks)
	}
	if err := store.DeleteFleetAlertAck("alert-1"); err != nil {
		t.Fatalf("DeleteFleetAlertAck returned error: %v", err)
	}
	acks, err = store.ListFleetAlertAcks()
	if err != nil {
		t.Fatalf("ListFleetAlertAcks after delete returned error: %v", err)
	}
	if len(acks) != 0 {
		t.Fatalf("acks after delete = %d, want 0", len(acks))
	}
}
