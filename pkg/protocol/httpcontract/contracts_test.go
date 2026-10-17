package httpcontract

import (
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestDefaultHeadersPrecedenceAndIsolation(t *testing.T) {
	authored := map[string]string{"user-agent": "authored", "ACCEPT-LANGUAGE": "fr", "X-Empty": ""}
	got := DefaultHeaders(map[string]any{"default_headers": authored, "user_agent": "typed", "accept_language": "en", "accept_encoding": "gzip"})
	want := map[string]string{"user-agent": "authored", "ACCEPT-LANGUAGE": "fr", "X-Empty": "", "Accept-Encoding": "gzip"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("headers = %#v, want %#v", got, want)
	}
	got["user-agent"] = "changed"
	if authored["user-agent"] != "authored" {
		t.Fatal("default headers alias authored map")
	}
	if got := DefaultHeaders(nil); len(got) != 0 || got == nil {
		t.Fatalf("empty headers = %#v", got)
	}
	typed := TypedDefaultHeaders(map[string]any{"user_agent": "typed", "accept_encoding": 42})
	if !reflect.DeepEqual(typed, map[string]string{"User-Agent": "typed"}) {
		t.Fatalf("typed = %#v", typed)
	}
	merged := MergeDefaultHeaders(typed, authored)
	if merged["user-agent"] != "authored" || merged["User-Agent"] != "" {
		t.Fatalf("merged = %#v", merged)
	}
}

func TestStructuralVersionPoliciesAreEnvironmentIndependent(t *testing.T) {
	want := []string{VersionHTTP11, VersionHTTP2, VersionHTTP3, VersionAutoH3}
	for _, enabled := range []string{"0", "1"} {
		t.Setenv("APEXLOAD_HTTP3", enabled)
		got := SupportedVersions()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("versions = %v", got)
		}
		got[0] = "mutated"
		if SupportedVersions()[0] != VersionHTTP11 {
			t.Fatal("versions alias shared storage")
		}
		for _, version := range want {
			if err := ValidateVersion(version); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !errors.Is(ValidateVersion("HTTP/1.0"), ErrHTTP10Unsupported) {
		t.Fatal("HTTP/1.0 sentinel changed")
	}
	if !errors.Is(ValidateVersion("4"), ErrUnsupportedVersion) {
		t.Fatal("unknown version sentinel changed")
	}
}

func TestAssertionValidationOrderAndCompilerParity(t *testing.T) {
	cases := []struct {
		assertion protocol.Assertion
		want      string
	}{
		{protocol.Assertion{Type: "unknown", Source: "body"}, "source applies only to text assertions"},
		{protocol.Assertion{Type: "text_matches", Source: "invalid"}, "value is required"},
		{protocol.Assertion{Type: "text_matches", Source: "invalid", Value: "["}, "source must be body, headers or all"},
		{protocol.Assertion{Type: "body_regex_capture_equals", Value: "["}, "expected string"},
		{protocol.Assertion{Type: "body_regex_capture_equals", Value: "[", Expected: ""}, "invalid pattern"},
		{protocol.Assertion{Type: "status_code", Expected: "{{var.status}}"}, ""},
	}
	for _, tc := range cases {
		var called bool
		err := ValidateAssertionDefinitionWithRegexCompiler(tc.assertion, func(pattern string) (*regexp.Regexp, error) { called = true; return regexp.Compile(pattern) })
		direct := ValidateAssertionDefinition(tc.assertion)
		if (err == nil) != (tc.want == "") || err != nil && !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%+v: %v, want %q", tc.assertion, err, tc.want)
		}
		if (direct == nil) != (err == nil) || err != nil && err.Error() != direct.Error() {
			t.Fatalf("cached syntax differs: %v / %v", err, direct)
		}
		if called != (tc.want == "invalid pattern") {
			t.Fatalf("compiler called before definition validation: %+v", tc.assertion)
		}
	}
	if err := ValidateAssertionDefinitionWithRegexCompiler(protocol.Assertion{Type: "text_matches", Value: "ok"}, nil); err != nil {
		t.Fatal(err)
	}
	first := []protocol.Assertion{{Type: "status_code_range"}, {Type: "unknown"}}
	if err := ValidateAssertions(first); err == nil || !strings.Contains(err.Error(), "assertions[0]: status_code_range") {
		t.Fatalf("first error = %v", err)
	}
	if err := ValidateAssertions(make([]protocol.Assertion, MaxActionAssertions+1)); err == nil || !strings.Contains(err.Error(), "must not exceed") {
		t.Fatalf("count error = %v", err)
	}
}

func TestQUICValidationOrder(t *testing.T) {
	negative := -1
	got := ValidateQUICSettings(-1, -1, -1, "invalid", &negative)
	want := []string{"handshake_timeout_ms", "max_idle_timeout_ms", "keep_alive_ms", "alt_svc_cache", "broken_ttl_ms"}
	if len(got) != len(want) {
		t.Fatalf("issues = %v", got)
	}
	for i, field := range want {
		if !strings.Contains(got[i], "config.http.quic."+field) {
			t.Fatalf("issues[%d] = %q, want %s", i, got[i], field)
		}
	}
	if issues := ValidateQUICSettings(0, 0, 0, "", nil); len(issues) != 0 {
		t.Fatal(issues)
	}
	if got := QUICSettingsConfig(0, 0, 0, "", nil); len(got) != 0 {
		t.Fatalf("unset settings = %#v", got)
	}
}
