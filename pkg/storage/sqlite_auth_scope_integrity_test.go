package storage

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestScopedAuthGrantsRequireDurableOwners(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	user := &User{ID: "scope-user", Subject: "scope-user", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	for _, binding := range []*UserRoleBinding{
		{ID: "missing-project-binding", UserID: user.ID, ScopeType: "project", ScopeID: "missing-project", Role: "viewer", CreatedAt: now, UpdatedAt: now},
		{ID: "missing-domain-binding", UserID: user.ID, ScopeType: "domain", ScopeID: "missing-domain", Role: "viewer", CreatedAt: now, UpdatedAt: now},
		{ID: "missing-repository-binding", UserID: user.ID, ScopeType: "application_repository", ScopeID: "missing-repository", Role: "viewer", CreatedAt: now, UpdatedAt: now},
	} {
		if err := store.SaveUserRoleBinding(binding); !errors.Is(err, ErrRoleScopeNotFound) {
			t.Fatalf("SaveUserRoleBinding(%s) error = %v, want ErrRoleScopeNotFound", binding.ScopeType, err)
		}
	}
	onboarded := &User{ID: "invalid-onboarding-user", Subject: "invalid-onboarding-user", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUserWithRoleBindings(onboarded, []*UserRoleBinding{{
		ID: "invalid-onboarding-binding", UserID: onboarded.ID, ScopeType: "project", ScopeID: "missing-project", Role: "viewer", CreatedAt: now, UpdatedAt: now,
	}}); !errors.Is(err, ErrRoleScopeNotFound) {
		t.Fatalf("SaveUserWithRoleBindings error = %v, want ErrRoleScopeNotFound", err)
	}
	if _, err := store.GetUserBySubject(onboarded.Subject); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("invalid scoped onboarding persisted user: %v", err)
	}
	if err := store.SaveUserRoleBinding(&UserRoleBinding{
		ID: "missing-user-binding", UserID: "missing-user", ScopeType: "global", Role: "viewer", CreatedAt: now, UpdatedAt: now,
	}); !errors.Is(err, ErrUserNotFound) {
		t.Fatalf("SaveUserRoleBinding missing owner error = %v, want ErrUserNotFound", err)
	}
	mismatchedOwner := &User{ID: "mismatched-onboarding-user", Subject: "mismatched-onboarding-user", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUserWithRoleBindings(mismatchedOwner, []*UserRoleBinding{{
		ID: "mismatched-owner-binding", UserID: user.ID, ScopeType: "global", Role: "system_admin", CreatedAt: now, UpdatedAt: now,
	}}); err == nil {
		t.Fatal("SaveUserWithRoleBindings accepted a binding owned by another user")
	}
	if _, err := store.GetUserBySubject(mismatchedOwner.Subject); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("mismatched onboarding persisted user: %v", err)
	}

	account := &ServiceAccount{
		ID: "missing-scope-service", Name: "missing scope", KeyHash: "missing-scope-hash", Active: true,
		ProjectRoles: map[string][]string{"missing-project": {"viewer"}}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveServiceAccount(account); !errors.Is(err, ErrRoleScopeNotFound) {
		t.Fatalf("SaveServiceAccount error = %v, want ErrRoleScopeNotFound", err)
	}
	if _, err := store.GetServiceAccount(account.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("invalid scoped service account persisted: %v", err)
	}

	// Legacy databases can already contain a dangling embedded grant. Removing
	// that stale entry must be allowed even though adding or retaining it is not.
	legacyProject := &Project{ID: "legacy-dangling-project", Name: "Legacy", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveProject(legacyProject); err != nil {
		t.Fatal(err)
	}
	legacyAccount := &ServiceAccount{
		ID: "legacy-dangling-service", Name: "legacy dangling", KeyHash: "legacy-dangling-hash", Active: true,
		ProjectRoles: map[string][]string{legacyProject.ID: {"viewer"}}, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveServiceAccount(legacyAccount); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec(`DELETE FROM projects WHERE id = ?`, legacyProject.ID); err != nil {
		t.Fatal(err)
	}
	loadedLegacy, err := store.GetServiceAccount(legacyAccount.ID)
	if err != nil {
		t.Fatal(err)
	}
	expectedUpdatedAt := loadedLegacy.UpdatedAt
	loadedLegacy.ProjectRoles = map[string][]string{}
	loadedLegacy.UpdatedAt = expectedUpdatedAt.Add(time.Minute)
	if updated, err := store.UpdateServiceAccountMetadata(loadedLegacy, expectedUpdatedAt); err != nil || !updated {
		t.Fatalf("remove legacy dangling grant = %v, %v; want true, nil", updated, err)
	}
}

func TestDeleteDomainRevokesEmbeddedAndRowScopedGrants(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	domain := &Domain{ID: "retired-domain", Name: "Retired Domain", CreatedAt: now, UpdatedAt: now}
	repository := &ApplicationRepository{ID: "retired-repository", DomainID: domain.ID, Name: "Retired Repository", CreatedAt: now, UpdatedAt: now}
	if err := store.SaveDomain(domain); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveApplicationRepository(repository); err != nil {
		t.Fatal(err)
	}
	user := &User{ID: "retired-scope-user", Subject: "retired-scope-user", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	bindings := []*UserRoleBinding{
		{ID: "retired-domain-binding", UserID: user.ID, ScopeType: "domain", ScopeID: domain.ID, Role: "project_admin", CreatedAt: now, UpdatedAt: now},
		{ID: "retired-repository-binding", UserID: user.ID, ScopeType: "application_repository", ScopeID: repository.ID, Role: "project_admin", CreatedAt: now, UpdatedAt: now},
		{ID: "preserved-domain-binding", UserID: user.ID, ScopeType: "domain", ScopeID: DefaultDomainID, Role: "viewer", CreatedAt: now, UpdatedAt: now},
	}
	for _, binding := range bindings {
		if err := store.SaveUserRoleBinding(binding); err != nil {
			t.Fatalf("SaveUserRoleBinding(%s): %v", binding.ID, err)
		}
	}
	account := &ServiceAccount{
		ID: "retired-scope-service", Name: "retired scope", KeyHash: "retired-scope-hash", Active: true,
		DomainRoles:     map[string][]string{domain.ID: {"project_admin"}, DefaultDomainID: {"viewer"}},
		RepositoryRoles: map[string][]string{repository.ID: {"project_admin"}, DefaultApplicationRepositoryID: {"viewer"}},
		CreatedAt:       now, UpdatedAt: now,
	}
	if err := store.SaveServiceAccount(account); err != nil {
		t.Fatal(err)
	}

	if err := store.DeleteDomain(domain.ID); err != nil {
		t.Fatal(err)
	}
	remainingBindings, err := store.ListUserRoleBindings(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remainingBindings) != 1 || remainingBindings[0].ScopeType != "domain" || remainingBindings[0].ScopeID != DefaultDomainID {
		t.Fatalf("remaining user bindings = %#v, want only default-domain grant", remainingBindings)
	}
	storedAccount, err := store.GetServiceAccount(account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := storedAccount.DomainRoles[domain.ID]; ok {
		t.Fatalf("deleted domain grant remains: %#v", storedAccount.DomainRoles)
	}
	if _, ok := storedAccount.RepositoryRoles[repository.ID]; ok {
		t.Fatalf("deleted repository grant remains: %#v", storedAccount.RepositoryRoles)
	}
	if len(storedAccount.DomainRoles[DefaultDomainID]) != 1 || len(storedAccount.RepositoryRoles[DefaultApplicationRepositoryID]) != 1 {
		t.Fatalf("unrelated scoped grants were removed: domains=%#v repositories=%#v", storedAccount.DomainRoles, storedAccount.RepositoryRoles)
	}

	// Reusing a retired identifier must not resurrect the old principal's access.
	if err := store.SaveDomain(domain); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveApplicationRepository(repository); err != nil {
		t.Fatal(err)
	}
	remainingBindings, err = store.ListUserRoleBindings(user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(remainingBindings) != 1 {
		t.Fatalf("retired grants revived after identifier reuse: %#v", remainingBindings)
	}
}
