package storage

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestObjectActivityTiesListInInsertionOrder(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	checkObjectActivityTiesListInInsertionOrder(t, store)
}

func TestObjectActivityLegacyBackfillKeepsAuditTieOrder(t *testing.T) {
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	checkObjectActivityBackfillKeepsAuditTieOrder(t, store)
}

// checkObjectActivityTiesListInInsertionOrder saves activity that shares
// created_at, with IDs whose order disagrees with the order it was written in,
// plus events written later but stamped earlier. It checks that the list,
// its filters and one-row pages run newest first in insertion order among
// ties, that the Deleted flag follows the create or delete written last, that
// the creator summary is the create written last, and that the last modifier
// and last archive summaries pick the event written last of each kind since
// that create.
func checkObjectActivityTiesListInInsertionOrder(t *testing.T, store *SQLiteStore) {
	t.Helper()
	const project = "project-activity-order"
	tied := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for _, event := range []struct {
		id, resource, action string
		createdAt            time.Time
	}{
		{id: "z-a-create", resource: "a", action: "create", createdAt: tied},
		{id: "m-a-modify-first", resource: "a", action: "modify", createdAt: tied},
		{id: "b-a-modify-second", resource: "a", action: "modify", createdAt: tied},
		{id: "y-a-archive", resource: "a", action: "archive", createdAt: tied},
		{id: "c-a-restore", resource: "a", action: "restore", createdAt: tied},
		// Created, then deleted in the same tick: b is deleted.
		{id: "y-b-create", resource: "b", action: "create", createdAt: tied},
		{id: "a-b-delete", resource: "b", action: "delete", createdAt: tied},
		// Deleted, then created again in the same tick: c exists.
		{id: "z-c-delete", resource: "c", action: "delete", createdAt: tied},
		{id: "a-c-create", resource: "c", action: "create", createdAt: tied},
		// d is modified, archived, deleted and created again under the same ID,
		// all in one tick: the new d inherits none of the old one's history.
		{id: "z-d-modify-old", resource: "d", action: "modify", createdAt: tied},
		{id: "y-d-archive-old", resource: "d", action: "archive", createdAt: tied},
		{id: "x-d-delete", resource: "d", action: "delete", createdAt: tied},
		{id: "a-d-create", resource: "d", action: "create", createdAt: tied},
		// e is created, deleted and created again in one tick: the second create
		// is the creator and e exists.
		{id: "m-e-create-first", resource: "e", action: "create", createdAt: tied},
		{id: "z-e-delete", resource: "e", action: "delete", createdAt: tied},
		{id: "a-e-create-second", resource: "e", action: "create", createdAt: tied},
		// f's create is written after its modify but stamped earlier: the
		// timestamp decides first, so the modify still counts for f.
		{id: "y-f-modify", resource: "f", action: "modify", createdAt: tied},
		{id: "b-f-create-earlier", resource: "f", action: "create", createdAt: tied.Add(-time.Second)},
		// Written last but stamped earlier: the timestamp still decides first.
		{id: "0-a-earlier", resource: "a", action: "modify", createdAt: tied.Add(-time.Second)},
	} {
		if err := store.SaveObjectActivity(&ObjectActivityEvent{
			ID: event.id, ProjectID: project, ResourceType: "script", ResourceID: event.resource, ResourceName: "Checkout",
			Action: event.action, ActorType: "user", ActorSubject: "user:" + event.id, Status: "success", CreatedAt: event.createdAt,
		}); err != nil {
			t.Fatalf("SaveObjectActivity(%s): %v", event.id, err)
		}
	}
	all := []string{"y-f-modify", "a-e-create-second", "z-e-delete", "m-e-create-first", "a-d-create", "x-d-delete", "y-d-archive-old", "z-d-modify-old", "a-c-create", "z-c-delete", "a-b-delete", "y-b-create", "c-a-restore", "y-a-archive", "b-a-modify-second", "m-a-modify-first", "z-a-create", "0-a-earlier", "b-f-create-earlier"}
	resourceA := []string{"c-a-restore", "y-a-archive", "b-a-modify-second", "m-a-modify-first", "z-a-create", "0-a-earlier"}

	for _, check := range []struct {
		name   string
		filter ObjectActivityFilter
		want   []string
	}{
		{name: "project", filter: ObjectActivityFilter{ProjectID: project}, want: all},
		{name: "resource", filter: ObjectActivityFilter{ProjectID: project, ResourceType: "script", ResourceID: "a"}, want: resourceA},
		{name: "action", filter: ObjectActivityFilter{ProjectID: project, Action: "modify"}, want: []string{"y-f-modify", "z-d-modify-old", "b-a-modify-second", "m-a-modify-first", "0-a-earlier"}},
		{name: "resource-list", filter: ObjectActivityFilter{ProjectID: project, ResourceIDs: []string{"b", "c"}}, want: all[8:12]},
		{name: "time-bounded", filter: ObjectActivityFilter{ProjectID: project, After: &tied, Before: &tied}, want: all[:17]},
	} {
		t.Run(check.name, func(t *testing.T) {
			events, err := store.ListObjectActivity(check.filter)
			if err != nil {
				t.Fatalf("ListObjectActivity returned error: %v", err)
			}
			if got := objectActivityIDs(events); strings.Join(got, ",") != strings.Join(check.want, ",") {
				t.Fatalf("activity order = %v, want %v", got, check.want)
			}
		})
	}

	t.Run("deleted-flag", func(t *testing.T) {
		events, err := store.ListObjectActivity(ObjectActivityFilter{ProjectID: project})
		if err != nil {
			t.Fatalf("ListObjectActivity returned error: %v", err)
		}
		for _, event := range events {
			if want := event.ResourceID == "b"; event.Deleted != want {
				t.Errorf("%s (resource %s) deleted = %v, want %v", event.ID, event.ResourceID, event.Deleted, want)
			}
		}
	})

	t.Run("summaries", func(t *testing.T) {
		summaries, err := store.ObjectActivitySummaries(project, "script", []string{"a", "b", "c", "d", "e", "f"})
		if err != nil {
			t.Fatalf("ObjectActivitySummaries returned error: %v", err)
		}
		id := func(event *ObjectActivityEvent) string {
			if event == nil {
				return ""
			}
			return event.ID
		}
		for resource, want := range map[string][3]string{
			"a": {"z-a-create", "b-a-modify-second", "c-a-restore"},
			"b": {"y-b-create", "", ""},
			"c": {"a-c-create", "", ""},
			"d": {"a-d-create", "", ""},
			"e": {"a-e-create-second", "", ""},
			"f": {"b-f-create-earlier", "y-f-modify", ""},
		} {
			summary := summaries[resource]
			if got := [3]string{id(summary.Creator), id(summary.LastModifier), id(summary.LastArchive)}; got != want {
				t.Errorf("resource %s creator/last modifier/last archive = %v, want %v", resource, got, want)
			}
		}
	})

	// One-row offset pages must walk the same total order without repeats,
	// both through the resource index and through the project-wide read that
	// sorts ties.
	for _, walk := range []struct {
		name   string
		filter ObjectActivityFilter
		want   []string
	}{
		{name: "offset-pages-resource", filter: ObjectActivityFilter{ProjectID: project, ResourceType: "script", ResourceID: "a"}, want: resourceA},
		{name: "offset-pages-project", filter: ObjectActivityFilter{ProjectID: project}, want: all},
	} {
		t.Run(walk.name, func(t *testing.T) {
			for offset, id := range walk.want {
				filter := walk.filter
				filter.Limit, filter.Offset = 1, offset
				page, err := store.ListObjectActivity(filter)
				if err != nil {
					t.Fatalf("ListObjectActivity offset %d returned error: %v", offset, err)
				}
				if got := objectActivityIDs(page); len(got) != 1 || got[0] != id {
					t.Errorf("activity page at offset %d = %v, want [%s]", offset, got, id)
				}
			}
		})
	}
}

// checkObjectActivityBackfillKeepsAuditTieOrder copies tied legacy audit events
// into object activity and checks the copies list as the audit events do, in
// the order they were written: not reversed, and not in either ID order.
func checkObjectActivityBackfillKeepsAuditTieOrder(t *testing.T, store *SQLiteStore) {
	t.Helper()
	const project = "project-activity-backfill"
	tied := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for _, event := range []struct{ id, action string }{
		{id: "c-legacy-create", action: "script.create"},
		{id: "z-legacy-update", action: "script.update"},
		{id: "a-legacy-update", action: "script.update"},
		{id: "m-legacy-update", action: "script.update"},
	} {
		if err := store.SaveAuditEvent(&AuditEvent{
			ID: event.id, Action: event.action, ActorType: "user", ActorSubject: "alice", ProjectID: project,
			ResourceType: "script", ResourceID: "legacy-script", Status: "success", Details: map[string]any{"name": "Checkout"}, CreatedAt: tied,
		}); err != nil {
			t.Fatalf("SaveAuditEvent(%s): %v", event.id, err)
		}
	}
	if _, err := store.db.Exec(`DELETE FROM object_activity_migrations`); err != nil {
		t.Fatalf("forget the legacy backfill: %v", err)
	}
	if err := store.backfillObjectActivity(); err != nil {
		t.Fatalf("backfillObjectActivity returned error: %v", err)
	}
	events, err := store.ListObjectActivity(ObjectActivityFilter{ProjectID: project})
	if err != nil {
		t.Fatalf("ListObjectActivity returned error: %v", err)
	}
	if got, want := strings.Join(objectActivityIDs(events), ","), "legacy:m-legacy-update,legacy:a-legacy-update,legacy:z-legacy-update,legacy:c-legacy-create"; got != want {
		t.Fatalf("backfilled activity order = %s, want %s", got, want)
	}
	summaries, err := store.ObjectActivitySummaries(project, "script", []string{"legacy-script"})
	if err != nil {
		t.Fatalf("ObjectActivitySummaries returned error: %v", err)
	}
	if modifier := summaries["legacy-script"].LastModifier; modifier == nil || modifier.ID != "legacy:m-legacy-update" {
		t.Fatalf("backfilled last modifier = %+v, want legacy:m-legacy-update", modifier)
	}
}

func objectActivityIDs(events []*ObjectActivityEvent) []string {
	ids := make([]string, 0, len(events))
	for _, event := range events {
		ids = append(ids, event.ID)
	}
	return ids
}
