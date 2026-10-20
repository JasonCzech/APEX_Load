package api

import (
	"net/http"

	"github.com/jasonczech/apexload/pkg/storage"
)

// PATCH is intentionally separate from the legacy PUT: an old controller must
// reject the method before rewriting typed metadata it cannot preserve.
const analysisMappingAssociationVersion = 1

type AnalysisMappingAssociationRequest struct {
	ContractVersion int    `json:"contract_version"`
	RunID           string `json:"run_id"`
	Revision        int64  `json:"revision"`
}

type AnalysisMappingAssociationResponse struct {
	ContractVersion int                     `json:"contract_version"`
	Import          *storage.AnalysisImport `json:"import"`
}

func (s *Server) handleAnalysisMappingAssociation(w http.ResponseWriter, r *http.Request) {
	store, in := s.loadAnalysisImport(w, r)
	if in == nil {
		return
	}
	if in.State != "ready" {
		writeError(w, 409, "only committed datasets can be associated")
		return
	}
	var req AnalysisMappingAssociationRequest
	if !decodeAnalysisQueryRequest(w, r, &req) {
		return
	}
	if req.ContractVersion != analysisMappingAssociationVersion {
		writeError(w, 409, "unsupported mapping association contract")
		return
	}
	if req.Revision < 1 || len(req.RunID) > 128 {
		writeError(w, 400, "invalid association identity or revision")
		return
	}
	// Version1/2 mappings preserve every currently supported summary/histogram
	// declaration. Unknown future versions cannot be rewritten through this contract.
	if in.Mapping == nil {
		writeError(w, 409, "dataset mapping is unavailable")
		return
	}
	if err := in.Mapping.ValidateDefinition(); err != nil {
		writeError(w, 409, "dataset mapping is unsupported by this association contract")
		return
	}
	if saved := s.applyAnalysisAssociation(w, r, store, in, req.RunID, req.Revision); saved != nil {
		writeJSON(w, 200, AnalysisMappingAssociationResponse{ContractVersion: analysisMappingAssociationVersion, Import: saved})
	}
}
