// Package recordingprofile holds a project's saved recording options: what
// the proxy and browser recorders capture and how their HTTP scripts are
// generated, grouped the way VuGen groups its recording options. The proxy
// recorder, the browser recorder and HAR imports apply the same profile.
package recordingprofile

import (
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// Limits on a profile.
const (
	MaxProfilesPerProject  = 32
	MaxListEntries         = 128
	MaxUserRules           = 64
	maxBackgroundPattern   = 512
	DefaultThinkTimeMs     = 500
	DefaultMinValueLength  = 4
	DefaultMaxValueLength  = 400
	maxThinkTimeThreshold  = 3600000
	maxCorrelationValueLen = 4096
)

// Profile is one saved set of recording options.
type Profile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Recording level and resources (VuGen General > Recording, Advanced
	// HTML): "url" or "html" for the browser recorder's HTTP script, and
	// "runtime", "in_step" or "separate" for non-HTML resources.
	HTTPScriptLevel string `json:"http_script_level,omitempty"`
	ResourceMode    string `json:"resource_mode,omitempty"`

	// Headers (HTTP Properties > Advanced > Headers): RecordHeaders are kept
	// even when the recorder drops them by default (User-Agent, Accept, ...);
	// DropHeaders are never recorded.
	RecordHeaders []string `json:"record_headers,omitempty"`
	DropHeaders   []string `json:"drop_headers,omitempty"`

	// Filters (Recording filters, content types): requests to excluded hosts
	// or whose responses have an excluded content type are not recorded as
	// steps; IncludeHosts, when set, records only those hosts.
	IncludeHosts        []string `json:"include_hosts,omitempty"`
	ExcludeHosts        []string `json:"exclude_hosts,omitempty"`
	ExcludeContentTypes []string `json:"exclude_content_types,omitempty"`

	// ThinkTimeThresholdMs is the smallest recorded pause kept as think time
	// (VuGen: think time threshold). Zero means the default, 500 ms.
	ThinkTimeThresholdMs int `json:"think_time_threshold_ms,omitempty"`

	// Code generation: page title checks (HTTP Properties > Advanced) and the
	// async scan. Nil means on.
	PageTitleChecks *bool `json:"page_title_checks,omitempty"`
	AsyncScan       *bool `json:"async_scan,omitempty"`

	// BackgroundRequests are the request paths an app calls without waiting
	// for them (client logging, analytics), which the generated script sends
	// as background requests, outside transaction times. A pattern matches a
	// request whose path holds its segments in order ("nebulaLogger",
	// "connect/proxy/app-analytics-logging"), ignoring case. Nil means
	// DefaultBackgroundRequests; an empty list keeps every request in line.
	BackgroundRequests *[]string `json:"background_requests,omitempty"`

	// Capture: the per-request body capture limit and whether recorded
	// Cookie headers are kept (the cookie jar sends them otherwise).
	BodyCaptureLimitBytes int   `json:"body_capture_limit_bytes,omitempty"`
	CaptureCookies        *bool `json:"capture_cookies,omitempty"`

	Correlation CorrelationSettings `json:"correlation,omitempty"`
}

// CorrelationSettings mirror VuGen's Correlations > Configuration.
type CorrelationSettings struct {
	// Disabled turns the automatic correlation scan off.
	Disabled bool `json:"disabled,omitempty"`
	// DisabledRules lists rule IDs or rule families (json, html, xml, header,
	// cookie, form, messaging) the scan skips.
	DisabledRules []string `json:"disabled_rules,omitempty"`
	// UserRules are the project's own boundary or regex rules.
	UserRules []UserRule `json:"user_rules,omitempty"`
	// ExcludedValues are values never correlated (exact match).
	ExcludedValues []string `json:"excluded_values,omitempty"`
	// ExcludedContentTypes are response content types the scan skips.
	ExcludedContentTypes []string `json:"excluded_content_types,omitempty"`
	// MinValueLength and MaxValueLength bound correlated values (VuGen
	// defaults 4 and 400; zero means the default).
	MinValueLength int `json:"min_value_length,omitempty"`
	MaxValueLength int `json:"max_value_length,omitempty"`
	// CaseInsensitive matches user-rule boundaries without regard to case.
	CaseInsensitive bool `json:"case_insensitive,omitempty"`
	// PreferRegex makes boundary user rules generate regex extractors.
	PreferRegex bool `json:"prefer_regex,omitempty"`
}

// UserRule is a project correlation rule: the value between Left and Right
// (boundary) or the first group of Regex, in responses from the listed hosts
// and content types (all when empty).
type UserRule struct {
	Name         string   `json:"name"`
	Type         string   `json:"type"`
	Left         string   `json:"left,omitempty"`
	Right        string   `json:"right,omitempty"`
	Regex        string   `json:"regex,omitempty"`
	Hosts        []string `json:"hosts,omitempty"`
	ContentTypes []string `json:"content_types,omitempty"`
	Disabled     bool     `json:"disabled,omitempty"`
}

var (
	profileIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	ruleNamePattern  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
)

// Validate checks a project's profiles.
func Validate(profiles []Profile) error {
	if len(profiles) > MaxProfilesPerProject {
		return fmt.Errorf("a project keeps at most %d recording profiles", MaxProfilesPerProject)
	}
	seen := map[string]bool{}
	for index, profile := range profiles {
		label := fmt.Sprintf("recording_profiles[%d]", index)
		if !profileIDPattern.MatchString(profile.ID) {
			return fmt.Errorf("%s.id must be 1-64 letters, digits, '.', '_' or '-'", label)
		}
		if seen[strings.ToLower(profile.ID)] {
			return fmt.Errorf("%s.id %q is duplicated", label, profile.ID)
		}
		seen[strings.ToLower(profile.ID)] = true
		if err := profile.validate(label); err != nil {
			return err
		}
	}
	return nil
}

func (p Profile) validate(label string) error {
	if strings.TrimSpace(p.Name) == "" || len(p.Name) > 128 {
		return fmt.Errorf("%s.name must be 1-128 characters", label)
	}
	switch p.HTTPScriptLevel {
	case "", "url", "html":
	default:
		return fmt.Errorf("%s.http_script_level must be url or html", label)
	}
	switch p.ResourceMode {
	case "", "runtime", "in_step", "separate":
	default:
		return fmt.Errorf("%s.resource_mode must be runtime, in_step or separate", label)
	}
	for name, list := range map[string][]string{
		"record_headers": p.RecordHeaders, "drop_headers": p.DropHeaders, "include_hosts": p.IncludeHosts,
		"exclude_hosts": p.ExcludeHosts, "exclude_content_types": p.ExcludeContentTypes,
		"correlation.disabled_rules": p.Correlation.DisabledRules, "correlation.excluded_values": p.Correlation.ExcludedValues,
		"correlation.excluded_content_types": p.Correlation.ExcludedContentTypes,
	} {
		if len(list) > MaxListEntries {
			return fmt.Errorf("%s.%s holds at most %d entries", label, name, MaxListEntries)
		}
		for _, entry := range list {
			if strings.TrimSpace(entry) == "" || len(entry) > maxCorrelationValueLen {
				return fmt.Errorf("%s.%s entries must be 1-%d characters", label, name, maxCorrelationValueLen)
			}
		}
	}
	if p.BackgroundRequests != nil {
		if err := ValidateBackgroundRequests(*p.BackgroundRequests); err != nil {
			return fmt.Errorf("%s.%w", label, err)
		}
	}
	if p.ThinkTimeThresholdMs < 0 || p.ThinkTimeThresholdMs > maxThinkTimeThreshold {
		return fmt.Errorf("%s.think_time_threshold_ms must be 0..%d", label, maxThinkTimeThreshold)
	}
	if p.BodyCaptureLimitBytes < 0 {
		return fmt.Errorf("%s.body_capture_limit_bytes must be zero or greater", label)
	}
	c := p.Correlation
	if c.MinValueLength < 0 || c.MaxValueLength < 0 || (c.MaxValueLength > 0 && c.MinValueLength > c.MaxValueLength) || c.MaxValueLength > maxCorrelationValueLen {
		return fmt.Errorf("%s.correlation value lengths must satisfy 0 <= min <= max <= %d", label, maxCorrelationValueLen)
	}
	if len(c.UserRules) > MaxUserRules {
		return fmt.Errorf("%s.correlation.user_rules holds at most %d rules", label, MaxUserRules)
	}
	names := map[string]bool{}
	for index, rule := range c.UserRules {
		ruleLabel := fmt.Sprintf("%s.correlation.user_rules[%d]", label, index)
		if !ruleNamePattern.MatchString(rule.Name) {
			return fmt.Errorf("%s.name must be a parameter name (letters, digits, '_')", ruleLabel)
		}
		if names[strings.ToLower(rule.Name)] {
			return fmt.Errorf("%s.name %q is duplicated", ruleLabel, rule.Name)
		}
		names[strings.ToLower(rule.Name)] = true
		if len(rule.Left) > maxCorrelationValueLen || len(rule.Right) > maxCorrelationValueLen || len(rule.Regex) > maxCorrelationValueLen {
			return fmt.Errorf("%s boundaries and regex must be at most %d characters", ruleLabel, maxCorrelationValueLen)
		}
		for field, list := range map[string][]string{"hosts": rule.Hosts, "content_types": rule.ContentTypes} {
			if len(list) > MaxListEntries {
				return fmt.Errorf("%s.%s holds at most %d entries", ruleLabel, field, MaxListEntries)
			}
			for _, entry := range list {
				if strings.TrimSpace(entry) == "" || len(entry) > maxCorrelationValueLen {
					return fmt.Errorf("%s.%s entries must be 1-%d characters", ruleLabel, field, maxCorrelationValueLen)
				}
			}
		}
		switch rule.Type {
		case "boundary":
			if rule.Left == "" || rule.Right == "" {
				return fmt.Errorf("%s needs a left and a right boundary", ruleLabel)
			}
		case "regex":
			compiled, err := regexp.Compile(rule.Regex)
			if err != nil || compiled.NumSubexp() < 1 {
				return fmt.Errorf("%s.regex must compile and capture the value in a group", ruleLabel)
			}
		default:
			return fmt.Errorf("%s.type must be boundary or regex", ruleLabel)
		}
	}
	return nil
}

// ValidateBackgroundRequests checks a list of background request patterns.
func ValidateBackgroundRequests(patterns []string) error {
	if len(patterns) > MaxListEntries {
		return fmt.Errorf("background_requests holds at most %d entries", MaxListEntries)
	}
	for _, pattern := range patterns {
		if len(backgroundSegments(pattern)) == 0 || len(pattern) > maxBackgroundPattern || strings.ContainsAny(pattern, "?#*:") ||
			strings.ContainsFunc(pattern, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) {
			return fmt.Errorf("background_requests entries must be URL path segments such as nebulaLogger or connect/proxy/app-analytics-logging, at most %d characters", maxBackgroundPattern)
		}
	}
	return nil
}

// DefaultBackgroundRequests are the background request patterns of a
// profile that names none: the Salesforce Mobile SDK's analytics upload, and
// the logging (Nebula Logger) and analytics-event calls Mobile SDK apps make
// through Apex REST.
var DefaultBackgroundRequests = []string{"connect/proxy/app-analytics-logging", "nebulaLogger", "analyticsEvent"}

// BackgroundRequestPatterns returns the patterns of the requests a
// conversion sends in the background: the profile's, or the defaults when
// there is no profile or it names none. The caller owns the list.
func (p *Profile) BackgroundRequestPatterns() []string {
	if p == nil || p.BackgroundRequests == nil {
		return slices.Clone(DefaultBackgroundRequests)
	}
	return slices.Clone(*p.BackgroundRequests)
}

// MatchBackgroundRequest returns the first pattern whose segments appear, in
// order and whole, in the URL's path, ignoring case; "" when none does. The
// URL may be absolute, relative or templated; its query is ignored.
func MatchBackgroundRequest(patterns []string, rawURL string) string {
	if len(patterns) == 0 {
		return ""
	}
	path := rawURL
	if _, rest, ok := strings.Cut(path, "://"); ok {
		path = "/"
		if slash := strings.Index(rest, "/"); slash >= 0 {
			path = rest[slash:]
		}
	}
	if cut := strings.IndexAny(path, "?#"); cut >= 0 {
		path = path[:cut]
	}
	segments := backgroundSegments(path)
	for _, pattern := range patterns {
		want := backgroundSegments(pattern)
		if len(want) == 0 || len(want) > len(segments) {
			continue
		}
		for start := 0; start+len(want) <= len(segments); start++ {
			matched := true
			for offset, segment := range want {
				if !strings.EqualFold(segments[start+offset], segment) {
					matched = false
					break
				}
			}
			if matched {
				return pattern
			}
		}
	}
	return ""
}

func backgroundSegments(path string) []string {
	var segments []string
	for _, segment := range strings.Split(strings.TrimSpace(path), "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	return segments
}

// Find returns the profile with the given ID.
func Find(profiles []Profile, id string) (Profile, bool) {
	for _, profile := range profiles {
		if strings.EqualFold(profile.ID, strings.TrimSpace(id)) {
			return profile, true
		}
	}
	return Profile{}, false
}

// ThinkTimeThreshold is the smallest recorded pause kept as think time.
func (p Profile) ThinkTimeThreshold() int {
	if p.ThinkTimeThresholdMs > 0 {
		return p.ThinkTimeThresholdMs
	}
	return DefaultThinkTimeMs
}

// HeaderDecision returns the profile's decision for a recorded header:
// keep is true for RecordHeaders and false for DropHeaders; decided is
// false when the profile leaves the header to the recorder's default.
func (p Profile) HeaderDecision(name string) (keep, decided bool) {
	for _, header := range p.DropHeaders {
		if strings.EqualFold(strings.TrimSpace(header), strings.TrimSpace(name)) {
			return false, true
		}
	}
	for _, header := range p.RecordHeaders {
		if strings.EqualFold(strings.TrimSpace(header), strings.TrimSpace(name)) {
			return true, true
		}
	}
	return false, false
}

// RecordsHost reports whether a request to host is recorded.
func (p Profile) RecordsHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if hostname, _, err := net.SplitHostPort(host); err == nil {
		host = hostname
	}
	host = strings.Trim(host, "[]")
	for _, excluded := range p.ExcludeHosts {
		if hostMatches(host, excluded) {
			return false
		}
	}
	if len(p.IncludeHosts) == 0 {
		return true
	}
	for _, included := range p.IncludeHosts {
		if hostMatches(host, included) {
			return true
		}
	}
	return false
}

// RecordsContentType reports whether a response of this content type is
// recorded as a step.
func (p Profile) RecordsContentType(contentType string) bool {
	return !contentTypeListed(contentType, p.ExcludeContentTypes)
}

// hostMatches matches a host against "host" or "*.domain".
func hostMatches(host, pattern string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
		return host == suffix || strings.HasSuffix(host, "."+suffix)
	}
	return host == pattern
}

// contentTypeListed matches a Content-Type against media types ("image/png")
// or wildcards ("image/*").
func contentTypeListed(contentType string, list []string) bool {
	media := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if media == "" {
		return false
	}
	for _, entry := range list {
		entry = strings.ToLower(strings.TrimSpace(entry))
		if prefix, ok := strings.CutSuffix(entry, "/*"); ok {
			if strings.HasPrefix(media, prefix+"/") {
				return true
			}
			continue
		}
		if media == entry {
			return true
		}
	}
	return false
}
