package api

// Telemetry diagnostics and SLA alerting: evaluate LiveMetrics against
// thresholds, sample transaction/JMS diagnostics, rank severity and throttle
// publishing, track TelemetryRunHealth transitions, build threshold/server
// alerts and stop-condition predicates, and broadcast health transitions.

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

func buildTelemetryDiagnostic(test *ManagedTest, live *LiveMetrics, thresholds ThresholdSettings) (*TelemetryDiagnostic, string) {
	if live == nil {
		return nil, "healthy"
	}
	thresholds = normalizeThresholdSettings(thresholds)

	conditions := make([]string, 0, 5)
	summaries := make([]string, 0, 5)
	signatureParts := make([]string, 0, 8)
	severity := "healthy"

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
		appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "stale_sample", "sample age", live.Telemetry.SampleAgeMs, thresholds.MaxTelemetrySampleAgeMs)
		appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "generator_lag", "generator processing lag", live.Telemetry.GeneratorProcessingLagMs, thresholds.MaxTelemetryGeneratorLagMs)
		appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "transport_lag", "transport lag", live.Telemetry.TransportLagMs, thresholds.MaxTelemetryTransportLagMs)
		appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "controller_fanout_lag", "controller fan-out lag", live.Telemetry.ControllerFanoutLagMs, thresholds.MaxTelemetryControllerFanoutMs)
		appendTelemetryBudgetDiagnostic(&conditions, &summaries, &signatureParts, &severity, "browser_render_lag", "browser render lag", live.Telemetry.BrowserRenderLagMs, thresholds.MaxTelemetryBrowserRenderLagMs)
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

	if len(conditions) == 0 {
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
		BreachActive:      breachActive,
		SustainedBreach:   sustained,
		BreachDurationSec: breachDurationSec,
		LastObservedAt:    now,
		LastTransitionAt:  lastTransitionAt,
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
	if in.BreachStartedAt != nil {
		startedAt := *in.BreachStartedAt
		out.BreachStartedAt = &startedAt
	}
	return &out
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
	if live != nil && live.TargetTPS > 0 && live.ThroughputTargetMissed {
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
	if metric == "throughput_gap_pct" {
		summary = fmt.Sprintf("throughput %.2f TPS is below target %.2f TPS", value, threshold)
		if gap, ok := alert["gap_pct"].(float64); ok && gap > 0 {
			summary += fmt.Sprintf(" (%.2f%% gap)", gap)
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

func (tr *TestRunner) broadcastTelemetryHealthTransition(test *ManagedTest, previous *TelemetryRunHealth, current *TelemetryRunHealth, now time.Time) {
	if tr == nil || test == nil || current == nil {
		return
	}
	previousStatus := ""
	if previous != nil {
		previousStatus = previous.Status
	}
	if previousStatus == current.Status {
		return
	}

	eventType := ""
	switch current.Status {
	case "degraded":
		eventType = "telemetry_degraded"
	case "breached":
		eventType = "telemetry_breached"
	case "healthy":
		if previousStatus == "" || previousStatus == "healthy" {
			return
		}
		eventType = "telemetry_recovered"
	default:
		return
	}

	message := map[string]any{
		"type":       "telemetry_health_changed",
		"event_type": eventType,
		"test_id":    test.ID,
		"project_id": normalizeProjectID(test.ProjectID),
		"test_name":  test.Name,
		"timestamp":  now,
		"data":       current,
	}
	if tr.hub != nil {
		tr.hub.BroadcastJSON(message)
	}
	if tr.notifier != nil {
		tr.notifier.Dispatch(tr.currentNotificationSettings(), NotificationEvent{
			Type:       eventType,
			TestID:     test.ID,
			ProjectID:  normalizeProjectID(test.ProjectID),
			TestName:   test.Name,
			Status:     current.Status,
			Severity:   current.Severity,
			Summary:    current.Summary,
			Conditions: append([]string(nil), current.Conditions...),
			Timestamp:  now,
		})
	}
}
