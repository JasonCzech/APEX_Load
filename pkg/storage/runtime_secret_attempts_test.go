package storage

import (
	"reflect"
	"testing"
	"time"
)

func TestRuntimeSecretProvisioningAttemptLifecycle(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/apexload.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Microsecond)
	attempt := &RuntimeSecretProvisioningAttempt{
		ID:                 "attempt-1",
		ProjectID:          DefaultProjectID,
		TestID:             "test-1",
		MaterializationKey: "plan/run/entry",
		RefKey:             "test:recovery-owner",
		Actor:              "runtime-test:test-1:attempt:attempt-1",
		SecretNames:        []string{"TOKEN", "PASSWORD"},
		LeaseExpiresAt:     now.Add(time.Minute),
		CreatedAt:          now,
	}
	created, err := store.CreateRuntimeSecretProvisioningAttempt(attempt)
	if err != nil || !created {
		t.Fatalf("create = %v, %v", created, err)
	}
	if created, err := store.CreateRuntimeSecretProvisioningAttempt(attempt); err != nil || created {
		t.Fatalf("duplicate create = %v, %v", created, err)
	}
	attempts, err := store.ListRuntimeSecretProvisioningAttempts()
	if err != nil || len(attempts) != 1 {
		t.Fatalf("list = %#v, %v", attempts, err)
	}
	got := attempts[0]
	if got.ID != attempt.ID || got.ProjectID != attempt.ProjectID || got.TestID != attempt.TestID ||
		got.MaterializationKey != attempt.MaterializationKey || got.RefKey != attempt.RefKey ||
		got.Actor != attempt.Actor || !reflect.DeepEqual(got.SecretNames, attempt.SecretNames) ||
		!got.LeaseExpiresAt.Equal(attempt.LeaseExpiresAt) || !got.CreatedAt.Equal(attempt.CreatedAt) {
		t.Fatalf("round trip = %#v, want %#v", got, attempt)
	}
	if deleted, err := store.DeleteRuntimeSecretProvisioningAttempt(attempt.ID, "wrong-actor"); err != nil || deleted {
		t.Fatalf("wrong-owner delete = %v, %v", deleted, err)
	}
	if deleted, err := store.DeleteRuntimeSecretProvisioningAttempt(attempt.ID, attempt.Actor); err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
	if attempts, err := store.ListRuntimeSecretProvisioningAttempts(); err != nil || len(attempts) != 0 {
		t.Fatalf("list after delete = %#v, %v", attempts, err)
	}
}
