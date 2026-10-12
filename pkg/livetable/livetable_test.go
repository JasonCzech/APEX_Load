package livetable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func tableFor(t *testing.T, profile Profile, rows []Row, limits Limits) *Table {
	t.Helper()
	table, err := New(Config{ID: "table", RunID: "run", Profile: profile, Columns: []string{"name", "n"}, Rows: rows, Limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	return table
}
func execute(t *testing.T, table *Table, r Request) Result {
	t.Helper()
	result, err := table.Execute(context.Background(), r)
	if err != nil {
		t.Fatalf("%s: %v", r.Operation, err)
	}
	return result
}
func row(name, n string) Row { return Row{Values: map[string]string{"name": name, "n": n}} }

func TestNativeConcurrentConsumptionAndReceipts(t *testing.T) {
	rows := make([]Row, 80)
	for i := range rows {
		rows[i] = row(fmt.Sprint(i), "0")
	}
	table := tableFor(t, ProfileNative, rows, Limits{})
	var wg sync.WaitGroup
	results := make(chan string, len(rows))
	for i := range rows {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := Request{ClientID: fmt.Sprintf("client-%d", i), Sequence: 1, Operation: OpPop}
			first, err := table.Execute(context.Background(), r)
			if err != nil {
				t.Error(err)
				return
			}
			again, err := table.Execute(context.Background(), r)
			if err != nil || !reflect.DeepEqual(first, again) {
				t.Errorf("retry changed outcome: %v %v", again, err)
			}
			results <- first.Row.ID
		}(i)
	}
	wg.Wait()
	close(results)
	seen := map[string]bool{}
	for id := range results {
		if seen[id] {
			t.Fatalf("record consumed twice: %s", id)
		}
		seen[id] = true
	}
	if len(seen) != len(rows) {
		t.Fatalf("got %d records", len(seen))
	}
	stats := table.Stats()
	if stats.Counts.Popped != 80 || stats.Counts.Available != 0 {
		t.Fatalf("bad conservation: %+v", stats.Counts)
	}
}

func TestConditionalFailureReplayAndDigestConflict(t *testing.T) {
	table := tableFor(t, ProfileNative, []Row{row("one", "1")}, Limits{})
	r := Request{ClientID: "a", Sequence: 1, Operation: OpUpdate, RecordID: "seed:1", Values: map[string]string{"n": "3"}, Expected: map[string]string{"n": "2"}}
	first := execute(t, table, r)
	if first.Status != StatusConditionFailed {
		t.Fatal(first)
	}
	execute(t, table, Request{ClientID: "b", Sequence: 1, Operation: OpUpdate, RecordID: "seed:1", Values: map[string]string{"n": "2"}})
	if retry := execute(t, table, r); !reflect.DeepEqual(first, retry) {
		t.Fatal("conditional retry reevaluated")
	}
	r.Values["n"] = "4"
	if _, err := table.Execute(context.Background(), r); CodeOf(err) != CodeConflict {
		t.Fatalf("changed payload: %v", err)
	}
}

func TestReceiptSurvivesCheckpointTailAndRetirement(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	table := tableFor(t, ProfileNative, []Row{row("one", "1"), row("two", "2")}, Limits{ReceiptRetention: 10 * time.Minute})
	table.now = func() time.Time { return now }
	var entries []Entry
	table.committer = CommitFunc(func(_ context.Context, e Entry) error { entries = append(entries, e); return nil })
	r := Request{ClientID: "a", Sequence: 1, Operation: OpPop}
	first := execute(t, table, r)
	snapshot, err := table.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	execute(t, table, Request{ClientID: "b", Sequence: 1, Operation: OpIncrement, RecordID: "seed:2", Columns: []string{"n"}, Delta: 1})
	restored, err := RestoreWithClock(snapshot, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	restored.now = func() time.Time { return now }
	if err = restored.Replay(entries[1]); err != nil {
		t.Fatal(err)
	}
	now = now.Add(3 * time.Minute)
	if retry := execute(t, restored, r); !reflect.DeepEqual(first, retry) {
		t.Fatal("checkpoint lost receipt")
	}
	now = now.Add(8 * time.Minute)
	if _, err = restored.Execute(context.Background(), r); CodeOf(err) != CodeExpired {
		t.Fatalf("expired receipt reexecuted: %v", err)
	}
	execute(t, restored, Request{ClientID: "a", Sequence: 3, Operation: OpRead})
	execute(t, restored, Request{ClientID: "a", Sequence: 3, Operation: OpPop})
	if _, err = restored.Execute(context.Background(), Request{ClientID: "a", Sequence: 2, Operation: OpPop}); CodeOf(err) != CodeExpired {
		t.Fatalf("lower unseen sequence admitted: %v", err)
	}
	snapshot, err = restored.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err = RestoreWithClock(snapshot, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, err = restored.Execute(context.Background(), r); CodeOf(err) != CodeExpired {
		t.Fatalf("retirement missing after checkpoint: %v", err)
	}
}

func TestCommitVisibilityAndUnknownOutcome(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	entered := make(chan struct{})
	release := make(chan struct{})
	var entry Entry
	table.committer = CommitFunc(func(_ context.Context, e Entry) error {
		entry = e
		close(entered)
		<-release
		return errors.New("connection lost")
	})
	writeDone := make(chan error, 1)
	go func() {
		_, err := table.Execute(context.Background(), Request{ClientID: "a", Sequence: 1, Operation: OpPush, Values: map[string]string{"name": "one"}})
		writeDone <- err
	}()
	<-entered
	readDone := make(chan error, 1)
	go func() { _, err := table.Execute(context.Background(), Request{Operation: OpRead}); readDone <- err }()
	select {
	case <-readDone:
		t.Fatal("read observed speculative state")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	var unknown *Error
	if err := <-writeDone; !errors.As(err, &unknown) || !unknown.OutcomeUnknown {
		t.Fatalf("commit error: %v", err)
	}
	if err := <-readDone; CodeOf(err) != CodeUnavailable {
		t.Fatalf("read after failed commit: %v", err)
	}
	if table.s.Revision != 0 || len(table.s.Records) != 0 {
		t.Fatal("failed commit published state")
	}
	// A successful database commit with a lost reply recovers the exact entry.
	fresh := tableFor(t, ProfileNative, nil, Limits{})
	snapshot, _ := fresh.Snapshot()
	recovered, err := Restore(snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = recovered.Replay(entry); err != nil {
		t.Fatal(err)
	}
	result := execute(t, recovered, Request{ClientID: "a", Sequence: 1, Operation: OpPush, Values: map[string]string{"name": "one"}})
	if result.Row == nil || recovered.Stats().Counts.Pushed != 1 {
		t.Fatal("uncertain commit did not reconcile")
	}
}

func TestLeaseRenewalRedeliveryAndQuarantine(t *testing.T) {
	now := time.Now().UTC()
	table := tableFor(t, ProfileNative, []Row{row("one", "1")}, Limits{DefaultLeaseDuration: time.Second, MaxLeaseDuration: 2 * time.Second, MaxLeaseLifetime: 3 * time.Second, MaxDeliveryAttempts: 2})
	table.now = func() time.Time { return now }
	first := execute(t, table, Request{ClientID: "a", OwnerID: "vu", Sequence: 1, Operation: OpLease})
	if first.Lease == nil {
		t.Fatal(first)
	}
	if _, err := table.Execute(context.Background(), Request{ClientID: "other", OwnerID: "attacker", Sequence: 1, Operation: OpAck, RecordID: first.Row.ID, LeaseToken: first.Lease.Token}); CodeOf(err) != CodeLease {
		t.Fatalf("forged lease accepted: %v", err)
	}
	now = now.Add(500 * time.Millisecond)
	renew := execute(t, table, Request{ClientID: "renew", OwnerID: "vu", Sequence: 1, Operation: OpRenew, RecordID: first.Row.ID, LeaseToken: first.Lease.Token, LeaseDuration: 2 * time.Second})
	if !renew.Lease.ExpiresAt.After(first.Lease.ExpiresAt) {
		t.Fatal("separate renewal lane failed")
	}
	now = now.Add(3 * time.Second)
	second := execute(t, table, Request{ClientID: "b", Sequence: 1, Operation: OpLease})
	if second.Lease.Token == first.Lease.Token || second.Lease.DeliveryAttempt != 2 {
		t.Fatal("lease acquisition not distinct")
	}
	if _, err := table.Execute(context.Background(), Request{ClientID: "a", OwnerID: "vu", Sequence: 2, Operation: OpAck, RecordID: first.Row.ID, LeaseToken: first.Lease.Token}); CodeOf(err) != CodeLease {
		t.Fatalf("old acquisition ack accepted: %v", err)
	}
	execute(t, table, Request{ClientID: "b", Sequence: 2, Operation: OpRelease, RecordID: second.Row.ID, LeaseToken: second.Lease.Token})
	if result := execute(t, table, Request{ClientID: "c", Sequence: 1, Operation: OpLease}); result.Status != StatusEmpty {
		t.Fatal("poison record redelivered")
	}
	if table.Stats().Counts.Quarantined != 1 {
		t.Fatal("missing quarantine")
	}
	page, err := table.Inspect(0, 1, 0)
	if err != nil || page.Rows[0].State != "quarantined" || page.Rows[0].DeliveryAttempts != 2 {
		t.Fatalf("quarantine export lost state: %#v %v", page, err)
	}
	snapshot, err := table.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Restore(snapshot, nil); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingConsumerAllowsProducerAndDuplicate(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	request := Request{ClientID: "consumer", Sequence: 1, Operation: OpPop, Wait: time.Second}
	results := make(chan Result, 2)
	errs := make(chan error, 2)
	for range 2 {
		go func() { r, err := table.Execute(context.Background(), request); results <- r; errs <- err }()
	}
	waitFor(t, func() bool { return table.Stats().Waiters == 1 })
	if _, err := table.Execute(context.Background(), Request{ClientID: "consumer", Sequence: 2, Operation: OpPush, Values: map[string]string{"name": "bad"}}); CodeOf(err) != CodeSequence {
		t.Fatalf("parallel same client admitted: %v", err)
	}
	execute(t, table, Request{ClientID: "producer", Sequence: 1, Operation: OpPush, Values: map[string]string{"name": "ready"}})
	a, b := <-results, <-results
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) || a.Row.Values["name"] != "ready" {
		t.Fatalf("bad wait results: %#v %#v", a, b)
	}
	if table.Stats().Waiters != 0 || table.Stats().Counts.Popped != 1 {
		t.Fatal("waiter leaked or consumed twice")
	}
}

func TestWaiterCancellationReceiptAndColumnFairness(t *testing.T) {
	table := tableFor(t, ProfileVTS, nil, Limits{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	r := Request{ClientID: "a", Sequence: 1, Operation: OpPop, Columns: []string{"name"}, Wait: time.Second}
	go func() { _, err := table.Execute(ctx, r); done <- err }()
	waitFor(t, func() bool { return table.Stats().Waiters == 1 })
	execute(t, table, Request{ClientID: "producer", Sequence: 1, Operation: OpPush, Values: map[string]string{"n": "1"}})
	got := execute(t, table, Request{ClientID: "b", Sequence: 1, Operation: OpPop, Columns: []string{"n"}})
	if got.Row.Values["n"] != "1" {
		t.Fatal("ineligible waiter blocked independent column")
	}
	cancel()
	if err := <-done; CodeOf(err) != CodeCanceled {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := table.Execute(context.Background(), r); CodeOf(err) != CodeCanceled {
		t.Fatalf("canceled operation retried as new: %v", err)
	}
}

func TestVTSRaggedClearRotateIncrementAndBounds(t *testing.T) {
	table := tableFor(t, ProfileVTS, []Row{{Values: map[string]string{"name": "one", "n": "1"}}, {Values: map[string]string{"name": "two"}}}, Limits{})
	execute(t, table, Request{ClientID: "a", Sequence: 1, Operation: OpClear, Position: 2})
	inspection, err := table.Inspect(0, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Rows[1].Values["name"] != "" {
		t.Fatal("clear did not empty present cell")
	}
	if _, ok := inspection.Rows[1].Values["n"]; ok {
		t.Fatal("clear filled absent cell")
	}
	if len(inspection.Rows) != 2 {
		t.Fatal("clear shifted row")
	}
	execute(t, table, Request{ClientID: "a", Sequence: 2, Operation: OpRotate, Columns: []string{"name"}})
	read := execute(t, table, Request{Operation: OpRead, Position: 1})
	if read.Row.Values["name"] != "" || read.Row.Values["n"] != "1" {
		t.Fatal("columns did not rotate independently")
	}
	execute(t, table, Request{ClientID: "a", Sequence: 3, Operation: OpIncrement, Position: 1, Columns: []string{"n"}, Delta: 1})
	if v := execute(t, table, Request{Operation: OpRead}).Row.Values["n"]; v != "2" {
		t.Fatal(v)
	}
	if _, err = table.Execute(context.Background(), Request{ClientID: "a", Sequence: 4, Operation: OpIncrement, Position: 2, Columns: []string{"n"}, Delta: 1}); CodeOf(err) != CodeInvalid {
		t.Fatal("absent integer accepted")
	}
	if _, err = table.Execute(context.Background(), Request{ClientID: "a", Sequence: 4, Operation: OpLease}); CodeOf(err) != CodeInvalid {
		t.Fatal("VTS lease accepted")
	}
	snapshot, err := table.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := restored.Inspect(0, 10, 0)
	before, _ := table.Inspect(0, 10, 0)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("ragged snapshot changed data")
	}
}

func TestBoundsRejectBeforeConsumptionAndProtectReceipts(t *testing.T) {
	table := tableFor(t, ProfileNative, []Row{row("long value that cannot fit the result envelope", "1")}, Limits{MaxResultBytes: 32, MaxReceiptBytes: 4096})
	if _, err := table.Execute(context.Background(), Request{ClientID: "a", Sequence: 1, Operation: OpPop}); CodeOf(err) != CodeCapacity {
		t.Fatalf("oversized response: %v", err)
	}
	if table.Stats().Counts.Available != 1 || table.Stats().Revision != 0 {
		t.Fatal("oversized response consumed row")
	}
	table = tableFor(t, ProfileNative, []Row{row("one", "1"), row("two", "2")}, Limits{MaxReceipts: 1})
	execute(t, table, Request{ClientID: "a", Sequence: 1, Operation: OpPop})
	if _, err := table.Execute(context.Background(), Request{ClientID: "b", Sequence: 1, Operation: OpPop}); CodeOf(err) != CodeCapacity {
		t.Fatalf("receipt budget: %v", err)
	}
	if table.Stats().Counts.Available != 1 {
		t.Fatal("receipt exhaustion consumed work")
	}
	table = tableFor(t, ProfileNative, nil, Limits{MaxClients: 1})
	execute(t, table, Request{ClientID: "a", Sequence: 1, Operation: OpPop})
	if _, err := table.Execute(context.Background(), Request{ClientID: "restarted", Sequence: 1, Operation: OpPop}); CodeOf(err) != CodeCapacity {
		t.Fatal("client tombstones grew beyond budget")
	}
}

func TestCloseReleasesLeasesAndRecoveryRejectsCorruption(t *testing.T) {
	table := tableFor(t, ProfileNative, []Row{row("one", "1")}, Limits{})
	execute(t, table, Request{ClientID: "a", Sequence: 1, Operation: OpLease})
	if err := table.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	stats := table.Stats()
	if stats.Counts.Leased != 0 || stats.Counts.Available != 1 {
		t.Fatal(stats.Counts)
	}
	if _, err := table.Execute(context.Background(), Request{Operation: OpRead}); CodeOf(err) != CodeClosed {
		t.Fatal("closed table still serves")
	}
	if result, err := table.Inspect(0, 10, 0); err != nil || len(result.Rows) != 1 {
		t.Fatalf("final inspection: %v %v", result, err)
	}
	snapshot, err := table.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Restore(snapshot, nil); err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	if err = json.Unmarshal(snapshot.Payload, &state); err != nil {
		t.Fatal(err)
	}
	state["data_bytes"] = 0
	snapshot.Payload, _ = json.Marshal(state)
	if _, err = Restore(snapshot, nil); err == nil {
		t.Fatal("corrupt accounting accepted")
	}
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition did not become true")
}

func TestNativeQueueAgainstReferenceModelAcrossRecovery(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	rng := rand.New(rand.NewSource(91))
	queue := []string{}
	sequence := uint64(0)
	for step := 0; step < 300; step++ {
		sequence++
		r := Request{ClientID: "model-client", Sequence: sequence}
		if len(queue) == 0 || rng.Intn(2) == 0 {
			value := fmt.Sprint(step)
			r.Operation = OpPush
			r.Values = map[string]string{"name": value}
			result := execute(t, table, r)
			if result.Row.Values["name"] != value {
				t.Fatal("push model mismatch")
			}
			queue = append(queue, value)
		} else {
			r.Operation = OpPop
			result := execute(t, table, r)
			if result.Row.Values["name"] != queue[0] {
				t.Fatalf("step%d got%s want%s", step, result.Row.Values["name"], queue[0])
			}
			queue = queue[1:]
		}
		if step%17 == 0 {
			snapshot, err := table.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			table, err = Restore(snapshot, nil)
			if err != nil {
				t.Fatal(err)
			}
		}
		if stats := table.Stats(); stats.Counts.Available != uint64(len(queue)) || stats.Counts.Seeded+stats.Counts.Pushed != stats.Counts.Available+stats.Counts.Popped {
			t.Fatalf("step%d conservation: %+v", step, stats.Counts)
		}
	}
}

func TestIntegerAndEncodedCapacityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		delta       int64
	}{{"upper", "9223372036854775807", 1}, {"lower", "-9223372036854775808", -1}, {"nonnumeric", "one", 1}, {"fraction", "1.5", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			table := tableFor(t, ProfileNative, []Row{row("one", tc.value)}, Limits{})
			_, err := table.Execute(context.Background(), Request{ClientID: "a", Sequence: 1, Operation: OpIncrement, Columns: []string{"n"}, Delta: tc.delta})
			if CodeOf(err) != CodeInvalid || table.Stats().Revision != 0 {
				t.Fatalf("overflow changed state: %v", err)
			}
		})
	}
	table := tableFor(t, ProfileNative, nil, Limits{MaxBytes: 1024})
	_, err := table.Execute(context.Background(), Request{ClientID: "a", Sequence: 1, Operation: OpPush, Values: map[string]string{"name": strings.Repeat("\x00", 200)}})
	if CodeOf(err) != CodeCapacity || table.Stats().Counts.Pushed != 0 {
		t.Fatalf("JSON escaping bypassed budget: %v", err)
	}
	_, err = New(Config{ID: "x", RunID: "run", Columns: []string{"value"}, Rows: []Row{{Values: map[string]string{"value": string([]byte{0xff})}}}})
	if CodeOf(err) != CodeInvalid {
		t.Fatal("invalid UTF-8 seed changes during serialization")
	}
	_, err = table.Execute(context.Background(), Request{ClientID: "a", Sequence: 1, Operation: OpPush, RecordID: "seed:1", Values: map[string]string{"name": "new"}})
	if CodeOf(err) != CodeInvalid {
		t.Fatal("caller recycled a consumed identity")
	}
}

func TestWaitDeadlineIsStableAndPendingClosesPromptly(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	r := Request{ClientID: "a", Sequence: 1, Operation: OpPop, Wait: time.Millisecond}
	if _, err := table.Execute(context.Background(), r); CodeOf(err) != CodeDeadline {
		t.Fatalf("wait deadline: %v", err)
	}
	execute(t, table, Request{ClientID: "producer", Sequence: 1, Operation: OpPush, Values: map[string]string{"name": "late"}})
	if _, err := table.Execute(context.Background(), r); CodeOf(err) != CodeDeadline {
		t.Fatal("timed-out wait consumed late data")
	}
	empty := tableFor(t, ProfileNative, nil, Limits{})
	done := make(chan error, 1)
	go func() {
		_, err := empty.Execute(context.Background(), Request{ClientID: "a", Sequence: 1, Operation: OpPop, Wait: time.Minute})
		done <- err
	}()
	waitFor(t, func() bool { return empty.Stats().Waiters == 1 })
	if err := empty.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if CodeOf(err) != CodeClosed {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close blocked on a pending consumer")
	}
}

func TestRecoveryClockSkewHoldsReassignmentAndRetryOutcomes(t *testing.T) {
	now := time.Now()
	table := tableFor(t, ProfileNative, []Row{row("one", "1")}, Limits{DefaultLeaseDuration: time.Second, MaxLeaseDuration: 2 * time.Second, MaxLeaseLifetime: 3 * time.Second, ReceiptRetention: time.Minute})
	table.now = func() time.Time { return now }
	request := Request{ClientID: "original", Sequence: 1, Operation: OpLease}
	first := execute(t, table, request)
	snapshot, err := table.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(10 * time.Hour)
	restored, err := RestoreWithClock(snapshot, nil, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if again := execute(t, restored, request); !reflect.DeepEqual(first, again) {
		t.Fatal("replacement clock prematurely retired receipt")
	}
	if result := execute(t, restored, Request{ClientID: "replacement", Sequence: 1, Operation: OpLease}); result.Status != StatusEmpty {
		t.Fatal("clock skew prematurely reassigned record")
	}
	now = now.Add(time.Second)
	if result := execute(t, restored, Request{ClientID: "replacement", Sequence: 2, Operation: OpLease}); result.Status != StatusEmpty {
		t.Fatal("recovery hold ended early")
	}
	now = now.Add(2 * time.Second)
	second := execute(t, restored, Request{ClientID: "replacement", Sequence: 3, Operation: OpLease})
	if second.Lease == nil || second.Lease.Token == first.Lease.Token {
		t.Fatal("record not reassigned after full local lease duration")
	}
	if _, err := restored.Execute(context.Background(), Request{ClientID: "original", Sequence: 2, Operation: OpAck, RecordID: first.Row.ID, LeaseToken: first.Lease.Token}); CodeOf(err) != CodeLease {
		t.Fatal("old token acknowledged new acquisition")
	}
}

func TestOperationTelemetryIsSeparateAndBounded(t *testing.T) {
	table := tableFor(t, ProfileNative, nil, Limits{})
	execute(t, table, Request{Operation: OpRead})
	_, _ = table.Execute(context.Background(), Request{Operation: "invalid"})
	_, _ = table.Execute(context.Background(), Request{ClientID: "a", Sequence: 1, Operation: OpPop, Wait: time.Millisecond})
	stats := table.Stats()
	if stats.OpCount != 3 || stats.OpErrors != 2 || stats.OpUnknown != 0 || stats.OpLatencyMaxNS == 0 || stats.OpLatencyTotalNS < stats.OpLatencyMaxNS {
		t.Fatalf("operation telemetry: %+v", stats)
	}
	var count uint64
	for _, n := range stats.OpLatencyBuckets {
		count += n
	}
	if count != 3 {
		t.Fatal("unbounded or missing histogram samples")
	}
	snapshot, err := table.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := Restore(snapshot, nil)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Stats().OpCount != 0 {
		t.Fatal("local telemetry replayed as workload evidence")
	}
}
