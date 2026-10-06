package storage

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// evaluationJob builds a job whose request digest follows its fingerprint, so
// a changed fingerprint is a changed request unless a test says otherwise.
func evaluationJob(id, key, fingerprint string) *EvaluationJob {
	return &EvaluationJob{ID: id, ProjectID: DefaultProjectID, Actor: "user:alice", Kind: "test_kind", IdempotencyKeyHash: key, RequestDigest: "digest-" + fingerprint, Request: `{"x":1}`, InputFingerprint: fingerprint}
}

var testEvaluationKinds = []string{"test_kind"}

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
	claimed, err := store.ClaimEvaluationJob("owner-a", now, time.Minute, testEvaluationKinds, 3)
	if err != nil || claimed == nil || claimed.ID != "older" || claimed.State != EvaluationJobRunning {
		t.Fatalf("claim oldest: %#v %v", claimed, err)
	}
	if ok, err := store.RenewEvaluationJob("older", "owner-b", now, time.Minute); err != nil || ok {
		t.Fatalf("non-owner renewed: %v %v", ok, err)
	}
	// An expired lease can be taken over; the previous owner's completion then
	// matches nothing.
	takeover, err := store.ClaimEvaluationJob("owner-b", now.Add(2*time.Minute), time.Minute, testEvaluationKinds, 3)
	if err != nil || takeover == nil || takeover.ID != "older" {
		t.Fatalf("takeover: %#v %v", takeover, err)
	}
	if done, err := store.CompleteEvaluationJobFenced("older", "owner-a", EvaluationJobReady, `{}`, "sha", "", "", now, "", "", 0); err != nil || done {
		t.Fatalf("displaced owner completed: %v %v", done, err)
	}
	if done, err := store.CompleteEvaluationJobFenced("older", "owner-b", EvaluationJobReady, `{"ok":true}`, "sha", "", "", now, "", "", 0); err != nil || !done {
		t.Fatalf("owner completion: %v %v", done, err)
	}
	newer, err := store.ClaimEvaluationJob("owner-c", now, time.Minute, testEvaluationKinds, 3)
	if err != nil || newer == nil || newer.ID != "newer" {
		t.Fatalf("second claim: %#v %v", newer, err)
	}
	if cancelled, err := store.CancelEvaluationJob(DefaultProjectID, "newer", now); err != nil || !cancelled {
		t.Fatalf("cancel: %v %v", cancelled, err)
	}
	if done, _ := store.CompleteEvaluationJobFenced("newer", "owner-c", EvaluationJobReady, `{}`, "sha", "", "", now, "", "", 0); done {
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

// exerciseEvaluationJobReviewFixes covers the review fixes on one store: the
// key is judged by the request digest, claims are limited to registered kinds,
// attempts are capped, listings leave bodies out, failures carry a code and a
// missing project is typed.
func exerciseEvaluationJobReviewFixes(t *testing.T, store *SQLiteStore) {
	t.Helper()
	first := evaluationJob("digest-1", "key-d", "fp-before")
	if _, created, err := store.CreateEvaluationJob(first, 10); err != nil || !created {
		t.Fatalf("create: %v %v", created, err)
	}
	// The run advanced between a lost response and its identical retry: the
	// fingerprint changed but the request did not.
	retry := evaluationJob("digest-retry", "key-d", "fp-after")
	retry.RequestDigest = first.RequestDigest
	if replay, created, err := store.CreateEvaluationJob(retry, 10); err != nil || created || replay.ID != "digest-1" {
		t.Fatalf("identical retry after inputs advanced: %#v %v %v", replay, created, err)
	}
	changed := evaluationJob("digest-changed", "key-d", "fp-before")
	changed.RequestDigest = "digest-other-request"
	if _, _, err := store.CreateEvaluationJob(changed, 10); !errors.Is(err, ErrEvaluationJobKeyConflict) {
		t.Fatalf("changed request with an unchanged fingerprint: %v", err)
	}
	if _, _, err := store.CreateEvaluationJob(&EvaluationJob{ID: "keyed-no-digest", ProjectID: DefaultProjectID, Actor: "user:alice", Kind: "test_kind", IdempotencyKeyHash: "k", Request: `{}`, InputFingerprint: "fp"}, 10); !errors.Is(err, ErrEvaluationJobInvalid) {
		t.Fatalf("keyed job without a request digest: %v", err)
	}
	missing := evaluationJob("no-project", "", "fp")
	missing.ProjectID = "no-such-project"
	if _, _, err := store.CreateEvaluationJob(missing, 10); !errors.Is(err, ErrEvaluationJobProjectNotFound) {
		t.Fatalf("missing project: %v", err)
	}

	// A kind this controller does not run is never claimed.
	other := evaluationJob("other-kind", "", "fp-other")
	other.Kind = "newer_kind"
	if _, _, err := store.CreateEvaluationJob(other, 10); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	if claimed, err := store.ClaimEvaluationJob("owner", now, time.Minute, nil, 3); err != nil || claimed != nil {
		t.Fatalf("claim with no registered kinds: %#v %v", claimed, err)
	}
	claimed, err := store.ClaimEvaluationJob("owner-1", now, time.Minute, []string{"test_kind"}, 2)
	if err != nil || claimed == nil || claimed.ID != "digest-1" || claimed.Attempts != 1 {
		t.Fatalf("first claim: %#v %v", claimed, err)
	}
	// The runner dies; the lease expires and the job is claimed again, then
	// expires once its attempts are exhausted instead of blocking the queue.
	later := now.Add(2 * time.Minute)
	if again, err := store.ClaimEvaluationJob("owner-2", later, time.Minute, []string{"test_kind"}, 2); err != nil || again == nil || again.ID != "digest-1" || again.Attempts != 2 {
		t.Fatalf("second claim: %#v %v", again, err)
	}
	if next, err := store.ClaimEvaluationJob("owner-3", later.Add(2*time.Minute), time.Minute, []string{"test_kind"}, 2); err != nil || next != nil {
		t.Fatalf("claim after attempts were exhausted: %#v %v", next, err)
	}
	poisoned, err := store.GetEvaluationJob(DefaultProjectID, "digest-1")
	if err != nil || poisoned.State != EvaluationJobExpired || poisoned.FailureCode != EvaluationFailureAttemptsExhausted || poisoned.Owner != "" {
		t.Fatalf("poison job: %#v %v", poisoned, err)
	}
	if untouched, err := store.GetEvaluationJob(DefaultProjectID, "other-kind"); err != nil || untouched.State != EvaluationJobQueued {
		t.Fatalf("a job of an unregistered kind was touched: %#v %v", untouched, err)
	}

	// Failures carry a code; a ready result does not.
	ready := evaluationJob("ready", "", "fp-ready")
	if _, _, err := store.CreateEvaluationJob(ready, 10); err != nil {
		t.Fatal(err)
	}
	running, err := store.ClaimEvaluationJob("owner-r", later, time.Minute, []string{"test_kind"}, 3)
	if err != nil || running == nil || running.ID != "ready" {
		t.Fatalf("claim ready job: %#v %v", running, err)
	}
	if _, err := store.CompleteEvaluationJobFenced("ready", "owner-r", EvaluationJobFailed, "", "", "", "no code", later, "", "", 0); err == nil {
		t.Fatal("a failure without a code was accepted")
	}
	// Multi-byte text: the stored size counts bytes, not characters.
	readyResult := `{"big":"` + strings.Repeat("é", 500) + `"}`
	if done, err := store.CompleteEvaluationJobFenced("ready", "owner-r", EvaluationJobReady, readyResult, "sha", "", "", later, "", "", 0); err != nil || !done {
		t.Fatalf("complete: %v %v", done, err)
	}
	listed, err := store.ListEvaluationJobs(DefaultProjectID, "test_kind", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range listed {
		if job.Result != "" || job.Request != "" {
			t.Fatalf("a listing carried bodies: %#v", job)
		}
		if job.ID == "ready" && job.ResultBytes != int64(len(readyResult)) {
			t.Fatalf("listing result size %d", job.ResultBytes)
		}
	}
	if full, err := store.GetEvaluationJob(DefaultProjectID, "ready"); err != nil || full.Result == "" || full.Request == "" {
		t.Fatalf("a job read by id lost its bodies: %v", err)
	}
}

func TestEvaluationJobReviewFixes(t *testing.T) {
	exerciseEvaluationJobReviewFixes(t, newAuthoringReceiptTestStore(t))
}

// A completion records only under the current execution term, and a job
// whose project was deleted while it ran records nothing.
func TestEvaluationJobCompletionIsFencedByTermAndProjectDeletion(t *testing.T) {
	exerciseEvaluationJobCompletionFences(t, newAuthoringReceiptTestStore(t))
}

func exerciseEvaluationJobCompletionFences(t *testing.T, store *SQLiteStore) {
	t.Helper()
	if _, _, err := store.CreateEvaluationJob(evaluationJob("fenced", "", "fp-fenced"), 10); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(time.Second)
	if claimed, err := store.ClaimEvaluationJob("job-owner", now, time.Hour, testEvaluationKinds, 3); err != nil || claimed == nil {
		t.Fatalf("claim: %#v %v", claimed, err)
	}
	ok, lease, err := store.TryAcquireSchedulerLeaseTerm("controller-execution-leader", "leader-1", time.Now(), time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if done, err := store.CompleteEvaluationJobFenced("fenced", "job-owner", EvaluationJobReady, `{}`, "sha", "", "", now, "controller-execution-leader", "leader-1", lease.Generation+1); err != nil || done {
		t.Fatalf("a deposed term completed the job: %v %v", done, err)
	}
	if job, _ := store.GetEvaluationJob(DefaultProjectID, "fenced"); job.State != EvaluationJobRunning {
		t.Fatalf("a refused completion changed the job: %#v", job)
	}
	if done, err := store.CompleteEvaluationJobFenced("fenced", "job-owner", EvaluationJobReady, `{}`, "sha", "", "", now, "controller-execution-leader", "leader-1", lease.Generation); err != nil || !done {
		t.Fatalf("the current term: %v %v", done, err)
	}

	if err := store.SaveProject(&Project{ID: "eval-deleted", Name: "Deleted during evaluation"}); err != nil {
		t.Fatal(err)
	}
	doomed := evaluationJob("doomed", "", "fp-doomed")
	doomed.ProjectID = "eval-deleted"
	if _, _, err := store.CreateEvaluationJob(doomed, 10); err != nil {
		t.Fatal(err)
	}
	if claimed, err := store.ClaimEvaluationJob("doomed-owner", now, time.Hour, testEvaluationKinds, 3); err != nil || claimed == nil || claimed.ID != "doomed" {
		t.Fatalf("claim: %#v %v", claimed, err)
	}
	if err := store.DeleteProject("eval-deleted"); err != nil {
		t.Fatal(err)
	}
	if done, err := store.CompleteEvaluationJobFenced("doomed", "doomed-owner", EvaluationJobReady, `{}`, "sha", "", "", now, "", "", 0); err != nil || done {
		t.Fatalf("a job of a deleted project completed: %v %v", done, err)
	}
	if _, err := store.GetEvaluationJob("eval-deleted", "doomed"); err == nil {
		t.Fatal("the deleted project's job survived")
	}
}
