package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	analytics "github.com/jasonczech/apexload/pkg/analysis/duckdb"
)

func TestAnalysisCapabilitiesAdvertiseExactImportedIdentityVersionWithoutNativeAdmission(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			projects := ""
			if enabled {
				projects = "project"
			}
			t.Setenv("APEXLOAD_ANALYSIS_PROJECTS", projects)
			// Invalid operator geometry cannot turn a supported wire contract into
			// available native execution or allocate a heavy job.
			t.Setenv("APEXLOAD_ANALYSIS_MEMORY_LIMIT", "3GiB")
			beforeJobs, beforeNative := len(analysisPipelineJobs), analytics.NativeBusy()
			w := httptest.NewRecorder()
			(&Server{}).handleAnalysisCapabilities(w, httptest.NewRequest(http.MethodGet, "/api/v1/analysis/capabilities?project_id=project", nil))
			var got AnalysisCapabilitiesResponse
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || got.ImportedIdentityVersion != 2 || got.Enabled != enabled || got.NativeParquet != analytics.Available() || got.ResourceConfigurationAccepted {
				t.Fatalf("identity contract conflated with execution admission: status=%d %+v", w.Code, got)
			}
			if len(analysisPipelineJobs) != beforeJobs || analytics.NativeBusy() != beforeNative {
				t.Fatal("capability inspection admitted native ownership")
			}
		})
	}
}

func TestOpenAPICapabilitiesRequireImportedIdentityVersion(t *testing.T) {
	route := discoveredRoute{Method: http.MethodGet, Path: "/api/v1/analysis/capabilities", Handler: "handleAnalysisCapabilities"}
	contracts, err := inferRouteContracts("server.go", []discoveredRoute{route})
	if err != nil {
		t.Fatal(err)
	}
	schema := contracts[route.Method+" "+route.Path].Responses["200"].Content["application/json"].Schema
	version := schema.Properties["imported_identity_version"]
	if version.Type != "integer" || version.Nullable || !containsOpenAPIField(schema.Required, "imported_identity_version") {
		t.Fatal("identity compatibility authority absent or optional in capabilities schema", schema)
	}
}
