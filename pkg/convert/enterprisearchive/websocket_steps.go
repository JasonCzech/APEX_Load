package enterprisearchive

import (
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/bodyencoding"
)

// LoadRunner web_websocket_connect, web_websocket_send and
// web_websocket_close become ws_connect, ws_send and ws_close steps in the
// HTTP script, whose socket shares the HTTP session. The connection's
// OnMessageCB becomes a ws_on_message step after the connect.
const (
	// webSocketCallbacksReasonCode: the socket runs, but a callback does not.
	webSocketCallbacksReasonCode = "websocket_callbacks_not_executed"
	// webSocketAttributeReasonCode: a connect attribute APEX does not send.
	webSocketAttributeReasonCode = "websocket_attribute_unsupported"
	// webSocketUnsupportedReasonCode: a WebSocket call the importer cannot
	// run; it is preserved and not executed.
	webSocketUnsupportedReasonCode = "websocket_step_unsupported"
)

var webSocketCallbackAttributes = []string{"OnOpenCB", "OnMessageCB", "OnErrorCB", "OnCloseCB"}

// sourceWebSockets tracks the import's socket: APEX runs one per virtual
// user.
type sourceWebSockets struct {
	id   string
	open bool
}

// webSocketReviewAction preserves a WebSocket call that cannot run as an
// execution-gated review action.
func webSocketReviewAction(fn ParsedFunction, detail string) dsl.Action {
	return sourceReviewActionWithCode(fn, webSocketUnsupportedReasonCode, detail)
}

// convertWebSocketConnect maps web_websocket_connect to ws_connect, followed
// by ws_on_message when its OnMessageCB translates. It returns a reason when
// the connection cannot run.
func convertWebSocketConnect(fn ParsedFunction, state *sourceWebSockets, files map[string]string) ([]dsl.Action, []string, string) {
	id := asyncCallArg(fn, "ID")
	uri := asyncCallArg(fn, "URI")
	switch {
	case uri == "":
		return nil, nil, "it has no URI"
	case state.open && id != state.id:
		return nil, nil, fmt.Sprintf("connection %q opens while connection %q is open, and APEX runs one WebSocket per virtual user", id, state.id)
	}
	connect := dsl.Action{Name: webSocketStepName("connect", id), Type: "ws_connect", Target: convertParamReferences(uri)}
	if origin := asyncCallArg(fn, "Origin"); origin != "" {
		connect.Headers = map[string]string{"Origin": convertParamReferences(origin)}
	}
	var findings []importreview.Finding
	var unknown []string
	known := map[string]bool{"id": true, "uri": true, "origin": true}
	for _, name := range webSocketCallbackAttributes {
		known[strings.ToLower(name)] = true
	}
	for _, arg := range fn.CallArgs {
		if arg.Kind == "key_value" && !known[strings.ToLower(arg.Name)] {
			unknown = append(unknown, arg.Name)
		}
	}
	if len(unknown) > 0 {
		findings = append(findings, promotionFinding(fn, webSocketAttributeReasonCode, fmt.Sprintf("web_websocket_connect attributes %s are not sent by APEX.", strings.Join(unknown, ", "))))
	}
	idLiteral := strconv.Quote(id)
	var onMessage []dsl.Action
	var untranslated []string
	var warnings []string
	for _, attribute := range webSocketCallbackAttributes {
		name := asyncCallArg(fn, attribute)
		if name == "" {
			continue
		}
		arguments := map[int]string{0: idLiteral}
		branch := ""
		if attribute == "OnMessageCB" {
			arguments[2] = `lr_eval_string("{ws_message}")`
			branch = "ws_on_message"
		}
		translation := translateSourceCallback(files, attribute, name, branch, arguments)
		switch {
		case translation.reason != "":
			untranslated = append(untranslated, fmt.Sprintf("%s=%s (%s)", attribute, name, translation.reason))
		case attribute == "OnMessageCB":
			onMessage = translation.steps
		}
		warnings = append(warnings, droppedCallbackLogsWarnings(fn, []sourceCallbackTranslation{translation})...)
	}
	if len(untranslated) > 0 {
		finding := promotionFinding(fn, webSocketCallbacksReasonCode, fmt.Sprintf(
			"WebSocket %q runs, but these callbacks do not: %s. Rebuild what they do as ws_on_message steps.", id, strings.Join(untranslated, "; ")))
		finding.Resolution = "rebuild_websocket_callbacks"
		findings = append(findings, finding)
	}
	if len(findings) > 0 {
		importreview.ApplyToAction(&connect, findings)
	}
	actions := []dsl.Action{connect}
	if len(onMessage) > 0 {
		actions = append(actions, dsl.Action{Name: webSocketStepName("messages", id), Type: "ws_on_message", Control: &dsl.ControlFlow{Steps: onMessage}})
	}
	state.id, state.open = id, true
	return actions, warnings, ""
}

// convertWebSocketSend maps web_websocket_send to ws_send. Buffer is text
// with parameters; IsBinary=1 sends its bytes as a binary frame, and
// Buffer/Binary names bytes with C escapes.
func convertWebSocketSend(fn ParsedFunction, state *sourceWebSockets) (dsl.Action, string) {
	id := asyncCallArg(fn, "ID")
	if !state.open || id != state.id {
		return dsl.Action{}, fmt.Sprintf("no converted WebSocket %q is open", id)
	}
	action := dsl.Action{Name: webSocketStepName("send", id), Type: "ws_send"}
	switch buffer, escaped := asyncCallArgRaw(fn, "Buffer"), asyncCallArgRaw(fn, "Buffer/Binary"); {
	case escaped != "":
		templated := convertParamReferences(escaped)
		decoded, err := bodyencoding.DecodeCEscapes(templated)
		if err != nil {
			return dsl.Action{}, "its Buffer/Binary escapes do not decode"
		}
		if reason := setWebSocketBinaryBody(&action, decoded, templated != escaped); reason != "" {
			return dsl.Action{}, reason
		}
	case buffer != "" && asyncCallArg(fn, "IsBinary") == "1":
		templated := convertParamReferences(buffer)
		if reason := setWebSocketBinaryBody(&action, []byte(templated), templated != buffer); reason != "" {
			return dsl.Action{}, reason
		}
	case buffer != "":
		action.Body = convertParamReferences(buffer)
	default:
		return dsl.Action{}, "it has no Buffer"
	}
	return action, ""
}

// setWebSocketBinaryBody sends data as a binary frame: fixed bytes as
// base64, or, when it has parameters, text whose templates expand at replay.
func setWebSocketBinaryBody(action *dsl.Action, data []byte, parameters bool) string {
	action.Params = map[string]any{"type": "binary"}
	if !parameters {
		action.Body, action.BodyEncoding = base64.StdEncoding.EncodeToString(data), "base64"
		return ""
	}
	if !utf8.Valid(data) {
		return "its binary buffer mixes parameters with bytes that are not text"
	}
	action.Body = string(data)
	return ""
}

// convertWebSocketClose maps web_websocket_close to ws_close with its Code
// and Reason.
func convertWebSocketClose(fn ParsedFunction, state *sourceWebSockets) (dsl.Action, string) {
	id := asyncCallArg(fn, "ID")
	if !state.open || id != state.id {
		return dsl.Action{}, fmt.Sprintf("no converted WebSocket %q is open", id)
	}
	action := dsl.Action{Name: webSocketStepName("close", id), Type: "ws_close"}
	params := map[string]any{}
	if raw := asyncCallArg(fn, "Code"); raw != "" {
		code, err := strconv.Atoi(raw)
		if err != nil {
			return dsl.Action{}, fmt.Sprintf("Code=%s is not a number", raw)
		}
		params["code"] = code
	}
	if reason := asyncCallArg(fn, "Reason"); reason != "" {
		params["reason"] = convertParamReferences(reason)
	}
	if len(params) > 0 {
		action.Params = params
	}
	state.open = false
	return action, ""
}

// asyncCallArgRaw returns a key=value argument without trimming it.
func asyncCallArgRaw(fn ParsedFunction, key string) string {
	for _, arg := range fn.CallArgs {
		if arg.Kind == "key_value" && strings.EqualFold(arg.Name, key) {
			return arg.Value
		}
	}
	return ""
}

func webSocketStepName(step, id string) string {
	if id == "" {
		return "WebSocket " + step
	}
	return "WebSocket " + id + " " + step
}

// webSocketBaseURL is the base URL of a script whose only URLs are
// WebSocket URIs.
func webSocketBaseURL(functions []ParsedFunction) string {
	for _, fn := range functions {
		if fn.Name != "web_websocket_connect" {
			continue
		}
		uri := asyncCallArg(fn, "URI")
		scheme, rest, ok := strings.Cut(uri, "://")
		if !ok {
			continue
		}
		switch strings.ToLower(scheme) {
		case "ws":
			scheme = "http"
		case "wss":
			scheme = "https"
		default:
			continue
		}
		host, _, _ := strings.Cut(rest, "/")
		if host != "" {
			return scheme + "://" + host
		}
	}
	return ""
}
