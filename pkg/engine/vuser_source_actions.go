package engine

// vuser_source_actions.go — split from vuser.go (same package, behavior-preserving).

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	protocolhttp "github.com/jasonczech/apexload/pkg/protocol/http"
)

func (vu *VUser) handleEngineAction(ctx context.Context, action dsl.Action) (bool, error) {
	switch dsl.CanonicalActionType(action.Type) {
	case "rendezvous":
		return true, vu.waitAtRendezvous(ctx, action)
	case "error_message":
		vu.logErrorMessageAction(ctx, action)
		return true, nil
	case "source_message":
		vu.handleSourceMessageAction(ctx, action)
		return true, nil
	case "source_runtime":
		return true, vu.handleSourceRuntimeAction(ctx, action)
	case "cookie_clear":
		return true, vu.clearProtocolCookies(ctx)
	case "http_oauth2_token":
		return true, vu.handleHTTPOAuth2TokenAction(ctx, action)
	case "source_transaction":
		vu.handleSourceTransactionAction(action)
		return true, nil
	case "source_timer":
		vu.handleSourceTimerAction(action)
		return true, nil
	case "source_metric":
		vu.handleSourceMetricAction(ctx, action)
		return true, nil
	case "source_parameter", "source_json", "source_xml", "source_database", "source_secret":
		vu.handleSourceStateAction(ctx, action)
		return true, nil
	case "source_external", "source_review", "custom_call":
		vu.logSourceReviewAction(ctx, action)
		return true, nil
	case "control_break", "control_continue":
		// Loop control transfers are intercepted by the runner switch (load) and
		// expandReplayAction (replay) to produce a flow signal; reaching here
		// means the action is being executed as a plain leaf (replay event
		// emission or a stray break/continue outside a loop) — treat it as a
		// successful no-op so it surfaces in the timeline without erroring.
		return true, nil
	default:
		return false, nil
	}
}

// clearProtocolCookies dispatches a cookie_clear action to the active protocol
// when it exposes the protocol.CookieController capability (HTTP does). It is a
// safe no-op when the VUser has no protocol bound yet (e.g. the catalog
// dispatch cross-check) or the protocol keeps no cookie store.
func (vu *VUser) clearProtocolCookies(ctx context.Context) error {
	if vu == nil || vu.Proto == nil {
		return nil
	}
	controller, ok := vu.Proto.(protocol.CookieController)
	if !ok {
		return nil
	}
	return controller.ClearCookies(ctx)
}

// browserEmulationNewUserEachIteration reports whether browser emulation's
// "simulate a new user on each iteration" is enabled — the flag that clears
// both the simulated browser cache and the cookie jar at iteration start.
func browserEmulationNewUserEachIteration(cfg dsl.ScriptConfig) bool {
	emu := cfg.BrowserEmulation
	return emu != nil && emu.Cache != nil && emu.Cache.NewUserEachIteration != nil && *emu.Cache.NewUserEachIteration
}

// clearProtocolCache dispatches to the protocol.CacheController capability
// (HTTP's simulated browser cache). Safe no-op when the protocol keeps no
// response cache.
func (vu *VUser) clearProtocolCache(ctx context.Context) error {
	if vu == nil || vu.Proto == nil {
		return nil
	}
	controller, ok := vu.Proto.(protocol.CacheController)
	if !ok {
		return nil
	}
	return controller.ClearCache(ctx)
}

func (vu *VUser) handleHTTPOAuth2TokenAction(ctx context.Context, action dsl.Action) error {
	params := vu.interpolateAnyMap(action.Params)
	saveAs, _ := actionParamString(params, "save_as")
	if saveAs == "" {
		saveAs = "access_token"
	}
	paramString := func(key string) string {
		value, _ := actionParamString(params, key)
		return value
	}
	tokenReq := protocolhttp.OAuthTokenRequest{
		TokenURL:     paramString("token_url"),
		GrantType:    paramString("grant_type"),
		ClientID:     paramString("client_id"),
		ClientSecret: paramString("client_secret"),
		Scope:        paramString("scope"),
		Audience:     paramString("audience"),
		Issuer:       paramString("issuer"),
		Subject:      paramString("subject"),
		PrivateKey:   paramString("private_key"),
		RefreshToken: paramString("refresh_token"),
		Username:     paramString("username"),
		Password:     paramString("password"),
	}
	var allowedTargetHosts []string
	if vu.Script != nil && vu.Script.Metadata != nil && vu.Script.Metadata.RuntimePolicy != nil {
		allowedTargetHosts = vu.Script.Metadata.RuntimePolicy.AllowedTargetHosts
	}
	if err := protocol.ValidateTargetURL(tokenReq.TokenURL, allowedTargetHosts); err != nil {
		return fmt.Errorf("http_oauth2_token %q: %w", action.Name, err)
	}
	if httpProto, ok := vu.Proto.(*protocolhttp.HTTP); ok {
		tokenReq.HTTPClient = httpProto.OAuthHTTPClient()
	} else if len(allowedTargetHosts) > 0 {
		// Other protocol wrappers do not expose an HTTP client whose proxy and
		// redirect legs carry the CI policy. Fail closed rather than falling back
		// to the process-global client and an ungoverned environment proxy.
		return fmt.Errorf("http_oauth2_token %q cannot enforce the CI runtime target policy for protocol %T", action.Name, vu.Proto)
	}
	token, err := protocolhttp.FetchOAuth2Token(ctx, tokenReq)
	if err != nil {
		return fmt.Errorf("http_oauth2_token %q: %w", action.Name, err)
	}
	redactionConfig, err := WithSecretValueRedactionChecked(vu.RedactionConfig, []string{token.AccessToken})
	if err != nil {
		return fmt.Errorf("http_oauth2_token %q: prepare token redaction: %s", action.Name, scrubSensitiveText(err.Error()))
	}
	// Install the exact matcher before the token becomes visible to templates,
	// logs, protocol actions, or retained evidence.
	vu.RedactionConfig = redactionConfig
	vu.runtimeRedaction.Store(redactionConfig)
	vu.ensureVariables()
	writeVariableAlias := func(key, value string) {
		vu.Variables[key] = value
		vu.Variables[strings.ToLower(key)] = value
	}
	writeVariableAlias(saveAs, token.AccessToken)
	writeVariableAlias(saveAs+"_token_type", token.TokenType)
	writeVariableAlias(saveAs+"_instance_url", token.InstanceURL)
	if !token.ExpiresAt.IsZero() {
		writeVariableAlias(saveAs+"_expires_at", strconv.FormatInt(token.ExpiresAt.Unix(), 10))
	}
	return nil
}

func (vu *VUser) handleSourceMessageAction(ctx context.Context, action dsl.Action) {
	message := vu.renderErrorMessage(action)
	level, _ := actionParamString(action.Params, "level")
	attrs := []any{
		"vuser", vu.ID,
		"group", vu.Group,
		"action", action.Name,
		"function", sourceActionFunction(action),
		"message", message,
	}
	if status, _ := actionParamBool(action.Params, "status_message"); status {
		vu.ensureVariables()
		vu.Variables["vuser_status"] = message
	}
	switch level {
	case "debug":
		slog.Default().DebugContext(ctx, "source message", attrs...)
	case "error":
		slog.Default().ErrorContext(ctx, "source message", attrs...)
	default:
		slog.Default().InfoContext(ctx, "source message", attrs...)
	}
}

func (vu *VUser) handleSourceRuntimeAction(ctx context.Context, action dsl.Action) error {
	function := sourceActionFunction(action)
	vu.ensureVariables()
	switch function {
	case "lr_abort":
		return fmt.Errorf("source runtime abort requested by %s", action.Name)
	case "lr_exit":
		vu.Variables["__source_exit_requested"] = strings.Join(sourceActionArgs(action), ",")
	case "lr_disable_ip_spoofing":
		// Import-compatibility marker only — see lr_enable_ip_spoofing below.
		vu.Variables["__lr_ip_spoofing"] = "disabled"
	case "lr_enable_ip_spoofing":
		// Import-compatibility markers only. APEX Load binds source IPs at VUser init
		// from config.web_runtime.source_ips (round-robin per VUser); there is no
		// runtime toggle, so these record intent but do not change binding behavior.
		// The __lr_ip_spoofing variable is intentionally not read anywhere.
		vu.Variables["__lr_ip_spoofing"] = "enabled"
	case "lr_load_dll":
		vu.logSourceReviewAction(ctx, action)
	default:
		slog.Default().DebugContext(ctx, "source runtime action",
			"vuser", vu.ID,
			"group", vu.Group,
			"action", action.Name,
			"function", function,
		)
	}
	return nil
}

func (vu *VUser) handleSourceTransactionAction(action dsl.Action) {
	function := sourceActionFunction(action)
	args := sourceActionArgs(action)
	name := firstEngineValue(action.Name, args...)
	vu.ensureVariables()
	switch {
	case strings.Contains(function, "start"):
		vu.Variables[sourceTransactionStartKey(name)] = strconv.FormatInt(time.Now().UnixNano(), 10)
	case strings.Contains(function, "end"), strings.Contains(function, "stop"), function == "lr_fail_trans_with_error":
		success := !strings.Contains(function, "fail")
		if len(args) > 1 {
			status := strings.ToLower(args[len(args)-1])
			if strings.Contains(status, "fail") {
				success = false
			}
		}
		start := time.Now()
		if raw := vu.Variables[sourceTransactionStartKey(name)]; raw != "" {
			if nanos, err := strconv.ParseInt(raw, 10, 64); err == nil {
				start = time.Unix(0, nanos)
			}
		}
		vu.recordTransaction(name, time.Since(start), success, nil)
		vu.Variables[sourceTransactionStatusKey(name)] = strconv.FormatBool(success)
	case strings.Contains(function, "status"):
		if len(args) > 1 {
			vu.Variables[sourceTransactionStatusKey(args[0])] = args[1]
		}
	}
}

func (vu *VUser) handleSourceTimerAction(action dsl.Action) {
	function := sourceActionFunction(action)
	args := sourceActionArgs(action)
	name := firstEngineValue(action.Name, args...)
	vu.ensureVariables()
	switch function {
	case "lr_start_timer":
		vu.Variables[sourceTimerStartKey(name)] = strconv.FormatInt(time.Now().UnixNano(), 10)
	case "lr_end_timer":
		if raw := vu.Variables[sourceTimerStartKey(name)]; raw != "" {
			if nanos, err := strconv.ParseInt(raw, 10, 64); err == nil {
				durationMs := time.Since(time.Unix(0, nanos)).Milliseconds()
				vu.Variables[sourceTimerDurationKey(name)] = strconv.FormatInt(durationMs, 10)
			}
		}
	}
}

func (vu *VUser) handleSourceMetricAction(ctx context.Context, action dsl.Action) {
	args := sourceActionArgs(action)
	name := firstEngineValue(action.Name, args...)
	value := ""
	if len(args) > 1 {
		value = args[1]
	}
	slog.Default().InfoContext(ctx, "source metric",
		"vuser", vu.ID,
		"group", vu.Group,
		"action", action.Name,
		"function", sourceActionFunction(action),
		"name", name,
		"value", value,
	)
}

func (vu *VUser) handleSourceStateAction(ctx context.Context, action dsl.Action) {
	function := sourceActionFunction(action)
	switch action.Type {
	case "source_parameter":
		vu.handleSourceParameterAction(function, action)
	case "source_json", "source_xml":
		vu.handleSourceDocumentAction(function, action)
	case "source_secret":
		vu.handleSourceSecretAction(function, action)
	default:
		vu.logSourceReviewAction(ctx, action)
	}
}

func (vu *VUser) handleSourceParameterAction(function string, action dsl.Action) {
	args := sourceActionArgs(action)
	vu.ensureVariables()
	switch function {
	case "lr_save_string", "lr_save_int":
		if len(args) >= 2 {
			vu.setSourceVariable(args[1], args[0])
		}
	case "lr_save_var":
		if len(args) >= 4 {
			vu.setSourceVariable(args[3], args[0])
		}
	case "lr_param_sprintf":
		if len(args) >= 2 {
			values := make([]any, 0, len(args)-2)
			for _, arg := range args[2:] {
				values = append(values, arg)
			}
			vu.setSourceVariable(args[0], renderPrintfLikeMessage(args[1], values))
		}
	case "lr_param_increment":
		if len(args) >= 1 {
			delta := 1
			if len(args) > 1 {
				if parsed, err := strconv.Atoi(strings.TrimSpace(args[1])); err == nil {
					delta = parsed
				}
			}
			current, _ := strconv.Atoi(strings.TrimSpace(vu.Variables[strings.ToLower(args[0])]))
			vu.setSourceVariable(args[0], strconv.Itoa(current+delta))
		}
	case "lr_free_parameter":
		if len(args) >= 1 {
			delete(vu.Variables, args[0])
			delete(vu.Variables, strings.ToLower(args[0]))
		}
	case "lr_save_timestamp":
		name := firstEngineValue("timestamp", args...)
		vu.setSourceVariable(name, strconv.FormatInt(time.Now().Unix(), 10))
	case "lr_save_datetime":
		name := firstEngineValue("datetime", args...)
		format := defaultDateTimeLayout
		if len(args) > 1 {
			format = sourceDateTimeLayout(args[0])
			name = args[len(args)-1]
		}
		vu.setSourceVariable(name, time.Now().Format(format))
	case "lr_whoami":
		vu.setSourceVariable("vuser_id", strconv.Itoa(vu.ID))
		vu.setSourceVariable("vuser_group", vu.Group)
	case "lr_get_host_name", "lr_get_master_host_name":
		if host, err := os.Hostname(); err == nil {
			vu.setSourceVariable(firstEngineValue("host_name", args...), host)
		}
	case "lr_get_vuser_ip":
		vu.setSourceVariable(firstEngineValue("vuser_ip", args...), "127.0.0.1")
	case "web_convert_param":
		vu.handleWebConvertParamAction(action, args)
	default:
		if saveName := saveParamNameFromAction(action); saveName != "" {
			vu.setSourceVariable(saveName, firstEngineValue("", args...))
		}
	}
}

func (vu *VUser) handleWebConvertParamAction(action dsl.Action, args []string) {
	saveName := ""
	if value, ok := actionParamString(action.Params, "save_param"); ok {
		saveName = value
	} else if len(args) > 0 {
		saveName = args[0]
	}
	if strings.TrimSpace(saveName) == "" {
		return
	}
	source := ""
	if value, ok := actionParamString(action.Params, "source_string"); ok {
		source = value
	} else {
		for _, arg := range args[1:] {
			key, value, ok := strings.Cut(arg, "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "SourceString") {
				source = value
				break
			}
		}
	}
	sourceEncoding := sourceActionParamString(action, "source_encoding", args)
	targetEncoding := sourceActionParamString(action, "target_encoding", args)
	value := vu.interpolate(source)
	switch strings.ToLower(strings.TrimSpace(sourceEncoding)) {
	case "html":
		value = html.UnescapeString(value)
	}
	switch strings.ToLower(strings.TrimSpace(targetEncoding)) {
	case "url":
		value = url.QueryEscape(value)
	case "html":
		value = html.EscapeString(value)
	}
	vu.setSourceVariable(saveName, value)
}

func sourceActionParamString(action dsl.Action, name string, args []string) string {
	if value, ok := actionParamString(action.Params, name); ok {
		return value
	}
	sourceKey := strings.ReplaceAll(name, "_", "")
	for _, arg := range args {
		key, value, ok := strings.Cut(arg, "=")
		if ok && strings.EqualFold(strings.ReplaceAll(strings.TrimSpace(key), "_", ""), sourceKey) {
			return value
		}
	}
	return ""
}

func (vu *VUser) handleSourceDocumentAction(function string, action dsl.Action) {
	args := sourceActionArgs(action)
	vu.ensureVariables()
	saveName := saveParamNameFromAction(action)
	if saveName == "" && len(args) > 0 {
		saveName = args[len(args)-1]
	}
	if saveName == "" {
		return
	}
	switch {
	case strings.Contains(function, "find"):
		vu.setSourceVariable(saveName, "0")
	case strings.Contains(function, "get_values"), strings.Contains(function, "extract"), strings.Contains(function, "stringify"), strings.Contains(function, "eval"):
		vu.setSourceVariable(saveName, firstEngineValue("", args...))
	}
}

func (vu *VUser) handleSourceSecretAction(function string, action dsl.Action) {
	args := sourceActionArgs(action)
	saveName := saveParamNameFromAction(action)
	if saveName == "" && len(args) > 0 {
		saveName = args[len(args)-1]
	}
	if saveName == "" {
		saveName = strings.TrimPrefix(function, "lr_")
	}
	vu.setSourceVariable(saveName, "{{secret."+strings.ToLower(saveName)+"}}")
}

func (vu *VUser) logSourceReviewAction(ctx context.Context, action dsl.Action) {
	slog.Default().InfoContext(ctx, "source review action",
		"vuser", vu.ID,
		"group", vu.Group,
		"action", action.Name,
		"function", sourceActionFunction(action),
		"support_mode", sourceActionSupportMode(action),
	)
}

func (vu *VUser) ensureVariables() {
	if vu.Variables == nil {
		vu.Variables = make(map[string]string)
	}
}

func (vu *VUser) setSourceVariable(name string, value string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	vu.ensureVariables()
	resolved := vu.interpolate(value)
	vu.Variables[name] = resolved
	vu.Variables[strings.ToLower(name)] = resolved
}

func sourceActionFunction(action dsl.Action) string {
	if value, ok := actionParamString(action.Params, "function"); ok {
		return value
	}
	if value, ok := action.Metadata["source_function"].(string); ok {
		return value
	}
	return action.Type
}

func sourceActionSupportMode(action dsl.Action) string {
	if value, ok := actionParamString(action.Params, "support_mode"); ok {
		return value
	}
	if value, ok := action.Metadata["support_mode"].(string); ok {
		return value
	}
	return ""
}

func sourceActionArgs(action dsl.Action) []string {
	if action.Params == nil {
		return nil
	}
	raw, ok := action.Params["args"]
	if !ok {
		return nil
	}
	values := anySlice(raw)
	args := make([]string, 0, len(values))
	for _, value := range values {
		args = append(args, strings.TrimSpace(fmt.Sprint(value)))
	}
	return args
}

func anySlice(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []string:
		out := make([]any, len(typed))
		for i, value := range typed {
			out[i] = value
		}
		return out
	case nil:
		return nil
	default:
		return []any{typed}
	}
}

func firstEngineValue(fallback string, values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return fallback
}

func sourceTransactionStartKey(name string) string {
	return "__lr_transaction." + strings.ToLower(strings.TrimSpace(name)) + ".start"
}

func sourceTransactionStatusKey(name string) string {
	return "__lr_transaction." + strings.ToLower(strings.TrimSpace(name)) + ".status"
}

func sourceTimerStartKey(name string) string {
	return "__lr_timer." + strings.ToLower(strings.TrimSpace(name)) + ".start"
}

func sourceTimerDurationKey(name string) string {
	return "__lr_timer." + strings.ToLower(strings.TrimSpace(name)) + ".duration_ms"
}

func saveParamNameFromAction(action dsl.Action) string {
	for _, key := range []string{"save_param", "saveparam", "save_param_name", "param", "parameter", "name"} {
		if value, ok := actionParamString(action.Params, key); ok {
			return value
		}
	}
	for _, entry := range structuredCallArgs(action) {
		name, _ := entry["name"].(string)
		value, _ := entry["value"].(string)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "saveparam", "save_param", "param", "parameter", "name":
			return value
		}
	}
	return ""
}

func structuredCallArgs(action dsl.Action) []map[string]any {
	if action.Params == nil {
		return nil
	}
	raw, ok := action.Params["call_args"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if entry, ok := item.(map[string]any); ok {
				out = append(out, entry)
			}
		}
		return out
	default:
		return nil
	}
}

func sourceDateTimeLayout(format string) string {
	switch strings.TrimSpace(format) {
	case "", "%Y-%m-%d %H:%M:%S":
		return defaultDateTimeLayout
	case "%H:%M:%S":
		return defaultTimeLayout
	default:
		layout := strings.TrimSpace(format)
		replacements := []struct {
			old string
			new string
		}{
			{"%Y", "2006"},
			{"%m", "01"},
			{"%d", "02"},
			{"%H", "15"},
			{"%M", "04"},
			{"%S", "05"},
		}
		for _, replacement := range replacements {
			layout = strings.ReplaceAll(layout, replacement.old, replacement.new)
		}
		return layout
	}
}
