package agentforce

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// importedAgentScript models a freshly-imported Agentforce flow with NO replay
// wiring: a captured (stale) bearer token, a literal session id in the message
// path, no SSE handling, and no end-session teardown.
func importedAgentScript() *dsl.Script {
	return &dsl.Script{
		Name:     "Agent Flow",
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://acme.my.salesforce.com"},
		Actions: []dsl.Action{
			{
				Name:    "Start Session",
				Type:    "POST",
				Method:  "POST",
				Target:  "https://acme.my.salesforce.com/einstein/ai-agent/v1/agents/0XxAGENT/sessions",
				Headers: map[string]string{"Authorization": "Bearer 00Dxx!REC0RDED", "Content-Type": "application/json"},
				Body:    `{"externalSessionKey":"abc"}`,
			},
			{
				Name:    "Send Message",
				Type:    "POST",
				Method:  "POST",
				Target:  "https://acme.my.salesforce.com/einstein/ai-agent/v1/sessions/SESSION123/messages/stream",
				Headers: map[string]string{"Authorization": "Bearer 00Dxx!REC0RDED", "Content-Type": "application/json"},
				Body:    `{"message":{"sequenceId":1,"type":"Text","text":"hi"}}`,
			},
		},
	}
}

func findExtractor(action dsl.Action, name string) (dsl.Extractor, bool) {
	for _, ext := range action.Extractors {
		if ext.Name == name {
			return ext, true
		}
	}
	return dsl.Extractor{}, false
}

func TestTransformMakesAgentforceScriptReplayReady(t *testing.T) {
	script := importedAgentScript()
	changes := Transform(script)
	if len(changes) == 0 {
		t.Fatal("Transform returned no changes for an unwired Agentforce flow")
	}

	// 1. Token init action prepended.
	if len(script.Init) != 1 || script.Init[0].Type != "http_oauth2_token" {
		t.Fatalf("init = %#v, want a prepended http_oauth2_token action", script.Init)
	}
	if got := script.Init[0].Params["save_as"]; got != agentTokenVar {
		t.Fatalf("token save_as = %v, want %q", got, agentTokenVar)
	}
	if got := script.Init[0].Params["grant_type"]; got != "client_credentials" {
		t.Fatalf("token grant_type = %v, want client_credentials", got)
	}

	start := script.Actions[0]
	message := script.Actions[1]

	// 2. session id extractor on session-start.
	ext, ok := findExtractor(start, agentSessionVar)
	if !ok || ext.Type != "jsonpath" || ext.Expression != "$.sessionId" || ext.OnMiss != "fail" {
		t.Fatalf("start extractors = %#v, want $.sessionId -> %s (fail)", start.Extractors, agentSessionVar)
	}

	// 3. Authorization rewritten to the token variable on both conversation steps.
	wantAuth := "Bearer {{var." + agentTokenVar + "}}"
	for _, action := range []dsl.Action{start, message} {
		if action.Headers["Authorization"] != wantAuth {
			t.Fatalf("%s Authorization = %q, want %q", action.Name, action.Headers["Authorization"], wantAuth)
		}
	}

	// 4. session id reused in the message path; session-start path left intact.
	if strings.Contains(message.Target, "SESSION123") || !strings.Contains(message.Target, "{{var."+agentSessionVar+"}}") {
		t.Fatalf("message target = %q, want session id rewritten to a variable", message.Target)
	}
	if !strings.HasSuffix(start.Target, "/sessions") {
		t.Fatalf("start target = %q, should be unchanged (no session-id segment)", start.Target)
	}

	// 5. SSE configured on the streaming turn.
	if message.Headers["Accept"] != "text/event-stream" {
		t.Fatalf("message Accept = %q, want text/event-stream", message.Headers["Accept"])
	}
	if message.Params["stream"] != "sse" {
		t.Fatalf("message params = %#v, want stream=sse", message.Params)
	}
	if _, ok := findExtractor(message, "agent_text"); !ok {
		t.Fatalf("message extractors = %#v, want an sse_concat extractor", message.Extractors)
	}
	if _, ok := findExtractor(message, "agent_end_of_turn"); !ok {
		t.Fatalf("message extractors = %#v, want an sse_event extractor", message.Extractors)
	}

	// 6. end-session teardown appended, scoped to the session id.
	if len(script.Teardown) != 1 || script.Teardown[0].Method != "DELETE" {
		t.Fatalf("teardown = %#v, want one DELETE end-session action", script.Teardown)
	}
	if !strings.HasSuffix(script.Teardown[0].Target, "/sessions/{{var."+agentSessionVar+"}}") {
		t.Fatalf("teardown target = %q, want it scoped to the session variable", script.Teardown[0].Target)
	}

	// 7. The result must be a valid DSL script.
	if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("transformed script failed validation: %v", issues)
	}
}

func TestTransformIsIdempotent(t *testing.T) {
	script := importedAgentScript()
	if changes := Transform(script); len(changes) == 0 {
		t.Fatal("first Transform should report changes")
	}
	initLen, actionLen, teardownLen := len(script.Init), len(script.Actions), len(script.Teardown)

	again := Transform(script)
	if len(again) != 0 {
		t.Fatalf("second Transform reported %d change(s): %#v, want none", len(again), again)
	}
	if len(script.Init) != initLen || len(script.Actions) != actionLen || len(script.Teardown) != teardownLen {
		t.Fatalf("re-running Transform changed action counts (init=%d actions=%d teardown=%d)",
			len(script.Init), len(script.Actions), len(script.Teardown))
	}
}

func TestTransformNoOpForNonAgentforce(t *testing.T) {
	script := &dsl.Script{
		Protocol: "http",
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.com"},
		Actions: []dsl.Action{
			{Name: "List Orders", Type: "GET", Method: "GET", Target: "https://api.example.com/orders"},
		},
	}
	changes := Transform(script)
	if len(changes) != 0 {
		t.Fatalf("Transform mutated a non-Agentforce script: %#v", changes)
	}
	if len(script.Init) != 0 || len(script.Teardown) != 0 {
		t.Fatalf("Transform added actions to a non-Agentforce script: init=%d teardown=%d", len(script.Init), len(script.Teardown))
	}
}

func TestTransformReusesExistingTokenVariable(t *testing.T) {
	script := importedAgentScript()
	// A token action already exists with a custom save_as; the transform must
	// point Authorization at that variable, not its default, and not add another.
	script.Init = []dsl.Action{{
		Name:   "Existing Token",
		Type:   "http_oauth2_token",
		Params: map[string]any{"save_as": "sf_token"},
	}}
	Transform(script)
	if len(script.Init) != 1 {
		t.Fatalf("init = %#v, want no extra token action", script.Init)
	}
	if got := script.Actions[1].Headers["Authorization"]; got != "Bearer {{var.sf_token}}" {
		t.Fatalf("Authorization = %q, want it to reuse the existing token variable sf_token", got)
	}
}
