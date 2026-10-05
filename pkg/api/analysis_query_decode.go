package api

import (
	"errors"
	"io"
	"net/http"
)

// Direct approved query requests share the new-write closed grammar. Unknown
// options, aliases and duplicate semantic keys cannot disappear before native
// operation admission. Historical stored definition reads remain unchanged.
func decodeAnalysisQueryRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			writeError(w, http.StatusRequestEntityTooLarge, "analytical query exceeds request byte limit")
		} else {
			writeError(w, http.StatusBadRequest, "invalid analytical query request")
		}
		return false
	}
	if decodeAnalysisDefinitionJSON(body, target) != nil {
		writeError(w, http.StatusBadRequest, "unsupported, ambiguous or malformed analytical query request")
		return false
	}
	return true
}
