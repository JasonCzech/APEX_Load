package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

func evacuationCheckpointFixture(t *testing.T) (*TestRunner, *storage.SQLiteStore, *ManagedTest, uint64, string, []byte, map[string][]GeneratorShardSummary, map[string][]GeneratorShardSummary) {
	t.Helper()
	tr, store, leader := newAdoptionTestRunner(t)
	owner, generation := leader.executionLeaseTerm()
	test := &ManagedTest{ID: "guarded-evacuation", Status: "RUNNING", Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue"}}
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.ID, Status: test.Status, StartTime: time.Now().UTC(), Groups: []any{}}); err != nil {
		t.Fatal(err)
	}
	tr.tests[test.ID] = test
	token := bytes.Repeat([]byte{9}, generatorrpc.RunSupervisionTokenSize)
	previous := map[string][]GeneratorShardSummary{"ga": {{Group: "ga", Target: "gen-a", VUsers: 5, ShardCount: 1}}}
	next := cloneGroupShardMap(previous)
	next["ga"][0].Target = "gen-b"
	return tr, store, test, generation, owner, token, previous, next
}

func TestRunEvacuationCheckpointInterruptionAndCompletion(t *testing.T) {
	for _, initial := range []bool{false, true} {
		t.Run(map[bool]string{false: "unarmed", true: "armed"}[initial], func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			if initial {
				tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
			}
			guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
			if err != nil || guard == "" {
				t.Fatalf("begin = %q, %v", guard, err)
			}
			record, err := store.GetTest(test.ID)
			if err != nil || record.ContinuityCheckpoint != guard {
				t.Fatalf("guard not durable: %v", err)
			}
			if tr.isRunAdoptionCandidate(test) || runContinuityStatusLocked(test).Armed {
				t.Fatal("interrupted evacuation remains adoptable")
			}
			var decoded runEvacuationCheckpoint
			if err := json.Unmarshal([]byte(guard), &decoded); err != nil {
				t.Fatal(err)
			}
			opened, err := openRunSupervisionToken(context.Background(), tr.continuityKeyWrapper, test.ID, decoded.Prior)
			if err != nil || !bytes.Equal(opened, token) {
				t.Fatalf("prior lineage lost: %v", err)
			}
			if _, _, err := decodeRunContinuityCheckpoint(guard); err == nil {
				t.Fatal("legacy reader accepted an in-flight topology")
			}
			tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
			record, _ = store.GetTest(test.ID)
			if record.ContinuityCheckpoint != guard {
				t.Fatal("ordinary re-arm erased guard")
			}
			if _, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next); err == nil {
				t.Fatal("second evacuation bypassed unresolved guard")
			}
			if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, previous, guard); err == nil {
				t.Fatal("wrong replacement topology completed guard")
			}
			if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, next, guard); err != nil {
				t.Fatal(err)
			}
			record, _ = store.GetTest(test.ID)
			cp, present, err := decodeRunContinuityCheckpoint(record.ContinuityCheckpoint)
			if err != nil || !present || cp.Shards["ga"][0].Target != "gen-b" || !runContinuityStatusLocked(test).Armed {
				t.Fatalf("completion not armed: %+v %v", cp, err)
			}
			if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, next, guard); err == nil {
				t.Fatal("replayed completion overwrote newer checkpoint")
			}
		})
	}
}

type evacuationCheckpointFaultStore struct {
	*storage.SQLiteStore
	before      func()
	reject      bool
	rejectWrite func(string) bool
}

func (s *evacuationCheckpointFaultStore) CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner string) (storage.TestStatusTransitionResult, error) {
	if s.before != nil {
		s.before()
	}
	if s.reject || (s.rejectWrite != nil && s.rejectWrite(next)) {
		return "", errors.New("injected checkpoint write failure")
	}
	return s.SQLiteStore.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(id, expected, next, lease, owner)
}

func TestRunEvacuationCheckpointRejectsUnavailableOwnershipAndStorage(t *testing.T) {
	for _, fault := range []string{"store", "terminal", "stale-snapshot", "wrong-owner", "wrong-generation", "canceled", "unsupported", "lost-lease"} {
		t.Run(fault, func(t *testing.T) {
			tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
			tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
			expected := test.continuityCheckpoint
			ctx := context.Background()
			switch fault {
			case "store":
				tr.testStore = &evacuationCheckpointFaultStore{SQLiteStore: store, reject: true}
			case "terminal":
				now := time.Now()
				if err := store.UpdateTestStatus(test.ID, "FAILED", &now, "stopped"); err != nil {
					t.Fatal(err)
				}
			case "stale-snapshot":
				if _, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, "changed", executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
			case "wrong-owner":
				owner = "other-owner"
			case "wrong-generation":
				generation++
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "lost-lease":
				if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
					t.Fatal(err)
				}
			case "unsupported":
				setContinuityCheckpointLocked(test, `{"version":99}`)
				expected = test.continuityCheckpoint
			}
			guard, err := tr.beginRunEvacuationCheckpoint(ctx, test, generation, owner, "gen-a", token, previous, next)
			if err == nil || guard != "" {
				t.Fatalf("unsafe begin accepted: %q %v", guard, err)
			}
			if test.continuityCheckpoint != expected {
				t.Fatal("failed persistence changed in-memory checkpoint")
			}
		})
	}
}

func TestRunEvacuationCheckpointRejectedCompletionRemainsUnarmed(t *testing.T) {
	tr, store, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	guard, err := tr.beginRunEvacuationCheckpoint(context.Background(), test, generation, owner, "gen-a", token, previous, next)
	if err != nil {
		t.Fatal(err)
	}
	test.Assignment.FailoverPolicy = "fail"
	if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, next, guard); err == nil {
		t.Fatal("changed policy falsely acknowledged completion")
	}
	test.Assignment.FailoverPolicy = "continue"
	tr.testStore = &evacuationCheckpointFaultStore{SQLiteStore: store, reject: true}
	if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, next, guard); err == nil {
		t.Fatal("failed completion accepted")
	}
	record, _ := store.GetTest(test.ID)
	if record.ContinuityCheckpoint != guard || runContinuityStatusLocked(test).Armed {
		t.Fatal("failed completion cleared durable guard")
	}
	tr.testStore = store
	restored := &ManagedTest{ID: test.ID, Status: "RUNNING", Assignment: test.Assignment}
	setContinuityCheckpointLocked(restored, record.ContinuityCheckpoint)
	if err := tr.reconcileStoredActiveTest(restored); err != nil {
		t.Fatal(err)
	}
	if restored.Status != "FAILED" || !strings.Contains(restored.Error, "during generator evacuation") {
		t.Fatalf("interrupted outcome: %s %s", restored.Status, restored.Error)
	}
}

func TestRunContinuityCASDoesNotOverwriteConcurrentEvacuationIntent(t *testing.T) {
	tr, store, test, generation, owner, token, previous, _ := evacuationCheckpointFixture(t)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	const concurrent = `{"version":2,"source":"gen-a"}`
	tr.testStore = &evacuationCheckpointFaultStore{SQLiteStore: store, before: func() {
		if _, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, concurrent, executionLeaseName, owner); err != nil {
			t.Fatal(err)
		}
	}}
	if err := tr.writeRunContinuity(context.Background(), test, generation, owner, token, previous, ""); err == nil {
		t.Fatal("stale arm overwrote concurrent intent")
	}
	record, _ := store.GetTest(test.ID)
	if record.ContinuityCheckpoint != concurrent {
		t.Fatal("durable intent overwritten")
	}
}

func TestRunEvacuationCheckpointPreservesHistoryAndRejectsInvalidCompletion(t *testing.T) {
	tr, _, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	tr.armRunContinuity(context.Background(), test, generation, owner, token, previous)
	// A previous re-arm may have failed after recording an adoption in memory.
	test.continuityFailovers = []RunFailover{{PriorGeneration: 1, Generation: 2, AdoptedAt: time.Now().UTC(), AdoptedGenerators: 1}}
	guard, err := tr.beginRunEvacuationCheckpoint(nil, test, generation, owner, "gen-a", token, previous, next)
	if err != nil {
		t.Fatal(err)
	}
	restored := &ManagedTest{Assignment: test.Assignment}
	setContinuityCheckpointLocked(restored, guard)
	if len(restored.continuityFailovers) != 1 || runContinuityStatusLocked(restored).Armed {
		t.Fatal("interruption lost historical failover or became armed")
	}
	for _, field := range []string{"version", "test", "generation", "owner", "source", "time", "topology"} {
		t.Run(field, func(t *testing.T) {
			var changed runEvacuationCheckpoint
			if err := json.Unmarshal([]byte(guard), &changed); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "version":
				changed.Version = 1
			case "test":
				changed.TestID = "other"
			case "generation":
				changed.Generation++
			case "owner":
				changed.OwnerID = "other"
			case "source":
				changed.Source = ""
			case "time":
				changed.PreparedAt = time.Time{}
			case "topology":
				changed.NextShards["ga"][0].VUsers++
			}
			raw, _ := json.Marshal(changed)
			if err := validateRunEvacuationCompletion(string(raw), test.ID, generation, owner, next); err == nil {
				t.Fatal("mismatched completion accepted")
			}
		})
	}
	if err := validateRunEvacuationCompletion("{", test.ID, generation, owner, next); err == nil {
		t.Fatal("malformed completion accepted")
	}
}

func TestRunEvacuationCheckpointPreservesNonContinuityBehavior(t *testing.T) {
	tr, _, test, generation, owner, token, previous, next := evacuationCheckpointFixture(t)
	for _, policy := range []string{"fail", "continue"} {
		test.Assignment.FailoverPolicy = policy
		if policy == "continue" {
			tr.continuityKeyWrapper = nil
		}
		guard, err := tr.beginRunEvacuationCheckpoint(nil, test, generation, owner, "gen-a", token, previous, next)
		if err != nil || guard != "" {
			t.Fatalf("unarmed %s run unexpectedly guarded: %v", policy, err)
		}
	}
}
