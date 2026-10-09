package recorder

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	gorillawebsocket "github.com/gorilla/websocket"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type recorderErrorReader struct {
	err error
}

func (r recorderErrorReader) Read([]byte) (int, error) { return 0, r.err }

type recorderErrorWriter struct {
	err error
}

func (w recorderErrorWriter) Write(value []byte) (int, error) { return len(value), w.err }

type recorderRoundTripFunc func(*http.Request) (*http.Response, error)

func (f recorderRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNewRecorder(t *testing.T) {
	r := New(":9999")
	if r.addr != ":9999" {
		t.Fatalf("expected :9999, got %s", r.addr)
	}

	r2 := New("")
	if r2.addr != defaultProxyAddr {
		t.Fatalf("expected default %s, got %s", defaultProxyAddr, r2.addr)
	}
	transport, ok := r.proxyClient.Transport.(*http.Transport)
	if !ok || transport.MaxResponseHeaderBytes != maxRecorderHeaderBytes {
		t.Fatalf("upstream response header limit = %#v, want %d", transport, maxRecorderHeaderBytes)
	}
}

func TestCopyAndCaptureBodyMarksPartialReadErrorTruncated(t *testing.T) {
	wantErr := errors.New("upstream body interrupted")
	src := io.MultiReader(strings.NewReader("partial"), recorderErrorReader{err: wantErr})
	var dst bytes.Buffer

	captured, wireSize, truncated, err := copyAndCaptureBody(&dst, src, 1024)
	if !errors.Is(err, wantErr) {
		t.Fatalf("copy error = %v, want %v", err, wantErr)
	}
	if !truncated {
		t.Fatal("partial response terminated by a read error was not marked truncated")
	}
	if string(captured) != "partial" || dst.String() != "partial" || wireSize != int64(len("partial")) {
		t.Fatalf("partial copy = captured %q, dst %q, wire size %d", captured, dst.String(), wireSize)
	}
}

func TestCopyAndCaptureBodyMarksWriteErrorTruncated(t *testing.T) {
	wantErr := errors.New("downstream write interrupted")

	captured, wireSize, truncated, err := copyAndCaptureBody(
		recorderErrorWriter{err: wantErr},
		strings.NewReader("partial"),
		1024,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("copy error = %v, want %v", err, wantErr)
	}
	if !truncated {
		t.Fatal("response terminated by a write error was not marked truncated")
	}
	if string(captured) != "partial" || wireSize != int64(len("partial")) {
		t.Fatalf("partial copy = captured %q, wire size %d", captured, wireSize)
	}
}

func TestHandleProxyBoundsStalledNonStreamingResponseBody(t *testing.T) {
	var body *contextOnlyStalledRecorderBody
	rec := New(":0")
	rec.proxyAuthToken = ""
	rec.requireProxyAuth = false
	rec.upstreamBodyInactivityTimeout = 20 * time.Millisecond
	rec.proxyClient = &http.Client{Transport: recorderRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		body = &contextOnlyStalledRecorderBody{ctx: req.Context(), closed: make(chan struct{})}
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Length": []string{"8"}},
			Body:          body,
			ContentLength: 8,
			Request:       req,
		}, nil
	})}

	done := make(chan struct{})
	go func() {
		rec.handleProxy(
			httptest.NewRecorder(),
			httptest.NewRequest(http.MethodGet, "http://origin.example.test/stalled", nil),
		)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ordinary HTTP proxy did not bound a stalled response body")
	}
	if body == nil {
		t.Fatal("ordinary HTTP proxy did not create an upstream response body")
	}
	select {
	case <-body.closed:
	default:
		t.Fatal("ordinary HTTP proxy did not close the stalled upstream body")
	}
	if slots := len(rec.proxySlots); slots != 0 {
		t.Fatalf("ordinary HTTP proxy retained %d request slot(s) after timeout", slots)
	}
}

func TestStartProxyBoundsInboundHeaders(t *testing.T) {
	rec := New("127.0.0.1:0")
	if err := rec.StartProxy(); err != nil {
		t.Fatal(err)
	}
	defer rec.StopProxy()
	rec.mu.RLock()
	server := rec.proxy
	rec.mu.RUnlock()
	if server == nil || server.MaxHeaderBytes != maxRecorderHeaderBytes {
		t.Fatalf("proxy MaxHeaderBytes = %#v, want %d", server, maxRecorderHeaderBytes)
	}
}

func allowPrivateProxyTargets(t *testing.T) {
	t.Helper()
	t.Setenv(envProxyAllowPrivateTargets, "true")
}

func TestProxyStartupHardening(t *testing.T) {
	t.Run("production requires explicit opt-in", func(t *testing.T) {
		t.Setenv("APEXLOAD_ENV", "production")
		rec := New(defaultProxyAddr)
		if err := rec.validateProxyStartup(); err == nil || !strings.Contains(err.Error(), envProxyAllowProduction) {
			t.Fatalf("validateProxyStartup error = %v, want production opt-in error", err)
		}
	})

	t.Run("wide bind requires auth or explicit unauthenticated opt-in", func(t *testing.T) {
		t.Setenv("APEXLOAD_ENV", "")
		rec := New(":8081")
		if err := rec.validateProxyStartup(); err == nil || !strings.Contains(err.Error(), envProxyAuthToken) {
			t.Fatalf("validateProxyStartup error = %v, want token requirement", err)
		}
	})

	t.Run("wide bind allows configured token", func(t *testing.T) {
		t.Setenv(envProxyAuthToken, "secret-token")
		rec := New(":8081")
		if err := rec.validateProxyStartup(); err != nil {
			t.Fatalf("validateProxyStartup with token: %v", err)
		}
	})

	t.Run("production rejects insecure upstream TLS", func(t *testing.T) {
		t.Setenv("APEXLOAD_ENV", "production")
		t.Setenv(envProxyAllowProduction, "true")
		t.Setenv(envRecorderTLSInsecure, "true")
		rec := New(defaultProxyAddr)
		if err := rec.validateProxyStartup(); err == nil || !strings.Contains(err.Error(), envRecorderTLSInsecure) {
			t.Fatalf("validateProxyStartup error = %v, want development-only TLS error", err)
		}
	})
}

func TestStartSessionRejectsExcessiveBodyCaptureLimit(t *testing.T) {
	rec := New(defaultProxyAddr)
	if _, err := rec.StartSession("project", "large", SessionFilters{BodyCaptureLimitBytes: maxBodyCaptureLimitBytes + 1}, nil); err == nil {
		t.Fatal("StartSession accepted an excessive body capture limit")
	}
}

func TestStartSessionBoundsRetainedInputs(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		session   string
		projectID string
		filters   SessionFilters
	}{
		{name: "id", id: strings.Repeat("i", maxRecorderSessionIDBytes+1)},
		{name: "name", id: "session", session: strings.Repeat("n", maxRecorderSessionNameBytes+1)},
		{name: "project", id: "session", projectID: strings.Repeat("p", maxRecorderProjectIDBytes+1)},
		{name: "filter entries", id: "session", filters: SessionFilters{ExcludeHosts: make([]string, maxRecorderFilterEntries+1)}},
		{name: "filter value", id: "session", filters: SessionFilters{IncludeHosts: []string{strings.Repeat("h", maxRecorderFilterValueBytes+1)}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := New(defaultProxyAddr)
			if _, err := rec.StartSessionForProject(tc.id, tc.session, tc.projectID, tc.filters, nil); err == nil {
				t.Fatal("StartSessionForProject accepted an oversized retained input")
			}
		})
	}
}

func TestStartSessionEvictsOldestStoppedSessionAtCountLimit(t *testing.T) {
	rec := New(defaultProxyAddr)
	base := time.Now().Add(-time.Hour)
	for index := 0; index < maxRetainedRecorderSessions; index++ {
		stopped := base.Add(time.Duration(index) * time.Second)
		id := fmt.Sprintf("old-%02d", index)
		rec.sessions[id] = &Session{
			ID:            id,
			Status:        "stopped",
			StartedAt:     stopped,
			StoppedAt:     &stopped,
			retainedBytes: 1024,
		}
	}

	if _, err := rec.StartSession("new", "new", SessionFilters{}, nil); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, ok := rec.sessions["old-00"]; ok {
		t.Fatal("oldest stopped session was not evicted")
	}
	if len(rec.sessions) != maxRetainedRecorderSessions {
		t.Fatalf("retained sessions = %d, want %d", len(rec.sessions), maxRetainedRecorderSessions)
	}
}

func TestStartSessionEvictsStoppedSessionsToReserveByteBudget(t *testing.T) {
	rec := New(defaultProxyAddr)
	base := time.Now().Add(-time.Hour)
	for index := 0; index < 2; index++ {
		stopped := base.Add(time.Duration(index) * time.Second)
		id := fmt.Sprintf("old-%d", index)
		rec.sessions[id] = &Session{
			ID:            id,
			Status:        "stopped",
			StartedAt:     stopped,
			StoppedAt:     &stopped,
			retainedBytes: 300 << 20,
		}
	}

	if _, err := rec.StartSession("new", "new", SessionFilters{}, nil); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if _, ok := rec.sessions["old-0"]; ok {
		t.Fatal("oldest stopped session was not evicted to reserve the byte budget")
	}
	if _, ok := rec.sessions["old-1"]; !ok {
		t.Fatal("newer stopped session was unnecessarily evicted")
	}
}

func TestRecorderUpstreamTLSVerificationDefaultsSecure(t *testing.T) {
	allowPrivateProxyTargets(t)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()

	secure := New(defaultProxyAddr)
	if _, err := secure.proxyClient.Get(server.URL); err == nil {
		t.Fatal("self-signed upstream certificate was accepted by default")
	}

	t.Setenv(envRecorderTLSInsecure, "true")
	insecureDev := New(defaultProxyAddr)
	resp, err := insecureDev.proxyClient.Get(server.URL)
	if err != nil {
		t.Fatalf("explicit development TLS override failed: %v", err)
	}
	resp.Body.Close()
}

func TestStripProxyRequestHeaders(t *testing.T) {
	header := http.Header{
		"Connection":                  {"keep-alive, X-Internal-Hop"},
		"X-Internal-Hop":              {"private"},
		"Proxy-Authorization":         {"Bearer proxy-secret"},
		"X-Apexload-Recorder-Token":   {"recorder-secret"},
		"X-Apexload-Recorder-Private": {"internal"},
		"Authorization":               {"Bearer origin-token"},
	}
	stripProxyRequestHeaders(header, false)
	for _, name := range []string{"Connection", "X-Internal-Hop", "Proxy-Authorization", "X-Apexload-Recorder-Token", "X-Apexload-Recorder-Private"} {
		if value := header.Get(name); value != "" {
			t.Errorf("%s leaked upstream: %q", name, value)
		}
	}
	if header.Get("Authorization") != "Bearer origin-token" {
		t.Fatal("origin Authorization header should be preserved")
	}
}

func TestStartProxyReportsBindFailureSynchronously(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	rec := New(listener.Addr().String())
	if err := rec.StartProxy(); err == nil {
		t.Fatal("StartProxy returned success for an unavailable address")
	}
}

func TestProxyAuthTokenEnforced(t *testing.T) {
	t.Setenv(envProxyAuthToken, "secret-token")
	rec := New(defaultProxyAddr)

	req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	if rec.authorizeProxyRequest(req) {
		t.Fatal("request without proxy auth should be rejected")
	}
	req.Header.Set("Proxy-Authorization", "Bearer secret-token")
	if !rec.authorizeProxyRequest(req) {
		t.Fatal("Bearer proxy auth token should be accepted")
	}
	req.Header.Set("Proxy-Authorization", "Basic YXBleGxvYWQ6c2VjcmV0LXRva2Vu")
	if !rec.authorizeProxyRequest(req) {
		t.Fatal("Basic proxy password should be accepted for browser compatibility")
	}
	resp := httptest.NewRecorder()
	req.Header.Del("Proxy-Authorization")
	rec.handleProxy(resp, req)
	challenges := resp.Header().Values("Proxy-Authenticate")
	if len(challenges) != 2 || !strings.HasPrefix(challenges[0], "Basic ") || !strings.HasPrefix(challenges[1], "Bearer ") {
		t.Fatalf("Proxy-Authenticate = %#v, want Basic and Bearer challenges", challenges)
	}
}

func TestProxyConcurrencyLimitFailsFast(t *testing.T) {
	rec := New(defaultProxyAddr)
	for index := 0; index < maxConcurrentRecorderProxyRequests; index++ {
		if !rec.acquireProxySlot() {
			t.Fatalf("proxy slot %d was rejected before the configured limit", index)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
	resp := httptest.NewRecorder()
	rec.handleProxy(resp, req)
	if resp.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", resp.Code, http.StatusServiceUnavailable)
	}
	for index := 0; index < maxConcurrentRecorderProxyRequests; index++ {
		rec.releaseProxySlot()
	}
	if !rec.acquireProxySlot() {
		t.Fatal("released proxy capacity was not reusable")
	}
	rec.releaseProxySlot()
}

func TestProxyRejectsPrivateTargetsByDefault(t *testing.T) {
	rec := New(defaultProxyAddr)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/private", nil)
	rr := httptest.NewRecorder()
	rec.handleProxy(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for private target", rr.Code)
	}

	t.Setenv(envProxyAllowPrivateTargets, "true")
	if err := validateProxyTargetURL("http://127.0.0.1/private"); err != nil {
		t.Fatalf("private target opt-in should allow localhost: %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	r := New(":0")

	var events []SessionEvent
	notify := func(evt SessionEvent) {
		events = append(events, evt)
	}

	s, err := r.StartSession("s1", "Test Session", SessionFilters{ExcludeStatic: true}, notify)
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != "recording" {
		t.Fatalf("expected recording, got %s", s.Status)
	}
	if s.Name != "Test Session" {
		t.Fatalf("expected Test Session, got %s", s.Name)
	}

	_, err = r.StartSession("s2", "Another", SessionFilters{}, nil)
	if err == nil {
		t.Fatal("expected error starting second session")
	}

	active := r.ActiveSession()
	if active == nil || active.ID != "s1" {
		t.Fatal("expected active session s1")
	}

	sessions := r.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}

	got, ok := r.GetSession("s1")
	if !ok || got.ID != "s1" {
		t.Fatal("GetSession failed")
	}

	_, ok = r.GetSession("nonexistent")
	if ok {
		t.Fatal("expected not found")
	}

	stopped, err := r.StopSession("s1")
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != "stopped" {
		t.Fatalf("expected stopped, got %s", stopped.Status)
	}
	if stopped.StoppedAt == nil {
		t.Fatal("expected StoppedAt to be set")
	}

	if r.ActiveSession() != nil {
		t.Fatal("expected no active session after stop")
	}

	_, err = r.StopSession("s1")
	if err == nil {
		t.Fatal("expected error stopping already-stopped session")
	}

	_, err = r.StopSession("nonexistent")
	if err == nil {
		t.Fatal("expected error stopping nonexistent session")
	}
}

func TestRecorderSnapshotsDeltasAndEventsOmitHeavyBodies(t *testing.T) {
	r := New(":0")
	var events []SessionEvent
	session, err := r.StartSession("s1", "Delta Test", SessionFilters{}, func(evt SessionEvent) {
		events = append(events, evt)
	})
	if err != nil {
		t.Fatal(err)
	}
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}}
	req1, _ := http.NewRequest(http.MethodPost, "https://shop.example.com/api/login", nil)
	req1.Header.Set("Content-Type", "application/json")
	seq1 := r.recordRequest(session, req1, []byte(`{"password":"secret"}`), resp, []byte(`{"token":"abc"}`), 10*time.Millisecond)
	r.appendWebSocketMessage(session, seq1, RecordedWebSocketMessage{
		Direction: "sent",
		Opcode:    1,
		Payload:   `{"subscribe":true}`,
		Timestamp: time.Now(),
	})
	req2, _ := http.NewRequest(http.MethodGet, "https://shop.example.com/api/cart", nil)
	r.recordRequest(session, req2, nil, resp, []byte(`{"items":[]}`), 10*time.Millisecond)

	if len(events) < 3 {
		t.Fatalf("events = %d, want request recorded/update events", len(events))
	}
	for _, evt := range events {
		if evt.Request == nil {
			continue
		}
		if evt.Request.Body != "" || evt.Request.ResponseBody != "" {
			t.Fatalf("event %s carried bodies: %#v", evt.Type, evt.Request)
		}
		for _, msg := range evt.Request.WebSocketMessages {
			if msg.Payload != "" {
				t.Fatalf("event %s carried websocket payload %q", evt.Type, msg.Payload)
			}
		}
	}

	full, ok := r.GetSession("s1")
	if !ok {
		t.Fatal("GetSession failed")
	}
	if len(full.Requests) != 2 {
		t.Fatalf("full requests = %d, want 2", len(full.Requests))
	}
	if full.Requests[0].Body == "" || full.Requests[0].ResponseBody == "" {
		t.Fatalf("stored request lost bodies: %#v", full.Requests[0])
	}
	if got := full.Requests[0].WebSocketMessages[0].Payload; got != `{"subscribe":true}` {
		t.Fatalf("stored websocket payload = %q, want preserved payload", got)
	}

	summary, ok := r.GetSessionWithOptions("s1", false, 0)
	if !ok {
		t.Fatal("summary GetSessionWithOptions failed")
	}
	if len(summary.Requests) != 0 || summary.RequestCount != 2 || summary.LastSeq != 2 {
		t.Fatalf("summary = %#v, want counts without request bodies", summary)
	}
	listed := r.ListSessionSummaries()
	if len(listed) != 1 || len(listed[0].Requests) != 0 || listed[0].RequestCount != 2 {
		t.Fatalf("listed summaries = %#v, want request-free summary", listed)
	}
	delta, ok := r.GetSessionWithOptions("s1", true, 1)
	if !ok {
		t.Fatal("delta GetSessionWithOptions failed")
	}
	if len(delta.Requests) != 1 || delta.Requests[0].Seq != 2 || delta.RequestCount != 2 || delta.LastSeq != 2 {
		t.Fatalf("delta = %#v, want only requests after seq 1 with full counts", delta)
	}
	tail, ok := r.GetSessionWindow("s1", true, 0, 1)
	if !ok {
		t.Fatal("tail GetSessionWindow failed")
	}
	if len(tail.Requests) != 1 || tail.Requests[0].Seq != 2 || tail.RequestCount != 2 || tail.LastSeq != 2 {
		t.Fatalf("tail = %#v, want latest request with full counts", tail)
	}
}

func TestGetSessionWindowBoundsDeltaByTail(t *testing.T) {
	r := New(":0")
	session, err := r.StartSession("s1", "Bounded Delta", SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	response := &http.Response{StatusCode: http.StatusOK}
	for index := 0; index < 3; index++ {
		request, requestErr := http.NewRequest(http.MethodGet, fmt.Sprintf("https://example.com/%d", index), nil)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		r.recordRequest(session, request, nil, response, nil, time.Millisecond)
	}

	deltaTail, ok := r.GetSessionWindow("s1", true, 1, 1)
	if !ok {
		t.Fatal("delta tail GetSessionWindow failed")
	}
	if len(deltaTail.Requests) != 1 || deltaTail.Requests[0].Seq != 3 || deltaTail.RequestCount != 3 || deltaTail.LastSeq != 3 {
		t.Fatalf("delta tail = %#v, want only latest request with full counts", deltaTail)
	}
}

func TestDeleteSession(t *testing.T) {
	r := New(":0")
	r.StartSession("s1", "Delete Test", SessionFilters{}, nil)
	r.StopSession("s1")

	if !r.DeleteSession("s1") {
		t.Fatal("expected delete to succeed")
	}
	if r.DeleteSession("s1") {
		t.Fatal("expected delete to fail on already-deleted")
	}
	if len(r.ListSessions()) != 0 {
		t.Fatal("expected 0 sessions after delete")
	}
}

func TestDeleteSessionClosesRegisteredTunnels(t *testing.T) {
	r := New(":0")
	r.StartSession("s1", "Tunnel Delete", SessionFilters{}, nil)
	clientRecorder, clientPeer := net.Pipe()
	upstreamRecorder, upstreamPeer := net.Pipe()
	defer clientPeer.Close()
	defer upstreamPeer.Close()

	r.registerTunnel(&recorderTunnel{sessionID: "s1", clientConn: clientRecorder, upstreamConn: upstreamRecorder})
	if !r.DeleteSession("s1") {
		t.Fatal("expected delete to succeed")
	}

	_ = clientPeer.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := clientPeer.Write([]byte("x")); err == nil {
		t.Fatal("client tunnel peer write succeeded after DeleteSession; want closed pipe")
	}
	_ = upstreamPeer.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	if _, err := upstreamPeer.Write([]byte("x")); err == nil {
		t.Fatal("upstream tunnel peer write succeeded after DeleteSession; want closed pipe")
	}
}

func TestDeleteActiveSession(t *testing.T) {
	r := New(":0")
	r.StartSession("s1", "Active Delete", SessionFilters{}, nil)

	r.DeleteSession("s1")
	if r.ActiveSession() != nil {
		t.Fatal("expected no active session after deleting active")
	}
}

func TestPauseResumeSessionSkipsCaptureWhilePaused(t *testing.T) {
	rec := New(":0")
	session, err := rec.StartSession("test", "Pause Test", SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rec.PauseSession("test"); err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("GET", "http://example.com/api/paused", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}
	rec.recordRequest(session, req, nil, resp, []byte(`{"ok":true}`), 10*time.Millisecond)
	if len(session.Requests) != 0 {
		t.Fatalf("expected paused session to skip capture, got %d requests", len(session.Requests))
	}

	if _, err := rec.ResumeSession("test"); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest("GET", "http://example.com/api/resumed", nil)
	rec.recordRequest(session, req, nil, resp, []byte(`{"ok":true}`), 10*time.Millisecond)

	if len(session.Requests) != 1 {
		t.Fatalf("expected resumed session to capture 1 request, got %d", len(session.Requests))
	}
}

func TestUpdateRequestCurationFields(t *testing.T) {
	rec := New(":0")
	session, err := rec.StartSession("test", "Curation Test", SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	session.Requests = append(session.Requests, RecordedRequest{Seq: 1, Method: "GET", Path: "/api/users"})

	stepName := "Load Users"
	transaction := "Browse Users"
	note := "Keep this as the main API call"
	excluded := true
	think := int64(1500)
	updated, err := rec.UpdateRequest("test", 1, RequestUpdate{
		StepName:        &stepName,
		TransactionName: &transaction,
		Note:            &note,
		Excluded:        &excluded,
		ThinkTimeMs:     &think,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.StepName != stepName || updated.TransactionName != transaction || updated.Note != note {
		t.Fatalf("unexpected updated labels: %+v", updated)
	}
	if !updated.Excluded {
		t.Fatal("expected request to be excluded")
	}
	if updated.ThinkTimeMs != think {
		t.Fatalf("expected think time %d, got %d", think, updated.ThinkTimeMs)
	}
}

func TestUpdateRequestRejectsOversizedCurationFields(t *testing.T) {
	rec := New(":0")
	session, err := rec.StartSession("test", "Curation Limits", SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/api/users", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}
	if seq := rec.recordRequest(session, req, nil, resp, nil, time.Millisecond); seq != 1 {
		t.Fatalf("recorded seq = %d, want 1", seq)
	}
	retainedBefore := session.retainedBytes

	tests := []struct {
		name   string
		update RequestUpdate
	}{
		{name: "step name", update: RequestUpdate{StepName: recorderStringPointer(strings.Repeat("s", maxRecorderStepNameBytes+1))}},
		{name: "transaction name", update: RequestUpdate{TransactionName: recorderStringPointer(strings.Repeat("t", maxRecorderTransactionNameBytes+1))}},
		{name: "note", update: RequestUpdate{Note: recorderStringPointer(strings.Repeat("n", maxRecorderNoteBytes+1))}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := rec.UpdateRequest("test", 1, tc.update); err == nil {
				t.Fatal("UpdateRequest accepted an oversized curation field")
			}
			if session.retainedBytes != retainedBefore {
				t.Fatalf("retained bytes changed after rejected update: %d -> %d", retainedBefore, session.retainedBytes)
			}
			if got := session.Requests[0]; got.StepName != "" || got.TransactionName != "" || got.Note != "" {
				t.Fatalf("request changed after rejected update: %+v", got)
			}
		})
	}
}

func TestRecordRequestRetentionRejectionHasNoPartialMutation(t *testing.T) {
	rec := New(":0")
	var events []SessionEvent
	session, err := rec.StartSession("test", "Retention Limit", SessionFilters{}, func(event SessionEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatal(err)
	}
	session.retainedBytes = maxRecordedSessionBytes - 512
	req, _ := http.NewRequest(http.MethodPost, "http://example.com/api/users", strings.NewReader("payload"))
	req.Header.Set("Content-Type", "text/plain")
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}

	if seq := rec.recordRequest(session, req, []byte("payload"), resp, []byte("response"), time.Second); seq != 0 {
		t.Fatalf("recorded seq = %d, want rejection", seq)
	}
	if session.seq != 0 || len(session.Requests) != 0 || session.storedBodyBytes != 0 {
		t.Fatalf("rejected request mutated session: seq=%d requests=%d bodies=%d", session.seq, len(session.Requests), session.storedBodyBytes)
	}
	if session.BaseURL != "" || !session.lastReqTime.IsZero() || !session.lastReqEnd.IsZero() {
		t.Fatalf("rejected request mutated timing/base URL: base=%q request=%v end=%v", session.BaseURL, session.lastReqTime, session.lastReqEnd)
	}
	if session.DroppedRequests != 1 || !session.Truncated || len(session.Warnings) != 1 {
		t.Fatalf("request cap evidence = dropped:%d truncated:%v warnings:%v", session.DroppedRequests, session.Truncated, session.Warnings)
	}
	if len(events) != 1 || events[0].Type != "recording_warning" || events[0].Warning == "" {
		t.Fatalf("warning events = %#v, want one explicit recording warning", events)
	}
	if seq := rec.recordRequest(session, req, []byte("payload"), resp, []byte("response"), time.Second); seq != 0 {
		t.Fatalf("second record seq = %d, want rejection", seq)
	}
	if session.DroppedRequests != 2 || len(session.Warnings) != 1 || len(events) != 1 {
		t.Fatalf("repeated cap evidence = dropped:%d warnings:%v events:%#v", session.DroppedRequests, session.Warnings, events)
	}
	snapshot := session.snapshot()
	if snapshot.DroppedRequests != 2 || !snapshot.Truncated || len(snapshot.Warnings) != 1 {
		t.Fatalf("snapshot omitted cap evidence: %+v", snapshot)
	}
}

func TestAppendWebSocketMessageHonorsSessionRetentionLimit(t *testing.T) {
	rec := New(":0")
	session, err := rec.StartSession("test", "WebSocket Retention Limit", SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com/socket", nil)
	resp := &http.Response{StatusCode: http.StatusSwitchingProtocols, Header: make(http.Header)}
	if seq := rec.recordRequest(session, req, nil, resp, nil, time.Millisecond); seq != 1 {
		t.Fatalf("recorded seq = %d, want 1", seq)
	}
	message := RecordedWebSocketMessage{Direction: "client_to_server", Payload: "payload", Timestamp: time.Now()}
	session.retainedBytes = maxRecordedSessionBytes - estimateRecordedWebSocketMessageBytes(message) - 31

	rec.appendWebSocketMessage(session, 1, message)
	if len(session.Requests[0].WebSocketMessages) != 0 || session.webSocketMessages != 0 || session.webSocketPayloadBytes != 0 {
		t.Fatalf("rejected websocket message mutated session: %+v", session.Requests[0].WebSocketMessages)
	}
	if session.DroppedWebSocketMessages != 1 || !session.Truncated || len(session.Warnings) != 1 {
		t.Fatalf("websocket cap evidence = dropped:%d truncated:%v warnings:%v", session.DroppedWebSocketMessages, session.Truncated, session.Warnings)
	}
}

func recorderStringPointer(value string) *string {
	return &value
}

func TestProxyIntegration(t *testing.T) {
	allowPrivateProxyTargets(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-Id", "req-abc-123")
		if r.URL.Path == "/api/login" {
			w.WriteHeader(200)
			fmt.Fprint(w, `{"token":"eyJhbGciOiJIUzI1NiJ9.test.sig","user_id":"12345"}`)
			return
		}
		if r.URL.Path == "/api/data" {
			w.WriteHeader(200)
			fmt.Fprint(w, `{"items":[1,2,3]}`)
			return
		}
		w.WriteHeader(200)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer backend.Close()

	rec := New(":0")
	rec.proxy = &http.Server{
		Addr:    ":0",
		Handler: http.HandlerFunc(rec.handleProxy),
	}

	var captured []SessionEvent
	rec.StartSession("test", "Proxy Test", SessionFilters{ExcludeStatic: true}, func(evt SessionEvent) {
		captured = append(captured, evt)
	})

	proxy := httptest.NewServer(http.HandlerFunc(rec.handleProxy))
	defer proxy.Close()

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: func(r *http.Request) (*url.URL, error) {
				return url.Parse(proxy.URL)
			},
		},
	}
	_ = client

	loginReq, _ := http.NewRequest("POST", backend.URL+"/api/login", strings.NewReader(`{"username":"test"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Host = strings.TrimPrefix(backend.URL, "http://")

	rr := httptest.NewRecorder()
	rec.handleProxy(rr, loginReq)

	if rr.Code != 200 {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "token") {
		t.Fatalf("expected token in response, got %s", body)
	}

	time.Sleep(100 * time.Millisecond)

	dataReq, _ := http.NewRequest("GET", backend.URL+"/api/data", nil)
	dataReq.Host = strings.TrimPrefix(backend.URL, "http://")
	rr2 := httptest.NewRecorder()
	rec.handleProxy(rr2, dataReq)

	if rr2.Code != 200 {
		t.Fatalf("expected 200 for data, got %d", rr2.Code)
	}

	session, _ := rec.GetSession("test")
	if len(session.Requests) != 2 {
		t.Fatalf("expected 2 recorded requests, got %d", len(session.Requests))
	}

	req1 := session.Requests[0]
	if req1.Method != "POST" {
		t.Fatalf("expected POST, got %s", req1.Method)
	}
	if req1.StatusCode != 200 {
		t.Fatalf("expected status 200, got %d", req1.StatusCode)
	}
	if req1.Body != `{"username":"test"}` {
		t.Fatalf("expected request body, got %q", req1.Body)
	}
	if !strings.Contains(req1.ResponseBody, "token") {
		t.Fatalf("expected response body with token, got %q", req1.ResponseBody)
	}

	req2 := session.Requests[1]
	if req2.Method != "GET" {
		t.Fatalf("expected GET, got %s", req2.Method)
	}
	if req2.ThinkTimeMs < 50 {
		t.Logf("think time might be low: %dms", req2.ThinkTimeMs)
	}

	if len(captured) != 2 {
		t.Fatalf("expected 2 events, got %d", len(captured))
	}
}

func TestProxyCapturesWebSocketFrames(t *testing.T) {
	allowPrivateProxyTargets(t)
	upgrader := gorillawebsocket.Upgrader{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("websocket upgrade failed: %v", err)
			return
		}
		defer conn.Close()
		for {
			messageType, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(messageType, payload); err != nil {
				return
			}
		}
	}))
	defer backend.Close()

	backendURL, err := url.Parse(backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	backendURL.Scheme = "ws"
	backendURL.Path = "/ws"
	backendURL.RawQuery = "room=orders"

	rec := New(":0")
	if _, err := rec.StartSession("test", "WebSocket Test", SessionFilters{BodyCaptureLimitBytes: 64}, nil); err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(rec.handleProxy))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	dialer := gorillawebsocket.Dialer{
		Proxy:            http.ProxyURL(proxyURL),
		HandshakeTimeout: 2 * time.Second,
	}

	conn, resp, err := dialer.Dial(backendURL.String(), nil)
	if err != nil {
		if resp != nil {
			t.Fatalf("websocket dial status=%d err=%v", resp.StatusCode, err)
		}
		t.Fatalf("websocket dial: %v", err)
	}
	defer conn.Close()
	if resp == nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade status = %#v, want 101", resp)
	}

	if err := conn.WriteMessage(gorillawebsocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("write text: %v", err)
	}
	if _, payload, err := conn.ReadMessage(); err != nil || string(payload) != "hello" {
		t.Fatalf("read text echo payload=%q err=%v", payload, err)
	}
	binaryPayload := []byte{0x00, 0xff, 0x10}
	if err := conn.WriteMessage(gorillawebsocket.BinaryMessage, binaryPayload); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if _, payload, err := conn.ReadMessage(); err != nil || !bytes.Equal(payload, binaryPayload) {
		t.Fatalf("read binary echo payload=%v err=%v", payload, err)
	}

	var recorded RecordedRequest
	deadline := time.Now().Add(2 * time.Second)
	for {
		session, _ := rec.GetSession("test")
		if len(session.Requests) == 1 && len(session.Requests[0].WebSocketMessages) >= 4 {
			recorded = session.Requests[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for websocket capture; session=%#v", session)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if recorded.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("recorded status = %d, want 101", recorded.StatusCode)
	}
	if recorded.Scheme != "ws" || recorded.Path != "/ws" || recorded.QueryString != "room=orders" {
		t.Fatalf("recorded URL fields = scheme=%q path=%q query=%q", recorded.Scheme, recorded.Path, recorded.QueryString)
	}
	messages := recorded.WebSocketMessages
	if messages[0].Direction != webSocketDirectionClientToServer || messages[0].Opcode != webSocketOpcodeText || messages[0].Payload != "hello" || messages[0].PayloadEncoding != "" {
		t.Fatalf("first websocket message = %#v", messages[0])
	}
	if messages[1].Direction != webSocketDirectionServerToClient || messages[1].Payload != "hello" {
		t.Fatalf("second websocket message = %#v", messages[1])
	}
	wantBinary := base64.StdEncoding.EncodeToString(binaryPayload)
	if messages[2].Direction != webSocketDirectionClientToServer || messages[2].Opcode != webSocketOpcodeBinary || messages[2].Payload != wantBinary || messages[2].PayloadEncoding != "base64" {
		t.Fatalf("third websocket message = %#v, want base64 binary", messages[2])
	}
	if messages[3].Direction != webSocketDirectionServerToClient || messages[3].Payload != wantBinary || messages[3].PayloadEncoding != "base64" {
		t.Fatalf("fourth websocket message = %#v, want base64 binary echo", messages[3])
	}
}

func TestRecordRequestRichMetadataRedactionAndTruncation(t *testing.T) {
	rec := New(":0")
	session, err := rec.StartSession("test", "Metadata Test", SessionFilters{
		ExcludeStatic:         true,
		BodyCaptureLimitBytes: 12,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest("POST", "http://example.com/api/data?debug=true", strings.NewReader(`{"request":"abcdefghijklmnopqrstuvwxyz"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret")

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Set-Cookie":   []string{"session=secret", "theme=dark"},
			"Content-Type": []string{"application/json"},
			"X-Auth-Token": []string{"response-secret"},
		},
	}
	rec.recordRequest(session, req, []byte(`{"request":"abcdefghijklmnopqrstuvwxyz"}`), resp, []byte(`{"payload":"abcdefghijklmnopqrstuvwxyz"}`), 10*time.Millisecond)

	if len(session.Requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(session.Requests))
	}

	recorded := session.Requests[0]
	if recorded.QueryString != "debug=true" {
		t.Fatalf("expected query string, got %q", recorded.QueryString)
	}
	if recorded.ResourceType != "api" {
		t.Fatalf("expected api resource type, got %q", recorded.ResourceType)
	}
	if !recorded.BodyTruncated || !recorded.ResponseBodyTruncated {
		t.Fatalf("expected request and response bodies to be marked truncated: %+v", recorded)
	}
	if recorded.Headers["Authorization"] != "[redacted]" {
		t.Fatalf("expected authorization to be redacted, got %q", recorded.Headers["Authorization"])
	}
	if got := recorded.ResponseHeadersMulti["Set-Cookie"]; len(got) != 2 || got[0] != "[redacted]" || got[1] != "[redacted]" {
		t.Fatalf("expected redacted multi set-cookie values, got %#v", got)
	}
	if recorded.ResponseHeaders["X-Auth-Token"] != "[redacted]" || recorded.RawResponseHeaders["X-Auth-Token"] != "response-secret" {
		t.Fatalf("response correlation header views = scrubbed:%q raw:%q", recorded.ResponseHeaders["X-Auth-Token"], recorded.RawResponseHeaders["X-Auth-Token"])
	}
	wire, err := json.Marshal(recorded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "response-secret") {
		t.Fatalf("raw response correlation headers leaked through JSON: %s", wire)
	}
}

func TestBodyCaptureBoundsRequestStream(t *testing.T) {
	capture := newBodyCapture(4)
	reader := requestBodyReader(io.NopCloser(strings.NewReader("abcdef")), capture)
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(body) != "abcdef" {
		t.Fatalf("proxied body = %q, want full body", body)
	}
	if got := string(capture.bytes()); got != "abcd" {
		t.Fatalf("captured body = %q, want bounded prefix", got)
	}
	if got := capture.size(); got != 6 {
		t.Fatalf("captured size = %d, want full stream size", got)
	}
	if !capture.truncated() {
		t.Fatal("capture.truncated() = false, want true")
	}
}

func TestStreamAndRecordResponsePreservesRequestBodyCaptureMetadata(t *testing.T) {
	rec := New(":0")
	session, err := rec.StartSession("test", "Streaming Request Body Metadata", SessionFilters{BodyCaptureLimitBytes: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	capture := newBodyCapture(4)
	if _, err := capture.Write([]byte("abcdef")); err != nil {
		t.Fatalf("capture write: %v", err)
	}
	req, _ := http.NewRequest("POST", "http://example.com/upload", nil)
	req.Header.Set("Content-Type", "text/plain")
	resp := &http.Response{
		StatusCode:       http.StatusOK,
		Header:           http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:             io.NopCloser(strings.NewReader("data: ok\n\n")),
		TransferEncoding: []string{"chunked"},
	}

	var forwarded bytes.Buffer
	rec.streamAndRecordResponse(&forwarded, nil, session, req, capture.bytes(), resp, time.Now(), recordOptions{
		requestBodySizeBytes:     boundedInt(capture.size()),
		requestBodySizeKnown:     true,
		forceRequestBodyTruncate: capture.truncated(),
	})

	if forwarded.String() != "data: ok\n\n" {
		t.Fatalf("forwarded streaming body = %q", forwarded.String())
	}
	if len(session.Requests) != 1 {
		t.Fatalf("recorded requests = %d, want 1", len(session.Requests))
	}
	recorded := session.Requests[0]
	if recorded.Body != "abcd" {
		t.Fatalf("recorded request body = %q, want capped prefix", recorded.Body)
	}
	if recorded.BodySizeBytes != 6 {
		t.Fatalf("request body size = %d, want 6", recorded.BodySizeBytes)
	}
	if !recorded.BodyTruncated {
		t.Fatal("expected streaming request body to be marked truncated")
	}
	if !recorded.ResponseBodyTruncated {
		t.Fatal("expected streaming response body to be marked truncated")
	}
	if recorded.ResponseBodySizeBytes != len("data: ok\n\n") {
		t.Fatalf("response body size = %d, want %d", recorded.ResponseBodySizeBytes, len("data: ok\n\n"))
	}
}

func TestRecordRequestCaptureCookiesOptIn(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example.com/api/session", nil)
	req.Header.Set("Cookie", "session=sid-123; theme=dark")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Set-Cookie": []string{"session=sid-456; Path=/; HttpOnly"},
		},
	}

	redactingRecorder := New(":0")
	redactedSession, err := redactingRecorder.StartSession("redacted", "Redacted Cookies", SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	redactingRecorder.recordRequest(redactedSession, req.Clone(req.Context()), nil, resp, []byte(`{}`), 10*time.Millisecond)
	if got := redactedSession.Requests[0].Headers["Cookie"]; got != "[redacted]" {
		t.Fatalf("default cookie header = %q, want redacted", got)
	}
	if got := redactedSession.Requests[0].ResponseHeaders["Set-Cookie"]; got != "[redacted]" {
		t.Fatalf("default set-cookie header = %q, want redacted", got)
	}

	capturingRecorder := New(":0")
	capturedSession, err := capturingRecorder.StartSession("captured", "Captured Cookies", SessionFilters{CaptureCookies: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	capturingRecorder.recordRequest(capturedSession, req.Clone(req.Context()), nil, resp, []byte(`{}`), 10*time.Millisecond)
	if got := capturedSession.Requests[0].Headers["Cookie"]; got != "session=sid-123; theme=dark" {
		t.Fatalf("captured cookie header = %q", got)
	}
	if got := capturedSession.Requests[0].ResponseHeaders["Set-Cookie"]; got != "session=sid-456; Path=/; HttpOnly" {
		t.Fatalf("captured set-cookie header = %q", got)
	}
}

func TestRecordRequestKeepsRawHeadersForCorrelationOnly(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example.com/api/session", nil)
	req.Header.Set("Authorization", "Bearer tok-abcdef123456")
	req.Header.Set("X-Api-Key", "raw-api-secret")
	req.Header.Set("Proxy-Authorization", "Basic proxy-secret")
	req.Header.Set("X-APEXLoad-Recorder-Token", "recorder-control-secret")
	req.Header.Set("x-apexload-recorder-private", "private-control-value")
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{
		"X-APEXLoad-Recorder-Response": {"response-control-secret"},
	}}

	rec := New(":0")
	session, err := rec.StartSession("headers", "Header Correlation", SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	rec.recordRequest(session, req, nil, resp, []byte(`{}`), 10*time.Millisecond)

	recorded := session.Requests[0]
	if got := recorded.Headers["Authorization"]; got != "[redacted]" {
		t.Fatalf("public authorization header = %q, want redacted", got)
	}
	if got := recorded.RawHeaders["Authorization"]; got != "Bearer tok-abcdef123456" {
		t.Fatalf("raw authorization header = %q", got)
	}
	if _, ok := recorded.RawHeaders["Proxy-Authorization"]; ok {
		t.Fatalf("proxy authorization leaked into raw headers: %#v", recorded.RawHeaders)
	}
	for label, headers := range map[string]map[string]string{
		"sanitized request":   recorded.Headers,
		"correlation request": recorded.RawHeaders,
		"sanitized response":  recorded.ResponseHeaders,
	} {
		for name := range headers {
			if isRecorderControlHeader(name) {
				t.Fatalf("%s retained recorder control header %q: %#v", label, name, headers)
			}
		}
	}
	for label, headers := range map[string]map[string][]string{
		"sanitized multi-value request":   recorded.HeadersMulti,
		"correlation multi-value request": recorded.RawHeadersMulti,
		"sanitized multi-value response":  recorded.ResponseHeadersMulti,
	} {
		for name := range headers {
			if isRecorderControlHeader(name) {
				t.Fatalf("%s retained recorder control header %q: %#v", label, name, headers)
			}
		}
	}
}

func TestRecordRequestCapturesBinaryBodiesAsBase64(t *testing.T) {
	rec := New(":0")
	session, err := rec.StartSession("test", "Binary Body Test", SessionFilters{
		ExcludeStatic:         true,
		BodyCaptureLimitBytes: 4,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	reqBody := []byte{0x00, 0xff, 0x10, 0x80, 0x42}
	respBody := []byte{0xde, 0xad, 0xbe, 0xef, 0x01}
	req, _ := http.NewRequest("POST", "http://example.com/api/upload", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp := &http.Response{
		StatusCode: http.StatusCreated,
		Header: http.Header{
			"Content-Type": []string{"application/octet-stream"},
		},
	}

	rec.recordRequest(session, req, reqBody, resp, respBody, 10*time.Millisecond)

	if len(session.Requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(session.Requests))
	}
	recorded := session.Requests[0]
	if recorded.BodyEncoding != "base64" {
		t.Fatalf("request body encoding = %q, want base64", recorded.BodyEncoding)
	}
	if recorded.Body != base64.StdEncoding.EncodeToString(reqBody[:4]) {
		t.Fatalf("request body = %q, want base64 of limited bytes", recorded.Body)
	}
	if !recorded.BodyTruncated {
		t.Fatal("expected request body to be marked truncated")
	}
	if recorded.ResponseBodyEncoding != "" {
		t.Fatalf("response body encoding = %q, want unavailable omitted storage", recorded.ResponseBodyEncoding)
	}
	if recorded.ResponseBody != "" || recorded.ResponseContentCapture.ReasonCode != "response_content_decode_limit" {
		t.Fatalf("response body/capture = %q/%+v, want unavailable content", recorded.ResponseBody, recorded.ResponseContentCapture)
	}
	if !recorded.ResponseBodyTruncated {
		t.Fatal("expected response body to be marked truncated")
	}
}

func TestRecordRequestPreservesMultipartUploadBodyAsReplayableBase64(t *testing.T) {
	rec := New(":0")
	session, err := rec.StartSession("test", "Multipart Body Test", SessionFilters{
		BodyCaptureLimitBytes: 4,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("description", "quarterly report"); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", "report.bin")
	if err != nil {
		t.Fatal(err)
	}
	fileBytes := []byte{0x00, 0xff, 0x10, 0x80, 0x42, 0x43}
	if _, err := part.Write(fileBytes); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reqBody := body.Bytes()
	req, _ := http.NewRequest("POST", "http://example.com/api/upload", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp := &http.Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}

	rec.recordRequest(session, req, reqBody, resp, []byte(`{"ok":true}`), 10*time.Millisecond)

	recorded := session.Requests[0]
	if recorded.BodyEncoding != "base64" {
		t.Fatalf("request body encoding = %q, want base64", recorded.BodyEncoding)
	}
	if recorded.BodyTruncated {
		t.Fatal("multipart body should not be truncated when within capture cap")
	}
	decoded, err := base64.StdEncoding.DecodeString(recorded.Body)
	if err != nil {
		t.Fatalf("decode recorded body: %v", err)
	}
	if !bytes.Equal(decoded, reqBody) {
		t.Fatalf("recorded multipart body did not round-trip")
	}

	// The upload converts to body_multipart: the text field stays
	// editable, and the file becomes a file data source.
	converted := ConvertSession(session, "")
	action := converted.Script.Actions[0]
	structured := action.BodyMultipart
	if structured == nil || action.Body != "" || len(structured.Parts) != 2 {
		t.Fatalf("converted action = %+v, want a two-part body_multipart", action)
	}
	if structured.Parts[0].Name != "description" || structured.Parts[0].Value != "quarterly report" {
		t.Fatalf("text part = %+v", structured.Parts[0])
	}
	file := structured.Parts[1]
	if file.Name != "file" || file.Filename != "report.bin" || file.Value != "{{file.upload_file}}" {
		t.Fatalf("file part = %+v", file)
	}
	if got := action.Headers["Content-Type"]; got != "multipart/form-data" {
		t.Fatalf("Content-Type = %q, want the boundary left to replay", got)
	}
	if issues := dsl.ValidateScriptIssues(converted.Script); len(issues) != 0 {
		t.Fatalf("converted upload does not validate: %v", issues)
	}
	if len(converted.Script.DataSources) != 1 {
		t.Fatalf("data sources = %+v", converted.Script.DataSources)
	}
	source := converted.Script.DataSources[0]
	content, err := base64.StdEncoding.DecodeString(source.InlineData)
	if err != nil || source.Name != "upload_file" || source.Type != "file" || source.FileName != "report.bin" || source.Encoding != "base64" || !bytes.Equal(content, fileBytes) {
		t.Fatalf("file data source = %+v", source)
	}
}

func TestRecordRequestLeavesTextBodiesUnencoded(t *testing.T) {
	rec := New(":0")
	session, err := rec.StartSession("test", "Text Body Test", SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	reqBody := []byte(`{"name":"alice"}`)
	respBody := []byte(`{"ok":true}`)
	req, _ := http.NewRequest("POST", "http://example.com/api/users", bytes.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
	}

	rec.recordRequest(session, req, reqBody, resp, respBody, 10*time.Millisecond)

	recorded := session.Requests[0]
	if recorded.BodyEncoding != "" || recorded.ResponseBodyEncoding != "" {
		t.Fatalf("expected text bodies to stay unencoded, got request=%q response=%q", recorded.BodyEncoding, recorded.ResponseBodyEncoding)
	}
	if recorded.Body != string(reqBody) || recorded.ResponseBody != string(respBody) {
		t.Fatalf("text bodies changed: request=%q response=%q", recorded.Body, recorded.ResponseBody)
	}
}

func TestProxyStreamsSSEWithoutWaitingForEOF(t *testing.T) {
	allowPrivateProxyTargets(t)
	backendReady := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: hello\n\n"))
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		close(backendReady)
		<-r.Context().Done()
	}))
	defer backend.Close()

	rec := New(":0")
	if _, err := rec.StartSession("test", "SSE Test", SessionFilters{BodyCaptureLimitBytes: 64}, nil); err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(rec.handleProxy))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   2 * time.Second,
	}

	resp, err := client.Get(backend.URL + "/events")
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, len("data: hello\n\n"))
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatalf("reading first SSE event: %v", err)
	}
	if string(buf) != "data: hello\n\n" {
		t.Fatalf("first SSE event = %q", buf)
	}
	<-backendReady

	deadline := time.Now().Add(2 * time.Second)
	for {
		session, _ := rec.GetSession("test")
		if len(session.Requests) == 1 {
			recorded := session.Requests[0]
			if recorded.ResponseBody != "" || recorded.ResponseContentCapture.ReasonCode != "response_content_truncated" {
				t.Fatalf("captured SSE body/capture = %q/%+v, want incomplete content omitted", recorded.ResponseBody, recorded.ResponseContentCapture)
			}
			if !recorded.ResponseBodyTruncated {
				t.Fatal("expected streaming response to be marked truncated")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for streaming response to be recorded")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRecordRequestThinkTimeUsesPreviousResponseEnd(t *testing.T) {
	rec := New(":0")
	session, err := rec.StartSession("test", "Think Time Test", SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}
	startedAt := time.Now()

	firstReq, _ := http.NewRequest("GET", "http://example.com/api/first", nil)
	secondReq, _ := http.NewRequest("GET", "http://example.com/api/second", nil)
	thirdReq, _ := http.NewRequest("GET", "http://example.com/api/third", nil)

	rec.recordRequestStarted(session, secondReq, nil, resp, []byte(`{}`), 100*time.Millisecond, startedAt.Add(10*time.Millisecond))
	rec.recordRequestStarted(session, firstReq, nil, resp, []byte(`{}`), 5*time.Millisecond, startedAt)
	rec.recordRequestStarted(session, thirdReq, nil, resp, []byte(`{}`), 20*time.Millisecond, startedAt.Add(125*time.Millisecond))

	if len(session.Requests) != 3 {
		t.Fatalf("expected 3 requests, got %d", len(session.Requests))
	}
	if !session.Requests[0].Timestamp.Equal(startedAt.Add(10 * time.Millisecond)) {
		t.Fatalf("first recorded timestamp = %s, want request start", session.Requests[0].Timestamp)
	}
	if session.Requests[1].ThinkTimeMs != 0 {
		t.Fatalf("overlapping earlier request think time = %d, want 0", session.Requests[1].ThinkTimeMs)
	}
	if session.Requests[2].ThinkTimeMs != 15 {
		t.Fatalf("third request think time = %d, want 15ms gap from previous response end", session.Requests[2].ThinkTimeMs)
	}
}

func TestProxyCapturesResponseToLimitWhileForwardingFullBody(t *testing.T) {
	allowPrivateProxyTargets(t)
	payload := strings.Repeat("x", 32)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, payload)
	}))
	defer backend.Close()

	rec := New(":0")
	if _, err := rec.StartSession("test", "Capture Limit Test", SessionFilters{BodyCaptureLimitBytes: 8}, nil); err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(rec.handleProxy))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   2 * time.Second,
	}

	resp, err := client.Get(backend.URL + "/download")
	if err != nil {
		t.Fatalf("GET through proxy: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading response: %v", err)
	}
	if string(body) != payload {
		t.Fatalf("client body length = %d, want full payload %d", len(body), len(payload))
	}

	session, _ := rec.GetSession("test")
	if len(session.Requests) != 1 {
		t.Fatalf("recorded requests = %d, want 1", len(session.Requests))
	}
	recorded := session.Requests[0]
	if recorded.ResponseBody != "" || recorded.ResponseContentCapture.ReasonCode != "response_content_truncated" {
		t.Fatalf("recorded response body/capture = %q/%+v, want omitted incomplete capture", recorded.ResponseBody, recorded.ResponseContentCapture)
	}
	if !recorded.ResponseBodyTruncated {
		t.Fatal("expected recorded response body to be marked truncated")
	}
	if recorded.ResponseBodySizeBytes != len(payload) {
		t.Fatalf("response body size = %d, want raw forwarded size %d", recorded.ResponseBodySizeBytes, len(payload))
	}
}

func TestStaticFiltering(t *testing.T) {
	allowPrivateProxyTargets(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, "ok")
	}))
	defer backend.Close()

	rec := New(":0")
	rec.StartSession("test", "Static Test", SessionFilters{ExcludeStatic: true}, nil)

	for _, path := range []string{"/api/data", "/style.css", "/app.js", "/logo.png", "/api/users"} {
		req, _ := http.NewRequest("GET", backend.URL+path, nil)
		rr := httptest.NewRecorder()
		rec.handleProxy(rr, req)
	}

	session, _ := rec.GetSession("test")
	if len(session.Requests) != 2 {
		t.Fatalf("expected 2 non-static requests, got %d (should filter .css, .js, .png)", len(session.Requests))
	}
}

func TestStaticFilteringCanBeDisabled(t *testing.T) {
	allowPrivateProxyTargets(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, "ok")
	}))
	defer backend.Close()

	rec := New(":0")
	rec.StartSession("test", "Static Capture Test", SessionFilters{ExcludeStatic: false, ExcludeStaticSet: true}, nil)

	for _, path := range []string{"/api/data", "/app.js"} {
		req, _ := http.NewRequest("GET", backend.URL+path, nil)
		rr := httptest.NewRecorder()
		rec.handleProxy(rr, req)
	}

	session, _ := rec.GetSession("test")
	if len(session.Requests) != 2 {
		t.Fatalf("expected static request to be captured, got %d requests", len(session.Requests))
	}
}

func TestHostFiltering(t *testing.T) {
	allowPrivateProxyTargets(t)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		io.WriteString(w, "ok")
	}))
	defer backend.Close()

	backendHost := strings.TrimPrefix(backend.URL, "http://")

	rec := New(":0")
	rec.StartSession("test", "Host Test", SessionFilters{
		ExcludeStatic: true,
		IncludeHosts:  []string{backendHost},
	}, nil)

	req, _ := http.NewRequest("GET", backend.URL+"/api/test", nil)
	rr := httptest.NewRecorder()
	rec.handleProxy(rr, req)

	session, _ := rec.GetSession("test")
	if len(session.Requests) != 1 {
		t.Fatalf("expected 1 request from included host, got %d", len(session.Requests))
	}
}

func TestIsStaticResource(t *testing.T) {
	tests := []struct {
		path   string
		expect bool
	}{
		{"/api/users", false},
		{"/style.css", true},
		{"/app.js", true},
		{"/logo.png", true},
		{"/font.woff2", true},
		{"/data.json", false},
		{"/api/v1/login", false},
		{"/image.svg", true},
		{"/favicon.ico", true},
	}

	for _, tt := range tests {
		u, _ := url.Parse("http://example.com" + tt.path)
		got := isStaticResource(u)
		if got != tt.expect {
			t.Errorf("isStaticResource(%s) = %v, want %v", tt.path, got, tt.expect)
		}
	}
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zlibBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func brotliBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := brotli.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRecordRequestDecompressesResponseBody(t *testing.T) {
	rec := New(":0")
	session, err := rec.StartSession("test", "Decompress Test", SessionFilters{}, nil)
	if err != nil {
		t.Fatal(err)
	}

	payload := []byte(`{"token":"session-token-abcdef123456"}`)

	cases := []struct {
		name     string
		encoding string
		body     []byte
	}{
		{"gzip", "gzip", gzipBytes(t, payload)},
		{"deflate-zlib", "deflate", zlibBytes(t, payload)},
		{"brotli", "br", brotliBytes(t, payload)},
	}

	for i, tc := range cases {
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://example.com/api/login%d", i), nil)
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type":     []string{"application/json"},
				"Content-Encoding": []string{tc.encoding},
				"Content-Length":   []string{fmt.Sprintf("%d", len(tc.body))},
			},
		}
		rec.recordRequest(session, req, nil, resp, tc.body, 10*time.Millisecond)

		recorded := session.Requests[i]
		if recorded.ResponseBody != string(payload) {
			t.Fatalf("%s: expected decompressed body %q, got %q", tc.name, payload, recorded.ResponseBody)
		}
		if recorded.ResponseBodySizeBytes != len(tc.body) {
			t.Fatalf("%s: expected raw size %d, got %d", tc.name, len(tc.body), recorded.ResponseBodySizeBytes)
		}
		if recorded.ResponseHeaders["Content-Encoding"] != tc.encoding {
			t.Fatalf("%s: captured wire Content-Encoding was not retained", tc.name)
		}
		if recorded.ResponseHeaders["Content-Length"] != fmt.Sprintf("%d", len(tc.body)) {
			t.Fatalf("%s: captured wire Content-Length was not retained", tc.name)
		}
	}
}

func TestDecodeBody(t *testing.T) {
	payload := []byte("hello world correlation payload")

	if got, ok := decodeBody(gzipBytes(t, payload), "gzip", defaultBodyCaptureLimitBytes); !ok || string(got) != string(payload) {
		t.Fatalf("gzip decode failed: ok=%v got=%q", ok, got)
	}
	if got, ok := decodeBody(zlibBytes(t, payload), "deflate", defaultBodyCaptureLimitBytes); !ok || string(got) != string(payload) {
		t.Fatalf("deflate decode failed: ok=%v got=%q", ok, got)
	}
	// Unencoded body is returned untouched.
	if got, ok := decodeBody(payload, "", defaultBodyCaptureLimitBytes); ok || string(got) != string(payload) {
		t.Fatalf("identity decode should be a no-op: ok=%v got=%q", ok, got)
	}
	if got, ok := decodeBody(brotliBytes(t, payload), "br", defaultBodyCaptureLimitBytes); !ok || string(got) != string(payload) {
		t.Fatalf("brotli decode failed: ok=%v got=%q", ok, got)
	}
	// Malformed gzip falls back to raw without panicking.
	if got, ok := decodeBody([]byte("not actually gzip"), "gzip", defaultBodyCaptureLimitBytes); ok || string(got) != "not actually gzip" {
		t.Fatalf("malformed gzip should fall back to raw: ok=%v got=%q", ok, got)
	}
	// Malformed brotli falls back to raw without panicking.
	if got, ok := decodeBody([]byte("not actually brotli"), "br", defaultBodyCaptureLimitBytes); ok || string(got) != "not actually brotli" {
		t.Fatalf("malformed brotli should fall back to raw: ok=%v got=%q", ok, got)
	}
}

func TestSanitizeHeadersUsesSharedSensitiveHeaderRules(t *testing.T) {
	headers := http.Header{
		"X-Session-Token": {"session-secret"},
		"X-Credential":    {"credential-secret"},
		"Set-Cookie":      {"session=captured-cookie"},
		"X-Safe":          {"safe"},
	}
	sanitized := sanitizeHeaders(headers, nil, false, true)
	if sanitized["X-Session-Token"][0] != "[redacted]" || sanitized["X-Credential"][0] != "[redacted]" {
		t.Fatalf("custom sensitive headers were disclosed: %#v", sanitized)
	}
	if sanitized["Set-Cookie"][0] != "session=captured-cookie" {
		t.Fatalf("capture_cookies opt-in was ignored: %#v", sanitized["Set-Cookie"])
	}
	if sanitized["X-Safe"][0] != "safe" {
		t.Fatalf("safe header changed: %#v", sanitized)
	}
	raw := correlationHeaders(headers, nil, false, true)
	if raw["X-Session-Token"][0] != "session-secret" {
		t.Fatalf("private correlation view lost the raw token: %#v", raw)
	}
}

func TestCapturedBodyTruncatesAtUTF8BoundaryAndPreservesInvalidBytes(t *testing.T) {
	body, truncated, encoding := capturedBody([]byte("a€b"), "text/plain", 2)
	if body != "a" || !truncated || encoding != "" {
		t.Fatalf("split UTF-8 capture = %q/%t/%q, want valid prefix a", body, truncated, encoding)
	}

	invalid := []byte{'a', 0xff, 'b'}
	body, truncated, encoding = capturedBody(invalid, "text/plain", len(invalid))
	if body != base64.StdEncoding.EncodeToString(invalid) || truncated || encoding != "base64" {
		t.Fatalf("invalid text capture = %q/%t/%q, want lossless base64", body, truncated, encoding)
	}
}

func TestIsTextContent(t *testing.T) {
	tests := []struct {
		ct     string
		expect bool
	}{
		{"application/json", true},
		{"text/html", true},
		{"text/plain", true},
		{"application/xml", true},
		{"image/png", false},
		{"application/octet-stream", false},
		{"application/x-www-form-urlencoded", true},
	}

	for _, tt := range tests {
		got := isTextContent(tt.ct)
		if got != tt.expect {
			t.Errorf("isTextContent(%q) = %v, want %v", tt.ct, got, tt.expect)
		}
	}
}

// TestRecorderStatusReadWriteIsRaceFree exercises the fix making s.mu the owning
// lock for Session.Status/StoppedAt: snapshot reads (GetSession) must not race the
// lifecycle writers (Pause/Resume/Stop). Run under go test -race.
func TestRecorderStatusReadWriteIsRaceFree(t *testing.T) {
	r := New("127.0.0.1:0")
	if _, err := r.StartSession("s1", "race", SessionFilters{}, nil); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				_, _ = r.GetSession("s1")
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 200; j++ {
			_, _ = r.PauseSession("s1")
			_, _ = r.ResumeSession("s1")
		}
		_, _ = r.StopSession("s1")
	}()
	wg.Wait()
}
