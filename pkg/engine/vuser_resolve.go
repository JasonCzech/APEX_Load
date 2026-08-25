package engine

// vuser_resolve.go — split from vuser.go (same package, behavior-preserving).

import (
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
)

func validateMessageDetailRuntimeSupport(script *dsl.Script) error {
	if script == nil || script.Config.Results.MessageDetail == nil {
		return nil
	}
	mode := strings.ToLower(strings.TrimSpace(script.Config.Results.MessageDetail.Mode))
	switch mode {
	case "", "off", "errors":
		return nil
	case "sampled", "adaptive":
		if strings.ToLower(strings.TrimSpace(script.Protocol)) != "http" {
			return fmt.Errorf("message detail mode %q currently requires the http protocol", mode)
		}
		return nil
	default:
		return fmt.Errorf("unsupported message detail mode %q", mode)
	}
}

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
	if vu.Variables == nil {
		vu.Variables = make(map[string]string)
	}
	for _, name := range vu.DataProvider.SourceNames() {
		row := vu.DataProvider.GetRow(name, vu.ID)
		for k, v := range row {
			vu.Variables[k] = v
		}
	}
	_ = vu.refreshImportedCredentialRuntimeValues()
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

func actionLogAttrs(actionName string, resolved *protocol.Action, result *protocol.Result, cfg dsl.LogConfig, includeError bool, redactionConfig *RedactionConfig) []any {
	if result == nil {
		result = &protocol.Result{}
	}
	attrs := []any{
		"action", cloneBoundedResultString(scrubSensitiveTextWithConfig(actionName, redactionConfig), maxResultMetadataBytes),
		"status", result.StatusCode,
		"duration_ms", result.Duration.Milliseconds(),
	}
	if !includeError {
		attrs = append(attrs, "bytes", result.BytesReceived)
	} else {
		attrs = append(attrs, "error",
			cloneBoundedResultString(scrubSensitiveTextWithConfig(result.ErrorMessage, redactionConfig), maxResultErrorBytes))
	}

	options := make(map[string]struct{}, len(cfg.ExtendedOptions))
	for _, option := range cfg.ExtendedOptions {
		options[strings.TrimSpace(option)] = struct{}{}
	}

	if _, ok := options["parameter_substitution"]; ok && resolved != nil {
		target := resolved.Target
		if resolved.RedactedTarget != "" {
			target = resolved.RedactedTarget
		}
		requestBody := string(resolved.Body)
		if len(resolved.RedactedBody) > 0 {
			requestBody = string(resolved.RedactedBody)
		}
		attrs = append(attrs,
			"target", scrubSensitiveTextWithConfig(target, redactionConfig),
			"request_body", scrubSensitiveTextWithConfig(requestBody, redactionConfig),
		)
	}
	if _, ok := options["data_returned"]; ok {
		attrs = append(attrs, "response_body",
			cloneBoundedResultString(scrubSensitiveTextWithConfig(result.ResponseBody, redactionConfig), maxRetainedResultEvidenceBytes))
	}
	if _, ok := options["advanced_trace"]; ok {
		attrs = append(attrs,
			"dependency_target", cloneBoundedResultString(scrubSensitiveTextWithConfig(result.DependencyTarget, redactionConfig), maxResultMetadataBytes),
			"correlation_id", cloneBoundedResultString(scrubSensitiveTextWithConfig(result.CorrelationID, redactionConfig), maxResultMetadataBytes),
			"dns_ms", result.DNSTime.Milliseconds(),
			"connect_ms", result.ConnectTime.Milliseconds(),
			"tls_ms", result.TLSTime.Milliseconds(),
			"ttfb_ms", result.TTFB.Milliseconds(),
		)
	}

	return attrs
}

func (vu *VUser) bindRuntimeTextRedactor() {
	if vu == nil || vu.Proto == nil {
		return
	}
	vu.runtimeRedaction.Store(vu.RedactionConfig)
	bindProtocolRuntimeRedaction(vu.Proto, func() *RedactionConfig { return vu.runtimeRedaction.Load() })
}

// bindProtocolRuntimeRedaction installs the evidence gate before protocol Init
// as well as after VUser construction. Browser Init can otherwise create a raw
// Playwright HAR working file before the VUser has a chance to suppress it.
func bindProtocolRuntimeRedaction(proto protocol.Protocol, provider func() *RedactionConfig) {
	if proto == nil || provider == nil {
		return
	}
	if setter, ok := proto.(protocol.RuntimeTextRedactorSetter); ok {
		setter.SetRuntimeTextRedactor(func(value string) string {
			return scrubSensitiveTextWithConfig(value, currentRedactionConfig(provider))
		})
	}
	if setter, ok := proto.(protocol.RuntimeSecretActivitySetter); ok {
		setter.SetRuntimeSecretActivity(func() bool {
			return currentRedactionConfig(provider).HasRuntimeSecretMaterial()
		})
	}
}

func (vu *VUser) resolveAction(action dsl.Action) *protocol.Action {
	vu.ensureImportedCredentialInspection()
	vu.unresolvedSecretRefs = nil
	vu.unresolvedFileRefs = nil
	vu.unresolvedParameterRefs = nil
	target, redactedTarget := vu.interpolateWithSecrets(action.Target)
	selector, redactedSelector := vu.interpolateWithSecrets(action.Selector)
	value, redactedValue := vu.interpolateWithSecrets(action.Value)
	headers, redactedHeaders := vu.interpolateMapWithSecrets(action.Headers)
	headerList, redactedHeaderList := vu.interpolateHeaderListWithSecrets(action.HeaderList)
	var defaultHeaders map[string]string
	var defaultHeaderList protocol.HeaderList
	resolvedDefaultHeaders := scriptUsesHTTPDefaultHeaders(vu.Script)
	if resolvedDefaultHeaders {
		defaultHeaders, _ = vu.interpolateMapWithSecrets(vu.Script.Config.HTTP.DefaultHeaders)
		defaultHeaderList, _ = vu.interpolateHeaderListWithSecrets(vu.Script.Config.HTTP.DefaultHeaderList)
	}
	body, redactedBody := vu.interpolateActionBody(
		action.Body,
		action.BodyEncoding,
		actionBodyTemplateValueEncoding(action.Metadata),
	)
	auth, redactedAuth := toProtocolAuthConfig(action.Auth, vu)
	soapAction, redactedSOAPAction := toProtocolSOAPActions(action.SOAP, vu)
	params, redactedParams := vu.interpolateAnyMapWithSecrets(action.Params)
	if dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(action.Type))) == "java_vuser_run" && !vu.javaVuserSkipsParameterEvaluation() {
		params, redactedParams = vu.withJavaVuserRuntimeParameters(params, redactedParams)
	}
	discardSuccessfulResponseBody := false
	captureMessageDetail := false
	var detailPolicy *dsl.MessageDetailConfig
	if vu.Script != nil {
		discardSuccessfulResponseBody = !logConfigRetainsSuccessfulResponseBody(vu.Script.Config.Log)
		detailPolicy = vu.Script.Config.Results.MessageDetail
		captureMessageDetail = vu.shouldCaptureSuccessfulMessageDetail(action.Name, detailPolicy)
		if captureMessageDetail {
			discardSuccessfulResponseBody = false
		}
	}
	resolved := &protocol.Action{
		Name:                         action.Name,
		Type:                         action.Type,
		Method:                       action.Method,
		Target:                       target,
		RedactedTarget:               redactedTarget,
		Selector:                     selector,
		RedactedSelector:             redactedSelector,
		Value:                        value,
		RedactedValue:                redactedValue,
		Headers:                      headers,
		HeaderList:                   headerList,
		RedactedHeaders:              redactedHeaders,
		RedactedHeaderList:           redactedHeaderList,
		ResolvedDefaultHeaders:       resolvedDefaultHeaders,
		DefaultHeaders:               defaultHeaders,
		DefaultHeaderList:            defaultHeaderList,
		Body:                         []byte(body),
		BodyEncoding:                 action.BodyEncoding,
		RedactedBody:                 []byte(redactedBody),
		Params:                       params,
		RedactedParams:               redactedParams,
		WaitFor:                      toProtocolWaitCondition(action.WaitFor, vu),
		Sync:                         toProtocolBrowserSyncPolicy(action.Sync, vu),
		Capture:                      toProtocolCaptureOptions(action.Capture),
		Metadata:                     vu.interpolateAnyMap(action.Metadata),
		HierarchyPath:                resolvedAttributionHierarchy(action.Attribution, vu),
		PageName:                     resolvedAttributionPage(action.Attribution, vu),
		Extractors:                   toProtocolExtractors(action.Extractors, vu),
		Assertions:                   toProtocolAssertions(action.Assertions, vu),
		Auth:                         auth,
		RedactedAuth:                 redactedAuth,
		TimeoutMs:                    action.TimeoutMs,
		MaxRedirects:                 cloneIntPtr(action.MaxRedirects),
		SOAP:                         soapAction,
		RedactedSOAP:                 redactedSOAPAction,
		DiscardResponseBodyOnSuccess: discardSuccessfulResponseBody,
		CaptureMessageDetail:         captureMessageDetail,
	}
	if detailPolicy != nil {
		resolved.MessageDetailMode = strings.ToLower(strings.TrimSpace(detailPolicy.Mode))
		resolved.MessageDetailMaxItems = normalizedMessageDetailMaxItems(detailPolicy.MaxItems)
		resolved.MessageDetailMaxItemBytes = normalizedMessageDetailMaxItemBytes(detailPolicy.MaxItemBytes)
		resolved.MessageDetailMaxTotalBytes = normalizedMessageDetailMaxTotalBytes(detailPolicy.MaxTotalBytes)
	}
	// The authored action is the only place the origin of these credentials can
	// be read; by the time the execution path sees the resolved action,
	// {{secret.NAME}} has already been replaced by its value. Both per-action
	// surfaces — the auth block and the target's URL userinfo — are enumerated
	// by actionCredentialCompositeSurfaces.
	vu.actionCredentialComposites = resolvedActionCredentialComposites{
		action: resolved,
		values: runtimeActionCompositeValues(&action, resolved),
	}
	return resolved
}

func resolvedAttributionHierarchy(attribution *dsl.AnalyticsAttribution, vu *VUser) string {
	if attribution == nil {
		return ""
	}
	return strings.TrimSpace(vu.interpolate(attribution.HierarchyPath))
}

func resolvedAttributionPage(attribution *dsl.AnalyticsAttribution, vu *VUser) string {
	if attribution == nil {
		return ""
	}
	return strings.TrimSpace(vu.interpolate(attribution.PageName))
}

const (
	defaultMessageDetailMaxItems      = 128
	defaultMessageDetailMaxItemBytes  = int64(1 << 20)
	defaultMessageDetailMaxTotalBytes = int64(16 << 20)
)

func normalizedMessageDetailMaxItems(value int) int {
	if value <= 0 {
		return defaultMessageDetailMaxItems
	}
	return min(value, 4096)
}

func normalizedMessageDetailMaxItemBytes(value int64) int64 {
	if value <= 0 {
		return defaultMessageDetailMaxItemBytes
	}
	return min(value, int64(16<<20))
}

func normalizedMessageDetailMaxTotalBytes(value int64) int64 {
	if value <= 0 {
		return defaultMessageDetailMaxTotalBytes
	}
	return min(value, int64(1<<30))
}

func (vu *VUser) shouldCaptureSuccessfulMessageDetail(actionName string, config *dsl.MessageDetailConfig) bool {
	if vu == nil || config == nil {
		return false
	}
	mode := strings.ToLower(strings.TrimSpace(config.Mode))
	if mode != "sampled" && mode != "adaptive" {
		return false
	}
	rate := config.SampleRate
	if mode == "adaptive" && rate <= 0 {
		rate = 0.01
	}
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		vu.resultSequence++
		return true
	}
	vu.resultSequence++
	hash := fnv.New64a()
	_, _ = fmt.Fprintf(hash, "%s\x00%d\x00%s\x00%d", vu.Group, vu.ID, actionName, vu.resultSequence)
	const buckets = uint64(1_000_000)
	return hash.Sum64()%buckets < uint64(rate*float64(buckets))
}

func actionBodyTemplateValueEncoding(metadata map[string]any) string {
	if len(metadata) == 0 {
		return ""
	}
	value, _ := metadata[dsl.ImportURLFormTemplateValueEncodingMetadataKey].(string)
	return strings.TrimSpace(value)
}

func (vu *VUser) interpolateActionBody(body, encoding, templateValueEncoding string) (string, string) {
	interpolate := vu.interpolateWithSecrets
	if templateValueEncoding == dsl.ImportURLFormTemplateValueEncodingQueryComponent {
		interpolate = vu.interpolateURLFormTemplateValuesWithSecrets
	}
	decoded, encoded, err := dsl.DecodeBodyEncoding(body, encoding, 0)
	if !encoded || err != nil {
		// Preserve the established full-body-template behavior. A value such as
		// {{secret.BASE64_PAYLOAD}} is itself resolved into encoded data and is
		// therefore intentionally not decodable until after interpolation.
		return interpolate(body)
	}
	resolved, redacted := interpolate(string(decoded))
	return base64.StdEncoding.EncodeToString([]byte(resolved)),
		base64.StdEncoding.EncodeToString([]byte(redacted))
}

// javaVuserSkipsParameterEvaluation reports the script-level "skip automatic
// parameter evaluation" performance setting. When enabled, the automatic
// snapshot below never runs, so no data-source rows or named parameters are
// injected into a Java action and the per-invocation each_occurrence cursor
// does not advance — matching LoadRunner's Java RTS performance flag. Note the
// per-iteration data-row/each_iteration refresh (vu.loadDataRow /
// Parameters.RefreshIteration) is independent of this snapshot and still runs;
// those values simply never reach the JVM while skipping is enabled.
func (vu *VUser) javaVuserSkipsParameterEvaluation() bool {
	if vu.Script == nil {
		return false
	}
	skip := vu.Script.Config.JavaVuser.SkipParameterEvaluation
	return skip != nil && *skip
}

// withJavaVuserRuntimeParameters overlays the VUser's current parameter state
// onto the Java sidecar command. Precedence is current data-row values, then
// named script/.prm parameters, then explicitly authored lr_parameters. The
// explicit action map therefore remains the final authority for intentional
// per-lifecycle overrides.
//
// Automatically injected values are runtime-only. Their redacted projection is
// deliberately opaque so parameter files and current data rows cannot leak via
// action diagnostics or tracing.
func (vu *VUser) withJavaVuserRuntimeParameters(params, redactedParams map[string]any) (map[string]any, map[string]any) {
	explicit, explicitOK := stringAnyMap(params["lr_parameters"])
	if params != nil {
		if _, present := params["lr_parameters"]; present && !explicitOK {
			// Preserve malformed authored input so protocol validation reports it;
			// silently replacing it would hide a script error.
			return params, redactedParams
		}
	}
	method := ""
	if params != nil {
		if value, ok := params["method"].(string); ok {
			method = value
		}
	}
	automatic := vu.javaVuserRuntimeParameterSnapshot(method, explicit)
	if len(automatic) == 0 {
		return params, redactedParams
	}
	redactedExplicit, _ := stringAnyMap(redactedParams["lr_parameters"])

	merged := make(map[string]any, len(automatic)+len(explicit))
	redactedMerged := make(map[string]any, len(automatic)+len(redactedExplicit))
	for name, value := range automatic {
		merged[name] = value
		redactedMerged[name] = "[REDACTED]"
	}
	for name, value := range explicit {
		merged[name] = value
	}
	for name, value := range redactedExplicit {
		redactedMerged[name] = value
	}

	if params == nil {
		params = make(map[string]any, 1)
	}
	if redactedParams == nil {
		redactedParams = make(map[string]any, 1)
	}
	params["lr_parameters"] = merged
	redactedParams["lr_parameters"] = redactedMerged
	return params, redactedParams
}

func (vu *VUser) javaVuserRuntimeParameterSnapshot(method string, overridden map[string]any) map[string]string {
	if vu == nil {
		return nil
	}
	values := make(map[string]string)
	// Data rows are intentionally selected from declared data-source namespaces
	// instead of copying vu.Variables wholesale. Extracted credentials and other
	// transient variables must not be exposed to the JVM unless the script author
	// explicitly places them in lr_parameters.
	if vu.Script != nil {
		for _, source := range vu.Script.DataSources {
			prefix := strings.TrimSpace(source.Name) + "."
			if prefix == "." {
				continue
			}
			for name, value := range vu.Variables {
				if strings.HasPrefix(name, prefix) {
					values[name] = value
				}
			}
		}
	}
	if vu.Parameters != nil {
		advanceOccurrences := javaVuserMethodConsumesOccurrenceRows(method)
		for _, name := range vu.Parameters.order {
			if _, isOverridden := overridden[name]; isOverridden {
				// The explicitly authored lr_parameters entry is the final
				// authority for this name; resolving it here would advance
				// each_occurrence selection state for a value the JVM never sees.
				continue
			}
			if !advanceOccurrences && effectiveParameterUpdate(vu.Parameters.params[name]) == "each_occurrence" {
				// LoadRunner consumes each_occurrence rows at substitution sites in
				// the iteration body. init/end phase invocations must not burn
				// data rows the Java iteration code never read.
				continue
			}
			if value, ok := vu.Parameters.ResolveNamed(name); ok {
				values[name] = value
			}
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

// javaVuserMethodConsumesOccurrenceRows reports whether a java_vuser lifecycle
// method represents iteration work, i.e. the phase where LoadRunner advances
// each_occurrence parameters. Init/end phase invocations reuse cached values
// only.
func javaVuserMethodConsumesOccurrenceRows(method string) bool {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "", "action", "run", "full_lifecycle":
		return true
	default:
		return false
	}
}

func stringAnyMap(value any) (map[string]any, bool) {
	if value == nil {
		return nil, true
	}
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[string]string:
		converted := make(map[string]any, len(typed))
		for name, item := range typed {
			converted[name] = item
		}
		return converted, true
	default:
		return nil, false
	}
}

func scriptUsesHTTPDefaultHeaders(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(script.Protocol)) {
	case "", "http", "soap", "java_http":
		return true
	default:
		return false
	}
}

func (vu *VUser) interpolateHeaderListWithSecrets(headers dsl.HeaderList) (protocol.HeaderList, protocol.HeaderList) {
	if len(headers) == 0 {
		return nil, nil
	}
	resolved := make(protocol.HeaderList, 0, len(headers))
	redacted := make(protocol.HeaderList, 0, len(headers))
	for _, header := range headers {
		name, redactedName := vu.interpolateWithSecrets(header.Name)
		value, redactedValue := vu.interpolateWithSecrets(header.Value)
		resolved = append(resolved, protocol.HeaderEntry{Name: name, Value: value, Enabled: header.Enabled})
		redacted = append(redacted, protocol.HeaderEntry{Name: redactedName, Value: redactedValue, Enabled: header.Enabled})
	}
	return resolved, redacted
}

func toProtocolAuthConfig(auth *dsl.AuthConfig, vu *VUser) (*protocol.AuthConfig, *protocol.AuthConfig) {
	if auth == nil {
		return nil, nil
	}
	username, redactedUsername := vu.interpolateWithSecrets(auth.Username)
	password, redactedPassword := vu.interpolateWithSecrets(auth.Password)
	token, redactedToken := vu.interpolateWithSecrets(auth.Token)
	domain, redactedDomain := vu.interpolateWithSecrets(auth.Domain)
	resolved := &protocol.AuthConfig{
		Type:     auth.Type,
		Username: username,
		Password: password,
		Token:    token,
		Domain:   domain,
	}
	redacted := &protocol.AuthConfig{
		Type:     auth.Type,
		Username: redactedUsername,
		Password: redactedPassword,
		Token:    redactedToken,
		Domain:   redactedDomain,
	}
	return resolved, redacted
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
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
	return vu.interpolateWithSecretsTransform(s, nil)
}

// interpolateURLFormTemplateValuesWithSecrets preserves the already encoded
// literal spans of an application/x-www-form-urlencoded body and escapes each
// template's resolved value as one form component. Unresolved templates remain
// intact so the existing secret/file/parameter execution gates can reject them.
func (vu *VUser) interpolateURLFormTemplateValuesWithSecrets(s string) (string, string) {
	return vu.interpolateWithSecretsTransform(s, url.QueryEscape)
}

func (vu *VUser) interpolateWithSecretsTransform(s string, transform func(string) string) (string, string) {
	if s == "" {
		return s, s
	}
	matches := templatePattern.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return s, s
	}
	var resolved strings.Builder
	var redacted strings.Builder
	resolved.Grow(len(s))
	redacted.Grow(len(s))
	hasRedaction := false
	last := 0
	for _, match := range matches {
		token := s[match[0]:match[1]]
		expr := strings.TrimSpace(token[2 : len(token)-2])
		resolved.WriteString(s[last:match[0]])
		redacted.WriteString(s[last:match[0]])
		last = match[1]
		if expr == "" {
			resolved.WriteString(token)
			redacted.WriteString(token)
			continue
		}
		resolvedValue := token
		redactedValue := token
		resolvedOK := false
		if value, ok := vu.resolveTemplateValue(expr); ok {
			resolvedValue = value
			redactedValue = value
			resolvedOK = true
		} else if strings.HasPrefix(expr, "secret.") {
			vu.unresolvedSecretRefs = append(vu.unresolvedSecretRefs, expr)
		} else if strings.HasPrefix(expr, "file.") {
			vu.unresolvedFileRefs = append(vu.unresolvedFileRefs, expr)
		} else if strings.HasPrefix(expr, "param.") {
			vu.unresolvedParameterRefs = append(vu.unresolvedParameterRefs, expr)
		}
		if strings.HasPrefix(expr, "secret.") {
			hasRedaction = true
			redactedValue = "[REDACTED]"
		} else if strings.HasPrefix(expr, "file.") {
			hasRedaction = true
			if resolvedOK {
				redactedValue = "[FILE:" + strings.TrimSpace(strings.TrimPrefix(expr, "file.")) + "]"
			}
		}
		if resolvedOK {
			if transform != nil {
				resolvedValue = transform(resolvedValue)
				redactedValue = transform(redactedValue)
			}
		}
		resolved.WriteString(resolvedValue)
		redacted.WriteString(redactedValue)
	}
	resolved.WriteString(s[last:])
	if !hasRedaction {
		resolvedString := resolved.String()
		return resolvedString, resolvedString
	}
	redacted.WriteString(s[last:])
	return resolved.String(), redacted.String()
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

func (vu *VUser) interpolateAnyMapWithSecrets(m map[string]any) (map[string]any, map[string]any) {
	if m == nil {
		return nil, nil
	}
	resolved := make(map[string]any, len(m))
	redacted := make(map[string]any, len(m))
	for k, v := range m {
		resolvedValue, redactedValue := vu.interpolateAnyWithSecrets(v)
		resolved[k] = resolvedValue
		redacted[k] = redactedValue
	}
	return resolved, redacted
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

func (vu *VUser) interpolateAnyWithSecrets(v any) (any, any) {
	switch typed := v.(type) {
	case string:
		return vu.interpolateWithSecrets(typed)
	case []any:
		resolved := make([]any, len(typed))
		redacted := make([]any, len(typed))
		for i := range typed {
			resolved[i], redacted[i] = vu.interpolateAnyWithSecrets(typed[i])
		}
		return resolved, redacted
	case map[string]any:
		resolved := make(map[string]any, len(typed))
		redacted := make(map[string]any, len(typed))
		for k, nested := range typed {
			resolved[k], redacted[k] = vu.interpolateAnyWithSecrets(nested)
		}
		return resolved, redacted
	default:
		return v, v
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
	case strings.HasPrefix(expr, "file."):
		if vu.DataProvider == nil {
			return "", false
		}
		key := strings.TrimSpace(strings.TrimPrefix(expr, "file."))
		return vu.DataProvider.GetFileContent(key)
	case strings.HasPrefix(expr, "fn."), strings.HasPrefix(expr, "fake."):
		return resolveBuiltinFunction(expr, time.Now, vu.rng)
	default:
		return "", false
	}
}

func (vu *VUser) secretResolver() SecretResolver {
	if vu.SecretResolver == nil {
		vu.SecretResolver = defaultSecretResolverForScript(vu.Script)
	}
	return vu.SecretResolver
}

const (
	maxVUserExtractedVariables = 4096
	maxVUserExtractionBytes    = 4 << 20
)

func (vu *VUser) mergeExtractions(r *protocol.Result) error {
	if vu == nil || r == nil || len(r.Extractions) == 0 {
		return nil
	}
	if err := protocol.ValidateExtractions(r.Extractions); err != nil {
		return err
	}
	if vu.Variables == nil {
		vu.Variables = make(map[string]string)
	}
	if vu.extractedVariables == nil {
		vu.extractedVariables = make(map[string]int)
	}
	additionalEntries := 0
	additionalBytes := 0
	for key, value := range r.Extractions {
		newSize := len(key) + len(value)
		oldSize, tracked := vu.extractedVariables[key]
		if !tracked {
			additionalEntries++
		}
		additionalBytes += newSize - oldSize
	}
	if additionalEntries > maxVUserExtractedVariables-len(vu.extractedVariables) {
		return fmt.Errorf("VUser extracted variables exceed %d entries", maxVUserExtractedVariables)
	}
	if additionalBytes > maxVUserExtractionBytes-vu.extractedVariableBytes {
		return fmt.Errorf("VUser extracted variables exceed %d bytes", maxVUserExtractionBytes)
	}
	// Build the complete runtime matcher before retaining any sensitive
	// extraction. If its reversible spellings exceed the bounded matcher
	// budget, fail this extraction atomically instead of keeping a value that
	// later evidence could expose.
	// Only the NAME of the receiving variable decides this. An extraction used to
	// also be registered when an import happened to reference that variable from
	// a credential-governed field, but import provenance says nothing about the
	// value the system under test just returned — and the operator reads the
	// import. scrub.SensitiveName is origin-independent and stays: a token landing
	// in `session_token` is still suppressed.
	sensitiveScopes := make(map[string][]string)
	for key, value := range r.Extractions {
		if scrub.SensitiveName(key) {
			sensitiveScopes["extraction:"+strings.ToLower(strings.TrimSpace(key))] = []string{value}
		}
	}
	if err := vu.replaceRuntimeCredentialScopes(sensitiveScopes); err != nil {
		return fmt.Errorf("sensitive extraction redaction: %s", err)
	}
	for key, value := range r.Extractions {
		size := len(key) + len(value)
		oldSize := vu.extractedVariables[key]
		vu.Variables[key] = value
		vu.extractedVariables[key] = size
		vu.extractedVariableBytes += size - oldSize
	}
	// Sensitive runtime extractions (tokens, sessions, passwords, and similar
	// values) must remain usable by later actions without becoming observable
	// in their resolved targets, evidence, logs, or replay reports. Add their
	// exact values only to the in-memory redaction projection after the entire
	// extraction batch has passed validation and retention bounds.
	return nil
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

func toProtocolExtractors(exts []dsl.Extractor, vu *VUser) []protocol.Extractor {
	out := make([]protocol.Extractor, len(exts))
	for i, e := range exts {
		expression := e.Expression
		if vu != nil {
			expression, _ = vu.interpolateWithSecrets(expression)
		}
		out[i] = protocol.Extractor{
			Name:       e.Name,
			Source:     e.Source,
			Type:       e.Type,
			Expression: expression,
			OnMiss:     e.OnMiss,
			Mode:       e.Mode,
			SaveOffset: e.SaveOffset,
			SaveLength: e.SaveLength,
			Convert:    e.Convert,
		}
	}
	return out
}

func toProtocolAssertions(asserts []dsl.Assertion, vu *VUser) []protocol.Assertion {
	out := make([]protocol.Assertion, len(asserts))
	for i, a := range asserts {
		expected := a.Expected
		value := a.Value
		if vu != nil {
			expected, _ = vu.interpolateAnyWithSecrets(expected)
			value, _ = vu.interpolateWithSecrets(value)
		}
		out[i] = protocol.Assertion{
			Type:     a.Type,
			Expected: expected,
			Value:    value,
			Min:      a.Min,
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
