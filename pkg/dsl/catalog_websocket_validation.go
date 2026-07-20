package dsl

import (
	"fmt"
	"strings"
)

func validateWebSocketConfigIssues(script *Script) []string {
	if script == nil || strings.TrimSpace(script.Protocol) != "websocket" {
		return nil
	}
	cfg := script.Config.WebSocket
	var issues []string
	if cfg.ReadTimeoutMs < 0 {
		issues = append(issues, "config.websocket.read_timeout_ms must be zero or greater")
	} else if cfg.ReadTimeoutMs > MaxConfiguredProtocolTimeoutMS {
		issues = append(issues, fmt.Sprintf("config.websocket.read_timeout_ms must not exceed %d", MaxConfiguredProtocolTimeoutMS))
	}
	if cfg.WriteTimeoutMs < 0 {
		issues = append(issues, "config.websocket.write_timeout_ms must be zero or greater")
	} else if cfg.WriteTimeoutMs > MaxConfiguredProtocolTimeoutMS {
		issues = append(issues, fmt.Sprintf("config.websocket.write_timeout_ms must not exceed %d", MaxConfiguredProtocolTimeoutMS))
	}
	if cfg.MaxMessageSize < 0 {
		issues = append(issues, "config.websocket.max_message_size must be zero or greater")
	} else if cfg.MaxMessageSize > MaxWebSocketMessageBytes {
		issues = append(issues, fmt.Sprintf("config.websocket.max_message_size must not exceed %d", MaxWebSocketMessageBytes))
	}
	return issues
}
