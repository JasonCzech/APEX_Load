package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recorder"
	recbrowser "github.com/jasonczech/apexload/pkg/recorder/browser"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
	"github.com/jasonczech/apexload/pkg/storage"
)

const testSalesforceHAR = `{
  "log": {
    "entries": [
      {
        "request": {
          "method": "GET",
          "url": "https://attone--perfhyper2.sandbox.my.salesforce.com/login",
          "headers": [],
          "httpVersion": "HTTP/1.1"
        },
        "response": {
          "status": 200,
          "headers": [],
          "httpVersion": "HTTP/1.1",
          "content": {"text": "{\"access_token\":\"tok-abcdef123456\"}"}
        }
      },
      {
        "request": {
          "method": "POST",
          "url": "https://attone--perfhyper2.sandbox.lightning.force.com/aura",
          "headers": [{"name": "Content-Type", "value": "application/json"}],
          "httpVersion": "HTTP/1.1",
          "postData": {"text": "{\"actions\":[{\"id\":\"1;a\",\"descriptor\":\"apex://AccountController/ACTION$get\"}],\"token\":\"tok-abcdef123456\"}"}
        },
        "response": {
          "status": 200,
          "headers": [],
          "httpVersion": "HTTP/1.1",
          "content": {"text": "{\"actions\":[{\"state\":\"SUCCESS\"}]}"}
        }
      }
    ]
  }
}`

const testBrowserDualScriptHAR = `{
  "log": {
    "entries": [
      {
        "request": {
          "method": "POST",
          "url": "https://shop.example.test/api/orders",
          "headers": [
            {"name": "Authorization", "value": "Bearer secret-browser-token"},
            {"name": "Content-Type", "value": "application/json"}
          ],
          "httpVersion": "HTTP/1.1",
          "postData": {"text": "{\"customer_email\":\"buyer@example.com\",\"sku\":\"SKU-998877\"}"}
        },
        "response": {
          "status": 200,
          "headers": [{"name": "Content-Type", "value": "application/json"}],
          "httpVersion": "HTTP/1.1",
          "content": {"text": "{\"order_id\":\"ord-1001\"}"}
        }
      },
      {
        "request": {
          "method": "POST",
          "url": "https://shop.example.test/api/orders/confirm",
          "headers": [{"name": "Content-Type", "value": "application/json"}],
          "httpVersion": "HTTP/1.1",
          "postData": {"text": "{\"customer_email\":\"buyer@example.com\",\"sku\":\"SKU-998877\"}"}
        },
        "response": {
          "status": 200,
          "headers": [{"name": "Content-Type", "value": "application/json"}],
          "httpVersion": "HTTP/1.1",
          "content": {"text": "{\"confirmed\":true}"}
        }
      }
    ]
  }
}`

func TestHTTPAuthoringRejectsInvalidPolicyAndModeEnums(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	stubBrowserRecordingDependencyProbe(t, nil)
	server := NewServer(ServerConfig{Store: store})
	proxySession, err := server.recorder.StartSessionForProject("proxy-invalid-policy", "Checkout", storage.DefaultProjectID, recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	proxySession.Requests = append(proxySession.Requests, recorder.RecordedRequest{
		Seq: 1, Method: http.MethodGet, URL: "https://shop.example.test/cart", Path: "/cart", StatusCode: http.StatusOK,
	})
	browserSession := stopBrowserRecordingFixture(t, server, &recbrowser.Session{
		StartURL: "https://shop.example.test",
		HAR:      []byte(testBrowserDualScriptHAR),
	}, storage.DefaultProjectID, "BrowserInvalidPolicy")

	cases := []struct {
		name      string
		path      string
		body      map[string]any
		wantField string
	}{
		{
			name:      "import/har invalid conversion_policy",
			path:      "/api/v1/scripts/import/har",
			body:      map[string]any{"project_id": storage.DefaultProjectID, "content": testSalesforceHAR, "conversion_policy": "invalid_policy"},
			wantField: "conversion_policy",
		},
		{
			name:      "import/har invalid correlation_mode",
			path:      "/api/v1/scripts/import/har",
			body:      map[string]any{"project_id": storage.DefaultProjectID, "content": testSalesforceHAR, "correlation_mode": "invalid_mode"},
			wantField: "correlation_mode",
		},
		{
			name:      "import/har invalid resource_mode",
			path:      "/api/v1/scripts/import/har",
			body:      map[string]any{"project_id": storage.DefaultProjectID, "content": testSalesforceHAR, "resource_mode": "invalid_resource_mode"},
			wantField: "resource_mode",
		},
		{
			name:      "import/salesforce invalid conversion_policy",
			path:      "/api/v1/scripts/import/salesforce",
			body:      map[string]any{"project_id": storage.DefaultProjectID, "content": testSalesforceHAR, "conversion_policy": "invalid_policy"},
			wantField: "conversion_policy",
		},
		{
			name:      "import/sample invalid correlation_mode",
			path:      "/api/v1/scripts/import/sample",
			body:      map[string]any{"project_id": storage.DefaultProjectID, "format": "har", "content": testSalesforceHAR, "correlation_mode": "invalid_mode"},
			wantField: "correlation_mode",
		},
		{
			name:      "recorder/preview invalid conversion_policy",
			path:      "/api/v1/recorder/" + proxySession.ID + "/convert/preview",
			body:      map[string]any{"conversion_policy": "invalid_policy"},
			wantField: "conversion_policy",
		},
		{
			name:      "recorder/convert invalid resource_mode",
			path:      "/api/v1/recorder/" + proxySession.ID + "/convert",
			body:      map[string]any{"resource_mode": "invalid_resource_mode"},
			wantField: "resource_mode",
		},
		{
			name:      "browser-recorder/preview invalid correlation_mode",
			path:      "/api/v1/browser-recorder/sessions/" + browserSession.ID + "/convert/preview",
			body:      map[string]any{"also_create_http_script": true, "correlation_mode": "invalid_mode"},
			wantField: "correlation_mode",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := mustMarshalJSONForTest(t, tc.body)
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Fatalf("%s status = %d, want 400 body=%s", tc.name, resp.Code, resp.Body.String())
			}
			if !strings.Contains(resp.Body.String(), tc.wantField) {
				t.Fatalf("%s 400 body = %q, want offending field %q named", tc.name, resp.Body.String(), tc.wantField)
			}
		})
	}
}

func TestHTTPAuthoringUnifiedPolicyAndSalesforceProfileEnforcement(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	profile := recordingprofile.Profile{
		ID:   "sf-disabled-corr",
		Name: "SF Disabled Correlations",
		Correlation: recordingprofile.CorrelationSettings{
			Disabled: true,
		},
	}
	if err := store.SaveProject(&storage.Project{
		ID:        storage.DefaultProjectID,
		Name:      "Default",
		CreatedAt: now,
		UpdatedAt: now,
		Settings: storage.ProjectSettings{
			RecordingProfiles: []recordingprofile.Profile{profile},
		},
	}); err != nil {
		t.Fatalf("SaveProject with recording profile: %v", err)
	}

	server := NewServer(ServerConfig{Store: store})

	t.Run("import/sample legacy vs unified_v1 salesforce native and effective_settings", func(t *testing.T) {
		legacyBody := mustMarshalJSONForTest(t, map[string]any{
			"project_id": storage.DefaultProjectID,
			"format":     "har",
			"content":    testSalesforceHAR,
		})
		legacyReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", bytes.NewReader(legacyBody))
		legacyReq.Header.Set("Content-Type", "application/json")
		legacyResp := httptest.NewRecorder()
		server.mux.ServeHTTP(legacyResp, legacyReq)
		if legacyResp.Code != http.StatusOK {
			t.Fatalf("legacy sample status = %d body=%s", legacyResp.Code, legacyResp.Body.String())
		}
		var legacyOut struct {
			Script            dsl.Script                `json:"script"`
			EffectiveSettings *sample.EffectiveSettings `json:"effective_settings"`
		}
		if err := json.Unmarshal(legacyResp.Body.Bytes(), &legacyOut); err != nil {
			t.Fatal(err)
		}
		if legacyOut.EffectiveSettings == nil || legacyOut.EffectiveSettings.ConversionPolicy != sample.ConversionPolicyLegacy {
			t.Fatalf("legacy effective_settings = %#v, want legacy", legacyOut.EffectiveSettings)
		}
		if legacyOut.EffectiveSettings.SalesforceNative {
			t.Fatalf("legacy sample SalesforceNative = true, want false")
		}

		unifiedBody := mustMarshalJSONForTest(t, map[string]any{
			"project_id":        storage.DefaultProjectID,
			"format":            "har",
			"content":           testSalesforceHAR,
			"conversion_policy": "unified_v1",
		})
		unifiedReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", bytes.NewReader(unifiedBody))
		unifiedReq.Header.Set("Content-Type", "application/json")
		unifiedResp := httptest.NewRecorder()
		server.mux.ServeHTTP(unifiedResp, unifiedReq)
		if unifiedResp.Code != http.StatusOK {
			t.Fatalf("unified sample status = %d body=%s", unifiedResp.Code, unifiedResp.Body.String())
		}
		var unifiedOut struct {
			Script            dsl.Script                `json:"script"`
			EffectiveSettings *sample.EffectiveSettings `json:"effective_settings"`
		}
		if err := json.Unmarshal(unifiedResp.Body.Bytes(), &unifiedOut); err != nil {
			t.Fatal(err)
		}
		if unifiedOut.EffectiveSettings == nil || unifiedOut.EffectiveSettings.ConversionPolicy != sample.ConversionPolicyUnifiedV1 {
			t.Fatalf("unified effective_settings = %#v, want unified_v1", unifiedOut.EffectiveSettings)
		}
		if !unifiedOut.EffectiveSettings.SalesforceNative {
			t.Fatalf("unified sample SalesforceNative = false, want true")
		}
		if unifiedOut.Script.GeneratedBy != "apexload-salesforce-builder" {
			t.Fatalf("unified sample GeneratedBy = %q, want apexload-salesforce-builder", unifiedOut.Script.GeneratedBy)
		}
		persisted, ok := sample.ReadEffectiveSettings(&unifiedOut.Script)
		if !ok || persisted.ConversionPolicy != sample.ConversionPolicyUnifiedV1 {
			t.Fatalf("persisted effective_settings = %#v (ok=%v), want unified_v1", persisted, ok)
		}
	})

	t.Run("import/salesforce respects recording_profile_id correlation rules", func(t *testing.T) {
		sfBody := mustMarshalJSONForTest(t, map[string]any{
			"project_id":           storage.DefaultProjectID,
			"content":              testSalesforceHAR,
			"recording_profile_id": profile.ID,
		})
		sfReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/salesforce", bytes.NewReader(sfBody))
		sfReq.Header.Set("Content-Type", "application/json")
		sfResp := httptest.NewRecorder()
		server.mux.ServeHTTP(sfResp, sfReq)
		if sfResp.Code != http.StatusOK {
			t.Fatalf("salesforce import status = %d body=%s", sfResp.Code, sfResp.Body.String())
		}
		var sfOut struct {
			Script            dsl.Script                `json:"script"`
			Correlations      []any                     `json:"correlations"`
			EffectiveSettings *sample.EffectiveSettings `json:"effective_settings"`
		}
		if err := json.Unmarshal(sfResp.Body.Bytes(), &sfOut); err != nil {
			t.Fatal(err)
		}
		if sfOut.EffectiveSettings == nil || sfOut.EffectiveSettings.RecordingProfileID != profile.ID || sfOut.EffectiveSettings.CorrelationMode != sample.CorrelationModeDisabled {
			t.Fatalf("salesforce effective_settings = %#v, want profile %q and disabled correlations", sfOut.EffectiveSettings, profile.ID)
		}
		if len(sfOut.Correlations) != 0 {
			t.Fatalf("salesforce correlations = %#v, want none when profile disables correlation", sfOut.Correlations)
		}
		if len(sfOut.Script.Actions) == 0 {
			t.Fatal("expected at least 1 action in salesforce import script")
		}
		if len(sfOut.Script.Actions[0].Extractors) != 0 {
			t.Fatalf("salesforce login extractors = %#v, want none when profile disables correlation", sfOut.Script.Actions[0].Extractors)
		}
	})
}

func TestRecorderPreviewAndSavePreservesOptionResolvedBaselineAndEffectiveSettings(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	server := NewServer(ServerConfig{Store: store})
	session, err := server.recorder.StartSessionForProject(
		"recording-options-baseline",
		"CheckoutWithAssets",
		storage.DefaultProjectID,
		recorder.SessionFilters{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	session.BaseURL = "https://shop.example.test"
	session.Requests = append(session.Requests,
		recorder.RecordedRequest{
			Seq:                 1,
			Method:              http.MethodGet,
			URL:                 "https://shop.example.test/index.html",
			Path:                "/index.html",
			StatusCode:          http.StatusOK,
			ResponseBody:        "<html><head><title>Shop</title></head></html>",
			RequestHTTPVersion:  "HTTP/1.1",
			ResponseHTTPVersion: "HTTP/1.1",
		},
		recorder.RecordedRequest{
			Seq:                 2,
			Method:              http.MethodGet,
			URL:                 "https://shop.example.test/static/app.js",
			Path:                "/static/app.js",
			StatusCode:          http.StatusOK,
			ResponseBody:        "console.log('app');",
			RequestHTTPVersion:  "HTTP/1.1",
			ResponseHTTPVersion: "HTTP/1.1",
		},
	)

	// Preview with non-default options: resource_mode="separate" keeps the static JS request as a separate action (2 actions total, instead of 1).
	previewPayload := mustMarshalJSONForTest(t, map[string]any{
		"name":              "Checkout separate assets",
		"conversion_policy": "unified_v1",
		"resource_mode":     "separate",
		"async_scan":        false,
	})
	previewReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/recorder/"+session.ID+"/convert/preview",
		bytes.NewReader(previewPayload),
	)
	previewReq.Header.Set("Content-Type", "application/json")
	previewResp := httptest.NewRecorder()
	server.mux.ServeHTTP(previewResp, previewReq)
	if previewResp.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", previewResp.Code, previewResp.Body.String())
	}
	var preview struct {
		Script            dsl.Script                `json:"script"`
		EffectiveSettings *sample.EffectiveSettings `json:"effective_settings"`
	}
	if err := json.Unmarshal(previewResp.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Script.Actions) != 2 {
		t.Fatalf("preview actions = %d, want 2 with resource_mode=separate", len(preview.Script.Actions))
	}
	if preview.EffectiveSettings == nil || preview.EffectiveSettings.ResourceMode != "separate" || preview.EffectiveSettings.ConversionPolicy != "unified_v1" {
		t.Fatalf("preview effective_settings = %#v", preview.EffectiveSettings)
	}

	// Verify un-attested fallback rejects non-default conversion settings instead of mismatched default reconversion.
	unattested := cloneScriptDefinition(&preview.Script)
	if unattested.Metadata != nil && unattested.Metadata.Import != nil && unattested.Metadata.Import.Preserved != nil {
		delete(unattested.Metadata.Import.Preserved, importDraftAttestationMetadataKey)
	}
	unattestedCreatePayload := mustMarshalJSONForTest(t, map[string]any{
		"project_id":                  storage.DefaultProjectID,
		"source_recording_session_id": session.ID,
		"script":                      unattested,
	})
	unattestedReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(unattestedCreatePayload))
	unattestedReq.Header.Set("Content-Type", "application/json")
	unattestedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unattestedResp, unattestedReq)
	if unattestedResp.Code != http.StatusConflict {
		t.Fatalf("unattested non-default save status = %d, want 409 body=%s", unattestedResp.Code, unattestedResp.Body.String())
	}

	// Save the attested preview draft via POST /api/v1/scripts.
	createPayload := mustMarshalJSONForTest(t, map[string]any{
		"project_id":                  storage.DefaultProjectID,
		"source_recording_session_id": session.ID,
		"script":                      preview.Script,
	})
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(createPayload))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("attested save status = %d body=%s", createResp.Code, createResp.Body.String())
	}
	var saved StoredScript
	if err := json.Unmarshal(createResp.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	baseline, err := store.GetScriptRecording(saved.ID)
	if err != nil {
		t.Fatalf("GetScriptRecording: %v", err)
	}
	if baseline == nil || baseline.RequestCount != 2 || len(baseline.Observations) != 2 {
		t.Fatalf("persisted baseline = %#v, want 2 observations aligned with resource_mode=separate", baseline)
	}
	if baseline.Observations[0].ID != 0 || baseline.Observations[1].ID != 1 {
		t.Fatalf("persisted observation IDs = [%d, %d], want [0, 1]", baseline.Observations[0].ID, baseline.Observations[1].ID)
	}

	// Verify PUT /api/v1/scripts/{id} preserves server-owned effective_settings.
	if saved.Script == nil || saved.Script.Metadata == nil || saved.Script.Metadata.Import == nil || saved.Script.Metadata.Import.Preserved == nil || saved.Script.Metadata.Import.Preserved[sample.PreservedEffectiveSettingsKey] == nil {
		t.Fatalf("expected saved script to carry Preserved[%q] before PUT, got %#v", sample.PreservedEffectiveSettingsKey, saved.Script)
	}
	updatedScript := cloneScriptDefinition(saved.Script)
	updatedScript.Name = "Checkout separate assets updated"
	delete(updatedScript.Metadata.Import.Preserved, sample.PreservedEffectiveSettingsKey)
	if _, stillPresent := updatedScript.Metadata.Import.Preserved[sample.PreservedEffectiveSettingsKey]; stillPresent {
		t.Fatalf("expected client PUT payload to omit %q", sample.PreservedEffectiveSettingsKey)
	}
	putPayload := mustMarshalJSONForTest(t, map[string]any{"script": updatedScript})
	putReq := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+saved.ID, bytes.NewReader(putPayload))
	putReq.Header.Set("Content-Type", "application/json")
	putResp := httptest.NewRecorder()
	server.mux.ServeHTTP(putResp, putReq)
	if putResp.Code != http.StatusOK {
		t.Fatalf("PUT status = %d body=%s", putResp.Code, putResp.Body.String())
	}
	reloaded, err := store.GetScript(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	restoredSettings, ok := sample.ReadEffectiveSettings(reloaded)
	if !ok || restoredSettings.ResourceMode != "separate" || restoredSettings.ConversionPolicy != "unified_v1" {
		t.Fatalf("restored effective_settings after PUT = %#v (ok=%v)", restoredSettings, ok)
	}
}

func TestBrowserDualScriptHTTPCompanionPreviewAndTargetedVerify(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	stubBrowserRecordingDependencyProbe(t, nil)
	server := NewServer(ServerConfig{Store: store})
	browserSession := stopBrowserRecordingFixture(t, server, &recbrowser.Session{
		StartURL: "https://shop.example.test",
		HAR:      []byte(testBrowserDualScriptHAR),
	}, storage.DefaultProjectID, "BrowserDual")

	previewBody := mustMarshalJSONForTest(t, map[string]any{
		"name":                    "Browser Checkout",
		"also_create_http_script": true,
		"conversion_policy":       "unified_v1",
	})
	previewReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+browserSession.ID+"/convert/preview",
		bytes.NewReader(previewBody),
	)
	previewReq.Header.Set("Content-Type", "application/json")
	previewResp := httptest.NewRecorder()
	server.mux.ServeHTTP(previewResp, previewReq)
	if previewResp.Code != http.StatusOK {
		t.Fatalf("browser preview status = %d body=%s", previewResp.Code, previewResp.Body.String())
	}
	var previewOut struct {
		HTTPParameterization          map[string]any             `json:"http_parameterization"`
		HTTPPreflight                 map[string]any             `json:"http_preflight"`
		HTTPSecretBindingRequirements []SecretBindingRequirement `json:"http_secret_binding_requirements"`
		EffectiveSettings             *sample.EffectiveSettings  `json:"effective_settings"`
	}
	if err := json.Unmarshal(previewResp.Body.Bytes(), &previewOut); err != nil {
		t.Fatal(err)
	}
	if previewOut.HTTPParameterization == nil {
		t.Fatal("expected http_parameterization in dual-script browser preview")
	}
	if previewOut.HTTPPreflight == nil {
		t.Fatal("expected http_preflight in dual-script browser preview")
	}
	if len(previewOut.HTTPSecretBindingRequirements) == 0 {
		t.Fatal("expected http_secret_binding_requirements in dual-script browser preview")
	}
	if previewOut.EffectiveSettings == nil || previewOut.EffectiveSettings.ConversionPolicy != "unified_v1" || previewOut.EffectiveSettings.SourceEntryPoint != sample.EntryPointBrowserHTTP {
		t.Fatalf("browser preview effective_settings = %#v", previewOut.EffectiveSettings)
	}

	reqID := previewOut.HTTPSecretBindingRequirements[0].ID
	convertBody := mustMarshalJSONForTest(t, map[string]any{
		"name":                    "Browser Checkout",
		"also_create_http_script": true,
		"conversion_policy":       "unified_v1",
		"parameterize":            true,
		"secret_bindings": []map[string]any{{
			"requirement_id": reqID,
			"secret_name":    "BROWSER_API_TOKEN",
			"script_index":   1,
		}},
	})
	convertReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+browserSession.ID+"/convert",
		bytes.NewReader(convertBody),
	)
	convertReq.Header.Set("Content-Type", "application/json")
	convertResp := httptest.NewRecorder()
	server.mux.ServeHTTP(convertResp, convertReq)
	if convertResp.Code != http.StatusCreated {
		t.Fatalf("browser convert status = %d body=%s", convertResp.Code, convertResp.Body.String())
	}
	if !strings.Contains(convertResp.Body.String(), "{{secret.BROWSER_API_TOKEN}}") {
		t.Fatalf("converted http_script did not receive targeted secret binding: %s", convertResp.Body.String())
	}
}

func mustMarshalJSONForTest(t *testing.T, v any) []byte {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return encoded
}

func TestHARMultipartUploadSelectionsAndSampleQueryFallbacks(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})

	// 1. Multipart HAR upload rejects malformed secret_bindings JSON with 400
	var badBuf bytes.Buffer
	badWriter := multipart.NewWriter(&badBuf)
	part, err := badWriter.CreateFormFile("file", "capture.har")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(testSalesforceHAR)); err != nil {
		t.Fatal(err)
	}
	if err := badWriter.WriteField("secret_bindings", "{not-json"); err != nil {
		t.Fatal(err)
	}
	if err := badWriter.Close(); err != nil {
		t.Fatal(err)
	}
	badReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id="+storage.DefaultProjectID, &badBuf)
	badReq.Header.Set("Content-Type", badWriter.FormDataContentType())
	badResp := httptest.NewRecorder()
	server.mux.ServeHTTP(badResp, badReq)
	if badResp.Code != http.StatusBadRequest || !strings.Contains(badResp.Body.String(), "invalid secret_bindings") {
		t.Fatalf("expected 400 invalid secret_bindings, got %d: %s", badResp.Code, badResp.Body.String())
	}

	// 2. Multipart HAR upload accepts valid parameterization_selections and secret_bindings JSON
	var okBuf bytes.Buffer
	okWriter := multipart.NewWriter(&okBuf)
	okPart, err := okWriter.CreateFormFile("file", "capture.har")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := okPart.Write([]byte(testBrowserDualScriptHAR)); err != nil {
		t.Fatal(err)
	}
	if err := okWriter.WriteField("conversion_policy", "unified_v1"); err != nil {
		t.Fatal(err)
	}
	if err := okWriter.WriteField("parameterization_selections", `[]`); err != nil {
		t.Fatal(err)
	}
	if err := okWriter.WriteField("secret_bindings", `[]`); err != nil {
		t.Fatal(err)
	}
	if err := okWriter.Close(); err != nil {
		t.Fatal(err)
	}
	okReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id="+storage.DefaultProjectID, &okBuf)
	okReq.Header.Set("Content-Type", okWriter.FormDataContentType())
	okResp := httptest.NewRecorder()
	server.mux.ServeHTTP(okResp, okReq)
	if okResp.Code != http.StatusOK {
		t.Fatalf("valid multipart HAR status = %d body=%s", okResp.Code, okResp.Body.String())
	}

	// 3. Sample import honors top-level and query-string boolean/mode fallbacks
	sampleBody := mustMarshalJSONForTest(t, map[string]any{
		"name":               "Sample Query Fallbacks",
		"project_id":         storage.DefaultProjectID,
		"format":             "har",
		"content":            testSalesforceHAR,
		"include_assertions": false,
		"page_title_checks":  false,
	})
	sampleReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/sample?conversion_policy=unified_v1&infer_extractors=false&async_scan=false&parameterize=true",
		bytes.NewReader(sampleBody),
	)
	sampleReq.Header.Set("Content-Type", "application/json")
	sampleResp := httptest.NewRecorder()
	server.mux.ServeHTTP(sampleResp, sampleReq)
	if sampleResp.Code != http.StatusOK {
		t.Fatalf("sample import status = %d body=%s", sampleResp.Code, sampleResp.Body.String())
	}
	var sampleOut struct {
		EffectiveSettings *sample.EffectiveSettings `json:"effective_settings"`
	}
	if err := json.Unmarshal(sampleResp.Body.Bytes(), &sampleOut); err != nil {
		t.Fatal(err)
	}
	if sampleOut.EffectiveSettings == nil {
		t.Fatal("expected effective_settings on sample import response")
	}
	if sampleOut.EffectiveSettings.IncludeAssertions || sampleOut.EffectiveSettings.InferExtractors ||
		sampleOut.EffectiveSettings.PageTitleChecks || sampleOut.EffectiveSettings.AsyncScan ||
		!sampleOut.EffectiveSettings.Parameterize {
		t.Fatalf("unexpected effective_settings from top-level/query fallbacks: %#v", sampleOut.EffectiveSettings)
	}
}

func TestExplicitIncludeStaticAssetsFalseOverridesProfileAndUnifiedParameterizeParity(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	stubBrowserRecordingDependencyProbe(t, nil)
	server := NewServer(ServerConfig{Store: store})

	createdProfile := &recordingprofile.Profile{
		ID:           "sep-profile",
		Name:         "Separate Assets Profile",
		ResourceMode: "separate",
	}

	// 1. Explicit include_static_assets:false overrides profile ResourceMode:"separate" on both HAR and proxy conversion.
	f := false
	proxySession, err := server.recorder.StartSessionForProject(
		"rec-static-override",
		"StaticOverride",
		storage.DefaultProjectID,
		recorder.SessionFilters{},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	proxySession.BaseURL = "https://shop.example.test"
	proxySession.Requests = append(proxySession.Requests,
		recorder.RecordedRequest{
			Seq:                 1,
			Method:              "GET",
			URL:                 "https://shop.example.test/search?email=alice%40example.test",
			Path:                "/search?email=alice%40example.test",
			StatusCode:          200,
			ResponseBody:        "<html><title>Search</title></html>",
			RawResponseHeaders:  map[string]string{"Content-Type": "text/html"},
			RequestHTTPVersion:  "HTTP/1.1",
			ResponseHTTPVersion: "HTTP/1.1",
		},
		recorder.RecordedRequest{
			Seq:                 2,
			Method:              "GET",
			URL:                 "https://shop.example.test/static/app.js",
			Path:                "/static/app.js",
			StatusCode:          200,
			IsStatic:            true,
			ResponseBody:        "console.log(1);",
			RawResponseHeaders:  map[string]string{"Content-Type": "application/javascript"},
			RequestHTTPVersion:  "HTTP/1.1",
			ResponseHTTPVersion: "HTTP/1.1",
		},
	)
	proxyRes := recorder.ConvertSessionWithOptions(proxySession, "StaticOverride", recorder.ConvertOptions{
		ConversionPolicy:    "unified_v1",
		IncludeStaticAssets: &f,
		Profile:             createdProfile,
	})
	if proxyRes.EffectiveSettings == nil || proxyRes.EffectiveSettings.ResourceMode != "runtime" {
		t.Fatalf("proxy EffectiveSettings.ResourceMode = %#v, want runtime when include_static_assets=false", proxyRes.EffectiveSettings)
	}
	if len(proxyRes.Script.Actions) != 1 {
		t.Fatalf("proxy Actions len = %d, want 1 (static asset omitted)", len(proxyRes.Script.Actions))
	}

	// 2. Unified parameterization parity across proxy conversion and browser HTTP conversion.
	const emailHAR = `{
  "log": {
    "version": "1.2",
    "creator": {"name": "test", "version": "1.0"},
    "entries": [
      {
        "startedDateTime": "2026-04-20T12:00:00.000Z",
        "time": 10,
        "request": {
          "method": "GET",
          "url": "https://shop.example.test/search?email=alice%40example.test",
          "httpVersion": "HTTP/1.1",
          "headers": [{"name": "Accept", "value": "application/json"}],
          "queryString": [],
          "headersSize": -1,
          "bodySize": 0
        },
        "response": {
          "status": 200,
          "statusText": "OK",
          "httpVersion": "HTTP/1.1",
          "headers": [{"name": "Content-Type", "value": "application/json"}],
          "content": {"size": 11, "mimeType": "application/json", "text": "{\"ok\":true}"},
          "redirectURL": "",
          "headersSize": -1,
          "bodySize": 11
        },
        "cache": {},
        "timings": {"send": 1, "wait": 8, "receive": 1}
      }
    ]
  }
}`
	proxyPreviewReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/recorder/"+proxySession.ID+"/convert/preview",
		bytes.NewReader(mustMarshalJSONForTest(t, map[string]any{
			"name":                  "Proxy Email",
			"conversion_policy":     "unified_v1",
			"include_static_assets": false,
			"parameterize":          true,
		})),
	)
	proxyPreviewReq.Header.Set("Content-Type", "application/json")
	proxyPreviewResp := httptest.NewRecorder()
	server.mux.ServeHTTP(proxyPreviewResp, proxyPreviewReq)
	if proxyPreviewResp.Code != http.StatusOK {
		t.Fatalf("proxy preview status = %d body=%s", proxyPreviewResp.Code, proxyPreviewResp.Body.String())
	}

	browserSession := stopBrowserRecordingFixture(t, server, &recbrowser.Session{
		StartURL: "https://shop.example.test",
		HAR:      []byte(emailHAR),
	}, storage.DefaultProjectID, "BrowserEmail")
	browserPreviewReq := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/browser-recorder/sessions/"+browserSession.ID+"/convert/preview",
		bytes.NewReader(mustMarshalJSONForTest(t, map[string]any{
			"name":                    "Browser Email",
			"also_create_http_script": true,
			"conversion_policy":       "unified_v1",
			"parameterize":            true,
		})),
	)
	browserPreviewReq.Header.Set("Content-Type", "application/json")
	browserPreviewResp := httptest.NewRecorder()
	server.mux.ServeHTTP(browserPreviewResp, browserPreviewReq)
	if browserPreviewResp.Code != http.StatusOK {
		t.Fatalf("browser preview status = %d body=%s", browserPreviewResp.Code, browserPreviewResp.Body.String())
	}
	if !strings.Contains(proxyPreviewResp.Body.String(), "{{data.recorded_inputs.email}}") {
		t.Fatalf("proxy preview did not parameterize email query: %s", proxyPreviewResp.Body.String())
	}
	if !strings.Contains(browserPreviewResp.Body.String(), "{{data.recorded_inputs.email}}") {
		t.Fatalf("browser HTTP preview did not parameterize email query: %s", browserPreviewResp.Body.String())
	}
}
