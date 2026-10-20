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

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestJavaHTTPVersionedRepresentationObservations(t *testing.T) {
	for _, tc := range []struct {
		name, input, encoding, compression, state, responseState string
		decoded                                                  int64
		status, responseLimit                                    int
		discard, compressResponse, reachesTarget                 bool
	}{
		{name: "binary", input: "AP8BAg==", state: "decoded", decoded: 4, responseState: "complete", reachesTarget: true},
		{name: "empty", state: "empty", responseState: "complete", reachesTarget: true},
		{name: "whitespace empty representation", input: "  ", state: "decoded", responseState: "complete", reachesTarget: true},
		{name: "gzip body", input: base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("payload"), 100)), compression: "gzip", state: "decoded", decoded: 700, responseState: "complete", reachesTarget: true},
		{name: "gzip response representation", input: "YQ==", state: "decoded", decoded: 1, responseState: "complete", compressResponse: true, reachesTarget: true},
		{name: "HTTP failure retains representation", input: "YQ==", state: "decoded", decoded: 1, status: 500, responseState: "complete", reachesTarget: true},
		{name: "response cap is unavailable", input: "YQ==", state: "decoded", decoded: 1, responseLimit: 2, responseState: "unavailable", reachesTarget: true},
		{name: "discarded response is explicit", input: "YQ==", state: "decoded", decoded: 1, discard: true, responseState: "discarded", reachesTarget: true},
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
