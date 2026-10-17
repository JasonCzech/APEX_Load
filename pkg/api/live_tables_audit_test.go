package api

import (
	"context"
	"errors"
	"testing"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

type liveAuditFaultStore struct {
	SQLiteStore *storage.SQLiteStore
	failAction  string
}

func (s *liveAuditFaultStore) SaveAuditEvent(event *storage.AuditEvent) error {
	if event.Action == s.failAction {
		return errors.New("injected audit failure")
	}
	return s.SQLiteStore.SaveAuditEvent(event)
}

func TestLiveTableAutomaticFinalizationRequiresAuditAndRetriesOutcome(t *testing.T) {
	for _, stage := range []string{"admitted", "completed"} {
		t.Run(stage, func(t *testing.T) {
			runner, store, test := newLiveRuntimeFixture(t, "memory", "write_back")
			fault := &liveAuditFaultStore{SQLiteStore: store, failAction: "live_table.finalize." + stage}
			runner.auditStore = sqliteOverlay(fault)
			id := liveTableID(test.ID, "work")
			if _, err := runner.liveClient(test.ID, "local").Execute(context.Background(), id, livetable.Request{ClientID: "worker", Sequence: 1, Operation: livetable.OpPop}); err != nil {
				t.Fatal(err)
			}
			if err := runner.finalizeLiveTables(context.Background(), test, "COMPLETE"); err == nil {
				t.Fatal("audit failure did not retain retryable finalization")
			}
			rows, err := store.ListDataPoolRows("live-seed")
			wantRows := 3
			if stage == "completed" {
				wantRows = 2
			}
			if err != nil || len(rows) != wantRows {
				t.Fatalf("pool rows after failed %s audit = %d, want %d: %v", stage, len(rows), wantRows, err)
			}
			fault.failAction = ""
			if err := runner.finalizeLiveTables(context.Background(), test, "COMPLETE"); err != nil {
				t.Fatal(err)
			}
			if err := runner.finalizeLiveTables(context.Background(), test, "COMPLETE"); err != nil {
				t.Fatal(err)
			}
			admitted, err := store.ListAuditEvents(storage.AuditEventFilter{ResourceType: "live_table", ResourceID: id, Action: "live_table.finalize.admitted"})
			if err != nil || len(admitted) != 1 {
				t.Fatalf("admission audit duplicated or missing = %+v %v", admitted, err)
			}
			completed, err := store.ListAuditEvents(storage.AuditEventFilter{ResourceType: "live_table", ResourceID: id, Action: "live_table.finalize.completed"})
			if err != nil || len(completed) != 1 {
				t.Fatalf("outcome audit duplicated or missing = %+v %v", completed, err)
			}
		})
	}
}
