// Package agentforce provides a review-layer analyzer that detects the
// Salesforce Agentforce Agent API conversation lifecycle inside an imported DSL
// script (token acquisition -> session start -> send message -> receive
// response -> end session). It surfaces session-id correlation, streaming
// (SSE) handling, and response/guardrail assertion recommendations. It operates
// on the converted *dsl.Script so HAR, Postman, and OpenAPI imports share it.
package agentforce

import (
	"net/url"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// Review summarizes Agentforce Agent API findings for an imported script.
type Review struct {
	Detected    bool            `json:"detected"`
	Streaming   bool            `json:"streaming"`
	Lifecycle   []LifecycleStep `json:"lifecycle,omitempty"`
	SessionVars []SessionVar    `json:"session_vars,omitempty"`
	Assertions  []AssertionHint `json:"assertion_hints,omitempty"`
	Blockers    []string        `json:"blockers,omitempty"`
	NextActions []string        `json:"next_actions,omitempty"`
}

// LifecycleStep records one detected Agent API step.
type LifecycleStep struct {
	Step       string `json:"step"`
	ActionName string `json:"action_name,omitempty"`
	Method     string `json:"method,omitempty"`
	Path       string `json:"path,omitempty"`
}

// SessionVar is a recommended correlation extractor for the conversation.
type SessionVar struct {
	Name       string `json:"name"`
	Source     string `json:"source"`
	Type       string `json:"type"`
	Expression string `json:"expression"`
	Detail     string `json:"detail,omitempty"`
}

// AssertionHint recommends a response/guardrail assertion for a lifecycle step.
type AssertionHint struct {
	Step   string `json:"step"`
	Detail string `json:"detail"`
}

// Analyze inspects a converted script for the Agentforce Agent API lifecycle.
func Analyze(script *dsl.Script) *Review {
	review := &Review{}
	if script == nil {
		return review
	}

	basePath := basePathOf(script.Config.BaseURL)
	steps := map[string]bool{}
	streamingActionMissingSSE := false
	for _, action := range allActions(script) {
		step := classifyAgentforceStep(action, basePath)
		if step == "" {
			continue
		}
		steps[step] = true
		hasSSE := actionHasSSE(action)
		if hasSSE {
			review.Streaming = true
		}
		// SSE handling is judged per streaming action: a streaming endpoint that
		// does not itself consume SSE must be flagged even if an unrelated sibling
		// action carries an SSE extractor.
		if step == "receive_stream" {
			review.Streaming = true
			if !hasSSE {
				streamingActionMissingSSE = true
			}
		}
		review.Lifecycle = append(review.Lifecycle, LifecycleStep{
			Step:       step,
			ActionName: action.Name,
			Method:     strings.ToUpper(convertutil.FirstNonEmpty(action.Method, action.Type)),
			Path:       effectivePath(action.Target, basePath),
		})
	}

	sort.SliceStable(review.Lifecycle, func(i, j int) bool {
		return lifecycleRank(review.Lifecycle[i].Step) < lifecycleRank(review.Lifecycle[j].Step)
	})

	conversationSteps := steps["session_start"] || steps["send_message"] || steps["receive_stream"] || steps["end_session"]
	review.Detected = conversationSteps

	if !review.Detected {
		return review
	}

	if steps["session_start"] {
		review.SessionVars = append(review.SessionVars, SessionVar{
			Name:       "agent_session_id",
			Source:     "body",
			Type:       "jsonpath",
			Expression: "$.sessionId",
			Detail:     "Extract sessionId from the session-start response and reuse as {{var.agent_session_id}} in message and end-session requests.",
		})
	}
	if steps["send_message"] || steps["receive_stream"] {
		review.Assertions = append(review.Assertions, AssertionHint{
			Step:   "send_message",
			Detail: "Assert the response carries a non-empty agent message and does not contain a guardrail/refusal marker (e.g. \"I can't help with that\").",
		})
		review.NextActions = append(review.NextActions,
			"Give each agent message a unique, increasing sequenceId within the session (the id is per-session, so a fresh session each iteration restarts it); parameterize it (e.g. {{fn.timestamp.unix()}} or a counter source) so replayed turns are not rejected as stale.",
			"Reuse the session id from the session-start response in the message and end-session request paths ({{var.agent_session_id}}), not a recorded literal.",
		)
	}

	if !steps["token"] {
		review.Blockers = append(review.Blockers, "Agent API requires an OAuth2 access token; add an http_oauth2_token init action (client_credentials or jwt_bearer) before the conversation.")
	}
	if streamingActionMissingSSE {
		review.Blockers = append(review.Blockers, "A streaming Agent message endpoint was detected; consume it with stream=sse and sse_concat/sse_event extractors to capture the response and measure time-to-first-token.")
	}
	if !steps["end_session"] {
		review.NextActions = append(review.NextActions, "Add an end-session (DELETE .../sessions/{id}) teardown step so load tests release agent sessions.")
	}
	review.NextActions = append(review.NextActions,
		"Parameterize agent utterances from a data source to exercise diverse conversation paths.",
		"Replay against a non-production Agentforce org and review guardrail/refusal rates before promotion.",
	)
	return review
}

func classifyAgentforceStep(action dsl.Action, basePath string) string {
	if strings.EqualFold(action.Type, "http_oauth2_token") {
		return "token"
	}
	path := strings.ToLower(effectivePath(action.Target, basePath))
	method := strings.ToUpper(convertutil.FirstNonEmpty(action.Method, action.Type))
	if strings.Contains(path, "/services/oauth2/token") {
		return "token"
	}
	if !strings.Contains(path, "ai-agent") && !strings.Contains(path, "/einstein/") {
		return ""
	}
	switch {
	case strings.Contains(path, "/messages/stream"):
		return "receive_stream"
	case strings.Contains(path, "/messages"):
		return "send_message"
	case method == "DELETE" && strings.Contains(path, "/sessions"):
		return "end_session"
	case strings.Contains(path, "/sessions"):
		return "session_start"
	default:
		return "agent_other"
	}
}

func actionHasSSE(action dsl.Action) bool {
	for _, extractor := range action.Extractors {
		if extractor.Type == "sse_concat" || extractor.Type == "sse_event" {
			return true
		}
	}
	if action.Params != nil {
		switch value := action.Params["stream"].(type) {
		case string:
			return strings.EqualFold(value, "sse") || strings.EqualFold(value, "true")
		case bool:
			return value
		}
	}
	return false
}

func lifecycleRank(step string) int {
	switch step {
	case "token":
		return 0
	case "session_start":
		return 1
	case "send_message":
		return 2
	case "receive_stream":
		return 3
	case "end_session":
		return 5
	default:
		return 4
	}
}

func allActions(script *dsl.Script) []dsl.Action {
	actions := make([]dsl.Action, 0, len(script.Init)+len(script.Actions)+len(script.Teardown))
	actions = append(actions, script.Init...)
	actions = append(actions, script.Actions...)
	actions = append(actions, script.Teardown...)
	return actions
}

func pathOf(target string) string {
	target = strings.TrimSpace(target)
	if target == "" {
		return ""
	}
	if strings.Contains(target, "://") {
		if parsed, err := url.Parse(target); err == nil {
			return parsed.EscapedPath()
		}
	}
	if idx := strings.IndexByte(target, '?'); idx >= 0 {
		return target[:idx]
	}
	return target
}

// basePathOf extracts the path portion of the script base URL so relative action
// targets (common in OpenAPI/Postman imports) can be resolved against the agent
// API prefix that lives in the base URL.
func basePathOf(baseURL string) string {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return ""
	}
	if strings.Contains(baseURL, "://") {
		if parsed, err := url.Parse(baseURL); err == nil {
			return parsed.EscapedPath()
		}
		return ""
	}
	return pathOf(baseURL)
}

// effectivePath returns the full request path: an absolute target uses its own
// path; a relative target is joined onto the base URL path.
func effectivePath(target, basePath string) string {
	if strings.Contains(strings.TrimSpace(target), "://") {
		return pathOf(target)
	}
	targetPath := pathOf(target)
	if basePath == "" {
		return targetPath
	}
	return strings.TrimRight(basePath, "/") + "/" + strings.TrimLeft(targetPath, "/")
}
