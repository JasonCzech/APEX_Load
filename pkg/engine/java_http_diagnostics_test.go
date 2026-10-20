package engine

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func javaEngineDiagnostic() *protocol.JavaHTTPDiagnostics {
	zero, size := 0.0, int64(0)
	return &protocol.JavaHTTPDiagnostics{Version: 1, Mode: "embedded_opaque_bytes", InputState: "empty", RequestDecodedBytes: &size, PreparedRequestBodyBytes: &size, AppliedRequestCompression: "none", ResponseState: "complete", ResponseRetainedBytes: &size, ResponseBase64Bytes: &size, Base64EncodeMs: &zero, EnclosingMs: 1, ObjectFootprint: "unavailable_embedded_opaque_bytes"}
}

func TestJavaHTTPMetricsOwnPointersAndExcludeSyntheticTransactions(t *testing.T) {
	d := javaEngineDiagnostic()
	input := &protocol.Result{ActionName: "opaque-call", Success: true, ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: d, RequestPayloadKB: 1}}
	projected := compactMetricsResult(input)
	*d.Base64EncodeMs = 99
	if *projected.ServerTelemetry.JavaHTTP.Base64EncodeMs != 0 {
		t.Fatal("compact result aliases source")
	}
	action := &ActionMetrics{}
	mergeActionResult(action, projected)
	if action.ServerTelemetry.JavaHTTP == nil || action.ServerTelemetry.JavaHTTP.Samples != 1 || action.ServerTelemetry.JavaHTTP.RequestDecoded.Count != 1 {
		t.Fatal("action lost observed zero", action)
	}
	projected.BusinessTransaction = true
	transaction := &ActionMetrics{}
	mergeActionResult(transaction, projected)
	if transaction.ServerTelemetry.JavaHTTP != nil || transaction.Count != 1 {
		t.Fatal("synthetic transaction duplicated diagnostic sample", transaction)
	}
	if compactMetricsResult(projected).ServerTelemetry.JavaHTTP != nil || projected.ServerTelemetry.JavaHTTP == nil {
		t.Fatal("transaction compaction leaked diagnostics or mutated producer")
	}
	vu := &VUser{}
	tx := vu.recordTransaction("wrapper", time.Millisecond, true, projected)
	if tx == nil || tx.ServerTelemetry == nil || tx.ServerTelemetry.JavaHTTP != nil || tx.ServerTelemetry.RequestPayloadKB != 1 || projected.ServerTelemetry.JavaHTTP == nil {
		t.Fatal("wrapper changed source/legacy telemetry or duplicated Java sample", tx)
	}
}

func TestJavaHTTPMetricBoundsRetainRefusalWithoutPeerContent(t *testing.T) {
	input := &protocol.Result{ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: &protocol.JavaHTTPDiagnostics{Version: 9, Mode: strings.Repeat("payload-secret", 1024)}}}
	projected := compactMetricsResult(input)
	if projected.ServerTelemetry.JavaHTTP == nil || projected.ServerTelemetry.JavaHTTP.Valid() {
		t.Fatal("refusal became absent or measured")
	}
	encoded, _ := json.Marshal(projected)
	if strings.Contains(string(encoded), "payload-secret") {
		t.Fatal("metric projection retained invalid peer content")
	}
	scrubSensitiveResultMetadata(input, nil)
	encoded, _ = json.Marshal(input)
	if strings.Contains(string(encoded), "payload-secret") || input.ServerTelemetry.JavaHTTP == nil {
		t.Fatal("redaction lost refusal or retained peer content")
	}
	summary := &ServerTelemetrySummary{}
	mergeServerTelemetrySummary(summary, projected.ServerTelemetry)
	if summary.JavaHTTP == nil || summary.JavaHTTP.InvalidSamples != 1 || summary.JavaHTTP.Samples != 0 {
		t.Fatal("invalid diagnostic became measured", summary)
	}
}

func TestJavaHTTPInlineSummaryParticipatesInSnapshotBudget(t *testing.T) {
	s := &ServerTelemetrySummary{}
	mergeServerTelemetrySummary(s, &protocol.ServerTelemetry{JavaHTTP: javaEngineDiagnostic()})
	with := MetricsSnapshot{ByAction: map[string]*ActionMetrics{"call": {ServerTelemetry: s}}}
	got := estimateMetricsSnapshotBytes(with)
	size := int(unsafe.Sizeof(*s.JavaHTTP))
	s.JavaHTTP = nil
	without := estimateMetricsSnapshotBytes(with)
	if got-without < size {
		t.Fatalf("summary %d bytes not counted in retention delta %d", size, got-without)
	}
}
