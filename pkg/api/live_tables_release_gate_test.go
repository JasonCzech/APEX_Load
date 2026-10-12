package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLiveTableContinuationReleaseGatePreservesOrdinaryRuns(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	ordinary := storedContinueRun(t, runner, store, "ordinary", "continue")
	ordinary.DataPools = []storage.ScenarioDataPoolBinding{{PoolID: "legacy", Alias: "legacy", AccessMode: "consume"}}
	if err := validateLiveTableContinuation(ordinary.DataPools, ordinary.Assignment); err != nil || runner.runAdoptionGrace(ordinary) != defaultRunAdoptionGrace {
		t.Fatalf("ordinary continuation changed: %v", err)
	}
	for _, durability := range []string{"memory", ""} {
		t.Run(durability, func(t *testing.T) {
			test := liveIntegrationNewRun(t, runner, store, "blocked-"+durability, []storage.ScenarioDataPoolBinding{{Alias: "work", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{SeedPolicy: "empty", Columns: []string{"value"}, Durability: durability}}})
			test.Assignment = &GeneratorAssignmentSettings{FailoverPolicy: "continue"}
			if err := runner.prepareLiveTables(context.Background(), test); !errors.Is(err, errLiveTableHAContinuationUnavailable) {
				t.Fatalf("memory-only live continuation admitted: %v", err)
			}
			if runner.liveRun(test.ID) != nil || runner.runAdoptionGrace(test) != 0 {
				t.Fatal("blocked run reserved memory or enabled generator adoption grace")
			}
			records, err := store.ListLiveTablesForRun(context.Background(), test.ID, test.ProjectID)
			if err != nil || len(records) != 0 {
				t.Fatalf("blocked launch created durable tables: %+v %v", records, err)
			}
		})
	}
	durable := liveIntegrationNewRun(t, runner, store, "durable-continue", []storage.ScenarioDataPoolBinding{{Alias: "work", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{SeedPolicy: "empty", Columns: []string{"value"}, Durability: "durable"}}})
	durable.Assignment = &GeneratorAssignmentSettings{FailoverPolicy: "continue"}
	if err := runner.prepareLiveTables(context.Background(), durable); err != nil || runner.runAdoptionGrace(durable) != defaultRunAdoptionGrace {
		t.Fatalf("durable continuation was not admitted: %v", err)
	}
	for _, options := range []*storage.LiveTableOptions{nil, {Durability: "memory"}} {
		mixed := append(cloneScenarioDataPoolBindings(durable.DataPools), storage.ScenarioDataPoolBinding{Alias: "memory", AccessMode: "live", LiveOptions: options})
		if err := validateLiveTableContinuation(mixed, durable.Assignment); !errors.Is(err, errLiveTableHAContinuationUnavailable) {
			t.Fatalf("mixed durable/memory continuation admitted: %v", err)
		}
	}
}

func TestLiveTableMemoryContinuationRejectedAtRunCreation(t *testing.T) {
	server, scriptID, _ := seedDataPoolContractServer(t)
	_, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Memory continuation", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: scriptID, VUsers: 1}},
		Assignment: &GeneratorAssignmentSettings{FailoverPolicy: "continue"},
		DataPools:  []storage.ScenarioDataPoolBinding{{Alias: "work", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{SeedPolicy: "empty", Columns: []string{"value"}, Durability: "memory"}}},
	})
	if err == nil || !strings.Contains(err.Error(), errLiveTableHAContinuationUnavailable.Error()) {
		t.Fatalf("creation admitted memory-only live continuation: %v", err)
	}
}

func TestLiveTableDurableContinuationRestoresBeforeServingWork(t *testing.T) {
	runner, store, test := newLiveRuntimeFixture(t, "durable", "write_back")
	liveIntegrationPop(t, runner, test)
	old := runner.liveRun(test.ID)
	test.Assignment = &GeneratorAssignmentSettings{FailoverPolicy: "continue"}
	runner.liveTablesMu.Lock()
	delete(runner.liveTables, test.ID)
	runner.liveTablesMu.Unlock()
	if err := runner.restoreLiveTables(context.Background(), test, old.term); err != nil {
		t.Fatalf("durable continuation failed: %v", err)
	}
	run := runner.liveRun(test.ID)
	id := liveTableID(test.ID, "work")
	if run == nil || !run.ready || run.closed || run.tables[id].table.Stats().Counts.Available != 1 {
		t.Fatal("recovery did not restore the complete active result")
	}
	if _, err := runner.liveClient(test.ID, "generator").Execute(context.Background(), id, livetable.Request{Operation: livetable.OpRead}); err != nil {
		t.Fatalf("restored durable continuation did not serve work: %v", err)
	}
	recovery, err := store.ReadLiveTableRecovery(context.Background(), id, test.ProjectID)
	if err != nil || recovery.Table.State != "active" {
		t.Fatalf("recovered output did not remain active: %+v %v", recovery, err)
	}
	if final := run.tables[id].final; final != nil {
		t.Fatalf("recovery prematurely finalized output: %+v", final)
	}
	liveIntegrationAssertRows(t, store, "live-seed", 3)
}
