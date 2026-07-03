package recorder

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
	gorillawebsocket "github.com/gorilla/websocket"
)

func TestNewRecorder(t *testing.T) {
	r := New(":9999")
	if r.addr != ":9999" {
		t.Fatalf("expected :9999, got %s", r.addr)
	}

	r2 := New("")
	if r2.addr != defaultProxyAddr {
		t.Fatalf("expected default %s, got %s", defaultProxyAddr, r2.addr)
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
	if recorded.ResponseBodyEncoding != "base64" {
		t.Fatalf("response body encoding = %q, want base64", recorded.ResponseBodyEncoding)
	}
	if recorded.ResponseBody != base64.StdEncoding.EncodeToString(respBody[:4]) {
		t.Fatalf("response body = %q, want base64 of limited bytes", recorded.ResponseBody)
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

	converted := ConvertSession(session, "")
	action := converted.Script.Actions[0]
	if action.BodyEncoding != "base64" {
		t.Fatalf("converted body encoding = %q, want base64", action.BodyEncoding)
	}
	decodedAction, err := base64.StdEncoding.DecodeString(action.Body)
	if err != nil {
		t.Fatalf("decode converted body: %v", err)
	}
	if !bytes.Equal(decodedAction, reqBody) {
		t.Fatalf("converted multipart body did not round-trip")
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
			if recorded.ResponseBody != "data: hello\n\n" {
				t.Fatalf("captured SSE body = %q", recorded.ResponseBody)
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
	if recorded.ResponseBody != payload[:8] {
		t.Fatalf("recorded response body = %q, want capped prefix", recorded.ResponseBody)
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
		if _, ok := recorded.ResponseHeaders["Content-Encoding"]; ok {
			t.Fatalf("%s: expected Content-Encoding header to be stripped", tc.name)
		}
		if _, ok := recorded.ResponseHeaders["Content-Length"]; ok {
			t.Fatalf("%s: expected Content-Length header to be stripped", tc.name)
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
