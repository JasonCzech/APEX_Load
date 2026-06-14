package agentforce

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestAnalyzeDetectsFullAgentLifecycle(t *testing.T) {
	script := &dsl.Script{
		Init: []dsl.Action{{Name: "Token", Type: "http_oauth2_token"}},
		Actions: []dsl.Action{
			{Name: "Start", Type: "POST", Method: "POST", Target: "https://api.salesforce.com/einstein/ai-agent/v1/agents/0Xx/sessions"},
			{
				Name: "Send", Type: "POST", Method: "POST",
				Target:     "https://api.salesforce.com/einstein/ai-agent/v1/sessions/abc/messages",
				Extractors: []dsl.Extractor{{Name: "agent_text", Source: "body", Type: "sse_concat", Expression: "TextChunk"}},
			},
			{Name: "End", Type: "DELETE", Method: "DELETE", Target: "https://api.salesforce.com/einstein/ai-agent/v1/sessions/abc"},
		},
	}

	review := Analyze(script)
	if !review.Detected || !review.Streaming {
		t.Fatalf("review = %#v, want detected + streaming", review)
	}
	if len(review.SessionVars) != 1 || review.SessionVars[0].Name != "agent_session_id" {
		t.Fatalf("session vars = %#v, want agent_session_id", review.SessionVars)
	}
	if len(review.Blockers) != 0 {
		t.Fatalf("blockers = %#v, want none (token + SSE handled)", review.Blockers)
	}
	gotOrder := make([]string, 0, len(review.Lifecycle))
	for _, step := range review.Lifecycle {
		gotOrder = append(gotOrder, step.Step)
	}
	want := []string{"token", "session_start", "send_message", "end_session"}
	if strings.Join(gotOrder, ",") != strings.Join(want, ",") {
		t.Fatalf("lifecycle order = %v, want %v", gotOrder, want)
	}
}

func TestAnalyzeFlagsMissingTokenAndUnhandledStream(t *testing.T) {
	script := &dsl.Script{
		Actions: []dsl.Action{{
			Name: "Stream", Type: "POST", Method: "POST",
			Target: "https://api.salesforce.com/einstein/ai-agent/v1/sessions/abc/messages/stream",
		}},
	}
	review := Analyze(script)
	if !review.Detected || !review.Streaming {
		t.Fatalf("review = %#v, want detected + streaming", review)
	}
	var tokenBlocker, sseBlocker bool
	for _, blocker := range review.Blockers {
		if strings.Contains(blocker, "OAuth2 access token") {
			tokenBlocker = true
		}
		if strings.Contains(blocker, "stream=sse") {
			sseBlocker = true
		}
	}
	if !tokenBlocker || !sseBlocker {
		t.Fatalf("blockers = %#v, want token + sse blockers", review.Blockers)
	}
}

func TestAnalyzeResolvesRelativeTargetsAgainstBaseURL(t *testing.T) {
	// OpenAPI/Postman imports often place the agent prefix in the base URL and
	// leave action targets relative.
	script := &dsl.Script{
		Config: dsl.ScriptConfig{BaseURL: "https://api.salesforce.com/einstein/ai-agent/v1"},
		Init:   []dsl.Action{{Name: "Token", Type: "http_oauth2_token"}},
		Actions: []dsl.Action{
			{Name: "Start", Type: "POST", Method: "POST", Target: "/agents/0Xx/sessions"},
			{Name: "Send", Type: "POST", Method: "POST", Target: "/sessions/abc/messages"},
		},
	}
	review := Analyze(script)
	if !review.Detected {
		t.Fatalf("review = %#v, want detected via base-URL prefix", review)
	}
	steps := map[string]bool{}
	for _, step := range review.Lifecycle {
		steps[step.Step] = true
	}
	if !steps["session_start"] || !steps["send_message"] {
		t.Fatalf("lifecycle = %#v, want session_start + send_message", review.Lifecycle)
	}
}

func TestAnalyzeFlagsStreamEndpointEvenWhenSiblingHandlesSSE(t *testing.T) {
	// A real streaming endpoint without SSE handling must be flagged even if an
	// unrelated sibling action carries an SSE extractor.
	script := &dsl.Script{
		Config: dsl.ScriptConfig{BaseURL: "https://api.salesforce.com/einstein/ai-agent/v1"},
		Init:   []dsl.Action{{Name: "Token", Type: "http_oauth2_token"}},
		Actions: []dsl.Action{
			{
				Name: "Send (non-stream, has SSE extractor)", Type: "POST", Method: "POST",
				Target:     "/sessions/abc/messages",
				Extractors: []dsl.Extractor{{Name: "agent_text", Source: "body", Type: "sse_concat", Expression: "TextChunk"}},
			},
			{Name: "Stream (no SSE handling)", Type: "POST", Method: "POST", Target: "/sessions/abc/messages/stream"},
		},
	}
	review := Analyze(script)
	found := false
	for _, blocker := range review.Blockers {
		if strings.Contains(blocker, "stream=sse") {
			found = true
		}
	}
	if !found {
		t.Fatalf("blockers = %#v, want streaming-without-SSE blocker for the unhandled stream endpoint", review.Blockers)
	}
}

func TestAnalyzeIgnoresNonAgentScripts(t *testing.T) {
	script := &dsl.Script{
		Actions: []dsl.Action{{Type: "GET", Method: "GET", Target: "https://example.com/api/foo"}},
	}
	if review := Analyze(script); review.Detected {
		t.Fatalf("review = %#v, want not detected", review)
	}
}
