package storage

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestProjectLiveTableOperatorCommitIsAtomic(t *testing.T) {
	checkProjectLiveTableOperatorCommitIsAtomic(t, newHAFencingStore(t))
}
func checkProjectLiveTableOperatorCommitIsAtomic(t *testing.T, s *SQLiteStore) {
	ctx := context.Background()
	input, term := projectTableFixture(t, s)
	d, err := s.CreateProjectLiveTable(ctx, input, term)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	entry := ProjectLiveTableEntry{LiveTableEntry: LiveTableEntry{TableID: d.CurrentInstanceID, Revision: 1, At: now, Payload: []byte(`{"encrypted":"mutation"}`)}}
	receipt := ProjectLiveTableOperatorReceipt{InstanceID: d.CurrentInstanceID, ActorSubject: "editor", KeyHash: strings.Repeat("a", 64), Digest: strings.Repeat("b", 64), ClientScope: "operator/scope/", IssuedAt: now, ExpiresAt: now.Add(time.Minute), Outcome: []byte(`{"encrypted":"outcome"}`)}
	audit := &AuditEvent{ID: "operator-event", ActorSubject: receipt.ActorSubject, Action: "project_live_table.mutate", ProjectID: d.ProjectID, ResourceType: "project_live_table", ResourceID: d.ID, CreatedAt: now, Details: map[string]any{"bad": func() {}}}
	if err = s.AppendProjectLiveTableOperatorEntry(ctx, entry, receipt, audit, term); err == nil {
		t.Fatal("invalid audit accepted")
	}
	recovery, err := s.ReadProjectLiveTableRecovery(ctx, d.ProjectID, d.CurrentInstanceID)
	if err != nil || recovery.Instance.Revision != 0 || len(recovery.Entries) != 0 {
		t.Fatal("mutation escaped audit rollback", err)
	}
	if _, err = s.GetProjectLiveTableOperatorReceipt(ctx, receipt.InstanceID, receipt.ActorSubject, receipt.KeyHash); err == nil {
		t.Fatal("receipt escaped rollback")
	}
	audit.Details = map[string]any{"operation": "append", "revision": "1"}
	if err = s.AppendProjectLiveTableOperatorEntry(ctx, entry, receipt, audit, term); err != nil {
		t.Fatal(err)
	}
	if err = s.AppendProjectLiveTableOperatorEntry(ctx, entry, receipt, audit, term); err != nil {
		t.Fatal("lost reply", err)
	}
	var count int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE id=?`, audit.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("audit duplicated %d %v", count, err)
	}
	altered := receipt
	altered.Digest = strings.Repeat("c", 64)
	if err = s.AppendProjectLiveTableOperatorEntry(ctx, entry, altered, audit, term); !errors.Is(err, ErrLiveTableConflict) {
		t.Fatalf("identity collision %v", err)
	}
	if err = s.DeleteProjectLiveTableOperatorReceipt(ctx, receipt, 1, term); !errors.Is(err, ErrLiveTableConflict) {
		t.Fatalf("receipt retired early %v", err)
	}
}
