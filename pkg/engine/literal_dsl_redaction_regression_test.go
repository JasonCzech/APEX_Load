package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/livetable"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func buildTestRedactionForScript(t *testing.T, script *dsl.Script, resolver SecretResolver) *RedactionConfig {
	t.Helper()
	secretResolver := resolver
	if secretResolver == nil {
		secretResolver = defaultSecretResolverForScript(script)
	}
	secretResolver, secretValues, err := snapshotReferencedSecretsContext(context.Background(), script, secretResolver)
	if err != nil {
		t.Fatalf("snapshotReferencedSecretsContext: %v", err)
	}
	secretValues, scriptSourcedValues, _, err := runtimeSecretValuesForScript(script, secretResolver, secretValues)
	if err != nil {
		t.Fatalf("runtimeSecretValuesForScript: %v", err)
	}
	cfg, _, err := buildRuntimeSecretRedaction(nil, secretValues, scriptSourcedValues)
	if err != nil {
		t.Fatalf("buildRuntimeSecretRedaction: %v", err)
	}
	return ensureDynamicSecretRegistry(cfg)
}

func TestLiteralDSLCredentialsAlwaysRedactWithoutRunEvidenceMetadataOrRuntimeSecretMaterial(t *testing.T) {
	const (
		soapPwd        = "synth-soap-userpwd-7101"
		wssePwd        = "synth-wsse-pwd-7102"
		bearerTok      = "synth-bearer-tok-7103"
		proxyCred      = "synth-proxy-cred-7104"
		cookieVal      = "synth-session-cookie-7105"
		apiKeyVal      = "synth-apikey-7106"
		queryTok       = "synth-query-tok-7107"
		urlUserPwd     = "synth-url-userpwd-7108"
		jsonAccPwd     = "synth-json-accpwd-7109"
		jsonEmailTok   = "synth-json-emailtok-7110"
		formConfirmTok = "synth-form-confirm-7111"
		csvInlinePwd   = "synth-csv-inline-pwd-7112"
		paramLoginPwd  = "synth-param-loginpwd-7113"
		kafkaLiteral   = "synth~kafka~literal~secret~value~7114~abcd"
	)

	rawScript, err := json.Marshal(map[string]any{
		"name":     "literal-dsl-unconditional-redaction",
		"protocol": "http",
		"config": map[string]any{
			"base_url": fmt.Sprintf("https://operator:%s@api.example.test", urlUserPwd),
			"http": map[string]any{
				"proxy": map[string]any{
					"url": fmt.Sprintf("http://proxyuser:%s@proxy.example.test:8080", proxyCred),
				},
			},
			"log": map[string]any{
				"level":               "debug",
				"redact_run_evidence": false,
			},
			"kafka": map[string]any{
				"oauth": map[string]any{
					"mode":              "client_credentials",
					"token_url":         "https://auth.example.test/oauth/token",
					"client_id":         "orders-client",
					"client_secret_ref": kafkaLiteral,
				},
			},
		},
		"metadata": map[string]any{
			"import": map[string]any{
				"source": "postman",
			},
		},
		"data_sources": []any{map[string]any{
			"name":             "users",
			"type":             "csv",
			"first_row_header": true,
			"inline_data":      "USER,PWD\nalice," + csvInlinePwd + "\n",
		}},
		"parameters": []any{map[string]any{
			"name":   "login_password",
			"type":   "list",
			"values": []string{paramLoginPwd},
		}},
		"actions": []any{
			map[string]any{
				"name":   "soap-login",
				"type":   "request",
				"method": "POST",
				"target": "/soap?access_token=" + queryTok,
				"headers": map[string]string{
					"Authorization": "Bearer " + bearerTok,
					"Cookie":        "SID=" + cookieVal,
					"X-Api-Key":     apiKeyVal,
				},
				"metadata": map[string]any{
					dsl.ImportPreservedCredentialTargetsMetadataKey: []any{
						map[string]any{"field": "body", "pointer": "/nonexistent/stale/locator"},
					},
				},
				"body": `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:ns="urn:test" xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"><soapenv:Header><wsse:Security><wsse:UsernameToken><wsse:Password>` + wssePwd + `</wsse:Password></wsse:UsernameToken></wsse:Security></soapenv:Header><soapenv:Body><ns:Login><ns:userPassword>` + soapPwd + `</ns:userPassword></ns:Login></soapenv:Body></soapenv:Envelope>`,
			},
			map[string]any{
				"name":   "json-login",
				"type":   "request",
				"method": "POST",
				"target": "/api/login",
				"body":   `{"accPassword":"` + jsonAccPwd + `","emailToken":"` + jsonEmailTok + `"}`,
			},
			map[string]any{
				"name":   "form-confirm",
				"type":   "request",
				"method": "POST",
				"target": "/api/confirm",
				"body":   `user=alice&_CONFIRMATIONTOKEN=` + formConfirmTok,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal script: %v", err)
	}
	script, err := dsl.Parse(rawScript)
	if err != nil {
		t.Fatalf("dsl.Parse: %v", err)
	}

	cfg := buildTestRedactionForScript(t, script, nil)
	if cfg == nil {
		t.Fatalf("expected RedactionConfig to be non-nil for literal DSL credentials")
	}
	// Guard 2 (#14): literal DSL credentials must NOT flip HasRuntimeSecretMaterial() to true.
	if cfg.HasRuntimeSecretMaterial() {
		t.Fatalf("HasRuntimeSecretMaterial() = true for literal-only DSL script; must stay false so browser screenshots/HAR are not suppressed")
	}

	allCanaries := []string{
		soapPwd, wssePwd, bearerTok, proxyCred, cookieVal, apiKeyVal,
		queryTok, urlUserPwd, jsonAccPwd, jsonEmailTok, formConfirmTok,
		csvInlinePwd, paramLoginPwd, kafkaLiteral,
	}
	for _, secret := range allCanaries {
		got := cfg.ScrubText("echoed=" + secret)
		if strings.Contains(got, secret) {
			t.Errorf("RedactionConfig failed to scrub literal DSL secret %q: got %q", secret, got)
		}
	}

	// Dynamic runtime token registration activates runtime secret material.
	cfg.RegisterDynamicSecrets("oauth:access_token", []string{"synth-dynamic-oauth-7199"})
	if !cfg.HasRuntimeSecretMaterial() {
		t.Fatalf("HasRuntimeSecretMaterial() = false after registering dynamic OAuth token")
	}
	if got := cfg.ScrubText("Bearer synth-dynamic-oauth-7199"); strings.Contains(got, "synth-dynamic-oauth-7199") {
		t.Fatalf("dynamic OAuth token leaked: %q", got)
	}
}

func TestMailSASLBase64CompositesRedactForLiteralAndPlatformManagedCredentials(t *testing.T) {
	const (
		mailUser   = "mailer@example.test"
		literalPwd = "synth-smtp-literal-pwd-8201"
		managedPwd = "synth-smtp-managed-pwd-8202"
	)

	literalRaw, _ := json.Marshal(map[string]any{
		"name":     "smtp-literal",
		"protocol": "network",
		"config": map[string]any{
			"network": map[string]any{
				"username": mailUser,
				"password": literalPwd,
			},
		},
		"actions": []any{map[string]any{
			"name":   "send-mail",
			"type":   "smtp_send",
			"target": "127.0.0.1:2525",
		}},
	})
	literalScript, err := dsl.Parse(literalRaw)
	if err != nil {
		t.Fatalf("dsl.Parse literal mail: %v", err)
	}
	literalCfg := buildTestRedactionForScript(t, literalScript, nil)
	plainWire := base64.StdEncoding.EncodeToString([]byte("\x00" + mailUser + "\x00" + literalPwd))
	loginPassWire := base64.StdEncoding.EncodeToString([]byte(literalPwd))
	for _, wire := range []string{plainWire, loginPassWire} {
		if scrubbed := literalCfg.ScrubText("AUTH trace: " + wire); strings.Contains(scrubbed, wire) {
			t.Fatalf("literal mail SASL base64 wire %q was not redacted: %q", wire, scrubbed)
		}
	}
	if literalCfg.HasRuntimeSecretMaterial() {
		t.Fatalf("literal mail credentials must not mark HasRuntimeSecretMaterial()")
	}

	managedRaw, _ := json.Marshal(map[string]any{
		"name":     "smtp-managed",
		"protocol": "network",
		"config": map[string]any{
			"network": map[string]any{
				"username": mailUser,
				"password": "{{secret.SMTP_PASS}}",
			},
		},
		"actions": []any{map[string]any{
			"name":   "send-mail",
			"type":   "smtp_send",
			"target": "127.0.0.1:2525",
		}},
	})
	managedScript, err := dsl.Parse(managedRaw)
	if err != nil {
		t.Fatalf("dsl.Parse managed mail: %v", err)
	}
	resolver := SecretResolverFunc(func(name string) (string, bool) {
		return managedPwd, name == "SMTP_PASS"
	})
	managedCfg := buildTestRedactionForScript(t, managedScript, resolver)
	managedPlainWire := base64.StdEncoding.EncodeToString([]byte("\x00" + mailUser + "\x00" + managedPwd))
	if scrubbed := managedCfg.ScrubText("AUTH PLAIN " + managedPlainWire); strings.Contains(scrubbed, managedPlainWire) {
		t.Fatalf("managed mail SASL base64 wire leaked: %q", scrubbed)
	}
	if !managedCfg.HasRuntimeSecretMaterial() {
		t.Fatalf("platform-managed {{secret.*}} mail credentials must mark HasRuntimeSecretMaterial()")
	}
}

func TestRunShakeoutInitializesParametersAndRedactsLiteralAndManagedSecrets(t *testing.T) {
	const (
		paramPwd   = "synth-shakeout-param-pwd-8301"
		literalKey = "synth-shakeout-apikey-8302"
		managedTok = "synth-shakeout-secret-8303"
	)

	protoName := "shakeout_redaction_test_proto"
	protocol.Register(protoName, func() protocol.Protocol {
		return &shakeoutEchoFailureProto{}
	})

	script := &dsl.Script{
		Name:     "shakeout-redaction",
		Protocol: protoName,
		Parameters: []dsl.Parameter{{
			Name:   "login_password",
			Type:   "list",
			Values: []string{paramPwd},
		}},
		Actions: []dsl.Action{{
			Name:   "auth-step",
			Type:   "request",
			Method: "POST",
			Target: "/auth?token={{secret.SHAKEOUT_TOKEN}}",
			Headers: map[string]string{
				"X-Api-Key": literalKey,
			},
			Body: `{"password":"{{param.login_password}}"}`,
		}},
	}

	err := RunScriptShakeoutWithSecretResolver(context.Background(), "shakeout-group", script, SecretResolverFunc(func(name string) (string, bool) {
		return managedTok, name == "SHAKEOUT_TOKEN"
	}))
	if err == nil {
		t.Fatalf("expected RunScriptShakeoutWithSecretResolver to fail on unsuccessful action")
	}
	for _, secret := range []string{paramPwd, literalKey, managedTok} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("shakeout error leaked %q: %v", secret, err)
		}
	}
}

type shakeoutEchoFailureProto struct {
	redactor protocol.TextRedactor
}

func (p *shakeoutEchoFailureProto) Name() string                               { return "shakeout_redaction_test_proto" }
func (p *shakeoutEchoFailureProto) Init(context.Context, map[string]any) error { return nil }
func (p *shakeoutEchoFailureProto) Teardown(context.Context) error             { return nil }
func (p *shakeoutEchoFailureProto) HealthCheck(context.Context) error          { return nil }
func (p *shakeoutEchoFailureProto) SetRuntimeTextRedactor(fn protocol.TextRedactor) {
	p.redactor = fn
}
func (p *shakeoutEchoFailureProto) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	bodyStr := string(action.Body)
	if strings.Contains(bodyStr, "{{param.login_password}}") {
		return nil, fmt.Errorf("shakeout did not initialize VUser parameters before executing action: %s", bodyStr)
	}
	errText := fmt.Sprintf("upstream rejected target=%s key=%s body=%s", action.Target, action.Headers["X-Api-Key"], bodyStr)
	return &protocol.Result{
		ActionName:   action.Name,
		Success:      false,
		StatusCode:   401,
		ErrorMessage: errText,
		ResponseBody: errText,
		Snapshot: &protocol.ErrorSnapshot{
			RequestURL:     action.Target,
			RequestHeaders: action.Headers,
			RequestBody:    bodyStr,
			ResponseBody:   errText,
		},
	}, nil
}

type preflightRedactionBindTestProto struct {
	redactor     protocol.TextRedactor
	registrar    protocol.RuntimeSecretRegistrar
	secretActive func() bool
	minted       string
}

func (p *preflightRedactionBindTestProto) Name() string { return "preflight_redaction_bind_test_proto" }
func (p *preflightRedactionBindTestProto) SetRuntimeTextRedactor(fn protocol.TextRedactor) {
	p.redactor = fn
}
func (p *preflightRedactionBindTestProto) SetRuntimeSecretRegistrar(fn protocol.RuntimeSecretRegistrar) {
	p.registrar = fn
}
func (p *preflightRedactionBindTestProto) SetRuntimeSecretActivity(fn func() bool) {
	p.secretActive = fn
}
func (p *preflightRedactionBindTestProto) Init(context.Context, map[string]any) error {
	if p.secretActive == nil || !p.secretActive() {
		return fmt.Errorf("expected runtimeSecretsActive=true during browser session preflight")
	}
	if p.registrar == nil {
		return fmt.Errorf("expected runtimeSecretRegistrar to be bound during browser session preflight")
	}
	p.registrar("session:salesforce_jwt", []string{p.minted})
	return fmt.Errorf("frontdoor exchange failed with sid=%s", p.minted)
}
func (p *preflightRedactionBindTestProto) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	return &protocol.Result{Success: true, StatusCode: 200}, nil
}
func (p *preflightRedactionBindTestProto) Teardown(context.Context) error    { return nil }
func (p *preflightRedactionBindTestProto) HealthCheck(context.Context) error { return nil }

func TestPreflightBrowserSessionBindsRuntimeSecretRegistrarAndPreservesDynamicRegistry(t *testing.T) {
	const (
		platformKey = "synth-preflight-jwt-key-8401"
		mintedSID   = "synth-preflight-minted-sid-8402"
	)
	base, err := WithSecretValueRedactionChecked(nil, []string{platformKey})
	if err != nil {
		t.Fatalf("WithSecretValueRedactionChecked: %v", err)
	}
	first := ensureDynamicSecretRegistry(base)
	second := ensureDynamicSecretRegistry(first)
	second.RegisterDynamicSecrets("oauth:shared", []string{"synth-upstream-dyn-8403"})
	if got := first.ScrubText("token=synth-upstream-dyn-8403"); strings.Contains(got, "synth-upstream-dyn-8403") {
		t.Fatalf("ensureDynamicSecretRegistry clobbered upstream dynamic registry: %q", got)
	}

	protoName := "preflight_redaction_bind_test_proto"
	protocol.Register(protoName, func() protocol.Protocol {
		return &preflightRedactionBindTestProto{minted: mintedSID}
	})
	group := GroupConfig{
		Name:            "observers",
		Script:          &dsl.Script{Name: "obs", Protocol: protoName},
		RedactionConfig: ensureDynamicSecretRegistry(base),
	}
	binding := &browserIdentityBinding{
		ordinal:  0,
		identity: dsl.BrowserIdentity{Username: "observer@example.test"},
	}
	msg := preflightBrowserSession(context.Background(), group, map[string]any{}, 0, binding, "https://app.example.test/home")
	if msg == "" {
		t.Fatalf("expected preflight failure message")
	}
	if strings.Contains(msg, mintedSID) || strings.Contains(msg, platformKey) {
		t.Fatalf("preflightBrowserSession leaked secret in failure message: %q", msg)
	}
}

func TestDynamicSecretOverflowFailsClosedAcrossLiveTableAndReplay(t *testing.T) {
	const (
		sharedTok  = "synth-dyn-shared-tok-8501"
		mintedTok  = "synth-dyn-minted-tok-8502"
		rotatedTok = "synth-dyn-rotated-tok-8503"
	)
	base, err := WithScriptSourcedSecretValueRedaction(nil, []string{sharedTok})
	if err != nil {
		t.Fatalf("WithScriptSourcedSecretValueRedaction: %v", err)
	}
	cfg := ensureDynamicSecretRegistry(base)
	cfg.RegisterDynamicSecrets("oauth:token", []string{sharedTok, mintedTok})
	// Second registration in the same scope must union, never evict mintedTok.
	cfg.RegisterDynamicSecrets("oauth:token", []string{rotatedTok})
	if got := cfg.ScrubText(mintedTok + " " + rotatedTok + " https://app.example.test/lightning/page/home"); strings.Contains(got, mintedTok) || strings.Contains(got, rotatedTok) || !strings.Contains(got, "https://app.example.test/lightning/page/home") {
		t.Fatalf("dynamic secret scope union or path preservation failed: %q", got)
	}
	if _, stillScriptOnly := allRuntimeScriptSourcedValues(cfg)[sharedTok]; stillScriptOnly {
		t.Fatalf("dynamic secret promotion failed to remove %q from allRuntimeScriptSourcedValues", sharedTok)
	}

	sink := &unavailableMetricIdentitySink{}
	vu := &VUser{
		Group:                "Declared group",
		Script:               &dsl.Script{Name: "overflow-test", Protocol: "http"},
		Variables:            map[string]string{"tok": mintedTok},
		Metrics:              NewMetricsCollector(16),
		SnapshotStore:        NewSnapshotStore(8),
		ResultSink:           sink,
		RedactionConfig:      cfg,
		iterationTemperature: "warm",
	}
	vu.runtimeRedaction.Store(cfg)

	if vu.liveRequestValuesAllowed(livetable.Request{Values: map[string]string{"col": mintedTok}}) {
		t.Fatalf("expected liveRequestValuesAllowed to block dynamic runtime secret before overflow")
	}

	// Overflow dynamic secret matcher budget while vu.runtimeCredentialRedactionErr remains nil.
	cfg.RegisterDynamicSecrets("oauth:overflow", []string{strings.Repeat("x", maxRuntimeSecretRedactionRawBytes+1)})
	if !cfg.publicationUnavailable() {
		t.Fatalf("expected publicationUnavailable()=true after dynamic matcher overflow")
	}
	if vu.runtimeCredentialRedactionErr != nil {
		t.Fatalf("expected vu.runtimeCredentialRedactionErr==nil to test dynamic-only overflow")
	}
	if vu.runtimeCredentialRedactionError() == nil {
		t.Fatalf("runtimeCredentialRedactionError() must return non-nil after dynamic matcher overflow")
	}
	if _, attachErr := attachConcurrentRuntimeMatcher(cfg, base); attachErr == nil {
		t.Fatalf("attachConcurrentRuntimeMatcher must fail closed when base is publicationUnavailable")
	}
	if attached := attachRuntimeSecretRedaction(unavailableRuntimePublicationConfig, base); !attached.publicationUnavailable() {
		t.Fatalf("attachRuntimeSecretRedaction must preserve publicationUnavailable when base config is unavailable")
	}
	if attached := attachRuntimeSecretRedaction(base, unavailableRuntimePublicationConfig); !attached.publicationUnavailable() {
		t.Fatalf("attachRuntimeSecretRedaction must preserve publicationUnavailable when runtime config is unavailable")
	}
	if vu.liveRequestValuesAllowed(livetable.Request{Values: map[string]string{"col": mintedTok}}) {
		t.Fatalf("liveRequestValuesAllowed failed open after dynamic secret overflow")
	}
	if vu.replayFinalVariables() != nil {
		t.Fatalf("replayFinalVariables must return nil when dynamic publication is unavailable")
	}

	tx := vu.recordTransaction("Declared tx", time.Millisecond, true, &protocol.Result{
		ErrorMessage:     mintedTok,
		DependencyTarget: mintedTok,
	})
	if tx.Success || tx.ActionName != "Action evidence unavailable" || len(tx.PropertyDimensions) != 0 || len(sink.transactions) != 1 || sink.transactions[0].ActionName != "Action evidence unavailable" {
		t.Fatalf("recordTransaction failed open after dynamic overflow: tx=%+v sink=%+v", tx, sink.transactions)
	}
	metricTx := <-vu.Metrics.ch
	if metricTx.Group != "Declared group" || metricTx.ActionName != "Declared tx" || strings.Contains(metricTx.ErrorMessage, mintedTok) {
		t.Fatalf("declared transaction metric identity or redaction broken: %+v", metricTx)
	}

	res := &protocol.Result{
		Success:          true,
		ActionName:       "secret-step-" + mintedTok,
		ErrorMessage:     mintedTok,
		Extractions:      map[string]string{"sid": mintedTok},
		Snapshot:         &protocol.ErrorSnapshot{ResponseBody: mintedTok},
		ProtocolEvidence: map[string]any{"detail": mintedTok},
	}
	_ = vu.recordResult(res, dsl.ErrorHandlingConfig{SnapshotOnError: true}, dsl.LogConfig{})
	if res.Success || res.ActionName != "runtime evidence detail unavailable" || strings.Contains(res.ErrorMessage, mintedTok) || len(res.Extractions) != 0 || len(vu.SnapshotStore.Snapshots()) != 0 {
		t.Fatalf("recordResult failed open after dynamic overflow: res=%+v snaps=%d", res, len(vu.SnapshotStore.Snapshots()))
	}

	ev := &ReplayActionEvent{ActionName: "step", Body: mintedTok, ErrorMessage: mintedTok}
	scrubReplayActionEvent(ev, cfg)
	if strings.Contains(ev.Body, mintedTok) || strings.Contains(ev.ErrorMessage, mintedTok) {
		t.Fatalf("scrubReplayActionEvent leaked after dynamic overflow: %+v", ev)
	}
	dbg := &ReplayDebugAction{ActionName: mintedTok, Target: mintedTok}
	scrubReplayDebugAction(dbg, cfg)
	if strings.Contains(dbg.ActionName, mintedTok) || strings.Contains(dbg.Target, mintedTok) {
		t.Fatalf("scrubReplayDebugAction leaked after dynamic overflow: %+v", dbg)
	}
	if got := recoveredPanicLogTextWithConfig(mintedTok, cfg); strings.Contains(got, mintedTok) {
		t.Fatalf("recoveredPanicLogTextWithConfig leaked after dynamic overflow: %q", got)
	}
}

func TestDefaultHeaderListBodyAssignmentsAndOAuthTokenURLCompositesAreRedacted(t *testing.T) {
	const (
		staticDefTok   = "short-def-tok-91"
		dynDefTok      = "short-dyn-tok-92"
		inlineJSPin    = "short-pin-93"
		inlineObjPin   = "short-obj-94"
		litOAuthPass   = "shortpw95"
		paramOAuthPass = "shortpw96"
	)

	// 1. Finding 1: static config.http.default_header_list and resolved DefaultHeaders / DefaultHeaderList.
	echoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = fmt.Fprintf(w, `<html><script>const accPassword = '%s'; const cfg = {password: '%s'};</script><body>auth=%s custom=%s</body></html>`,
			inlineJSPin, inlineObjPin, r.Header.Get("Authorization"), r.Header.Get("X-Api-Key"))
	}))
	defer echoSrv.Close()

	script := &dsl.Script{
		Name:     "default-header-list-and-body-scrub",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: echoSrv.URL,
			HTTP: dsl.HTTPConfig{
				DefaultHeaderList: dsl.HeaderList{
					{Name: "Authorization", Value: "Bearer " + staticDefTok},
				},
				DefaultHeaders: map[string]string{
					"X-Api-Key": "{{param.dyn_key}}",
				},
			},
		},
		Parameters: []dsl.Parameter{
			{Name: "dyn_key", Type: "list", Values: []string{dynDefTok}},
		},
		Actions: []dsl.Action{
			{Name: "echo", Type: "request", Method: "GET", Target: "/echo"},
		},
	}

	vals, err := ImportedCredentialValues(script)
	if err != nil {
		t.Fatalf("ImportedCredentialValues: %v", err)
	}
	if !strings.Contains(strings.Join(vals, "\n"), staticDefTok) {
		t.Fatalf("ImportedCredentialValues missed config.http.default_header_list credential: %v", vals)
	}
	resolvedVals, err := resolvedActionCredentialValues(script, &protocol.Action{
		DefaultHeaders: map[string]string{"X-Api-Key": dynDefTok},
		DefaultHeaderList: protocol.HeaderList{
			{Name: "Authorization", Value: "Bearer " + staticDefTok},
		},
	})
	if err != nil {
		t.Fatalf("resolvedActionCredentialValues: %v", err)
	}
	for _, want := range []string{staticDefTok, dynDefTok} {
		if !strings.Contains(strings.Join(resolvedVals, "\n"), want) {
			t.Fatalf("resolvedActionCredentialValues missed %q from resolved DefaultHeaders/DefaultHeaderList: %v", want, resolvedVals)
		}
	}

	// 2. Finding 2: ReplayScript + scrubSensitiveResultWithOptions redact echoed default headers AND inline JS body/HTML assignments.
	replayRes, err := ReplayScript(context.Background(), script, ReplayOptions{})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	replayJSON, _ := json.Marshal(replayRes)
	for _, secret := range []string{staticDefTok, dynDefTok, inlineJSPin, inlineObjPin} {
		if strings.Contains(string(replayJSON), secret) {
			t.Fatalf("ReplayScript leaked %q: %s", secret, replayJSON)
		}
	}

	cfg := buildTestRedactionForScript(t, script, nil)
	if cfg.HasRuntimeSecretMaterial() {
		t.Fatalf("HasRuntimeSecretMaterial() = true for literal-only script")
	}
	res := &protocol.Result{
		ResponseBody: `<script>const accPassword = '` + inlineJSPin + `';</script>`,
		HTML:         `<script>const cfg = {password: '` + inlineObjPin + `'};</script>`,
		Snapshot: &protocol.ErrorSnapshot{
			RequestBody:  `<script>const accPassword = '` + inlineJSPin + `';</script>`,
			ResponseBody: `<script>const cfg = {password: '` + inlineObjPin + `'};</script>`,
			HTML:         `<script>const accPassword = '` + inlineJSPin + `';</script>`,
			Resources: []protocol.ResourceSnapshot{
				{ResponseBody: `<script>const cfg = {password: '` + inlineObjPin + `'};</script>`},
			},
		},
	}
	scrubSensitiveResult(res, cfg)
	resJSON, _ := json.Marshal(res)
	for _, secret := range []string{inlineJSPin, inlineObjPin} {
		if strings.Contains(string(resJSON), secret) {
			t.Fatalf("scrubSensitiveResult leaked inline JS secret %q: %s", secret, resJSON)
		}
	}

	// 3. Finding 4: http_oauth2_token token_url Basic userinfo composite (literal + templated).
	oauthSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, "upstream rejected header %s", r.Header.Get("Authorization"))
	}))
	defer oauthSrv.Close()
	parsedOAuth, err := url.Parse(oauthSrv.URL)
	if err != nil {
		t.Fatalf("parse oauthSrv URL: %v", err)
	}
	litTokenURL := fmt.Sprintf("http://svc:%s@%s/oauth/token", litOAuthPass, parsedOAuth.Host)
	litBasic := base64.StdEncoding.EncodeToString([]byte("svc:" + litOAuthPass))
	paramBasic := base64.StdEncoding.EncodeToString([]byte("svc:" + paramOAuthPass))

	oauthScript := &dsl.Script{
		Name:     "oauth-token-url-userinfo",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: oauthSrv.URL},
		Parameters: []dsl.Parameter{
			{Name: "idp_pass", Type: "list", Values: []string{paramOAuthPass}},
		},
		Actions: []dsl.Action{
			{
				Name: "mint-lit",
				Type: "http_oauth2_token",
				Params: map[string]any{
					"token_url":  litTokenURL,
					"grant_type": "client_credentials",
					"client_id":  "cid",
					"save_as":    "tok1",
				},
			},
			{
				Name: "mint-param",
				Type: "http_oauth2_token",
				Params: map[string]any{
					"token_url":  fmt.Sprintf("http://svc:{{param.idp_pass}}@%s/oauth/token", parsedOAuth.Host),
					"grant_type": "client_credentials",
					"client_id":  "cid",
					"save_as":    "tok2",
				},
			},
		},
	}
	oauthVals, err := ImportedCredentialValues(oauthScript)
	if err != nil {
		t.Fatalf("ImportedCredentialValues(oauthScript): %v", err)
	}
	if !strings.Contains(strings.Join(oauthVals, "\n"), litBasic) {
		t.Fatalf("ImportedCredentialValues missed literal token_url Basic composite %q: %v", litBasic, oauthVals)
	}

	oauthReplay, err := ReplayScript(context.Background(), oauthScript, ReplayOptions{})
	if err != nil {
		t.Fatalf("ReplayScript(oauthScript): %v", err)
	}
	oauthReplayJSON, _ := json.Marshal(oauthReplay)
	for _, secret := range []string{litOAuthPass, litBasic, paramOAuthPass, paramBasic} {
		if strings.Contains(string(oauthReplayJSON), secret) {
			t.Fatalf("ReplayScript leaked OAuth token_url Basic secret/composite %q: %s", secret, oauthReplayJSON)
		}
	}
}

func TestRound4TemplatedURLsShakeoutAndConcurrentTransactionRedaction(t *testing.T) {
	const (
		queryKey       = "short-key-88"
		mixedPass      = "short-pass-99"
		neutralP1Pass  = "short-p1-77"
		tenantBasicPwd = "short-tenant-66"
		runtimeQueryK  = "short-rt-key-55"
		shakeoutSecret = "short-shake-44"
		siblingSecret  = "short-sibling-33"
	)

	// 1. Static templated URL collection: host template + literal query secret,
	// templated user + literal pass, neutral param in userinfo, and templated path + literal Basic composite.
	templatedScript := &dsl.Script{
		Name:     "templated-url-redaction",
		Protocol: "http",
		Parameters: []dsl.Parameter{
			{Name: "p1", Type: "list", Values: []string{neutralP1Pass}},
			{Name: "tenant", Type: "list", Values: []string{"acme"}},
		},
		Actions: []dsl.Action{
			{
				Name:   "templated-host-query",
				Type:   "request",
				Method: "GET",
				Target: "https://{{var.host}}/oauth/token?api_key=" + queryKey,
			},
			{
				Name:   "templated-user-lit-pass",
				Type:   "request",
				Method: "GET",
				Target: "https://{{param.user}}:" + mixedPass + "@idp.example.test/token",
			},
			{
				Name:   "neutral-param-pass",
				Type:   "request",
				Method: "GET",
				Target: "https://svc:{{param.p1}}@idp.example.test/token",
			},
			{
				Name:   "templated-path-basic-composite",
				Type:   "request",
				Method: "GET",
				Target: "https://svc:" + tenantBasicPwd + "@idp.example.test/{{param.tenant}}/token",
			},
		},
	}
	vals, plan, err := inspectImportedCredentials(templatedScript)
	if err != nil {
		t.Fatalf("inspectImportedCredentials(templatedScript): %v", err)
	}
	tenantBasicB64 := base64.StdEncoding.EncodeToString([]byte("svc:" + tenantBasicPwd))
	joinedVals := strings.Join(vals, "\n")
	for _, want := range []string{queryKey, mixedPass, neutralP1Pass, tenantBasicPwd, tenantBasicB64} {
		if !strings.Contains(joinedVals, want) {
			t.Fatalf("inspectImportedCredentials missed %q in %v", want, vals)
		}
	}
	if plan == nil {
		t.Fatalf("expected non-nil templatePlan for neutral param p1 in userinfo")
	}
	if _, ok := plan.expressions["param.p1"]; !ok {
		t.Fatalf("templatePlan missed neutral userinfo param.p1: %v", plan.expressions)
	}

	// Mixed-origin userinfo: {{secret.SVC_USER}}:short-pass-99 must still emit
	// resolvedPassword from appendScriptSourcedCompositeValues.
	surface, ok := urlUserinfoCredentialSurface(
		"action.params.token_url",
		"https://{{secret.SVC_USER}}:"+mixedPass+"@idp.example.test/oauth/token",
		"https://resolved-user:"+mixedPass+"@idp.example.test/oauth/token",
	)
	if !ok {
		t.Fatalf("expected urlUserinfoCredentialSurface ok=true")
	}
	scriptComposites := appendScriptSourcedCompositeValues(nil, surface)
	if !strings.Contains(strings.Join(scriptComposites, "\n"), mixedPass) {
		t.Fatalf("appendScriptSourcedCompositeValues dropped literal password on mixed-origin surface: %v", scriptComposites)
	}

	// Runtime http_oauth2_token with neutral-named variable k in token_url query.
	oauthQuerySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = fmt.Fprintf(w, "denied api_key=%s", r.URL.Query().Get("api_key"))
	}))
	defer oauthQuerySrv.Close()
	parsedQuerySrv, _ := url.Parse(oauthQuerySrv.URL)
	oauthQueryScript := &dsl.Script{
		Name:     "oauth-runtime-query",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: oauthQuerySrv.URL},
		Actions: []dsl.Action{
			{
				Name: "mint-query",
				Type: "http_oauth2_token",
				Params: map[string]any{
					"token_url":  "http://" + parsedQuerySrv.Host + "/oauth/token?api_key={{var.k}}",
					"grant_type": "client_credentials",
					"client_id":  "cid",
					"save_as":    "tok",
				},
			},
		},
	}
	oauthQueryReport, err := ReplayScript(context.Background(), oauthQueryScript, ReplayOptions{
		Variables: map[string]string{"k": runtimeQueryK},
	})
	if err != nil {
		t.Fatalf("ReplayScript(oauthQueryScript): %v", err)
	}
	oauthQueryBytes, _ := json.Marshal(oauthQueryReport)
	if strings.Contains(string(oauthQueryBytes), runtimeQueryK) {
		t.Fatalf("ReplayScript leaked runtime OAuth token_url query secret %q: %s", runtimeQueryK, oauthQueryBytes)
	}

	// 2. scrubReplayBody scrubs inline JS credential assignments.
	scrubbedReplayReqBody := scrubReplayBody(&protocol.Action{
		Body: []byte(`<script>const accPassword = 'short-pin-123';</script>`),
	}, nil)
	if strings.Contains(scrubbedReplayReqBody, "short-pin-123") {
		t.Fatalf("scrubReplayBody leaked inline JS credential assignment: %s", scrubbedReplayReqBody)
	}

	// 3. runShakeoutAction mergeExtractions failure scrubs action.Name via RedactionConfig
	// and withholds detail when observeExtractionClassification overflows inside mergeExtractions.
	rc, err := WithScriptSourcedSecretValueRedaction(nil, []string{shakeoutSecret})
	if err != nil {
		t.Fatalf("WithScriptSourcedSecretValueRedaction: %v", err)
	}
	shakeVU := &VUser{
		Script: &dsl.Script{
			Name:     "shake",
			Protocol: "http",
		},
		Proto: stubShakeoutResultProto{result: &protocol.Result{
			Success:     true,
			StatusCode:  200,
			Extractions: map[string]string{"big": strings.Repeat("x", maxVUserExtractionBytes+64)},
		}},
		Variables:       map[string]string{},
		RedactionConfig: ensureDynamicSecretRegistry(rc),
	}
	shakeErr := runShakeoutAction(context.Background(), shakeVU, dsl.Action{
		Name:   "step-" + shakeoutSecret,
		Type:   "request",
		Method: "GET",
		Target: "https://example.test",
	}, "action")
	if shakeErr == nil || strings.Contains(shakeErr.Error(), shakeoutSecret) || !strings.Contains(shakeErr.Error(), "extraction retention failed") {
		t.Fatalf("runShakeoutAction mergeExtractions error leaked secret or lost marker: %v", shakeErr)
	}
	overflowVU := &VUser{
		Script: &dsl.Script{
			Name:     "shake-overflow",
			Protocol: "http",
		},
		Proto: stubShakeoutResultProto{result: &protocol.Result{
			Success:     true,
			StatusCode:  200,
			Extractions: map[string]string{"access_token": strings.Repeat("t", maxRuntimeSecretRedactionRawBytes+64)},
		}},
		Variables:       map[string]string{},
		RedactionConfig: ensureDynamicSecretRegistry(rc),
	}
	unavailShakeErr := runShakeoutAction(context.Background(), overflowVU, dsl.Action{
		Name:   "step-" + shakeoutSecret,
		Type:   "request",
		Method: "GET",
		Target: "https://example.test",
	}, "action")
	if unavailShakeErr == nil || strings.Contains(unavailShakeErr.Error(), "step-") || !strings.Contains(unavailShakeErr.Error(), "extraction retention failed: error detail unavailable") {
		t.Fatalf("runShakeoutAction failed to withhold detail when resultPublicationUnavailable: %v", unavailShakeErr)
	}

	// 4. recordTransaction uses resultPublicationRedactionConfig() and omits public
	// evidence when concurrentPublicationUnavailable is true.
	siblingRC, err := WithSecretValueRedactionChecked(nil, []string{siblingSecret})
	if err != nil {
		t.Fatalf("WithSecretValueRedactionChecked: %v", err)
	}
	txVU := &VUser{
		Group:                          "grp",
		concurrentPublicationRedaction: siblingRC,
	}
	recordedTx := txVU.recordTransaction("tx-checkout", 0, false, &protocol.Result{
		CorrelationID: "corr-" + siblingSecret,
		Hierarchy:     "Checkout/" + siblingSecret,
		ErrorMessage:  "upstream echoed " + siblingSecret,
		Extractions:   map[string]string{"note": siblingSecret},
	})
	txJSON, _ := json.Marshal(recordedTx)
	if strings.Contains(string(txJSON), siblingSecret) {
		t.Fatalf("recordTransaction ignored concurrentPublicationRedaction and leaked sibling secret: %s", txJSON)
	}

	txVU.concurrentPublicationUnavailable = true
	omittedTx := txVU.recordTransaction("tx-public-name", 0, false, &protocol.Result{
		ErrorMessage: "sensitive detail",
		Extractions:  map[string]string{"tok": "val"},
	})
	if omittedTx.ActionName != "" || len(omittedTx.Extractions) != 0 || !omittedTx.ExtractionsIncomplete {
		t.Fatalf("recordTransaction failed to omit public evidence on concurrentPublicationUnavailable: %+v", omittedTx)
	}

	// 5. Password/passphrase/camelCase headers (X-AccPassword, ApproverPasswd, X-Password)
	// in DefaultHeaderList and action.HeaderList + Snapshot.ResponseHeaders.
	const (
		defaultHdrPwd = "short-defhdr-21"
		actionHdrPwd  = "short-acthdr-22"
		paramHdrPwd   = "short-prmhdr-23"
		respApiKey    = "short-srvkey-24"
	)
	hdrEchoSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Key", respApiKey)
		w.Header().Set("ApproverPasswd", "999")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, "echo def=%s act=%s prm=%s",
			r.Header.Get("X-AccPassword"),
			r.Header.Get("ApproverPasswd"),
			r.Header.Get("X-Password"),
		)
	}))
	defer hdrEchoSrv.Close()
	hdrScript := &dsl.Script{
		Name:     "password-header-redaction",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: hdrEchoSrv.URL,
			HTTP: dsl.HTTPConfig{
				DefaultHeaderList: []dsl.HeaderEntry{
					{Name: "X-AccPassword", Value: defaultHdrPwd},
				},
			},
		},
		Parameters: []dsl.Parameter{
			{Name: "h1", Type: "list", Values: []string{paramHdrPwd}},
		},
		Actions: []dsl.Action{
			{
				Name:   "send-hdrs",
				Type:   "request",
				Method: "GET",
				Target: "/check",
				HeaderList: []dsl.HeaderEntry{
					{Name: "ApproverPasswd", Value: actionHdrPwd},
					{Name: "X-Password", Value: "{{param.h1}}"},
				},
			},
		},
	}
	hdrReport, err := ReplayScript(context.Background(), hdrScript, ReplayOptions{})
	if err != nil {
		t.Fatalf("ReplayScript(hdrScript): %v", err)
	}
	hdrReportBytes, _ := json.Marshal(hdrReport)
	for _, secret := range []string{defaultHdrPwd, actionHdrPwd, paramHdrPwd, respApiKey} {
		if strings.Contains(string(hdrReportBytes), secret) {
			t.Fatalf("ReplayScript leaked password/API-key header secret %q: %s", secret, hdrReportBytes)
		}
	}

	snapRes := &protocol.Result{
		Success: false,
		Snapshot: &protocol.ErrorSnapshot{
			RequestHeaders:  map[string]string{"ApproverPasswd": "999"},
			ResponseHeaders: map[string]string{"Api-Key": respApiKey, "X-Passphrase": "short-passphrase-25"},
		},
	}
	scrubSensitiveResultWithOptions(snapRes, nil, true)
	if snapRes.Snapshot.RequestHeaders["ApproverPasswd"] != redactedPayloadValue ||
		snapRes.Snapshot.ResponseHeaders["Api-Key"] != redactedPayloadValue ||
		snapRes.Snapshot.ResponseHeaders["X-Passphrase"] != redactedPayloadValue {
		t.Fatalf("scrubSensitiveHeaders missed password/api-key/passphrase header: %+v", snapRes.Snapshot)
	}
}

func TestEnsureDynamicRedactionRegistryDoesNotMutateSharedConfigConcurrently(t *testing.T) {
	sharedBase, err := WithScriptSourcedSecretValueRedaction(nil, []string{"synth-shared-lit-9901"})
	if err != nil {
		t.Fatalf("WithScriptSourcedSecretValueRedaction: %v", err)
	}
	if sharedBase.findDynamicRegistry() != nil {
		t.Fatalf("expected sharedBase.dynamic == nil before VUser initialization")
	}

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = sharedBase.ScrubText("pin=synth-shared-lit-9901")
					_ = sharedBase.HasRuntimeSecretMaterial()
					_ = sharedBase.findDynamicRegistry()
				}
			}
		}()
	}

	var writers sync.WaitGroup
	for i := range 8 {
		writers.Add(1)
		go func(id int) {
			defer writers.Done()
			vu := &VUser{
				ID:                          id,
				Script:                      &dsl.Script{Name: "dyn-race", Protocol: "http"},
				Proto:                       &preflightRedactionBindTestProto{minted: "synth-dyn-9902"},
				Metrics:                     NewMetricsCollector(8),
				RedactionConfig:             sharedBase,
				runtimeCredentialBase:       sharedBase,
				runtimeCredentialStateReady: true,
			}
			vu.bindRuntimeTextRedactor()
			vu.RedactionConfig.RegisterDynamicSecrets("oauth:test", []string{fmt.Sprintf("synth-vu-tok-%d", id)})
			if got := vu.RedactionConfig.ScrubText(fmt.Sprintf("Bearer synth-vu-tok-%d", id)); strings.Contains(got, "synth-vu-tok-") {
				t.Errorf("VUser %d dynamic secret was not redacted: %q", id, got)
			}
		}(i)
	}
	writers.Wait()
	close(stop)
	readers.Wait()

	if sharedBase.findDynamicRegistry() != nil {
		t.Fatalf("ensureDynamicRedactionRegistry mutated sharedBase.dynamic in place")
	}
}

type stubShakeoutResultProto struct {
	result *protocol.Result
}

func (s stubShakeoutResultProto) Name() string                               { return "http" }
func (s stubShakeoutResultProto) Init(context.Context, map[string]any) error { return nil }
func (s stubShakeoutResultProto) Execute(context.Context, *protocol.Action) (*protocol.Result, error) {
	return s.result, nil
}
func (s stubShakeoutResultProto) Teardown(context.Context) error    { return nil }
func (s stubShakeoutResultProto) HealthCheck(context.Context) error { return nil }
