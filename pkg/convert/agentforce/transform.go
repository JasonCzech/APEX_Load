package agentforce

import (
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Variable names the transform wires into the converted script. agentTokenVar is
// the OAuth2 access-token VUser variable; agentSessionVar is the Agent API
// session id extracted from the session-start response. The session variable name
// matches the analyzer's review recommendation (see Analyze SessionVars).
const (
	agentTokenVar   = "agent_access_token"
	agentSessionVar = "agent_session_id"
)

// Change records one mutation the transform applied to a script so the import
// review can show an auditable diff of what was rewritten.
type Change struct {
	Step       string `json:"step"`
	ActionName string `json:"action_name,omitempty"`
	Detail     string `json:"detail"`
}

// Transform rewrites an imported Agentforce Agent API script in place so it
// actually replays: it acquires an OAuth2 token, extracts and reuses the session
// id, configures SSE on streaming turns, and ends the session in teardown. It is
// a no-op (returns no changes) when the Agent API lifecycle is not detected, and
// it is idempotent — re-running it makes no further changes. The runtime already
// supports every primitive it emits (http_oauth2_token, sse_concat/sse_event),
// so this only wires what the review previously only recommended.
func Transform(script *dsl.Script) []Change {
	changes := make([]Change, 0)
	if script == nil || !Analyze(script).Detected {
		return changes
	}
	basePath := basePathOf(script.Config.BaseURL)

	var tokenAction, sessionStart, endSession, messageAction *dsl.Action
	conversation := make([]*dsl.Action, 0)
	streams := make([]*dsl.Action, 0)
	for _, action := range allActionPtrs(script) {
		switch classifyAgentforceStep(*action, basePath) {
		case "token":
			if tokenAction == nil {
				tokenAction = action
			}
		case "session_start":
			sessionStart = action
			conversation = append(conversation, action)
		case "send_message":
			if messageAction == nil {
				messageAction = action
			}
			conversation = append(conversation, action)
		case "receive_stream":
			if messageAction == nil {
				messageAction = action
			}
			streams = append(streams, action)
			conversation = append(conversation, action)
		case "end_session":
			endSession = action
			conversation = append(conversation, action)
		}
	}

	tokenVar := resolveTokenVar(tokenAction)

	// 1. Token acquisition. Prepend an http_oauth2_token init action when none
	// exists so a live bearer token is available instead of a stale recorded one.
	if tokenAction == nil {
		script.Init = append([]dsl.Action{newTokenAction(tokenVar)}, script.Init...)
		changes = append(changes, Change{
			Step:       "token",
			ActionName: "Acquire Agent API Token",
			Detail:     "Prepended an http_oauth2_token init action (client_credentials) saving {{var." + tokenVar + "}}.",
		})
	}

	// 2. Authorization. Point every conversation request at the token variable,
	// replacing any captured (and now stale) Authorization header.
	for _, action := range conversation {
		if setBearerAuth(action, tokenVar) {
			changes = append(changes, Change{
				Step:       "auth",
				ActionName: action.Name,
				Detail:     "Set Authorization to Bearer {{var." + tokenVar + "}}.",
			})
		}
	}

	// 3. Session correlation. Extract sessionId on session-start and reuse it in
	// the path of every later message/end-session request.
	if sessionStart != nil && addSessionExtractor(sessionStart) {
		changes = append(changes, Change{
			Step:       "session",
			ActionName: sessionStart.Name,
			Detail:     "Added a $.sessionId extractor saving {{var." + agentSessionVar + "}}.",
		})
	}
	for _, action := range allActionPtrs(script) {
		switch classifyAgentforceStep(*action, basePath) {
		case "send_message", "receive_stream", "end_session":
			if rewriteSessionInTarget(action) {
				changes = append(changes, Change{
					Step:       "session",
					ActionName: action.Name,
					Detail:     "Rewrote the session id in the request path to {{var." + agentSessionVar + "}}.",
				})
			}
		}
	}

	// 4. Streaming. Configure SSE consumption on streaming message turns.
	for _, action := range streams {
		if ensureSSE(action) {
			changes = append(changes, Change{
				Step:       "stream",
				ActionName: action.Name,
				Detail:     "Configured SSE: Accept: text/event-stream, stream=sse, and sse_concat/sse_event extractors.",
			})
		}
	}

	// 5. Teardown. End the session so load tests release agent sessions. The
	// end-session URL is derived from a message request's (now rewritten) path.
	if endSession == nil && messageAction != nil {
		if target, ok := sessionScopedTarget(messageAction.Target); ok {
			script.Teardown = append(script.Teardown, newEndSessionAction(target, tokenVar))
			changes = append(changes, Change{
				Step:       "teardown",
				ActionName: "End Agent Session",
				Detail:     "Appended a DELETE session teardown action targeting " + target + ".",
			})
		}
	}

	return changes
}

// allActionPtrs returns pointers to every action in the script (init, actions,
// teardown) so the transform can mutate them in place.
func allActionPtrs(script *dsl.Script) []*dsl.Action {
	ptrs := make([]*dsl.Action, 0, len(script.Init)+len(script.Actions)+len(script.Teardown))
	for i := range script.Init {
		ptrs = append(ptrs, &script.Init[i])
	}
	for i := range script.Actions {
		ptrs = append(ptrs, &script.Actions[i])
	}
	for i := range script.Teardown {
		ptrs = append(ptrs, &script.Teardown[i])
	}
	return ptrs
}

// resolveTokenVar returns the VUser variable that holds the access token: the
// existing token action's save_as (default access_token) or the transform's
// default when a token action will be added.
func resolveTokenVar(tokenAction *dsl.Action) string {
	if tokenAction == nil {
		return agentTokenVar
	}
	if saveAs, ok := tokenAction.Params["save_as"].(string); ok && strings.TrimSpace(saveAs) != "" {
		return strings.TrimSpace(saveAs)
	}
	return "access_token"
}

func newTokenAction(tokenVar string) dsl.Action {
	return dsl.Action{
		Name: "Acquire Agent API Token",
		Type: "http_oauth2_token",
		Params: map[string]any{
			"token_url":     "{{env.SALESFORCE_TOKEN_URL}}",
			"grant_type":    "client_credentials",
			"client_id":     "{{env.SALESFORCE_CLIENT_ID}}",
			"client_secret": "{{secret.SALESFORCE_CLIENT_SECRET}}",
			"scope":         "{{env.SALESFORCE_SCOPE}}",
			"save_as":       tokenVar,
		},
	}
}

func newEndSessionAction(target, tokenVar string) dsl.Action {
	return dsl.Action{
		Name:   "End Agent Session",
		Type:   "DELETE",
		Method: "DELETE",
		Target: target,
		Headers: map[string]string{
			"Authorization":        "Bearer {{var." + tokenVar + "}}",
			"x-session-end-reason": "UserRequest",
		},
	}
}

// setBearerAuth sets the canonical Authorization header to the token variable,
// removing any case-variant Authorization key. Returns true if it changed.
func setBearerAuth(action *dsl.Action, tokenVar string) bool {
	want := "Bearer {{var." + tokenVar + "}}"
	if action.Headers == nil {
		action.Headers = map[string]string{}
	}
	changed := false
	for key := range action.Headers {
		if key != "Authorization" && strings.EqualFold(key, "Authorization") {
			delete(action.Headers, key)
			changed = true
		}
	}
	if action.Headers["Authorization"] != want {
		action.Headers["Authorization"] = want
		changed = true
	}
	return changed
}

// addSessionExtractor adds the $.sessionId extractor to the session-start action
// unless an equivalent extractor is already present. Returns true if it added it.
func addSessionExtractor(action *dsl.Action) bool {
	for _, ext := range action.Extractors {
		if ext.Name == agentSessionVar || (ext.Type == "jsonpath" && ext.Expression == "$.sessionId") {
			return false
		}
	}
	action.Extractors = append(action.Extractors, dsl.Extractor{
		Name:       agentSessionVar,
		Source:     "body",
		Type:       "jsonpath",
		Expression: "$.sessionId",
		OnMiss:     "fail",
	})
	return true
}

// rewriteSessionInTarget replaces the concrete session-id path segment (the
// segment immediately after "/sessions/") with the session variable. Idempotent.
func rewriteSessionInTarget(action *dsl.Action) bool {
	scoped, ok := sessionScopedTarget(action.Target)
	if !ok {
		return false
	}
	const marker = "/sessions/"
	segStart := strings.Index(action.Target, marker) + len(marker)
	segment := scoped[segStart:]
	if segment == "" || segment == "{{var."+agentSessionVar+"}}" {
		return false
	}
	rewritten := action.Target[:segStart] + "{{var." + agentSessionVar + "}}" + action.Target[len(scoped):]
	if rewritten == action.Target {
		return false
	}
	action.Target = rewritten
	return true
}

// sessionScopedTarget returns target truncated to the end of the session-id
// segment (".../sessions/<id>"), dropping any trailing "/messages" etc.
func sessionScopedTarget(target string) (string, bool) {
	const marker = "/sessions/"
	idx := strings.Index(target, marker)
	if idx < 0 {
		return "", false
	}
	start := idx + len(marker)
	rest := target[start:]
	end := len(rest)
	if i := strings.IndexAny(rest, "/?"); i >= 0 {
		end = i
	}
	if end == 0 {
		return "", false
	}
	return target[:start+end], true
}

// ensureSSE configures a streaming message turn to consume SSE: the Accept
// header, the stream params, and the sse_concat/sse_event extractors. Each piece
// is applied only if missing, so the function is idempotent.
func ensureSSE(action *dsl.Action) bool {
	changed := false
	if action.Headers == nil {
		action.Headers = map[string]string{}
	}
	for key := range action.Headers {
		if key != "Accept" && strings.EqualFold(key, "Accept") {
			delete(action.Headers, key)
			changed = true
		}
	}
	if action.Headers["Accept"] != "text/event-stream" {
		action.Headers["Accept"] = "text/event-stream"
		changed = true
	}
	if action.Params == nil {
		action.Params = map[string]any{}
	}
	for key, value := range map[string]any{
		"stream":            "sse",
		"stop_on_event":     "EndOfTurn",
		"max_events":        200,
		"stream_timeout_ms": 60000,
	} {
		if _, ok := action.Params[key]; !ok {
			action.Params[key] = value
			changed = true
		}
	}
	if !hasSSEExtractor(action) {
		action.Extractors = append(action.Extractors,
			dsl.Extractor{Name: "agent_text", Source: "body", Type: "sse_concat", Expression: "TextChunk", OnMiss: "empty"},
			dsl.Extractor{Name: "agent_end_of_turn", Source: "body", Type: "sse_event", Expression: "EndOfTurn", OnMiss: "empty"},
		)
		changed = true
	}
	return changed
}

func hasSSEExtractor(action *dsl.Action) bool {
	for _, ext := range action.Extractors {
		if ext.Type == "sse_concat" || ext.Type == "sse_event" {
			return true
		}
	}
	return false
}
