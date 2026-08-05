package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestProtocolBundleSupportMatrixEndpointCoversRequestedBundles(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/protocol-bundles/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload protocolBundleSupportResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	for _, bundle := range []string{"web_api", "database", "network", "oracle_e_business", "templates", "sap_gui_foundation"} {
		if payload.BundleCounts[bundle] == nil {
			t.Fatalf("bundle_counts missing %q in %#v", bundle, payload.BundleCounts)
		}
	}
	for _, id := range []string{"web-api-bruno", "database-sql", "network-dns", "network-ldap", "oeb-oracle-web", "oeb-oracle-nca", "sap-gui-foundation"} {
		if !protocolBundleMatrixContains(payload.SupportMatrix, id) {
			t.Fatalf("support_matrix missing %q", id)
		}
	}
	if !protocolBundleEntitlementContains(payload.Entitlements, "sap_gui_foundation") {
		t.Fatalf("entitlements = %#v, want sap_gui_foundation", payload.Entitlements)
	}
	if !protocolBundleRunnerContains(payload.RunnerClasses, "windows_sap_gui_runner") {
		t.Fatalf("runner_classes = %#v, want windows_sap_gui_runner", payload.RunnerClasses)
	}
	if !protocolBundleRunnerContains(payload.RunnerClasses, "bruno_node_runner") {
		t.Fatalf("runner_classes = %#v, want bruno_node_runner", payload.RunnerClasses)
	}
	if payload.StatusCounts["review_only"] == 0 || payload.StatusCounts["external_runner_required"] == 0 {
		t.Fatalf("status_counts = %#v, want review-only and external-runner rows", payload.StatusCounts)
	}
}

func TestTrustedHeaderAuthRequiresViewerForProtocolBundleSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/protocol-bundles/support-matrix", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/protocol-bundles/support-matrix", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Roles", RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusOK {
		t.Fatalf("viewer status = %d, want %d body=%s", viewerResp.Code, http.StatusOK, viewerResp.Body.String())
	}
}

func TestProtocolBundleJavaVuserRunnerDescribesQualifiedPersistentPlatforms(t *testing.T) {
	supportMatrix := protocolBundleSupportMatrix()
	var javaEntry *ProtocolBundleSupportEntry
	for index := range supportMatrix {
		if supportMatrix[index].ID == "template-java-vuser" {
			javaEntry = &supportMatrix[index]
			break
		}
	}
	if javaEntry == nil {
		t.Fatal("Java Vuser support entry is missing")
	}
	javaContractParts := append([]string(nil), javaEntry.RuntimeCoverage...)
	javaContractParts = append(javaContractParts, javaEntry.RemainingGaps...)
	javaContractParts = append(javaContractParts, javaEntry.RecommendedWorkflow)
	javaContract := strings.Join(javaContractParts, " ")
	for _, required := range []string{
		protocol.FeatureJavaVuserPersistentLifecycleV1,
		protocol.FeatureJavaVuserSessionFrameTokenV1,
		"strong descendant-process containment",
		"stock non-privileged Helm, Kustomize, and Compose sidecars support legacy full_lifecycle only",
		"qualified Windows runner or delegated-cgroup Linux runner",
	} {
		if !strings.Contains(javaContract, required) {
			t.Errorf("Java Vuser support contract is missing %q: %#v", required, javaEntry)
		}
	}

	runnerClasses := protocolBundleRunnerClasses()
	var runner *ProtocolBundleRunnerClass
	for index := range runnerClasses {
		if runnerClasses[index].ID == "java_vuser_jvm_runner" {
			runner = &runnerClasses[index]
			break
		}
	}
	if runner == nil {
		t.Fatal("Java Vuser runner class is missing")
	}
	if runner.OS != "linux_or_windows" {
		t.Errorf("Java Vuser runner OS = %q, want linux_or_windows", runner.OS)
	}
	if !strings.Contains(runner.Notes, protocol.FeatureJavaVuserPersistentLifecycleV1) ||
		!strings.Contains(runner.Notes, protocol.FeatureJavaVuserSessionFrameTokenV1) ||
		!strings.Contains(runner.Notes, "stock non-privileged sidecars remain legacy-only") {
		t.Errorf("Java Vuser runner notes = %q, want persistent capability and stock-sidecar boundary", runner.Notes)
	}
}

func protocolBundleMatrixContains(entries []ProtocolBundleSupportEntry, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func protocolBundleEntitlementContains(entries []ProtocolBundleEntitlement, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}

func protocolBundleRunnerContains(entries []ProtocolBundleRunnerClass, id string) bool {
	for _, entry := range entries {
		if entry.ID == id {
			return true
		}
	}
	return false
}
