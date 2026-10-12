package api

import (
	"context"
	"errors"
	"testing"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLiveTableInterruptedFinalizationRestoresFrozenRun(t *testing.T) {
	for _, stage := range []string{"closed_checkpoint", "closed_record", "committed_output"} {
		t.Run(stage, func(t *testing.T) {
			runner, store, _ := newLiveRuntimeFixture(t, "memory", "discard")
			firstOptions := &storage.LiveTableOptions{Durability: "durable", EndMode: "save_as_new", OutputPoolName: "First output"}
			secondOptions := &storage.LiveTableOptions{Durability: "durable", EndMode: "write_back"}
			test := liveIntegrationNewRun(t, runner, store, "interrupted-finalization", []storage.ScenarioDataPoolBinding{
				{PoolID: "live-seed", Alias: "first", AccessMode: "live", LiveOptions: firstOptions},
				{PoolID: "live-seed", Alias: "second", AccessMode: "live", LiveOptions: secondOptions},
			})
			ctx := context.Background()
			if err := runner.prepareLiveTables(ctx, test); err != nil {
				t.Fatal(err)
			}
			firstID, secondID := liveTableID(test.ID, "first"), liveTableID(test.ID, "second")
			if _, err := runner.liveClient(test.ID, "worker").Execute(ctx, secondID, livetable.Request{ClientID: "worker", Sequence: 1, Operation: livetable.OpPop}); err != nil {
				t.Fatal(err)
			}
			run := runner.liveRun(test.ID)
			first := run.tables[firstID]
			if err := first.table.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if err := runner.checkpointLiveTable(ctx, run, first, true); err != nil {
				t.Fatal(err)
			}
			if stage != "closed_checkpoint" {
				if err := store.CloseLiveTable(ctx, firstID, first.table.Stats().Revision, liveStorageTerm(run.term)); err != nil {
					t.Fatal(err)
				}
			}
			var committed *storage.LiveTableFinalizationResult
			if stage == "committed_output" {
				rows, _, err := liveTableExportRows(first.table, first.table.Stats().Revision)
				if err != nil {
					t.Fatal(err)
				}
				request := storage.LiveTableFinalizationRequest{TableID: firstID, ProjectID: test.ProjectID, Revision: first.table.Stats().Revision, Mode: "save_as_new", OutputPoolName: "First output", Schema: map[string]string{"value": "string"}}
				for _, row := range rows {
					request.Rows = append(request.Rows, &storage.SharedDataPoolRow{ID: row.ID, Data: row.Values, Status: "available"})
				}
				committed, err = store.FinalizeLiveTable(ctx, request, liveStorageTerm(run.term))
				if err != nil {
					t.Fatal(err)
				}
			}
			runner.liveTablesMu.Lock()
			delete(runner.liveTables, test.ID)
			runner.liveTablesMu.Unlock()
			if err := runner.restoreLiveTables(ctx, test, run.term); !errors.Is(err, errLiveTableFinalizationInterrupted) {
				t.Fatalf("interrupted close was not detected: %v", err)
			}
			restored := runner.liveRun(test.ID)
			if restored == nil || !restored.ready || !restored.closed || len(restored.tables) != 2 {
				t.Fatalf("incomplete frozen recovery: %+v", restored)
			}
			for _, id := range []string{firstID, secondID} {
				if _, err := runner.liveClient(test.ID, "worker").Execute(ctx, id, livetable.Request{Operation: livetable.OpRead}); err == nil {
					t.Fatal("interrupted finalization resumed table serving")
				}
			}
			if err := runner.finalizeLiveTables(ctx, test, "FAILED"); err != nil {
				t.Fatal(err)
			}
			for id, want := range map[string]uint64{firstID: 2, secondID: 1} {
				recovery, err := store.ReadLiveTableRecovery(ctx, id, test.ProjectID)
				if err != nil || recovery.Table.State != "closed" || recovery.Table.CheckpointRevision != recovery.Table.Revision {
					t.Fatalf("remaining table not closed/checkpointed: %+v %v", recovery, err)
				}
				if got := restored.tables[id].table.Stats().Counts.Available; got != want {
					t.Fatalf("remaining output discarded: %s has %d rows, want %d", id, got, want)
				}
			}
			if committed != nil {
				final := restored.tables[firstID].final
				if final == nil || *final != *committed {
					t.Fatalf("previous committed output changed: %+v, want %+v", final, committed)
				}
			}
			if final := restored.tables[secondID].final; final == nil || final.State != "pending" || final.Reason != "run_not_complete" {
				t.Fatalf("failed recovery wrote back automatically: %+v", final)
			}
			liveIntegrationAssertRows(t, store, "live-seed", 3)
		})
	}
}

func TestLiveTableClosedLifecycleRejectsOpenCheckpoint(t *testing.T) {
	runner, store, test := newLiveRuntimeFixture(t, "durable", "discard")
	run := runner.liveRun(test.ID)
	id := liveTableID(test.ID, "work")
	// Storage cannot interpret the opaque envelope. A closed lifecycle with an
	// authenticated but still-open checkpoint must be rejected by the runtime.
	if err := store.CloseLiveTable(context.Background(), id, 0, liveStorageTerm(run.term)); err != nil {
		t.Fatal(err)
	}
	runner.liveTablesMu.Lock()
	delete(runner.liveTables, test.ID)
	runner.liveTablesMu.Unlock()
	if err := runner.restoreLiveTables(context.Background(), test, run.term); err == nil || errors.Is(err, errLiveTableFinalizationInterrupted) {
		t.Fatalf("inconsistent closed lifecycle was accepted: %v", err)
	}
	if runner.liveRun(test.ID) != nil {
		t.Fatal("corrupt recovered state was installed")
	}
}
