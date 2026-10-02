package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
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
	for _, protocol := range []string{"websocket", "grpc", "database", "network"} {
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
	for protocol, available := range map[string]bool{
		"jms-solace":     dsl.SolaceJMSRealAdapterAvailable(),
		"jms-servicebus": dsl.ServiceBusJMSRealAdapterAvailable(),
		"kafka":          dsl.KafkaRealAdapterAvailable(),
		"eventhubs":      dsl.EventHubsRealAdapterAvailable(),
	} {
		entry := nativeProtocolMatrixEntry(payload.SupportMatrix, protocol)
		if entry == nil {
			t.Fatalf("support matrix missing %s", protocol)
		}
		wantStatus, wantPosture := realAdapterSupport(available)
		if entry.Status != wantStatus || entry.ExecutionPosture != wantPosture {
			t.Fatalf("%s status/posture = %s/%s, want %s/%s", protocol, entry.Status, entry.ExecutionPosture, wantStatus, wantPosture)
		}
		if len(entry.ActionEditor) == 0 || len(entry.ReplayEvidence) == 0 || len(entry.ReportExport) == 0 {
			t.Fatalf("%s entry missing parity surfaces: %#v", protocol, entry)
		}
	}
	ibmMQEntry := nativeProtocolMatrixEntry(payload.SupportMatrix, "jms-ibmmq")
	if ibmMQEntry == nil {
		t.Fatal("support matrix missing jms-ibmmq")
	}
	if ibmMQEntry.Status != "review_only" || ibmMQEntry.ExecutionPosture != "mock_only" {
		t.Fatalf("jms-ibmmq status/posture = %s/%s", ibmMQEntry.Status, ibmMQEntry.ExecutionPosture)
	}
	kafkaEntry := nativeProtocolMatrixEntry(payload.SupportMatrix, "kafka")
	if kafkaEntry == nil {
		t.Fatal("support matrix missing kafka")
	}
	for _, gap := range kafkaEntry.RemainingGaps {
		if strings.Contains(gap, "Avro/Protobuf") ||
			strings.Contains(gap, "Avro schema-registry payloads remain review-only") ||
			strings.Contains(gap, "Avro remains review-only") {
			t.Fatalf("Kafka native support matrix has stale Avro review-only wording: %q", gap)
		}
	}
	if payload.StatusCounts["supported"] < 4 || payload.StatusCounts["review_only"] != 1 {
		t.Fatalf("status counts = %#v, want four baseline supported protocols and one review-only protocol", payload.StatusCounts)
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

func TestNativeProtocolAuthoringCatalogParity(t *testing.T) {
	for _, entry := range nativeProtocolSupportMatrix() {
		specs := dsl.ActionSpecsForProtocol(entry.Protocol)
		if len(specs) != len(entry.ActionTypes) {
			t.Fatalf("%s catalog mismatch", entry.Protocol)
		}
		for i, spec := range specs {
			if entry.ActionTypes[i] != spec.Type {
				t.Fatalf("%s stale action %s", entry.Protocol, entry.ActionTypes[i])
			}
		}
		if entry.Protocol == "kafka" && (strings.Contains(strings.Join(entry.ActionEditor, " "), "seek (mock only)") || strings.Contains(strings.Join(entry.Validation, " "), "real seek rejection")) {
			t.Fatal("stale Kafka guidance")
		}
	}
}
