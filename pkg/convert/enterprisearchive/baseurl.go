package enterprisearchive

// baseurl.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// unresolvedBaseURLHost is the parseable placeholder substituted for a
// parameterized base-URL host that could not be resolved to a concrete value at
// import time. `.invalid` is reserved (RFC 6761), so it can never accidentally
// resolve — the operator must set the real host before running.
const unresolvedBaseURLHost = "set-base-url.invalid"

var (
	rawHostParameterPattern       = regexp.MustCompile(`^\{([A-Za-z_][A-Za-z0-9_]*)\}$`)
	templatedHostParameterPattern = regexp.MustCompile(`^\{\{(?:var|param)\.([a-z0-9_]+)\}\}$`)
)

// resolveTemplatedBaseURL replaces a parameterized base-URL host (e.g.
// "http://{server_url}/mi/") with the concrete value of that parameter. The
// engine uses config.base_url verbatim — it is never template-expanded
// (pkg/engine/config.go passes it straight through, and the HTTP protocol
// prepends it raw in ResolveURL) — so a leading "{" makes url.Parse fail at
// replay/debug time ("invalid character \"{\" in host name"). LoadRunner
// commonly parameterizes the recorded host; this resolves it from the imported
// parameters. When no importable value exists, the host is replaced with a
// parseable placeholder and a warning is emitted so the operator sets it.
func resolveTemplatedBaseURL(result *ConvertResult) {
	if result == nil || result.Script == nil {
		return
	}
	base := strings.TrimSpace(result.Script.Config.BaseURL)
	if base == "" || baseURLIsParseable(base) {
		return
	}
	scheme, host, rest, ok := splitSchemeHostRest(base)
	if !ok {
		return
	}
	name, ok := baseURLHostParameterName(host)
	if !ok {
		// Host isn't a single parameter reference (e.g. embedded mid-host); leave
		// it for manual review rather than guessing.
		return
	}
	if value, ok := concreteParameterValueByCanonicalName(result.Script, canonicalSourceParameterName(name)); ok {
		if rebuilt, ok := rebuildConcreteBaseURL(scheme, value, rest); ok {
			result.Script.Config.BaseURL = rebuilt
			return
		}
	}
	placeholder := scheme + "://" + unresolvedBaseURLHost + rest
	if !baseURLIsParseable(placeholder) {
		placeholder = scheme + "://" + unresolvedBaseURLHost
	}
	result.Script.Config.BaseURL = placeholder
	result.Warnings = append(result.Warnings, fmt.Sprintf(
		"base URL host was parameterized as {%s} and no value was found in the import; set config.base_url to the target host before running (placeholder %q applied)",
		name, placeholder))
}

// baseURLIsParseable reports whether s is a concrete URL the engine can use
// directly: it parses, carries a host, and has no leftover template braces.
func baseURLIsParseable(s string) bool {
	if strings.ContainsAny(s, "{}") {
		return false
	}
	parsed, err := url.Parse(s)
	return err == nil && parsed.Host != ""
}

// splitSchemeHostRest splits "scheme://host/rest..." into its parts. rest keeps
// its leading slash (or is empty).
func splitSchemeHostRest(base string) (scheme, host, rest string, ok bool) {
	schemeIndex := strings.Index(base, "://")
	if schemeIndex <= 0 {
		return "", "", "", false
	}
	scheme = base[:schemeIndex]
	remainder := base[schemeIndex+len("://"):]
	if slash := strings.Index(remainder, "/"); slash >= 0 {
		return scheme, remainder[:slash], remainder[slash:], true
	}
	return scheme, remainder, "", true
}

// baseURLHostParameterName returns the parameter name when the whole host is a
// single source ({name}) or template ({{var|param.name}}) reference.
func baseURLHostParameterName(host string) (string, bool) {
	if m := rawHostParameterPattern.FindStringSubmatch(host); m != nil {
		return m[1], true
	}
	if m := templatedHostParameterPattern.FindStringSubmatch(host); m != nil {
		return m[1], true
	}
	return "", false
}

// rebuildConcreteBaseURL forms a concrete base URL from a resolved parameter
// value, preserving the recorded path. The value may be a bare host, host:port,
// or a full URL; a full URL contributes its scheme and byte-exact authority
// (including userinfo), while the recording's path wins.
func rebuildConcreteBaseURL(scheme, value, rest string) (string, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", false
	}
	if isAbsoluteHTTPURL(value) {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" {
			return "", false
		}
		authority, ok := rawAbsoluteURLAuthority(value)
		if !ok {
			return "", false
		}
		candidate := parsed.Scheme + "://" + authority + rest
		if baseURLIsParseable(candidate) {
			return candidate, true
		}
		return "", false
	}
	hostPart := value
	if slash := strings.Index(hostPart, "/"); slash >= 0 {
		hostPart = hostPart[:slash]
	}
	if hostPart == "" {
		return "", false
	}
	candidate := scheme + "://" + hostPart + rest
	if baseURLIsParseable(candidate) {
		return candidate, true
	}
	return "", false
}

func rawAbsoluteURLAuthority(value string) (string, bool) {
	schemeEnd := strings.Index(value, "://")
	if schemeEnd <= 0 {
		return "", false
	}
	start := schemeEnd + len("://")
	end := len(value)
	if offset := strings.IndexAny(value[start:], "/?#"); offset >= 0 {
		end = start + offset
	}
	if start == end {
		return "", false
	}
	return value[start:end], true
}

// concreteParameterValueByCanonicalName resolves a parameter value by matching
// canonical names (the host token's casing/spelling may differ from the stored
// parameter's), reusing firstConcreteParameterValue for the value lookup.
func concreteParameterValueByCanonicalName(script *dsl.Script, canonicalName string) (string, bool) {
	if script == nil || canonicalName == "" {
		return "", false
	}
	for _, param := range script.Parameters {
		if canonicalSourceParameterName(param.Name) != canonicalName {
			continue
		}
		if value, ok := firstConcreteParameterValue(script, param.Name); ok {
			return value, true
		}
	}
	return "", false
}

// detectBaseURL finds the common base URL from all web_ function calls.
func detectBaseURL(functions []ParsedFunction) string {
	var urls []string
	for _, fn := range functions {
		if fn.URL != "" && strings.HasPrefix(fn.URL, "http") {
			urls = append(urls, fn.URL)
		}
	}

	if len(urls) == 0 {
		return "https://example.com"
	}

	// Parse first URL to get base
	parsed, err := url.Parse(urls[0])
	if err != nil {
		return detectTemplatedBaseURL(urls[0])
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return detectTemplatedBaseURL(urls[0])
	}

	return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
}

func detectTemplatedBaseURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if schemeIndex := strings.Index(rawURL, "://"); schemeIndex >= 0 {
		hostStart := schemeIndex + len("://")
		hostEnd := len(rawURL)
		if slashIndex := strings.Index(rawURL[hostStart:], "/"); slashIndex >= 0 {
			hostEnd = hostStart + slashIndex
		}
		if hostEnd > hostStart {
			return rawURL[:hostEnd]
		}
	}
	return rawURL
}

// makeRelative strips the base URL from an absolute URL to create a relative path.
func makeRelative(rawURL, baseURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	baseURL = strings.TrimSpace(baseURL)
	if baseURL != "" {
		if strings.HasPrefix(rawURL, baseURL) {
			rel := strings.TrimPrefix(rawURL, baseURL)
			if rel == "" {
				rel = "/"
			}
			return rel
		}
		if rel, ok := sameOriginRelativeTarget(rawURL, baseURL); ok {
			return rel
		}
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return rawURL
	}
	return rawURL
}

func sameOriginRelativeTarget(rawURL, baseURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", false
	}
	if !strings.EqualFold(parsed.Scheme, base.Scheme) || !strings.EqualFold(parsed.Host, base.Host) {
		return "", false
	}
	// Userinfo is part of the executable URL semantics. Relativizing such a URL
	// would silently discard it, leaving credentials only in provenance
	// metadata rather than on the request that is actually replayed.
	if parsed.User != nil {
		return "", false
	}
	path := parsed.EscapedPath()
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	if path == "" {
		path = "/"
	}
	return path, true
}

func collectRecordedResourceURLs(functions []ParsedFunction) []string {
	urls := make([]string, 0)
	seen := make(map[string]struct{})
	for _, fn := range functions {
		if !isRequestFunction(fn.Name) || firstNonEmptyArg(fn, "Resource") != "1" {
			continue
		}
		rawURL := strings.TrimSpace(fn.URL)
		if rawURL == "" {
			rawURL = strings.TrimSpace(fn.Action)
		}
		if rawURL == "" {
			continue
		}
		if _, ok := seen[rawURL]; ok {
			continue
		}
		seen[rawURL] = struct{}{}
		urls = append(urls, rawURL)
	}
	return urls
}

// sanitizeName converts a source step name to a valid DSL action name.
func sanitizeName(name string) string {
	if name == "" {
		return "unnamed_action"
	}

	// Replace spaces and special chars with underscores
	replacer := strings.NewReplacer(
		" ", "_",
		"-", "_",
		".", "_",
		"/", "_",
		"\\", "_",
		"(", "",
		")", "",
		"[", "",
		"]", "",
		"'", "",
		"\"", "",
		",", "",
		":", "_",
	)

	result := replacer.Replace(name)

	// Remove consecutive underscores
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}

	// Trim leading/trailing underscores
	result = strings.Trim(result, "_")

	if result == "" {
		return "unnamed_action"
	}

	return result
}

// convertParamReferences converts source {ParamName} references to APEX Load {{var.paramname}}.
// Archive-level post-processing rewrites known .prm-backed parameters to {{param.name}},
// leaving web_reg_save_param and other dynamic values as {{var.name}}.
func convertParamReferences(s string) string {
	// Match {ParamName} but not {{already.converted}}
	re := regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	return re.ReplaceAllStringFunc(s, func(token string) string {
		match := re.FindStringSubmatch(token)
		if len(match) != 2 {
			return token
		}
		name := canonicalSourceParameterName(match[1])
		if name == "" {
			return token
		}
		return "{{var." + name + "}}"
	})
}

// computeThinkTimeStats calculates mean and standard deviation for think times.
func computeThinkTimeStats(times []float64) (mean, std float64) {
	if len(times) == 0 {
		return 0, 0
	}

	sum := 0.0
	for _, t := range times {
		sum += t
	}
	mean = sum / float64(len(times))

	if len(times) == 1 {
		return mean, 0
	}

	variance := 0.0
	for _, t := range times {
		diff := t - mean
		variance += diff * diff
	}
	variance /= float64(len(times) - 1)
	std = math.Sqrt(variance)

	return mean, std
}
