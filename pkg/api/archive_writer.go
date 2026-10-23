package api

// writer — split from archive.go (same package, behavior-preserving).

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/hostnet"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func newRunArchiveWriter(test *ManagedTest, provenance RunArchiveProvenance) (*runArchiveWriter, error) {
	if test == nil {
		return nil, fmt.Errorf("test is required")
	}
	if err := validateArchiveRunID(test.ID); err != nil {
		return nil, err
	}
	rootDir, err := resolveArchiveDirectoryRoot(resolveArchiveRoot(), true)
	if err != nil {
		return nil, fmt.Errorf("prepare archive root: %w", err)
	}
	budget, err := newArchiveCaptureBudget(rootDir)
	if err != nil {
		return nil, err
	}
	conversion, err := newArchiveConversionBudget(rootDir)
	if err != nil {
		return nil, err
	}
	// Raw and derived output share one incremental namespace reservation. Their
	// separate per-attempt byte limits still apply.
	conversion.backlog = budget.backlog
	archiveDir, err := ensureArchiveRelativeDirectory(rootDir, test.ID, 0o755)
	if err != nil {
		return nil, fmt.Errorf("create archive dir: %w", err)
	}
	if err := rejectArchiveBacklogDirectoryAlias(rootDir, archiveDir); err != nil {
		return nil, err
	}
	targets := []string{"transactions.jsonl", "resources.jsonl", "interval_summaries.jsonl", "browser_documents.jsonl", "browser_documents.parquet", runArchiveManifestFileName}
	if test.ELKCapture != nil {
		targets = append(targets, "elk_evidence.jsonl")
	}
	if test.DynatraceCapture != nil {
		targets = append(targets, "dynatrace_evidence.jsonl")
	}
	lookupCapture := test.ELKCapture != nil || test.DynatraceCapture != nil
	if lookupCapture {
		targets = append(targets, providerLookupsFileName)
	}
	if err := validateArchiveArtifactTargets(archiveDir, targets...); err != nil {
		return nil, fmt.Errorf("inspect archive targets: %w", err)
	}

	open := func(name string) (*os.File, *bufio.Writer, *json.Encoder, error) {
		file, err := replaceArchiveArtifactForWrite(archiveDir, name, 0o644, false)
		if err != nil {
			return nil, nil, nil, err
		}
		writer := bufio.NewWriterSize(archiveCaptureFileWriter{file: file, budget: budget}, 256*1024)
		encoder := json.NewEncoder(archiveCaptureRecordWriter{destination: writer, budget: budget})
		return file, writer, encoder, nil
	}

	txFile, txBuf, txEncoder, err := open("transactions.jsonl")
	if err != nil {
		return nil, fmt.Errorf("open transactions archive: %w", err)
	}
	resourceFile, resourceBuf, resourceEncoder, err := open("resources.jsonl")
	if err != nil {
		txFile.Close()
		return nil, fmt.Errorf("open resources archive: %w", err)
	}
	summaryFile, summaryBuf, summaryEncoder, err := open("interval_summaries.jsonl")
	if err != nil {
		txFile.Close()
		resourceFile.Close()
		return nil, fmt.Errorf("open summaries archive: %w", err)
	}
	var elkFile *os.File
	var elkBuf *bufio.Writer
	var elkEncoder *json.Encoder
	elkEvidenceFile := ""
	if test.ELKCapture != nil {
		elkFile, elkBuf, elkEncoder, err = open("elk_evidence.jsonl")
		if err != nil {
			txFile.Close()
			resourceFile.Close()
			summaryFile.Close()
			return nil, fmt.Errorf("open ELK evidence archive: %w", err)
		}
		elkEvidenceFile = "elk_evidence.jsonl"
	}
	var dynatraceFile *os.File
	var dynatraceBuf *bufio.Writer
	var dynatraceEncoder *json.Encoder
	dynatraceEvidenceFile := ""
	if test.DynatraceCapture != nil {
		dynatraceFile, dynatraceBuf, dynatraceEncoder, err = open("dynatrace_evidence.jsonl")
		if err != nil {
			txFile.Close()
			resourceFile.Close()
			summaryFile.Close()
			if elkFile != nil {
				elkFile.Close()
			}
			return nil, fmt.Errorf("open Dynatrace evidence archive: %w", err)
		}
		dynatraceEvidenceFile = "dynatrace_evidence.jsonl"
	}
	var lookupFile *os.File
	var lookupBuf *bufio.Writer
	var lookupEncoder *json.Encoder
	providerLookupsFile := ""
	if lookupCapture {
		lookupFile, lookupBuf, lookupEncoder, err = open(providerLookupsFileName)
		if err != nil {
			for _, file := range []*os.File{txFile, resourceFile, summaryFile, elkFile, dynatraceFile} {
				if file != nil {
					file.Close()
				}
			}
			return nil, fmt.Errorf("open provider lookup archive: %w", err)
		}
		providerLookupsFile = providerLookupsFileName
	}

	initialProvenance := maskRunArchiveProvenance(provenance)
	writer := &runArchiveWriter{
		manifest: RunArchiveManifest{
			BrowserDocumentCaptureRequested: groupsRequestBrowserDocumentCapture(test.Groups),
			SchemaVersion:                   archiveSchemaVersion,
			BrowserProofExpectedOwners:      []string{"controller"},
			BrowserProofAuthorityMode:       "local",
			TestID:                          test.ID,
			TestName:                        test.Name,
			CreatedAt:                       time.Now(),
			HostNetwork:                     []hostnet.RunEvidence{localRunHostNetworkEvidence()},
			Provenance:                      initialProvenance,
			InitialProvenance:               &initialProvenance,
			TransactionsFile:                "transactions.jsonl",
			TransactionsParquetFile:         "transactions.parquet",
			ResourceSamplesFile:             "resources.jsonl",
			ResourceSamplesParquetFile:      "resources.parquet",
			IntervalSummariesFile:           "interval_summaries.jsonl",
			IntervalSummariesParquetFile:    "interval_summaries.parquet",
			ELKEvidenceFile:                 elkEvidenceFile,
			ELKObservation:                  elkObservation(elkEvidenceFile),
			DynatraceEvidenceFile:           dynatraceEvidenceFile,
			ProviderLookupsFile:             providerLookupsFile,
		},
		region:           strings.TrimSpace(os.Getenv("APEXLOAD_REGION")),
		rootDir:          archiveDir,
		captureBudget:    budget,
		conversionBudget: conversion,
		manifestPath:     filepath.Join(archiveDir, runArchiveManifestFileName),
		txFile:           txFile,
		resourceFile:     resourceFile,
		summaryFile:      summaryFile,
		elkFile:          elkFile,
		dynatraceFile:    dynatraceFile,
		txBuf:            txBuf,
		resourceBuf:      resourceBuf,
		summaryBuf:       summaryBuf,
		elkBuf:           elkBuf,
		dynatraceBuf:     dynatraceBuf,
		txEncoder:        txEncoder,
		resourceEncoder:  resourceEncoder,
		summaryEncoder:   summaryEncoder,
		elkEncoder:       elkEncoder,
		dynatraceEncoder: dynatraceEncoder,
		flushStop:        make(chan struct{}),
		flushStopped:     make(chan struct{}),
		lookupFile:       lookupFile,
		lookupBuf:        lookupBuf,
		lookupEncoder:    lookupEncoder,
	}
	go writer.flushLoop()
	return writer, nil
}

func (w *runArchiveWriter) flushLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	defer close(w.flushStopped)
	for {
		select {
		case <-ticker.C:
			w.mu.Lock()
			if err := w.flushBuffersLocked(); err != nil && w.flushErr == nil {
				w.flushErr = err
			}
			w.mu.Unlock()
		case <-w.flushStop:
			return
		}
	}
}

func (w *runArchiveWriter) stopFlushLoop() {
	if w == nil || w.flushStop == nil {
		return
	}
	close(w.flushStop)
	if w.flushStopped != nil {
		<-w.flushStopped
	}
	w.flushStop = nil
}

func (w *runArchiveWriter) flushBuffersLocked() error {
	var failures []error
	for _, writer := range []*bufio.Writer{w.txBuf, w.resourceBuf, w.summaryBuf, w.elkBuf, w.dynatraceBuf, w.browserDocumentsBuf, w.lookupBuf} {
		if writer == nil {
			continue
		}
		if err := writer.Flush(); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// SetTerminationReason records the execution driver's explicit completion signal.
func (w *runArchiveWriter) SetTerminationReason(reason string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.manifest.TerminationReason = reason
}

func (w *runArchiveWriter) RefreshProvenance(provenance RunArchiveProvenance) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.manifest.Provenance = maskRunArchiveProvenance(provenance)
}

func captureAPEXLoadEnv() map[string]string {
	// Archive manifests are readable by project viewers and may be retained or
	// exported outside the controller. Capture only deployment descriptors that
	// are useful when reproducing a run. In particular, do not try to identify
	// credentials by name: connection URLs and provider-specific variables can
	// carry credentials without containing words such as "secret" or "token".
	allowed := map[string]struct{}{
		"APEXLOAD_ARCHIVE_BACKEND": {},
		"APEXLOAD_BUILD_TIME":      {},
		"APEXLOAD_CACHE_BACKEND":   {},
		"APEXLOAD_COMMIT":          {},
		"APEXLOAD_CONTROLLER_HA":   {},
		"APEXLOAD_ENV":             {},
		"APEXLOAD_ENVIRONMENT":     {},
		"APEXLOAD_PUBLIC_REGION":   {},
		"APEXLOAD_REGION":          {},
		"APEXLOAD_TIER":            {},
		"APEXLOAD_VERSION":         {},
	}
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, ok := allowed[key]; !ok {
			continue
		}
		// Keep the manifest line-oriented and bounded even if a deployment
		// descriptor was supplied from an untrusted environment source.
		value = strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' || r == 0 {
				return -1
			}
			return r
		}, value)
		runes := []rune(value)
		if len(runes) > 512 {
			value = string(runes[:512])
		}
		env[key] = value
	}
	if len(env) == 0 {
		return nil
	}
	return env
}

func (w *runArchiveWriter) RecordResult(result *protocol.Result) error {
	if result != nil && result.SampleKind == "browser_document_evidence" {
		return fmt.Errorf("browser documents require the typed evidence sink")
	}
	if result == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.flushErr != nil {
		return w.flushErr
	}
	population := "action"
	if result.BusinessTransaction {
		population = "transaction"
	}
	event := TransactionEvent{
		CSVExportIDs:          result.CSVExportIDs,
		ExtractionsIncomplete: result.ExtractionsIncomplete,
		Population:            population,
		Timestamp:             result.StartTime,
		TestID:                w.manifest.TestID,
		TestName:              w.manifest.TestName,
		Group:                 result.Group,
		Region:                firstNonEmpty(result.Region, w.region),
		NetworkProfile:        result.NetworkProfile,
		PropertyDimensions:    result.PropertyDimensions,
		SourceTransaction:     result.SourceTransaction,
		Hierarchy:             firstNonEmpty(result.Hierarchy, result.SourcePath),
		PageName:              result.PageName,
		Phase:                 result.Phase,
		VUserID:               result.VUserID,
		Iteration:             result.Iteration,
		Interrupted:           result.Interrupted,
		VUserLifetime:         result.VUserLifetime,
		EndsIteration:         result.EndsIteration,
		ElapsedMs:             float64(result.WallElapsed) / float64(time.Millisecond),
		MessageDetailPriority: result.MessageDetailPriority,
		ActionName:            result.ActionName,
		BusinessTransaction:   result.BusinessTransaction,
		CorrelationID:         result.CorrelationID,
		DependencyType:        result.DependencyType,
		DependencyTarget:      result.DependencyTarget,
		DurationMs:            durationMillis(result.Duration),
		WorkerDurationMs:      durationMillis(result.WorkerDuration),
		DNSMs:                 durationMillis(result.DNSTime),
		ConnectMs:             durationMillis(result.ConnectTime),
		TLSMs:                 durationMillis(result.TLSTime),
		TTFBMs:                durationMillis(result.TTFB),
		BytesSent:             result.BytesSent,
		BytesReceived:         result.BytesReceived,
		ByteEvidence:          protocol.ByteEvidence(result),
		StatusCode:            result.StatusCode,
		Success:               result.Success,
		ErrorMessage:          result.ErrorMessage,
		Extractions:           result.Extractions,
		PageURL:               result.PageURL,
		PageTitle:             result.PageTitle,
		ConsoleEntries:        cloneProtocolConsoleEntries(result.ConsoleEntries),
		NetworkEntries:        cloneProtocolNetworkEntries(result.NetworkEntries),
		DOMSummary:            cloneProtocolDOMSummary(result.DOMSummary),
		ClientMetrics:         result.ClientMetrics,
		ServerTelemetry:       result.ServerTelemetry,
		ProtocolEvidence:      cloneAnyMap(result.ProtocolEvidence),
		Snapshot:              result.Snapshot,
		SourceStepID:          result.SourceStepID,
		SourceStepIndex:       result.SourceStepIndex,
		SourceAction:          result.SourceAction,
		SourceActionName:      result.SourceActionName,
		SourceProfile:         result.SourceProfile,
	}
	applySentTraceContext(&event, result.TraceContext)
	event.HTTPVersion, event.HTTPVersionFallback, event.AttemptCount = protocol.ArchiveHTTPVersionFields(result)
	if len(result.TraceLinks) > 0 || result.TraceLinksTruncated {
		event.TraceLinks = append([]protocol.TraceLink(nil), result.TraceLinks...)
		event.TraceLinksTruncated = result.TraceLinksTruncated
	}
	// Direct recorder callers do not pass through the engine's pre-redaction
	// binding. Bind them against the same immutable admission provenance here.
	provenance := w.manifest.InitialProvenance
	if provenance == nil {
		provenance = &w.manifest.Provenance
	}
	if event.CSVExportIDs == nil && len(provenance.CSVExports) > 0 {
		event.CSVExportIDs = []string{}
		for _, declaration := range provenance.CSVExports {
			if declaration.Group == result.Group && declaration.Export.ActionName == result.ActionName {
				event.CSVExportIDs = append(event.CSVExportIDs, declaration.Export.ID)
			}
		}
	}
	// Business keys are digested from their raw extracted values, before
	// scrubbing can change them.
	if w.businessKeys != nil {
		w.businessKeys.apply(&event)
		w.manifest.BusinessKeyDigestsUnavailable = w.manifest.BusinessKeyDigestsUnavailable || w.businessKeys.unavailable
	}
	// Defense in depth for callers that record protocol results without going
	// through the VUser projection seam. This also detaches nested evidence from
	// the live result before the encoder observes it.
	sanitizeArchivedTransaction(&event)
	w.applyMessageDetailBudget(&event, result)
	finalizeTransactionEventDerivedFields(&event, w.manifest.TransactionCount)
	if err := w.txEncoder.Encode(event); err != nil {
		w.flushErr = err
		return err
	}
	w.manifest.TransactionCount++
	return nil
}

func (w *runArchiveWriter) applyMessageDetailBudget(event *TransactionEvent, result *protocol.Result) {
	if w == nil || event == nil || result == nil || !result.Success || !result.MessageDetailRequested {
		return
	}
	if w.manifest.MessageDetailCapture == nil {
		w.manifest.MessageDetailCapture = &RunArchiveMessageDetailCapture{Dropped: make(map[string]int64)}
	}
	stats := w.manifest.MessageDetailCapture
	drop := func(reason string) {
		stats.Dropped[reason]++
		event.Snapshot = nil
		result.Snapshot = nil
		result.MessageDetailRetained = false
	}
	if event.Snapshot == nil {
		drop("protocol_no_detail")
		return
	}
	maxItems := int64(result.MessageDetailMaxItems)
	maxItemBytes := result.MessageDetailMaxItemBytes
	maxTotalBytes := result.MessageDetailMaxTotalBytes
	if maxItems <= 0 || maxItemBytes <= 0 || maxTotalBytes <= 0 {
		drop("invalid_budget")
		return
	}
	admission := w.detailUsage.Admit(engine.MessageDetailLimits{
		MaxItems: maxItems, MaxItemBytes: maxItemBytes, MaxTotalBytes: maxTotalBytes,
		Adaptive: strings.EqualFold(result.MessageDetailMode, "adaptive"),
	}, result.MessageDetailPriority, event.Snapshot)
	if admission.Truncated {
		stats.TruncatedItems++
	}
	if admission.Drop != engine.MessageDetailKept {
		drop(admission.Drop.Reason(admission.Priority))
		return
	}
	event.Snapshot = admission.Snapshot
	stats.RetainedItems++
	stats.RetainedBytes += admission.Bytes
	result.Snapshot = event.Snapshot
	result.MessageDetailRetained = true
}

// RecordTransaction persists synthetic business transactions emitted by the
// engine's automatic/explicit transaction paths. Those paths do not call
// RecordResult for the same logical transaction, while adapter-supplied business
// transactions continue to arrive through RecordResult exactly once.
func (w *runArchiveWriter) RecordTransaction(result *protocol.Result) error {
	return w.RecordResult(result)
}

func (w *runArchiveWriter) RecordResourceSample(timestamp time.Time, activeVUsers int64, resources *engine.SystemResources) error {
	if resources == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.flushErr != nil {
		return w.flushErr
	}
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
		ContainerCPUPeriodsDelta:          resources.ContainerCPUPeriodsDelta,
	}
	if err := w.resourceEncoder.Encode(record); err != nil {
		w.flushErr = err
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
	if w.flushErr != nil {
		return w.flushErr
	}
	activeRuntimeSeconds := live.ActiveRuntimeSeconds
	record := IntervalSummaryRecord{
		JavaHTTP:                 live.JavaHTTP.Retained(),
		MessagingProgress:        live.MessagingProgress.Clone(),
		Messaging:                protocol.CloneMessagingSummaries(live.Messaging),
		ObservationState:         live.ObservationState,
		CompletedIterations:      live.CompletedIterations,
		Timestamp:                live.Timestamp,
		TestID:                   w.manifest.TestID,
		TestName:                 w.manifest.TestName,
		ActiveVUsers:             live.ActiveVUsers,
		TPS:                      live.TPS,
		TargetTPS:                live.TargetTPS,
		ThroughputGapPct:         live.ThroughputGapPct,
		ThroughputTargetMissed:   live.ThroughputTargetMissed,
		AvgResponseMs:            live.AvgResponseMs,
		ErrorPct:                 live.ErrorPct,
		TotalTx:                  live.TotalTx,
		TotalErrors:              live.TotalErrors,
		P50Ms:                    live.P50Ms,
		P90Ms:                    live.P90Ms,
		P95Ms:                    live.P95Ms,
		P99Ms:                    live.P99Ms,
		LoadLevels:               append([]ActiveLoadLevel(nil), live.LoadLevels...),
		ActiveRuntimeSeconds:     &activeRuntimeSeconds,
		SampleWindowSeconds:      live.SampleWindowSeconds,
		LoadLevelDurationDigests: loadLevelDurationDigests(live.GroupMetrics),
		Groups:                   cloneGroupLiveMetricsMap(live.GroupMetrics),
		Transactions:             intervalTransactionsFromLive(live.Transactions),
		ByRegion:                 intervalTransactionsFromLive(live.ByRegion),
		Browser:                  cloneBrowserLiveMetrics(live.Browser),
		BrowserDocuments:         live.BrowserDocuments.Clone(),
		BrowserByDevice:          cloneBrowserByDeviceMetrics(live.BrowserByDevice),
		Telemetry:                archivedTelemetryHealth(live.Telemetry),
		TelemetryStatus:          cloneTelemetryRunHealth(live.TelemetryStatus),
		ELK:                      cloneELKCaptureStatus(live.ELK),
		Dynatrace:                cloneDynatraceCaptureStatus(live.Dynatrace),
	}
	record.LoadLevelDurationSketches = loadLevelDurationSketches(live.GroupMetrics)
	if err := w.summaryEncoder.Encode(record); err != nil {
		w.flushErr = err
		return err
	}
	w.manifest.IntervalSummaryCount++
	return nil
}

// RecordELKSamples appends the already bounded, deduplicated capture selected by
// the controller. Redaction is repeated here so every archive writer boundary is
// safe even when a caller bypasses the Elasticsearch polling path.
func (w *runArchiveWriter) RecordELKSamples(samples []ELKLogSample) error {
	if w == nil || w.elkEncoder == nil || len(samples) == 0 {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.flushErr != nil {
		return w.flushErr
	}
	for _, sample := range samples {
		sample = sanitizeELKSample(sample)
		if err := w.elkEncoder.Encode(sample); err != nil {
			w.flushErr = err
			return err
		}
		w.manifest.ELKEvidenceCount++
	}
	return nil
}

// RecordProviderLookups appends lookup outcomes, up to the run's bound.
func (w *runArchiveWriter) RecordProviderLookups(records []ProviderLookupRecord) error {
	if w == nil || w.lookupEncoder == nil || len(records) == 0 {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.flushErr != nil {
		return w.flushErr
	}
	for _, record := range records {
		if w.manifest.ProviderLookupCount >= maxProviderLookupRecords {
			w.manifest.ProviderLookupsTruncated = true
			return nil
		}
		if err := w.lookupEncoder.Encode(record); err != nil {
			w.flushErr = err
			return err
		}
		w.manifest.ProviderLookupCount++
	}
	return nil
}

func (w *runArchiveWriter) RecordDynatraceEvidence(records []DynatraceEvidenceRecord) error {
	if w == nil || w.dynatraceEncoder == nil || len(records) == 0 {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.flushErr != nil {
		return w.flushErr
	}
	for _, record := range records {
		if record.Log != nil {
			sample := sanitizeDynatraceLog(*record.Log)
			record.Log = &sample
		}
		if record.Problem != nil {
			problem := sanitizeDynatraceProblem(*record.Problem)
			record.Problem = &problem
		}
		if err := w.dynatraceEncoder.Encode(record); err != nil {
			w.flushErr = err
			return err
		}
		w.manifest.DynatraceEvidenceCount++
	}
	return nil
}

func loadLevelDurationDigests(groups map[string]*GroupLiveMetrics) map[string]*engine.TDigestSnapshot {
	if len(groups) == 0 {
		return nil
	}
	result := make(map[string]*engine.TDigestSnapshot, len(groups)+1)
	total := engine.NewTDigest(200)
	for name, metrics := range groups {
		if metrics == nil || metrics.DurationDigest == nil || metrics.DurationDigest.Count <= 0 {
			continue
		}
		copy := *metrics.DurationDigest
		copy.Centroids = append([]engine.TDigestCentroidSnapshot(nil), metrics.DurationDigest.Centroids...)
		result[name] = &copy
		total.Merge(engine.NewTDigestFromSnapshot(copy))
	}
	if total.Count() > 0 {
		snapshot := total.Snapshot()
		result["__total__"] = &snapshot
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// loadLevelDurationSketches mirrors loadLevelDurationDigests: each group's
// sketch, and under __total__ their merge.
func loadLevelDurationSketches(groups map[string]*GroupLiveMetrics) map[string]*engine.DurationSketchSnapshot {
	if len(groups) == 0 {
		return nil
	}
	result := make(map[string]*engine.DurationSketchSnapshot, len(groups)+1)
	total := engine.NewDurationSketch()
	for name, metrics := range groups {
		if metrics == nil {
			continue
		}
		sketch := engine.DurationSketchForCount(metrics.DurationSketch, metrics.Transactions+metrics.Errors)
		if sketch == nil {
			continue
		}
		result[name] = engine.CloneDurationSketchSnapshot(metrics.DurationSketch)
		total.Merge(sketch)
	}
	if total.Count() > 0 {
		snapshot := total.Snapshot()
		result["__total__"] = &snapshot
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (w *runArchiveWriter) Finalize(status string, completedAt time.Time, errStr string, slaEvaluated bool, slaBreached bool, slaDetails []string) (*storage.RunArchiveRecord, error) {
	if err := w.sealBrowserSourceJournal(status == "COMPLETE"); err != nil {
		w.MarkBrowserDocumentReceiptIncomplete()
	}
	w.stopFlushLoop()
	w.harMu.Lock()
	defer w.harMu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()
	defer w.captureBudget.close()
	defer w.conversionBudget.close()
	if w.flushErr != nil {
		// A logical admission failure did not accept the rejected record. Flush
		// earlier accepted records where possible, preserving a recoverable raw
		// prefix without ever publishing it as a complete archive.
		return nil, errors.Join(w.flushErr, w.flushBuffersLocked(), w.syncAndCloseDataFilesLocked())
	}
	if w.manifest.TerminationReason == "" {
		w.manifest.TerminationReason = "unknown"
	}
	switch status {
	case "COMPLETE":
		if w.manifest.Provenance.StoppedBySubject != "" {
			w.manifest.TerminationReason = "operator_stopped"
		} else if errStr != "" {
			w.manifest.TerminationReason = "stopped_early"
		}
	case "ABORTED":
		w.manifest.TerminationReason = "aborted"
	case "FAILED":
		w.manifest.TerminationReason = "failed"
	}
	w.manifest.Status = status
	w.manifest.CompletedAt = completedAt
	w.manifest.Error = errStr
	w.manifest.SLAEvaluated = slaEvaluated
	w.manifest.SLABreached = slaBreached
	w.manifest.SLADetails = slaDetails
	for index := range w.manifest.HostNetwork {
		w.manifest.HostNetwork[index].Finish("", os.Getenv)
	}

	if err := errors.Join(w.flushBuffersLocked(), w.syncAndCloseDataFilesLocked(), w.closeBrowserInteractionsLocked()); err != nil {
		return nil, err
	}
	if err := applyTransactionSLARules(w.rootDir, &w.manifest); err != nil {
		return nil, err
	}
	if err := applyTransactionResponseTimeSLAs(w.rootDir, &w.manifest); err != nil {
		return nil, err
	}
	if err := validateArchiveArtifactTargets(w.rootDir,
		w.manifest.TransactionsParquetFile,
		w.manifest.ResourceSamplesParquetFile,
		w.manifest.IntervalSummariesParquetFile,
		w.manifest.BrowserDocumentsParquetFile,
		filepath.Base(w.manifestPath),
	); err != nil {
		return nil, err
	}
	if err := writeArchiveParquetFiles(w.rootDir, &w.manifest, w.conversionBudget); err != nil {
		return nil, err
	}
	if err := materializeDeclaredCSVExports(w.rootDir, &w.manifest, w.conversionBudget); err != nil {
		return nil, err
	}
	if err := updateRunArchiveArtifactIntegrity(w.rootDir, &w.manifest); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(w.manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := syncArchiveArtifacts(w.rootDir, w.manifestPath); err != nil {
		return nil, err
	}
	if err := writeArchiveArtifactDurableWithWriter(w.rootDir, filepath.Base(w.manifestPath), data, 0o644, func(file *os.File, data []byte) (int, error) {
		if w.captureBudget == nil {
			return file.Write(data)
		}
		return (archiveCaptureFileWriter{file: file, budget: w.captureBudget}).Write(data)
	}, nil); err != nil {
		return nil, err
	}
	manifestSHA256 := sha256BytesHex(data)
	backend := currentArchiveBackend()
	w.retireArchivedHARSourcesLocked()
	return &storage.RunArchiveRecord{
		TestID:               w.manifest.TestID,
		SchemaVersion:        w.manifest.SchemaVersion,
		ArchiveBackend:       backend.Backend,
		ArchivePath:          w.rootDir,
		ManifestPath:         w.manifestPath,
		ManifestSHA256:       manifestSHA256,
		ArchiveURI:           backend.runURI(w.manifest.TestID),
		ManifestURI:          backend.manifestURI(w.manifest.TestID),
		TransactionCount:     w.manifest.TransactionCount,
		ResourceSampleCount:  w.manifest.ResourceSampleCount,
		IntervalSummaryCount: w.manifest.IntervalSummaryCount,
		CreatedAt:            w.manifest.CreatedAt,
		CompletedAt:          w.manifest.CompletedAt,
	}, nil
}

func (w *runArchiveWriter) syncAndCloseDataFilesLocked() error {
	if w == nil {
		return nil
	}
	files := []*os.File{w.txFile, w.resourceFile, w.summaryFile, w.elkFile, w.dynatraceFile, w.browserDocumentsFile, w.browserReceiptsFile, w.lookupFile}
	w.txFile = nil
	w.resourceFile = nil
	w.summaryFile = nil
	w.elkFile = nil
	w.dynatraceFile = nil
	w.browserDocumentsFile = nil
	w.browserReceiptsFile = nil
	w.browserDocumentsEncoder = nil
	w.browserDocumentsBuf = nil
	w.lookupFile = nil
	var failures []error
	for _, file := range files {
		if file == nil {
			continue
		}
		if err := file.Sync(); err != nil {
			failures = append(failures, err)
		}
		if err := file.Close(); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func syncArchiveArtifacts(root, manifestPath string) error {
	rootHandle, err := openArchiveDirectoryNoSymlink(root)
	if err != nil {
		return err
	}
	if err := rootHandle.Close(); err != nil {
		return err
	}
	absManifest, err := filepath.Abs(manifestPath)
	if err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if absPath == absManifest {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive artifact %q is a symbolic link", path)
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		file, info, err := openArchiveArtifactForRead(root, filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			_ = file.Close()
			return fmt.Errorf("archive artifact %q is not a regular file", path)
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if syncErr != nil && !isIgnorableReadOnlySyncError(syncErr) {
			return syncErr
		}
		return closeErr
	})
}

func isIgnorableReadOnlySyncError(err error) bool {
	// On Windows, Sync on a read-only handle can return access denied even
	// though data durability is already guaranteed by the writer-side Sync prior
	// to close. Ignore that specific class so manifest publication can proceed.
	return runtime.GOOS == "windows" && os.IsPermission(err)
}

func writeArchiveManifestDurable(path string, data []byte) error {
	return writeArchiveArtifactDurable(filepath.Dir(path), filepath.Base(path), data, 0o644)
}

// writeArchiveArtifactDurable atomically replaces a root-relative archive
// artifact without following linked parents or a linked destination. Adjunct
// writers use this boundary so publication cannot be redirected by persisted
// or locally planted paths.
func writeArchiveArtifactDurable(rootPath, name string, data []byte, perm os.FileMode) error {
	return writeArchiveArtifactDurableWithBudget(rootPath, name, data, perm, nil)
}

func writeArchiveArtifactDurableWithBudget(rootPath, name string, data []byte, perm os.FileMode, budget *archiveOutputBudget) error {
	var ctx context.Context
	if budget != nil {
		ctx = budget.ctx
	}
	return writeArchiveArtifactDurableWithWriter(rootPath, name, data, perm, func(file *os.File, data []byte) (int, error) {
		if budget == nil {
			return file.Write(data)
		}
		return (&archiveOutputFile{file: file, budget: budget}).Write(data)
	}, ctx)
}

func writeArchiveArtifactDurableWithWriter(rootPath, name string, data []byte, perm os.FileMode, write func(*os.File, []byte) (int, error), ctx context.Context) error {
	normalized, err := normalizeArchiveArtifactName(name)
	if err != nil {
		return err
	}
	root, err := openArchiveDirectoryNoSymlink(rootPath)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := inspectArchiveRelativeParents(root, normalized, false, 0); err != nil {
		return err
	}
	if _, err := inspectArchiveArtifactTarget(root, normalized, true); err != nil {
		return err
	}
	parent := filepath.Dir(filepath.FromSlash(normalized))
	base := filepath.Base(filepath.FromSlash(normalized))
	tempName, err := archiveTemporaryName(parent, base)
	if err != nil {
		return err
	}
	tmp, err := root.OpenFile(tempName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	keepTemp := true
	defer func() {
		if keepTemp {
			_ = root.Remove(tempName)
		}
	}()
	n, writeErr := write(tmp, data)
	if n != len(data) && writeErr == nil {
		writeErr = io.ErrShortWrite
	}
	if writeErr != nil {
		_ = tmp.Close()
		return writeErr
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	// Re-check the target immediately before rename. Rename replaces a regular
	// file or a link itself and never follows the destination, but rejecting a
	// planted link keeps the archive namespace fail-closed and auditable.
	if _, err := inspectArchiveArtifactTarget(root, normalized, true); err != nil {
		return err
	}
	if err := root.Rename(tempName, filepath.FromSlash(normalized)); err != nil {
		return err
	}
	keepTemp = false
	if err := syncArchiveDirectory(rootPath); err != nil {
		if isIgnorableArchiveDirectorySyncError(err) {
			return nil
		}
		return fmt.Errorf("sync archive directory: %w", err)
	}
	return nil
}

func isIgnorableArchiveDirectorySyncError(err error) bool {
	// A Windows directory that refuses the write-access reopen its flush needs
	// (syncArchiveDirectoryHandle) has still taken the atomic rename or write.
	// Treat that as non-fatal so archives can still be published.
	return runtime.GOOS == "windows" && os.IsPermission(err)
}

func sha256BytesHex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func refreshRunArchiveManifestTrust(record *storage.RunArchiveRecord) error {
	if record == nil || strings.TrimSpace(record.ManifestPath) == "" {
		return nil
	}
	file, _, err := openArchiveArtifactForRead(filepath.Dir(record.ManifestPath), filepath.Base(record.ManifestPath))
	if err != nil {
		return err
	}
	defer file.Close()
	sum, err := sha256Reader(file)
	if err != nil {
		return err
	}
	record.ManifestSHA256 = sum
	return nil
}

func updateRunArchiveArtifactIntegrity(root string, manifest *RunArchiveManifest) error {
	if manifest == nil {
		return nil
	}
	descriptors := []RunArchiveArtifact{
		{Name: manifest.TransactionsFile, Kind: "dataset", Format: "jsonl", RecordType: "transactions", Count: manifest.TransactionCount},
		{Name: manifest.TransactionsParquetFile, Kind: "dataset", Format: "parquet", RecordType: "transactions", Count: manifest.TransactionCount},
		{Name: manifest.ResourceSamplesFile, Kind: "dataset", Format: "jsonl", RecordType: "resource_samples", Count: manifest.ResourceSampleCount},
		{Name: manifest.ResourceSamplesParquetFile, Kind: "dataset", Format: "parquet", RecordType: "resource_samples", Count: manifest.ResourceSampleCount},
		{Name: manifest.IntervalSummariesFile, Kind: "dataset", Format: "jsonl", RecordType: "interval_summaries", Count: manifest.IntervalSummaryCount},
		{Name: manifest.IntervalSummariesParquetFile, Kind: "dataset", Format: "parquet", RecordType: "interval_summaries", Count: manifest.IntervalSummaryCount},
		{Name: manifest.BrowserReceiptsFile, Kind: "evidence", Format: "jsonl", RecordType: "browser_receipts_versioned", Count: manifest.BrowserReceiptRecordCount},
		{Name: manifest.BrowserDocumentsFile, Kind: "evidence", Format: "jsonl", RecordType: "browser_documents_v2", Count: manifest.BrowserDocumentRecordCount},
		{Name: manifest.BrowserDocumentsParquetFile, Kind: "evidence", Format: "parquet", RecordType: "browser_documents_v2", Count: manifest.BrowserDocumentRecordCount},
		{Name: manifest.TestManifestFile, Kind: "manifest", Format: "json", RecordType: "test_manifest"},
		{Name: manifest.TestNotesFile, Kind: "notes", Format: "json", RecordType: "test_notes"},
		{Name: manifest.ELKEvidenceFile, Kind: "evidence", Format: "jsonl", RecordType: "elk_log_samples", Count: manifest.ELKEvidenceCount},
		{Name: manifest.DynatraceEvidenceFile, Kind: "evidence", Format: "jsonl", RecordType: "dynatrace_evidence", Count: manifest.DynatraceEvidenceCount},
		{Name: manifest.MonitorEvidenceFile, Kind: "evidence", Format: "jsonl", RecordType: "monitor_points_v1", Count: manifest.MonitorEvidenceCount},
		{Name: manifest.MonitorManagementFile, Kind: "evidence", Format: "jsonl", RecordType: "monitor_management_v1", Count: manifest.MonitorManagementSnapshotCount},
		{Name: manifest.MonitorDiagnosticsFile, Kind: "evidence", Format: "jsonl", RecordType: "monitor_diagnostics_v1", Count: manifest.MonitorDiagnosticsCount},
		{Name: manifest.IntervalDistributionsFile, Kind: "evidence", Format: "jsonl", RecordType: intervalDistributionRecordType, Count: manifest.IntervalDistributionCount},
		{Name: manifest.ProviderLookupsFile, Kind: "evidence", Format: "jsonl", RecordType: providerLookupRecordType, Count: manifest.ProviderLookupCount},
		{Name: manifest.ExemplarsFile, Kind: "evidence", Format: "jsonl", RecordType: exemplarRecordType, Count: manifest.ExemplarCount},
		{Name: manifest.ClockObservationsFile, Kind: "evidence", Format: "jsonl", RecordType: clockObservationRecordType, Count: manifest.ClockObservationCount},
		{Name: manifest.TelemetrySourcesFile, Kind: "evidence", Format: "jsonl", RecordType: telemetrySourcesRecordType, Count: manifest.TelemetrySourceEpochCount},
		{Name: manifest.JointIntervalsFile, Kind: "evidence", Format: "jsonl", RecordType: jointIntervalRecordType, Count: manifest.JointIntervalCount},
		{Name: manifest.JointIntervalsParquetFile, Kind: "evidence", Format: "parquet", RecordType: jointIntervalRecordType, Count: manifest.JointIntervalCount},
		{Name: manifest.PhaseEventsFile, Kind: "evidence", Format: "jsonl", RecordType: phaseEventRecordType, Count: manifest.PhaseEventCount},
	}
	for _, har := range manifest.BrowserHARArtifacts {
		descriptors = append(descriptors, RunArchiveArtifact{Name: har.Name, Kind: "evidence", Format: "har", RecordType: runArchiveHARRecordType})
	}
	if manifest.BrowserInteractionsFile != "" {
		descriptors = append(descriptors, RunArchiveArtifact{Name: manifest.BrowserInteractionsFile, Kind: "evidence", Format: "jsonl", RecordType: browserInteractionsRecordType, Count: manifest.BrowserInteractionRecordCount})
	}
	for _, export := range manifest.CSVExports {
		if export.Status == "complete" {
			descriptors = append(descriptors, RunArchiveArtifact{Name: export.FileName, Kind: "csv_export", Format: "csv", RecordType: "action_results", Count: export.Rows})
		}
	}
	manifest.BrowserDocumentsSHA256 = ""
	manifest.BrowserDocumentsParquetSHA256 = ""
	artifacts := make([]RunArchiveArtifact, 0, len(descriptors))
	for _, artifact := range descriptors {
		if strings.TrimSpace(artifact.Name) == "" {
			continue
		}
		file, info, err := openArchiveArtifactForRead(root, artifact.Name)
		if err != nil {
			if os.IsNotExist(err) && artifact.Name != manifest.BrowserReceiptsFile && artifact.Name != manifest.BrowserDocumentsFile && artifact.Name != manifest.BrowserDocumentsParquetFile {
				continue
			}
			return err
		}
		artifact.SizeBytes = info.Size()
		sum, err := sha256Reader(file)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		artifact.SHA256 = sum
		if artifact.RecordType == runArchiveHARRecordType && !runArchiveHARMatches(manifest, artifact) {
			return fmt.Errorf("archive HAR %s differs from its receipt", artifact.Name)
		}
		if artifact.Name == manifest.BrowserReceiptsFile {
			manifest.BrowserReceiptsSHA256 = sum
		}
		if artifact.Name == manifest.BrowserDocumentsFile {
			manifest.BrowserDocumentsSHA256 = sum
		}
		if artifact.Name == manifest.BrowserDocumentsParquetFile {
			manifest.BrowserDocumentsParquetSHA256 = sum
		}
		artifacts = append(artifacts, artifact)
	}
	manifest.Artifacts = artifacts
	return nil
}

func updateRunArchiveSingleArtifactIntegrity(root string, manifest *RunArchiveManifest, artifact RunArchiveArtifact) error {
	if manifest == nil {
		return nil
	}
	if strings.TrimSpace(artifact.Name) == "" {
		return fmt.Errorf("archive artifact name is required")
	}
	file, info, err := openArchiveArtifactForRead(root, artifact.Name)
	if err != nil {
		return err
	}
	sum, err := sha256Reader(file)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	artifact.SizeBytes = info.Size()
	artifact.SHA256 = sum
	for i := range manifest.Artifacts {
		if manifest.Artifacts[i].Name == artifact.Name {
			manifest.Artifacts[i] = artifact
			return nil
		}
	}
	manifest.Artifacts = append(manifest.Artifacts, artifact)
	return nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	return sha256Reader(file)
}

func sha256Reader(reader io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, reader); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
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

// elkObservation labels the legacy ELK capture whenever it is archived.
func elkObservation(file string) string {
	if file == "" {
		return ""
	}
	return monitorprovider.ObservationCumulative
}

// localRunHostNetworkEvidence is the controller's own network evidence for a
// local run, whose VUsers it executes.
func localRunHostNetworkEvidence() hostnet.RunEvidence {
	evidence := hostnet.StartRunEvidence("", os.Getenv)
	evidence.Source = "controller"
	return evidence
}
