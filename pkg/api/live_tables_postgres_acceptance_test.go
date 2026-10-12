//go:build postgres_integration

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// These tests use real PostgreSQL transactions, independent controller state,
// and (for the lost-reply trial) the real generator gRPC/engine path. They are
// bounded fault evidence, not a replacement for process/deployment HA trials.
func newPostgresLiveRunner(t *testing.T, dsn, owner string) (*TestRunner, *storage.PostgresStore) {
	t.Helper()
	store, err := storage.NewPostgresStore(dsn, storage.StoreConnectionOptions{MaxOpenConns: 4}, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	// Leadership must be installed before stored active runs are hydrated; a
	// freshly constructed successor is a follower until it acquires its lease.
	runner := NewTestRunner(10, NewHub(), store, WithRunContinuityKeyWrapper(testContinuityWrapper(t)), WithExecutionLeadership(newExecutionLeadership(store, owner)))
	t.Cleanup(runner.beginShutdown)
	return runner, store
}

func postgresLiveRun(t *testing.T, runner *TestRunner, store storage.Store, id string, aliases int) *ManagedTest {
	t.Helper()
	var bindings []storage.ScenarioDataPoolBinding
	for i := 0; i < aliases; i++ {
		bindings = append(bindings, storage.ScenarioDataPoolBinding{PoolID: "pg-live-seed", Alias: fmt.Sprintf("work%d", i), AccessMode: "live", LiveOptions: &storage.LiveTableOptions{Durability: "durable", EndMode: "write_back"}})
	}
	bindings, err := normalizeDataPoolBindingIntent(bindings)
	if err != nil {
		t.Fatal(err)
	}
	test := &ManagedTest{ID: id, Name: id, ProjectID: storage.DefaultProjectID, Status: "RUNNING", CreatedAt: time.Now(), CreatedBySubject: "dev-mode", DataPools: bindings}
	if err := store.SaveTest(&storage.TestRecord{ID: id, Name: id, ProjectID: test.ProjectID, Status: test.Status, StartTime: test.CreatedAt, Groups: []any{}, DataPools: bindings}); err != nil {
		t.Fatal(err)
	}
	runner.tests[id] = test
	if err := runner.claimTestExecutionFence(test); err != nil {
		t.Fatal(err)
	}
	if err := runner.prepareLiveTables(t.Context(), test); err != nil {
		t.Fatal(err)
	}
	return test
}

func postgresLiveSeed(t *testing.T, store storage.Store) {
	t.Helper()
	if err := store.SaveDataPool(&storage.SharedDataPool{ID: "pg-live-seed", Name: "PostgreSQL live seed", ProjectID: storage.DefaultProjectID, Schema: map[string]string{"value": "string"}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: fmt.Sprintf("seed-%02d", i), PoolID: "pg-live-seed", Status: "available", Data: map[string]string{"value": fmt.Sprintf("private-seed-%02d", i)}}); err != nil {
			t.Fatal(err)
		}
	}
}

func takeOverPostgresLiveRunner(t *testing.T, owner *TestRunner, ownerStore *storage.PostgresStore, successor *TestRunner) executionLeaseTerm {
	t.Helper()
	old, err := owner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	// Revoke the durable term while retaining the predecessor's local lease
	// belief, a deterministic split-brain boundary stronger than an expired
	// local deadline. A stale writer must still be refused by PostgreSQL.
	if err := ownerStore.ReleaseSchedulerLease(executionLeaseName, old.ownerID); err != nil {
		t.Fatal(err)
	}
	successor.leadership.tick()
	term, err := successor.captureExecutionLeaseTerm()
	if err != nil || term.generation <= old.generation {
		t.Fatalf("successor did not acquire a newer term: %+v %v", term, err)
	}
	return term
}

func TestPostgresLiveTableControllerRecoveryCompactionAndAggregateBudget(t *testing.T) {
	dsn := newPostgresScriptAuthoringAPIFixture(t)
	owner, store := newPostgresLiveRunner(t, dsn, "live-owner")
	owner.leadership.tick()
	postgresLiveSeed(t, store)
	var runs []*ManagedTest
	for i, aliases := range []int{2, 2, 1} {
		test := postgresLiveRun(t, owner, store, fmt.Sprintf("pg-run-%d", i), aliases)
		runs = append(runs, test)
		// Discard only the controller cache, as a stopped/crashed predecessor
		// would. Durable tables remain active and must be reconstructed.
		owner.liveTablesMu.Lock()
		delete(owner.liveTables, test.ID)
		owner.liveTablesMu.Unlock()
	}
	term, err := owner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.restoreLiveTables(t.Context(), runs[0], term); err != nil {
		t.Fatal(err)
	}
	id := liveTableID(runs[0].ID, "work0")
	request := livetable.Request{ClientID: "worker", OwnerID: "worker", Operation: livetable.OpPop}
	var last livetable.Result
	for sequence := uint64(1); sequence <= 10; sequence++ {
		request.Sequence = sequence
		last, err = owner.liveClient(runs[0].ID, "generator/workers").Execute(t.Context(), id, request)
		if err != nil || last.Row == nil {
			t.Fatalf("pop %d: %+v %v", sequence, last, err)
		}
	}
	recovery, err := store.ReadLiveTableRecovery(t.Context(), id, runs[0].ProjectID)
	if err != nil || recovery.Table.CheckpointRevision != 8 || len(recovery.Entries) != 2 {
		t.Fatalf("trial did not cross compaction plus journal tail: %+v %v", recovery, err)
	}
	encoded, _ := json.Marshal(recovery)
	if bytes.Contains(encoded, []byte("private-seed")) {
		t.Fatal("PostgreSQL recovery contains plaintext rows")
	}
	successor, secondStore := newPostgresLiveRunner(t, dsn, "live-successor")
	term = takeOverPostgresLiveRunner(t, owner, store, successor)
	if err := successor.restoreLiveTables(t.Context(), runs[0], term); err != nil {
		t.Fatal(err)
	}
	retry, err := successor.liveClient(runs[0].ID, "generator/workers").Execute(t.Context(), id, request)
	if err != nil || retry.Row == nil || retry.Row.ID != last.Row.ID {
		t.Fatalf("receipt lost across controller replacement and compaction: %+v %v", retry, err)
	}
	request.Sequence++
	if _, err := owner.liveClient(runs[0].ID, "generator/workers").Execute(t.Context(), id, request); err == nil {
		t.Fatal("stale controller appended after PostgreSQL ownership changed")
	}
	if got := successor.liveRun(runs[0].ID).tables[id].table.Stats().Counts.Available; got != 6 {
		t.Fatalf("retry/stale owner consumed additional rows: %d", got)
	}
	fencedRecovery, err := secondStore.ReadLiveTableRecovery(t.Context(), id, runs[0].ProjectID)
	if err != nil || fencedRecovery.Table.Revision != 10 {
		t.Fatalf("stale owner changed the durable revision: %+v %v", fencedRecovery, err)
	}
	if err := successor.restoreLiveTables(t.Context(), runs[1], term); err != nil {
		t.Fatal(err)
	}
	if err := successor.restoreLiveTables(t.Context(), runs[2], term); err == nil || !strings.Contains(err.Error(), "project memory budget") {
		t.Fatalf("aggregate recovery admitted more than the project limit: %v", err)
	}
	if successor.liveRun(runs[2].ID) != nil || successor.liveTablesRecoveryReserved != 0 || len(successor.liveTablesRecoveryByProject) != 0 {
		t.Fatal("rejected aggregate recovery leaked partial state or reservation")
	}
	rows, err := secondStore.ListDataPoolRows("pg-live-seed")
	if err != nil || len(rows) != 16 {
		t.Fatalf("live recovery changed the ordinary pool: %d %v", len(rows), err)
	}
}

func TestPostgresLiveTableGeneratorLostReplyPartitionFailsClosed(t *testing.T) {
	dsn := newPostgresScriptAuthoringAPIFixture(t)
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	owner, store := newPostgresLiveRunner(t, dsn, "live-generator-owner")
	owner.leadership.tick()
	postgresLiveSeed(t, store)
	test := postgresLiveRun(t, owner, store, "pg-generator-run", 1)
	term, err := owner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	listener := bufconn.Listen(2 << 20)
	server := grpc.NewServer()
	service := generatorrpc.NewService("pg-generator", 2, generatorrpc.WithExecution(), generatorrpc.WithRunSupervisionRequired(30*time.Second, 0), generatorrpc.WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")))
	pb.RegisterGeneratorControlServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	client, err := generatorrpc.Dial(ctx, "pg-generator", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	claim, err := client.ClaimExecutionEpoch(ctx, term.generation, term.ownerID, 20*time.Second)
	if err != nil || !claim.Accepted {
		t.Fatalf("generator epoch: %+v %v", claim, err)
	}
	var targetRequests atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetRequests.Add(1) }))
	t.Cleanup(target.Close)
	script := &dsl.Script{ID: "pg-live-worker", Name: "Live worker", Protocol: "http", Config: dsl.ScriptConfig{BaseURL: target.URL}, Init: []dsl.Action{{Name: "Pop once", Type: "live_table", Params: map[string]any{"alias": "work0", "operation": "pop", "result_prefix": "item"}}}, Actions: []dsl.Action{{Name: "Target", Type: "GET", Target: "/"}}}
	body, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	token := bytes.Repeat([]byte{42}, generatorrpc.RunSupervisionTokenSize)
	digest := sha256.Sum256(body)
	assigned, err := client.AssignWork(ctx, generatorrpc.Assignment{TestID: test.ID, GroupName: "workers", ExecutionGeneration: term.generation, ScriptID: script.ID, ScriptContent: body, ExpectedScriptHash: hex.EncodeToString(digest[:]), VUserCount: 1, TargetDuration: time.Minute, RunSupervisionToken: token, LiveTableBindingsJSON: owner.liveBindingJSON(test.ID), PrepareOnly: true})
	if err != nil || !assigned.Accepted {
		t.Fatalf("assign generator: %+v %v", assigned, err)
	}
	streamCtx, disconnect := context.WithCancel(ctx)
	defer disconnect()
	committed := make(chan struct{})
	var once sync.Once
	blockedReply := liveTableClientFunc(func(ctx context.Context, id string, request livetable.Request) (livetable.Result, error) {
		result, err := owner.liveClient(test.ID, "pg-generator/workers").Execute(ctx, id, request)
		if err == nil && result.Row != nil {
			once.Do(func() { close(committed) })
		}
		<-ctx.Done() // The mutation commits, but its reply is lost in the partition.
		return result, err
	})
	if err := client.OpenLiveTables(streamCtx, test.ID, "workers", term.generation, token, blockedReply); err != nil {
		t.Fatal(err)
	}
	if result, err := client.ClaimRunSupervision(ctx, generatorrpc.RunSupervisionClaim{TestID: test.ID, ExecutionGeneration: term.generation, Token: token, RequestedTTL: 30 * time.Second, ExecutionLeaseOwnerID: term.ownerID}); err != nil || !result.Accepted {
		t.Fatalf("initial supervision: %+v %v", result, err)
	}
	commit, err := client.CommitWorkSupervisedAt(ctx, test.ID, term.generation, time.Time{}, token)
	if err != nil || !commit.Accepted {
		t.Fatalf("commit generator: %+v %v", commit, err)
	}
	select {
	case <-committed:
	case <-ctx.Done():
		t.Fatal("real generator did not reach the durable table mutation")
	}
	disconnect()
	successor, secondStore := newPostgresLiveRunner(t, dsn, "live-generator-successor")
	newTerm := takeOverPostgresLiveRunner(t, owner, store, successor)
	// This run selected fail-on-controller-loss. Restore its committed output,
	// but claim the new generator epoch without adoption so unknown work stops.
	if err := successor.restoreLiveTables(ctx, test, newTerm); err != nil {
		t.Fatalf("recover failed run output: %v", err)
	}
	claim, err = client.ClaimExecutionEpoch(ctx, newTerm.generation, newTerm.ownerID, 20*time.Second)
	if err != nil || !claim.Accepted {
		t.Fatalf("successor generator fence: %+v %v", claim, err)
	}
	if err := successor.finalizeLiveTables(ctx, test, "FAILED"); err != nil {
		t.Fatalf("retain failed run output: %v", err)
	}
	if err := client.OpenLiveTables(ctx, test.ID, "workers", term.generation, token, owner.liveClient(test.ID, "pg-generator/workers")); err == nil {
		t.Fatal("old-generation generator channel reattached")
	}
	id := liveTableID(test.ID, "work0")
	recovery, err := secondStore.ReadLiveTableRecovery(ctx, id, test.ProjectID)
	if err != nil || recovery.Table.State != "closed" {
		t.Fatalf("lost-reply output was not recoverable: %+v %v", recovery, err)
	}
	run := successor.liveRun(test.ID)
	if !run.closed || run.tables[id].table.Stats().Counts.Available != 15 {
		t.Fatal("partition recovery lost or duplicated the committed consume")
	}
	if targetRequests.Load() != 0 {
		t.Fatal("unknown table outcome allowed target work")
	}
	rows, err := secondStore.ListDataPoolRows("pg-live-seed")
	if err != nil || len(rows) != 16 {
		t.Fatalf("failed continuation changed the source pool: %d %v", len(rows), err)
	}
}

func TestPostgresLiveTableRecoveryReferenceCapacity(t *testing.T) {
	dsn := newPostgresScriptAuthoringAPIFixture(t)
	owner, store := newPostgresLiveRunner(t, dsn, "capacity-owner")
	owner.leadership.tick()
	var columns []string
	values := map[string]string{}
	for i := 0; i < 64; i++ {
		column := fmt.Sprintf("c%02d", i)
		columns = append(columns, column)
		values[column] = strings.Repeat("d", 2048)
	}
	var fixtureRows []livetable.Row
	for i := 0; i < 60; i++ {
		fixtureRows = append(fixtureRows, livetable.Row{ID: fmt.Sprintf("capacity-%d", i), Version: 1, Values: values})
	}
	var runs []*ManagedTest
	var reserved, dataBytes, receiptBytes int64
	var encryptedBytes int
	for i, tableCount := range []int{2, 2, 2, 2, 1} {
		owner.leadership.tick()
		project := fmt.Sprintf("capacity-project-%d", i/2)
		if i%2 == 0 {
			if err := store.SaveProject(&storage.Project{ID: project, Name: project}); err != nil {
				t.Fatal(err)
			}
		}
		var bindings []storage.ScenarioDataPoolBinding
		for alias := 0; alias < tableCount; alias++ {
			bindings = append(bindings, storage.ScenarioDataPoolBinding{Alias: fmt.Sprintf("table%d", alias), AccessMode: "live", LiveOptions: &storage.LiveTableOptions{SeedPolicy: "empty", Columns: columns, Durability: "durable", EndMode: "discard"}})
		}
		bindings, err := normalizeDataPoolBindingIntent(bindings)
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("capacity-run-%d", i)
		test := &ManagedTest{ID: id, Name: id, ProjectID: project, Status: "RUNNING", DataPools: bindings, CreatedAt: time.Now()}
		if err := store.SaveTest(&storage.TestRecord{ID: id, Name: id, ProjectID: project, Status: "RUNNING", StartTime: test.CreatedAt, Groups: []any{}, DataPools: bindings}); err != nil {
			t.Fatal(err)
		}
		owner.tests[id] = test
		if err := owner.claimTestExecutionFence(test); err != nil {
			t.Fatal(err)
		}
		if err := owner.prepareLiveTables(t.Context(), test); err != nil {
			t.Fatal(err)
		}
		run := owner.liveRun(id)
		reserved += run.budget
		for _, rt := range run.tables {
			owner.leadership.tick()
			// Build a valid near-full core fixture, then commit real encrypted
			// transitions and publish through the production checkpoint path.
			// Capacity setup need not wait for receipt-retention windows while
			// slowly growing a real target workload to this same durable state.
			rt.table, err = livetable.New(livetable.Config{ID: rt.record.ID, RunID: id, Profile: livetable.ProfileNative, Columns: columns, Rows: fixtureRows, Limits: liveCoreLimits(rt.binding.LiveOptions), Committer: owner.liveCommitter(rt, run.term)})
			if err != nil {
				t.Fatal(err)
			}
			// Ordinary receipts deliberately leave the control reserve available
			// for lease settlement; twelve near-maximal results fill that budget.
			for sequence := uint64(1); sequence <= 11; sequence++ {
				if _, err := rt.table.Execute(t.Context(), livetable.Request{ClientID: "capacity", Sequence: sequence, Operation: livetable.OpNext}); err != nil {
					t.Fatal(err)
				}
			}
			if err := owner.checkpointLiveTable(t.Context(), run, rt, true); err != nil {
				t.Fatal(err)
			}
			// Preserve a real encrypted journal tail after the full checkpoint.
			if _, err := rt.table.Execute(t.Context(), livetable.Request{ClientID: "capacity", Sequence: 12, Operation: livetable.OpNext}); err != nil {
				t.Fatal(err)
			}
			stats := rt.table.Stats()
			if stats.DataBytes < 7<<20 || stats.ReceiptBytes < 1250<<10 {
				t.Fatalf("capacity fixture too small: %+v", stats)
			}
			dataBytes += stats.DataBytes
			receiptBytes += stats.ReceiptBytes
			recovery, err := store.ReadLiveTableRecovery(t.Context(), rt.record.ID, project)
			if err != nil || len(recovery.Entries) != 1 {
				t.Fatalf("capacity fixture lacks checkpoint plus journal: %+v %v", recovery, err)
			}
			encryptedBytes += len(recovery.Checkpoint) + len(recovery.Entries[0].Payload)
		}
		runs = append(runs, test)
		owner.liveTablesMu.Lock()
		delete(owner.liveTables, id)
		owner.liveTablesMu.Unlock()
	}
	if reserved != 126<<20 || reserved > liveTableControllerBudget {
		t.Fatalf("reference reservation %d does not exercise the controller admission boundary", reserved)
	}
	successor, _ := newPostgresLiveRunner(t, dsn, "capacity-successor")
	term := takeOverPostgresLiveRunner(t, owner, store, successor)
	ctx, cancel := context.WithTimeout(t.Context(), runAdoptionClaimBudget)
	defer cancel()
	stopControl := make(chan struct{})
	controlDone := make(chan time.Duration, 1)
	go func() {
		var longest time.Duration
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopControl:
				controlDone <- longest
				return
			case <-ticker.C:
				started := time.Now()
				successor.mu.Lock()
				successor.mu.Unlock()
				successor.liveTablesMu.Lock()
				successor.liveTablesMu.Unlock()
				longest = max(longest, time.Since(started))
			}
		}
	}()
	var stopOnce sync.Once
	stopProbe := func() { stopOnce.Do(func() { close(stopControl) }) }
	defer stopProbe()
	started := time.Now()
	for _, test := range runs {
		if err := successor.restoreLiveTables(ctx, test, term); err != nil {
			t.Fatalf("aggregate recovery: %v", err)
		}
		for _, rt := range successor.liveRun(test.ID).tables {
			if stats := rt.table.Stats(); stats.Revision != 12 || stats.Counts.Available != 60 {
				t.Fatalf("near-full checkpoint/tail was not restored: %+v", stats)
			}
		}
	}
	elapsed := time.Since(started)
	// A ten-second reference maximum reserves five seconds of the existing
	// shared adoption budget for claims and other control work.
	if elapsed > 10*time.Second {
		t.Fatalf("reference recovery took %s; exceeds 10s data-recovery allowance in shared %s budget", elapsed, runAdoptionClaimBudget)
	}
	// Stop without holding runner locks; a stuck probe must fail within a
	// bounded interval instead of hanging the acceptance test itself.
	stopProbe()
	select {
	case longest := <-controlDone:
		if longest > 250*time.Millisecond {
			t.Fatalf("control lock stalled %s during recovery", longest)
		}
		t.Logf("PostgreSQL reference recovery: tables=9 runs=5 projects=3 reserved=%d data=%d receipts=%d encrypted=%d elapsed=%s longest_control_wait=%s", reserved, dataBytes, receiptBytes, encryptedBytes, elapsed, longest)
	case <-time.After(time.Second):
		t.Fatal("control-plane responsiveness probe did not finish")
	}
}
