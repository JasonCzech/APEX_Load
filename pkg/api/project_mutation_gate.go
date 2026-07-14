package api

import (
	"net/http"
	"strings"
)

const deleteProjectRoutePattern = "DELETE /api/v1/projects/{id}"

// projectMutationAdmission serializes every synchronous HTTP mutation against
// project deletion. It intentionally uses one controller-wide gate: many legacy
// routes derive project ownership from secondary IDs or request bodies, so a
// global read side is the only fail-closed choice that cannot be bypassed by an
// omitted or forged project_id. Reads remain fully concurrent.
func (s *Server) projectMutationAdmission(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutatingHTTPMethod(r.Method) || s.isProjectDeleteRequest(r) {
			next.ServeHTTP(w, r)
			return
		}
		s.projectMutationMu.RLock()
		defer s.projectMutationMu.RUnlock()
		next.ServeHTTP(w, r)
	})
}

func isMutatingHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (s *Server) isProjectDeleteRequest(r *http.Request) bool {
	if s == nil || s.mux == nil || r == nil || r.Method != http.MethodDelete {
		return false
	}
	_, pattern := s.mux.Handler(r)
	return pattern == deleteProjectRoutePattern
}
