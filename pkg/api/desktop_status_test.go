package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/buildinfo"
)

func TestHandleDesktopStatusIncludesBuildInfo(t *testing.T) {
	srv := NewServer(ServerConfig{DesktopMode: true})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/desktop/status", nil)
	rec := httptest.NewRecorder()
	srv.handleDesktopStatus(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got DesktopStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if !got.DesktopMode {
		t.Error("desktop_mode should be true")
	}
	// Version is sourced from buildinfo.Get(), which always resolves a non-empty
	// display version (falling back to VCS/dev metadata), so the field must be
	// populated for the dashboard's About dialog and home view.
	if got.Version == "" {
		t.Error("version should be populated from buildinfo")
	}
	if want := buildinfo.Get().DisplayVersion; got.Version != want {
		t.Errorf("version = %q, want buildinfo display version %q", got.Version, want)
	}
}
