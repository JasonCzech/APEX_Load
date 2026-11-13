package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// net/http synthesizes "Authorization: Basic ..." from URL userinfo inside
// Client.send. That header is built from script-resolved halves and is recorded
// as it went on the wire: the engine's exact matcher withholds it when a half
// carries a platform value, and otherwise it follows the setting.
func TestReplayHARRecordsUserinfoSynthesizedAuthorization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	target := strings.Replace(server.URL, "http://", "http://alice1:userinfo-pw-4474@", 1) + "/path"

	t.Run("synthesized from userinfo", func(t *testing.T) {
		h := scriptRequestScopeHTTP(t, server.URL)
		result, err := h.Execute(context.Background(), &protocol.Action{Name: "get", Type: "GET", Method: "GET", Target: target})
		if err != nil || result == nil || len(result.NetworkEntries) != 1 {
			t.Fatalf("%+v %v", result, err)
		}
		entry := result.NetworkEntries[0]
		if !entry.ScriptRequest || !strings.HasPrefix(entry.RequestHeaders["Authorization"], "Basic ") {
			t.Fatalf("userinfo Basic ScriptRequest=%v headers=%v", entry.ScriptRequest, entry.RequestHeaders)
		}
	})
	t.Run("authored Authorization beside userinfo", func(t *testing.T) {
		h := scriptRequestScopeHTTP(t, server.URL)
		result, err := h.Execute(context.Background(), &protocol.Action{Name: "get", Type: "GET", Method: "GET", Target: target, Headers: map[string]string{"Authorization": "Bearer authored-lit-4475"}})
		if err != nil || result == nil || len(result.NetworkEntries) != 1 {
			t.Fatalf("%+v %v", result, err)
		}
		entry := result.NetworkEntries[0]
		if entry.RequestHeaders["Authorization"] != "Bearer authored-lit-4475" {
			t.Fatalf("authored Authorization headers=%v", entry.RequestHeaders)
		}
	})
}

// A Digest retry clones the challenged request, which already carries the
// cookies net/http's jar added. With config.log.redact_run_evidence off the
// request snapshot shows what the script authored plus the Digest header, never
// a server-issued jar cookie.
func TestHTTPDigestSnapshotOmitsJarCookieWithAuthoredEvidenceVisible(t *testing.T) {
	const serverSession = "server-minted-sess-55501"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "JSESSIONID", Value: serverSession, Path: "/"})
			w.WriteHeader(http.StatusOK)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Digest ") {
			w.Header().Set("WWW-Authenticate", `Digest realm="apex", nonce="nonce-55501", qop="auth", algorithm=MD5`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{name: "no authored headers"},
		{name: "authored cookie", headers: map[string]string{"Cookie": "pref=authored-lit-55503"}, want: "pref=authored-lit-55503"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := initHTTP(t, map[string]any{
				"base_url":                               server.URL,
				protocol.RedactAuthoredEvidenceConfigKey: false,
			})
			defer h.Teardown(context.Background())
			h.SetRuntimeAuthoredTextRedactor(func(value string) string { return value })
			if _, err := h.Execute(context.Background(), &protocol.Action{Name: "login", Type: "GET", Method: "GET", Target: "/login"}); err != nil {
				t.Fatal(err)
			}
			result, err := h.Execute(context.Background(), &protocol.Action{
				Name: "private", Type: "GET", Method: "GET", Target: "/private", Headers: tc.headers,
				Auth: &protocol.AuthConfig{Type: "digest", Username: "digest-user", Password: "literal-digest-pw-55502"},
			})
			if err != nil || result == nil || result.Snapshot == nil {
				t.Fatalf("%+v %v", result, err)
			}
			headers := result.Snapshot.RequestHeaders
			if !strings.HasPrefix(headers["Authorization"], "Digest ") {
				t.Fatalf("snapshot Authorization = %q, want a Digest header", headers["Authorization"])
			}
			for key, value := range headers {
				if strings.Contains(value, serverSession) {
					t.Fatalf("snapshot header %s publishes the server-issued jar cookie: %q", key, value)
				}
			}
			if headers["Cookie"] != tc.want {
				t.Fatalf("snapshot Cookie = %q, want the authored %q", headers["Cookie"], tc.want)
			}
		})
	}
}
