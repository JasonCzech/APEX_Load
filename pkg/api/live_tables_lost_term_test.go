package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

// prepareLostTermLiveTables gives the fixture's run one live table that the
// predecessor term prepared and served, so its journal holds an entry. It
// returns the table's ID and the predecessor's client for it.
func prepareLostTermLiveTables(t *testing.T, f *evacuationRollbackFixture, durability, endMode string) (string, livetable.Client) {
	t.Helper()
	ctx := context.Background()
	saveLiveSeedPool(t, f)
	installLostTermBindings(t, f, storage.ScenarioDataPoolBinding{PoolID: "live-seed", Alias: "work", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{Durability: durability, EndMode: endMode}})
	id := liveTableID(f.test.ID, "work")
	client := f.tr.liveClient(f.test.ID, "gen-a/ga")
	if _, err := client.Execute(ctx, id, livetable.Request{ClientID: "worker", OwnerID: "worker", Sequence: 1, Operation: livetable.OpPop}); err != nil {
		t.Fatal(err)
	}
	return id, client
}

func saveLiveSeedPool(t *testing.T, f *evacuationRollbackFixture) {
	t.Helper()
	pool := &storage.SharedDataPool{ID: "live-seed", Name: "Live seed", ProjectID: storage.DefaultProjectID, Schema: map[string]string{"value": "string"}}
	if err := f.store.SaveDataPool(pool); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"row-one", "row-two"} {
		if err := f.store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: id, PoolID: pool.ID, Data: map[string]string{"value": id}, Status: "available"}); err != nil {
			t.Fatal(err)
		}
	}
}

// installLostTermBindings gives the fixture's run the bindings, durably, and
// prepares their live tables under the predecessor's term.
func installLostTermBindings(t *testing.T, f *evacuationRollbackFixture, raw ...storage.ScenarioDataPoolBinding) {
	t.Helper()
	bindings, err := normalizeDataPoolBindingIntent(raw)
	if err != nil {
		t.Fatal(err)
	}
	f.tr.mu.Lock()
	f.test.ProjectID, f.test.DataPools = storage.DefaultProjectID, bindings
	f.tr.mu.Unlock()
	record, err := f.store.GetTest(f.test.ID)
	if err != nil {
		t.Fatal(err)
	}
	record.ProjectID, record.DataPools = storage.DefaultProjectID, bindings
	if err := f.store.SaveTest(record); err != nil {
		t.Fatal(err)
	}
	if err := f.tr.prepareLiveTables(context.Background(), f.test); err != nil {
		t.Fatal(err)
	}
}

// liveTableBudgetInUse is the live-table memory the runner counts.
func (tr *TestRunner) liveTableBudgetInUse() int64 {
	tr.liveTablesMu.Lock()
	defer tr.liveTablesMu.Unlock()
	var total int64
	for _, run := range tr.liveTables {
		total += run.budget
	}
	return total + tr.liveTablesRecoveryReserved
}

func durableLiveTable(t *testing.T, f *evacuationRollbackFixture, id string) (storage.LiveTableRecord, *storage.LiveTableFinalizationResult) {
	t.Helper()
	ctx := context.Background()
	records, err := f.store.ListLiveTablesForRun(ctx, f.test.ID, storage.DefaultProjectID)
	if err != nil || len(records) != 1 || records[0].ID != id {
		t.Fatalf("durable live tables = %+v, %v", records, err)
	}
	final, err := f.store.GetLiveTableFinalization(ctx, id, storage.DefaultProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return records[0], nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return records[0], final
}

// assertLostTablesReleased checks the lost term's tables left memory and the
// budget, can serve nothing, and left storage as that term wrote it.
func assertLostTablesReleased(t *testing.T, f *evacuationRollbackFixture, lost *runLiveTables, client livetable.Client, id string) {
	t.Helper()
	if current := f.tr.liveRun(f.test.ID); current != nil {
		t.Fatalf("live tables still registered after the lease was won again: term %+v", current.term)
	}
	if budget := f.tr.liveTableBudgetInUse(); budget != 0 {
		t.Fatalf("live-table budget in use = %d, want 0", budget)
	}
	lost.mu.RLock()
	closed := lost.closed
	lost.mu.RUnlock()
	if !closed {
		t.Fatal("the lost term's tables are still open")
	}
	if _, err := client.Execute(context.Background(), id, livetable.Request{ClientID: "worker", OwnerID: "worker", Sequence: 2, Operation: livetable.OpPop}); err == nil {
		t.Fatal("the lost term's client still served the table")
	}
	record, final := durableLiveTable(t, f, id)
	if record.State != "active" || record.OwnerID != f.oldOwner || record.OwnerGeneration != f.oldGen || record.Revision == 0 || final != nil {
		t.Fatalf("durable table after the release = %+v, finalization %+v", record, final)
	}
}

// assertFinalizedUnderTheNewTerm checks the new term's terminal write claimed
// the table and finalized it durably, leaving no retry.
func assertFinalizedUnderTheNewTerm(t *testing.T, f *evacuationRollbackFixture, id string) {
	t.Helper()
	term := f.term()
	record, final := durableLiveTable(t, f, id)
	if record.State != "closed" || record.OwnerID != term.ownerID || record.OwnerGeneration != term.generation || final == nil || final.Mode != "discard" {
		t.Fatalf("durable table after the terminal write = %+v, finalization %+v", record, final)
	}
	restored := f.tr.liveRun(f.test.ID)
	if restored == nil || restored.term != term {
		t.Fatal("the finalized tables are not the new term's")
	}
	restored.mu.RLock()
	closed := restored.closed
	restored.mu.RUnlock()
	if !closed {
		t.Fatal("the finalized tables are still open")
	}
	f.awaitLiveTableRetries(t)
}

// A run the new term holds fail-closed keeps no live tables of the lost term
// in memory: the new term releases them as it acquires the lease, and its
// terminal writer then restores and finalizes them from storage. A lost-term
// writer writes nothing.
func TestLostTermLiveTablesOfAHeldRun(t *testing.T) {
	for _, resolve := range []string{"attestation", "fence recovery"} {
		t.Run(resolve, func(t *testing.T) {
			// A version 4 source_fenced journal is no candidate: it is held.
			var id string
			var client livetable.Client
			f := newEvacuationRollForwardFixture(t, rollForwardOptions{version: 4, phase: runEvacuationPhaseSourceFenced}, func(f *evacuationRollbackFixture) {
				id, client = prepareLostTermLiveTables(t, f, "durable", "discard")
			})
			storeReloadableRun(t, f)
			lost := f.tr.liveRun(f.test.ID)
			if lost == nil || f.tr.liveTableBudgetInUse() == 0 {
				t.Fatal("the predecessor's tables are not registered")
			}
			f.tr.mu.Lock()
			f.test.executionFence = f.oldOwner
			f.tr.mu.Unlock()
			lostRun := f.test
			if err := f.tr.reloadAndReconcile(); err != nil {
				t.Fatal(err)
			}
			current := f.reloadedRun(t)
			if current == lostRun || current.Status != "STOPPING" {
				t.Fatalf("held run after the reload: same object %v, status %s", current == lostRun, current.Status)
			}
			f.test = current
			assertLostTablesReleased(t, f, lost, client, id)

			// The lost term's writer holds the old object and fence.
			if err := f.tr.completeTestRun(lostRun, "COMPLETE", time.Now().UTC(), "", nil); err != nil {
				t.Fatal(err)
			}
			if record, _ := durableLiveTable(t, f, id); record.State != "active" || record.OwnerID != f.oldOwner || f.liveTableRetrying() {
				t.Fatalf("a lost-term writer touched the table: %+v", record)
			}
			if row, err := f.store.GetTest(f.test.ID); err != nil || row.Status != "STOPPING" {
				t.Fatalf("a lost-term writer changed the run's row: %+v, %v", row, err)
			}
			// The lost object, unregistered, is no writer even with the
			// current term's fence.
			newTerm := f.term()
			f.tr.mu.Lock()
			lostFence := lostRun.executionFence
			lostRun.executionFence = newTerm.ownerID
			f.tr.mu.Unlock()
			f.tr.finishLiveTables(lostRun, "FAILED")
			f.tr.mu.Lock()
			lostRun.executionFence = lostFence
			f.tr.mu.Unlock()
			if record, _ := durableLiveTable(t, f, id); record.State != "active" || record.OwnerID != f.oldOwner || f.liveTableRetrying() || f.tr.liveRun(f.test.ID) != nil {
				t.Fatalf("an unregistered object with the current fence touched the table: %+v", record)
			}
			// The registered object with the lost term's fence is no
			// current writer either.
			f.tr.mu.Lock()
			fence := current.executionFence
			current.executionFence = f.oldOwner
			f.tr.mu.Unlock()
			f.tr.finishLiveTables(current, "FAILED")
			f.tr.mu.Lock()
			current.executionFence = fence
			f.tr.mu.Unlock()
			if record, _ := durableLiveTable(t, f, id); record.State != "active" || record.OwnerID != f.oldOwner || f.liveTableRetrying() || f.tr.liveRun(f.test.ID) != nil {
				t.Fatalf("a writer holding the lost term's fence touched the table: %+v", record)
			}
			if resolve == "attestation" {
				f.attest(t, current)
			} else {
				markEvacuationRecoveryRunStarted(t, f.store, current)
				if err := f.tr.recoverInterruptedRunEvacuation(current); err != nil {
					t.Fatal(err)
				}
				if record, err := f.store.GetTest(f.test.ID); err != nil || record.Status != "FAILED" || !strings.HasPrefix(record.Error, interruptedEvacuationFenceProof) {
					t.Fatalf("fence recovery = %+v, %v", record, err)
				}
			}
			assertFinalizedUnderTheNewTerm(t, f, id)
		})
	}
}

// A run whose checkpoint the new term cannot use is held before Phase A
// restores its tables: they are released and finalized by the attestation.
func TestLostTermLiveTablesOfAnUndecodableCheckpoint(t *testing.T) {
	var id string
	var client livetable.Client
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{}, func(f *evacuationRollbackFixture) {
		id, client = prepareLostTermLiveTables(t, f, "durable", "discard")
		var guard runEvacuationCheckpoint
		if err := json.Unmarshal([]byte(f.guard), &guard); err != nil {
			t.Fatal(err)
		}
		guard.Prior.PinnedSecretCiphertext = "not-a-sealed-secret"
		raw := encodeEvacuationGuard(t, guard)
		if err := f.tr.swapRunContinuityCheckpoint(f.test, f.guard, raw, f.oldOwner); err != nil {
			t.Fatal(err)
		}
		f.guard = raw
	})
	lost := f.tr.liveRun(f.test.ID)
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	record, err := f.store.GetTest(f.test.ID)
	if err != nil || record.Status != "STOPPING" || !strings.Contains(record.Error, "could not use the evacuation journal's next checkpoint") {
		t.Fatalf("undecodable checkpoint = %+v, %v", record, err)
	}
	if claims, _ := f.spareGen.snapshot(); len(claims) != 0 {
		t.Fatalf("a generator was claimed without a usable checkpoint: %+v", claims)
	}
	assertLostTablesReleased(t, f, lost, client, id)
	f.attest(t, f.test)
	assertFinalizedUnderTheNewTerm(t, f, id)
}

// An adoption candidate restores its tables under the new term; the lost
// term's are released first and never counted twice.
func TestLostTermLiveTablesOfAnAdoptedRun(t *testing.T) {
	var id string
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{}, func(f *evacuationRollbackFixture) {
		id, _ = prepareLostTermLiveTables(t, f, "durable", "discard")
	})
	lost := f.tr.liveRun(f.test.ID)
	budget := f.tr.liveTableBudgetInUse()
	if err := f.tr.adoptOrphanedRuns(); err != nil {
		t.Fatal(err)
	}
	if f.test.adoptedResume == nil || f.test.adoptedResume.finishStatus != "" {
		t.Fatalf("the run was not rolled forward: %+v", f.test.adoptedResume)
	}
	restored := f.tr.liveRun(f.test.ID)
	if restored == nil || restored == lost || restored.term != f.term() {
		t.Fatal("the run's tables were not restored under the new term")
	}
	if got := f.tr.liveTableBudgetInUse(); got != budget {
		t.Fatalf("live-table budget in use = %d, want %d", got, budget)
	}
	if record, _ := durableLiveTable(t, f, id); record.State != "active" || record.OwnerID != f.term().ownerID {
		t.Fatalf("restored table = %+v", record)
	}
}

// A table kept only in memory left nothing in storage to finalize: the new
// term releases it, its record stays as its term left it, and the terminal
// writer leaves no retry behind.
func TestLostTermMemoryLiveTablesAreOnlyReleased(t *testing.T) {
	var id string
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{version: 4, phase: runEvacuationPhaseSourceFenced}, func(f *evacuationRollbackFixture) {
		// Only a run that fails on controller loss may keep tables in memory.
		f.test.Assignment.FailoverPolicy = "fail"
		id, _ = prepareLostTermLiveTables(t, f, "memory", "discard")
	})
	storeReloadableRun(t, f)
	f.tr.mu.Lock()
	f.test.executionFence = f.oldOwner
	f.tr.mu.Unlock()
	// Without run adoption Phase A does nothing; the acquisition still
	// releases the lost term's tables.
	f.tr.continuityKeyWrapper = nil
	if err := f.tr.reloadAndReconcile(); err != nil {
		t.Fatal(err)
	}
	if f.tr.liveRun(f.test.ID) != nil || f.tr.liveTableBudgetInUse() != 0 {
		t.Fatal("the lost term's memory table is still counted")
	}
	f.test = f.reloadedRun(t)
	if f.test.Status != "STOPPING" {
		t.Fatalf("held run status = %s", f.test.Status)
	}
	f.attest(t, f.test)
	record, final := durableLiveTable(t, f, id)
	if record.State != "active" || record.OwnerID != f.oldOwner || final != nil {
		t.Fatalf("memory table record = %+v, finalization %+v", record, final)
	}
	if f.liveTableRetrying() || f.tr.liveRun(f.test.ID) != nil {
		t.Fatal("a memory table was restored or its finalization retried")
	}
}

// The release keeps the current term's tables and summaries released after
// their finalization, and drops only tables of another term.
func TestReleaseLostTermLiveTablesKeepsTheCurrentTerm(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{})
	term := f.term()
	put := func(id string, run *runLiveTables) {
		f.tr.liveTablesMu.Lock()
		if f.tr.liveTables == nil {
			f.tr.liveTables = make(map[string]*runLiveTables)
		}
		run.testID = id
		f.tr.liveTables[id] = run
		f.tr.liveTablesMu.Unlock()
	}
	lostTerm := executionLeaseTerm{ownerID: f.oldOwner, generation: f.oldGen}
	put("lost", &runLiveTables{term: lostTerm, budget: 7, ready: true})
	put("current", &runLiveTables{term: term, budget: 11, ready: true})
	put("summary", &runLiveTables{term: lostTerm, releasedAt: time.Now()})
	if released := f.tr.releaseLostTermLiveTables(term); len(released) != 1 || released[0] != "lost" {
		t.Fatalf("released = %v", released)
	}
	if f.tr.liveRun("lost") != nil || f.tr.liveRun("current") == nil || f.tr.liveRun("summary") == nil {
		t.Fatal("the release dropped the wrong tables")
	}
	if got := f.tr.liveTableBudgetInUse(); got != 11 {
		t.Fatalf("budget in use = %d, want 11", got)
	}
}

// The lost term closed its table and was lost before recording the
// finalization. The new term restores the closed table and its terminal
// writer finalizes it, leaving no retry behind.
func TestLostTermLiveTablesClosedBeforeTheirFinalization(t *testing.T) {
	var id string
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{version: 4, phase: runEvacuationPhaseSourceFenced}, func(f *evacuationRollbackFixture) {
		id, _ = prepareLostTermLiveTables(t, f, "durable", "discard")
		closeWithoutFinalization(t, f, id)
	})
	f.tr.releaseLostTermLiveTables(f.term())
	if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
		t.Fatal(err)
	}
	if record, final := durableLiveTable(t, f, id); record.State != "closed" || final != nil {
		t.Fatalf("lost term's closing table = %+v, finalization %+v", record, final)
	}
	f.attest(t, f.test)
	if f.liveTableRetrying() {
		t.Fatal("a closed table the terminal writer finalized is retried")
	}
	assertFinalizedUnderTheNewTerm(t, f, id)
}

// failingLiveTableStore refuses to list live tables while failing is set, and
// refuses as many finalizations as failFinalize counts.
type failingLiveTableStore struct {
	*storage.SQLiteStore
	failing      atomic.Bool
	failFinalize atomic.Int32
	finalized    atomic.Int32
}

func (s *failingLiveTableStore) ListLiveTablesForRun(ctx context.Context, testID, projectID string) ([]storage.LiveTableRecord, error) {
	if s.failing.Load() {
		return nil, errors.New("live table storage unavailable")
	}
	return s.SQLiteStore.ListLiveTablesForRun(ctx, testID, projectID)
}

func (s *failingLiveTableStore) FinalizeLiveTable(ctx context.Context, request storage.LiveTableFinalizationRequest, term storage.LiveTableExecutionTerm) (*storage.LiveTableFinalizationResult, error) {
	if s.failFinalize.Add(-1) >= 0 {
		return nil, errors.New("live table finalization unavailable")
	}
	s.finalized.Add(1)
	return s.SQLiteStore.FinalizeLiveTable(ctx, request, term)
}

// A failed finalization of unrestored tables is retried under the term that
// tried it, and the retry ends once that term ended, without writing.
func TestUnrestoredLiveTableRetryEndsWithItsTerm(t *testing.T) {
	var id string
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{version: 4, phase: runEvacuationPhaseSourceFenced}, func(f *evacuationRollbackFixture) {
		id, _ = prepareLostTermLiveTables(t, f, "durable", "discard")
	})
	f.tr.releaseLostTermLiveTables(f.term())
	if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
		t.Fatal(err)
	}
	failing := &failingLiveTableStore{SQLiteStore: f.store}
	failing.failing.Store(true)
	f.tr.dataPoolStore = failing
	f.tr.mu.Lock()
	if err := f.tr.claimTestExecutionFence(f.test); err != nil {
		f.tr.mu.Unlock()
		t.Fatal(err)
	}
	f.tr.mu.Unlock()
	f.tr.finishLiveTables(f.test, "FAILED")
	if !f.liveTableRetrying() {
		t.Fatal("a failed finalization of unrestored tables was not retried")
	}
	// The same leadership object loses the lease and wins it again, under a
	// newer term, while the retry runs; storage recovers.
	ended, _ := loseAndWinAgain(t, f)
	failing.failing.Store(false)
	f.awaitLiveTableRetries(t)
	if failing.finalized.Load() != 0 {
		t.Fatal("an ended term's retry finalized the table")
	}
	// Whenever an attempt of the ended term runs, it gives up without
	// touching storage.
	if err := f.tr.unrestoredLiveTableFinalizer(f.test, ended, "FAILED")(); err != nil {
		t.Fatalf("an attempt of an ended term = %v, want it to give up", err)
	}
	record, final := durableLiveTable(t, f, id)
	if record.State != "active" || record.OwnerID != f.oldOwner || final != nil || f.tr.liveRun(f.test.ID) != nil {
		t.Fatalf("an ended term's retry wrote the table: %+v, finalization %+v (ended term %+v)", record, final, ended)
	}
}

// Tables the lost term finalized completely leave nothing to do: the new
// term's terminal writer neither restores nor claims them.
func TestLostTermLiveTablesAlreadyFinalized(t *testing.T) {
	var id string
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{version: 4, phase: runEvacuationPhaseSourceFenced}, func(f *evacuationRollbackFixture) {
		id, _ = prepareLostTermLiveTables(t, f, "durable", "discard")
		if err := f.tr.finalizeLiveTables(context.Background(), f.test, "FAILED"); err != nil {
			t.Fatal(err)
		}
	})
	before, final := durableLiveTable(t, f, id)
	if before.State != "closed" || final == nil {
		t.Fatalf("the lost term's finalization = %+v, %+v", before, final)
	}
	f.tr.releaseLostTermLiveTables(f.term())
	if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
		t.Fatal(err)
	}
	f.attest(t, f.test)
	if f.tr.liveRun(f.test.ID) != nil || f.liveTableRetrying() {
		t.Fatal("finalized tables were restored again")
	}
	if after, _ := durableLiveTable(t, f, id); after.OwnerID != before.OwnerID || after.OwnerGeneration != before.OwnerGeneration {
		t.Fatalf("finalized tables were claimed again: %+v", after)
	}
}

// A held run's tables whose first finalization attempt fails before the
// restore, and whose finalization then fails once on the retry: every failure
// returns to the one retry, which ends with the table finalized under the new
// term. The lost term had closed the table without finalizing it.
func TestUnrestoredLiveTableFinalizationFailureInsideTheRetry(t *testing.T) {
	var id string
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{version: 4, phase: runEvacuationPhaseSourceFenced}, func(f *evacuationRollbackFixture) {
		id, _ = prepareLostTermLiveTables(t, f, "durable", "discard")
		closeWithoutFinalization(t, f, id)
	})
	f.tr.releaseLostTermLiveTables(f.term())
	if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
		t.Fatal(err)
	}
	failing := &failingLiveTableStore{SQLiteStore: f.store}
	failing.failing.Store(true)
	failing.failFinalize.Store(1)
	f.tr.dataPoolStore = failing
	f.tr.mu.Lock()
	if err := f.tr.claimTestExecutionFence(f.test); err != nil {
		f.tr.mu.Unlock()
		t.Fatal(err)
	}
	f.tr.mu.Unlock()
	f.tr.finishLiveTables(f.test, "FAILED")
	if !f.liveTableRetrying() {
		t.Fatal("a finalization that failed before the restore was not retried")
	}
	failing.failing.Store(false)
	f.awaitLiveTableRetries(t)
	if failing.finalized.Load() != 1 || failing.failFinalize.Load() >= 0 {
		t.Fatalf("finalizations = %d after a refused one (left to refuse %d)", failing.finalized.Load(), failing.failFinalize.Load())
	}
	assertFinalizedUnderTheNewTerm(t, f, id)
}

// closeWithoutFinalization closes the table as its term's finalization does,
// and stops before the finalization result is recorded.
func closeWithoutFinalization(t *testing.T, f *evacuationRollbackFixture, id string) {
	t.Helper()
	ctx := context.Background()
	run := f.tr.liveRun(f.test.ID)
	rt := run.tables[id]
	if err := rt.table.Close(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := f.tr.checkpointLiveTable(ctx, run, rt, true); err != nil {
		t.Fatal(err)
	}
	if err := f.store.CloseLiveTable(ctx, id, rt.table.Stats().Revision, liveStorageTerm(run.term)); err != nil {
		t.Fatal(err)
	}
}

// A fail-policy run may keep a table only in memory beside a durable one. The
// memory table cannot be finalized, but it does not keep the durable one from
// being finalized under the new term.
func TestLostTermDurableTableBesideAMemoryTable(t *testing.T) {
	ctx := context.Background()
	var durable, memory string
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{version: 4, phase: runEvacuationPhaseSourceFenced}, func(f *evacuationRollbackFixture) {
		f.test.Assignment.FailoverPolicy = "fail"
		saveLiveSeedPool(t, f)
		installLostTermBindings(t, f,
			storage.ScenarioDataPoolBinding{PoolID: "live-seed", Alias: "work", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{Durability: "durable", EndMode: "discard"}},
			storage.ScenarioDataPoolBinding{PoolID: "live-seed", Alias: "scratch", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{Durability: "memory", EndMode: "discard"}})
		durable, memory = liveTableID(f.test.ID, "work"), liveTableID(f.test.ID, "scratch")
		if _, err := f.tr.liveClient(f.test.ID, "gen-a/ga").Execute(ctx, durable, livetable.Request{ClientID: "worker", OwnerID: "worker", Sequence: 1, Operation: livetable.OpPop}); err != nil {
			t.Fatal(err)
		}
	})
	f.tr.releaseLostTermLiveTables(f.term())
	if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
		t.Fatal(err)
	}
	f.attest(t, f.test)
	f.awaitLiveTableRetries(t)
	term := f.term()
	records, err := f.store.ListLiveTablesForRun(ctx, f.test.ID, storage.DefaultProjectID)
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %+v, %v", records, err)
	}
	for _, record := range records {
		final, finalErr := f.store.GetLiveTableFinalization(ctx, record.ID, storage.DefaultProjectID)
		switch record.ID {
		case durable:
			if record.State != "closed" || record.OwnerID != term.ownerID || finalErr != nil || final == nil {
				t.Fatalf("durable table = %+v, finalization %+v, %v", record, final, finalErr)
			}
		case memory:
			if record.State != "active" || record.OwnerID != f.oldOwner || !errors.Is(finalErr, sql.ErrNoRows) {
				t.Fatalf("memory table = %+v, finalization %+v, %v", record, final, finalErr)
			}
		}
	}
	// The run view and the archive summary still show the memory table, as
	// unavailable, beside the finalized durable one.
	summaries := f.tr.liveTableArchiveSummaries(f.test.ID)
	states := map[string]string{}
	for _, summary := range summaries {
		states[summary.TableID] = summary.State
	}
	if len(summaries) != 2 || states[durable] != "closed" || states[memory] != "unavailable" {
		t.Fatalf("archive summaries = %+v", summaries)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.SetPathValue("id", f.test.ID)
	request = request.WithContext(context.WithValue(request.Context(), authorizedProjectContextKey{}, storage.DefaultProjectID))
	response := httptest.NewRecorder()
	(&Server{runner: f.tr}).handleListLiveTables(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `"id":"`+memory+`","alias":"scratch"`) || !strings.Contains(body, `"durability":"memory","state":"unavailable"`) || !strings.Contains(body, `"id":"`+durable+`"`) {
		t.Fatalf("run view = %d %s", response.Code, body)
	}
}

// A held run bound only to a project table has no run tables. Its active
// attachment is detached under the new term when the run ends, so the
// project table is not kept from quiescing.
func TestLostTermProjectTableAttachmentIsDetached(t *testing.T) {
	ctx := context.Background()
	var tableID string
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{version: 4, phase: runEvacuationPhaseSourceFenced}, func(f *evacuationRollbackFixture) {
		definition, err := f.tr.projectLiveTableService().Create(ctx, ProjectLiveTableCreateInput{ProjectID: storage.DefaultProjectID, Name: "Shared orders", Config: storage.ProjectLiveTableConfig{Columns: []string{"value"}}, ActorSubject: "dev-mode"})
		if err != nil {
			t.Fatal(err)
		}
		tableID = definition.ID
		f.test.CreatedBySubject = "dev-mode"
		f.test.Groups[0].script = &dsl.Script{Actions: []dsl.Action{{Type: "live_table", Params: map[string]any{"alias": "orders", "operation": "pop"}}}}
		installLostTermBindings(t, f, storage.ScenarioDataPoolBinding{Alias: "orders", Scope: "project", ProjectTableID: definition.ID, AccessMode: "live"})
	})
	attachments, err := f.store.ListProjectLiveTableRunAttachments(ctx, storage.DefaultProjectID, f.test.ID)
	if err != nil || len(attachments) != 1 || attachments[0].State != "active" {
		t.Fatalf("the lost term's attachment = %+v, %v", attachments, err)
	}
	f.tr.releaseLostTermLiveTables(f.term())
	if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
		t.Fatal(err)
	}
	f.attest(t, f.test)
	f.awaitLiveTableRetries(t)
	attachments, err = f.store.ListProjectLiveTableRunAttachments(ctx, storage.DefaultProjectID, f.test.ID)
	if err != nil || len(attachments) != 1 || attachments[0].State != "detached" || attachments[0].SettledRevision == nil || attachments[0].TableID != tableID {
		t.Fatalf("the attachment after the run ended = %+v, %v", attachments, err)
	}
}

// A restore that fails for a reason no retry can change (here a stored table
// no binding of the run names) leaves no retry behind and the tables as
// storage has them.
func TestUnrecoverableUnrestoredLiveTablesAreNotRetried(t *testing.T) {
	var id string
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{version: 4, phase: runEvacuationPhaseSourceFenced}, func(f *evacuationRollbackFixture) {
		saveLiveSeedPool(t, f)
		installLostTermBindings(t, f,
			storage.ScenarioDataPoolBinding{PoolID: "live-seed", Alias: "work", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{Durability: "durable", EndMode: "discard"}},
			storage.ScenarioDataPoolBinding{PoolID: "live-seed", Alias: "other", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{Durability: "durable", EndMode: "discard"}})
		id = liveTableID(f.test.ID, "work")
	})
	f.tr.releaseLostTermLiveTables(f.term())
	if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
		t.Fatal(err)
	}
	f.tr.mu.Lock()
	f.test.DataPools = f.test.DataPools[:1]
	f.tr.mu.Unlock()
	f.attest(t, f.test)
	if f.liveTableRetrying() || f.tr.liveRun(f.test.ID) != nil {
		t.Fatal("an unrecoverable restore is retried")
	}
	records, err := f.store.ListLiveTablesForRun(context.Background(), f.test.ID, storage.DefaultProjectID)
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %+v, %v", records, err)
	}
	for _, record := range records {
		if record.State != "active" || record.OwnerID != f.oldOwner {
			t.Fatalf("table after an unrecoverable restore = %+v (work is %s)", record, id)
		}
	}
}

// A binding whose table storage never initialized (the lost term was lost
// while it launched the run) does not keep the tables that exist from being
// finalized.
func TestUnrestoredLiveTablesWithAnIncompleteManifest(t *testing.T) {
	var id string
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{version: 4, phase: runEvacuationPhaseSourceFenced}, func(f *evacuationRollbackFixture) {
		id, _ = prepareLostTermLiveTables(t, f, "durable", "discard")
	})
	f.tr.releaseLostTermLiveTables(f.term())
	if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
		t.Fatal(err)
	}
	missing, err := normalizeDataPoolBindingIntent([]storage.ScenarioDataPoolBinding{{PoolID: "live-seed", Alias: "missing", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{Durability: "durable", EndMode: "discard"}}})
	if err != nil {
		t.Fatal(err)
	}
	f.tr.mu.Lock()
	f.test.DataPools = append(f.test.DataPools, missing...)
	f.tr.mu.Unlock()
	f.attest(t, f.test)
	assertFinalizedUnderTheNewTerm(t, f, id)
}

// loseAndWinAgain makes the fixture's own leadership object lose the lease
// and win it again under a newer term, as a process does, and returns both
// terms. The loss is handled asynchronously, so it waits for the new term.
func loseAndWinAgain(t *testing.T, f *evacuationRollbackFixture) (executionLeaseTerm, executionLeaseTerm) {
	t.Helper()
	ended := f.term()
	leadership := f.tr.leadership
	leadership.mu.Lock()
	leadership.localLeaseDeadline = time.Now().Add(-time.Second)
	leadership.mu.Unlock()
	if owner, _ := leadership.executionLeaseTerm(); owner != "" {
		t.Fatalf("the lease was not lost: %s", owner)
	}
	if err := f.store.ReleaseSchedulerLease(executionLeaseName, ended.ownerID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		leadership.mu.Lock()
		leadership.acquireNotBefore = time.Time{}
		leadership.mu.Unlock()
		leadership.tick()
		if current := f.term(); current.ownerID != "" && current != ended {
			return ended, current
		}
		if time.Now().After(deadline) {
			t.Fatalf("the lease was not won again under a newer term: %+v", f.term())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A restore whose term ends after its reads, while a newer term of the same
// process restores the run's tables, does not register its own over them.
func TestUnrestoredLiveTableRestoreOfAnEndedTermIsNotRegistered(t *testing.T) {
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{version: 4, phase: runEvacuationPhaseSourceFenced}, func(f *evacuationRollbackFixture) {
		prepareLostTermLiveTables(t, f, "durable", "discard")
	})
	f.tr.releaseLostTermLiveTables(f.term())
	if err := f.tr.reconcileStoredActiveTest(f.test); err != nil {
		t.Fatal(err)
	}
	ended := f.term()
	var newer *runLiveTables
	f.tr.beforeLiveTableRestoreRegistration = func() {
		f.tr.beforeLiveTableRestoreRegistration = nil
		_, current := loseAndWinAgain(t, f)
		newer = f.registerLiveTables(current)
		newer.budget = 7
	}
	defer func() { f.tr.beforeLiveTableRestoreRegistration = nil }()
	err := f.tr.unrestoredLiveTableFinalizer(f.test, ended, "FAILED")()
	if err == nil || !strings.Contains(err.Error(), "owner expired") {
		t.Fatalf("a restore of an ended term = %v, want it refused", err)
	}
	if newer == nil || f.tr.liveRun(f.test.ID) != newer {
		t.Fatal("an ended term's restore replaced the newer term's tables")
	}
	if budget := f.tr.liveTableBudgetInUse(); budget != 7 {
		t.Fatalf("live-table budget in use = %d, want only the newer term's 7", budget)
	}
	// The next attempt of the ended term gives up.
	if err := f.tr.unrestoredLiveTableFinalizer(f.test, ended, "FAILED")(); err != nil {
		t.Fatalf("a later attempt of the ended term = %v", err)
	}
}

// Tables restored only to be finalized never serve, even before their
// finalization runs.
func TestFinalizeOnlyRestoreNeverServes(t *testing.T) {
	var id string
	f := newEvacuationRollForwardFixture(t, rollForwardOptions{version: 4, phase: runEvacuationPhaseSourceFenced}, func(f *evacuationRollbackFixture) {
		id, _ = prepareLostTermLiveTables(t, f, "durable", "discard")
	})
	f.tr.releaseLostTermLiveTables(f.term())
	ctx := context.Background()
	if err := f.tr.restoreRunLiveTables(ctx, f.test, f.term(), true); err != nil {
		t.Fatal(err)
	}
	restored := f.tr.liveRun(f.test.ID)
	if restored == nil || restored.term != f.term() {
		t.Fatal("the finalize-only restore registered nothing")
	}
	if _, err := f.tr.liveClient(f.test.ID, "gen-a/ga").Execute(ctx, id, livetable.Request{ClientID: "worker", OwnerID: "worker", Sequence: 2, Operation: livetable.OpPop}); err == nil {
		t.Fatal("a table restored only to be finalized served a request")
	}
}
