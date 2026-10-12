package api

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/storage"
)

func projectOperatorInput(instance string) ProjectLiveTableMutation {
	return ProjectLiveTableMutation{IdempotencyKey: uuid.NewString(), IssuedAt: time.Now().UTC(), ExpectedInstanceID: instance, ExpectedRevision: "0", Operation: "append", Values: map[string]string{"value": "private-operator-value"}}
}
func projectOperatorAudit(d *storage.ProjectLiveTableDefinition, actor string) *storage.AuditEvent {
	return &storage.AuditEvent{ID: uuid.NewString(), Action: "project_live_table.mutate", ActorType: "user", ActorSubject: actor, ProjectID: d.ProjectID, ResourceType: "project_live_table", ResourceID: d.ID, Status: "success", CreatedAt: time.Now().UTC(), Details: map[string]any{"operation": "append"}}
}

func TestProjectLiveTableOperatorContract(t *testing.T) {
	now := time.Now().UTC()
	base := projectOperatorInput("instance")
	base.IssuedAt = now
	for _, raw := range []string{"0", "9007199254740993", "18446744073709551615"} {
		t.Run("revision-"+raw, func(t *testing.T) {
			input := base
			input.ExpectedRevision = raw
			request, expires, err := input.request(livetable.ProfileNative, now, time.Minute)
			if err != nil || request.ExpectedRevision == nil || strconv.FormatUint(*request.ExpectedRevision, 10) != raw || !expires.Equal(now.Add(time.Minute)) {
				t.Fatalf("strict revision lost: %+v %v %v", request, expires, err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		edit func(*ProjectLiveTableMutation)
	}{
		{"missing-key", func(v *ProjectLiveTableMutation) { v.IdempotencyKey = "" }},
		{"zero-key", func(v *ProjectLiveTableMutation) { v.IdempotencyKey = uuid.Nil.String() }},
		{"uppercase-key", func(v *ProjectLiveTableMutation) { v.IdempotencyKey = "AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA" }},
		{"missing-instance", func(v *ProjectLiveTableMutation) { v.ExpectedInstanceID = "" }},
		{"missing-revision", func(v *ProjectLiveTableMutation) { v.ExpectedRevision = "" }},
		{"revision-leading-zero", func(v *ProjectLiveTableMutation) { v.ExpectedRevision = "01" }},
		{"revision-signed", func(v *ProjectLiveTableMutation) { v.ExpectedRevision = "+1" }},
		{"revision-negative", func(v *ProjectLiveTableMutation) { v.ExpectedRevision = "-1" }},
		{"revision-exponent", func(v *ProjectLiveTableMutation) { v.ExpectedRevision = "1e3" }},
		{"revision-overflow", func(v *ProjectLiveTableMutation) { v.ExpectedRevision = "18446744073709551616" }},
		{"future-clock", func(v *ProjectLiveTableMutation) { v.IssuedAt = now.Add(31 * time.Second) }},
		{"missing-time", func(v *ProjectLiveTableMutation) { v.IssuedAt = time.Time{} }},
		{"expired", func(v *ProjectLiveTableMutation) { v.IssuedAt = now.Add(-time.Minute) }},
		{"unknown-operation", func(v *ProjectLiveTableMutation) { v.Operation = "lease" }},
		{"append-selected-row", func(v *ProjectLiveTableMutation) { v.RecordID = "forged" }},
		{"empty-values", func(v *ProjectLiveTableMutation) { v.Values = nil }},
		{"oversized-body", func(v *ProjectLiveTableMutation) { v.Values = map[string]string{"value": strings.Repeat("x", 1<<20)} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			tc.edit(&input)
			if _, _, err := input.request(livetable.ProfileNative, now, time.Minute); err == nil {
				t.Fatal("invalid command accepted")
			} else if tc.name == "expired" && livetable.CodeOf(err) != livetable.CodeExpired {
				t.Fatalf("expiry must have a distinct outcome: %v", err)
			}
		})
	}
	for _, operation := range []string{"update", "clear", "increment"} {
		for _, profile := range []livetable.Profile{livetable.ProfileNative, livetable.ProfileVTS} {
			t.Run(string(profile)+"-"+operation, func(t *testing.T) {
				input := base
				input.Operation = operation
				if operation != "update" {
					input.Values = nil
					input.Columns = []string{"value"}
				}
				if operation == "increment" {
					input.Delta = "-9223372036854775808"
				}
				if profile == livetable.ProfileNative {
					input.RecordID = "row"
					input.ExpectedVersion = "9007199254740993"
				} else {
					input.Position = 1
				}
				request, _, err := input.request(profile, now, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				if operation == "increment" && request.Delta != math.MinInt64 {
					t.Fatal("int64 delta lost precision")
				}
				if profile == livetable.ProfileNative {
					input.ExpectedVersion = "0"
				} else {
					input.ExpectedVersion = "1"
				}
				if _, _, err := input.request(profile, now, time.Minute); err == nil {
					t.Fatal("invalid profile row condition accepted")
				}
			})
		}
	}
	for _, delta := range []string{"-0", "01", "1.5", "9223372036854775808", "-9223372036854775809"} {
		input := base
		input.Operation, input.RecordID, input.ExpectedVersion, input.Delta = "increment", "row", "1", delta
		input.Values, input.Columns = nil, []string{"value"}
		if _, _, err := input.request(livetable.ProfileNative, now, time.Minute); err == nil {
			t.Fatalf("invalid delta accepted %q", delta)
		}
	}
}

func TestProjectLiveTableOperatorJSONPrecisionAndStrictFields(t *testing.T) {
	result := projectOperatorResult(livetable.Result{Status: livetable.StatusOK, Revision: 9007199254740993, Count: math.MaxInt64, Row: &livetable.Row{ID: "row", Version: math.MaxUint64, Values: map[string]string{"empty": ""}}})
	body, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"revision":"9007199254740993"`, `"version":"18446744073709551615"`, `"count":"9223372036854775807"`, `"empty":""`} {
		if !bytes.Contains(body, []byte(value)) {
			t.Fatalf("wire precision/presence lost: %s", body)
		}
	}
	for _, body := range []string{`{"client_id":"forged"}`, `{"owner_id":"forged"}`, `{"sequence":1}`, `{"lease_token":"forged"}`, `{"expected_revision":0}`, `{"operation":"append"} {}`, `null`} {
		recorder := httptest.NewRecorder()
		var decoded ProjectLiveTableMutation
		ok := decodeProjectTableBody(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)), &decoded)
		if body == "null" {
			// A null body cannot supply a valid semantic command even if JSON
			// decoding leaves the zero value; the handler validates it next.
			if _, _, err := decoded.request(livetable.ProfileNative, time.Now(), time.Minute); err == nil {
				t.Fatal("null command accepted")
			}
		} else if ok || recorder.Code != http.StatusBadRequest {
			t.Fatalf("forged fields accepted: %s", body)
		}
	}
}

func TestProjectLiveTableOperatorRetryCASAndAuditPrivacy(t *testing.T) {
	runner, store, d := newProjectTableFixture(t)
	svc, ctx := runner.projectLiveTableService(), context.Background()
	input := projectOperatorInput(d.CurrentInstanceID)
	first, err := svc.ExecuteOperator(ctx, d.ProjectID, d.ID, "operator", input, projectOperatorAudit(d, "operator"))
	if err != nil || first.Result.Status != livetable.StatusOK || first.Result.Row == nil {
		t.Fatalf("append %+v %v", first, err)
	}
	retry, err := svc.ExecuteOperator(ctx, d.ProjectID, d.ID, "operator", input, projectOperatorAudit(d, "operator"))
	if err != nil || !retry.Replayed || !reflect.DeepEqual(first.Result, retry.Result) || !first.ExpiresAt.Equal(retry.ExpiresAt) {
		t.Fatalf("exact retry %+v %v", retry, err)
	}
	altered := input
	altered.Values = map[string]string{"value": "altered"}
	if _, err := svc.ExecuteOperator(ctx, d.ProjectID, d.ID, "operator", altered, projectOperatorAudit(d, "operator")); livetable.CodeOf(err) != livetable.CodeConflict {
		t.Fatalf("key/body mismatch accepted: %v", err)
	}
	stale := projectOperatorInput(d.CurrentInstanceID)
	if _, err := svc.ExecuteOperator(ctx, d.ProjectID, d.ID, "operator", stale, projectOperatorAudit(d, "operator")); livetable.CodeOf(err) != livetable.CodeConflict {
		t.Fatalf("strict zero revision treated as absent: %v", err)
	}
	update := projectOperatorInput(d.CurrentInstanceID)
	update.Operation, update.RecordID, update.ExpectedVersion, update.ExpectedRevision = "update", first.Result.Row.ID, "2", first.Result.Revision
	update.Values = map[string]string{"value": "must-not-overwrite"}
	condition, err := svc.ExecuteOperator(ctx, d.ProjectID, d.ID, "operator", update, projectOperatorAudit(d, "operator"))
	if err != nil || condition.Result.Status != livetable.StatusConditionFailed {
		t.Fatalf("row CAS %+v %v", condition, err)
	}
	revision, _ := strconv.ParseUint(condition.Result.Revision, 10, 64)
	page, err := svc.Inspect(ctx, d.ProjectID, d.ID, d.CurrentInstanceID, revision, 0, 100)
	if err != nil || len(page.Rows) != 1 || page.Rows[0].Values["value"] != "private-operator-value" {
		t.Fatalf("conflict mutated rows %+v %v", page, err)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "project_live_table.mutate", ResourceType: "project_live_table", ResourceID: d.ID, Limit: 100})
	if err != nil || len(events) != 2 {
		t.Fatalf("exact audit count %d %v", len(events), err)
	}
	for _, event := range events {
		body, _ := json.Marshal(event)
		if bytes.Contains(body, []byte("private-operator-value")) || bytes.Contains(body, []byte("must-not-overwrite")) || bytes.Contains(body, []byte(input.IdempotencyKey)) {
			t.Fatalf("audit disclosed values or retry key: %s", body)
		}
	}
}

func TestProjectLiveTableOperatorAuditFailureRollsBackJournalAndOutcome(t *testing.T) {
	runner, store, d := newProjectTableFixture(t)
	audit := projectOperatorAudit(d, "operator")
	if err := store.SaveAuditEvent(audit); err != nil {
		t.Fatal(err)
	}
	input := projectOperatorInput(d.CurrentInstanceID)
	if _, err := runner.projectLiveTableService().ExecuteOperator(context.Background(), d.ProjectID, d.ID, "operator", input, audit); err == nil {
		t.Fatal("mutation acknowledged after duplicate audit insert failed")
	}
	recovery, err := store.ReadProjectLiveTableRecovery(context.Background(), d.ProjectID, d.CurrentInstanceID)
	if err != nil || recovery.Instance.Revision != 0 || len(recovery.Entries) != 0 {
		t.Fatalf("audit failure published a transition: %+v %v", recovery, err)
	}
}

func newProjectOperatorServer(t *testing.T) (*Server, *storage.SQLiteStore, *storage.ProjectLiveTableDefinition) {
	t.Helper()
	store, err := storage.NewSQLiteStore(t.TempDir() + "/operator.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 4, BrunoArtifactWrapper: testContinuityWrapper(t)})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.shutdown(ctx)
	})
	d, err := server.runner.projectLiveTableService().Create(context.Background(), ProjectLiveTableCreateInput{ProjectID: storage.DefaultProjectID, Name: "Operator table", ActorSubject: "dev-mode", Config: storage.ProjectLiveTableConfig{Columns: []string{"value"}}})
	if err != nil {
		t.Fatal(err)
	}
	return server, store, d
}

func TestProjectLiveTableOperatorRoutesAuthorizeAndKeepMetadataValueFree(t *testing.T) {
	server, store, d := newProjectOperatorServer(t)
	input := projectOperatorInput(d.CurrentInstanceID)
	first, err := server.runner.projectLiveTableService().ExecuteOperator(context.Background(), d.ProjectID, d.ID, "seed-operator", input, projectOperatorAudit(d, "seed-operator"))
	if err != nil {
		t.Fatal(err)
	}
	enableTrustedHeaderAuth(t, server)
	base := "/api/v1/projects/" + d.ProjectID + "/live-tables/" + d.ID
	do := func(method, path, role, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Auth-Request-User", "scoped-operator")
		req.Header.Set("X-APEXLoad-Project-Roles", role)
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, req)
		return response
	}
	for _, path := range []string{strings.TrimSuffix(base, "/"+d.ID), base} {
		response := do(http.MethodGet, path, d.ProjectID+"="+RoleViewer, "")
		if response.Code != 200 || strings.Contains(response.Body.String(), "private-operator-value") || strings.Contains(response.Body.String(), "lease_token") {
			t.Fatalf("metadata %+v %s", response.Code, response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("metadata cached")
		}
	}
	rows := base + "/rows?instance_id=" + d.CurrentInstanceID + "&revision=" + first.Result.Revision
	if response := do(http.MethodGet, rows, "foreign="+RoleViewer, ""); response.Code != 403 || strings.Contains(response.Body.String(), "private-operator-value") {
		t.Fatalf("cross-project read %d %s", response.Code, response.Body.String())
	}
	if response := do(http.MethodGet, rows, d.ProjectID+"="+RoleViewer, ""); response.Code != 200 || !strings.Contains(response.Body.String(), "private-operator-value") || response.Header().Get("X-APEXLoad-Data-Sensitivity") != "raw" {
		t.Fatalf("explicit read %d %s", response.Code, response.Body.String())
	}
	body, _ := json.Marshal(projectOperatorInput(d.CurrentInstanceID))
	if response := do(http.MethodPost, base+"/mutations", d.ProjectID+"="+RoleViewer, string(body)); response.Code != 403 {
		t.Fatalf("viewer mutated %d %s", response.Code, response.Body.String())
	}
	if response := do(http.MethodPost, base+"/mutations", d.ProjectID+"="+RoleEditor, `{"owner_id":"forged"}`); response.Code != 400 {
		t.Fatalf("forged identity %d %s", response.Code, response.Body.String())
	}
	if response := do(http.MethodPost, base+"/lifecycle", d.ProjectID+"="+RoleEditor, `{}`); response.Code != 403 {
		t.Fatalf("editor lifecycle %d %s", response.Code, response.Body.String())
	}
	if response := do(http.MethodGet, base+"/rows?instance_id="+d.CurrentInstanceID+"&revision=01", d.ProjectID+"="+RoleViewer, ""); response.Code != 400 {
		t.Fatalf("noncanonical read %d %s", response.Code, response.Body.String())
	}

	for _, route := range []struct{ method, path, scope string }{{http.MethodGet, rows, "read"}, {http.MethodPost, base + "/mutations", "scripts:write"}, {http.MethodPost, base + "/lifecycle", "scripts:write"}} {
		for _, scope := range []string{"results:read", "tests:run", "read", "write", "scripts:write"} {
			key := uuid.NewString()
			now := time.Now().UTC()
			if err := store.SaveServiceAccount(&storage.ServiceAccount{ID: key, Name: "operator-scope", KeyHash: hashAPIKey(key), Active: true, GlobalRoles: []string{RoleSystemAdmin}, Scopes: []string{scope}, CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
			req.Header.Set("X-APEXLoad-API-Key", key)
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, req)
			if scope != route.scope && (response.Code != 403 || !strings.Contains(response.Body.String(), "token scope")) {
				t.Fatalf("%s %s scope %s leaked authority: %d %s", route.method, route.path, scope, response.Code, response.Body.String())
			}
			if scope == route.scope && response.Code == 403 {
				t.Fatalf("required scope rejected: %s", response.Body.String())
			}
		}
	}
	// Permission revocation is checked on each request; an earlier raw read
	// cannot turn the next request into a capability for a former member.
	if response := do(http.MethodGet, rows, "foreign="+RoleViewer, ""); response.Code != 403 {
		t.Fatalf("revoked role read: %d", response.Code)
	}
}

func TestProjectLiveTableOperatorRejectsLeasedRow(t *testing.T) {
	runner, store, d := newProjectTableFixture(t)
	ctx, svc := context.Background(), runner.projectLiveTableService()
	first, err := svc.ExecuteOperator(ctx, d.ProjectID, d.ID, "operator", projectOperatorInput(d.CurrentInstanceID), projectOperatorAudit(d, "operator"))
	if err != nil || first.Result.Row == nil {
		t.Fatalf("append %+v %v", first, err)
	}
	run := projectTableRun(t, runner, store, "operator-lease-run", d, "lease")
	binding := runner.liveBindingsForGroup(run.ID, "workers")[0]
	lease, err := runner.liveClient(run.ID, "local/workers").Execute(ctx, binding.TableID, livetable.Request{ClientID: "worker", OwnerID: "worker", Sequence: 1, Operation: livetable.OpLease})
	if err != nil || lease.Row == nil {
		t.Fatalf("lease %+v %v", lease, err)
	}
	input := projectOperatorInput(d.CurrentInstanceID)
	input.Operation, input.RecordID, input.ExpectedRevision, input.ExpectedVersion = "update", lease.Row.ID, strconv.FormatUint(lease.Revision, 10), strconv.FormatUint(lease.Row.Version, 10)
	input.Values = map[string]string{"value": "must-not-overwrite-a-lease"}
	if _, err := svc.ExecuteOperator(ctx, d.ProjectID, d.ID, "operator", input, projectOperatorAudit(d, "operator")); livetable.CodeOf(err) != livetable.CodeLease {
		t.Fatalf("leased row edit accepted: %v", err)
	}
}

type projectReadAuditHookStore struct {
	*storage.SQLiteStore
	onCompleted func()
}

func (s *projectReadAuditHookStore) SaveAuditEvent(event *storage.AuditEvent) error {
	if err := s.SQLiteStore.SaveAuditEvent(event); err != nil {
		return err
	}
	if strings.HasPrefix(event.Action, "project_live_table.rows.") && event.Status == "success" && s.onCompleted != nil {
		s.onCompleted()
	}
	return nil
}

func TestProjectLiveTableReadRechecksOwnerAndCredentialsAfterAudit(t *testing.T) {
	for _, change := range []string{"owner", "credential"} {
		t.Run(change, func(t *testing.T) {
			server, store, d := newProjectOperatorServer(t)
			first, err := server.runner.projectLiveTableService().ExecuteOperator(context.Background(), d.ProjectID, d.ID, "operator", projectOperatorInput(d.CurrentInstanceID), projectOperatorAudit(d, "operator"))
			if err != nil {
				t.Fatal(err)
			}
			key, now := uuid.NewString(), time.Now().UTC()
			account := &storage.ServiceAccount{ID: key, Name: "read-check", KeyHash: hashAPIKey(key), Active: true, GlobalRoles: []string{RoleSystemAdmin}, Scopes: []string{"read"}, CreatedAt: now, UpdatedAt: now}
			if change == "credential" {
				enableTrustedHeaderAuth(t, server)
				if err := store.SaveServiceAccount(account); err != nil {
					t.Fatal(err)
				}
			}
			server.store = &projectReadAuditHookStore{SQLiteStore: store, onCompleted: func() {
				if change == "owner" {
					server.runner.stopProjectLiveTableLeadership()
				} else {
					if err := store.UpdateServiceAccountActive(account.ID, false, time.Now().UTC()); err != nil {
						t.Fatal(err)
					}
				}
			}}
			path := "/api/v1/projects/" + d.ProjectID + "/live-tables/" + d.ID + "/export?instance_id=" + d.CurrentInstanceID + "&revision=" + first.Result.Revision
			req := httptest.NewRequest(http.MethodGet, path, nil)
			if change == "credential" {
				req.Header.Set("X-APEXLoad-API-Key", key)
			}
			response := httptest.NewRecorder()
			server.mux.ServeHTTP(response, req)
			want := http.StatusConflict
			if change == "credential" {
				want = http.StatusForbidden
			}
			if response.Code != want || strings.Contains(response.Body.String(), "private-operator-value") || response.Header().Get("X-APEXLoad-Data-Sensitivity") != "" {
				t.Fatalf("post-audit %s leak: %d %s", change, response.Code, response.Body.String())
			}
		})
	}
}

func TestProjectLiveTableCreateRejectsInvalidConfigurationAsClientError(t *testing.T) {
	server, _, d := newProjectOperatorServer(t)
	path := "/api/v1/projects/" + d.ProjectID + "/live-tables"
	for _, body := range []string{
		`{"name":"","profile":"native_queue","columns":["value"],"limits":{}}`,
		`{"name":"Invalid","profile":"unknown","columns":["value"],"limits":{}}`,
		`{"name":"Invalid","profile":"native_queue","columns":[],"limits":{}}`,
		`{"name":"Invalid","profile":"native_queue","columns":["value"],"limits":{"max_rows":-1}}`,
	} {
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid create status %d: %s", response.Code, response.Body.String())
		}
	}
}

func TestProjectLiveTableInspectionAdaptsSmallAndLargeRowPages(t *testing.T) {
	server, store, base := newProjectOperatorServer(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		count, size int
	}{{"small-capacity", 3, 10}, {"large-cells", 105, 4000}} {
		t.Run(tc.name, func(t *testing.T) {
			pool := &storage.SharedDataPool{ID: uuid.NewString(), ProjectID: base.ProjectID, Name: "Source " + tc.name, Schema: map[string]string{"value": "string"}}
			if err := store.SaveDataPool(pool); err != nil {
				t.Fatal(err)
			}
			for index := 0; index < tc.count; index++ {
				if err := store.SaveDataPoolRow(&storage.SharedDataPoolRow{ID: "row-" + uuid.NewString(), PoolID: pool.ID, Data: map[string]string{"value": strings.Repeat("x", tc.size)}, Status: "available"}); err != nil {
					t.Fatal(err)
				}
			}
			d, err := server.runner.projectLiveTableService().Create(ctx, ProjectLiveTableCreateInput{ProjectID: base.ProjectID, Name: tc.name, ActorSubject: "dev-mode", SeedPoolID: pool.ID, Config: storage.ProjectLiveTableConfig{Columns: []string{"value"}, MaxRows: tc.count}})
			if err != nil {
				t.Fatal(err)
			}
			path := "/api/v1/projects/" + d.ProjectID + "/live-tables/" + d.ID
			for _, suffix := range []string{"/rows", "/export"} {
				response := httptest.NewRecorder()
				server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path+suffix+"?instance_id="+d.CurrentInstanceID+"&revision=0&limit=100", nil))
				if response.Code != http.StatusOK {
					t.Fatalf("%s %s: %d %s", tc.name, suffix, response.Code, response.Body.String())
				}
				var body ProjectLiveTableRowsResponse
				if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if body.Total != int64(tc.count) || len(body.Rows) == 0 || suffix == "/export" && len(body.Rows) != tc.count {
					t.Fatalf("lost bounded page rows: %+v", body)
				}
				if tc.name == "large-cells" && suffix == "/rows" && len(body.Rows) >= 100 {
					t.Fatal("page was not reduced to its byte budget")
				}
			}
		})
	}
}

func TestProjectLiveTableOperatorRetainedReceiptAfterResetAndArchive(t *testing.T) {
	for _, action := range []string{"reset", "archive"} {
		t.Run(action, func(t *testing.T) {
			runner, store, d := newProjectTableFixture(t)
			ctx, svc := context.Background(), runner.projectLiveTableService()
			input := projectOperatorInput(d.CurrentInstanceID)
			first, err := svc.ExecuteOperator(ctx, d.ProjectID, d.ID, "operator", input, projectOperatorAudit(d, "operator"))
			if err != nil {
				t.Fatal(err)
			}
			revision, err := parseProjectTableRevision(first.Result.Revision)
			if err != nil {
				t.Fatal(err)
			}
			quiesced, err := svc.ChangeLifecycle(ctx, ProjectLiveTableLifecycleInput{ProjectID: d.ProjectID, TableID: d.ID, Action: "quiesce", Reason: "reviewed", ExpectedDefinitionRevision: d.Revision, ExpectedInstanceID: d.CurrentInstanceID, ExpectedRevision: revision})
			if err != nil {
				t.Fatal(err)
			}
			command := ProjectLiveTableLifecycleInput{ProjectID: d.ProjectID, TableID: d.ID, Action: action, Reason: "reviewed", ExpectedDefinitionRevision: quiesced.Revision, ExpectedInstanceID: d.CurrentInstanceID, ExpectedRevision: revision}
			if action == "reset" {
				_, err = svc.Reset(ctx, ProjectLiveTableResetInput{ProjectLiveTableLifecycleInput: command})
			} else {
				_, err = svc.ChangeLifecycle(ctx, command)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.evictIdle(ctx, true); err != nil {
				t.Fatal(err)
			}
			if len(runner.projectLiveTables) != 0 {
				t.Fatal("fixture retained an actor")
			}
			retry, err := svc.ExecuteOperator(ctx, d.ProjectID, d.ID, "operator", input, projectOperatorAudit(d, "operator"))
			if err != nil || !retry.Replayed || !reflect.DeepEqual(first.Result, retry.Result) || !first.ExpiresAt.Equal(retry.ExpiresAt) {
				t.Fatalf("retained exact retry %+v %v", retry, err)
			}
			if len(runner.projectLiveTables) != 0 {
				t.Fatal("receipt replay reopened a mutation actor")
			}
			changed := input
			changed.Values = map[string]string{"value": "different"}
			if _, err := svc.ExecuteOperator(ctx, d.ProjectID, d.ID, "operator", changed, projectOperatorAudit(d, "operator")); livetable.CodeOf(err) != livetable.CodeConflict {
				t.Fatalf("changed body accepted: %v", err)
			}
			fresh := input
			fresh.IdempotencyKey = uuid.NewString()
			if _, err := svc.ExecuteOperator(ctx, d.ProjectID, d.ID, "operator", fresh, projectOperatorAudit(d, "operator")); err == nil {
				t.Fatal("new command accepted for retired instance")
			}
			if result, err := svc.ExecuteOperator(ctx, d.ProjectID, d.ID, "different-actor", input, projectOperatorAudit(d, "different-actor")); err == nil || result.Result.Row != nil {
				t.Fatalf("cross-actor receipt disclosed: %+v %v", result, err)
			}
			if result, err := svc.ExecuteOperator(ctx, "foreign-project", d.ID, "operator", input, projectOperatorAudit(d, "operator")); err == nil || result.Result.Row != nil {
				t.Fatalf("cross-project receipt disclosed: %+v %v", result, err)
			}
			events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "project_live_table.mutate", ResourceType: "project_live_table", ResourceID: d.ID, Limit: 100})
			if err != nil || len(events) != 1 {
				t.Fatalf("retry added an operator transition or audit: %d %v", len(events), err)
			}
		})
	}
}
