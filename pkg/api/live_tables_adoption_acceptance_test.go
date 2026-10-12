//go:build postgres_integration

package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Exercise both production adoption phases, with independent controllers and
// PostgreSQL connections plus a real generator engine. The first controller
// commits a pop but loses its response; only the restored receipt may release
// the blocked VUser after ownership and supervision tokens rotate.
func TestPostgresLiveTableAdoptedGeneratorReplaysCommittedReceipt(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_GENERATOR_ARCHIVE_MIN_FREE_BYTES", "1")
	dsn := newPostgresScriptAuthoringAPIFixture(t)
	owner, store := newPostgresLiveRunner(t, dsn, "live-adoption-owner")
	owner.leadership.tick()
	postgresLiveSeed(t, store)
	test := postgresLiveRun(t, owner, store, "live-adoption-run", 1)
	term, err := owner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	client, _ := newTestGeneratorRPCClient(t, "live-adoption-generator", 2,
		generatorrpc.WithExecution(), generatorrpc.WithRunSupervisionRequired(30*time.Second, 0),
		generatorrpc.WithExecutionEpochStatePath(filepath.Join(t.TempDir(), "epoch.json")))
	claim, err := client.ClaimExecutionEpoch(ctx, term.generation, term.ownerID, 20*time.Second)
	if err != nil || !claim.Accepted {
		t.Fatalf("initial generator epoch: %+v %v", claim, err)
	}
	targetValues := make(chan string, 16)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case targetValues <- r.Header.Get("X-Live-Value"):
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	script := &dsl.Script{ID: "adoption-worker", Name: "Adoption worker", ProjectID: storage.DefaultProjectID, Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: target.URL},
		Init:    []dsl.Action{{Name: "Pop once", Type: "live_table", Params: map[string]any{"alias": "work0", "operation": "pop", "result_prefix": "item"}}},
		Actions: []dsl.Action{{Name: "Target", Type: "GET", Target: "/", Headers: map[string]string{"X-Live-Value": "{{var.item.values.value}}"}}}}
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	token := bytes.Repeat([]byte{61}, generatorrpc.RunSupervisionTokenSize)
	digest := sha256.Sum256(body)
	assigned, err := client.AssignWork(ctx, generatorrpc.Assignment{TestID: test.ID, GroupName: "workers", ExecutionGeneration: term.generation,
		ScriptID: script.ID, ScriptContent: body, ExpectedScriptHash: hex.EncodeToString(digest[:]), VUserCount: 1, TargetDuration: time.Minute, RunSupervisionToken: token,
		AdoptionGrace: defaultRunAdoptionGrace, LiveTableBindingsJSON: owner.liveBindingJSON(test.ID), PrepareOnly: true})
	if err != nil || !assigned.Accepted {
		t.Fatalf("assign generator: %+v %v", assigned, err)
	}
	// Persist the actual immutable policy and adoption proof before generator
	// commit, as launch does. The successor must hydrate these from PostgreSQL.
	test.Assignment = &GeneratorAssignmentSettings{FailoverPolicy: "continue"}
	started := time.Now().UTC()
	groups := encodeStoredGroups([]TestGroup{{Name: "workers", ScriptID: script.ID, Protocol: "http", VUsers: 1, TargetDuration: "1m", script: script, baseScript: script}})
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.ID, ProjectID: test.ProjectID, Status: "RUNNING",
		StartTime: started, StartedAt: &started, Duration: int64(time.Minute), TotalVUsers: 1,
		Groups: groups, Assignment: test.Assignment, DataPools: test.DataPools}); err != nil {
		t.Fatal(err)
	}
	sealed, err := sealRunSupervisionToken(ctx, owner.continuityKeyWrapper, test.ID, term.generation, token)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint, err := json.Marshal(runContinuityCheckpoint{Version: runContinuityCheckpointVersion, ExecutionGeneration: term.generation,
		ExecutionLeaseOwnerID: term.ownerID, RunSupervisionToken: sealed, ArmedAt: started,
		Shards: map[string][]GeneratorShardSummary{"workers": {{Group: "workers", Target: "live-adoption-generator", VUsers: 1, ShardCount: 1}}}})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := store.UpdateTestContinuityCheckpointIfLeaseHeld(test.ID, string(checkpoint), executionLeaseName, term.ownerID); err != nil || result != storage.TestStatusTransitionUpdated {
		t.Fatalf("persist continuity proof: %s %v", result, err)
	}
	setContinuityCheckpointLocked(test, string(checkpoint))
	streamCtx, disconnect := context.WithCancel(ctx)
	defer disconnect()
	type committedPop struct {
		request livetable.Request
		result  livetable.Result
	}
	committed := make(chan committedPop, 1)
	blockedReply := liveTableClientFunc(func(ctx context.Context, id string, request livetable.Request) (livetable.Result, error) {
		result, err := owner.liveClient(test.ID, "live-adoption-generator/workers").Execute(ctx, id, request)
		if err == nil && result.Row != nil {
			select {
			case committed <- committedPop{request, result}:
			default:
			}
		}
		<-ctx.Done()
		return result, err
	})
	if err := client.OpenLiveTables(streamCtx, test.ID, "workers", term.generation, token, blockedReply); err != nil {
		t.Fatal(err)
	}
	if result, err := client.ClaimRunSupervision(ctx, generatorrpc.RunSupervisionClaim{TestID: test.ID, ExecutionGeneration: term.generation, Token: token, RequestedTTL: 30 * time.Second, ExecutionLeaseOwnerID: term.ownerID}); err != nil || !result.Accepted {
		t.Fatalf("initial supervision: %+v %v", result, err)
	}
	if result, err := client.CommitWorkSupervisedAt(ctx, test.ID, term.generation, time.Time{}, token); err != nil || !result.Accepted {
		t.Fatalf("initial commit: %+v %v", result, err)
	}
	var first committedPop
	select {
	case first = <-committed:
	case <-ctx.Done():
		t.Fatal("generator did not commit the first table operation")
	}
	disconnect()

	successor, secondStore := newPostgresLiveRunner(t, dsn, "live-adoption-successor")
	newTerm := takeOverPostgresLiveRunner(t, owner, store, successor)
	id := liveTableID(test.ID, "work0")
	var restoredBeforeClaim atomic.Bool
	backend := newDistributedExecutionBackend([]string{"live-adoption-generator"}, 5*time.Second, func(context.Context, string) (generatorClient, error) {
		run := successor.liveRun(test.ID)
		if run == nil || !run.ready || run.closed || run.term != newTerm || run.tables[id].table.Stats().Counts.Available != 15 {
			return nil, errors.New("generator adoption attempted before complete durable table restoration")
		}
		restoredBeforeClaim.Store(true)
		return client.Retain()
	})
	backend.enableRunSupervision(30*time.Second, 5*time.Second)
	successor.execution = backend
	successor.mu.RLock()
	adopted := successor.tests[test.ID]
	successor.mu.RUnlock()
	if adopted == nil || !successor.isRunAdoptionCandidate(adopted) {
		t.Fatal("successor did not hydrate the durable live run as an adoption candidate")
	}
	if adopted.runtimeHydrationError != "" {
		t.Fatalf("successor did not restore the executable run snapshot: %s", adopted.runtimeHydrationError)
	}
	if err := successor.adoptOrphanedRuns(); err != nil {
		t.Fatalf("Phase A: %v", err)
	}
	successor.mu.RLock()
	resume := adopted.adoptedResume
	rotated := adopted.remoteRunSupervisionToken
	successor.mu.RUnlock()
	if !restoredBeforeClaim.Load() || resume == nil || !resume.secured || resume.finishStatus != "" || bytes.Equal(token, rotated[:]) {
		t.Fatalf("Phase A did not restore then secure the adopted generator: restored=%v resume=%+v", restoredBeforeClaim.Load(), resume)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		// Join normal stop/finalization before helper cleanup closes PostgreSQL;
		// otherwise the live metrics goroutine can outlive its test's database.
		if err := successor.stopTestWithContext(stopCtx, test.ID, false); err != nil {
			t.Errorf("stop adopted test fixture: %v", err)
		}
		if !successor.awaitRunsFinalized(stopCtx, map[string]*ManagedTest{test.ID: adopted}) {
			t.Error("adopted test fixture did not finish before database cleanup")
			_ = successor.teardownActiveTestsAfterLeadershipLoss(stopCtx)
		}
	})
	select {
	case value := <-targetValues:
		t.Fatalf("target work ran before the restored channel was attached: %q", value)
	default:
	}
	successor.resumeAdoptedRuns()
	select {
	case value := <-targetValues:
		if value != first.result.Row.Values["value"] {
			t.Fatalf("resumed VUser used %q; committed receipt held %q", value, first.result.Row.Values["value"])
		}
	case <-ctx.Done():
		t.Fatal("adopted generator did not resume target work from its retained receipt")
	}
	successor.mu.RLock()
	active, status, generation := adopted.remoteActivated, adopted.Status, adopted.executionGeneration
	successor.mu.RUnlock()
	if !active || status != "RUNNING" || generation != newTerm.generation {
		t.Fatalf("Phase B state: active=%v status=%s generation=%d", active, status, generation)
	}
	if stats := successor.liveRun(test.ID).tables[id].table.Stats(); stats.Revision != 1 || stats.Counts.Available != 15 {
		t.Fatalf("retry consumed another row: %+v", stats)
	}
	// A predecessor retaining local lease belief still cannot mutate shared
	// PostgreSQL or reattach the generator's previous generation/token.
	stale := first.request
	stale.Sequence++
	if _, err := owner.liveClient(test.ID, "live-adoption-generator/workers").Execute(ctx, id, stale); err == nil {
		t.Fatal("stale controller appended after adoption")
	}
	if err := client.OpenLiveTables(ctx, test.ID, "workers", term.generation, token, owner.liveClient(test.ID, "live-adoption-generator/workers")); err == nil {
		t.Fatal("previous generator epoch reattached after token rotation")
	}
	recovery, err := secondStore.ReadLiveTableRecovery(ctx, id, test.ProjectID)
	if err != nil || recovery.Table.Revision != 1 || recovery.Table.State != "active" {
		t.Fatalf("adoption/stale mutation changed durable state: %+v %v", recovery, err)
	}
	rows, err := secondStore.ListDataPoolRows("pg-live-seed")
	if err != nil || len(rows) != 16 {
		t.Fatalf("adoption changed the original shared pool: %d %v", len(rows), err)
	}
}
