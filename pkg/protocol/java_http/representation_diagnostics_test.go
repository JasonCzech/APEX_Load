package java_http

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/javarunner"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestJavaHTTPVersionedRepresentationObservations(t *testing.T) {
	for _, tc := range []struct {
		name, input, encoding, compression, state, responseState string
		decoded                                                  int64
		status, responseLimit                                    int
		discard, compressResponse, reachesTarget, lateFailure    bool
	}{
		{name: "binary", input: "AP8BAg==", state: "decoded", decoded: 4, responseState: "complete", reachesTarget: true},
		{name: "empty", state: "empty", responseState: "complete", reachesTarget: true},
		{name: "whitespace empty representation", input: "  ", state: "decoded", responseState: "complete", reachesTarget: true},
		{name: "gzip body", input: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("payload"), 100)), compression: "gzip", state: "decoded", decoded: 700, responseState: "complete", reachesTarget: true},
		{name: "gzip response representation", input: "YQ==", state: "decoded", decoded: 1, responseState: "complete", compressResponse: true, reachesTarget: true},
		{name: "HTTP failure retains representation", input: "YQ==", state: "decoded", decoded: 1, status: 500, responseState: "complete", reachesTarget: true},
		{name: "response cap is unavailable", input: "YQ==", state: "decoded", decoded: 1, responseLimit: 2, responseState: "unavailable", reachesTarget: true},
		{name: "discarded response is explicit", input: "YQ==", state: "decoded", decoded: 1, discard: true, responseState: "discarded", reachesTarget: true},
		// A text check fails the action after the adapter discarded the body.
		{name: "late failure after discard is unavailable", input: "YQ==", state: "decoded", decoded: 1, discard: true, lateFailure: true, responseState: "unavailable", reachesTarget: true},
		{name: "truncated base64", input: "YWJ", state: "decode_failed", responseState: "unavailable"},
		{name: "unsupported encoding", input: "YQ==", encoding: "utf8", state: "unsupported_encoding", responseState: "unavailable"},
		{name: "unsupported empty encoding", encoding: "utf8", state: "unsupported_encoding", responseState: "unavailable"},
		{name: "failed compression", input: "YQ==", compression: "unsupported", state: "decoded", decoded: 1, responseState: "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int64
			var bodyBytes atomic.Int64
			response := []byte{0, 255, 7}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				calls.Add(1)
				bodyBytes.Store(int64(len(body)))
				status := tc.status
				if status == 0 {
					status = 200
				}
				if tc.compressResponse {
					w.Header().Set("Content-Encoding", "gzip")
					w.WriteHeader(status)
					gz := gzip.NewWriter(w)
					_, _ = gz.Write(response)
					_ = gz.Close()
				} else {
					w.WriteHeader(status)
					_, _ = w.Write(response)
				}
			}))
			defer server.Close()
			j := &JavaHTTP{}
			if err := j.Init(context.Background(), map[string]any{"java_http_preserve_binary_result": true}); err != nil {
				t.Fatal(err)
			}
			defer j.Teardown(context.Background())
			if tc.lateFailure {
				if err := j.http.ConfigureVerification(protocol.Verification{Operation: "register", ID: "absent", Text: "never-present-marker", Scope: "body"}); err != nil {
					t.Fatal(err)
				}
			}
			action := &protocol.Action{Name: "opaque", Method: "POST", Target: server.URL, Body: []byte(tc.input), BodyEncoding: tc.encoding, DiscardResponseBodyOnSuccess: tc.discard, Params: map[string]any{}}
			if tc.compression != "" {
				action.Params["content_encoding"] = tc.compression
			}
			if tc.responseLimit > 0 {
				action.Params["max_body_bytes"] = tc.responseLimit
			}
			result, err := j.Execute(context.Background(), action)
			if err != nil || result == nil || result.ServerTelemetry == nil || result.ServerTelemetry.JavaHTTP == nil {
				t.Fatal("missing versioned observation", result, err)
			}
			d := result.ServerTelemetry.JavaHTTP
			if tc.lateFailure && result.Success {
				t.Fatal("verification did not fail the action")
			}
			if !d.Valid() || d.InputState != tc.state || d.RequestEncodedBytes != int64(len(tc.input)) || d.ResponseState != tc.responseState || d.ObjectFootprint != "unavailable_embedded_opaque_bytes" {
				t.Fatalf("wrong observation %+v", d)
			}
			if tc.reachesTarget {
				if calls.Load() != 1 || d.PreparedRequestBodyBytes == nil || *d.PreparedRequestBodyBytes != bodyBytes.Load() {
					t.Fatal("prepared body differs from independent endpoint", calls.Load(), d)
				}
				compression := tc.compression
				if compression == "" {
					compression = "none"
				}
				if d.AppliedRequestCompression != compression {
					t.Fatal(d.AppliedRequestCompression)
				}
			} else if calls.Load() != 0 || d.PreparedRequestBodyBytes != nil {
				t.Fatal("preflight fabricated preparation/traffic", calls.Load(), d)
			}
			if tc.state == "decoded" || tc.state == "empty" {
				if d.RequestDecodedBytes == nil || *d.RequestDecodedBytes != tc.decoded {
					t.Fatal("wrong decoded representation", d)
				}
			} else if d.RequestDecodedBytes != nil {
				t.Fatal("failed decode invented representation", d)
			}
			if tc.responseState == "complete" {
				if d.ResponseRetainedBytes == nil || *d.ResponseRetainedBytes != 3 || d.ResponseBase64Bytes == nil || *d.ResponseBase64Bytes != 4 {
					t.Fatal("wrong retained/encoded response", d)
				}
			}
			if tc.responseState == "unavailable" && (d.ResponseRetainedBytes != nil || d.ResponseBase64Bytes != nil) {
				t.Fatal("missing response became zero", d)
			}
			if tc.responseState == "discarded" && (d.ResponseRetainedBytes == nil || *d.ResponseRetainedBytes != 0) {
				t.Fatal("discarded response lost explicit state", d)
			}
			if d.Base64DecodeMs != nil && result.ServerTelemetry.SerializationMs != max(*d.Base64DecodeMs, 0.000001) {
				t.Fatal("legacy decode meaning changed")
			}
			if d.Base64EncodeMs != nil && result.ServerTelemetry.DeserializationMs != max(*d.Base64EncodeMs, 0.000001) {
				t.Fatal("legacy encode meaning changed")
			}
			if d.EnclosingMs < float64(result.Duration)/1e6 {
				t.Fatal("enclosing interval excludes wrapped HTTP", d.EnclosingMs, result.Duration)
			}
		})
	}
}

func TestJavaHTTPVersionedTimingPreservesTimerResolution(t *testing.T) {
	for _, elapsed := range []time.Duration{0, time.Nanosecond, time.Microsecond, time.Millisecond} {
		legacy, observed := base64OperationMeasurement(elapsed)
		if legacy != max(elapsed, time.Nanosecond) || observed != float64(elapsed)/float64(time.Millisecond) {
			t.Fatal(elapsed, legacy, observed)
		}
	}
}

func TestJavaHTTPSerializedRepresentationVsHTTPBodyAndContainedFootprint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	// 1. Embedded opaque-byte mode: serialized_representation_vs_http_body is computed with explicit compression & units, and ObjectFootprint stays unavailable_embedded_opaque_bytes.
	embedded := &JavaHTTP{}
	if err := embedded.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	defer embedded.Teardown(context.Background())

	rawPayload := bytes.Repeat([]byte("java-serialized-payload-"), 16)
	encodedBody := base64.StdEncoding.EncodeToString(rawPayload)
	resIdentity, err := embedded.Execute(context.Background(), &protocol.Action{
		Name:   "identity-call",
		Method: "POST",
		Target: server.URL,
		Body:   []byte(encodedBody),
	})
	if err != nil || resIdentity == nil || resIdentity.ServerTelemetry == nil || resIdentity.ServerTelemetry.JavaHTTP == nil {
		t.Fatalf("identity call failed: %v", err)
	}
	diagIdentity := resIdentity.ServerTelemetry.JavaHTTP
	if diagIdentity.ObjectFootprint != "unavailable_embedded_opaque_bytes" || diagIdentity.ContainedObjectFootprint != nil {
		t.Fatalf("expected embedded opaque-byte footprint unavailable, got %+v", diagIdentity)
	}
	cmpIdentity := diagIdentity.SerializedRepresentationVsHTTPBody()
	if !cmpIdentity.Available || cmpIdentity.Compression != "identity" || cmpIdentity.AppliedRequestCompression != "none" || cmpIdentity.BytesUnit != "bytes" || cmpIdentity.RatioUnit != "ratio" {
		t.Fatalf("unexpected identity comparison: %+v", cmpIdentity)
	}
	if cmpIdentity.DecodedRepresentationBytes == nil || *cmpIdentity.DecodedRepresentationBytes != int64(len(rawPayload)) || cmpIdentity.HTTPBodyBytes == nil || *cmpIdentity.HTTPBodyBytes != int64(len(rawPayload)) || cmpIdentity.DeltaBytes == nil || *cmpIdentity.DeltaBytes != 0 {
		t.Fatalf("unexpected identity byte comparison: %+v", cmpIdentity)
	}
	if cmpIdentity.BodyPerRepresentationRatio == nil || *cmpIdentity.BodyPerRepresentationRatio != 1.0 {
		t.Fatalf("expected ratio 1.0, got %+v", cmpIdentity.BodyPerRepresentationRatio)
	}

	resGzip, err := embedded.Execute(context.Background(), &protocol.Action{
		Name:   "gzip-call",
		Method: "POST",
		Target: server.URL,
		Body:   []byte(encodedBody),
		Params: map[string]any{"content_encoding": "gzip"},
	})
	if err != nil || resGzip == nil || resGzip.ServerTelemetry == nil || resGzip.ServerTelemetry.JavaHTTP == nil {
		t.Fatalf("gzip call failed: %v", err)
	}
	cmpGzip := resGzip.ServerTelemetry.JavaHTTP.SerializedRepresentationVsHTTPBody()
	if !cmpGzip.Available || cmpGzip.Compression != "gzip" || cmpGzip.BodyPerRepresentationRatio == nil || *cmpGzip.BodyPerRepresentationRatio >= 1.0 {
		t.Fatalf("expected compressed body < decoded representation, got %+v", cmpGzip)
	}

	resEmpty, err := embedded.Execute(context.Background(), &protocol.Action{
		Name:   "empty-call",
		Method: "POST",
		Target: server.URL,
	})
	if err != nil || resEmpty == nil || resEmpty.ServerTelemetry == nil || resEmpty.ServerTelemetry.JavaHTTP == nil {
		t.Fatalf("empty call failed: %v", err)
	}
	cmpEmpty := resEmpty.ServerTelemetry.JavaHTTP.SerializedRepresentationVsHTTPBody()
	if cmpEmpty.Available || cmpEmpty.BodyPerRepresentationRatio != nil || cmpEmpty.UnavailableReason != "zero_decoded_representation_bytes" {
		t.Fatalf("expected zero-decoded-bytes ratio to be unavailable, got %+v", cmpEmpty)
	}
	var emptySummary *protocol.JavaHTTPSummary
	protocol.MergeJavaHTTPDiagnostic(&emptySummary, resEmpty.ServerTelemetry.JavaHTTP)
	cmpEmptySummary := emptySummary.SerializedRepresentationVsHTTPBody()
	if cmpEmptySummary.Available || cmpEmptySummary.BodyPerRepresentationRatio != nil || cmpEmptySummary.UnavailableReason != "zero_or_overflow_representation_bytes" {
		t.Fatalf("expected zero-decoded-bytes summary ratio to be unavailable, got %+v", cmpEmptySummary)
	}

	// 2. Opt-in Contained JVM Object-Footprint Helper when JDK is available (using float64 config numbers as produced by json.Unmarshal).
	if _, err := javarunner.ResolveJDK(context.Background(), javarunner.ResolveOptions{}); err == nil {
		withFootprint := &JavaHTTP{}
		if err := withFootprint.Init(context.Background(), map[string]any{
			"java_http_footprint_fixture":      "approved_serializable_record",
			"java_http_footprint_array_length": float64(256),
			"java_http_footprint_chain_depth":  float64(8),
			"java_http_footprint_max_nodes":    float64(512),
			"java_http_footprint_max_depth":    float64(16),
			"java_http_footprint_run_codec":    true,
		}); err != nil {
			t.Fatalf("Init with footprint helper: %v", err)
		}
		defer withFootprint.Teardown(context.Background())

		resFp, err := withFootprint.Execute(context.Background(), &protocol.Action{
			Name:   "footprint-call",
			Method: "POST",
			Target: server.URL,
			Body:   []byte(encodedBody),
		})
		if err != nil || resFp == nil || !resFp.Success || resFp.ServerTelemetry == nil || resFp.ServerTelemetry.JavaHTTP == nil {
			t.Fatalf("Execute with footprint helper failed: %+v err=%v", resFp, err)
		}
		fp := resFp.ServerTelemetry.JavaHTTP.ContainedObjectFootprint
		if fp == nil || !fp.Valid() || fp.ReachableGraph.MaxNodes != 512 || fp.ReachableGraph.MaxDepth != 16 {
			t.Fatalf("expected valid ContainedObjectFootprint with float64-configured bounds, got %+v", fp)
		}
		if !fp.ShallowObject.Available || fp.ShallowObject.Method != protocol.JavaHTTPShallowObjectMethodV1 || fp.ShallowObject.Bytes == nil || *fp.ShallowObject.Bytes <= 0 {
			t.Fatalf("unexpected shallow footprint: %+v", fp.ShallowObject)
		}
		if !fp.ReachableGraph.Available || fp.ReachableGraph.Method != protocol.JavaHTTPReachableGraphMethodV1 || fp.ReachableGraph.Bytes == nil || *fp.ReachableGraph.Bytes <= *fp.ShallowObject.Bytes {
			t.Fatalf("unexpected reachable graph footprint: %+v", fp.ReachableGraph)
		}
		if !fp.CodecExecuted || fp.JavaSerializationMs == nil || fp.JavaDeserializationMs == nil {
			t.Fatalf("expected codec timings on approved_serializable_record, got %+v", fp)
		}
	}
}

func TestJavaHTTPControlledEndpointMatrixQualification(t *testing.T) {
	binaryReq := []byte{0xac, 0xed, 0x00, 0x05, 0xff, 0x00, 0x7f, 0x80}
	binaryResp := []byte{0xac, 0xed, 0x00, 0x05, 0xde, 0xad, 0xbe, 0xef, 0x00}
	compressible := bytes.Repeat([]byte("java-serialized-representation-block-"), 16)

	var endpointRequests atomic.Int64
	var lastWireBytes atomic.Int64
	var lastDecompressedBytes atomic.Int64
	var nextStatus atomic.Int32
	nextStatus.Store(http.StatusOK)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wire, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		endpointRequests.Add(1)
		lastWireBytes.Store(int64(len(wire)))
		decompressed := wire
		switch r.Header.Get("Content-Encoding") {
		case "gzip":
			gr, gzErr := gzip.NewReader(bytes.NewReader(wire))
			if gzErr != nil {
				t.Errorf("gzip reader: %v", gzErr)
				return
			}
			decompressed, gzErr = io.ReadAll(gr)
			_ = gr.Close()
			if gzErr != nil {
				t.Errorf("gzip read: %v", gzErr)
				return
			}
		case "deflate":
			zr, zlErr := zlib.NewReader(bytes.NewReader(wire))
			if zlErr != nil {
				t.Errorf("deflate reader: %v", zlErr)
				return
			}
			decompressed, zlErr = io.ReadAll(zr)
			_ = zr.Close()
			if zlErr != nil {
				t.Errorf("deflate read: %v", zlErr)
				return
			}
		}
		lastDecompressedBytes.Store(int64(len(decompressed)))
		w.WriteHeader(int(nextStatus.Load()))
		_, _ = w.Write(binaryResp)
	}))
	defer server.Close()

	adapter := &JavaHTTP{}
	if err := adapter.Init(context.Background(), map[string]any{"java_http_preserve_binary_result": true}); err != nil {
		t.Fatal(err)
	}
	defer adapter.Teardown(context.Background())

	// 1. Empty, invalid, and truncated base64 + zero denominators.
	for _, bad := range []string{"Y", "YWJ", "YWJj%"} {
		before := endpointRequests.Load()
		res, err := adapter.Execute(context.Background(), &protocol.Action{
			Name: "invalid-b64", Method: "POST", Target: server.URL, Body: []byte(bad),
		})
		if err != nil || res == nil || res.Success || res.BytesSent != 0 || endpointRequests.Load() != before {
			t.Fatalf("bad base64 %q reached target or succeeded: %+v err=%v", bad, res, err)
		}
		d := res.ServerTelemetry.JavaHTTP
		if d == nil || !d.Valid() || d.InputState != "decode_failed" || d.RequestDecodedBytes != nil || d.PreparedRequestBodyBytes != nil {
			t.Fatalf("unexpected diagnostics for %q: %+v", bad, d)
		}
		cmp := d.SerializedRepresentationVsHTTPBody()
		if cmp.Available || cmp.UnavailableReason != "missing_request_representation_or_http_body" {
			t.Fatalf("expected unavailable comparison for %q, got %+v", bad, cmp)
		}
	}

	// Empty base64 -> zero denominator on serialized_representation_vs_http_body and zero enclosing work share.
	resEmpty, err := adapter.Execute(context.Background(), &protocol.Action{
		Name: "empty-b64", Method: "POST", Target: server.URL,
	})
	if err != nil || resEmpty == nil || !resEmpty.Success || endpointRequests.Load() != 1 || lastWireBytes.Load() != 0 {
		t.Fatalf("empty base64 failed: %+v err=%v", resEmpty, err)
	}
	if cmp := resEmpty.ServerTelemetry.JavaHTTP.SerializedRepresentationVsHTTPBody(); cmp.Available || cmp.BodyPerRepresentationRatio != nil || cmp.UnavailableReason != "zero_decoded_representation_bytes" {
		t.Fatalf("expected zero denominator unavailability, got %+v", cmp)
	}
	zeroDur := 0.0
	zeroByte := int64(0)
	var zeroSummary *protocol.JavaHTTPSummary
	protocol.MergeJavaHTTPDiagnostic(&zeroSummary, &protocol.JavaHTTPDiagnostics{
		Version: 1, Mode: "embedded_opaque_bytes", InputState: "empty",
		RequestDecodedBytes: &zeroByte, PreparedRequestBodyBytes: &zeroByte,
		AppliedRequestCompression: "none", ResponseState: "unavailable",
		Base64DecodeMs: nil, EnclosingMs: zeroDur, ObjectFootprint: "unavailable_embedded_opaque_bytes",
	})
	if _, ok := zeroSummary.Base64Work.Share(); ok {
		t.Fatal("expected zero enclosing duration denominator to be unscored")
	}

	// 2. Binary payload preservation (request + response).
	resBin, err := adapter.Execute(context.Background(), &protocol.Action{
		Name: "binary-preserve", Method: "POST", Target: server.URL,
		Body: []byte(base64.StdEncoding.EncodeToString(binaryReq)),
	})
	if err != nil || resBin == nil || !resBin.Success || lastWireBytes.Load() != int64(len(binaryReq)) {
		t.Fatalf("binary preservation failed: %+v err=%v", resBin, err)
	}
	dBin := resBin.ServerTelemetry.JavaHTTP
	if *dBin.RequestDecodedBytes != int64(len(binaryReq)) || *dBin.PreparedRequestBodyBytes != int64(len(binaryReq)) || *dBin.ResponseRetainedBytes != int64(len(binaryResp)) || *dBin.ResponseBase64Bytes != 12 {
		t.Fatalf("unexpected binary byte counts: %+v", dBin)
	}
	if resBin.ResponseBody != base64.StdEncoding.EncodeToString(binaryResp) {
		t.Fatalf("binary response body corrupted: got %q", resBin.ResponseBody)
	}

	// 3. Identity, gzip, and deflate compression + serialized_representation_vs_http_body against independent decompressor.
	encodedCompressible := []byte(base64.StdEncoding.EncodeToString(compressible))
	for _, mode := range []struct {
		param, wantApplied, wantCompression string
		expectCompressed                    bool
	}{
		{"", "none", "identity", false},
		{"gzip", "gzip", "gzip", true},
		{"deflate", "deflate", "deflate", true},
	} {
		params := map[string]any{}
		if mode.param != "" {
			params["content_encoding"] = mode.param
		}
		resComp, err := adapter.Execute(context.Background(), &protocol.Action{
			Name: "comp-" + mode.wantCompression, Method: "POST", Target: server.URL,
			Body: encodedCompressible, Params: params,
		})
		if err != nil || resComp == nil || !resComp.Success {
			t.Fatalf("compression %s failed: %+v err=%v", mode.wantCompression, resComp, err)
		}
		if lastDecompressedBytes.Load() != int64(len(compressible)) {
			t.Fatalf("endpoint decompressed %d bytes, want %d", lastDecompressedBytes.Load(), len(compressible))
		}
		wire := lastWireBytes.Load()
		cmp := resComp.ServerTelemetry.JavaHTTP.SerializedRepresentationVsHTTPBody()
		if !cmp.Available || cmp.Compression != mode.wantCompression || cmp.AppliedRequestCompression != mode.wantApplied || cmp.HTTPBodyBytes == nil || *cmp.HTTPBodyBytes != wire || cmp.DecodedRepresentationBytes == nil || *cmp.DecodedRepresentationBytes != int64(len(compressible)) {
			t.Fatalf("compression %s comparison mismatch: %+v (wire=%d)", mode.wantCompression, cmp, wire)
		}
		if mode.expectCompressed && (*cmp.BodyPerRepresentationRatio >= 1.0 || *cmp.DeltaBytes >= 0) {
			t.Fatalf("expected %s to compress payload below 1.0 ratio, got %+v", mode.wantCompression, cmp)
		}
	}

	// 4. HTTP 4xx, HTTP 5xx, and connection failure.
	for _, status := range []int32{http.StatusNotFound, http.StatusInternalServerError} {
		nextStatus.Store(status)
		resFail, err := adapter.Execute(context.Background(), &protocol.Action{
			Name: "http-fail", Method: "POST", Target: server.URL,
			Body: []byte(base64.StdEncoding.EncodeToString(binaryReq)),
		})
		if err != nil || resFail == nil || resFail.Success || resFail.StatusCode != int(status) {
			t.Fatalf("status %d expected failure: %+v err=%v", status, resFail, err)
		}
		dFail := resFail.ServerTelemetry.JavaHTTP
		if dFail == nil || !dFail.Valid() || dFail.PreparedRequestBodyBytes == nil || *dFail.PreparedRequestBodyBytes != int64(len(binaryReq)) || dFail.ResponseState != "complete" {
			t.Fatalf("status %d lost representation evidence: %+v", status, dFail)
		}
	}
	nextStatus.Store(http.StatusOK)

	// Connection failure (closed loopback listener): 0 endpoint calls, 0 wire bytes sent, unavailable response.
	closedLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closedURL := "http://" + closedLn.Addr().String()
	_ = closedLn.Close()
	beforeCalls := endpointRequests.Load()
	resConnFail, err := adapter.Execute(context.Background(), &protocol.Action{
		Name: "conn-fail", Method: "POST", Target: closedURL,
		Body: []byte(base64.StdEncoding.EncodeToString(binaryReq)),
	})
	if err != nil || resConnFail == nil || resConnFail.Success || resConnFail.BytesSent != 0 || endpointRequests.Load() != beforeCalls {
		t.Fatalf("connection failure unexpected result: %+v err=%v", resConnFail, err)
	}
	dConnFail := resConnFail.ServerTelemetry.JavaHTTP
	if dConnFail == nil || !dConnFail.Valid() || dConnFail.ResponseState != "unavailable" || dConnFail.ResponseRetainedBytes != nil {
		t.Fatalf("connection failure invented response representation: %+v", dConnFail)
	}

	// 5. Contained JVM helper qualification across shared/cyclic graphs, traversal exhaustion, unavailable -javaagent, timeout kill, and codec exception.
	if _, err := javarunner.ResolveJDK(context.Background(), javarunner.ResolveOptions{}); err == nil {
		sharedFp, err := MeasureContainedObjectFootprint(context.Background(), javarunner.FootprintRequest{FixtureKind: "shared_diamond"})
		if err != nil || !sharedFp.Valid() || sharedFp.ReachableGraph.UniqueObjects != 5 || sharedFp.ReachableGraph.TraversalExhausted {
			t.Fatalf("shared_diamond footprint mismatch: %+v err=%v", sharedFp, err)
		}
		cyclicFp, err := MeasureContainedObjectFootprint(context.Background(), javarunner.FootprintRequest{FixtureKind: "cyclic_ring", ChainDepth: 6})
		if err != nil || !cyclicFp.Valid() || cyclicFp.ReachableGraph.UniqueObjects != 6 || cyclicFp.ReachableGraph.TraversalExhausted {
			t.Fatalf("cyclic_ring footprint mismatch: %+v err=%v", cyclicFp, err)
		}
		exhaustedFp, err := MeasureContainedObjectFootprint(context.Background(), javarunner.FootprintRequest{FixtureKind: "deep_chain", ChainDepth: 10, MaxDepth: 3, MaxNodes: 4})
		if err != nil || !exhaustedFp.Valid() || !exhaustedFp.ReachableGraph.TraversalExhausted {
			t.Fatalf("deep_chain exhaustion mismatch: %+v err=%v", exhaustedFp, err)
		}
		noAgentFp, err := MeasureContainedObjectFootprint(context.Background(), javarunner.FootprintRequest{FixtureKind: "shared_diamond", DisableJavaAgent: true})
		if err != nil || !noAgentFp.Valid() || noAgentFp.ShallowObject.Available || noAgentFp.ShallowObject.UnavailableReason != "jvm_instrumentation_agent_unavailable" || noAgentFp.ShallowObject.FieldLayoutFallbackBytes == nil {
			t.Fatalf("DisableJavaAgent footprint mismatch: %+v err=%v", noAgentFp, err)
		}
		if _, err := MeasureContainedObjectFootprint(context.Background(), javarunner.FootprintRequest{FixtureKind: "sleep_timeout", Timeout: 200 * time.Millisecond}); err == nil || !strings.Contains(err.Error(), "terminated after context/timeout cancellation") {
			t.Fatalf("expected timeout termination, got %v", err)
		}
		if _, err := MeasureContainedObjectFootprint(context.Background(), javarunner.FootprintRequest{FixtureKind: "codec_exception", RunCodec: true}); err == nil || !strings.Contains(err.Error(), "simulated approved codec serialization failure") {
			t.Fatalf("expected codec exception failure, got %v", err)
		}
	}
}
