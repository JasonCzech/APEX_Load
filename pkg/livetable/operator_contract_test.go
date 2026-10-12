package livetable

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestOperatorRevisionAndExactOutcome(t *testing.T) {
	table := tableFor(t, ProfileNative, []Row{row("original", "1")}, Limits{})
	var entries []Entry
	table.committer = CommitFunc(func(_ context.Context, e Entry) error { entries = append(entries, e); return nil })
	revision := uint64(0)
	request := Request{ClientID: "operator", Sequence: 1, Operation: OpPush, Values: map[string]string{"name": "new"}, ExpectedRevision: &revision}
	first := execute(t, table, request)
	if len(entries) != 1 || entries[0].Outcome == nil || !reflect.DeepEqual(entries[0].Outcome.Result, first) || entries[0].Counts.Available != 2 {
		t.Fatalf("missing committed outcome: %+v", entries)
	}
	retry := execute(t, table, request)
	if !reflect.DeepEqual(retry, first) || len(entries) != 1 {
		t.Fatal("retry re-executed or checked stale revision before receipt")
	}
	request.Sequence++
	if _, err := table.Execute(context.Background(), request); CodeOf(err) != CodeConflict {
		t.Fatalf("stale revision: %v", err)
	}
	if table.Stats().Counts.Available != 2 {
		t.Fatal("stale command mutated table")
	}
	encoded, err := json.Marshal(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	if _, ok := wire["Outcome"]; ok {
		t.Fatal("runtime outcome leaked into journal envelope")
	}
	if _, ok := wire["Counts"]; ok {
		t.Fatal("runtime counts leaked into journal envelope")
	}
}

func TestOperatorCannotEditLeasedRecord(t *testing.T) {
	for _, operation := range []Operation{OpUpdate, OpClear, OpIncrement} {
		t.Run(string(operation), func(t *testing.T) {
			table := tableFor(t, ProfileNative, []Row{row("original", "1")}, Limits{})
			leased := execute(t, table, Request{ClientID: "worker", Sequence: 1, Operation: OpLease})
			revision := table.Stats().Revision
			request := Request{ClientID: "operator", Sequence: 1, Operation: operation, RecordID: leased.Row.ID, ExpectedRevision: &revision, Columns: []string{"n"}, Delta: 1}
			if operation == OpUpdate {
				request.Values = map[string]string{"n": "2"}
			}
			if _, err := table.Execute(context.Background(), request); CodeOf(err) != CodeLease {
				t.Fatalf("leased edit: %v", err)
			}
			if table.Stats().Revision != revision {
				t.Fatal("leased edit committed")
			}
		})
	}
}

func TestRevisionConditionalWaitRejected(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	revision := uint64(0)
	_, err := table.Execute(context.Background(), Request{ClientID: "operator", Sequence: 1, Operation: OpPop, Wait: 1, ExpectedRevision: &revision})
	if CodeOf(err) != CodeInvalid {
		t.Fatalf("conditional waiter accepted: %v", err)
	}
}
