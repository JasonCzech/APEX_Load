package dsl

import (
	"fmt"
	"math"
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
	assertIssue(t, issues, fmt.Sprintf("actions[0].think_time.multiplier must be finite and between 0 and %d", MaxThinkTimeMultiplier))

	rendezvousIssues := validateRendezvousActionIssues(0, script.Actions[0])
	assertIssue(t, rendezvousIssues, fmt.Sprintf(`actions[0] "paced": rendezvous timeout_ms must not exceed %d`, MaxConfiguredProtocolTimeoutMS))
}
