package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

var basicCredentialPattern = regexp.MustCompile(`Basic ([A-Za-z0-9+/=]+)`)

// NTLM/Negotiate auth sends a pre-challenge Basic header the adapter builds
// from DOMAIN\user:password. The script never authored that header, so with
// config.log.redact_run_evidence off a {{secret.*}} password must still not
// reach run evidence in any base64 alignment, from action or config auth.
func TestReplayIntegratedAuthPreChallengeBasicStaysMaskedWithAuthoredEvidenceVisible(t *testing.T) {
	registerScriptRequestScope()
	const platformPassword = "PLATFORM-NTLM-PW-7781"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	type authCase struct {
		name    string
		auth    dsl.AuthConfig
		cfgAuth bool
	}
	var cases []authCase
	for _, authType := range []string{"ntlm", "negotiate", "negotiate_any"} {
		// "alice:" is 6 bytes (aligned), "bob:" 4 and "alice1:" 7 (not aligned).
		for _, user := range []string{"alice", "bob", "alice1"} {
			for _, domain := range []string{"", "CORP"} {
				for _, cfgAuth := range []bool{false, true} {
					where := "action"
					if cfgAuth {
						where = "config"
					}
					cases = append(cases, authCase{
						name:    where + "-" + authType + "-" + user + "-" + domain,
						auth:    dsl.AuthConfig{Type: authType, Username: user, Password: "{{secret.ntlm_pw}}", Domain: domain},
						cfgAuth: cfgAuth,
					})
				}
			}
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}}
			action := dsl.Action{Name: "Get", Type: "GET", Target: "/x", Headers: map[string]string{"X-Trace": "authored-trace-7782"}}
			auth := tc.auth
			if tc.cfgAuth {
				cfg.HTTP.Auth = auth
			} else {
				action.Auth = &auth
			}
			script := setRunEvidenceRedaction(&dsl.Script{Name: "ntlm", Protocol: scopeHTTPProtocol, Config: cfg, Actions: []dsl.Action{action}}, false)
			opts := scopeDebugOptions("ntlm-" + tc.name)
			opts.SecretResolver = SecretResolverFunc(func(name string) (string, bool) { return platformPassword, name == "ntlm_pw" })
			report, err := ReplayScript(context.Background(), script, opts)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			text := string(encoded)
			if strings.Contains(text, platformPassword) {
				t.Fatalf("raw platform password in report: %s", text)
			}
			for _, match := range basicCredentialPattern.FindAllStringSubmatch(text, -1) {
				raw, _ := base64.StdEncoding.DecodeString(match[1])
				if strings.Contains(string(raw), platformPassword) {
					t.Fatalf("platform password published as Basic composite: %q", raw)
				}
			}
			sawScriptRequest := false
			for _, event := range report.Events {
				for _, entry := range event.Network {
					if !entry.AuthoredRequestRetained {
						continue
					}
					sawScriptRequest = true
					if got := entry.RequestHeaders["X-Trace"]; got != "authored-trace-7782" {
						t.Fatalf("authored header X-Trace = %q, want it visible: %v", got, entry.RequestHeaders)
					}
				}
			}
			if !sawScriptRequest {
				t.Fatalf("no authored request entry in report: %s", text)
			}
		})
	}
}
