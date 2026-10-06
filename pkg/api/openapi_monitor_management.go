package api

import (
	"go/ast"
	"net/http"
	"sort"
)

// Only these nonrecursive, bounded evidence contracts receive a fresh model
// depth budget. The visiting set still fences cycles; other models retain the
// general recursion limit, including arbitrary user-authored structures.
func completeBoundedEvidenceModel(pkg, name string) bool {
	if pkg == "" || pkg == "api" {
		switch name {
		case "TelemetryPolicyEvidence", "TelemetryBudgetPolicy", "TelemetryStageBudget", "BrowserResourcePage", "BrowserResourcePageItem", "BrowserResourcePageLocator", "BrowserResourcePagePopulation", "BrowserResourcePageCoverage":
			return true
		}
	}
	if pkg == "protocol" {
		switch name {
		case "GRPCDiagnostics", "GRPCSummary", "WebSocketDiagnostics", "WebSocketSummary", "ServerTelemetry",
			"BrowserResourceEvidence", "BrowserResourcePhase", "BrowserResourceBytes",
			"BrowserRuntimeCohort", "BrowserDocumentObservation", "BrowserDocumentEvidence",
			"BrowserDocumentSummary", "BrowserDocumentMetricSummary", "BrowserMetricDistribution",
			"BrowserRuntimeMetricAvailability", "BrowserRuntimeMetricCapability":
			return true
		}
	}
	if pkg == "analysis" {
		switch name {
		case "MonitorEvidenceEnvelope", "MonitorEvidenceAttribution", "MessagingManagementSnapshot", "MessagingManagementRow", "ManagementEntity", "KafkaManagementEntity", "ServiceBusManagementEntity", "SolaceManagementEntity", "KafkaOffsets", "ServiceBusRuntime", "SolaceSEMP", "ManagementInteger", "ManagementScope", "ManagementIdentity":
			return true
		}
	}
	return false
}

// A shared handler returns a collection for GET and a single source for saves.
// The bounded AST walker cannot choose the branch from the route method. Keep
// that distinction explicit while deriving the source fields from Go models.
func applyMonitorManagementContract(route discoveredRoute, contract *inferredRouteContract, definitions map[string]ast.Expr) {
	if contract == nil {
		return
	}
	if route.Method == http.MethodGet && route.Path == "/api/v1/projects/{id}/monitor-sources" {
		schema := openAPISchemaForASTType(&ast.ArrayType{Elt: &ast.SelectorExpr{X: ast.NewIdent("storage"), Sel: ast.NewIdent("MonitorSource")}}, definitions, map[string]bool{}, 0)
		if contract.Responses == nil {
			contract.Responses = map[string]oasResponse{}
		}
		contract.Responses["200"] = oasResponse{Description: "Project monitor sources, including immutable approved management scope and identity when configured.", Content: map[string]oasMediaType{"application/json": {Schema: schema}}}
	}
	if route.Method != http.MethodGet || route.Path != "/api/v1/tests/{id}/monitor-management" {
		return
	}
	for _, name := range []string{"from", "to"} {
		found := false
		for i := range contract.QueryParameters {
			if contract.QueryParameters[i].Name == name {
				found = true
				break
			}
		}
		if !found {
			contract.QueryParameters = append(contract.QueryParameters, oasParameter{Name: name, In: "query", Schema: oasParamSchema{Type: "string", Format: "date-time"}, Description: "Optional UTC capture-time boundary; from is inclusive and to is exclusive."})
		}
	}
	for i := range contract.QueryParameters {
		if contract.QueryParameters[i].Name == "limit" {
			contract.QueryParameters[i].Description = "Maximum receipts scanned per page: 1 through 100, default 20. Response bytes are bounded to 2 MiB; continue using the returned opaque cursor."
		}
	}
	sort.Slice(contract.QueryParameters, func(i, j int) bool { return contract.QueryParameters[i].Name < contract.QueryParameters[j].Name })
}
