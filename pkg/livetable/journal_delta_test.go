package livetable

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestVTSJournalSplicesStaySmallAndReplayExactly(t *testing.T) {
	rows := make([]Row, 5000)
	for i := range rows {
		rows[i] = row(strings.Repeat("x", 1024), fmt.Sprint(i))
	}
	table := tableFor(t, ProfileVTS, rows, Limits{})
	seed, _ := table.Snapshot()
	restored, err := Restore(seed, nil)
	if err != nil {
		t.Fatal(err)
	}
	var entry Entry
	table.committer = CommitFunc(func(_ context.Context, e Entry) error { entry = e; return nil })
	requests := []Request{
		{Operation: OpPop, Columns: []string{"name"}},
		{Operation: OpRotate, Columns: []string{"n"}},
		{Operation: OpPush, Values: map[string]string{"n": "tail"}},
		{Operation: OpUpdate, Position: 5005, Values: map[string]string{"name": "padded"}},
		{Operation: OpClear, Position: 2},
		{Operation: OpIncrement, Position: 3, Columns: []string{"n"}, Delta: 1},
	}
	for i, request := range requests {
		request.ClientID, request.Sequence = "client", uint64(i+1)
		execute(t, table, request)
		if len(entry.Payload) > 4096 || strings.Contains(string(entry.Payload), "column_data") {
			t.Fatalf("%s journal copied table contents: %d bytes", request.Operation, len(entry.Payload))
		}
		if err := restored.Replay(entry); err != nil {
			t.Fatalf("replay %s: %v", request.Operation, err)
		}
		if !reflect.DeepEqual(table.s, restored.s) {
			t.Fatalf("%s replay changed state", request.Operation)
		}
	}
	// Corrupt indices must reject before publication, never panic/allocate.
	before, _ := restored.Snapshot()
	var d transition
	_ = json.Unmarshal(entry.Payload, &d)
	d.Receipt = nil
	d.ColumnSplices = []columnSplice{{Column: "n", Offset: -1, Delete: 1}}
	entry.PreviousRevision, entry.Revision = restored.s.Revision, restored.s.Revision+1
	entry.Payload, _ = json.Marshal(d)
	if err := restored.Replay(entry); CodeOf(err) != CodeInvalid {
		t.Fatalf("corrupt splice: %v", err)
	}
	after, _ := restored.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("corrupt replay changed state")
	}
}

func TestJournalCapacityRejectsBeforeDurableCommit(t *testing.T) {
	table := tableFor(t, ProfileNative, []Row{row(strings.Repeat("x", 2048), "0")}, Limits{MaxEntryBytes: 512})
	commits := 0
	table.committer = CommitFunc(func(context.Context, Entry) error { commits++; return nil })
	_, err := table.Execute(context.Background(), Request{ClientID: "client", Sequence: 1, Operation: OpPop})
	if CodeOf(err) != CodeCapacity || err.(*Error).OutcomeUnknown || commits != 0 || table.Stats().Counts.Available != 1 || table.Stats().Revision != 0 {
		t.Fatalf("capacity did not reject before commit: %v commits=%d stats=%+v", err, commits, table.Stats())
	}
	if _, err := table.Snapshot(); err != nil {
		t.Fatal("definite capacity failure poisoned serving", err)
	}
}

func TestNativeCloseJournalDoesNotCopyLeasedValues(t *testing.T) {
	rows := make([]Row, 20)
	for i := range rows {
		rows[i] = row(strings.Repeat("x", 16000), "0")
	}
	table := tableFor(t, ProfileNative, rows, Limits{})
	for i := range rows {
		execute(t, table, Request{ClientID: "client", Sequence: uint64(i + 1), Operation: OpLease})
	}
	before, _ := table.Snapshot()
	var entry Entry
	table.committer = CommitFunc(func(_ context.Context, e Entry) error { entry = e; return nil })
	if err := table.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(entry.Payload) > 4096 || strings.Contains(string(entry.Payload), "xxxxxxxxxx") {
		t.Fatalf("close recopied values: %d bytes", len(entry.Payload))
	}
	restored, err := Restore(before, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.Replay(entry); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(table.s, restored.s) {
		t.Fatal("compact close replay differs")
	}
}

func TestTerminalDiscardJournalIsConstantSizeAndReplays(t *testing.T) {
	for _, profile := range []Profile{ProfileNative, ProfileVTS} {
		table := tableFor(t, profile, []Row{row("one", "1"), row("two", "2")}, Limits{})
		before, _ := table.Snapshot()
		var entry Entry
		table.committer = CommitFunc(func(_ context.Context, e Entry) error { entry = e; return nil })
		if err := table.Close(context.Background(), true); err != nil {
			t.Fatal(err)
		}
		if len(entry.Payload) > 512 {
			t.Fatalf("discard journal: %d", len(entry.Payload))
		}
		restored, err := Restore(before, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := restored.Replay(entry); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(table.s, restored.s) {
			t.Fatal("discard recovery differs")
		}
	}
}

func TestCommittedProducerOutcomeSurvivesLaterWaiterCommitFailure(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	waitResult := make(chan error, 1)
	go func() {
		_, err := table.Execute(context.Background(), Request{ClientID: "wait", Sequence: 1, Operation: OpPop, Wait: time.Second})
		waitResult <- err
	}()
	deadline := time.Now().Add(time.Second)
	for table.Stats().Waiters == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if table.Stats().Waiters == 0 {
		t.Fatal("waiter did not arrive")
	}
	commits := 0
	table.committer = CommitFunc(func(context.Context, Entry) error {
		commits++
		if commits == 2 {
			return fmt.Errorf("ambiguous waiter commit")
		}
		return nil
	})
	result, err := table.Execute(context.Background(), Request{ClientID: "push", Sequence: 1, Operation: OpPush, Values: map[string]string{"name": "one"}})
	if err != nil || result.Status != StatusOK {
		t.Fatalf("committed producer outcome was replaced: %+v %v", result, err)
	}
	if err := <-waitResult; CodeOf(err) != CodeUnavailable {
		t.Fatalf("waiter should fail closed: %v", err)
	}
}

func TestExpiredLeasesUseBoundedJournalBatches(t *testing.T) {
	rows := make([]Row, 300)
	for i := range rows {
		rows[i] = row(fmt.Sprint(i), "0")
	}
	table := tableFor(t, ProfileNative, rows, Limits{MaxEntryBytes: 16 << 10})
	now := time.Now()
	table.now = func() time.Time { return now }
	for i := range rows {
		execute(t, table, Request{ClientID: "owner", Sequence: uint64(i + 1), Operation: OpLease})
	}
	entries := 0
	table.committer = CommitFunc(func(_ context.Context, entry Entry) error {
		entries++
		if len(entry.Payload) > 16<<10 {
			t.Fatal("oversized expiry batch")
		}
		return nil
	})
	now = now.Add(time.Minute)
	execute(t, table, Request{Operation: OpRead})
	if entries != 3 || table.Stats().Counts.Available != 300 {
		t.Fatalf("expiry batches=%d stats=%+v", entries, table.Stats())
	}
}

func TestCanceledWaitReceiptUsesBoundedSettlementContext(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	bounded := make(chan bool, 1)
	table.committer = CommitFunc(func(ctx context.Context, _ Entry) error {
		deadline, ok := ctx.Deadline()
		bounded <- ok && time.Until(deadline) <= 5*time.Second && ctx.Err() == nil
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := table.Execute(ctx, Request{ClientID: "wait", Sequence: 1, Operation: OpPop, Wait: time.Minute})
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for table.Stats().Waiters == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; CodeOf(err) != CodeCanceled {
		t.Fatal(err)
	}
	if !<-bounded {
		t.Fatal("cancellation created an unbounded or canceled receipt commit")
	}
}

// Measures core work + JSON journal encoding with a synchronous in-memory sink,
// not storage fsync/network/HA or a production throughput claim. Advancing the
// clock keeps a bounded one-receipt working set so b.N cannot fill retention.
func BenchmarkJournalOperation(b *testing.B) {
	for _, profile := range []Profile{ProfileNative, ProfileVTS} {
		for _, n := range []int{100, 10000} {
			b.Run(fmt.Sprintf("%s/rows=%d", profile, n), func(b *testing.B) {
				rows := make([]Row, n)
				for i := range rows {
					rows[i] = Row{Values: map[string]string{"value": strings.Repeat("x", 256)}}
				}
				now := time.Now()
				var bytes int64
				table, err := New(Config{ID: "bench", RunID: "bench", Profile: profile, Columns: []string{"value"}, Rows: rows, Now: func() time.Time { return now }, Committer: CommitFunc(func(_ context.Context, e Entry) error { bytes += int64(len(e.Payload)); return nil })})
				if err != nil {
					b.Fatal(err)
				}
				request := Request{ClientID: "client", Operation: OpUpdate, RecordID: "seed:1", Values: map[string]string{"value": strings.Repeat("y", 256)}}
				if profile == ProfileVTS {
					request.RecordID = ""
					request.Operation = OpRotate
					request.Values = nil
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					now = now.Add(3 * time.Minute)
					request.Sequence = uint64(i + 1)
					if _, err := table.Execute(context.Background(), request); err != nil {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(bytes)/float64(b.N), "journal_B/op")
			})
		}
	}
}
