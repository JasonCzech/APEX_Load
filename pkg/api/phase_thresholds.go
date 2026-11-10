package api

import (
	"fmt"
	"math"
	"strings"

	"github.com/jasonczech/apexload/pkg/engine"
)

// runStopRuleSustainedPhaseLatency names the auto-stop of a phase-aware
// latency threshold.
const runStopRuleSustainedPhaseLatency = "sustained_phase_latency"

// maxPhaseLatencyThresholds bounds the phase-aware threshold rules, and with
// them the distinct threshold_alert metrics one evaluation can raise.
const maxPhaseLatencyThresholds = 20

// PhaseLatencyThreshold is a phase-aware latency threshold: a breach is the
// fleet's percentile of one network phase above MaxMs in a live metrics
// sample, e.g. connect P95 above 500 ms. Like max_p99_latency_ms it raises a
// live alert (and notification webhooks), and it stops the run only when
// StopConsecutiveSamples is set and that many consecutive samples breach.
//
// The percentile is over the business transactions that spent time in the
// phase (see engine.PhaseSketches). A sample whose phase percentiles are
// unknown (a generator that predates them) or that has no sample in the
// phase is no data: it never breaches, and it resets the stop streak.
type PhaseLatencyThreshold struct {
	// Phase is dns, connect, tls, server (TTFB minus setup) or download.
	Phase string `json:"phase"`
	// Percentile is 50, 90, 95 or 99.
	Percentile int     `json:"percentile"`
	MaxMs      float64 `json:"max_ms"`
	// StopConsecutiveSamples auto-stops the run after this many consecutive
	// breaching samples (one per metrics evaluation, as for the P99 stop);
	// nil or 0 keeps the rule alert-only.
	StopConsecutiveSamples *int `json:"stop_consecutive_samples,omitempty"`
}

func validPhaseLatencyPercentile(percentile int) bool {
	switch percentile {
	case 50, 90, 95, 99:
		return true
	}
	return false
}

// validatePhaseLatencyThresholds checks the rules a settings update stores.
func validatePhaseLatencyThresholds(rules []PhaseLatencyThreshold) error {
	if len(rules) > maxPhaseLatencyThresholds {
		return fmt.Errorf("thresholds.phase_latency allows at most %d rules", maxPhaseLatencyThresholds)
	}
	seen := make(map[string]bool, len(rules))
	for index, rule := range rules {
		path := fmt.Sprintf("thresholds.phase_latency[%d]", index)
		if !engine.ValidNetworkPhase(rule.Phase) {
			return fmt.Errorf("%s.phase must be one of %s", path, strings.Join(engine.NetworkPhases, ", "))
		}
		if !validPhaseLatencyPercentile(rule.Percentile) {
			return fmt.Errorf("%s.percentile must be 50, 90, 95 or 99", path)
		}
		if math.IsNaN(rule.MaxMs) || math.IsInf(rule.MaxMs, 0) || rule.MaxMs <= 0 {
			return fmt.Errorf("%s.max_ms must be a finite number > 0", path)
		}
		if rule.StopConsecutiveSamples != nil && *rule.StopConsecutiveSamples < 0 {
			return fmt.Errorf("%s.stop_consecutive_samples must be >= 0", path)
		}
		key := phaseLatencyMetric(rule)
		if seen[key] {
			return fmt.Errorf("%s duplicates another rule for %s P%d", path, rule.Phase, rule.Percentile)
		}
		seen[key] = true
	}
	return nil
}

// usablePhaseLatencyThresholds returns the rules evaluation may act on. A
// stored blob is not re-validated on load (see validateSystemSettingsUpdateRules),
// so a malformed rule is skipped here rather than evaluated.
func usablePhaseLatencyThresholds(rules []PhaseLatencyThreshold) []PhaseLatencyThreshold {
	out := make([]PhaseLatencyThreshold, 0, len(rules))
	seen := make(map[string]bool, len(rules))
	for _, rule := range rules {
		if len(out) == maxPhaseLatencyThresholds {
			break
		}
		key := phaseLatencyMetric(rule)
		if seen[key] || validatePhaseLatencyThresholds([]PhaseLatencyThreshold{rule}) != nil {
			continue
		}
		seen[key] = true
		out = append(out, rule)
	}
	return out
}

func clonePhaseLatencyThresholds(rules []PhaseLatencyThreshold) []PhaseLatencyThreshold {
	if rules == nil {
		return nil
	}
	out := make([]PhaseLatencyThreshold, len(rules))
	for index, rule := range rules {
		out[index] = rule
		if rule.StopConsecutiveSamples != nil {
			v := *rule.StopConsecutiveSamples
			out[index].StopConsecutiveSamples = &v
		}
	}
	return out
}

// phaseLatencyMetric is the threshold_alert metric of a rule, e.g.
// "phase_connect_p95_ms".
func phaseLatencyMetric(rule PhaseLatencyThreshold) string {
	return fmt.Sprintf("phase_%s_p%d_ms", rule.Phase, rule.Percentile)
}

// livePhaseLatencyValue is the sample's fleet percentile for the rule's
// phase, and false when it is unknown or the phase had no sample.
func livePhaseLatencyValue(live *LiveMetrics, rule PhaseLatencyThreshold) (float64, bool) {
	if live == nil {
		return 0, false
	}
	value, ok := live.PhasePercentiles.Phase(rule.Phase).Value(rule.Percentile)
	if !ok || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}

// buildPhaseLatencyThresholdAlerts raises one alert per breached rule.
func buildPhaseLatencyThresholdAlerts(test *ManagedTest, live *LiveMetrics, rules []PhaseLatencyThreshold) []map[string]any {
	var alerts []map[string]any
	for _, rule := range usablePhaseLatencyThresholds(rules) {
		value, ok := livePhaseLatencyValue(live, rule)
		if !ok || value <= rule.MaxMs {
			continue
		}
		alerts = append(alerts, map[string]any{
			"type":       "threshold_alert",
			"test_id":    test.ID,
			"test_name":  test.Name,
			"timestamp":  live.Timestamp,
			"metric":     phaseLatencyMetric(rule),
			"phase":      rule.Phase,
			"percentile": rule.Percentile,
			"value":      value,
			"threshold":  rule.MaxMs,
		})
	}
	return alerts
}

// shouldStopForSustainedPhaseLatency advances each stop-enabled rule's
// streak and returns the first rule whose streak reached its sample count.
// A sample with no data for a rule (unknown percentiles, no sample in the
// phase) resets that rule's streak: it is never counted as a breach.
func (tr *TestRunner) shouldStopForSustainedPhaseLatency(test *ManagedTest, live *LiveMetrics, thresholds ThresholdSettings) (PhaseLatencyThreshold, float64, bool) {
	if tr == nil || test == nil || live == nil {
		return PhaseLatencyThreshold{}, 0, false
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	rules := usablePhaseLatencyThresholds(thresholds.PhaseLatency)
	if test.Status != "RUNNING" || len(rules) == 0 {
		test.phaseLatencyBreachConsecutive = nil
		return PhaseLatencyThreshold{}, 0, false
	}
	streaks := make(map[string]int, len(rules))
	var fired *PhaseLatencyThreshold
	var firedValue float64
	for index, rule := range rules {
		if rule.StopConsecutiveSamples == nil || *rule.StopConsecutiveSamples <= 0 {
			continue
		}
		key := phaseLatencyMetric(rule)
		value, ok := livePhaseLatencyValue(live, rule)
		if !ok || value <= rule.MaxMs {
			continue
		}
		streaks[key] = test.phaseLatencyBreachConsecutive[key] + 1
		if fired == nil && streaks[key] >= *rule.StopConsecutiveSamples {
			fired, firedValue = &rules[index], value
		}
	}
	// Only the breaching rules keep a streak: every other one starts over.
	test.phaseLatencyBreachConsecutive = streaks
	if fired == nil {
		return PhaseLatencyThreshold{}, 0, false
	}
	return *fired, firedValue, true
}

func phaseLatencyStopReason(rule PhaseLatencyThreshold, value float64) string {
	return fmt.Sprintf("sustained %s P%d latency threshold breached: %.1f ms above %.1f ms for %d consecutive samples", phaseLatencyLabel(rule.Phase), rule.Percentile, value, rule.MaxMs, *rule.StopConsecutiveSamples)
}

func phaseLatencyLabel(phase string) string {
	switch phase {
	case engine.PhaseDNS:
		return "DNS"
	case engine.PhaseTLS:
		return "TLS"
	}
	return phase
}
