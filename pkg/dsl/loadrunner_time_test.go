package dsl

import (
	"strings"
	"testing"
	"time"
)

func TestLoadRunnerTimeKeepsFractionAndZoneLiterals(t *testing.T) {
	now := time.Date(2026, 10, 1, 8, 4, 5, 123456789, time.FixedZone("EDT", -4*3600))
	for _, tc := range []struct{ pattern, want string }{
		{"%Y-%m-%dT%H:%M:%S.000Z", "2026-10-01T08:04:05.000Z"},
		{"literal 2006 %% %Y/%m/%d", "literal 2006 % 2026/10/01"},
		{"%a %A %b %B %y %I %p %z %Z", "Thu Thursday Oct October 26 08 AM -0400 EDT"},
	} {
		got, err := FormatLoadRunnerTime(now, tc.pattern)
		if err != nil || got != tc.want {
			t.Fatalf("%q => %q, %v; want %q", tc.pattern, got, err, tc.want)
		}
	}
	for _, pattern := range []string{"", "%", "%Q", "%c", strings.Repeat("x", 257)} {
		if _, err := FormatLoadRunnerTime(now, pattern); err == nil {
			t.Fatalf("unsupported pattern accepted: %q", pattern)
		}
	}
}

func TestLoadRunnerTimeDialectValidatesParameterTypeAndPattern(t *testing.T) {
	for _, tc := range []struct {
		kind, pattern string
		valid         bool
	}{
		{"time", "%Y-%m-%dT%H:%M:%S.000Z", true},
		{"date_time", "%H:%M:%S", true},
		{"counter", "%H", false},
		{"time", "%Q", false},
	} {
		s := &Script{Parameters: []Parameter{{Name: "clock", Type: tc.kind, Format: tc.pattern, FormatDialect: "loadrunner_strftime"}}}
		issues := validateJMeterCompatibility(s, false)
		if (len(issues) == 0) != tc.valid {
			t.Fatalf("%+v issues=%v", tc, issues)
		}
	}
}
