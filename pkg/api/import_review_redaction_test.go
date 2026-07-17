package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/enterprisearchive"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestImportResponseRecursivelyRedactsReviewEvidenceCopies(t *testing.T) {
	const secret = "m9Q!"
	inspection := map[string]any{
		"steps": []any{map[string]any{
			"source_snippet": `<Argument name="Password" value="` + secret + `" selector="[data-token='` + secret + `']"/>`,
			"object_descriptor": map[string]any{
				"name":       "Password",
				"text":       secret,
				"selector":   `[data-password="` + secret + `"]`,
				"attributes": map[string]any{"value": secret},
			},
		}},
	}
	review := map[string]any{
		"source_files": []any{map[string]any{
			"path":    "script.xml",
			"content": `<Argument name="Password" value="` + secret + `"/>`,
		}},
	}
	artifact := map[string]any{"trueclient_archive_inspection": inspection}
	script := &dsl.Script{
		Name: "review", Version: "1", Protocol: "browser", ProjectID: "project-a",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "trueclient", Preserved: map[string]any{
			"source_review":                 review,
			"review_artifact":               artifact,
			"trueclient_archive_inspection": inspection,
		}}},
	}

	response := newScriptImportResponse(scriptImportResponseOptions{
		Script:         script,
		Scripts:        []*dsl.Script{script},
		SourceReview:   review,
		ReviewArtifact: artifact,
		Result: map[string]any{
			"script":          script,
			"source_review":   review,
			"review_artifact": artifact,
			"target":          "https://example.test/login?token={{secret.API_TOKEN}}&mode=test",
		},
	})
	result, _ := response["result"].(map[string]any)
	if got := result["target"]; got != "https://example.test/login?token={{secret.API_TOKEN}}&mode=test" {
		t.Fatalf("ordinary executable result field changed by review sanitizer: %#v", got)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(secret)) {
		t.Fatalf("import response leaked recursively embedded TrueClient secret: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte(maskedScriptSecret)) {
		t.Fatalf("import response did not retain redaction sentinel: %s", encoded)
	}
}

func TestImportResponseRedactsCredentialBearingURLsOutsideReviewSubtrees(t *testing.T) {
	const (
		user     = "source-user"
		password = "source-password"
		token    = "source-query-token"
	)
	rawURL := "https://" + user + ":" + password + "@example.test/resource?access_token=" + token + "&mode=normal"
	response := newScriptImportResponse(scriptImportResponseOptions{
		Summary: map[string]any{
			"base_url": rawURL,
			"servers":  []string{rawURL},
		},
		Result: map[string]any{
			"recorded_resource_urls": []string{rawURL},
			"nested": map[string]any{
				"service_endpoint": rawURL,
			},
		},
	})
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{user, password, token} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("import response leaked URL credential %q: %s", secret, encoded)
		}
	}
	if !bytes.Contains(encoded, []byte(maskedScriptSecret)) {
		t.Fatalf("import response omitted URL redaction sentinel: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte("mode=normal")) {
		t.Fatalf("URL projection discarded ordinary query material: %s", encoded)
	}
}

func TestImportResponseRedactsDuplicatedResultDiagnostics(t *testing.T) {
	const secret = "result-warning-token-7Qx9K2Lm4Np8"
	response := newScriptImportResponse(scriptImportResponseOptions{
		Warnings: []string{"Authorization: Bearer " + secret},
		Result: map[string]any{
			"warnings":    []string{"Authorization: Bearer " + secret},
			"assumptions": []string{"password=" + secret},
			"unsupported": []string{"Cookie: session=" + secret},
			"summary":     "X-API-Key: " + secret,
		},
	})
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(secret)) {
		t.Fatalf("duplicated converter diagnostics disclosed a source credential: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte(maskedScriptSecret)) {
		t.Fatalf("diagnostic redaction marker missing: %s", encoded)
	}
}

func TestEnterpriseImportResponseRedactsRecordedResourceURLCredentials(t *testing.T) {
	const (
		username = "resource-source-user"
		password = "resource-source-password"
		token    = "resource-query-token"
	)
	rawURL := "https://" + username + ":" + password + "@cdn.example.test/app.js?token=" + token + "&variant=modern"
	script := &dsl.Script{
		Name: "Enterprise resources", Version: "1", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Request", Type: "request", Method: http.MethodGet, Target: "/"}},
	}
	result := &enterprisearchive.ConvertResult{
		Script:                       script,
		Scripts:                      []*dsl.Script{script},
		RecordedResourceURLs:         []string{rawURL},
		RecordedResourceURLsCaptured: true,
	}
	server := NewServer(ServerConfig{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-script-archive", nil)
	response := httptest.NewRecorder()
	server.writeEnterpriseArchiveImportResponse(
		response,
		request,
		http.StatusOK,
		result,
		enterprisearchive.ParseRuntimeSettingMigrationPolicy(""),
	)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	for _, secret := range []string{username, password, token} {
		if strings.Contains(response.Body.String(), secret) {
			t.Fatalf("enterprise import disclosed recorded-resource URL credential %q: %s", secret, response.Body.String())
		}
	}
	if !strings.Contains(response.Body.String(), maskedScriptSecret) || !strings.Contains(response.Body.String(), "variant=modern") {
		t.Fatalf("enterprise recorded-resource URL was not safely projected: %s", response.Body.String())
	}
}

func TestPostmanImportRedactsShortCustomSensitiveHeaderEverywhere(t *testing.T) {
	server := NewServer(ServerConfig{})
	const secret = "v9Q!"
	payload := map[string]any{
		"name":       "Sensitive Postman header",
		"project_id": "project-a",
		"content": `{
  "info": {"name": "Sensitive Collection"},
  "item": [{
    "name": "Get",
    "request": {
      "method": "GET",
      "header": [{"key": "X-Auth-Token", "value": "` + secret + `"}],
      "url": "https://example.test/get"
    }
  }]
}`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/postman", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), secret) {
		t.Fatalf("Postman response disclosed short custom sensitive header: %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), maskedScriptSecret) {
		t.Fatalf("Postman response omitted redaction sentinel: %s", response.Body.String())
	}
}

func TestDurableProjectionRecursivelyRedactsTrueClientReviewEvidence(t *testing.T) {
	const secret = "x2P!"
	script := &dsl.Script{
		Name: "durable review", Version: "1", Protocol: "browser", ProjectID: "project-a",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "trueclient", Preserved: map[string]any{
			"review_artifact": map[string]any{
				"trueclient_archive_inspection": map[string]any{
					"steps": []any{map[string]any{
						"source_snippet": `<Argument name="Password" value="` + secret + `"/>`,
						"object_descriptor": map[string]any{
							"name": "Password", "text": secret, "attributes": map[string]any{"value": secret},
						},
					}},
				},
			},
		}}},
	}
	projected := maskScriptSecrets(script)
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(secret)) {
		t.Fatalf("durable review projection leaked TrueClient secret: %s", encoded)
	}
	if !bytes.Contains(encoded, []byte(maskedScriptSecret)) {
		t.Fatalf("durable review projection omitted redaction sentinel: %s", encoded)
	}
}
