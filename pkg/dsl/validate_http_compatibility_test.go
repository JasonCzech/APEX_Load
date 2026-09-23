package dsl

import (
	"strings"
	"testing"
)

func TestValidateHTTPCompatibilityPolicies(t *testing.T) {
	for _, tc := range []struct {
		cfg        HTTPConfig
		errorField string
	}{
		{HTTPConfig{PageTimeoutMs: 3600000, IPVersion: "ipv4", CacheMaxUnusedIterations: 1000000, HTMLParseMode: "any", CacheExcludedMIMETypes: []string{"text/xml"}}, ""},
		{HTTPConfig{PageTimeoutMs: 3600001}, "page_timeout_ms"},
		{HTTPConfig{IPVersion: "2"}, "ip_version"},
		{HTTPConfig{CacheMaxUnusedIterations: -1}, "cache_max_unused_iterations"},
		{HTTPConfig{HTMLParseMode: "auto"}, "html_parse_mode"},
		{HTTPConfig{HTMLExcludedMIMETypes: []string{"text/*"}}, "html_excluded_mime_types"},
	} {
		s := &Script{Name: "compatibility", Protocol: "http", Config: ScriptConfig{BaseURL: "https://example.test", HTTP: tc.cfg}, Actions: []Action{{Name: "page", Type: "http_request", Method: "GET", Target: "/"}}}
		issues := ValidateScriptIssues(s)
		if tc.errorField == "" && len(issues) > 0 {
			t.Fatal(issues)
		}
		if tc.errorField != "" && !strings.Contains(strings.Join(issues, "\n"), tc.errorField) {
			t.Fatalf("missing %s: %v", tc.errorField, issues)
		}
	}
}
