package api

// report_aggregate — split from archive.go (same package, behavior-preserving).

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func mergeRunArchiveTelemetrySummary(summary *RunArchiveTelemetrySummary, point IntervalSummaryRecord) {
	if summary == nil || (point.Telemetry == nil && point.TelemetryStatus == nil) {
		return
	}
	summary.Samples++
	if point.Telemetry != nil {
		summary.DroppedResults += point.Telemetry.DroppedResults
		summary.WebsocketDroppedMessages += point.Telemetry.WebsocketDroppedMessages
		summary.WebsocketDroppedEvents += point.Telemetry.WebsocketDroppedEvents
		if point.Telemetry.SampleAgeMs > summary.MaxSampleAgeMs {
			summary.MaxSampleAgeMs = point.Telemetry.SampleAgeMs
		}
		if point.Telemetry.GeneratorProcessingLagMs > summary.MaxGeneratorProcessingLagMs {
			summary.MaxGeneratorProcessingLagMs = point.Telemetry.GeneratorProcessingLagMs
		}
		if point.Telemetry.TransportLagMs > summary.MaxTransportLagMs {
			summary.MaxTransportLagMs = point.Telemetry.TransportLagMs
		}
		if point.Telemetry.ControllerFanoutLagMs > summary.MaxControllerFanoutLagMs {
			summary.MaxControllerFanoutLagMs = point.Telemetry.ControllerFanoutLagMs
		}
		if point.Telemetry.BrowserRenderLagMs > summary.MaxBrowserRenderLagMs {
			summary.MaxBrowserRenderLagMs = point.Telemetry.BrowserRenderLagMs
		}
	}
	if point.TelemetryStatus == nil {
		return
	}
	switch point.TelemetryStatus.Status {
	case "breached":
		summary.BreachedIntervals++
		recordTelemetryBreachTime(summary, point.Timestamp)
	case "degraded":
		summary.DegradedIntervals++
	case "healthy":
		summary.HealthyIntervals++
	}
	if point.TelemetryStatus.SustainedBreach {
		summary.SustainedBreachIntervals++
	}
	if len(point.TelemetryStatus.Conditions) > 0 && summary.ConditionCounts == nil {
		summary.ConditionCounts = map[string]int64{}
	}
	for _, condition := range point.TelemetryStatus.Conditions {
		if condition == "" {
			continue
		}
		summary.ConditionCounts[condition]++
	}
}

type networkPhaseAccumulator struct {
	count        int64
	dnsTotal     float64
	connectTotal float64
	tlsTotal     float64
	ttfbTotal    float64
	maxDNS       float64
	maxConnect   float64
	maxTLS       float64
	maxTTFB      float64
}

func buildRunArchiveNetworkPhaseSummary(transactions []TransactionEvent) RunArchiveNetworkPhaseSummary {
	accumulator := newRunArchiveNetworkPhaseAccumulator()
	for _, tx := range transactions {
		accumulator.merge(tx)
	}
	return accumulator.finalize()
}

type runArchiveNetworkPhaseAccumulator struct {
	overall      *networkPhaseAccumulator
	byAction     map[string]*networkPhaseAccumulator
	byDependency map[string]*networkPhaseAccumulator
}

func newRunArchiveNetworkPhaseAccumulator() *runArchiveNetworkPhaseAccumulator {
	return &runArchiveNetworkPhaseAccumulator{
		overall:      &networkPhaseAccumulator{},
		byAction:     map[string]*networkPhaseAccumulator{},
		byDependency: map[string]*networkPhaseAccumulator{},
	}
}

func (a *runArchiveNetworkPhaseAccumulator) merge(tx TransactionEvent) {
	// Skip rows with no positive phase measurement. Using <= 0 also discards any
	// negative "unmeasured" sentinel a future importer/protocol might emit, so it
	// cannot pull the averages below the real values.
	if tx.DNSMs <= 0 && tx.ConnectMs <= 0 && tx.TLSMs <= 0 && tx.TTFBMs <= 0 {
		return
	}
	mergeNetworkPhaseAccumulator(a.overall, tx)
	if tx.ActionName != "" {
		acc := a.byAction[tx.ActionName]
		if acc == nil {
			acc = &networkPhaseAccumulator{}
			a.byAction[tx.ActionName] = acc
		}
		mergeNetworkPhaseAccumulator(acc, tx)
	}
	if tx.DependencyTarget != "" {
		key := tx.DependencyTarget
		if tx.DependencyType != "" {
			key = tx.DependencyType + ":" + key
		}
		acc := a.byDependency[key]
		if acc == nil {
			acc = &networkPhaseAccumulator{}
			a.byDependency[key] = acc
		}
		mergeNetworkPhaseAccumulator(acc, tx)
	}
}

func (a *runArchiveNetworkPhaseAccumulator) finalize() RunArchiveNetworkPhaseSummary {
	if a == nil || a.overall.count == 0 {
		return RunArchiveNetworkPhaseSummary{}
	}
	summary := RunArchiveNetworkPhaseSummary{
		Samples:  a.overall.count,
		Overall:  finalizeNetworkPhaseAccumulator(a.overall),
		ByAction: finalizeNetworkPhaseAccumulatorMap(a.byAction),
	}
	if dependencies := finalizeNetworkPhaseAccumulatorMap(a.byDependency); len(dependencies) > 0 {
		summary.ByDependency = dependencies
	}
	return summary
}

func mergeNetworkPhaseAccumulator(acc *networkPhaseAccumulator, tx TransactionEvent) {
	if acc == nil {
		return
	}
	// Clamp negative sentinels to zero so an "unmeasured" phase neither drags the
	// running average below the real value nor leaves the max pinned at zero.
	dns := clampNonNegative(tx.DNSMs)
	connect := clampNonNegative(tx.ConnectMs)
	tls := clampNonNegative(tx.TLSMs)
	ttfb := clampNonNegative(tx.TTFBMs)
	acc.count++
	acc.dnsTotal += dns
	acc.connectTotal += connect
	acc.tlsTotal += tls
	acc.ttfbTotal += ttfb
	if dns > acc.maxDNS {
		acc.maxDNS = dns
	}
	if connect > acc.maxConnect {
		acc.maxConnect = connect
	}
	if tls > acc.maxTLS {
		acc.maxTLS = tls
	}
	if ttfb > acc.maxTTFB {
		acc.maxTTFB = ttfb
	}
}

func clampNonNegative(value float64) float64 {
	if value < 0 {
		return 0
	}
	return value
}

func finalizeNetworkPhaseAccumulator(acc *networkPhaseAccumulator) RunArchiveNetworkPhases {
	if acc == nil || acc.count == 0 {
		return RunArchiveNetworkPhases{}
	}
	divisor := float64(acc.count)
	return RunArchiveNetworkPhases{
		Count:        acc.count,
		AvgDNSMs:     acc.dnsTotal / divisor,
		AvgConnectMs: acc.connectTotal / divisor,
		AvgTLSMs:     acc.tlsTotal / divisor,
		AvgTTFBMs:    acc.ttfbTotal / divisor,
		MaxDNSMs:     acc.maxDNS,
		MaxConnectMs: acc.maxConnect,
		MaxTLSMs:     acc.maxTLS,
		MaxTTFBMs:    acc.maxTTFB,
	}
}

func finalizeNetworkPhaseAccumulatorMap(values map[string]*networkPhaseAccumulator) map[string]RunArchiveNetworkPhases {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]RunArchiveNetworkPhases, len(values))
	for key, acc := range values {
		finalized := finalizeNetworkPhaseAccumulator(acc)
		if finalized.Count > 0 {
			out[key] = finalized
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func buildRunArchiveArtifactInventory(record *storage.RunArchiveRecord, manifest *RunArchiveManifest) []RunArchiveArtifact {
	if manifest == nil {
		return nil
	}
	if len(manifest.Artifacts) > 0 {
		return append([]RunArchiveArtifact(nil), manifest.Artifacts...)
	}
	if record == nil || strings.TrimSpace(record.ArchivePath) == "" {
		return nil
	}
	descriptors := []RunArchiveArtifact{
		{Name: manifest.TransactionsFile, Kind: "dataset", Format: "jsonl", RecordType: "transactions", Count: manifest.TransactionCount},
		{Name: manifest.TransactionsParquetFile, Kind: "dataset", Format: "parquet", RecordType: "transactions", Count: manifest.TransactionCount},
		{Name: manifest.ResourceSamplesFile, Kind: "dataset", Format: "jsonl", RecordType: "resource_samples", Count: manifest.ResourceSampleCount},
		{Name: manifest.ResourceSamplesParquetFile, Kind: "dataset", Format: "parquet", RecordType: "resource_samples", Count: manifest.ResourceSampleCount},
		{Name: manifest.IntervalSummariesFile, Kind: "dataset", Format: "jsonl", RecordType: "interval_summaries", Count: manifest.IntervalSummaryCount},
		{Name: manifest.IntervalSummariesParquetFile, Kind: "dataset", Format: "parquet", RecordType: "interval_summaries", Count: manifest.IntervalSummaryCount},
		{Name: manifest.TestManifestFile, Kind: "manifest", Format: "json", RecordType: "test_manifest"},
		{Name: manifest.TestNotesFile, Kind: "notes", Format: "json", RecordType: "test_notes"},
	}
	out := make([]RunArchiveArtifact, 0, len(descriptors))
	for _, artifact := range descriptors {
		if strings.TrimSpace(artifact.Name) == "" {
			continue
		}
		path, err := archiveArtifactPath(record.ArchivePath, artifact.Name)
		if err != nil {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		artifact.SizeBytes = info.Size()
		out = append(out, artifact)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func recordTelemetryBreachTime(summary *RunArchiveTelemetrySummary, timestamp time.Time) {
	if summary == nil || timestamp.IsZero() {
		return
	}
	if summary.FirstBreachAt == nil || timestamp.Before(*summary.FirstBreachAt) {
		first := timestamp
		summary.FirstBreachAt = &first
	}
	if summary.LastBreachAt == nil || timestamp.After(*summary.LastBreachAt) {
		last := timestamp
		summary.LastBreachAt = &last
	}
}

func finalizeRunArchiveTelemetrySummary(summary RunArchiveTelemetrySummary) *RunArchiveTelemetrySummary {
	if summary.BreachedIntervals > 0 {
		summary.Status = "breached"
	} else if summary.DegradedIntervals > 0 {
		summary.Status = "degraded"
	} else {
		summary.Status = "healthy"
	}
	if len(summary.ConditionCounts) == 0 {
		summary.ConditionCounts = nil
	}
	return &summary
}

func archiveErrorPatternSource(tx TransactionEvent) string {
	if message := strings.TrimSpace(tx.ErrorMessage); message != "" {
		return message
	}
	if tx.StatusCode > 0 {
		return fmt.Sprintf("request failed with status %d", tx.StatusCode)
	}
	return "request failed"
}

func mergeArchiveErrorPattern(patterns map[string]*RunArchiveErrorPattern, actions map[string]map[string]struct{}, tx TransactionEvent) {
	source := archiveErrorPatternSource(tx)
	signature := normalizeArchiveErrorSignature(source)
	pattern, ok := patterns[signature]
	if !ok {
		pattern = &RunArchiveErrorPattern{
			Signature:  signature,
			Sample:     source,
			FirstSeen:  tx.Timestamp,
			LatestSeen: tx.Timestamp,
		}
		if tx.Snapshot != nil {
			pattern.Snapshot = tx.Snapshot
		}
		patterns[signature] = pattern
	}
	pattern.Count++
	if pattern.FirstSeen.IsZero() || (!tx.Timestamp.IsZero() && tx.Timestamp.Before(pattern.FirstSeen)) {
		pattern.FirstSeen = tx.Timestamp
	}
	if tx.Timestamp.After(pattern.LatestSeen) {
		pattern.LatestSeen = tx.Timestamp
	}
	if pattern.Snapshot == nil && tx.Snapshot != nil {
		pattern.Snapshot = tx.Snapshot
	}
	if tx.ActionName != "" {
		if actions[signature] == nil {
			actions[signature] = make(map[string]struct{})
		}
		actions[signature][tx.ActionName] = struct{}{}
	}
}

func archiveSOAPOperation(tx TransactionEvent) string {
	if len(tx.Extractions) == 0 {
		return ""
	}
	return strings.TrimSpace(tx.Extractions["soap_operation"])
}

func archiveSOAPFaultCode(tx TransactionEvent) string {
	if tx.ServerTelemetry == nil {
		return ""
	}
	return strings.TrimSpace(tx.ServerTelemetry.SOAPFaultCode)
}

func archiveSOAPFaultCategory(tx TransactionEvent) string {
	if tx.ServerTelemetry == nil {
		return ""
	}
	return strings.TrimSpace(tx.ServerTelemetry.SOAPFaultCategory)
}

func incrementArchiveCount(values map[string]int64, key string) {
	if values == nil || key == "" {
		return
	}
	values[key]++
}

func mergeArchiveSOAPFaultTimelineBucket(values map[time.Time]*RunArchiveSOAPFaultTimelineBucket, tx TransactionEvent) {
	if len(values) == 0 && values == nil {
		return
	}
	bucketTime := archiveSOAPFaultBucketTimestamp(tx.Timestamp)
	if bucketTime.IsZero() {
		return
	}
	faultCode := archiveSOAPFaultCode(tx)
	faultCategory := archiveSOAPFaultCategory(tx)
	if faultCode == "" && faultCategory == "" {
		return
	}
	bucket, ok := values[bucketTime]
	if !ok {
		bucket = &RunArchiveSOAPFaultTimelineBucket{
			Timestamp:  bucketTime,
			ByCode:     make(map[string]int64),
			ByCategory: make(map[string]int64),
		}
		values[bucketTime] = bucket
	}
	bucket.Total++
	incrementArchiveCount(bucket.ByCode, faultCode)
	incrementArchiveCount(bucket.ByCategory, faultCategory)
}

func archiveSOAPFaultBucketTimestamp(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.Truncate(time.Minute)
}

func mergeArchiveSOAPPayloadTimelineBucket(values map[time.Time]*RunArchiveSOAPPayloadTimelineBucket, tx TransactionEvent) {
	if values == nil {
		return
	}
	bucketTime := archiveSOAPPayloadBucketTimestamp(tx.Timestamp)
	if bucketTime.IsZero() {
		return
	}
	requestPayloadKB, responsePayloadKB, envelopeOverheadKB := archiveSOAPPayloadTelemetry(tx)
	if requestPayloadKB == 0 && responsePayloadKB == 0 && envelopeOverheadKB == 0 {
		return
	}
	bucket, ok := values[bucketTime]
	if !ok {
		bucket = &RunArchiveSOAPPayloadTimelineBucket{Timestamp: bucketTime}
		values[bucketTime] = bucket
	}
	bucket.Samples++
	bucket.AvgRequestPayloadKB += requestPayloadKB
	bucket.AvgResponsePayloadKB += responsePayloadKB
	bucket.AvgEnvelopeOverheadKB += envelopeOverheadKB
	if envelopeOverheadKB > bucket.MaxEnvelopeOverheadKB {
		bucket.MaxEnvelopeOverheadKB = envelopeOverheadKB
	}
}

func archiveSOAPPayloadBucketTimestamp(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.Truncate(time.Minute)
}

func archiveSOAPPayloadTelemetry(tx TransactionEvent) (float64, float64, float64) {
	if tx.ServerTelemetry == nil || (archiveSOAPOperation(tx) == "" && tx.ServerTelemetry.EnvelopeOverheadKB == 0 && archiveSOAPFaultCode(tx) == "") {
		return 0, 0, 0
	}
	return tx.ServerTelemetry.RequestPayloadKB, tx.ServerTelemetry.ResponsePayloadKB, tx.ServerTelemetry.EnvelopeOverheadKB
}

func finalizeArchiveSOAPPayloadTimelineBucket(bucket RunArchiveSOAPPayloadTimelineBucket) RunArchiveSOAPPayloadTimelineBucket {
	if bucket.Samples == 0 {
		return bucket
	}
	divisor := float64(bucket.Samples)
	bucket.AvgRequestPayloadKB /= divisor
	bucket.AvgResponsePayloadKB /= divisor
	bucket.AvgEnvelopeOverheadKB /= divisor
	return bucket
}

func mergeRunArchiveActionStats(stats RunArchiveActionStats, quantiles *archiveQuantileAccumulator, tx TransactionEvent) RunArchiveActionStats {
	if tx.ServerTelemetry != nil {
		protocol.MergeGRPCDiagnostic(&stats.GRPC, tx.ServerTelemetry.GRPC)
		protocol.MergeWebSocketDiagnostic(&stats.WebSocket, tx.ServerTelemetry.WebSocket)
	}
	stats.Transactions++
	stats.AvgResponseMs += tx.DurationMs
	quantiles.Add(tx.DurationMs)
	if !tx.Success {
		stats.Errors++
	}
	if stats.Transactions == 1 || tx.DurationMs < stats.MinResponseMs {
		stats.MinResponseMs = tx.DurationMs
	}
	if tx.DurationMs > stats.MaxResponseMs {
		stats.MaxResponseMs = tx.DurationMs
	}
	return stats
}

func finalizeRunArchiveActionStats(stats RunArchiveActionStats, quantiles *archiveQuantileAccumulator) RunArchiveActionStats {
	stats.DiagnosticFindings = protocol.ProtocolDiagnosticFindings(stats.GRPC, stats.WebSocket)
	if stats.Transactions > 0 {
		stats.ErrorPct = float64(stats.Errors) / float64(stats.Transactions) * 100
		stats.AvgResponseMs = stats.AvgResponseMs / float64(stats.Transactions)
		if quantiles != nil {
			stats.P50Ms = quantiles.Quantile(0.50)
			stats.P90Ms = quantiles.Quantile(0.90)
			stats.P95Ms = quantiles.Quantile(0.95)
			stats.P99Ms = quantiles.Quantile(0.99)
		}
	}
	stats.durations = nil
	return stats
}

func mergeReportBrowserMetrics(summary *BrowserLiveMetrics, metrics *protocol.ClientMetrics) {
	if summary == nil || metrics == nil {
		return
	}
	summary.Samples++
	summary.AvgFCPMs += metrics.FCPMs
	summary.AvgLCPMs += metrics.LCPMs
	summary.AvgCLSScore += metrics.CLSScore
	summary.AvgINPMs += metrics.INPMs
	summary.AvgTTFBMs += metrics.TTFBMs
	summary.AvgBlockingResources += float64(metrics.BlockingResourceCount)
	summary.AvgLongTaskCount += float64(metrics.LongTaskCount)
	summary.AvgJSHeapUsedMB += metrics.JSHeapUsedMB
	summary.AvgBrowserProcessCPU += metrics.BrowserProcessCPUPct
	summary.AvgBrowserProcessMem += metrics.BrowserProcessMemMB
}

func finalizeReportBrowserMetrics(summary *BrowserLiveMetrics) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgFCPMs /= divisor
	summary.AvgLCPMs /= divisor
	summary.AvgCLSScore /= divisor
	summary.AvgINPMs /= divisor
	summary.AvgTTFBMs /= divisor
	summary.AvgBlockingResources /= divisor
	summary.AvgLongTaskCount /= divisor
	summary.AvgJSHeapUsedMB /= divisor
	summary.AvgBrowserProcessCPU /= divisor
	summary.AvgBrowserProcessMem /= divisor
}

func mergeReportBrowserDeviceMetrics(summary *BrowserDeviceLiveMetrics, metrics *protocol.ClientMetrics) {
	if summary == nil || metrics == nil {
		return
	}
	if summary.BrowserEngine == "" {
		summary.BrowserEngine = metrics.BrowserEngine
	}
	if summary.DevicePlatform == "" {
		summary.DevicePlatform = metrics.DevicePlatform
	}
	summary.Samples++
	summary.AvgFCPMs += metrics.FCPMs
	summary.AvgLCPMs += metrics.LCPMs
	summary.AvgCLSScore += metrics.CLSScore
	summary.AvgINPMs += metrics.INPMs
	summary.AvgTTFBMs += metrics.TTFBMs
	summary.AvgBlockingResources += float64(metrics.BlockingResourceCount)
	summary.AvgLongTaskCount += float64(metrics.LongTaskCount)
	summary.AvgJSHeapUsedMB += metrics.JSHeapUsedMB
	summary.AvgBrowserProcessCPU += metrics.BrowserProcessCPUPct
	summary.AvgBrowserProcessMem += metrics.BrowserProcessMemMB
}

func mergeReportBrowserPlatformMetrics(summary *BrowserPlatformLiveMetrics, metrics *protocol.ClientMetrics) {
	if summary == nil || metrics == nil {
		return
	}
	if summary.BrowserEngine == "" {
		summary.BrowserEngine = metrics.BrowserEngine
	} else if metrics.BrowserEngine != "" && summary.BrowserEngine != metrics.BrowserEngine {
		summary.BrowserEngine = "multiple"
	}
	summary.Samples++
	summary.AvgFCPMs += metrics.FCPMs
	summary.AvgLCPMs += metrics.LCPMs
	summary.AvgCLSScore += metrics.CLSScore
	summary.AvgINPMs += metrics.INPMs
	summary.AvgTTFBMs += metrics.TTFBMs
	summary.AvgBlockingResources += float64(metrics.BlockingResourceCount)
	summary.AvgLongTaskCount += float64(metrics.LongTaskCount)
	summary.AvgJSHeapUsedMB += metrics.JSHeapUsedMB
	summary.AvgBrowserProcessCPU += metrics.BrowserProcessCPUPct
	summary.AvgBrowserProcessMem += metrics.BrowserProcessMemMB
}

func mergeReportBrowserEngineMetrics(summary *BrowserEngineLiveMetrics, metrics *protocol.ClientMetrics) {
	if summary == nil || metrics == nil {
		return
	}
	summary.Samples++
	summary.AvgFCPMs += metrics.FCPMs
	summary.AvgLCPMs += metrics.LCPMs
	summary.AvgCLSScore += metrics.CLSScore
	summary.AvgINPMs += metrics.INPMs
	summary.AvgTTFBMs += metrics.TTFBMs
	summary.AvgBlockingResources += float64(metrics.BlockingResourceCount)
	summary.AvgLongTaskCount += float64(metrics.LongTaskCount)
	summary.AvgJSHeapUsedMB += metrics.JSHeapUsedMB
	summary.AvgBrowserProcessCPU += metrics.BrowserProcessCPUPct
	summary.AvgBrowserProcessMem += metrics.BrowserProcessMemMB
}

func finalizeReportBrowserEngineMetrics(summary *BrowserEngineLiveMetrics) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgFCPMs /= divisor
	summary.AvgLCPMs /= divisor
	summary.AvgCLSScore /= divisor
	summary.AvgINPMs /= divisor
	summary.AvgTTFBMs /= divisor
	summary.AvgBlockingResources /= divisor
	summary.AvgLongTaskCount /= divisor
	summary.AvgJSHeapUsedMB /= divisor
	summary.AvgBrowserProcessCPU /= divisor
	summary.AvgBrowserProcessMem /= divisor
}

func finalizeReportBrowserPlatformMetrics(summary *BrowserPlatformLiveMetrics) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgFCPMs /= divisor
	summary.AvgLCPMs /= divisor
	summary.AvgCLSScore /= divisor
	summary.AvgINPMs /= divisor
	summary.AvgTTFBMs /= divisor
	summary.AvgBlockingResources /= divisor
	summary.AvgLongTaskCount /= divisor
	summary.AvgJSHeapUsedMB /= divisor
	summary.AvgBrowserProcessCPU /= divisor
	summary.AvgBrowserProcessMem /= divisor
}

func finalizeReportBrowserDeviceMetrics(summary *BrowserDeviceLiveMetrics) {
	if summary == nil || summary.Samples == 0 {
		return
	}
	divisor := float64(summary.Samples)
	summary.AvgFCPMs /= divisor
	summary.AvgLCPMs /= divisor
	summary.AvgCLSScore /= divisor
	summary.AvgINPMs /= divisor
	summary.AvgTTFBMs /= divisor
	summary.AvgBlockingResources /= divisor
	summary.AvgLongTaskCount /= divisor
	summary.AvgJSHeapUsedMB /= divisor
	summary.AvgBrowserProcessCPU /= divisor
	summary.AvgBrowserProcessMem /= divisor
}
