package storage

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newAuthoringReceiptTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func authoringReceiptInput(owner string) AuthoringMutationReceiptClaimInput {
	return AuthoringMutationReceiptClaimInput{
		ProjectID: DefaultProjectID, Actor: "user:alice", Purpose: AuthoringPurposeScriptCreate,
		KeyHash: "hmac-sha256:key", RequestDigest: "hmac-sha256:request", ResourceType: "script",
		ResourceID: "derived-script-id", OwnerToken: owner, Retention: 24 * time.Hour,
	}
}

func TestAuthoringReceiptClaimCompleteAndReplay(t *testing.T) {
	store := newAuthoringReceiptTestStore(t)
	now := time.Now().UTC()
	claim, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-1"), now, time.Minute)
	if err != nil || !claim.Created || claim.Replay || claim.Receipt.ResourceID != "derived-script-id" {
		t.Fatalf("first claim: %#v %v", claim, err)
	}
	if _, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-2"), now, time.Minute); !errors.Is(err, ErrAuthoringReceiptBusy) {
		t.Fatalf("concurrent claim while leased: %v", err)
	}
	if done, err := store.CompleteAuthoringMutationReceipt(claim.Receipt.ID, "owner-2", "rev-1", now); err != nil || done {
		t.Fatalf("non-owner completed the receipt: %v %v", done, err)
	}
	if done, err := store.CompleteAuthoringMutationReceipt(claim.Receipt.ID, "owner-1", "rev-1", now); err != nil || !done {
		t.Fatalf("owner completion: %v %v", done, err)
	}
	replay, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-3"), now.Add(time.Hour), time.Minute)
	if err != nil || !replay.Replay || replay.Receipt.ResourceRevision != "rev-1" || replay.Receipt.ResourceID != "derived-script-id" {
		t.Fatalf("replay: %#v %v", replay, err)
	}
}

func TestAuthoringReceiptRefusesChangedRequestExpiryAndDeletedResource(t *testing.T) {
	store := newAuthoringReceiptTestStore(t)
	now := time.Now().UTC()
	claim, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-1"), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteAuthoringMutationReceipt(claim.Receipt.ID, "owner-1", "rev-1", now); err != nil {
		t.Fatal(err)
	}
	changed := authoringReceiptInput("owner-2")
	changed.RequestDigest = "hmac-sha256:other"
	if _, err := store.ClaimAuthoringMutationReceipt(changed, now, time.Minute); !errors.Is(err, ErrAuthoringReceiptConflict) {
		t.Fatalf("changed request: %v", err)
	}
	if _, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-2"), now.Add(25*time.Hour), time.Minute); !errors.Is(err, ErrAuthoringReceiptExpired) {
		t.Fatalf("expired key: %v", err)
	}
	if err := store.TombstoneAuthoringMutationReceipts("script", "derived-script-id", "deleted", now); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{now, now.Add(25 * time.Hour)} {
		if _, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-2"), at, time.Minute); !errors.Is(err, ErrAuthoringReceiptTombstoned) {
			t.Fatalf("deleted resource at %s: %v", at, err)
		}
	}
}

func TestAuthoringReceiptStalePendingIsTakenOverAndAbandonReleasesKey(t *testing.T) {
	store := newAuthoringReceiptTestStore(t)
	now := time.Now().UTC()
	first, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-1"), now, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	takeover, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-2"), now.Add(2*time.Second), time.Minute)
	if err != nil || takeover.Created || takeover.Replay || takeover.Receipt.ID != first.Receipt.ID || takeover.Receipt.OwnerToken != "owner-2" {
		t.Fatalf("stale takeover: %#v %v", takeover, err)
	}
	if done, _ := store.CompleteAuthoringMutationReceipt(first.Receipt.ID, "owner-1", "rev", now); done {
		t.Fatal("the displaced owner completed the receipt")
	}
	if gone, err := store.AbandonAuthoringMutationReceipt(first.Receipt.ID, "owner-2"); err != nil || !gone {
		t.Fatalf("abandon: %v %v", gone, err)
	}
	fresh, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-3"), now.Add(3*time.Second), time.Minute)
	if err != nil || !fresh.Created {
		t.Fatalf("key after abandon: %#v %v", fresh, err)
	}
}

func TestAuthoringReceiptConcurrentClaimsHaveOneOwner(t *testing.T) {
	store := newAuthoringReceiptTestStore(t)
	now := time.Now().UTC()
	var wg sync.WaitGroup
	results := make([]error, 8)
	created := make([]bool, 8)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			claim, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-"+string(rune('a'+i))), now, time.Minute)
			results[i] = err
			created[i] = err == nil && claim.Created
		}(i)
	}
	wg.Wait()
	owners := 0
	for i, err := range results {
		if created[i] {
			owners++
		} else if !errors.Is(err, ErrAuthoringReceiptBusy) {
			t.Fatalf("claim %d: %v", i, err)
		}
	}
	if owners != 1 {
		t.Fatalf("owners = %d, want 1", owners)
	}
}

func TestAuthoringReceiptsAreScopedByActorAndPurpose(t *testing.T) {
	store := newAuthoringReceiptTestStore(t)
	now := time.Now().UTC()
	base := authoringReceiptInput("owner-1")
	if _, err := store.ClaimAuthoringMutationReceipt(base, now, time.Minute); err != nil {
		t.Fatal(err)
	}
	otherActor := base
	otherActor.Actor, otherActor.OwnerToken = "user:bob", "owner-2"
	otherPurpose := base
	otherPurpose.Purpose, otherPurpose.ResourceType, otherPurpose.OwnerToken = AuthoringPurposeScenarioCreate, "scenario", "owner-3"
	for _, in := range []AuthoringMutationReceiptClaimInput{otherActor, otherPurpose} {
		if claim, err := store.ClaimAuthoringMutationReceipt(in, now, time.Minute); err != nil || !claim.Created {
			t.Fatalf("independent scope %s/%s: %#v %v", in.Actor, in.Purpose, claim, err)
		}
	}
}

// exerciseAuthoringReceiptTombstoneRestore withdraws only the tombstones a
// refused delete wrote, never an earlier one.
func exerciseAuthoringReceiptTombstoneRestore(t *testing.T, store *SQLiteStore) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	claim, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-1"), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteAuthoringMutationReceipt(claim.Receipt.ID, "owner-1", "rev-1", now); err != nil {
		t.Fatal(err)
	}
	refused := now.Add(time.Second)
	if err := store.TombstoneAuthoringMutationReceipts("script", "derived-script-id", "deleted", refused); err != nil {
		t.Fatal(err)
	}
	if err := store.RestoreAuthoringMutationReceiptTombstones("script", "derived-script-id", "deleted", refused.Add(time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-2"), now, time.Minute); !errors.Is(err, ErrAuthoringReceiptTombstoned) {
		t.Fatalf("a different delete's restore withdrew the tombstone: %v", err)
	}
	if err := store.RestoreAuthoringMutationReceiptTombstones("script", "derived-script-id", "deleted", refused); err != nil {
		t.Fatal(err)
	}
	replay, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-2"), now, time.Minute)
	if err != nil || !replay.Replay || replay.Receipt.ResourceRevision != "rev-1" {
		t.Fatalf("restored receipt does not replay: %#v %v", replay, err)
	}
	deleted := now.Add(2 * time.Second)
	if err := store.TombstoneAuthoringMutationReceipts("script", "derived-script-id", "deleted", deleted); err != nil {
		t.Fatal(err)
	}
	// A later refused delete of a recreated resource cannot revive it.
	if err := store.TombstoneAuthoringMutationReceipts("script", "derived-script-id", "deleted", deleted.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := store.RestoreAuthoringMutationReceiptTombstones("script", "derived-script-id", "deleted", deleted.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-3"), now, time.Minute); !errors.Is(err, ErrAuthoringReceiptTombstoned) {
		t.Fatalf("committed delete's tombstone was withdrawn: %v", err)
	}
}

func TestAuthoringReceiptTombstoneRestore(t *testing.T) {
	exerciseAuthoringReceiptTombstoneRestore(t, newAuthoringReceiptTestStore(t))
}
