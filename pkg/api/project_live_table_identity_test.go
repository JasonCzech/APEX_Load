package api

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestProjectLiveTableAttachmentCursorIsolationAndRetirement(t *testing.T) {
	runner, store, d := newProjectTableFixture(t)
	ctx := context.Background()
	producer := projectTableRun(t, runner, store, "cursor-producer", d, "push")
	pb := runner.liveBindingsForGroup(producer.ID, "workers")[0]
	for i, value := range []string{"first", "second"} {
		if _, err := runner.liveClient(producer.ID, "local/workers").Execute(ctx, pb.TableID, livetable.Request{ClientID: "worker", Sequence: uint64(i + 1), Operation: livetable.OpPush, Values: map[string]string{"value": value}}); err != nil {
			t.Fatal(err)
		}
	}
	first := projectTableRun(t, runner, store, "cursor-first", d, "next")
	second := projectTableRun(t, runner, store, "cursor-second", d, "next")
	read := func(test *ManagedTest, sequence uint64, cursor, want string) {
		t.Helper()
		binding := runner.liveBindingsForGroup(test.ID, "workers")[0]
		result, err := runner.liveClient(test.ID, "local/workers").Execute(ctx, binding.TableID, livetable.Request{ClientID: "worker", Sequence: sequence, Operation: livetable.OpNext, Cursor: cursor})
		if err != nil || result.Row == nil || result.Row.Values["value"] != want {
			t.Fatalf("%s cursor %q: %+v %v; want %s", test.ID, cursor, result, err, want)
		}
	}
	read(first, 1, "named", "first")
	// Different generator channels of the same attachment share its explicit
	// cursor, while a second run has an independent cursor of the same name.
	firstBinding := runner.liveBindingsForGroup(first.ID, "workers")[0]
	shardResult, err := runner.liveClient(first.ID, "remote/second-generator/workers").Execute(ctx, firstBinding.TableID, livetable.Request{ClientID: "worker", Sequence: 1, Operation: livetable.OpNext, Cursor: "named"})
	if err != nil || shardResult.Row == nil || shardResult.Row.Values["value"] != "second" {
		t.Fatalf("attachment cursor split by generator: %+v %v", shardResult, err)
	}
	read(second, 1, "named", "first")
	read(first, 2, "", "first")
	read(second, 2, "", "second")
	attachments, err := store.ListProjectLiveTableRunAttachments(ctx, d.ProjectID, first.ID)
	if err != nil || len(attachments) != 1 {
		t.Fatalf("first attachment %+v %v", attachments, err)
	}
	firstPrefix := projectAttachmentPrefix(attachments[0].Incarnation)
	if err := runner.finalizeLiveTables(ctx, first, "COMPLETE"); err != nil {
		t.Fatal(err)
	}
	db := projectFixtureSQL(t, store)
	if _, err := db.Exec(`UPDATE project_live_table_attachments SET retire_after=? WHERE test_id=?`, time.Now().Add(-time.Second), first.ID); err != nil {
		t.Fatal(err)
	}
	if err := runner.projectLiveTableService().sweep(ctx); err != nil {
		t.Fatal(err)
	}
	rt, err := runner.projectLiveTableService().acquire(ctx, d.ProjectID, d.ID, d.CurrentInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.projectLiveTableService().release(rt)
	snapshot, err := rt.table.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		Cursors map[string]uint64 `json:"cursors"`
	}
	if err := json.Unmarshal(snapshot.Payload, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Cursors) != 2 || state.Cursors["sequential"] != 2 {
		t.Fatalf("retirement removed shared/other cursor or retained detached cursor: %+v", state.Cursors)
	}
	for key := range state.Cursors {
		if strings.HasPrefix(key, firstPrefix) {
			t.Fatalf("detached cursor retained: %s", key)
		}
	}
	read(second, 3, "named", "second")
}

func TestProjectLiveTableDefaultLeaseOwnerMatchesClient(t *testing.T) {
	runner, store, d := newProjectTableFixture(t)
	ctx := context.Background()
	producer := projectTableRun(t, runner, store, "owner-producer", d, "push")
	pb := runner.liveBindingsForGroup(producer.ID, "workers")[0]
	if _, err := runner.liveClient(producer.ID, "local/workers").Execute(ctx, pb.TableID, livetable.Request{ClientID: "producer", Sequence: 1, Operation: livetable.OpPush, Values: map[string]string{"value": "owned"}}); err != nil {
		t.Fatal(err)
	}
	consumer := projectTableRun(t, runner, store, "default-owner", d, "lease")
	cb := runner.liveBindingsForGroup(consumer.ID, "workers")[0]
	client := runner.liveClient(consumer.ID, "local/workers")
	result, err := client.Execute(ctx, cb.TableID, livetable.Request{ClientID: "worker", Sequence: 1, Operation: livetable.OpLease})
	if err != nil || result.Lease == nil || result.Row == nil {
		t.Fatalf("lease %+v %v", result, err)
	}
	if _, err := client.Execute(ctx, cb.TableID, livetable.Request{ClientID: "worker", OwnerID: "worker", Sequence: 2, Operation: livetable.OpRelease, RecordID: result.Row.ID, LeaseToken: result.Lease.Token}); err != nil {
		t.Fatalf("explicit client owner could not settle default owner: %v", err)
	}
}

type projectDelayedDefinitionStore struct {
	*storage.SQLiteStore
	pause   atomic.Bool
	read    chan struct{}
	release chan struct{}
}

func (s *projectDelayedDefinitionStore) GetProjectLiveTable(ctx context.Context, project, id string) (*storage.ProjectLiveTableDefinition, error) {
	d, err := s.SQLiteStore.GetProjectLiveTable(ctx, project, id)
	if err == nil && s.pause.Swap(false) {
		close(s.read)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return d, err
}

func TestProjectLiveTableDelayedReadCannotDowngradeLifecycle(t *testing.T) {
	runner, store, d := newProjectTableFixture(t)
	ctx := context.Background()
	svc := runner.projectLiveTableService()
	rt, err := svc.acquire(ctx, d.ProjectID, d.ID, d.CurrentInstanceID)
	if err != nil {
		t.Fatal(err)
	}
	svc.release(rt)
	wrapper := &projectDelayedDefinitionStore{SQLiteStore: store, read: make(chan struct{}), release: make(chan struct{})}
	wrapper.pause.Store(true)
	runner.dataPoolStore = wrapper
	var release sync.Once
	done := make(chan struct{})
	defer func() {
		release.Do(func() { close(wrapper.release) })
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("delayed acquisition worker did not exit")
		}
	}()
	result := make(chan error, 1)
	go func() {
		defer close(done)
		rt, err := svc.acquire(ctx, d.ProjectID, d.ID, d.CurrentInstanceID)
		if err == nil {
			svc.release(rt)
		}
		result <- err
	}()
	select {
	case <-wrapper.read:
	case <-time.After(5 * time.Second):
		t.Fatal("acquisition did not reach definition read")
	}
	quiesced, err := svc.ChangeLifecycle(ctx, ProjectLiveTableLifecycleInput{ProjectID: d.ProjectID, TableID: d.ID, Action: "quiesce", Reason: "test", ExpectedDefinitionRevision: d.Revision, ExpectedInstanceID: d.CurrentInstanceID, ExpectedRevision: 0})
	if err != nil || quiesced.State != "quiesced" {
		t.Fatalf("quiesce %+v %v", quiesced, err)
	}
	release.Do(func() { close(wrapper.release) })
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("delayed acquisition did not finish")
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.definition.Revision != quiesced.Revision || rt.definition.State != "quiesced" {
		t.Fatalf("stale read downgraded committed lifecycle: %+v", rt.definition)
	}
}
