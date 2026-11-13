package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// Platform secret-store values, runtime-minted tokens and anything derived from
// them stay redacted in every mode, including the unredacted debugger, while
// script literals follow config.log.redact_run_evidence (off here) and stay
// visible in the debugger.

func platformDerivedReplayOptions(name string, unredacted bool, secrets map[string]string) ReplayOptions {
	opts := scopeDebugOptions(name)
	opts.UnredactedDebug = unredacted
	opts.SecretResolver = SecretResolverFunc(func(key string) (string, bool) {
		value, ok := secrets[key]
		return value, ok
	})
	return opts
}

// net/http synthesizes "Authorization: Basic base64(user:password)" from URL
// userinfo. When the password reaches the userinfo through a variable assigned
// from {{secret.*}}, that composite is platform-derived and must not appear in
// the network entries, the HAR or the report, with the setting off and in the
// unredacted debugger.
func TestReplayVariableSourcedUserinfoBasicStaysRedacted(t *testing.T) {
	registerScriptRequestScope()
	const platformPassword = "PLATFORM-UI-PW-4471"
	composite := base64.StdEncoding.EncodeToString([]byte("alice1:" + platformPassword))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("echo " + r.Header.Get("Authorization")))
	}))
	defer server.Close()
	for _, mode := range []struct {
		name       string
		unredacted bool
	}{{name: "run", unredacted: false}, {name: "unredacted-debug", unredacted: true}} {
		t.Run(mode.name, func(t *testing.T) {
			target := strings.Replace(server.URL, "http://", "http://alice1:{{var.pw}}@", 1) + "/path"
			script := setRunEvidenceRedaction(&dsl.Script{
				Name: "userinfo", Protocol: scopeHTTPProtocol,
				Config: dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
				Actions: []dsl.Action{
					{Name: "SavePW", Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []any{"{{secret.pw}}", "pw"}}},
					{Name: "Get", Type: "GET", Target: target},
				},
			}, false)
			report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("userinfo-"+mode.name, mode.unredacted, map[string]string{"pw": platformPassword}))
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			for _, leaked := range []string{platformPassword, composite} {
				if strings.Contains(string(encoded), leaked) {
					t.Fatalf("platform-derived value %q in report: %s", leaked, encoded)
				}
			}
		})
	}
}

// A script-literal userinfo pair stays visible in the unredacted debugger: the
// composite registration is driven by platform values only.
func TestReplayUnredactedDebugKeepsLiteralUserinfoBasicVisible(t *testing.T) {
	registerScriptRequestScope()
	const literalPassword = "literal-ui-pw-4472"
	composite := base64.StdEncoding.EncodeToString([]byte("alice2:" + literalPassword))
	// The server does not echo the header: the assertion is on the request
	// surface (network entries and the HAR), not on response content.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("fail"))
	}))
	defer server.Close()
	target := strings.Replace(server.URL, "http://", "http://alice2:"+literalPassword+"@", 1) + "/path"
	script := setRunEvidenceRedaction(&dsl.Script{
		Name: "userinfo-literal", Protocol: scopeHTTPProtocol,
		Config:  dsl.ScriptConfig{ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
		Actions: []dsl.Action{{Name: "Get", Type: "GET", Target: target}},
	}, false)
	report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("userinfo-literal", true, map[string]string{"other": "unused-platform-value-4473"}))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), composite) {
		t.Fatalf("script-literal userinfo composite %q hidden in the unredacted debugger: %s", composite, encoded)
	}
}

// A token minted by http_oauth2_token is a runtime-minted platform secret: the
// unredacted debugger masks it in the paused session, final_variables, request
// evidence and the debug HAR, while a script literal stays visible.
func TestReplayUnredactedDebugMasksMintedOAuthToken(t *testing.T) {
	registerScriptRequestScope()
	const (
		mintedToken = "MINTED-ACCESS-TOKEN-77413XQZ"
		literal     = "literal-header-visible-77414"
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if r.URL.Path == "/oauth/token" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"access_token":"` + mintedToken + `","token_type":"Bearer","expires_in":3600}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("echo " + r.Header.Get("X-Token")))
	}))
	defer server.Close()
	script := setRunEvidenceRedaction(&dsl.Script{
		Name: "oauth-debug", Protocol: scopeHTTPProtocol,
		Config: dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
		Actions: []dsl.Action{
			{Name: "Mint", Type: "http_oauth2_token", Params: map[string]any{
				"token_url": server.URL + "/oauth/token", "grant_type": "client_credentials",
				"client_id": "cid", "client_secret": "literal-client-secret-77415", "save_as": "access_token",
			}},
			{Name: "Use", Type: "GET", Target: "/api", Headers: map[string]string{"X-Token": "{{access_token}}", "X-Literal": literal}},
		},
	}, false)
	controller := &debugSecretMaskController{}
	opts := platformDerivedReplayOptions("oauth-debug", true, nil)
	opts.DebugController = controller
	report, err := ReplayScript(context.Background(), script, opts)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), mintedToken) {
		t.Fatalf("minted OAuth token in the unredacted debug report: %s", encoded)
	}
	if !strings.Contains(string(encoded), literal) {
		t.Fatalf("script literal hidden in the unredacted debug report: %s", encoded)
	}
	paused, err := json.Marshal(controller.seen)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(paused), mintedToken) {
		t.Fatalf("minted OAuth token in the paused debug session: %s", paused)
	}
}

// registrarReplayProtocol registers a runtime-minted secret through the
// RuntimeSecretRegistrar (as Kafka and browser do) and echoes it in its result.
type registrarReplayProtocol struct {
	replayTestProtocol
	mu        sync.Mutex
	registrar protocol.RuntimeSecretRegistrar
	minted    string
}

func (p *registrarReplayProtocol) SetRuntimeSecretRegistrar(registrar protocol.RuntimeSecretRegistrar) {
	p.mu.Lock()
	p.registrar = registrar
	p.mu.Unlock()
}

func (p *registrarReplayProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.mu.Lock()
	registrar := p.registrar
	p.mu.Unlock()
	if registrar != nil {
		registrar("oauth:kafka", []string{p.minted})
	}
	return &protocol.Result{
		Success:          true,
		Extractions:      map[string]string{"echo": p.minted},
		ProtocolEvidence: map[string]any{"token": p.minted, "target": action.Target},
	}, nil
}

func TestReplayUnredactedDebugMasksRegistrarMintedSecrets(t *testing.T) {
	const (
		minted  = "REGISTRAR-MINTED-TOKEN-88231"
		literal = "literal-target-visible-88232"
	)
	name := "replay-registrar-minted-secret"
	protocol.Register(name, func() protocol.Protocol { return &registrarReplayProtocol{minted: minted} })
	script := &dsl.Script{Name: "registrar", Protocol: name, Actions: []dsl.Action{{Name: "produce", Type: "produce", Target: literal}}}
	controller := &debugSecretMaskController{}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{
		UnredactedDebug: true, DebugController: controller, IgnoreThinkTime: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), minted) {
		t.Fatalf("registrar-minted secret in the unredacted debug report: %s", encoded)
	}
	if !strings.Contains(string(encoded), literal) {
		t.Fatalf("script literal hidden in the unredacted debug report: %s", encoded)
	}
	paused, err := json.Marshal(controller.seen)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(paused), minted) {
		t.Fatalf("registrar-minted secret in the paused debug session: %s", paused)
	}
}

// A Digest retry clones the challenged request, which already carries the
// cookies net/http's jar added. With the setting off the request snapshot keeps
// what the script authored, so a server-issued jar cookie must not be published
// there; archive sanitization keeps an AuthoredRequestRetained snapshot as is.
func TestReplayDigestSnapshotOmitsServerIssuedJarCookieWithAuthoredEvidenceVisible(t *testing.T) {
	registerScriptRequestScope()
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
	for _, protocolName := range []string{scopeHTTPProtocol, scopeSOAPProtocol} {
		t.Run(protocolName, func(t *testing.T) {
			action := dsl.Action{
				Name: "Private", Type: "GET", Target: "/private",
				Auth: &dsl.AuthConfig{Type: "digest", Username: "digest-user", Password: "literal-digest-pw-55502"},
			}
			if protocolName == scopeSOAPProtocol {
				action.Type, action.Body = "soap", `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><ping/></soapenv:Body></soapenv:Envelope>`
			}
			script := setRunEvidenceRedaction(&dsl.Script{
				Name: "digest-jar", Protocol: protocolName,
				Config: dsl.ScriptConfig{BaseURL: server.URL, ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: dsl.ContinueOnErrorValue(true)}},
				Actions: []dsl.Action{
					{Name: "Login", Type: "GET", Target: "/login"},
					action,
				},
			}, false)
			report, err := ReplayScript(context.Background(), script, platformDerivedReplayOptions("digest-jar-"+protocolName, false, nil))
			if err != nil {
				t.Fatal(err)
			}
			snapshots := 0
			for _, event := range report.Events {
				if event.Snapshot != nil {
					snapshots++
					if _, ok := event.Snapshot.RequestHeaders["Authorization"]; !ok {
						t.Fatalf("Digest snapshot lost its Authorization header: %v", event.Snapshot.RequestHeaders)
					}
				}
			}
			if snapshots == 0 {
				t.Fatalf("no request snapshot produced: %+v", report.Events)
			}
			encoded, err := json.Marshal(report)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), serverSession) {
				t.Fatalf("server-issued jar cookie published with the setting off: %s", encoded)
			}
		})
	}
}
