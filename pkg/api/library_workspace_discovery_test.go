package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLibraryRemoteWorkspaceDiscovery(t *testing.T) {
	t.Setenv("APEXLOAD_LIBRARY_ALLOW_INSECURE_HTTP", "1")
	t.Setenv("APEXLOAD_LIBRARY_REMOTE_ALLOW_PRIVATE_HOSTS", "true")
	local := NewServer(ServerConfig{})
	remoteHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/projects":
			writeJSON(w, 200, map[string]any{"projects": []map[string]any{{"id": "empty-project", "name": "Empty project", "settings": map[string]string{"secret": "do-not-disclose"}}}, "count": 1})
		case "/api/v1/applications":
			if r.URL.Query().Get("project_id") != "empty-project" {
				t.Error("missing parent scope")
			}
			writeJSON(w, 200, map[string]any{"applications": []any{}, "count": 0})
		case "/api/v1/applications/empty-app/releases":
			writeJSON(w, 200, map[string]any{"releases": []any{}, "count": 0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer remoteHTTP.Close()
	remote := createMappedLibraryRemoteForTest(t, local, remoteHTTP.URL, "local-project", "empty-project", nil, nil)
	for _, tc := range []struct {
		query    string
		status   int
		contains string
	}{
		{"kind=projects", 200, "Empty project"},
		{"kind=applications&parent_id=empty-project", 200, `"items":[]`},
		{"kind=releases&parent_id=empty-app", 200, `"items":[]`},
		{"kind=applications", 400, "parent project"},
		{"kind=releases", 400, "parent application"},
		{"kind=releases&parent_id=..", 400, "parent application"},
		{"kind=secrets", 400, "kind must"},
		{"kind=releases&parent_id=missing", 502, "internal server error"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			response := httptest.NewRecorder()
			local.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/system/library/remotes/"+remote.ID+"/workspaces?"+tc.query, nil))
			if response.Code != tc.status || !strings.Contains(response.Body.String(), tc.contains) {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "do-not-disclose") {
				t.Fatal("remote policy leaked")
			}
		})
	}
}
