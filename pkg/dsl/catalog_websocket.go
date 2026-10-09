package dsl

import (
	"fmt"
	"strings"
)

func validateWebSocketActionIssues(index int, action Action) []string {
	spec, ok := protocolActionSpec("websocket", action.Type)
	if !ok {
		return []string{fmt.Sprintf("actions[%d] %q: unsupported websocket action type %q", index, action.Name, action.Type)}
	}
	if spec.Validate == nil {
		return nil
	}
	return spec.Validate(index, action)
}

// websocketActionSpecs declares the websocket protocol action types. HTTP
// scripts accept them too (LoadRunner web_websocket_*), running on a socket
// that shares the HTTP session.
func websocketActionSpecs() []ActionSpec {
	return []ActionSpec{
		{Type: "ws_on_message", Protocols: []string{"websocket", "http"}, Summary: "Registers serial per-VUser message callbacks for the current connection; reconnect requires registration again.", Fields: []FieldSpec{{Name: "control", Kind: FieldKindObject, Required: true, Doc: "control.steps executes for each queued message; var.ws_message and var.ws_message_type contain the event."}}},
		{Type: "ws_dispatch", Protocols: []string{"websocket", "http"}, Summary: "Waits until the next callback interval and dispatches queued messages serially."},
		{
			Type:      "ws_connect",
			Protocols: []string{"websocket", "http"},
			Summary:   "Opens the WebSocket connection.",
			Fields: []FieldSpec{
				{Name: "target", Required: true, Kind: FieldKindString, Doc: "WebSocket URL (ws:// or wss://); relative paths resolve against config.base_url. In an HTTP script the handshake shares the script's cookie jar and TLS settings, so a socket opened after an HTTP login carries the session cookie."},
				{Name: "headers", Kind: FieldKindObject, Doc: "Handshake request headers."},
			},
			Validate: func(index int, action Action) []string {
				if strings.TrimSpace(action.Target) == "" {
					return []string{fmt.Sprintf("actions[%d] %q: missing target", index, action.Name)}
				}
				return nil
			},
		},
		{
			Type:      "ws_send",
			Protocols: []string{"websocket", "http"},
			Summary:   "Sends a message over the open connection.",
			Fields: []FieldSpec{
				{Name: "body", Kind: FieldKindString, Doc: "Message payload; template variables are expanded."},
				{Name: "body_file", Kind: FieldKindString, Doc: "Local file containing the message payload. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there."},
				{Name: "params.type", Kind: FieldKindEnum, Enum: []string{"text", "binary"}, SchemaEnum: true, EnumTrimSpace: true, EnumAllowBlank: true, Doc: "Message frame type; defaults to text."},
			},
			RequireOneOf: [][]string{{"body", "body_file"}},
			Validate: func(index int, action Action) []string {
				label := fmt.Sprintf("actions[%d] %q", index, action.Name)
				var issues []string
				if action.Body == "" {
					issues = append(issues, fmt.Sprintf("%s: ws_send requires body", label))
				}
				if messageType, ok := actionParamStringValue(action.Params, "type"); ok && !isAllowedValue(messageType, "text", "binary") {
					issues = append(issues, fmt.Sprintf("%s: ws_send type must be text or binary", label))
				}
				return issues
			},
		},
		{
			Type:      "ws_receive",
			Protocols: []string{"websocket", "http"},
			Summary:   "Waits for the next inbound message.",
			Fields: []FieldSpec{
				{Name: "params.timeout_ms", Kind: FieldKindInt, Maximum: MaxConfiguredProtocolTimeoutMS, Doc: "Receive timeout in milliseconds; zero through 3600000."},
			},
			Validate: func(index int, action Action) []string {
				if timeoutMs, ok := actionParamIntValue(action.Params, "timeout_ms"); ok {
					if timeoutMs < 0 {
						return []string{fmt.Sprintf("actions[%d] %q: ws_receive timeout_ms must be zero or greater", index, action.Name)}
					}
					if timeoutMs > MaxConfiguredProtocolTimeoutMS {
						return []string{fmt.Sprintf("actions[%d] %q: ws_receive timeout_ms must not exceed %d", index, action.Name, MaxConfiguredProtocolTimeoutMS)}
					}
				}
				return nil
			},
		},
		{
			Type:      "ws_close",
			Protocols: []string{"websocket", "http"},
			Summary:   "Closes the WebSocket connection.",
		},
		{
			Type:      "ws_ping",
			Protocols: []string{"websocket", "http"},
			Summary:   "Sends a ping control frame.",
		},
	}
}

func isWebSocketStepType(actionType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(actionType)), "ws_")
}

// validateHTTPScriptWebSocketStep validates a ws_* step of an HTTP script as
// a websocket script would, with ws_on_message callback steps validated
// against the HTTP protocol they run in.
func validateHTTPScriptWebSocketStep(path string, index int, action Action) []string {
	var issues []string
	if strings.TrimSpace(action.Name) == "" {
		issues = append(issues, fmt.Sprintf("%s: missing name", path))
	}
	issues = append(issues, validateExtractorIssues("websocket", path, action)...)
	if action.Type == "ws_on_message" {
		return append(issues, validateControlFlowIssues("http", path, action)...)
	}
	return append(issues, rebaseActionIssues(fmt.Sprintf("actions[%d]", index), path, validateWebSocketActionIssues(index, action))...)
}
