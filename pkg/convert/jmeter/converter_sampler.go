package jmeter

// converter_sampler.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/tmpl"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func applyJMeterCacheManager(node *xmlNode, script *dsl.Script) {
	if script == nil {
		return
	}
	if script.Config.WebRuntime.AdditionalAttributes == nil {
		script.Config.WebRuntime.AdditionalAttributes = map[string]string{}
	}
	clearEachIteration := firstNonEmpty(prop(node, "clearEachIteration"), prop(node, "CacheManager.clearEachIteration"))
	useExpires := firstNonEmpty(prop(node, "useExpires"), prop(node, "CacheManager.useExpires"))
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

func convertHTTPSampler(node *treeNode, ctx *convertContext, opts ConvertOptions, diagnostics *diag.Collector) (*dsl.Action, []string) {
	element := node.Element
	name := sanitizeName(attrOr(element, "testname", "HTTP Request"))
	location := jmeterElementLocation(element)
	method := firstNonEmpty(prop(element, "HTTPSampler.method"), "GET")
	rawPath := convertVariablesWithDiagnostics(firstNonEmpty(prop(element, "HTTPSampler.path"), ctx.defaultPath, "/"), diagnostics, location)
	protocol := firstNonEmpty(prop(element, "HTTPSampler.protocol"), ctx.defaultProtocol)
	domain := convertVariablesWithDiagnostics(firstNonEmpty(prop(element, "HTTPSampler.domain"), ctx.defaultDomain), diagnostics, location)
	port := firstNonEmpty(prop(element, "HTTPSampler.port"), ctx.defaultPort)
	target := rawPath
	absoluteURL := rawPath
	if domain != "" {
		absolute := buildURL(protocol, domain, port, rawPath)
		absoluteURL = absolute
		target = makeRelative(absolute, firstNonEmpty(opts.BaseURL, buildBaseURL(protocol, domain, port)))
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
	for k, v := range ctx.headers {
		action.Headers[k] = convertVariablesWithDiagnostics(v, diagnostics, location)
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
	return action, warnings
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
		paramName := firstNonEmpty(prop(file, "File.paramname"), prop(file, "HTTPFileArg.paramname"), "file")
		filePath := convertVariablesWithDiagnostics(firstNonEmpty(prop(file, "File.path"), prop(file, "HTTPFileArg.path")), diagnostics, location)
		mimeType := firstNonEmpty(prop(file, "File.mimetype"), prop(file, "HTTPFileArg.mimetype"), "application/octet-stream")
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
		return 10, true
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
