package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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
	for _, bundle := range []string{"database", "network", "oracle_e_business", "templates", "sap_gui_foundation"} {
		if payload.BundleCounts[bundle] == nil {
			t.Fatalf("bundle_counts missing %q in %#v", bundle, payload.BundleCounts)
		}
	}
	for _, id := range []string{"database-sql", "network-dns", "network-ldap", "oeb-oracle-web", "oeb-oracle-nca", "sap-gui-foundation"} {
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
