package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAuthoringReceiptOriginalOutcomes(t *testing.T) {
	exerciseAuthoringReceiptOriginalOutcomes(t, newAuthoringReceiptTestStore(t))
}

func originalOutcomeClaim(t *testing.T, store *SQLiteStore, key, owner, resourceType, resourceID string, now time.Time) *AuthoringMutationReceiptClaim {
	t.Helper()
	input := authoringReceiptInput(owner)
	input.KeyHash, input.ResourceType, input.ResourceID, input.OutcomeVersion = key, resourceType, resourceID, AuthoringReceiptOutcomeOriginal
	if resourceType == "scenario" {
		input.Purpose = AuthoringPurposeScenarioCreate
	}
	claim, err := store.ClaimAuthoringMutationReceipt(input, now, time.Minute)
	if err != nil {
		t.Fatalf("claim %s: %v", key, err)
	}
	return claim
}

// exerciseAuthoringReceiptOriginalOutcomes proves the version 2 contract on
// one store: the create and the receipt's original outcome commit together or
// not at all.
func exerciseAuthoringReceiptOriginalOutcomes(t *testing.T, store *SQLiteStore) {
	t.Helper()
	now := time.Now().UTC()
	render := func(snapshot *ScriptAuthoringSnapshot) (string, error) {
		return `{"id":"` + snapshot.Script.ID + `","revision":"` + snapshot.ContentRevision + `"}`, nil
	}
	newScript := func(id string) *dsl.Script {
		return &dsl.Script{ID: id, Name: id, Protocol: "http", Version: "1", ProjectID: DefaultProjectID}
	}

	t.Run("script create and receipt commit together", func(t *testing.T) {
		claim := originalOutcomeClaim(t, store, "key-script", "owner-1", "script", "outcome-script", now)
		if claim.Receipt.OutcomeVersion != AuthoringReceiptOutcomeOriginal || !claim.Created {
			t.Fatalf("claim: %#v", claim.Receipt)
		}
		result, err := store.CreateScriptAuthoringCompletingReceipt(newScript("outcome-script"), AuthoringReceiptCompletion{ReceiptID: claim.Receipt.ID, OwnerToken: "owner-1", Now: now}, render)
		if err != nil || !result.Applied {
			t.Fatalf("create: %#v %v", result, err)
		}
		replay := originalOutcomeClaim(t, store, "key-script", "owner-2", "script", "outcome-script", now)
		want := `{"id":"outcome-script","revision":"` + result.Snapshot.ContentRevision + `"}`
		if !replay.Replay || replay.Receipt.OriginalOutcome != want || replay.Receipt.ResourceRevision != result.Snapshot.ContentRevision {
			t.Fatalf("replay = %#v, want outcome %s", replay.Receipt, want)
		}
	})

	t.Run("a lost reservation rolls the create back", func(t *testing.T) {
		claim := originalOutcomeClaim(t, store, "key-lost", "owner-1", "script", "lost-script", now)
		_, err := store.CreateScriptAuthoringCompletingReceipt(newScript("lost-script"), AuthoringReceiptCompletion{ReceiptID: claim.Receipt.ID, OwnerToken: "someone-else", Now: now}, render)
		if !errors.Is(err, ErrAuthoringReceiptLost) {
			t.Fatalf("create with a foreign owner token: %v", err)
		}
		if _, err := store.GetScriptAuthoringSnapshot("lost-script"); err == nil {
			t.Fatal("script committed although its receipt was not completed")
		}
	})

	t.Run("a tombstoned reservation rolls the create back", func(t *testing.T) {
		claim := originalOutcomeClaim(t, store, "key-tombstoned", "owner-1", "script", "tombstoned-script", now)
		if err := store.TombstoneAuthoringMutationReceipts("script", "tombstoned-script", "deleted", now); err != nil {
			t.Fatal(err)
		}
		_, err := store.CreateScriptAuthoringCompletingReceipt(newScript("tombstoned-script"), AuthoringReceiptCompletion{ReceiptID: claim.Receipt.ID, OwnerToken: "owner-1", Now: now}, render)
		if !errors.Is(err, ErrAuthoringReceiptLost) {
			t.Fatalf("create under a tombstoned receipt: %v", err)
		}
		if _, err := store.GetScriptAuthoringSnapshot("tombstoned-script"); err == nil {
			t.Fatal("script committed under a tombstoned receipt")
		}
	})

	t.Run("a legacy receipt cannot be completed with an original outcome", func(t *testing.T) {
		input := authoringReceiptInput("owner-1")
		input.KeyHash, input.ResourceID = "key-legacy", "legacy-script"
		claim, err := store.ClaimAuthoringMutationReceipt(input, now, time.Minute)
		if err != nil || claim.Receipt.OutcomeVersion != AuthoringReceiptOutcomeLegacy {
			t.Fatalf("legacy claim: %#v %v", claim, err)
		}
		_, err = store.CreateScriptAuthoringCompletingReceipt(newScript("legacy-script"), AuthoringReceiptCompletion{ReceiptID: claim.Receipt.ID, OwnerToken: "owner-1", Now: now}, render)
		if !errors.Is(err, ErrAuthoringReceiptLost) {
			t.Fatalf("legacy receipt completed atomically: %v", err)
		}
	})

	t.Run("a renderer failure or oversized outcome commits nothing", func(t *testing.T) {
		claim := originalOutcomeClaim(t, store, "key-render", "owner-1", "script", "render-script", now)
		failed := errors.New("render failed")
		_, err := store.CreateScriptAuthoringCompletingReceipt(newScript("render-script"), AuthoringReceiptCompletion{ReceiptID: claim.Receipt.ID, OwnerToken: "owner-1", Now: now}, func(*ScriptAuthoringSnapshot) (string, error) { return "", failed })
		if !errors.Is(err, failed) {
			t.Fatalf("renderer failure: %v", err)
		}
		oversized := make([]byte, MaxAuthoringReceiptOutcomeBytes+1)
		_, err = store.CreateScriptAuthoringCompletingReceipt(newScript("render-script"), AuthoringReceiptCompletion{ReceiptID: claim.Receipt.ID, OwnerToken: "owner-1", Now: now}, func(*ScriptAuthoringSnapshot) (string, error) { return string(oversized), nil })
		if err == nil {
			t.Fatal("an outcome beyond the bound was stored")
		}
		if _, err := store.GetScriptAuthoringSnapshot("render-script"); err == nil {
			t.Fatal("script committed without its outcome")
		}
		// The reservation is still this request's to complete.
		if _, err := store.CreateScriptAuthoringCompletingReceipt(newScript("render-script"), AuthoringReceiptCompletion{ReceiptID: claim.Receipt.ID, OwnerToken: "owner-1", Now: now}, render); err != nil {
			t.Fatalf("create after a failed render: %v", err)
		}
	})

	t.Run("an existing ID is not applied and leaves the receipt pending", func(t *testing.T) {
		claim := originalOutcomeClaim(t, store, "key-existing", "owner-1", "script", "outcome-script", now)
		result, err := store.CreateScriptAuthoringCompletingReceipt(newScript("outcome-script"), AuthoringReceiptCompletion{ReceiptID: claim.Receipt.ID, OwnerToken: "owner-1", Now: now}, render)
		if err != nil || result.Applied {
			t.Fatalf("create over an existing script: %#v %v", result, err)
		}
		if _, err := store.ClaimAuthoringMutationReceipt(func() AuthoringMutationReceiptClaimInput {
			input := authoringReceiptInput("owner-2")
			input.KeyHash, input.ResourceID, input.OutcomeVersion = "key-existing", "outcome-script", AuthoringReceiptOutcomeOriginal
			return input
		}(), now, time.Minute); !errors.Is(err, ErrAuthoringReceiptBusy) {
			t.Fatalf("receipt after a refused create: %v", err)
		}
	})

	t.Run("scenario create and receipt commit together", func(t *testing.T) {
		claim := originalOutcomeClaim(t, store, "key-scenario", "owner-1", "scenario", "outcome-scenario", now)
		renderScenario := func(snapshot *ScenarioAuthoringSnapshot) (string, error) {
			return `{"id":"` + snapshot.Scenario.ID + `","revision":"` + snapshot.ContentRevision + `"}`, nil
		}
		_, err := store.CreateScenarioAuthoringCompletingReceipt(&Scenario{ID: "outcome-scenario", Name: "Outcome", Duration: "1m"}, nil, nil,
			AuthoringReceiptCompletion{ReceiptID: claim.Receipt.ID, OwnerToken: "wrong-owner", Now: now}, renderScenario)
		if !errors.Is(err, ErrAuthoringReceiptLost) {
			t.Fatalf("scenario create with a foreign owner token: %v", err)
		}
		if _, err := store.GetScenarioAuthoringSnapshot("outcome-scenario"); err == nil {
			t.Fatal("scenario committed although its receipt was not completed")
		}
		result, err := store.CreateScenarioAuthoringCompletingReceipt(&Scenario{ID: "outcome-scenario", Name: "Outcome", Duration: "1m"}, nil, nil,
			AuthoringReceiptCompletion{ReceiptID: claim.Receipt.ID, OwnerToken: "owner-1", Now: now}, renderScenario)
		if err != nil || !result.Applied {
			t.Fatalf("scenario create: %#v %v", result, err)
		}
		replay := originalOutcomeClaim(t, store, "key-scenario", "owner-2", "scenario", "outcome-scenario", now)
		if want := `{"id":"outcome-scenario","revision":"` + result.Snapshot.ContentRevision + `"}`; !replay.Replay || replay.Receipt.OriginalOutcome != want {
			t.Fatalf("scenario replay = %#v, want %s", replay.Receipt, want)
		}
	})

	t.Run("the first claim fixes the contract", func(t *testing.T) {
		input := authoringReceiptInput("owner-9")
		input.KeyHash, input.ResourceID = "key-script", "outcome-script"
		replay, err := store.ClaimAuthoringMutationReceipt(input, now, time.Minute)
		if err != nil || !replay.Replay || replay.Receipt.OutcomeVersion != AuthoringReceiptOutcomeOriginal {
			t.Fatalf("legacy retry of an original-outcome key: %#v %v", replay, err)
		}
		input.OutcomeVersion = 3
		if _, err := store.ClaimAuthoringMutationReceipt(input, now, time.Minute); err == nil {
			t.Fatal("an unknown receipt contract was accepted")
		}
	})

	t.Run("a committed delete and expiry clear the stored outcome", func(t *testing.T) {
		readOutcome := func(id string) string {
			var outcome string
			if err := store.db.QueryRow(`SELECT original_outcome FROM authoring_mutation_receipts WHERE id = ?`, id).Scan(&outcome); err != nil {
				t.Fatal(err)
			}
			return outcome
		}
		deleted := originalOutcomeClaim(t, store, "key-deleted", "owner-1", "script", "deleted-script", now)
		if _, err := store.CreateScriptAuthoringCompletingReceipt(newScript("deleted-script"), AuthoringReceiptCompletion{ReceiptID: deleted.Receipt.ID, OwnerToken: "owner-1", Now: now}, render); err != nil {
			t.Fatal(err)
		}
		if readOutcome(deleted.Receipt.ID) == "" {
			t.Fatal("outcome not stored")
		}
		if err := store.RenewAuthoringMutationReceiptTombstones("script", "deleted-script", "deleted", now); err != nil {
			t.Fatal(err)
		}
		if readOutcome(deleted.Receipt.ID) != "" {
			t.Fatal("a deleted resource's original outcome was kept")
		}
		live := originalOutcomeClaim(t, store, "key-expiring", "owner-1", "script", "expiring-script", now)
		if _, err := store.CreateScriptAuthoringCompletingReceipt(newScript("expiring-script"), AuthoringReceiptCompletion{ReceiptID: live.Receipt.ID, OwnerToken: "owner-1", Now: now}, render); err != nil {
			t.Fatal(err)
		}
		if scrubbed, err := store.ScrubExpiredAuthoringReceipts(now.Add(time.Hour)); err != nil || scrubbed != 0 || readOutcome(live.Receipt.ID) == "" {
			t.Fatalf("an unexpired receipt was scrubbed: %d %v", scrubbed, err)
		}
		if scrubbed, err := store.ScrubExpiredAuthoringReceipts(now.Add(25 * time.Hour)); err != nil || scrubbed < 1 || readOutcome(live.Receipt.ID) != "" {
			t.Fatalf("expired outcome kept: %d %v", scrubbed, err)
		}
		var digest string
		if err := store.db.QueryRow(`SELECT request_digest FROM authoring_mutation_receipts WHERE id = ?`, live.Receipt.ID).Scan(&digest); err != nil || digest != "" {
			t.Fatalf("expired request digest kept: %q %v", digest, err)
		}
		// A scrubbed key is still answered as expired (or deleted), never as
		// a conflict or a new request.
		retry := authoringReceiptInput("owner-9")
		retry.KeyHash, retry.ResourceID, retry.RequestDigest, retry.OutcomeVersion = "key-expiring", "expiring-script", "hmac-sha256:anything", AuthoringReceiptOutcomeOriginal
		if _, err := store.ClaimAuthoringMutationReceipt(retry, now.Add(25*time.Hour), time.Minute); !errors.Is(err, ErrAuthoringReceiptExpired) {
			t.Fatalf("claim of a scrubbed key: %v", err)
		}
		// A claiming replica whose clock lags the scrub still answers expired.
		if _, err := store.ClaimAuthoringMutationReceipt(retry, now, time.Minute); !errors.Is(err, ErrAuthoringReceiptExpired) {
			t.Fatalf("claim of a scrubbed key by a lagging clock: %v", err)
		}
		if _, err := store.ScrubExpiredAuthoringReceipts(now.Add(25 * time.Hour)); err != nil {
			t.Fatal(err)
		}
		retry.KeyHash, retry.ResourceID = "key-deleted", "deleted-script"
		if _, err := store.ClaimAuthoringMutationReceipt(retry, now.Add(25*time.Hour), time.Minute); !errors.Is(err, ErrAuthoringReceiptTombstoned) {
			t.Fatalf("claim of a scrubbed deleted key: %v", err)
		}
	})

	t.Run("an expired or scrubbed reservation cannot gain an outcome", func(t *testing.T) {
		readOutcome := func(id string) string {
			var outcome string
			if err := store.db.QueryRow(`SELECT original_outcome FROM authoring_mutation_receipts WHERE id = ?`, id).Scan(&outcome); err != nil {
				t.Fatal(err)
			}
			return outcome
		}
		// A create that commits after its key's replay lifetime.
		late := originalOutcomeClaim(t, store, "key-late", "owner-1", "script", "late-script", now)
		if _, err := store.CreateScriptAuthoringCompletingReceipt(newScript("late-script"), AuthoringReceiptCompletion{ReceiptID: late.Receipt.ID, OwnerToken: "owner-1", Now: now.Add(25 * time.Hour)}, render); !errors.Is(err, ErrAuthoringReceiptLost) {
			t.Fatalf("completion of an expired reservation: %v", err)
		}
		if readOutcome(late.Receipt.ID) != "" {
			t.Fatal("an expired reservation stored an outcome")
		}
		if _, err := store.GetScriptAuthoringSnapshot("late-script"); err == nil {
			t.Fatal("the create committed although its receipt could not be completed")
		}
		// A create whose clock lags a scrub that already ran.
		scrubbed := originalOutcomeClaim(t, store, "key-scrubbed", "owner-1", "script", "scrubbed-script", now)
		if _, err := store.ScrubExpiredAuthoringReceipts(now.Add(25 * time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateScriptAuthoringCompletingReceipt(newScript("scrubbed-script"), AuthoringReceiptCompletion{ReceiptID: scrubbed.Receipt.ID, OwnerToken: "owner-1", Now: now.Add(time.Hour)}, render); !errors.Is(err, ErrAuthoringReceiptLost) {
			t.Fatalf("completion of a scrubbed reservation: %v", err)
		}
		if readOutcome(scrubbed.Receipt.ID) != "" {
			t.Fatal("a scrubbed reservation stored an outcome")
		}
	})

	t.Run("a reservation rolled back past its lifetime still answers expired", func(t *testing.T) {
		// Claimed 25 hours ago, so its key can no longer replay.
		past := time.Now().UTC().Add(-25 * time.Hour)
		late := originalOutcomeClaim(t, store, "key-late-retry", "owner-1", "script", "late-retry-script", past)
		if _, err := store.CreateScriptAuthoringCompletingReceipt(newScript("late-retry-script"), AuthoringReceiptCompletion{ReceiptID: late.Receipt.ID, OwnerToken: "owner-1", Now: time.Now().UTC()}, render); !errors.Is(err, ErrAuthoringReceiptLost) {
			t.Fatalf("completion past the lifetime: %v", err)
		}
		// The request's own cleanup cannot remove it ...
		if gone, err := store.AbandonAuthoringMutationReceipt(late.Receipt.ID, "owner-1"); err != nil || gone {
			t.Fatalf("abandon of an expired reservation: %v %v", gone, err)
		}
		// ... so a retry of the key is answered as expired, not claimed afresh.
		retry := authoringReceiptInput("owner-2")
		retry.KeyHash, retry.ResourceType, retry.ResourceID, retry.OutcomeVersion = "key-late-retry", "script", "late-retry-script", AuthoringReceiptOutcomeOriginal
		if _, err := store.ClaimAuthoringMutationReceipt(retry, time.Now().UTC(), time.Minute); !errors.Is(err, ErrAuthoringReceiptExpired) {
			t.Fatalf("retry of the expired key: %v", err)
		}
	})

	t.Run("an earlier digest scheme's receipt is the same request", func(t *testing.T) {
		input := authoringReceiptInput("owner-1")
		input.KeyHash, input.ResourceID, input.RequestDigest = "key-scheme", "scheme-script", "hmac-sha256:earlier-scheme"
		if _, err := store.ClaimAuthoringMutationReceipt(input, now, time.Millisecond); err != nil {
			t.Fatal(err)
		}
		input.OwnerToken, input.RequestDigest, input.LegacyRequestDigest = "owner-2", "hmac-sha256:current-scheme", "hmac-sha256:earlier-scheme"
		if claim, err := store.ClaimAuthoringMutationReceipt(input, now.Add(time.Second), time.Minute); err != nil || claim.Created {
			t.Fatalf("retry under the current scheme: %#v %v", claim, err)
		}
		input.OwnerToken, input.LegacyRequestDigest = "owner-3", "hmac-sha256:something-else"
		if _, err := store.ClaimAuthoringMutationReceipt(input, now.Add(2*time.Second), time.Minute); !errors.Is(err, ErrAuthoringReceiptConflict) {
			t.Fatalf("a different request matched: %v", err)
		}
	})

}
