package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	soapprotocol "github.com/jasonczech/apexload/pkg/protocol/soap"
)

var registerScriptRequestScopeProtocols sync.Once

var (
	wsseDigestTextPattern = regexp.MustCompile(`#PasswordDigest">([^<]*)</wsse:Password>`)
	wsseNonceTextPattern  = regexp.MustCompile(`<wsse:Nonce>([^<]*)</wsse:Nonce>`)
)

// visibleElementText reports whether pattern finds an element whose text is
// present and not the redaction marker.
func visibleElementText(pattern *regexp.Regexp, body string) bool {
	for _, match := range pattern.FindAllStringSubmatch(body, -1) {
		if match[1] != "" && match[1] != redactedPayloadValue {
			return true
		}
	}
	return false
}

const (
	scopeHTTPProtocol = "script-request-scope-http"
	scopeSOAPProtocol = "script-request-scope-soap"
)

func registerScriptRequestScope() {
	registerScriptRequestScopeProtocols.Do(func() {
		protocol.Register(scopeHTTPProtocol, func() protocol.Protocol { return &httpprotocol.HTTP{} })
		protocol.Register(scopeSOAPProtocol, func() protocol.Protocol { return &soapprotocol.SOAP{} })
	})
}

func scopeDebugOptions(name string) ReplayOptions {
	return ReplayOptions{
		ReplaySessionID: name, IgnoreThinkTime: true,
		DebugController: replayDebugControllerFunc(func(context.Context, ReplayDebugAction) error { return nil }),
		RuntimeProfile:  &ReplayRuntimeProfile{HAROptions: &protocol.ReplayHAROptions{Bodies: "both", Redirects: true}},
	}
}

// Fed by real HTTP adapter output: traffic the server named (page resources,
// a meta-refresh follow-up) and server-issued jar cookies keep the full mask
// with config.log.redact_run_evidence off, while the authored request side
// (an authored Cookie the jar did not touch) stays visible.
func TestReplayServerSourcedRequestTrafficKeepsMaskingWithAuthoredEvidenceVisible(t *testing.T) {
	registerScriptRequestScope()
	const (
		serverSession  = "SERVER-ISSUED-SESSION-9101"
		refreshToken   = "PAGE-REFRESH-TOKEN-9102"
		resourceToken  = "PAGE-RESOURCE-TOKEN-9103"
		authoredCookie = "authored-lit-9104"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "SESSIONID", Value: serverSession, Path: "/"})
		case "/refresh":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<meta http-equiv="refresh" content="0;url=/final?token=`+refreshToken+`">`)
			return
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, `<html><body>page</body></html>`)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	for _, mode := range runEvidenceRedactionModes {
		t.Run(mode.name, func(t *testing.T) {
			script := setRunEvidenceRedaction(&dsl.Script{
				Name: "scope", Protocol: scopeHTTPProtocol,
				Config: dsl.ScriptConfig{BaseURL: server.URL, HTTP: dsl.HTTPConfig{MaxMetaRefresh: 2}, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
				Actions: []dsl.Action{
					{Name: "Pref", Type: "GET", Target: "/pref", Headers: map[string]string{"Cookie": "pref=" + authoredCookie}},
					{Name: "Login", Type: "GET", Target: "/login"},
					{Name: "Use", Type: "GET", Target: "/use"},
					{Name: "Page", Type: "GET", Target: "/page", Params: map[string]any{"resources": []any{map[string]any{"url": "/sheet.css?access_token=" + resourceToken}}}},
					{Name: "Refresh", Type: "GET", Target: "/refresh"},
				},
			}, mode.redact)
			report, err := ReplayScript(context.Background(), script, scopeDebugOptions("scope-"+mode.name))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{serverSession, refreshToken, resourceToken} {
				if strings.Contains(string(encoded), value) {
					t.Fatalf("server-sourced value %q published: %s", value, encoded)
				}
			}
			if len(report.Events) < 5 {
				t.Fatalf("events = %d: %s", len(report.Events), report.Error)
			}
			// The page resource and the follow-up were observed (so the test
			// would see a leak) and their URLs are present, masked.
			var sawResource, sawFollow bool
			for _, event := range report.Events {
				for _, entry := range event.Network {
					sawResource = sawResource || strings.Contains(entry.URL, "/sheet.css")
					sawFollow = sawFollow || strings.Contains(entry.URL, "/final")
					if (strings.Contains(entry.URL, "/sheet.css") || strings.Contains(entry.URL, "/final")) && entry.AuthoredRequestRetained {
						t.Fatalf("server-named entry %q marked authored", entry.URL)
					}
				}
			}
			if !sawResource || !sawFollow {
				t.Fatalf("resource=%v follow=%v: %s", sawResource, sawFollow, encoded)
			}
			use := report.Events[2]
			if len(use.Network) == 0 || use.Network[0].RequestHeaders["Cookie"] != redactedPayloadValue {
				t.Fatalf("jar Cookie = %+v, want the whole header masked", use.Network)
			}
			pref := report.Events[0]
			if len(pref.Network) == 0 {
				t.Fatalf("pref entries missing")
			}
			if visible := strings.Contains(pref.Network[0].RequestHeaders["Cookie"], authoredCookie); visible == mode.redact {
				t.Fatalf("authored Cookie = %q, want visible=%v", pref.Network[0].RequestHeaders["Cookie"], !mode.redact)
			}
		})
	}
}

// A WS-Security PasswordDigest computed from a {{secret.*}} SOAP password is a
// derived platform credential: it stays out of authored request evidence with
// the setting off. A digest of a script-literal password stays visible.
func TestReplaySOAPPlatformPasswordDigestStaysMaskedWithAuthoredEvidenceVisible(t *testing.T) {
	registerScriptRequestScope()
	const platformPassword = "platform-soap-pass-9201"
	const literalPassword = "literal-soap-pass-9202"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><ok/></soapenv:Body></soapenv:Envelope>`)
	}))
	defer server.Close()
	for _, mode := range runEvidenceRedactionModes {
		for _, source := range []struct {
			name     string
			password string
			masked   bool
		}{
			{name: "platform", password: "{{secret.soap_pw}}", masked: true},
			{name: "literal", password: literalPassword, masked: mode.redact},
		} {
			t.Run(mode.name+"/"+source.name, func(t *testing.T) {
				script := setRunEvidenceRedaction(&dsl.Script{
					Name: "soap digest", Protocol: scopeSOAPProtocol,
					Config:  dsl.ScriptConfig{BaseURL: server.URL, SOAP: dsl.SOAPConfig{WSSecurity: &dsl.SOAPWSSecurityConfig{UsernameToken: &dsl.SOAPUsernameTokenConfig{Username: "svc-user", Password: source.password, PasswordType: "digest"}}}},
					Actions: []dsl.Action{{Name: "Call", Type: "soap", Target: "/svc", Body: `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Header/><soapenv:Body><ping/></soapenv:Body></soapenv:Envelope>`}},
				}, mode.redact)
				opts := scopeDebugOptions("soap-" + mode.name + "-" + source.name)
				opts.SecretResolver = SecretResolverFunc(func(name string) (string, bool) { return platformPassword, name == "soap_pw" })
				report, err := ReplayScript(context.Background(), script, opts)
				if err != nil {
					t.Fatal(err)
				}
				if len(report.Events) == 0 || len(report.Events[0].Network) == 0 {
					t.Fatalf("no network entry: %+v %s", report.Events, report.Error)
				}
				entry := report.Events[0].Network[0]
				if entry.HAR == nil || entry.HAR.RequestBody == nil || !strings.Contains(entry.HAR.RequestBody.Text, "UsernameToken") {
					t.Fatalf("HAR request body missing the UsernameToken: %+v", entry.HAR)
				}
				for label, body := range map[string]string{"har": entry.HAR.RequestBody.Text, "entry": entry.RequestBody} {
					if body == "" {
						continue
					}
					digestVisible := visibleElementText(wsseDigestTextPattern, body)
					if digestVisible == source.masked {
						t.Fatalf("%s body digest visible=%v, want %v: %s", label, digestVisible, !source.masked, body)
					}
					if source.masked && visibleElementText(wsseNonceTextPattern, body) {
						t.Fatalf("%s body kept the digest's nonce: %s", label, body)
					}
				}
				encoded, _ := json.Marshal(report)
				if strings.Contains(string(encoded), platformPassword) {
					t.Fatalf("platform password leaked: %s", encoded)
				}
			})
		}
	}
}

// A meta-refresh follow-up's snapshot URL came from the server's page; it keeps
// the full scrub while the authored snapshot URL follows the setting.
func TestSnapshotServerNamedURLKeepsMaskingWithAuthoredEvidenceVisible(t *testing.T) {
	config := authoredEvidenceRootConfig(t, false)
	for _, tc := range []struct {
		notAuthored bool
		visible     bool
	}{{notAuthored: false, visible: true}, {notAuthored: true, visible: false}} {
		result := &protocol.Result{Snapshot: &protocol.ErrorSnapshot{
			RequestURL:            "https://api.example.test/final?access_token=" + authoredLiteralToken,
			RequestURLNotAuthored: tc.notAuthored,
		}}
		scrubSensitiveResultWithOptions(result, config, true)
		if strings.Contains(result.Snapshot.RequestURL, authoredLiteralToken) != tc.visible {
			t.Fatalf("notAuthored=%v: RequestURL = %q, want visible=%v", tc.notAuthored, result.Snapshot.RequestURL, tc.visible)
		}
	}
}
