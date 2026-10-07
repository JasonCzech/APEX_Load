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
	ackSamples      int64
	maxAck          float64
	invalidDuration bool
	invalidBytes    bool
	lagSamples      int64
	offsetSamples   int64
	maxLag          float64
	maxOffset       float64
	count           int64
	errors          int64
	bytesSent       int64
	bytesReceived   int64
	durationTotal   float64
	durationMin     float64
	durationMax     float64
	durations       *engine.TDigest
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

	// Rule indexes keep scope intersections exact and the working set bounded.
	stats := make([]*transactionSLAAccumulator, len(rules))
	protocols := map[string]string{}
	for _, group := range manifest.Provenance.Groups {
		protocols[group.Name] = group.Protocol
	}
	decoder := json.NewDecoder(file)
	for {
		var event TransactionEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("decode transaction evidence for SLA evaluation: %w", err)
		}
		for i, rule := range rules {
			population := rule.Population
			if population == "" {
				population = "transaction"
			}
			if archiveEventPopulation(event) != population || event.ActionName != rule.Transaction || (rule.Group != "" && rule.Group != event.Group) || (rule.Protocol != "" && rule.Protocol != protocols[event.Group]) {
				continue
			}
			a := stats[i]
			if a == nil {
				a = &transactionSLAAccumulator{durationMin: math.Inf(1), durations: engine.NewTDigest(100)}
				stats[i] = a
			}
			a.count++
			if telemetry := event.ServerTelemetry; telemetry != nil {
				if telemetry.AcknowledgementMs != nil && finiteComparison(*telemetry.AcknowledgementMs) && *telemetry.AcknowledgementMs >= 0 {
					a.ackSamples++
					a.maxAck = math.Max(a.maxAck, *telemetry.AcknowledgementMs)
				}
				if telemetry.ConsumerLagObserved && finiteComparison(telemetry.ConsumerLagMs) && telemetry.ConsumerLagMs >= 0 {
					a.lagSamples++
					a.maxLag = math.Max(a.maxLag, telemetry.ConsumerLagMs)
				}
				if telemetry.ConsumerOffsetLagObserved && telemetry.ConsumerOffsetLag >= 0 && telemetry.ConsumerOffsetLag <= 1<<53 {
					a.offsetSamples++
					a.maxOffset = math.Max(a.maxOffset, float64(telemetry.ConsumerOffsetLag))
				}
			}
			if !event.Success {
				a.errors++
			}
			if event.BytesSent < 0 || event.BytesReceived < 0 || event.BytesSent > math.MaxInt64-a.bytesSent || event.BytesReceived > math.MaxInt64-a.bytesReceived {
				a.invalidBytes = true
			} else {
				a.bytesSent += event.BytesSent
				a.bytesReceived += event.BytesReceived
			}
			if !finiteComparison(event.DurationMs) || event.DurationMs < 0 {
				a.invalidDuration = true
				continue
			}
			if !finiteComparison(a.durationTotal + event.DurationMs) {
				a.invalidDuration = true
				continue
			}
			a.durationTotal += event.DurationMs
			a.durationMin = math.Min(a.durationMin, event.DurationMs)
			a.durationMax = math.Max(a.durationMax, event.DurationMs)
			a.durations.Add(event.DurationMs)
		}
	}

	results := make([]TransactionSLARuleResult, 0, len(rules))
	allTransactionRulesEvaluated := true
	for i, rule := range rules {
		result := evaluateTransactionSLARule(rule, stats[i], transactionSLARunSeconds(manifest))
		if archiveSLAIncomplete(manifest) || (rule.Protocol != "" && !transactionSLAProtocolKnown(rule, protocols)) {
			result.Status, result.Observed, result.Message = "not_evaluated", nil, "Incomplete archive source coverage for "+rule.Transaction
		}
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
		result.Message = fmt.Sprintf("transaction %q%s has insufficient archive evidence for %s", rule.Transaction, transactionSLAScope(rule), rule.Metric)
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
		return float64(stats.bytesSent), !stats.invalidBytes
	case "bytes_received":
		if stats == nil {
			return 0, true
		}
		return float64(stats.bytesReceived), !stats.invalidBytes
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
	case "max_acknowledgement_ms":
		return stats.maxAck, stats.ackSamples == stats.count
	case "average_duration_ms":
		return stats.durationTotal / float64(stats.count), !stats.invalidDuration
	case "p90_duration_ms":
		return stats.durations.Quantile(0.90), !stats.invalidDuration
	case "p95_duration_ms":
		return stats.durations.Quantile(0.95), !stats.invalidDuration
	case "p99_duration_ms":
		return stats.durations.Quantile(0.99), !stats.invalidDuration
	case "min_duration_ms":
		return stats.durationMin, !stats.invalidDuration
	case "max_duration_ms":
		return stats.durationMax, !stats.invalidDuration
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
	return fmt.Sprintf("transaction %q%s %s %s: observed %.6g, expected %s %.6g (%s)", rule.Transaction, transactionSLAScope(rule), rule.Metric, outcome, observed, rule.Comparator, rule.Threshold, transactionSLASeverity(rule))
}

// archiveCollectionIncomplete says the archive lacks evidence a source or
// an export did not deliver: derived buckets over it are incomplete, never
// measured.
func archiveCollectionIncomplete(manifest *RunArchiveManifest) bool {
	return archiveSLAIncomplete(manifest) || manifest != nil && manifest.CSVExportIncomplete != ""
}

func archiveSLAIncomplete(manifest *RunArchiveManifest) bool {
	if manifest != nil && manifest.SourceCoverage != nil {
		for _, source := range manifest.SourceCoverage.Sources {
			if !source.Collected {
				return true
			}
		}
	}
	return false
}

func transactionSLAProtocolKnown(rule TransactionSLARule, protocols map[string]string) bool {
	if rule.Group != "" {
		return protocols[rule.Group] == rule.Protocol
	}
	for _, p := range protocols {
		if p == rule.Protocol {
			return true
		}
	}
	return false
}

func transactionSLAScope(rule TransactionSLARule) string {
	if rule.Population == "" && rule.Protocol == "" && rule.Group == "" {
		return ""
	}
	population := rule.Population
	if population == "" {
		population = "transaction"
	}
	return fmt.Sprintf(" [population=%s, protocol=%q, group=%q]", population, rule.Protocol, rule.Group)
}
