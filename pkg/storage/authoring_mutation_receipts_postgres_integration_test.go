//go:build postgres_integration

package storage

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPostgresAuthoringMutationReceipts(t *testing.T) {
	store, _ := newPostgresScenarioRevisionFixture(t)
	now := time.Now().UTC()
	claim, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-1"), now, time.Minute)
	if err != nil || !claim.Created {
		t.Fatalf("first claim: %#v %v", claim, err)
	}
	if _, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-2"), now, time.Minute); !errors.Is(err, ErrAuthoringReceiptBusy) {
		t.Fatalf("leased claim: %v", err)
	}
	if done, err := store.CompleteAuthoringMutationReceipt(claim.Receipt.ID, "owner-1", "rev-1", now); err != nil || !done {
		t.Fatalf("complete: %v %v", done, err)
	}
	replay, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-3"), now, time.Minute)
	if err != nil || !replay.Replay || replay.Receipt.ResourceRevision != "rev-1" {
		t.Fatalf("replay: %#v %v", replay, err)
	}
	changed := authoringReceiptInput("owner-4")
	changed.RequestDigest = "hmac-sha256:other"
	if _, err := store.ClaimAuthoringMutationReceipt(changed, now, time.Minute); !errors.Is(err, ErrAuthoringReceiptConflict) {
		t.Fatalf("changed request: %v", err)
	}
	if err := store.TombstoneAuthoringMutationReceipts("script", "derived-script-id", "deleted", now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-5"), now, time.Minute); !errors.Is(err, ErrAuthoringReceiptTombstoned) {
		t.Fatalf("tombstoned: %v", err)
	}
	concurrent := authoringReceiptInput("")
	concurrent.KeyHash = "hmac-sha256:concurrent"
	var wg sync.WaitGroup
	created := make([]bool, 6)
	for i := range created {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			in := concurrent
			in.OwnerToken = "concurrent-" + string(rune('a'+i))
			claim, err := store.ClaimAuthoringMutationReceipt(in, now, time.Minute)
			if err != nil && !errors.Is(err, ErrAuthoringReceiptBusy) {
				t.Errorf("concurrent claim: %v", err)
			}
			created[i] = err == nil && claim.Created
		}(i)
	}
	wg.Wait()
	owners := 0
	for _, ok := range created {
		if ok {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("concurrent owners = %d", owners)
	}
}
