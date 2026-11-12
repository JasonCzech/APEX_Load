package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	protocolhttp "github.com/jasonczech/apexload/pkg/protocol/http"
	_ "github.com/jasonczech/apexload/pkg/protocol/soap"
)

func TestHTTPOAuth2TokenActionUsesConfiguredHTTPClient(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "xy",
			"expires_in":   300,
		})
	}))
	defer tokenServer.Close()

	var proxyCalls atomic.Int32
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		outReq := r.Clone(r.Context())
		outReq.RequestURI = ""
		resp, err := http.DefaultTransport.RoundTrip(outReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxyServer.Close()

	proto := &protocolhttp.HTTP{}
	if err := proto.Init(context.Background(), map[string]any{
		"proxy_mode": "custom",
		"proxy_url":  proxyServer.URL,
	}); err != nil {
		t.Fatalf("HTTP Init: %v", err)
	}
	defer proto.Teardown(context.Background())

	sink := &captureResultSink{}
	vu := &VUser{
		Proto:      proto,
		Variables:  map[string]string{},
		Metrics:    NewMetricsCollector(4),
		ResultSink: sink,
	}
	err := vu.handleHTTPOAuth2TokenAction(context.Background(), dsl.Action{
		Name: "oauth",
		Type: "http_oauth2_token",
		Params: map[string]any{
			"token_url":     tokenServer.URL,
			"grant_type":    "client_credentials",
			"client_id":     "client",
			"client_secret": "secret",
			"save_as":       "oauth_token",
		},
	})
	if err != nil {
		t.Fatalf("handleHTTPOAuth2TokenAction returned error: %v", err)
	}
	if proxyCalls.Load() == 0 {
		t.Fatal("OAuth token action did not use the HTTP protocol's configured proxy client")
	}
	if got := vu.Variables["oauth_token"]; got != "xy" {
		t.Fatalf("oauth_token = %q, want xy", got)
	}
	if message := vu.renderErrorMessage(dsl.Action{Value: "token={{var.oauth_token}}"}); strings.Contains(message, "xy") {
		t.Fatalf("source/log message retained issued OAuth token: %q", message)
	}
	vu.recordResult(&protocol.Result{
		ActionName:   "neutral-echo",
		ResponseBody: "ordinary response echoed xy",
		ErrorMessage: "ordinary error echoed xy",
		Success:      false,
	}, dsl.ErrorHandlingConfig{}, dsl.LogConfig{})
	if sink.result == nil {
		t.Fatal("OAuth echo result was not retained")
	}
	encoded, err := json.Marshal(sink.result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "xy") {
		t.Fatalf("neutral result retained issued OAuth token: %s", encoded)
	}
}

func TestHTTPOAuth2TokenActionFailsClosedWithoutPolicyAwareProtocolClient(t *testing.T) {
	vu := &VUser{
		Proto: &stubProtocol{},
		Script: &dsl.Script{Metadata: &dsl.ScriptMetadata{RuntimePolicy: &dsl.ScriptRuntimePolicy{
			AllowedTargetHosts: []string{"tokens.example.test"},
		}}},
		Variables: map[string]string{},
	}
	err := vu.handleHTTPOAuth2TokenAction(context.Background(), dsl.Action{
		Name: "oauth",
		Type: "http_oauth2_token",
		Params: map[string]any{
			"token_url":     "https://tokens.example.test/oauth/token",
			"grant_type":    "client_credentials",
			"client_id":     "client",
			"client_secret": "{{secret.oauth_client_secret}}",
		},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot enforce the CI runtime target policy") {
		t.Fatalf("OAuth policy error = %v, want fail-closed protocol-client rejection", err)
	}

	if err := protocol.ValidateTargetURL("https://attacker.example.test/token", vu.Script.Metadata.RuntimePolicy.AllowedTargetHosts); err == nil {
		t.Fatal("test policy unexpectedly allowed attacker target")
	}
}

func TestReplayFinalVariablesScrubDynamicallyIssuedOAuthToken(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "xy", "expires_in": 300})
	}))
	defer tokenServer.Close()

	script := &dsl.Script{
		Name:     "oauth replay",
		Protocol: "http",
		Actions: []dsl.Action{{
			Name: "oauth",
			Type: "http_oauth2_token",
			Params: map[string]any{
				"token_url":     tokenServer.URL,
				"grant_type":    "client_credentials",
				"client_id":     "client",
				"client_secret": "secret",
				"save_as":       "custom_value",
			},
		}},
	}
	report, err := ReplayScript(context.Background(), script, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if value := report.FinalVariables["custom_value"]; strings.Contains(value, "xy") || value == "" {
		t.Fatalf("replay final variable retained dynamically issued token: %q", value)
	}
}

func TestHTTPOAuth2TokenActionWorksOnSOAPProtocolWithProxyAndTargetPolicy(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "soap-token-123",
			"expires_in":   300,
		})
	}))
	defer tokenServer.Close()

	var proxyCalls atomic.Int32
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyCalls.Add(1)
		outReq := r.Clone(r.Context())
		outReq.RequestURI = ""
		resp, err := http.DefaultTransport.RoundTrip(outReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for key, values := range resp.Header {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxyServer.Close()

	soapProto, err := protocol.Get("soap")
	if err != nil {
		t.Fatalf("protocol.Get(soap): %v", err)
	}
	if err := soapProto.Init(context.Background(), map[string]any{
		"proxy_mode": "custom",
		"proxy_url":  proxyServer.URL,
	}); err != nil {
		t.Fatalf("soap Init: %v", err)
	}
	defer func() { _ = soapProto.Teardown(context.Background()) }()

	vu := &VUser{
		Proto: soapProto,
		Script: &dsl.Script{
			Protocol: "soap",
			Metadata: &dsl.ScriptMetadata{
				RuntimePolicy: &dsl.ScriptRuntimePolicy{
					AllowedTargetHosts: []string{"127.0.0.1", "localhost"},
				},
			},
		},
		Variables: map[string]string{},
		Metrics:   NewMetricsCollector(4),
	}
	err = vu.handleHTTPOAuth2TokenAction(context.Background(), dsl.Action{
		Name: "soap-oauth",
		Type: "http_oauth2_token",
		Params: map[string]any{
			"token_url":     tokenServer.URL,
			"grant_type":    "client_credentials",
			"client_id":     "soap-client",
			"client_secret": "soap-secret",
			"save_as":       "soap_access_token",
		},
	})
	if err != nil {
		t.Fatalf("handleHTTPOAuth2TokenAction on SOAP returned error: %v", err)
	}
	if proxyCalls.Load() == 0 {
		t.Fatal("expected SOAP OAuth token request to use SOAP's configured proxy HTTP client")
	}
	if got := vu.Variables["soap_access_token"]; got != "soap-token-123" {
		t.Fatalf("soap_access_token = %q, want soap-token-123", got)
	}
}

func TestClearIterationUserStateHonorsClearCookiesPerIterationForSOAP(t *testing.T) {
	var seenCookies []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenCookies = append(seenCookies, r.Header.Get("Cookie"))
		if r.Header.Get("Cookie") == "" {
			http.SetCookie(w, &http.Cookie{Name: "soap_sid", Value: "v1", Path: "/"})
		}
		w.Header().Set("Content-Type", "text/xml")
		_, _ = io.WriteString(w, `<Envelope><Body><Ok/></Body></Envelope>`)
	}))
	defer ts.Close()

	soapProto, err := protocol.Get("soap")
	if err != nil {
		t.Fatalf("protocol.Get(soap): %v", err)
	}
	if err := soapProto.Init(context.Background(), map[string]any{"base_url": ts.URL}); err != nil {
		t.Fatalf("soap Init: %v", err)
	}
	defer func() { _ = soapProto.Teardown(context.Background()) }()

	clearTrue := true
	cfg := dsl.ScriptConfig{
		BaseURL: ts.URL,
		HTTP:    dsl.HTTPConfig{ClearCookiesPerIter: &clearTrue},
	}
	vu := &VUser{
		Proto:  soapProto,
		Script: &dsl.Script{Protocol: "soap", Config: cfg},
	}

	// Iteration 1 sets cookie in jar and replays it on the second call in iteration 1.
	if _, err := soapProto.Execute(context.Background(), &protocol.Action{Target: "/ws", Body: []byte(`<Envelope/>`)}); err != nil {
		t.Fatalf("Execute 1: %v", err)
	}
	if _, err := soapProto.Execute(context.Background(), &protocol.Action{Target: "/ws", Body: []byte(`<Envelope/>`)}); err != nil {
		t.Fatalf("Execute 2: %v", err)
	}
	if len(seenCookies) != 2 || !strings.Contains(seenCookies[1], "soap_sid=v1") {
		t.Fatalf("seenCookies before iteration clear = %v, want soap_sid=v1 on second call", seenCookies)
	}

	if step, err := vu.clearIterationUserState(context.Background(), &cfg); err != nil {
		t.Fatalf("clearIterationUserState failed at %s: %v", step, err)
	}

	if _, err := soapProto.Execute(context.Background(), &protocol.Action{Target: "/ws", Body: []byte(`<Envelope/>`)}); err != nil {
		t.Fatalf("Execute 3: %v", err)
	}
	if seenCookies[2] != "" {
		t.Fatalf("cookie after clearIterationUserState on SOAP = %q, want empty", seenCookies[2])
	}
}
