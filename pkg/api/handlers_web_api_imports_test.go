package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestHandleWebAPIExecutionSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/web-api/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		SupportMatrix []struct {
			ID              string `json:"id"`
			Status          string `json:"status"`
			ExecutionStatus string `json:"execution_status"`
		} `json:"support_matrix"`
		BenchmarkEnvelopes []struct {
			Protocol          string `json:"protocol"`
			RecommendedVUsers int    `json:"recommended_vusers_per_generator"`
		} `json:"benchmark_envelopes"`
		Diagnostics []struct {
			Protocol string   `json:"protocol"`
			Signals  []string `json:"signals"`
		} `json:"diagnostics"`
		SampleProjects []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"sample_projects"`
		StatusCounts map[string]int `json:"status_counts"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	for _, id := range []string{"http-web", "openapi-authoring", "postman-authoring", "soap-wsdl", "java-over-http", "websocket", "grpc", "jms-solace", "browser-canaries", "devweb-import"} {
		if !webAPIMatrixContains(result.SupportMatrix, id) {
			t.Fatalf("support matrix missing %s", id)
		}
	}
	for _, protocol := range []string{"http", "soap", "java_http", "websocket", "grpc", "jms-solace", "browser"} {
		if !webAPIEnvelopeContains(result.BenchmarkEnvelopes, protocol) {
			t.Fatalf("benchmark envelopes missing %s", protocol)
		}
		if !webAPIDiagnosticsContains(result.Diagnostics, protocol) {
			t.Fatalf("diagnostics missing %s", protocol)
		}
	}
	if len(result.SampleProjects) < 6 {
		t.Fatalf("sample projects = %d, want at least 6", len(result.SampleProjects))
	}
	if result.StatusCounts["supported"] == 0 {
		t.Fatal("expected supported status count")
	}
}

func TestHandleDevWebSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/devweb/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		SupportMatrix []struct {
			ID              string `json:"id"`
			ExecutionStatus string `json:"execution_status"`
		} `json:"support_matrix"`
		Notes []string `json:"notes"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if !devWebMatrixContains(result.SupportMatrix, "devweb-grpc-unary") {
		t.Fatal("expected gRPC unary DevWeb support entry")
	}
	if len(result.Notes) == 0 {
		t.Fatal("expected support matrix notes")
	}
}

func TestHandleOpenAPISupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/openapi/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		SupportMatrix []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"support_matrix"`
		Notes []string `json:"notes"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	for _, id := range []string{"openapi-chaining", "openapi-extractors", "openapi-security", "openapi-replay-readiness"} {
		if !openAPIMatrixContains(result.SupportMatrix, id) {
			t.Fatalf("support matrix missing %s: %#v", id, result.SupportMatrix)
		}
	}
	if len(result.Notes) == 0 {
		t.Fatal("expected support matrix notes")
	}
}

func TestHandleImportOpenAPI(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name":       "Orders API",
		"project_id": "proj-api",
		"content": `{
  "openapi": "3.0.3",
  "info": {"title": "Orders", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "paths": {
    "/orders/{id}": {
      "get": {
        "operationId": "getOrder",
        "parameters": [{"name": "id", "in": "path", "schema": {"type": "string"}}],
        "responses": {"200": {}}
      }
    }
  }
}`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			ProjectID string `json:"project_id"`
			Protocol  string `json:"protocol"`
			Actions   []struct {
				Target string `json:"target"`
			} `json:"actions"`
			Metadata struct {
				Import struct {
					Preserved map[string]any `json:"preserved"`
				} `json:"import"`
			} `json:"metadata"`
		} `json:"script"`
		ReviewSections []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
		SourceReview map[string]any   `json:"source_review"`
		Preflight    *ReplayPreflight `json:"preflight"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.ProjectID != "proj-api" || result.Script.Protocol != "http" {
		t.Fatalf("script = %+v", result.Script)
	}
	if len(result.Script.Actions) != 1 || !strings.Contains(result.Script.Actions[0].Target, "{{data.openapi.id}}") {
		t.Fatalf("unexpected actions: %+v", result.Script.Actions)
	}
	if len(result.SourceReview) == 0 {
		t.Fatal("expected root source_review in response")
	}
	if result.Script.Metadata.Import.Preserved["source_review"] == nil {
		t.Fatalf("script metadata missing preserved source_review: %#v", result.Script.Metadata.Import.Preserved)
	}
	if result.Preflight == nil || !result.Preflight.Valid || result.Preflight.ActionCount != 1 {
		t.Fatalf("preflight = %+v, want valid one-action replay preflight", result.Preflight)
	}
	if result.Script.Metadata.Import.Preserved["replay_preflight"] == nil {
		t.Fatalf("script metadata missing preserved replay_preflight: %#v", result.Script.Metadata.Import.Preserved)
	}
	assertReviewSection(t, result.ReviewSections, "openapi_operations", "info", "getOrder")
}

func TestHandleImportOpenAPIRedactsCredentialsFromResultAndRootSummaries(t *testing.T) {
	server := NewServer(ServerConfig{})
	const (
		username = "openapi-source-user"
		password = "openapi-source-password"
		token    = "openapi-query-token"
	)
	serverURL := "https://" + username + ":" + password + "@api.example.test?access_token=" + token + "&region=east"
	payload, err := json.Marshal(map[string]any{
		"name":       "Protected server summary",
		"project_id": "proj-api",
		"content": `{
  "openapi": "3.0.3",
  "info": {"title": "Protected", "version": "1.0"},
  "servers": [{"url": "` + serverURL + `"}],
  "paths": {"/health": {"get": {"operationId": "health", "responses": {"200": {"description": "ok"}}}}}
}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	editable := string(envelope["script"]) + string(envelope["scripts"])
	for _, secret := range []string{username, password, token} {
		if !strings.Contains(editable, secret) {
			t.Fatalf("authorized OpenAPI editable draft lost URL credential %q: %s", secret, editable)
		}
	}
	delete(envelope, "script")
	delete(envelope, "scripts")
	reviewPayload, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{username, password, token} {
		if strings.Contains(string(reviewPayload), secret) {
			t.Fatalf("OpenAPI result/review surfaces disclosed URL credential %q: %s", secret, reviewPayload)
		}
	}
	if !strings.Contains(string(reviewPayload), maskedScriptSecret) || !strings.Contains(string(reviewPayload), "region=east") {
		t.Fatalf("OpenAPI URL projection did not preserve a safe summary: %s", reviewPayload)
	}
}

func TestHandleImportOpenAPIReturnsPreflightAndCorrelations(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name":       "Orders CRUD",
		"project_id": "proj-api",
		"content": `{
  "openapi": "3.0.3",
  "info": {"title": "Orders", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "paths": {
    "/orders": {
      "post": {
        "operationId": "createOrder",
        "responses": {"201": {
          "description": "created",
          "content": {"application/json": {"schema": {
            "type": "object",
            "properties": {"id": {"type": "string"}}
          }}}
        }}
      }
    },
    "/orders/{orderId}": {
      "get": {
        "operationId": "getOrder",
        "parameters": [{"name": "orderId", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			Actions []struct {
				Name       string `json:"name"`
				Target     string `json:"target"`
				Extractors []struct {
					Name       string `json:"name"`
					Expression string `json:"expression"`
				} `json:"extractors"`
			} `json:"actions"`
		} `json:"script"`
		Correlations []struct {
			Name        string `json:"name"`
			FoundInStep int    `json:"found_in_step"`
			UsedInSteps []int  `json:"used_in_steps"`
		} `json:"correlations"`
		Parameterization struct {
			Suggestions []struct {
				Status string `json:"status"`
			} `json:"suggestions"`
		} `json:"parameterization"`
		Preflight *ReplayPreflight `json:"preflight"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions = %+v, want create and get", result.Script.Actions)
	}
	if !openAPIImportExtractorContains(result.Script.Actions[0].Extractors, "order_id", "$.id") {
		t.Fatalf("create extractors = %+v, want order_id", result.Script.Actions[0].Extractors)
	}
	if !strings.Contains(result.Script.Actions[1].Target, "{{var.order_id}}") {
		t.Fatalf("get target = %q, want chained var", result.Script.Actions[1].Target)
	}
	if len(result.Correlations) != 1 || result.Correlations[0].Name != "order_id" || result.Correlations[0].FoundInStep != 1 || len(result.Correlations[0].UsedInSteps) != 1 || result.Correlations[0].UsedInSteps[0] != 2 {
		t.Fatalf("correlations = %+v, want order_id found in step 1 and used by step 2", result.Correlations)
	}
	if result.Preflight == nil || !result.Preflight.Valid || result.Preflight.ActionCount != 2 {
		t.Fatalf("preflight = %+v, want valid two-action preflight", result.Preflight)
	}
}

func TestHandleCommitOpenAPIImportCreateReplaceAndDryRun(t *testing.T) {
	server := NewServer(ServerConfig{})
	createPayload := map[string]any{
		"script": openAPICommitTestScript("Created OpenAPI", "proj-api", []dsl.Action{
			openAPICommitTestAction("ListOrders", "GET", "/orders", "GET /orders"),
		}),
	}
	createBody, err := json.Marshal(createPayload)
	if err != nil {
		t.Fatalf("json.Marshal create returned error: %v", err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi/commit", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()

	server.mux.ServeHTTP(createResp, createReq)

	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created struct {
		StoredScript StoredScript      `json:"stored_script"`
		Replaced     bool              `json:"replaced"`
		DryRun       bool              `json:"dry_run"`
		Diff         openAPIImportDiff `json:"diff"`
		Preflight    *ReplayPreflight  `json:"preflight"`
	}
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("json.Unmarshal create returned error: %v", err)
	}
	if created.StoredScript.ID == "" || created.Replaced || created.DryRun || created.Diff.Added != 1 || created.Preflight == nil || !created.Preflight.Valid {
		t.Fatalf("created response = %+v", created)
	}

	dryRunPayload := map[string]any{
		"mode":             "replace",
		"target_script_id": created.StoredScript.ID,
		"replace_mode":     "full",
		"dry_run":          true,
		"script": openAPICommitTestScript("Dry Run OpenAPI", "proj-api", []dsl.Action{
			openAPICommitTestAction("ListOrders", "GET", "/orders?dry=1", "GET /orders"),
		}),
	}
	dryRunBody, err := json.Marshal(dryRunPayload)
	if err != nil {
		t.Fatalf("json.Marshal dry-run returned error: %v", err)
	}
	dryRunReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi/commit", bytes.NewReader(dryRunBody))
	dryRunReq.Header.Set("Content-Type", "application/json")
	dryRunResp := httptest.NewRecorder()

	server.mux.ServeHTTP(dryRunResp, dryRunReq)

	if dryRunResp.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want %d body=%s", dryRunResp.Code, http.StatusOK, dryRunResp.Body.String())
	}
	var dryRun struct {
		DryRun bool              `json:"dry_run"`
		Diff   openAPIImportDiff `json:"diff"`
	}
	if err := json.Unmarshal(dryRunResp.Body.Bytes(), &dryRun); err != nil {
		t.Fatalf("json.Unmarshal dry-run returned error: %v", err)
	}
	if !dryRun.DryRun || dryRun.Diff.ReplaceMode != "full" || dryRun.Diff.Added != 1 || dryRun.Diff.Removed != 1 {
		t.Fatalf("dry-run response = %+v", dryRun)
	}
	stillCreated, ok := server.runner.GetScript(created.StoredScript.ID)
	if !ok || stillCreated.Name != "Created OpenAPI" {
		t.Fatalf("dry-run mutated stored script: %+v ok=%v", stillCreated, ok)
	}

	fullReplacePayload := map[string]any{
		"mode":             "replace",
		"target_script_id": created.StoredScript.ID,
		"replace_mode":     "full",
		"script": openAPICommitTestScript("Full Replace OpenAPI", "proj-api", []dsl.Action{
			openAPICommitTestAction("CreateOrder", "POST", "/orders", "POST /orders"),
		}),
	}
	fullReplaceBody, err := json.Marshal(fullReplacePayload)
	if err != nil {
		t.Fatalf("json.Marshal full replace returned error: %v", err)
	}
	fullReplaceReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi/commit", bytes.NewReader(fullReplaceBody))
	fullReplaceReq.Header.Set("Content-Type", "application/json")
	fullReplaceResp := httptest.NewRecorder()

	server.mux.ServeHTTP(fullReplaceResp, fullReplaceReq)

	if fullReplaceResp.Code != http.StatusOK {
		t.Fatalf("full replace status = %d, want %d body=%s", fullReplaceResp.Code, http.StatusOK, fullReplaceResp.Body.String())
	}
	var fullReplaced struct {
		StoredScript StoredScript      `json:"stored_script"`
		Replaced     bool              `json:"replaced"`
		Diff         openAPIImportDiff `json:"diff"`
	}
	if err := json.Unmarshal(fullReplaceResp.Body.Bytes(), &fullReplaced); err != nil {
		t.Fatalf("json.Unmarshal full replace returned error: %v", err)
	}
	if !fullReplaced.Replaced || fullReplaced.StoredScript.ID != created.StoredScript.ID || fullReplaced.StoredScript.Name != "Full Replace OpenAPI" || fullReplaced.Diff.ReplaceMode != "full" {
		t.Fatalf("full replace response = %+v", fullReplaced)
	}

	existing := openAPICommitTestScript("Merge Target", "proj-api", []dsl.Action{
		openAPICommitTestAction("GetOrder", "GET", "/orders/{{data.openapi.orderid}}", "GET /orders/{orderId}"),
		{Name: "CustomAudit", Type: "request", Method: "GET", Target: "/custom-audit"},
	})
	storedExisting := server.runner.StoreScript(&existing)
	mergePayload := map[string]any{
		"mode":             "replace",
		"target_script_id": storedExisting.ID,
		"replace_mode":     "merge_by_operation_id",
		"script": openAPICommitTestScript("Merged OpenAPI", "proj-api", []dsl.Action{
			openAPICommitTestAction("GetOrderUpdated", "GET", "/orders/{{var.order_id}}", "GET /orders/{orderId}"),
			openAPICommitTestAction("CreateOrder", "POST", "/orders", "POST /orders"),
		}),
	}
	mergeBody, err := json.Marshal(mergePayload)
	if err != nil {
		t.Fatalf("json.Marshal merge returned error: %v", err)
	}
	mergeReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi/commit", bytes.NewReader(mergeBody))
	mergeReq.Header.Set("Content-Type", "application/json")
	mergeResp := httptest.NewRecorder()

	server.mux.ServeHTTP(mergeResp, mergeReq)

	if mergeResp.Code != http.StatusOK {
		t.Fatalf("merge status = %d, want %d body=%s", mergeResp.Code, http.StatusOK, mergeResp.Body.String())
	}
	var merged struct {
		StoredScript StoredScript      `json:"stored_script"`
		Diff         openAPIImportDiff `json:"diff"`
	}
	if err := json.Unmarshal(mergeResp.Body.Bytes(), &merged); err != nil {
		t.Fatalf("json.Unmarshal merge returned error: %v", err)
	}
	if merged.Diff.Updated != 1 || merged.Diff.Added != 1 || merged.Diff.Preserved != 1 {
		t.Fatalf("merge diff = %+v, want updated/added/preserved", merged.Diff)
	}
	if len(merged.StoredScript.Script.Actions) != 3 || merged.StoredScript.Script.Actions[2].Name != "CustomAudit" {
		t.Fatalf("merged actions = %+v, want custom action preserved after imported operations", merged.StoredScript.Script.Actions)
	}

	notFoundPayload := map[string]any{
		"mode":             "replace",
		"target_script_id": "does-not-exist",
		"script":           openAPICommitTestScript("Missing Target", "proj-api", []dsl.Action{openAPICommitTestAction("ListOrders", "GET", "/orders", "GET /orders")}),
	}
	notFoundBody, err := json.Marshal(notFoundPayload)
	if err != nil {
		t.Fatalf("json.Marshal not-found returned error: %v", err)
	}
	notFoundReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi/commit", bytes.NewReader(notFoundBody))
	notFoundReq.Header.Set("Content-Type", "application/json")
	notFoundResp := httptest.NewRecorder()
	server.mux.ServeHTTP(notFoundResp, notFoundReq)
	if notFoundResp.Code != http.StatusNotFound {
		t.Fatalf("not-found status = %d, want %d body=%s", notFoundResp.Code, http.StatusNotFound, notFoundResp.Body.String())
	}

	invalidPayload := map[string]any{
		"script": dsl.Script{Name: "Invalid OpenAPI", Protocol: "http", Actions: []dsl.Action{{Name: "Broken", Type: "request", Method: "GET"}}},
	}
	invalidBody, err := json.Marshal(invalidPayload)
	if err != nil {
		t.Fatalf("json.Marshal invalid returned error: %v", err)
	}
	invalidReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi/commit", bytes.NewReader(invalidBody))
	invalidReq.Header.Set("Content-Type", "application/json")
	invalidResp := httptest.NewRecorder()
	server.mux.ServeHTTP(invalidResp, invalidReq)
	if invalidResp.Code != http.StatusBadRequest {
		t.Fatalf("invalid status = %d, want %d body=%s", invalidResp.Code, http.StatusBadRequest, invalidResp.Body.String())
	}
}

func TestHandleCommitAsyncAPIImportCreateReplaceAndDryRun(t *testing.T) {
	server := NewServer(ServerConfig{})
	createPayload := map[string]any{
		"script": asyncAPICommitTestScript("Created AsyncAPI", "proj-async", []dsl.Action{
			asyncAPICommitTestAction("PublishOrders", "kafka_produce", "orders.created", "orders"),
		}),
	}
	createBody, err := json.Marshal(createPayload)
	if err != nil {
		t.Fatalf("json.Marshal create returned error: %v", err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/asyncapi/commit", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)

	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created struct {
		StoredScript StoredScript      `json:"stored_script"`
		Replaced     bool              `json:"replaced"`
		DryRun       bool              `json:"dry_run"`
		Diff         openAPIImportDiff `json:"diff"`
		Preflight    *ReplayPreflight  `json:"preflight"`
	}
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("json.Unmarshal create returned error: %v", err)
	}
	if created.StoredScript.ID == "" || created.Replaced || created.DryRun || created.Diff.Added != 1 || created.Preflight == nil {
		t.Fatalf("created response = %+v", created)
	}

	dryRunPayload := map[string]any{
		"mode":             "replace",
		"target_script_id": created.StoredScript.ID,
		"replace_mode":     "full",
		"dry_run":          true,
		"script": asyncAPICommitTestScript("Dry Run AsyncAPI", "proj-async", []dsl.Action{
			asyncAPICommitTestAction("PublishOrders", "kafka_produce", "orders.updated", "orders"),
		}),
	}
	dryRunBody, err := json.Marshal(dryRunPayload)
	if err != nil {
		t.Fatalf("json.Marshal dry-run returned error: %v", err)
	}
	dryRunReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/asyncapi/commit", bytes.NewReader(dryRunBody))
	dryRunReq.Header.Set("Content-Type", "application/json")
	dryRunResp := httptest.NewRecorder()
	server.mux.ServeHTTP(dryRunResp, dryRunReq)

	if dryRunResp.Code != http.StatusOK {
		t.Fatalf("dry-run status = %d, want %d body=%s", dryRunResp.Code, http.StatusOK, dryRunResp.Body.String())
	}
	var dryRun struct {
		DryRun bool              `json:"dry_run"`
		Diff   openAPIImportDiff `json:"diff"`
	}
	if err := json.Unmarshal(dryRunResp.Body.Bytes(), &dryRun); err != nil {
		t.Fatalf("json.Unmarshal dry-run returned error: %v", err)
	}
	if !dryRun.DryRun || dryRun.Diff.ReplaceMode != "full" || dryRun.Diff.Added != 1 || dryRun.Diff.Removed != 1 {
		t.Fatalf("dry-run response = %+v", dryRun)
	}
	stillCreated, ok := server.runner.GetScript(created.StoredScript.ID)
	if !ok || stillCreated.Name != "Created AsyncAPI" {
		t.Fatalf("dry-run mutated stored script: %+v ok=%v", stillCreated, ok)
	}

	existing := asyncAPICommitTestScript("Merge Target", "proj-async", []dsl.Action{
		asyncAPICommitTestAction("ConsumeOrders", "kafka_consume", "orders.created", "orders"),
		{Name: "CustomAudit", Type: "kafka_consume", Target: "custom.audit"},
	})
	storedExisting := server.runner.StoreScript(&existing)
	updated := asyncAPICommitTestAction("ConsumeOrders", "kafka_consume", "orders.created", "orders")
	updated.Assertions = []dsl.Assertion{{Type: "message_received"}}
	mergePayload := map[string]any{
		"mode":             "replace",
		"target_script_id": storedExisting.ID,
		"replace_mode":     "merge_by_operation_id",
		"script": asyncAPICommitTestScript("Merged AsyncAPI", "proj-async", []dsl.Action{
			updated,
			asyncAPICommitTestAction("PublishPayments", "kafka_produce", "payments.created", "payments"),
		}),
	}
	mergeBody, err := json.Marshal(mergePayload)
	if err != nil {
		t.Fatalf("json.Marshal merge returned error: %v", err)
	}
	mergeReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/asyncapi/commit", bytes.NewReader(mergeBody))
	mergeReq.Header.Set("Content-Type", "application/json")
	mergeResp := httptest.NewRecorder()
	server.mux.ServeHTTP(mergeResp, mergeReq)

	if mergeResp.Code != http.StatusOK {
		t.Fatalf("merge status = %d, want %d body=%s", mergeResp.Code, http.StatusOK, mergeResp.Body.String())
	}
	var merged struct {
		StoredScript StoredScript      `json:"stored_script"`
		Diff         openAPIImportDiff `json:"diff"`
	}
	if err := json.Unmarshal(mergeResp.Body.Bytes(), &merged); err != nil {
		t.Fatalf("json.Unmarshal merge returned error: %v", err)
	}
	if merged.Diff.Updated != 1 || merged.Diff.Added != 1 || merged.Diff.Preserved != 1 {
		t.Fatalf("merge diff = %+v, want updated/added/preserved", merged.Diff)
	}
	if len(merged.StoredScript.Script.Actions) != 3 || merged.StoredScript.Script.Actions[2].Name != "CustomAudit" {
		t.Fatalf("merged actions = %+v, want custom action preserved after imported messages", merged.StoredScript.Script.Actions)
	}
}

func TestHandleImportOpenAPIRawJSONDocument(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi?project_id=proj-api&base_url=https://override.example.test", strings.NewReader(`{
  "openapi": "3.0.3",
  "info": {"title": "Raw Orders", "version": "1.0"},
  "paths": {
    "/orders": {
      "get": {
        "operationId": "listOrders",
        "responses": {"200": {}}
      }
    }
  }
}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			ProjectID string `json:"project_id"`
			Config    struct {
				BaseURL string `json:"base_url"`
			} `json:"config"`
			Actions []struct {
				Name string `json:"name"`
			} `json:"actions"`
		} `json:"script"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.ProjectID != "proj-api" || result.Script.Config.BaseURL != "https://override.example.test" {
		t.Fatalf("script = %+v", result.Script)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Name != "listOrders" {
		t.Fatalf("actions = %+v, want listOrders", result.Script.Actions)
	}
}

func TestHandleImportOpenAPISurfacesUnsupportedElements(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name": "External Ref API",
		"content": `{
  "openapi": "3.0.3",
  "info": {"title": "External Ref", "version": "1.0"},
  "servers": [{"url": "https://api.example.test"}],
  "paths": {
    "/orders": {
      "post": {
        "operationId": "createOrder",
        "requestBody": {
          "content": {
            "application/json": {
              "schema": {"$ref": "external.yaml#/components/schemas/Order"}
            }
          }
        },
        "responses": {"201": {}}
      }
    }
  }
}`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/openapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		UnsupportedElements []string `json:"unsupported_elements"`
		ReviewSections      []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if !stringSliceContains(result.UnsupportedElements, "schema reference external.yaml") {
		t.Fatalf("unsupported elements = %#v, want unresolved ref diagnostic", result.UnsupportedElements)
	}
	assertReviewSection(t, result.ReviewSections, "import_diagnostics_warning", "warning", "OpenAPI schema $ref at POST /orders [warning]: schema reference external.yaml#/components/schemas/Order could not be resolved; external refs are not inlined")
}

func TestHandleImportPostman(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name":       "Orders Postman",
		"project_id": "proj-postman",
		"content": `{
  "info": {"name": "Orders Collection", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"},
  "variable": [{"key": "baseUrl", "value": "https://api.example.test"}, {"key": "order_id", "value": "42"}],
  "item": [{
    "name": "Orders",
    "item": [{
      "name": "Get Order",
      "request": {
        "method": "GET",
        "header": [{"key": "X-Request-ID", "value": "{{request_id}}"}],
        "url": "{{baseUrl}}/orders/{{order_id}}"
      }
    }]
  }]
}`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			ProjectID  string `json:"project_id"`
			Protocol   string `json:"protocol"`
			Parameters []struct {
				Name string `json:"name"`
			} `json:"parameters"`
			Config struct {
				BaseURL string `json:"base_url"`
			} `json:"config"`
			Actions []struct {
				Name    string            `json:"name"`
				Target  string            `json:"target"`
				Headers map[string]string `json:"headers"`
			} `json:"actions"`
		} `json:"script"`
		Stats struct {
			ConvertedActions  int `json:"converted_actions"`
			EnvironmentValues int `json:"environment_values"`
		} `json:"stats"`
		Preflight      *ReplayPreflight `json:"preflight"`
		ReviewSections []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.ProjectID != "proj-postman" || result.Script.Protocol != "http" || result.Script.Config.BaseURL != "https://api.example.test" {
		t.Fatalf("script = %+v", result.Script)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Name != "Orders_Get_Order" || result.Script.Actions[0].Target != "/orders/{{var.order_id}}" {
		t.Fatalf("actions = %+v", result.Script.Actions)
	}
	if result.Script.Actions[0].Headers["X-Request-ID"] != "{{var.request_id}}" {
		t.Fatalf("headers = %+v", result.Script.Actions[0].Headers)
	}
	if len(result.Script.Parameters) != 1 || result.Script.Parameters[0].Name != "order_id" {
		t.Fatalf("parameters = %+v", result.Script.Parameters)
	}
	if result.Stats.ConvertedActions != 1 {
		t.Fatalf("stats = %+v", result.Stats)
	}
	if result.Preflight == nil || result.Preflight.Valid || result.Preflight.ActionCount != 1 || !stringSliceContains(result.Preflight.MissingVariables, "request_id") {
		t.Fatalf("preflight = %+v", result.Preflight)
	}
	assertReviewSection(t, result.ReviewSections, "postman_requests", "info", "GET /orders/{{var.order_id}}")
	assertReviewSection(t, result.ReviewSections, "postman_variables", "info", "order_id")
}

func TestHandleImportPostmanMultipartEnvironment(t *testing.T) {
	server := NewServer(ServerConfig{})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "orders.postman_collection.json")
	if err != nil {
		t.Fatalf("CreateFormFile collection returned error: %v", err)
	}
	_, _ = file.Write([]byte(`{
  "info": {"name": "Orders Collection"},
  "variable": [{"key": "baseUrl", "value": "https://api.example.test"}, {"key": "token", "value": "collection-token"}],
  "item": [{"name": "Orders", "request": {"method": "GET", "url": "{{baseUrl}}/orders", "header": [{"key": "X-Token", "value": "{{token}}"}]}}]
}`))
	envFile, err := writer.CreateFormFile("environment", "local.postman_environment.json")
	if err != nil {
		t.Fatalf("CreateFormFile environment returned error: %v", err)
	}
	_, _ = envFile.Write([]byte(`{"name":"Local","values":[{"key":"baseUrl","value":"https://local.example.test","enabled":true},{"key":"token","value":"env-token","enabled":true}]}`))
	_ = writer.WriteField("project_id", "proj-postman")
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			Config struct {
				BaseURL string `json:"base_url"`
			} `json:"config"`
			DataSources []struct {
				InlineData string `json:"inline_data"`
			} `json:"data_sources"`
			Actions []struct {
				Headers map[string]string `json:"headers"`
			} `json:"actions"`
		} `json:"script"`
		Stats struct {
			EnvironmentValues int `json:"environment_values"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.Config.BaseURL != "https://local.example.test" {
		t.Fatalf("base URL = %q", result.Script.Config.BaseURL)
	}
	if result.Stats.EnvironmentValues != 2 {
		t.Fatalf("environment values = %d, want 2", result.Stats.EnvironmentValues)
	}
	// Keep the resolved source column as well as the executable occurrence:
	// deleting it would change parameterized replay and would force an operator
	// who already supplied the environment file to reconstruct its value.
	if len(result.Script.DataSources) != 1 ||
		!strings.Contains(result.Script.DataSources[0].InlineData, "env-token") {
		t.Fatalf("data sources = %+v, want imported credential column retained", result.Script.DataSources)
	}
	if len(result.Script.Actions) != 1 ||
		result.Script.Actions[0].Headers["X-Token"] != "env-token" {
		t.Fatalf("destination action removed imported environment credential: %+v", result.Script.Actions)
	}
	if strings.Contains(resp.Body.String(), "collection-token") {
		t.Fatalf("response retained shadowed collection credential: %s", resp.Body.String())
	}
}

func TestHandleSavePostmanImportCreateAndReplace(t *testing.T) {
	server := NewServer(ServerConfig{})
	createPayload := map[string]any{
		"script": dsl.Script{
			Name:      "Created Postman",
			Version:   "1.0.0",
			Protocol:  "http",
			ProjectID: "proj-postman",
			Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
			Actions:   []dsl.Action{{Name: "Get", Type: "request", Method: "GET", Target: "/orders"}},
		},
	}
	createBody, err := json.Marshal(createPayload)
	if err != nil {
		t.Fatalf("json.Marshal create returned error: %v", err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman/save", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()

	server.mux.ServeHTTP(createResp, createReq)

	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created struct {
		StoredScript StoredScript     `json:"stored_script"`
		Replaced     bool             `json:"replaced"`
		Preflight    *ReplayPreflight `json:"preflight"`
	}
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("json.Unmarshal create returned error: %v", err)
	}
	if created.StoredScript.ID == "" || created.Replaced || created.Preflight == nil || !created.Preflight.Valid {
		t.Fatalf("created response = %+v", created)
	}

	replacePayload := map[string]any{
		"mode":             "replace",
		"target_script_id": created.StoredScript.ID,
		"script": dsl.Script{
			Name:      "Replaced Postman",
			Version:   "1.0.0",
			Protocol:  "http",
			ProjectID: "proj-postman",
			Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
			Actions:   []dsl.Action{{Name: "List", Type: "request", Method: "GET", Target: "/orders?updated=1"}},
		},
	}
	replaceBody, err := json.Marshal(replacePayload)
	if err != nil {
		t.Fatalf("json.Marshal replace returned error: %v", err)
	}
	replaceReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman/save", bytes.NewReader(replaceBody))
	replaceReq.Header.Set("Content-Type", "application/json")
	replaceResp := httptest.NewRecorder()

	server.mux.ServeHTTP(replaceResp, replaceReq)

	if replaceResp.Code != http.StatusOK {
		t.Fatalf("replace status = %d, want %d body=%s", replaceResp.Code, http.StatusOK, replaceResp.Body.String())
	}
	var replaced struct {
		StoredScript StoredScript `json:"stored_script"`
		Replaced     bool         `json:"replaced"`
	}
	if err := json.Unmarshal(replaceResp.Body.Bytes(), &replaced); err != nil {
		t.Fatalf("json.Unmarshal replace returned error: %v", err)
	}
	if !replaced.Replaced || replaced.StoredScript.ID != created.StoredScript.ID || replaced.StoredScript.Name != "Replaced Postman" {
		t.Fatalf("replaced response = %+v", replaced)
	}
}

func TestHandleImportWSDL(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name":       "Account SOAP",
		"project_id": "proj-soap",
		"content": `<definitions name="AccountService" targetNamespace="urn:accounts" xmlns="http://schemas.xmlsoap.org/wsdl/" xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/">
  <portType name="AccountPortType"><operation name="GetAccount"/></portType>
  <binding name="AccountBinding" type="tns:AccountPortType"><operation name="GetAccount"><soap:operation soapAction="urn:GetAccount"/></operation></binding>
  <service name="AccountService"><port name="AccountPort"><soap:address location="https://soap.example.test/account"/></port></service>
</definitions>`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/wsdl", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			ProjectID string `json:"project_id"`
			Protocol  string `json:"protocol"`
			Actions   []struct {
				Target  string            `json:"target"`
				Headers map[string]string `json:"headers"`
				Body    string            `json:"body"`
			} `json:"actions"`
		} `json:"script"`
		ReviewSections []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.ProjectID != "proj-soap" || result.Script.Protocol != "soap" {
		t.Fatalf("script = %+v", result.Script)
	}
	if len(result.Script.Actions) != 1 || !strings.Contains(result.Script.Actions[0].Body, "GetAccount") {
		t.Fatalf("unexpected actions: %+v", result.Script.Actions)
	}
	if result.Script.Actions[0].Target != "https://soap.example.test/account" {
		t.Fatalf("target = %q, want absolute SOAP endpoint", result.Script.Actions[0].Target)
	}
	if len(result.Script.Actions[0].Headers) != 0 {
		t.Fatalf("headers = %#v, converter should leave SOAP transport headers to runtime", result.Script.Actions[0].Headers)
	}
	assertReviewSection(t, result.ReviewSections, "import_diagnostics_warning", "warning", "WSDL input message at operation GetAccount [warning]: input message/part could not be resolved; generated a placeholder SOAP body")
	assertReviewSection(t, result.ReviewSections, "wsdl_binding", "info", "endpoint: https://soap.example.test/account")
}

func TestHandleImportWSDLMultipartZipWithBindingSelector(t *testing.T) {
	server := NewServer(ServerConfig{})
	var zipBytes bytes.Buffer
	zw := zip.NewWriter(&zipBytes)
	w, err := zw.Create("service.wsdl")
	if err != nil {
		t.Fatalf("zip create returned error: %v", err)
	}
	if _, err := w.Write([]byte(`<?xml version="1.0"?>
<definitions name="DualService"
  targetNamespace="urn:dual"
  xmlns="http://schemas.xmlsoap.org/wsdl/"
  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
  xmlns:soap12="http://schemas.xmlsoap.org/wsdl/soap12/"
  xmlns:tns="urn:dual">
  <message name="LookupInput"><part name="parameters" type="xsd:string"/></message>
  <portType name="DualPortType"><operation name="Lookup"><input message="tns:LookupInput"/></operation></portType>
  <binding name="DualSoap11" type="tns:DualPortType">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Lookup"><soap:operation soapAction="urn:Lookup11"/></operation>
  </binding>
  <binding name="DualSoap12" type="tns:DualPortType">
    <soap12:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <operation name="Lookup"><soap12:operation soapAction="urn:Lookup12"/></operation>
  </binding>
  <service name="DualService">
    <port name="Soap11Port" binding="tns:DualSoap11"><soap:address location="https://dual.example.test/soap11"/></port>
    <port name="Soap12Port" binding="tns:DualSoap12"><soap12:address location="https://dual.example.test/soap12"/></port>
  </service>
</definitions>`)); err != nil {
		t.Fatalf("zip write returned error: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close returned error: %v", err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "bundle.zip")
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	if _, err := part.Write(zipBytes.Bytes()); err != nil {
		t.Fatalf("part.Write returned error: %v", err)
	}
	if err := writer.WriteField("port_name", "Soap12Port"); err != nil {
		t.Fatalf("WriteField returned error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("writer.Close returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/wsdl", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			Config struct {
				BaseURL string `json:"base_url"`
				SOAP    struct {
					Version string `json:"version"`
				} `json:"soap"`
			} `json:"config"`
			Actions []struct {
				Target string `json:"target"`
				SOAP   struct {
					Action string `json:"action"`
				} `json:"soap"`
			} `json:"actions"`
		} `json:"script"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.Config.BaseURL != "https://dual.example.test/soap12" || result.Script.Config.SOAP.Version != "1.2" {
		t.Fatalf("config = %+v, want selected SOAP 1.2 endpoint", result.Script.Config)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Target != "https://dual.example.test/soap12" || result.Script.Actions[0].SOAP.Action != "urn:Lookup12" {
		t.Fatalf("actions = %+v, want selected SOAP 1.2 action", result.Script.Actions)
	}
}

func stringSliceContains(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func openAPIMatrixContains(entries []struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}, id string) bool {
	for _, entry := range entries {
		if entry.ID == id && entry.Status != "" {
			return true
		}
	}
	return false
}

func openAPIImportExtractorContains(entries []struct {
	Name       string `json:"name"`
	Expression string `json:"expression"`
}, name string, expression string) bool {
	for _, entry := range entries {
		if entry.Name == name && entry.Expression == expression {
			return true
		}
	}
	return false
}

func openAPICommitTestScript(name string, projectID string, actions []dsl.Action) dsl.Script {
	return dsl.Script{
		Name:      name,
		Version:   "1.0.0",
		Protocol:  "http",
		ProjectID: projectID,
		Config:    dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Actions:   actions,
	}
}

func openAPICommitTestAction(name string, method string, target string, operationKey string) dsl.Action {
	parts := strings.SplitN(operationKey, " ", 2)
	path := target
	if len(parts) == 2 {
		path = parts[1]
	}
	return dsl.Action{
		Name:   name,
		Type:   "request",
		Method: method,
		Target: target,
		Metadata: map[string]any{
			"openapi": map[string]any{
				"operation_id":  name,
				"method":        method,
				"path":          path,
				"operation_key": operationKey,
			},
		},
	}
}

func asyncAPICommitTestScript(name string, projectID string, actions []dsl.Action) dsl.Script {
	return dsl.Script{
		Name:      name,
		Version:   "1.0.0",
		Protocol:  "kafka",
		ProjectID: projectID,
		Config: dsl.ScriptConfig{Kafka: dsl.KafkaConfig{
			Brokers:          []string{"kafka:9092"},
			SecurityProtocol: "plaintext",
			Mock:             true,
		}},
		Actions: actions,
	}
}

func asyncAPICommitTestAction(name string, actionType string, target string, channel string) dsl.Action {
	body := ""
	if actionType == "kafka_produce" {
		body = `{"id":"sample"}`
	}
	return dsl.Action{
		Name:   name,
		Type:   actionType,
		Target: target,
		Body:   body,
		Metadata: map[string]any{
			"asyncapi_channel": channel,
		},
	}
}

func webAPIMatrixContains(entries []struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	ExecutionStatus string `json:"execution_status"`
}, id string) bool {
	for _, entry := range entries {
		if entry.ID == id && entry.Status != "" && entry.ExecutionStatus != "" {
			return true
		}
	}
	return false
}

func webAPIEnvelopeContains(entries []struct {
	Protocol          string `json:"protocol"`
	RecommendedVUsers int    `json:"recommended_vusers_per_generator"`
}, protocol string) bool {
	for _, entry := range entries {
		if entry.Protocol == protocol && entry.RecommendedVUsers > 0 {
			return true
		}
	}
	return false
}

func webAPIDiagnosticsContains(entries []struct {
	Protocol string   `json:"protocol"`
	Signals  []string `json:"signals"`
}, protocol string) bool {
	for _, entry := range entries {
		if entry.Protocol == protocol && len(entry.Signals) > 0 {
			return true
		}
	}
	return false
}

func devWebMatrixContains(entries []struct {
	ID              string `json:"id"`
	ExecutionStatus string `json:"execution_status"`
}, id string) bool {
	for _, entry := range entries {
		if entry.ID == id && entry.ExecutionStatus != "" {
			return true
		}
	}
	return false
}

func preflightHasCheck(entries []struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}, id string) bool {
	for _, entry := range entries {
		if entry.ID == id && entry.Status != "" {
			return true
		}
	}
	return false
}

func scriptHasPreservedReplayPreflight(metadata map[string]any) bool {
	importMeta, ok := metadata["import"].(map[string]any)
	if !ok {
		return false
	}
	preserved, ok := importMeta["preserved"].(map[string]any)
	if !ok {
		return false
	}
	preflight, ok := preserved["replay_preflight"].(map[string]any)
	if !ok {
		return false
	}
	checks, ok := preflight["checks"].([]any)
	return ok && len(checks) > 0
}

func TestHandleImportAsyncAPI(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name":       "Orders Events",
		"project_id": "proj-async",
		"content": `asyncapi: 3.0.0
info:
  title: Orders
  version: 1.0.0
servers:
  prod:
    host: kafka:9092
    protocol: kafka
channels:
  orderCreated:
    address: orders.created
    messages:
      m:
        payload:
          type: object
          properties:
            orderId:
              type: string
operations:
  send:
    action: send
    channel:
      $ref: '#/channels/orderCreated'
`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/asyncapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			ProjectID string `json:"project_id"`
			Protocol  string `json:"protocol"`
			Actions   []struct {
				Type   string `json:"type"`
				Target string `json:"target"`
			} `json:"actions"`
		} `json:"script"`
		Scripts []struct {
			Protocol string `json:"protocol"`
		} `json:"scripts"`
		Preflight struct {
			Valid  bool `json:"valid"`
			Checks []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"checks"`
		} `json:"preflight"`
		ReviewSections []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
		SourceReview map[string]any `json:"source_review"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.ProjectID != "proj-async" || result.Script.Protocol != "kafka" {
		t.Fatalf("script = %+v", result.Script)
	}
	if len(result.Script.Actions) != 1 || result.Script.Actions[0].Type != "kafka_produce" || result.Script.Actions[0].Target != "orders.created" {
		t.Fatalf("unexpected actions: %+v", result.Script.Actions)
	}
	if len(result.Scripts) != 1 || result.Scripts[0].Protocol != "kafka" {
		t.Fatalf("scripts = %+v, want one kafka script", result.Scripts)
	}
	if !preflightHasCheck(result.Preflight.Checks, "messaging_kafka") {
		t.Fatalf("missing AsyncAPI replay preflight: %+v", result.Preflight)
	}
	if len(result.SourceReview) == 0 {
		t.Fatal("expected root source_review in response")
	}
	assertReviewSection(t, result.ReviewSections, "asyncapi_operations", "info", "send")
}

func TestHandleImportAsyncAPIMultipartPassesOptions(t *testing.T) {
	server := NewServer(ServerConfig{})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "orders.asyncapi.yaml")
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	if _, err := part.Write([]byte(`asyncapi: 3.0.0
info:
  title: Orders
  version: 1.0.0
servers:
  prod:
    host: kafka:9092
    protocol: kafka
channels:
  orderCreated:
    address: orders.created
    messages:
      m:
        payload:
          type: object
          properties:
            orderId:
              type: string
operations:
  send:
    action: send
    channel:
      $ref: '#/channels/orderCreated'
`)); err != nil {
		t.Fatalf("write multipart file returned error: %v", err)
	}
	fields := map[string]string{
		"project_id":            "proj-multipart",
		"script_name":           "Orders Multipart",
		"brokers":               "override-a:9092,override-b:9092",
		"mock":                  "false",
		"include_assertions":    "false",
		"default_think_time_ms": "125",
		"serialization":         "json_schema",
		"schema_registry_url":   "http://registry:8081",
		"schema_id":             "42",
		"subject":               "orders-value",
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("WriteField(%s) returned error: %v", key, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("multipart close returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/asyncapi", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			Name    string `json:"name"`
			Project string `json:"project_id"`
			Config  struct {
				Kafka struct {
					Brokers           []string `json:"brokers"`
					Mock              bool     `json:"mock"`
					Serialization     string   `json:"serialization"`
					SchemaRegistryURL string   `json:"schema_registry_url"`
					SchemaID          int      `json:"schema_id"`
					Subject           string   `json:"subject"`
				} `json:"kafka"`
				ThinkTime struct {
					MeanMs int `json:"mean_ms"`
				} `json:"think_time"`
			} `json:"config"`
			Actions []struct {
				Assertions []any `json:"assertions"`
			} `json:"actions"`
		} `json:"script"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.Name != "Orders_Multipart" || result.Script.Project != "proj-multipart" {
		t.Fatalf("script identity = %+v", result.Script)
	}
	if got := result.Script.Config.Kafka.Brokers; len(got) != 2 || got[0] != "override-a:9092" || got[1] != "override-b:9092" {
		t.Fatalf("brokers = %v, want multipart override", got)
	}
	if result.Script.Config.Kafka.Mock {
		t.Fatal("mock = true, want multipart mock=false passthrough")
	}
	if result.Script.Config.Kafka.Serialization != "json_schema" ||
		result.Script.Config.Kafka.SchemaRegistryURL != "http://registry:8081" ||
		result.Script.Config.Kafka.SchemaID != 42 ||
		result.Script.Config.Kafka.Subject != "orders-value" {
		t.Fatalf("serde config not passed through: %+v", result.Script.Config.Kafka)
	}
	if result.Script.Config.ThinkTime.MeanMs != 125 {
		t.Fatalf("think time mean_ms = %d, want 125", result.Script.Config.ThinkTime.MeanMs)
	}
	if len(result.Script.Actions) != 1 || len(result.Script.Actions[0].Assertions) != 0 {
		t.Fatalf("include_assertions=false not honored: %+v", result.Script.Actions)
	}
}

func TestHandleImportAsyncAPIMixedKafkaJMSPreservesPerScriptPreflight(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name":       "Mixed AsyncAPI",
		"project_id": "proj-mixed",
		"content": `asyncapi: 3.0.0
info:
  title: Mixed
  version: 1.0.0
servers:
  kafkaBroker:
    host: kafka:9092
    protocol: kafka
  amqpBroker:
    host: amqp.example.test:5671
    protocol: amqp
channels:
  orders:
    address: orders.events?order_id=ORD-123
    servers:
      - $ref: '#/servers/kafkaBroker'
    messages:
      m:
        payload:
          type: object
          properties:
            orderId:
              type: string
  shipments:
    address: shipments.events?shipment_id=SHP-123
    servers:
      - $ref: '#/servers/amqpBroker'
    bindings:
      jms:
        destinationType: topic
    messages:
      m:
        payload:
          type: object
          properties:
            shipmentId:
              type: string
operations:
  sendOrder:
    action: send
    channel:
      $ref: '#/channels/orders'
  receiveOrder:
    action: receive
    channel:
      $ref: '#/channels/orders'
  sendShipment:
    action: send
    channel:
      $ref: '#/channels/shipments'
  receiveShipment:
    action: receive
    channel:
      $ref: '#/channels/shipments'
`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/asyncapi", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Scripts []struct {
			Name     string         `json:"name"`
			Protocol string         `json:"protocol"`
			Metadata map[string]any `json:"metadata"`
			Config   struct {
				Kafka struct {
					AutoOffsetReset string `json:"auto_offset_reset"`
				} `json:"kafka"`
				JMS struct {
					BrokerURL       string `json:"broker_url"`
					DestinationType string `json:"destination_type"`
				} `json:"jms"`
			} `json:"config"`
		} `json:"scripts"`
		Preflight struct {
			Checks []struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"checks"`
		} `json:"preflight"`
		Parameterization struct {
			Suggestions []struct {
				ScriptName string `json:"script_name"`
				VarName    string `json:"var_name"`
			} `json:"suggestions"`
		} `json:"parameterization"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Scripts) != 2 {
		t.Fatalf("scripts = %+v, want Kafka + JMS split", result.Scripts)
	}
	scriptNames := make(map[string]struct{}, len(result.Scripts))
	for _, script := range result.Scripts {
		scriptNames[script.Name] = struct{}{}
	}
	suggestionScriptNames := make(map[string]struct{}, len(result.Parameterization.Suggestions))
	for _, suggestion := range result.Parameterization.Suggestions {
		if suggestion.ScriptName != "" {
			suggestionScriptNames[suggestion.ScriptName] = struct{}{}
		}
	}
	for name := range scriptNames {
		if _, ok := suggestionScriptNames[name]; !ok {
			t.Fatalf("parameterization suggestions = %+v, want script-scoped suggestions for %q", result.Parameterization.Suggestions, name)
		}
	}
	if !preflightHasCheck(result.Preflight.Checks, "messaging_kafka") {
		t.Fatalf("top-level preflight = %+v, want Kafka readiness for first script", result.Preflight)
	}
	foundKafka := false
	foundJMS := false
	for _, script := range result.Scripts {
		if !scriptHasPreservedReplayPreflight(script.Metadata) {
			t.Fatalf("%s script missing preserved replay_preflight metadata: %+v", script.Protocol, script.Metadata)
		}
		switch script.Protocol {
		case "kafka":
			foundKafka = true
			if script.Config.Kafka.AutoOffsetReset != "earliest" {
				t.Fatalf("kafka auto_offset_reset = %q, want earliest", script.Config.Kafka.AutoOffsetReset)
			}
		case "jms-solace":
			foundJMS = true
			if script.Config.JMS.BrokerURL != "amqp.example.test:5671" || script.Config.JMS.DestinationType != "topic" {
				t.Fatalf("jms config = %+v, want broker URL + topic destination", script.Config.JMS)
			}
		}
	}
	if !foundKafka || !foundJMS {
		t.Fatalf("scripts = %+v, want Kafka and JMS protocols", result.Scripts)
	}
}

func TestHandleImportBrokerConfigMQSC(t *testing.T) {
	server := NewServer(ServerConfig{})
	mqsc := "DEFINE QLOCAL('ORDER.REQUEST') MAXDEPTH(5000)\nDEFINE TOPIC('EVENTS') TOPICSTR('events/orders')\n"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/broker-config?filename=qm.mqsc&project_id=proj-mq", bytes.NewReader([]byte(mqsc)))
	req.Header.Set("Content-Type", "text/plain")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			ProjectID string `json:"project_id"`
			Protocol  string `json:"protocol"`
			Actions   []struct {
				Type   string `json:"type"`
				Target string `json:"target"`
			} `json:"actions"`
		} `json:"script"`
		ReviewSections []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.ProjectID != "proj-mq" || result.Script.Protocol != "jms-ibmmq" {
		t.Fatalf("script = %+v", result.Script)
	}
	if len(result.Script.Actions) != 4 { // 2 destinations x publish+consume
		t.Fatalf("expected 4 actions, got %+v", result.Script.Actions)
	}
	assertReviewSection(t, result.ReviewSections, "broker_config", "warning", "Format: mqsc")
}

func TestHandleImportBrokerConfigJNDI(t *testing.T) {
	server := NewServer(ServerConfig{})
	jndi := "connectionFactory.cf = amqps://broker.example.test:5671\nqueue.OrderQueue = ORDER.QUEUE\n"
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/broker-config?filename=jndi.properties", bytes.NewReader([]byte(jndi)))
	req.Header.Set("Content-Type", "text/plain")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Script struct {
			Protocol string `json:"protocol"`
		} `json:"script"`
		ReviewSections []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.Protocol != "jms-solace" {
		t.Fatalf("protocol = %q, want jms-solace", result.Script.Protocol)
	}
	assertReviewSection(t, result.ReviewSections, "broker_config", "info", "Format: jndi")
}

func TestHandleBrokerConfigSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/broker-config/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		SupportMatrix []struct {
			ID string `json:"id"`
		} `json:"support_matrix"`
		Notes []string `json:"notes"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.SupportMatrix) == 0 || len(result.Notes) == 0 {
		t.Fatalf("expected non-empty support matrix and notes, got %+v", result)
	}
}

func TestHandleAsyncAPISupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/asyncapi/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		SupportMatrix []struct {
			ID string `json:"id"`
		} `json:"support_matrix"`
		Notes []string `json:"notes"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.SupportMatrix) == 0 || len(result.Notes) == 0 {
		t.Fatalf("expected non-empty support matrix and notes, got %+v", result)
	}
}
