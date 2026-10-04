package http

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"hash/adler32"
	stdhttp "net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

var combinedReviewHTTPRawDeflate = []byte{0x78, 0x02, 0x00, 0xfd, 0xff, 'a', 'b', 0x01, 0x00, 0x00, 0xff, 0xff}

func combinedReviewHTTPWrappedDeflate(t *testing.T) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zlib.NewWriter(&output)
	if _, err := writer.Write([]byte("ab")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

// The corrupt Adler-32 is part of the raw interpretation's stored payload.
// Retrying the wrapped failure as raw flate would therefore succeed incorrectly.
func combinedReviewHTTPCorruptWrappedDeflate() []byte {
	wire := make([]byte, 65448)
	copy(wire, []byte{0x78, 0x5e, 0x00, 0xa1, 0xff, 0x5e, 0x00})
	wire[99] = 0x01
	binary.LittleEndian.PutUint16(wire[100:102], 65353)
	binary.LittleEndian.PutUint16(wire[102:104], ^uint16(65353))
	wire = append(wire, 0x01, 0x00, 0x00, 0xff, 0xff)
	wire = binary.BigEndian.AppendUint32(wire, adler32.Checksum(wire[7:65448]))
	wire[len(wire)-1] ^= 1
	return wire
}

func TestHTTPDeflateCombinedReviewWireCaptureCacheAndPolicy(t *testing.T) {
	wrapped := combinedReviewHTTPWrappedDeflate(t)
	badHeader := bytes.Clone(wrapped)
	badHeader[1] ^= 1
	for _, tc := range []struct {
		name  string
		wire  []byte
		valid bool
	}{
		{"raw_with_zlib_CMF", combinedReviewHTTPRawDeflate, true},
		{"wrapped", wrapped, true},
		{"invalid_wrapped_header", badHeader, false},
		{"wrapped_checksum_with_valid_raw_interpretation", combinedReviewHTTPCorruptWrappedDeflate(), false},
	} {
		for _, policy := range []string{"1.1", "2"} {
			t.Run(tc.name+"/"+policy, func(t *testing.T) {
				var calls atomic.Int64
				server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
					calls.Add(1)
					w.Header().Set("Content-Encoding", "deflate")
					w.Header().Set("Content-Type", "text/plain")
					w.Header().Set("Content-Length", strconv.Itoa(len(tc.wire)))
					w.Header().Set("Cache-Control", "max-age=3600")
					_, _ = w.Write(tc.wire)
				}))
				h := versionHTTP(t, map[string]any{"http_version": policy, "http_cache_enabled": true, "receive_buffer_bytes": 1, protocol.CorrelationEvidenceCaptureConfigKey: true})
				for pass := range 2 {
					result, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL, CaptureMessageDetail: true, Extractors: []protocol.Extractor{{Name: "observed", Source: "body", Type: "regex", Expression: `^(ab)$`}}})
					if err != nil || result == nil {
						t.Fatalf("request returned no result: %v", err)
					}
					if !tc.valid {
						if result.Success || result.ResponseBody != "" || len(result.Extractions) != 0 || result.Snapshot == nil || result.Snapshot.ResponseBody != "" || result.Snapshot.ResponseBodyState != "not_captured" {
							t.Fatal("corrupt wrapped content published a complete or partial representation")
						}
						if result.CorrelationEvidence == nil || result.CorrelationEvidence.UnavailableReason == "" || result.CorrelationEvidence.ResponseBody != "" || result.ProtocolEvidence["response_content_state"] != "unavailable" {
							t.Fatal("corrupt wrapped content retained raw correlation or claimed completion")
						}
						continue
					}
					if !result.Success || result.ResponseBody != "ab" || result.Extractions["observed"] != "ab" || result.Snapshot == nil || result.Snapshot.ResponseBody != "ab" || result.Snapshot.ResponseBodyState != "captured" {
						t.Fatalf("valid wire deflate failed to reach capture/extraction: %s", result.ErrorMessage)
					}
					if result.Snapshot.ResponseHeaders["Content-Encoding"] != "" || result.Snapshot.ResponseHeaders["Content-Length"] != "" || result.ProtocolEvidence["response_content_state"] != "complete" || result.HTTPVersionPolicy != policy {
						t.Fatal("decoded snapshot or receipt lost representation/policy semantics")
					}
					if pass == 0 {
						if result.BytesReceived != int64(len(tc.wire)) || result.HTTPVersionNegotiated != policy || result.ProtocolEvidence["response_content_basis"] != "decoded" || result.CorrelationEvidence == nil || result.CorrelationEvidence.ResponseBody != "ab" || result.CorrelationEvidence.ResponseHeaders["Content-Encoding"] != "deflate" {
							t.Fatal("wire deflate lost byte, version, or raw-header receipts")
						}
					} else if result.HTTPVersionSource != "cache" || result.HTTPVersionNegotiated != "" || result.ProtocolEvidence["response_content_source"] != "cache" {
						t.Fatal("decoded cache was fetched/redecoded or invented a wire negotiation")
					}
				}
				wantCalls := int64(2)
				if tc.valid {
					wantCalls = 1
				}
				if calls.Load() != wantCalls {
					t.Fatalf("deflate cache admission calls=%d, want %d", calls.Load(), wantCalls)
				}
			})
		}
	}
}

func TestHTTPDeflateCombinedReviewResourceCaptureAndCache(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		name, wire := "valid_raw", combinedReviewHTTPRawDeflate
		if corrupt {
			name, wire = "corrupt_wrapped", combinedReviewHTTPCorruptWrappedDeflate()
		}
		t.Run(name, func(t *testing.T) {
			var resourceCalls atomic.Int64
			server := versionTLS(t, stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
				if r.URL.Path == "/resource" {
					resourceCalls.Add(1)
					w.Header().Set("Content-Encoding", "deflate")
					w.Header().Set("Content-Type", "text/plain")
					w.Header().Set("Cache-Control", "max-age=3600")
					_, _ = w.Write(wire)
					return
				}
				_, _ = w.Write([]byte("page"))
			}))
			h := versionHTTP(t, map[string]any{"http_version": "2", "http_cache_enabled": true})
			for pass := range 2 {
				result, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: server.URL, CaptureMessageDetail: true, CaptureResourceDetails: true, Params: map[string]any{"resources": []ExplicitResource{{URL: "/resource"}}}})
				if err != nil || result == nil || result.Snapshot == nil || len(result.Snapshot.Resources) != 1 {
					t.Fatalf("resource request/capture missing: %v", err)
				}
				resource := result.Snapshot.Resources[0]
				if corrupt {
					if resource.FailureKind != "content_decode" || resource.ResponseBody != "" || resource.ResponseBodyState != "not_captured" || result.ProtocolEvidence["resource_failures"] != 1 {
						t.Fatal("corrupt wrapped resource accepted a raw fallback or retained partial content")
					}
				} else if resource.FailureKind != "" || resource.ResponseBody != "ab" || resource.ResponseBodyState != "captured" || resource.CacheHit != (pass == 1) || result.ProtocolEvidence["resource_failures"] != 0 {
					t.Fatal("valid raw-deflate resource lost capture or cache admission")
				}
			}
			wantCalls := int64(1)
			if corrupt {
				wantCalls = 2
			}
			if resourceCalls.Load() != wantCalls {
				t.Fatalf("resource cache admission calls=%d, want %d", resourceCalls.Load(), wantCalls)
			}
		})
	}
}
