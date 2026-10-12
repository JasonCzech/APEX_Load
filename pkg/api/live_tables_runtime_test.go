package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newLiveRuntimeFixture(t *testing.T, durability, endMode string) (*TestRunner, *storage.SQLiteStore, *ManagedTest) {
	t.Helper()
	runner, store, _ := newAdoptionTestRunner(t)
	pool := &storage.SharedDataPool{ID: "live-seed", Name: "Live seed", ProjectID: storage.DefaultProjectID, Schema: map[string]string{"value": "string"}}
	if err := store.SaveDataPool(pool); err != nil {
		t.Fatal(err)
	}
	for i, value := range []string{"private-customer-one", "private-customer-two"} {
		if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: []string{"row-one", "row-two"}[i], PoolID: pool.ID, Data: map[string]string{"value": value}, Status: "available"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: "inactive", PoolID: pool.ID, Data: map[string]string{"value": "historical"}, Status: "consumed"}); err != nil {
		t.Fatal(err)
	}
	bindings, err := normalizeDataPoolBindingIntent([]storage.ScenarioDataPoolBinding{{PoolID: pool.ID, Alias: "work", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{Durability: durability, EndMode: endMode}}})
	if err != nil {
		t.Fatal(err)
	}
	test := &ManagedTest{ID: "live-test", Name: "Live test", ProjectID: storage.DefaultProjectID, Status: "INITIALIZING", CreatedAt: time.Now(), CreatedBySubject: "dev-mode", DataPools: bindings}
	if err := store.SaveTest(&storage.TestRecord{ID: test.ID, Name: test.Name, ProjectID: test.ProjectID, Status: test.Status, StartTime: test.CreatedAt, Groups: []any{}, DataPools: bindings}); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.tests[test.ID] = test
	runner.mu.Unlock()
	if err := runner.prepareLiveTables(context.Background(), test); err != nil {
		t.Fatal(err)
	}
	return runner, store, test
}
func TestLiveTableRuntimeEncryptedRecoveryPreservesRetriesAndScopes(t *testing.T) {
	runner, store, test := newLiveRuntimeFixture(t, "durable", "discard")
	ctx := context.Background()
	id := liveTableID(test.ID, "work")
	req := livetable.Request{ClientID: "worker", OwnerID: "worker", Sequence: 1, Operation: livetable.OpPop}
	first, err := runner.liveClient(test.ID, "gen-a/group").Execute(ctx, id, req)
	if err != nil || first.Row == nil {
		t.Fatalf("first: %+v %v", first, err)
	}
	second, err := runner.liveClient(test.ID, "gen-b/group").Execute(ctx, id, req)
	if err != nil || second.Row == nil || first.Row.ID == second.Row.ID {
		t.Fatalf("distinct scopes reused a receipt: %+v %v", second, err)
	}
	recovery, err := store.ReadLiveTableRecovery(ctx, id, test.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(recovery)
	if bytes.Contains(data, []byte("private-customer")) {
		t.Fatal("recovery storage exposed plaintext rows")
	}
	term, err := runner.captureExecutionLeaseTerm()
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.restoreLiveTables(ctx, test, term); err != nil {
		t.Fatal(err)
	}
	retry, err := runner.liveClient(test.ID, "gen-a/group").Execute(ctx, id, req)
	if err != nil || retry.Row == nil || retry.Row.ID != first.Row.ID {
		t.Fatalf("retry after recovery: %+v %v", retry, err)
	}
	rows, err := store.ListDataPoolRows("live-seed")
	if err != nil || len(rows) != 3 {
		t.Fatalf("source was modified by live mutations: %+v %v", rows, err)
	}
	if _, err := runner.liveClient(test.ID, "gen-a/group").Execute(ctx, "another-run-table", req); err == nil {
		t.Fatal("cross-run table was accepted")
	}
}
func TestLiveTableRuntimeFinalizationPreservesInactiveRowsAndRetries(t *testing.T) {
	runner, store, test := newLiveRuntimeFixture(t, "memory", "write_back")
	test.Status = "COMPLETE"
	ctx := context.Background()
	id := liveTableID(test.ID, "work")
	if _, err := runner.liveClient(test.ID, "local").Execute(ctx, id, livetable.Request{ClientID: "worker", Sequence: 1, Operation: livetable.OpPop}); err != nil {
		t.Fatal(err)
	}
	if err := runner.finalizeLiveTables(ctx, test); err != nil {
		t.Fatal(err)
	}
	if err := runner.finalizeLiveTables(ctx, test); err != nil {
		t.Fatal(err)
	}
	rows, err := store.ListDataPoolRows("live-seed")
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("want remaining active plus inactive: %+v", rows)
	}
	found := false
	for _, row := range rows {
		if row.ID == "inactive" && row.Status == "consumed" {
			found = true
		}
	}
	if !found {
		t.Fatal("inactive source row was discarded")
	}
}
func TestLiveTableRuntimeFinalizationClosesWaiterWithoutDeadlock(t *testing.T) {
	runner, _, test := newLiveRuntimeFixture(t, "memory", "discard")
	ctx := context.Background()
	id := liveTableID(test.ID, "work")
	client := runner.liveClient(test.ID, "local")
	for seq := uint64(1); seq <= 2; seq++ {
		if _, err := client.Execute(ctx, id, livetable.Request{ClientID: "drain", Sequence: seq, Operation: livetable.OpPop}); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := client.Execute(ctx, id, livetable.Request{ClientID: "waiter", Sequence: 1, Operation: livetable.OpPop, Wait: time.Minute})
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for runner.liveRun(test.ID).tables[id].table.Stats().Waiters == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	finished := make(chan error, 1)
	go func() { finished <- runner.finalizeLiveTables(ctx, test) }()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("finalization blocked on an empty-table waiter")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("waiter did not close")
	}
}
func TestLiveTableEnvelopeRejectsPurposeRevisionAndTableSubstitution(t *testing.T) {
	ctx := context.Background()
	cipher, err := newLiveTableCipher(ctx, testContinuityWrapper(t), "table-a")
	if err != nil {
		t.Fatal(err)
	}
	body, err := cipher.seal(map[string]string{"secret": "protected"}, liveTableAAD("table-a", "entry", 1))
	if err != nil {
		t.Fatal(err)
	}
	for _, aad := range []string{liveTableAAD("table-b", "entry", 1), liveTableAAD("table-a", "entry", 2), liveTableAAD("table-a", "checkpoint", 1)} {
		var out map[string]string
		if cipher.open(body, aad, &out) == nil {
			t.Fatalf("accepted %s", aad)
		}
	}
	if bytes.Contains(body, []byte("protected")) {
		t.Fatal("plaintext in envelope")
	}
}
func TestLiveTableRuntimeRowsStayOutOfSummaryAndSnapshotPath(t *testing.T) {
	runner, _, test := newLiveRuntimeFixture(t, "memory", "discard")
	runner.mu.Lock()
	sources, err := runner.buildSharedDataSourcesLocked(test.DataPools)
	runner.mu.Unlock()
	if err != nil || len(sources) != 0 {
		t.Fatalf("live binding injected snapshot: %+v %v", sources, err)
	}
	server := &Server{runner: runner}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.SetPathValue("id", test.ID)
	response := httptest.NewRecorder()
	server.handleListLiveTables(response, req)
	if response.Code != 200 || strings.Contains(response.Body.String(), "private-customer") {
		t.Fatalf("metadata response leaked rows: %d %s", response.Code, response.Body.String())
	}
	req.SetPathValue("tableID", "other-table")
	response = httptest.NewRecorder()
	server.handleLiveTableRows(response, req)
	if response.Code != 409 {
		t.Fatalf("unknown table=%d", response.Code)
	}
}
