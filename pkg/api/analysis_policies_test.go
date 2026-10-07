package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/config"
	"github.com/jasonczech/apexload/pkg/storage"
)

func policyRequest(kind, name string, revision int64, definition string) map[string]any {
	request := map[string]any{"kind": kind, "name": name, "definition": json.RawMessage(definition)}
	if revision > 0 {
		request["revision"] = revision
	}
	return request
}

const policiesPath = "/api/v1/analysis/policies?project_id=" + storage.DefaultProjectID

func TestAnalysisPoliciesValidateEveryKind(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	valid := map[string]string{
		storage.AnalysisDefinitionKindTelemetryPolicy:           `{"version":1,"sample_age_ms":2000,"sustained_window_seconds":15}`,
		storage.AnalysisDefinitionKindTelemetryAcceptancePolicy: `{"version":1,"mode":"fail_ci","min_wall_time_coverage":0.9,"stage_lag_percentiles":[{"stage":"end_to_end","percentile":"p99","max_ms":2000}]}`,
		storage.AnalysisDefinitionKindBrowserPolicy:             `{"version":1,"metrics":[{"metric":"lcp","good":2500,"poor":4000,"weight":1},{"metric":"cls","good":0.1,"poor":0.25,"weight":1}],"baseline":{"mode":"latest_compatible"}}`,
		storage.AnalysisDefinitionKindJourneyPolicy:             `{"version":1,"journeys":[{"id":"checkout","name":"Checkout","start":{"transaction":"Cart"},"success":{"transaction":"Confirm"},"weight":2}]}`,
		storage.AnalysisDefinitionKindDerivedOperation:          `{"version":1,"operation":"moving_mean","metric":"response_time","population":"transaction","parameters":{"window_buckets":5}}`,
	}
	for kind, definition := range valid {
		response := authoringAPIRequest(t, server, http.MethodPost, policiesPath, policyRequest(kind, "Valid "+kind, 0, definition), "")
		if response.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", kind, response.Code, response.Body.String())
		}
	}
	invalid := map[string][]string{
		storage.AnalysisDefinitionKindTelemetryPolicy:           {`{"version":1}`, `{"version":2,"sample_age_ms":1}`, `{"version":1,"sample_age_ms":-1}`},
		storage.AnalysisDefinitionKindTelemetryAcceptancePolicy: {`{"version":1,"mode":"advisory"}`, `{"version":1,"mode":"block","max_loss_ratio":0.1}`, `{"version":1,"mode":"fail_ci","min_wall_time_coverage":1.5}`, `{"version":1,"mode":"fail_ci","stage_lag_percentiles":[{"stage":"render","percentile":"p99","max_ms":1}]}`},
		storage.AnalysisDefinitionKindBrowserPolicy:             {`{"version":1,"metrics":[],"baseline":{"mode":"none"}}`, `{"version":1,"metrics":[{"metric":"lcp","good":4000,"poor":2500,"weight":1}],"baseline":{"mode":"none"}}`, `{"version":1,"metrics":[{"metric":"lcp","good":1,"poor":2,"weight":0}],"baseline":{"mode":"none"}}`, `{"version":1,"metrics":[{"metric":"lcp","good":1,"poor":2,"weight":1}],"baseline":{"mode":"pinned_run"}}`},
		storage.AnalysisDefinitionKindJourneyPolicy:             {`{"version":1,"journeys":[]}`, `{"version":1,"journeys":[{"id":"Bad ID","name":"x","start":{"transaction":"a"},"success":{"transaction":"b"},"weight":1}]}`, `{"version":1,"journeys":[{"id":"a","name":"x","start":{"transaction":"a","action":"b"},"success":{"transaction":"b"},"weight":1}]}`},
		storage.AnalysisDefinitionKindDerivedOperation:          {`{"version":1,"operation":"sql","metric":"response_time","population":"transaction"}`, `{"version":1,"operation":"error_ratio","metric":"response_time","population":"message"}`, `{"version":1,"operation":"moving_mean","metric":"response_time","population":"transaction","unknown":1}`},
	}
	for kind, definitions := range invalid {
		for _, definition := range definitions {
			response := authoringAPIRequest(t, server, http.MethodPost, policiesPath, policyRequest(kind, "Invalid", 0, definition), "")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("%s accepted %s: %d %s", kind, definition, response.Code, response.Body.String())
			}
		}
	}
	if response := authoringAPIRequest(t, server, http.MethodPost, policiesPath, policyRequest("mapping", "Not a policy", 0, `{"version":1}`), ""); response.Code != http.StatusBadRequest {
		t.Fatalf("non-policy kind accepted: %d", response.Code)
	}
}

func TestAnalysisPoliciesRevisionsDefaultsAndDeletion(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	kind := storage.AnalysisDefinitionKindTelemetryAcceptancePolicy
	created := authoringAPIRequest(t, server, http.MethodPost, policiesPath, policyRequest(kind, "Gate", 0, `{"version":1,"apply_by_default":true,"mode":"advisory","max_loss_ratio":0.01}`), "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", created.Code, created.Body.String())
	}
	var policy storage.AnalysisDefinition
	if err := json.Unmarshal(created.Body.Bytes(), &policy); err != nil {
		t.Fatal(err)
	}
	second := authoringAPIRequest(t, server, http.MethodPost, policiesPath, policyRequest(kind, "Second default", 0, `{"version":1,"apply_by_default":true,"mode":"advisory","max_loss_ratio":0.02}`), "")
	if second.Code != http.StatusConflict || !strings.Contains(second.Body.String(), "already applies by default") {
		t.Fatalf("second default: %d %s", second.Code, second.Body.String())
	}
	itemPath := "/api/v1/analysis/policies/" + policy.ID + "?project_id=" + storage.DefaultProjectID
	if stale := authoringAPIRequest(t, server, http.MethodPut, itemPath, policyRequest(kind, "Gate", 7, `{"version":1,"apply_by_default":true,"mode":"fail_ci","max_loss_ratio":0.01}`), ""); stale.Code != http.StatusConflict {
		t.Fatalf("stale revision: %d %s", stale.Code, stale.Body.String())
	}
	if changedKind := authoringAPIRequest(t, server, http.MethodPut, itemPath, policyRequest(storage.AnalysisDefinitionKindTelemetryPolicy, "Gate", 1, `{"version":1,"sample_age_ms":1}`), ""); changedKind.Code != http.StatusBadRequest {
		t.Fatalf("kind change: %d %s", changedKind.Code, changedKind.Body.String())
	}
	updated := authoringAPIRequest(t, server, http.MethodPut, itemPath, policyRequest(kind, "Gate", 1, `{"version":1,"apply_by_default":true,"mode":"fail_ci","max_loss_ratio":0.01}`), "")
	if updated.Code != http.StatusOK {
		t.Fatalf("update: %d %s", updated.Code, updated.Body.String())
	}
	list := authoringAPIRequest(t, server, http.MethodGet, policiesPath+"&kind="+kind, nil, "")
	var listed []storage.AnalysisDefinition
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed) != 1 || listed[0].Revision != 2 {
		t.Fatalf("list: %s %v", list.Body.String(), err)
	}
	if deleted := authoringAPIRequest(t, server, http.MethodDelete, itemPath+"&revision=2", nil, ""); deleted.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", deleted.Code, deleted.Body.String())
	}
	if latest := authoringAPIRequest(t, server, http.MethodGet, itemPath, nil, ""); latest.Code != http.StatusNotFound {
		t.Fatalf("deleted policy still readable as latest: %d", latest.Code)
	}
	if pinned := authoringAPIRequest(t, server, http.MethodGet, itemPath+"&revision=1", nil, ""); pinned.Code != http.StatusOK {
		t.Fatalf("a pinned revision must stay readable after deletion: %d", pinned.Code)
	}
	if after := authoringAPIRequest(t, server, http.MethodPost, policiesPath, policyRequest(kind, "Replacement default", 0, `{"version":1,"apply_by_default":true,"mode":"advisory","max_loss_ratio":0.02}`), ""); after.Code != http.StatusCreated {
		t.Fatalf("a deleted default must not block a new one: %d %s", after.Code, after.Body.String())
	}
}

func TestRunPolicyPinsFreezeTheDefaultsInForceAtStart(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, test := newPassiveTransitionTestRunner(t, "policy-pins")
	backend := newTelemetryPolicyBarrierBackend(t)
	runner.setExecutionBackend(backend)
	project := normalizeProjectID(test.ProjectID)
	definition := json.RawMessage(`{"version":1,"apply_by_default":true,"mode":"fail_ci","min_wall_time_coverage":0.9}`)
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: "gate", ProjectID: project, Kind: storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, Name: "Gate", Definition: definition}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: "opt-in", ProjectID: project, Kind: storage.AnalysisDefinitionKindBrowserPolicy, Name: "Not default", Definition: json.RawMessage(`{"version":1,"metrics":[{"metric":"lcp","good":1,"poor":2,"weight":1}],"baseline":{"mode":"none"}}`)}, 0); err != nil {
		t.Fatal(err)
	}
	if err := runner.StartTest(test.ID); err != nil {
		t.Fatal(err)
	}
	backend.expected.Store(true)
	waitTelemetryBackend(t, backend)
	digest := sha256.Sum256(definition)
	want := []RunPolicyPin{{Kind: storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, ID: "gate", Name: "Gate", Revision: 1, SHA256: hex.EncodeToString(digest[:]), Source: "project_default"}}
	stored, err := store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	initial := decodeStoredInitialProvenance(stored.InitialProvenance)
	if initial == nil || !reflect.DeepEqual(initial.PolicyPins, want) {
		t.Fatalf("admission receipt pins: %#v", initial)
	}
	// A later edit is a new revision; it never reinterprets the started run.
	if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: "gate", ProjectID: project, Kind: storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, Name: "Gate", Definition: json.RawMessage(`{"version":1,"apply_by_default":true,"mode":"advisory","min_wall_time_coverage":0.5}`)}, 1); err != nil {
		t.Fatal(err)
	}
	finishTelemetryBackend(t, backend)
	stored, err = store.GetTest(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after := decodeStoredInitialProvenance(stored.InitialProvenance); after == nil || !reflect.DeepEqual(after.PolicyPins, want) {
		t.Fatalf("pins changed after Start: %#v", after)
	}
	runner.mu.RLock()
	live := cloneRunPolicyPins(test.initialArchiveProvenance.PolicyPins)
	runner.mu.RUnlock()
	if !reflect.DeepEqual(live, want) {
		t.Fatalf("installed receipt pins: %#v", live)
	}
}

func TestRunPolicyPinsRefuseAmbiguousDefaultsBeforeAdmission(t *testing.T) {
	t.Setenv(config.EnvArchiveDir, t.TempDir())
	runner, store, test := newPassiveTransitionTestRunner(t, "policy-ambiguous")
	runner.setExecutionBackend(newTelemetryPolicyBarrierBackend(t))
	project := normalizeProjectID(test.ProjectID)
	for _, id := range []string{"first", "second"} {
		// Seeded directly: the API refuses a second default, but a concurrent
		// pair of saves could still race past that check.
		if _, err := store.SaveAnalysisDefinition(storage.AnalysisDefinition{ID: id, ProjectID: project, Kind: storage.AnalysisDefinitionKindTelemetryAcceptancePolicy, Name: id, Definition: json.RawMessage(`{"version":1,"apply_by_default":true,"mode":"fail_ci","max_loss_ratio":0}`)}, 0); err != nil {
			t.Fatal(err)
		}
	}
	err := runner.StartTest(test.ID)
	if err == nil || !strings.Contains(err.Error(), "more than one") {
		t.Fatalf("ambiguous default policies admitted: %v", err)
	}
	stored, getErr := store.GetTest(test.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if stored.Status != "QUEUED" || decodeStoredInitialProvenance(stored.InitialProvenance) != nil {
		t.Fatalf("refused Start persisted admission: %s %#v", stored.Status, stored.InitialProvenance)
	}
}

// A browser policy may pin only a run of its own project as its baseline.
func TestBrowserPolicyPinsOnlyARunOfItsProject(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	if err := store.SaveProject(&storage.Project{ID: "other-project", Name: "Other"}); err != nil {
		t.Fatal(err)
	}
	for _, run := range []*storage.TestRecord{{ID: "own-run", ProjectID: storage.DefaultProjectID, Name: "own", Status: "COMPLETE"}, {ID: "foreign-run", ProjectID: "other-project", Name: "foreign", Status: "COMPLETE"}} {
		if err := store.SaveTest(run); err != nil {
			t.Fatal(err)
		}
	}
	pinned := func(run string) string {
		return `{"version":1,"metrics":[{"metric":"lcp","good":2500,"poor":4000,"weight":1}],"baseline":{"mode":"pinned_run","run_id":"` + run + `"}}`
	}
	for run, want := range map[string]int{"foreign-run": http.StatusNotFound, "missing-run": http.StatusNotFound, "own-run": http.StatusCreated} {
		response := authoringAPIRequest(t, server, http.MethodPost, policiesPath, policyRequest(storage.AnalysisDefinitionKindBrowserPolicy, "Pinned "+run, 0, pinned(run)), "")
		if response.Code != want {
			t.Fatalf("pinned %s: %d %s", run, response.Code, response.Body.String())
		}
	}
	// An update cannot re-pin it to another project's run either.
	list := authoringAPIRequest(t, server, http.MethodGet, policiesPath+"&kind="+storage.AnalysisDefinitionKindBrowserPolicy, nil, "")
	var listed []storage.AnalysisDefinition
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed) != 1 {
		t.Fatalf("list: %d %s", list.Code, list.Body.String())
	}
	created := listed[0]
	updatePath := "/api/v1/analysis/policies/" + created.ID + "?project_id=" + storage.DefaultProjectID
	update := authoringAPIRequest(t, server, http.MethodPut, updatePath, policyRequest(storage.AnalysisDefinitionKindBrowserPolicy, created.Name, created.Revision, pinned("foreign-run")), "")
	if update.Code != http.StatusNotFound || !strings.Contains(update.Body.String(), "baseline run not found in project") {
		t.Fatalf("update pinning a foreign run: %d %s", update.Code, update.Body.String())
	}
	if own := authoringAPIRequest(t, server, http.MethodPut, updatePath, policyRequest(storage.AnalysisDefinitionKindBrowserPolicy, created.Name, created.Revision, pinned("own-run")), ""); own.Code != http.StatusOK {
		t.Fatalf("update pinning its own run: %d %s", own.Code, own.Body.String())
	}
}
