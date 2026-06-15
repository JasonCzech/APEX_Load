package enterprisearchive

// baseurl.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strings"
)

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
	path := parsed.Path
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
