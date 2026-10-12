package api

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newProjectTableFixture(t *testing.T) (*TestRunner, *storage.SQLiteStore, *storage.ProjectLiveTableDefinition) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "project.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	runner := NewTestRunner(10, NewHub(), store, WithRunContinuityKeyWrapper(testContinuityWrapper(t)))
	t.Cleanup(runner.beginShutdown)
	definition, err := runner.projectLiveTableService().Create(context.Background(), ProjectLiveTableCreateInput{ProjectID: storage.DefaultProjectID, Name: "Shared orders", Config: storage.ProjectLiveTableConfig{Columns: []string{"value"}}, ActorSubject: "dev-mode"})
	if err != nil {
		t.Fatal(err)
	}
	return runner, store, definition
}
func projectTableRun(t *testing.T, runner *TestRunner, store *storage.SQLiteStore, id string, d *storage.ProjectLiveTableDefinition, operation string) *ManagedTest {
	t.Helper()
	bindings, err := normalizeDataPoolBindingIntent([]storage.ScenarioDataPoolBinding{{Alias: "orders", Scope: "project", ProjectTableID: d.ID, AccessMode: "live"}})
	if err != nil {
		t.Fatal(err)
	}
	test := liveIntegrationNewRun(t, runner, store, id, bindings)
	params := map[string]any{"alias": "orders", "operation": operation}
	if operation == "push" {
		params["values"] = map[string]string{"value": "data"}
	}
	if operation == "lease" {
		params["result_prefix"] = "claimed"
	}
	test.Groups = []TestGroup{{Name: "workers", VUsers: 1, script: &dsl.Script{Actions: []dsl.Action{{Type: "live_table", Params: params}}}}}
	if err := runner.prepareLiveTables(context.Background(), test); err != nil {
		t.Fatal(err)
	}
	return test
}
func TestProjectLiveTablesShareInstancesAndDetachOneRun(t *testing.T) {
	runner, store, d := newProjectTableFixture(t)
	ctx := context.Background()
	producer := projectTableRun(t, runner, store, "producer", d, "push")
	consumer := projectTableRun(t, runner, store, "consumer", d, "pop")
	pb, cb := runner.liveBindingsForGroup(producer.ID, "workers"), runner.liveBindingsForGroup(consumer.ID, "workers")
	if len(pb) != 1 || len(cb) != 1 || pb[0].TableID == cb[0].TableID {
		t.Fatalf("attachments %+v %+v", pb, cb)
	}
	if len(runner.projectLiveTables) != 1 || runner.liveRun(producer.ID).budget != 0 {
		t.Fatal("shared instance was charged per run")
	}
	push := runner.liveClient(producer.ID, "local/workers")
	pop := runner.liveClient(consumer.ID, "local/workers")
	for seq := uint64(1); seq <= 2; seq++ {
		if _, err := push.Execute(ctx, pb[0].TableID, livetable.Request{ClientID: "same-worker", Sequence: seq, Operation: livetable.OpPush, Values: map[string]string{"value": "private"}}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := pop.Execute(ctx, cb[0].TableID, livetable.Request{ClientID: "same-worker", Sequence: 1, Operation: livetable.OpPop})
	if err != nil || first.Row.Values["value"] != "private" {
		t.Fatalf("shared consumer %+v %v", first, err)
	}
	if _, err := push.Execute(ctx, cb[0].TableID, livetable.Request{ClientID: "same-worker", Sequence: 3, Operation: livetable.OpPop}); err == nil {
		t.Fatal("cross-run attachment accepted")
	}
	if _, err := push.Execute(ctx, pb[0].TableID, livetable.Request{ClientID: "same-worker", Sequence: 3, Operation: livetable.OpPop}); err == nil {
		t.Fatal("undeclared operation accepted")
	}
	if err := runner.finalizeLiveTables(ctx, producer, "COMPLETE"); err != nil {
		t.Fatal(err)
	}
	if len(runner.liveRun(producer.ID).projectAttachments) != 0 {
		t.Fatal("terminal run retained shared raw state")
	}
	second, err := pop.Execute(ctx, cb[0].TableID, livetable.Request{ClientID: "same-worker", Sequence: 2, Operation: livetable.OpPop})
	if err != nil || second.Row == nil || second.Row.ID == first.Row.ID {
		t.Fatalf("finishing producer closed shared table %+v %v", second, err)
	}
	if err := runner.finalizeLiveTables(ctx, consumer, "COMPLETE"); err != nil {
		t.Fatal(err)
	}
	records, err := store.ListLiveTablesForRun(ctx, producer.ID, d.ProjectID)
	if err != nil || len(records) != 0 {
		t.Fatal("project table persisted as run table")
	}
	if err := runner.projectLiveTableService().evictIdle(ctx, true); err != nil {
		t.Fatal(err)
	}
	if len(runner.projectLiveTables) != 0 {
		t.Fatal("idle core reservation retained")
	}
	loaded, err := runner.projectLiveTableService().acquire(ctx, d.ProjectID, d.ID, d.CurrentInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	runner.projectLiveTableService().release(loaded)
	if loaded.table.Stats().Counts.Popped != 2 {
		t.Fatal("idle reload reseeded shared table")
	}
}
func TestProjectLiveTableLocalLeaseAndRetainedArchive(t *testing.T) {
	runner, _, d := newProjectTableFixture(t)
	ctx := context.Background()
	if runner.durableExecutionFence || runner.leadership != nil {
		t.Fatal("project creation enabled run HA")
	}
	svc := runner.projectLiveTableService()
	rt, err := svc.acquire(ctx, d.ProjectID, d.ID, d.CurrentInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	term := rt.term
	svc.release(rt)
	if !runner.projectLiveTableTermCurrent(term) {
		t.Fatal("local project term is not held")
	}
	if _, err := svc.Inspect(ctx, d.ProjectID, d.ID, d.CurrentInstanceID, 1, 0, 10); !errors.Is(err, storage.ErrLiveTableConflict) && livetable.CodeOf(err) != livetable.CodeConflict {
		t.Fatalf("stale inspect %v", err)
	}
	quiesced, err := svc.ChangeLifecycle(ctx, ProjectLiveTableLifecycleInput{ProjectID: d.ProjectID, TableID: d.ID, Action: "quiesce", Reason: "done", ExpectedDefinitionRevision: d.Revision, ExpectedInstanceID: d.CurrentInstanceID, ExpectedRevision: 0})
	if err != nil || quiesced.State != "quiesced" {
		t.Fatalf("quiesce %+v %v", quiesced, err)
	}
	archived, err := svc.ChangeLifecycle(ctx, ProjectLiveTableLifecycleInput{ProjectID: d.ProjectID, TableID: d.ID, Action: "archive", Reason: "done", ExpectedDefinitionRevision: quiesced.Revision, ExpectedInstanceID: d.CurrentInstanceID, ExpectedRevision: 0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Inspect(ctx, d.ProjectID, d.ID, d.CurrentInstanceID, 0, 0, 10); err != nil {
		t.Fatalf("retained archived read %s %v", archived.State, err)
	}
	runner.beginShutdown()
	if runner.projectLiveTableTermCurrent(term) {
		t.Fatal("project owner survived shutdown")
	}
	select {
	case <-runner.projectLiveLeaseDone:
	case <-time.After(time.Second):
		t.Fatal("local lease worker leaked")
	}
}
func TestProjectLiveTableBindingRejectsRunOverrides(t *testing.T) {
	base := storage.ScenarioDataPoolBinding{Scope: "project", ProjectTableID: "project-table", Alias: "orders", AccessMode: "live"}
	got, err := normalizeDataPoolBindingIntent([]storage.ScenarioDataPoolBinding{base})
	if err != nil || got[0].LiveOptions.Durability != "" || got[0].LiveOptions.SeedPolicy != "" {
		t.Fatalf("project options defaulted run policies %+v %v", got, err)
	}
	for _, o := range []*storage.LiveTableOptions{{SeedPolicy: "empty"}, {Durability: "durable"}, {EndMode: "discard"}, {Columns: []string{"override"}}, {MaxRows: 1}} {
		b := base
		b.LiveOptions = o
		if _, err := normalizeDataPoolBindingIntent([]storage.ScenarioDataPoolBinding{b}); err == nil {
			t.Fatalf("run override accepted %+v", o)
		}
	}
}
