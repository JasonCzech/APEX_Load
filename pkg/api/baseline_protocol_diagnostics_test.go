package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestBaselineProtocolFindingsRetainQualificationAndIdentity(t *testing.T) {
	measure := func(ms float64, messages int64) *BaselineMeasurement {
		m := &BaselineMeasurement{Samples: 100, Successes: 100}
		for range 100 {
			protocol.MergeGRPCDiagnostic(&m.GRPC, &protocol.GRPCDiagnostics{Method: "/test.Service/Call", Mode: "unary", CallMs: ms, EncodeMs: &ms})
			protocol.MergeWebSocketDiagnostic(&m.WebSocket, &protocol.WebSocketDiagnostics{Operation: "ws_receive", ReceiveMs: &ms, MessageType: "text", MessagesReceived: messages})
		}
		return m
	}
	a, b := measure(80, 2), measure(20, 1)
	window := &BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{StartSeconds: 0, EndSeconds: 100}, Load: 10}
	row := BaselineComparisonRow{ID: "rpc", Name: "Call", Unit: "ms", Current: a, Window: window, Baselines: map[string]BaselineComparisonCell{"b": {Measurement: b, Window: window}}}
	build := func(row BaselineComparisonRow) *BaselineComparisonSnapshot {
		out := &BaselineComparisonSnapshot{Policy: defaultBaselineComparisonPolicy(), Baselines: []BaselineRunIdentity{{ID: "b"}}, Transactions: []BaselineComparisonRow{row}}
		buildBaselineFindings(out)
		return out
	}
	out := build(row)
	want := map[string]bool{"gRPC call P95": false, "gRPC protobuf encode P95": false, "WebSocket text receive-wait P95": false, "WebSocket message throughput": false}
	for _, f := range out.Findings {
		if _, ok := want[f.Metric]; ok {
			want[f.Metric] = true
			if f.Status != "changed" {
				t.Errorf("qualified finding: %+v", f)
			}
			if f.Metric == "WebSocket message throughput" && !strings.Contains(f.Message, "/s") {
				t.Errorf("wrong throughput units: %+v", f)
			}
		}
	}
	for metric, found := range want {
		if !found {
			t.Errorf("missing %s", metric)
		}
	}
	a.Samples++ // One retained action lacks diagnostics: do not score throughput.
	for _, f := range build(row).Findings {
		if f.Metric == "WebSocket message throughput" && f.Status == "changed" {
			t.Fatal("partial diagnostic coverage scored throughput")
		}
	}
	a.Samples--
	cell := row.Baselines["b"]
	cell.Qualification = []string{"Different workload"}
	row.Baselines["b"] = cell
	for _, f := range build(row).Findings {
		if f.Status == "changed" {
			t.Fatalf("unqualified evidence scored: %+v", f)
		}
	}
	b.GRPC.Method = "/test.Service/Other"
	for _, f := range build(row).Findings {
		if strings.HasPrefix(f.Metric, "gRPC") {
			t.Fatalf("mixed method comparison: %+v", f)
		}
	}
}

func TestBaselineProtocolCanonicalMetricsAndNonConflation(t *testing.T) {
	m := &BaselineMeasurement{Samples: 100, Successes: 100, Throughput: 10}
	encodeMs, decodeMs := 60.0, 65.0
	pingRTT := 12.0
	recvWait := 45.0
	for range 100 {
		protocol.MergeGRPCDiagnostic(&m.GRPC, &protocol.GRPCDiagnostics{
			Method:           "/test.Service/Stream",
			Mode:             "bidi_stream",
			CallMs:           100,
			EncodeMs:         &encodeMs,
			DecodeMs:         &decodeMs,
			MessagesSent:     2,
			MessagesReceived: 3,
			RequestBytes:     200,
			ResponseBytes:    300,
		})
		protocol.MergeWebSocketDiagnostic(&m.WebSocket, &protocol.WebSocketDiagnostics{
			Operation:        "ws_receive",
			ReceiveMs:        &recvWait,
			PingRTTMs:        []float64{pingRTT},
			MessageType:      "text",
			MessagesReceived: 1,
			PendingWrites:    4,
		})
	}
	grpcCA := m.GRPC.ClientAnalysis()
	if grpcCA.UncompressedPayloadBytes == nil || *grpcCA.UncompressedPayloadBytes != 50000 {
		t.Fatalf("expected total uncompressed payload bytes 50000, got %+v", grpcCA.UncompressedPayloadBytes)
	}
	if grpcCA.CodecOverlapRatio == nil || *grpcCA.CodecOverlapRatio <= 1.0 {
		t.Fatalf("expected unclamped grpc_codec_overlap_ratio > 1.0 (1.25), got %+v", grpcCA.CodecOverlapRatio)
	}
	if grpcCA.HTTP2TransportPhase != "unavailable" || grpcCA.ServerProcessingPhase != "unavailable" {
		t.Fatalf("expected unavailable transport/server phases, got %+v", grpcCA)
	}
	if v, ok := baselineRegressionValue(m, "grpc_uncompressed_payload_bytes"); !ok || v != 500 {
		t.Fatalf("grpc_uncompressed_payload_bytes = %v, %v; want 500, true", v, ok)
	}
	if v, ok := baselineRegressionValue(m, "grpc_client_call_rate"); !ok || v != 10 {
		t.Fatalf("grpc_client_call_rate = %v, %v; want 10, true", v, ok)
	}
	if v, ok := baselineRegressionValue(m, "grpc_codec_overlap_ratio"); !ok || v != 1.25 {
		t.Fatalf("grpc_codec_overlap_ratio = %v, %v; want 1.25, true", v, ok)
	}

	wsCA := m.WebSocket.ClientAnalysis()
	if wsCA.ApplicationPendingWrites != 4 || wsCA.PendingWritesScope != "application_writer_count" || wsCA.KernelTransportQueueDepth != "unavailable" {
		t.Fatalf("unexpected WebSocket client analysis: %+v", wsCA)
	}
	rttVal, rttOK := baselineRegressionValue(m, "ws_ping_pong_rtt_ms")
	recvVal, recvOK := baselineRegressionValue(m, "ws_receive_wait_ms")
	if !rttOK || !recvOK || rttVal == recvVal {
		t.Fatalf("expected distinct ws_ping_pong_rtt_ms (%v) and ws_receive_wait_ms (%v)", rttVal, recvVal)
	}
	if pendingVal, ok := baselineRegressionValue(m, "ws_application_pending_writes"); !ok || pendingVal != 4 {
		t.Fatalf("ws_application_pending_writes = %v, %v; want 4, true", pendingVal, ok)
	}

	// Verify evaluateBaselineRegression direction and threshold for grpc_client_call_rate:
	// CallMs=10 -> 100 calls/call-s; CallMs=20 -> 50 calls/call-s; CallMs=100 -> 10 calls/call-s.
	makeCallRateMeasurement := func(callMs float64) *BaselineMeasurement {
		bm := &BaselineMeasurement{Samples: 100, Successes: 100, Throughput: 1000.0 / callMs}
		for range 100 {
			protocol.MergeGRPCDiagnostic(&bm.GRPC, &protocol.GRPCDiagnostics{
				Method: "/test.Service/Unary",
				Mode:   "unary",
				CallMs: callMs,
			})
		}
		return bm
	}
	win := &BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{StartSeconds: 0, EndSeconds: 100}, Load: 10}
	// Case 1: drop from 100 calls/call-s (baseline CallMs=10) to 10 calls/call-s (current CallMs=100) -> regressed.
	dropSnap := &BaselineComparisonSnapshot{
		Current:   BaselineRunIdentity{ID: "cur", Status: "COMPLETE"},
		Baselines: []BaselineRunIdentity{{ID: "base", Status: "COMPLETE"}},
		Policy:    defaultBaselineComparisonPolicy(),
		Request:   BaselineComparisonRequest{RegressionMetrics: []string{"grpc_client_call_rate"}},
		Transactions: []BaselineComparisonRow{{
			ID:       "rpc-rate",
			Name:     "Unary",
			Protocol: "grpc",
			Group:    "g",
			Window:   win,
			Current:  makeCallRateMeasurement(100),
			Baselines: map[string]BaselineComparisonCell{
				"base": {Measurement: makeCallRateMeasurement(10), Window: win},
			},
		}},
	}
	dropVerdict := evaluateBaselineRegression(dropSnap)
	if dropVerdict.Status != "regressed" || len(dropVerdict.Evidence) != 1 || dropVerdict.Evidence[0].Status != "regressed" {
		t.Fatalf("expected grpc_client_call_rate drop (100 -> 10 calls/s) to regress, got %+v", dropVerdict)
	}
	// Case 2: increase from 50 calls/call-s (baseline CallMs=20) to 100 calls/call-s (current CallMs=10) -> passed.
	gainSnap := &BaselineComparisonSnapshot{
		Current:   BaselineRunIdentity{ID: "cur", Status: "COMPLETE"},
		Baselines: []BaselineRunIdentity{{ID: "base", Status: "COMPLETE"}},
		Policy:    defaultBaselineComparisonPolicy(),
		Request:   BaselineComparisonRequest{RegressionMetrics: []string{"grpc_client_call_rate"}},
		Transactions: []BaselineComparisonRow{{
			ID:       "rpc-rate",
			Name:     "Unary",
			Protocol: "grpc",
			Group:    "g",
			Window:   win,
			Current:  makeCallRateMeasurement(10),
			Baselines: map[string]BaselineComparisonCell{
				"base": {Measurement: makeCallRateMeasurement(20), Window: win},
			},
		}},
	}
	gainVerdict := evaluateBaselineRegression(gainSnap)
	if gainVerdict.Status != "passed" || len(gainVerdict.Evidence) != 1 || gainVerdict.Evidence[0].Status != "passed" {
		t.Fatalf("expected grpc_client_call_rate increase (50 -> 100 calls/s) to pass, got %+v", gainVerdict)
	}
}

func TestNonMessagingProtocolCrossCuttingLegacyMixedPeersRedactionAndBounds(t *testing.T) {
	// 1. Legacy archive field absence: rows without typed diagnostics remain absent through archive sanitization, reports, and baseline comparison.
	legacyEvent := TransactionEvent{
		ActionName: "legacy-call",
		Population: "action",
		DurationMs: 12.5,
		Success:    true,
	}
	sanitizeArchivedTransaction(&legacyEvent)
	if legacyEvent.ServerTelemetry != nil {
		t.Fatalf("legacy transaction gained fabricated ServerTelemetry: %+v", legacyEvent.ServerTelemetry)
	}
	emptyReportLines := strings.Join(protocolReportLines(&RunArchiveReport{
		ByAction: map[string]RunArchiveActionStats{
			"legacy-call": {Transactions: 100, AvgResponseMs: 12.5, P95Ms: 20.0},
		},
	}), "\n")
	if !strings.Contains(emptyReportLines, protocolReportNoSamples) {
		t.Fatalf("expected no-samples report notice for legacy archive, got: %s", emptyReportLines)
	}
	win := &BaselineLoadWindow{BaselineComparisonWindow: BaselineComparisonWindow{StartSeconds: 0, EndSeconds: 100}, Load: 10}
	legacySnap := &BaselineComparisonSnapshot{
		Current:   BaselineRunIdentity{ID: "cur", Status: "COMPLETE"},
		Baselines: []BaselineRunIdentity{{ID: "base", Status: "COMPLETE"}},
		Policy:    defaultBaselineComparisonPolicy(),
		Request:   BaselineComparisonRequest{RegressionMetrics: []string{"grpc_uncompressed_payload_bytes", "ws_ping_pong_rtt_ms"}},
		Transactions: []BaselineComparisonRow{{
			ID:       "legacy-call",
			Name:     "legacy-call",
			Protocol: "grpc",
			Group:    "g",
			Window:   win,
			Current:  &BaselineMeasurement{Samples: 100, Successes: 100},
			Baselines: map[string]BaselineComparisonCell{
				"base": {Measurement: &BaselineMeasurement{Samples: 100, Successes: 100}, Window: win},
			},
		}},
	}
	buildBaselineFindings(legacySnap)
	for _, f := range legacySnap.Findings {
		if strings.HasPrefix(f.Metric, "gRPC") || strings.HasPrefix(f.Metric, "WebSocket") {
			t.Fatalf("legacy absence produced protocol finding: %+v", f)
		}
	}

	// 2. Mixed generator peers: merging a current peer and a legacy peer marks coverage incomplete and prevents partial-sample throughput promotion.
	var javaSum *protocol.JavaHTTPSummary
	protocol.ObserveJavaHTTPAction(&javaSum, javaAPIDiagnostic())
	mergedAgg := mergeGeneratorMetricAggregates([]generatorrpc.MetricAggregate{
		{Name: "java-mixed", Count: 1, DependencyType: "java_http", JavaHTTP: javaSum},
		{Name: "java-mixed", Count: 1, DependencyType: "java_http", JavaHTTP: nil},
	})
	if mergedAgg == nil || mergedAgg.JavaHTTP == nil || !mergedAgg.JavaHTTP.Incomplete || mergedAgg.JavaHTTP.Samples != 1 {
		t.Fatalf("expected mixed generator peer merge to mark JavaHTTP incomplete with 1 sample, got %+v", mergedAgg)
	}

	// 3. PII / credential redaction across Java HTTP, gRPC, and WebSocket diagnostics, archive events, reports, and baselines.
	sentinels := []string{
		"DO_NOT_RETAIN_JAVA_CREDENTIAL_SECRET",
		"DO_NOT_RETAIN_GRPC_CREDENTIAL_SECRET",
		"DO_NOT_RETAIN_WS_CREDENTIAL_SECRET",
	}
	decBytes, prepBytes := int64(32), int64(32)
	decMs, encMs, callMs, rttMs := 1.2, 0.8, 15.0, 4.5
	statusOK := int32(0)
	redactedEvent := TransactionEvent{
		ActionName:   "multi-proto",
		Population:   "action",
		ErrorMessage: "java_http failed with password=" + sentinels[0],
		Extractions: map[string]string{
			"password": sentinels[1],
		},
		PropertyDimensions: map[string]string{
			"api_key": "api_key=" + sentinels[2],
		},
		ServerTelemetry: &protocol.ServerTelemetry{
			JavaHTTP: &protocol.JavaHTTPDiagnostics{
				Version: 1, Mode: "embedded_opaque_bytes", InputState: "decoded",
				RequestEncodedBytes: 44, RequestDecodedBytes: &decBytes, PreparedRequestBodyBytes: &prepBytes,
				AppliedRequestCompression: "none", ResponseState: "complete",
				ResponseRetainedBytes: &decBytes, ResponseBase64Bytes: &prepBytes,
				Base64DecodeMs: &decMs, Base64EncodeMs: &encMs, EnclosingMs: 10.0,
				ObjectFootprint: "unavailable_embedded_opaque_bytes",
			},
			GRPC: &protocol.GRPCDiagnostics{
				Method: "/test.Service/Call?access_token=" + sentinels[1], Mode: "unary", CallMs: callMs,
				EncodeMs: &encMs, DecodeMs: &decMs, StatusCode: &statusOK,
				MessagesSent: 1, MessagesReceived: 1, RequestBytes: 32, ResponseBytes: 32,
			},
			WebSocket: &protocol.WebSocketDiagnostics{
				Operation: "ws_receive", MessageType: "text", ReceiveMs: &callMs,
				PingRTTMs: []float64{rttMs}, MessagesReceived: 1, BytesReceived: 32, PendingWrites: 2,
			},
		},
	}
	rawBefore, err := json.Marshal(redactedEvent)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sentinels {
		if !strings.Contains(string(rawBefore), s) {
			t.Fatalf("pre-sanitization fixture missing sentinel %q", s)
		}
	}
	sanitizeArchivedTransaction(&redactedEvent)
	encodedEvent, err := json.Marshal(redactedEvent)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sentinels {
		if strings.Contains(string(encodedEvent), s) {
			t.Fatalf("archived transaction leaked sentinel %q", s)
		}
	}

	// 4. Bounded measurement overhead: merging 2,000 samples keeps O(1) summary JSON bounds across Java HTTP (with footprint), gRPC, and WebSocket.
	var boundedJava *protocol.JavaHTTPSummary
	var boundedGRPC *protocol.GRPCSummary
	var boundedWS *protocol.WebSocketSummary
	shallowBytes, graphBytes, serBytes := int64(64), int64(256), int64(128)
	serMs, deserMs := 0.4, 0.5
	fpDiag := redactedEvent.ServerTelemetry.JavaHTTP.Clone()
	fpDiag.ContainedObjectFootprint = &protocol.JavaHTTPObjectFootprintEvidence{
		FixtureKind: "approved_serializable_record",
		JVMVersion:  "21.0.2",
		ShallowObject: protocol.JavaHTTPShallowObjectFootprint{
			Metric: "java_shallow_object_bytes", Method: protocol.JavaHTTPShallowObjectMethodV1,
			JVMVersion: "21.0.2", Available: true, Bytes: &shallowBytes, Unit: "bytes",
			RetainedHeapNote: protocol.JavaHTTPObjectFootprintRetainedNote,
		},
		ReachableGraph: protocol.JavaHTTPReachableGraphFootprint{
			Metric: "java_reachable_graph_bytes", Method: protocol.JavaHTTPReachableGraphMethodV1,
			JVMVersion: "21.0.2", Available: true, Bytes: &graphBytes, Unit: "bytes",
			UniqueObjects: 5, MaxNodes: 4096, MaxDepth: 32,
			RetainedHeapNote: protocol.JavaHTTPObjectFootprintRetainedNote,
		},
		CodecExecuted: true, SerializedBytes: &serBytes,
		JavaSerializationMs: &serMs, JavaDeserializationMs: &deserMs,
	}
	for i := 0; i < 2000; i++ {
		protocol.ObserveJavaHTTPAction(&boundedJava, fpDiag)
		protocol.MergeGRPCDiagnostic(&boundedGRPC, redactedEvent.ServerTelemetry.GRPC)
		protocol.MergeWebSocketDiagnostic(&boundedWS, redactedEvent.ServerTelemetry.WebSocket)
	}
	javaJSON, err := json.Marshal(boundedJava)
	if err != nil || len(javaJSON) > protocol.MaxJavaHTTPActionsBytes/protocol.MaxJavaHTTPActions {
		t.Fatalf("JavaHTTPSummary exceeded per-action bound after 2000 samples: len=%d max=%d err=%v", len(javaJSON), protocol.MaxJavaHTTPActionsBytes/protocol.MaxJavaHTTPActions, err)
	}
	grpcJSON, err := json.Marshal(boundedGRPC)
	if err != nil || len(grpcJSON) > 16<<10 {
		t.Fatalf("GRPCSummary exceeded 16 KiB bound after 2000 samples: len=%d err=%v", len(grpcJSON), err)
	}
	wsJSON, err := json.Marshal(boundedWS)
	if err != nil || len(wsJSON) > 16<<10 {
		t.Fatalf("WebSocketSummary exceeded 16 KiB bound after 2000 samples: len=%d err=%v", len(wsJSON), err)
	}
	// Verify per-action PingRTTMs slice bound (max 32).
	excessRTT := redactedEvent.ServerTelemetry.WebSocket.Clone()
	excessRTT.PingRTTMs = make([]float64, 33)
	if excessRTT.Valid() {
		t.Fatal("expected WebSocketDiagnostics with >32 PingRTTMs entries to be rejected")
	}
}
