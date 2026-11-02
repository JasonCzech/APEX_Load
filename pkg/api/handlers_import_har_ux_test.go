package api

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// TestHARImportValidateOnlySkipsSourceSecretSession pins what validate_only
// still keeps: the draft save attestation, so the reviewed draft can be saved,
// while the source-secret session is skipped.
func TestHARImportValidateOnlySkipsSourceSecretSession(t *testing.T) {
	server, _, _, projectID := newProjectSecretAPIServer(t)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatal(err)
	}

	harJSON := `{
		"log": {
			"entries": [{
				"request": {
					"method": "POST",
					"url": "https://api.example.test/login",
					"headers": [{"name": "Authorization", "value": "Bearer synthetic-secret-token-value-123456"}],
					"postData": {"mimeType": "application/json", "text": "{\"user\":\"alice\"}"}
				},
				"response": {
					"status": 200,
					"headers": [{"name": "Content-Type", "value": "application/json"}],
					"content": {"mimeType": "application/json", "text": "{\"ok\":true}"}
				}
			}]
		}
	}`

	// 1. validate_only=true (JSON body only) allocates no source-secret
	// session, yet still issues the attestation that saves the draft.
	inspectPayload, err := json.Marshal(map[string]any{
		"name":          "Validate Only Inspect",
		"project_id":    projectID,
		"content":       harJSON,
		"validate_only": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	inspectReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id="+projectID, bytes.NewReader(inspectPayload))
	inspectReq.Header.Set("Content-Type", "application/json")
	inspectResp := httptest.NewRecorder()
	server.mux.ServeHTTP(inspectResp, inspectReq)
	if inspectResp.Code != http.StatusOK {
		t.Fatalf("validate_only status = %d: %s", inspectResp.Code, inspectResp.Body.String())
	}
	var inspectBody map[string]any
	if err := json.Unmarshal(inspectResp.Body.Bytes(), &inspectBody); err != nil {
		t.Fatal(err)
	}
	if sid, _ := inspectBody["source_secret_session_id"].(string); sid != "" {
		t.Fatalf("validate_only allocated source_secret_session_id = %q", sid)
	}
	if _, hasExpiry := inspectBody["import_draft_expires_at"]; !hasExpiry {
		t.Fatalf("validate_only omitted import_draft_expires_at needed to save the draft: %#v", inspectBody)
	}
	// Verify validate_only draft can be saved via POST /api/v1/scripts.
	commitPayload, err := json.Marshal(map[string]any{
		"project_id": projectID,
		"script":     inspectBody["script"],
	})
	if err != nil {
		t.Fatal(err)
	}
	commitReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts?project_id="+projectID, bytes.NewReader(commitPayload))
	commitReq.Header.Set("Content-Type", "application/json")
	commitResp := httptest.NewRecorder()
	server.mux.ServeHTTP(commitResp, commitReq)
	if commitResp.Code != http.StatusCreated && commitResp.Code != http.StatusOK {
		t.Fatalf("save of validate_only draft status = %d: %s", commitResp.Code, commitResp.Body.String())
	}

	// 2. Full conversion (validate_only omitted/false) allocates the protected session normally.
	fullPayload, err := json.Marshal(map[string]any{
		"name":       "Full Convert",
		"project_id": projectID,
		"content":    harJSON,
	})
	if err != nil {
		t.Fatal(err)
	}
	fullReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id="+projectID, bytes.NewReader(fullPayload))
	fullReq.Header.Set("Content-Type", "application/json")
	fullResp := httptest.NewRecorder()
	server.mux.ServeHTTP(fullResp, fullReq)
	if fullResp.Code != http.StatusOK {
		t.Fatalf("full convert status = %d: %s", fullResp.Code, fullResp.Body.String())
	}
	var fullBody map[string]any
	if err := json.Unmarshal(fullResp.Body.Bytes(), &fullBody); err != nil {
		t.Fatal(err)
	}
	if sid, _ := fullBody["source_secret_session_id"].(string); strings.TrimSpace(sid) == "" {
		t.Fatalf("full convert did not allocate source_secret_session_id: %#v", fullBody)
	}
}

// TestHARImportValidateOnlySkipsSourceFullTextOnEveryChannel covers the
// retention validate_only still skips, the full text of a source file cut at
// the review preview limit, on each channel that carries the flag: the JSON
// body, the query string of a JSON upload, and a multipart field.
func TestHARImportValidateOnlySkipsSourceFullTextOnEveryChannel(t *testing.T) {
	const projectID = "validate-only-source-text"
	server := sourceReviewTextTestServer(t, projectID)
	// One request whose response body pushes the capture past the preview
	// limit, so a full import keeps the whole text beside the review.
	harJSON := `{"log":{"entries":[{"request":{"method":"GET","url":"https://api.example.test/catalog","headers":[]},` +
		`"response":{"status":200,"headers":[{"name":"Content-Type","value":"text/plain"}],` +
		`"content":{"mimeType":"text/plain","text":"` + strings.Repeat("catalog-row ", sourceview.MaxFileChars/8) + `"}}}]}}`
	if len(harJSON) <= sourceview.MaxFileChars {
		t.Fatalf("fixture is %d bytes, want more than the %d-character preview", len(harJSON), sourceview.MaxFileChars)
	}

	postJSON := func(t *testing.T, query string, fields map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		fields["project_id"] = projectID
		fields["content"] = harJSON
		payload, err := json.Marshal(fields)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?filename=catalog.har&project_id="+projectID+query, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}
	postMultipart := func(t *testing.T, validateOnly bool) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("project_id", projectID)
		if validateOnly {
			_ = mw.WriteField("validate_only", "true")
		}
		file, err := mw.CreateFormFile("file", "catalog.har")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write([]byte(harJSON))
		_ = mw.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}
	fullTextDigests := func(t *testing.T, resp *httptest.ResponseRecorder) []string {
		t.Helper()
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
		}
		var decoded struct {
			SourceReview *sourceview.Review `json:"source_review"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.SourceReview == nil || len(decoded.SourceReview.SourceFiles) == 0 {
			t.Fatalf("response has no source review: %s", resp.Body.String())
		}
		var digests []string
		truncated := false
		for _, file := range decoded.SourceReview.SourceFiles {
			truncated = truncated || file.Truncated
			if file.FullSHA256 != "" {
				digests = append(digests, file.FullSHA256)
			}
		}
		if !truncated {
			t.Fatalf("source review kept the whole capture in its preview; the fixture must exceed it")
		}
		return digests
	}

	// The controls: without the flag each channel keeps the full text, so the
	// validate_only cases below fail if the flag stops being read.
	if digests := fullTextDigests(t, postJSON(t, "", map[string]any{"name": "Full JSON"})); len(digests) == 0 {
		t.Fatal("a full JSON import kept no source full text; the validate_only cases would prove nothing")
	}
	if digests := fullTextDigests(t, postMultipart(t, false)); len(digests) == 0 {
		t.Fatal("a full multipart import kept no source full text; the validate_only case would prove nothing")
	}

	cases := []struct {
		name string
		post func(t *testing.T) *httptest.ResponseRecorder
	}{
		{"json body", func(t *testing.T) *httptest.ResponseRecorder {
			return postJSON(t, "", map[string]any{"name": "Body Flag", "validate_only": true})
		}},
		{"json query", func(t *testing.T) *httptest.ResponseRecorder {
			return postJSON(t, "&validate_only=true", map[string]any{"name": "Query Flag"})
		}},
		{"multipart field", func(t *testing.T) *httptest.ResponseRecorder {
			return postMultipart(t, true)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if digests := fullTextDigests(t, tc.post(t)); len(digests) != 0 {
				t.Fatalf("validate_only kept source full text %v", digests)
			}
		})
	}
}

func TestHARPairDuplicateCaptureSkipsDiffWithActionableReason(t *testing.T) {
	capture := apiCredentialCorrelationHAR("duplicate-token-1234567890")

	t.Run("json identical secondary_content", func(t *testing.T) {
		resp, decoded := postHARPairJSON(t, "/api/v1/scripts/import/har", map[string]any{
			"name":              "Duplicate Pair",
			"content":           capture,
			"secondary_content": "  " + capture + "\n",
		})
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
		}
		if decoded.Summary.HARDiff == nil || decoded.Summary.HARDiff.Compared || !strings.Contains(decoded.Summary.HARDiff.Reason, "identical captures") {
			t.Fatalf("har_diff = %#v, want compared=false with identical captures reason", decoded.Summary.HARDiff)
		}
		if decoded.Summary.ArtifactMode != har.ArtifactModeDesignHAR {
			t.Fatalf("artifact_mode = %q, want %q", decoded.Summary.ArtifactMode, har.ArtifactModeDesignHAR)
		}
	})

	t.Run("multipart raw baseline vs zipped identical secondary_file", func(t *testing.T) {
		var zipBuf bytes.Buffer
		zw := zip.NewWriter(&zipBuf)
		entry, err := zw.Create("capture.har")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(capture)); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}

		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		f1, err := mw.CreateFormFile("file", "run1.har")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f1.Write([]byte(capture))
		f2, err := mw.CreateFormFile("secondary_file", "run1.zip")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = f2.Write(zipBuf.Bytes())
		_ = mw.Close()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp := httptest.NewRecorder()
		NewServer(ServerConfig{}).mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
		}
		var decoded harPairImportResponse
		if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Summary.HARDiff == nil || decoded.Summary.HARDiff.Compared || !strings.Contains(decoded.Summary.HARDiff.Reason, "identical captures") {
			t.Fatalf("har_diff = %#v, want compared=false for zipped identical secondary_file", decoded.Summary.HARDiff)
		}
	})

	// The baseline is salvaged before the import compares it, so the raw
	// comparison never equals it byte for byte; the comparison job salvages
	// its copy the same way and must still see one capture, not diff it
	// against itself.
	t.Run("same truncated capture salvaged on both sides", func(t *testing.T) {
		truncated := truncatedSalvageHAR()
		resp, decoded := postHARPairJSON(t, "/api/v1/scripts/import/har", map[string]any{
			"name":              "Duplicate Truncated Pair",
			"content":           truncated,
			"secondary_content": truncated,
			"salvage_truncated": true,
		})
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
		}
		if decoded.Summary.HARDiff == nil || decoded.Summary.HARDiff.Compared || !strings.Contains(decoded.Summary.HARDiff.Reason, "identical captures") {
			t.Fatalf("har_diff = %#v, want compared=false with identical captures reason", decoded.Summary.HARDiff)
		}
	})
}

// TestPrepareHARComparisonInflatesOnlyInsideTheJob pins that a zipped
// comparison is unwrapped by the comparison job alone: one that turns out to be
// the baseline stops before conversion, and a bad archive fails only the
// comparison, with the archive's own error.
func TestPrepareHARComparisonInflatesOnlyInsideTheJob(t *testing.T) {
	capture := apiCredentialCorrelationHAR("zipped-duplicate-token-1234567890")
	zipped := func(names ...string) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, name := range names {
			entry, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = entry.Write([]byte(capture))
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	if identicalHARComparisonPayload([]byte(capture), zipped("run.har")) {
		t.Fatal("identicalHARComparisonPayload inflated a zipped comparison; only the comparison job may")
	}
	prepared, _, err := prepareHARComparison(context.Background(), zipped("run.har"), []byte(capture), har.ConvertOptions{})
	if !errors.Is(err, errIdenticalHARComparison) || prepared != nil {
		t.Fatalf("prepareHARComparison(zipped baseline) = %v, %v; want errIdenticalHARComparison and no conversion", prepared, err)
	}
	_, _, err = prepareHARComparison(context.Background(), zipped("one.har", "two.har"), []byte(capture), har.ConvertOptions{})
	if err == nil || !strings.Contains(err.Error(), "2 .har files") {
		t.Fatalf("prepareHARComparison(two-capture archive) err = %v, want the archive error", err)
	}
}

func TestHARImportBaseURLValidation(t *testing.T) {
	capture := `{"log":{"entries":[{"request":{"method":"GET","url":"https://api.example.test/ping"},"response":{"status":200,"content":{"text":"ok"}}}]}}`

	for _, bad := range []string{"acme.my.salesforce.com", "ftp://acme.my.salesforce.com", "ftp://files{{x}}", "javascript:{{x}}", "not a url {{"} {
		resp, _ := postHARPairJSON(t, "/api/v1/scripts/import/har", map[string]any{
			"name":    "Bad BaseURL",
			"content": capture,
			"options": map[string]any{"base_url": bad},
		})
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("base_url %q status = %d, want 400: %s", bad, resp.Code, resp.Body.String())
		}
	}

	for _, good := range []string{"https://acme.my.salesforce.com", "{{param.base_url}}"} {
		resp, _ := postHARPairJSON(t, "/api/v1/scripts/import/har", map[string]any{
			"name":    "Good BaseURL",
			"content": capture,
			"options": map[string]any{"base_url": good},
		})
		if resp.Code != http.StatusOK {
			t.Fatalf("base_url %q status = %d, want 200: %s", good, resp.Code, resp.Body.String())
		}
	}
}

func TestHARImportBaseURLTemplatesMustBeWellFormed(t *testing.T) {
	for value, want := range map[string]bool{
		"":                                  true,
		"https://acme.my.salesforce.com":    true,
		"{{param.base_url}}":                true,
		"{{BaseURL}}/app":                   true,
		"https://{{env}}.example.test":      true,
		"http://api.example.test/{{ver}}":   true,
		"https://{{host}}:{{port}}/app":     true,
		"https://api.example.test:{{port}}": true,
		"ftp://files{{x}}":                  false,
		"javascript:{{x}}":                  false,
		"not a url {{":                      false,
		"{{unclosed":                        false,
		"{{}}":                              false,
		"https://api.example.test/}}":       false,
		"{{a}} {{b}}":                       false,
		"api.example.test/{{path}}":         false,
	} {
		if got := harImportBaseURLAccepted(value); got != want {
			t.Errorf("harImportBaseURLAccepted(%q) = %v, want %v", value, got, want)
		}
	}
}

func TestSavedHARImportRefreshesReplayPreflightWhenBodyBlockerRemoved(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })

	// Entry 1 has a valid request; Entry 2 has an unexported POST body (params present, text missing).
	harJSON := `{
		"log": {
			"entries": [
				{
					"request": {
						"method": "GET",
						"url": "https://api.example.test/items",
						"headers": []
					},
					"response": {
						"status": 200,
						"headers": [{"name": "Content-Type", "value": "application/json"}],
						"content": {"mimeType": "application/json", "text": "{\"items\":[]}"}
					}
				},
				{
					"request": {
						"method": "POST",
						"url": "https://api.example.test/submit",
						"headers": [
							{"name": "Content-Type", "value": "application/x-www-form-urlencoded"},
							{"name": "Content-Length", "value": "128"}
						]
					},
					"response": {
						"status": 200,
						"headers": [],
						"content": {"text": "ok"}
					}
				}
			]
		}
	}`

	importPayload, _ := json.Marshal(map[string]any{
		"name":       "Blocked HAR",
		"project_id": storage.DefaultProjectID,
		"content":    harJSON,
	})
	importReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id="+storage.DefaultProjectID, bytes.NewReader(importPayload))
	importReq.Header.Set("Content-Type", "application/json")
	importResp := httptest.NewRecorder()
	server.mux.ServeHTTP(importResp, importReq)
	if importResp.Code != http.StatusOK {
		t.Fatalf("import status = %d: %s", importResp.Code, importResp.Body.String())
	}

	var imported struct {
		Script    json.RawMessage `json:"script"`
		Preflight *struct {
			Valid bool `json:"valid"`
		} `json:"preflight"`
	}
	if err := json.Unmarshal(importResp.Body.Bytes(), &imported); err != nil {
		t.Fatal(err)
	}
	if imported.Preflight == nil || imported.Preflight.Valid {
		t.Fatalf("imported preflight = %+v, want valid=false due to har_request_body_not_exported", imported.Preflight)
	}

	createPayload, err := json.Marshal(map[string]any{
		"project_id": storage.DefaultProjectID,
		"script":     imported.Script,
	})
	if err != nil {
		t.Fatal(err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(createPayload))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", createResp.Code, createResp.Body.String())
	}
	var created StoredScript
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if pre := preservedReplayPreflight(created.Script); pre == nil || pre.Valid {
		t.Fatalf("saved preflight = %+v, want valid=false while blocked action remains", pre)
	}

	// Unrelated edit leaves execution_blockers intact -> preflight MUST stay valid=false.
	if resp := putAuthoringEdit(t, server, created.ID, func(_ *testing.T, document map[string]any) {
		document["name"] = "Blocked HAR Renamed"
	}); resp.Code != http.StatusOK {
		t.Fatalf("PUT unrelated = %d: %s", resp.Code, resp.Body.String())
	}
	stillBlocked := durableRepairSnapshot(t, server, created.ID).Script
	if pre := preservedReplayPreflight(stillBlocked); pre == nil || pre.Valid {
		t.Fatalf("preflight unexpectedly cleared without removing execution_blockers: %+v", pre)
	}

	// Removing the blocked action via PUT refreshes replay_preflight to valid=true.
	if resp := putAuthoringEdit(t, server, created.ID, func(_ *testing.T, document map[string]any) {
		document["actions"] = stripBlockedJSONActions(document["actions"])
	}); resp.Code != http.StatusOK {
		t.Fatalf("PUT remove blocked action = %d: %s", resp.Code, resp.Body.String())
	}
	unblocked := durableRepairSnapshot(t, server, created.ID).Script
	if pre := preservedReplayPreflight(unblocked); pre == nil || !pre.Valid {
		t.Fatalf("preflight after deleting blocked action = %+v, want valid=true", pre)
	}
}

func TestUncapturedAuraTokenReportsHonestPreflightWarning(t *testing.T) {
	buildOneAppHAR := func(includeSetCookie bool) []byte {
		var oneAppRespHeaders []map[string]string
		oneAppRespHeaders = append(oneAppRespHeaders, map[string]string{"name": "Content-Type", "value": "text/html"})
		if includeSetCookie {
			oneAppRespHeaders = append(oneAppRespHeaders, map[string]string{
				"name":  "Set-Cookie",
				"value": "__Host-ERIC_PROD-999=eyJhbGciOiJIUzI1NiJ9.syntheticPayload.syntheticSig; Path=/; Secure; HttpOnly",
			})
		}
		payload := map[string]any{
			"log": map[string]any{
				"entries": []any{
					map[string]any{
						"request": map[string]any{"method": "GET", "url": "https://acme.lightning.force.com/one/one.app", "headers": []any{}},
						"response": map[string]any{
							"status":  200,
							"headers": oneAppRespHeaders,
							"content": map[string]any{
								"mimeType": "text/html",
								"text":     `<!doctype html><html><script>var auraConfig = {"eikoocnekot":"__Host-ERIC_PROD-999"};</script></html>`,
							},
						},
					},
					map[string]any{
						"request": map[string]any{
							"method":  "POST",
							"url":     "https://acme.lightning.force.com/aura?r=1",
							"headers": []map[string]string{{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}},
							"postData": map[string]any{
								"mimeType": "application/x-www-form-urlencoded",
								"text":     "message=%7B%22actions%22%3A%5B%7B%22id%22%3A%221%3Ba%22%2C%22descriptor%22%3A%22aura%3A%2F%2FRecordUiController%2FACTION%24getRecord%22%2C%22callingDescriptor%22%3A%22UNKNOWN%22%2C%22params%22%3A%7B%7D%7D%5D%7D&aura.context=%7B%22mode%22%3A%22PROD%22%7D&aura.token=eyJhbGciOiJIUzI1NiJ9.syntheticPayload.syntheticSig",
							},
						},
						"response": map[string]any{
							"status":  200,
							"headers": []map[string]string{{"name": "Content-Type", "value": "application/json"}},
							"content": map[string]any{"mimeType": "application/json", "text": `{"actions":[{"id":"1;a","state":"SUCCESS"}]}`},
						},
					},
				},
			},
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}

	server := NewServer(ServerConfig{})
	checkVariablesStatus := func(harBytes []byte) (valid bool, varStatus, varDetail string) {
		body, _ := json.Marshal(map[string]any{"name": "Aura Token Preflight", "content": string(harBytes)})
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
		}
		var out struct {
			Preflight struct {
				Valid  bool `json:"valid"`
				Checks []struct {
					ID     string `json:"id"`
					Status string `json:"status"`
					Detail string `json:"detail"`
				} `json:"checks"`
			} `json:"preflight"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		for _, c := range out.Preflight.Checks {
			if c.ID == "variables" {
				return out.Preflight.Valid, c.Status, c.Detail
			}
		}
		t.Fatal("missing variables check in preflight")
		return false, "", ""
	}

	validUncap, statusUncap, detailUncap := checkVariablesStatus(buildOneAppHAR(false))
	if !validUncap || statusUncap != "warn" || !strings.Contains(detailUncap, "unverified in sanitized HAR capture") {
		t.Fatalf("uncaptured aura.token preflight = (valid=%v, status=%q, detail=%q), want (true, warn, unverified...)", validUncap, statusUncap, detailUncap)
	}

	validCap, statusCap, _ := checkVariablesStatus(buildOneAppHAR(true))
	if !validCap || statusCap != "pass" {
		t.Fatalf("captured aura.token preflight = (valid=%v, status=%q), want (true, pass)", validCap, statusCap)
	}
}

func TestHARImportOpenAPIDocumentsPairComparisonAndValidateOnly(t *testing.T) {
	data, err := os.ReadFile("apidocs/swagger.yaml")
	if err != nil {
		t.Fatal(err)
	}
	spec := string(data)
	idx := strings.Index(spec, "  /api/v1/scripts/import/har:\n")
	if idx < 0 {
		t.Fatal("missing /api/v1/scripts/import/har in swagger.yaml")
	}
	end := strings.Index(spec[idx+30:], "\n  /api/v1/")
	section := spec[idx:]
	if end > 0 {
		section = spec[idx : idx+30+end]
	}
	for _, token := range []string{
		"multipart/form-data:",
		"application/json:",
		"application/octet-stream:",
		"secondary_file:",
		"secondary_content:",
		"validate_only",
		"project_id",
		"filter_client_telemetry",
	} {
		if !strings.Contains(section, token) {
			t.Errorf("/api/v1/scripts/import/har OpenAPI section missing %q", token)
		}
	}
}

func buildSyntheticSalesforceHARForPairE2E(t *testing.T, sessionSuffix, recordID string) []byte {
	t.Helper()
	const (
		loginHost     = "https://acme--sandbox.sandbox.my.salesforce.com"
		lightningHost = "https://acme--sandbox.sandbox.lightning.force.com"
	)
	rawSID := "00Dxx0000001gEREAY!AQEAQSyntheticSession" + sessionSuffix
	escapedSID := url.QueryEscape(rawSID)

	searchAuraMsg := `{"actions":[{"id":"11;a","descriptor":"aura://ApexActionController/ACTION$execute","callingDescriptor":"UNKNOWN","params":{"namespace":"","classname":"InteractionSearchController","method":"findCustomer","params":{"query":"Acme"}}}]}`
	searchAuraBody := "message=" + url.QueryEscape(searchAuraMsg) + "&aura.context=%7B%22mode%22%3A%22PROD%22%2C%22fwuid%22%3A%22fw-synth-1%22%2C%22app%22%3A%22one%3Aone%22%7D&aura.token=eyJhbGciOiJIUzI1NiJ9.synth-" + sessionSuffix
	searchAuraResp := `{"actions":[{"id":"11;a","state":"SUCCESS","returnValue":{"interactionId":"` + recordID + `","staticAppId":"06mxx0000008StaticA"}}],"context":{"fwuid":"fw-synth-1"}}`

	detailAuraMsg := `{"actions":[{"id":"12;a","descriptor":"aura://RecordUiController/ACTION$getRecordWithFields","callingDescriptor":"UNKNOWN","params":{"recordId":"` + recordID + `","appId":"06mxx0000008StaticA"}}]}`
	detailAuraBody := "message=" + url.QueryEscape(detailAuraMsg) + "&aura.context=%7B%22mode%22%3A%22PROD%22%2C%22fwuid%22%3A%22fw-synth-1%22%2C%22app%22%3A%22one%3Aone%22%7D&aura.token=eyJhbGciOiJIUzI1NiJ9.synth-" + sessionSuffix

	telemetryMsg := `{"actions":[{"id":"13;a","descriptor":"serviceComponent://ui.force.components.controllers.instrumentation.InstrumentationBeaconController/ACTION$send","callingDescriptor":"UNKNOWN","params":{}}]}`
	telemetryBody := "message=" + url.QueryEscape(telemetryMsg) + "&aura.context=%7B%22mode%22%3A%22PROD%22%7D&aura.token=eyJhbGciOiJIUzI1NiJ9.synth-" + sessionSuffix

	doc := map[string]any{
		"log": map[string]any{
			"version": "1.2",
			"creator": map[string]any{"name": "WebInspector", "version": "537.36"},
			"entries": []any{
				map[string]any{
					"startedDateTime": "2026-11-01T12:00:00.000Z",
					"time":            45,
					"request": map[string]any{
						"method":      "POST",
						"url":         loginHost + "/",
						"httpVersion": "HTTP/2.0",
						"headers":     []map[string]string{{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}},
						"postData": map[string]any{
							"mimeType": "application/x-www-form-urlencoded",
							"text":     "un=perf.user%40acme.example.test&pw=SynthPass%212026&Login=Log+In",
						},
					},
					"response": map[string]any{
						"status":      302,
						"httpVersion": "HTTP/2.0",
						"headers":     []map[string]string{{"name": "Location", "value": loginHost + "/secur/frontdoor.jsp?sid=" + escapedSID}},
						"content":     map[string]any{"mimeType": "text/html", "text": ""},
					},
				},
				map[string]any{
					"startedDateTime": "2026-11-01T12:00:00.120Z",
					"time":            35,
					"request": map[string]any{
						"method":      "GET",
						"url":         loginHost + "/secur/frontdoor.jsp?sid=" + escapedSID,
						"httpVersion": "HTTP/2.0",
						"headers":     []any{},
						"queryString": []map[string]string{{"name": "sid", "value": rawSID}},
					},
					"response": map[string]any{
						"status":      302,
						"httpVersion": "HTTP/2.0",
						"headers":     []map[string]string{{"name": "Location", "value": lightningHost + "/one/one.app"}},
						"content":     map[string]any{"mimeType": "text/html", "text": ""},
					},
				},
				map[string]any{
					"startedDateTime": "2026-11-01T12:00:00.300Z",
					"time":            90,
					"request": map[string]any{
						"method":      "GET",
						"url":         lightningHost + "/one/one.app",
						"httpVersion": "HTTP/2.0",
						"headers":     []any{},
					},
					"response": map[string]any{
						"status":      200,
						"httpVersion": "HTTP/2.0",
						"headers":     []map[string]string{{"name": "Content-Type", "value": "text/html; charset=utf-8"}},
						"content": map[string]any{
							"mimeType": "text/html",
							"text":     `<!doctype html><html><head><title>Lightning Experience</title><script>var auraConfig={"eikoocnekot":"__Host-ERIC_PROD"};</script></head><body></body></html>`,
						},
					},
				},
				map[string]any{
					"startedDateTime": "2026-11-01T12:00:03.000Z",
					"time":            110,
					"request": map[string]any{
						"method":      "POST",
						"url":         lightningHost + "/aura?r=1",
						"httpVersion": "HTTP/2.0",
						"headers":     []map[string]string{{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}},
						"postData":    map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": searchAuraBody},
					},
					"response": map[string]any{
						"status":      200,
						"httpVersion": "HTTP/2.0",
						"headers":     []map[string]string{{"name": "Content-Type", "value": "application/json"}},
						"content":     map[string]any{"mimeType": "application/json", "text": searchAuraResp},
					},
				},
				map[string]any{
					"startedDateTime": "2026-11-01T12:00:05.500Z",
					"time":            85,
					"request": map[string]any{
						"method":      "POST",
						"url":         lightningHost + "/aura?r=2",
						"httpVersion": "HTTP/2.0",
						"headers":     []map[string]string{{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}},
						"postData":    map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": detailAuraBody},
					},
					"response": map[string]any{
						"status":      200,
						"httpVersion": "HTTP/2.0",
						"headers":     []map[string]string{{"name": "Content-Type", "value": "application/json"}},
						"content":     map[string]any{"mimeType": "application/json", "text": `{"actions":[{"id":"12;a","state":"SUCCESS","returnValue":{"ok":true}}]}`},
					},
				},
				// Telemetry beacon omitted by default FilterClientTelemetry.
				map[string]any{
					"startedDateTime": "2026-11-01T12:00:06.100Z",
					"time":            20,
					"request": map[string]any{
						"method":      "POST",
						"url":         lightningHost + "/aura?r=3&InstrumentationBeaconController.send=1",
						"httpVersion": "HTTP/2.0",
						"headers":     []map[string]string{{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}},
						"postData":    map[string]any{"mimeType": "application/x-www-form-urlencoded", "text": telemetryBody},
					},
					"response": map[string]any{
						"status":      200,
						"httpVersion": "HTTP/2.0",
						"headers":     []map[string]string{{"name": "Content-Type", "value": "application/json"}},
						"content":     map[string]any{"mimeType": "application/json", "text": `{"actions":[{"id":"13;a","state":"SUCCESS"}]}`},
					},
				},
				// Large POST whose request body was omitted by browser HAR export (har_request_body_not_exported).
				map[string]any{
					"startedDateTime": "2026-11-01T12:00:08.500Z",
					"time":            60,
					"request": map[string]any{
						"method":      "POST",
						"url":         lightningHost + "/aura?r=4",
						"httpVersion": "HTTP/2.0",
						"bodySize":    96719,
						"headers": []map[string]string{
							{"name": "Content-Type", "value": "application/x-www-form-urlencoded"},
							{"name": "Content-Length", "value": "96719"},
						},
					},
					"response": map[string]any{
						"status":      200,
						"httpVersion": "HTTP/2.0",
						"headers":     []map[string]string{{"name": "Content-Type", "value": "application/json"}},
						"content":     map[string]any{"mimeType": "application/json", "text": `{"actions":[{"id":"14;a","state":"SUCCESS"}]}`},
					},
				},
				map[string]any{
					"startedDateTime": "2026-11-01T12:00:11.000Z",
					"time":            30,
					"request": map[string]any{
						"method":      "GET",
						"url":         loginHost + "/secur/logout.jsp",
						"httpVersion": "HTTP/2.0",
						"headers":     []any{},
					},
					"response": map[string]any{
						"status":      302,
						"httpVersion": "HTTP/2.0",
						"headers":     []map[string]string{{"name": "Location", "value": loginHost + "/"}},
						"content":     map[string]any{"mimeType": "text/html", "text": ""},
					},
				},
			},
		},
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestSyntheticSalesforceHARPairEndToEndWorkflow(t *testing.T) {
	server, store := newAuditTestServer(t)
	t.Cleanup(func() { store.Close() })

	b1 := buildSyntheticSalesforceHARForPairE2E(t, "RunA1001", "a3Cxx000000bcUXEAY")
	b2 := buildSyntheticSalesforceHARForPairE2E(t, "RunB2002", "a3Cxx000000bcUYEAZ")

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	f1, err := mw.CreateFormFile("file", "acme-sandbox-login-1.har")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f1.Write(b1); err != nil {
		t.Fatal(err)
	}
	f2, err := mw.CreateFormFile("secondary_file", "acme-sandbox-login-2.har")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f2.Write(b2); err != nil {
		t.Fatal(err)
	}
	_ = mw.WriteField("salvage_truncated", "true")
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id="+storage.DefaultProjectID, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("import status = %d: %s", resp.Code, resp.Body.String())
	}

	var decoded struct {
		Script  dsl.Script          `json:"script"`
		Stats   sample.ConvertStats `json:"stats"`
		Summary struct {
			GenerationMode string `json:"generation_mode"`
			HARDiff        *struct {
				Compared          bool `json:"compared"`
				DynamicCandidates int  `json:"dynamic_candidates"`
			} `json:"har_diff"`
		} `json:"summary"`
		Preflight struct {
			Valid bool `json:"valid"`
		} `json:"preflight"`
	}
	var rawEnvelope map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body.Bytes(), &rawEnvelope); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Summary.GenerationMode != "salesforce_native_http" {
		t.Fatalf("generation_mode = %q, want salesforce_native_http", decoded.Summary.GenerationMode)
	}
	if decoded.Summary.HARDiff == nil || !decoded.Summary.HARDiff.Compared || decoded.Summary.HARDiff.DynamicCandidates == 0 {
		t.Fatalf("har_diff = %+v, want compared=true with dynamic candidates", decoded.Summary.HARDiff)
	}
	if len(decoded.Script.Init) == 0 {
		t.Fatal("expected desktop sign-in chain hoisted into script.Init")
	}
	if len(decoded.Script.Teardown) == 0 {
		t.Fatal("expected logout.jsp hoisted into script.Teardown")
	}

	countBlocked := func(actions []dsl.Action) int {
		n := 0
		var walk func([]dsl.Action)
		walk = func(list []dsl.Action) {
			for _, a := range list {
				raw, _ := json.Marshal(a.Metadata)
				if strings.Contains(string(raw), sample.HARRequestBodyNotExportedCode) {
					n++
				}
				if a.Params != nil {
					if reqsRaw, ok := a.Params["requests"]; ok {
						reqsJSON, _ := json.Marshal(reqsRaw)
						var subActions []dsl.Action
						if json.Unmarshal(reqsJSON, &subActions) == nil {
							walk(subActions)
						}
					}
				}
				if a.Control != nil {
					walk(a.Control.Steps)
					walk(a.Control.Else)
					walk(a.Control.Default)
					for _, c := range a.Control.Cases {
						walk(c.Steps)
					}
				}
			}
		}
		walk(actions)
		return n
	}
	blocked := countBlocked(decoded.Script.Init) + countBlocked(decoded.Script.Actions) + countBlocked(decoded.Script.Teardown)
	if blocked != 1 {
		t.Fatalf("blocked unexported-body actions = %d, want 1 (the bodiless r=4 POST)", blocked)
	}
	// The beacon carries a body, so the blocked count above cannot see it;
	// default telemetry filtering is pinned by the stats and the script.
	if decoded.Stats.FilteredTelemetry < 1 {
		t.Fatalf("stats.filtered_telemetry = %d, want the InstrumentationBeaconController POST filtered by default", decoded.Stats.FilteredTelemetry)
	}
	// Only the steps: the preserved source review still shows the capture.
	stepsJSON, err := json.Marshal([][]dsl.Action{decoded.Script.Init, decoded.Script.Actions, decoded.Script.Teardown})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stepsJSON), "InstrumentationBeaconController") {
		t.Fatal("telemetry beacon InstrumentationBeaconController survived as a step; default filtering should drop it")
	}
	if decoded.Preflight.Valid {
		t.Fatal("import preflight valid = true, want false while the unexported-body POST blocks replay")
	}

	// Stage 2: Exclude the unexported-body action via POST /api/v1/scripts/import/exclude-actions,
	// verifying attestation rotation + replay_preflight.valid == true before saving.
	excludePayload, err := json.Marshal(map[string]any{
		"script":                    rawEnvelope["script"],
		"exclude_unexported_bodies": true,
	})
	if err != nil {
		t.Fatal(err)
	}
	excludeReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/exclude-actions", bytes.NewReader(excludePayload))
	excludeReq.Header.Set("Content-Type", "application/json")
	excludeResp := httptest.NewRecorder()
	server.mux.ServeHTTP(excludeResp, excludeReq)
	if excludeResp.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/scripts/import/exclude-actions status = %d: %s", excludeResp.Code, excludeResp.Body.String())
	}
	var excluded struct {
		Script       json.RawMessage  `json:"script"`
		RemovedCount int              `json:"removed_count"`
		Preflight    *ReplayPreflight `json:"preflight"`
	}
	if err := json.Unmarshal(excludeResp.Body.Bytes(), &excluded); err != nil {
		t.Fatal(err)
	}
	if excluded.RemovedCount != 1 {
		t.Fatalf("removed_count = %d, want 1", excluded.RemovedCount)
	}
	if excluded.Preflight == nil || !excluded.Preflight.Valid {
		t.Fatalf("Stage 2 preflight after excluding unexported-body action = %+v, want Valid=true", excluded.Preflight)
	}

	// Save the cleaned, re-attested draft via POST /api/v1/scripts/import/commit-batch and verify durable replay_preflight.valid == true.
	commitBody, err := json.Marshal(map[string]any{
		"scripts":        []json.RawMessage{excluded.Script},
		"operation_id":   "har-ux-e2e-synthetic-sf-pair",
		"source_profile": "har",
	})
	if err != nil {
		t.Fatal(err)
	}
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/commit-batch?project_id="+storage.DefaultProjectID, bytes.NewReader(commitBody))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusOK && createResp.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/scripts/import/commit-batch status = %d: %s", createResp.Code, createResp.Body.String())
	}
	var batchSaved struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(createResp.Body.Bytes(), &batchSaved); err != nil {
		t.Fatal(err)
	}
	if len(batchSaved.Scripts) != 1 {
		t.Fatalf("saved scripts = %d, want 1", len(batchSaved.Scripts))
	}
	saved := batchSaved.Scripts[0]
	finalScript := durableRepairSnapshot(t, server, saved.ID).Script
	finalPre := preservedReplayPreflight(finalScript)
	if finalPre == nil || !finalPre.Valid {
		t.Fatalf("final durable replay_preflight after Stage 2 Exclude & commit-batch = %+v, want Valid=true", finalPre)
	}
}

func TestExcludeImportDraftActions(t *testing.T) {
	server := NewServer(ServerConfig{})

	postExclude := func(payload map[string]any) *httptest.ResponseRecorder {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/exclude-actions", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		return resp
	}

	t.Run("rejects missing script or criteria", func(t *testing.T) {
		if resp := postExclude(map[string]any{"exclude_unexported_bodies": true}); resp.Code != http.StatusBadRequest {
			t.Fatalf("missing script status = %d, want 400", resp.Code)
		}
		if resp := postExclude(map[string]any{
			"script": map[string]any{"name": "S", "version": "1.0.0", "protocol": "http", "config": map[string]any{"base_url": "https://example.test"}},
		}); resp.Code != http.StatusBadRequest {
			t.Fatalf("missing criteria status = %d, want 400", resp.Code)
		}
	})

	t.Run("prunes emptied source_transaction markers and blocked http_sequence sub-requests", func(t *testing.T) {
		script := &dsl.Script{
			Name:     "Exclude_Transactions_And_Sequence",
			Version:  "1.0.0",
			Protocol: "http",
			Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
			Actions: []dsl.Action{
				dsl.TransactionMarker("01_keep", true),
				{
					Name: "Concurrent group",
					Type: "http_sequence",
					Params: map[string]any{
						"requests": []dsl.Action{
							{Name: "01 - GET ok", Type: "http_request", Method: "GET", Target: "https://example.test/ok"},
							{
								Name:     "02 - POST unexported",
								Type:     "http_request",
								Method:   "POST",
								Target:   "https://example.test/beacon",
								Metadata: map[string]any{"import_action_uid": "uid-seq-blocked", "execution_blockers": []string{sample.HARRequestBodyNotExportedCode}},
							},
						},
					},
				},
				dsl.TransactionMarker("01_keep", false),
				dsl.TransactionMarker("02_empty_after_exclude", true),
				{
					Name:     "03 - POST unexported standalone",
					Type:     "http_request",
					Method:   "POST",
					Target:   "https://example.test/unload",
					Metadata: map[string]any{"import_action_uid": "uid-top-blocked", "execution_blockers": []string{sample.HARRequestBodyNotExportedCode}},
				},
				dsl.TransactionMarker("02_empty_after_exclude", false),
			},
		}

		resp := postExclude(map[string]any{
			"script":                    script,
			"exclude_unexported_bodies": true,
		})
		if resp.Code != http.StatusOK {
			t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
		}
		var out struct {
			Script       dsl.Script       `json:"script"`
			RemovedCount int              `json:"removed_count"`
			Preflight    *ReplayPreflight `json:"preflight"`
		}
		if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		if out.RemovedCount != 2 {
			t.Fatalf("removed_count = %d, want 2", out.RemovedCount)
		}
		if out.Preflight == nil || !out.Preflight.Valid {
			t.Fatalf("preflight = %+v, want Valid=true", out.Preflight)
		}
		if len(out.Script.Actions) != 3 {
			t.Fatalf("actions len = %d, want 3 (01_keep start, http_sequence, 01_keep end): %#v", len(out.Script.Actions), out.Script.Actions)
		}
		for _, act := range out.Script.Actions {
			if strings.Contains(act.Name, "02_empty_after_exclude") {
				t.Fatalf("expected emptied transaction markers to be pruned, got %#v", out.Script.Actions)
			}
		}
	})

	t.Run("rejects exclusion that leaves only empty transaction markers", func(t *testing.T) {
		script := &dsl.Script{
			Name:     "Only_Blocked",
			Version:  "1.0.0",
			Protocol: "http",
			Config:   dsl.ScriptConfig{BaseURL: "https://example.test"},
			Actions: []dsl.Action{
				dsl.TransactionMarker("01_only", true),
				{
					Name:     "01 - POST blocked",
					Type:     "http_request",
					Method:   "POST",
					Target:   "https://example.test/blocked",
					Metadata: map[string]any{"execution_blockers": []string{sample.HARRequestBodyNotExportedCode}},
				},
				dsl.TransactionMarker("01_only", false),
			},
		}
		resp := postExclude(map[string]any{
			"script":                    script,
			"exclude_unexported_bodies": true,
		})
		if resp.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422: %s", resp.Code, resp.Body.String())
		}
	})
}

func stripBlockedJSONActions(raw any) []any {
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]any, 0, len(items))
	for _, elem := range items {
		act, _ := elem.(map[string]any)
		metaRaw, _ := json.Marshal(act["metadata"])
		if strings.Contains(string(metaRaw), sample.HARRequestBodyNotExportedCode) {
			continue
		}
		if params, ok := act["params"].(map[string]any); ok {
			if reqs, hasReqs := params["requests"]; hasReqs {
				filtered := stripBlockedJSONActions(reqs)
				if len(filtered) == 0 {
					continue
				}
				params["requests"] = filtered
			}
		}
		if ctrl, ok := act["control"].(map[string]any); ok {
			if steps, hasSteps := ctrl["steps"]; hasSteps {
				ctrl["steps"] = stripBlockedJSONActions(steps)
			}
			if elseSteps, hasElse := ctrl["else"]; hasElse {
				ctrl["else"] = stripBlockedJSONActions(elseSteps)
			}
			if defSteps, hasDef := ctrl["default"]; hasDef {
				ctrl["default"] = stripBlockedJSONActions(defSteps)
			}
		}
		out = append(out, act)
	}
	return out
}

func TestHARConverterFixesTelemetryDefaultAuraNamesAndCountRecount(t *testing.T) {
	// 1. Default telemetry filtering + TotalRequests / FilteredTelemetry stats + numbered Aura step naming.
	harJSON := `{
		"log": {
			"entries": [
				{
					"startedDateTime": "2026-03-01T10:00:00.000Z",
					"time": 100,
					"request": {
						"method": "POST",
						"url": "https://acme.my.salesforce.com/aura?r=1",
						"headers": [{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}],
						"postData": {
							"mimeType": "application/x-www-form-urlencoded",
							"text": "message=%7B%22actions%22%3A%5B%7B%22id%22%3A%221%3Ba%22%2C%22descriptor%22%3A%22aura%3A%2F%2FInstrumentationBeacon.sendData%22%7D%5D%7D"
						}
					},
					"response": {
						"status": 200,
						"headers": [{"name": "Content-Type", "value": "application/json"}],
						"content": {"mimeType": "application/json", "text": "{\"actions\":[]}"}
					}
				},
				{
					"startedDateTime": "2026-03-01T10:00:00.010Z",
					"time": 200,
					"request": {
						"method": "POST",
						"url": "https://acme.my.salesforce.com/aura?r=2",
						"headers": [{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}],
						"postData": {
							"mimeType": "application/x-www-form-urlencoded",
							"text": "message=%7B%22actions%22%3A%5B%7B%22id%22%3A%222%3Ba%22%2C%22descriptor%22%3A%22aura%3A%2F%2FApexActionController%2FACTION%24execute%22%2C%22params%22%3A%7B%22classname%22%3A%22OrderController%22%2C%22method%22%3A%22submit%22%7D%7D%5D%7D"
						}
					},
					"response": {
						"status": 200,
						"headers": [{"name": "Content-Type", "value": "application/json"}],
						"content": {"mimeType": "application/json", "text": "{\"actions\":[{\"id\":\"2;a\",\"state\":\"SUCCESS\"}]}"}
					}
				},
				{
					"startedDateTime": "2026-03-01T10:00:00.020Z",
					"time": 200,
					"request": {
						"method": "POST",
						"url": "https://acme.my.salesforce.com/s/sfsites/aura?r=3",
						"headers": [{"name": "Content-Type", "value": "application/x-www-form-urlencoded"}],
						"postData": {
							"mimeType": "application/x-www-form-urlencoded",
							"text": "message=%7B%22actions%22%3A%5B%7B%22id%22%3A%223%3Ba%22%2C%22descriptor%22%3A%22serviceComponent%3A%2F%2Fui.force.components.controllers.lists.selectableListDataProvider.SelectableListDataProviderController%2FACTION%24getItems%22%7D%5D%7D"
						}
					},
					"response": {
						"status": 200,
						"headers": [{"name": "Content-Type", "value": "application/json"}],
						"content": {"mimeType": "application/json", "text": "{\"actions\":[{\"id\":\"3;a\",\"state\":\"SUCCESS\"}]}"}
					}
				}
			]
		}
	}`

	resp, _ := postHARPairJSON(t, "/api/v1/scripts/import/har", map[string]any{
		"name":    "Salesforce Aura Naming & Counts",
		"content": harJSON,
	})
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", resp.Code, resp.Body.String())
	}
	var body struct {
		Script dsl.Script `json:"script"`
		Stats  struct {
			TotalRequests     int `json:"total_requests"`
			FilteredAssets    int `json:"filtered_assets"`
			FilteredTelemetry int `json:"filtered_telemetry"`
			ConvertedActions  int `json:"converted_actions"`
			Assertions        int `json:"assertions"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Stats.TotalRequests != 3 {
		t.Fatalf("stats.total_requests = %d, want 3", body.Stats.TotalRequests)
	}
	if body.Stats.FilteredTelemetry != 1 {
		t.Fatalf("stats.filtered_telemetry = %d, want 1 (telemetry filtering defaults on)", body.Stats.FilteredTelemetry)
	}
	if body.Stats.ConvertedActions != 2 {
		t.Fatalf("stats.converted_actions = %d, want 2", body.Stats.ConvertedActions)
	}
	if body.Stats.Assertions < 2 {
		t.Fatalf("stats.assertions = %d, want >= 2 (including requests grouped in http_sequence)", body.Stats.Assertions)
	}
	rawScript, _ := json.Marshal(body.Script)
	if !strings.Contains(string(rawScript), "01 - POST aura — OrderController.submit") {
		t.Fatalf("expected numbered Aura action name to be enriched with OrderController.submit, got: %s", string(rawScript))
	}
	if !strings.Contains(string(rawScript), "02 - POST s sfsites aura — SelectableListDataProviderController.getItems") {
		t.Fatalf("expected numbered sfsites Aura action name to be enriched with SelectableListDataProviderController.getItems, got: %s", string(rawScript))
	}
}
