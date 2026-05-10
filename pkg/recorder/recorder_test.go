package recorder

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewRecorder(t *testing.T) {
	r := New(":9999")
	if r.addr != ":9999" {
		t.Fatalf("expected :9999, got %s", r.addr)
	}

	r2 := New("")
	if r2.addr != ":8081" {
		t.Fatalf("expected default :8081, got %s", r2.addr)
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

func TestProxyIntegration(t *testing.T) {
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

func TestStaticFiltering(t *testing.T) {
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

func TestHostFiltering(t *testing.T) {
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
