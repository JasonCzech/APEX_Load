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

func TestHARImportMapsCapturedHTTP3ByMode(t *testing.T) {
	har, err := json.Marshal(map[string]any{"log": map[string]any{"version": "1.2", "creator": map[string]any{"name": "synthetic", "version": "1"}, "entries": []any{
		map[string]any{"startedDateTime": "2026-01-01T00:00:00.000Z",
			"request":  map[string]any{"method": "GET", "url": "https://h3.example.test/items", "httpVersion": "h3", "headers": []any{}},
			"response": map[string]any{"status": 200, "httpVersion": "h3", "headers": []any{}, "content": map[string]any{"mimeType": "application/json", "text": "{}"}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{})
	// JSON clients put HAR options under "options"; multipart clients send
	// http_version_mode as a form field.
	importHAR := func(mode string) *httptest.ResponseRecorder {
		body := map[string]any{"name": "H3 capture", "content": string(har)}
		if mode != "" {
			body["options"] = map[string]any{"http_version_mode": mode}
		}
		payload, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, req)
		return response
	}
	for mode, want := range map[string]string{"": "auto+h3", "upgrade": "auto+h3", "exact": "3", "inherit": ""} {
		response := importHAR(mode)
		if response.Code != http.StatusOK {
			t.Fatalf("mode %q: status = %d body=%s", mode, response.Code, response.Body.String())
		}
		var decoded struct {
			Script struct {
				Actions []struct {
					HTTPVersion string `json:"http_version"`
				} `json:"actions"`
			} `json:"script"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		if len(decoded.Script.Actions) != 1 || decoded.Script.Actions[0].HTTPVersion != want {
			t.Fatalf("mode %q: actions = %+v, want leaf %q", mode, decoded.Script.Actions, want)
		}
	}
	if response := importHAR("assume"); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "http_version_mode") {
		t.Fatalf("unknown mode: status = %d body=%s", response.Code, response.Body.String())
	}
	multipartImport := func(mode string) *httptest.ResponseRecorder {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		file, _ := writer.CreateFormFile("file", "capture.har")
		_, _ = file.Write(har)
		_ = writer.WriteField("http_version_mode", mode)
		_ = writer.Close()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", &body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, req)
		return response
	}
	if response := multipartImport("exact"); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"http_version":"3"`) {
		t.Fatalf("multipart exact: status = %d body=%.400s", response.Code, response.Body.String())
	}
	if response := multipartImport("assume"); response.Code != http.StatusBadRequest {
		t.Fatalf("multipart unknown mode: status = %d", response.Code)
	}
}
