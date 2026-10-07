package api

import (
	"net/http"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func writeBrunoRunnerRetired(w http.ResponseWriter) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"error": dsl.BrunoRunnerRetiredMessage,
		"code":  "bruno_runner_retired",
	})
}
