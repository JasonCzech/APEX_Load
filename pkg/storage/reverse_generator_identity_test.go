package storage

import (
	"sync"
	"testing"
)

func TestReverseGeneratorOwnership(t *testing.T) {
	checkReverseGeneratorOwnership(t, newComputeConnectionTestStore(t))
}

func checkReverseGeneratorOwnership(t *testing.T, store *SQLiteStore) {
	t.Helper()
	for _, test := range []struct {
		target, generator string
		want              bool
		wantErr           bool
	}{
		{"target-a", "warm-generator", true, false},
		{"target-a", "warm-generator", true, false},
		{"target-b", "warm-generator", false, false},
		{"", "warm-generator", false, true},
		{"target-a", "", false, true},
		{"target-a", "tunnel://warm-generator", false, true},
		{"target-a", "warm-generator|region=other", false, true},
		{"target-a", "warm-generator?identity=other", false, true},
	} {
		got, err := store.ClaimReverseGeneratorIdentity(test.target, test.generator)
		if got != test.want || (err != nil) != test.wantErr {
			t.Fatalf("claim(%q, %q) = %v, %v", test.target, test.generator, got, err)
		}
	}
	intent := &CapacityInstanceIntent{ID: "owned-intent", ExecutionTargetID: "target-a", FleetPoolID: "pool-a", ClientToken: "owned-client", LaunchNonce: "nonce", GeneratorID: "planned-generator"}
	if err := store.CreateCapacityInstanceIntent(intent); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimReverseGeneratorIdentity("target-b", intent.GeneratorID); claimed || err != nil {
		t.Fatalf("claim stole a not-yet-enrolled launch identity: %v, %v", claimed, err)
	}
	conflicting := *intent
	conflicting.ID, conflicting.ExecutionTargetID, conflicting.ClientToken = "conflicting-intent", "target-b", "conflicting-client"
	if err := store.CreateCapacityInstanceIntent(&conflicting); err == nil {
		t.Fatal("capacity intent reused another target's identity")
	}
	invalid := *intent
	invalid.ID, invalid.ClientToken, invalid.GeneratorID = "invalid-intent", "invalid-client", "tunnel://planned-generator"
	if err := store.CreateCapacityInstanceIntent(&invalid); err == nil {
		t.Fatal("capacity intent accepted an aliased generator identity")
	}
	// An old database has capacity intents but no ownership rows yet. Its first
	// enrollment must preserve the recorded owner, not whichever token arrives.
	if _, err := store.db.Exec(`DELETE FROM reverse_generator_owners WHERE generator_id = ?`, intent.GeneratorID); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimReverseGeneratorIdentity("target-b", intent.GeneratorID); claimed || err != nil {
		t.Fatalf("legacy intent authority was overwritten: %v, %v", claimed, err)
	}
	if claimed, err := store.ClaimReverseGeneratorIdentity("target-a", intent.GeneratorID); !claimed || err != nil {
		t.Fatalf("rejected claim poisoned legitimate legacy owner: %v, %v", claimed, err)
	}

	// A failed capacity insert must not leave an unowned identity claimed by a
	// rolled-back transaction.
	duplicate := *intent
	duplicate.GeneratorID = "rolled-back-generator"
	if err := store.CreateCapacityInstanceIntent(&duplicate); err == nil {
		t.Fatal("duplicate capacity intent unexpectedly succeeded")
	}
	if claimed, err := store.ClaimReverseGeneratorIdentity("target-b", duplicate.GeneratorID); !claimed || err != nil {
		t.Fatalf("failed launch insert leaked ownership: %v, %v", claimed, err)
	}

	start := make(chan struct{})
	results := make(chan string, 2)
	var wg sync.WaitGroup
	for _, target := range []string{"target-a", "target-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			claimed, err := store.ClaimReverseGeneratorIdentity(target, "raced-generator")
			if err != nil {
				t.Errorf("concurrent claim: %v", err)
			}
			if claimed {
				results <- target
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	if len(results) != 1 {
		t.Fatalf("concurrent target owners = %d, want 1", len(results))
	}
}

func TestReverseGeneratorOwnershipSurvivesRestart(t *testing.T) {
	path := t.TempDir() + "/reverse-owner.db"
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimReverseGeneratorIdentity("target-a", "warm-generator"); !claimed || err != nil {
		t.Fatalf("first claim = %v, %v", claimed, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if claimed, err := store.ClaimReverseGeneratorIdentity("target-b", "warm-generator"); claimed || err != nil {
		t.Fatalf("restart reset target ownership = %v, %v", claimed, err)
	}
}

func TestReverseGeneratorOwnershipPreservesLegacyPoolBindings(t *testing.T) {
	store := newComputeConnectionTestStore(t)
	// Direct SQL models rows written before the identity ledger existed.
	for _, source := range []string{"member", "metadata"} {
		poolID, generatorID := "pool-"+source, "generator-"+source
		if _, err := store.db.Exec(`INSERT INTO fleet_pools (id, name, cloud_provider, execution_target_id, created_at, updated_at)
			VALUES (?, ?, 'static', 'target-a', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, poolID, poolID); err != nil {
			t.Fatal(err)
		}
		var err error
		if source == "member" {
			_, err = store.db.Exec(`INSERT INTO fleet_pool_members (id, pool_id, generator_id, target, joined_at)
				VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`, "member-id", poolID, generatorID, "tunnel://"+generatorID)
		} else {
			_, err = store.db.Exec(`INSERT INTO generator_metadata (generator_id, fleet_pool_id, target, created_at, updated_at)
				VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`, generatorID, poolID, "tunnel://"+generatorID)
		}
		if err != nil {
			t.Fatal(err)
		}
		if claimed, err := store.ClaimReverseGeneratorIdentity("target-b", generatorID); claimed || err != nil {
			t.Fatalf("legacy %s binding was overwritten: %v, %v", source, claimed, err)
		}
		if claimed, err := store.ClaimReverseGeneratorIdentity("target-a", generatorID); !claimed || err != nil {
			t.Fatalf("legacy %s rightful owner rejected: %v, %v", source, claimed, err)
		}
	}
}
