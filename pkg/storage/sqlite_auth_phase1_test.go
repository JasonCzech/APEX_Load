package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newAuthPhase1SQLiteStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestBumpUserSessionEpoch(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	user := &User{ID: uuid.New().String(), Subject: "epoch", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}
	if loaded, _ := store.GetUser(user.ID); loaded.SessionEpoch != 0 {
		t.Fatalf("initial session epoch = %d, want 0", loaded.SessionEpoch)
	}
	got, err := store.BumpUserSessionEpoch(user.ID, now)
	if err != nil || got != 1 {
		t.Fatalf("BumpUserSessionEpoch = %d (err=%v), want 1", got, err)
	}
	if loaded, _ := store.GetUser(user.ID); loaded.SessionEpoch != 1 {
		t.Fatalf("persisted session epoch = %d, want 1", loaded.SessionEpoch)
	}
}

func TestUpdateUserIdentitySeenDoesNotRewriteSecurityFields(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC().Truncate(time.Second)
	disabledAt := now.Add(time.Minute)
	user := &User{
		ID:           uuid.New().String(),
		Subject:      "profile-refresh",
		Email:        "old@example.test",
		DisplayName:  "Old Name",
		Active:       false,
		DisabledAt:   &disabledAt,
		SessionEpoch: 4,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := store.SaveUser(user); err != nil {
		t.Fatalf("SaveUser: %v", err)
	}

	seenAt := now.Add(5 * time.Minute)
	if err := store.UpdateUserIdentitySeen(user.ID, "New Name", "new@example.test", seenAt, seenAt); err != nil {
		t.Fatalf("UpdateUserIdentitySeen: %v", err)
	}
	loaded, err := store.GetUser(user.ID)
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if loaded.DisplayName != "New Name" || loaded.Email != "new@example.test" || loaded.LastSeenAt == nil || !loaded.LastSeenAt.Equal(seenAt) {
		t.Fatalf("profile fields = %#v, want targeted refresh", loaded)
	}
	if loaded.Active || loaded.DisabledAt == nil || !loaded.DisabledAt.Equal(disabledAt) || loaded.SessionEpoch != 4 {
		t.Fatalf("security fields changed after identity refresh: %#v", loaded)
	}
}

func TestCountActiveGlobalRoleHolders(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	if err := store.SaveProject(&Project{ID: "admin-project", Name: "Admin Project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("SaveProject: %v", err)
	}
	addAdmin := func(subject string, active bool, scopeType string) {
		user := &User{ID: uuid.New().String(), Subject: subject, Active: active, CreatedAt: now, UpdatedAt: now}
		if err := store.SaveUser(user); err != nil {
			t.Fatalf("SaveUser(%s): %v", subject, err)
		}
		scopeID := ""
		if scopeType == "project" {
			scopeID = "admin-project"
		}
		if err := store.SaveUserRoleBinding(&UserRoleBinding{ID: uuid.New().String(), UserID: user.ID, ScopeType: scopeType, ScopeID: scopeID, Role: "system_admin", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveUserRoleBinding(%s): %v", subject, err)
		}
	}

	addAdmin("active-global", true, "global")    // counts
	addAdmin("disabled-global", false, "global") // excluded: inactive
	addAdmin("project-admin", true, "project")   // excluded: not global scope

	count, err := store.CountActiveGlobalRoleHolders("system_admin")
	if err != nil {
		t.Fatalf("CountActiveGlobalRoleHolders returned error: %v", err)
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1 (only the active global-scope admin)", count)
	}

	if count, err := store.CountActiveGlobalRoleHolders("viewer"); err != nil || count != 0 {
		t.Fatalf("viewer count = %d (err=%v), want 0", count, err)
	}
}

func TestUpdateUserActiveGuardLastAdminConcurrent(t *testing.T) {
	store := newAuthPhase1SQLiteStore(t)
	now := time.Now().UTC()
	adminA := &User{ID: uuid.New().String(), Subject: "admin-a", Active: true, CreatedAt: now, UpdatedAt: now}
	adminB := &User{ID: uuid.New().String(), Subject: "admin-b", Active: true, CreatedAt: now, UpdatedAt: now}
	for _, user := range []*User{adminA, adminB} {
		if err := store.SaveUser(user); err != nil {
			t.Fatalf("SaveUser(%s): %v", user.ID, err)
		}
		if err := store.SaveUserRoleBinding(&UserRoleBinding{ID: uuid.New().String(), UserID: user.ID, ScopeType: "global", Role: storageSystemAdminRole, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveUserRoleBinding(%s): %v", user.ID, err)
		}
	}

	start := make(chan struct{})
	results := make(chan bool, 2)
	errs := make(chan error, 2)
	for _, id := range []string{adminA.ID, adminB.ID} {
		id := id
		go func() {
			<-start
			ok, err := store.UpdateUserActiveGuardLastAdmin(id, false, now.Add(time.Second))
			results <- ok
			errs <- err
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("UpdateUserActiveGuardLastAdmin error: %v", err)
		}
		<-results
	}

	count, err := store.CountActiveGlobalRoleHolders(storageSystemAdminRole)
	if err != nil {
		t.Fatalf("CountActiveGlobalRoleHolders: %v", err)
	}
	if count != 1 {
		t.Fatalf("active system admins = %d, want exactly one survivor", count)
	}
}

func TestDeleteUserRoleBindingGuardLastAdminConcurrent(t *testing.T) {
	store := newAuthPhase1SQLiteStore(t)
	now := time.Now().UTC()
	bindingIDs := make([]string, 0, 2)
	for _, subject := range []string{"admin-a", "admin-b"} {
		user := &User{ID: uuid.New().String(), Subject: subject, Active: true, CreatedAt: now, UpdatedAt: now}
		if err := store.SaveUser(user); err != nil {
			t.Fatalf("SaveUser(%s): %v", subject, err)
		}
		bindingID := uuid.New().String()
		bindingIDs = append(bindingIDs, bindingID)
		if err := store.SaveUserRoleBinding(&UserRoleBinding{ID: bindingID, UserID: user.ID, ScopeType: "global", Role: storageSystemAdminRole, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveUserRoleBinding(%s): %v", subject, err)
		}
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, id := range bindingIDs {
		id := id
		go func() {
			<-start
			_, err := store.DeleteUserRoleBindingGuardLastAdmin(id)
			errs <- err
		}()
	}
	close(start)
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("DeleteUserRoleBindingGuardLastAdmin error: %v", err)
		}
	}

	count, err := store.CountActiveGlobalRoleHolders(storageSystemAdminRole)
	if err != nil {
		t.Fatalf("CountActiveGlobalRoleHolders: %v", err)
	}
	if count != 1 {
		t.Fatalf("active system admins = %d, want exactly one survivor", count)
	}
}

func TestBootstrapFirstAdminOnlyWinsOnEmptyUserTable(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	bootstrap := func(subject string) (bool, error) {
		user := &User{ID: uuid.New().String(), Subject: subject, Active: true, CreatedAt: now, UpdatedAt: now}
		binding := &UserRoleBinding{ID: uuid.New().String(), UserID: user.ID, ScopeType: "global", Role: "system_admin", CreatedAt: now, UpdatedAt: now}
		event := &AuditEvent{ID: uuid.New().String(), Action: "auth.bootstrap.admin_granted", ActorType: "system", ActorSubject: subject, ResourceType: "user", ResourceID: user.ID, Status: "success", CreatedAt: now}
		return store.BootstrapFirstAdmin(user, binding, event)
	}

	granted, err := bootstrap("first-admin")
	if err != nil || !granted {
		t.Fatalf("first bootstrap granted=%v err=%v, want granted", granted, err)
	}
	granted, err = bootstrap("second-admin")
	if err != nil {
		t.Fatalf("second bootstrap returned error: %v", err)
	}
	if granted {
		t.Fatalf("second bootstrap granted access despite non-empty user table")
	}
	users, err := store.ListUsers(UserFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	if len(users) != 1 || users[0].Subject != "first-admin" {
		t.Fatalf("users = %#v, want only first-admin", users)
	}
	count, err := store.CountAuditEvents(AuditEventFilter{Action: "auth.bootstrap.admin_granted"})
	if err != nil || count != 1 {
		t.Fatalf("bootstrap audit count = %d (err=%v), want 1", count, err)
	}
}

func TestDeleteAuditAndUsageBefore(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	old := now.Add(-72 * time.Hour)
	cutoff := now.Add(-24 * time.Hour)

	for _, ts := range []time.Time{old, now} {
		if err := store.SaveAuditEvent(&AuditEvent{ID: uuid.New().String(), Action: "x", ActorType: "user", ActorSubject: "a", ResourceType: "t", ResourceID: "1", Status: "success", CreatedAt: ts}); err != nil {
			t.Fatalf("SaveAuditEvent: %v", err)
		}
		if err := store.SaveUsageAccountingSnapshot(&UsageAccountingSnapshot{ID: uuid.New().String(), GeneratedAt: ts, PayloadJSON: "{}", CreatedAt: ts}); err != nil {
			t.Fatalf("SaveUsageAccountingSnapshot: %v", err)
		}
	}

	auditPruned, err := store.DeleteAuditEventsBefore(cutoff)
	if err != nil || auditPruned != 1 {
		t.Fatalf("DeleteAuditEventsBefore = %d (err=%v), want 1", auditPruned, err)
	}
	usagePruned, err := store.DeleteUsageAccountingSnapshotsBefore(cutoff)
	if err != nil || usagePruned != 1 {
		t.Fatalf("DeleteUsageAccountingSnapshotsBefore = %d (err=%v), want 1", usagePruned, err)
	}

	events, err := store.ListAuditEvents(AuditEventFilter{Limit: 10})
	if err != nil || len(events) != 1 {
		t.Fatalf("remaining audit events = %d (err=%v), want 1", len(events), err)
	}
}

func TestListAuditEventsEmptyProjectAllowListMatchesNothing(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	for _, event := range []*AuditEvent{
		{ID: "audit-proj-a", Action: "a", ActorType: "user", ActorSubject: "alice", ProjectID: "proj-a", ResourceType: "test", ResourceID: "a", Status: "success", CreatedAt: now},
		{ID: "audit-proj-b", Action: "b", ActorType: "user", ActorSubject: "bob", ProjectID: "proj-b", ResourceType: "test", ResourceID: "b", Status: "success", CreatedAt: now.Add(time.Second)},
	} {
		if err := store.SaveAuditEvent(event); err != nil {
			t.Fatalf("SaveAuditEvent(%s): %v", event.ID, err)
		}
	}

	unrestricted, err := store.ListAuditEvents(AuditEventFilter{Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents unrestricted returned error: %v", err)
	}
	if len(unrestricted) != 2 {
		t.Fatalf("unrestricted events = %d, want 2", len(unrestricted))
	}

	none, err := store.ListAuditEvents(AuditEventFilter{Limit: 10, ProjectIDs: []string{}})
	if err != nil {
		t.Fatalf("ListAuditEvents empty allow-list returned error: %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("empty allow-list events = %#v, want none", none)
	}

	allowed, err := store.ListAuditEvents(AuditEventFilter{Limit: 10, ProjectIDs: []string{"proj-a"}})
	if err != nil {
		t.Fatalf("ListAuditEvents project allow-list returned error: %v", err)
	}
	if len(allowed) != 1 || allowed[0].ProjectID != "proj-a" {
		t.Fatalf("project allow-list events = %#v, want only proj-a", allowed)
	}
}

func TestSaveUserWithRoleBindingsRollsBackOnBindingFailure(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	user := &User{ID: "user-rollback", Subject: "rollback", Active: true, CreatedAt: now, UpdatedAt: now}
	bindings := []*UserRoleBinding{
		{ID: "duplicate-binding-id", UserID: user.ID, ScopeType: "global", Role: "viewer", CreatedAt: now, UpdatedAt: now},
		{ID: "duplicate-binding-id", UserID: user.ID, ScopeType: "project", ScopeID: "proj-a", Role: "viewer", CreatedAt: now, UpdatedAt: now},
	}
	if err := store.SaveUserWithRoleBindings(user, bindings); err == nil {
		t.Fatalf("SaveUserWithRoleBindings should fail on duplicate binding id")
	}
	if _, err := store.GetUserBySubject("rollback"); err == nil {
		t.Fatalf("failed onboarding transaction should not persist the user")
	}
}
