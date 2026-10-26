package http

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestReplayHARCapturesObservedRedirectsBodiesAndTimings(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/first":
			w.Header().Set("Location", "/last")
			w.WriteHeader(307)
			_, _ = w.Write([]byte(`{"hop":1}`))
		default:
			raw, _ := io.ReadAll(r.Body)
			_, _ = w.Write(raw)
		}
	}))
	defer target.Close()
	for _, redirects := range []bool{false, true} {
		c, _ := protocol.NewReplayHARCapture(protocol.ReplayHAROptions{Bodies: "both", Redirects: redirects, Timings: true})
		h := initHTTP(t, map[string]any{"base_url": target.URL, protocol.ReplayNetworkCaptureConfigKey: true, protocol.ReplayHARCaptureConfigKey: c})
		defer h.Teardown(context.Background())
		result, err := h.Execute(context.Background(), &protocol.Action{Name: "observed", Type: "request", Method: "POST", Target: "/first", Headers: map[string]string{"Content-Type": "application/json"}, Body: []byte(`{"hello":"world"}`)})
		if err != nil || !result.Success {
			t.Fatalf("%+v %v", result, err)
		}
		want := 1
		if redirects {
			want = 2
		}
		if len(result.NetworkEntries) != want {
			t.Fatalf("entries=%+v", result.NetworkEntries)
		}
		last := result.NetworkEntries[want-1]
		if last.URL != target.URL+"/last" || last.HAR.ResponseBody == nil || last.HAR.ResponseBody.Text != `{"hello":"world"}` || last.HAR.RequestBody.Text != `{"hello":"world"}` {
			t.Fatalf("body evidence=%+v", last.HAR)
		}
		if len(last.HAR.Phases) == 0 {
			t.Fatal("missing timings")
		}
	}
}
func TestReplayHARBodyReaderNeverChangesStream(t *testing.T) {
	var body []byte
	var complete bool
	reader := &replayHARBodyReader{ReadCloser: io.NopCloser(strings.NewReader("123456789")), limit: 4, done: func(p []byte, ok bool) { body = append([]byte(nil), p...); complete = ok }}
	raw, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil || string(raw) != "123456789" || string(body) != "12345" || !complete {
		t.Fatalf("%q %q %v", raw, body, err)
	}
}

func TestReplayHARRetainsDecodedFinalBody(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		z := gzip.NewWriter(w)
		_, _ = z.Write([]byte(`{"message":"decoded response"}`))
		_ = z.Close()
	}))
	defer target.Close()
	c, _ := protocol.NewReplayHARCapture(protocol.ReplayHAROptions{Bodies: "response"})
	h := initHTTP(t, map[string]any{"base_url": target.URL, protocol.ReplayNetworkCaptureConfigKey: true, protocol.ReplayHARCaptureConfigKey: c})
	defer h.Teardown(context.Background())
	result, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Target: "/"})
	if err != nil || !result.Success || len(result.NetworkEntries) != 1 || result.NetworkEntries[0].HAR.ResponseBody.Text != `{"message":"decoded response"}` {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestReplayHARResourceCaptureUsesObservedExchange(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css")
		if r.URL.Path == "/start" {
			w.Header().Set("Location", "/sheet")
			w.WriteHeader(302)
			return
		}
		_, _ = w.Write([]byte("body { color: red }"))
	}))
	defer target.Close()
	c, _ := protocol.NewReplayHARCapture(protocol.ReplayHAROptions{Bodies: "response", Redirects: true, Timings: true})
	h := initHTTP(t, map[string]any{"base_url": target.URL, protocol.ReplayNetworkCaptureConfigKey: true, protocol.ReplayHARCaptureConfigKey: c})
	defer h.Teardown(context.Background())
	var extra []protocol.NetworkEntry
	ctx := context.WithValue(context.Background(), replayResourceHARKey{}, func(entries []protocol.NetworkEntry) { extra = append(extra, entries...) })
	entry, _, _, failed, _ := h.fetchResource(ctx, resourceRef{url: target.URL + "/start", resourceType: "stylesheet"}, target.URL, nil)
	if failed || len(extra) != 1 || entry.URL != target.URL+"/sheet" || entry.HAR == nil || entry.HAR.ResponseBody == nil || entry.HAR.ResponseBody.Text != "body { color: red }" || len(entry.HAR.Phases) == 0 {
		t.Fatalf("entry=%+v HAR=%+v extra=%+v failed=%v", entry, entry.HAR, extra, failed)
	}
}

func TestReplayHARConnectIncludesTLS(t *testing.T) {
	start, tcpEnd, tlsStart, tlsEnd := 0.0, 2.0, 2.0, 7.0
	phases := []protocol.BrowserResourcePhase{{Name: "connect", StartMS: &start, EndMS: &tcpEnd}, {Name: "tls", StartMS: &tlsStart, EndMS: &tlsEnd}}
	got := harConnectIncludesTLS(phases)
	if *got[0].EndMS != 7 || *phases[0].EndMS != 2 {
		t.Fatal("HAR connection duration excludes TLS or mutated its source")
	}
}

func TestReplayHAROmitsRefusedDispatchAndKeepsFailedConnectionAttempts(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) }))
	defer target.Close()
	capture, _ := protocol.NewReplayHARCapture(protocol.ReplayHAROptions{Timings: false})
	h := initHTTP(t, map[string]any{"base_url": target.URL, protocol.TargetPolicyConfigKey: []string{"allowed.invalid"}, protocol.ReplayNetworkCaptureConfigKey: true, protocol.ReplayHARCaptureConfigKey: capture})
	defer h.Teardown(context.Background())
	result, _ := h.Execute(context.Background(), &protocol.Action{Type: "request", Method: "GET", Target: "/"})
	if result.Success || len(result.NetworkEntries) != 0 {
		t.Fatalf("policy refusal became retained traffic: %+v", result)
	}

	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	failed := initHTTP(t, map[string]any{"base_url": closed.URL, protocol.ReplayNetworkCaptureConfigKey: true, protocol.ReplayHARCaptureConfigKey: capture})
	defer failed.Teardown(context.Background())
	result, _ = failed.Execute(context.Background(), &protocol.Action{Type: "request", Method: "GET", Target: "/", TimeoutMs: 1000})
	if result.Success || len(result.NetworkEntries) != 1 || result.NetworkEntries[0].FailureKind != "transport" {
		t.Fatalf("lost failed network attempt: %+v", result)
	}
}

func TestReplayHARRefusedRedirectKeepsLastObservedRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "http://refused.invalid/never-dispatched")
		w.WriteHeader(http.StatusFound)
	}))
	defer target.Close()
	capture, _ := protocol.NewReplayHARCapture(protocol.ReplayHAROptions{Redirects: false})
	h := initHTTP(t, map[string]any{"base_url": target.URL, protocol.TargetPolicyConfigKey: []string{"127.0.0.1"}, protocol.ReplayNetworkCaptureConfigKey: true, protocol.ReplayHARCaptureConfigKey: capture})
	defer h.Teardown(context.Background())
	result, _ := h.Execute(context.Background(), &protocol.Action{Type: "request", Method: "GET", Target: "/"})
	if result.Success || len(result.NetworkEntries) != 1 || result.NetworkEntries[0].URL != target.URL+"/" || result.NetworkEntries[0].StatusCode != http.StatusFound {
		t.Fatalf("lost actual redirect or retained authored target: %+v", result)
	}
}
