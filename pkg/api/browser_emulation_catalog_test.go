package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleBrowserEmulationCatalog(t *testing.T) {
	server := NewServer(ServerConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/dsl/browser-emulation-catalog", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", resp.Code, resp.Body.String())
	}
	var payload browserEmulationCatalogResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Browsers) == 0 || len(payload.Platforms) == 0 || len(payload.Languages) == 0 {
		t.Fatalf("catalog payload incomplete: %d browsers, %d platforms, %d languages", len(payload.Browsers), len(payload.Platforms), len(payload.Languages))
	}
	platformIDs := map[string]bool{}
	for _, platform := range payload.Platforms {
		platformIDs[platform.ID] = true
	}
	for _, browser := range payload.Browsers {
		if browser.Type == "" || len(browser.Versions) == 0 || len(browser.Platforms) == 0 {
			t.Fatalf("browser entry incomplete: %#v", browser)
		}
		for _, platform := range browser.Platforms {
			if !platformIDs[platform] {
				t.Fatalf("browser %s references unknown platform %q", browser.Type, platform)
			}
		}
	}
}
