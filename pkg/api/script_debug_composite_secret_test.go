package api

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// The unredacted debugger hides secret-store values in every spelling a
// transport builds from them, not only the bare value: Basic auth pairs from
// config.http.auth and from a per-action auth block, and SMTP AUTH PLAIN
// payloads from config.network. Script literals stay readable in both
// redact_run_evidence modes.
func TestScriptDebugManagerHidesSecretStoreCompositesInEveryMode(t *testing.T) {
	for _, mode := range []struct {
		name   string
		redact bool
	}{{name: "redaction_off", redact: false}, {name: "redaction_on", redact: true}} {
		t.Run(mode.name+"/http_basic", func(t *testing.T) {
			assertDebugHidesHTTPBasicSecretComposites(t, mode.name, mode.redact)
		})
		t.Run(mode.name+"/smtp_sasl", func(t *testing.T) {
			assertDebugHidesSMTPSASLSecretComposites(t, mode.name, mode.redact)
		})
		t.Run(mode.name+"/integrated_and_digest", func(t *testing.T) {
			assertDebugHidesIntegratedAndDigestSecretAuth(t, mode.name, mode.redact)
		})
	}
}

func collectDebugSurfaces(t *testing.T, manager *ScriptDebugManager, sessionID, finalStatus string) map[string]string {
	t.Helper()
	paused := waitForScriptDebugStatus(t, manager, sessionID, ScriptDebugPaused)
	pausedJSON, err := json.Marshal(paused)
	if err != nil {
		t.Fatalf("marshal paused debug session: %v", err)
	}
	if _, ok, changed := manager.Resume(sessionID); !ok || !changed {
		t.Fatalf("Resume debug session failed: ok=%v changed=%v", ok, changed)
	}
	done := waitForScriptDebugStatus(t, manager, sessionID, finalStatus)
	doneJSON, err := json.Marshal(done)
	if err != nil {
		t.Fatalf("marshal completed debug session: %v", err)
	}
	surfaces := map[string]string{
		"paused session":    string(pausedJSON),
		"completed session": string(doneJSON),
	}
	if harBytes, harAvail, ok := manager.ExportHARForProject(sessionID, ""); ok && harAvail.Available {
		surfaces["debug HAR"] = string(harBytes)
	}
	return surfaces
}

func assertDebugHidesHTTPBasicSecretComposites(t *testing.T, modeName string, redact bool) {
	t.Helper()
	const (
		configUser   = "alice-debug"
		configSecret = "synth-debug-basic-config-9811"
		actionUser   = "bob-debug"
		actionSecret = "synth-debug-basic-action-9812"
		literalNote  = "synth-debug-literal-note-9813"
	)
	configPair := base64.StdEncoding.EncodeToString([]byte(configUser + ":" + configSecret))
	actionPair := base64.StdEncoding.EncodeToString([]byte(actionUser + ":" + actionSecret))

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Echo-Auth", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"echo_auth":"` + r.Header.Get("Authorization") + `","note":"` + literalNote + `"}`))
	}))
	defer srv.Close()

	sessionID := "debug-basic-composite-" + modeName
	redactSetting := redact
	continueTrue := true
	manager := NewScriptDebugManager(NewHub())
	manager.Start("", &dsl.Script{
		Name:     "Debug Basic Composite",
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL: srv.URL,
			Log:     dsl.LogConfig{RedactRunEvidence: &redactSetting},
			HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
				Type: "basic", Username: configUser, Password: "{{secret.DEBUG_BASIC_PW}}",
			}},
		},
		Actions: []dsl.Action{
			{Name: "config-basic", Type: "request", Method: "GET", Target: "/config?note=" + literalNote},
			{
				Name: "action-basic", Type: "request", Method: "GET", Target: "/action?note=" + literalNote,
				Auth: &dsl.AuthConfig{Type: "basic", Username: actionUser, Password: "{{secret.DEBUG_ACTION_PW}}"},
			},
			{Name: "after", Type: "request", Method: "GET", Target: "/status"},
		},
	}, ScriptDebugStartRequest{
		ReplaySessionID: sessionID,
		Breakpoints:     []ScriptDebugBreakpoint{{Phase: "action", ActionIndex: 2}},
		ContinueOnError: &continueTrue,
		LogLevel:        "trace",
		RuntimeProfile: &engine.ReplayRuntimeProfile{
			HAROptions: &protocol.ReplayHAROptions{Bodies: "both"},
		},
		SecretResolver: engine.SecretResolverFunc(func(name string) (string, bool) {
			switch name {
			case "DEBUG_BASIC_PW":
				return configSecret, true
			case "DEBUG_ACTION_PW":
				return actionSecret, true
			}
			return "", false
		}),
	})

	surfaces := collectDebugSurfaces(t, manager, sessionID, ScriptDebugCompleted)
	if _, ok := surfaces["debug HAR"]; !ok {
		t.Fatalf("debug HAR export unavailable")
	}
	for label, surface := range surfaces {
		for _, secret := range []string{configSecret, actionSecret, configPair, actionPair} {
			if strings.Contains(surface, secret) {
				t.Fatalf("%s leaked secret-store spelling %q (redact_run_evidence=%t): %s", label, secret, redact, surface)
			}
		}
		// The debug HAR export runs the generic export scrubbers, which are
		// outside the debugger's value policy; the sessions are the raw view.
		if label != "debug HAR" && !strings.Contains(surface, literalNote) {
			t.Fatalf("%s hid script literal %q (redact_run_evidence=%t): %s", label, literalNote, redact, surface)
		}
	}
}

func assertDebugHidesSMTPSASLSecretComposites(t *testing.T, modeName string, redact bool) {
	t.Helper()
	const (
		smtpUser    = "mailer-debug@example.test"
		smtpSecret  = "synth-debug-smtp-pw-9821"
		literalNote = "synth-debug-smtp-literal-9822"
	)
	authPlain := base64.StdEncoding.EncodeToString([]byte("\x00" + smtpUser + "\x00" + smtpSecret))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go serveEchoingSMTP(conn)
		}
	}()

	sessionID := "debug-smtp-composite-" + modeName
	redactSetting := redact
	continueTrue := true
	manager := NewScriptDebugManager(NewHub())
	manager.Start("", &dsl.Script{
		Name:     "Debug SMTP Composite",
		Protocol: "network",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			Log: dsl.LogConfig{RedactRunEvidence: &redactSetting},
			Network: dsl.NetworkConfig{
				Username: smtpUser,
				Password: "{{secret.DEBUG_SMTP_PW}}",
				From:     smtpUser,
				To:       []string{"dest@example.test"},
			},
		},
		Actions: []dsl.Action{
			{Name: "smtp-auth", Type: "smtp_send", Target: listener.Addr().String(), Body: "hello " + literalNote},
			{Name: "smtp-again", Type: "smtp_send", Target: listener.Addr().String(), Body: "hello " + literalNote},
		},
	}, ScriptDebugStartRequest{
		ReplaySessionID: sessionID,
		Breakpoints:     []ScriptDebugBreakpoint{{Phase: "action", ActionIndex: 1}},
		ContinueOnError: &continueTrue,
		LogLevel:        "trace",
		SecretResolver: engine.SecretResolverFunc(func(name string) (string, bool) {
			return smtpSecret, name == "DEBUG_SMTP_PW"
		}),
	})

	surfaces := collectDebugSurfaces(t, manager, sessionID, ScriptDebugFailed)
	combined := ""
	for label, surface := range surfaces {
		for _, secret := range []string{smtpSecret, authPlain} {
			if strings.Contains(surface, secret) {
				t.Fatalf("%s leaked secret-store spelling %q (redact_run_evidence=%t): %s", label, secret, redact, surface)
			}
		}
		combined += surface
	}
	// The fake server echoes the AUTH payload in its 535 reply, so the
	// debugger must have seen the rejection for the absence check to mean anything.
	if !strings.Contains(combined, "535") {
		t.Fatalf("debug session never carried the SMTP 535 echo: %s", combined)
	}
	if !strings.Contains(combined, smtpUser) {
		t.Fatalf("debugger hid script literal %q (redact_run_evidence=%t): %s", smtpUser, redact, combined)
	}
}

// serveEchoingSMTP rejects AUTH with a reply that echoes the client's
// credential payload, then answers until the client hangs up so the client
// always reads the 535 reply rather than a reset.
func serveEchoingSMTP(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	_, _ = conn.Write([]byte("220 smtp.example.test ESMTP\r\n"))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"), strings.HasPrefix(command, "HELO"):
			_, _ = conn.Write([]byte("250-smtp.example.test\r\n250 AUTH PLAIN\r\n"))
		case strings.HasPrefix(command, "AUTH"):
			_, _ = conn.Write([]byte("535 5.7.8 rejected " + strings.TrimSpace(line) + "\r\n"))
		case strings.HasPrefix(command, "QUIT"):
			_, _ = conn.Write([]byte("221 bye\r\n"))
			return
		default:
			_, _ = conn.Write([]byte("502 unsupported\r\n"))
		}
	}
}

// NTLM/Negotiate send a pre-challenge Basic pair over the NTLM user name
// (DOMAIN\\user with a domain), and Digest answers a challenge with a keyed
// hash: neither may expose a secret-store password in the debugger.
func assertDebugHidesIntegratedAndDigestSecretAuth(t *testing.T, modeName string, redact bool) {
	t.Helper()
	const (
		secret      = "PLATFORM-DBG-PW-6611"
		literalNote = "synth-debug-integrated-literal-6613"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/digest") && !strings.HasPrefix(r.Header.Get("Authorization"), "Digest ") {
			w.Header().Set("WWW-Authenticate", `Digest realm="test", nonce="abc123", qop="auth", algorithm=MD5`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	sessionID := "debug-integrated-digest-" + modeName
	redactSetting := redact
	continueTrue := true
	manager := NewScriptDebugManager(NewHub())
	secretAuth := func(typ, domain string) *dsl.AuthConfig {
		return &dsl.AuthConfig{Type: typ, Username: "alice1", Password: "{{secret.DEBUG_INTEGRATED_PW}}", Domain: domain}
	}
	manager.Start("", &dsl.Script{
		Name:     "Debug Integrated Digest",
		Protocol: "http",
		Version:  "1.0.0",
		Config:   dsl.ScriptConfig{BaseURL: srv.URL, Log: dsl.LogConfig{RedactRunEvidence: &redactSetting}},
		Actions: []dsl.Action{
			{Name: "ntlm-domain", Type: "request", Method: "GET", Target: "/ntlm?note=" + literalNote, Auth: secretAuth("ntlm", "CORP")},
			{Name: "negotiate", Type: "request", Method: "GET", Target: "/negotiate?note=" + literalNote, Auth: secretAuth("negotiate", "")},
			{Name: "negotiate-any-domain", Type: "request", Method: "GET", Target: "/any", Auth: secretAuth("negotiate_any", "CORP")},
			{Name: "digest", Type: "request", Method: "GET", Target: "/digest", Auth: secretAuth("digest", "")},
			{Name: "after", Type: "request", Method: "GET", Target: "/status"},
		},
	}, ScriptDebugStartRequest{
		ReplaySessionID: sessionID,
		Breakpoints:     []ScriptDebugBreakpoint{{Phase: "action", ActionIndex: 4}},
		ContinueOnError: &continueTrue,
		LogLevel:        "trace",
		RuntimeProfile:  &engine.ReplayRuntimeProfile{HAROptions: &protocol.ReplayHAROptions{Bodies: "both"}},
		SecretResolver: engine.SecretResolverFunc(func(name string) (string, bool) {
			return secret, name == "DEBUG_INTEGRATED_PW"
		}),
	})

	// Every request answers 500, so the session ends failed.
	surfaces := collectDebugSurfaces(t, manager, sessionID, ScriptDebugFailed)
	pairs := []string{}
	for _, user := range []string{"alice1", `CORP\alice1`} {
		pairs = append(pairs, base64.StdEncoding.EncodeToString([]byte(user+":"+secret)))
	}
	for label, surface := range surfaces {
		for _, leaked := range append([]string{secret, "Digest username="}, pairs...) {
			if strings.Contains(surface, leaked) {
				t.Fatalf("%s leaked %q (redact_run_evidence=%t): %s", label, leaked, redact, surface)
			}
		}
		if label != "debug HAR" && !strings.Contains(surface, literalNote) {
			t.Fatalf("%s hid script literal %q (redact_run_evidence=%t): %s", label, literalNote, redact, surface)
		}
	}
}
