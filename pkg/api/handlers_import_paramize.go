package api

import "github.com/jasonczech/apexload/pkg/recordbridge"

import "github.com/jasonczech/apexload/pkg/internal/importreview"

import (
	"net/http"
)

type importParameterizeApplyRequest = recordbridge.ImportParameterizeApplyRequest

type importParameterizeApplyResponse = recordbridge.ImportParameterizeApplyResponse

// handleApplyImportParameterization handles
// POST /api/v1/scripts/import/parameterize/apply.
// It applies the user-approved subset of parameterization suggestions to an
// import draft and returns the mutated script plus per-change diffs. The
// endpoint is importer-agnostic: any import response's parameterization block
// can be replayed through it before the script is saved.
func (s *Server) handleApplyImportParameterization(w http.ResponseWriter, r *http.Request) {
	s.recordingHTTPService().HandleApplyImportParameterization(w, r)
}

var sanitizeParameterizationDiffs = importreview.SanitizeParameterizationDiffs
