package api

import (
	"net/http"

	"github.com/jasonczech/apexload/pkg/convert/paramize"
)

// scriptParameterizeSuggestResponse mirrors paramize.Result and adds a note that
// explains the editor's limitation: correlation detection needs captured
// request/response observations, which a saved script does not carry, so the
// "Deep scan (server)" flow surfaces hardcoded data parameters and secrets only.
type scriptParameterizeSuggestResponse struct {
	paramize.Result
	Note string `json:"note,omitempty"`
}

// handleSuggestScriptParameterization handles
// POST /api/v1/scripts/{id}/parameterize/suggest.
//
// It runs the deterministic parameterization engine over a saved script and returns
// data-param and secret suggestions for the editor's "Deep scan (server)" flow. The
// engine is import-agnostic — it operates on any dsl.Script — so the editor reuses the
// same suggestion model the importers produce. The approved subset is then applied via
// the shared POST /api/v1/scripts/import/parameterize/apply endpoint, which already
// accepts a raw script plus the selected suggestions.
func (s *Server) handleSuggestScriptParameterization(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	stored, ok := s.runner.GetScript(id)
	if !ok {
		writeError(w, http.StatusNotFound, "script not found")
		return
	}

	result := paramize.Suggest(stored.Script, paramize.Options{
		EnableDataParams: true,
		EnableSecrets:    true,
	})

	s.recordAuditEvent(r, "script.parameterize.suggest", "success", "script", id, stored.Script.ProjectID, map[string]any{
		"suggestions": len(result.Suggestions),
	})

	writeJSON(w, http.StatusOK, scriptParameterizeSuggestResponse{
		Result: result,
		Note:   "Correlation detection needs captured request/response samples, which a saved script does not carry — this scan covers hardcoded data parameters and secrets only.",
	})
}
