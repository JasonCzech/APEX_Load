package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func authoringAPIRequest(t *testing.T, s *Server, method, path string, body any, tag string) *httptest.ResponseRecorder {
	t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	if tag != "" {
		req.Header.Set("If-Match", tag)
	}
	response := httptest.NewRecorder()
	s.mux.ServeHTTP(response, req)
	return response
}
func requireAuthoringAPIProjection(t *testing.T, s *Server, id string) (*ScriptAuthoringProjection, string) {
	t.Helper()
	response := authoringAPIRequest(t, s, http.MethodGet, "/api/v1/scripts/"+id+"?view=authoring", nil, "")
	if response.Code != http.StatusOK {
		t.Fatalf("authoring GET: %d %s", response.Code, response.Body.String())
	}
	var projection ScriptAuthoringProjection
	if err := json.Unmarshal(response.Body.Bytes(), &projection); err != nil {
		t.Fatal(err)
	}
	tag, err := scriptAuthoringETag(projection.ContentRevision)
	if err != nil || response.Header().Get("ETag") != tag || projection.ProjectionVersion != scriptAuthoringProjectionVersion || response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal("unpaired or unqualified projection/tag")
	}
	return &projection, tag
}
func authoringAPIScript(id string) *dsl.Script {
	goal := 217.5
	return &dsl.Script{ID: id, ProjectID: storage.DefaultProjectID, Name: "Authoring Flow", Version: "1", Protocol: "http", GoalConfig: &dsl.GoalConfig{TargetAverageTPS: &goal}, Config: dsl.ScriptConfig{BaseURL: "https://example.com"}, Actions: []dsl.Action{{Name: "Open", Type: "request", Method: "GET", Target: "/"}}}
}
func TestScriptAuthoringAPIConditionedSaveAndStalePlacement(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	create := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts", map[string]any{"script": authoringAPIScript("authoring-api")}, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	var created StoredScript
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	first, tag := requireAuthoringAPIProjection(t, server, created.ID)
	if created.AuthoringProjection == nil || created.ContentRevision != first.ContentRevision || create.Header().Get("ETag") != "" {
		t.Fatal("create returned an unpaired or byte-strong enriched response")
	}
	candidate := authoringAPIScript(created.ID)
	candidate.Name = "Saved"
	candidate.CreatedAt = time.Now().Add(24 * time.Hour)
	update := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+created.ID, map[string]any{"script": candidate}, tag)
	if update.Code != http.StatusOK || update.Header().Get("X-APEX-Script-Save-Mode") != "conditioned" || update.Header().Get("ETag") != "" {
		t.Fatalf("conditioned save: %d %s", update.Code, update.Body.String())
	}
	var saved StoredScript
	if err := json.Unmarshal(update.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	next, nextTag := requireAuthoringAPIProjection(t, server, created.ID)
	if next.ContentRevision == first.ContentRevision || saved.AuthoringProjection == nil || saved.ContentRevision != next.ContentRevision || saved.Script.CreatedAt != created.Script.CreatedAt || *next.Script.GoalConfig.TargetAverageTPS != 217.5 {
		t.Fatal("save snapshot/incarnation/goal fidelity failed")
	}
	// The early stale precondition wins even over an invalid/raw-secret payload.
	invalid := authoringAPIScript(created.ID)
	invalid.Actions[0].Type = "invalid"
	invalid.Actions[0].Headers = map[string]string{"Authorization": "Bearer do-not-disclose"}
	stale := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+created.ID, map[string]any{"script": invalid}, tag)
	if stale.Code != http.StatusPreconditionFailed || strings.Contains(stale.Body.String(), "do-not-disclose") {
		t.Fatalf("stale save not refused before source work: %d %s", stale.Code, stale.Body.String())
	}
	if err := store.SaveApplication(&storage.Application{ID: "authoring-app", ProjectID: storage.DefaultProjectID, Name: "App", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	appID := "authoring-app"
	if _, _, err := store.ReplaceLibraryAssignment("script", created.ID, storage.DefaultProjectID, &appID, nil, 0); err != nil {
		t.Fatal(err)
	}
	placement, placementTag := requireAuthoringAPIProjection(t, server, created.ID)
	if placementTag == nextTag || placement.LibraryPlacement.ApplicationID == nil || *placement.LibraryPlacement.ApplicationID != "authoring-app" {
		t.Fatal("placement not part of authoring nonce")
	}
	blocked := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+created.ID, map[string]any{"script": candidate}, nextTag)
	if blocked.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale placement save: %d %s", blocked.Code, blocked.Body.String())
	}
	current, _ := requireAuthoringAPIProjection(t, server, created.ID)
	if current.ContentRevision != placement.ContentRevision || current.Script.Name != "Saved" {
		t.Fatal("rejected save changed authored state")
	}
}
func TestScriptAuthoringAPIStrictPolicyAndHeaderAdmission(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	if _, err := server.runner.CreateScriptAuthoringDurable(authoringAPIScript("strict-script")); err != nil {
		t.Fatal(err)
	}
	_, tag := requireAuthoringAPIProjection(t, server, "strict-script")
	project, err := store.GetProject(storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.Authoring = &storage.ProjectAuthoringPolicy{RequireScriptSaveIfMatch: true}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, tag string
		status    int
	}{{"missing", "", 428}, {"wildcard", "*", 400}, {"weak", "W/" + tag, 400}, {"list", tag + "," + tag, 400}, {"correct", tag, 200}} {
		t.Run(test.name, func(t *testing.T) {
			res := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/strict-script", map[string]any{"script": authoringAPIScript("strict-script")}, test.tag)
			if res.Code != test.status {
				t.Fatalf("status=%d wanted=%d body=%s", res.Code, test.status, res.Body.String())
			}
		})
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "script.update", Limit: 100})
	if err != nil || len(events) != 1 {
		t.Fatalf("refused saves produced mutation audit: %#v %v", events, err)
	}
}
func TestScriptAuthoringAPILegacyAuditAndCreateOnly(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	create := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts", map[string]any{"script": authoringAPIScript("ordinary-id")}, "")
	if create.Code != 201 {
		t.Fatal(create.Body.String())
	}
	initial, _ := requireAuthoringAPIProjection(t, server, "ordinary-id")
	for _, name := range []string{"Authoring Flow", "Overwrite"} {
		candidate := authoringAPIScript("ordinary-id")
		candidate.Name = name
		duplicate := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts", map[string]any{"script": candidate}, "")
		if duplicate.Code != 409 {
			t.Fatalf("ordinary create replaced/replayed existing row: %d %s", duplicate.Code, duplicate.Body.String())
		}
	}
	current, _ := requireAuthoringAPIProjection(t, server, "ordinary-id")
	if current.ContentRevision != initial.ContentRevision {
		t.Fatal("duplicate create mutated existing row")
	}
	candidate := authoringAPIScript("ordinary-id")
	candidate.Name = "Legacy saved"
	update := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/ordinary-id", map[string]any{"script": candidate}, "")
	if update.Code != 200 || update.Header().Get("X-APEX-Script-Save-Mode") != "legacy_unconditioned" {
		t.Fatalf("legacy mode not explicit: %d %s", update.Code, update.Body.String())
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "script.update", Limit: 100})
	if err != nil || len(events) != 2 {
		t.Fatalf("legacy intent/outcome missing: %#v %v", events, err)
	}
	if events[0].Details["operation_id"] == "" || events[0].Details["operation_id"] != events[1].Details["operation_id"] || events[0].Details["outcome"] != "committed" || events[1].Details["outcome"] != "pending" || events[0].Details["save_mode"] != "legacy_unconditioned" {
		t.Fatal("legacy audit provenance is not paired")
	}
}
func TestScriptAuthoringAPIConcurrentCreateAndDurableCacheBypass(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	var group sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := server.runner.CreateScriptAuthoringDurable(authoringAPIScript("concurrent-api-create"))
			results <- err
		}()
	}
	group.Wait()
	close(results)
	created, conflicted := 0, 0
	for err := range results {
		if err == nil {
			created++
		} else if errors.Is(err, errScriptAuthoringAlreadyExists) {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	if created != 1 || conflicted != 1 {
		t.Fatalf("concurrent create outcomes=%d/%d", created, conflicted)
	}
	fresh := authoringAPIScript("other-replica-script")
	if err := store.SaveScript(fresh); err != nil {
		t.Fatal(err)
	}
	projection, tag := requireAuthoringAPIProjection(t, server, fresh.ID)
	candidate := cloneScriptDefinition(fresh)
	candidate.Name = "Observed from durable row"
	saved := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+fresh.ID, map[string]any{"script": candidate}, tag)
	if saved.Code != 200 {
		t.Fatalf("durable row absent from local cache could not save: %d %s", saved.Code, saved.Body.String())
	}
	current, _ := requireAuthoringAPIProjection(t, server, fresh.ID)
	if current.ContentRevision == projection.ContentRevision || current.Script.Name != candidate.Name {
		t.Fatal("durable cache bypass lost revision")
	}
}

// Capability and fault delegates keep all persistence against real SQLite.
// Overrides isolate optional-interface admission and commit/audit uncertainty.
type scriptRevisionUnsupportedStore struct{ Store storage.Store }
type scriptAuthoringFaultStore struct {
	SQLiteStore             *storage.SQLiteStore
	auditCalls, failAuditAt int
	beforeMutation          func()
	postCommitError         bool
}

func (s *scriptAuthoringFaultStore) SaveAuditEvent(event *storage.AuditEvent) error {
	s.auditCalls++
	if s.auditCalls == s.failAuditAt {
		return errors.New("controlled audit transport failure")
	}
	return s.SQLiteStore.SaveAuditEvent(event)
}
func (s *scriptAuthoringFaultStore) UpdateScriptAuthoringIfCurrent(candidate *dsl.Script, expected storage.ScriptAuthoringPrecondition) (*storage.ScriptAuthoringMutationResult, error) {
	if s.beforeMutation != nil {
		hook := s.beforeMutation
		s.beforeMutation = nil
		hook()
	}
	result, err := s.SQLiteStore.UpdateScriptAuthoringIfCurrent(candidate, expected)
	if err == nil && result != nil && result.Applied && s.postCommitError {
		return nil, errors.New("controlled post-commit transport failure")
	}
	return result, err
}
func TestScriptAuthoringAPIUnsupportedStoreCannotBypassStrictPolicy(t *testing.T) {
	_, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	script := authoringAPIScript("unsupported-revision")
	if err := store.SaveScript(script); err != nil {
		t.Fatal(err)
	}
	project, err := store.GetProject(storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.Authoring = &storage.ProjectAuthoringPolicy{RequireScriptSaveIfMatch: true}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: storeOverlay(scriptRevisionUnsupportedStore{Store: store})})
	// Legacy startup may backfill a missing reference key. Capture mutation
	// authority after setup, immediately before the requests under test.
	before, err := store.GetScriptAuthoringSnapshot(script.ID)
	if err != nil {
		t.Fatal(err)
	}
	candidate := authoringAPIScript(script.ID)
	candidate.Name = "Must not save"
	for _, tc := range []struct {
		tag  string
		want int
	}{{"", 428}, {`"script-authoring-v1:` + before.ContentRevision + `"`, 503}} {
		response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+script.ID, map[string]any{"script": candidate}, tc.tag)
		if response.Code != tc.want {
			t.Fatalf("unsupported strict store admitted save: %d %s", response.Code, response.Body.String())
		}
	}
	after, err := store.GetScriptAuthoringSnapshot(script.ID)
	if err != nil || after.ContentRevision != before.ContentRevision || after.Script.Name != before.Script.Name {
		t.Fatalf("unsupported store mutated strict resource: before=%#v after=%#v err=%v", before, after, err)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "script.update", Limit: 100})
	if err != nil || len(events) != 0 {
		t.Fatal("refused unsupported strict mutation produced success/intent audit")
	}
	get := authoringAPIRequest(t, server, http.MethodGet, "/api/v1/scripts/"+script.ID+"?view=authoring", nil, "")
	if get.Code != 503 || get.Header().Get("ETag") != "" {
		t.Fatal("unsupported store advertised canonical projection")
	}
}
func TestScriptAuthoringAPIRequiredAuditFailuresAndUnknownCommit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failAt     int
		postCommit bool
		mutated    bool
		outcome    string
	}{{"intent_failure", 1, false, false, ""}, {"outcome_failure", 2, false, true, ""}, {"commit_transport_uncertainty", 0, true, true, "unknown"}} {
		t.Run(tc.name, func(t *testing.T) {
			_, base := newAuditTestServer(t)
			t.Cleanup(func() { base.Close() })
			if err := base.SaveScript(authoringAPIScript("audit-fault")); err != nil {
				t.Fatal(err)
			}
			fault := &scriptAuthoringFaultStore{SQLiteStore: base, failAuditAt: tc.failAt, postCommitError: tc.postCommit}
			server := NewServer(ServerConfig{Store: sqliteOverlay(fault)})
			before, _ := requireAuthoringAPIProjection(t, server, "audit-fault")
			candidate := authoringAPIScript("audit-fault")
			candidate.Name = "Committed candidate"
			response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/audit-fault", map[string]any{"script": candidate}, "")
			if response.Code != 503 {
				t.Fatalf("unqualified mutation acknowledged: %d %s", response.Code, response.Body.String())
			}
			after, _ := requireAuthoringAPIProjection(t, server, "audit-fault")
			if (after.ContentRevision != before.ContentRevision) != tc.mutated {
				t.Fatal("audit/commit fault did not preserve expected durable outcome")
			}
			if tc.mutated && !strings.Contains(response.Body.String(), "reload") {
				t.Fatal("uncertain response lacks reload-before-retry guidance")
			}
			events, err := base.ListAuditEvents(storage.AuditEventFilter{Action: "script.update", Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			if tc.failAt == 1 && len(events) != 0 {
				t.Fatal("failed intent produced persistent audit or write")
			}
			if tc.failAt == 2 && (len(events) != 1 || events[0].Details["outcome"] != "pending") {
				t.Fatal("failed outcome must retain original intent")
			}
			if tc.postCommit && (len(events) != 2 || events[0].Status != "failure" || events[0].Details["outcome"] != tc.outcome) {
				t.Fatal("committed transport error falsely asserted not_committed")
			}
		})
	}
}
func TestScriptAuthoringAPIFinalCASRefusesConcurrentSecretBindingChange(t *testing.T) {
	_, base := newAuditTestServer(t)
	t.Cleanup(func() { base.Close() })
	original := authoringAPIScript("secret-fence")
	original.Actions[0].Headers = map[string]string{"Authorization": "Bearer {{secret.ORIGINAL_TOKEN}}"}
	if err := base.SaveScript(original); err != nil {
		t.Fatal(err)
	}
	fault := &scriptAuthoringFaultStore{SQLiteStore: base}
	server := NewServer(ServerConfig{Store: sqliteOverlay(fault)})
	_, tag := requireAuthoringAPIProjection(t, server, original.ID)
	fault.beforeMutation = func() {
		winner := cloneScriptDefinition(original)
		winner.Actions[0].Headers["Authorization"] = "Bearer {{secret.ROTATED_TOKEN}}"
		winner.Name = "Concurrent winner"
		if err := base.SaveScript(winner); err != nil {
			t.Fatal(err)
		}
	}
	candidate := cloneScriptDefinition(original)
	candidate.Name = "Stale candidate"
	response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+original.ID, map[string]any{"script": candidate}, tag)
	if response.Code != 412 {
		t.Fatalf("final CAS allowed late secret-binding overwrite: %d %s", response.Code, response.Body.String())
	}
	durable, err := base.GetScript(original.ID)
	if err != nil || durable.Name != "Concurrent winner" || durable.Actions[0].Headers["Authorization"] != "Bearer {{secret.ROTATED_TOKEN}}" {
		t.Fatal("late save lost concurrent secret binding")
	}
}
func TestScriptAuthoringAPIForeignScopePrecedesPreconditions(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	if _, err := server.runner.CreateScriptAuthoringDurable(authoringAPIScript("scope-script")); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"", "*", `"script-authoring-v1:scr-v1-` + strings.Repeat("a", 64) + `"`} {
		request := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/scope-script", strings.NewReader(`{"script":null}`))
		request.SetPathValue("id", "scope-script")
		bindAuthorizedProjectToRequest(request, "foreign-project")
		if tag != "" {
			request.Header.Set("If-Match", tag)
		}
		response := httptest.NewRecorder()
		server.handleUpdateScript(response, request)
		if response.Code != 404 || strings.Contains(response.Body.String(), "revision") || strings.Contains(response.Body.String(), "If-Match") {
			t.Fatalf("foreign precondition disclosed: %d %s", response.Code, response.Body.String())
		}
	}
	current, err := store.GetScript("scope-script")
	if err != nil || current.Name != "Authoring Flow" {
		t.Fatal("foreign save changed content")
	}
}
func TestScriptAuthoringAPIRefKeyAcrossReplicasAndCanonicalSave(t *testing.T) {
	owner, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	stale := NewServer(ServerConfig{Store: store})
	created, err := owner.runner.CreateScriptAuthoringDurable(authoringAPIScript("replica-refkey"))
	if err != nil {
		t.Fatal(err)
	}
	stale.runner.mu.RLock()
	_, aliasExists := stale.runner.refKeyIndex[normalizeRefKeyCandidate(created.RefKey)]
	stale.runner.mu.RUnlock()
	if aliasExists {
		t.Fatal("fixture unexpectedly has current replica alias")
	}
	var tag string
	for _, alias := range []string{created.RefKey, strings.ToLower(created.RefKey), strings.ToUpper(created.RefKey)} {
		projection, currentTag := requireAuthoringAPIProjection(t, stale, alias)
		if projection.ID != created.ID || projection.ContentRevision != created.ContentRevision {
			t.Fatal("current alias did not resolve coherent durable snapshot")
		}
		if tag != "" && tag != currentTag {
			t.Fatal("case aliases returned different authoring authority")
		}
		tag = currentTag
	}
	candidate := authoringAPIScript(created.ID)
	candidate.Name = "RefKey conditioned edit"
	saved := authoringAPIRequest(t, stale, http.MethodPut, "/api/v1/scripts/"+created.RefKey, map[string]any{"script": candidate}, tag)
	if saved.Code != 200 {
		t.Fatalf("canonical RefKey save failed: %d %s", saved.Code, saved.Body.String())
	}
	var stored StoredScript
	if err := json.Unmarshal(saved.Body.Bytes(), &stored); err != nil {
		t.Fatal(err)
	}
	other, otherTag := requireAuthoringAPIProjection(t, owner, stored.RefKey)
	if other.ID != created.ID || other.Script.Name != candidate.Name || otherTag == tag {
		t.Fatal("updated alias on opposite stale replica did not resolve final snapshot")
	}
}
func TestScriptAuthoringAPIExactIDWinsOverAnotherScriptsReference(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	first, err := server.runner.CreateScriptAuthoringDurable(authoringAPIScript("reference-owner"))
	if err != nil {
		t.Fatal(err)
	}
	create := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts", map[string]any{"script": authoringAPIScript(first.RefKey)}, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("ordinary create resolved primary ID as display alias: %d %s", create.Code, create.Body.String())
	}
	var second StoredScript
	if err := json.Unmarshal(create.Body.Bytes(), &second); err != nil {
		t.Fatal(err)
	}
	if second.ID != first.RefKey || second.RefKey == first.RefKey {
		t.Fatal("fixture did not create independent exact-ID and reference owners")
	}
	read, tag := requireAuthoringAPIProjection(t, server, second.ID)
	if read.ID != second.ID || read.ContentRevision != second.ContentRevision {
		t.Fatal("authoring read did not preserve the exact durable ID")
	}
	candidate := authoringAPIScript(second.ID)
	candidate.Name = "Exact ID saved"
	response := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+second.ID, map[string]any{"script": candidate}, tag)
	if response.Code != http.StatusOK {
		t.Fatalf("exact-ID conditioned save rebound to alias: %d %s", response.Code, response.Body.String())
	}
	updated, err := store.GetScriptAuthoringSnapshot(second.ID)
	if err != nil || updated.Script.Name != candidate.Name || updated.ContentRevision == second.ContentRevision {
		t.Fatalf("exact-ID owner was not updated: %#v %v", updated, err)
	}
	untouched, err := store.GetScriptAuthoringSnapshot(first.ID)
	if err != nil || untouched.Script.Name != first.Script.Name || untouched.ContentRevision != first.ContentRevision {
		t.Fatalf("reference owner was changed: %#v %v", untouched, err)
	}
}

func TestScriptAuthoringAPIApprovedOriginsExposeOnlySafeRevisionHeaders(t *testing.T) {
	for _, origin := range []string{"wails://wails.localhost", "http://wails.localhost", "https://dashboard.example.com"} {
		request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/v1/scripts/script?view=authoring", nil)
		request.Header.Set("Origin", origin)
		response := httptest.NewRecorder()
		decision := applyCORSOriginHeaders(response, request, []string{origin})
		if !decision.allowed || response.Header().Get("Access-Control-Allow-Origin") != origin {
			t.Fatal("approved desktop/dashboard origin rejected")
		}
		exposed := strings.Split(response.Header().Get("Access-Control-Expose-Headers"), ",")
		names := map[string]bool{}
		for _, name := range exposed {
			names[strings.TrimSpace(name)] = true
		}
		if len(names) != 8 || !names["X-APEXLoad-Replay-Durability"] || !names["ETag"] || !names["X-APEXLOAD-Audit-Status"] || !names["X-APEX-Script-Save-Mode"] || !names["X-APEX-Scenario-Save-Mode"] || !names["X-APEX-Original-Content-Revision"] || !names["Idempotent-Replayed"] || !names["X-Request-ID"] || names["*"] {
			t.Fatal("safe revision headers unreadable or wildcard exposure broadened")
		}
		denied := httptest.NewRecorder()
		applyCORSOriginHeaders(denied, request, nil)
		if denied.Header().Get("Access-Control-Expose-Headers") != "" || denied.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("unapproved origin received revision exposure")
		}
	}
}

// A5: draft validation echoes the client's fingerprint so a result binds to
// exactly the document it describes; stored validation names the durable
// revision it read.
func TestScriptValidationEchoesItsBinding(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })
	decode := func(response *httptest.ResponseRecorder) scriptValidationResponse {
		t.Helper()
		var body scriptValidationResponse
		if response.Code != http.StatusOK {
			t.Fatalf("validate: %d %s", response.Code, response.Body.String())
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	draft := authoringAPIScript("")
	echoed := decode(authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/validate", map[string]any{"script": draft, "fingerprint": "sdf1-0123456789abcdef-1f"}, ""))
	if echoed.ValidatedFingerprint != "sdf1-0123456789abcdef-1f" || echoed.ValidatedContentRevision != "" {
		t.Fatalf("draft echo = %q/%q", echoed.ValidatedFingerprint, echoed.ValidatedContentRevision)
	}
	if plain := decode(authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/validate", map[string]any{"script": draft}, "")); plain.ValidatedFingerprint != "" {
		t.Fatalf("an omitted fingerprint echoed %q", plain.ValidatedFingerprint)
	}
	for _, bad := range []string{strings.Repeat("a", 129), "has space", "tab\there", "nön-ascii"} {
		if response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/validate", map[string]any{"script": draft, "fingerprint": bad}, ""); response.Code != http.StatusBadRequest {
			t.Fatalf("fingerprint %q: %d", bad, response.Code)
		}
	}

	create := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts", map[string]any{"script": authoringAPIScript("validated-revision")}, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", create.Code, create.Body.String())
	}
	projection, tag := requireAuthoringAPIProjection(t, server, "validated-revision")
	stored := decode(authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/validated-revision/validate", nil, ""))
	if stored.ValidatedContentRevision != projection.ContentRevision || stored.ValidatedFingerprint != "" {
		t.Fatalf("stored echo = %q, want %q", stored.ValidatedContentRevision, projection.ContentRevision)
	}
	candidate := authoringAPIScript("validated-revision")
	candidate.Name = "Renamed"
	if update := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/validated-revision", map[string]any{"script": candidate}, tag); update.Code != http.StatusOK {
		t.Fatalf("save: %d %s", update.Code, update.Body.String())
	}
	next, _ := requireAuthoringAPIProjection(t, server, "validated-revision")
	if again := decode(authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/validated-revision/validate", nil, "")); again.ValidatedContentRevision != next.ContentRevision || next.ContentRevision == projection.ContentRevision {
		t.Fatalf("revalidation echo = %q, want the new revision %q", again.ValidatedContentRevision, next.ContentRevision)
	}
}
