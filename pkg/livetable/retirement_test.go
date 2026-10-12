package livetable

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProjectScopeSnapshotAndRetirement(t *testing.T) {
	var entries []Entry
	table, err := New(Config{ID: "instance", Scope: "project", ScopeID: "project", Columns: []string{"id"}, Rows: []Row{{Values: map[string]string{"id": "one"}}, {Values: map[string]string{"id": "two"}}}, Committer: CommitFunc(func(_ context.Context, e Entry) error { entries = append(entries, e); return nil })})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := table.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if initial.RunID != "" || initial.ScopeID != "project" {
		t.Fatalf("project identity %+v", initial)
	}
	execute(t, table, Request{ClientID: "a/one/command", OwnerID: "a/one/owner", Sequence: 1, Operation: OpLease})
	execute(t, table, Request{ClientID: "a/two/command", OwnerID: "a/two/owner", Sequence: 1, Operation: OpLease})
	if err := table.RetireOwnerScope(context.Background(), "a/one/"); CodeOf(err) != CodeConflict {
		t.Fatalf("owned lease retirement %v", err)
	}
	if err := table.RevokeOwnerScope(context.Background(), "a/one/"); err != nil {
		t.Fatal(err)
	}
	if got := table.Stats(); got.Counts.Leased != 1 || got.Counts.Available != 1 {
		t.Fatalf("settled another attachment %+v", got)
	}
	if err := table.RetireOwnerScope(context.Background(), "a/one/"); err != nil {
		t.Fatal(err)
	}
	if got := table.Stats(); got.Clients != 1 || got.Receipts != 1 {
		t.Fatalf("retained retired metadata %+v", got)
	}
	restored, err := Restore(initial, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := restored.Replay(e); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := table.Snapshot()
	b, _ := restored.Snapshot()
	if string(a.Payload) != string(b.Payload) {
		t.Fatal("replay differs")
	}
	malformed := initial
	malformed.ScopeID = "other"
	if _, err := Restore(malformed, nil); err == nil {
		t.Fatal("scope mismatch accepted")
	}
	var raw map[string]any
	_ = json.Unmarshal(a.Payload, &raw)
	if raw["run_id"] != nil {
		t.Fatal("project table has fake run identity")
	}
}
func TestProjectAuthorizerRechecksWaiters(t *testing.T) {
	var revoked atomic.Bool
	table, err := New(Config{ID: "i", Scope: "project", ScopeID: "p", Columns: []string{"id"}, Authorize: func(r Request) error {
		if revoked.Load() && strings.HasPrefix(r.ClientID, "a/one/") {
			return fail(CodeClosed, "detached")
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := table.Execute(context.Background(), Request{ClientID: "a/one/c", Sequence: 1, Operation: OpPop, Wait: time.Second})
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for table.Stats().Waiters == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	revoked.Store(true)
	execute(t, table, Request{ClientID: "a/two/c", Sequence: 1, Operation: OpPush, Values: map[string]string{"id": "safe"}})
	select {
	case err := <-done:
		if CodeOf(err) != CodeClosed {
			t.Fatalf("wait result %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter not revoked")
	}
	if table.Stats().Counts.Available != 1 {
		t.Fatal("revoked waiter consumed row")
	}
}
