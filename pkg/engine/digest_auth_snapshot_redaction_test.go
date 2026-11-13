package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A Digest response is a keyed hash over user:realm:password that no exact
// matcher can find. With config.log.redact_run_evidence off it is shown only
// for a script-literal password: a password that reaches the action through a
// variable assigned from {{secret.*}} must keep "Digest [REDACTED]".
func TestReplayDigestSnapshotHidesPlatformPasswordFromVariableWithAuthoredEvidenceVisible(t *testing.T) {
	registerScriptRequestScope()
	const platformPassword = "PLATFORM-DIGEST-PW-7791"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Digest ") {
			w.Header().Set("WWW-Authenticate", `Digest realm="apex", nonce="nonce-7791", qop="auth", algorithm=MD5`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name     string
		password string
		saveFrom string
		masked   bool
		// scriptLevel puts the credentials in config.http.auth instead of
		// the action's auth block.
		scriptLevel bool
	}{
		{name: "variable-from-secret", password: "{{var.pw}}", saveFrom: "{{secret.pw}}", masked: true},
		{name: "direct-secret", password: "{{secret.pw}}", masked: true},
		{name: "script-literal", password: "literal-digest-pw-7792", masked: false},
		{name: "variable-from-literal", password: "{{var.pw}}", saveFrom: "literal-digest-pw-7793", masked: false},
		{name: "script-level-literal", password: "literal-digest-pw-7794", masked: false, scriptLevel: true},
		{name: "script-level-secret", password: "{{secret.pw}}", masked: true, scriptLevel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var actions []dsl.Action
			if tc.saveFrom != "" {
				actions = append(actions, dsl.Action{
					Name: "SavePW", Type: "source_parameter",
					Params: map[string]any{"function": "lr_save_string", "args": []any{tc.saveFrom, "pw"}},
				})
			}
			auth := &dsl.AuthConfig{Type: "digest", Username: "digest-user", Password: tc.password}
			get := dsl.Action{Name: "Get", Type: "GET", Target: "/private"}
			if !tc.scriptLevel {
				get.Auth = auth
			}
			actions = append(actions, get)
			cfg := dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}}
			if tc.scriptLevel {
				cfg.HTTP.Auth = *auth
			}
			script := setRunEvidenceRedaction(&dsl.Script{Name: "digest", Protocol: scopeHTTPProtocol, Config: cfg, Actions: actions}, false)
			opts := scopeDebugOptions("digest-" + tc.name)
			opts.SecretResolver = SecretResolverFunc(func(name string) (string, bool) { return platformPassword, name == "pw" })
			report, err := ReplayScript(context.Background(), script, opts)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), platformPassword) {
				t.Fatalf("raw platform password in report: %s", encoded)
			}
			var authorization string
			found := false
			for _, event := range report.Events {
				if event.Snapshot != nil && event.Snapshot.RequestHeaders != nil {
					if value, ok := event.Snapshot.RequestHeaders["Authorization"]; ok {
						authorization, found = value, true
					}
				}
			}
			if !found {
				t.Fatalf("no snapshot Authorization header in report: %s", encoded)
			}
			if tc.masked {
				if authorization != "Digest [REDACTED]" {
					t.Fatalf("snapshot Authorization = %q, want Digest [REDACTED]", authorization)
				}
				if strings.Contains(string(encoded), `response=\"`) {
					t.Fatalf("report carries a Digest response hash: %s", encoded)
				}
				return
			}
			if !strings.HasPrefix(authorization, "Digest ") || !strings.Contains(authorization, "response=") {
				t.Fatalf("snapshot Authorization = %q, want the script-literal Digest header visible", authorization)
			}
		})
	}
}
