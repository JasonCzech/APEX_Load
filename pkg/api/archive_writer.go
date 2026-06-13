package api

// writer — split from archive.go (same package, behavior-preserving).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newRunArchiveWriter(test *ManagedTest, provenance RunArchiveProvenance) (*runArchiveWriter, error) {
	rootDir := resolveArchiveRoot()
	archiveDir := filepath.Join(rootDir, test.ID)
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return nil, fmt.Errorf("create archive dir: %w", err)
	}

	open := func(name string) (*os.File, *json.Encoder, error) {
		file, err := os.Create(filepath.Join(archiveDir, name))
		if err != nil {
			return nil, nil, err
		}
		return file, json.NewEncoder(file), nil
	}

	txFile, txEncoder, err := open("transactions.jsonl")
	if err != nil {
		return nil, fmt.Errorf("open transactions archive: %w", err)
	}
	resourceFile, resourceEncoder, err := open("resources.jsonl")
	if err != nil {
		txFile.Close()
		return nil, fmt.Errorf("open resources archive: %w", err)
	}
	summaryFile, summaryEncoder, err := open("interval_summaries.jsonl")
	if err != nil {
		txFile.Close()
		resourceFile.Close()
		return nil, fmt.Errorf("open summaries archive: %w", err)
	}

	return &runArchiveWriter{
		manifest: RunArchiveManifest{
			SchemaVersion:                archiveSchemaVersion,
			TestID:                       test.ID,
			TestName:                     test.Name,
			CreatedAt:                    time.Now(),
			Provenance:                   provenance,
			TransactionsFile:             "transactions.jsonl",
			TransactionsParquetFile:      "transactions.parquet",
			ResourceSamplesFile:          "resources.jsonl",
			ResourceSamplesParquetFile:   "resources.parquet",
			IntervalSummariesFile:        "interval_summaries.jsonl",
			IntervalSummariesParquetFile: "interval_summaries.parquet",
		},
		region:          strings.TrimSpace(os.Getenv("APEXLOAD_REGION")),
		rootDir:         archiveDir,
		manifestPath:    filepath.Join(archiveDir, "manifest.json"),
		txFile:          txFile,
		resourceFile:    resourceFile,
		summaryFile:     summaryFile,
		txEncoder:       txEncoder,
		resourceEncoder: resourceEncoder,
		summaryEncoder:  summaryEncoder,
	}, nil
}

func captureAPEXLoadEnv() map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !strings.HasPrefix(key, "APEXLOAD_") {
			continue
		}
		upper := strings.ToUpper(key)
		if strings.Contains(upper, "SECRET") || strings.Contains(upper, "TOKEN") || strings.Contains(upper, "PASSWORD") || strings.Contains(upper, "KEY") {
			continue
		}
		env[key] = value
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func (w *runArchiveWriter) RecordResult(result *protocol.Result) error {
	if result == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	event := TransactionEvent{
		Timestamp:         result.StartTime,
		TestID:            w.manifest.TestID,
		TestName:          w.manifest.TestName,
		Group:             result.Group,
		Region:            firstNonEmpty(result.Region, w.region),
		ActionName:        result.ActionName,
		CorrelationID:     result.CorrelationID,
		TraceID:           traceIDFromCorrelationID(result.CorrelationID),
		SpanID:            spanIDFromCorrelationID(result.CorrelationID),
		NetworkProfile:    result.NetworkProfile,
		DependencyType:    result.DependencyType,
		DependencyTarget:  result.DependencyTarget,
		DurationMs:        durationMillis(result.Duration),
		DNSMs:             durationMillis(result.DNSTime),
		ConnectMs:         durationMillis(result.ConnectTime),
		TLSMs:             durationMillis(result.TLSTime),
		TTFBMs:            durationMillis(result.TTFB),
		BytesSent:         result.BytesSent,
		BytesReceived:     result.BytesReceived,
		StatusCode:        result.StatusCode,
		Success:           result.Success,
		ErrorMessage:      result.ErrorMessage,
		Extractions:       result.Extractions,
		PageURL:           result.PageURL,
		PageTitle:         result.PageTitle,
		ConsoleEntries:    cloneProtocolConsoleEntries(result.ConsoleEntries),
		NetworkEntries:    cloneProtocolNetworkEntries(result.NetworkEntries),
		DOMSummary:        cloneProtocolDOMSummary(result.DOMSummary),
		ClientMetrics:     result.ClientMetrics,
		ServerTelemetry:   result.ServerTelemetry,
		ProtocolEvidence:  cloneAnyMap(result.ProtocolEvidence),
		Snapshot:          result.Snapshot,
		SourceStepID:      result.SourceStepID,
		SourceStepIndex:   result.SourceStepIndex,
		SourceAction:      result.SourceAction,
		SourceActionName:  result.SourceActionName,
		SourceTransaction: result.SourceTransaction,
		SourceProfile:     result.SourceProfile,
	}
	if err := w.txEncoder.Encode(event); err != nil {
		return err
	}
	w.manifest.TransactionCount++
	return nil
}

func (w *runArchiveWriter) RecordResourceSample(timestamp time.Time, activeVUsers int64, resources *engine.SystemResources) error {
	if resources == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	record := ResourceSample{
		Timestamp:                         timestamp,
		TestID:                            w.manifest.TestID,
		TestName:                          w.manifest.TestName,
		ActiveVUsers:                      activeVUsers,
		CPUProcessPct:                     resources.CPUProcessPct,
		CPUFreePct:                        resources.CPUFreePct,
		MemProcessMB:                      resources.MemProcessMb,
		MemFreeMB:                         resources.MemFreeMb,
		NetSentKbps:                       resources.NetSentKbps,
		NetRecvKbps:                       resources.NetRecvKbps,
		DiskReadKbps:                      resources.DiskReadKbps,
		DiskWriteKbps:                     resources.DiskWriteKbps,
		CPUUserPct:                        resources.CPUUserPct,
		CPUSystemPct:                      resources.CPUSystemPct,
		CPUIOWaitPct:                      resources.CPUIOWaitPct,
		CPUStealPct:                       resources.CPUStealPct,
		Load1:                             resources.Load1,
		Load5:                             resources.Load5,
		Load15:                            resources.Load15,
		ThreadCount:                       resources.ThreadCount,
		OpenFDCount:                       resources.OpenFDCount,
		GoroutineCount:                    resources.GoroutineCount,
		HeapAllocMB:                       resources.HeapAllocMB,
		HeapInUseMB:                       resources.HeapInUseMB,
		GCCountDelta:                      resources.GCCountDelta,
		GCPauseMs:                         resources.GCPauseMs,
		ContainerCPULimitCores:            resources.ContainerCPULimitCores,
		ContainerCPUUsagePct:              resources.ContainerCPUUsagePct,
		ContainerCPUThrottledPeriodsDelta: resources.ContainerCPUThrottledPeriodsDelta,
		ContainerCPUThrottledSecondsRate:  resources.ContainerCPUThrottledSecondsRate,
		ContainerMemoryWorkingSetMB:       resources.ContainerMemoryWorkingSetMB,
		ContainerMemoryLimitMB:            resources.ContainerMemoryLimitMB,
		ContainerMemoryUsagePct:           resources.ContainerMemoryUsagePct,
		ContainerMemoryFailCountDelta:     resources.ContainerMemoryFailCountDelta,
		ContainerOOMKillCountDelta:        resources.ContainerOOMKillCountDelta,
		NetDropInPerSec:                   resources.NetDropInPerSec,
		NetDropOutPerSec:                  resources.NetDropOutPerSec,
		NetErrorInPerSec:                  resources.NetErrorInPerSec,
		NetErrorOutPerSec:                 resources.NetErrorOutPerSec,
		TCPRetransmitsPerSec:              resources.TCPRetransmitsPerSec,
		TCPEstablished:                    resources.TCPEstablished,
		TCPTimeWait:                       resources.TCPTimeWait,
		EphemeralPortUtilizationPct:       resources.EphemeralPortUtilizationPct,
		DiskIOTimePct:                     resources.DiskIOTimePct,
		DiskWeightedIOTimePct:             resources.DiskWeightedIOTimePct,
		DiskInProgress:                    resources.DiskInProgress,
		External:                          cloneFloat64Map(resources.External),
	}
	if err := w.resourceEncoder.Encode(record); err != nil {
		return err
	}
	w.manifest.ResourceSampleCount++
	return nil
}

func (w *runArchiveWriter) RecordIntervalSummary(live *LiveMetrics) error {
	if live == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	record := IntervalSummaryRecord{
		Timestamp:              live.Timestamp,
		TestID:                 w.manifest.TestID,
		TestName:               w.manifest.TestName,
		ActiveVUsers:           live.ActiveVUsers,
		TPS:                    live.TPS,
		TargetTPS:              live.TargetTPS,
		ThroughputGapPct:       live.ThroughputGapPct,
		ThroughputTargetMissed: live.ThroughputTargetMissed,
		AvgResponseMs:          live.AvgResponseMs,
		ErrorPct:               live.ErrorPct,
		TotalTx:                live.TotalTx,
		TotalErrors:            live.TotalErrors,
		P50Ms:                  live.P50Ms,
		P90Ms:                  live.P90Ms,
		P95Ms:                  live.P95Ms,
		P99Ms:                  live.P99Ms,
		Groups:                 cloneGroupLiveMetricsMap(live.GroupMetrics),
		Browser:                cloneBrowserLiveMetrics(live.Browser),
		BrowserByDevice:        cloneBrowserByDeviceMetrics(live.BrowserByDevice),
		Telemetry:              cloneTelemetryHealth(live.Telemetry),
		TelemetryStatus:        cloneTelemetryRunHealth(live.TelemetryStatus),
	}
	if err := w.summaryEncoder.Encode(record); err != nil {
		return err
	}
	w.manifest.IntervalSummaryCount++
	return nil
}

func (w *runArchiveWriter) Finalize(status string, completedAt time.Time, errStr string, slaBreached bool, slaDetails []string) (*storage.RunArchiveRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.manifest.Status = status
	w.manifest.CompletedAt = completedAt
	w.manifest.Error = errStr
	w.manifest.SLABreached = slaBreached
	w.manifest.SLADetails = slaDetails

	for _, file := range []*os.File{w.txFile, w.resourceFile, w.summaryFile} {
		if file == nil {
			continue
		}
		if err := file.Sync(); err != nil {
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
	}
	if err := writeArchiveParquetFiles(w.rootDir, &w.manifest); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(w.manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(w.manifestPath, data, 0o644); err != nil {
		return nil, err
	}
	if err := uploadArchiveDirectory(context.Background(), w.rootDir, w.manifest.TestID); err != nil {
		return nil, err
	}

	backend := currentArchiveBackend()
	return &storage.RunArchiveRecord{
		TestID:               w.manifest.TestID,
		SchemaVersion:        w.manifest.SchemaVersion,
		ArchiveBackend:       backend.Backend,
		ArchivePath:          w.rootDir,
		ManifestPath:         w.manifestPath,
		ArchiveURI:           backend.runURI(w.manifest.TestID),
		ManifestURI:          backend.manifestURI(w.manifest.TestID),
		TransactionCount:     w.manifest.TransactionCount,
		ResourceSampleCount:  w.manifest.ResourceSampleCount,
		IntervalSummaryCount: w.manifest.IntervalSummaryCount,
		CreatedAt:            w.manifest.CreatedAt,
		CompletedAt:          w.manifest.CompletedAt,
	}, nil
}

func traceIDFromCorrelationID(correlationID string) string {
	traceID, _ := traceparentIDs(correlationID)
	return traceID
}

func spanIDFromCorrelationID(correlationID string) string {
	_, spanID := traceparentIDs(correlationID)
	return spanID
}

func traceparentIDs(correlationID string) (string, string) {
	parts := strings.Split(strings.TrimSpace(correlationID), "-")
	if len(parts) != 4 {
		return "", ""
	}
	traceID := strings.ToLower(strings.TrimSpace(parts[1]))
	spanID := strings.ToLower(strings.TrimSpace(parts[2]))
	if len(traceID) != 32 || len(spanID) != 16 || !isLowerHex(traceID) || !isLowerHex(spanID) || isAllZeroHex(traceID) || isAllZeroHex(spanID) {
		return "", ""
	}
	return traceID, spanID
}

func isLowerHex(value string) bool {
	for _, ch := range value {
		if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
			return false
		}
	}
	return true
}

func isAllZeroHex(value string) bool {
	for _, ch := range value {
		if ch != '0' {
			return false
		}
	}
	return true
}

func cloneAnyMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func cloneBrowserLiveMetrics(metrics *BrowserLiveMetrics) *BrowserLiveMetrics {
	if metrics == nil {
		return nil
	}
	clone := *metrics
	return &clone
}

func cloneBrowserByDeviceMetrics(metrics map[string]*BrowserDeviceLiveMetrics) map[string]*BrowserDeviceLiveMetrics {
	if len(metrics) == 0 {
		return nil
	}
	clone := make(map[string]*BrowserDeviceLiveMetrics, len(metrics))
	for name, value := range metrics {
		if value == nil {
			continue
		}
		copied := *value
		clone[name] = &copied
	}
	if len(clone) == 0 {
		return nil
	}
	return clone
}

func durationMillis(duration time.Duration) float64 {
	return float64(duration.Microseconds()) / 1000.0
}
