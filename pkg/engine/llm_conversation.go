package engine

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	defaultLLMConversationHistoryVar = "llm_conversation_history"
	maxLLMConversationTurnBytes      = 32 * 1024
	maxLLMConversationHistoryBytes   = 64 * 1024
)

// LLMConversationConfig configures multi-turn conversation history management for llm_request.
type LLMConversationConfig struct {
	HistoryVar         string `json:"history_var,omitempty"`
	SystemPrompt       string `json:"system_prompt,omitempty"`
	MaxTurns           int    `json:"max_turns,omitempty"`
	MaxContextTokens   int    `json:"max_context_tokens,omitempty"`
	MaxHistoryBytes    int    `json:"max_history_bytes,omitempty"`
	TruncationStrategy string `json:"truncation_strategy,omitempty"` // "sliding_window" (default) | "drop_oldest"
}

// LLMConversationMessage represents a single role/content message in a conversation history.
type LLMConversationMessage struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	Redacted string `json:"-"`
}

type pendingLLMConversationTurn struct {
	action      *protocol.Action
	cfg         *LLMConversationConfig
	redactedCfg *LLMConversationConfig
	turnMsgs    []LLMConversationMessage
}

func applyLLMConversationParams(cfg *LLMConversationConfig, m map[string]any) {
	if cfg == nil || m == nil {
		return
	}
	if v, ok := m["conversation_var"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.HistoryVar = strings.TrimSpace(v)
	}
	if v, ok := m["history_var"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.HistoryVar = strings.TrimSpace(v)
	}
	if v, ok := m["system"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.SystemPrompt = v
	}
	if v, ok := m["system_prompt"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.SystemPrompt = v
	}
	if v, ok := actionParamInt(m, "max_turns"); ok && v > 0 {
		cfg.MaxTurns = v
	}
	if v, ok := actionParamInt(m, "max_history_turns"); ok && v > 0 {
		cfg.MaxTurns = v
	}
	if v, ok := actionParamInt(m, "max_history_tokens"); ok && v > 0 {
		cfg.MaxContextTokens = v
	}
	if v, ok := actionParamInt(m, "max_context_tokens"); ok && v > 0 {
		cfg.MaxContextTokens = v
	}
	if v, ok := actionParamInt(m, "max_history_bytes"); ok && v > 0 {
		cfg.MaxHistoryBytes = v
	}
	if v, ok := m["truncation_strategy"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.TruncationStrategy = strings.ToLower(strings.TrimSpace(v))
	}
}

func normalizeLLMConversationConfig(cfg *LLMConversationConfig) *LLMConversationConfig {
	if cfg == nil {
		return nil
	}
	if strings.TrimSpace(cfg.HistoryVar) == "" {
		cfg.HistoryVar = defaultLLMConversationHistoryVar
	}
	if strings.TrimSpace(cfg.TruncationStrategy) == "" {
		cfg.TruncationStrategy = "sliding_window"
	}
	if cfg.MaxHistoryBytes <= 0 || cfg.MaxHistoryBytes > maxLLMConversationHistoryBytes {
		cfg.MaxHistoryBytes = maxLLMConversationHistoryBytes
	}
	return cfg
}

func parseLLMConversationConfig(params map[string]any) (*LLMConversationConfig, bool) {
	if params == nil {
		return nil, false
	}
	if raw, ok := params["conversation"]; ok && raw != nil {
		cfg := &LLMConversationConfig{
			HistoryVar:         defaultLLMConversationHistoryVar,
			TruncationStrategy: "sliding_window",
			MaxHistoryBytes:    maxLLMConversationHistoryBytes,
		}
		applyLLMConversationParams(cfg, params)
		switch typed := raw.(type) {
		case map[string]any:
			applyLLMConversationParams(cfg, typed)
			return normalizeLLMConversationConfig(cfg), true
		case LLMConversationConfig:
			if strings.TrimSpace(typed.HistoryVar) != "" {
				cfg.HistoryVar = strings.TrimSpace(typed.HistoryVar)
			}
			if strings.TrimSpace(typed.SystemPrompt) != "" {
				cfg.SystemPrompt = typed.SystemPrompt
			}
			if typed.MaxTurns > 0 {
				cfg.MaxTurns = typed.MaxTurns
			}
			if typed.MaxContextTokens > 0 {
				cfg.MaxContextTokens = typed.MaxContextTokens
			}
			if typed.MaxHistoryBytes > 0 {
				cfg.MaxHistoryBytes = typed.MaxHistoryBytes
			}
			if strings.TrimSpace(typed.TruncationStrategy) != "" {
				cfg.TruncationStrategy = strings.ToLower(strings.TrimSpace(typed.TruncationStrategy))
			}
			return normalizeLLMConversationConfig(cfg), true
		case *LLMConversationConfig:
			if typed == nil {
				return nil, false
			}
			if strings.TrimSpace(typed.HistoryVar) != "" {
				cfg.HistoryVar = strings.TrimSpace(typed.HistoryVar)
			}
			if strings.TrimSpace(typed.SystemPrompt) != "" {
				cfg.SystemPrompt = typed.SystemPrompt
			}
			if typed.MaxTurns > 0 {
				cfg.MaxTurns = typed.MaxTurns
			}
			if typed.MaxContextTokens > 0 {
				cfg.MaxContextTokens = typed.MaxContextTokens
			}
			if typed.MaxHistoryBytes > 0 {
				cfg.MaxHistoryBytes = typed.MaxHistoryBytes
			}
			if strings.TrimSpace(typed.TruncationStrategy) != "" {
				cfg.TruncationStrategy = strings.ToLower(strings.TrimSpace(typed.TruncationStrategy))
			}
			return normalizeLLMConversationConfig(cfg), true
		case bool:
			if typed {
				return normalizeLLMConversationConfig(cfg), true
			}
		}
	}
	for _, key := range []string{"conversation_var", "history_var"} {
		if histVar, ok := params[key].(string); ok && strings.TrimSpace(histVar) != "" {
			cfg := &LLMConversationConfig{
				HistoryVar:         strings.TrimSpace(histVar),
				TruncationStrategy: "sliding_window",
				MaxHistoryBytes:    maxLLMConversationHistoryBytes,
			}
			applyLLMConversationParams(cfg, params)
			return normalizeLLMConversationConfig(cfg), true
		}
	}
	return nil, false
}

// prepareLLMConversationAction prepends prior conversation turns from vu.Variables[history_var]
// onto the outbound llm_request action's messages[] array and enforces turn/token/byte truncation bounds.
func (vu *VUser) prepareLLMConversationAction(action dsl.Action, resolved *protocol.Action) {
	if vu == nil {
		return
	}
	vu.lastLLMConversationAction = nil
	vu.lastLLMConversationTurn = nil
	if resolved == nil {
		return
	}
	cfg, ok := parseLLMConversationConfig(resolved.Params)
	if !ok || cfg == nil {
		return
	}
	if vu.llmConversationVars == nil {
		vu.llmConversationVars = make(map[string]struct{}, 2)
	}
	vu.llmConversationVars[cfg.HistoryVar] = struct{}{}

	redactedCfg, _ := parseLLMConversationConfig(resolved.RedactedParams)
	if redactedCfg == nil {
		redactedCfg = cfg
	}

	history := vu.loadLLMConversationHistory(cfg.HistoryVar)
	currentTurnMsgs := extractCurrentTurnMessagesWithRedaction(resolved)

	combined := buildCombinedConversationMessages(cfg, redactedCfg, history, currentTurnMsgs)
	combined = truncateLLMConversationMessages(cfg, combined)

	if resolved.Params == nil {
		resolved.Params = make(map[string]any, 4)
	}
	msgMaps := conversationMessagesToMaps(combined)
	redactedMsgMaps := redactedConversationMessagesToMaps(combined)
	resolved.Params["messages"] = msgMaps
	if resolved.RedactedParams == nil {
		resolved.RedactedParams = make(map[string]any, len(resolved.Params))
		for k, v := range resolved.Params {
			resolved.RedactedParams[k] = v
		}
	}
	resolved.RedactedParams["messages"] = redactedMsgMaps

	// If an explicit JSON body with a messages[] array was provided, splice the combined messages into it.
	isLLMReq := strings.EqualFold(strings.TrimSpace(resolved.Type), "llm_request")
	hadRedactedBody := len(resolved.RedactedBody) > 0
	rawRedactedBody := resolved.RedactedBody
	if !hadRedactedBody {
		rawRedactedBody = resolved.Body
	}
	if len(resolved.Body) > 0 {
		if updated, ok := spliceConversationMessagesIntoJSONBody(resolved.Body, msgMaps, isLLMReq); ok {
			resolved.Body = updated
		}
	}
	if len(rawRedactedBody) > 0 {
		if updatedRedacted, ok := spliceConversationMessagesIntoJSONBody(rawRedactedBody, redactedMsgMaps, isLLMReq); ok {
			resolved.RedactedBody = updatedRedacted
		}
	}

	vu.lastLLMConversationAction = resolved
	vu.lastLLMConversationTurn = &pendingLLMConversationTurn{
		action:      resolved,
		cfg:         cfg,
		redactedCfg: redactedCfg,
		turnMsgs:    currentTurnMsgs,
	}
}

func conversationMessagesToMaps(msgs []LLMConversationMessage) []map[string]any {
	out := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		out[i] = map[string]any{
			"role":    m.Role,
			"content": m.Content,
		}
	}
	return out
}

func redactedConversationMessagesToMaps(msgs []LLMConversationMessage) []map[string]any {
	out := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		content := m.Redacted
		if content == "" && m.Content != "" {
			content = m.Content
		}
		out[i] = map[string]any{
			"role":    m.Role,
			"content": content,
		}
	}
	return out
}

func spliceConversationMessagesIntoJSONBody(rawBody []byte, msgMaps []map[string]any, isLLMReq bool) ([]byte, bool) {
	var bodyMap map[string]any
	if json.Unmarshal(rawBody, &bodyMap) != nil {
		return nil, false
	}
	if _, hasMsgs := bodyMap["messages"]; !hasMsgs && !isLLMReq {
		return nil, false
	}
	bodyMap["messages"] = msgMaps
	updated, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, false
	}
	return updated, true
}

// appendLLMConversationTurn appends the user turn and assistant response to the conversation
// history variable within the VUser's extraction byte budget.
func (vu *VUser) appendLLMConversationTurn(result *protocol.Result) error {
	if vu == nil || result == nil {
		return nil
	}
	if result.BusinessTransaction || result.SampleKind == "child" || result.DependencyType == "websocket" {
		return nil
	}
	var assistantText string
	if result.ProtocolEvidence != nil {
		if raw, ok := result.ProtocolEvidence["__llm_assistant_text"].(string); ok {
			assistantText = raw
		}
		delete(result.ProtocolEvidence, "__llm_assistant_text")
		if len(result.ProtocolEvidence) == 0 {
			result.ProtocolEvidence = nil
		}
	}
	turn := vu.lastLLMConversationTurn
	resolved := vu.lastLLMConversationAction
	vu.lastLLMConversationTurn = nil
	vu.lastLLMConversationAction = nil
	if resolved == nil && turn != nil {
		resolved = turn.action
	}
	if resolved == nil || !result.Success {
		return nil
	}
	var cfg, redactedCfg *LLMConversationConfig
	var turnMsgs []LLMConversationMessage
	if turn != nil && turn.action == resolved && turn.cfg != nil {
		cfg = turn.cfg
		redactedCfg = turn.redactedCfg
		turnMsgs = turn.turnMsgs
	} else {
		var ok bool
		cfg, ok = parseLLMConversationConfig(resolved.Params)
		if !ok || cfg == nil {
			return nil
		}
		redactedCfg, _ = parseLLMConversationConfig(resolved.RedactedParams)
		turnMsgs = extractCurrentTurnMessagesWithRedaction(resolved)
	}
	if redactedCfg == nil {
		redactedCfg = cfg
	}

	if assistantText == "" && len(result.Extractions) > 0 && resolved.Params != nil {
		for _, k := range []string{"save_text_as", "save_assistant_text", "save_response_as", "save_as", "response_var"} {
			if varName, ok := resolved.Params[k].(string); ok && strings.TrimSpace(varName) != "" {
				if v := result.Extractions[strings.TrimSpace(varName)]; v != "" {
					assistantText = v
					break
				}
			}
		}
	}
	if len(assistantText) > maxLLMConversationTurnBytes {
		assistantText = trimValidUTF8Prefix(assistantText, maxLLMConversationTurnBytes)
	}

	history := vu.loadLLMConversationHistory(cfg.HistoryVar)
	if len(history) == 0 {
		if strings.TrimSpace(cfg.SystemPrompt) != "" {
			redSys := redactedCfg.SystemPrompt
			if strings.TrimSpace(redSys) == "" {
				redSys = cfg.SystemPrompt
			}
			history = append(history, LLMConversationMessage{
				Role:     "system",
				Content:  trimValidUTF8Prefix(cfg.SystemPrompt, maxLLMConversationTurnBytes),
				Redacted: trimValidUTF8Prefix(redSys, maxLLMConversationTurnBytes),
			})
		} else if len(turnMsgs) > 0 && turnMsgs[0].Role == "system" && strings.TrimSpace(turnMsgs[0].Content) != "" {
			redSys := turnMsgs[0].Redacted
			if strings.TrimSpace(redSys) == "" {
				redSys = turnMsgs[0].Content
			}
			history = append(history, LLMConversationMessage{
				Role:     "system",
				Content:  trimValidUTF8Prefix(turnMsgs[0].Content, maxLLMConversationTurnBytes),
				Redacted: trimValidUTF8Prefix(redSys, maxLLMConversationTurnBytes),
			})
		}
	}

	appendedUser := false
	for _, m := range turnMsgs {
		if m.Role == "system" {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		redContent := m.Redacted
		if strings.TrimSpace(redContent) == "" {
			redContent = m.Content
		}
		role := m.Role
		if role == "" {
			role = "user"
		}
		history = append(history, LLMConversationMessage{
			Role:     role,
			Content:  trimValidUTF8Prefix(m.Content, maxLLMConversationTurnBytes),
			Redacted: trimValidUTF8Prefix(redContent, maxLLMConversationTurnBytes),
		})
		appendedUser = true
	}
	if !appendedUser && resolved.Params != nil {
		if p, ok := resolved.Params["prompt"].(string); ok && strings.TrimSpace(p) != "" {
			redP := p
			if resolved.RedactedParams != nil {
				if rp, ok := resolved.RedactedParams["prompt"].(string); ok && strings.TrimSpace(rp) != "" {
					redP = rp
				}
			}
			history = append(history, LLMConversationMessage{
				Role:     "user",
				Content:  trimValidUTF8Prefix(p, maxLLMConversationTurnBytes),
				Redacted: trimValidUTF8Prefix(redP, maxLLMConversationTurnBytes),
			})
		}
	}
	if strings.TrimSpace(assistantText) != "" {
		history = append(history, LLMConversationMessage{
			Role:     "assistant",
			Content:  assistantText,
			Redacted: assistantText,
		})
	}
	history = truncateLLMConversationMessages(cfg, history)
	if vu.llmConversationHistory == nil {
		vu.llmConversationHistory = make(map[string][]LLMConversationMessage, 2)
	}
	histCopy := make([]LLMConversationMessage, len(history))
	copy(histCopy, history)
	vu.llmConversationHistory[cfg.HistoryVar] = histCopy

	encoded, err := json.Marshal(history)
	if err != nil {
		return err
	}
	return vu.mergeExtractions(&protocol.Result{
		Extractions: map[string]string{
			cfg.HistoryVar: string(encoded),
		},
	})
}

// resetLLMConversations clears multi-turn conversation history at the start of each iteration
// so turns do not leak across conversations.
func (vu *VUser) resetLLMConversations() {
	if vu == nil {
		return
	}
	vu.lastLLMConversationAction = nil
	vu.lastLLMConversationTurn = nil
	clear(vu.llmConversationHistory)
	if len(vu.llmConversationVars) == 0 {
		return
	}
	for histVar := range vu.llmConversationVars {
		if vu.extractedVariables != nil {
			if oldSize, tracked := vu.extractedVariables[histVar]; tracked {
				vu.extractedVariableBytes -= oldSize
				if vu.extractedVariableBytes < 0 {
					vu.extractedVariableBytes = 0
				}
				delete(vu.extractedVariables, histVar)
			}
		}
		if vu.Variables != nil {
			delete(vu.Variables, histVar)
		}
	}
}

func (vu *VUser) loadLLMConversationHistory(historyVar string) []LLMConversationMessage {
	if vu == nil || vu.Variables == nil {
		return nil
	}
	raw := strings.TrimSpace(vu.Variables[historyVar])
	if raw == "" {
		return nil
	}
	var msgs []LLMConversationMessage
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		return nil
	}
	cached := vu.llmConversationHistory[historyVar]
	for i := range msgs {
		if i < len(cached) && cached[i].Role == msgs[i].Role && cached[i].Content == msgs[i].Content && cached[i].Redacted != "" {
			msgs[i].Redacted = cached[i].Redacted
		} else {
			msgs[i].Redacted = msgs[i].Content
		}
	}
	return msgs
}

func extractCurrentTurnMessagesWithRedaction(resolved *protocol.Action) []LLMConversationMessage {
	if resolved == nil {
		return nil
	}
	msgs := extractCurrentTurnMessagesFrom(resolved.Params, resolved.Body)
	redactedMsgs := extractCurrentTurnMessagesFrom(resolved.RedactedParams, resolved.RedactedBody)
	for i := range msgs {
		if i < len(redactedMsgs) && redactedMsgs[i].Role == msgs[i].Role && redactedMsgs[i].Content != "" {
			msgs[i].Redacted = redactedMsgs[i].Content
		} else {
			msgs[i].Redacted = msgs[i].Content
		}
	}
	return msgs
}

func extractCurrentTurnMessagesFrom(params map[string]any, body []byte) []LLMConversationMessage {
	if params != nil {
		if rawMsgs, ok := params["messages"]; ok && rawMsgs != nil {
			switch typed := rawMsgs.(type) {
			case []any:
				var out []LLMConversationMessage
				for _, item := range typed {
					if m, ok := item.(map[string]any); ok {
						role, _ := m["role"].(string)
						content, _ := m["content"].(string)
						if role != "" && content != "" {
							out = append(out, LLMConversationMessage{Role: role, Content: content, Redacted: content})
						}
					}
				}
				if len(out) > 0 {
					return out
				}
			case []map[string]any:
				var out []LLMConversationMessage
				for _, m := range typed {
					role, _ := m["role"].(string)
					content, _ := m["content"].(string)
					if role != "" && content != "" {
						out = append(out, LLMConversationMessage{Role: role, Content: content, Redacted: content})
					}
				}
				if len(out) > 0 {
					return out
				}
			case string:
				var out []LLMConversationMessage
				if json.Unmarshal([]byte(typed), &out) == nil && len(out) > 0 {
					for i := range out {
						out[i].Redacted = out[i].Content
					}
					return out
				}
			}
		}
		if prompt, ok := params["prompt"].(string); ok && strings.TrimSpace(prompt) != "" {
			return []LLMConversationMessage{{Role: "user", Content: prompt, Redacted: prompt}}
		}
	}
	if len(body) > 0 {
		var bodyObj struct {
			Messages []LLMConversationMessage `json:"messages"`
			Prompt   string                   `json:"prompt"`
		}
		if json.Unmarshal(body, &bodyObj) == nil {
			if len(bodyObj.Messages) > 0 {
				for i := range bodyObj.Messages {
					bodyObj.Messages[i].Redacted = bodyObj.Messages[i].Content
				}
				return bodyObj.Messages
			}
			if strings.TrimSpace(bodyObj.Prompt) != "" {
				return []LLMConversationMessage{{Role: "user", Content: bodyObj.Prompt, Redacted: bodyObj.Prompt}}
			}
		}
	}
	return nil
}

func buildCombinedConversationMessages(cfg, redactedCfg *LLMConversationConfig, history, current []LLMConversationMessage) []LLMConversationMessage {
	var out []LLMConversationMessage
	sysPrompt := strings.TrimSpace(cfg.SystemPrompt)
	redSysPrompt := sysPrompt
	if redactedCfg != nil && strings.TrimSpace(redactedCfg.SystemPrompt) != "" {
		redSysPrompt = strings.TrimSpace(redactedCfg.SystemPrompt)
	}

	historyRest := history
	hasSystem := false
	if len(history) > 0 && history[0].Role == "system" {
		out = append(out, history[0])
		historyRest = history[1:]
		hasSystem = true
	} else if sysPrompt != "" {
		out = append(out, LLMConversationMessage{Role: "system", Content: sysPrompt, Redacted: redSysPrompt})
		hasSystem = true
	} else if len(current) > 0 && current[0].Role == "system" {
		out = append(out, current[0])
		hasSystem = true
	}

	out = append(out, historyRest...)
	for _, m := range current {
		if m.Role == "system" && hasSystem {
			continue
		}
		out = append(out, m)
	}
	return out
}

func truncateLLMConversationMessages(cfg *LLMConversationConfig, msgs []LLMConversationMessage) []LLMConversationMessage {
	if cfg == nil || len(msgs) == 0 {
		return msgs
	}
	var sysMsg *LLMConversationMessage
	rest := msgs
	if msgs[0].Role == "system" {
		cp := msgs[0]
		if len(cp.Content) > maxLLMConversationTurnBytes {
			cp.Content = trimValidUTF8Prefix(cp.Content, maxLLMConversationTurnBytes)
		}
		if len(cp.Redacted) > maxLLMConversationTurnBytes {
			cp.Redacted = trimValidUTF8Prefix(cp.Redacted, maxLLMConversationTurnBytes)
		}
		sysMsg = &cp
		rest = msgs[1:]
	}

	// 1. Enforce max_turns: each turn is either a user+assistant pair (2 messages)
	// or a trailing user message being prepared for the current request.
	if cfg.MaxTurns > 0 {
		maxNonSystemMsgs := cfg.MaxTurns * 2
		if len(rest) > maxNonSystemMsgs {
			rest = rest[len(rest)-maxNonSystemMsgs:]
		}
	}

	// 2. Enforce max_context_tokens using deterministic ~4 bytes/token estimate.
	if cfg.MaxContextTokens > 0 {
		sysTokens := 0
		if sysMsg != nil {
			sysTokens = protocol.EstimateTokenCount(sysMsg.Content) + 4
		}
		budget := cfg.MaxContextTokens - sysTokens
		if budget < 1 {
			budget = 1
		}
		totalRestTokens := func(slice []LLMConversationMessage) int {
			sum := 0
			for _, m := range slice {
				sum += protocol.EstimateTokenCount(m.Content) + 4
			}
			return sum
		}
		for len(rest) > 1 && totalRestTokens(rest) > budget {
			// Drop oldest message (or oldest user+assistant pair if sliding_window and >2 messages).
			if cfg.TruncationStrategy == "sliding_window" && len(rest) > 2 && rest[0].Role == "user" && rest[1].Role == "assistant" {
				rest = rest[2:]
			} else {
				rest = rest[1:]
			}
		}
	}

	// 3. Enforce max_history_bytes (capped at maxLLMConversationHistoryBytes = 64 KiB)
	// so serialized conversation state never exceeds VUser variable extraction limits.
	maxBytes := cfg.MaxHistoryBytes
	if maxBytes <= 0 || maxBytes > maxLLMConversationHistoryBytes {
		maxBytes = maxLLMConversationHistoryBytes
	}
	assemble := func(slice []LLMConversationMessage) []LLMConversationMessage {
		if sysMsg != nil {
			out := make([]LLMConversationMessage, 0, 1+len(slice))
			out = append(out, *sysMsg)
			out = append(out, slice...)
			return out
		}
		out := make([]LLMConversationMessage, len(slice))
		copy(out, slice)
		return out
	}
	encodedLen := func(slice []LLMConversationMessage) int {
		b, err := json.Marshal(assemble(slice))
		if err != nil {
			return 0
		}
		return len(b)
	}
	for len(rest) > 1 && encodedLen(rest) > maxBytes {
		if cfg.TruncationStrategy == "sliding_window" && len(rest) > 2 && rest[0].Role == "user" && rest[1].Role == "assistant" {
			rest = rest[2:]
		} else {
			rest = rest[1:]
		}
	}

	out := assemble(rest)
	for i := len(out) - 1; i >= 0; i-- {
		wasIdentical := out[i].Redacted == out[i].Content
		for len(out[i].Content) > 0 {
			b, err := json.Marshal(out)
			if err != nil || len(b) <= maxBytes {
				break
			}
			excess := len(b) - maxBytes
			target := len(out[i].Content) - excess
			if target < 0 {
				target = 0
			}
			if target >= len(out[i].Content) {
				target = len(out[i].Content) - 1
			}
			out[i].Content = trimValidUTF8Prefix(out[i].Content, target)
			if wasIdentical {
				out[i].Redacted = out[i].Content
			} else if len(out[i].Redacted) > target {
				out[i].Redacted = trimValidUTF8Prefix(out[i].Redacted, target)
			}
		}
	}
	return out
}

func trimValidUTF8Prefix(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
