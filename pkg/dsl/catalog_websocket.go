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

// websocketActionSpecs declares the websocket protocol action types.
func websocketActionSpecs() []ActionSpec {
	return []ActionSpec{
		{
			Type:      "ws_connect",
			Protocols: []string{"websocket"},
			Summary:   "Opens the WebSocket connection.",
			Fields: []FieldSpec{
				{Name: "target", Required: true, Kind: FieldKindString, Doc: "WebSocket URL (ws:// or wss://); relative paths resolve against config.base_url."},
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
			Protocols: []string{"websocket"},
			Summary:   "Sends a message over the open connection.",
			Fields: []FieldSpec{
				{Name: "body", Kind: FieldKindString, Doc: "Message payload; template variables are expanded."},
				{Name: "body_file", Kind: FieldKindString, Doc: "Local file containing the message payload. Resolved by ParseFile relative to the script directory, confined to that directory, then stored as inline body. Not accepted by API uploads; use body there."},
				{Name: "params.type", Kind: FieldKindEnum, Enum: []string{"text", "binary"}, Doc: "Message frame type; defaults to text."},
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
			Protocols: []string{"websocket"},
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
			Protocols: []string{"websocket"},
			Summary:   "Closes the WebSocket connection.",
		},
		{
			Type:      "ws_ping",
			Protocols: []string{"websocket"},
			Summary:   "Sends a ping control frame.",
		},
	}
}
