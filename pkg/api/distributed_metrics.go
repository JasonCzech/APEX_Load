package api

// distributed_metrics.go — split from distributed_execution_backend.go (same package, behavior-preserving).

import (
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
)

func aggregateDistributedMetricsBatches(testID string, batches map[string]generatorrpc.MetricsBatch) generatorrpc.MetricsBatch {
	combined := generatorrpc.MetricsBatch{TestID: testID, GeneratorID: "distributed"}
	targets := make([]string, 0, len(batches))
	hasCollectedAt := false
	hasPublishedAt := false
	hasControllerReceivedAt := false
	groupsByName := make(map[string]int32)
	for target := range batches {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	for _, target := range targets {
		batch := batches[target]
		if batch.TimestampUnixMs > combined.TimestampUnixMs {
			combined.TimestampUnixMs = batch.TimestampUnixMs
		}
		combined.Stats = mergeDistributedGeneratorStats(combined.Stats, batch.Stats)
		combined.Transactions = append(combined.Transactions, batch.Transactions...)
		for _, group := range batch.Groups {
			if group.Name == "" {
				continue
			}
			groupsByName[group.Name] += group.ActiveVUsers
		}
		if batch.Telemetry != nil {
			if combined.Telemetry == nil {
				combined.Telemetry = &generatorrpc.MetricsTelemetry{}
			}
			combined.Telemetry.DroppedResults += batch.Telemetry.DroppedResults
			if batch.Telemetry.CollectedAtUnixMs > 0 && (!hasCollectedAt || batch.Telemetry.CollectedAtUnixMs < combined.Telemetry.CollectedAtUnixMs) {
				combined.Telemetry.CollectedAtUnixMs = batch.Telemetry.CollectedAtUnixMs
				hasCollectedAt = true
			}
			if batch.Telemetry.PublishedAtUnixMs > 0 && (!hasPublishedAt || batch.Telemetry.PublishedAtUnixMs > combined.Telemetry.PublishedAtUnixMs) {
				combined.Telemetry.PublishedAtUnixMs = batch.Telemetry.PublishedAtUnixMs
				hasPublishedAt = true
			}
			if batch.Telemetry.ControllerReceivedAtUnixMs > 0 && (!hasControllerReceivedAt || batch.Telemetry.ControllerReceivedAtUnixMs > combined.Telemetry.ControllerReceivedAtUnixMs) {
				combined.Telemetry.ControllerReceivedAtUnixMs = batch.Telemetry.ControllerReceivedAtUnixMs
				hasControllerReceivedAt = true
			}
			if batch.Telemetry.SampleAgeMs > combined.Telemetry.SampleAgeMs {
				combined.Telemetry.SampleAgeMs = batch.Telemetry.SampleAgeMs
			}
		}
	}
	groupNames := make([]string, 0, len(groupsByName))
	for name := range groupsByName {
		groupNames = append(groupNames, name)
	}
	sort.Strings(groupNames)
	for _, name := range groupNames {
		combined.Groups = append(combined.Groups, generatorrpc.GroupMetric{Name: name, ActiveVUsers: groupsByName[name]})
	}
	return combined
}

func summarizeGeneratorBatchTransactions(transactions []generatorrpc.TransactionMetric) (float64, float64, float64, float64) {
	if len(transactions) == 0 {
		return 0, 0, 0, 0
	}
	durations := make([]float64, 0, len(transactions))
	errors := 0
	successes := 0
	for _, tx := range transactions {
		durations = append(durations, tx.DurationMs)
		if !tx.Success {
			errors++
		} else {
			successes++
		}
	}
	sort.Float64s(durations)
	errorPct := float64(errors) / float64(len(transactions)) * 100
	return percentileValue(durations, 0.95), percentileValue(durations, 0.99), errorPct, float64(successes)
}

func buildLiveMetricsFromDistributedBatch(test *ManagedTest, batch generatorrpc.MetricsBatch) *LiveMetrics {
	timestamp := time.UnixMilli(batch.TimestampUnixMs)
	if batch.TimestampUnixMs == 0 {
		timestamp = time.Now()
	}

	live := &LiveMetrics{
		Timestamp:        timestamp,
		ActiveVUsers:     int64(batch.Stats.ActiveVUsers),
		Transactions:     make(map[string]*TxMetrics),
		DependencyHealth: make(map[string]*TxMetrics),
		SOAPOperations:   make(map[string]*TxMetrics),
		GroupMetrics:     make(map[string]*GroupLiveMetrics),
		Telemetry:        &TelemetryHealth{},
		Resources: &engine.SystemResources{
			CPUProcessPct: batch.Stats.CPUPercent,
			MemProcessMb:  float64(batch.Stats.MemoryBytes) / (1024 * 1024),
		},
	}
	live.Resources = resourcesFromGeneratorStats(batch.Stats)
	if batch.Telemetry != nil {
		live.Telemetry.DroppedResults = batch.Telemetry.DroppedResults
		if batch.Telemetry.CollectedAtUnixMs > 0 {
			live.Telemetry.CollectedAt = time.UnixMilli(batch.Telemetry.CollectedAtUnixMs)
		}
		if batch.Telemetry.PublishedAtUnixMs > 0 {
			live.Telemetry.GeneratorPublishedAt = time.UnixMilli(batch.Telemetry.PublishedAtUnixMs)
		}
		if batch.Telemetry.ControllerReceivedAtUnixMs > 0 {
			live.Telemetry.ControllerReceivedAt = time.UnixMilli(batch.Telemetry.ControllerReceivedAtUnixMs)
		}
		if !live.Telemetry.CollectedAt.IsZero() && !live.Telemetry.GeneratorPublishedAt.IsZero() {
			live.Telemetry.GeneratorProcessingLagMs = float64(live.Telemetry.GeneratorPublishedAt.Sub(live.Telemetry.CollectedAt).Microseconds()) / 1000.0
		}
		if !live.Telemetry.GeneratorPublishedAt.IsZero() && !live.Telemetry.ControllerReceivedAt.IsZero() {
			live.Telemetry.TransportLagMs = float64(live.Telemetry.ControllerReceivedAt.Sub(live.Telemetry.GeneratorPublishedAt).Microseconds()) / 1000.0
		}
		live.Telemetry.SampleAgeMs = batch.Telemetry.SampleAgeMs
	}
	if live.Telemetry.CollectedAt.IsZero() {
		live.Telemetry.CollectedAt = timestamp
	}

	durationsByName := make(map[string][]float64)
	durationsByDependencyTarget := make(map[string][]float64)
	durationsBySOAPOperation := make(map[string][]float64)
	durationsByGroup := make(map[string][]float64)
	groupAggregates := make(map[string]*GroupLiveMetrics)
	transactionAttempts := int64(0)
	for _, tx := range batch.Transactions {
		transactionAttempts++
		if !tx.Success {
			live.TotalErrors++
		} else {
			live.TotalTx++
		}
		live.AvgResponseMs += tx.DurationMs
		durationsByName[tx.Name] = append(durationsByName[tx.Name], tx.DurationMs)
		if !tx.Success && tx.Snapshot != nil {
			evidence := liveErrorEvidenceFromTransaction(tx)
			if evidence.Signature != "" {
				live.ErrorEvidence = append(live.ErrorEvidence, evidence)
			}
		}
		groupName := strings.TrimSpace(tx.Group)
		if groupName == "" && len(test.Groups) == 1 {
			groupName = test.Groups[0].Name
		}
		if groupName != "" {
			groupMetrics := groupAggregates[groupName]
			if groupMetrics == nil {
				groupMetrics = &GroupLiveMetrics{Name: groupName}
				groupAggregates[groupName] = groupMetrics
			}
			if !tx.Success {
				groupMetrics.Errors++
			} else {
				groupMetrics.Transactions++
				groupMetrics.TPS++
			}
			groupMetrics.AvgResponseMs += tx.DurationMs
			durationsByGroup[groupName] = append(durationsByGroup[groupName], tx.DurationMs)
		}
		metrics := live.Transactions[tx.Name]
		if metrics == nil {
			metrics = &TxMetrics{Name: tx.Name}
			live.Transactions[tx.Name] = metrics
		}
		mergeDistributedTransactionMetrics(metrics, tx)

		if dependencyTarget := strings.TrimSpace(tx.DependencyTarget); dependencyTarget != "" {
			durationsByDependencyTarget[dependencyTarget] = append(durationsByDependencyTarget[dependencyTarget], tx.DurationMs)
			dependencyMetrics := live.DependencyHealth[dependencyTarget]
			if dependencyMetrics == nil {
				dependencyMetrics = &TxMetrics{Name: dependencyTarget}
				live.DependencyHealth[dependencyTarget] = dependencyMetrics
			}
			mergeDistributedTransactionMetrics(dependencyMetrics, tx)
		}

		if operation := strings.TrimSpace(tx.SOAPOperation); operation != "" {
			soapMetrics := live.SOAPOperations[operation]
			if soapMetrics == nil {
				soapMetrics = &TxMetrics{Name: operation}
				live.SOAPOperations[operation] = soapMetrics
			}
			durationsBySOAPOperation[operation] = append(durationsBySOAPOperation[operation], tx.DurationMs)
			mergeDistributedTransactionMetrics(soapMetrics, tx)
		}
	}

	if transactionAttempts > 0 {
		live.TPS = float64(live.TotalTx)
		live.ErrorPct = float64(live.TotalErrors) / float64(transactionAttempts) * 100
		live.AvgResponseMs /= float64(transactionAttempts)
	}

	names := make([]string, 0, len(durationsByName))
	allDurations := make([]float64, 0, len(batch.Transactions))
	for name, durations := range durationsByName {
		names = append(names, name)
		sort.Float64s(durations)
		allDurations = append(allDurations, durations...)
		finalizeDistributedTxMetrics(live.Transactions[name], durations)
	}
	for dependencyTarget, durations := range durationsByDependencyTarget {
		sort.Float64s(durations)
		finalizeDistributedTxMetrics(live.DependencyHealth[dependencyTarget], durations)
	}
	for operation, durations := range durationsBySOAPOperation {
		sort.Float64s(durations)
		finalizeDistributedTxMetrics(live.SOAPOperations[operation], durations)
	}
	for groupName, durations := range durationsByGroup {
		sort.Float64s(durations)
		metrics := groupAggregates[groupName]
		if metrics == nil {
			continue
		}
		attempts := metrics.Transactions + metrics.Errors
		if attempts > 0 {
			metrics.AvgResponseMs /= float64(attempts)
		}
		metrics.P50Ms, metrics.P90Ms, metrics.P95Ms, metrics.P99Ms = percentilesFromSorted(durations)
		digest := engine.NewTDigest(200)
		for _, duration := range durations {
			digest.Add(duration * 1_000_000)
		}
		digestSnapshot := digest.Snapshot()
		metrics.DurationDigest = &digestSnapshot
	}
	sort.Strings(names)
	if len(allDurations) > 0 {
		sort.Float64s(allDurations)
		live.P50Ms, live.P90Ms, live.P95Ms, live.P99Ms = percentilesFromSorted(allDurations)
	}
	if len(batch.Groups) > 0 {
		for _, group := range batch.Groups {
			groupMetrics := groupAggregates[group.Name]
			if groupMetrics == nil {
				groupMetrics = &GroupLiveMetrics{Name: group.Name}
			}
			groupMetrics.VUsers = int(group.ActiveVUsers)
			live.GroupMetrics[group.Name] = groupMetrics
		}
		for groupName, groupMetrics := range groupAggregates {
			if _, ok := live.GroupMetrics[groupName]; !ok {
				live.GroupMetrics[groupName] = groupMetrics
			}
		}
	} else if len(groupAggregates) > 0 {
		configuredVUsers := make(map[string]int, len(test.Groups))
		for _, group := range test.Groups {
			configuredVUsers[group.Name] = group.VUsers
		}
		for groupName, groupMetrics := range groupAggregates {
			groupMetrics.VUsers = configuredVUsers[groupName]
			live.GroupMetrics[groupName] = groupMetrics
		}
	} else if len(test.Groups) == 1 {
		groupName := test.Groups[0].Name
		live.GroupMetrics[groupName] = &GroupLiveMetrics{
			Name:          groupName,
			VUsers:        int(batch.Stats.ActiveVUsers),
			Transactions:  live.TotalTx,
			Errors:        live.TotalErrors,
			TPS:           live.TPS,
			AvgResponseMs: live.AvgResponseMs,
			P50Ms:         live.P50Ms,
			P90Ms:         live.P90Ms,
			P95Ms:         live.P95Ms,
			P99Ms:         live.P99Ms,
		}
	}

	return live
}

func mergeDistributedTransactionMetrics(metrics *TxMetrics, tx generatorrpc.TransactionMetric) {
	if metrics == nil {
		return
	}
	metrics.Count++
	if !tx.Success {
		metrics.Errors++
		metrics.LastErrorMessage = tx.ErrorMessage
		metrics.LastStatusCode = int(tx.StatusCode)
		if tx.StartTimeUnixMs > 0 {
			metrics.LastErrorAt = time.UnixMilli(tx.StartTimeUnixMs)
		} else {
			metrics.LastErrorAt = time.Now()
		}
	}
	mergeTransactionMetricMetadata(metrics, tx.DependencyType, tx.DependencyTarget, tx.CorrelationID)
	mergeDistributedServerTelemetry(metrics, tx.ServerTelemetry)
	metrics.AvgDNSMs += tx.DNSMs
	metrics.AvgConnectMs += tx.ConnectMs
	metrics.AvgTLSMs += tx.TLSMs
	metrics.AvgTTFBMs += tx.TTFBMs
	metrics.AvgMs += tx.DurationMs
}

func mergeTransactionMetricMetadata(metrics *TxMetrics, dependencyType string, dependencyTarget string, correlationID string) {
	if metrics == nil {
		return
	}
	mergeStableTxMetadata(&metrics.DependencyType, dependencyType)
	mergeStableTxMetadata(&metrics.DependencyTarget, dependencyTarget)
	if correlationID == "" {
		return
	}
	metrics.CorrelationCount++
	for _, existing := range metrics.CorrelationIDs {
		if existing == correlationID {
			return
		}
	}
	if len(metrics.CorrelationIDs) < 3 {
		metrics.CorrelationIDs = append(metrics.CorrelationIDs, correlationID)
	}
}

func mergeStableTxMetadata(current *string, next string) {
	if current == nil || next == "" {
		return
	}
	if *current == "" {
		*current = next
		return
	}
	if *current != next {
		*current = "multiple"
	}
}

func cloneEngineServerTelemetrySummary(summary *engine.ServerTelemetrySummary) *ServerTelemetrySummary {
	if summary == nil {
		return nil
	}
	return &ServerTelemetrySummary{
		Samples:                   summary.Samples,
		AvgServerProcessingMs:     summary.AvgServerProcessingMs,
		AvgTransferMs:             summary.AvgTransferMs,
		AvgRequestPayloadKB:       summary.AvgRequestPayloadKB,
		AvgResponsePayloadKB:      summary.AvgResponsePayloadKB,
		RequestPayloadHistogram:   cloneTelemetryClassCounts(summary.RequestPayloadHistogram),
		ResponsePayloadHistogram:  cloneTelemetryClassCounts(summary.ResponsePayloadHistogram),
		AvgFirstEventMs:           summary.AvgFirstEventMs,
		MaxFirstEventMs:           summary.MaxFirstEventMs,
		EventCount:                summary.EventCount,
		AvgMessageLatencyMs:       summary.AvgMessageLatencyMs,
		MaxMessageLatencyMs:       summary.MaxMessageLatencyMs,
		MessagesSent:              summary.MessagesSent,
		MessagesReceived:          summary.MessagesReceived,
		AvgMessagesSentPerSec:     summary.AvgMessagesSentPerSec,
		AvgMessagesReceivedPerSec: summary.AvgMessagesReceivedPerSec,
		AvgConsumerLagMs:          summary.AvgConsumerLagMs,
		MaxConsumerLagMs:          summary.MaxConsumerLagMs,
		RequestSizeClasses:        cloneTelemetryClassCounts(summary.RequestSizeClasses),
		ResponseSizeClasses:       cloneTelemetryClassCounts(summary.ResponseSizeClasses),
		ConnectionReusedCount:     summary.ConnectionReusedCount,
		ConnectionReusedPct:       summary.ConnectionReusedPct,
	}
}

func mergeDistributedServerTelemetry(metrics *TxMetrics, telemetry *generatorrpc.ServerTelemetry) {
	if metrics == nil || telemetry == nil {
		return
	}
	if metrics.ServerTelemetry == nil {
		metrics.ServerTelemetry = &ServerTelemetrySummary{}
	}
	metrics.ServerTelemetry.Samples++
	metrics.ServerTelemetry.AvgServerProcessingMs += telemetry.ServerProcessingMs
	metrics.ServerTelemetry.AvgTransferMs += telemetry.TransferMs
	metrics.ServerTelemetry.AvgRequestPayloadKB += telemetry.RequestPayloadKB
	metrics.ServerTelemetry.AvgResponsePayloadKB += telemetry.ResponsePayloadKB
	incrementTelemetryHistogramCount(&metrics.ServerTelemetry.RequestPayloadHistogram, telemetry.RequestPayloadKB)
	incrementTelemetryHistogramCount(&metrics.ServerTelemetry.ResponsePayloadHistogram, telemetry.ResponsePayloadKB)
	metrics.ServerTelemetry.AvgFirstEventMs += telemetry.FirstEventMs
	if telemetry.FirstEventMs > metrics.ServerTelemetry.MaxFirstEventMs {
		metrics.ServerTelemetry.MaxFirstEventMs = telemetry.FirstEventMs
	}
	metrics.ServerTelemetry.EventCount += int64(telemetry.EventCount)
	metrics.ServerTelemetry.AvgMessageLatencyMs += telemetry.MessageLatencyMs
	if telemetry.MessageLatencyMs > metrics.ServerTelemetry.MaxMessageLatencyMs {
		metrics.ServerTelemetry.MaxMessageLatencyMs = telemetry.MessageLatencyMs
	}
	metrics.ServerTelemetry.MessagesSent += int64(telemetry.MessagesSent)
	metrics.ServerTelemetry.MessagesReceived += int64(telemetry.MessagesReceived)
	metrics.ServerTelemetry.AvgMessagesSentPerSec += telemetry.MessagesSentPerSec
	metrics.ServerTelemetry.AvgMessagesReceivedPerSec += telemetry.MessagesReceivedPerSec
	metrics.ServerTelemetry.AvgConsumerLagMs += telemetry.ConsumerLagMs
	if telemetry.ConsumerLagMs > metrics.ServerTelemetry.MaxConsumerLagMs {
		metrics.ServerTelemetry.MaxConsumerLagMs = telemetry.ConsumerLagMs
	}
	incrementTelemetryClassCount(&metrics.ServerTelemetry.RequestSizeClasses, telemetry.RequestSizeClass)
	incrementTelemetryClassCount(&metrics.ServerTelemetry.ResponseSizeClasses, telemetry.ResponseSizeClass)
	if telemetry.ConnectionReused {
		metrics.ServerTelemetry.ConnectionReusedCount++
	}
}

func finalizeAPIServerTelemetrySummary(summary *ServerTelemetrySummary) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgServerProcessingMs /= divisor
	summary.AvgTransferMs /= divisor
	summary.AvgRequestPayloadKB /= divisor
	summary.AvgResponsePayloadKB /= divisor
	summary.AvgFirstEventMs /= divisor
	summary.AvgMessageLatencyMs /= divisor
	summary.AvgMessagesSentPerSec /= divisor
	summary.AvgMessagesReceivedPerSec /= divisor
	summary.AvgConsumerLagMs /= divisor
	summary.ConnectionReusedPct = float64(summary.ConnectionReusedCount) / divisor * 100
	if len(summary.RequestSizeClasses) == 0 {
		summary.RequestSizeClasses = nil
	}
	if len(summary.ResponseSizeClasses) == 0 {
		summary.ResponseSizeClasses = nil
	}
	if len(summary.RequestPayloadHistogram) == 0 {
		summary.RequestPayloadHistogram = nil
	}
	if len(summary.ResponsePayloadHistogram) == 0 {
		summary.ResponsePayloadHistogram = nil
	}
}

func cloneTelemetryClassCounts(in map[string]int64) map[string]int64 {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int64, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func incrementTelemetryHistogramCount(values *map[string]int64, payloadKB float64) {
	if values == nil {
		return
	}
	key := payloadHistogramBucket(payloadKB)
	if key == "" {
		return
	}
	if *values == nil {
		*values = make(map[string]int64)
	}
	(*values)[key]++
}

func payloadHistogramBucket(payloadKB float64) string {
	switch {
	case payloadKB <= 0:
		return ""
	case payloadKB < 1:
		return "0-1 KB"
	case payloadKB < 4:
		return "1-4 KB"
	case payloadKB < 16:
		return "4-16 KB"
	case payloadKB < 64:
		return "16-64 KB"
	default:
		return "64+ KB"
	}
}

func incrementTelemetryClassCount(values *map[string]int64, key string) {
	if values == nil || key == "" {
		return
	}
	if *values == nil {
		*values = make(map[string]int64)
	}
	(*values)[key]++
}

func percentileValue(sorted []float64, percentile float64) float64 {
	return percentileFromSortedSamples(sorted, percentile)
}

// percentilesFromSorted returns the P50/P90/P95/P99 of a pre-sorted sample set,
// keeping the percentile set defined in a single place.
func percentilesFromSorted(sorted []float64) (p50, p90, p95, p99 float64) {
	return percentileValue(sorted, 0.50), percentileValue(sorted, 0.90), percentileValue(sorted, 0.95), percentileValue(sorted, 0.99)
}

// finalizeDistributedTxMetrics averages the per-attempt accumulators by Count,
// finalizes the server-telemetry summary, and assigns percentiles from the
// already-sorted durations. Shared by the per-transaction, per-dependency-target
// and per-SOAP-operation finalize loops.
func finalizeDistributedTxMetrics(metrics *TxMetrics, sortedDurations []float64) {
	metrics.AvgDNSMs /= float64(metrics.Count)
	metrics.AvgConnectMs /= float64(metrics.Count)
	metrics.AvgTLSMs /= float64(metrics.Count)
	metrics.AvgTTFBMs /= float64(metrics.Count)
	metrics.AvgMs /= float64(metrics.Count)
	finalizeAPIServerTelemetrySummary(metrics.ServerTelemetry)
	metrics.P50Ms, metrics.P90Ms, metrics.P95Ms, metrics.P99Ms = percentilesFromSorted(sortedDurations)
}
