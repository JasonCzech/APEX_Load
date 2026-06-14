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
	if threshold == nil || value <= 0 || value <= *threshold {
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
	return &out
}

func buildThresholdAlerts(test *ManagedTest, live *LiveMetrics, thresholds ThresholdSettings) []map[string]any {
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
	if thresholds.MaxErrorRatePct != nil && live.ErrorPct > *thresholds.MaxErrorRatePct {
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
