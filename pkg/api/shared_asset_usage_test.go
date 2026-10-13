package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestSharedAssetUsageReportsEachAssetsUsers(t *testing.T) {
	s, store := newAuditTestServer(t)
	defer store.Close()
	now := time.Now().UTC()
	mustDo := func(label string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
	}
	mustDo("project", store.SaveProject(&storage.Project{ID: "usage-p", Name: "Retail", CreatedAt: now}))
	mustDo("repository", store.SaveObjectRepository(&storage.ObjectRepository{ID: "object-repo-pages", ProjectID: "usage-p", Name: "Pages", Objects: []storage.RepositoryObject{{LogicalName: "Login"}}}))
	for _, script := range []*dsl.Script{
		{ID: "usage-recorded", Name: "Recorded", Protocol: "browser", ProjectID: "usage-p", CreatedAt: now, Actions: []dsl.Action{{Name: "Login", Type: "browser_click", ObjectRef: "object-repo-pages:Login"}}},
		{ID: "usage-target", Name: "Target", Protocol: "http", ProjectID: "usage-p", CreatedAt: now},
	} {
		mustDo("script "+script.ID, store.SaveScript(script))
	}
	mustDo("pool", store.SaveDataPool(&storage.SharedDataPool{ID: "usage-pool", Name: "Customers", ProjectID: "usage-p", CreatedAt: now}))
	mustDo("unused pool", store.SaveDataPool(&storage.SharedDataPool{ID: "usage-idle", Name: "Idle", ProjectID: "usage-p", CreatedAt: now}))
	mustDo("scenario", store.SaveScenarioWithDataPoolBindings(&storage.Scenario{ID: "usage-scenario", Name: "Checkout run", Duration: "1m", CreatedAt: now},
		[]storage.ScenarioScript{{ScenarioID: "usage-scenario", ScriptID: "usage-target"}},
		[]storage.ScenarioDataPoolBinding{{ID: "usage-binding", PoolID: "usage-pool", Alias: "customers", AccessMode: "read_only", SelectionStrategy: "sequential"}}))
	mustDo("ruleset", store.SaveCorrelationRuleset(&storage.CorrelationRuleset{ID: "usage-rules", ProjectID: "usage-p", Name: "Session", Source: "script:usage-recorded", CreatedAt: now, UpdatedAt: now}))
	for index, event := range []struct {
		script, ruleset, status string
	}{
		{"usage-target", "usage-rules", "success"},
		{"usage-target", "usage-rules", "success"}, // a second application lists the script once
		{"usage-recorded", "usage-rules", "failure"},
		{"usage-deleted", "usage-rules", "success"}, // a script that no longer exists
		{"usage-recorded", "another-ruleset", "success"},
	} {
		mustDo("audit", store.SaveAuditEvent(&storage.AuditEvent{ID: "usage-audit-" + string(rune('a'+index)), Action: "correlation_ruleset.apply", ActorType: "user", ActorSubject: "dev", ProjectID: "usage-p", ResourceType: "script", ResourceID: event.script, Status: event.status, Details: map[string]any{"ruleset_id": event.ruleset, "matched": 1}, CreatedAt: now.Add(time.Duration(index) * time.Second)}))
	}

	read := func(path string) (*httptest.ResponseRecorder, SharedAssetUsageResponse) {
		t.Helper()
		w := httptest.NewRecorder()
		s.mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		var response SharedAssetUsageResponse
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode: %v %s", err, w.Body)
			}
		}
		return w, response
	}
	w, usage := read("/api/v1/projects/usage-p/shared-asset-usage")
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body)
	}
	pools := map[string][]SharedAssetScenarioUse{"usage-pool": {{ID: "usage-scenario", Name: "Checkout run", Alias: "customers"}}}
	tables := map[string][]SharedAssetScenarioUse{}
	repositories := map[string][]SharedAssetScriptUse{"object-repo-pages": {{ID: "usage-recorded", Name: "Recorded"}}}
	rulesets := map[string]SharedAssetRulesetUse{"usage-rules": {
		LearnedFrom: &SharedAssetScriptUse{ID: "usage-recorded", Name: "Recorded"},
		AppliedTo:   []SharedAssetScriptUse{{ID: "usage-target", Name: "Target"}},
	}}
	want := SharedAssetUsageResponse{ProjectID: "usage-p", DataPools: &pools, LiveTables: &tables, ObjectRepositories: &repositories, CorrelationRulesets: &rulesets}
	if !reflect.DeepEqual(usage, want) {
		t.Fatalf("usage = %#v, want %#v", usage, want)
	}

	// A section asks for its own kind; the others are left out, not reported empty.
	w, usage = read("/api/v1/projects/usage-p/shared-asset-usage?include=correlation_rulesets")
	if w.Code != http.StatusOK || usage.DataPools != nil || usage.ObjectRepositories != nil || !reflect.DeepEqual(usage.CorrelationRulesets, &rulesets) {
		t.Fatalf("rulesets only: %d %s", w.Code, w.Body)
	}
	w, usage = read("/api/v1/projects/usage-p/shared-asset-usage?include=data_pools,live_tables")
	if w.Code != http.StatusOK || !reflect.DeepEqual(usage.DataPools, &pools) || !reflect.DeepEqual(usage.LiveTables, &tables) || usage.ObjectRepositories != nil || usage.CorrelationRulesets != nil {
		t.Fatalf("data only: %d %s", w.Code, w.Body)
	}
	if w, _ = read("/api/v1/projects/usage-p/shared-asset-usage?include=secrets"); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown kind: %d %s", w.Code, w.Body)
	}

	// An empty project answers with empty maps, never null.
	mustDo("empty project", store.SaveProject(&storage.Project{ID: "usage-empty", Name: "Empty", CreatedAt: now}))
	w, _ = read("/api/v1/projects/usage-empty/shared-asset-usage")
	if w.Code != http.StatusOK {
		t.Fatalf("empty status %d %s", w.Code, w.Body)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"data_pools", "live_tables", "object_repositories", "correlation_rulesets"} {
		if string(raw[key]) != "{}" {
			t.Fatalf("%s = %s, want {}", key, raw[key])
		}
	}
}
