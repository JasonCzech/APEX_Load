package storage

import (
	"context"
	"errors"
	"github.com/jasonczech/apexload/pkg/dsl"
	"testing"
	"time"
)

func projectTableFixture(t *testing.T, s *SQLiteStore) (ProjectLiveTableCreate, LiveTableExecutionTerm) {
	t.Helper()
	_, term := liveTableTestFixture(t, s)
	return ProjectLiveTableCreate{Definition: ProjectLiveTableDefinition{ID: "project-table", ProjectID: DefaultProjectID, Name: "Orders", CurrentInstanceID: "instance-1", RetentionDays: 7, Config: ProjectLiveTableConfig{Profile: "native_queue", Columns: []string{"id"}, MaxReceipts: 100}}, Instance: ProjectLiveTableInstance{ID: "instance-1", TableID: "project-table", ProjectID: DefaultProjectID, SeedDigest: "seed"}, Checkpoint: []byte(`{"encrypted":"checkpoint"}`)}, term
}
func TestProjectLiveTablePersistenceLifecycle(t *testing.T) {
	checkProjectLiveTablePersistenceLifecycle(t, newHAFencingStore(t))
}
func checkProjectLiveTablePersistenceLifecycle(t *testing.T, s *SQLiteStore) {
	ctx := context.Background()
	input, term := projectTableFixture(t, s)
	if _, err := s.CreateProjectLiveTable(ctx, input, LiveTableExecutionTerm{}); !errors.Is(err, ErrLiveTableFenced) {
		t.Fatalf("unfenced create %v", err)
	}
	d, err := s.CreateProjectLiveTable(ctx, input, term)
	if err != nil {
		t.Fatal(err)
	}
	attachments, err := s.CreateProjectLiveTableAttachments(ctx, []ProjectLiveTableAttachmentCreate{{ExpectedDefinitionRevision: d.Revision, Attachment: ProjectLiveTableAttachment{ID: "attachment", Incarnation: "incarnation", ProjectID: d.ProjectID, TableID: d.ID, InstanceID: d.CurrentInstanceID, TestID: "live-run", RunIncarnation: "live-run", Group: "workers", Alias: "orders", Principal: "dev-mode", AllowedOperations: []string{"lease", "ack"}}}}, term)
	if err != nil || len(attachments) != 1 {
		t.Fatalf("attach %v %v", attachments, err)
	}
	entry := ProjectLiveTableEntry{LiveTableEntry: LiveTableEntry{TableID: d.CurrentInstanceID, Revision: 1, At: time.Now().UTC(), Payload: []byte(`{"encrypted":"lease"}`)}, AttachmentID: "attachment", LeasedCount: 1}
	if err := s.AppendProjectLiveTableEntry(ctx, entry, term); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendProjectLiveTableEntry(ctx, entry, term); err != nil {
		t.Fatal(err)
	}
	op := ProjectLiveTableLifecycle{ProjectID: d.ProjectID, TableID: d.ID, ExpectedDefinitionRevision: 1, ExpectedInstanceID: d.CurrentInstanceID, ExpectedRevision: 0, State: "quiescing", Reason: "Maintenance"}
	if _, err := s.TransitionProjectLiveTable(ctx, op, term); !errors.Is(err, ErrLiveTableConflict) {
		t.Fatalf("stale revision %v", err)
	}
	op.ExpectedRevision = 1
	d, err = s.TransitionProjectLiveTable(ctx, op, term)
	if err != nil {
		t.Fatal(err)
	}
	op.ExpectedDefinitionRevision = d.Revision
	op.State = "quiesced"
	if _, err := s.TransitionProjectLiveTable(ctx, op, term); !errors.Is(err, ErrLiveTableConflict) {
		t.Fatalf("live attachments allowed %v", err)
	}
	if err := s.DetachProjectLiveTableAttachments(ctx, d.ProjectID, "live-run", time.Now().Add(10*time.Minute), term); err != nil {
		t.Fatal(err)
	}
	entry.PreviousRevision = 1
	entry.Revision = 2
	entry.Payload = []byte(`{"encrypted":"release"}`)
	entry.LeasedCount = 0
	if err := s.AppendProjectLiveTableEntry(ctx, entry, term); !errors.Is(err, ErrLiveTableClosed) {
		t.Fatalf("detached write %v", err)
	}
	entry.AttachmentID = ""
	if err := s.AppendProjectLiveTableEntry(ctx, entry, term); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkProjectLiveTableAttachmentSettled(ctx, "attachment", 2, term); err != nil {
		t.Fatal(err)
	}
	if err := s.RetireProjectLiveTableAttachment(ctx, "attachment", 2, term); !errors.Is(err, ErrLiveTableConflict) {
		t.Fatalf("early retirement %v", err)
	}
	if err := s.PublishProjectLiveTableCheckpoint(ctx, d.CurrentInstanceID, 1, []byte(`{"encrypted":"checkpoint-1"}`), term); err != nil {
		t.Fatal(err)
	}
	r, err := s.ReadProjectLiveTableRecovery(ctx, d.ProjectID, d.CurrentInstanceID)
	if err != nil || len(r.Entries) != 1 || r.Instance.Revision != 2 {
		t.Fatalf("recovery %+v %v", r, err)
	}
	op.ExpectedRevision = 2
	d, err = s.TransitionProjectLiveTable(ctx, op, term)
	if err != nil {
		t.Fatal(err)
	}
	op.ExpectedDefinitionRevision = d.Revision
	if _, err := s.db.Exec(`UPDATE project_live_table_attachments SET retire_after=? WHERE id='attachment'`, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.RetireProjectLiveTableAttachment(ctx, "attachment", 2, term); err != nil {
		t.Fatal(err)
	}
	replacement := input.Instance
	replacement.ID = "instance-2"
	d, err = s.ReplaceProjectLiveTableInstance(ctx, ProjectLiveTableReset{ProjectLiveTableLifecycle: op, Instance: replacement, Checkpoint: input.Checkpoint}, term)
	if err != nil {
		t.Fatal(err)
	}
	if d.CurrentInstanceID != "instance-2" || d.State != "quiesced" {
		t.Fatalf("reset %+v", d)
	}
	entry.Revision = 3
	entry.PreviousRevision = 2
	if err := s.AppendProjectLiveTableEntry(ctx, entry, term); !errors.Is(err, ErrLiveTableClosed) {
		t.Fatalf("old instance write %v", err)
	}
	r, err = s.ReadProjectLiveTableRecovery(ctx, d.ProjectID, "instance-1")
	if err != nil || r.Instance.State != "retired" || len(r.Entries) != 1 {
		t.Fatalf("old retained output %+v %v", r, err)
	}
	if _, err := s.GetProjectLiveTable(ctx, "other-project", d.ID); err == nil {
		t.Fatal("cross-project read")
	}
}
func TestProjectLiveTableAuditFailureRollsBack(t *testing.T) {
	s := newHAFencingStore(t)
	input, term := projectTableFixture(t, s)
	input.Audit = &AuditEvent{ID: "audit", Action: "create", ActorSubject: "actor", Details: map[string]any{"unsupported": func() {}}}
	if _, err := s.CreateProjectLiveTable(context.Background(), input, term); err == nil {
		t.Fatal("invalid audit accepted")
	}
	tables, err := s.ListProjectLiveTables(context.Background(), DefaultProjectID)
	if err != nil || len(tables) != 0 {
		t.Fatalf("orphaned table %+v %v", tables, err)
	}
}

func TestProjectLiveTablePostgresForeignKeyGraph(t *testing.T) {
	specs, err := postgresForeignKeySpecs()
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{"project_live_tables.project_id": "projects", "project_live_table_instances.table_id": "project_live_tables", "project_live_table_journal.instance_id": "project_live_table_instances", "project_live_table_attachments.instance_id": "project_live_table_instances", "project_live_table_operator_receipts.instance_id": "project_live_table_instances"}
	for _, spec := range specs {
		key := spec.table + "." + spec.column
		if want, ok := expected[key]; ok {
			if spec.references != want {
				t.Fatalf("%s references %s", key, spec.references)
			}
			delete(expected, key)
		}
		if spec.table == "project_live_table_attachments" && spec.references == "tests" {
			t.Fatal("test deletion must retain attachment tombstones")
		}
	}
	if len(expected) > 0 {
		t.Fatalf("missing foreign keys %v", expected)
	}
}

func TestProjectLiveTableCanonicalScenarioAuthoring(t *testing.T) {
	s := newHAFencingStore(t)
	input, term := projectTableFixture(t, s)
	d, err := s.CreateProjectLiveTable(context.Background(), input, term)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveScript(&dsl.Script{ID: "project-script", ProjectID: DefaultProjectID, Name: "Native", Protocol: "http"}); err != nil {
		t.Fatal(err)
	}
	groups := []ScenarioScript{{ScriptID: "project-script", Name: "Workers", VUsers: 1}}
	projectBinding := ScenarioDataPoolBinding{ID: "project-ref", Scope: "project", ProjectTableID: d.ID, Alias: "orders", AccessMode: "live", SelectionStrategy: "manual", LiveOptions: &LiveTableOptions{EmptyPolicy: "error", OperationTimeoutMS: 120000}}
	for _, tc := range []struct {
		id      string
		binding ScenarioDataPoolBinding
		valid   bool
	}{
		{"project-authored", projectBinding, true},
		{"empty-authored", ScenarioDataPoolBinding{ID: "empty", Alias: "empty", AccessMode: "live", SelectionStrategy: "manual", LiveOptions: &LiveTableOptions{SeedPolicy: "empty", Columns: []string{"value"}}}, true},
		{"original-authored", ScenarioDataPoolBinding{ID: "original", Alias: "original", AccessMode: "live", SelectionStrategy: "manual", LiveOptions: &LiveTableOptions{SeedPolicy: "original_seed", SeedRunID: "prior"}}, true},
		{"ordinary-missing", ScenarioDataPoolBinding{ID: "ordinary", Alias: "ordinary", AccessMode: "read_only", SelectionStrategy: "sequential"}, false},
	} {
		t.Run(tc.id, func(t *testing.T) {
			result, err := s.CreateScenarioAuthoringIfAbsent(&Scenario{ID: tc.id, Name: tc.id, Duration: "1m"}, groups, []ScenarioDataPoolBinding{tc.binding})
			if !tc.valid {
				if err == nil {
					t.Fatal("missing ordinary pool accepted")
				}
				return
			}
			if err != nil || !result.Applied {
				t.Fatalf("canonical authoring %+v %v", result, err)
			}
			got := result.Snapshot.DataPools[0]
			if got.Scope != tc.binding.Scope || got.ProjectTableID != tc.binding.ProjectTableID || got.PoolID != tc.binding.PoolID {
				t.Fatalf("reference lost %+v", got)
			}
			if tc.binding.Scope == "project" {
				before := result.Snapshot.ContentRevision
				got.LiveOptions = &LiveTableOptions{EmptyPolicy: "wait", WaitMS: 1000, OperationTimeoutMS: 120000}
				updated, err := s.UpdateScenarioAuthoringIfCurrent(result.Snapshot.Scenario, result.Snapshot.Scripts, []ScenarioDataPoolBinding{got}, scenarioAuthoringExpected(result.Snapshot))
				if err != nil || !updated.Applied || updated.Snapshot.ContentRevision == before {
					t.Fatalf("binding edit lost revision %+v %v", updated, err)
				}
			}
		})
	}
}
