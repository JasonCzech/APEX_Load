package java_http

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
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
