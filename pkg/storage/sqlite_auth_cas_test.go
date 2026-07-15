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

func TestServiceAccountRotationCASReturnsOnlyWinningCredential(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	account := &ServiceAccount{ID: "svc-rotate-cas", Name: "rotate", KeyHash: "old", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveServiceAccount(account); err != nil {
		t.Fatal(err)
	}

	firstAt := now.Add(time.Minute)
	if rotated, err := store.RotateServiceAccountKeyIfUnchanged(account.ID, "winner", now, firstAt); err != nil || !rotated {
		t.Fatalf("first rotation = %v, %v; want true, nil", rotated, err)
	}
	if rotated, err := store.RotateServiceAccountKeyIfUnchanged(account.ID, "stale-loser", now, now.Add(2*time.Minute)); err != nil || rotated {
		t.Fatalf("stale rotation = %v, %v; want false, nil", rotated, err)
	}
	loaded, err := store.GetServiceAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.KeyHash != "winner" {
		t.Fatalf("winning key hash was replaced by stale rotation: %#v", loaded)
	}

	if err := store.DeleteServiceAccount(account.ID); err != nil {
		t.Fatal(err)
	}
	if rotated, err := store.RotateServiceAccountKeyIfUnchanged(account.ID, "resurrected", firstAt, now.Add(3*time.Minute)); err != nil || rotated {
		t.Fatalf("post-delete rotation = %v, %v; want false, nil", rotated, err)
	}
}

func TestServiceAccountLastUsedDoesNotInvalidateMutationsOrWriteEveryRequest(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC().Truncate(time.Microsecond)
	account := &ServiceAccount{ID: "svc-last-used", Name: "last used", KeyHash: "old", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveServiceAccount(account); err != nil {
		t.Fatal(err)
	}
	firstUse := now.Add(time.Hour)
	if err := store.UpdateServiceAccountLastUsed(account.ID, firstUse); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetServiceAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LastUsedAt == nil || !loaded.LastUsedAt.Equal(firstUse) || !loaded.UpdatedAt.Equal(now) {
		t.Fatalf("last-used update changed administrative version: %#v", loaded)
	}
	if err := store.UpdateServiceAccountLastUsed(account.ID, firstUse.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetServiceAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.LastUsedAt == nil || !loaded.LastUsedAt.Equal(firstUse) {
		t.Fatalf("last-used write was not throttled: %#v", loaded.LastUsedAt)
	}
	if rotated, err := store.RotateServiceAccountKeyIfUnchanged(account.ID, "new", now, now.Add(2*time.Hour)); err != nil || !rotated {
		t.Fatalf("last-used telemetry invalidated key rotation: %v, %v", rotated, err)
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
	expected := *stale
	stale.DisplayName = "Updated profile"
	stale.UpdatedAt = now.Add(2 * time.Minute)
	updated, err := store.UpdateUserGuardLastAdminIfUnchanged(stale, &expected)
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

func TestUserSubjectRenameAdvancesSessionEpoch(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	user := &User{ID: "renamed-session-user", Subject: "old-subject", Active: true, SessionEpoch: 3, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}

	current, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	expected := *current
	current.Subject = "new-subject"
	current.UpdatedAt = now.Add(time.Minute)
	updated, err := store.UpdateUserGuardLastAdminIfUnchanged(current, &expected)
	if err != nil || !updated {
		t.Fatalf("guarded subject rename = %v, %v", updated, err)
	}
	loaded, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SessionEpoch != 4 {
		t.Fatalf("guarded rename session epoch = %d, want 4", loaded.SessionEpoch)
	}

	loaded.Subject = "third-subject"
	loaded.SessionEpoch = 0 // A stale caller must not reset the durable epoch.
	loaded.UpdatedAt = now.Add(2 * time.Minute)
	if err := store.SaveUser(loaded); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SessionEpoch != 5 {
		t.Fatalf("upsert rename session epoch = %d, want 5", loaded.SessionEpoch)
	}
}

func TestUserUpsertsNeverRollBackSessionEpoch(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	user := &User{
		ID: "monotonic-session-user", Subject: "monotonic-session-user", Active: true,
		SessionEpoch: 7, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}

	stale := *user
	stale.SessionEpoch = 2
	stale.DisplayName = "stale profile snapshot"
	stale.UpdatedAt = now.Add(time.Minute)
	if err := store.SaveUser(&stale); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SessionEpoch != 7 {
		t.Fatalf("SaveUser rolled session epoch back to %d, want 7", loaded.SessionEpoch)
	}

	stale.SessionEpoch = 1
	stale.UpdatedAt = now.Add(2 * time.Minute)
	if err := store.SaveUserWithRoleBindings(&stale, nil); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.GetUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SessionEpoch != 7 {
		t.Fatalf("SaveUserWithRoleBindings rolled session epoch back to %d, want 7", loaded.SessionEpoch)
	}
}

func TestUpdateUserGuardLastAdminRejectsStaleAdministratorFields(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now().UTC()
	user := &User{ID: "stale-admin-edit", Subject: "stale-admin-edit", Email: "old@example.test", DisplayName: "Old", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	first, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstExpected := *first
	first.DisplayName = "First writer"
	first.UpdatedAt = now.Add(time.Minute)
	if updated, err := store.UpdateUserGuardLastAdminIfUnchanged(first, &firstExpected); err != nil || !updated {
		t.Fatalf("first update = %t, %v", updated, err)
	}
	staleExpected := *stale
	stale.Email = "stale@example.test"
	stale.UpdatedAt = now.Add(2 * time.Minute)
	if updated, err := store.UpdateUserGuardLastAdminIfUnchanged(stale, &staleExpected); !errors.Is(err, ErrUserConcurrentChange) || updated {
		t.Fatalf("stale update = %t, %v, want concurrency rejection", updated, err)
	}
	loaded, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DisplayName != "First writer" || loaded.Email != "old@example.test" {
		t.Fatalf("stale update overwrote administrator fields: %#v", loaded)
	}
}
