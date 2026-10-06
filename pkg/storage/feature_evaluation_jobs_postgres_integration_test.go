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
	claimed, err := store.ClaimEvaluationJob("owner", now, time.Minute)
	if err != nil || claimed == nil || claimed.ID != "pg-1" {
		t.Fatalf("claim: %#v %v", claimed, err)
	}
	if done, err := store.CompleteEvaluationJobFenced("pg-1", "owner", EvaluationJobReady, `{"ok":true}`, "sha", "", now, "", "", 0); err != nil || !done {
		t.Fatalf("complete: %v %v", done, err)
	}
	if hidden, err := store.HideEvaluationJob(DefaultProjectID, "pg-1", now); err != nil || !hidden {
		t.Fatalf("hide: %v %v", hidden, err)
	}
}
