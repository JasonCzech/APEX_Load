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

// Typed auth headers derived from a secret-store value — a Basic pair, the
// NTLM/Negotiate pre-challenge Basic pair, a Digest response hash, a
// WS-Security PasswordDigest — stay hidden from every report surface: in the
// unredacted debugger, and with config.log.redact_run_evidence off when the
// credential arrives through a variable assigned from {{secret.*}}. Script
// literals stay readable.

var basicHeaderPattern = regexp.MustCompile(`Basic ([A-Za-z0-9+/=]{8,})`)

func assertNoBasicPairCarrying(t *testing.T, label, report, secret string) {
	t.Helper()
	for _, match := range basicHeaderPattern.FindAllStringSubmatch(report, -1) {
		decoded, err := base64.StdEncoding.DecodeString(match[1])
		if err == nil && strings.Contains(string(decoded), secret) {
			t.Fatalf("%s: Basic header decodes to %q", label, decoded)
		}
	}
}

func unredactedDebugReplayOptions(name string, secret string) ReplayOptions {
	opts := scopeDebugOptions(name)
	opts.UnredactedDebug = true
	opts.SecretResolver = SecretResolverFunc(func(key string) (string, bool) { return secret, key == "pw" })
	return opts
}

func statusServer(status int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(status)
	}))
}

func TestUnredactedDebugHidesIntegratedAuthPreChallengeSecretComposite(t *testing.T) {
	registerScriptRequestScope()
	const secret = "PLATFORM-DBG-PW-6611"
	server := statusServer(http.StatusInternalServerError)
	defer server.Close()
	for _, typ := range []string{"ntlm", "negotiate", "negotiate_any"} {
		for _, domain := range []string{"", "CORP"} {
			for _, surface := range []string{"action", "config"} {
				name := typ + "/" + surface + "/domain=" + domain
				t.Run(name, func(t *testing.T) {
					auth := dsl.AuthConfig{Type: typ, Username: "alice1", Password: "{{secret.pw}}", Domain: domain}
					cfg := dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}}
					action := dsl.Action{Name: "Get", Type: "GET", Target: "/private?note=literal-note-6612"}
					if surface == "action" {
						action.Auth = &auth
					} else {
						cfg.HTTP.Auth = auth
					}
					script := setRunEvidenceRedaction(&dsl.Script{Name: "integrated", Protocol: scopeHTTPProtocol, Config: cfg, Actions: []dsl.Action{action}}, false)
					report, err := ReplayScript(context.Background(), script, unredactedDebugReplayOptions("integrated-"+strings.ReplaceAll(name, "/", "-"), secret))
					if err != nil {
						t.Fatal(err)
					}
					encoded, _ := json.Marshal(report)
					text := string(encoded)
					if strings.Contains(text, secret) {
						t.Fatalf("raw secret in debug report: %s", text)
					}
					assertNoBasicPairCarrying(t, name, text, secret)
					if !strings.Contains(text, "literal-note-6612") {
						t.Fatalf("debug report hid the script literal: %s", text)
					}
				})
			}
		}
	}
}

// digestServer challenges every request without Authorization and fails the
// authenticated retry, so both hops reach the report.
func digestServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Digest ") {
			w.Header().Set("WWW-Authenticate", `Digest realm="test", nonce="abc123", qop="auth", algorithm=MD5`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
}

func TestUnredactedDebugHidesDigestResponseFromSecretPassword(t *testing.T) {
	registerScriptRequestScope()
	const secret = "PLATFORM-DBG-DIGEST-6621"
	server := digestServer()
	defer server.Close()
	for _, tc := range []struct {
		name     string
		typ      string
		surface  string
		password string
		visible  bool
	}{
		{name: "digest/action/secret", typ: "digest", surface: "action", password: "{{secret.pw}}"},
		{name: "digest/config/secret", typ: "digest", surface: "config", password: "{{secret.pw}}"},
		{name: "negotiate_any/action/secret", typ: "negotiate_any", surface: "action", password: "{{secret.pw}}"},
		{name: "digest/action/literal", typ: "digest", surface: "action", password: "literal-digest-pw-6622", visible: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth := dsl.AuthConfig{Type: tc.typ, Username: "digest-user", Password: tc.password}
			cfg := dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}}
			action := dsl.Action{Name: "Get", Type: "GET", Target: "/private"}
			if tc.surface == "action" {
				action.Auth = &auth
			} else {
				cfg.HTTP.Auth = auth
			}
			script := setRunEvidenceRedaction(&dsl.Script{Name: "digest", Protocol: scopeHTTPProtocol, Config: cfg, Actions: []dsl.Action{action}}, false)
			report, err := ReplayScript(context.Background(), script, unredactedDebugReplayOptions("digest-"+strings.ReplaceAll(tc.name, "/", "-"), secret))
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(report)
			text := string(encoded)
			if strings.Contains(text, secret) {
				t.Fatalf("raw secret in debug report: %s", text)
			}
			if got := strings.Contains(text, "Digest username="); got != tc.visible {
				t.Fatalf("Digest response visible=%v, want %v: %s", got, tc.visible, text)
			}
		})
	}
}

var debugWSSEDigestPattern = regexp.MustCompile(`#PasswordDigest\\?">([^<]*)</wsse:Password>`)

var jsonMarkupUnescaper = strings.NewReplacer(`\u003c`, "<", `\u003e`, ">", `\u0026`, "&")

func TestUnredactedDebugHidesWSSecurityPasswordDigestFromSecretPassword(t *testing.T) {
	registerScriptRequestScope()
	const secret = "PLATFORM-DBG-WSSE-6631"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><soapenv:Fault><faultcode>x</faultcode><faultstring>y</faultstring></soapenv:Fault></soapenv:Body></soapenv:Envelope>`)
	}))
	defer server.Close()
	for _, source := range []struct {
		name     string
		password string
		visible  bool
	}{{name: "secret", password: "{{secret.pw}}"}, {name: "literal", password: "literal-wsse-pw-6632", visible: true}} {
		t.Run(source.name, func(t *testing.T) {
			script := setRunEvidenceRedaction(&dsl.Script{
				Name: "wsse", Protocol: scopeSOAPProtocol,
				Config: dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)},
					SOAP: dsl.SOAPConfig{WSSecurity: &dsl.SOAPWSSecurityConfig{UsernameToken: &dsl.SOAPUsernameTokenConfig{Username: "svc-user", Password: source.password, PasswordType: "digest"}}}},
				Actions: []dsl.Action{{Name: "Call", Type: "soap", Target: "/svc", Body: `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Header/><soapenv:Body><ping/></soapenv:Body></soapenv:Envelope>`}},
			}, false)
			report, err := ReplayScript(context.Background(), script, unredactedDebugReplayOptions("wsse-"+source.name, secret))
			if err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(report)
			text := string(encoded)
			if strings.Contains(text, secret) {
				t.Fatalf("raw secret in debug report: %s", text)
			}
			matches := debugWSSEDigestPattern.FindAllStringSubmatch(jsonMarkupUnescaper.Replace(text), -1)
			if len(matches) == 0 {
				i := strings.Index(text, "UsernameToken")
				if i < 0 {
					i = 0
				}
				t.Fatalf("debug report carries no PasswordDigest element: %s", text[i:min(len(text), i+900)])
			}
			for _, match := range matches {
				visible := match[1] != "" && match[1] != redactedPayloadValue
				if visible != source.visible {
					t.Fatalf("PasswordDigest %q visible=%v, want %v", match[1], visible, source.visible)
				}
			}
		})
	}
}

// With the setting off, typed Basic auth whose password is a variable assigned
// from {{secret.*}} must not publish base64(user:secret); a variable assigned
// from a script literal keeps the authored header visible. The same holds in
// the unredacted debugger.
func TestBasicAuthFromVariableHoldingSecretStaysWithheld(t *testing.T) {
	registerScriptRequestScope()
	const secret = "PLATFORM-BASIC-PW-8812"
	const literal = "literal-basic-pw-8813"
	server := statusServer(http.StatusInternalServerError)
	defer server.Close()
	for _, debug := range []bool{false, true} {
		for _, source := range []struct {
			name, value string
			visible     bool
		}{{name: "secret", value: "{{secret.pw}}"}, {name: "literal", value: literal, visible: true}} {
			for _, surface := range []string{"basic", "ntlm"} {
				label := source.name + "/" + surface
				if debug {
					label = "debug/" + label
				}
				t.Run(label, func(t *testing.T) {
					actions := []dsl.Action{
						{Name: "SavePW", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{source.value, "pw"}}},
						{Name: "Get", Type: "GET", Target: "/private", Auth: &dsl.AuthConfig{Type: surface, Username: "basic-user", Password: "{{var.pw}}"}},
					}
					cfg := dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}}
					script := setRunEvidenceRedaction(&dsl.Script{Name: "basicvar", Protocol: scopeHTTPProtocol, Config: cfg, Actions: actions}, false)
					opts := scopeDebugOptions("basicvar-" + strings.ReplaceAll(label, "/", "-"))
					opts.UnredactedDebug = debug
					opts.SecretResolver = SecretResolverFunc(func(name string) (string, bool) { return secret, name == "pw" })
					report, err := ReplayScript(context.Background(), script, opts)
					if err != nil {
						t.Fatal(err)
					}
					encoded, _ := json.Marshal(report)
					text := string(encoded)
					if strings.Contains(text, secret) {
						t.Fatalf("raw platform value in report: %s", text)
					}
					assertNoBasicPairCarrying(t, label, text, secret)
					if source.visible && surface == "basic" {
						pair := base64.StdEncoding.EncodeToString([]byte("basic-user:" + literal))
						var snapshotAuth, networkAuth string
						for _, event := range report.Events {
							if event.Snapshot != nil && event.Snapshot.RequestHeaders["Authorization"] != "" {
								snapshotAuth = event.Snapshot.RequestHeaders["Authorization"]
							}
							for _, entry := range event.Network {
								if entry.ScriptRequest && entry.RequestHeaders["Authorization"] != "" {
									networkAuth = entry.RequestHeaders["Authorization"]
								}
							}
						}
						if snapshotAuth != "Basic "+pair {
							t.Fatalf("snapshot Authorization = %q, want the authored literal pair", snapshotAuth)
						}
						if networkAuth != "Basic "+pair {
							t.Fatalf("network Authorization = %q, want the authored literal pair", networkAuth)
						}
					}
				})
			}
		}
	}
}
