package dsl

import (
	"strings"
	"testing"
)

func TestProtocolMixExpectationValidationAndDefaults(t *testing.T) {
	ratio := func(value float64) *float64 { return &value }
	script := func(expectation *ProtocolMixExpectation) *Script {
		return &Script{Name: "mix", Version: "1", Protocol: "http", Actions: []Action{{Name: "GET", Type: "GET", Target: "https://example.test"}},
			Config: ScriptConfig{Results: ResultsConfig{ProtocolMix: expectation}}}
	}
	for name, test := range map[string]struct {
		expectation *ProtocolMixExpectation
		want        string
	}{
		"absent":               {nil, ""},
		"bounds":               {&ProtocolMixExpectation{MaxFallbackRatio: ratio(0), MaxQUICFailureRatio: ratio(1), MinSamples: 1}, ""},
		"negative fallback":    {&ProtocolMixExpectation{MaxFallbackRatio: ratio(-0.1)}, "config.results.protocol_mix.max_fallback_ratio must be between 0 and 1"},
		"percent not fraction": {&ProtocolMixExpectation{MaxQUICFailureRatio: ratio(5)}, "config.results.protocol_mix.max_quic_failure_ratio must be between 0 and 1"},
		"negative samples":     {&ProtocolMixExpectation{MinSamples: -1}, "config.results.protocol_mix.min_samples must be between 0 and 1000000"},
	} {
		t.Run(name, func(t *testing.T) {
			issues := strings.Join(ValidateScriptIssues(script(test.expectation)), "\n")
			if test.want == "" {
				if strings.Contains(issues, "protocol_mix") {
					t.Fatalf("unexpected issues: %s", issues)
				}
				return
			}
			if !strings.Contains(issues, test.want) {
				t.Fatalf("issues = %q, want %q", issues, test.want)
			}
		})
	}

	var absent *ProtocolMixExpectation
	if fallback, quic, samples := absent.Effective(); fallback != DefaultProtocolMixMaxRatio || quic != DefaultProtocolMixMaxRatio || samples != DefaultProtocolMixMinSamples {
		t.Fatalf("defaults = %v %v %d", fallback, quic, samples)
	}
	// An explicit zero is a real expectation; zero samples keep the default.
	if fallback, quic, samples := (&ProtocolMixExpectation{MaxFallbackRatio: ratio(0)}).Effective(); fallback != 0 || quic != DefaultProtocolMixMaxRatio || samples != DefaultProtocolMixMinSamples {
		t.Fatalf("explicit zero = %v %v %d", fallback, quic, samples)
	}
}
