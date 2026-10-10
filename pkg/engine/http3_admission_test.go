package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

func http3EngineScript(t *testing.T, baseURL, version string) *dsl.Script {
	t.Helper()
	return &dsl.Script{Name: "h3", Version: "1", Protocol: "http",
		Config:  dsl.ScriptConfig{BaseURL: baseURL, HTTP: dsl.HTTPConfig{Version: version}},
		Actions: []dsl.Action{{Name: "home", Type: "GET", Target: "/"}}}
}

func TestHTTP3AdmissionBlocksEngineLaunchAndReplay(t *testing.T) {
	t.Setenv(httpprotocol.EnvHTTP3, "")
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { requests.Add(1) }))
	t.Cleanup(server.Close)
	script := http3EngineScript(t, server.URL, "auto+h3")
	err := importExecutionGateError(script, "launch")
	if err == nil || !strings.Contains(err.Error(), "script launch is blocked") || !strings.Contains(err.Error(), httpprotocol.EnvHTTP3) {
		t.Fatalf("launch gate = %v, want a refusal naming %s", err, httpprotocol.EnvHTTP3)
	}
	if _, err := ReplayScript(context.Background(), script, ReplayOptions{}); err == nil || !strings.Contains(err.Error(), "script replay is blocked") {
		t.Fatalf("replay = %v, want the HTTP/3 refusal", err)
	}
	if _, err := New(Config{Script: script, VUsers: 1}); err == nil || !strings.Contains(err.Error(), httpprotocol.EnvHTTP3) {
		t.Fatalf("engine.New = %v, want the HTTP/3 refusal", err)
	}
	if n := requests.Load(); n != 0 {
		t.Fatalf("a refused HTTP/3 run sent %d requests", n)
	}
	// The flag alone is not enough before the transport ships.
	t.Setenv(httpprotocol.EnvHTTP3, "true")
	if err := importExecutionGateError(script, "launch"); err == nil || !strings.Contains(err.Error(), "no HTTP/3 transport") {
		t.Fatalf("flag on, no transport: %v", err)
	}
	// Scripts without HTTP/3 are unaffected by the flag.
	if err := importExecutionGateError(http3EngineScript(t, server.URL, "2"), "launch"); err != nil {
		t.Fatalf("an HTTP/2 script was blocked: %v", err)
	}
}
