package api

import (
	"net/http"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// dslActionCatalogResponse is the payload of GET /api/v1/dsl/actions: the
// declarative action-type catalog the dashboard editor uses for completion
// and field hints, and external tooling uses for schema-aware authoring.
// Serving it from the controller keeps the editor in lockstep with the
// running validator — no bundled schema to drift.
type dslActionCatalogResponse struct {
	Protocol     string           `json:"protocol,omitempty"`
	Protocols    []string         `json:"protocols"`
	CommonFields []dsl.FieldSpec  `json:"common_fields"`
	Actions      []dsl.ActionSpec `json:"actions"`
}

// handleListDSLActions returns the DSL action-type catalog. An optional
// ?protocol= query (e.g. "browser", "jms-solace") narrows the list to the
// action types valid for that script protocol, engine actions included
// where the validator accepts them.
func (s *Server) handleListDSLActions(w http.ResponseWriter, r *http.Request) {
	protocol := strings.TrimSpace(r.URL.Query().Get("protocol"))
	response := dslActionCatalogResponse{
		Protocol:     protocol,
		Protocols:    dsl.CatalogProtocolFamilies(),
		CommonFields: dsl.CommonActionFields(),
	}
	if protocol == "" {
		response.Actions = dsl.AllActionSpecs()
	} else {
		response.Actions = dsl.ActionSpecsForProtocol(protocol)
	}
	writeJSON(w, http.StatusOK, response)
}
