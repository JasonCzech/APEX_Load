package api

// writer — split from archive.go (same package, behavior-preserving).

import (
	"bufio"
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
	archiveDir, err := ensureArchiveRelativeDirectory(rootDir, test.ID, 0o755)
	if err != nil {
		return nil, fmt.Errorf("create archive dir: %w", err)
	}
	if err := validateArchiveArtifactTargets(archiveDir, "transactions.jsonl", "resources.jsonl", "interval_summaries.jsonl", runArchiveManifestFileName); err != nil {
		return nil, fmt.Errorf("inspect archive targets: %w", err)
	}

	open := func(name string) (*os.File, *bufio.Writer, *json.Encoder, error) {
		file, err := replaceArchiveArtifactForWrite(archiveDir, name, 0o644, false)
		if err != nil {
			return nil, nil, nil, err
		}
		writer := bufio.NewWriterSize(file, 256*1024)
		return file, writer, json.NewEncoder(writer), nil
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

	initialProvenance := maskRunArchiveProvenance(provenance)
	writer := &runArchiveWriter{
		manifest: RunArchiveManifest{
			SchemaVersion:                archiveSchemaVersion,
			TestID:                       test.ID,
			TestName:                     test.Name,
			CreatedAt:                    time.Now(),
			Provenance:                   initialProvenance,
			InitialProvenance:            &initialProvenance,
			TransactionsFile:             "transactions.jsonl",
			TransactionsParquetFile:      "transactions.parquet",
			ResourceSamplesFile:          "resources.jsonl",
			ResourceSamplesParquetFile:   "resources.parquet",
			IntervalSummariesFile:        "interval_summaries.jsonl",
			IntervalSummariesParquetFile: "interval_summaries.parquet",
		},
		region:          strings.TrimSpace(os.Getenv("APEXLOAD_REGION")),
		rootDir:         archiveDir,
		manifestPath:    filepath.Join(archiveDir, runArchiveManifestFileName),
		txFile:          txFile,
		resourceFile:    resourceFile,
		summaryFile:     summaryFile,
		txBuf:           txBuf,
		resourceBuf:     resourceBuf,
		summaryBuf:      summaryBuf,
		txEncoder:       txEncoder,
		resourceEncoder: resourceEncoder,
		summaryEncoder:  summaryEncoder,
		flushStop:       make(chan struct{}),
		flushStopped:    make(chan struct{}),
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
	for _, writer := range []*bufio.Writer{w.txBuf, w.resourceBuf, w.summaryBuf} {
		if writer == nil {
			continue
		}
		if err := writer.Flush(); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
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
	if result == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.flushErr != nil {
		return w.flushErr
	}
	event := TransactionEvent{
		Timestamp:             result.StartTime,
		TestID:                w.manifest.TestID,
		TestName:              w.manifest.TestName,
		Group:                 result.Group,
		Region:                firstNonEmpty(result.Region, w.region),
		NetworkProfile:        result.NetworkProfile,
		SourceTransaction:     result.SourceTransaction,
		Hierarchy:             firstNonEmpty(result.Hierarchy, result.SourcePath),
		PageName:              result.PageName,
		Phase:                 result.Phase,
		MessageDetailPriority: result.MessageDetailPriority,
		ActionName:            result.ActionName,
		BusinessTransaction:   result.BusinessTransaction,
		CorrelationID:         result.CorrelationID,
		TraceID:               traceIDFromCorrelationID(result.CorrelationID),
		SpanID:                spanIDFromCorrelationID(result.CorrelationID),
		DependencyType:        result.DependencyType,
		DependencyTarget:      result.DependencyTarget,
		DurationMs:            durationMillis(result.Duration),
		DNSMs:                 durationMillis(result.DNSTime),
		ConnectMs:             durationMillis(result.ConnectTime),
		TLSMs:                 durationMillis(result.TLSTime),
		TTFBMs:                durationMillis(result.TTFB),
		BytesSent:             result.BytesSent,
		BytesReceived:         result.BytesReceived,
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
	// Defense in depth for callers that record protocol results without going
	// through the VUser projection seam. This also detaches nested evidence from
	// the live result before the encoder observes it.
	sanitizeArchivedTransaction(&event)
	w.applyMessageDetailBudget(&event, result)
	finalizeTransactionEventDerivedFields(&event, w.manifest.TransactionCount)
	if err := w.txEncoder.Encode(event); err != nil {
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
	priority := 0
	priorityItemLimit, priorityByteLimit := maxItems, maxTotalBytes
	if strings.EqualFold(result.MessageDetailMode, "adaptive") {
		priority = min(max(result.MessageDetailPriority, 1), 3)
		priorityItemLimit = adaptiveMessageDetailPriorityLimit(maxItems, priority)
		priorityByteLimit = adaptiveMessageDetailPriorityLimit(maxTotalBytes, priority)
		if w.detailPriorityItems[priority] >= priorityItemLimit || priorityItemLimit == 0 {
			drop(fmt.Sprintf("priority_%d_item_limit", priority))
			return
		}
		if w.detailPriorityBytes[priority] >= priorityByteLimit || priorityByteLimit == 0 {
			drop(fmt.Sprintf("priority_%d_byte_limit", priority))
			return
		}
	}
	if w.detailItems >= maxItems {
		drop("item_limit")
		return
	}
	remaining := maxTotalBytes - w.detailBytes
	if priority > 0 {
		remaining = min(remaining, priorityByteLimit-w.detailPriorityBytes[priority])
	}
	if remaining <= 0 {
		drop("total_byte_limit")
		return
	}
	limit := min(maxItemBytes, remaining)
	before := archiveMessageDetailSnapshotBytes(event.Snapshot)
	if before > limit {
		event.Snapshot = engine.BoundErrorSnapshot(event.Snapshot, int(limit))
		stats.TruncatedItems++
	}
	retained := archiveMessageDetailSnapshotBytes(event.Snapshot)
	if event.Snapshot == nil || retained <= 0 || retained > limit {
		drop("total_byte_limit")
		return
	}
	w.detailItems++
	w.detailBytes += retained
	if priority > 0 {
		w.detailPriorityItems[priority]++
		w.detailPriorityBytes[priority] += retained
	}
	stats.RetainedItems++
	stats.RetainedBytes += retained
	result.Snapshot = event.Snapshot
	result.MessageDetailRetained = true
}

// adaptiveMessageDetailPriorityLimit divides a finite budget into the authored
// 25/25/50 low/mid/high shares without losing units to integer rounding. The
// highest priority receives the odd unit first, followed by the middle class;
// this keeps a one-item adaptive budget useful while the three limits always
// sum to the original total.
func adaptiveMessageDetailPriorityLimit(total int64, priority int) int64 {
	if total <= 0 || priority < 1 || priority > 3 {
		return 0
	}
	high := (total + 1) / 2
	remaining := total - high
	low := remaining / 2
	middle := remaining - low
	switch priority {
	case 1:
		return low
	case 2:
		return middle
	default:
		return high
	}
}

func archiveMessageDetailSnapshotBytes(snapshot *protocol.ErrorSnapshot) int64 {
	if snapshot == nil {
		return 0
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return 0
	}
	return int64(len(encoded))
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
	if w.flushErr != nil {
		return w.flushErr
	}
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
		Transactions:           intervalTransactionsFromLive(live.Transactions),
		ByRegion:               intervalTransactionsFromLive(live.ByRegion),
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

func (w *runArchiveWriter) Finalize(status string, completedAt time.Time, errStr string, slaEvaluated bool, slaBreached bool, slaDetails []string) (*storage.RunArchiveRecord, error) {
	w.stopFlushLoop()
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.flushErr != nil {
		return nil, errors.Join(w.flushErr, w.syncAndCloseDataFilesLocked())
	}
	w.manifest.Status = status
	w.manifest.CompletedAt = completedAt
	w.manifest.Error = errStr
	w.manifest.SLAEvaluated = slaEvaluated
	w.manifest.SLABreached = slaBreached
	w.manifest.SLADetails = slaDetails

	if err := errors.Join(w.flushBuffersLocked(), w.syncAndCloseDataFilesLocked()); err != nil {
		return nil, err
	}
	if err := applyTransactionSLARules(w.rootDir, &w.manifest); err != nil {
		return nil, err
	}
	if err := validateArchiveArtifactTargets(w.rootDir,
		w.manifest.TransactionsParquetFile,
		w.manifest.ResourceSamplesParquetFile,
		w.manifest.IntervalSummariesParquetFile,
		filepath.Base(w.manifestPath),
	); err != nil {
		return nil, err
	}
	if err := writeArchiveParquetFiles(w.rootDir, &w.manifest); err != nil {
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
	if err := writeArchiveManifestDurable(w.manifestPath, data); err != nil {
		return nil, err
	}
	manifestSHA256 := sha256BytesHex(data)
	backend := currentArchiveBackend()
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
	files := []*os.File{w.txFile, w.resourceFile, w.summaryFile}
	w.txFile = nil
	w.resourceFile = nil
	w.summaryFile = nil
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
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
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
	// Some Windows filesystems reject directory Sync with access denied even
	// after atomic rename/write succeeds. Treat this as non-fatal so archives
	// can still be published.
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
		{Name: manifest.TestManifestFile, Kind: "manifest", Format: "json", RecordType: "test_manifest"},
		{Name: manifest.TestNotesFile, Kind: "notes", Format: "json", RecordType: "test_notes"},
	}
	artifacts := make([]RunArchiveArtifact, 0, len(descriptors))
	for _, artifact := range descriptors {
		if strings.TrimSpace(artifact.Name) == "" {
			continue
		}
		file, info, err := openArchiveArtifactForRead(root, artifact.Name)
		if err != nil {
			if os.IsNotExist(err) {
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
