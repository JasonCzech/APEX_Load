package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestProjectLiveTableDeletionOwnership(t *testing.T) {
	for _, target := range []string{"run", "project"} {
		t.Run(target, func(t *testing.T) {
			checkProjectLiveTableDeletionOwnership(t, newHAFencingStore(t), target)
		})
	}
}

func checkProjectLiveTableDeletionOwnership(t *testing.T, store *SQLiteStore, target string) {
	t.Helper()
	ctx := context.Background()
	base, term := projectTableFixture(t, store)
	now := time.Now().UTC()
	for _, project := range []string{"delete-project", "keep-project"} {
		if err := store.SaveProject(&Project{ID: project, Name: project}); err != nil {
			t.Fatal(err)
		}
		run := project + "-run"
		if err := store.SaveTest(&TestRecord{ID: run, ProjectID: project, Name: run, Status: "RUNNING", StartTime: now, Groups: []any{}}); err != nil {
			t.Fatal(err)
		}
		input := base
		input.Definition.ID, input.Definition.ProjectID = project+"-table", project
		input.Definition.CurrentInstanceID = project + "-instance"
		input.Instance.ID, input.Instance.TableID, input.Instance.ProjectID = input.Definition.CurrentInstanceID, input.Definition.ID, project
		definition, err := store.CreateProjectLiveTable(ctx, input, term)
		if err != nil {
			t.Fatal(err)
		}
		attachment := ProjectLiveTableAttachment{ID: project + "-attachment", Incarnation: "attachment-incarnation", ProjectID: project, TableID: definition.ID, InstanceID: input.Instance.ID, TestID: run, RunIncarnation: run, Group: "workers", Alias: "orders", Principal: "dev-mode", AllowedOperations: []string{"read"}}
		if _, err := store.CreateProjectLiveTableAttachments(ctx, []ProjectLiveTableAttachmentCreate{{ExpectedDefinitionRevision: definition.Revision, Attachment: attachment}}, term); err != nil {
			t.Fatal(err)
		}
		entry := ProjectLiveTableEntry{LiveTableEntry: LiveTableEntry{TableID: input.Instance.ID, Revision: 1, At: now, Payload: []byte("encrypted-journal")}}
		receipt := ProjectLiveTableOperatorReceipt{InstanceID: input.Instance.ID, ActorSubject: "editor", KeyHash: strings.Repeat("a", 64), Digest: strings.Repeat("b", 64), ClientScope: "operator/deletion/", IssuedAt: now, ExpiresAt: now.Add(time.Minute), Outcome: []byte("encrypted-outcome")}
		audit := &AuditEvent{ID: project + "-audit", ActorSubject: receipt.ActorSubject, Action: "project_live_table.mutate", ProjectID: project, ResourceType: "project_live_table", ResourceID: definition.ID, CreatedAt: now}
		if err := store.AppendProjectLiveTableOperatorEntry(ctx, entry, receipt, audit, term); err != nil {
			t.Fatal(err)
		}
		if err := store.InitializeRendezvous(ctx, RendezvousRecord{RunID: run, Group: "workers", ProjectID: project, Snapshot: []byte(`{"barrier":"waiting"}`)}, term); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	if target == "project" {
		err = store.DeleteProject("delete-project")
	} else {
		err = store.DeleteTest("delete-project-run")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range []string{"delete-project", "keep-project"} {
		want := 1
		if target == "project" && project == "delete-project" {
			want = 0
		}
		for _, table := range []struct{ name, column, value string }{
			{"project_live_tables", "project_id", project},
			{"project_live_table_instances", "project_id", project},
			{"project_live_table_journal", "instance_id", project + "-instance"},
			{"project_live_table_operator_receipts", "instance_id", project + "-instance"},
			{"project_live_table_attachments", "instance_id", project + "-instance"},
		} {
			var count int
			if err := store.db.QueryRow(`SELECT COUNT(*) FROM `+table.name+` WHERE `+table.column+`=?`, table.value).Scan(&count); err != nil || count != want {
				t.Fatalf("%s %s after %s deletion = %d, want %d: %v", project, table.name, target, count, want, err)
			}
		}
		wantRendezvous := 1
		if project == "delete-project" {
			wantRendezvous = 0
		}
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM run_rendezvous WHERE project_id=?`, project).Scan(&count); err != nil || count != wantRendezvous {
			t.Fatalf("%s rendezvous after %s deletion = %d, want %d: %v", project, target, count, wantRendezvous, err)
		}
		// Value-free governance evidence outlives either deletion; table data
		// and exact encrypted outcomes follow their project's lifecycle.
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM audit_events WHERE id=?`, project+"-audit").Scan(&count); err != nil || count != 1 {
			t.Fatalf("audit retention = %d: %v", count, err)
		}
	}
	if target == "run" {
		attachments, err := store.ListProjectLiveTableRunAttachments(ctx, "delete-project", "delete-project-run")
		if err != nil || len(attachments) != 1 || attachments[0].State != "detached" || attachments[0].DetachedAt == nil || attachments[0].RetireAfter == nil {
			t.Fatalf("deleted run must leave a bounded detached cleanup tombstone: %+v %v", attachments, err)
		}
	}
}
