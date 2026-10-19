package storage

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListAuditEventsBreaksTimestampTiesByInsertionOrder(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	checkAuditEventTiesListInInsertionOrder(t, store)
}

// checkAuditEventTiesListInInsertionOrder saves events that share created_at
// with IDs whose order disagrees with the order they were written in, the way
// a start and the resume right after it can share one Windows clock tick, plus
// one written last but stamped earlier. It checks that each filter case below,
// and one-row offset pages of the unfiltered list, list them newest first, in
// insertion order among ties.
func checkAuditEventTiesListInInsertionOrder(t *testing.T, store *SQLiteStore) {
	t.Helper()
	tied := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for _, event := range []struct {
		id        string
		action    string
		status    string
		createdAt time.Time
	}{
		{id: "z-start", action: "browser_replay.session.start", status: "success", createdAt: tied},
		{id: "a-resume", action: "browser_replay.session.resume", status: "success", createdAt: tied},
		// One action twice: on SQLite an action filter reads the (action,
		// status, created_at) index, which lists this pair pending first, so
		// only the tie-break puts the later success above it.
		{id: "y-stop-pending", action: "browser_replay.session.stop", status: "pending", createdAt: tied},
		{id: "b-stop-success", action: "browser_replay.session.stop", status: "success", createdAt: tied},
		// Written last but stamped earlier: the timestamp still decides first.
		{id: "0-earlier", action: "browser_replay.session.create", status: "success", createdAt: tied.Add(-time.Second)},
	} {
		if err := store.SaveAuditEvent(&AuditEvent{
			ID: event.id, Action: event.action, ActorType: "user", ActorSubject: "alice",
			ProjectID: "project-order", ResourceType: "browser_replay_session", ResourceID: "session-1",
			Status: event.status, CreatedAt: event.createdAt,
		}); err != nil {
			t.Fatalf("SaveAuditEvent(%s): %v", event.id, err)
		}
	}
	want := []string{"b-stop-success", "y-stop-pending", "a-resume", "z-start", "0-earlier"}

	for _, check := range []struct {
		name   string
		filter AuditEventFilter
		want   []string
	}{
		{name: "unfiltered", filter: AuditEventFilter{Limit: 10}, want: want},
		{name: "project", filter: AuditEventFilter{Limit: 10, ProjectID: "project-order"}, want: want},
		{name: "resource", filter: AuditEventFilter{Limit: 10, ResourceType: "browser_replay_session", ResourceID: "session-1"}, want: want},
		{name: "action", filter: AuditEventFilter{Limit: 10, Action: "browser_replay.session.stop"}, want: want[:2]},
		{name: "time-bounded", filter: AuditEventFilter{Limit: 10, CreatedAfter: &tied, CreatedBefore: &tied}, want: want[:4]},
	} {
		t.Run(check.name, func(t *testing.T) {
			events, err := store.ListAuditEvents(check.filter)
			if err != nil {
				t.Fatalf("ListAuditEvents returned error: %v", err)
			}
			if got := auditEventIDs(events); strings.Join(got, ",") != strings.Join(check.want, ",") {
				t.Fatalf("audit order = %v, want %v", got, check.want)
			}
		})
	}

	// One-row offset pages must walk the same total order without repeats.
	t.Run("offset-pages", func(t *testing.T) {
		for offset, id := range want {
			page, err := store.ListAuditEvents(AuditEventFilter{Limit: 1, Offset: offset})
			if err != nil {
				t.Fatalf("ListAuditEvents offset %d returned error: %v", offset, err)
			}
			if got := auditEventIDs(page); len(got) != 1 || got[0] != id {
				t.Errorf("audit page at offset %d = %v, want [%s]", offset, got, id)
			}
		}
	})
}

func auditEventIDs(events []*AuditEvent) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	return ids
}
