//go:build postgres_integration

package storage

import (
	"errors"
	"testing"
	"time"
)

func TestPostgresFeatureEvaluationJobs(t *testing.T) {
	store, _ := newPostgresScenarioRevisionFixture(t)
	if _, created, err := store.CreateEvaluationJob(evaluationJob("pg-1", "key", "fp"), 3); err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	if again, created, err := store.CreateEvaluationJob(evaluationJob("pg-2", "key", "fp"), 3); err != nil || created || again.ID != "pg-1" {
		t.Fatalf("idempotent create: %#v %v %v", again, created, err)
	}
	if _, _, err := store.CreateEvaluationJob(evaluationJob("pg-3", "key", "other"), 3); !errors.Is(err, ErrEvaluationJobKeyConflict) {
		t.Fatalf("key conflict: %v", err)
	}
	// Keyless jobs never collide on the partial unique index.
	for _, id := range []string{"pg-4", "pg-5"} {
		if _, _, err := store.CreateEvaluationJob(evaluationJob(id, "", "fp-"+id), 3); err != nil {
			t.Fatalf("keyless %s: %v", id, err)
		}
	}
	now := time.Now().UTC()
	claimed, err := store.ClaimEvaluationJob("owner", now, time.Minute, testEvaluationKinds, 3)
	if err != nil || claimed == nil || claimed.ID != "pg-1" {
		t.Fatalf("claim: %#v %v", claimed, err)
	}
	if done, err := store.CompleteEvaluationJobFenced("pg-1", "owner", EvaluationJobReady, `{"ok":true}`, "sha", "", "", now, "", "", 0); err != nil || !done {
		t.Fatalf("complete: %v %v", done, err)
	}
	if hidden, err := store.HideEvaluationJob(DefaultProjectID, "pg-1", now); err != nil || !hidden {
		t.Fatalf("hide: %v %v", hidden, err)
	}
}

func TestPostgresEvaluationJobReviewFixes(t *testing.T) {
	store, _ := newPostgresScenarioRevisionFixture(t)
	exerciseEvaluationJobReviewFixes(t, store)
}

func TestPostgresEvaluationJobCompletionFences(t *testing.T) {
	store, _ := newPostgresScenarioRevisionFixture(t)
	exerciseEvaluationJobCompletionFences(t, store)
}

// Two PostgreSQL controllers: a takeover after lease expiry fences the first
// owner, and a cancel makes a late completion invisible.
func TestPostgresEvaluationJobTakeoverAndCancelThenLatePublish(t *testing.T) {
	first, url := newPostgresScenarioRevisionFixture(t)
	peer, err := NewPostgresStore(url, StoreConnectionOptions{}, false)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	for _, id := range []string{"take", "cancel"} {
		if _, _, err := first.CreateEvaluationJob(evaluationJob(id, "", "fp-"+id), 5); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	now := time.Now().UTC()
	if claimed, err := first.ClaimEvaluationJob("owner-a", now, time.Minute, testEvaluationKinds, 3); err != nil || claimed == nil || claimed.ID != "take" {
		t.Fatalf("claim: %#v %v", claimed, err)
	}
	takeover, err := peer.ClaimEvaluationJob("owner-b", now.Add(2*time.Minute), time.Minute, testEvaluationKinds, 3)
	if err != nil || takeover == nil || takeover.ID != "take" || takeover.Attempts != 2 {
		t.Fatalf("takeover: %#v %v", takeover, err)
	}
	if done, err := first.CompleteEvaluationJobFenced("take", "owner-a", EvaluationJobReady, `{}`, "sha", "", "", now, "", "", 0); err != nil || done {
		t.Fatalf("displaced owner published: %v %v", done, err)
	}
	if done, err := peer.CompleteEvaluationJobFenced("take", "owner-b", EvaluationJobReady, `{"ok":true}`, "sha", "", "", now, "", "", 0); err != nil || !done {
		t.Fatalf("owner completion: %v %v", done, err)
	}
	if claimed, err := first.ClaimEvaluationJob("owner-c", now.Add(2*time.Minute), time.Minute, testEvaluationKinds, 3); err != nil || claimed == nil || claimed.ID != "cancel" {
		t.Fatalf("claim second: %#v %v", claimed, err)
	}
	if cancelled, err := peer.CancelEvaluationJob(DefaultProjectID, "cancel", now); err != nil || !cancelled {
		t.Fatalf("cancel: %v %v", cancelled, err)
	}
	if done, err := first.CompleteEvaluationJobFenced("cancel", "owner-c", EvaluationJobReady, `{}`, "sha", "", "", now, "", "", 0); err != nil || done {
		t.Fatalf("late publish after cancel: %v %v", done, err)
	}
	if job, err := peer.GetEvaluationJob(DefaultProjectID, "cancel"); err != nil || job.State != EvaluationJobCancelled || job.Result != "" {
		t.Fatalf("cancelled job: %#v %v", job, err)
	}
}

func TestPostgresFeatureAcceptanceSequence(t *testing.T) {
	store, _ := newPostgresScenarioRevisionFixture(t)
	exerciseFeatureAcceptanceSequence(t, store)
}
