package http

import (
	"context"
	"github.com/jasonczech/apexload/pkg/protocol"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestExplicitPOSTResourcesAndPositionalVerification(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/asset" {
			hits.Add(1)
			if r.Referer() != "https://source.example/path" {
				t.Errorf("referer=%q", r.Referer())
			}
			if c, err := r.Cookie("session"); err != nil || c.Value != "fresh" {
				t.Error("parent cookie not propagated")
			}
			time.Sleep(10 * time.Millisecond)
			io.WriteString(w, "RESOURCE ERROR")
			return
		}
		if r.Method != "POST" {
			t.Errorf("method=%s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "a=one&a=two%20words" {
			t.Errorf("body=%q", body)
		}
		w.Header().Set("Content-Type", "application/json")
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "fresh"})
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	h := &HTTP{}
	if err := h.Init(context.Background(), map[string]any{}); err != nil {
		t.Fatal(err)
	}
	defer h.Teardown(context.Background())
	a := &protocol.Action{Type: "http_request", Name: "submit", Method: "POST", Target: server.URL, Body: []byte("a=one&a=two%20words"), Params: map[string]any{"resources": []any{map[string]any{"url": server.URL + "/asset", "referer": "https://source.example/path"}}}}
	execute := func(success bool) {
		t.Helper()
		result, err := h.Execute(context.Background(), a)
		if err != nil {
			t.Fatal(err)
		}
		if result.Success != success {
			t.Fatalf("success=%v error=%s", result.Success, result.ErrorMessage)
		}
		if result.Duration < 10*time.Millisecond {
			t.Fatal("resource duration excluded")
		}
	}
	execute(true) // init has no retrospectively registered checks
	if err := h.ConfigureVerification(protocol.Verification{Operation: "register", ID: "errors", Text: "resource error", Scope: "all", CaseInsensitive: true, FailIfFound: true}); err != nil {
		t.Fatal(err)
	}
	execute(false)
	if err := h.ConfigureVerification(protocol.Verification{Operation: "pause", ID: "errors"}); err != nil {
		t.Fatal(err)
	}
	execute(true)
	if err := h.ConfigureVerification(protocol.Verification{Operation: "resume", ID: "errors"}); err != nil {
		t.Fatal(err)
	}
	execute(false)
	if err := h.ConfigureVerification(protocol.Verification{Operation: "register", ID: "errors", Text: "ERROR", Scope: "noresource", FailIfFound: true}); err != nil {
		t.Fatal(err)
	}
	execute(true)
	if hits.Load() != 5 {
		t.Fatalf("resource hits=%d", hits.Load())
	}
}

func TestVerificationRedirectAndVUserIsolation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Location", "/final")
			w.Header().Set("X-Error", "found")
			w.WriteHeader(302)
			return
		}
		io.WriteString(w, "okay")
	}))
	defer server.Close()
	for i := 0; i < 2; i++ {
		h := &HTTP{}
		if err := h.Init(context.Background(), map[string]any{}); err != nil {
			t.Fatal(err)
		}
		defer h.Teardown(context.Background())
		if i == 0 {
			_ = h.ConfigureVerification(protocol.Verification{Operation: "register", ID: "redirect", Text: "X-Error", Scope: "headers", FailIfFound: true})
		}
		result, err := h.Execute(context.Background(), &protocol.Action{Type: "GET", Method: "GET", Target: server.URL})
		if err != nil || result.Success != (i == 1) {
			t.Fatalf("VU %d result=%+v err=%v", i, result, err)
		}
	}
}

func TestExplicitResourceAuthoringTemplatesAndFilters(t *testing.T) {
	params := map[string]any{"resources": []any{map[string]any{"url": "https://{{var.host}}/{{param.asset}}", "referer": "{{var.ref}}"}}}
	if err := ValidateExplicitResourceTemplates(params); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseExplicitResources(params); err == nil {
		t.Fatal("unsubstituted host accepted at replay")
	}
	fetcher := newResourceFetcher(&HTTP{}, map[string]any{"web_download_filters_mode": "block_list", "web_download_filters_hosts": []string{"blocked.test"}})
	if fetcher == nil || !fetcher.blockedByDownloadFilters("https://blocked.test/asset") {
		t.Fatal("explicit-only resource filters lost")
	}
}

func TestVerificationSnapshotPreservesPausedRegistration(t *testing.T) {
	parent, branch := &HTTP{}, &HTTP{}
	check := protocol.Verification{Operation: "register", ID: "paused", Text: "ERROR", Scope: "body", FailIfFound: true}
	if err := parent.ConfigureVerification(check); err != nil {
		t.Fatal(err)
	}
	if err := parent.ConfigureVerification(protocol.Verification{Operation: "pause", ID: "paused"}); err != nil {
		t.Fatal(err)
	}
	for _, operation := range parent.VerificationSnapshot() {
		if err := branch.ConfigureVerification(operation); err != nil {
			t.Fatal(err)
		}
	}
	if _, observation := branch.verificationContext(context.Background()); observation != nil {
		t.Fatal("cloned check was prematurely resumed")
	}
	if err := branch.ConfigureVerification(protocol.Verification{Operation: "resume", ID: "paused"}); err != nil {
		t.Fatal(err)
	}
	ctx, observation := branch.verificationContext(context.Background())
	observeVerification(ctx, nil, []byte("ERROR"), false, true)
	if observation.err() == nil {
		t.Fatal("resumed check lost its definition")
	}
	if _, observation := parent.verificationContext(context.Background()); observation != nil {
		t.Fatal("branch resumed its parent")
	}
}

func TestExplicitResourceRefererWinsOverHTMLDiscovery(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/asset" {
			hits.Add(1)
			if got := r.Referer(); got != "https://recorded.example/origin" {
				t.Errorf("explicit referer replaced by discovery: %q", got)
			}
			io.WriteString(w, "asset")
			return
		}
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, `<html><img src="/asset"></html>`)
	}))
	defer server.Close()
	h := initHTTP(t, map[string]any{"http_download_non_html_resources": true})
	defer h.Teardown(context.Background())
	result, err := h.Execute(context.Background(), &protocol.Action{Type: "http_request", Method: "GET", Target: server.URL, Params: map[string]any{"resources": []any{map[string]any{"url": "/asset", "referer": "https://recorded.example/origin"}}}})
	if err != nil || !result.Success || hits.Load() != 1 {
		t.Fatalf("result=%+v err=%v resource hits=%d", result, err, hits.Load())
	}
}
