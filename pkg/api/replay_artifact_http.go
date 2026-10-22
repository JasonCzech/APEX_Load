package api

import "net/http"

// This wrapper runs inside the existing replay RBAC middleware. Project-scoped
// callers keep its exact binding. Global/bypass callers resolve one project too,
// rather than passing an unrestricted identity into retained byte reads.
func (s *Server) bindBrowserReplayReadProject(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := authorizedProjectFromRequest(r); !ok {
			project, status, message := resolveBrowserReplaySessionProjectID(s.browserReplay, s.replayArtifacts)(r)
			if status != 0 {
				writeError(w, status, message)
				return
			}
			bindAuthorizedProjectToRequest(r, project)
		}
		next.ServeHTTP(w, r)
	})
}
