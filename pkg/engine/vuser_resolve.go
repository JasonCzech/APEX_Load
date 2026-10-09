package engine

// vuser_resolve.go — split from vuser.go (same package, behavior-preserving).

import (
	"encoding/base64"
	"fmt"
	"golang.org/x/text/encoding/charmap"
	"hash/fnv"
	"io"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/aurapayload"
	"github.com/jasonczech/apexload/pkg/internal/bodyencoding"
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
		if p := strings.ToLower(strings.TrimSpace(script.Protocol)); p != "http" && p != "http/1.1" && p != "http/2" && p != "soap" && p != "java_http" {
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
func (vu *VUser) loadDataRow() error {
	if vu.DataProvider == nil || !vu.DataProvider.HasSources() {
		return nil
	}
	if vu.Variables == nil {
		vu.Variables = make(map[string]string)
	}
	for _, name := range vu.DataProvider.SourceNames() {
		row, receipt := vu.DataProvider.getRowWithReceipt(name, vu.ID)
		vu.recordReplayRuntimeRow("datasource", name, name, receipt)
		// A missing row must never retain a previous iteration's bindings.
		source := vu.DataProvider.sources[name]
		for _, column := range source.columns {
			delete(vu.Variables, name+"."+column)
		}
		if row == nil && source.strategy == "pop" {
			return fmt.Errorf("data source %q exhausted", name)
		}
		for k, v := range row {
			vu.Variables[k] = v
		}
	}
	_ = vu.refreshImportedCredentialRuntimeValues()
	return nil
}

// makeLogger creates a logger for this VUser based on the log config.
func (vu *VUser) makeLogger(cfg dsl.LogConfig) *slog.Logger {
	if vu.runtimeCredentialRedactionErr != nil {
		// No complete matcher exists; outcome sinks retain the fixed failure.
		cfg.Level = "disabled"
	}
	attrs := []any{"vuser", vu.ID, "group", vu.Group}

	switch cfg.Level {
	case "disabled":
		// Use a no-op handler by setting level extremely high
		return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
			Level: slog.Level(100),
		}))
	default:
		if cfg.OnErrorOnly {
			return vu.errorOnlyLogger(cfg)
		}
		return slog.New(&runtimePublicationLogHandler{next: slog.Default().Handler(), vu: vu}).With(attrs...)
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
		limit := maxRetainedResultEvidenceBytes
		if cfg.MaxResponseBytes > 0 && cfg.MaxResponseBytes < limit {
			limit = cfg.MaxResponseBytes
		}
		attrs = append(attrs, "response_body",
			cloneBoundedResultString(scrubSensitiveTextWithConfig(result.ResponseBody, redactionConfig), limit))
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
	vu.runtimeRedaction.Store(vu.resultPublicationRedactionConfig())
	bindProtocolRuntimeRedaction(vu.Proto, func() *RedactionConfig { return vu.runtimeRedaction.Load() })
	vu.Metrics.bindBrowserDocumentRedaction(vu.Proto, func() *RedactionConfig { return vu.runtimeRedaction.Load() })
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
	vu.unresolvedRunRefs = nil
	vu.bodyResolutionError = nil
	vu.structuredBodyResolvedAction = nil
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
		action.Metadata[dsl.ImportAuraTemplateContractMetadataKey],
	)
	auth, redactedAuth := toProtocolAuthConfig(action.Auth, vu)
	soapAction, redactedSOAPAction := toProtocolSOAPActions(action.SOAP, vu)
	params, redactedParams := vu.interpolateAnyMapWithSecrets(action.Params)
	if dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(action.Type))) == "java_vuser_run" {
		if !vu.javaVuserSkipsParameterEvaluation() {
			params, redactedParams = vu.withJavaVuserRuntimeParameters(params, redactedParams)
		}
		params, redactedParams = vu.withJavaVuserParamBraces(params, redactedParams)
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
		HTTPVersion:                  action.HTTPVersion,
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
		resolved.CaptureResourceDetails = detailPolicy.IncludeResources != nil && *detailPolicy.IncludeResources && resolved.MessageDetailMode != "off"
		resolved.MessageDetailMaxItems = normalizedMessageDetailMaxItems(detailPolicy.MaxItems)
		resolved.MessageDetailMaxItemBytes = normalizedMessageDetailMaxItemBytes(detailPolicy.MaxItemBytes)
		resolved.MessageDetailMaxTotalBytes = normalizedMessageDetailMaxTotalBytes(detailPolicy.MaxTotalBytes)
	}
	// The authored action is the only place the origin of these credentials can
	// be read; by the time the execution path sees the resolved action,
	// {{secret.NAME}} has already been replaced by its value. Both per-action
	// surfaces — the auth block and the target's URL userinfo — are enumerated
	// by actionCredentialCompositeSurfaces.
	if actionBodyTemplateValueEncoding(action.Metadata) == dsl.ImportAuraJSONTemplateValueEncoding && vu.bodyResolutionError == nil {
		vu.structuredBodyResolvedAction = resolved
	}
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

func (vu *VUser) interpolateActionBody(body, encoding, templateValueEncoding string, rawContract any) (string, string) {
	if templateValueEncoding == dsl.ImportAuraJSONTemplateValueEncoding {
		remaining := aurapayload.MaxTemplateOccurrences
		var interpolationErr error
		resolve := func(value string, templateContext aurapayload.TemplateContext) (string, string) {
			if interpolationErr != nil {
				return value, value
			}
			allowance := &remaining
			if budget := vu.concurrentPreparation; budget != nil {
				allowance = &budget.templatesRemaining
			}
			transform := func(index int, _ string, actual, safe string) (string, string, error) {
				if !templateContext.IsFormWireToken(index) {
					return actual, safe, nil
				}
				decoded, err := url.QueryUnescape(actual)
				if err != nil {
					return "", "", &aurapayload.Error{Code: aurapayload.InvalidForm}
				}
				if safe == actual {
					safe = decoded
				}
				return decoded, safe, nil
			}
			actual, safe, err := vu.interpolateWithSecretsPolicyBounded(value, transform, aurapayload.MaxBodyBytes, allowance, templateContext.IsLiteralToken)
			if err != nil {
				interpolationErr = err
				return value, value
			}
			return actual, safe
		}
		var resolved, redacted string
		var err error
		if budget := vu.concurrentPreparation; budget != nil {
			// Reserve codec work separately from callback interpolation so both
			// passes cannot independently consume the same group remainder.
			allowance := min(aurapayload.MaxWorkBytes, budget.workRemaining/2, budget.bytesRemaining)
			budget.workRemaining -= allowance
			var work int
			resolved, redacted, work, err = aurapayload.ResolveContextualWithWorkAccounting(body, encoding, rawContract, resolve, allowance)
			budget.workRemaining += allowance - work
		} else {
			resolved, redacted, _, err = aurapayload.ResolveContextualWithWorkAccounting(body, encoding, rawContract, resolve, aurapayload.MaxWorkBytes)
		}
		if interpolationErr != nil {
			err = interpolationErr
		}
		if err != nil {
			vu.bodyResolutionError = err
			return body, body
		}
		if budget := vu.concurrentPreparation; budget != nil {
			if err := budget.charge(len(resolved) + len(redacted)); err != nil {
				vu.bodyResolutionError = err
				return "", ""
			}
		}
		return resolved, redacted
	}
	if rawContract != nil {
		vu.bodyResolutionError = &aurapayload.Error{Code: aurapayload.InvalidTemplateContract}
		return body, body
	}
	if bodyencoding.IsCEscaped(encoding) {
		return vu.resolveCEscapedBody(body)
	}
	interpolate := vu.interpolateWithSecrets
	if templateValueEncoding == dsl.ImportURLFormTemplateValueEncodingQueryComponent {
		interpolate = vu.interpolateURLFormTemplateValuesWithSecrets
		if strings.EqualFold(encoding, "windows-1252") {
			interpolate = func(s string) (string, string) {
				return vu.interpolateWithSecretsTransform(s, func(value string) string {
					raw, err := charmap.Windows1252.NewEncoder().String(value)
					if err != nil {
						vu.unresolvedParameterRefs = append(vu.unresolvedParameterRefs, "form value is not representable in Windows-1252")
						return ""
					}
					return url.QueryEscape(raw)
				})
			}
		}
	}
	decodeLimit := 0
	if budget := vu.concurrentPreparation; budget != nil {
		decodeLimit = max(1, budget.bytesRemaining)
		if strings.EqualFold(strings.TrimSpace(encoding), "base64") && !dsl.IsTrustedFullBodyBase64Template(body) {
			if err := budget.charge(base64.StdEncoding.DecodedLen(len(body))); err != nil {
				return "", ""
			}
		}
	}
	decoded, encoded, err := dsl.DecodeBodyEncoding(body, encoding, decodeLimit)
	if err == dsl.ErrBodyEncodingLimit && vu.concurrentPreparation != nil {
		vu.concurrentPreparation.err = err
		return "", ""
	}
	if !encoded || err != nil {
		// Preserve the established full-body-template behavior. A value such as
		// {{secret.BASE64_PAYLOAD}} is itself resolved into encoded data and is
		// therefore intentionally not decodable until after interpolation.
		return interpolate(body)
	}
	resolved, redacted := interpolate(string(decoded))
	if budget := vu.concurrentPreparation; budget != nil {
		if err := budget.charge(base64.StdEncoding.EncodedLen(len(resolved)) + base64.StdEncoding.EncodedLen(len(redacted))); err != nil {
			return "", ""
		}
	}
	return base64.StdEncoding.EncodeToString([]byte(resolved)),
		base64.StdEncoding.EncodeToString([]byte(redacted))
}

// resolveCEscapedBody resolves a c_escaped body (LoadRunner BodyBinary):
// templates first, with each value escaped so it is sent verbatim, then the
// authored escapes are decoded into the wire bytes.
func (vu *VUser) resolveCEscapedBody(body string) (string, string) {
	resolved, redacted := vu.interpolateWithSecretsTransform(body, bodyencoding.EscapeCText)
	decoded, err := bodyencoding.DecodeCEscapes(resolved)
	if err != nil {
		vu.bodyResolutionError = err
		return body, body
	}
	decodedRedacted, err := bodyencoding.DecodeCEscapes(redacted)
	if err != nil {
		// Never fall back to the resolved bytes: they carry secret values.
		decodedRedacted = []byte(redacted)
	}
	return string(decoded), string(decodedRedacted)
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

// withJavaVuserParamBraces delivers custom parameter delimiters (.usr
// ParamLeftBrace/ParamRightBrace) to the runner's lr.eval_string as reserved
// lr_parameters entries, which the lrapi shim consumes instead of storing as
// parameters. They are configuration, so they travel even when automatic
// parameter evaluation is skipped.
func (vu *VUser) withJavaVuserParamBraces(params, redactedParams map[string]any) (map[string]any, map[string]any) {
	if vu == nil || vu.Script == nil {
		return params, redactedParams
	}
	cfg := vu.Script.Config.JavaVuser
	if cfg.ParamLeftBrace == "" || cfg.ParamRightBrace == "" {
		return params, redactedParams
	}
	set := func(container map[string]any) (map[string]any, bool) {
		if container == nil {
			container = make(map[string]any, 1)
		}
		entries, ok := stringAnyMap(container["lr_parameters"])
		if !ok {
			if _, present := container["lr_parameters"]; present {
				return container, false // malformed authored input; validation reports it
			}
			entries = make(map[string]any, 2)
		}
		merged := make(map[string]any, len(entries)+2)
		for name, value := range entries {
			merged[name] = value
		}
		merged[javaVuserParamLeftBraceKey] = cfg.ParamLeftBrace
		merged[javaVuserParamRightBraceKey] = cfg.ParamRightBrace
		container["lr_parameters"] = merged
		return container, true
	}
	var ok bool
	if params, ok = set(params); ok {
		redactedParams, _ = set(redactedParams)
	}
	return params, redactedParams
}

// Reserved lr_parameters keys for custom parameter delimiters; they must match
// PARAM_LEFT_BRACE_KEY and PARAM_RIGHT_BRACE_KEY in runners/java_vuser/lrapi/lr.java.
const (
	javaVuserParamLeftBraceKey  = "__apexload_param_left_brace"
	javaVuserParamRightBraceKey = "__apexload_param_right_brace"
)

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
	case "", "http", "http/1.1", "http/2", "soap", "java_http":
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
	if budget := vu.concurrentPreparation; budget != nil {
		if budget.err != nil {
			return "", ""
		}
		if budget.bytesRemaining <= 0 && s != "" {
			budget.err = fmt.Errorf("concurrent HTTP preparation byte limit exceeded")
			return "", ""
		}
		resolved, redacted, err := vu.interpolateWithSecretsTransformBounded(s, transform, budget.bytesRemaining, &budget.templatesRemaining)
		if err != nil {
			budget.err = err
		}
		return resolved, redacted
	}
	resolved, redacted, _ := vu.interpolateWithSecretsTransformBounded(s, transform, 0, nil)
	return resolved, redacted
}

// Selected structured bodies check length while appending each authored
// occurrence. Runtime values are never expanded into an unbounded temporary
// string before the codec can enforce its own escaped/output work bounds.
func (vu *VUser) interpolateWithSecretsTransformBounded(s string, transform func(string) string, maxBytes int, remaining *int) (string, string, error) {
	var occurrenceTransform func(int, string, string, string) (string, string, error)
	if transform != nil {
		occurrenceTransform = func(_ int, _ string, actual, safe string) (string, string, error) {
			if budget := vu.concurrentPreparation; budget != nil && (len(actual) > budget.bytesRemaining/3 || len(safe) > budget.bytesRemaining/3) {
				return "", "", fmt.Errorf("concurrent HTTP preparation byte limit exceeded")
			}
			return transform(actual), transform(safe), nil
		}
	}
	return vu.interpolateWithSecretsOccurrenceTransformBounded(s, occurrenceTransform, maxBytes, remaining)
}

func (vu *VUser) interpolateWithSecretsOccurrenceTransformBounded(s string, transform func(int, string, string, string) (string, string, error), maxBytes int, remaining *int) (string, string, error) {
	return vu.interpolateWithSecretsPolicyBounded(s, transform, maxBytes, remaining, nil)
}

func (vu *VUser) interpolateWithSecretsPolicyBounded(s string, transform func(int, string, string, string) (string, string, error), maxBytes int, remaining *int, literal func(int) bool) (string, string, error) {
	if maxBytes > 0 && len(s) > maxBytes {
		return "", "", &aurapayload.Error{Code: aurapayload.ByteLimit}
	}
	if s == "" {
		return s, s, nil
	}
	matchLimit := -1
	if remaining != nil {
		matchLimit = *remaining + 1
	}
	matches := templatePattern.FindAllStringIndex(s, matchLimit)
	if remaining != nil {
		if len(matches) > *remaining {
			return "", "", &aurapayload.Error{Code: aurapayload.TokenLimit}
		}
		*remaining -= len(matches)
	}
	if len(matches) == 0 {
		if budget := vu.concurrentPreparation; budget != nil {
			if err := budget.charge(2 * len(s)); err != nil {
				return "", "", err
			}
		}
		return s, s, nil
	}
	var resolved, redacted strings.Builder
	if vu.concurrentPreparation == nil {
		resolved.Grow(len(s))
		redacted.Grow(len(s))
	}
	writePair := func(actual, safe string) error {
		if maxBytes > 0 && (len(actual) > maxBytes-resolved.Len() || len(safe) > maxBytes-redacted.Len()) {
			return &aurapayload.Error{Code: aurapayload.ByteLimit}
		}
		if budget := vu.concurrentPreparation; budget != nil {
			if err := budget.charge(len(actual) + len(safe)); err != nil {
				return err
			}
		}
		resolved.WriteString(actual)
		redacted.WriteString(safe)
		return nil
	}
	hasRedaction, last := false, 0
	for occurrenceIndex, match := range matches {
		token := s[match[0]:match[1]]
		expr := strings.TrimSpace(token[2 : len(token)-2])
		if err := writePair(s[last:match[0]], s[last:match[0]]); err != nil {
			return "", "", err
		}
		last = match[1]
		if expr == "" || literal != nil && literal(occurrenceIndex) {
			if err := writePair(token, token); err != nil {
				return "", "", err
			}
			continue
		}
		if budget := vu.concurrentPreparation; budget != nil && strings.HasPrefix(expr, "var.") {
			budget.references = append(budget.references, strings.TrimSpace(strings.TrimPrefix(expr, "var.")))
		}
		resolvedValue, redactedValue, resolvedOK := token, token, false
		if value, ok := vu.resolveTemplateValue(expr); ok {
			resolvedValue, redactedValue, resolvedOK = value, value, true
		} else if strings.HasPrefix(expr, "secret.") {
			vu.unresolvedSecretRefs = append(vu.unresolvedSecretRefs, expr)
		} else if strings.HasPrefix(expr, "file.") {
			vu.unresolvedFileRefs = append(vu.unresolvedFileRefs, expr)
		} else if strings.HasPrefix(expr, "param.") {
			vu.unresolvedParameterRefs = append(vu.unresolvedParameterRefs, expr)
		} else if strings.HasPrefix(expr, "run.") {
			vu.unresolvedRunRefs = append(vu.unresolvedRunRefs, expr)
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
		if resolvedOK && transform != nil {
			var err error
			resolvedValue, redactedValue, err = transform(occurrenceIndex, token, resolvedValue, redactedValue)
			if err != nil {
				return "", "", err
			}
		}
		if err := writePair(resolvedValue, redactedValue); err != nil {
			return "", "", err
		}
	}
	if err := writePair(s[last:], s[last:]); err != nil {
		return "", "", err
	}
	if !hasRedaction {
		actual := resolved.String()
		return actual, actual, nil
	}
	return resolved.String(), redacted.String(), nil
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
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	if vu.concurrentPreparation != nil {
		sort.Strings(keys)
	}
	for _, k := range keys {
		resolvedValue, redactedValue := vu.interpolateWithSecrets(m[k])
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
	for _, k := range vu.interpolationKeys(m) {
		out[k] = vu.interpolateAny(m[k])
	}
	return out
}

func (vu *VUser) interpolateAnyMapWithSecrets(m map[string]any) (map[string]any, map[string]any) {
	if m == nil {
		return nil, nil
	}
	resolved := make(map[string]any, len(m))
	redacted := make(map[string]any, len(m))
	for _, k := range vu.interpolationKeys(m) {
		resolvedValue, redactedValue := vu.interpolateAnyWithSecrets(m[k])
		resolved[k] = resolvedValue
		redacted[k] = redactedValue
	}
	return resolved, redacted
}

func (vu *VUser) interpolateAny(v any) any {
	switch typed := v.(type) {
	case string:
		return vu.interpolate(typed)
	case []string:
		out := make([]string, len(typed))
		for i := range typed {
			out[i] = vu.interpolate(typed[i])
		}
		return out
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = vu.interpolateAny(typed[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for _, k := range vu.interpolationKeys(typed) {
			out[k] = vu.interpolateAny(typed[k])
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
	case []string:
		resolved, redacted := make([]string, len(typed)), make([]string, len(typed))
		for i := range typed {
			resolved[i], redacted[i] = vu.interpolateWithSecrets(typed[i])
		}
		return resolved, redacted
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
		for _, k := range vu.interpolationKeys(typed) {
			resolved[k], redacted[k] = vu.interpolateAnyWithSecrets(typed[k])
		}
		return resolved, redacted
	default:
		return v, v
	}
}

func (vu *VUser) resolveTemplateValue(expr string) (string, bool) {
	switch {
	case expr == "run.id":
		return vu.RunID, vu.RunID != "" && strings.TrimSpace(vu.RunID) == vu.RunID
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

// observeExtractionClassification admits response-derived classifications before
// logs, retry decisions or variable storage can discard their source values.
// It never changes variables or advances data/parameter allocation.
func (vu *VUser) observeExtractionClassification(r *protocol.Result) error {
	if vu == nil || r == nil {
		return nil
	}
	if r.ExtractionsIncomplete {
		vu.failRuntimeCredentialRedaction(fmt.Errorf("runtime credential extraction classification unavailable"))
		return vu.runtimeCredentialRedactionErr
	}
	if err := protocol.ValidateExtractions(r.Extractions); err != nil {
		vu.failRuntimeCredentialRedaction(fmt.Errorf("runtime credential extraction classification unavailable"))
		return err
	}
	// Observing a named sensitive response value establishes its classification
	// even if the separate variable-storage budget rejects this batch. Keep the
	// bounded observation history so neutral aliases and earlier tokens remain
	// protected across later scope refreshes and variable replacements.
	vu.ensureRuntimeCredentialRedactionState()
	const observedScope = "extraction:observed"
	values := vu.runtimeCredentialScopes[observedScope]
	var updates []string
	for key, value := range r.Extractions {
		if value == "" || !scrub.SensitiveName(key) {
			continue
		}
		index := sort.SearchStrings(values, value)
		if index < len(values) && values[index] == value {
			continue
		}
		if updates == nil {
			updates = append([]string(nil), values...)
		}
		updates = append(updates, value)
	}
	if updates != nil {
		if err := vu.replaceRuntimeCredentialScope(observedScope, updates); err != nil {
			return fmt.Errorf("sensitive extraction redaction: %s", err)
		}
	}
	return nil
}

func (vu *VUser) mergeExtractions(r *protocol.Result) error {
	if vu == nil || r == nil {
		return nil
	}
	if err := vu.observeExtractionClassification(r); err != nil {
		return err
	}
	if len(r.Extractions) == 0 {
		return nil
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
	for key, value := range r.Extractions {
		size := len(key) + len(value)
		oldSize := vu.extractedVariables[key]
		vu.Variables[key] = value
		vu.extractedVariables[key] = size
		vu.extractedVariableBytes += size - oldSize
	}
	// Raw variables remain exact and are retained only after atomic storage admission.
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
		defaultValue := e.DefaultValue
		if defaultValue != nil {
			value := *defaultValue
			if vu != nil {
				value, _ = vu.interpolateWithSecrets(value)
			}
			defaultValue = &value
		}
		out[i] = protocol.Extractor{
			DefaultValue: defaultValue,
			Name:         e.Name,
			Source:       e.Source,
			Type:         e.Type,
			Expression:   expression,
			OnMiss:       e.OnMiss,
			Mode:         e.Mode,
			SaveOffset:   e.SaveOffset,
			SaveLength:   e.SaveLength,
			Convert:      e.Convert,
			Scope:        toProtocolExtractorScope(e.Scope),
		}
	}
	return out
}

func toProtocolExtractorScope(scope *dsl.ExtractorScope) *protocol.ExtractorScope {
	if scope == nil {
		return nil
	}
	return &protocol.ExtractorScope{
		Redirects:   scope.Redirects,
		Resources:   scope.Resources,
		RequestURL:  scope.RequestURL,
		ContentType: scope.ContentType,
		HeaderNames: append([]string(nil), scope.HeaderNames...),
	}
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
			Type:            a.Type,
			LegacyHTMLCheck: a.LegacyHTMLCheck,
			Expected:        expected,
			Value:           value,
			Min:             a.Min,
			Max:             a.Max,
			CaseInsensitive: a.CaseInsensitive,
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

// recordDataRowError makes exhaustion visible in run results before stopping.
func (vu *VUser) recordDataRowError(err error) {
	message := scrubSensitiveTextWithConfig(err.Error(), vu.RedactionConfig)
	vu.makeLogger(vu.Script.Config.Log).Error("data row load failed", "error", message)
	if vu.Metrics != nil {
		vu.recordResult(&protocol.Result{ActionName: "data source", StartTime: time.Now(), ErrorMessage: message, Success: false}, vu.Script.Config.ErrorHandling, vu.Script.Config.Log)
	}
}

// currentDataBindings captures only data-source bindings for lifecycle cleanup.
func (vu *VUser) currentDataBindings() map[string]string {
	if vu.DataProvider == nil {
		return nil
	}
	hasPop := false
	for _, source := range vu.DataProvider.sources {
		if source.strategy == "pop" {
			hasPop = true
			break
		}
	}
	if !hasPop {
		return nil
	}
	bindings := make(map[string]string)
	for name, source := range vu.DataProvider.sources {
		for _, column := range source.columns {
			key := name + "." + column
			if value, ok := vu.Variables[key]; ok {
				bindings[key] = value
			}
		}
	}
	return bindings
}

func (vu *VUser) restoreDataBindings(bindings map[string]string) {
	if vu.DataProvider == nil {
		return
	}
	for name, source := range vu.DataProvider.sources {
		for _, column := range source.columns {
			delete(vu.Variables, name+"."+column)
		}
	}
	for key, value := range bindings {
		vu.Variables[key] = value
	}
}
