package http

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// The HTTP adapter's byte counts are what http_body.v1 says (NA-C): body
// bytes on the wire, a compressed body counted compressed; a response served
// from the cache (fresh, or after a 304) receives nothing; a request that
// never wrote its headers sends nothing; and a step that dispatched no
// request is not a request.
func TestHTTPByteEvidenceMeasuresWireBodies(t *testing.T) {
	payload := []byte(strings.Repeat("measured body ", 512))
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write(payload)
	_ = writer.Close()
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		switch r.URL.Path {
		case "/gzip":
			w.Header().Set("Content-Encoding", "gzip")
			w.Header().Set("Content-Length", strconv.Itoa(compressed.Len()))
			w.Header().Set("Cache-Control", "max-age=3600")
			_, _ = w.Write(compressed.Bytes())
		case "/etag":
			if r.Header.Get("If-None-Match") == `"v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"v1"`)
			w.Header().Set("Cache-Control", "no-cache")
			_, _ = w.Write([]byte("revalidated body"))
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer server.Close()
	h := initHTTP(t, map[string]any{"http_cache_enabled": true})
	run := func(action *protocol.Action) *protocol.Result {
		t.Helper()
		result, err := h.Execute(context.Background(), action)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	want := func(name string, result *protocol.Result, evidence string, sent, received int64) {
		t.Helper()
		if result.ByteEvidence != evidence || result.BytesSent != sent || result.BytesReceived != received {
			t.Fatalf("%s: evidence %q sent %d received %d, want %q %d %d", name, result.ByteEvidence, result.BytesSent, result.BytesReceived, evidence, sent, received)
		}
	}
	want("gzip from the wire", run(&protocol.Action{Type: "GET", Target: server.URL + "/gzip"}), protocol.ByteEvidenceHTTPBody, 0, int64(compressed.Len()))
	before := hits.Load()
	fresh := run(&protocol.Action{Type: "GET", Target: server.URL + "/gzip"})
	if hits.Load() != before || fresh.ResponseBody != string(payload) {
		t.Fatal("the second request was not a fresh cache hit")
	}
	want("fresh cache hit", fresh, protocol.ByteEvidenceHTTPBody, 0, 0)
	want("first etag response", run(&protocol.Action{Type: "GET", Target: server.URL + "/etag"}), protocol.ByteEvidenceHTTPBody, 0, int64(len("revalidated body")))
	revalidated := run(&protocol.Action{Type: "GET", Target: server.URL + "/etag"})
	if revalidated.ResponseBody != "revalidated body" {
		t.Fatalf("revalidated body %q", revalidated.ResponseBody)
	}
	want("served after a 304", revalidated, protocol.ByteEvidenceHTTPBody, 0, 0)
	want("posted body", run(&protocol.Action{Type: "POST", Target: server.URL + "/post", Body: []byte("eleven byte")}), protocol.ByteEvidenceHTTPBody, 11, 2)
	// A refused dial writes no headers, so its body was never sent.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refused := "http://" + listener.Addr().String() + "/post"
	_ = listener.Close()
	if result := run(&protocol.Action{Type: "POST", Target: refused, Body: []byte("eleven byte")}); result.Success {
		t.Fatal("the refused dial succeeded")
	} else {
		want("refused dial", result, protocol.ByteEvidenceHTTPBody, 0, 0)
	}
	// An action refused before dispatch is no request.
	if result := run(&protocol.Action{Type: "GET", Target: "http://[::1"}); result.Success {
		t.Fatal("a malformed target succeeded")
	} else {
		want("refused before dispatch", result, protocol.ByteEvidenceNone, 0, 0)
	}
}

// A meta-refresh follow-up refused before sending keeps the refreshing
// page's measured bytes measurements; a dispatched follow-up keeps its own
// label, and both pages' bytes count.
func TestMetaRefreshKeepsThePagesByteEvidence(t *testing.T) {
	page := &protocol.Result{ByteEvidence: protocol.ByteEvidenceHTTPBody, BytesSent: 10, BytesReceived: 100}
	refused := &protocol.Result{ByteEvidence: protocol.ByteEvidenceNone}
	mergeMetaRefreshPage(refused, page)
	if refused.ByteEvidence != protocol.ByteEvidenceHTTPBody || refused.BytesSent != 10 || refused.BytesReceived != 100 {
		t.Fatalf("refused follow-up %+v", refused)
	}
	followed := &protocol.Result{ByteEvidence: protocol.ByteEvidenceHTTPBody, BytesReceived: 5}
	mergeMetaRefreshPage(followed, page)
	if followed.ByteEvidence != protocol.ByteEvidenceHTTPBody || followed.BytesReceived != 105 {
		t.Fatalf("followed page %+v", followed)
	}
}
