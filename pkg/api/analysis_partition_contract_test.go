package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
)

func TestNativePartitionCapabilitiesDescribeContractWithoutAdmittingWork(t *testing.T) {
	t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", "")
	beforeJobs, beforeNative := len(analysisPipelineJobs), analytics.NativeBusy()
	w := httptest.NewRecorder()
	(&Server{}).handleAnalysisCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/v1/analysis/capabilities", nil))
	var got AnalysisCapabilitiesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.PartitionManifestVersion != 1 || got.PartitionPruningAvailable != analytics.Available() || got.Enabled || len(analysisPipelineJobs) != beforeJobs || analytics.NativeBusy() != beforeNative {
		t.Fatal("partition wire capability invented project/job admission", got)
	}
}

func TestOpenAPINativePartitionSelectionAndCapabilityContractsAreTyped(t *testing.T) {
	routes := []discoveredRoute{
		{Method: http.MethodGet, Path: "/api/v1/analysis/capabilities", Handler: "handleAnalysisCapabilities"},
		{Method: http.MethodPost, Path: "/api/v1/analysis/datasets/{id}/series", Handler: "handleAnalysisDatasetSeries"},
		{Method: http.MethodPost, Path: "/api/v1/analysis/compare", Handler: "handleAnalysisComparison"},
	}
	contracts, err := inferRouteContracts("server.go", routes)
	if err != nil {
		t.Fatal(err)
	}
	caps := contracts[routes[0].Method+" "+routes[0].Path].Responses["200"].Content["application/json"].Schema
	if caps.Properties["partition_manifest_version"].Type != "integer" || caps.Properties["partition_pruning_available"].Type != "boolean" {
		t.Fatal("partition compatibility metadata absent", caps.Properties)
	}
	for _, route := range routes[1:] {
		schema := contracts[route.Method+" "+route.Path].Responses["200"].Content["application/json"].Schema
		window := schema.Properties["time_window"]
		if window.Type != "object" || window.Properties["resolution"].Type != "string" {
			t.Fatal("explicit window response untyped", route.Path, window)
		}
		for _, field := range []string{"requested_from", "requested_to", "from", "to"} {
			if window.Properties[field].Type != "string" || window.Properties[field].Format != "date-time" {
				t.Fatal("window boundary untyped", field, window.Properties[field])
			}
		}
		selections := schema.Properties["artifact_selections"]
		if selections.Type != "array" || selections.Items == nil || selections.Items.Type != "object" {
			t.Fatal("untyped selection response", route.Path, selections)
		}
		for _, field := range []string{"dataset_id", "mode", "manifest_digest"} {
			if selections.Items.Properties[field].Type != "string" {
				t.Fatal("selection identity not typed", field)
			}
		}
		for _, field := range []string{"dataset_revision", "manifest_version", "total_objects", "selected_objects", "pruned_objects", "total_artifact_bytes", "staged_artifact_bytes", "avoided_artifact_bytes"} {
			if selections.Items.Properties[field].Type != "integer" {
				t.Fatal("selection geometry not typed", field)
			}
		}
		if _, exists := selections.Items.Properties["physical_scan_bytes"]; exists {
			t.Fatal("artifact selection invented physical scan measurement")
		}
	}
}
