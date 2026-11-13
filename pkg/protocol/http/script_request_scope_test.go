package http

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Only the action's own top-level request is script-authored
// (NetworkEntry.ScriptRequest). Page resources and meta-refresh follow-ups go to
// URLs the server named, and a jar-filled Cookie header carries cookies the
// server issued, so those keep the full scrub whatever
// config.log.redact_run_evidence says.

func scriptRequestScopeHTTP(t *testing.T, baseURL string) *HTTP {
	t.Helper()
	capture, err := protocol.NewReplayHARCapture(protocol.ReplayHAROptions{Bodies: "both", Redirects: true})
	if err != nil {
		t.Fatal(err)
	}
	h := initHTTP(t, map[string]any{
		"base_url":                               baseURL,
		"max_meta_refresh":                       2,
		protocol.ReplayNetworkCaptureConfigKey:   true,
		protocol.ReplayHARCaptureConfigKey:       capture,
		protocol.RedactAuthoredEvidenceConfigKey: false,
	})
	t.Cleanup(func() { _ = h.Teardown(context.Background()) })
	return h
}

func TestReplayHARResourceEntriesAreNotScriptRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/page" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body>page</body></html>`))
			return
		}
		w.Header().Set("Content-Type", "text/css")
		_, _ = w.Write([]byte(`body{}`))
	}))
	defer server.Close()
	h := scriptRequestScopeHTTP(t, server.URL)
	result, err := h.Execute(context.Background(), &protocol.Action{
		Name: "page", Type: "GET", Method: "GET", Target: "/page",
		Params: map[string]any{"resources": []any{map[string]any{"url": "/sheet.css?access_token=PAGE-TOKEN-1"}}},
	})
	if err != nil || result == nil {
		t.Fatalf("execute: %+v %v", result, err)
	}
	var main, resource *protocol.NetworkEntry
	for i := range result.NetworkEntries {
		entry := &result.NetworkEntries[i]
		switch {
		case strings.Contains(entry.URL, "/page"):
			main = entry
		case strings.Contains(entry.URL, "/sheet.css"):
			resource = entry
		}
	}
	if main == nil || resource == nil {
		t.Fatalf("entries = %+v", result.NetworkEntries)
	}
	if !main.ScriptRequest {
		t.Fatal("the action's own request lost ScriptRequest")
	}
	if resource.ScriptRequest {
		t.Fatalf("page resource %q flagged ScriptRequest", resource.URL)
	}
}

func TestReplayHARMetaRefreshFollowUpIsNotScriptRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/refresh":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<meta http-equiv="refresh" content="0;url=/final?token=PAGE-TOKEN-2">`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("fail"))
		}
	}))
	defer server.Close()
	h := scriptRequestScopeHTTP(t, server.URL)
	result, err := h.Execute(context.Background(), &protocol.Action{Name: "refresh", Type: "GET", Method: "GET", Target: "/refresh"})
	if err != nil || result == nil || len(result.NetworkEntries) != 2 {
		t.Fatalf("entries = %+v err %v", result, err)
	}
	if !result.NetworkEntries[0].ScriptRequest {
		t.Fatal("the action's own request lost ScriptRequest")
	}
	follow := result.NetworkEntries[1]
	if !strings.Contains(follow.URL, "/final") || follow.ScriptRequest {
		t.Fatalf("meta-refresh follow-up %q ScriptRequest=%v, want false", follow.URL, follow.ScriptRequest)
	}
	if result.Snapshot == nil || !strings.Contains(result.Snapshot.RequestURL, "/final") || !result.Snapshot.RequestURLNotAuthored {
		t.Fatalf("follow-up snapshot must be marked server-named: %+v", result.Snapshot)
	}
}

func TestReplayHARScriptRequestFlagsJarCookies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "SESSIONID", Value: "SERVER-ISSUED-SESSION", Path: "/"})
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	t.Run("jar cookie only", func(t *testing.T) {
		h := scriptRequestScopeHTTP(t, server.URL)
		if _, err := h.Execute(context.Background(), &protocol.Action{Name: "login", Type: "GET", Method: "GET", Target: "/login"}); err != nil {
			t.Fatal(err)
		}
		result, err := h.Execute(context.Background(), &protocol.Action{Name: "use", Type: "GET", Method: "GET", Target: "/use"})
		if err != nil || len(result.NetworkEntries) != 1 {
			t.Fatalf("%+v %v", result, err)
		}
		entry := result.NetworkEntries[0]
		if !entry.ScriptRequest || !strings.Contains(entry.RequestHeaders["Cookie"], "SERVER-ISSUED-SESSION") || !entry.RequestCookieNotAuthored {
			t.Fatalf("jar cookie entry ScriptRequest=%v NotAuthored=%v headers=%v", entry.ScriptRequest, entry.RequestCookieNotAuthored, entry.RequestHeaders)
		}
	})
	t.Run("authored cookie extended by the jar", func(t *testing.T) {
		h := scriptRequestScopeHTTP(t, server.URL)
		if _, err := h.Execute(context.Background(), &protocol.Action{Name: "login", Type: "GET", Method: "GET", Target: "/login"}); err != nil {
			t.Fatal(err)
		}
		result, err := h.Execute(context.Background(), &protocol.Action{Name: "use", Type: "GET", Method: "GET", Target: "/use", Headers: map[string]string{"Cookie": "pref=authored-lit"}})
		if err != nil || len(result.NetworkEntries) != 1 {
			t.Fatalf("%+v %v", result, err)
		}
		if entry := result.NetworkEntries[0]; !entry.RequestCookieNotAuthored {
			t.Fatalf("jar-extended Cookie not flagged: %v", entry.RequestHeaders)
		}
	})
	t.Run("authored cookie alone", func(t *testing.T) {
		h := scriptRequestScopeHTTP(t, server.URL)
		result, err := h.Execute(context.Background(), &protocol.Action{Name: "use", Type: "GET", Method: "GET", Target: "/use", Headers: map[string]string{"Cookie": "pref=authored-lit"}})
		if err != nil || len(result.NetworkEntries) != 1 {
			t.Fatalf("%+v %v", result, err)
		}
		entry := result.NetworkEntries[0]
		if entry.RequestCookieNotAuthored || entry.RequestHeaders["Cookie"] != "pref=authored-lit" {
			t.Fatalf("authored Cookie flagged: NotAuthored=%v headers=%v", entry.RequestCookieNotAuthored, entry.RequestHeaders)
		}
	})
}

// A typed Basic/Bearer header built from script-literal credentials is shown in
// the request snapshot and the replay HAR when config.log.redact_run_evidence
// is off; one built from a platform-managed secret keeps the placeholder:
// RedactedAuth differs (a {{secret.*}} reference), or the bound authored
// redactor changes a half (a variable assigned from {{secret.*}}). Without a
// bound redactor origin cannot be proven, so the placeholder stays.
func TestHTTPSnapshotTypedAuthFollowsRunEvidenceSetting(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	literalBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("lit-user:lit-pass-8801"))
	identity := protocol.TextRedactor(func(value string) string { return value })
	masksPlatform := protocol.TextRedactor(func(value string) string {
		return strings.ReplaceAll(value, "platform-pass-8806", "[REDACTED]")
	})
	for _, tc := range []struct {
		name     string
		redact   bool
		auth     *protocol.AuthConfig
		redacted *protocol.AuthConfig
		redactor protocol.TextRedactor
		want     string
	}{
		{name: "basic literal off", auth: &protocol.AuthConfig{Type: "basic", Username: "lit-user", Password: "lit-pass-8801"}, redacted: &protocol.AuthConfig{Type: "basic", Username: "lit-user", Password: "lit-pass-8801"}, redactor: identity, want: literalBasic},
		{name: "basic literal off no redactor", auth: &protocol.AuthConfig{Type: "basic", Username: "lit-user", Password: "lit-pass-8801"}, redacted: &protocol.AuthConfig{Type: "basic", Username: "lit-user", Password: "lit-pass-8801"}, want: "Basic [REDACTED]"},
		{name: "basic literal on", redact: true, auth: &protocol.AuthConfig{Type: "basic", Username: "lit-user", Password: "lit-pass-8801"}, redacted: &protocol.AuthConfig{Type: "basic", Username: "lit-user", Password: "lit-pass-8801"}, redactor: identity, want: "Basic [REDACTED]"},
		{name: "basic platform off", auth: &protocol.AuthConfig{Type: "basic", Username: "lit-user", Password: "platform-pass-8802"}, redacted: &protocol.AuthConfig{Type: "basic", Username: "lit-user", Password: "[REDACTED]"}, redactor: identity, want: "Basic [REDACTED]"},
		{name: "basic platform via variable off", auth: &protocol.AuthConfig{Type: "basic", Username: "lit-user", Password: "platform-pass-8806"}, redacted: &protocol.AuthConfig{Type: "basic", Username: "lit-user", Password: "platform-pass-8806"}, redactor: masksPlatform, want: "Basic [REDACTED]"},
		{name: "bearer literal off", auth: &protocol.AuthConfig{Type: "bearer", Token: "lit-tok-8803"}, redacted: &protocol.AuthConfig{Type: "bearer", Token: "lit-tok-8803"}, redactor: identity, want: "Bearer lit-tok-8803"},
		{name: "bearer platform off", auth: &protocol.AuthConfig{Type: "bearer", Token: "platform-tok-8804"}, redacted: &protocol.AuthConfig{Type: "bearer", Token: "[REDACTED]"}, redactor: identity, want: "Bearer [REDACTED]"},
		{name: "bearer origin unknown off", auth: &protocol.AuthConfig{Type: "bearer", Token: "unknown-tok-8805"}, redactor: identity, want: "Bearer [REDACTED]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture, err := protocol.NewReplayHARCapture(protocol.ReplayHAROptions{Bodies: "both", Redirects: true})
			if err != nil {
				t.Fatal(err)
			}
			h := initHTTP(t, map[string]any{
				"base_url":                               server.URL,
				protocol.RedactAuthoredEvidenceConfigKey: tc.redact,
				protocol.ReplayNetworkCaptureConfigKey:   true,
				protocol.ReplayHARCaptureConfigKey:       capture,
			})
			defer h.Teardown(context.Background())
			if tc.redactor != nil {
				h.SetRuntimeAuthoredTextRedactor(tc.redactor)
			}
			result, err := h.Execute(context.Background(), &protocol.Action{Name: "auth", Type: "GET", Method: "GET", Target: "/x", Auth: tc.auth, RedactedAuth: tc.redacted})
			if err != nil || result == nil || result.Snapshot == nil {
				t.Fatalf("%+v %v", result, err)
			}
			if got := result.Snapshot.RequestHeaders["Authorization"]; got != tc.want {
				t.Fatalf("snapshot Authorization = %q, want %q", got, tc.want)
			}
			if tc.redact {
				// With the setting on the engine name-masks the HAR header.
				return
			}
			if len(result.NetworkEntries) == 0 {
				t.Fatalf("no network entry")
			}
			if got := result.NetworkEntries[0].RequestHeaders["Authorization"]; got != tc.want {
				t.Fatalf("network Authorization = %q, want %q", got, tc.want)
			}
		})
	}
}
