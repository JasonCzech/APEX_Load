package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

type reimportHARResponse struct {
	Script          map[string]any          `json:"script"`
	ExistingScripts []*importExistingScript `json:"existing_scripts"`
}

func newReimportTestServer(t *testing.T) *Server {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "reimport.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return NewServer(ServerConfig{Store: store})
}

func reimportHAR(t *testing.T, server *Server, name, path string) reimportHARResponse {
	t.Helper()
	content := `{"log":{"entries":[{"request":{"method":"GET","url":"https://api.example.test` + path + `","headers":[]},` +
		`"response":{"status":200,"content":{"text":"{}"}}}]}}`
	body, err := json.Marshal(map[string]any{"name": name, "content": content})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id="+storage.DefaultProjectID, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("import status = %d body=%.400s", response.Code, response.Body.String())
	}
	var decoded reimportHARResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func saveReimport(t *testing.T, server *Server, operationID string, script map[string]any, target *importUpdateTarget) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{
		"operation_id":   operationID,
		"source_profile": "har",
		"scripts":        []any{script},
	}
	if target != nil {
		payload["update_targets"] = []any{target}
	}
	return postGenericImportBatch(t, server, payload)
}

func savedReimportScript(t *testing.T, response *httptest.ResponseRecorder) StoredScript {
	t.Helper()
	if response.Code != http.StatusCreated {
		t.Fatalf("save status = %d body=%.400s", response.Code, response.Body.String())
	}
	var decoded struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || len(decoded.Scripts) != 1 {
		t.Fatalf("save response = %.400s (%v)", response.Body.String(), err)
	}
	return decoded.Scripts[0]
}

// A HAR imported again under the same file name matches the script the first
// import saved, and a save naming it updates that script as a new version in
// place: same ID and folder, the next reference-key version.
func TestReimportUpdatesTheEarlierScriptAsANewVersion(t *testing.T) {
	server := newReimportTestServer(t)
	first := reimportHAR(t, server, "checkout.har", "/orders")
	if len(first.ExistingScripts) != 0 {
		t.Fatalf("first import matched %+v, want nothing", first.ExistingScripts)
	}
	original := savedReimportScript(t, saveReimport(t, server, "reimport-first", first.Script, nil))

	second := reimportHAR(t, server, "checkout.har", "/orders/v2")
	if len(second.ExistingScripts) != 1 || second.ExistingScripts[0] == nil {
		t.Fatalf("re-import matched %+v, want the earlier script", second.ExistingScripts)
	}
	match := second.ExistingScripts[0]
	if match.ScriptID != original.ID || match.Matches != 1 {
		t.Fatalf("match = %+v, want script %s", match, original.ID)
	}
	if match.SameSource {
		t.Fatal("a file name is not a declared identity, so the match must not count as the same source")
	}
	if match.EditedSinceImport {
		t.Fatal("an untouched imported script was reported as edited")
	}

	updated := savedReimportScript(t, saveReimport(t, server, "reimport-second", second.Script,
		&importUpdateTarget{ScriptID: match.ScriptID, ExpectedUpdatedAt: match.UpdatedAt}))
	if updated.ID != original.ID {
		t.Fatalf("update created %s, want %s updated in place", updated.ID, original.ID)
	}
	if !strings.HasSuffix(updated.RefKey, "-v2") || strings.TrimSuffix(updated.RefKey, "-v2") != strings.TrimSuffix(original.RefKey, "-v1") {
		t.Fatalf("ref key = %q, want the next version of %q", updated.RefKey, original.RefKey)
	}
	stored, ok := server.runner.GetScript(original.ID)
	if !ok || stored.Script == nil || len(stored.Script.Actions) != 1 || stored.Script.Actions[0].Target != "/orders/v2" {
		t.Fatalf("updated script = %+v, want the re-imported request", stored)
	}
	if scripts, err := server.store.ListScripts(storage.DefaultProjectID, nil); err != nil || len(scripts) != 1 {
		t.Fatalf("project scripts = %d (%v), want the one script updated", len(scripts), err)
	}

	third := reimportHAR(t, server, "checkout.har", "/orders/v3")
	if len(third.ExistingScripts) != 1 || third.ExistingScripts[0] == nil || third.ExistingScripts[0].EditedSinceImport {
		t.Fatalf("after an update the match = %+v, want it unedited", third.ExistingScripts)
	}
}

// Updating a script the review showed is refused when it changed since, or
// when it came from another source.
func TestReimportRefusesAStaleOrForeignUpdateTarget(t *testing.T) {
	server := newReimportTestServer(t)
	first := reimportHAR(t, server, "checkout.har", "/orders")
	original := savedReimportScript(t, saveReimport(t, server, "stale-first", first.Script, nil))
	other := reimportHAR(t, server, "browse.har", "/browse")
	foreign := savedReimportScript(t, saveReimport(t, server, "stale-other", other.Script, nil))

	again := reimportHAR(t, server, "checkout.har", "/orders/v2")
	match := again.ExistingScripts[0]
	for name, target := range map[string]*importUpdateTarget{
		"changed since review": {ScriptID: match.ScriptID, ExpectedUpdatedAt: match.UpdatedAt.Add(-time.Second)},
		"another source":       {ScriptID: foreign.ID, ExpectedUpdatedAt: foreign.Script.UpdatedAt},
		"missing script":       {ScriptID: "8e3c5e1e-3f7a-4c39-9d52-5b0c8f2a6b11", ExpectedUpdatedAt: match.UpdatedAt},
	} {
		response := saveReimport(t, server, "stale-"+strings.ReplaceAll(name, " ", "-"), again.Script, target)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), importUpdateTargetStaleCode) {
			t.Errorf("%s: status = %d body=%.300s, want 409 %s", name, response.Code, response.Body.String(), importUpdateTargetStaleCode)
		}
	}
	if stored, ok := server.runner.GetScript(original.ID); !ok || stored.Script.Actions[0].Target != "/orders" {
		t.Fatal("a refused update changed the script")
	}
}

// A script edited in APEX after its import is flagged, because updating it
// replaces those edits.
func TestReimportFlagsAScriptEditedSinceImport(t *testing.T) {
	server := newReimportTestServer(t)
	first := reimportHAR(t, server, "checkout.har", "/orders")
	original := savedReimportScript(t, saveReimport(t, server, "edited-first", first.Script, nil))
	stored, ok := server.runner.GetScript(original.ID)
	if !ok {
		t.Fatal("saved script is unavailable")
	}
	edited := cloneScriptDefinition(stored.Script)
	edited.Actions[0].ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 2500}
	if _, updated, err := server.runner.UpdateScriptDurable(original.ID, edited); err != nil || !updated {
		t.Fatalf("edit script: updated=%v err=%v", updated, err)
	}
	again := reimportHAR(t, server, "checkout.har", "/orders")
	if len(again.ExistingScripts) != 1 || again.ExistingScripts[0] == nil || !again.ExistingScripts[0].EditedSinceImport {
		t.Fatalf("match = %+v, want it flagged as edited since import", again.ExistingScripts)
	}
}

func reimportOpenAPI(t *testing.T, server *Server, path string) reimportHARResponse {
	t.Helper()
	spec := `{"openapi":"3.0.3","info":{"title":"Orders API","version":"1"},"servers":[{"url":"https://api.example.test"}],` +
		`"paths":{"` + path + `":{"get":{"operationId":"listOrders","responses":{"200":{}}}}}}`
	body, err := json.Marshal(map[string]any{"name": "orders.json", "content": spec, "project_id": storage.DefaultProjectID})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi?project_id="+storage.DefaultProjectID, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("OpenAPI import status = %d body=%.400s", response.Code, response.Body.String())
	}
	var decoded reimportHARResponse
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func postOpenAPIReimportCommit(t *testing.T, server *Server, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi/commit?project_id="+storage.DefaultProjectID, bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

func commitOpenAPIReimport(t *testing.T, server *Server, script map[string]any, targetID string) StoredScript {
	t.Helper()
	payload := map[string]any{"script": script}
	if targetID != "" {
		payload["mode"] = "replace"
		payload["target_script_id"] = targetID
	}
	response := postOpenAPIReimportCommit(t, server, payload)
	if response.Code != http.StatusCreated && response.Code != http.StatusOK {
		t.Fatalf("OpenAPI commit status = %d body=%.400s", response.Code, response.Body.String())
	}
	var decoded struct {
		StoredScript StoredScript `json:"stored_script"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || decoded.StoredScript.ID == "" {
		t.Fatalf("OpenAPI commit response = %.400s (%v)", response.Body.String(), err)
	}
	return decoded.StoredScript
}

// A specification's declared title is its identity: a re-import of the same
// API matches the script the first one saved as the same source, unedited,
// and replacing it keeps the script.
func TestReimportOpenAPIMatchesByDeclaredTitle(t *testing.T) {
	server := newReimportTestServer(t)
	first := reimportOpenAPI(t, server, "/orders")
	original := commitOpenAPIReimport(t, server, first.Script, "")

	second := reimportOpenAPI(t, server, "/v2/orders")
	if len(second.ExistingScripts) != 1 || second.ExistingScripts[0] == nil {
		t.Fatalf("re-import matched %+v, want the earlier script", second.ExistingScripts)
	}
	match := second.ExistingScripts[0]
	if match.ScriptID != original.ID || !match.SameSource || match.EditedSinceImport {
		t.Fatalf("match = %+v, want %s from the same source, unedited", match, original.ID)
	}
	replaced := commitOpenAPIReimport(t, server, second.Script, match.ScriptID)
	if replaced.ID != original.ID {
		t.Fatalf("replace saved %s, want %s", replaced.ID, original.ID)
	}
	third := reimportOpenAPI(t, server, "/v3/orders")
	if len(third.ExistingScripts) != 1 || third.ExistingScripts[0] == nil || third.ExistingScripts[0].EditedSinceImport {
		t.Fatalf("after a replace the match = %+v, want it unedited", third.ExistingScripts)
	}
}

func reimportJMeter(t *testing.T, server *Server, path string) (scripts []map[string]any, existing []*importExistingScript) {
	t.Helper()
	jmx := `<jmeterTestPlan version="1.2"><hashTree>
  <TestPlan testname="Checkout plan"/><hashTree>
    <ThreadGroup testname="Buyers">
      <stringProp name="ThreadGroup.num_threads">2</stringProp>
    </ThreadGroup><hashTree>
      <HTTPSamplerProxy testname="Home">
        <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
        <stringProp name="HTTPSampler.protocol">https</stringProp>
        <stringProp name="HTTPSampler.path">` + path + `</stringProp>
        <stringProp name="HTTPSampler.method">GET</stringProp>
      </HTTPSamplerProxy><hashTree/>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter?filename=checkout.jmx&project_id="+storage.DefaultProjectID, strings.NewReader(jmx))
	request.Header.Set("Content-Type", "application/xml")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("JMeter import status = %d body=%.400s", response.Code, response.Body.String())
	}
	var decoded struct {
		Scripts         []map[string]any        `json:"scripts"`
		ExistingScripts []*importExistingScript `json:"existing_scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Scripts, decoded.ExistingScripts
}

func postJMeterReimport(t *testing.T, server *Server, scripts []map[string]any, targets []any) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{"scripts": scripts}
	if targets != nil {
		payload["update_targets"] = targets
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter/save", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

func saveJMeterReimport(t *testing.T, server *Server, scripts []map[string]any, targets []any) StoredScript {
	t.Helper()
	response := postJMeterReimport(t, server, scripts, targets)
	if response.Code != http.StatusCreated {
		t.Fatalf("JMeter save status = %d body=%.400s", response.Code, response.Body.String())
	}
	var decoded struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil || len(decoded.Scripts) != 1 {
		t.Fatalf("JMeter save response = %.400s (%v)", response.Body.String(), err)
	}
	return decoded.Scripts[0]
}

// A JMeter test plan's own name and the thread group identify its scripts: a
// re-import of the same plan updates each thread group's script in place.
func TestReimportJMeterUpdatesTheThreadGroupScript(t *testing.T) {
	server := newReimportTestServer(t)
	scripts, existing := reimportJMeter(t, server, "/home")
	if len(scripts) != 1 || len(existing) != 0 {
		t.Fatalf("first import = %d scripts, matches %+v", len(scripts), existing)
	}
	original := saveJMeterReimport(t, server, scripts, nil)

	scripts, existing = reimportJMeter(t, server, "/home/v2")
	if len(existing) != 1 || existing[0] == nil || existing[0].ScriptID != original.ID || !existing[0].SameSource || existing[0].EditedSinceImport {
		t.Fatalf("re-import matched %+v, want %s from the same plan, unedited", existing, original.ID)
	}
	stale := postJMeterReimport(t, server, scripts, []any{importUpdateTarget{ScriptID: existing[0].ScriptID, ExpectedUpdatedAt: existing[0].UpdatedAt.Add(-time.Second)}})
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), importUpdateTargetStaleCode) {
		t.Fatalf("update of a script changed after review = %d %.300s, want 409", stale.Code, stale.Body.String())
	}
	updated := saveJMeterReimport(t, server, scripts, []any{importUpdateTarget{ScriptID: existing[0].ScriptID, ExpectedUpdatedAt: existing[0].UpdatedAt}})
	if updated.ID != original.ID || !strings.HasSuffix(updated.RefKey, "-v2") {
		t.Fatalf("updated = %s %s, want %s at its next version", updated.ID, updated.RefKey, original.ID)
	}
	stored, ok := server.runner.GetScript(original.ID)
	if !ok || !strings.Contains(stored.Script.Actions[0].Target, "/home/v2") {
		t.Fatalf("updated script target = %+v", stored)
	}
}

// The LoadRunner archive save updates the script an earlier import of the
// same project made, in place, and refuses one from another project.
func TestReimportLoadRunnerSaveUpdatesInPlace(t *testing.T) {
	server := newReimportTestServer(t)
	script := func(target, project string) map[string]any {
		draft := enterpriseBatchTestScript("WLS_Login", storage.DefaultProjectID)
		draft["metadata"] = map[string]any{"import": map[string]any{
			"source": enterpriseArchiveSaveProfile,
			"preserved": map[string]any{
				dsl.ImportSourceDocumentKey:         project,
				dsl.ImportSourceDocumentDeclaredKey: true,
			},
		}}
		draft["actions"] = []any{map[string]any{"name": "Request", "type": "request", "method": "GET", "target": target}}
		return draft
	}
	created := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id": "lr-reimport-first",
		"scripts":      []any{script("/login", "WLS_Login")},
	})
	original := savedReimportScript(t, created)
	stored, _ := server.runner.GetScript(original.ID)

	foreign := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id":   "lr-reimport-foreign",
		"scripts":        []any{script("/other", "WLS_Other")},
		"update_targets": []any{importUpdateTarget{ScriptID: original.ID, ExpectedUpdatedAt: stored.Script.UpdatedAt}},
	})
	if foreign.Code != http.StatusConflict || !strings.Contains(foreign.Body.String(), importUpdateTargetStaleCode) {
		t.Fatalf("foreign project update = %d %.300s, want 409", foreign.Code, foreign.Body.String())
	}
	stale := postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id":   "lr-reimport-stale",
		"scripts":        []any{script("/login/v2", "WLS_Login")},
		"update_targets": []any{importUpdateTarget{ScriptID: original.ID, ExpectedUpdatedAt: stored.Script.UpdatedAt.Add(-time.Second)}},
	})
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), importUpdateTargetStaleCode) {
		t.Fatalf("update of a script changed after review = %d %.300s, want 409", stale.Code, stale.Body.String())
	}
	updated := savedReimportScript(t, postEnterpriseArchiveBatch(t, server, map[string]any{
		"operation_id":   "lr-reimport-second",
		"scripts":        []any{script("/login/v2", "WLS_Login")},
		"update_targets": []any{importUpdateTarget{ScriptID: original.ID, ExpectedUpdatedAt: stored.Script.UpdatedAt}},
	}))
	if updated.ID != original.ID || !strings.HasSuffix(updated.RefKey, "-v2") {
		t.Fatalf("updated = %s %s, want %s at its next version", updated.ID, updated.RefKey, original.ID)
	}
}

// A match carries how the earlier script supplied each input its import
// recorded, so the review can offer the same choice again; a credential-shaped
// name stays out.
func TestReimportMatchCarriesTheEarlierInputChoices(t *testing.T) {
	server := newReimportTestServer(t)
	draft := func() map[string]any {
		script := enterpriseBatchTestScript("Orders", storage.DefaultProjectID)
		script["metadata"] = map[string]any{"import": map[string]any{
			"source": enterpriseArchiveSaveProfile,
			"preserved": map[string]any{
				dsl.ImportSourceDocumentKey:         "Orders",
				dsl.ImportSourceDocumentDeclaredKey: true,
			},
			"inputs": map[string]any{
				"customer":  map[string]any{"mode": "data", "alias": "customers", "column": "id"},
				"region":    map[string]any{"mode": "value"},
				"apiSecret": map[string]any{"mode": "value"},
				"apiToken":  map[string]any{"mode": "secret", "reference": "{{secret.API_TOKEN}}"},
			},
		}}
		script["parameters"] = []any{
			map[string]any{"name": "region", "type": "list", "values": []any{"us-east"}},
			map[string]any{"name": "apiSecret", "type": "list", "values": []any{"typed-by-hand"}},
		}
		return script
	}
	decode := func() *dsl.Script {
		var script dsl.Script
		encoded, _ := json.Marshal(draft())
		if err := json.Unmarshal(encoded, &script); err != nil {
			t.Fatal(err)
		}
		return &script
	}
	if _, _, err := server.runner.CreateImportedScriptDurableReportingReplay(decode(), enterpriseArchiveSaveProfile); err != nil {
		t.Fatal(err)
	}
	candidate := *decode()
	matches := server.importExistingScripts(storage.DefaultProjectID, []*dsl.Script{&candidate})
	if len(matches) != 1 || matches[0] == nil {
		t.Fatalf("matches = %+v", matches)
	}
	inputs := matches[0].Inputs
	if inputs["customer"].Mode != "data" || inputs["customer"].Alias != "customers" || inputs["customer"].Column != "id" {
		t.Errorf("customer input = %+v, want the data pool column", inputs["customer"])
	}
	if inputs["region"].Mode != "value" || inputs["region"].Value != "us-east" {
		t.Errorf("region input = %+v, want the value for every user", inputs["region"])
	}
	if inputs["apiToken"].Mode != "secret" || inputs["apiToken"].Reference != "{{secret.API_TOKEN}}" {
		t.Errorf("apiToken input = %+v, want its project secret offered again", inputs["apiToken"])
	}
	if _, leaked := inputs["apiSecret"]; leaked {
		t.Error("a credential-shaped input was offered again")
	}
}

// An update keeps the name the operator gave the script after its import; a
// rename alone is not an edit to its steps or settings.
func TestReimportUpdateKeepsTheScriptsCurrentName(t *testing.T) {
	server := newReimportTestServer(t)
	first := reimportHAR(t, server, "checkout.har", "/orders")
	original := savedReimportScript(t, saveReimport(t, server, "rename-first", first.Script, nil))
	stored, _ := server.runner.GetScript(original.ID)
	renamed := cloneScriptDefinition(stored.Script)
	renamed.Name = "Checkout (renamed)"
	if _, updated, err := server.runner.UpdateScriptDurable(original.ID, renamed); err != nil || !updated {
		t.Fatalf("rename: updated=%v err=%v", updated, err)
	}
	again := reimportHAR(t, server, "checkout.har", "/orders/v2")
	match := again.ExistingScripts[0]
	if match == nil || match.EditedSinceImport {
		t.Fatalf("match = %+v, want a rename not to count as an edit", match)
	}
	updated := savedReimportScript(t, saveReimport(t, server, "rename-second", again.Script,
		&importUpdateTarget{ScriptID: match.ScriptID, ExpectedUpdatedAt: match.UpdatedAt}))
	if updated.Name != "Checkout (renamed)" {
		t.Fatalf("updated name = %q, want the operator's name kept", updated.Name)
	}
}

// An update keeps the goals and runtime policy set on the script in APEX,
// which an import never writes.
func TestReimportUpdateKeepsGoalsAndRuntimePolicy(t *testing.T) {
	server := newReimportTestServer(t)
	original := savedReimportScript(t, saveReimport(t, server, "goals-first", reimportHAR(t, server, "checkout.har", "/orders").Script, nil))
	stored, _ := server.runner.GetScript(original.ID)
	configured := cloneScriptDefinition(stored.Script)
	p95 := 800.0
	configured.GoalConfig = &dsl.GoalConfig{P95LatencyMs: &p95}
	configured.Metadata.RuntimePolicy = &dsl.ScriptRuntimePolicy{AllowedTargetHosts: []string{"api.example.test"}}
	if _, ok, err := server.runner.UpdateScriptDurable(original.ID, configured); err != nil || !ok {
		t.Fatalf("configure script: updated=%v err=%v", ok, err)
	}
	again := reimportHAR(t, server, "checkout.har", "/orders/v2")
	match := again.ExistingScripts[0]
	savedReimportScript(t, saveReimport(t, server, "goals-second", again.Script,
		&importUpdateTarget{ScriptID: match.ScriptID, ExpectedUpdatedAt: match.UpdatedAt}))
	current, _ := server.runner.GetScript(original.ID)
	if current.Script.Actions[0].Target != "/orders/v2" {
		t.Fatal("the update did not apply")
	}
	if current.Script.GoalConfig == nil || current.Script.GoalConfig.P95LatencyMs == nil || *current.Script.GoalConfig.P95LatencyMs != p95 {
		t.Fatalf("goals after update = %+v, want them kept", current.Script.GoalConfig)
	}
	if current.Script.Metadata == nil || current.Script.Metadata.RuntimePolicy == nil ||
		len(current.Script.Metadata.RuntimePolicy.AllowedTargetHosts) != 1 {
		t.Fatalf("runtime policy after update = %+v, want it kept", current.Script.Metadata)
	}
}

// Two scripts of one import with the same identity are offered different
// earlier scripts, newest first, so one save never names a script twice.
func TestReimportOffersEachEarlierScriptOnce(t *testing.T) {
	server := newReimportTestServer(t)
	older := savedReimportScript(t, saveReimport(t, server, "once-first", reimportHAR(t, server, "checkout.har", "/a").Script, nil))
	newer := savedReimportScript(t, saveReimport(t, server, "once-second", reimportHAR(t, server, "checkout.har", "/b").Script, nil))
	draft := func() *dsl.Script {
		script := &dsl.Script{Name: "Checkout", Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "har"}}}
		dsl.SetImportSourceIdentity(script, "checkout.har", false, "")
		return script
	}
	matches := server.importExistingScripts(storage.DefaultProjectID, []*dsl.Script{draft(), draft(), draft()})
	if matches[0] == nil || matches[1] == nil || matches[2] != nil {
		t.Fatalf("matches = %+v, want one earlier script for each of the first two", matches)
	}
	if matches[0].ScriptID != newer.ID || matches[1].ScriptID != older.ID {
		t.Fatalf("matched %s then %s, want the newer %s then the older %s", matches[0].ScriptID, matches[1].ScriptID, newer.ID, older.ID)
	}
}

// A re-import replace through the OpenAPI commit is fenced on the version the
// review showed, and keeps the script's current name.
func TestReimportOpenAPIReplaceIsFencedAndKeepsTheName(t *testing.T) {
	server := newReimportTestServer(t)
	original := commitOpenAPIReimport(t, server, reimportOpenAPI(t, server, "/orders").Script, "")
	stored, _ := server.runner.GetScript(original.ID)
	renamed := cloneScriptDefinition(stored.Script)
	renamed.Name = "Orders (team)"
	if _, updated, err := server.runner.UpdateScriptDurable(original.ID, renamed); err != nil || !updated {
		t.Fatalf("rename: updated=%v err=%v", updated, err)
	}
	second := reimportOpenAPI(t, server, "/v2/orders")
	match := second.ExistingScripts[0]
	replace := func(expected time.Time) *httptest.ResponseRecorder {
		return postOpenAPIReimportCommit(t, server, map[string]any{
			"script": second.Script, "mode": "replace", "target_script_id": match.ScriptID,
			"replace_mode": "full", "expected_updated_at": expected,
		})
	}
	if response := replace(match.UpdatedAt.Add(-time.Second)); response.Code != http.StatusConflict ||
		!strings.Contains(response.Body.String(), importUpdateTargetStaleCode) {
		t.Fatalf("stale replace status = %d body=%.300s, want 409 %s", response.Code, response.Body.String(), importUpdateTargetStaleCode)
	}
	if current, _ := server.runner.GetScript(original.ID); current.Script.Actions[0].Target != stored.Script.Actions[0].Target {
		t.Fatal("a refused replace changed the script")
	}
	response := replace(match.UpdatedAt)
	if response.Code != http.StatusOK {
		t.Fatalf("replace status = %d body=%.300s", response.Code, response.Body.String())
	}
	current, _ := server.runner.GetScript(original.ID)
	if current.Name != "Orders (team)" || current.Script.Actions[0].Target == stored.Script.Actions[0].Target {
		t.Fatalf("replaced script = %q %q, want the new steps under the operator's name", current.Name, current.Script.Actions[0].Target)
	}
}

// A save retried with the same operation after its update committed answers
// with the updated script instead of refusing the now-changed target.
func TestReimportUpdateRetryReplaysTheSave(t *testing.T) {
	server := newReimportTestServer(t)
	original := savedReimportScript(t, saveReimport(t, server, "retry-first", reimportHAR(t, server, "checkout.har", "/orders").Script, nil))
	again := reimportHAR(t, server, "checkout.har", "/orders/v2")
	target := &importUpdateTarget{ScriptID: again.ExistingScripts[0].ScriptID, ExpectedUpdatedAt: again.ExistingScripts[0].UpdatedAt}
	updated := savedReimportScript(t, saveReimport(t, server, "retry-second", again.Script, target))
	retried := saveReimport(t, server, "retry-second", again.Script, target)
	if retried.Code != http.StatusCreated && retried.Code != http.StatusOK {
		t.Fatalf("retry status = %d body=%.300s, want the save replayed", retried.Code, retried.Body.String())
	}
	if !strings.Contains(retried.Body.String(), original.ID) {
		t.Fatalf("retry body = %.300s, want the updated script %s", retried.Body.String(), original.ID)
	}
	if current, _ := server.runner.GetScript(original.ID); current.RefKey != updated.RefKey {
		t.Fatalf("ref key after retry = %q, want %q (no further version)", current.RefKey, updated.RefKey)
	}
}

// An update is pending until the script holds what the save writes, which an
// unchanged source (an update that changes nothing) already does.
func TestImportUpdatesPendingComparesWhatTheSaveWrites(t *testing.T) {
	script := func(target string) *dsl.Script {
		return &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Name: "Request", Type: "request", Method: http.MethodGet, Target: target}}}
	}
	targets := []*importUpdateTarget{{ScriptID: "a", ExpectedUpdatedAt: time.Now()}}
	if !importUpdatesPending([]*StoredScript{{Script: script("/v1")}}, []*dsl.Script{script("/v2")}, targets) {
		t.Fatal("a script still at its earlier content was taken as updated")
	}
	if importUpdatesPending([]*StoredScript{{Script: script("/v2")}}, []*dsl.Script{script("/v2")}, targets) {
		t.Fatal("a script holding what the save writes was taken as still pending")
	}
	if !importUpdatesPending([]*StoredScript{{}}, []*dsl.Script{script("/v2")}, targets) {
		t.Fatal("a stored asset without a script was taken as updated")
	}
}

// The reviewed version is the same update time to the microsecond Postgres
// keeps, or the same content.
func TestImportUpdateTargetReviewedComparesTimeOrContent(t *testing.T) {
	reviewed := time.Date(2026, 10, 15, 1, 2, 3, 123456789, time.UTC)
	script := &dsl.Script{Protocol: "http", UpdatedAt: reviewed.Truncate(time.Microsecond)}
	if !importUpdateTargetReviewed(script, importUpdateTarget{ExpectedUpdatedAt: reviewed}) {
		t.Fatal("a script read back at microsecond precision was taken as changed")
	}
	script.UpdatedAt = reviewed.Add(time.Second)
	if importUpdateTargetReviewed(script, importUpdateTarget{ExpectedUpdatedAt: reviewed}) {
		t.Fatal("a later version was taken as the one reviewed")
	}
	digest, err := importedScriptContentDigest(script)
	if err != nil {
		t.Fatal(err)
	}
	if !importUpdateTargetReviewed(script, importUpdateTarget{ExpectedUpdatedAt: reviewed, ExpectedContentDigest: digest}) {
		t.Fatal("a later version with the reviewed content was taken as changed")
	}
}

type failCreatesStore struct {
	*libraryAtomicForwardingStore
	fail bool
}

func (s *failCreatesStore) CreateScriptIfAbsent(script *dsl.Script) (bool, error) {
	if s.fail {
		return false, errors.New("injected create failure")
	}
	return s.libraryAtomicForwardingStore.CreateScriptIfAbsent(script)
}

func (s *failCreatesStore) ListImportedScriptIdentities(projectID, source string) ([]storage.ImportedScriptIdentity, error) {
	return s.libraryAtomicForwardingStore.Store.(importedScriptIdentityLister).ListImportedScriptIdentities(projectID, source)
}

func asyncAPIReimportScript(name, step string) map[string]any {
	script := asyncAPIBatchTestScript(name, storage.DefaultProjectID, "")
	script["metadata"] = map[string]any{"import": map[string]any{"source": "asyncapi", "preserved": map[string]any{
		dsl.ImportSourceDocumentKey: "Orders events", dsl.ImportSourceDocumentDeclaredKey: true, dsl.ImportSourceItemKey: name,
	}}}
	script["actions"].([]any)[0].(map[string]any)["name"] = step
	return script
}

func asyncAPIReimportScripts(t *testing.T, response *httptest.ResponseRecorder) []StoredScript {
	t.Helper()
	if response.Code != http.StatusCreated {
		t.Fatalf("AsyncAPI batch status = %d body=%.400s", response.Code, response.Body.String())
	}
	var decoded struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded.Scripts
}

// An AsyncAPI batch updates the scripts of each protocol group as new
// versions, and a batch that fails after an update puts it back.
func TestReimportAsyncAPIBatchUpdatesAndRollsBack(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "asyncapi-reimport.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Close() })
	store := &failCreatesStore{libraryAtomicForwardingStore: &libraryAtomicForwardingStore{Store: base}}
	server := NewServer(ServerConfig{Store: store})
	first := asyncAPIReimportScripts(t, postAsyncAPIBatch(t, server, map[string]any{
		"operation_id": "asyncapi-reimport-first",
		"scripts":      []any{asyncAPIReimportScript("Orders", "publish v1"), asyncAPIReimportScript("Refunds", "publish v1")},
	}))
	targetFor := func(id string) map[string]any {
		current, ok := server.runner.GetScript(id)
		if !ok {
			t.Fatalf("script %s is gone", id)
		}
		digest, err := importedScriptContentDigest(current.Script)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]any{"script_id": id, "expected_updated_at": current.Script.UpdatedAt, "expected_content_digest": digest}
	}
	staleTarget := targetFor(first[0].ID)
	staleTarget["expected_updated_at"] = staleTarget["expected_updated_at"].(time.Time).Add(-time.Second)
	delete(staleTarget, "expected_content_digest")
	if stale := postAsyncAPIBatch(t, server, map[string]any{
		"operation_id":   "asyncapi-reimport-stale",
		"scripts":        []any{asyncAPIReimportScript("Orders", "publish v2")},
		"update_targets": []any{staleTarget},
	}); stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), importUpdateTargetStaleCode) {
		t.Fatalf("update of a script changed after review = %d %.300s, want 409", stale.Code, stale.Body.String())
	}
	updated := asyncAPIReimportScripts(t, postAsyncAPIBatch(t, server, map[string]any{
		"operation_id":   "asyncapi-reimport-second",
		"scripts":        []any{asyncAPIReimportScript("Orders", "publish v2"), asyncAPIReimportScript("Refunds", "publish v2")},
		"update_targets": []any{targetFor(first[0].ID), targetFor(first[1].ID)},
	}))
	for index := range first {
		if updated[index].ID != first[index].ID || updated[index].Script.Actions[0].Name != "publish v2" {
			t.Fatalf("script %d = %s %q, want %s updated in place", index+1, updated[index].ID, updated[index].Script.Actions[0].Name, first[index].ID)
		}
	}

	third := map[string]any{
		"operation_id":   "asyncapi-reimport-third",
		"scripts":        []any{asyncAPIReimportScript("Orders", "publish v3"), asyncAPIReimportScript("Shipping", "publish v1")},
		"update_targets": []any{targetFor(first[0].ID), nil},
	}
	store.fail = true
	response := postAsyncAPIBatch(t, server, third)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("failed batch status = %d body=%.300s", response.Code, response.Body.String())
	}
	current, _ := server.runner.GetScript(first[0].ID)
	if current.Script.Actions[0].Name != "publish v2" {
		t.Fatalf("after the failed batch the script has %q, want its update rolled back", current.Script.Actions[0].Name)
	}
	if current.RefKey == updated[0].RefKey {
		t.Fatal("the failed batch never applied its update, so nothing was rolled back")
	}
	if scripts, err := base.ListScripts(storage.DefaultProjectID, nil); err != nil || len(scripts) != 2 {
		t.Fatalf("project scripts = %d (%v), want the failed create gone", len(scripts), err)
	}

	// Putting the script back is a further version, but its content is the
	// one reviewed, so retrying the same save goes through.
	store.fail = false
	retried := asyncAPIReimportScripts(t, postAsyncAPIBatch(t, server, third))
	if retried[0].ID != first[0].ID || retried[0].Script.Actions[0].Name != "publish v3" {
		t.Fatalf("retried script 1 = %s %q, want %s updated", retried[0].ID, retried[0].Script.Actions[0].Name, first[0].ID)
	}
}

// A save whose update committed but whose receipt was lost is replayed by a
// retry of the same operation, not refused because the script moved on.
func TestReimportRetryAdoptsAnUpdateItsEarlierAttemptApplied(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "reimport-lost-receipt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { base.Close() })
	store := &failOnceDirectImportReceiptStore{SQLiteStore: base}
	server := NewServer(ServerConfig{Store: store})
	original := savedReimportScript(t, saveReimport(t, server, "lost-first", reimportHAR(t, server, "checkout.har", "/orders").Script, nil))
	again := reimportHAR(t, server, "checkout.har", "/orders/v2")
	match := again.ExistingScripts[0]
	target := &importUpdateTarget{ScriptID: match.ScriptID, ExpectedUpdatedAt: match.UpdatedAt, ExpectedContentDigest: match.ContentDigest}

	// The failing save is the store's first receipt claim, so the retry finds
	// its receipt expired and takes it over.
	store.claims = 0
	store.failComplete = true
	if lost := saveReimport(t, server, "lost-second", again.Script, target); lost.Code != http.StatusInternalServerError {
		t.Fatalf("lost receipt status = %d body=%.300s, want 500", lost.Code, lost.Body.String())
	}
	applied, _ := server.runner.GetScript(original.ID)
	if applied.Script.Actions[0].Target != "/orders/v2" {
		t.Fatal("the update did not commit before the receipt was lost")
	}
	retried := savedReimportScript(t, saveReimport(t, server, "lost-second", again.Script, target))
	if retried.ID != original.ID || retried.RefKey != applied.RefKey {
		t.Fatalf("retry = %s %s, want %s replayed at %s", retried.ID, retried.RefKey, original.ID, applied.RefKey)
	}
}

// A scenario group the draft leaves unnamed is named after the name an
// updated script keeps, not the imported one.
func TestJMeterScenarioGroupsTakeTheKeptScriptName(t *testing.T) {
	scripts := []*dsl.Script{{ID: "script-buyers", Name: "Buyers", Protocol: "http"}}
	draft := &jmeter.ScenarioDraft{Name: "Checkout", Duration: "1m", ScenarioMode: "vuser_driven",
		Groups: []jmeter.ScenarioDraftGroup{{ScriptName: "Buyers", VUsers: 1}}}
	request, err := buildJMeterScenarioRequestNamed(scripts, draft, map[string]string{"script-buyers": "Buyers (team)"})
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Groups) != 1 || request.Groups[0].Name != "Buyers (team)" || request.Groups[0].ScriptID != "script-buyers" {
		t.Fatalf("groups = %+v, want one named after the kept script name", request.Groups)
	}
}
