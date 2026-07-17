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
