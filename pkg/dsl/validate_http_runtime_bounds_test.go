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
		{name: "unsupported action method", mutate: func(script *Script) { script.Actions[0].Method = "PROPFIND" }, want: `unsupported HTTP method "PROPFIND"`},
		{name: "unsupported action auth", mutate: func(script *Script) { script.Actions[0].Auth = &AuthConfig{Type: "ntlm"} }, want: "configure an external auth flow"},
		{name: "action timeout", mutate: func(script *Script) { script.Actions[0].TimeoutMs = MaxConfiguredProtocolTimeoutMS + 1 }, want: "timeout_ms must be between"},
		{name: "action redirects", mutate: func(script *Script) { script.Actions[0].MaxRedirects = &tooManyRedirects }, want: "max_redirects must be between"},
		{name: "SSE timeout", mutate: func(script *Script) {
			script.Actions[0].Params = map[string]any{"stream_timeout_ms": float64(MaxConfiguredProtocolTimeoutMS) + 1}
		}, want: "params.stream_timeout_ms"},
		{name: "fractional SSE timeout", mutate: func(script *Script) { script.Actions[0].Params = map[string]any{"stream_timeout_ms": 1.5} }, want: "params.stream_timeout_ms"},
		{name: "extractor expression", mutate: func(script *Script) {
			script.Actions[0].Extractors = []Extractor{{
				Name:       "oversized",
				Source:     "body",
				Type:       "regex",
				Expression: strings.Repeat("x", httpprotocol.MaxExtractorExpressionBytes+1),
			}}
		}, want: "expression must not exceed"},
		{name: "header assertion name", mutate: func(script *Script) {
			script.Actions[0].Assertions = []Assertion{{Type: "header_contains", Value: " ", Expected: "xml"}}
		}, want: "header name is required"},
		{name: "header assertion expected type", mutate: func(script *Script) {
			script.Actions[0].Assertions = []Assertion{{Type: "header_contains", Value: "Content-Type", Expected: 1}}
		}, want: "expected string"},
		{name: "invalid assertion regex", mutate: func(script *Script) {
			script.Actions[0].Assertions = []Assertion{{Type: "body_regex_capture_equals", Value: "(", Expected: "value"}}
		}, want: "invalid pattern"},
		{name: "oversized assertion regex", mutate: func(script *Script) {
			script.Actions[0].Assertions = []Assertion{{
				Type: "body_regex_capture_equals", Value: strings.Repeat("x", 4097), Expected: "value",
			}}
		}, want: "pattern exceeds"},
		{name: "unknown assertion", mutate: func(script *Script) {
			script.Actions[0].Assertions = []Assertion{{Type: "status_cod", Expected: 200}}
		}, want: "unsupported assertion type"},
		{name: "fractional status assertion", mutate: func(script *Script) {
			script.Actions[0].Assertions = []Assertion{{Type: "status_code", Expected: 200.5}}
		}, want: "expected integer"},
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

func TestValidateHTTPSequenceChildAssertions(t *testing.T) {
	newScript := func(assertions any) *Script {
		return &Script{
			Name:     "sequence assertions",
			Protocol: "http",
			Config:   ScriptConfig{BaseURL: "https://example.test"},
			Actions: []Action{{
				Name: "sequence",
				Type: "http_sequence",
				Params: map[string]any{"requests": []any{map[string]any{
					"name":       "child",
					"method":     "GET",
					"target":     "/",
					"assertions": assertions,
				}}},
			}},
		}
	}

	t.Run("invalid definition", func(t *testing.T) {
		issues := ValidateScriptIssues(newScript([]any{map[string]any{
			"type": "body_regex_capture_equals", "value": "(", "expected": "value",
		}}))
		if got := strings.Join(issues, "\n"); !strings.Contains(got, "invalid pattern") ||
			!strings.Contains(got, `.params.requests[0] "child": assertions[0]`) {
			t.Fatalf("issues = %v", issues)
		}
	})

	t.Run("count bound", func(t *testing.T) {
		assertions := make([]Assertion, MaxActionAssertions+1)
		for index := range assertions {
			assertions[index] = Assertion{Type: "status_code", Expected: 200}
		}
		issues := ValidateScriptIssues(newScript(assertions))
		if got := strings.Join(issues, "\n"); !strings.Contains(got, "assertions must not exceed") {
			t.Fatalf("issues = %v", issues)
		}
	})

	t.Run("malformed shape", func(t *testing.T) {
		issues := ValidateScriptIssues(newScript(map[string]any{
			"type": "status_code", "expected": 200,
		}))
		if got := strings.Join(issues, "\n"); !strings.Contains(got, "must be a valid assertion array") {
			t.Fatalf("issues = %v", issues)
		}
	})
}

func TestValidateHTTPSequenceRejectsMalformedExtractorAndChildShapes(t *testing.T) {
	newScript := func(field string, value any) *Script {
		request := map[string]any{
			"name": "child", "method": "GET", "target": "/",
			field: value,
		}
		return &Script{
			Name:     "sequence child shapes",
			Protocol: "http",
			Config:   ScriptConfig{BaseURL: "https://example.test"},
			Actions: []Action{{
				Name: "sequence", Type: "http_sequence",
				Params: map[string]any{"requests": []any{request}},
			}},
		}
	}

	if got := strings.Join(ValidateScriptIssues(newScript(
		"extractors",
		map[string]any{"name": "token", "type": "regex"},
	)), "\n"); !strings.Contains(got, `.extractors must be a valid extractor array`) {
		t.Fatalf("malformed extractor issues = %q", got)
	}

	if got := strings.Join(ValidateScriptIssues(newScript(
		"auth",
		"not-an-auth-object",
	)), "\n"); !strings.Contains(got, `must be a valid HTTP request object`) {
		t.Fatalf("malformed child issues = %q", got)
	}
}

func TestValidateHTTPAllowsUnsupportedAuthOnlyOnExecutionBlockedDraft(t *testing.T) {
	t.Parallel()
	script := &Script{
		Name:     "blocked auth import",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name: "blocked", Type: "request", Target: "/",
			Auth:     &AuthConfig{Type: "ntlm"},
			Metadata: map[string]any{"execution_blocker": "import_review_required"},
		}},
	}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("blocked draft issues = %v", issues)
	}
	delete(script.Actions[0].Metadata, "execution_blocker")
	script.Actions[0].Metadata["review_gate"] = "bruno_auth"
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("legacy Bruno blocked draft issues = %v", issues)
	}
	delete(script.Actions[0].Metadata, "review_gate")
	if issues := ValidateScriptIssues(script); !strings.Contains(strings.Join(issues, "\n"), "external auth flow") {
		t.Fatalf("ungated issues = %v", issues)
	}
}

func TestValidateHTTPSequenceSyntheticMethodIsNotTreatedAsHTTPVerb(t *testing.T) {
	newSequence := func(childMethod string) *Script {
		return &Script{
			Name:     "sequence method",
			Protocol: "http",
			Config:   ScriptConfig{BaseURL: "https://example.test"},
			Actions: []Action{{
				Name:   "sequence",
				Type:   "http_sequence",
				Method: "SEQUENCE",
				Params: map[string]any{"requests": []any{map[string]any{
					"name": "child", "method": childMethod, "target": "/",
				}}},
			}},
		}
	}

	if issues := ValidateScriptIssues(newSequence("GET")); len(issues) != 0 {
		t.Fatalf("synthetic sequence method should be validated by the container action, got %v", issues)
	}

	if issues := ValidateScriptIssues(newSequence("TRACE")); !strings.Contains(strings.Join(issues, "\n"), `params.requests[0] "child": unsupported HTTP method/type "TRACE"`) {
		t.Fatalf("unsupported child method should still fail validation, got %v", issues)
	}

	ordinary := &Script{
		Name:     "ordinary request",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name: "request", Type: "GET", Method: "SEQUENCE", Target: "/",
		}},
	}
	if issues := ValidateScriptIssues(ordinary); !strings.Contains(strings.Join(issues, "\n"), `unsupported HTTP method "SEQUENCE"`) {
		t.Fatalf("synthetic method should remain invalid for ordinary requests, got %v", issues)
	}
}
