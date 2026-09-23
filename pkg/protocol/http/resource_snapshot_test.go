package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestResourceCaptureDoesNotChangeTrafficOrOutcomes(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, `<img src="/resource?token=sensitive"><script src="/missing"></script>`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Set-Cookie", "session=secret")
		w.Header().Set("Cache-Control", "max-age=60")
		if r.URL.Path == "/missing" {
			w.WriteHeader(404)
		}
		fmt.Fprint(w, `{"token":"sensitive","value":"fixture"}`)
	}))
	defer server.Close()
	for _, failResources := range []bool{false, true} {
		var baseline *protocol.Result
		for _, capture := range []bool{false, true} {
			h := initHTTP(t, map[string]any{"http_download_non_html_resources": true, "http_cache_enabled": true, "fail_noncritical_resources": failResources})
			before := hits.Load()
			a := &protocol.Action{Type: "GET", Target: server.URL, Method: "GET", CaptureMessageDetail: capture, CaptureResourceDetails: capture}
			r, err := h.Execute(context.Background(), a)
			if err != nil {
				t.Fatal(err)
			}
			if hits.Load()-before != 3 {
				t.Fatal("capture changed request count")
			}
			if !capture {
				baseline = r
				continue
			}
			if r.Success != baseline.Success || r.BytesReceived != baseline.BytesReceived || evidenceInt(t, r, "resource_requests") != 2 {
				t.Fatal("capture changed replay outcome")
			}
			if r.Snapshot == nil || len(r.Snapshot.Resources) != 2 {
				t.Fatalf("resource evidence: %+v", r.Snapshot)
			}
			raw, _ := json.Marshal(r.Snapshot.Resources)
			if strings.Contains(string(raw), "sensitive") || strings.Contains(string(raw), "session=secret") {
				t.Fatalf("resource secret leaked: %s", raw)
			}
			before = hits.Load()
			r, err = h.Execute(context.Background(), a)
			if err != nil {
				t.Fatal(err)
			}
			if hits.Load()-before != 2 {
				t.Fatalf("cache behavior changed: %d", hits.Load()-before)
			}
			found := false
			for _, resource := range r.Snapshot.Resources {
				found = found || resource.CacheHit
			}
			if !found {
				t.Fatal("cached resource evidence missing")
			}
		}
	}
}

func TestResourceCaptureLimitsAndCancellation(t *testing.T) {
	c := newResourceCapture(1024)
	defer c.lease.release()
	for i := 0; i < 10; i++ {
		c.record(protocol.NetworkEntry{URL: "https://example.test/resource", StatusCode: 200}, nil, nil, []byte(strings.Repeat("x", 10000)), false, true)
	}
	s := &protocol.ErrorSnapshot{}
	c.attach(s)
	if s.OmittedResources == 0 {
		t.Fatal("budget exhaustion invisible")
	}
	big := newResourceCapture(1 << 20)
	defer big.lease.release()
	big.record(protocol.NetworkEntry{URL: "https://example.test", StatusCode: 200}, nil, nil, []byte(strings.Repeat("secret", 10000)), false, true)
	s = &protocol.ErrorSnapshot{}
	big.attach(s)
	if len(s.Resources) != 1 || !s.Resources[0].Truncated || s.Resources[0].ResponseBody != "" {
		t.Fatal("oversized body retained a raw prefix")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c2 := newResourceCapture(1 << 20)
	defer c2.lease.release()
	h := initHTTP(t, map[string]any{})
	_, _, _, failed, _ := h.fetchResource(context.WithValue(ctx, resourceCaptureKey{}, c2), resourceRef{url: "https://example.invalid/resource"}, "https://example.invalid", nil)
	if !failed || len(c2.entries) != 1 || c2.entries[0].FailureKind != "canceled" {
		t.Fatalf("cancellation: %+v", c2.entries)
	}
}

func TestResourcesCaptureRequiresExistingDownloads(t *testing.T) {
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<img src="/resource">`)
	}))
	defer s.Close()
	h := initHTTP(t, map[string]any{})
	r, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: s.URL, CaptureMessageDetail: true, CaptureResourceDetails: true})
	if err != nil || hits.Load() != 1 || len(r.Snapshot.Resources) != 0 {
		t.Fatal("capture initiated a resource request")
	}
}

func BenchmarkResourceSnapshotCapture(b *testing.B) {
	body := []byte(`{"message":"fixture response"}`)
	for _, enabled := range []bool{false, true} {
		b.Run(fmt.Sprint(enabled), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var c *resourceCapture
				if enabled {
					c = newResourceCapture(1 << 20)
				}
				c.record(protocol.NetworkEntry{URL: "https://example.test/resource", StatusCode: 200}, nil, nil, body, false, true)
				if c != nil {
					c.lease.release()
				}
			}
		})
	}
}

func TestResourceCaptureOmitsBinaryAndMarksHeaderLimits(t *testing.T) {
	c := newResourceCapture(1 << 20)
	defer c.lease.release()
	c.record(protocol.NetworkEntry{URL: "https://example.test/binary", StatusCode: 200}, http.Header{"X-Large": {strings.Repeat("x", 4097)}}, http.Header{"Content-Type": {"application/octet-stream"}}, []byte("ascii binary"), false, true)
	s := &protocol.ErrorSnapshot{}
	c.attach(s)
	if len(s.Resources) != 1 || s.Resources[0].ResponseBody != "" || !s.Resources[0].Truncated || s.Resources[0].RequestHeaders["X-Large"] != "[not retained: header limit]" {
		t.Fatalf("missing omission evidence: %+v", s)
	}
}

func TestResourceEvidenceExhaustionCannotFailResourceFetch(t *testing.T) {
	c := newResourceCapture(1 << 20)
	if c.lease.budget == &processHTTPResponseCaptureBudget {
		t.Fatal("optional evidence competes with required response reads")
	}
	c.lease = (&responseCaptureBudget{limit: 1}).lease()
	defer c.lease.release()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "fixture") }))
	defer server.Close()
	h := initHTTP(t, map[string]any{})
	_, _, _, failed, _ := h.fetchResource(context.WithValue(context.Background(), resourceCaptureKey{}, c), resourceRef{url: server.URL}, server.URL, nil)
	if failed || c.omitted != 1 || len(c.entries) != 0 {
		t.Fatalf("exhaustion changed fetch: failed=%v omitted=%d", failed, c.omitted)
	}
}
