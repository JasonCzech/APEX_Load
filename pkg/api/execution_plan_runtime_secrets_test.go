package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func validateExecutionPlanRow(t *testing.T, row map[string]any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{}
	return server.validateExecutionPlan(&storage.ExecutionPlan{
		Name:    "plan",
		Entries: []storage.ExecutionPlanEntry{{ScenarioID: "scenario", RuntimeOverrides: raw}},
	})
}

// Every secret-bearing field the run-side mask covers must be a project
// credential reference in a plan entry, since plan rows are stored and
// returned verbatim.
func TestValidateExecutionPlanRejectsTypedRuntimeCredentials(t *testing.T) {
	cases := []struct {
		name string
		path string
		row  map[string]any
	}{
		{"http auth password", "http.auth.password", map[string]any{"http": map[string]any{"auth": map[string]any{"type": "basic", "username": "ops", "password": "hunter2"}}}},
		{"http auth token", "http.auth.token", map[string]any{"http": map[string]any{"auth": map[string]any{"type": "bearer", "token": "hunter2"}}}},
		{"http proxy password", "http.proxy.password", map[string]any{"http": map[string]any{"proxy": map[string]any{"username": "ops", "password": "hunter2"}}}},
		{"http proxy url userinfo", "http.proxy.url", map[string]any{"http": map[string]any{"proxy": map[string]any{"url": "http://ops:hunter2@proxy.example.test:8080"}}}},
		{"authorization header", "http.default_headers.Authorization", map[string]any{"http": map[string]any{"default_headers": map[string]string{"Authorization": "Bearer hunter2"}}}},
		{"cookie header", "http.default_header_list.Cookie", map[string]any{"http": map[string]any{"default_header_list": []map[string]string{{"name": "Cookie", "value": "sid=hunter2"}}}}},
		{"base url query credential", "base_url", map[string]any{"base_url": "https://api.example.test/?access_token=hunter2"}},
		{"browser http credentials", "browser_config.http_credentials.password", map[string]any{"browser_config": map[string]any{"http_credentials": map[string]any{"username": "ops", "password": "hunter2"}}}},
		{"browser proxy password", "browser_config.proxy.password", map[string]any{"browser_config": map[string]any{"proxy": map[string]any{"server": "http://proxy.example.test:8080", "password": "hunter2"}}}},
		{"browser certificate passphrase", "browser_config.client_certificates.0.passphrase", map[string]any{"browser_config": map[string]any{"client_certificates": []map[string]any{{"origin": "https://app.example.test", "passphrase": "hunter2"}}}}},
		{"java vm parameter", "java_vuser.additional_vm_parameters.0", map[string]any{"java_vuser": map[string]any{"additional_vm_parameters": []string{"-Djavax.net.ssl.keyStorePassword=hunter2"}}}},
		{"env credential", "env.API_TOKEN", map[string]any{"env": map[string]string{"API_TOKEN": "hunter2"}}},
		{"soap password", "soap.ws_security.username_token.password", map[string]any{"soap": map[string]any{"ws_security": map[string]any{"username_token": map[string]any{"username": "svc", "password": "hunter2"}}}}},
		{"reserved reference", "http.auth.password", map[string]any{"http": map[string]any{"auth": map[string]any{"password": "{{secret.APEX_RUNTIME_OVERRIDE_G1_abc}}"}}}},
		{"partial row", "http.auth.password", map[string]any{"http": map[string]any{"auth": map[string]any{"password": "hunter2"}}, "override_fields": []string{"http.auth.password"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, message := validateExecutionPlanRow(t, tc.row)
			if status != http.StatusBadRequest || !strings.Contains(message, tc.path) {
				t.Fatalf("validation = (%d, %q), want 400 naming %s", status, message, tc.path)
			}
			if strings.Contains(message, "hunter2") {
				t.Fatalf("validation message echoes the credential: %q", message)
			}
		})
	}
}

func TestValidateExecutionPlanAcceptsRuntimeCredentialReferences(t *testing.T) {
	row := map[string]any{
		"http": map[string]any{
			"auth":            map[string]any{"type": "basic", "username": "ops", "password": "{{secret.API_PASSWORD}}", "token": "{{ secret.API_TOKEN }}"},
			"proxy":           map[string]any{"url": "http://{{secret.PROXY_USER}}:{{secret.PROXY_PASS}}@proxy.example.test:8080", "username": "ops", "password": "{{secret.PROXY_PASS}}"},
			"default_headers": map[string]string{"Authorization": "Bearer {{secret.API_TOKEN}}", "Accept": "application/json"},
			"default_header_list": []map[string]string{
				{"name": "Cookie", "value": "sid={{secret.SESSION_ID}}"},
				{"name": "X-Trace", "value": "on"},
			},
		},
		"base_url": "https://api.example.test/v1?access_token={{secret.API_TOKEN}}",
		"browser_config": map[string]any{
			"http_credentials":    map[string]any{"username": "ops", "password": "{{secret.BROWSER_PASS}}"},
			"client_certificates": []map[string]any{{"origin": "https://app.example.test", "passphrase": "{{secret.CERT_PASS}}"}},
		},
		"java_vuser": map[string]any{"additional_vm_parameters": []string{"-Djavax.net.ssl.keyStorePassword={{secret.KEYSTORE_PASS}}", "-Xmx512m"}},
		"env":        map[string]string{"API_TOKEN": "{{secret.API_TOKEN}}", "REGION": "us-east-1"},
		"soap":       map[string]any{"ws_security": map[string]any{"username_token": map[string]any{"username": "svc", "password": "{{secret.SOAP_PASS}}"}}},
	}
	if status, message := validateExecutionPlanRow(t, row); status != 0 {
		t.Fatalf("validation = (%d, %q), want accepted", status, message)
	}
	if status, message := validateExecutionPlanRow(t, map[string]any{"http": map[string]any{"auth": map[string]any{"password": ""}}}); status != 0 {
		t.Fatalf("an explicit empty credential = (%d, %q), want accepted", status, message)
	}
}

// A row stored before every field was checked is masked on read.
func TestMaskExecutionPlanForResponseMasksStoredTypedCredentials(t *testing.T) {
	typed := json.RawMessage(`{"http":{"auth":{"type":"basic","password":"hunter2"}}}`)
	reference := json.RawMessage(`{"http":{"auth":{"password":"{{secret.API_PASSWORD}}"}},"override_fields":["http.auth.password"]}`)
	plan := &storage.ExecutionPlan{Entries: []storage.ExecutionPlanEntry{
		{ScenarioID: "a", RuntimeOverrides: typed},
		{ScenarioID: "b", RuntimeOverrides: reference},
	}}
	masked := maskExecutionPlanForResponse(plan)
	if strings.Contains(string(masked.Entries[0].RuntimeOverrides), "hunter2") {
		t.Fatalf("masked row still holds the credential: %s", masked.Entries[0].RuntimeOverrides)
	}
	if !strings.Contains(string(masked.Entries[0].RuntimeOverrides), "[REDACTED]") {
		t.Fatalf("masked row = %s, want the redaction sentinel", masked.Entries[0].RuntimeOverrides)
	}
	if string(masked.Entries[1].RuntimeOverrides) != string(reference) {
		t.Fatalf("reference-only row = %s, want it returned as stored", masked.Entries[1].RuntimeOverrides)
	}
	if string(plan.Entries[0].RuntimeOverrides) != string(typed) {
		t.Fatal("masking mutated the stored plan")
	}
}

// A masked read of a legacy plan row, saved back with only the typed
// credentials replaced, keeps every other value as stored; a row that still
// sends the redaction placeholder is rejected instead of being stored.
func TestExecutionPlanMaskedReadRoundTripKeepsStoredValues(t *testing.T) {
	server, store := newTimeslotServer(t)
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", map[string]any{
		"project_id": "proj-a", "name": "nightly", "run_mode": "sequential",
		"entries": []map[string]any{{"order": 0, "scenario_id": "scn-1"}},
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created storage.ExecutionPlan
	_ = json.Unmarshal(create.Body.Bytes(), &created)
	stored, err := store.GetExecutionPlan(created.ID)
	if err != nil || stored == nil {
		t.Fatalf("GetExecutionPlan: %v", err)
	}
	// A row saved before every secret-bearing field was checked.
	stored.Entries[0].RuntimeOverrides = json.RawMessage(`{"http":{"auth":{"type":"basic","username":"alice","password":"pw","token":"{{secret.API_TOKEN}}"},"request_timeout_ms":9007199254740993},` +
		`"soap":{"ws_security":{"username_token":{"username":"svc","password":"{{secret.SOAP_PASS}}"}}},` +
		`"java_vuser":{"additional_vm_parameters":["-Xmx512m","-Djavax.net.ssl.keyStorePassword=hunter2"]},` +
		`"env":{"REGION":"us-east-1","API_TOKEN":"hunter2"}}`)
	if err := store.SaveExecutionPlan(stored); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}

	get := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans/"+created.ID, nil)
	if get.Code != http.StatusOK {
		t.Fatalf("get status = %d body=%s", get.Code, get.Body.String())
	}
	var read storage.ExecutionPlan
	_ = json.Unmarshal(get.Body.Bytes(), &read)
	var row map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(read.Entries[0].RuntimeOverrides)))
	decoder.UseNumber()
	if err := decoder.Decode(&row); err != nil {
		t.Fatal(err)
	}
	httpRow := row["http"].(map[string]any)
	auth := httpRow["auth"].(map[string]any)
	vm := row["java_vuser"].(map[string]any)["additional_vm_parameters"].([]any)
	env := row["env"].(map[string]any)
	soapUser := row["soap"].(map[string]any)["ws_security"].(map[string]any)["username_token"].(map[string]any)
	if auth["username"] != "alice" || auth["token"] != "{{secret.API_TOKEN}}" || soapUser["username"] != "svc" || soapUser["password"] != "{{secret.SOAP_PASS}}" ||
		vm[0] != "-Xmx512m" || env["REGION"] != "us-east-1" || httpRow["request_timeout_ms"].(json.Number).String() != "9007199254740993" {
		t.Fatalf("masked read changed values that are not typed credentials: %s", read.Entries[0].RuntimeOverrides)
	}
	if auth["password"] != "[REDACTED]" || vm[1] != "[REDACTED]" || env["API_TOKEN"] != "[REDACTED]" {
		t.Fatalf("masked read = %s, want the typed credentials redacted", read.Entries[0].RuntimeOverrides)
	}

	put := func(row map[string]any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(row)
		return timeslotRequest(t, server, http.MethodPut, "/api/v1/execution-plans/"+created.ID, map[string]any{
			"name": read.Name, "run_mode": read.RunMode, "revision": read.Revision,
			"entries": []map[string]any{{"order": 0, "scenario_id": "scn-1", "runtime_overrides": json.RawMessage(raw)}},
		})
	}
	// Only the password replaced: the masked VM parameter and env value are refused.
	auth["password"] = "{{secret.PW}}"
	if rec := put(row); rec.Code != http.StatusBadRequest ||
		!strings.Contains(rec.Body.String(), "java_vuser.additional_vm_parameters.1") || !strings.Contains(rec.Body.String(), "env.API_TOKEN") {
		t.Fatalf("put with placeholders = %d %s, want 400 naming them", rec.Code, rec.Body.String())
	}
	// A username sent back as the placeholder is refused too.
	auth["username"] = "[REDACTED]"
	vm[1] = "-Djavax.net.ssl.keyStorePassword={{secret.KEYSTORE_PASS}}"
	env["API_TOKEN"] = "{{secret.API_TOKEN}}"
	if rec := put(row); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "http.auth.username") {
		t.Fatalf("put with a placeholder username = %d %s, want 400", rec.Code, rec.Body.String())
	}
	auth["username"] = "alice"
	if rec := put(row); rec.Code != http.StatusOK {
		t.Fatalf("put with references = %d %s, want 200", rec.Code, rec.Body.String())
	}
	saved, err := store.GetExecutionPlan(created.ID)
	if err != nil || saved == nil {
		t.Fatalf("GetExecutionPlan: %v", err)
	}
	text := string(saved.Entries[0].RuntimeOverrides)
	for _, want := range []string{`"username":"alice"`, `"token":"{{secret.API_TOKEN}}"`, `"-Xmx512m"`, `"REGION":"us-east-1"`, `9007199254740993`, `"username":"svc"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("stored row = %s, missing %s", text, want)
		}
	}
	if strings.Contains(text, "REDACTED") || strings.Contains(text, "hunter2") {
		t.Fatalf("stored row = %s, want no placeholder or typed credential", text)
	}
}

func TestExecutionPlanRedactedPathsFindsEscapedPlaceholders(t *testing.T) {
	raw := json.RawMessage(`{"http":{"proxy":{"url":"http://%5BREDACTED%5D@proxy.example.test"}},"base_url":"https://api.example.test/?k=[REDACTED]","env":{"A":"ok"}}`)
	got := strings.Join(executionPlanRedactedPaths(raw), ",")
	if got != "base_url,http.proxy.url" {
		t.Fatalf("redacted paths = %q", got)
	}
}

// legacyTypedCredentialPlan stores a plan whose entry row was saved before
// every secret-bearing field was checked, so it holds typed credentials.
func legacyTypedCredentialPlan(t *testing.T, server *Server, store storage.Store) *storage.ExecutionPlan {
	t.Helper()
	return legacyExecutionPlanWithRow(t, server, store, json.RawMessage(`{"http":{"auth":{"type":"basic","username":"alice","password":"hunter2"}},"env":{"API_TOKEN":"abc-secret-token"}}`))
}

// legacyExecutionPlanWithRow stores a plan whose entry row is written straight
// to the store, bypassing validation, as a row saved by an earlier build.
func legacyExecutionPlanWithRow(t *testing.T, server *Server, store storage.Store, row json.RawMessage) *storage.ExecutionPlan {
	t.Helper()
	create := timeslotRequest(t, server, http.MethodPost, "/api/v1/execution-plans", map[string]any{
		"project_id": "proj-a", "name": "legacy", "run_mode": "sequential",
		"entries": []map[string]any{{"order": 0, "scenario_id": "scn-1"}},
	})
	if create.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", create.Code, create.Body.String())
	}
	var created storage.ExecutionPlan
	_ = json.Unmarshal(create.Body.Bytes(), &created)
	stored, err := store.GetExecutionPlan(created.ID)
	if err != nil || stored == nil {
		t.Fatalf("GetExecutionPlan: %v", err)
	}
	stored.Entries[0].RuntimeOverrides = row
	if err := store.SaveExecutionPlan(stored); err != nil {
		t.Fatalf("SaveExecutionPlan: %v", err)
	}
	reloaded, err := store.GetExecutionPlan(created.ID)
	if err != nil || reloaded == nil {
		t.Fatalf("GetExecutionPlan: %v", err)
	}
	return reloaded
}

// A run snapshot copies the legacy row; every run read masks it while the
// stored snapshot keeps the values resume replays.
func TestExecutionPlanRunReadsMaskLegacyTypedCredentials(t *testing.T) {
	server, store := newTimeslotServer(t)
	plan := legacyTypedCredentialPlan(t, server, store)
	now := time.Now().UTC()
	run := &storage.ExecutionPlanRun{
		ID: "run-legacy", PlanID: plan.ID, ProjectID: plan.ProjectID, RunMode: "sequential", Status: "completed",
		Entries: []storage.ExecutionPlanRunEntry{{
			Order: 0, ScenarioID: "scn-1", Status: "complete",
			RuntimeOverrides: append(json.RawMessage(nil), plan.Entries[0].RuntimeOverrides...),
		}},
		CreatedAt: now, UpdatedAt: now, CompletedAt: &now,
	}
	if err := store.SaveExecutionPlanRun(run); err != nil {
		t.Fatalf("SaveExecutionPlanRun: %v", err)
	}
	for _, path := range []string{
		"/api/v1/execution-plans/" + plan.ID + "/runs",
		"/api/v1/execution-plans/" + plan.ID + "/runs/" + run.ID,
		"/api/v1/execution-plans?project_id=proj-a&include_activity=true",
	} {
		rec := timeslotRequest(t, server, http.MethodGet, path, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body.String())
		}
		body := rec.Body.String()
		if strings.Contains(body, "hunter2") || strings.Contains(body, "abc-secret-token") {
			t.Fatalf("GET %s returned a typed credential: %s", path, body)
		}
		if !strings.Contains(body, "[REDACTED]") || !strings.Contains(body, `"username":"alice"`) {
			t.Fatalf("GET %s = %s, want the credentials masked and the username kept", path, body)
		}
	}
	if !strings.Contains(string(mustGetPlanRun(t, store, run.ID).Entries[0].RuntimeOverrides), "hunter2") {
		t.Fatal("masking a run view changed the stored snapshot resume replays")
	}
}

func mustGetPlanRun(t *testing.T, store storage.Store, id string) *storage.ExecutionPlanRun {
	t.Helper()
	run, err := store.GetExecutionPlanRun(id)
	if err != nil || run == nil {
		t.Fatalf("GetExecutionPlanRun: %v", err)
	}
	return run
}

// The plan list masks only its response: activity reads the stored plan, so a
// pending approval's content hash (taken over the unmasked rows) still matches.
func TestExecutionPlanListActivityKeepsApprovalForLegacyTypedCredentialPlan(t *testing.T) {
	server, store := newTimeslotServer(t)
	plan := legacyTypedCredentialPlan(t, server, store)
	now := time.Now().UTC()
	approval := &storage.ExecutionPlanApproval{
		ID: "appr-legacy", PlanID: plan.ID, ProjectID: plan.ProjectID, Policy: "quota_burst", Status: "pending",
		PlanContentHash: server.runner.executionPlanApprovalContentHash(plan), RequestedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := store.SaveExecutionPlanApproval(approval); err != nil {
		t.Fatalf("SaveExecutionPlanApproval: %v", err)
	}
	rec := timeslotRequest(t, server, http.MethodGet, "/api/v1/execution-plans?project_id=proj-a&include_activity=true", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d %s", rec.Code, rec.Body.String())
	}
	var payload struct {
		Plans []struct {
			ID       string `json:"id"`
			Entries  []storage.ExecutionPlanEntry
			Activity *struct {
				PendingApproval *storage.ExecutionPlanApproval `json:"pending_approval"`
			} `json:"activity"`
		} `json:"execution_plans"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Plans) != 1 || payload.Plans[0].Activity == nil || payload.Plans[0].Activity.PendingApproval == nil ||
		payload.Plans[0].Activity.PendingApproval.ID != approval.ID {
		t.Fatalf("list activity = %s, want the pending approval", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Fatalf("list returned a typed credential: %s", rec.Body.String())
	}
}

// A typed Playwright session state, at the section level or per identity, is
// a credential: a plan entry must reference it, and a legacy stored row is
// masked on read.
func TestExecutionPlanBrowserStorageStateNeedsReference(t *testing.T) {
	state := `{"cookies":[{"name":"sid","value":"hunter2"}]}`
	cases := []struct {
		name string
		path string
		row  map[string]any
	}{
		{"section storage state", "browser_config.storage_state", map[string]any{"browser_config": map[string]any{"storage_state": state}}},
		{"identity storage state", "browser_config.session.identities.0.storage_state", map[string]any{"browser_config": map[string]any{"session": map[string]any{"strategy": "storage_state_pool", "identities": []map[string]any{{"username": "u", "storage_state": state}}}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, message := validateExecutionPlanRow(t, tc.row)
			if status != http.StatusBadRequest || !strings.Contains(message, tc.path) {
				t.Fatalf("validation = (%d, %q), want 400 naming %s", status, message, tc.path)
			}
			raw, err := json.Marshal(tc.row)
			if err != nil {
				t.Fatal(err)
			}
			masked, changed := maskExecutionPlanRuntimeOverridesRow(raw)
			if !changed || strings.Contains(string(masked), "hunter2") || !strings.Contains(string(masked), "[REDACTED]") {
				t.Fatalf("masked legacy row = %s (changed %v), want the session state redacted", masked, changed)
			}
		})
	}
	references := map[string]any{"browser_config": map[string]any{
		"storage_state": "{{secret.STATE}}",
		"session":       map[string]any{"identities": []map[string]any{{"username": "u", "storage_state": "{{secret.STATE_U}}"}}},
	}}
	if status, message := validateExecutionPlanRow(t, references); status != 0 {
		t.Fatalf("references = (%d, %q), want accepted", status, message)
	}
}

// A reference followed by typed text is not a reference: the typed suffix
// is a credential the run-side mask redacts.
func TestExecutionPlanURLReferenceWithTypedSuffixIsTyped(t *testing.T) {
	for _, value := range []string{
		"https://h/x?token={{secret.T}}hunter2",
		"https://h/x?a={{secret.A}}&token={{secret.T}}hunter2",
		"https://h/x#token={{secret.T}}hunter2;b=1",
	} {
		if !executionPlanURLCarriesTypedCredential(value) {
			t.Fatalf("%q: want a typed credential", value)
		}
	}
	for _, value := range []string{
		"https://h/x?token={{secret.T}}",
		"https://h/x?a={{secret.A}}&token={{secret.T}}#b=1",
		"https://h/x?token={{secret.T}};access_token={{secret.U}}",
	} {
		if executionPlanURLCarriesTypedCredential(value) {
			t.Fatalf("%q: want references accepted", value)
		}
	}
	if status, message := validateExecutionPlanRow(t, map[string]any{"base_url": "https://h/x?token={{secret.T}}hunter2"}); status != http.StatusBadRequest {
		t.Fatalf("validation = (%d, %q), want 400", status, message)
	}
}

// A legacy row whose keys are not in canonical casing, or that repeats a key
// in another casing, is still masked: encoding/json read those values.
func TestMaskExecutionPlanRowMasksNonCanonicalKeyCasing(t *testing.T) {
	rows := []string{
		`{"browser_config":{"session":{"identities":[{"username":"u","Password":"hunter2"}],"jwt":{"Private_Key":"hunter2-key"}}}}`,
		`{"HTTP":{"Auth":{"password":"hunter2","Password":"hunter2-later"}}}`,
		`{"http":{"auth":{"Password":"hunter2-first","password":"hunter2"}}}`,
	}
	for _, row := range rows {
		masked, changed := maskExecutionPlanRuntimeOverridesRow(json.RawMessage(row))
		if !changed || strings.Contains(string(masked), "hunter2") {
			t.Fatalf("masked %s = %s (changed %v), want every credential redacted", row, masked, changed)
		}
	}
	// Map keys (headers, env) stay exact: an env name in another casing is a
	// different variable, and its reference is returned as stored.
	masked, _ := maskExecutionPlanRuntimeOverridesRow(json.RawMessage(`{"env":{"API_TOKEN":"hunter2","api_token":"{{secret.VISIBLE}}"}}`))
	if strings.Contains(string(masked), "hunter2") || !strings.Contains(string(masked), "{{secret.VISIBLE}}") {
		t.Fatalf("masked env = %s", masked)
	}
}

// When the JSON walk cannot place a credential, the struct fallback still
// redacts every path the scan reported.
func TestMaskExecutionPlanRowFallbackRedactsEveryReportedPath(t *testing.T) {
	overrides := &RuntimeSettingsOverride{}
	if err := json.Unmarshal([]byte(`{"browser_config":{"session":{"identities":[{"username":"u","password":"hunter2"}],"jwt":{"private_key":"hunter2-key"}}}}`), &overrides); err != nil {
		t.Fatal(err)
	}
	raw := maskExecutionPlanOverridesFallback(overrides, executionPlanTypedCredentials(overrides))
	if strings.Contains(string(raw), "hunter2") {
		t.Fatalf("fallback = %s, want every reported credential redacted", raw)
	}
}

// encoding/json keeps the last of two keys equal under case folding, so a row
// that repeats a key can hide a typed credential behind a later reference:
// such a row is refused, whatever the depth.
func TestValidateExecutionPlanRejectsCaseVariantDuplicateKeys(t *testing.T) {
	rows := []string{
		`{"http":{"auth":{"password":"hunter2","Password":"{{secret.A}}"}}}`,
		`{"http":{"auth":{"password":"hunter2"}},"HTTP":{"auth":{"password":"{{secret.A}}"}}}`,
		`{"http":{"auth":{"password":"hunter2","password":"{{secret.A}}"}}}`,
		`{"env":{"API_TOKEN":"hunter2","api_token":"{{secret.A}}"}}`,
		`{"http":{"default_header_list":[{"name":"Authorization","value":"Bearer hunter2","Value":"{{secret.A}}"}]}}`,
	}
	server := &Server{}
	for _, row := range rows {
		status, message := server.validateExecutionPlan(&storage.ExecutionPlan{
			Name:    "plan",
			Entries: []storage.ExecutionPlanEntry{{ScenarioID: "scenario", RuntimeOverrides: json.RawMessage(row)}},
		})
		if status != http.StatusBadRequest || !strings.Contains(message, "letter case") {
			t.Fatalf("validate %s = (%d, %q), want 400 naming the repeated key", row, status, message)
		}
		if strings.Contains(message, "hunter2") {
			t.Fatalf("validation message echoes the credential: %q", message)
		}
	}
}

// A legacy row stored with a typed credential behind a later case-variant
// reference is masked on every plan and run read.
func TestExecutionPlanReadsMaskCaseVariantDuplicateKeyRows(t *testing.T) {
	rows := []string{
		`{"http":{"auth":{"username":"alice","password":"hunter2","Password":"{{secret.A}}"}},"env":{"REGION":"us-east-1"}}`,
		`{"http":{"auth":{"username":"alice","password":"hunter2"}},"HTTP":{"auth":{"password":"{{secret.A}}"}},"env":{"REGION":"us-east-1"}}`,
		`{"http":{"default_header_list":[{"name":"Authorization","value":"Bearer hunter2","Value":"{{secret.A}}"}]},"env":{"REGION":"us-east-1"}}`,
	}
	for _, row := range rows {
		server, store := newTimeslotServer(t)
		plan := legacyExecutionPlanWithRow(t, server, store, json.RawMessage(row))
		now := time.Now().UTC()
		run := &storage.ExecutionPlanRun{
			ID: "run-dup", PlanID: plan.ID, ProjectID: plan.ProjectID, RunMode: "sequential", Status: "completed",
			Entries: []storage.ExecutionPlanRunEntry{{
				Order: 0, ScenarioID: "scn-1", Status: "complete",
				RuntimeOverrides: append(json.RawMessage(nil), plan.Entries[0].RuntimeOverrides...),
			}},
			CreatedAt: now, UpdatedAt: now, CompletedAt: &now,
		}
		if err := store.SaveExecutionPlanRun(run); err != nil {
			t.Fatalf("SaveExecutionPlanRun: %v", err)
		}
		for _, path := range []string{
			"/api/v1/execution-plans/" + plan.ID,
			"/api/v1/execution-plans?project_id=proj-a",
			"/api/v1/execution-plans?project_id=proj-a&include_activity=true",
			"/api/v1/execution-plans/" + plan.ID + "/runs",
			"/api/v1/execution-plans/" + plan.ID + "/runs/" + run.ID,
		} {
			rec := timeslotRequest(t, server, http.MethodGet, path, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s = %d %s", path, rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if strings.Contains(body, "hunter2") {
				t.Fatalf("row %s: GET %s returned the typed credential: %s", row, path, body)
			}
			if !strings.Contains(body, "us-east-1") {
				t.Fatalf("row %s: GET %s = %s, want values outside the repeated key kept", row, path, body)
			}
		}
		if !strings.Contains(string(mustGetPlanRun(t, store, run.ID).Entries[0].RuntimeOverrides), "hunter2") {
			t.Fatal("masking a run view changed the stored snapshot resume replays")
		}
	}
}
