package engine

// vuser_resolve.go — split from vuser.go (same package, behavior-preserving).

import (
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func actionParamInt(params map[string]any, key string) (int, bool) {
	if params == nil {
		return 0, false
	}
	value, ok := params[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case float32:
		return int(typed), true
	default:
		return 0, false
	}
}

func actionParamString(params map[string]any, key string) (string, bool) {
	if params == nil {
		return "", false
	}
	value, ok := params[key]
	if !ok {
		return "", false
	}
	typed, ok := value.(string)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(typed)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

func actionParamBool(params map[string]any, key string) (bool, bool) {
	if params == nil {
		return false, false
	}
	value, ok := params[key]
	if !ok {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return false, false
	}
}

// loadDataRow populates VUser variables from configured data sources.
func (vu *VUser) loadDataRow() {
	if vu.DataProvider == nil || !vu.DataProvider.HasSources() {
		return
	}
	for _, name := range vu.DataProvider.SourceNames() {
		row := vu.DataProvider.GetRow(name, vu.ID)
		for k, v := range row {
			vu.Variables[k] = v
		}
	}
}

// makeLogger creates a logger for this VUser based on the log config.
func (vu *VUser) makeLogger(cfg dsl.LogConfig) *slog.Logger {
	attrs := []any{"vuser", vu.ID, "group", vu.Group}

	switch cfg.Level {
	case "disabled":
		// Use a no-op handler by setting level extremely high
		return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
			Level: slog.Level(100),
		}))
	default:
		return slog.With(attrs...)
	}
}

func actionLogAttrs(actionName string, resolved *protocol.Action, result *protocol.Result, cfg dsl.LogConfig, includeError bool) []any {
	attrs := []any{
		"action", actionName,
		"status", result.StatusCode,
		"duration_ms", result.Duration.Milliseconds(),
	}
	if !includeError {
		attrs = append(attrs, "bytes", result.BytesReceived)
	} else {
		attrs = append(attrs, "error", result.ErrorMessage)
	}

	options := make(map[string]struct{}, len(cfg.ExtendedOptions))
	for _, option := range cfg.ExtendedOptions {
		options[strings.TrimSpace(option)] = struct{}{}
	}

	if _, ok := options["parameter_substitution"]; ok {
		target := resolved.Target
		if resolved.RedactedTarget != "" {
			target = resolved.RedactedTarget
		}
		requestBody := string(resolved.Body)
		if len(resolved.RedactedBody) > 0 {
			requestBody = string(resolved.RedactedBody)
		}
		attrs = append(attrs,
			"target", target,
			"request_body", requestBody,
		)
	}
	if _, ok := options["data_returned"]; ok {
		attrs = append(attrs, "response_body", result.ResponseBody)
	}
	if _, ok := options["advanced_trace"]; ok {
		attrs = append(attrs,
			"dependency_target", result.DependencyTarget,
			"correlation_id", result.CorrelationID,
			"dns_ms", result.DNSTime.Milliseconds(),
			"connect_ms", result.ConnectTime.Milliseconds(),
			"tls_ms", result.TLSTime.Milliseconds(),
			"ttfb_ms", result.TTFB.Milliseconds(),
		)
	}

	return attrs
}

func (vu *VUser) resolveAction(action dsl.Action) *protocol.Action {
	target, redactedTarget := vu.interpolateWithSecrets(action.Target)
	selector, _ := vu.interpolateWithSecrets(action.Selector)
	value, _ := vu.interpolateWithSecrets(action.Value)
	headers, redactedHeaders := vu.interpolateMapWithSecrets(action.Headers)
	body, redactedBody := vu.interpolateWithSecrets(action.Body)
	soapAction, redactedSOAPAction := toProtocolSOAPActions(action.SOAP, vu)
	return &protocol.Action{
		Name:            action.Name,
		Type:            action.Type,
		Method:          action.Method,
		Target:          target,
		RedactedTarget:  redactedTarget,
		Selector:        selector,
		Value:           value,
		Headers:         headers,
		RedactedHeaders: redactedHeaders,
		Body:            []byte(body),
		BodyEncoding:    action.BodyEncoding,
		RedactedBody:    []byte(redactedBody),
		Params:          vu.interpolateAnyMap(action.Params),
		WaitFor:         toProtocolWaitCondition(action.WaitFor, vu),
		Sync:            toProtocolBrowserSyncPolicy(action.Sync, vu),
		Capture:         toProtocolCaptureOptions(action.Capture),
		Metadata:        vu.interpolateAnyMap(action.Metadata),
		Extractors:      toProtocolExtractors(action.Extractors),
		Assertions:      toProtocolAssertions(action.Assertions),
		SOAP:            soapAction,
		RedactedSOAP:    redactedSOAPAction,
	}
}

func toProtocolSOAPAction(action *dsl.SOAPActionConfig, vu *VUser) *protocol.SOAPActionConfig {
	resolved, _ := toProtocolSOAPActions(action, vu)
	return resolved
}

func toProtocolSOAPActions(action *dsl.SOAPActionConfig, vu *VUser) (*protocol.SOAPActionConfig, *protocol.SOAPActionConfig) {
	if action == nil {
		return nil, nil
	}
	resolvedAction, redactedAction := vu.interpolateWithSecrets(action.Action)
	resolvedOperation, redactedOperation := vu.interpolateWithSecrets(action.Operation)
	resolvedBodyMode, redactedBodyMode := vu.interpolateWithSecrets(action.BodyMode)
	return &protocol.SOAPActionConfig{
			Action:    resolvedAction,
			Operation: resolvedOperation,
			BodyMode:  resolvedBodyMode,
		}, &protocol.SOAPActionConfig{
			Action:    redactedAction,
			Operation: redactedOperation,
			BodyMode:  redactedBodyMode,
		}
}

func (vu *VUser) interpolate(s string) string {
	if s == "" {
		return s
	}
	resolved, _ := vu.interpolateWithSecrets(s)
	return resolved
}

func (vu *VUser) interpolateWithSecrets(s string) (string, string) {
	if s == "" {
		return s, s
	}
	hasSecret := false
	resolved := templatePattern.ReplaceAllStringFunc(s, func(token string) string {
		expr := strings.TrimSpace(token[2 : len(token)-2])
		if expr == "" {
			return token
		}
		if value, ok := vu.resolveTemplateValue(expr); ok {
			return value
		}
		return token
	})
	redacted := templatePattern.ReplaceAllStringFunc(s, func(token string) string {
		expr := strings.TrimSpace(token[2 : len(token)-2])
		if expr == "" {
			return token
		}
		if strings.HasPrefix(expr, "secret.") {
			hasSecret = true
			return "[REDACTED]"
		}
		if value, ok := vu.resolveTemplateValue(expr); ok {
			return value
		}
		return token
	})
	if !hasSecret {
		return resolved, resolved
	}
	return resolved, redacted
}

func (vu *VUser) interpolateMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	resolved, _ := vu.interpolateMapWithSecrets(m)
	return resolved
}

func (vu *VUser) interpolateMapWithSecrets(m map[string]string) (map[string]string, map[string]string) {
	if m == nil {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	redacted := make(map[string]string, len(m))
	for k, v := range m {
		resolvedValue, redactedValue := vu.interpolateWithSecrets(v)
		out[k] = resolvedValue
		redacted[k] = redactedValue
	}
	return out, redacted
}

func (vu *VUser) interpolateAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = vu.interpolateAny(v)
	}
	return out
}

func (vu *VUser) interpolateAny(v any) any {
	switch typed := v.(type) {
	case string:
		return vu.interpolate(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = vu.interpolateAny(typed[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, nested := range typed {
			out[k] = vu.interpolateAny(nested)
		}
		return out
	default:
		return v
	}
}

func (vu *VUser) resolveTemplateValue(expr string) (string, bool) {
	switch {
	case strings.HasPrefix(expr, "var."):
		key := strings.TrimSpace(strings.TrimPrefix(expr, "var."))
		value, ok := vu.Variables[key]
		if !ok && vu.Parameters != nil {
			return vu.Parameters.ResolveNamed(key)
		}
		return value, ok
	case strings.HasPrefix(expr, "param."):
		if vu.Parameters == nil {
			return "", false
		}
		key := strings.TrimSpace(strings.TrimPrefix(expr, "param."))
		return vu.Parameters.ResolveNamed(key)
	case strings.HasPrefix(expr, "data."):
		// Data variables are stored as "source.column", so map data.source.column -> source.column.
		key := strings.TrimSpace(strings.TrimPrefix(expr, "data."))
		value, ok := vu.Variables[key]
		return value, ok
	case strings.HasPrefix(expr, "env."):
		key := strings.TrimSpace(strings.TrimPrefix(expr, "env."))
		value, ok := lookupScriptEnv(key)
		return value, ok
	case strings.HasPrefix(expr, "secret."):
		key := strings.TrimSpace(strings.TrimPrefix(expr, "secret."))
		return resolveSecretTemplateValue(key, vu.secretResolver())
	case strings.HasPrefix(expr, "fn."), strings.HasPrefix(expr, "fake."):
		return resolveBuiltinFunction(expr, time.Now)
	default:
		return "", false
	}
}

func (vu *VUser) secretResolver() SecretResolver {
	if vu.SecretResolver == nil {
		vu.SecretResolver = newDefaultSecretResolver()
	}
	return vu.SecretResolver
}

func (vu *VUser) mergeExtractions(r *protocol.Result) {
	for k, v := range r.Extractions {
		vu.Variables[k] = v
	}
}

func replaceAll(s, old, new string) string {
	for {
		i := indexOf(s, old)
		if i < 0 {
			return s
		}
		s = s[:i] + new + s[i+len(old):]
	}
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func toProtocolExtractors(exts []dsl.Extractor) []protocol.Extractor {
	out := make([]protocol.Extractor, len(exts))
	for i, e := range exts {
		out[i] = protocol.Extractor{
			Name:       e.Name,
			Source:     e.Source,
			Type:       e.Type,
			Expression: e.Expression,
			OnMiss:     e.OnMiss,
			Mode:       e.Mode,
			SaveOffset: e.SaveOffset,
			SaveLength: e.SaveLength,
			Convert:    e.Convert,
		}
	}
	return out
}

func toProtocolAssertions(asserts []dsl.Assertion) []protocol.Assertion {
	out := make([]protocol.Assertion, len(asserts))
	for i, a := range asserts {
		out[i] = protocol.Assertion{
			Type:     a.Type,
			Expected: a.Expected,
			Value:    a.Value,
			Max:      a.Max,
		}
	}
	return out
}

func toProtocolWaitCondition(wait *dsl.WaitCondition, vu *VUser) *protocol.WaitCondition {
	if wait == nil {
		return nil
	}
	return &protocol.WaitCondition{
		Type:      wait.Type,
		Value:     vu.interpolate(wait.Value),
		TimeoutMs: wait.TimeoutMs,
	}
}

func toProtocolBrowserSyncPolicy(sync *dsl.BrowserSyncPolicy, vu *VUser) *protocol.BrowserSyncPolicy {
	if sync == nil {
		return nil
	}
	return &protocol.BrowserSyncPolicy{
		Type:      sync.Type,
		Value:     vu.interpolate(sync.Value),
		TimeoutMs: sync.TimeoutMs,
		StableMs:  sync.StableMs,
	}
}

func toProtocolCaptureOptions(capture *dsl.BrowserCaptureOptions) *protocol.BrowserCaptureOptions {
	if capture == nil {
		return nil
	}
	return &protocol.BrowserCaptureOptions{
		Screenshot: capture.Screenshot,
		FullPage:   capture.FullPage,
		HTML:       capture.HTML,
	}
}
