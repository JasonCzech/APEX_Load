package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestServiceAccountMetadataCASDoesNotRestoreOrUndoCredentialState(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Microsecond)
	account := &ServiceAccount{
		ID: "svc-cas", Name: "before", KeyHash: "initial-hash", Active: true,
		GlobalRoles: []string{"viewer"}, Scopes: []string{"results:read"}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveServiceAccount(account); err != nil {
		t.Fatal(err)
	}

	stale, err := store.GetServiceAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RotateServiceAccountKey(account.ID, "rotated-hash", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	stale.Name = "stale metadata"
	stale.UpdatedAt = now.Add(2 * time.Minute)
	updated, err := store.UpdateServiceAccountMetadata(stale, now)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("stale metadata update unexpectedly won after key rotation")
	}
	rotated, err := store.GetServiceAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.KeyHash != "rotated-hash" || rotated.Name != "before" {
		t.Fatalf("rotation was overwritten: %#v", rotated)
	}

	deleteSnapshot := *rotated
	if err := store.DeleteServiceAccount(account.ID); err != nil {
		t.Fatal(err)
	}
	deleteSnapshot.Name = "resurrected"
	deleteSnapshot.UpdatedAt = now.Add(3 * time.Minute)
	updated, err = store.UpdateServiceAccountMetadata(&deleteSnapshot, rotated.UpdatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if updated {
		t.Fatal("metadata update resurrected a deleted service account")
	}
	if _, err := store.GetServiceAccount(account.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted service account was restored: %v", err)
	}
}

func TestSaveServiceAccountIsCreateOnly(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	account := &ServiceAccount{ID: "svc-create", Name: "original", KeyHash: "original-key", Active: true, Scopes: []string{"read"}, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveServiceAccount(account); err != nil {
		t.Fatal(err)
	}
	duplicate := *account
	duplicate.Name = "replacement"
	duplicate.KeyHash = "replacement-key"
	if err := store.SaveServiceAccount(&duplicate); err == nil {
		t.Fatal("duplicate create unexpectedly replaced an existing credential")
	}
	loaded, err := store.GetServiceAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Name != "original" || loaded.KeyHash != "original-key" {
		t.Fatalf("duplicate create changed existing account: %#v", loaded)
	}
}

func TestUpdateUserGuardLastAdminConcurrent(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	for _, id := range []string{"admin-a", "admin-b"} {
		if err := store.SaveUser(&User{ID: id, Subject: id, Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveUserRoleBinding(&UserRoleBinding{ID: "binding-" + id, UserID: id, ScopeType: "global", Role: storageSystemAdminRole, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"admin-a", "admin-b"} {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			when := now.Add(time.Minute)
			ok, err := store.UpdateUserGuardLastAdmin(&User{ID: id, Subject: id, Active: false, UpdatedAt: when, DisabledAt: &when})
			results <- ok
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent update failed: %v", err)
		}
	}
	wins := 0
	for ok := range results {
		if ok {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("successful deactivations = %d, want exactly one", wins)
	}
	count, err := store.CountActiveGlobalRoleHolders(storageSystemAdminRole)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("active system administrators = %d, want 1", count)
	}
}

func TestUpdateUserGuardLastAdminPreservesConcurrentSessionEpoch(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	user := &User{ID: "profile-user", Subject: "profile-user", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	stale, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.BumpUserSessionEpoch(user.ID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	stale.DisplayName = "Updated profile"
	stale.UpdatedAt = now.Add(2 * time.Minute)
	updated, err := store.UpdateUserGuardLastAdmin(stale)
	if err != nil || !updated {
		t.Fatalf("profile update = %v, %v", updated, err)
	}
	loaded, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SessionEpoch != 1 || loaded.DisplayName != "Updated profile" {
		t.Fatalf("profile update rolled back session state: %#v", loaded)
	}
}
