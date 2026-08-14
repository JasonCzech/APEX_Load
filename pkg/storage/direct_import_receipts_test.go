package storage

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestDirectImportReceiptLeaseFencesConcurrentRetryAndReplaysCompletion(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Round(0)
	first, err := store.ClaimDirectImportReceipt(
		DefaultProjectID, "jmeter", "operation-hash", "request-hash",
		"owner-one", now, time.Minute,
	)
	if err != nil {
		t.Fatalf("claim first receipt: %v", err)
	}
	if first.Receipt == nil || !first.Created || first.Replay {
		t.Fatalf("first claim = %+v", first)
	}
	if _, err := store.ClaimDirectImportReceipt(
		DefaultProjectID, "jmeter", "operation-hash", "request-hash",
		"owner-two", now.Add(10*time.Second), time.Minute,
	); !errors.Is(err, ErrDirectImportReceiptBusy) {
		t.Fatalf("concurrent claim error = %v, want busy", err)
	}

	stale, err := store.ClaimDirectImportReceipt(
		DefaultProjectID, "jmeter", "operation-hash", "request-hash",
		"owner-two", now.Add(2*time.Minute), time.Minute,
	)
	if err != nil {
		t.Fatalf("claim stale receipt: %v", err)
	}
	if stale.Created || stale.Replay || stale.Receipt.OwnerToken != "owner-two" {
		t.Fatalf("stale claim = %+v", stale)
	}
	if renewed, err := store.RenewDirectImportReceipt(
		first.Receipt.ID, "owner-one", now.Add(2*time.Minute), time.Minute,
	); err != nil || renewed {
		t.Fatalf("stale owner renewal = %t, %v; want false", renewed, err)
	}
	if completed, err := store.CompleteDirectImportReceipt(
		first.Receipt.ID, "owner-one", []string{"script-1"}, "", now.Add(2*time.Minute),
	); err != nil || completed {
		t.Fatalf("stale owner completion = %t, %v; want false", completed, err)
	}
	if completed, err := store.CompleteDirectImportReceipt(
		stale.Receipt.ID, "owner-two", []string{"script-1", "script-2"}, "scenario-1",
		now.Add(2*time.Minute),
	); err != nil || !completed {
		t.Fatalf("current owner completion = %t, %v; want true", completed, err)
	}

	replay, err := store.ClaimDirectImportReceipt(
		DefaultProjectID, "jmeter", "operation-hash", "request-hash",
		"owner-three", now.Add(3*time.Minute), time.Minute,
	)
	if err != nil {
		t.Fatalf("claim completed receipt: %v", err)
	}
	if !replay.Replay || replay.Receipt.State != DirectImportReceiptComplete ||
		len(replay.Receipt.ScriptIDs) != 2 ||
		replay.Receipt.ScriptIDs[0] != "script-1" ||
		replay.Receipt.ScriptIDs[1] != "script-2" ||
		replay.Receipt.ScenarioID != "scenario-1" {
		t.Fatalf("completed replay = %+v", replay)
	}
	if _, err := store.ClaimDirectImportReceipt(
		DefaultProjectID, "jmeter", "operation-hash", "different-request",
		"owner-four", now.Add(4*time.Minute), time.Minute,
	); !errors.Is(err, ErrDirectImportReceiptConflict) {
		t.Fatalf("conflicting replay error = %v, want conflict", err)
	}
}

func TestDirectImportReceiptAbandonRequiresCurrentOwner(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	claim, err := store.ClaimDirectImportReceipt(
		DefaultProjectID, "browser_recorder", "operation-hash", "request-hash",
		"owner", now, time.Minute,
	)
	if err != nil {
		t.Fatalf("claim receipt: %v", err)
	}
	if abandoned, err := store.AbandonDirectImportReceipt(claim.Receipt.ID, "other"); err != nil || abandoned {
		t.Fatalf("foreign abandon = %t, %v; want false", abandoned, err)
	}
	if abandoned, err := store.AbandonDirectImportReceipt(claim.Receipt.ID, "owner"); err != nil || !abandoned {
		t.Fatalf("owner abandon = %t, %v; want true", abandoned, err)
	}
}

func TestRecoverDirectImportReceiptRequiresExactDurableEvidence(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "receipt-recovery.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC().Round(0)
	claim, err := store.ClaimDirectImportReceipt(
		DefaultProjectID, "browser_recorder", "operation-hash", "request-hash",
		"lost-owner", now, time.Hour,
	)
	if err != nil {
		t.Fatalf("claim receipt: %v", err)
	}
	if recovered, err := store.RecoverDirectImportReceipt(
		claim.Receipt.ID, DefaultProjectID, "browser_recorder", "operation-hash", "wrong-request",
		[]string{"script-1", "script-2"}, "", now.Add(time.Second),
	); err != nil || recovered {
		t.Fatalf("mismatched recovery = %t, %v; want false", recovered, err)
	}
	if recovered, err := store.RecoverDirectImportReceipt(
		claim.Receipt.ID, DefaultProjectID, "browser_recorder", "operation-hash", "request-hash",
		[]string{"script-1", "script-2"}, "", now.Add(2*time.Second),
	); err != nil || !recovered {
		t.Fatalf("exact recovery = %t, %v; want true", recovered, err)
	}
	if recovered, err := store.RecoverDirectImportReceipt(
		claim.Receipt.ID, DefaultProjectID, "browser_recorder", "operation-hash", "request-hash",
		[]string{"script-2", "script-1"}, "", now.Add(3*time.Second),
	); err != nil || recovered {
		t.Fatalf("reordered replay recovery = %t, %v; want false", recovered, err)
	}
	if recovered, err := store.RecoverDirectImportReceipt(
		claim.Receipt.ID, DefaultProjectID, "browser_recorder", "operation-hash", "request-hash",
		[]string{"script-1", "script-2"}, "", now.Add(4*time.Second),
	); err != nil || !recovered {
		t.Fatalf("idempotent exact recovery = %t, %v; want true", recovered, err)
	}
}

func TestDirectImportReceiptReleasePreservesIdentityForImmediateRetry(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer store.Close()
	now := time.Now().UTC()
	first, err := store.ClaimDirectImportReceipt(
		DefaultProjectID, "jmeter", "operation-hash", "request-hash",
		"owner-one", now, time.Minute,
	)
	if err != nil {
		t.Fatalf("claim first receipt: %v", err)
	}
	if released, err := store.ReleaseDirectImportReceipt(
		first.Receipt.ID,
		"other-owner",
		now.Add(time.Second),
	); err != nil || released {
		t.Fatalf("foreign release = %t, %v; want false", released, err)
	}
	if released, err := store.ReleaseDirectImportReceipt(
		first.Receipt.ID,
		"owner-one",
		now.Add(time.Second),
	); err != nil || !released {
		t.Fatalf("owner release = %t, %v; want true", released, err)
	}

	retry, err := store.ClaimDirectImportReceipt(
		DefaultProjectID, "jmeter", "operation-hash", "request-hash",
		"owner-two", now.Add(2*time.Second), time.Minute,
	)
	if err != nil {
		t.Fatalf("claim released receipt: %v", err)
	}
	if retry.Created || retry.Replay || retry.Receipt.ID != first.Receipt.ID {
		t.Fatalf("released receipt retry = %+v, want same pending identity %s", retry, first.Receipt.ID)
	}
	if abandoned, err := store.AbandonDirectImportReceipt(retry.Receipt.ID, "owner-two"); err != nil || !abandoned {
		t.Fatalf("clean up retry receipt = %t, %v", abandoned, err)
	}
}
