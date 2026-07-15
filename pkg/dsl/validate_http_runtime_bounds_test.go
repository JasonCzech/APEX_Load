package dsl

import (
	"strings"
	"testing"
	"time"

	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

func TestValidateHTTPRuntimeControlBounds(t *testing.T) {
	tooManyRedirects := httpprotocol.MaxConfiguredRedirects + 1
	cases := []struct {
		name   string
		mutate func(*Script)
		want   string
	}{
		{name: "action timeout", mutate: func(script *Script) { script.Actions[0].TimeoutMs = MaxConfiguredProtocolTimeoutMS + 1 }, want: "timeout_ms must be between"},
		{name: "action redirects", mutate: func(script *Script) { script.Actions[0].MaxRedirects = &tooManyRedirects }, want: "max_redirects must be between"},
		{name: "SSE timeout", mutate: func(script *Script) {
			script.Actions[0].Params = map[string]any{"stream_timeout_ms": float64(MaxConfiguredProtocolTimeoutMS) + 1}
		}, want: "params.stream_timeout_ms"},
		{name: "fractional SSE timeout", mutate: func(script *Script) { script.Actions[0].Params = map[string]any{"stream_timeout_ms": 1.5} }, want: "params.stream_timeout_ms"},
		{name: "network jitter", mutate: func(script *Script) {
			script.Config.WebRuntime.NetworkSimulation.JitterMs = MaxConfiguredProtocolTimeoutMS + 1
		}, want: "network_simulation.jitter_ms must not exceed"},
		{name: "stream buffer", mutate: func(script *Script) {
			script.Config.WebRuntime.Streaming.BufferSizeSeconds = int(httpprotocol.MaxConfiguredStreamBuffer/time.Second) + 1
		}, want: "streaming.buffer_size_seconds must not exceed"},
		{name: "stream bitrate", mutate: func(script *Script) {
			script.Config.WebRuntime.Streaming.FixedBitrateKbps = httpprotocol.MaxConfiguredStreamKbps + 1
		}, want: "streaming.fixed_bitrate_kbps must not exceed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := &Script{Name: "http bounds", Protocol: "http", Config: ScriptConfig{BaseURL: "https://example.test"}, Actions: []Action{{Name: "request", Type: "request", Target: "/"}}}
			tc.mutate(script)
			issues := ValidateScriptIssues(script)
			if !strings.Contains(strings.Join(issues, "\n"), tc.want) {
				t.Fatalf("issues = %v, want %q", issues, tc.want)
			}
		})
	}
}
