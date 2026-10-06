package api

import (
	"go/ast"
	"net/http"
	"sort"
	"strconv"
)

func applyBrowserResourcePageContract(route discoveredRoute, contract *inferredRouteContract, definitions map[string]ast.Expr) {
	if contract == nil || route.Method != http.MethodGet || route.Path != "/api/v1/tests/{id}/archive/browser-resources" {
		return
	}
	descriptions := map[string]string{
		"project_id": "Optional exact project scope. Durable joined run/archive project authority is rechecked before publication.",
		"limit":      "Page size 1 through 100, default 50. Scan and selection are bounded; results are never silently truncated.",
		"cursor":     "Opaque 15-minute continuation for this exact query, canonical run/project, durable publication and completed source fingerprint. A changed source or authorization refuses continuation.",
		"sort_by":    "Approved sort; missing measurements sort last in both directions. Stable transaction/resource ordinals resolve ties; legacy values never substitute for native sort measurements.",
		"sort_dir":   "Sort direction, default asc; unavailable measurements remain last.",
	}
	contract.QueryParameters = nil
	for _, name := range []string{"project_id", "limit", "cursor", "sort_by", "sort_dir", "action_name", "group", "region", "resource_type", "failure_kind", "state", "source_generation", "request_id", "redirect_chain_id", "document_id", "initiating_action_id", "observation_action_id", "origin", "browser_engine", "device_profile"} {
		parameter := oasParameter{Name: name, In: "query", Schema: oasParamSchema{Type: "string"}, Description: descriptions[name]}
		if parameter.Description == "" {
			parameter.Description = "Optional exact retained resource filter. Repeated, unsupported or explicit empty fields are refused."
		}
		switch name {
		case "limit":
			parameter.Schema = oasParamSchema{Type: "integer"}
		case "sort_by":
			parameter.Schema.Enum = []string{"capture_order", "duration_ms", "native_start_ms", "response_encoded_body"}
		case "sort_dir":
			parameter.Schema.Enum = []string{"asc", "desc"}
		case "state":
			parameter.Schema.Enum = []string{"finished", "failed", "censored"}
		case "failure_kind":
			parameter.Schema.Enum = []string{"http", "transport", "timeout", "canceled", "body_limit"}
		}
		contract.QueryParameters = append(contract.QueryParameters, parameter)
	}
	sort.Slice(contract.QueryParameters, func(i, j int) bool { return contract.QueryParameters[i].Name < contract.QueryParameters[j].Name })
	if contract.Responses == nil {
		contract.Responses = map[string]oasResponse{}
	}
	for _, status := range []int{400, 403, 404, 408, 409, 413, 422, 503} {
		contract.Responses[strconv.Itoa(status)] = jsonErrorResponse(http.StatusText(status) + "; no partial resource page is returned.")
	}
	response := contract.Responses["200"]
	media := response.Content["application/json"]
	// This handler writes pre-encoded JSON bytes. Generic media inference sees
	// its literal Content-Type and otherwise replaces the typed JSON model with
	// string. Derive this approved operation directly from its actual Go DTO.
	schema := openAPISchemaForASTType(ast.NewIdent("BrowserResourcePage"), definitions, map[string]bool{}, 0)
	closeStructuredOpenAPISchema(&schema)
	media.Schema = schema
	if response.Content == nil {
		response.Content = map[string]oasMediaType{}
	}
	response.Content["application/json"] = media
	contract.Responses["200"] = response
}
