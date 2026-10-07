//go:build postgres_integration

package storage

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
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

func TestPostgresAuthoringReceiptTombstoneRestore(t *testing.T) {
	store, _ := newPostgresScenarioRevisionFixture(t)
	exerciseAuthoringReceiptTombstoneRestore(t, store)
}

func TestPostgresAuthoringReceiptTombstoneRenewal(t *testing.T) {
	store, _ := newPostgresScenarioRevisionFixture(t)
	exerciseAuthoringReceiptTombstoneRenewal(t, store)
}

// Under READ COMMITTED a refused delete's restore reads the resource before
// the winning delete commits, then waits on the receipt row the winner is
// renewing. PostgreSQL re-checks the restore's condition against the renewed
// row, which no longer carries the loser's tombstone time, and skips it.
func TestPostgresAuthoringReceiptRenewalOutlastsAWaitingRestore(t *testing.T) {
	store, databaseURL := newPostgresScenarioRevisionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	appName := parsed.Query().Get("application_name")
	if err := store.SaveScript(&dsl.Script{ID: "derived-script-id", Name: "Derived", Version: "1.0.0", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}}}); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	claim, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-1"), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteAuthoringMutationReceipt(claim.Receipt.ID, "owner-1", "rev-1", now); err != nil {
		t.Fatal(err)
	}
	loser := now.Add(time.Second)
	if err := store.TombstoneAuthoringMutationReceipts("script", "derived-script-id", "deleted", loser); err != nil {
		t.Fatal(err)
	}
	// The winner deletes and renews; it has not committed when the loser's
	// restore starts, so the restore still sees the resource.
	winner, err := store.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer winner.Rollback()
	if _, err := winner.ExecContext(ctx, `DELETE FROM scripts WHERE id = ?`, "derived-script-id"); err != nil {
		t.Fatal(err)
	}
	if err := renewAuthoringMutationReceiptTombstones(winner, "script", "derived-script-id", "deleted", loser.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	restored := make(chan error, 1)
	go func() {
		restored <- store.RestoreAuthoringMutationReceiptTombstones("script", "derived-script-id", "deleted", loser)
	}()
	for waiting := false; !waiting; {
		if err := store.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=? AND wait_event_type='Lock' AND query LIKE 'UPDATE authoring_mutation_receipts SET tombstoned_at = NULL%')`, appName).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-restored:
			t.Fatalf("the restore did not wait for the renewing delete: %v", err)
		case <-ctx.Done():
			t.Fatal("the restore never waited on the receipt row")
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := winner.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-restored; err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimAuthoringMutationReceipt(authoringReceiptInput("owner-2"), now, time.Minute); !errors.Is(err, ErrAuthoringReceiptTombstoned) {
		t.Fatalf("a waiting restore withdrew the renewed tombstone: %v", err)
	}
}

func TestPostgresExclusivePolicyDefaults(t *testing.T) {
	store, _ := newPostgresScenarioRevisionFixture(t)
	exerciseExclusivePolicyDefaults(t, store)
}
