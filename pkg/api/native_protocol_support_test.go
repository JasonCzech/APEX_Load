package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNativeProtocolSupportMatrixEndpointCoversRemainingExecutableProtocols(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/protocols/native-support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload NativeProtocolSupportResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if payload.SchemaVersion != nativeProtocolSupportSchemaVersion {
		t.Fatalf("schema version = %q", payload.SchemaVersion)
	}
	for _, protocol := range []string{"websocket", "grpc", "database", "network", "jms-solace", "jms-servicebus", "kafka"} {
		entry := nativeProtocolMatrixEntry(payload.SupportMatrix, protocol)
		if entry == nil {
			t.Fatalf("support matrix missing %s", protocol)
		}
		if entry.Status != "supported" || entry.ExecutionPosture != "native_linux" {
			t.Fatalf("%s status/posture = %s/%s", protocol, entry.Status, entry.ExecutionPosture)
		}
		if len(entry.ActionEditor) == 0 || len(entry.ReplayEvidence) == 0 || len(entry.ReportExport) == 0 {
			t.Fatalf("%s entry missing parity surfaces: %#v", protocol, entry)
		}
	}
	if payload.StatusCounts["supported"] < 5 {
		t.Fatalf("status counts = %#v, want at least five supported protocols", payload.StatusCounts)
	}
}

func TestTrustedHeaderAuthRequiresViewerForNativeProtocolSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	enableTrustedHeaderAuth(t, server)

	unauthorizedReq := httptest.NewRequest(http.MethodGet, "/api/v1/protocols/native-support-matrix", nil)
	unauthorizedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(unauthorizedResp, unauthorizedReq)
	if unauthorizedResp.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d body=%s", unauthorizedResp.Code, http.StatusUnauthorized, unauthorizedResp.Body.String())
	}

	viewerReq := httptest.NewRequest(http.MethodGet, "/api/v1/protocols/native-support-matrix", nil)
	viewerReq.Header.Set("X-Auth-Request-User", "viewer-user")
	viewerReq.Header.Set("X-APEXLoad-Roles", RoleViewer)
	viewerResp := httptest.NewRecorder()
	server.mux.ServeHTTP(viewerResp, viewerReq)
	if viewerResp.Code != http.StatusOK {
		t.Fatalf("viewer status = %d, want %d body=%s", viewerResp.Code, http.StatusOK, viewerResp.Body.String())
	}
}

func nativeProtocolMatrixEntry(entries []NativeProtocolSupportEntry, protocol string) *NativeProtocolSupportEntry {
	for _, entry := range entries {
		if entry.Protocol == protocol {
			return &entry
		}
	}
	return nil
}
