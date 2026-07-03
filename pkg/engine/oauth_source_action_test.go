package engine

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	protocolhttp "github.com/jasonczech/apexload/pkg/protocol/http"
)

func TestHTTPOAuth2TokenActionUsesConfiguredHTTPClient(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "via-proxy",
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

	vu := &VUser{
		Proto:     proto,
		Variables: map[string]string{},
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
	if got := vu.Variables["oauth_token"]; got != "via-proxy" {
		t.Fatalf("oauth_token = %q, want via-proxy", got)
	}
}
