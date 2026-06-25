package storage

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

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

func TestCountActiveGlobalRoleHolders(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	addAdmin := func(subject string, active bool, scopeType string) {
		user := &User{ID: uuid.New().String(), Subject: subject, Active: active, CreatedAt: now, UpdatedAt: now}
		if err := store.SaveUser(user); err != nil {
			t.Fatalf("SaveUser(%s): %v", subject, err)
		}
		if err := store.SaveUserRoleBinding(&UserRoleBinding{ID: uuid.New().String(), UserID: user.ID, ScopeType: scopeType, Role: "system_admin", CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatalf("SaveUserRoleBinding(%s): %v", subject, err)
		}
	}

	addAdmin("active-global", true, "global")     // counts
	addAdmin("disabled-global", false, "global")  // excluded: inactive
	addAdmin("project-admin", true, "project")    // excluded: not global scope

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
