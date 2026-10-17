package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCapacityOperationFencesAttemptAndLeaseAndCommitsWarmSize(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "capacity-owner.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	pool := &FleetPool{ID: "capacity-pool", Name: "capacity pool", Status: "active", DesiredSize: 2}
	if err := store.SaveFleetPool(pool); err != nil {
		t.Fatal(err)
	}
	request := &ProvisioningRequest{ID: "capacity-request", FleetPoolID: pool.ID, Revision: 1, AttemptToken: "attempt-a", Status: "provisioning", DesiredCount: 4}
	if err := store.SaveProvisioningRequest(request); err != nil {
		t.Fatal(err)
	}
	intent := &CapacityInstanceIntent{ID: "capacity-intent", ExecutionTargetID: "target", FleetPoolID: pool.ID, ProvisioningRequestID: request.ID, ClientToken: "token", LaunchNonce: "nonce", GeneratorID: "generator"}
	if err := store.CreateCapacityInstanceIntent(intent); err != nil {
		t.Fatal(err)
	}
	held, lease, err := store.TryAcquireSchedulerLeaseTerm("capacity-lease", "owner", time.Now(), time.Minute)
	if err != nil || !held {
		t.Fatalf("acquire lease: %v, %v", held, err)
	}
	if _, err := store.db.Exec(`UPDATE scheduler_leases SET expires_at=? WHERE name=?`, time.Now().Add(-time.Minute), "capacity-lease"); err != nil {
		t.Fatal(err)
	}
	if acquired, _, err := store.TryAcquireSchedulerLeaseTerm("capacity-lease", "intervening-owner", time.Now(), time.Minute); err != nil || !acquired {
		t.Fatalf("intervening takeover: %v, %v", acquired, err)
	}
	if err := store.ReleaseSchedulerLease("capacity-lease", "intervening-owner"); err != nil {
		t.Fatal(err)
	}
	held, successor, err := store.TryAcquireSchedulerLeaseTerm("capacity-lease", "owner", time.Now(), time.Minute)
	if err != nil || !held || successor.Generation <= lease.Generation {
		t.Fatalf("reacquire: %v, %+v, %v", held, successor, err)
	}
	for _, tc := range []struct {
		name, token string
		generation  uint64
		want        bool
	}{
		{"stale_generation", request.AttemptToken, lease.Generation, false},
		{"stale_attempt", "wrong-attempt", successor.Generation, false},
		{"current_owner", request.AttemptToken, successor.Generation, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := *intent
			candidate.ProviderID, candidate.State = "provider-instance", CapacityIntentReady
			changed, err := store.UpdateCapacityInstanceIntentIfAttemptAndLeaseTermHeld(&candidate, intent.UpdatedAt, request.ID, tc.token, "capacity-lease", "owner", tc.generation)
			if err != nil || changed != tc.want {
				t.Fatalf("intent update = %v, %v", changed, err)
			}
			status := "ready"
			_, metadataErr := store.PatchGeneratorMetadataIfCapacityAttemptHeld(intent.GeneratorID, GeneratorMetadataPatch{FleetPoolIDSet: true, FleetPoolID: &pool.ID, ProviderStatus: &status}, request.ID, tc.token, "capacity-lease", "owner", tc.generation)
			if (metadataErr == nil) != tc.want {
				t.Fatalf("membership update error = %v", metadataErr)
			}
			metadata, metadataReadErr := store.GetGeneratorMetadata(intent.GeneratorID)
			members, membersErr := store.ListFleetPoolMembers(pool.ID)
			if membersErr != nil {
				t.Fatal(membersErr)
			}
			if tc.want {
				if metadataReadErr != nil || metadata.FleetPoolID == nil || *metadata.FleetPoolID != pool.ID || metadata.ProviderStatus != "ready" || len(members) != 1 || members[0].GeneratorID != intent.GeneratorID {
					t.Fatalf("current membership metadata=%+v members=%+v err=%v", metadata, members, metadataReadErr)
				}
			} else if !errors.Is(metadataReadErr, sql.ErrNoRows) || len(members) != 0 {
				t.Fatalf("stale owner mutated membership: metadata=%+v members=%+v err=%v", metadata, members, metadataReadErr)
			}
			next := *request
			next.Revision++
			next.Status = "success"
			changed, err = store.CommitCapacityOperation(&next, request.Revision, tc.token, "capacity-lease", "owner", tc.generation, true)
			if err != nil || changed != tc.want {
				t.Fatalf("request commit = %v, %v", changed, err)
			}
			got, err := store.GetFleetPool(pool.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := 2
			if tc.want {
				want = 4
			}
			if got.DesiredSize != want {
				t.Fatalf("warm size=%d, want %d", got.DesiredSize, want)
			}
		})
	}
}

func TestCapacityOperationRollsBackRequestWhenWarmPublicationIsInvalid(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "capacity-atomic.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	request := &ProvisioningRequest{ID: "invalid-warm", Revision: 1, AttemptToken: "attempt", Status: "provisioning", FleetPoolID: "pool"}
	if err := store.SaveProvisioningRequest(request); err != nil {
		t.Fatal(err)
	}
	next := *request
	next.Revision++
	next.Status = "failed"
	if changed, err := store.CommitCapacityOperation(&next, 1, "attempt", "", "", 0, true); err == nil || changed {
		t.Fatalf("invalid warm commit=%v, %v", changed, err)
	}
	got, err := store.GetProvisioningRequest(request.ID)
	if err != nil || got.Revision != 1 || got.Status != "provisioning" {
		t.Fatalf("failed commit was not atomic: %+v, %v", got, err)
	}
}
