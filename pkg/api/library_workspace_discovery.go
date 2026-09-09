package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
)

// RemoteWorkspace is deliberately a projection: discovery never returns remote
// project policies, credentials or application configuration to the dashboard.
type RemoteWorkspace struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Server) handleLibraryRemoteWorkspaces(w http.ResponseWriter, r *http.Request) {
	remote, ok, err := s.snapshotLibraryRemoteConfig(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "remote library not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, "saved remote connection is unavailable")
		return
	}
	kind, parent := r.URL.Query().Get("kind"), r.URL.Query().Get("parent_id")
	path := "/api/v1/projects"
	switch kind {
	case "projects":
	case "applications":
		if parent == "" {
			writeError(w, http.StatusBadRequest, "parent project is required")
			return
		}
		path = "/api/v1/applications?project_id=" + url.QueryEscape(parent)
	case "releases":
		if parent == "" || parent == "." || parent == ".." || strings.ContainsAny(parent, "/\\") {
			writeError(w, http.StatusBadRequest, "parent application is required and must be a single resource identifier")
			return
		}
		path = "/api/v1/applications/" + url.PathEscape(parent) + "/releases"
	default:
		writeError(w, http.StatusBadRequest, "kind must be projects, applications or releases")
		return
	}
	var response map[string]json.RawMessage
	// Uses the same bounded, identity/egress-governed transport as federation.
	if err := s.remoteJSON(r.Context(), remote, http.MethodGet, path, nil, &response); err != nil {
		writeError(w, http.StatusBadGateway, "Remote workspace discovery failed. Check connection and workspace read permissions; older remotes may not support workspace catalogs.")
		return
	}
	raw, present := response[kind]
	if !present {
		writeError(w, http.StatusBadGateway, "remote did not return a workspace catalog")
		return
	}
	var items []RemoteWorkspace
	if err := json.Unmarshal(raw, &items); err != nil {
		writeError(w, http.StatusBadGateway, "invalid remote workspace catalog")
		return
	}
	if items == nil {
		items = []RemoteWorkspace{}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
