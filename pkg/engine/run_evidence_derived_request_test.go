package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// reportFirstNetworkEntries returns the replay report's ScriptRequest-side network
// entries: the first network entry of every event.
func reportFirstNetworkEntries(t *testing.T, report *ReplayReport) []protocol.NetworkEntry {
	t.Helper()
	var out []protocol.NetworkEntry
	for _, event := range report.Events {
		if len(event.Network) > 0 {
			out = append(out, event.Network[0])
		}
	}
	if len(out) == 0 {
		t.Fatalf("report has no network entries")
	}
	return out
}

// An Authorization header net/http builds from script-literal URL userinfo, and
// the NTLM/Negotiate pre-challenge Basic built from a script-literal auth block,
// are script-authored credentials: the request-header surface itself (not a
// response echo) follows config.log.redact_run_evidence in an ordinary replay.
func TestReplayLiteralSynthesizedAuthorizationFollowsRunEvidenceSetting(t *testing.T) {
	registerScriptRequestScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		// No echo of the request: the assertion is on the request surface.
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	const literalPassword = "literal-synth-pw-6611"
	cases := []struct {
		name   string
		user   string
		action dsl.Action
	}{
		{name: "userinfo", user: "bob6", action: dsl.Action{Name: "Get", Type: "GET", Target: strings.Replace(server.URL, "http://", "http://bob6:"+literalPassword+"@", 1) + "/p"}},
		{name: "ntlm", user: "carol6", action: dsl.Action{Name: "Get", Type: "GET", Target: server.URL + "/p", Auth: &dsl.AuthConfig{Type: "ntlm", Username: "carol6", Password: literalPassword}}},
		{name: "negotiate", user: "dave6", action: dsl.Action{Name: "Get", Type: "GET", Target: server.URL + "/p", Auth: &dsl.AuthConfig{Type: "negotiate", Username: "dave6", Password: literalPassword}}},
	}
	for _, tc := range cases {
		composite := "Basic " + base64.StdEncoding.EncodeToString([]byte(tc.user+":"+literalPassword))
		for _, redact := range []bool{false, true} {
			name := tc.name + "/setting-off"
			if redact {
				name = tc.name + "/setting-on"
			}
			t.Run(name, func(t *testing.T) {
				script := setRunEvidenceRedaction(&dsl.Script{
					Name: "synth-" + tc.name, Protocol: scopeHTTPProtocol,
					Actions: []dsl.Action{tc.action},
				}, redact)
				report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("synth-"+name, false, map[string]string{"other": "unused-platform-value-6612"}))
				if err != nil {
					t.Fatal(err)
				}
				entry := reportFirstNetworkEntries(t, report)[0]
				got := entry.RequestHeaders["Authorization"]
				if redact {
					if strings.Contains(got, literalPassword) || strings.Contains(got, composite[len("Basic "):]) {
						t.Fatalf("script-literal Authorization visible with the setting on: %q", got)
					}
					return
				}
				if got != composite {
					t.Fatalf("script-literal synthesized Authorization = %q with the setting off, want %q", got, composite)
				}
			})
		}
	}
}

// A base64 full-body template {{secret.NAME}} decodes, on the wire, to a value
// that is wholly derived from the platform secret. The decoded text is masked
// in every mode, whatever the JSON key it sits under, including the unredacted
// debugger.
func TestReplayBase64SecretBodyDecodedFormStaysRedacted(t *testing.T) {
	registerScriptRequestScope()
	echo := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		select {
		case echo <- string(data):
		default:
		}
		w.WriteHeader(http.StatusInternalServerError)
		// The server echoes what it received: response content is also evidence.
		_, _ = w.Write([]byte("echo " + string(data)))
	}))
	defer server.Close()
	for _, payload := range []struct{ name, key, inner string }{
		{name: "sensitive-key", key: "password", inner: "PLAT-DECODED-KEY-6621"},
		{name: "neutral-key", key: "note", inner: "PLAT-DECODED-NOTE-6622"},
	} {
		secretValue := base64.StdEncoding.EncodeToString([]byte(`{"` + payload.key + `":"` + payload.inner + `"}`))
		for _, mode := range []struct {
			name       string
			redact     bool
			unredacted bool
		}{{"setting-off", false, false}, {"setting-on", true, false}, {"unredacted-debug", false, true}} {
			t.Run(payload.name+"/"+mode.name, func(t *testing.T) {
				script := setRunEvidenceRedaction(&dsl.Script{
					Name: "b64", Protocol: scopeHTTPProtocol,
					Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
					Actions: []dsl.Action{{
						Name: "Post", Type: "POST", Target: server.URL + "/login",
						Headers:      map[string]string{"Content-Type": "application/json"},
						Body:         `{{secret.payload}}`,
						BodyEncoding: "base64",
					}},
				}, mode.redact)
				report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("b64-"+payload.name+mode.name, mode.unredacted, map[string]string{"payload": secretValue}))
				if err != nil {
					t.Fatal(err)
				}
				wire := <-echo
				if !strings.Contains(wire, payload.inner) {
					t.Fatalf("the wire body did not carry the decoded payload: %q", wire)
				}
				encoded, _ := json.Marshal(report)
				if idx := strings.Index(string(encoded), payload.inner); idx >= 0 {
					t.Fatalf("decoded platform secret in report: ...%s...", string(encoded)[max(0, idx-200):idx+len(payload.inner)])
				}
			})
		}
	}
}

// A body sent as UTF-16LE carries a platform secret in a byte spelling the
// exact matcher does not hold, under a key the name scrub cannot read. The
// recorded request body of every hop (the action's own request and a 307
// redirect that resends the body) shows the redacted authored body instead, in
// every mode, while script literals stay as the setting says.
func TestReplayTransformedBodyEncodingsKeepPlatformSecretRedacted(t *testing.T) {
	registerScriptRequestScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/login" {
			http.Redirect(w, r, "/next", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("fail"))
	}))
	defer server.Close()
	const platformSecret = "PLATFORM-XFORM-PW-6631"
	const literal = "literal-visible-6632"
	interleave := func(s string) string {
		var b strings.Builder
		for _, r := range s {
			b.WriteRune(r)
			b.WriteByte(0)
		}
		return b.String()
	}
	for _, key := range []string{"password", "note"} {
		for _, mode := range []struct {
			name       string
			redact     bool
			unredacted bool
		}{{"setting-off", false, false}, {"setting-on", true, false}, {"unredacted-debug", false, true}} {
			t.Run(key+"/"+mode.name, func(t *testing.T) {
				script := setRunEvidenceRedaction(&dsl.Script{
					Name: "xform", Protocol: scopeHTTPProtocol,
					Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
					Actions: []dsl.Action{{
						Name: "Post", Type: "POST", Target: server.URL + "/login",
						Headers:      map[string]string{"Content-Type": "application/json"},
						Body:         `{"user":"` + literal + `","` + key + `":"{{secret.pw}}"}`,
						BodyEncoding: "utf-16le",
					}},
				}, mode.redact)
				report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("xform-"+key+mode.name, mode.unredacted, map[string]string{"pw": platformSecret}))
				if err != nil {
					t.Fatal(err)
				}
				var texts []string
				hops := 0
				for _, event := range report.Events {
					for _, entry := range event.Network {
						hops++
						texts = append(texts, entry.RequestBody)
						if entry.HAR != nil && entry.HAR.RequestBody != nil {
							texts = append(texts, entry.HAR.RequestBody.Text)
						}
					}
				}
				if hops < 2 {
					t.Fatalf("want the request and its 307 redirect recorded, got %d entries", hops)
				}
				joined := strings.Join(texts, "\n")
				if needle := interleave("PLATFORM-XFORM"); strings.Contains(joined, needle) {
					t.Fatalf("UTF-16LE platform secret in request body evidence: %q", joined)
				}
				if !mode.redact && strings.Count(joined, interleave(literal)) < 2 {
					t.Fatalf("script literal hidden from a hop's request body evidence with the setting off: %q", joined)
				}
			})
		}
	}
}

// A transformed body whose platform value arrives through a variable assigned
// from {{secret.*}} has no secret reference for the adapter to see: the engine
// derives the transformed spellings from the resolved body itself
// (appendPlatformDerivedBodySpellings), so they stay redacted in every mode.
func TestReplayVariableCarriedTransformedBodyStaysRedacted(t *testing.T) {
	registerScriptRequestScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("echo " + string(data)))
	}))
	defer server.Close()
	const inner = "PLAT-VAR-DECODED-6641"
	b64Secret := base64.StdEncoding.EncodeToString([]byte(`{"note":"` + inner + `"}`))
	const u16Secret = "PLAT-VAR-U16-6642"
	interleaved := ""
	for _, r := range "PLAT-VAR-U16" {
		interleaved += string(r) + "\x00"
	}
	for _, tc := range []struct {
		name, encoding, body, secret, needle string
	}{
		{name: "base64", encoding: "base64", body: "{{var.payload}}", secret: b64Secret, needle: inner},
		{name: "utf-16le", encoding: "utf-16le", body: `{"note":"{{var.payload}}"}`, secret: u16Secret, needle: interleaved},
	} {
		for _, mode := range []struct {
			name       string
			redact     bool
			unredacted bool
		}{{"setting-off", false, false}, {"setting-on", true, false}, {"unredacted-debug", false, true}} {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				script := setRunEvidenceRedaction(&dsl.Script{
					Name: "var-xform", Protocol: scopeHTTPProtocol,
					Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
					Actions: []dsl.Action{
						{Name: "Save", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"{{secret.payload}}", "payload"}}},
						{Name: "Post", Type: "POST", Target: server.URL + "/login", Headers: map[string]string{"Content-Type": "application/json"}, Body: tc.body, BodyEncoding: tc.encoding},
					},
				}, mode.redact)
				report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("var-xform-"+tc.name+mode.name, mode.unredacted, map[string]string{"payload": tc.secret}))
				if err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(report)
				needle, _ := json.Marshal(tc.needle)
				if idx := strings.Index(string(encoded), strings.Trim(string(needle), `"`)); idx >= 0 {
					t.Fatalf("platform-derived body spelling in report: ...%s...", string(encoded)[max(0, idx-200):idx+len(needle)])
				}
			})
		}
	}
}

// An NTLM/Negotiate pre-challenge Basic built over a platform half is withheld
// in every mode, whether the half names {{secret.*}} or a variable carries it.
func TestReplayPlatformIntegratedAuthPreChallengeStaysRedacted(t *testing.T) {
	registerScriptRequestScope()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	const platformPassword = "PLAT-NTLM-PW-6651"
	composite := base64.StdEncoding.EncodeToString([]byte("erin6:" + platformPassword))
	for _, tc := range []struct {
		name    string
		actions []dsl.Action
	}{
		{name: "secret-reference", actions: []dsl.Action{{Name: "Get", Type: "GET", Target: server.URL + "/p", Auth: &dsl.AuthConfig{Type: "ntlm", Username: "erin6", Password: "{{secret.pw}}"}}}},
		{name: "variable", actions: []dsl.Action{
			{Name: "Save", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"{{secret.pw}}", "pw"}}},
			{Name: "Get", Type: "GET", Target: server.URL + "/p", Auth: &dsl.AuthConfig{Type: "ntlm", Username: "erin6", Password: "{{var.pw}}"}},
		}},
	} {
		for _, mode := range []struct {
			name       string
			redact     bool
			unredacted bool
		}{{"setting-off", false, false}, {"setting-on", true, false}, {"unredacted-debug", false, true}} {
			t.Run(tc.name+"/"+mode.name, func(t *testing.T) {
				script := setRunEvidenceRedaction(&dsl.Script{Name: "ntlm-plat", Protocol: scopeHTTPProtocol, Actions: tc.actions}, mode.redact)
				report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("ntlm-plat-"+tc.name+mode.name, mode.unredacted, map[string]string{"pw": platformPassword}))
				if err != nil {
					t.Fatal(err)
				}
				encoded, _ := json.Marshal(report)
				for _, leaked := range []string{platformPassword, composite} {
					if strings.Contains(string(encoded), leaked) {
						t.Fatalf("platform-derived %q in report: %s", leaked, encoded)
					}
				}
			})
		}
	}
}
