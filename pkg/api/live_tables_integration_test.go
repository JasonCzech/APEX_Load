package api

import (
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

func TestLiveTablePersistenceRechecksRevokedUserGrant(t *testing.T) {
	runner, store, test := newLiveRuntimeFixture(t, "memory", "write_back")
	user := &storage.User{ID: "live-author", Subject: "live-author-subject", Active: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	grants := []*storage.UserRoleBinding{{ID: "live-grant", UserID: user.ID, ScopeType: "project", ScopeID: test.ProjectID, Role: "editor", CreatedAt: time.Now(), UpdatedAt: time.Now()}}
	if err := store.SaveUserWithRoleBindings(user, grants); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	test.StartedBySubject = user.Subject
	runner.mu.Unlock()
	if err := runner.authorizeLiveTablePersistence(test); err != nil {
		t.Fatal(err)
	}
	liveIntegrationPop(t, runner, test)
	if err := store.DeleteUserRoleBinding(grants[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := runner.finalizeLiveTables(context.Background(), test, "COMPLETE"); err == nil {
		t.Fatal("revoked actor changed the source pool")
	}
	liveIntegrationAssertRows(t, store, "live-seed", 3)
	summary := runner.liveTableArchiveSummaries(test.ID)
	if len(summary) != 1 || summary[0].Finalization == nil || summary[0].Finalization.Reason != "permission_unavailable" {
		t.Fatalf("missing retryable permission outcome: %+v", summary)
	}
	if err := store.SaveUserWithRoleBindings(user, grants); err != nil {
		t.Fatal(err)
	}
	if err := runner.finalizeLiveTables(context.Background(), test, "COMPLETE"); err != nil {
		t.Fatal(err)
	}
	liveIntegrationAssertRows(t, store, "live-seed", 2)
}

func TestLiveTablePersistenceRequiresServiceAccountWriteScope(t *testing.T) {
	for _, tc := range []struct {
		scope   string
		allowed bool
	}{{"results:read", false}, {"write", true}} {
		t.Run(tc.scope, func(t *testing.T) {
			runner, store, test := newLiveRuntimeFixture(t, "memory", "write_back")
			account := &storage.ServiceAccount{ID: "live-automation", Name: "Live automation", KeyHash: "test-only", Active: true, GlobalRoles: []string{"editor"}, Scopes: []string{tc.scope}, CreatedAt: time.Now(), UpdatedAt: time.Now()}
			if err := store.SaveServiceAccount(account); err != nil {
				t.Fatal(err)
			}
			runner.mu.Lock()
			test.StartedBySubject = "service-account:" + account.ID
			runner.mu.Unlock()
			liveIntegrationPop(t, runner, test)
			err := runner.finalizeLiveTables(context.Background(), test, "COMPLETE")
			if (err == nil) != tc.allowed {
				t.Fatalf("scope=%s allowed=%v err=%v", tc.scope, tc.allowed, err)
			}
			want := 3
			if tc.allowed {
				want = 2
			}
			liveIntegrationAssertRows(t, store, "live-seed", want)
		})
	}
}

func TestLiveTableFrozenSeedIgnoresLaterPoolEdits(t *testing.T) {
	runner, store, source := newLiveRuntimeFixture(t, "durable", "discard")
	liveIntegrationPop(t, runner, source)
	if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: "row-one", PoolID: "live-seed", Data: map[string]string{"value": "changed-later"}, Status: "available"}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ id, policy, sourceID, want string }{
		{"frozen-run", "original_seed", source.ID, "private-customer-one"},
		{"fresh-run", "current_at_start", "", "changed-later"},
	} {
		t.Run(tc.id, func(t *testing.T) {
			test := liveIntegrationNewRun(t, runner, store, tc.id, []storage.ScenarioDataPoolBinding{{PoolID: "live-seed", Alias: "work", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{Durability: "durable", SeedPolicy: tc.policy, SeedRunID: tc.sourceID}}})
			if err := runner.prepareLiveTables(context.Background(), test); err != nil {
				t.Fatal(err)
			}
			table, release, err := runner.inspectLiveTable(context.Background(), test, liveTableID(test.ID, "work"))
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			rows, _, err := liveTableExportRows(table, table.Stats().Revision)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != 2 {
				t.Fatalf("frozen seed took mutable runtime contents: %+v", rows)
			}
			found := false
			for _, row := range rows {
				if row.ID == "row-one" && row.Values["value"] == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("seed %s missing expected value %q: %+v", tc.policy, tc.want, rows)
			}
		})
	}
}

func TestLiveTableFailedStartupDiscardsPartialInitialization(t *testing.T) {
	runner, store, _ := newAdoptionTestRunner(t)
	test := liveIntegrationNewRun(t, runner, store, "partial-run", []storage.ScenarioDataPoolBinding{
		{Alias: "first", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{SeedPolicy: "empty", Columns: []string{"id"}, EndMode: "save_as_new", OutputPoolName: "must not publish"}},
		{PoolID: "missing-pool", Alias: "second", AccessMode: "live"},
	})
	if err := runner.prepareLiveTables(context.Background(), test); err == nil {
		t.Fatal("missing second seed did not fail startup")
	}
	run := runner.liveRun(test.ID)
	if run == nil || len(run.tables) != 1 || run.ready {
		t.Fatalf("expected partial initialization: %+v", run)
	}
	if err := runner.failTestBeforeRun(test, "second seed unavailable"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		run.mu.RLock()
		budget, closed := run.budget, run.closed
		run.mu.RUnlock()
		if budget == 0 && closed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("partial runtime retained budget=%d closed=%v", budget, closed)
		}
		time.Sleep(time.Millisecond)
	}
	result, err := store.GetLiveTableFinalization(context.Background(), liveTableID(test.ID, "first"), test.ProjectID)
	if err != nil || result == nil || result.Mode != "discard" || result.OutputPoolID != "" {
		t.Fatalf("partial output was published: %+v %v", result, err)
	}
}

func TestLiveTableFinalizationClosesEveryTableBeforePermissionFailure(t *testing.T) {
	runner, store, _ := newLiveRuntimeFixture(t, "memory", "discard")
	test := liveIntegrationNewRun(t, runner, store, "multiple-outputs", []storage.ScenarioDataPoolBinding{
		{PoolID: "live-seed", Alias: "first", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{EndMode: "write_back"}},
		{PoolID: "live-seed", Alias: "second", AccessMode: "live", LiveOptions: &storage.LiveTableOptions{EndMode: "write_back"}},
	})
	if err := runner.prepareLiveTables(context.Background(), test); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	test.StartedBySubject = "removed-user"
	runner.mu.Unlock()
	if err := runner.finalizeLiveTables(context.Background(), test, "COMPLETE"); err == nil {
		t.Fatal("unknown actor was authorized")
	}
	run := runner.liveRun(test.ID)
	run.mu.RLock()
	defer run.mu.RUnlock()
	for alias, table := range run.tables {
		if !table.table.Stats().Closed || table.record.State != "closed" {
			t.Errorf("table %s remained open after terminal permission failure", alias)
		}
	}
	liveIntegrationAssertRows(t, store, "live-seed", 3)
}

func TestLiveTableFailedRunExplicitSaveIsIdempotentAndUpdatesSummary(t *testing.T) {
	runner, store, test := newLiveRuntimeFixture(t, "memory", "write_back")
	liveIntegrationPop(t, runner, test)
	if err := runner.finalizeLiveTables(context.Background(), test, "FAILED"); err != nil {
		t.Fatal(err)
	}
	liveIntegrationAssertRows(t, store, "live-seed", 3)
	runner.mu.Lock()
	test.Status = "FAILED"
	runner.mu.Unlock()
	ended := time.Now()
	if err := store.UpdateTestStatus(test.ID, "FAILED", &ended, "test failure"); err != nil {
		t.Fatal(err)
	}
	server := &Server{runner: runner, store: store}
	var outputID string
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"output_pool_name":"Recovered output"}`))
		req.SetPathValue("id", test.ID)
		req.SetPathValue("tableID", liveTableID(test.ID, "work"))
		response := httptest.NewRecorder()
		server.handleFinalizeLiveTable(response, req)
		if response.Code != http.StatusOK {
			t.Fatalf("save attempt %d: %d %s", i, response.Code, response.Body.String())
		}
		var result storage.LiveTableFinalizationResult
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Mode != "save_as_new" || result.State != "complete" || result.OutputPoolID == "" {
			t.Fatalf("unexpected outcome: %+v", result)
		}
		if i == 0 {
			outputID = result.OutputPoolID
		} else if outputID != result.OutputPoolID {
			t.Fatal("retry duplicated output pool")
		}
	}
	liveIntegrationAssertRows(t, store, outputID, 1)
	liveIntegrationAssertRows(t, store, "live-seed", 3)
	summaries := runner.liveTableArchiveSummaries(test.ID)
	if len(summaries) != 1 || summaries[0].Finalization == nil || summaries[0].Finalization.OutputPoolID != outputID || summaries[0].Finalization.State != "complete" {
		t.Fatalf("stale runtime outcome: %+v", summaries)
	}
}

func liveIntegrationNewRun(t *testing.T, runner *TestRunner, store *storage.SQLiteStore, id string, bindings []storage.ScenarioDataPoolBinding) *ManagedTest {
	t.Helper()
	test := &ManagedTest{ID: id, Name: id, ProjectID: storage.DefaultProjectID, Status: "INITIALIZING", CreatedAt: time.Now(), CreatedBySubject: "dev-mode", DataPools: bindings}
	if err := store.SaveTest(&storage.TestRecord{ID: id, Name: id, ProjectID: test.ProjectID, Status: test.Status, StartTime: test.CreatedAt, Groups: []any{}, DataPools: bindings}); err != nil {
		t.Fatal(err)
	}
	if err := runner.claimTestExecutionFence(test); err != nil {
		t.Fatal(err)
	}
	runner.mu.Lock()
	runner.tests[id] = test
	runner.mu.Unlock()
	return test
}

func liveIntegrationPop(t *testing.T, runner *TestRunner, test *ManagedTest) {
	t.Helper()
	if _, err := runner.liveClient(test.ID, "local").Execute(context.Background(), liveTableID(test.ID, "work"), livetable.Request{ClientID: "worker", Sequence: 1, Operation: livetable.OpPop}); err != nil {
		t.Fatal(err)
	}
}

func liveIntegrationAssertRows(t *testing.T, store *storage.SQLiteStore, poolID string, want int) {
	t.Helper()
	rows, err := store.ListDataPoolRows(poolID)
	if err != nil || len(rows) != want {
		t.Fatalf("pool %s rows=%d want=%d error=%v", poolID, len(rows), want, err)
	}
}
