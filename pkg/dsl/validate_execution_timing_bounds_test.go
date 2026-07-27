package dsl

import (
	"fmt"
	"math"
	"strings"
	"testing"
)

func TestExecutionTimingValidatorsBoundPacingThinkTimeAndRetries(t *testing.T) {
	tooLarge := MaxConfiguredProtocolTimeoutMS + 1
	script := &Script{
		Config: ScriptConfig{
			Pacing: PacingConfig{
				Type:          "random_delay",
				MinDelayMs:    tooLarge,
				MaxDelayMs:    -1,
				MinIntervalMs: 10,
				MaxIntervalMs: 1,
			},
			ThinkTime: ThinkTimeConfig{Type: "fixed", MeanMs: math.Inf(1)},
			ErrorHandling: ErrorHandlingConfig{
				MaxRetries:   MaxErrorRetries + 1,
				RetryDelayMs: tooLarge,
				RetryBackoff: "unbounded",
			},
		},
		Actions: []Action{{
			Name: "paced", Type: "rendezvous",
			Params:    map[string]any{"timeout_ms": tooLarge},
			ThinkTime: ThinkTimeConfig{Type: "fixed", Multiplier: MaxThinkTimeMultiplier + 1},
		}},
	}
	issues := validateExecutionResourceIssues(script)
	assertIssue(t, issues, fmt.Sprintf("config.pacing.min_delay_ms must be between 0 and %d", MaxConfiguredProtocolTimeoutMS))
	assertIssue(t, issues, fmt.Sprintf("config.pacing.max_delay_ms must be between 0 and %d", MaxConfiguredProtocolTimeoutMS))
	assertIssue(t, issues, "config.pacing.min_interval_ms must not exceed max_interval_ms")
	assertIssue(t, issues, fmt.Sprintf("config.think_time.mean_ms must be finite and between 0 and %d", MaxConfiguredProtocolTimeoutMS))
	assertIssue(t, issues, fmt.Sprintf("config.error_handling.max_retries must be between 0 and %d", MaxErrorRetries))
	assertIssue(t, issues, fmt.Sprintf("config.error_handling.retry_delay_ms must be between 0 and %d", MaxConfiguredProtocolTimeoutMS))
	assertIssue(t, issues, "config.error_handling.retry_backoff must be fixed or exponential")
	assertIssue(t, issues, fmt.Sprintf("actions[0].think_time.multiplier must be omitted (defaults/inherits 1) or finite, greater than 0, and at most %d", MaxThinkTimeMultiplier))

	rendezvousIssues := validateRendezvousActionIssues(0, script.Actions[0])
	assertIssue(t, rendezvousIssues, fmt.Sprintf(`actions[0] "paced": rendezvous timeout_ms must not exceed %d`, MaxConfiguredProtocolTimeoutMS))
}

func TestRecordedThinkTimeIsValid(t *testing.T) {
	script := &Script{Config: ScriptConfig{ThinkTime: ThinkTimeConfig{Type: "recorded", Multiplier: 1}}}
	for _, issue := range validateExecutionResourceIssues(script) {
		if strings.Contains(issue, "think_time.type") {
			t.Fatalf("recorded think time was rejected: %s", issue)
		}
	}
}

func TestZeroThinkTimeMultiplierRetainsOmittedValueCompatibility(t *testing.T) {
	script := &Script{
		Config:  ScriptConfig{ThinkTime: ThinkTimeConfig{Type: "recorded"}},
		Actions: []Action{{Name: "recorded", Type: "rendezvous", ThinkTime: ThinkTimeConfig{Type: "recorded"}}},
	}
	for _, issue := range validateExecutionResourceIssues(script) {
		if strings.Contains(issue, "think_time.multiplier") {
			t.Fatalf("zero-value multiplier should mean omitted/inherit, got: %s", issue)
		}
	}
}

func TestHTTPPublicValidationIncludesRuntimeAndSequenceChildTiming(t *testing.T) {
	script := &Script{
		Name: "HTTP timing", Protocol: "http",
		Config: ScriptConfig{
			BaseURL:   "https://example.test",
			ThinkTime: ThinkTimeConfig{Type: "recorded", Multiplier: MaxThinkTimeMultiplier + 1},
		},
		Actions: []Action{{
			Name: "sequence", Type: "http_sequence",
			Params: map[string]any{"requests": []map[string]any{{
				"name": "child", "type": "GET", "target": "/child",
				"think_time_after": "yes",
				"think_time":       map[string]any{"type": "fixed", "mean_ms": MaxConfiguredProtocolTimeoutMS + 1},
			}}},
		}},
	}
	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, fmt.Sprintf("config.think_time.multiplier must be omitted (defaults/inherits 1) or finite, greater than 0, and at most %d", MaxThinkTimeMultiplier))
	assertIssue(t, issues, `actions[0] "sequence".params.requests[0] "child".think_time_after must be a boolean`)
	assertIssue(t, issues, fmt.Sprintf(`actions[0] "sequence".params.requests[0] "child".think_time.mean_ms must be finite and between 0 and %d`, MaxConfiguredProtocolTimeoutMS))
}
