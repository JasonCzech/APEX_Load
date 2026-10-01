package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/jasonczech/apexload/pkg/engine"
)

type transactionSLAAccumulator struct {
	lagSamples    int64
	offsetSamples int64
	maxLag        float64
	maxOffset     float64
	count         int64
	errors        int64
	bytesSent     int64
	bytesReceived int64
	durationTotal float64
	durationMin   float64
	durationMax   float64
	durations     *engine.TDigest
}

// applyTransactionSLARules evaluates exact-name rules only after the complete
// JSONL stream has been closed or merged. This keeps terminal CI evidence
// independent from windowed live snapshots.
func applyTransactionSLARules(root string, manifest *RunArchiveManifest) error {
	if manifest == nil {
		return nil
	}
	rules := transactionRulesFromManifest(manifest)
	if len(rules) == 0 {
		return nil
	}
	if err := ValidateTransactionSLARules(rules); err != nil {
		return fmt.Errorf("validate archived transaction SLA rules: %w", err)
	}

	file, _, err := openArchiveArtifactForRead(root, manifest.TransactionsFile)
	if err != nil {
		return fmt.Errorf("open transaction evidence for SLA evaluation: %w", err)
	}
	defer file.Close()

	requested := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		requested[rule.Transaction] = struct{}{}
	}
	stats := make(map[string]*transactionSLAAccumulator, len(requested))
	decoder := json.NewDecoder(file)
	for {
		var event TransactionEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("decode transaction evidence for SLA evaluation: %w", err)
		}
		if !event.BusinessTransaction {
			continue
		}
		if _, ok := requested[event.ActionName]; !ok {
			continue
		}
		accumulator := stats[event.ActionName]
		if accumulator == nil {
			accumulator = &transactionSLAAccumulator{durationMin: event.DurationMs, durationMax: event.DurationMs, durations: engine.NewTDigest(100)}
			stats[event.ActionName] = accumulator
		}
		accumulator.count++
		if telemetry := event.ServerTelemetry; telemetry != nil {
			if telemetry.ConsumerLagObserved && telemetry.ConsumerLagMs >= 0 && !math.IsNaN(telemetry.ConsumerLagMs) && !math.IsInf(telemetry.ConsumerLagMs, 0) {
				accumulator.lagSamples++
				accumulator.maxLag = math.Max(accumulator.maxLag, telemetry.ConsumerLagMs)
			}
			if telemetry.ConsumerOffsetLagObserved && telemetry.ConsumerOffsetLag >= 0 && telemetry.ConsumerOffsetLag <= 1<<53 {
				accumulator.offsetSamples++
				accumulator.maxOffset = math.Max(accumulator.maxOffset, float64(telemetry.ConsumerOffsetLag))
			}
		}
		if !event.Success {
			accumulator.errors++
		}
		accumulator.bytesSent += event.BytesSent
		accumulator.bytesReceived += event.BytesReceived
		accumulator.durationTotal += event.DurationMs
		accumulator.durationMin = math.Min(accumulator.durationMin, event.DurationMs)
		accumulator.durationMax = math.Max(accumulator.durationMax, event.DurationMs)
		accumulator.durations.Add(event.DurationMs)
	}

	results := make([]TransactionSLARuleResult, 0, len(rules))
	allTransactionRulesEvaluated := true
	for _, rule := range rules {
		result := evaluateTransactionSLARule(rule, stats[rule.Transaction], transactionSLARunSeconds(manifest))
		results = append(results, result)
		if result.Status == "not_evaluated" {
			allTransactionRulesEvaluated = false
			continue
		}
		if result.Status == "fail" {
			manifest.SLABreached = true
			manifest.SLADetails = append(manifest.SLADetails, result.Message)
		} else if result.Status == "warning" {
			manifest.SLADetails = append(manifest.SLADetails, result.Message)
		}
	}
	manifest.TransactionRuleResults = results
	if goalConfigHasArchiveIndependentSLA(transactionGoalConfigFromManifest(manifest)) {
		manifest.SLAEvaluated = manifest.SLAEvaluated && allTransactionRulesEvaluated
	} else {
		manifest.SLAEvaluated = allTransactionRulesEvaluated
	}
	return nil
}

func transactionGoalConfigFromManifest(manifest *RunArchiveManifest) *GoalConfig {
	if manifest == nil {
		return nil
	}
	if manifest.Provenance.GoalConfig != nil {
		return manifest.Provenance.GoalConfig
	}
	if manifest.InitialProvenance != nil {
		return manifest.InitialProvenance.GoalConfig
	}
	return nil
}

func goalConfigHasArchiveIndependentSLA(config *GoalConfig) bool {
	return config != nil && (config.P95LatencyMs != nil ||
		config.MaxErrorPct != nil ||
		config.MaxAvgServerProcessingMs != nil ||
		config.MaxAvgTransferMs != nil ||
		config.MaxErrorCount != nil ||
		config.MaxLCPMs != nil ||
		config.MaxINPMs != nil ||
		config.MaxCLS != nil ||
		config.MaxThrottledRequests != nil ||
		config.MaxEndToEndLatencyMs != nil ||
		config.MaxPartitionSkewPct != nil ||
		config.MaxRedeliveryRatePct != nil ||
		config.MaxDeadLetterRatePct != nil)
}

func transactionRulesFromManifest(manifest *RunArchiveManifest) []TransactionSLARule {
	if manifest == nil {
		return nil
	}
	if manifest.Provenance.GoalConfig != nil && len(manifest.Provenance.GoalConfig.TransactionRules) > 0 {
		return append([]TransactionSLARule(nil), manifest.Provenance.GoalConfig.TransactionRules...)
	}
	if manifest.InitialProvenance != nil && manifest.InitialProvenance.GoalConfig != nil {
		return append([]TransactionSLARule(nil), manifest.InitialProvenance.GoalConfig.TransactionRules...)
	}
	return nil
}

func transactionSLARunSeconds(manifest *RunArchiveManifest) float64 {
	if manifest == nil || manifest.CompletedAt.IsZero() {
		return 0
	}
	startedAt := manifest.CreatedAt
	if manifest.Provenance.StartedAt != nil {
		startedAt = *manifest.Provenance.StartedAt
	} else if manifest.InitialProvenance != nil && manifest.InitialProvenance.StartedAt != nil {
		startedAt = *manifest.InitialProvenance.StartedAt
	}
	if elapsed := manifest.CompletedAt.Sub(startedAt).Seconds(); elapsed > 0 {
		return elapsed
	}
	return 0
}

func evaluateTransactionSLARule(rule TransactionSLARule, stats *transactionSLAAccumulator, runSeconds float64) TransactionSLARuleResult {
	rule.Severity = transactionSLASeverity(rule)
	result := TransactionSLARuleResult{Rule: rule, Status: "not_evaluated"}
	if stats != nil {
		result.MatchedCount = stats.count
	}
	observed, ok := transactionSLAMetricValue(rule.Metric, stats, runSeconds)
	if !ok {
		result.Message = fmt.Sprintf("transaction %q has insufficient archive evidence for %s", rule.Transaction, rule.Metric)
		return result
	}
	result.Observed = &observed
	if compareTransactionSLA(observed, rule.Comparator, rule.Threshold) {
		result.Status = "pass"
		result.Message = formatTransactionSLAResult(rule, observed, "passed")
		return result
	}
	result.Status = transactionSLASeverity(rule)
	result.Message = formatTransactionSLAResult(rule, observed, "breached")
	return result
}

func transactionSLAMetricValue(metric string, stats *transactionSLAAccumulator, runSeconds float64) (float64, bool) {
	count := int64(0)
	if stats != nil {
		count = stats.count
	}
	switch metric {
	case "minimum_count":
		return float64(count), true
	case "error_count":
		if stats == nil {
			return 0, true
		}
		return float64(stats.errors), true
	case "bytes_sent":
		if stats == nil {
			return 0, true
		}
		return float64(stats.bytesSent), true
	case "bytes_received":
		if stats == nil {
			return 0, true
		}
		return float64(stats.bytesReceived), true
	case "tps":
		if runSeconds <= 0 {
			return 0, false
		}
		successes := count
		if stats != nil {
			successes -= stats.errors
		}
		if successes < 0 {
			successes = 0
		}
		// Keep the terminal rule aligned with live/archive TxMetrics: TPS is
		// successful transactions per second, while failures remain visible in
		// error_count and error_rate_pct.
		return float64(successes) / runSeconds, true
	}
	if stats == nil || stats.count == 0 {
		return 0, false
	}
	switch metric {
	case "max_consumer_lag_ms":
		return stats.maxLag, stats.lagSamples == stats.count
	case "max_consumer_offset_lag":
		return stats.maxOffset, stats.offsetSamples == stats.count
	case "average_duration_ms":
		return stats.durationTotal / float64(stats.count), true
	case "p90_duration_ms":
		return stats.durations.Quantile(0.90), true
	case "p95_duration_ms":
		return stats.durations.Quantile(0.95), true
	case "min_duration_ms":
		return stats.durationMin, true
	case "max_duration_ms":
		return stats.durationMax, true
	case "error_rate_pct":
		return float64(stats.errors) * 100 / float64(stats.count), true
	default:
		return 0, false
	}
}

func compareTransactionSLA(observed float64, comparator string, threshold float64) bool {
	switch comparator {
	case "lt":
		return observed < threshold
	case "lte":
		return observed <= threshold
	case "gt":
		return observed > threshold
	case "gte":
		return observed >= threshold
	case "eq":
		return observed == threshold
	case "neq":
		return observed != threshold
	default:
		return false
	}
}

func formatTransactionSLAResult(rule TransactionSLARule, observed float64, outcome string) string {
	return fmt.Sprintf("transaction %q %s %s: observed %.6g, expected %s %.6g (%s)", rule.Transaction, rule.Metric, outcome, observed, rule.Comparator, rule.Threshold, transactionSLASeverity(rule))
}
