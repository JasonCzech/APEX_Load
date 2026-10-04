package recorder

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/sample"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/klauspost/compress/zstd"
)

func zstdResponseBytes(t *testing.T, body []byte) []byte {
	t.Helper()
	writer, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	return writer.EncodeAll(body, nil)
}

func TestRecorderResponseContentDirectHARRoundTrip(t *testing.T) {
	payload := []byte(`{"csrf_token":"response-token-abcdef123456"}`)
	for _, tc := range []struct {
		name      string
		values    []string
		wire      []byte
		reason    string
		limit     int
		truncated bool
	}{
		{"identity", nil, payload, "", 1024, false},
		{"gzip", []string{"gzip"}, gzipBytes(t, payload), "", 1024, false},
		{"brotli", []string{"br"}, brotliBytes(t, payload), "", 1024, false},
		{"zstd", []string{"zstd"}, zstdResponseBytes(t, payload), "", 1024, false},
		{"stacked separate occurrences", []string{"gzip", "br"}, brotliBytes(t, gzipBytes(t, payload)), "", 1024, false},
		{"malformed", []string{"gzip"}, []byte("private-wire-body-canary"), "invalid_response_content_encoding", 1024, false},
		{"unsupported", []string{"private-encoding-canary"}, []byte("private-wire-body-canary"), "unsupported_response_content_encoding", 1024, false},
		{"blank coding", []string{""}, payload, "invalid_response_content_encoding", 1024, false},
		{"capture truncated", []string{"gzip"}, gzipBytes(t, payload), "response_content_truncated", 1024, true},
		{"decoded limit", []string{"gzip"}, gzipBytes(t, []byte(strings.Repeat("x", 2000))), "response_content_decode_limit", 256, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := New(":0")
			session := &Session{ID: "content", Name: "Content", Status: "recording", StartedAt: time.Now(), BaseURL: "https://fixture.invalid", Filters: SessionFilters{BodyCaptureLimitBytes: tc.limit}}
			req, _ := http.NewRequest("GET", session.BaseURL+"/read", nil)
			resp := &http.Response{StatusCode: 200, Proto: "HTTP/1.1", Header: http.Header{"Content-Type": {"application/json"}, "Content-Length": {fmt.Sprint(len(tc.wire))}}}
			if tc.values != nil {
				resp.Header["Content-Encoding"] = tc.values
			}
			recorder.recordRequestWithOptions(session, req, nil, resp, tc.wire, time.Millisecond, recordOptions{forceResponseBodyTruncate: tc.truncated})
			got := session.Requests[0]
			if got.ResponseContentCapture.ReasonCode != tc.reason {
				t.Fatalf("capture=%+v", got.ResponseContentCapture)
			}
			if tc.reason == "" && got.ResponseBody != string(payload) {
				t.Fatal("complete logical body changed")
			}
			if tc.reason != "" && got.ResponseBody != "" {
				t.Fatal("failed capture retained a partial logical body")
			}
			if len(got.ResponseHeadersMulti["Content-Encoding"]) != len(tc.values) {
				t.Fatal("captured wire coding occurrences changed")
			}
			data, err := ExportHAR(session, false)
			if err != nil {
				t.Fatal(err)
			}
			var archive harArchive
			if err := json.Unmarshal(data, &archive); err != nil {
				t.Fatal(err)
			}
			entry := archive.Log.Entries[0]
			if tc.reason == "" && (entry.Response.Content.Text == nil || *entry.Response.Content.Text != string(payload) || entry.Response.Content.Size != len(payload) || entry.Response.BodySize != len(tc.wire)) {
				t.Fatal("HAR decoded/wire sizes or content changed")
			}
			if tc.reason != "" && (entry.Response.Content.Text != nil || entry.Response.Content.Size != -1) {
				t.Fatal("failed snapshot claimed successful HAR content")
			}
			imported, err := har.Convert(data, har.ConvertOptions{})
			if err != nil {
				t.Fatal(err)
			}
			direct := ConvertSession(session, "Content")
			for _, script := range []*dsl.Script{direct.Script, imported.Script} {
				capture := script.Actions[0].Metadata["response_content_capture"].(map[string]any)
				if tc.reason != "" && (capture["reason_code"] != tc.reason || script.Actions[0].Metadata["execution_blocker"] != "import_review_required") {
					t.Fatalf("direct/HAR failure disposition lost: %v", capture)
				}
				if tc.reason == "" && script.Actions[0].Metadata["execution_blocker"] != nil {
					t.Fatal("complete decoded response was gated")
				}
				projection, err := json.Marshal(script)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(projection), "private-wire-body-canary") || strings.Contains(string(projection), "private-encoding-canary") {
					t.Fatal("invalid capture values entered active script")
				}
			}
			baseline, err := sample.HARCorrelationBaseline(string(data), true)
			if err != nil {
				t.Fatal(err)
			}
			directBaseline := SessionBaseline(session)
			if baseline[0].ResponseBody != directBaseline[0].ResponseBody || (tc.reason == "" && (baseline[0].ResponseHeaders["Content-Encoding"] != "" || directBaseline[0].ResponseHeaders["Content-Encoding"] != "")) {
				t.Fatal("logical snapshots disagree")
			}
		})
	}
}

func TestRecorderResponseBinaryBodyAndLegacyPresence(t *testing.T) {
	resource := []byte{0x1f, 0x8b, 0x08, 0, 0xff}
	request := RecordedRequest{Seq: 1, Method: "GET", URL: "https://fixture.invalid/file", Path: "/file", StatusCode: 200, RequestHTTPVersion: "HTTP/1.1", ResponseHTTPVersion: "HTTP/1.1", ResponseBody: base64.StdEncoding.EncodeToString(resource), ResponseBodyEncoding: "base64", ResponseContentCapture: requestimport.CompleteResponseContent("wire", len(resource))}
	request.ResponseContentCapture.BodyKind = "binary"
	session := &Session{ID: "binary", Name: "Binary", BaseURL: "https://fixture.invalid", Requests: []RecordedRequest{request}}
	data, err := ExportHAR(session, false)
	if err != nil {
		t.Fatal(err)
	}
	var archive harArchive
	if err := json.Unmarshal(data, &archive); err != nil {
		t.Fatal(err)
	}
	stored := archive.Log.Entries[0].Response.Content
	decoded, err := base64.StdEncoding.DecodeString(*stored.Text)
	if err != nil || !bytes.Equal(decoded, resource) || stored.Size != len(resource) {
		t.Fatal("binary resource was not byte-exact")
	}
	baseline, err := sample.HARCorrelationBaseline(string(data), true)
	if err != nil {
		t.Fatal(err)
	}
	if baseline[0].ResponseBody != "" || SessionBaseline(session)[0].ResponseBody != "" {
		t.Fatal("binary resource became textual correlation evidence")
	}
	for _, value := range []string{"", " ", "gzip"} {
		legacy := request
		legacy.ResponseContentCapture = requestimport.ResponseContentCapture{}
		legacy.ResponseHeadersMulti = map[string][]string{"Content-Encoding": {value, value}}
		if capture := effectiveRecordedResponseContent(legacy); capture.ReasonCode != "response_content_provenance_unavailable" {
			t.Fatal("legacy coding presence falsely established decoded content")
		}
	}
}

func TestProxyPreservesResponseWireBytesAndBodylessCodings(t *testing.T) {
	allowPrivateProxyTargets(t)
	payload := []byte(`{"result":"logical"}`)
	wire := brotliBytes(t, gzipBytes(t, payload))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Accept-Encoding") != "" {
			t.Error("proxy invented Accept-Encoding")
		}
		w.Header().Add("Content-Encoding", "gzip")
		w.Header().Add("Content-Encoding", "br")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", fmt.Sprint(len(wire)))
		w.Write(wire)
	}))
	defer upstream.Close()
	recorder := New(":0")
	recorder.StartSession("wire", "Wire", SessionFilters{}, nil)
	response := httptest.NewRecorder()
	recorder.handleProxy(response, httptest.NewRequest("GET", upstream.URL+"/read", nil))
	if !bytes.Equal(response.Body.Bytes(), wire) || len(response.Header().Values("Content-Encoding")) != 2 {
		t.Fatal("proxy altered response wire bytes/headers")
	}
	session, _ := recorder.GetSession("wire")
	if len(session.Requests) != 1 || session.Requests[0].ResponseBody != string(payload) || !session.Requests[0].ResponseContentCapture.Complete() {
		t.Fatal("proxy did not separately capture decoded logical content")
	}
	for _, tc := range []struct {
		method string
		status int
	}{{"HEAD", 200}, {"GET", 204}, {"GET", 304}} {
		body, capture := decodeRecordedResponse(context.Background(), tc.method, &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Encoding": {"unsupported"}}}, nil, 1024, false)
		if len(body) != 0 || !capture.Complete() {
			t.Fatal("legal bodyless response falsely required decoding")
		}
	}
}

func TestLegacyBodylessRecorderContentCannotBecomeCorrelationOrHARSuccess(t *testing.T) {
	for _, tc := range []struct {
		method string
		status int
	}{{"HEAD", 200}, {"GET", 204}, {"GET", 304}} {
		req := RecordedRequest{Seq: 1, Method: tc.method, URL: "https://fixture.invalid/bodyless", Path: "/bodyless", StatusCode: tc.status, RequestHTTPVersion: "HTTP/1.1", ResponseHTTPVersion: "HTTP/1.1", ResponseBody: "private-bodyless-canary"}
		session := &Session{ID: "bodyless", Name: "Bodyless", BaseURL: "https://fixture.invalid", Requests: []RecordedRequest{req}}
		if SessionBaseline(session)[0].ResponseBody != "" {
			t.Fatal("contradictory legacy content became text correlation")
		}
		direct := ConvertSession(session, "Bodyless").Script.Actions[0]
		if direct.Metadata["execution_blocker"] != "import_review_required" {
			t.Fatal("legacy contradiction lost its source gate")
		}
		data, err := ExportHAR(session, false)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "private-bodyless-canary") {
			t.Fatal("bodyless HAR falsely retained logical content")
		}
		imported, err := har.Convert(data, har.ConvertOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if imported.Script.Actions[0].Metadata["execution_blocker"] != "import_review_required" {
			t.Fatal("HAR round-trip lost the contradiction gate")
		}
	}
}

func TestRecorderSupportedLargeLogicalBodySurvivesHARStorageAdmission(t *testing.T) {
	// This exceeds the historical HAR decoder's 10MiB cap while remaining a
	// supported recorder capture. Both paths must admit the same logical body.
	payload := []byte(strings.Repeat("x", 11<<20))
	recorder := New(":0")
	session := &Session{ID: "large", Name: "Large", Status: "recording", BaseURL: "https://fixture.invalid", Filters: SessionFilters{BodyCaptureLimitBytes: maxBodyCaptureLimitBytes}}
	req, _ := http.NewRequest("GET", session.BaseURL+"/large", nil)
	resp := &http.Response{StatusCode: 200, Proto: "HTTP/1.1", Header: http.Header{"Content-Type": {"text/plain"}}}
	recorder.recordRequest(session, req, nil, resp, payload, time.Millisecond)
	if !session.Requests[0].ResponseContentCapture.Complete() {
		t.Fatal("supported recorder body was rejected")
	}
	data, err := ExportHAR(session, false)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := sample.HARCorrelationBaseline(string(data), true)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseline) != 1 || len(baseline[0].ResponseBody) != len(payload) || baseline[0].ResponseBody != string(payload) {
		t.Fatal("supported recording failed HAR storage reimport")
	}
	inferExtractors := false
	imported, err := har.Convert(data, har.ConvertOptions{InferExtractors: &inferExtractors})
	if err != nil {
		t.Fatal(err)
	}
	imported.ProtectedRequirements.Take()
	direct := ConvertSession(session, "Large")
	for _, script := range []*dsl.Script{direct.Script, imported.Script} {
		capture := script.Actions[0].Metadata["response_content_capture"].(map[string]any)
		if capture["state"] != "complete" || script.Actions[0].Metadata[requestimport.ImportResponseContentFidelityMetadataKey] != nil {
			t.Fatal("supported direct/HAR import lost complete logical capture")
		}
	}
}

func TestRecorderResponseControlBytesRoundTripAsBinaryWithoutChangingRequests(t *testing.T) {
	resource := []byte{'a', 0, 'b', 0x7f}
	recorder := New(":0")
	session := &Session{ID: "control", Name: "Control", Status: "recording", BaseURL: "https://fixture.invalid"}
	req, _ := http.NewRequest("POST", session.BaseURL+"/file", nil)
	resp := &http.Response{StatusCode: 200, Proto: "HTTP/1.1", Header: http.Header{"Content-Type": {"application/octet-stream"}}}
	recorder.recordRequest(session, req, resource, resp, resource, time.Millisecond)
	stored := session.Requests[0]
	decoded, err := base64.StdEncoding.DecodeString(stored.ResponseBody)
	if err != nil || !bytes.Equal(decoded, resource) || stored.ResponseBodyEncoding != "base64" || stored.ResponseContentCapture.BodyKind != "binary" || !stored.ResponseContentCapture.Complete() {
		t.Fatal("complete control-byte resource lost exact binary storage")
	}
	if stored.Body != string(resource) || stored.BodyEncoding != "" {
		t.Fatal("response text admission changed request-body capture behavior")
	}
	data, err := ExportHAR(session, false)
	if err != nil {
		t.Fatal(err)
	}
	var archive harArchive
	if err := json.Unmarshal(data, &archive); err != nil {
		t.Fatal(err)
	}
	decoded, err = base64.StdEncoding.DecodeString(*archive.Log.Entries[0].Response.Content.Text)
	if err != nil || !bytes.Equal(decoded, resource) || archive.Log.Entries[0].Response.Content.Size != len(resource) {
		t.Fatal("HAR changed control-byte source storage")
	}
	imported, err := har.Convert(data, har.ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	imported.ProtectedRequirements.Take()
	for _, script := range []*dsl.Script{ConvertSession(session, "Control").Script, imported.Script} {
		capture := script.Actions[0].Metadata["response_content_capture"].(map[string]any)
		if capture["body_kind"] != "binary" || capture["state"] != "complete" || script.Actions[0].Metadata[requestimport.ImportResponseContentFidelityMetadataKey] != nil {
			t.Fatal("direct/HAR binary disposition diverged")
		}
	}
	baseline, err := sample.HARCorrelationBaseline(string(data), true)
	if err != nil || baseline[0].ResponseBody != "" || SessionBaseline(session)[0].ResponseBody != "" {
		t.Fatal("control-byte binary resource entered application-text correlation")
	}
}
