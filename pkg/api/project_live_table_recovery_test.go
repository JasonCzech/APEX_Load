package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

type projectLostReplyStore struct {
	*storage.SQLiteStore
	lose atomic.Bool
}

func (s *projectLostReplyStore) AppendProjectLiveTableEntry(ctx context.Context, e storage.ProjectLiveTableEntry, term storage.LiveTableExecutionTerm) error {
	if err := s.SQLiteStore.AppendProjectLiveTableEntry(ctx, e, term); err != nil {
		return err
	}
	if s.lose.Swap(false) {
		return errors.New("injected reply lost after durable commit")
	}
	return nil
}
func TestProjectLiveTableRecoversCommittedUnknownInSameOwner(t *testing.T) {
	runner, store, d := newProjectTableFixture(t)
	ctx := context.Background()
	producer := projectTableRun(t, runner, store, "uncertain-producer", d, "push")
	consumer := projectTableRun(t, runner, store, "still-attached-consumer", d, "pop")
	wrapper := &projectLostReplyStore{SQLiteStore: store}
	wrapper.lose.Store(true)
	runner.dataPoolStore = wrapper
	binding := runner.liveBindingsForGroup(producer.ID, "workers")[0]
	request := livetable.Request{ClientID: "worker", Sequence: 1, Operation: livetable.OpPush, Values: map[string]string{"value": "committed-once"}}
	client := runner.liveClient(producer.ID, "local/workers")
	if _, err := client.Execute(ctx, binding.TableID, request); !projectUnknown(err) {
		t.Fatalf("lost reply did not fence unknown outcome: %v", err)
	}
	old := runner.projectLiveTables[d.CurrentInstanceID]
	result, err := client.Execute(ctx, binding.TableID, request)
	if err != nil || result.Status != livetable.StatusOK {
		t.Fatalf("exact retry failed %+v %v", result, err)
	}
	current := runner.projectLiveTables[d.CurrentInstanceID]
	if current == old || current.term != old.term || current.table.Stats().Counts.Pushed != 1 {
		t.Fatal("recovery changed owner or duplicated committed mutation")
	}
	cb := runner.liveBindingsForGroup(consumer.ID, "workers")[0]
	popped, err := runner.liveClient(consumer.ID, "local/workers").Execute(ctx, cb.TableID, livetable.Request{ClientID: "reader", Sequence: 1, Operation: livetable.OpPop})
	if err != nil || popped.Row == nil || popped.Row.Values["value"] != "committed-once" {
		t.Fatalf("other attachment did not recover %+v %v", popped, err)
	}
	if runner.projectLiveTableService().pin(old) {
		runner.projectLiveTableService().release(old)
		t.Fatal("poisoned actor was republished")
	}
}
func projectFixtureSQL(t *testing.T, store *storage.SQLiteStore) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", store.BackendInfo().DSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func TestProjectLiveTableRestartReconcilesDeletedRunAndRetiresIdentity(t *testing.T) {
	runner, store, d := newProjectTableFixture(t)
	ctx := context.Background()
	producer := projectTableRun(t, runner, store, "maintenance-producer", d, "push")
	pb := runner.liveBindingsForGroup(producer.ID, "workers")[0]
	if _, err := runner.liveClient(producer.ID, "local/workers").Execute(ctx, pb.TableID, livetable.Request{ClientID: "producer", Sequence: 1, Operation: livetable.OpPush, Values: map[string]string{"value": "survives"}}); err != nil {
		t.Fatal(err)
	}
	if err := runner.finalizeLiveTables(ctx, producer, "COMPLETE"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateTestStatus(producer.ID, "COMPLETE", nil, ""); err != nil {
		t.Fatal(err)
	}
	consumer := projectTableRun(t, runner, store, "deleted-lessee", d, "lease")
	cb := runner.liveBindingsForGroup(consumer.ID, "workers")[0]
	result, err := runner.liveClient(consumer.ID, "local/workers").Execute(ctx, cb.TableID, livetable.Request{ClientID: "worker", OwnerID: "worker", Sequence: 1, Operation: livetable.OpLease})
	if err != nil || result.Lease == nil {
		t.Fatalf("lease %+v %v", result, err)
	}
	oldCore := runner.projectLiveTables[d.CurrentInstanceID]
	if err := store.DeleteTest(consumer.ID); err != nil {
		t.Fatal(err)
	}
	// No finalizer ran. The new controller must discover durable tombstones.
	runner.beginShutdown()
	replacement := NewTestRunner(10, NewHub(), store, WithRunContinuityKeyWrapper(testContinuityWrapper(t)))
	t.Cleanup(replacement.beginShutdown)
	if err := replacement.projectLiveTableService().sweep(ctx); err != nil {
		t.Fatal(err)
	}
	attachments, err := store.ListProjectLiveTableRunAttachments(ctx, d.ProjectID, consumer.ID)
	if err != nil || len(attachments) != 1 || attachments[0].State != "detached" || attachments[0].SettledRevision == nil {
		t.Fatalf("restart did not settle orphan %+v %v", attachments, err)
	}
	rt, err := replacement.projectLiveTableService().acquire(ctx, d.ProjectID, d.ID, d.CurrentInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if rt.table.Stats().Counts.Leased != 0 || rt.table.Stats().Counts.Available != 1 {
		t.Fatal("orphan lease did not return to shared instance")
	}
	if _, err := oldCore.table.Execute(ctx, livetable.Request{ClientID: "operator/stale/command", Sequence: 1, Operation: livetable.OpPush, Values: map[string]string{"value": "stale"}}); err == nil {
		t.Fatal("old owner served a project mutation")
	}
	replacement.projectLiveTableService().release(rt)
	db := projectFixtureSQL(t, store)
	if _, err := db.Exec(`UPDATE project_live_table_attachments SET retire_after=? WHERE table_id=?`, time.Now().Add(-time.Second), d.ID); err != nil {
		t.Fatal(err)
	}
	replacement.projectLiveMaintenanceCursor = ""
	if err := replacement.projectLiveTableService().sweep(ctx); err != nil {
		t.Fatal(err)
	}
	all, err := store.ListProjectLiveTableAttachments(ctx, d.ProjectID, d.ID)
	if err != nil || len(all) != 0 {
		t.Fatalf("expired tombstones retained %+v %v", all, err)
	}
	stats, err := replacement.projectLiveTableService().Stats(ctx, d.ProjectID, d.ID, d.CurrentInstanceID)
	if err != nil || stats.Clients != 0 || stats.Receipts != 0 || stats.Counts.Available != 1 {
		t.Fatalf("identity retirement did not reclaim metadata %+v %v", stats, err)
	}
}
func TestProjectLiveTableAutomaticLeaseAndProvenance(t *testing.T) {
	b := storage.ScenarioDataPoolBinding{Scope: "project", Alias: "orders", SelectionStrategy: "lease"}
	requirements, err := projectGroupRequirements(&dsl.Script{}, []storage.ScenarioDataPoolBinding{b})
	if err != nil || !reflect.DeepEqual(requirements["orders"], []string{"ack", "lease", "release", "renew"}) {
		t.Fatalf("automatic settlement requirement %+v %v", requirements, err)
	}
	runner, store, d := newProjectTableFixture(t)
	test := projectTableRun(t, runner, store, "provenance", d, "push")
	binding := runner.liveBindingsForGroup(test.ID, "workers")[0]
	if _, err := runner.liveClient(test.ID, "local/workers").Execute(context.Background(), binding.TableID, livetable.Request{ClientID: "private-client", Sequence: 1, Operation: livetable.OpPush, Values: map[string]string{"value": "secret-row"}}); err != nil {
		t.Fatal(err)
	}
	if err := runner.finalizeLiveTables(context.Background(), test, "COMPLETE"); err != nil {
		t.Fatal(err)
	}
	summary := runner.projectLiveTableArchiveSummaries(test.ID)
	body, _ := json.Marshal(summary)
	if len(summary) != 1 || summary[0].State != "detached" || summary[0].InstanceID != d.CurrentInstanceID || summary[0].Revision == 0 {
		t.Fatalf("attachment provenance %+v", summary)
	}
	for _, forbidden := range []string{"secret-row", "private-client", "pushed", "counts", "observations"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("shared values/global observations in run provenance: %s", body)
		}
	}
	if len(runner.liveTableArchiveSummaries(test.ID)) != 0 {
		t.Fatal("shared global counters attributed to run")
	}
}

func projectUnknown(err error) bool {
	var outcome *livetable.Error
	return errors.As(err, &outcome) && outcome.OutcomeUnknown
}

func TestProjectLiveTableRetirementSkipsUnexpiredTombstones(t *testing.T) {
	runner, store, d := newProjectTableFixture(t)
	ctx := context.Background()
	test := liveIntegrationNewRun(t, runner, store, "many-retained-attachments", nil)
	term, err := runner.captureProjectLiveTableTerm()
	if err != nil {
		t.Fatal(err)
	}
	creates := make([]storage.ProjectLiveTableAttachmentCreate, 65)
	for i := range creates {
		creates[i] = storage.ProjectLiveTableAttachmentCreate{ExpectedDefinitionRevision: d.Revision, Attachment: storage.ProjectLiveTableAttachment{ID: fmt.Sprintf("retained-%03d", i), Incarnation: fmt.Sprintf("scope-%03d", i), ProjectID: d.ProjectID, TableID: d.ID, InstanceID: d.CurrentInstanceID, TestID: test.ID, RunIncarnation: test.ID, Group: fmt.Sprintf("group-%03d", i), Alias: "orders", AllowedOperations: []string{"read"}, Principal: "dev-mode"}}
	}
	if _, err := store.CreateProjectLiveTableAttachments(ctx, creates, liveStorageTerm(term)); err != nil {
		t.Fatal(err)
	}
	if err := store.DetachProjectLiveTableAttachments(ctx, d.ProjectID, test.ID, time.Now().Add(time.Minute), liveStorageTerm(term)); err != nil {
		t.Fatal(err)
	}
	for _, a := range creates {
		if err := store.MarkProjectLiveTableAttachmentSettled(ctx, a.Attachment.ID, 0, liveStorageTerm(term)); err != nil {
			t.Fatal(err)
		}
	}
	db := projectFixtureSQL(t, store)
	if _, err := db.Exec(`UPDATE project_live_table_attachments SET retire_after=? WHERE id='retained-064'`, time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := runner.projectLiveTableService().sweep(ctx); err != nil {
		t.Fatal(err)
	}
	remaining, err := store.ListProjectLiveTableAttachments(ctx, d.ProjectID, d.ID)
	if err != nil || len(remaining) != 64 {
		t.Fatalf("unexpired prefix starved retirement: %d %v", len(remaining), err)
	}
	for _, a := range remaining {
		if a.ID == "retained-064" {
			t.Fatal("expired attachment not retired")
		}
	}
}
func TestProjectLiveTableFixedSchemaAndSnapshotBudget(t *testing.T) {
	runner, store, _ := newProjectTableFixture(t)
	ctx := context.Background()
	svc := runner.projectLiveTableService()
	d, err := svc.Create(ctx, ProjectLiveTableCreateInput{ProjectID: storage.DefaultProjectID, Name: "Column schema", ActorSubject: "dev-mode", Config: storage.ProjectLiveTableConfig{Profile: "vts_columns", Columns: []string{"value"}}})
	if err != nil {
		t.Fatal(err)
	}
	rt, err := svc.acquire(ctx, d.ProjectID, d.ID, d.CurrentInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	defer svc.release(rt)
	if _, err := rt.table.Execute(ctx, livetable.Request{ClientID: "operator/internal", Sequence: 1, Operation: livetable.OpCreateColumn, Columns: []string{"other"}}); livetable.CodeOf(err) != livetable.CodeInvalid {
		t.Fatalf("project fixed schema changed %v", err)
	}
	snapshot, err := rt.table.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateProjectTableSnapshot(snapshot, d.Config); err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(snapshot.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	payload["limits"].(map[string]any)["max_bytes"] = float64(d.Config.MaxBytes + 1)
	snapshot.Payload, _ = json.Marshal(payload)
	if err := validateProjectTableSnapshot(snapshot, d.Config); !errors.Is(err, storage.ErrLiveTableCorrupt) {
		t.Fatalf("unreserved snapshot budget accepted %v", err)
	}
	bindings, err := normalizeDataPoolBindingIntent([]storage.ScenarioDataPoolBinding{{Alias: "orders", Scope: "project", ProjectTableID: d.ID, AccessMode: "live"}})
	if err != nil {
		t.Fatal(err)
	}
	test := liveIntegrationNewRun(t, runner, store, "schema-mutator", bindings)
	test.Groups = []TestGroup{{Name: "workers", VUsers: 1, script: &dsl.Script{Actions: []dsl.Action{{Type: "live_table", Params: map[string]any{"alias": "orders", "operation": "create_column", "columns": []string{"other"}}}}}}}
	if err := runner.prepareLiveTables(ctx, test); err == nil {
		t.Fatal("dynamic project schema admitted")
	}
}
