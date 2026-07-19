package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/recorder"
)

func TestParseDesktopDevPreviewContext(t *testing.T) {
	tests := []struct {
		name        string
		mode        string
		projectID   string
		wantMode    string
		wantProject string
		wantError   bool
	}{
		{name: "admin", mode: "admin", projectID: "project-a", wantMode: "admin", wantProject: "project-a"},
		{name: "user", mode: " USER ", projectID: " project-b ", wantMode: "user", wantProject: "project-b"},
		{name: "missing mode defaults admin", wantMode: "admin"},
		{name: "user without project stays unscoped", mode: "user", wantMode: "user"},
		{name: "invalid mode", mode: "owner", projectID: "project-a", wantError: true},
		{name: "project with path syntax", mode: "user", projectID: "project/a", wantError: true},
		{name: "project with whitespace", mode: "user", projectID: "project a", wantError: true},
		{name: "project with control", mode: "user", projectID: "project-a\nX-Header: secret", wantError: true},
		{name: "project too long", mode: "user", projectID: strings.Repeat("a", maxDesktopDevProjectIDLength+1), wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ParseDesktopDevPreviewContext(test.mode, test.projectID)
			if test.wantError {
				if err == nil {
					t.Fatalf("ParseDesktopDevPreviewContext(%q, %q) = %#v, want error", test.mode, test.projectID, got)
				}
				return
			}
			if err != nil || got.Mode() != test.wantMode || got.ProjectID() != test.wantProject {
				t.Fatalf("ParseDesktopDevPreviewContext(%q, %q) = (%q, %q), %v; want (%q, %q)", test.mode, test.projectID, got.Mode(), got.ProjectID(), err, test.wantMode, test.wantProject)
			}
			header := make(http.Header)
			got.Apply(header)
			if header.Get(devViewHeader) != test.wantMode || header.Get(devProjectIDHeader) != test.wantProject {
				t.Fatalf("applied headers = %#v", header)
			}
		})
	}
}

func TestDesktopNativePreviewPreservesRawRecorderProjectIsolation(t *testing.T) {
	server := NewServer(ServerConfig{DesktopMode: true, DesktopCapability: "native-renderer-secret", Addr: "127.0.0.1:0"})
	session, err := server.recorder.StartSessionForProject("native-project-b-recording", "Project B", "project-b", recorder.SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.Requests = append(session.Requests, recorder.RecordedRequest{
		Seq: 1, Method: http.MethodGet, URL: "https://example.test/", Path: "/", StatusCode: http.StatusOK,
	})

	request := func(mode, projectID string) *httptest.ResponseRecorder {
		preview, parseErr := ParseDesktopDevPreviewContext(mode, projectID)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/v1/recorder/native-project-b-recording/export?format=har&raw=true", nil)
		req.Header.Set(DesktopCapabilityHeader, "native-renderer-secret")
		preview.Apply(req.Header)
		resp := httptest.NewRecorder()
		server.httpServer.Handler.ServeHTTP(resp, req)
		return resp
	}

	if denied := request("user", "project-a"); denied.Code != http.StatusForbidden {
		t.Fatalf("cross-project native raw export status=%d, want %d; body=%s", denied.Code, http.StatusForbidden, denied.Body.String())
	}
	if missing := request("user", ""); missing.Code != http.StatusForbidden {
		t.Fatalf("unscoped user native raw export status=%d, want %d; body=%s", missing.Code, http.StatusForbidden, missing.Body.String())
	}
	if allowed := request("user", "project-b"); allowed.Code != http.StatusOK {
		t.Fatalf("same-project native raw export status=%d, want %d; body=%s", allowed.Code, http.StatusOK, allowed.Body.String())
	}
	if admin := request("admin", "project-a"); admin.Code != http.StatusOK {
		t.Fatalf("admin native raw export status=%d, want %d; body=%s", admin.Code, http.StatusOK, admin.Body.String())
	}
}
