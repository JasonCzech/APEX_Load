package api

// Telemetry diagnostics and SLA alerting: evaluate LiveMetrics against
// thresholds, sample transaction/JMS diagnostics, rank severity and throttle
// publishing, track TelemetryRunHealth transitions, build threshold/server
// alerts and stop-condition predicates, and broadcast health transitions.

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// policyUnscored, when set, is why no telemetry policy segment classifies
// the interval (TEL-B): its budget decisions are unscored for that reason,
// and only the conditions no policy decides (losses, gaps, samples) are
// judged.
func buildTelemetryDiagnostic(test *ManagedTest, live *LiveMetrics, thresholds ThresholdSettings, policyUnscored string) (*TelemetryDiagnostic, string) {
	if live == nil {
		return nil, "healthy"
	}
	thresholds = normalizeThresholdSettings(thresholds)

	conditions := make([]string, 0, 5)
	summaries := make([]string, 0, 5)
	signatureParts := make([]string, 0, 8)
	var unscored []string
	severity := "healthy"
	budgets := policyUnscored == ""
	if !budgets {
		unscored = append(unscored, policyUnscored)
	}

	if live.Telemetry != nil {
		if live.Telemetry.DroppedResults > 0 {
			conditions = append(conditions, "collector_drops")
			summaries = append(summaries, fmt.Sprintf("collector dropped %d metric samples", live.Telemetry.DroppedResults))
			signatureParts = append(signatureParts, "collector_drops")
			severity = maxDiagnosticSeverity(severity, "danger")
		}
		if live.Telemetry.DroppedSubscriberBatches > 0 {
			conditions = append(conditions, "generator_stream_drops")
			summaries = append(summaries, fmt.Sprintf("generator stream dropped %d metrics batches", live.Telemetry.DroppedSubscriberBatches))
			signatureParts = append(signatureParts, "generator_stream_drops")
			severity = maxDiagnosticSeverity(severity, "danger")
		}
		if live.Telemetry.MissingMetricWindows > 0 {
			conditions = append(conditions, "generator_stream_gaps")
			summaries = append(summaries, fmt.Sprintf("generator stream is missing %d live metrics windows", live.Telemetry.MissingMetricWindows))
			signatureParts = append(signatureParts, "generator_stream_gaps")
			severity = maxDiagnosticSeverity(severity, "danger")
		}
		if live.Telemetry.WebsocketDroppedMessages > 0 || live.Telemetry.WebsocketDroppedEvents > 0 {
			conditions = append(conditions, "websocket_drops")
			summaries = append(summaries, fmt.Sprintf("websocket dropped %d client messages and %d broadcast events", live.Telemetry.WebsocketDroppedMessages, live.Telemetry.WebsocketDroppedEvents))
			signatureParts = append(signatureParts, "websocket_drops")
			severity = maxDiagnosticSeverity(severity, "danger")
		}
		if !budgets {
			// No policy classifies the interval: its budgets are not judged.
		} else if live.Telemetry.SampleAgeUnavailable {
			// No sample was reported: there is no age to judge.
		} else if reason := clockBudgetUnscored(live.Telemetry.SampleAgeMs, thresholds.MaxTelemetrySampleAgeMs, live.Telemetry.SampleAgeQuality); reason != "" {
			unscored = append(unscored, "stale_sample:"+reason)
		} else {
			appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "stale_sample", "sample age", clockScoredValue(live.Telemetry.SampleAgeMs, live.Telemetry.SampleAgeQuality), thresholds.MaxTelemetrySampleAgeMs)
		}
		if budgets {
			appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "generator_lag", "generator processing lag", live.Telemetry.GeneratorProcessingLagMs, thresholds.MaxTelemetryGeneratorLagMs)
			if reason := clockBudgetUnscored(live.Telemetry.TransportLagMs, thresholds.MaxTelemetryTransportLagMs, live.Telemetry.TransportLagQuality); reason != "" {
				unscored = append(unscored, "transport_lag:"+reason)
			} else {
				appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "transport_lag", "transport lag", clockScoredValue(live.Telemetry.TransportLagMs, live.Telemetry.TransportLagQuality), thresholds.MaxTelemetryTransportLagMs)
			}
			appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "controller_fanout_lag", "controller fan-out lag", live.Telemetry.ControllerFanoutLagMs, thresholds.MaxTelemetryControllerFanoutMs)
		}
		// Browser render lag is advisory (a viewer's tab is not the system
		// under test): it is reported, never scored into run health.
	}

	samples, sampleConditions, sampleSummaries, sampleSeverity, sampleSignature := sampleTransactionDiagnostics(test, live)
	if len(sampleConditions) > 0 {
		conditions = append(conditions, sampleConditions...)
		summaries = append(summaries, sampleSummaries...)
		signatureParts = append(signatureParts, sampleSignature...)
		severity = maxDiagnosticSeverity(severity, sampleSeverity)
	}

	jmsSamples, jmsConditions, jmsSummaries, jmsSeverity, jmsSignature := sampleJMSBrokerDiagnostics(live)
	if len(jmsConditions) > 0 {
		samples = append(samples, jmsSamples...)
		conditions = append(conditions, jmsConditions...)
		summaries = append(summaries, jmsSummaries...)
		signatureParts = append(signatureParts, jmsSignature...)
		severity = maxDiagnosticSeverity(severity, jmsSeverity)
	}

	dlqSamples, dlqConditions, dlqSummaries, dlqSeverity, dlqSignature := sampleJMSDeadLetterDiagnostics(live)
	if len(dlqConditions) > 0 {
		samples = append(samples, dlqSamples...)
		conditions = append(conditions, dlqConditions...)
		summaries = append(summaries, dlqSummaries...)
		signatureParts = append(signatureParts, dlqSignature...)
		severity = maxDiagnosticSeverity(severity, dlqSeverity)
	}

	ehSamples, ehConditions, ehSummaries, ehSeverity, ehSignature := sampleEventHubsThrottleDiagnostics(live)
	if len(ehConditions) > 0 {
		samples = append(samples, ehSamples...)
		conditions = append(conditions, ehConditions...)
		summaries = append(summaries, ehSummaries...)
		signatureParts = append(signatureParts, ehSignature...)
		severity = maxDiagnosticSeverity(severity, ehSeverity)
	}

	// An unscored budget decision is neither a breach nor a pass: it is part
	// of the signature so a change publishes, and run health records it.
	for _, entry := range unscored {
		signatureParts = append(signatureParts, "unscored:"+entry)
	}
	if len(conditions) == 0 {
		if !budgets {
			return &TelemetryDiagnostic{
				Severity:  "healthy",
				Summary:   telemetryPolicyUnscoredSummary(policyUnscored),
				Unscored:  unscored,
				Telemetry: live.Telemetry,
			}, policyUnscored
		}
		if len(unscored) > 0 {
			return &TelemetryDiagnostic{
				Severity:  "healthy",
				Summary:   "live telemetry budgets unscored: " + strings.Join(unscored, ", "),
				Unscored:  unscored,
				Telemetry: live.Telemetry,
			}, strings.Join(signatureParts, "|")
		}
		return &TelemetryDiagnostic{
			Severity:  "healthy",
			Summary:   "live telemetry healthy",
			Samples:   nil,
			Telemetry: live.Telemetry,
		}, "healthy"
	}

	signature := strings.Join(signatureParts, "|")
	return &TelemetryDiagnostic{
		Severity:   severity,
		Summary:    strings.Join(summaries, "; "),
		Conditions: conditions,
		Unscored:   unscored,
		Samples:    samples,
		Telemetry:  live.Telemetry,
	}, signature
}

func appendTelemetryBudgetDiagnostic(conditions *[]string, summaries *[]string, signatureParts *[]string, severity *string, condition string, label string, value float64, threshold *float64) {
	// A zero budget is the admin's explicit disable for this diagnostic
	// (ThresholdSettings documents the nil/0/positive tri-state).
	if threshold == nil || *threshold <= 0 || value <= 0 || value <= *threshold {
		return
	}
	*conditions = append(*conditions, condition)
	*summaries = append(*summaries, fmt.Sprintf("%s %.0f ms exceeds the %.0f ms telemetry budget", label, value, *threshold))
	*signatureParts = append(*signatureParts, condition)
	*severity = maxDiagnosticSeverity(*severity, "warning")
}

func sampleTransactionDiagnostics(test *ManagedTest, live *LiveMetrics) ([]DiagnosticSample, []string, []string, string, []string) {
	if live == nil || len(live.Transactions) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	latencyThreshold := slowTransactionDiagnosticThresholdMs
	if test != nil && test.GoalConfig != nil && test.GoalConfig.P95LatencyMs != nil && *test.GoalConfig.P95LatencyMs > 0 {
		latencyThreshold = *test.GoalConfig.P95LatencyMs
	}

	type candidate struct {
		sample DiagnosticSample
		rank   int
		key    string
	}

	candidates := make([]candidate, 0, len(live.Transactions))
	hasErrors := false
	hasSlow := false
	for name, tx := range live.Transactions {
		if tx == nil {
			continue
		}
		errorPct := 0.0
		if tx.Count > 0 {
			errorPct = float64(tx.Errors) / float64(tx.Count) * 100
		}
		issue := ""
		rank := 0
		if tx.Errors > 0 {
			issue = "error"
			rank = 2
			hasErrors = true
		}
		if tx.P99Ms >= latencyThreshold || tx.AvgMs >= latencyThreshold {
			if issue == "" {
				issue = "slow"
				rank = 1
			}
			hasSlow = true
		}
		if issue == "" {
			continue
		}
		candidates = append(candidates, candidate{
			sample: DiagnosticSample{
				Issue:            issue,
				Transaction:      name,
				DependencyType:   tx.DependencyType,
				DependencyTarget: tx.DependencyTarget,
				CorrelationIDs:   append([]string(nil), tx.CorrelationIDs...),
				Count:            tx.Count,
				Errors:           tx.Errors,
				ErrorPct:         errorPct,
				AvgMs:            tx.AvgMs,
				P99Ms:            tx.P99Ms,
			},
			rank: rank,
			key:  name,
		})
	}

	if len(candidates) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if left.rank != right.rank {
			return left.rank > right.rank
		}
		if left.sample.Errors != right.sample.Errors {
			return left.sample.Errors > right.sample.Errors
		}
		if left.sample.P99Ms != right.sample.P99Ms {
			return left.sample.P99Ms > right.sample.P99Ms
		}
		if left.sample.AvgMs != right.sample.AvgMs {
			return left.sample.AvgMs > right.sample.AvgMs
		}
		return strings.Compare(left.key, right.key) < 0
	})

	limit := maxTelemetryDiagnosticSamples
	if len(candidates) < limit {
		limit = len(candidates)
	}
	samples := make([]DiagnosticSample, 0, limit)
	signatureParts := make([]string, 0, limit+2)
	for _, candidate := range candidates[:limit] {
		samples = append(samples, candidate.sample)
		signatureParts = append(signatureParts, fmt.Sprintf("%s:%s:%d:%.0f", candidate.sample.Issue, candidate.sample.Transaction, candidate.sample.Errors, candidate.sample.P99Ms))
	}

	conditions := make([]string, 0, 2)
	summaries := make([]string, 0, 2)
	severity := "healthy"
	if hasErrors {
		conditions = append(conditions, "error_transactions")
		summaries = append(summaries, fmt.Sprintf("sampled %d transaction error hotspots", countSamplesByIssue(samples, "error")))
		signatureParts = append(signatureParts, "condition:error_transactions")
		severity = maxDiagnosticSeverity(severity, "danger")
	}
	if hasSlow {
		conditions = append(conditions, "slow_transactions")
		summaries = append(summaries, fmt.Sprintf("sampled %d slow transaction hotspots at or above %.0f ms", countSlowSamples(samples, latencyThreshold), latencyThreshold))
		signatureParts = append(signatureParts, fmt.Sprintf("condition:slow_transactions:%.0f", latencyThreshold))
		severity = maxDiagnosticSeverity(severity, "warning")
	}

	return samples, conditions, summaries, severity, signatureParts
}

func countSamplesByIssue(samples []DiagnosticSample, issue string) int {
	count := 0
	for _, sample := range samples {
		if sample.Issue == issue {
			count++
		}
	}
	return count
}

func countSlowSamples(samples []DiagnosticSample, latencyThreshold float64) int {
	count := 0
	for _, sample := range samples {
		if sample.Issue == "slow" || sample.P99Ms >= latencyThreshold || sample.AvgMs >= latencyThreshold {
			count++
		}
	}
	return count
}

func sampleJMSBrokerDiagnostics(live *LiveMetrics) ([]DiagnosticSample, []string, []string, string, []string) {
	if live == nil || len(live.Transactions) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	type candidate struct {
		sample DiagnosticSample
		key    string
	}

	candidates := make([]candidate, 0, len(live.Transactions))
	for name, tx := range live.Transactions {
		if tx == nil || !strings.HasPrefix(tx.DependencyType, "jms_") || tx.ServerTelemetry == nil {
			continue
		}
		telemetry := tx.ServerTelemetry
		if telemetry.AvgConsumerLagMs < jmsBrokerPressureMinLagMs {
			continue
		}
		baselineLatency := telemetry.AvgMessageLatencyMs
		if baselineLatency <= 0 {
			baselineLatency = telemetry.AvgTransferMs
		}
		if baselineLatency <= 0 {
			baselineLatency = jmsBrokerPressureMinLagMs / jmsBrokerPressureLagMultiplier
		}
		if telemetry.AvgConsumerLagMs < baselineLatency*jmsBrokerPressureLagMultiplier {
			continue
		}
		sendRate := telemetry.AvgMessagesSentPerSec
		recvRate := telemetry.AvgMessagesReceivedPerSec
		if recvRate > 0 && sendRate < recvRate*jmsBrokerPressureSendRecvRatio {
			continue
		}
		if sendRate == 0 && recvRate == 0 && telemetry.MessagesSent == 0 && telemetry.MessagesReceived == 0 {
			continue
		}
		candidates = append(candidates, candidate{
			sample: DiagnosticSample{
				Issue:                     "broker",
				Transaction:               name,
				DependencyType:            tx.DependencyType,
				DependencyTarget:          tx.DependencyTarget,
				CorrelationIDs:            append([]string(nil), tx.CorrelationIDs...),
				Count:                     tx.Count,
				AvgMs:                     tx.AvgMs,
				P99Ms:                     tx.P99Ms,
				AvgMessageLatencyMs:       telemetry.AvgMessageLatencyMs,
				AvgConsumerLagMs:          telemetry.AvgConsumerLagMs,
				AvgMessagesSentPerSec:     sendRate,
				AvgMessagesReceivedPerSec: recvRate,
			},
			key: name,
		})
	}

	if len(candidates) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].sample.AvgConsumerLagMs != candidates[j].sample.AvgConsumerLagMs {
			return candidates[i].sample.AvgConsumerLagMs > candidates[j].sample.AvgConsumerLagMs
		}
		if candidates[i].sample.AvgMessagesSentPerSec != candidates[j].sample.AvgMessagesSentPerSec {
			return candidates[i].sample.AvgMessagesSentPerSec > candidates[j].sample.AvgMessagesSentPerSec
		}
		return strings.Compare(candidates[i].key, candidates[j].key) < 0
	})

	limit := maxTelemetryDiagnosticSamples
	if len(candidates) < limit {
		limit = len(candidates)
	}
	samples := make([]DiagnosticSample, 0, limit)
	signatureParts := make([]string, 0, limit+1)
	for _, candidate := range candidates[:limit] {
		samples = append(samples, candidate.sample)
		signatureParts = append(signatureParts, fmt.Sprintf("broker:%s:%.0f:%.1f:%.1f", candidate.sample.Transaction, candidate.sample.AvgConsumerLagMs, candidate.sample.AvgMessagesSentPerSec, candidate.sample.AvgMessagesReceivedPerSec))
	}

	conditions := []string{"jms_broker_pressure"}
	summaries := []string{fmt.Sprintf("sampled %d JMS broker pressure hotspots where consumer lag materially exceeds request-reply latency", len(samples))}
	signatureParts = append(signatureParts, "condition:jms_broker_pressure")
	return samples, conditions, summaries, "warning", signatureParts
}

// sampleEventHubsThrottleDiagnostics flags Event Hubs producer transactions under
// ingestion pressure: throttle / ServerBusy / quota responses (the namespace is
// shedding load) or a hot-partition skew that will throttle one partition before
// the aggregate budget is hit. End-to-end latency is reported alongside but is
// not a trigger on its own (it has no fixed baseline without a goal threshold).
func sampleEventHubsThrottleDiagnostics(live *LiveMetrics) ([]DiagnosticSample, []string, []string, string, []string) {
	if live == nil || len(live.Transactions) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	type candidate struct {
		sample DiagnosticSample
		key    string
	}

	candidates := make([]candidate, 0, len(live.Transactions))
	for name, tx := range live.Transactions {
		if tx == nil || !strings.HasPrefix(tx.DependencyType, "eventhubs") || tx.ServerTelemetry == nil {
			continue
		}
		telemetry := tx.ServerTelemetry
		throttled := telemetry.TotalThrottledRequests
		skew := telemetry.PartitionSkewPct
		if throttled == 0 && skew < eventHubsPartitionSkewWarnPct {
			continue
		}
		candidates = append(candidates, candidate{
			sample: DiagnosticSample{
				Issue:                "eventhubs_throttle",
				Transaction:          name,
				DependencyType:       tx.DependencyType,
				DependencyTarget:     tx.DependencyTarget,
				CorrelationIDs:       append([]string(nil), tx.CorrelationIDs...),
				Count:                tx.Count,
				AvgMs:                tx.AvgMs,
				P99Ms:                tx.P99Ms,
				AvgEndToEndLatencyMs: telemetry.AvgEndToEndLatencyMs,
				ThrottledRequests:    throttled,
				PartitionSkewPct:     skew,
			},
			key: name,
		})
	}

	if len(candidates) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].sample.ThrottledRequests != candidates[j].sample.ThrottledRequests {
			return candidates[i].sample.ThrottledRequests > candidates[j].sample.ThrottledRequests
		}
		if candidates[i].sample.PartitionSkewPct != candidates[j].sample.PartitionSkewPct {
			return candidates[i].sample.PartitionSkewPct > candidates[j].sample.PartitionSkewPct
		}
		return strings.Compare(candidates[i].key, candidates[j].key) < 0
	})

	limit := maxTelemetryDiagnosticSamples
	if len(candidates) < limit {
		limit = len(candidates)
	}
	samples := make([]DiagnosticSample, 0, limit)
	signatureParts := make([]string, 0, limit+1)
	for _, candidate := range candidates[:limit] {
		samples = append(samples, candidate.sample)
		signatureParts = append(signatureParts, fmt.Sprintf("eventhubs_throttle:%s:%d:%.1f", candidate.sample.Transaction, candidate.sample.ThrottledRequests, candidate.sample.PartitionSkewPct))
	}

	conditions := []string{"eventhubs_throttle_pressure"}
	summaries := []string{fmt.Sprintf("sampled %d Event Hubs producer hotspots with throttling or hot-partition skew", len(samples))}
	signatureParts = append(signatureParts, "condition:eventhubs_throttle_pressure")
	return samples, conditions, summaries, "warning", signatureParts
}

func sampleJMSDeadLetterDiagnostics(live *LiveMetrics) ([]DiagnosticSample, []string, []string, string, []string) {
	if live == nil || len(live.Transactions) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	type candidate struct {
		sample DiagnosticSample
		key    string
	}

	candidates := make([]candidate, 0, len(live.Transactions))
	for name, tx := range live.Transactions {
		if tx == nil || !strings.HasPrefix(tx.DependencyType, "jms_dead_letter_") || tx.Count == 0 {
			continue
		}
		candidates = append(candidates, candidate{
			sample: DiagnosticSample{
				Issue:            "dead_letter",
				Transaction:      name,
				DependencyType:   tx.DependencyType,
				DependencyTarget: tx.DependencyTarget,
				CorrelationIDs:   append([]string(nil), tx.CorrelationIDs...),
				Count:            tx.Count,
				Errors:           tx.Errors,
				AvgMs:            tx.AvgMs,
				P99Ms:            tx.P99Ms,
			},
			key: name,
		})
	}

	if len(candidates) == 0 {
		return nil, nil, nil, "healthy", nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].sample.Count != candidates[j].sample.Count {
			return candidates[i].sample.Count > candidates[j].sample.Count
		}
		if candidates[i].sample.Errors != candidates[j].sample.Errors {
			return candidates[i].sample.Errors > candidates[j].sample.Errors
		}
		return strings.Compare(candidates[i].key, candidates[j].key) < 0
	})

	limit := maxTelemetryDiagnosticSamples
	if len(candidates) < limit {
		limit = len(candidates)
	}
	samples := make([]DiagnosticSample, 0, limit)
	signatureParts := make([]string, 0, limit+1)
	for _, candidate := range candidates[:limit] {
		samples = append(samples, candidate.sample)
		signatureParts = append(signatureParts, fmt.Sprintf("dead_letter:%s:%d:%d", candidate.sample.Transaction, candidate.sample.Count, candidate.sample.Errors))
	}

	conditions := []string{"jms_dead_letter_activity"}
	summaries := []string{fmt.Sprintf("observed %d JMS dead-letter destinations with live traffic", len(samples))}
	signatureParts = append(signatureParts, "condition:jms_dead_letter_activity")
	return samples, conditions, summaries, "warning", signatureParts
}

func maxDiagnosticSeverity(current string, next string) string {
	if diagnosticSeverityRank(next) > diagnosticSeverityRank(current) {
		return next
	}
	return current
}

func diagnosticSeverityRank(severity string) int {
	switch severity {
	case "danger":
		return 2
	case "warning":
		return 1
	default:
		return 0
	}
}

func shouldPublishTelemetryDiagnostic(state *telemetryDiagnosticState, signature string, now time.Time) bool {
	if state == nil {
		return signature != "healthy"
	}
	if signature == "healthy" {
		if state.LastSignature == "" || state.LastSignature == "healthy" {
			return false
		}
		state.LastSignature = "healthy"
		state.LastPublishedAt = now
		return true
	}
	if signature != state.LastSignature || state.LastPublishedAt.IsZero() || now.Sub(state.LastPublishedAt) >= telemetryDiagnosticSampleInterval {
		state.LastSignature = signature
		state.LastPublishedAt = now
		return true
	}
	return false
}

func updateTelemetryRunHealth(state *telemetryDiagnosticState, diagnostic *TelemetryDiagnostic, signature string, now time.Time) *TelemetryRunHealth {
	if state == nil {
		return nil
	}
	if diagnostic == nil {
		diagnostic = &TelemetryDiagnostic{Severity: "healthy", Summary: "live telemetry healthy"}
		signature = "healthy"
	}
	if signature == "" {
		signature = "healthy"
	}

	status := "healthy"
	sustained := false
	breachActive := false
	breachDurationSec := 0
	if diagnostic.Severity != "" && diagnostic.Severity != "healthy" {
		breachActive = true
		if state.BreachStartedAt == nil {
			startedAt := now
			state.BreachStartedAt = &startedAt
		}
		breachDurationSec = int(now.Sub(*state.BreachStartedAt).Seconds())
		if diagnostic.Severity == "danger" || now.Sub(*state.BreachStartedAt) >= telemetrySustainedBreachWindow {
			status = "breached"
			sustained = diagnostic.Severity != "danger"
		} else {
			status = "degraded"
		}
	} else {
		state.BreachStartedAt = nil
		if len(diagnostic.Unscored) > 0 {
			// Not a pass: an interval with an unscored budget decision is
			// unknown in coverage and archive summaries.
			status = "unscored"
		}
	}

	recovered := status == "healthy" && state.announced != ""
	announce := false
	switch status {
	case "degraded", "breached":
		// A spell interrupted by an unscored stretch is not announced again.
		announce = state.announced != status
		state.announced = status
	case "healthy":
		state.announced = ""
	}
	lastTransitionAt := now
	if state.Health != nil && state.Health.Status == status && !state.Health.LastTransitionAt.IsZero() {
		lastTransitionAt = state.Health.LastTransitionAt
	}
	health := &TelemetryRunHealth{
		Status:            status,
		Severity:          normalizedTelemetrySeverity(diagnostic.Severity),
		Summary:           diagnostic.Summary,
		CurrentSignature:  signature,
		Conditions:        append([]string(nil), diagnostic.Conditions...),
		Unscored:          append([]string(nil), diagnostic.Unscored...),
		BreachActive:      breachActive,
		SustainedBreach:   sustained,
		BreachDurationSec: breachDurationSec,
		LastObservedAt:    now,
		LastTransitionAt:  lastTransitionAt,
		recovered:         recovered,
		announce:          announce,
	}
	if state.BreachStartedAt != nil {
		startedAt := *state.BreachStartedAt
		health.BreachStartedAt = &startedAt
	}
	state.Health = health
	return cloneTelemetryRunHealth(health)
}

func normalizedTelemetrySeverity(severity string) string {
	switch severity {
	case "danger", "warning":
		return severity
	default:
		return "healthy"
	}
}

func cloneTelemetryRunHealth(in *TelemetryRunHealth) *TelemetryRunHealth {
	if in == nil {
		return nil
	}
	out := *in
	if in.Conditions != nil {
		out.Conditions = append([]string(nil), in.Conditions...)
	}
	if in.Unscored != nil {
		out.Unscored = append([]string(nil), in.Unscored...)
	}
	if in.ConditionLabels != nil {
		out.ConditionLabels = append([]string(nil), in.ConditionLabels...)
	}
	if in.StageBudgets != nil {
		out.StageBudgets = append([]TelemetryStageBudget(nil), in.StageBudgets...)
	}
	if in.BreachStartedAt != nil {
		startedAt := *in.BreachStartedAt
		out.BreachStartedAt = &startedAt
	}
	return &out
}

// archivedTelemetryHealth is a copy of live telemetry for an interval row or
// history snapshot: the run's cumulative coverage belongs to the live view
// and the manifest's telemetry_coverage, not to every row.
func archivedTelemetryHealth(in *TelemetryHealth) *TelemetryHealth {
	out := cloneTelemetryHealth(in)
	if out != nil {
		// Run-cumulative records are in the manifest, not on every row.
		out.Coverage, out.StageDistributions, out.Acceptance, out.RenderPopulation = nil, nil, nil, nil
		out.PolicySegments, out.StageBudgets, out.ValidityFindings = nil, nil, nil
	}
	return out
}

func cloneTelemetryHealth(in *TelemetryHealth) *TelemetryHealth {
	if in == nil {
		return nil
	}
	out := *in
	if in.Sources != nil {
		sources := *in.Sources
		out.Sources = &sources
	}
	out.Loss = in.Loss.clone()
	out.StageDistributions = cloneTelemetryStageDistributions(in.StageDistributions)
	out.Acceptance = cloneTelemetryAcceptanceVerdict(in.Acceptance)
	out.Publication = in.Publication.clone()
	out.RenderPopulation = in.RenderPopulation.clone()
	if in.PolicySegments != nil {
		out.PolicySegments = make([]TelemetryPolicySegmentReceipt, len(in.PolicySegments))
		for i, seg := range in.PolicySegments {
			seg.Policy = cloneTelemetryBudgetPolicy(seg.Policy)
			out.PolicySegments[i] = seg
		}
	}
	if in.StageBudgets != nil {
		out.StageBudgets = append([]TelemetryStageBudget(nil), in.StageBudgets...)
	}
	if in.IntervalConditionLabels != nil {
		out.IntervalConditionLabels = append([]string(nil), in.IntervalConditionLabels...)
	}
	out.ValidityFindings = cloneTelemetryValidityFindings(in.ValidityFindings)
	if in.Coverage != nil {
		coverage := *in.Coverage
		coverage.Sources = append([]TelemetrySourceCoverage(nil), in.Coverage.Sources...)
		out.Coverage = &coverage
	}
	return &out
}

func buildThresholdAlerts(test *ManagedTest, live *LiveMetrics, thresholds ThresholdSettings, inclusiveErrorThreshold ...bool) []map[string]any {
	alerts := make([]map[string]any, 0, 5)
	if thresholds.MaxP99LatencyMs != nil && live.P99Ms > *thresholds.MaxP99LatencyMs {
		alerts = append(alerts, map[string]any{
			"type":      "threshold_alert",
			"test_id":   test.ID,
			"test_name": test.Name,
			"timestamp": live.Timestamp,
			"metric":    "p99_ms",
			"value":     live.P99Ms,
			"threshold": *thresholds.MaxP99LatencyMs,
		})
	}
	inclusiveError := len(inclusiveErrorThreshold) > 0 && inclusiveErrorThreshold[0]
	if thresholds.MaxErrorRatePct != nil && (live.ErrorPct > *thresholds.MaxErrorRatePct || (inclusiveError && live.ErrorPct >= *thresholds.MaxErrorRatePct)) {
		alerts = append(alerts, map[string]any{
			"type":      "threshold_alert",
			"test_id":   test.ID,
			"test_name": test.Name,
			"timestamp": live.Timestamp,
			"metric":    "error_pct",
			"value":     live.ErrorPct,
			"threshold": *thresholds.MaxErrorRatePct,
			"action":    "stop_requested",
		})
	}
	if live != nil && live.TargetTPS > 0 && live.GoalGapPct != nil {
		// A goal run is judged over its goal window in both directions: an
		// overshoot loads the system under test beyond what the goal asked for
		// as surely as a shortfall misses it.
		if gap := *live.GoalGapPct; math.Abs(gap) >= goalGapAlertPct {
			metric := "throughput_gap_pct"
			if gap < 0 {
				metric = "throughput_over_pct"
			}
			alerts = append(alerts, map[string]any{
				"type":       "threshold_alert",
				"test_id":    test.ID,
				"test_name":  test.Name,
				"timestamp":  live.Timestamp,
				"metric":     metric,
				"value":      live.GoalWindowTPS,
				"threshold":  live.TargetTPS,
				"target_tps": live.TargetTPS,
				"gap_pct":    math.Abs(gap),
				"window":     goalThroughputWindowLabel,
			})
		}
	} else if live != nil && live.TargetTPS > 0 && live.ThroughputTargetMissed {
		alerts = append(alerts, map[string]any{
			"type":       "threshold_alert",
			"test_id":    test.ID,
			"test_name":  test.Name,
			"timestamp":  live.Timestamp,
			"metric":     "throughput_gap_pct",
			"value":      live.TPS,
			"threshold":  live.TargetTPS,
			"target_tps": live.TargetTPS,
			"gap_pct":    live.ThroughputGapPct,
		})
	}
	if alert := buildServerTelemetryThresholdAlert(test, live, thresholds.MaxAvgServerProcessingMs, "avg_server_processing_ms", func(summary *ServerTelemetrySummary) float64 {
		return summary.AvgServerProcessingMs
	}); alert != nil {
		alerts = append(alerts, alert)
	}
	if alert := buildServerTelemetryThresholdAlert(test, live, thresholds.MaxAvgTransferMs, "avg_transfer_ms", func(summary *ServerTelemetrySummary) float64 {
		return summary.AvgTransferMs
	}); alert != nil {
		alerts = append(alerts, alert)
	}
	return alerts
}

func buildServerTelemetryThresholdAlert(test *ManagedTest, live *LiveMetrics, threshold *float64, metric string, pickValue func(summary *ServerTelemetrySummary) float64) map[string]any {
	if threshold == nil || live == nil || len(live.Transactions) == 0 {
		return nil
	}

	var hotTx *TxMetrics
	hotValue := 0.0
	hotName := ""
	for name, tx := range live.Transactions {
		if tx == nil || tx.ServerTelemetry == nil {
			continue
		}
		value := pickValue(tx.ServerTelemetry)
		if value <= *threshold {
			continue
		}
		if hotTx == nil || value > hotValue {
			hotTx = tx
			hotValue = value
			hotName = name
		}
	}
	if hotTx == nil {
		return nil
	}

	return map[string]any{
		"type":              "threshold_alert",
		"test_id":           test.ID,
		"test_name":         test.Name,
		"timestamp":         live.Timestamp,
		"metric":            metric,
		"value":             hotValue,
		"threshold":         *threshold,
		"transaction":       hotName,
		"dependency_type":   hotTx.DependencyType,
		"dependency_target": hotTx.DependencyTarget,
	}
}

func shouldStopForErrorThreshold(live *LiveMetrics, thresholds ThresholdSettings) bool {
	return thresholds.MaxErrorRatePct != nil && live.ErrorPct > *thresholds.MaxErrorRatePct
}

// applyThresholdAlertStopActions makes the alert payload describe the action
// this evaluation will actually take. buildThresholdAlerts marks error-rate
// breaches as stop candidates, but final-drain, terminal, paused, and otherwise
// non-running evaluations must not claim a stop was requested.
func applyThresholdAlertStopActions(alerts []map[string]any, stopForError, stopForP99 bool) {
	for _, alert := range alerts {
		metric, _ := alert["metric"].(string)
		switch metric {
		case "error_pct":
			if stopForError {
				alert["action"] = "stop_requested"
			} else {
				delete(alert, "action")
			}
		case "p99_ms":
			if stopForP99 {
				alert["action"] = "stop_requested"
			} else {
				delete(alert, "action")
			}
		}
	}
}

// thresholdAlertNotificationKey dedupes threshold alerts per metric and, for
// per-transaction server-telemetry alerts, per transaction. Note that
// buildServerTelemetryThresholdAlert emits only the single hottest transaction
// per metric each tick, so per-transaction keys mostly matter when the hottest
// transaction rotates — each rotation is a distinct key with its own cooldown.
func thresholdAlertNotificationKey(alert map[string]any) string {
	metric, _ := alert["metric"].(string)
	if metric == "" {
		return ""
	}
	if transaction, _ := alert["transaction"].(string); transaction != "" {
		return metric + "|" + transaction
	}
	return metric
}

// sanitizeThresholdAlertText bounds script-author-controlled text (transaction
// names) before it is embedded in webhook summaries: control characters can
// corrupt downstream text handling, Slack mrkdwn interprets <...> links, Teams
// cards interpret [label](target) links, and unbounded names bloat payloads.
func sanitizeThresholdAlertText(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if r < 0x20 || r == 0x7f || strings.ContainsRune("<>[]()", r) {
			continue
		}
		builder.WriteRune(r)
	}
	out := strings.TrimSpace(builder.String())
	const maxThresholdAlertTextBytes = 120
	if len(out) > maxThresholdAlertTextBytes {
		cut := out[:maxThresholdAlertTextBytes]
		for len(cut) > 0 && !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		out = cut
	}
	return out
}

func thresholdAlertNotificationEvent(test *ManagedTest, status string, alert map[string]any, now time.Time) NotificationEvent {
	metric, _ := alert["metric"].(string)
	value, _ := alert["value"].(float64)
	threshold, _ := alert["threshold"].(float64)
	severity := "warning"
	if action, _ := alert["action"].(string); action == "stop_requested" {
		severity = "danger"
	}
	summary := fmt.Sprintf("%s %.2f exceeded threshold %.2f", metric, value, threshold)
	if metric == "throughput_gap_pct" || metric == "throughput_over_pct" {
		direction := "below"
		if metric == "throughput_over_pct" {
			direction = "above"
		}
		window := ""
		if label, ok := alert["window"].(string); ok && label != "" {
			window = " (" + label + ")"
		}
		summary = fmt.Sprintf("throughput %.2f TPS%s is %s target %.2f TPS", value, window, direction, threshold)
		if gap, ok := alert["gap_pct"].(float64); ok && gap > 0 {
			noun := "gap"
			if metric == "throughput_over_pct" {
				noun = "over"
			}
			summary += fmt.Sprintf(" (%.2f%% %s)", gap, noun)
		}
	}
	if alert["action"] == "advisory" {
		if explanation, ok := alert["explanation"].(string); ok {
			summary = explanation
		}
	}
	rawTransaction, _ := alert["transaction"].(string)
	if transaction := sanitizeThresholdAlertText(rawTransaction); transaction != "" && alert["action"] != "advisory" {
		summary = fmt.Sprintf("%s %.2f exceeded threshold %.2f for transaction %s", metric, value, threshold, transaction)
	}
	return NotificationEvent{
		Type:       "threshold_alert",
		TestID:     test.ID,
		ProjectID:  normalizeProjectID(test.ProjectID),
		TestName:   test.Name,
		Status:     status,
		Severity:   severity,
		Summary:    summary,
		Conditions: []string{metric},
		Timestamp:  now,
	}
}

// routeThresholdAlertNotifications forwards threshold breaches to the
// notification webhooks. The hub broadcast repeats every metrics tick while a
// breach persists, so webhook dispatch fires once per breach episode (episode
// keys include the transaction), while thresholdAlertRenotifyInterval is
// enforced PER METRIC: per-transaction episode keys rotate freely with the
// hottest transaction, and a per-key cooldown would let a rotating hotspot
// mint a fresh notification every tick. The per-metric cooldown bounds webhook
// volume to at most one delivery per metric per interval (and bounds the
// cooldown map to the fixed metric set). An episode that begins inside the
// cooldown is deferred, not swallowed: it notifies on the first evaluation
// after the cooldown expires if it is still breaching. A key that recovers
// re-arms for its next episode. State is in-memory per run, so a controller
// restart or HA failover mid-breach may re-notify once.
func (tr *TestRunner) routeThresholdAlertNotifications(test *ManagedTest, alerts []map[string]any, now time.Time) {
	if tr == nil || test == nil || tr.notifier == nil {
		return
	}
	settings := tr.currentNotificationSettings()
	tr.mu.Lock()
	if test.thresholdAlertActive == nil {
		test.thresholdAlertActive = make(map[string]bool)
	}
	if test.thresholdAlertNotifiedAt == nil {
		test.thresholdAlertNotifiedAt = make(map[string]time.Time)
	}
	activeNow := make(map[string]bool, len(alerts))
	for _, alert := range alerts {
		episodeKey := thresholdAlertNotificationKey(alert)
		if episodeKey == "" {
			continue
		}
		metric, _ := alert["metric"].(string)
		activeNow[episodeKey] = true
		alreadyNotified := test.thresholdAlertActive[episodeKey]
		lastNotified := test.thresholdAlertNotifiedAt[metric]
		if alreadyNotified || (!lastNotified.IsZero() && now.Sub(lastNotified) < thresholdAlertRenotifyInterval) {
			continue
		}
		event := thresholdAlertNotificationEvent(test, test.Status, alert, now)
		// Consume the episode/cooldown only after the bounded dispatcher admits
		// at least one matching destination. A breach first observed with no
		// configured destination, an invalid format, or a saturated dispatcher
		// is retried on its next evaluation.
		if !tr.notifier.Dispatch(settings, event) {
			continue
		}
		test.thresholdAlertActive[episodeKey] = true
		test.thresholdAlertNotifiedAt[metric] = now
	}
	for key := range test.thresholdAlertActive {
		if !activeNow[key] {
			delete(test.thresholdAlertActive, key)
		}
	}
	tr.mu.Unlock()
}

// shouldStopForSustainedP99 implements the opt-in sustained-P99 auto-stop: the
// run stops only after thresholds.p99_latency_stop_consecutive_samples
// consecutive metrics evaluations above max_p99_latency_ms. Any non-breaching
// sample (including paused/baseline windows, which report non-breaching
// values) resets the counter. Disabled (nil/0 samples or nil P99) keeps the
// alert-only behavior and clears any prior streak.
func (tr *TestRunner) shouldStopForSustainedP99(test *ManagedTest, live *LiveMetrics, thresholds ThresholdSettings) bool {
	if tr == nil || test == nil || live == nil {
		return false
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if test.Status != "RUNNING" || thresholds.P99LatencyStopConsecutiveSamples == nil ||
		*thresholds.P99LatencyStopConsecutiveSamples <= 0 || thresholds.MaxP99LatencyMs == nil {
		test.p99BreachConsecutive = 0
		return false
	}
	if live.P99Ms > *thresholds.MaxP99LatencyMs {
		test.p99BreachConsecutive++
	} else {
		test.p99BreachConsecutive = 0
	}
	return test.p99BreachConsecutive >= *thresholds.P99LatencyStopConsecutiveSamples
}

func (tr *TestRunner) testIsRunning(test *ManagedTest) bool {
	if tr == nil || test == nil {
		return false
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return test.Status == "RUNNING"
}

func shouldStopForGoalStatus(live *LiveMetrics) bool {
	return live.GoalStatus != nil && live.GoalStatus.StopTriggered
}

// telemetryHealthEvent is the notification a run-health transition raises,
// if any. Only the end of an announced degradation is a recovery: leaving an
// unscored stretch (run start, a clock expiry or step) is not, and an
// unscored status raises nothing.
func telemetryHealthEvent(previous, current *TelemetryRunHealth) string {
	if current == nil || previous != nil && previous.Status == current.Status {
		return ""
	}
	switch current.Status {
	case "degraded":
		if current.announce {
			return "telemetry_degraded"
		}
	case "breached":
		if current.announce {
			return "telemetry_breached"
		}
	case "healthy":
		if current.recovered {
			return "telemetry_recovered"
		}
	}
	return ""
}

func (tr *TestRunner) broadcastTelemetryHealthTransition(test *ManagedTest, previous *TelemetryRunHealth, current *TelemetryRunHealth, now time.Time) {
	if tr == nil || test == nil || current == nil {
		return
	}
	eventType := telemetryHealthEvent(previous, current)
	if eventType == "" {
		return
	}
	scrubber := newManagedTestTelemetryTextScrubber(test)
	scrubbedCurrent := scrubTelemetryRunHealth(current, scrubber)
	testName := scrubTelemetryIdentity(scrubber.text(test.Name))

	message := map[string]any{
		"type":       "telemetry_health_changed",
		"event_type": eventType,
		"test_id":    test.ID,
		"project_id": normalizeProjectID(test.ProjectID),
		"test_name":  testName,
		"timestamp":  now,
		"data":       scrubbedCurrent,
	}
	if tr.hub != nil {
		tr.hub.BroadcastJSON(message)
	}
	if tr.notifier != nil {
		tr.notifier.Dispatch(tr.currentNotificationSettings(), NotificationEvent{
			Type:       eventType,
			TestID:     test.ID,
			ProjectID:  normalizeProjectID(test.ProjectID),
			TestName:   testName,
			Status:     scrubbedCurrent.Status,
			Severity:   scrubbedCurrent.Severity,
			Summary:    scrubbedCurrent.Summary,
			Conditions: append([]string(nil), scrubbedCurrent.Conditions...),
			Timestamp:  now,
		})
	}
}
