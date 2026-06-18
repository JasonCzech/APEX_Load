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

// dslConfigDefaultsResponse is the payload of GET /api/v1/dsl/config-defaults: the
// load engine's built-in default ScriptConfig — the values the runtime applies when
// a script leaves a field unset. The dashboard's per-group runtime editor uses it as
// the lowest "inherited" layer so it can show the real effective value (never a
// blank placeholder) for fields a script does not set.
type dslConfigDefaultsResponse struct {
	Config dsl.ScriptConfig `json:"config"`
}

// handleConfigDefaults returns the engine's default ScriptConfig.
func (s *Server) handleConfigDefaults(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, dslConfigDefaultsResponse{Config: dsl.DefaultScriptConfig()})
}
