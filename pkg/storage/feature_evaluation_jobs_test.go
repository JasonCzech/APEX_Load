package storage

import (
	"errors"
	"testing"
	"time"
)

func evaluationJob(id, key, fingerprint string) *EvaluationJob {
	return &EvaluationJob{ID: id, ProjectID: DefaultProjectID, Actor: "user:alice", Kind: "test_kind", IdempotencyKeyHash: key, Request: `{"x":1}`, InputFingerprint: fingerprint}
}

func TestEvaluationJobCreateIsIdempotentAndBounded(t *testing.T) {
	store := newAuthoringReceiptTestStore(t)
	first, created, err := store.CreateEvaluationJob(evaluationJob("job-1", "key-1", "fp-1"), 2)
	if err != nil || !created || first.State != EvaluationJobQueued || first.Revision != 1 {
		t.Fatalf("create: %#v %v %v", first, created, err)
	}
	again, created, err := store.CreateEvaluationJob(evaluationJob("job-other", "key-1", "fp-1"), 2)
	if err != nil || created || again.ID != "job-1" {
		t.Fatalf("idempotent create: %#v %v %v", again, created, err)
	}
	if _, _, err := store.CreateEvaluationJob(evaluationJob("job-2", "key-1", "fp-2"), 2); !errors.Is(err, ErrEvaluationJobKeyConflict) {
		t.Fatalf("changed request with the same key: %v", err)
	}
	if _, _, err := store.CreateEvaluationJob(evaluationJob("job-2", "", "fp-2"), 2); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.CreateEvaluationJob(evaluationJob("job-3", "", "fp-3"), 2); !errors.Is(err, ErrEvaluationJobQuota) {
		t.Fatalf("quota: %v", err)
	}
}

func TestEvaluationJobLeaseCompletionCancellationAndHiding(t *testing.T) {
	store := newAuthoringReceiptTestStore(t)
	for _, id := range []string{"older", "newer"} {
		if _, _, err := store.CreateEvaluationJob(evaluationJob(id, "", "fp-"+id), 5); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	now := time.Now().UTC()
	claimed, err := store.ClaimEvaluationJob("owner-a", now, time.Minute)
	if err != nil || claimed == nil || claimed.ID != "older" || claimed.State != EvaluationJobRunning {
		t.Fatalf("claim oldest: %#v %v", claimed, err)
	}
	if ok, err := store.RenewEvaluationJob("older", "owner-b", now, time.Minute); err != nil || ok {
		t.Fatalf("non-owner renewed: %v %v", ok, err)
	}
	// An expired lease can be taken over; the previous owner's completion then
	// matches nothing.
	takeover, err := store.ClaimEvaluationJob("owner-b", now.Add(2*time.Minute), time.Minute)
	if err != nil || takeover == nil || takeover.ID != "older" {
		t.Fatalf("takeover: %#v %v", takeover, err)
	}
	if done, err := store.CompleteEvaluationJobFenced("older", "owner-a", EvaluationJobReady, `{}`, "sha", "", now, "", "", 0); err != nil || done {
		t.Fatalf("displaced owner completed: %v %v", done, err)
	}
	if done, err := store.CompleteEvaluationJobFenced("older", "owner-b", EvaluationJobReady, `{"ok":true}`, "sha", "", now, "", "", 0); err != nil || !done {
		t.Fatalf("owner completion: %v %v", done, err)
	}
	newer, err := store.ClaimEvaluationJob("owner-c", now, time.Minute)
	if err != nil || newer == nil || newer.ID != "newer" {
		t.Fatalf("second claim: %#v %v", newer, err)
	}
	if cancelled, err := store.CancelEvaluationJob(DefaultProjectID, "newer", now); err != nil || !cancelled {
		t.Fatalf("cancel: %v %v", cancelled, err)
	}
	if done, _ := store.CompleteEvaluationJobFenced("newer", "owner-c", EvaluationJobReady, `{}`, "sha", "", now, "", "", 0); done {
		t.Fatal("a cancelled job published a result")
	}
	if hidden, err := store.HideEvaluationJob(DefaultProjectID, "older", now); err != nil || !hidden {
		t.Fatalf("hide: %v %v", hidden, err)
	}
	listed, err := store.ListEvaluationJobs(DefaultProjectID, "", 10, 0)
	if err != nil || len(listed) != 1 || listed[0].ID != "newer" || listed[0].State != EvaluationJobCancelled {
		t.Fatalf("visible jobs: %#v %v", listed, err)
	}
	if job, err := store.GetEvaluationJob(DefaultProjectID, "older"); err != nil || job.HiddenAt == nil || job.Result != `{"ok":true}` {
		t.Fatalf("hidden job stays readable by id: %#v %v", job, err)
	}
	if _, err := store.GetEvaluationJob("another-project", "older"); !errors.Is(err, ErrEvaluationJobNotFound) {
		t.Fatalf("cross-project read: %v", err)
	}
}
