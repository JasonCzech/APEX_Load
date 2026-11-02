package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/sourceview"
)

// TestHARImportRetainedSourceTextNeverCarriesSalesforceSecrets drives a
// redact_sensitive Salesforce sign-in HAR import large enough that its source review keeps a retained
// full text, and checks every surface a reviewer can read the capture from:
// the import response's source review (preview and any full text), and the
// stored full text that GET /projects/{id}/source-review/texts/{sha256} serves
// to "Copy whole source file". None may carry the recorded password, session ID or Aura token.
func TestHARImportRetainedSourceTextNeverCarriesSalesforceSecrets(t *testing.T) {
	const projectID = "project-har-source-secrets"
	server := sourceReviewTextTestServer(t, projectID)

	capture := buildSyntheticSalesforceHARForPairE2E(t, "RunA1001", "a3Cxx000000bcUXEAY")
	var doc map[string]any
	if err := json.Unmarshal(capture, &doc); err != nil {
		t.Fatal(err)
	}
	logDoc := doc["log"].(map[string]any)
	// A large static response pushes the capture past the preview limit, so
	// the review retains the full text rather than only the preview.
	filler := strings.Repeat("static report row padding. ", (sourceview.MaxFileChars/27)+2000)
	logDoc["entries"] = append(logDoc["entries"].([]any), map[string]any{
		"startedDateTime": "2026-11-01T12:00:09.000Z",
		"time":            25,
		"request": map[string]any{
			"method":      "GET",
			"url":         "https://acme--sandbox.sandbox.lightning.force.com/resource/report.txt",
			"httpVersion": "HTTP/2.0",
			"headers":     []any{},
		},
		"response": map[string]any{
			"status":      200,
			"httpVersion": "HTTP/2.0",
			"headers":     []map[string]string{{"name": "Content-Type", "value": "text/plain"}},
			"content":     map[string]any{"mimeType": "text/plain", "text": filler},
		},
	})
	// Browsers write "&" in form bodies verbatim, not as "\u0026".
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(doc); err != nil {
		t.Fatal(err)
	}
	padded := encoded.Bytes()
	if len(padded) <= sourceview.MaxFileChars {
		t.Fatalf("capture is %d bytes, want it past the %d-char preview limit", len(padded), sourceview.MaxFileChars)
	}
	if !bytes.Contains(padded, []byte("SynthPass")) || !bytes.Contains(padded, []byte("AQEAQSyntheticSession")) {
		t.Fatal("synthetic capture lost the secrets this test guards")
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "acme-sandbox-login.har")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(padded); err != nil {
		t.Fatal(err)
	}
	// The dashboard sends redact_sensitive=true by default.
	if err := mw.WriteField("redact_sensitive", "true"); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har?project_id="+projectID, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("import status = %d: %s", resp.Code, resp.Body.String())
	}

	// The recorded values, in every encoding the capture holds them.
	secrets := []string{"SynthPass", "AQEAQSyntheticSession", "synth-RunA1001"}
	assertNoSecret := func(surface, text string) {
		t.Helper()
		for _, secret := range secrets {
			if strings.Contains(text, secret) {
				t.Fatalf("%s carries the recorded secret %q", surface, secret)
			}
		}
	}

	// The draft's sign-in user pool holds the recorded login on purpose; the
	// guarded surfaces are the source review and its retained full text.
	var envelope struct {
		SourceReview json.RawMessage `json:"source_review"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	assertNoSecret("import response source_review", string(envelope.SourceReview))
	var decoded struct {
		SourceReview *sourceview.Review `json:"source_review"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SourceReview == nil || len(decoded.SourceReview.SourceFiles) == 0 {
		t.Fatalf("import response has no source review: %s", resp.Body.String()[:min(resp.Body.Len(), 512)])
	}
	retained := 0
	for _, file := range decoded.SourceReview.SourceFiles {
		assertNoSecret("source review preview of "+file.Path, file.Content)
		assertNoSecret("source review full text of "+file.Path, file.FullText)
		if file.FullSHA256 == "" {
			continue
		}
		retained++
		text := getSourceReviewText(t, server, projectID, file.FullSHA256, nil)
		if text.Code != http.StatusOK {
			t.Fatalf("source-review text %s status = %d: %s", file.FullSHA256, text.Code, text.Body.String())
		}
		var served sourceReviewTextResponse
		if err := json.Unmarshal(text.Body.Bytes(), &served); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(served.Content, "static report row padding") {
			t.Fatalf("served text for %s is not the retained capture", file.Path)
		}
		assertNoSecret("GET source-review/texts for "+file.Path, served.Content)
	}
	if retained == 0 {
		t.Fatal("no source file retained a full text; the capture must exceed the preview limit for this test to guard the texts endpoint")
	}
}
