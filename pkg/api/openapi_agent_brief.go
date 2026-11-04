package api

import "strings"

// agentBriefErrorStatuses are the coded error statuses the agent-brief routes
// answer with (writeAgentError), which the handler walker cannot see.
var agentBriefErrorStatuses = map[string]string{
	"400": "Invalid detail, format or baseline (code invalid_detail, invalid_format or invalid_baseline).",
	"403": "A CI credential bound to another run (code forbidden), or no access to the run.",
	"404": "No such run, comparison or finding (code run_not_found, comparison_not_found or finding_not_found).",
	"409": "The run has not finished or its archive is unavailable (code run_not_finished or archive_unavailable).",
	"503": "Saved comparisons are unavailable on this controller (code comparison_storage_unavailable).",
	"500": "Controller failure (code internal, with ref).",
}

// applyAgentBriefErrorContract documents the agent-brief routes' coded
// errors: {error, code, remediation}, plus ref on a 500.
func applyAgentBriefErrorContract(route discoveredRoute, contract *inferredRouteContract) {
	if contract == nil || !strings.Contains(route.Path, "/agent-brief") {
		return
	}
	if contract.Responses == nil {
		contract.Responses = map[string]oasResponse{}
	}
	schema := oasSchema{
		Type: "object",
		Properties: map[string]oasSchema{
			"error":       {Type: "string", Description: "Human-readable error message."},
			"code":        {Type: "string", Description: "Stable machine-readable error code an agent branches on. Present when the agent-brief handler rejects the request; an access or lookup refusal from the route's middleware (403, 503) carries only error."},
			"remediation": {Type: "string", Description: "What to do next."},
			"ref":         {Type: "string", Description: "Server log correlation id (500 only)."},
		},
		Required: []string{"error"},
	}
	for status, description := range agentBriefErrorStatuses {
		contract.Responses[status] = oasResponse{Description: description, Content: map[string]oasMediaType{"application/json": {Schema: schema}}}
	}
}
