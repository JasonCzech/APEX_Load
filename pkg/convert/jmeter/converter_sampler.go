package jmeter

// converter_sampler.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/tmpl"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

func applyJMeterCacheManager(node *xmlNode, script *dsl.Script) {
	if script == nil {
		return
	}
	if script.Config.WebRuntime.AdditionalAttributes == nil {
		script.Config.WebRuntime.AdditionalAttributes = map[string]string{}
	}
	clearEachIteration := convertutil.FirstNonEmpty(prop(node, "clearEachIteration"), prop(node, "CacheManager.clearEachIteration"))
	useExpires := convertutil.FirstNonEmpty(prop(node, "useExpires"), prop(node, "CacheManager.useExpires"))
	if clearEachIteration != "" {
		script.Config.WebRuntime.AdditionalAttributes["jmeter.cache_manager.clear_each_iteration"] = strings.ToLower(clearEachIteration)
	}
	if useExpires != "" {
		script.Config.WebRuntime.AdditionalAttributes["jmeter.cache_manager.use_expires"] = strings.ToLower(useExpires)
	}
	if script.Config.WebRuntime.AdditionalAttributes["jmeter.cache_manager.clear_each_iteration"] == "" {
		script.Config.WebRuntime.AdditionalAttributes["jmeter.cache_manager.enabled"] = "true"
	}
	setJMeterRuntimeParity(script, "cacheManager", "partial")
}

func convertHTTPSampler(node *treeNode, ctx *convertContext, opts ConvertOptions, diagnostics *diag.Collector) (*dsl.Action, []string, error) {
	element := node.Element
	name := sanitizeName(attrOr(element, "testname", "HTTP Request"))
	location := jmeterElementLocation(element)
	method := convertutil.FirstNonEmpty(prop(element, "HTTPSampler.method"), "GET")
	rawPath := convertVariablesWithDiagnostics(convertutil.FirstNonEmpty(prop(element, "HTTPSampler.path"), ctx.defaultPath, "/"), diagnostics, location)
	protocol := convertVariablesWithDiagnostics(convertutil.FirstNonEmpty(prop(element, "HTTPSampler.protocol"), ctx.defaultProtocol), diagnostics, location)
	domain := convertVariablesWithDiagnostics(convertutil.FirstNonEmpty(prop(element, "HTTPSampler.domain"), ctx.defaultDomain), diagnostics, location)
	port := convertVariablesWithDiagnostics(convertutil.FirstNonEmpty(prop(element, "HTTPSampler.port"), ctx.defaultPort), diagnostics, location)
	resolution := ctx.variableScope.resolve(diagnostics)
	provenVariables := jmeterProvenVariables(ctx)
	rawPath, targetSourceVariables := resolution.expandRuntimeValue(rawPath, provenVariables)
	var applied []string
	protocol, applied = resolution.expandRuntimeValue(protocol, provenVariables)
	targetSourceVariables = append(targetSourceVariables, applied...)
	domain, applied = resolution.expandRuntimeValue(domain, provenVariables)
	targetSourceVariables = append(targetSourceVariables, applied...)
	port, applied = resolution.expandRuntimeValue(port, provenVariables)
	targetSourceVariables = uniqueSortedJMeterNames(append(targetSourceVariables, applied...))
	target := rawPath
	absoluteURL := rawPath
	if domain != "" {
		absolute := buildURL(protocol, domain, port, rawPath)
		absoluteURL = absolute
		samplerBaseURL := buildBaseURL(protocol, domain, port)
		if ctx.endpoint == nil {
			ctx.endpoint = &jmeterEndpointState{}
		}
		if ctx.endpoint.baseURL == "" && !strings.Contains(samplerBaseURL, "{{") {
			ctx.endpoint.baseURL = samplerBaseURL
		}
		// Relative requests to the first source endpoint follow the explicit
		// script-level override. Other hosts retain their absolute identity.
		projectionBaseURL := ctx.endpoint.baseURL
		if strings.HasPrefix(rawPath, "http://") || strings.HasPrefix(rawPath, "https://") {
			projectionBaseURL = strings.TrimSpace(opts.BaseURL)
		}

		target = makeRelative(absolute, projectionBaseURL)
	}
	action := &dsl.Action{
		Name:       name,
		Type:       method,
		Method:     method,
		Target:     target,
		Headers:    map[string]string{},
		Assertions: append([]dsl.Assertion(nil), ctx.assertions...),
	}
	var warnings []string
	for _, header := range ctx.headers {
		value := convertVariablesWithDiagnostics(header.Value, diagnostics, location)
		action.HeaderList = append(action.HeaderList, dsl.HeaderEntry{Name: header.Name, Value: value})
		setJMeterHeaderSummary(action.Headers, header.Name, value)
	}
	body, params := parseSamplerArguments(element, diagnostics, location)
	if body != "" {
		action.Body = convertVariablesWithDiagnostics(body, diagnostics, location)
	}
	if len(params) > 0 {
		action.Params = params
	}
	applySamplerFiles(action, element, diagnostics, location)
	if auth, ok := authForSampler(ctx.auths, absoluteURL, rawPath); ok {
		action.Auth = &auth
	}
	if reviewAuth, ok := reviewAuthForSampler(ctx.auths, absoluteURL, rawPath); ok {
		// The execution gate prevents transport while the exact source fields
		// remain available for an operator to translate without credential
		// re-entry. An explicit unsupported override also prevents inheritance
		// from an executable script-level credential.
		retained := reviewAuth.Config
		action.Auth = &retained
		applyJMeterAuthReviewGate(action, reviewAuth, location)
	}
	if timeout := samplerTimeoutMs(element); timeout > 0 {
		action.TimeoutMs = timeout
	}
	if redirects, ok := samplerRedirects(element); ok {
		action.MaxRedirects = &redirects
	}
	if ctx.transaction != "" {
		action.Name = sanitizeName(ctx.transaction + " " + action.Name)
		if action.Metadata == nil {
			action.Metadata = map[string]any{}
		}
		action.Metadata["transaction"] = ctx.transaction
	}
	action.Name = allocateJMeterActionName(ctx, action.Name)
	if extractors, extractorWarnings := parseSamplerExtractors(node, diagnostics); len(extractors) > 0 {
		action.Extractors = extractors
		warnings = append(warnings, extractorWarnings...)
	} else if len(extractorWarnings) > 0 {
		warnings = append(warnings, extractorWarnings...)
	}
	if opts.IncludeAssertions && !hasStatusAssertion(action.Assertions) {
		action.Assertions = append(action.Assertions, dsl.Assertion{Type: "status_code", Expected: 200})
	}
	if ctx.timerMs > 0 {
		action.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: float64(ctx.timerMs)}
		action.ThinkTimeAfter = true
	}
	if len(action.Headers) == 0 {
		action.Headers = nil
	}
	if domain == "" && !strings.HasPrefix(rawPath, "http://") && !strings.HasPrefix(rawPath, "https://") {
		warnings = append(warnings, fmt.Sprintf("%s: no domain/default host was found; target kept as %q", name, rawPath))
	}
	applySOAPSamplerHints(action)
	finalized, report, err := finalizeJMeterHTTPRequest(
		action,
		location,
		convertutil.FirstNonEmpty(opts.FileName, "import.jmx"),
		ctx,
		jmeterVariableApplication{target: targetSourceVariables},
	)
	if err != nil {
		return nil, warnings, fmt.Errorf("finalize JMeter HTTP sampler %q: %w", name, err)
	}
	if ctx.requestReports != nil {
		*ctx.requestReports = append(*ctx.requestReports, report)
	}
	if ctx.provenVariables == nil {
		ctx.provenVariables = make(map[string]struct{})
	}
	for _, extractor := range finalized.Extractors {
		if extractor.Name != "" {
			ctx.provenVariables[extractor.Name] = struct{}{}
		}
	}
	return &finalized, warnings, nil
}

func allocateJMeterActionName(ctx *convertContext, raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		base = "JMeter Action"
	}
	if ctx == nil {
		return base
	}
	if ctx.actionNames == nil {
		ctx.actionNames = make(map[string]int)
	}
	identity := strings.ToLower(base)
	if ctx.actionNames[identity] == 0 {
		ctx.actionNames[identity] = 1
		return base
	}
	for suffix := ctx.actionNames[identity] + 1; ; suffix++ {
		candidate := fmt.Sprintf("%s_%d", base, suffix)
		candidateIdentity := strings.ToLower(strings.TrimSpace(candidate))
		if ctx.actionNames[candidateIdentity] != 0 {
			continue
		}
		ctx.actionNames[identity] = suffix
		ctx.actionNames[candidateIdentity] = 1
		return candidate
	}
}

func setJMeterHeaderSummary(headers map[string]string, name string, value string) {
	for existing := range headers {
		if strings.EqualFold(strings.TrimSpace(existing), strings.TrimSpace(name)) {
			delete(headers, existing)
		}
	}
	headers[name] = value
}

func applyJMeterAuthReviewGate(action *dsl.Action, auth jmeterAuth, fallbackSourceRef string) {
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	if _, exists := action.Metadata["execution_blocker"]; !exists {
		action.Metadata["execution_blocker"] = "import_review_required"
	}
	reasonCode := auth.ReviewCode
	if reasonCode == "" {
		reasonCode = "unsupported_auth_scheme"
	}
	sourceRef := strings.TrimSpace(auth.SourceRef)
	if sourceRef == "" {
		sourceRef = fallbackSourceRef
	}
	action.Metadata["import_review"] = map[string]any{
		"version":      1,
		"source":       "jmeter",
		"reason_codes": []string{reasonCode},
		"source_refs":  []string{sourceRef},
		"resolution":   "replace_behavior_and_remove_gate",
	}
}

func parseSamplerArguments(node *xmlNode, diagnostics *diag.Collector, location string) (string, map[string]any) {
	rawBody := strings.EqualFold(prop(node, "HTTPSampler.postBodyRaw"), "true")
	args := descendantsByAttr(node, "elementType", "HTTPArgument")
	if len(args) == 0 {
		return "", nil
	}
	if rawBody {
		for _, arg := range args {
			if value := prop(arg, "Argument.value"); value != "" {
				return value, nil
			}
		}
	}
	params := map[string]any{}
	for _, arg := range args {
		name := prop(arg, "Argument.name")
		value := prop(arg, "Argument.value")
		if name != "" {
			params[name] = convertVariablesWithDiagnostics(value, diagnostics, location)
		}
	}
	if len(params) == 0 {
		return "", nil
	}
	return "", params
}

func applySamplerFiles(action *dsl.Action, node *xmlNode, diagnostics *diag.Collector, location string) {
	files := descendantsByAttr(node, "elementType", "HTTPFileArg")
	if len(files) == 0 {
		return
	}
	if action.Params == nil {
		action.Params = map[string]any{}
	}
	if action.Headers == nil {
		action.Headers = map[string]string{}
	}
	if firstHeaderValue(action.Headers, "Content-Type") == "" {
		action.Headers["Content-Type"] = "multipart/form-data"
	}
	for _, file := range files {
		paramName := convertutil.FirstNonEmpty(prop(file, "File.paramname"), prop(file, "HTTPFileArg.paramname"), "file")
		filePath := convertVariablesWithDiagnostics(convertutil.FirstNonEmpty(prop(file, "File.path"), prop(file, "HTTPFileArg.path")), diagnostics, location)
		mimeType := convertutil.FirstNonEmpty(prop(file, "File.mimetype"), prop(file, "HTTPFileArg.mimetype"), "application/octet-stream")
		placeholder := "{{file." + sanitizeName(paramName) + "}}"
		entry := map[string]any{
			"value":        placeholder,
			"content_type": mimeType,
		}
		if filePath != "" {
			entry["file_name"] = jmeterBaseName(filePath)
			entry["source_path"] = filePath
		}
		action.Params[paramName] = entry
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "JMeter HTTPFileArg", location, "file upload parameter "+paramName+" uses placeholder "+placeholder+"; provide governed file data before replay")
		}
	}
}

func samplerTimeoutMs(node *xmlNode) int {
	connectTimeout := parseInt(prop(node, "HTTPSampler.connect_timeout"))
	responseTimeout := parseInt(prop(node, "HTTPSampler.response_timeout"))
	if responseTimeout > 0 {
		return responseTimeout
	}
	return connectTimeout
}

func samplerRedirects(node *xmlNode) (int, bool) {
	follow, followOK := parseOptionalBool(prop(node, "HTTPSampler.follow_redirects"))
	auto, autoOK := parseOptionalBool(prop(node, "HTTPSampler.auto_redirects"))
	if !followOK && !autoOK {
		return 0, false
	}
	if follow || auto {
		return httpprotocol.Capabilities().MaxRedirects, true
	}
	return 0, true
}

var jmeterVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_.-]*)\}`)

func convertVariables(value string) string {
	return convertVariablesWithDiagnostics(value, nil, "")
}

func convertVariablesWithDiagnostics(value string, diagnostics *diag.Collector, location string) string {
	value = tmpl.TranslateDynamic(value, diagnostics, location)
	return jmeterVarPattern.ReplaceAllString(value, "{{var.$1}}")
}

func buildBaseURL(protocol, domain, port string) string {
	if domain == "" {
		return ""
	}
	if protocol == "" {
		protocol = "https"
	}
	if port != "" {
		return fmt.Sprintf("%s://%s:%s", protocol, domain, port)
	}
	return fmt.Sprintf("%s://%s", protocol, domain)
}

func buildURL(protocol, domain, port, rawPath string) string {
	base := buildBaseURL(protocol, domain, port)
	if strings.HasPrefix(rawPath, "http://") || strings.HasPrefix(rawPath, "https://") {
		return rawPath
	}
	if !strings.HasPrefix(rawPath, "/") {
		rawPath = "/" + rawPath
	}
	return base + rawPath
}

func makeRelative(rawURL, baseURL string) string {
	if baseURL == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return rawURL
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return rawURL
	}
	if strings.EqualFold(u.Scheme, base.Scheme) && strings.EqualFold(u.Host, base.Host) {
		target := u.Path
		if target == "" {
			target = "/"
		}
		if u.RawQuery != "" {
			target += "?" + u.RawQuery
		}
		return target
	}
	return rawURL
}

func detectBaseURL(actions []dsl.Action) string {
	for _, action := range actions {
		u, err := url.Parse(action.Target)
		if err == nil && u.Scheme != "" && u.Host != "" {
			return u.Scheme + "://" + u.Host
		}
	}
	return ""
}
