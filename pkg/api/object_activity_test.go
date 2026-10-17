package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestScriptArchiveActivityWorkflow(t *testing.T) {
	s, store := newAuditTestServer(t)
	defer store.Close()
	request := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.mux.ServeHTTP(w, r)
		return w
	}
	created := request("POST", "/api/v1/scripts", map[string]any{"script": map[string]any{"id": "activity-script", "name": "Checkout", "version": "1", "protocol": "http", "config": map[string]any{"base_url": "https://example.com"}, "actions": []any{map[string]any{"name": "Get", "type": "GET", "method": "GET", "target": "https://example.com"}}}})
	if created.Code != 201 {
		t.Fatalf("create %d %s", created.Code, created.Body)
	}
	var createdView map[string]json.RawMessage
	if err := json.Unmarshal(created.Body.Bytes(), &createdView); err != nil || createdView["activity"] == nil {
		t.Fatalf("creation attribution missing: %s %v", created.Body, err)
	}
	obj, err := store.GetScript("activity-script")
	if err != nil {
		t.Fatal(err)
	}
	archive := request("POST", "/api/v1/scripts/activity-script/archive", map[string]any{"updated_at": obj.UpdatedAt})
	if archive.Code != 200 {
		t.Fatalf("archive %d %s", archive.Code, archive.Body)
	}
	if repeated := request("POST", "/api/v1/scripts/activity-script/archive", map[string]any{"updated_at": obj.UpdatedAt}); repeated.Code != 200 {
		t.Fatalf("repeat: %d %s", repeated.Code, repeated.Body)
	}
	if response := request("GET", "/api/v1/scripts", nil); strings.Contains(response.Body.String(), "Checkout") {
		t.Fatalf("archive visible in default list: %s", response.Body)
	}
	if response := request("GET", "/api/v1/scripts?archive_state=archived", nil); !strings.Contains(response.Body.String(), "Checkout") {
		t.Fatalf("archived list: %s", response.Body)
	}
	if response := request("PUT", "/api/v1/scripts/activity-script", map[string]any{}); response.Code != 409 {
		t.Fatalf("archived edit %d %s", response.Code, response.Body)
	}
	obj, err = store.GetScript(obj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if response := request("POST", "/api/v1/scripts/activity-script/restore", map[string]any{"updated_at": obj.UpdatedAt}); response.Code != 200 {
		t.Fatalf("restore %d %s", response.Code, response.Body)
	}
	if response := request("DELETE", "/api/v1/scripts/activity-script", nil); response.Code != 200 {
		t.Fatalf("delete %d %s", response.Code, response.Body)
	}
	history := request("GET", "/api/v1/projects/unassigned/activity?resource_type=script&resource_id=activity-script", nil)
	if history.Code != 200 {
		t.Fatalf("history %d %s", history.Code, history.Body)
	}
	var payload struct {
		Events []storage.ObjectActivityEvent `json:"events"`
	}
	if err := json.Unmarshal(history.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Events) != 4 {
		t.Fatalf("events %#v", payload.Events)
	}
	for i, action := range []string{"delete", "restore", "archive", "create"} {
		if payload.Events[i].Action != action || payload.Events[i].ActorSubject != "dev-mode" || payload.Events[i].ResourceName != "Checkout" {
			t.Fatalf("event %#v", payload.Events[i])
		}
	}
}
func TestArchivedExecutionAdmission(t *testing.T) {
	s, store := newAuditTestServer(t)
	defer store.Close()
	script := &dsl.Script{ID: "archived-execution", Name: "Archived", Protocol: "http", Version: "1", Actions: []dsl.Action{{Type: "GET", Method: "GET", Target: "https://example.com"}}}
	if _, err := s.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	stored, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetObjectArchived("script", script.ID, stored.UpdatedAt, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runner.CreateTest(CreateTestRequest{Name: "Blocked", ScriptID: script.ID, VUsers: 1, Duration: "1s"}); !errors.Is(err, storage.ErrObjectArchived) {
		t.Fatalf("archived script admission error: %v", err)
	}
}
func TestActivityProjectionContainsOnlyChangedFields(t *testing.T) {
	s, store := newAuditTestServer(t)
	defer store.Close()
	script := &dsl.Script{ID: "safe-activity", Name: "Safe", Protocol: "http", Version: "1", Actions: []dsl.Action{{Type: "GET", Target: "https://example.com"}}}
	if _, err := s.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/safe-activity", nil)
	s.recordAuditEvent(r, "script.update", "success", "script", script.ID, "default", map[string]any{"password": "never-disclose", "script": script})
	events, err := store.ListObjectActivity(storage.ObjectActivityFilter{ProjectID: "default"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(events)
	if strings.Contains(string(raw), "never-disclose") || strings.Contains(string(raw), "https://example.com") {
		t.Fatalf("unsafe projection %s", raw)
	}
}

type activityFailureStore struct{ SQLiteStore *storage.SQLiteStore }

func (s *activityFailureStore) SaveObjectActivity(*storage.ObjectActivityEvent) error {
	return errors.New("activity disk unavailable")
}
func TestObjectActivityFailureDoesNotRejectMutation(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "activity.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: sqliteOverlay(&activityFailureStore{store}), MaxConcurrent: 2})
	r := httptest.NewRequest("POST", "/api/v1/scripts", strings.NewReader(`{"script":{"name":"Saved", "protocol":"http","version":"1","config":{"base_url":"https://example.com"},"actions":[{"name":"Get","type":"request","method":"GET","target":"/"}]}}`))
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, r)
	if w.Code != 201 || w.Header().Get("X-APEXLOAD-Audit-Status") != "unavailable" {
		t.Fatalf("mutation %d %s warning %s", w.Code, w.Body, w.Header())
	}
	var result StoredScript
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetScript(result.ID); err != nil {
		t.Fatalf("change not saved: %v", err)
	}
}
func TestObjectActivityScopeFilteringBeforePagination(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	for i, kind := range []string{"script", "test", "scenario", "script"} {
		if err := store.SaveObjectActivity(&storage.ObjectActivityEvent{ID: kind + string(rune('0'+i)), ProjectID: "default", ResourceType: kind, ResourceID: kind, Action: "create", Status: "success", CreatedAt: time.Now().Add(time.Duration(i) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	r := httptest.NewRequest("GET", "/api/v1/projects/default/activity?limit=1&offset=1", nil)
	r.SetPathValue("id", "default")
	// A results-only account must never page through script history.
	ctx := context.WithValue(r.Context(), authServiceAccountContextKey{}, &AuthenticatedServiceAccount{ID: "reader", Scopes: []string{"results:read"}})
	w := httptest.NewRecorder()
	server.handleProjectActivity(w, r.WithContext(ctx))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var response ObjectActivityListResponse
	json.Unmarshal(w.Body.Bytes(), &response)
	if len(response.Events) != 1 || response.Events[0].ResourceType != "test" || response.HasMore {
		t.Fatalf("scope %#v", response)
	}
}

func TestObjectActivityActorSnapshots(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	script := &dsl.Script{ID: "actor-script", Name: "Actor script", Protocol: "http", Version: "1", Actions: []dsl.Action{}}
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, kind, subject, display string
		request                      func(*http.Request) *http.Request
	}{
		{"development", "dev_mode", "dev-mode", "Development Mode", func(r *http.Request) *http.Request { return r }},
		{"user", "user", "oidc:alice", "Alice", func(r *http.Request) *http.Request {
			return r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, &AuthenticatedUser{Subject: "oidc:alice", DisplayName: "Alice"}))
		}},
		{"service", "service_account", "service-account:robot", "Build Robot", func(r *http.Request) *http.Request {
			return r.WithContext(context.WithValue(r.Context(), authServiceAccountContextKey{}, &AuthenticatedServiceAccount{ID: "robot", Name: "Build Robot"}))
		}},
		{"CI", "service_account", "github-ci:123:repo/workflow@main", "GitHub Actions · org/repo", func(r *http.Request) *http.Request {
			return withCIClaims(r, &ciBearerClaims{RepositoryID: "123", Repository: "org/repo", WorkflowRef: "repo/workflow@main", Actor: "alice"})
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := tc.request(httptest.NewRequest("PUT", "/api/v1/scripts/actor-script", nil))
			server.recordAuditEvent(r, "script.update", "success", "script", script.ID, storage.DefaultProjectID, nil)
			events, err := store.ListObjectActivity(storage.ObjectActivityFilter{ProjectID: storage.DefaultProjectID, ActorSubject: tc.subject})
			if err != nil || len(events) != 1 {
				t.Fatalf("events %#v %v", events, err)
			}
			event := events[0]
			if event.ActorType != tc.kind || event.ActorDisplayName != tc.display {
				t.Fatalf("actor %#v", event)
			}
			if tc.name == "CI" && event.InitiatedBy != "github:alice" {
				t.Fatalf("CI initiator %#v", event)
			}
		})
	}
}

func TestArchivedScheduleOccurrencesSkipWithoutReplay(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	runner := server.runner
	script, err := runner.StoreScriptDurable(&dsl.Script{ID: "schedule-archive-script", Name: "Scheduled", Protocol: "http", Version: "1", Actions: []dsl.Action{{Name: "Get", Type: "request", Target: "https://example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{Name: "Scheduled", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Minute)
	schedule := &storage.ScenarioSchedule{ID: "archive-schedule", ScenarioID: scenario.ID, ProjectID: scenario.ProjectID, Revision: 1, Name: "Every minute", CronExpr: "* * * * *", Timezone: "UTC", Enabled: true, MissedRunPolicy: "skip", NextRunAt: &now, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour)}
	runner.mu.Lock()
	err = runner.saveScenarioScheduleLocked(schedule)
	runner.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	durable, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetObjectArchived("script", script.ID, durable.UpdatedAt, true); err != nil {
		t.Fatal(err)
	}
	scheduler := newScenarioScheduler(runner)
	scheduler.now = func() time.Time { return now }
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatal(err)
	}
	occurrence, err := store.GetScenarioScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, now)
	if err != nil || occurrence == nil || occurrence.Status != "skipped" || occurrence.ReasonCode != "object_archived" {
		t.Fatalf("occurrence %#v %v", occurrence, err)
	}
	if !strings.Contains(occurrence.ConflictSummary, script.ID) {
		t.Fatalf("missing blocking dependency %#v", occurrence)
	}
	next, err := store.GetScenarioSchedule(schedule.ID)
	if err != nil || next.NextRunAt == nil || !next.NextRunAt.After(now) {
		t.Fatalf("schedule did not advance %#v %v", next, err)
	}
	durable, err = store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetObjectArchived("script", script.ID, durable.UpdatedAt, false); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.RunDueSchedules(); err != nil {
		t.Fatal(err)
	}
	all, err := store.ListScenarioScheduleOccurrences(schedule.ID)
	if err != nil || len(all) != 1 {
		t.Fatalf("restoration replayed skipped occurrence: %#v %v", all, err)
	}
}

func TestObjectActivityCompositeScopeAdmission(t *testing.T) {
	for _, tc := range []struct {
		scopes  []string
		allowed bool
	}{
		{[]string{"results:read"}, true}, {[]string{"read"}, true}, {[]string{"*"}, true}, {[]string{"automation:*"}, true}, {[]string{"scripts:write"}, false}, {[]string{"tests:run"}, false}, {nil, false},
	} {
		if got := serviceAccountAllowsScopes(&AuthenticatedServiceAccount{Scopes: tc.scopes}, []string{"object-activity:read"}); got != tc.allowed {
			t.Errorf("scopes %v admission %v", tc.scopes, got)
		}
	}
}

func TestAutomaticArchiveActivityUsesSystemIdentity(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	test, record := createArchiveFinalizationTest(t, store, "automatic-archive-activity", archiveRoot)
	durable, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	durable.CreatedBySubject = "oidc:alice"
	durable.CreatedByDisplayName = "Alice"
	if err := store.SaveTest(durable); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, NewHub(), store)
	if err := runner.finalizeRunArchiveDurably(context.Background(), test, record, true); err != nil {
		t.Fatal(err)
	}
	events, err := store.ListObjectActivity(storage.ObjectActivityFilter{ProjectID: storage.DefaultProjectID, ResourceID: test.ID, Action: "archive"})
	if err != nil || len(events) != 1 {
		t.Fatalf("archive activity %#v %v", events, err)
	}
	if events[0].ActorType != "system" || events[0].ActorSubject != "archive-finalizer" || events[0].InitiatedBy != "oidc:alice" {
		t.Fatalf("automatic attribution %#v", events[0])
	}
}
func TestBackgroundCIAndExecutionPlanAttribution(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	metadata, err := encodeCIRunMetadata(ciRunMetadata{SchemaVersion: ciRunSchemaVersion, Source: ciRunSource{Actor: "octocat"}})
	if err != nil {
		t.Fatal(err)
	}
	test := &ManagedTest{ID: "ci-activity", Name: "CI", ProjectID: storage.DefaultProjectID, CreatedAt: time.Now().UTC(), CreatedBySubject: "github-ci:123:workflow", CreatedByDisplayName: "GitHub Actions", MaterializationKey: metadata}
	actor := testCreationActor{Type: "service_account", Subject: test.CreatedBySubject, DisplayName: test.CreatedByDisplayName}
	server.runner.recordTestCreationActivity(test, actor)
	server.runner.recordTestStartActivity(test, actor)
	events, err := store.ListObjectActivity(storage.ObjectActivityFilter{ProjectID: storage.DefaultProjectID, ResourceID: test.ID})
	if err != nil || len(events) != 2 {
		t.Fatalf("CI events %#v %v", events, err)
	}
	for _, event := range events {
		if event.ActorType != "service_account" || event.InitiatedBy != "github:octocat" {
			t.Fatalf("CI attribution %#v", event)
		}
	}
	test.ID = "plan-activity"
	test.MaterializationKey = ""
	test.CreatedBySubject = "oidc:alice"
	test.CreatedByDisplayName = "Alice"
	server.runner.recordTestStartActivity(test, testCreationActor{Type: "system", Subject: test.CreatedBySubject, DisplayName: test.CreatedByDisplayName})
	events, err = store.ListObjectActivity(storage.ObjectActivityFilter{ProjectID: storage.DefaultProjectID, ResourceID: test.ID})
	if err != nil || len(events) != 1 || events[0].ActorSubject != "execution-plan-runner" || events[0].InitiatedBy != "oidc:alice" {
		t.Fatalf("plan attribution %#v %v", events, err)
	}
}

func TestArchivedExecutionPlanScheduleSkipsAndAdvances(t *testing.T) {
	store, plan, schedule, now := newExecutionPlanScheduleRecoveryFixture(t, "parallel")
	runner := NewTestRunner(1, NewHub(), store)
	if err := store.SaveProject(&storage.Project{ID: plan.ProjectID, Name: "Plan project", DomainID: "default"}); err != nil {
		t.Fatal(err)
	}
	script, err := runner.StoreScriptDurable(&dsl.Script{ProjectID: plan.ProjectID, ID: "plan-archive-script", Name: "Plan source", Protocol: "http", Version: "1", Actions: []dsl.Action{{Name: "Get", Type: "request", Target: "https://example.com"}}})
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := runner.StoreScenario(CreateScenarioRequest{Name: "Plan scenario", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	plan.Entries = []storage.ExecutionPlanEntry{{ScenarioID: scenario.ID}}
	if err := store.SaveExecutionPlan(plan); err != nil {
		t.Fatal(err)
	}
	source, err := store.GetScript(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SetObjectArchived("script", script.ID, source.UpdatedAt, true); err != nil {
		t.Fatal(err)
	}
	if _, err := runner.MaterializeAndRunExecutionPlan(plan, "", ""); !errors.Is(err, storage.ErrObjectArchived) {
		t.Fatalf("manual plan admission %v", err)
	}
	plannedAt := schedule.NextRunAt.UTC()
	if err := runner.runDueExecutionPlanScheduleOnce(schedule, now); err != nil {
		t.Fatal(err)
	}
	occurrence, err := store.GetExecutionPlanScheduleOccurrenceByScheduleAndPlannedAt(schedule.ID, plannedAt)
	if err != nil || occurrence == nil || occurrence.Status != "skipped" || occurrence.ReasonCode != "object_archived" || !strings.Contains(occurrence.ConflictSummary, script.ID) {
		t.Fatalf("occurrence %#v %v", occurrence, err)
	}
	future, err := store.GetExecutionPlanSchedule(schedule.ID)
	if err != nil || future.NextRunAt == nil || !future.NextRunAt.After(now) {
		t.Fatalf("schedule did not advance %#v %v", future, err)
	}
}

func TestExecutionPlanScheduleActivityRetainsInitiatorAcrossRecovery(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	actor := executionPlanScheduleOccurrenceCreationActor(&storage.ExecutionPlanScheduleOccurrence{ActorType: "user", ActorSubject: "oidc:alice", ActorDisplayName: "Alice"})
	snapshot := executionPlanRunEntrySnapshot(storage.ExecutionPlanEntry{}, "Scheduled", actor.Subject, actor.DisplayName, nil, nil, actor)
	recovered := executionPlanRunEntryCreationActor(snapshot)
	if recovered.Type != "user" || recovered.Subject != actor.Subject || recovered.InitiatedBy != actor.Subject {
		t.Fatalf("lost authorization or initiating identity: %#v", recovered)
	}
	test := &ManagedTest{ID: "scheduled-plan-attribution", Name: "Scheduled", ProjectID: "unassigned", CreatedBySubject: recovered.Subject, CreatedByDisplayName: recovered.DisplayName, CreatedAt: time.Now().UTC()}
	server.runner.recordTestCreationActivity(test, recovered)
	events, err := store.ListObjectActivity(storage.ObjectActivityFilter{ProjectID: "unassigned", ResourceID: test.ID, Limit: 10})
	if err != nil || len(events) != 1 || events[0].ActorType != "system" || events[0].ActorSubject != "execution-plan-scheduler" || events[0].InitiatedBy != "oidc:alice" {
		t.Fatalf("scheduled attribution %#v %v", events, err)
	}
}

func TestStartActivityIdentityIncludesIncarnation(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	now := time.Now().UTC()
	for _, createdAt := range []time.Time{now, now.Add(time.Hour)} {
		test := &ManagedTest{ID: "reused-run", Name: "Run", ProjectID: storage.DefaultProjectID, CreatedAt: createdAt}
		server.runner.recordTestStartActivity(test, testCreationActor{Type: "user", Subject: "oidc:alice", DisplayName: "Alice"})
		server.runner.mu.Lock()
		server.runner.tests[test.ID] = test
		server.runner.mu.Unlock()
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+test.ID+"/start", nil)
		server.recordAuditEvent(r, "test.start", "success", "test", test.ID, test.ProjectID, nil)
	}
	events, err := store.ListObjectActivity(storage.ObjectActivityFilter{ProjectID: storage.DefaultProjectID, ResourceID: "reused-run", Action: "start"})
	if err != nil || len(events) != 2 {
		t.Fatalf("incarnations collided or duplicated: %#v %v", events, err)
	}
}

func TestStartActivityDeduplicatesCommittedTransitions(t *testing.T) {
	for _, backgroundFirst := range []bool{true, false} {
		t.Run(fmt.Sprintf("background_first_%v", backgroundFirst), func(t *testing.T) {
			server, store := newAuditTestServer(t)
			defer store.Close()
			test := &ManagedTest{ID: "start-retries", Name: "Run", ProjectID: storage.DefaultProjectID, CreatedAt: time.Now().UTC()}
			actor := testCreationActor{Type: "user", Subject: "oidc:alice", DisplayName: "Alice"}
			server.runner.mu.Lock()
			server.runner.tests[test.ID] = test
			server.runner.mu.Unlock()
			r := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+test.ID+"/start", nil)
			r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, &AuthenticatedUser{Subject: actor.Subject, DisplayName: actor.DisplayName}))
			for _, status := range []string{"pending", "pending"} {
				server.recordAuditEvent(r, "test.start", status, "test", test.ID, test.ProjectID, nil)
			}
			if backgroundFirst {
				server.runner.recordTestStartActivity(test, actor)
			}
			for _, status := range []string{"accepted", "success", "accepted"} {
				server.recordAuditEvent(r, "test.start", status, "test", test.ID, test.ProjectID, nil)
			}
			if !backgroundFirst {
				server.runner.recordTestStartActivity(test, actor)
			}
			events, err := store.ListObjectActivity(storage.ObjectActivityFilter{ProjectID: test.ProjectID, ResourceID: test.ID, Action: "start"})
			if err != nil || len(events) != 2 {
				t.Fatalf("start retries duplicated transitions: %#v %v", events, err)
			}
			for _, event := range events {
				if event.ActorSubject != actor.Subject {
					t.Fatalf("start actor lost: %#v", event)
				}
			}
		})
	}
}

func TestImportSyncMapsToCanonicalModification(t *testing.T) {
	if got := lifecycleActivityAction("script.import.bruno.sync"); got != "modify" {
		t.Fatalf("sync action = %q", got)
	}
	server, store := newAuditTestServer(t)
	defer store.Close()
	script, err := server.runner.StoreScriptDurable(&dsl.Script{ID: "sync-activity", Name: "Source", Protocol: "http", Version: "1", Actions: []dsl.Action{}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/bruno/commit", nil)
	server.recordAuditEvent(r, "script.import.bruno.sync", "success", "script", script.ID, script.ProjectID, nil)
	events, err := store.ListObjectActivity(storage.ObjectActivityFilter{ProjectID: script.ProjectID, ResourceID: script.ID})
	if err != nil || len(events) != 1 || events[0].Action != "modify" {
		t.Fatalf("sync activity %#v %v", events, err)
	}
}

func TestSubresourceTransitionsRecordParentActivity(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	test := &ManagedTest{ID: "preparation-activity", Name: "Prepared run", ProjectID: storage.DefaultProjectID, CreatedAt: time.Now().UTC()}
	server.runner.mu.Lock()
	server.runner.tests[test.ID] = test
	server.runner.mu.Unlock()
	for _, action := range []string{"test.preparation.cancel", "test.preparation.renew", "report.annotations.update", "run.annotation.create", "run.annotation.delete"} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+test.ID+"/preparation", nil)
		r = r.WithContext(context.WithValue(r.Context(), authUserContextKey{}, &AuthenticatedUser{Subject: "oidc:alice", DisplayName: "Alice"}))
		server.recordAuditEvent(r, action, "success", "test", test.ID, test.ProjectID, nil)
	}
	events, err := store.ListObjectActivity(storage.ObjectActivityFilter{ProjectID: test.ProjectID, ResourceID: test.ID, Action: "modify"})
	if err != nil || len(events) != 5 {
		t.Fatalf("subresource transitions %#v %v", events, err)
	}
	sections := map[string]int{}
	for _, event := range events {
		if event.ActorSubject != "oidc:alice" || event.ResourceType != "test" || len(event.ChangedFields) != 1 {
			t.Fatalf("parent attribution %#v", event)
		}
		sections[event.ChangedFields[0]]++
	}
	if sections["preparation"] != 2 || sections["annotations"] != 3 {
		t.Fatalf("changed sections %#v", sections)
	}
}

func TestScenarioAndScriptSettingsActivityCoverage(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	script, err := server.runner.StoreScriptDurable(&dsl.Script{ID: "settings-coverage", Name: "Source", Protocol: "http", Version: "1", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "Get", Type: "request", Method: "GET", Target: "/"}}})
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Settings", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ action, kind, id, section string }{
		{"scenario.schedule.create", "scenario_schedule", "schedule", "schedules"},
		{"scenario.schedule.update", "scenario_schedule", "schedule", "schedules"},
		{"scenario.schedule.delete", "scenario_schedule", "schedule", "schedules"},
		{"scenario.schedule.occurrence.resolve", "scenario_schedule_occurrence", "occurrence", "schedules"},
		{"monitor.bindings.save", "scenario", scenario.ID, "monitor_bindings"},
		{"scenario.runtime.apply_to_groups", "scenario", scenario.ID, "runtime_settings"},
		{"object_repository.bind_script", "script", script.ID, "object_repository"},
	} {
		t.Run(tc.action, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/"+scenario.ID+"/schedules", nil)
			r.SetPathValue("id", scenario.ID)
			server.recordAuditEvent(r, tc.action, "success", tc.kind, tc.id, scenario.ProjectID, map[string]any{"scenario_id": scenario.ID})
			events, err := store.ListObjectActivity(storage.ObjectActivityFilter{ProjectID: scenario.ProjectID, Action: "modify", Limit: 1})
			if err != nil || len(events) != 1 || len(events[0].ChangedFields) != 1 || events[0].ChangedFields[0] != tc.section {
				t.Fatalf("settings activity %#v %v", events, err)
			}
			parent := scenario.ID
			if tc.kind == "script" {
				parent = script.ID
			}
			if events[0].ResourceID != parent {
				t.Fatalf("history attached to subresource: %#v", events[0])
			}
		})
	}
	before, _ := store.ListObjectActivity(storage.ObjectActivityFilter{ProjectID: script.ProjectID, ResourceID: script.ID})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/parameterize/apply", nil)
	server.recordAuditEvent(r, "script.import.parameterize.apply", "success", "script", script.ID, script.ProjectID, nil)
	after, err := store.ListObjectActivity(storage.ObjectActivityFilter{ProjectID: script.ProjectID, ResourceID: script.ID})
	if err != nil || len(after) != len(before) {
		t.Fatalf("draft preview claimed a committed change: %#v %v", after, err)
	}
}

type archiveRefreshRaceStore struct {
	SQLiteStore *storage.SQLiteStore
	runner      *TestRunner
}

func (s *archiveRefreshRaceStore) advanceReadRevision() {
	s.runner.mu.Lock()
	s.runner.contentRevision++
	s.runner.mu.Unlock()
}
func (s *archiveRefreshRaceStore) ListScripts(project string, folder *string) ([]*dsl.Script, error) {
	scripts, err := s.SQLiteStore.ListScripts(project, folder)
	s.advanceReadRevision()
	return scripts, err
}
func (s *archiveRefreshRaceStore) ListScenarios() ([]*storage.Scenario, error) {
	scenarios, err := s.SQLiteStore.ListScenarios()
	s.advanceReadRevision()
	return scenarios, err
}
func TestArchiveRefreshSurvivesConcurrentCatalogReadRevision(t *testing.T) {
	for _, kind := range []string{"script", "scenario"} {
		t.Run(kind, func(t *testing.T) {
			server, store := newAuditTestServer(t)
			defer store.Close()
			source, err := server.runner.StoreScriptDurable(&dsl.Script{ID: "archive-read-race", Name: "Source", Protocol: "http", Version: "1", Config: dsl.ScriptConfig{BaseURL: "https://example.test"}, Actions: []dsl.Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}}})
			if err != nil {
				t.Fatal(err)
			}
			id := source.ID
			version := source.Script.UpdatedAt
			if kind == "scenario" {
				scenario, err := server.runner.StoreScenario(CreateScenarioRequest{Name: "Scenario", Duration: "1m", Groups: []CreateGroupEntry{{ScriptID: source.ID, VUsers: 1}}})
				if err != nil {
					t.Fatal(err)
				}
				id, version = scenario.ID, scenario.UpdatedAt
			}
			raceStore := &archiveRefreshRaceStore{SQLiteStore: store, runner: server.runner}
			server.runner.scriptStore, server.runner.scenarioStore = sqliteOverlay(raceStore), sqliteOverlay(raceStore)
			for _, action := range []string{"archive", "restore"} {
				body, _ := json.Marshal(ObjectArchiveRequest{UpdatedAt: version})
				r := httptest.NewRequest(http.MethodPost, "/api/v1/"+kind+"s/"+id+"/"+action, bytes.NewReader(body))
				r.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				server.mux.ServeHTTP(w, r)
				if w.Code != http.StatusOK {
					t.Fatalf("%s response %d %s", action, w.Code, w.Body)
				}
				var response struct {
					ArchivedAt *time.Time `json:"archived_at"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || (response.ArchivedAt != nil) != (action == "archive") {
					t.Fatalf("wrong response archive state: %s %v", w.Body, err)
				}
				if kind == "script" {
					object, ok := server.runner.GetScript(id)
					if !ok {
						t.Fatal("changed script disappeared after its catalog refresh was fenced")
					}
					version = object.Script.UpdatedAt
					if (object.ArchivedAt != nil) != (action == "archive") {
						t.Fatalf("wrong archive state %#v", object)
					}
				} else {
					object, ok := server.runner.GetScenario(id)
					if !ok {
						t.Fatal("changed scenario disappeared after its catalog refresh was fenced")
					}
					version = object.UpdatedAt
					if (object.ArchivedAt != nil) != (action == "archive") {
						t.Fatalf("wrong archive state %#v", object)
					}
				}
			}
		})
	}
}
