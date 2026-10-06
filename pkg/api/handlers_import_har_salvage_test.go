package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func truncatedSalvageHAR() string {
	entry := func(path string) string {
		return `{"startedDateTime":"2026-01-01T00:00:00.000Z","request":{"method":"GET","url":"https://example.test/` + path + `","httpVersion":"HTTP/1.1","headers":[]},"response":{"status":200,"httpVersion":"HTTP/1.1","headers":[],"content":{"mimeType":"application/json","text":"{}"}}}`
	}
	full := `{"log":{"version":"1.2","creator":{"name":"synthetic","version":"1"},"entries":[` + entry("first") + "," + entry("second") + "," + entry("third") + `]}}`
	return full[:strings.Index(full, "/third")]
}

func postSalvageHAR(t *testing.T, route string, fields map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		_ = writer.WriteField(key, value)
	}
	part, err := writer.CreateFormFile("file", "truncated.har")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte(truncatedSalvageHAR()))
	_ = writer.Close()
	req := httptest.NewRequest(http.MethodPost, route, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	NewServer(ServerConfig{}).mux.ServeHTTP(response, req)
	return response
}

func TestHARImportSalvagesTruncatedCaptureOnlyWhenAsked(t *testing.T) {
	for _, route := range []string{"/api/v1/scripts/import/har", "/api/v1/scripts/import/salesforce"} {
		refused := postSalvageHAR(t, route, nil)
		if refused.Code != http.StatusUnprocessableEntity || !strings.Contains(refused.Body.String(), "truncated after 2 complete entries") || !strings.Contains(refused.Body.String(), "salvage_truncated=true") {
			t.Fatalf("%s without salvage: %d %s", route, refused.Code, refused.Body.String())
		}
		if route == "/api/v1/scripts/import/salesforce" {
			continue // a synthetic non-Salesforce capture is rejected there after salvage
		}
		salvaged := postSalvageHAR(t, route, map[string]string{"salvage_truncated": "true"})
		if salvaged.Code != http.StatusOK {
			t.Fatalf("%s with salvage: %d %s", route, salvaged.Code, salvaged.Body.String())
		}
		var decoded struct {
			Script struct {
				Actions []struct {
					Target string `json:"target"`
				} `json:"actions"`
			} `json:"script"`
			Warnings []string `json:"warnings"`
		}
		if err := json.Unmarshal(salvaged.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if len(decoded.Script.Actions) != 2 || !strings.Contains(strings.Join(decoded.Warnings, "\n"), "The HAR file is truncated: 2 complete entries recovered") {
			t.Fatalf("salvaged import = %d actions, warnings %v", len(decoded.Script.Actions), decoded.Warnings)
		}
	}
}
