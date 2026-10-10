package api

// report — split from archive.go (same package, behavior-preserving).

import (
	"context"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func buildRunArchiveReport(record *storage.RunArchiveRecord) (*RunArchiveReport, error) {
	lease, err := acquireArchiveMaterialized(context.Background(), record)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return nil, err
	}
	resources, err := readAllArchiveResources(record)
	if err != nil {
		return nil, err
	}
	summaries, err := readAllArchiveSummaries(record)
	if err != nil {
		return nil, err
	}
	var elkSamples []ELKLogSample
	if strings.TrimSpace(manifest.ELKEvidenceFile) != "" {
		elkSamples, err = readAllArchiveELKSamples(record)
		if err != nil {
			return nil, err
		}
	}
	var dynatraceEvidence []DynatraceEvidenceRecord
	if strings.TrimSpace(manifest.DynatraceEvidenceFile) != "" {
		dynatraceEvidence, err = readAllArchiveDynatraceEvidence(record)
		if err != nil {
			return nil, err
		}
	}
	var monitorDiagnostics []RunMonitorDiagnostic
	if manifest.MonitorDiagnosticsFile != "" {
		monitorDiagnostics, err = readAllArchiveRecords[RunMonitorDiagnostic](record, func(m *RunArchiveManifest) string { return m.MonitorDiagnosticsFile })
		if err != nil {
			return nil, err
		}
	}

	report := &RunArchiveReport{
		TelemetryPolicy:     telemetryPolicyEvidenceFromProvenance(manifest.InitialProvenance),
		SchemaVersion:       runArchiveReportSchemaVersion,
		Manifest:            maskRunArchiveManifest(manifest),
		ByAction:            make(map[string]RunArchiveActionStats),
		ByGroup:             make(map[string]RunArchiveActionStats),
		BySOAPOperation:     make(map[string]RunArchiveActionStats),
		ByNetworkProfile:    make(map[string]RunArchiveActionStats),
		ByProperty:          make(map[string]RunArchiveActionStats),
		ByRegion:            make(map[string]RunArchiveActionStats),
		BySOAPFaultCode:     make(map[string]int64),
		BySOAPFaultCategory: make(map[string]int64),
		BrowserByEngine:     make(map[string]*BrowserEngineLiveMetrics),
		BrowserByPlatform:   make(map[string]*BrowserPlatformLiveMetrics),
		BrowserByDevice:     make(map[string]*BrowserDeviceLiveMetrics),
		LoadLevelSummaries:  buildRunArchiveLoadLevelSummaries(summaries),
		ELKSamples:          elkSamples,
		DynatraceEvidence:   dynatraceEvidence,
		MonitorDiagnostics:  monitorDiagnostics,
		MonitorRuleResults:  manifest.MonitorRuleResults,
	}
	report.BrowserDocuments, report.BrowserDocumentEvidence, report.BrowserDocumentEvidenceTruncated, err = readArchiveBrowserDocumentSummary(record, manifest)
	if err != nil {
		return nil, err
	}
	if report.BrowserDocuments != nil {
		report.BrowserSoftNavigation = protocol.BrowserSoftNavigationUnsupported
	}
	if diagnosticsProjectEnabled(manifest.Provenance.ProjectID) {
		report.StatisticalFindings = archiveStatisticalFindings(manifest.Provenance.ProjectID, record.TestID, summaries)
	}
	faultTimelineBuckets := make(map[time.Time]*RunArchiveSOAPFaultTimelineBucket)
	payloadTimelineBuckets := make(map[time.Time]*RunArchiveSOAPPayloadTimelineBucket)
	actionSummaryBuckets := make(map[string][]archiveActionSummaryBucket)
	errorPatterns := make(map[string]*RunArchiveErrorPattern)
	errorPatternActions := make(map[string]map[string]struct{})
	overallQuantiles := &archiveQuantileAccumulator{}
	actionQuantiles := make(map[string]*archiveQuantileAccumulator)
	groupQuantiles := make(map[string]*archiveQuantileAccumulator)
	soapOperationQuantiles := make(map[string]*archiveQuantileAccumulator)
	networkProfileQuantiles := make(map[string]*archiveQuantileAccumulator)
	propertyQuantiles := make(map[string]*archiveQuantileAccumulator)
	regionQuantiles := make(map[string]*archiveQuantileAccumulator)
	networkPhases := newRunArchiveNetworkPhaseAccumulator()
	web2Migration := newRunArchiveWeb2MigrationAccumulator()
	var browserArtifactCount int64
	var actionSummaryCells int
	quantilesFor := func(values map[string]*archiveQuantileAccumulator, key, dimension string) (*archiveQuantileAccumulator, error) {
		if quantiles := values[key]; quantiles != nil {
			return quantiles, nil
		}
		if len(values) >= maxArchiveAnalysisDimensions {
			return nil, fmt.Errorf("archive report exceeds %d distinct %s values", maxArchiveAnalysisDimensions, dimension)
		}
		quantiles := &archiveQuantileAccumulator{}
		values[key] = quantiles
		return quantiles, nil
	}

	namedTransactions := make(map[string]*archivePopulationAccumulator)
	requests := make(map[string]*archivePopulationAccumulator)
	unknownRecords := make(map[string]*archivePopulationAccumulator)
	populations := map[string]*archivePopulationAccumulator{"action": {}, "transaction": {}, "unknown": {}}
	var totalDuration float64
	transactionIndex := 0
	validityResults := newValidityResultCounter(archiveValidityIntervals(manifest, summaries))
	err = forEachArchiveTransaction(record, func(txPtr *TransactionEvent) error {
		validityResults.add(txPtr.Timestamp)
		tx := *txPtr
		normalizeArchiveTransactionSnapshot(&tx)
		population := archiveEventPopulation(tx)
		populations[population].add(tx)
		if population == "unknown" {
			if unknownRecords[tx.ActionName] == nil {
				if len(unknownRecords) >= maxArchiveAnalysisDimensions {
					return fmt.Errorf("archive exceeds unclassified record dimension limit")
				}
				unknownRecords[tx.ActionName] = &archivePopulationAccumulator{}
			}
			unknownRecords[tx.ActionName].add(tx)
		}
		if population == "action" {
			if requests[tx.ActionName] == nil {
				if len(requests) >= maxArchiveAnalysisDimensions {
					return fmt.Errorf("archive exceeds request dimension limit")
				}
				requests[tx.ActionName] = &archivePopulationAccumulator{}
			}
			requests[tx.ActionName].add(tx)
		}
		if population == "transaction" {
			if namedTransactions[tx.ActionName] == nil {
				if len(namedTransactions) >= maxArchiveAnalysisDimensions {
					return fmt.Errorf("archive exceeds named transaction dimension limit")
				}
				namedTransactions[tx.ActionName] = &archivePopulationAccumulator{}
			}
			namedTransactions[tx.ActionName].add(tx)
		}
		report.Overview.Transactions++
		totalDuration += tx.DurationMs
		overallQuantiles.Add(tx.DurationMs)
		if !tx.Success {
			report.Overview.Errors++
			signature := archiveErrorPatternSignature(tx)
			if _, exists := errorPatterns[signature]; !exists && len(errorPatterns) >= maxArchiveAnalysisDimensions {
				return fmt.Errorf("archive report exceeds %d distinct error patterns", maxArchiveAnalysisDimensions)
			}
			mergeArchiveErrorPattern(errorPatterns, errorPatternActions, tx)
		}
		if transactionIndex == 0 || tx.DurationMs < report.Overview.MinResponseMs {
			report.Overview.MinResponseMs = tx.DurationMs
		}
		if tx.DurationMs > report.Overview.MaxResponseMs {
			report.Overview.MaxResponseMs = tx.DurationMs
		}

		actionDigest, err := quantilesFor(actionQuantiles, tx.ActionName, "action")
		if err != nil {
			return err
		}
		stats := report.ByAction[tx.ActionName]
		report.ByAction[tx.ActionName] = mergeRunArchiveActionStats(stats, actionDigest, tx)
		if len(summaries) > 0 {
			index := archiveSummaryBucketIndex(summaries, tx.Timestamp)
			if index >= 0 {
				for _, key := range []string{tx.ActionName, population + "\x00" + tx.ActionName, population + "\x00"} {
					buckets := actionSummaryBuckets[key]
					if len(buckets) == 0 {
						if len(summaries) > maxArchiveReportAggregateCells-actionSummaryCells {
							return fmt.Errorf("archive report exceeds %d action summary buckets", maxArchiveReportAggregateCells)
						}
						buckets = make([]archiveActionSummaryBucket, len(summaries))
						actionSummaryCells += len(summaries)
					}
					buckets[index].transactions++
					actionSummaryBuckets[key] = buckets
				}
			}
		}
		if tx.Group != "" {
			quantiles, err := quantilesFor(groupQuantiles, tx.Group, "group")
			if err != nil {
				return err
			}
			report.ByGroup[tx.Group] = mergeRunArchiveActionStats(report.ByGroup[tx.Group], quantiles, tx)
		}
		if bucketTime := archiveSOAPFaultBucketTimestamp(tx.Timestamp); !bucketTime.IsZero() && (archiveSOAPFaultCode(tx) != "" || archiveSOAPFaultCategory(tx) != "") {
			if _, exists := faultTimelineBuckets[bucketTime]; !exists && len(faultTimelineBuckets) >= maxArchiveAnalysisDimensions {
				return fmt.Errorf("archive report exceeds %d SOAP fault timeline buckets", maxArchiveAnalysisDimensions)
			}
		}
		mergeArchiveSOAPFaultTimelineBucket(faultTimelineBuckets, tx)
		if bucketTime := archiveSOAPPayloadBucketTimestamp(tx.Timestamp); !bucketTime.IsZero() {
			requestKB, responseKB, overheadKB := archiveSOAPPayloadTelemetry(tx)
			if requestKB != 0 || responseKB != 0 || overheadKB != 0 {
				if _, exists := payloadTimelineBuckets[bucketTime]; !exists && len(payloadTimelineBuckets) >= maxArchiveAnalysisDimensions {
					return fmt.Errorf("archive report exceeds %d SOAP payload timeline buckets", maxArchiveAnalysisDimensions)
				}
			}
		}
		mergeArchiveSOAPPayloadTimelineBucket(payloadTimelineBuckets, tx)
		if operation := archiveSOAPOperation(tx); operation != "" {
			quantiles, err := quantilesFor(soapOperationQuantiles, operation, "SOAP operation")
			if err != nil {
				return err
			}
			report.BySOAPOperation[operation] = mergeRunArchiveActionStats(report.BySOAPOperation[operation], quantiles, tx)
		}
		if tx.NetworkProfile != "" {
			quantiles, err := quantilesFor(networkProfileQuantiles, tx.NetworkProfile, "network profile")
			if err != nil {
				return err
			}
			report.ByNetworkProfile[tx.NetworkProfile] = mergeRunArchiveActionStats(report.ByNetworkProfile[tx.NetworkProfile], quantiles, tx)
		}
		for property, value := range tx.PropertyDimensions {
			if population != "action" {
				continue
			}
			key := property + "=" + value
			quantiles, err := quantilesFor(propertyQuantiles, key, "property")
			if err != nil {
				return err
			}
			report.ByProperty[key] = mergeRunArchiveActionStats(report.ByProperty[key], quantiles, tx)
		}
		if tx.Region != "" {
			quantiles, err := quantilesFor(regionQuantiles, tx.Region, "region")
			if err != nil {
				return err
			}
			report.ByRegion[tx.Region] = mergeRunArchiveActionStats(report.ByRegion[tx.Region], quantiles, tx)
		}
		faultCode := archiveSOAPFaultCode(tx)
		if _, exists := report.BySOAPFaultCode[faultCode]; faultCode != "" && !exists && len(report.BySOAPFaultCode) >= maxArchiveAnalysisDimensions {
			return fmt.Errorf("archive report exceeds %d SOAP fault codes", maxArchiveAnalysisDimensions)
		}
		faultCategory := archiveSOAPFaultCategory(tx)
		if _, exists := report.BySOAPFaultCategory[faultCategory]; faultCategory != "" && !exists && len(report.BySOAPFaultCategory) >= maxArchiveAnalysisDimensions {
			return fmt.Errorf("archive report exceeds %d SOAP fault categories", maxArchiveAnalysisDimensions)
		}
		incrementArchiveCount(report.BySOAPFaultCode, faultCode)
		incrementArchiveCount(report.BySOAPFaultCategory, faultCategory)
		if tx.ClientMetrics != nil {
			if report.Overview.Browser == nil {
				report.Overview.Browser = &BrowserLiveMetrics{}
			}
			mergeReportBrowserMetrics(report.Overview.Browser, tx.ClientMetrics)
			if engine := tx.ClientMetrics.BrowserEngine; engine != "" {
				if _, exists := report.BrowserByEngine[engine]; !exists && len(report.BrowserByEngine) >= maxArchiveAnalysisDimensions {
					return fmt.Errorf("archive report exceeds %d browser engines", maxArchiveAnalysisDimensions)
				}
				engineMetrics, ok := report.BrowserByEngine[engine]
				if !ok {
					engineMetrics = &BrowserEngineLiveMetrics{BrowserEngine: engine}
					report.BrowserByEngine[engine] = engineMetrics
				}
				mergeReportBrowserEngineMetrics(engineMetrics, tx.ClientMetrics)
			}
			if platform := tx.ClientMetrics.DevicePlatform; platform != "" {
				if _, exists := report.BrowserByPlatform[platform]; !exists && len(report.BrowserByPlatform) >= maxArchiveAnalysisDimensions {
					return fmt.Errorf("archive report exceeds %d browser platforms", maxArchiveAnalysisDimensions)
				}
				platformMetrics, ok := report.BrowserByPlatform[platform]
				if !ok {
					platformMetrics = &BrowserPlatformLiveMetrics{
						DevicePlatform: platform,
						BrowserEngine:  tx.ClientMetrics.BrowserEngine,
					}
					report.BrowserByPlatform[platform] = platformMetrics
				}
				mergeReportBrowserPlatformMetrics(platformMetrics, tx.ClientMetrics)
			}
			if profile := tx.ClientMetrics.DeviceProfile; profile != "" {
				if _, exists := report.BrowserByDevice[profile]; !exists && len(report.BrowserByDevice) >= maxArchiveAnalysisDimensions {
					return fmt.Errorf("archive report exceeds %d browser device profiles", maxArchiveAnalysisDimensions)
				}
				deviceMetrics, ok := report.BrowserByDevice[profile]
				if !ok {
					deviceMetrics = &BrowserDeviceLiveMetrics{
						DeviceProfile:  profile,
						DevicePlatform: tx.ClientMetrics.DevicePlatform,
						DeviceTier:     tx.ClientMetrics.DeviceTier,
						BrowserEngine:  tx.ClientMetrics.BrowserEngine,
					}
					report.BrowserByDevice[profile] = deviceMetrics
				}
				mergeReportBrowserDeviceMetrics(deviceMetrics, tx.ClientMetrics)
			}
		}
		report.BrowserResources = appendTopBrowserResourceTimingsFromTransaction(report.BrowserResources, tx, 50)
		if tx.DNSMs > 0 || tx.ConnectMs > 0 || tx.TLSMs > 0 || tx.TTFBMs > 0 {
			dependencyKey := tx.DependencyTarget
			if tx.DependencyType != "" && dependencyKey != "" {
				dependencyKey = tx.DependencyType + ":" + dependencyKey
			}
			if _, exists := networkPhases.byDependency[dependencyKey]; dependencyKey != "" && !exists && len(networkPhases.byDependency) >= maxArchiveAnalysisDimensions {
				return fmt.Errorf("archive report exceeds %d network phase dependencies", maxArchiveAnalysisDimensions)
			}
		}
		networkPhases.merge(tx)
		if err := web2Migration.merge(tx); err != nil {
			return err
		}
		browserArtifactCount += countBrowserArtifactsForTransaction(tx)
		transactionIndex++
		return nil
	})
	if err != nil {
		return nil, err
	}
	if report.Overview.Transactions > 0 {
		report.Overview.AvgResponseMs = totalDuration / float64(report.Overview.Transactions)
		report.Overview.ErrorPct = float64(report.Overview.Errors) / float64(report.Overview.Transactions) * 100
		report.Overview.P50Ms = overallQuantiles.Quantile(0.50)
		report.Overview.P90Ms = overallQuantiles.Quantile(0.90)
		report.Overview.P95Ms = overallQuantiles.Quantile(0.95)
		report.Overview.P99Ms = overallQuantiles.Quantile(0.99)
	}
	if len(errorPatterns) > 0 {
		report.ErrorPatterns = make([]RunArchiveErrorPattern, 0, len(errorPatterns))
		for signature, pattern := range errorPatterns {
			if pattern == nil {
				continue
			}
			finalizeArchiveErrorPatterns(pattern)
			if actions := errorPatternActions[signature]; len(actions) > 0 {
				pattern.Actions = make([]string, 0, len(actions))
				for action := range actions {
					pattern.Actions = append(pattern.Actions, action)
				}
				sort.Strings(pattern.Actions)
			}
			report.ErrorPatterns = append(report.ErrorPatterns, *pattern)
		}
		sort.Slice(report.ErrorPatterns, func(left, right int) bool {
			if report.ErrorPatterns[left].Count == report.ErrorPatterns[right].Count {
				return report.ErrorPatterns[left].FirstSeen.Before(report.ErrorPatterns[right].FirstSeen)
			}
			return report.ErrorPatterns[left].Count > report.ErrorPatterns[right].Count
		})
	}
	finalizeReportBrowserMetrics(report.Overview.Browser)
	for _, metrics := range report.BrowserByEngine {
		finalizeReportBrowserEngineMetrics(metrics)
	}
	if len(report.BrowserByEngine) == 0 {
		report.BrowserByEngine = nil
	}
	for _, metrics := range report.BrowserByPlatform {
		finalizeReportBrowserPlatformMetrics(metrics)
	}
	if len(report.BrowserByPlatform) == 0 {
		report.BrowserByPlatform = nil
	}
	for _, metrics := range report.BrowserByDevice {
		finalizeReportBrowserDeviceMetrics(metrics)
	}
	if len(report.BrowserByDevice) == 0 {
		report.BrowserByDevice = nil
	}
	report.BrowserResources = finalizeBrowserResourceTimings(report.BrowserResources, 50)
	report.Web2Migration = web2Migration.finalize()
	for action, stats := range report.ByAction {
		stats.PeakTPS = archiveActionPeakTPSFromBuckets(summaries, actionSummaryBuckets[action])
		report.ByAction[action] = finalizeRunArchiveActionStats(stats, actionQuantiles[action])
	}
	for group, stats := range report.ByGroup {
		report.ByGroup[group] = finalizeRunArchiveActionStats(stats, groupQuantiles[group])
	}
	if len(report.ByGroup) == 0 {
		report.ByGroup = nil
	}
	for operation, stats := range report.BySOAPOperation {
		report.BySOAPOperation[operation] = finalizeRunArchiveActionStats(stats, soapOperationQuantiles[operation])
	}
	if len(report.BySOAPOperation) == 0 {
		report.BySOAPOperation = nil
	}
	for profile, stats := range report.ByNetworkProfile {
		report.ByNetworkProfile[profile] = finalizeRunArchiveActionStats(stats, networkProfileQuantiles[profile])
	}
	if len(report.ByNetworkProfile) == 0 {
		report.ByNetworkProfile = nil
	}
	for profile, stats := range report.ByProperty {
		report.ByProperty[profile] = finalizeRunArchiveActionStats(stats, propertyQuantiles[profile])
	}
	if len(report.ByProperty) == 0 {
		report.ByProperty = nil
	}
	for region, stats := range report.ByRegion {
		report.ByRegion[region] = finalizeRunArchiveActionStats(stats, regionQuantiles[region])
	}
	if len(report.ByRegion) == 0 {
		report.ByRegion = nil
	}
	if len(report.BySOAPFaultCode) == 0 {
		report.BySOAPFaultCode = nil
	}
	if len(report.BySOAPFaultCategory) == 0 {
		report.BySOAPFaultCategory = nil
	}
	if len(faultTimelineBuckets) > 0 {
		report.SOAPFaultTimeline = make([]RunArchiveSOAPFaultTimelineBucket, 0, len(faultTimelineBuckets))
		for _, bucket := range faultTimelineBuckets {
			if bucket == nil {
				continue
			}
			finalized := *bucket
			if len(finalized.ByCode) == 0 {
				finalized.ByCode = nil
			}
			if len(finalized.ByCategory) == 0 {
				finalized.ByCategory = nil
			}
			report.SOAPFaultTimeline = append(report.SOAPFaultTimeline, finalized)
		}
		sort.Slice(report.SOAPFaultTimeline, func(left, right int) bool {
			return report.SOAPFaultTimeline[left].Timestamp.Before(report.SOAPFaultTimeline[right].Timestamp)
		})
	}
	if len(payloadTimelineBuckets) > 0 {
		report.SOAPPayloadTimeline = make([]RunArchiveSOAPPayloadTimelineBucket, 0, len(payloadTimelineBuckets))
		for _, bucket := range payloadTimelineBuckets {
			if bucket == nil {
				continue
			}
			report.SOAPPayloadTimeline = append(report.SOAPPayloadTimeline, finalizeArchiveSOAPPayloadTimelineBucket(*bucket))
		}
		sort.Slice(report.SOAPPayloadTimeline, func(left, right int) bool {
			return report.SOAPPayloadTimeline[left].Timestamp.Before(report.SOAPPayloadTimeline[right].Timestamp)
		})
	}

	report.Validity = archiveRunValidity(manifest, validityResults.counted(), summaries, resources)
	report.TelemetryAcceptance = telemetryAcceptanceForManifest(manifest)
	report.Overview.ResourceSamples = int64(len(resources))
	for _, sample := range resources {
		if sample.CPUProcessPct > report.ResourcePeaks.CPUProcessPct {
			report.ResourcePeaks.CPUProcessPct = sample.CPUProcessPct
		}
		if sample.MemProcessMB > report.ResourcePeaks.MemProcessMB {
			report.ResourcePeaks.MemProcessMB = sample.MemProcessMB
		}
		if sample.NetSentKbps > report.ResourcePeaks.NetSentKbps {
			report.ResourcePeaks.NetSentKbps = sample.NetSentKbps
		}
		if sample.NetRecvKbps > report.ResourcePeaks.NetRecvKbps {
			report.ResourcePeaks.NetRecvKbps = sample.NetRecvKbps
		}
		if sample.DiskReadKbps > report.ResourcePeaks.DiskReadKbps {
			report.ResourcePeaks.DiskReadKbps = sample.DiskReadKbps
		}
		if sample.DiskWriteKbps > report.ResourcePeaks.DiskWriteKbps {
			report.ResourcePeaks.DiskWriteKbps = sample.DiskWriteKbps
		}
		if sample.CPUIOWaitPct > report.ResourcePeaks.CPUIOWaitPct {
			report.ResourcePeaks.CPUIOWaitPct = sample.CPUIOWaitPct
		}
		if sample.CPUStealPct > report.ResourcePeaks.CPUStealPct {
			report.ResourcePeaks.CPUStealPct = sample.CPUStealPct
		}
		if sample.GCPauseMs > report.ResourcePeaks.GCPauseMs {
			report.ResourcePeaks.GCPauseMs = sample.GCPauseMs
		}
		if sample.ContainerCPUUsagePct > report.ResourcePeaks.ContainerCPUUsagePct {
			report.ResourcePeaks.ContainerCPUUsagePct = sample.ContainerCPUUsagePct
		}
		if sample.ContainerCPUThrottledSecondsRate > report.ResourcePeaks.ContainerCPUThrottledSecondsRate {
			report.ResourcePeaks.ContainerCPUThrottledSecondsRate = sample.ContainerCPUThrottledSecondsRate
		}
		if sample.ContainerMemoryUsagePct > report.ResourcePeaks.ContainerMemoryUsagePct {
			report.ResourcePeaks.ContainerMemoryUsagePct = sample.ContainerMemoryUsagePct
		}
		if sample.ContainerOOMKillCountDelta > report.ResourcePeaks.ContainerOOMKillCountDelta {
			report.ResourcePeaks.ContainerOOMKillCountDelta = sample.ContainerOOMKillCountDelta
		}
		if sample.NetDropInPerSec > report.ResourcePeaks.NetDropInPerSec {
			report.ResourcePeaks.NetDropInPerSec = sample.NetDropInPerSec
		}
		if sample.NetDropOutPerSec > report.ResourcePeaks.NetDropOutPerSec {
			report.ResourcePeaks.NetDropOutPerSec = sample.NetDropOutPerSec
		}
		if sample.NetErrorInPerSec > report.ResourcePeaks.NetErrorInPerSec {
			report.ResourcePeaks.NetErrorInPerSec = sample.NetErrorInPerSec
		}
		if sample.NetErrorOutPerSec > report.ResourcePeaks.NetErrorOutPerSec {
			report.ResourcePeaks.NetErrorOutPerSec = sample.NetErrorOutPerSec
		}
		if sample.TCPRetransmitsPerSec > report.ResourcePeaks.TCPRetransmitsPerSec {
			report.ResourcePeaks.TCPRetransmitsPerSec = sample.TCPRetransmitsPerSec
		}
		if sample.DiskIOTimePct > report.ResourcePeaks.DiskIOTimePct {
			report.ResourcePeaks.DiskIOTimePct = sample.DiskIOTimePct
		}
		if sample.ActiveVUsers > report.Overview.PeakActiveVUsers {
			report.Overview.PeakActiveVUsers = sample.ActiveVUsers
		}
	}
	report.OracleDB = buildRunArchiveOracleDBEvidence(resources, summaries)

	report.Overview.IntervalSummaries = int64(len(summaries))
	if len(summaries) > 0 {
		report.SummaryTimeline = make([]RunArchiveSummaryPoint, 0, len(summaries))
	}
	telemetrySummary := RunArchiveTelemetrySummary{}
	for _, summary := range summaries {
		if summary.TPS > report.Overview.PeakTPS {
			report.Overview.PeakTPS = summary.TPS
		}
		if summary.TargetTPS > report.Overview.PeakTargetTPS {
			report.Overview.PeakTargetTPS = summary.TargetTPS
		}
		if summary.ThroughputGapPct > report.Overview.MaxThroughputGapPct {
			report.Overview.MaxThroughputGapPct = summary.ThroughputGapPct
		}
		if summary.ThroughputTargetMissed {
			report.Overview.ThroughputMissedIntervals++
		}
		if summary.ActiveVUsers > report.Overview.PeakActiveVUsers {
			report.Overview.PeakActiveVUsers = summary.ActiveVUsers
		}
		if summary.MessagingProgress != nil {
			protocol.MergeMessagingProgress(&report.MessagingProgress, summary.MessagingProgress)
		}
		mergeRunArchiveTelemetrySummary(&telemetrySummary, summary)
		report.SummaryTimeline = append(report.SummaryTimeline, RunArchiveSummaryPoint{
			BrowserDocuments:       summary.BrowserDocuments.Clone(),
			Timestamp:              summary.Timestamp,
			ActiveVUsers:           summary.ActiveVUsers,
			TPS:                    summary.TPS,
			TargetTPS:              summary.TargetTPS,
			ThroughputGapPct:       summary.ThroughputGapPct,
			ThroughputTargetMissed: summary.ThroughputTargetMissed,
			ErrorPct:               summary.ErrorPct,
			P95Ms:                  summary.P95Ms,
			PercentileQuality:      summary.PercentileQuality,
			TotalTx:                summary.TotalTx,
			TotalErrors:            summary.TotalErrors,
		})
	}
	if telemetrySummary.Samples > 0 {
		report.Overview.Telemetry = finalizeRunArchiveTelemetrySummary(telemetrySummary)
	}

	report.EvidenceTiers = buildRunArchiveEvidenceTiersFromCounts(manifest, report, browserArtifactCount, countExternalMetricSamples(resources), countOracleDBMetricSamples(resources))
	report.Compatibility = buildRunArchiveReportCompatibility(record, manifest)
	report.Completeness = buildRunArchiveCompletenessScoreFromCounts(manifest, report, report.Overview.Transactions, int64(len(resources)), int64(len(summaries)))
	report.ChangeAttribution = buildRunArchiveChangeAttribution(manifest)
	report.ReportProfiles = buildRunArchiveReportProfiles()
	report.NetworkPhases = networkPhases.finalizeWithExpectation(manifest.Provenance.ProtocolMix)
	report.HostNetwork = buildRunArchiveHostNetwork(manifest.HostNetwork)
	report.HTTP3Preflight = cloneHTTP3Preflight(manifest.Provenance.HTTP3Preflight)
	report.ArtifactInventory = buildRunArchiveArtifactInventory(record, manifest)

	window := archivePopulationWindow(manifest)
	report.ByUnknown = make(map[string]RunArchiveActionStats)
	for name, acc := range unknownRecords {
		stats := acc.finish(window).RunArchiveActionStats
		stats.PeakTPS = archiveActionPeakTPSFromBuckets(summaries, actionSummaryBuckets["unknown\x00"+name])
		report.ByUnknown[name] = stats
	}
	report.ByRequest = make(map[string]RunArchiveActionStats)
	for name, acc := range requests {
		stats := acc.finish(window).RunArchiveActionStats
		stats.PeakTPS = archiveActionPeakTPSFromBuckets(summaries, actionSummaryBuckets["action\x00"+name])
		report.ByRequest[name] = stats
	}
	report.ByTransaction = make(map[string]RunArchiveActionStats)
	for name, acc := range namedTransactions {
		stats := acc.finish(window).RunArchiveActionStats
		stats.PeakTPS = archiveActionPeakTPSFromBuckets(summaries, actionSummaryBuckets["transaction\x00"+name])
		report.ByTransaction[name] = stats
	}
	report.MetricPopulations = make(map[string]ArchiveMetricPopulation)
	for name, acc := range populations {
		metrics := acc.finish(window)
		metrics.PeakTPS = archiveActionPeakTPSFromBuckets(summaries, actionSummaryBuckets[name+"\x00"])
		report.MetricPopulations[name] = metrics
	}
	goalOverview := report.Overview
	goalOverview.Transactions = report.MetricPopulations["transaction"].Transactions
	report.Overview.LoadModelGoals, report.Overview.GoalCompletion = buildRunArchiveGoalCompletion(manifest.Provenance, goalOverview)
	report.Outcome = buildIntegrationOutcome(report)
	return report, nil
}

type archiveLoadLevelAccumulator struct {
	level       ActiveLoadLevel
	duration    float64
	requests    int64
	errors      int64
	tps         float64
	digest      *engine.TDigest
	digestValid bool
	sampleCount int
}

func buildRunArchiveLoadLevelSummaries(summaries []IntervalSummaryRecord) []RunArchiveLoadLevelSummary {
	accumulators := make(map[string]*archiveLoadLevelAccumulator)
	for index, summary := range summaries {
		seconds := archiveSummaryIntervalSeconds(summaries, index)
		for _, level := range summary.LoadLevels {
			key := fmt.Sprintf("%s\x00%d\x00%d", level.Group, level.StageIndex, level.TargetVUsers)
			accumulator := accumulators[key]
			if accumulator == nil {
				accumulator = &archiveLoadLevelAccumulator{level: level, digestValid: true}
				accumulators[key] = accumulator
			}
			var requests, errors int64
			var throughput float64
			if level.Group == "__total__" {
				requests, errors = summary.TotalTx, summary.TotalErrors
				throughput = summary.TPS
			} else if metrics := summary.Groups[level.Group]; metrics != nil {
				requests, errors = metrics.Transactions, metrics.Errors
				throughput = metrics.TPS
			} else {
				continue
			}
			attempts := requests + errors
			if attempts > 0 {
				snapshot := summary.LoadLevelDurationDigests[level.Group]
				if snapshot == nil || snapshot.Count != attempts {
					accumulator.digestValid = false
				} else {
					if accumulator.digest == nil {
						accumulator.digest = engine.NewTDigest(snapshot.Compression)
					}
					accumulator.digest.Merge(engine.NewTDigestFromSnapshot(*snapshot))
				}
			}
			accumulator.duration += seconds
			accumulator.requests += requests
			accumulator.errors += errors
			if seconds > 0 {
				accumulator.tps += throughput * seconds
			}
			accumulator.sampleCount++
		}
	}
	result := make([]RunArchiveLoadLevelSummary, 0, len(accumulators))
	for _, accumulator := range accumulators {
		weight := accumulator.duration
		if weight <= 0 {
			weight = float64(accumulator.sampleCount)
		}
		errorPct := 0.0
		attempts := accumulator.requests + accumulator.errors
		if attempts > 0 {
			errorPct = float64(accumulator.errors) * 100 / float64(attempts)
		}
		configured, _ := time.ParseDuration(accumulator.level.Duration)
		observedDuration := accumulator.duration
		if configured > 0 && observedDuration > configured.Seconds() {
			observedDuration = configured.Seconds()
		}
		var p50, p90, p95, p99 float64
		if accumulator.digestValid && accumulator.digest != nil && accumulator.digest.Count() == attempts {
			p50 = accumulator.digest.Quantile(0.50) / float64(time.Millisecond)
			p90 = accumulator.digest.Quantile(0.90) / float64(time.Millisecond)
			p95 = accumulator.digest.Quantile(0.95) / float64(time.Millisecond)
			p99 = accumulator.digest.Quantile(0.99) / float64(time.Millisecond)
		}
		result = append(result, RunArchiveLoadLevelSummary{
			Group: accumulator.level.Group, StageIndex: accumulator.level.StageIndex, Name: accumulator.level.Name,
			TargetVUsers: accumulator.level.TargetVUsers, ConfiguredDuration: accumulator.level.Duration,
			ObservedDurationSeconds: observedDuration, Requests: accumulator.requests, Errors: accumulator.errors,
			ErrorRatePct: errorPct, Throughput: accumulator.tps / weight, P50Ms: p50,
			P90Ms: p90, P95Ms: p95, P99Ms: p99,
			Partial: configured > 0 && observedDuration+0.001 < configured.Seconds(),
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Group != result[j].Group {
			return result[i].Group < result[j].Group
		}
		return result[i].StageIndex < result[j].StageIndex
	})
	return result
}

func archiveSummaryIntervalSeconds(summaries []IntervalSummaryRecord, index int) float64 {
	if index >= 0 && index < len(summaries) && summaries[index].SampleWindowSeconds > 0 {
		window := summaries[index].SampleWindowSeconds
		current := summaries[index].ActiveRuntimeSeconds
		if current == nil {
			return window
		}
		advance := *current
		if index > 0 && summaries[index-1].ActiveRuntimeSeconds != nil {
			advance = *current - *summaries[index-1].ActiveRuntimeSeconds
		}
		if advance <= 0 {
			return 0
		}
		if advance < window {
			return advance
		}
		return window
	}
	if len(summaries) < 2 {
		return 0
	}
	if index+1 < len(summaries) {
		if current, next := summaries[index].ActiveRuntimeSeconds, summaries[index+1].ActiveRuntimeSeconds; current != nil && next != nil && *next >= *current {
			return *next - *current
		}
		if seconds := summaries[index+1].Timestamp.Sub(summaries[index].Timestamp).Seconds(); seconds > 0 {
			return seconds
		}
	}
	if index > 0 {
		if previous, current := summaries[index-1].ActiveRuntimeSeconds, summaries[index].ActiveRuntimeSeconds; previous != nil && current != nil && *current >= *previous {
			return *current - *previous
		}
		if seconds := summaries[index].Timestamp.Sub(summaries[index-1].Timestamp).Seconds(); seconds > 0 {
			return seconds
		}
	}
	return 0
}

func buildRunArchiveWeb2MigrationEvidence(transactions []TransactionEvent) *RunArchiveWeb2MigrationEvidence {
	accumulator := newRunArchiveWeb2MigrationAccumulator()
	for _, tx := range transactions {
		// A caller holding an in-memory slice already accepted its cardinality;
		// retain the legacy helper's no-error contract.
		_ = accumulator.merge(tx)
	}
	return accumulator.finalize()
}

type runArchiveWeb2MigrationAccumulator struct {
	stepSet        map[string]struct{}
	transactionSet map[string]struct{}
	profileSet     map[string]struct{}
	evidence       map[string]int
	replayed       int
	failed         int
}

func newRunArchiveWeb2MigrationAccumulator() *runArchiveWeb2MigrationAccumulator {
	return &runArchiveWeb2MigrationAccumulator{
		stepSet:        make(map[string]struct{}),
		transactionSet: make(map[string]struct{}),
		profileSet:     make(map[string]struct{}),
		evidence: map[string]int{
			"screenshots":        0,
			"dom_summaries":      0,
			"console_entries":    0,
			"network_entries":    0,
			"client_metrics":     0,
			"source_step_labels": 0,
		},
	}
}

func (acc *runArchiveWeb2MigrationAccumulator) merge(tx TransactionEvent) error {
	stepKey := web2SourceStepKey(tx)
	if stepKey == "" {
		return nil
	}
	if _, exists := acc.stepSet[stepKey]; !exists && len(acc.stepSet) >= maxArchiveAnalysisDimensions {
		return fmt.Errorf("archive report exceeds %d Web 2.0 source steps", maxArchiveAnalysisDimensions)
	}
	acc.stepSet[stepKey] = struct{}{}
	acc.replayed++
	if !tx.Success {
		acc.failed++
	}
	acc.evidence["source_step_labels"]++
	if tx.SourceTransaction != "" {
		if _, exists := acc.transactionSet[tx.SourceTransaction]; !exists && len(acc.transactionSet) >= maxArchiveAnalysisDimensions {
			return fmt.Errorf("archive report exceeds %d Web 2.0 transaction groups", maxArchiveAnalysisDimensions)
		}
		acc.transactionSet[tx.SourceTransaction] = struct{}{}
	}
	if tx.SourceProfile != "" {
		if _, exists := acc.profileSet[tx.SourceProfile]; !exists && len(acc.profileSet) >= maxArchiveAnalysisDimensions {
			return fmt.Errorf("archive report exceeds %d Web 2.0 source profiles", maxArchiveAnalysisDimensions)
		}
		acc.profileSet[tx.SourceProfile] = struct{}{}
	}
	if tx.Snapshot != nil {
		if tx.Snapshot.ScreenshotBase64 != "" {
			acc.evidence["screenshots"]++
		}
		if tx.Snapshot.DOMSummary != nil || tx.Snapshot.HTML != "" {
			acc.evidence["dom_summaries"]++
		}
		acc.evidence["console_entries"] += len(tx.Snapshot.ConsoleEntries)
		acc.evidence["network_entries"] += len(tx.Snapshot.NetworkEntries)
	}
	if tx.DOMSummary != nil {
		acc.evidence["dom_summaries"]++
	}
	acc.evidence["console_entries"] += len(tx.ConsoleEntries)
	acc.evidence["network_entries"] += len(tx.NetworkEntries)
	if tx.ClientMetrics != nil {
		acc.evidence["client_metrics"]++
	}
	return nil
}

func (acc *runArchiveWeb2MigrationAccumulator) finalize() *RunArchiveWeb2MigrationEvidence {
	if acc == nil || acc.replayed == 0 {
		return nil
	}
	status := "ready_for_review"
	nextActions := []string{"Review browser replay evidence, runtime parity, and selector stability before approval."}
	if acc.failed > 0 {
		status = "needs_repair"
		nextActions = append(nextActions, "Repair failed Web 2.0 replay steps and rerun browser replay.")
	}
	conversionReady := acc.evidence["network_entries"] > 0 && acc.failed == 0
	if !conversionReady {
		nextActions = append(nextActions, "Capture replay network entries before generating a protocol-load HTTP draft.")
	}
	return &RunArchiveWeb2MigrationEvidence{
		Status:                status,
		ImportedSteps:         len(acc.stepSet),
		ReplayedSteps:         acc.replayed,
		FailedSteps:           acc.failed,
		TransactionGroups:     sortedArchiveStringSet(acc.transactionSet),
		SourceProfiles:        sortedArchiveStringSet(acc.profileSet),
		ReplayEvidence:        acc.evidence,
		ConversionToHTTPReady: conversionReady,
		NextActions:           uniqueSortedStrings(nextActions),
	}
}

func web2SourceStepKey(tx TransactionEvent) string {
	if tx.SourceStepID != "" {
		return tx.SourceStepID
	}
	if tx.SourceStepIndex > 0 {
		return fmt.Sprintf("index:%d", tx.SourceStepIndex)
	}
	if tx.SourceAction != "" || tx.SourceActionName != "" || tx.SourceTransaction != "" || tx.SourceProfile != "" {
		return strings.Join([]string{tx.SourceTransaction, tx.SourceActionName, tx.SourceAction, tx.ActionName}, ":")
	}
	return ""
}

func sortedArchiveStringSet(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func buildRunArchiveGoalCompletion(provenance RunArchiveProvenance, overview RunArchiveOverview) (*RunArchiveLoadModelGoals, *RunArchiveGoalCompletion) {
	goals := &RunArchiveLoadModelGoals{}
	for _, group := range provenance.Groups {
		if len(group.Stages) > 0 {
			goals.StagedGroups++
			goals.StageCount += len(group.Stages)
			for _, stage := range group.Stages {
				if stage.TargetTPS != nil && *stage.TargetTPS > goals.TargetTPS {
					goals.TargetTPS = *stage.TargetTPS
				}
			}
		}
		if strings.EqualFold(strings.TrimSpace(group.Initialization), "at_once") {
			goals.OnceOnlyGroups++
		}
		if group.RunLogicGoals != nil {
			if group.RunLogicGoals.GlobalIterations > 0 {
				goals.ConfiguredGlobalIterations += int64(group.RunLogicGoals.GlobalIterations)
			}
			if group.RunLogicGoals.MaxTransactions > 0 {
				goals.ConfiguredMaxTransactions += group.RunLogicGoals.MaxTransactions
			}
		}
	}
	if provenance.TrafficProfile != nil && provenance.TrafficProfile.RuntimeTarget == "tps" {
		if peak := trafficProfilePeakAggregateTarget(provenance.TrafficProfile); peak > goals.TargetTPS {
			goals.TargetTPS = peak
		}
	}
	if overview.PeakTargetTPS > goals.TargetTPS {
		goals.TargetTPS = overview.PeakTargetTPS
	}
	if goals.ConfiguredGlobalIterations == 0 && goals.ConfiguredMaxTransactions == 0 && goals.TargetTPS == 0 && goals.StageCount == 0 && goals.OnceOnlyGroups == 0 {
		return nil, nil
	}
	completion := &RunArchiveGoalCompletion{
		AchievedTransactions:       overview.Transactions,
		ConfiguredMaxTransactions:  goals.ConfiguredMaxTransactions,
		ConfiguredGlobalIterations: goals.ConfiguredGlobalIterations,
		ThroughputTargetTPS:        goals.TargetTPS,
		ThroughputMissedIntervals:  overview.ThroughputMissedIntervals,
	}
	if goals.ConfiguredMaxTransactions > 0 {
		completion.TransactionCompletionPct = math.Min(100, float64(overview.Transactions)/float64(goals.ConfiguredMaxTransactions)*100)
	}
	if goals.ConfiguredGlobalIterations > 0 {
		completion.IterationEvidence = "Configured global iteration goals are archived; achieved iteration counts are inferred from transaction archives when action topology allows."
	}
	return goals, completion
}

func buildRunArchiveReportProfiles() []RunArchiveReportProfile {
	return []RunArchiveReportProfile{
		{
			ID:            "executive",
			Label:         "Executive",
			Description:   "Decision-focused summary with analyst commentary, supporting graphs, SLA verdict, error rate, throughput, release trend, and run context.",
			Audience:      "Business stakeholders, release owners, and sign-off authorities.",
			BestFor:       "Ship/no-ship decisions, business risk, SLA posture, and executive sign-off.",
			ExportFormats: []string{"html", "json", "csv", "analysis_zip", "pdf", "docx"},
			// "release_trend" renders real release-over-release data: buildRunArchiveReport
			// stays a pure single-record function, so the multi-run trend is attached at the
			// storage-aware handler layer ((*Server).attachReleaseTrend) and the section
			// degrades to a no-baseline/no-release note rather than a ghost. "run_context"
			// folds completeness + change attribution into one compact confidence block.
			IncludedSections: []string{"baseline_comparison", "verdict", "executive_overview", "graphs", "dashboard_panels", "sla_rollup", "telemetry_acceptance", "release_trend", "risk_summary", "rca", "run_context", "recommendations", "signoff"},
			SectionOrder:     []string{"baseline_comparison", "verdict", "executive_overview", "graphs", "dashboard_panels", "sla_rollup", "telemetry_acceptance", "release_trend", "risk_summary", "rca", "run_context", "recommendations", "signoff"},
			// The executive report states the leading hypothesis only.
			RootCauseHypothesisLimit: 1,
		},
		{
			ID:               "engineer",
			Label:            "Engineer",
			Description:      "Engineering drilldown with analyst commentary, supporting graphs, timelines, action stats, browser diagnostics, resource peaks, and error patterns.",
			Audience:         "Performance engineers, SREs, developers, and test analysts.",
			BestFor:          "Bottleneck isolation, regression diagnosis, resource saturation, and remediation planning.",
			ExportFormats:    []string{"html", "json", "csv", "analysis_zip", "pdf", "docx", "jsonl", "parquet"},
			IncludedSections: []string{"baseline_comparison", "verdict", "technical_overview", "sla_detail", "graphs", "dashboard_panels", "rca", "root_cause", "recommendations", "test_setup", "raw_exports", "generator_manifest", "controller_failover", "telemetry_acceptance", "completeness", "schema_compatibility", "change_attribution", "summary_timeline", "errors", "resources", "actions", "protocol_diagnostics", "browser", "browser_findings", "interaction_map", "journey_risk", "soap", "network_phases", "web2_migration"},
			SectionOrder:     []string{"baseline_comparison", "verdict", "technical_overview", "sla_detail", "graphs", "dashboard_panels", "rca", "root_cause", "recommendations", "test_setup", "raw_exports", "generator_manifest", "controller_failover", "telemetry_acceptance", "completeness", "schema_compatibility", "change_attribution", "summary_timeline", "errors", "resources", "actions", "protocol_diagnostics", "browser", "browser_findings", "interaction_map", "journey_risk", "soap", "network_phases", "web2_migration"},
		},
		{
			ID:               "audit",
			Label:            "Audit",
			Description:      "Governance package with schema compatibility, evidence tiers, completeness findings, provenance, and artifact inventory.",
			Audience:         "Compliance, governance, change-control, and QA gatekeepers.",
			BestFor:          "Evidence integrity, authorization traceability, reproducibility, and audit review.",
			ExportFormats:    []string{"html", "json", "csv", "analysis_zip", "pdf", "docx"},
			IncludedSections: []string{"baseline_comparison", "official_outcome", "audit_identity", "authorization", "governance", "provenance", "schema_compatibility", "evidence_tiers", "controller_failover", "telemetry_acceptance", "completeness", "change_attribution", "artifact_integrity", "report_authorship"},
			SectionOrder:     []string{"baseline_comparison", "official_outcome", "audit_identity", "authorization", "governance", "provenance", "schema_compatibility", "evidence_tiers", "controller_failover", "telemetry_acceptance", "completeness", "change_attribution", "artifact_integrity", "report_authorship"},
		},
	}
}

func lookupRunArchiveReportProfile(profileID string) (RunArchiveReportProfile, bool) {
	profileID = strings.ToLower(strings.TrimSpace(profileID))
	if profileID == "" {
		profileID = "engineer"
	}
	for _, profile := range buildRunArchiveReportProfiles() {
		if profile.ID == profileID {
			return profile, true
		}
	}
	return RunArchiveReportProfile{}, false
}

func buildRunArchiveChangeAttribution(manifest *RunArchiveManifest) RunArchiveChangeAttribution {
	if manifest == nil {
		return RunArchiveChangeAttribution{}
	}
	provenance := manifest.Provenance
	scriptRevisions := make([]string, 0, len(provenance.Groups))
	seenScripts := map[string]struct{}{}
	for _, group := range provenance.Groups {
		revision := strings.TrimSpace(group.ScriptID)
		if revision == "" {
			revision = strings.TrimSpace(group.ScriptName)
		}
		if revision == "" {
			continue
		}
		if _, ok := seenScripts[revision]; ok {
			continue
		}
		seenScripts[revision] = struct{}{}
		scriptRevisions = append(scriptRevisions, revision)
	}
	sort.Strings(scriptRevisions)

	generatorVersions := make([]string, 0, len(provenance.GeneratorInventory))
	seenVersions := map[string]struct{}{}
	for _, entry := range provenance.GeneratorInventory {
		version := strings.TrimSpace(entry.Version)
		if version == "" {
			continue
		}
		if _, ok := seenVersions[version]; ok {
			continue
		}
		seenVersions[version] = struct{}{}
		generatorVersions = append(generatorVersions, version)
	}
	sort.Strings(generatorVersions)

	return RunArchiveChangeAttribution{
		ScriptRevisions:       scriptRevisions,
		ScenarioRevision:      strings.TrimSpace(provenance.ScenarioID),
		Environment:           archiveFirstNonEmpty(provenance.ControllerEnv["APEXLOAD_ENVIRONMENT"], provenance.ControllerEnv["APEXLOAD_ENV"]),
		GeneratorVersions:     generatorVersions,
		DataRevision:          dataRevisionFromGroups(provenance.Groups),
		RuntimeOverrides:      provenance.RuntimeSettings != nil,
		RuntimeOverrideFields: runtimeOverrideFieldNames(provenance.RuntimeSettings),
	}
}

func dataRevisionFromGroups(groups []TestGroup) string {
	values := make([]string, 0, len(groups))
	seen := map[string]struct{}{}
	for _, group := range groups {
		if group.ScriptID == "" {
			continue
		}
		value := group.ScriptID + ":data"
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		values = append(values, value)
	}
	sort.Strings(values)
	return strings.Join(values, ",")
}

func runtimeOverrideFieldNames(settings *RuntimeSettingsOverride) []string {
	if settings == nil {
		return nil
	}
	fields := make([]string, 0, 6)
	if settings.Results != nil {
		fields = append(fields, "results")
	}
	if settings.Iterations != nil {
		fields = append(fields, "iterations")
	}
	if settings.Pacing != nil {
		fields = append(fields, "pacing")
	}
	if settings.ThinkTime != nil {
		fields = append(fields, "think_time")
	}
	if settings.Browser != nil {
		fields = append(fields, "browser")
	}
	if strings.TrimSpace(settings.LogLevel) != "" {
		fields = append(fields, "log_level")
	}
	if strings.TrimSpace(settings.AutoTransactions) != "" {
		fields = append(fields, "auto_transactions")
	}
	if strings.TrimSpace(settings.BaseURL) != "" {
		fields = append(fields, "base_url")
	}
	if len(settings.Env) > 0 {
		fields = append(fields, "env")
	}
	if len(settings.RedactionRules) > 0 {
		fields = append(fields, "redaction_rules")
	}
	if settings.ErrorHandling != nil {
		fields = append(fields, "error_handling")
	}
	if settings.Log != nil {
		fields = append(fields, "log")
	}
	if settings.RunLogic != nil {
		fields = append(fields, "run_logic")
	}
	if settings.HTTP != nil {
		fields = append(fields, "http")
	}
	if settings.WebRuntime != nil {
		fields = append(fields, "web_runtime")
	}
	if settings.BrowserConfig != nil {
		fields = append(fields, "browser_config")
	}
	if settings.BrowserEmulation != nil {
		fields = append(fields, "browser_emulation")
	}
	if settings.JavaVuser != nil {
		fields = append(fields, "java_vuser")
	}
	return fields
}

func archiveFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func buildRunArchiveCompletenessScore(manifest *RunArchiveManifest, report *RunArchiveReport, transactions []TransactionEvent, resources []ResourceSample, summaries []IntervalSummaryRecord) RunArchiveCompletenessScore {
	return buildRunArchiveCompletenessScoreFromCounts(manifest, report, int64(len(transactions)), int64(len(resources)), int64(len(summaries)))
}

func buildRunArchiveCompletenessScoreFromCounts(manifest *RunArchiveManifest, report *RunArchiveReport, transactionCount, resourceCount, summaryCount int64) RunArchiveCompletenessScore {
	score := RunArchiveCompletenessScore{ScorePct: 100, Status: "complete"}
	addFinding := func(dimension, severity string, penalty float64, message string) {
		score.Findings = append(score.Findings, RunArchiveCompletenessFinding{
			Dimension: dimension,
			Severity:  severity,
			Penalty:   penalty,
			Message:   message,
		})
		score.ScorePct -= penalty
	}
	if manifest == nil {
		addFinding("summary", "critical", 40, "Archive manifest is missing.")
	} else {
		if finding := archiveSourceCoverageFinding(manifest); finding != nil {
			addFinding(finding.Dimension, finding.Severity, finding.Penalty, finding.Message)
		}
		if strings.TrimSpace(manifest.Error) != "" {
			addFinding("archive_writer", "critical", 25, "Archive writer recorded an error: "+manifest.Error)
		}
		if manifest.TransactionCount > transactionCount {
			addFinding("artifact_truncation", "critical", 20, fmt.Sprintf("Transaction archive contains %d of %d expected events.", transactionCount, manifest.TransactionCount))
		}
		if manifest.ResourceSampleCount > resourceCount {
			addFinding("artifact_truncation", "warning", 10, fmt.Sprintf("Resource archive contains %d of %d expected samples.", resourceCount, manifest.ResourceSampleCount))
		}
		if manifest.IntervalSummaryCount > summaryCount {
			addFinding("missing_intervals", "critical", 20, fmt.Sprintf("Interval archive contains %d of %d expected summaries.", summaryCount, manifest.IntervalSummaryCount))
		}
	}
	if summaryCount == 0 {
		addFinding("missing_intervals", "warning", 15, "No interval summaries were archived for timeline or telemetry review.")
	} else if report == nil || report.Overview.Telemetry == nil || report.Overview.Telemetry.Samples == 0 {
		addFinding("telemetry_gaps", "warning", 15, "Interval summaries do not include telemetry health samples.")
	} else {
		telemetry := report.Overview.Telemetry
		if telemetry.DroppedResults > 0 || telemetry.WebsocketDroppedMessages > 0 || telemetry.WebsocketDroppedEvents > 0 {
			addFinding("telemetry_gaps", "warning", 10, "Telemetry reported dropped collector or websocket samples during the run.")
		}
		if telemetry.BreachedIntervals > 0 || telemetry.SustainedBreachIntervals > 0 {
			addFinding("telemetry_gaps", "critical", 15, "Telemetry SLA breaches were archived for one or more intervals.")
		} else if telemetry.DegradedIntervals > 0 {
			addFinding("telemetry_gaps", "warning", 8, "Telemetry was degraded for one or more archived intervals.")
		} else if telemetry.UnscoredIntervals > 0 {
			addFinding("telemetry_gaps", "warning", 5, "Telemetry budget decisions could not be scored (generator clock quality) for one or more archived intervals.")
		}
	}
	if report != nil {
		for _, output := range report.Compatibility.Outputs {
			if output.Status == "missing" {
				addFinding("artifact_truncation", "warning", 5, fmt.Sprintf("Expected %s output %q is missing.", output.Format, output.Artifact))
			}
		}
	}
	if score.ScorePct < 0 {
		score.ScorePct = 0
	}
	switch {
	case score.ScorePct >= 95:
		score.Status = "complete"
	case score.ScorePct >= 75:
		score.Status = "needs_review"
	default:
		score.Status = "incomplete"
	}
	return score
}

func buildRunArchiveReportCompatibility(record *storage.RunArchiveRecord, manifest *RunArchiveManifest) RunArchiveReportCompatibility {
	compatibility := RunArchiveReportCompatibility{
		Status:                   "compatible",
		ReportSchemaVersion:      runArchiveReportSchemaVersion,
		SupportedManifestSchemas: []string{archiveSchemaVersionV1, archiveSchemaVersionV2, archiveSchemaVersionV3, archiveSchemaVersion},
	}
	if manifest != nil {
		compatibility.ManifestSchemaVersion = manifest.SchemaVersion
	}
	if !supportedArchiveManifestSchema(compatibility.ManifestSchemaVersion) {
		compatibility.Status = "incompatible"
		compatibility.Issues = append(compatibility.Issues, RunArchiveCompatibilityIssue{
			Severity: "error",
			Code:     "unsupported_manifest_schema",
			Message:  fmt.Sprintf("manifest schema %q is not supported by report schema %q", compatibility.ManifestSchemaVersion, runArchiveReportSchemaVersion),
		})
	}

	manifestOK := compatibility.Status == "compatible"
	compatibility.Outputs = []RunArchiveOutputCompatibility{
		reportOutputCompatibility("json", "archive/report", runArchiveReportSchemaVersion, manifestOK, "JSON report is generated from the archive manifest and event files."),
		reportOutputCompatibility("standalone_html", "report", runArchiveReportSchemaVersion, manifestOK, "Standalone HTML report is rendered from the JSON report schema."),
		archiveFileOutputCompatibility(record, manifestField(manifest, "transactions_jsonl"), "transaction_jsonl", "transactions.jsonl", archiveSchemaVersion),
		archiveFileOutputCompatibility(record, manifestField(manifest, "resources_jsonl"), "resource_jsonl", "resources.jsonl", archiveSchemaVersion),
		archiveFileOutputCompatibility(record, manifestField(manifest, "interval_summaries_jsonl"), "interval_summary_jsonl", "interval_summaries.jsonl", archiveSchemaVersion),
		archiveFileOutputCompatibility(record, manifestField(manifest, "transactions_parquet"), "transactions_parquet", "transactions.parquet", archiveSchemaVersion),
		archiveFileOutputCompatibility(record, manifestField(manifest, "resources_parquet"), "resources_parquet", "resources.parquet", archiveSchemaVersion),
		archiveFileOutputCompatibility(record, manifestField(manifest, "interval_summaries_parquet"), "interval_summaries_parquet", "interval_summaries.parquet", archiveSchemaVersion),
		reportOutputCompatibility("csv", "archive/report?format=csv", runArchiveReportSchemaVersion, manifestOK, "CSV report export is generated from the archive report schema."),
		reportOutputCompatibility("pdf", "archive/report?format=pdf", runArchiveReportSchemaVersion, manifestOK, "PDF report export is generated from the archive report schema for profile-shaped reports."),
		reportOutputCompatibility("docx", "archive/report?format=docx", runArchiveReportSchemaVersion, manifestOK, "DOCX report export is generated directly from the shared profile and section registry."),
	}
	for _, output := range compatibility.Outputs {
		switch output.Status {
		case "missing":
			compatibility.Issues = append(compatibility.Issues, RunArchiveCompatibilityIssue{
				Severity: "warning",
				Code:     "missing_output",
				Message:  fmt.Sprintf("%s output %q is missing", output.Format, output.Artifact),
			})
		case "incompatible":
			compatibility.Status = "incompatible"
			compatibility.Issues = append(compatibility.Issues, RunArchiveCompatibilityIssue{
				Severity: "error",
				Code:     "incompatible_output",
				Message:  fmt.Sprintf("%s output %q is incompatible: %s", output.Format, output.Artifact, output.Reason),
			})
		}
	}
	return compatibility
}

func supportedArchiveManifestSchema(version string) bool {
	switch version {
	case archiveSchemaVersion, archiveSchemaVersionV3, archiveSchemaVersionV2, archiveSchemaVersionV1:
		return true
	default:
		return false
	}
}

func reportOutputCompatibility(format, artifact, schemaVersion string, compatible bool, reason string) RunArchiveOutputCompatibility {
	status := "compatible"
	if !compatible {
		status = "incompatible"
	}
	return RunArchiveOutputCompatibility{
		Format:        format,
		Artifact:      artifact,
		SchemaVersion: schemaVersion,
		Status:        status,
		Reason:        reason,
	}
}

func archiveFileOutputCompatibility(record *storage.RunArchiveRecord, artifactName, format, fallbackArtifact, schemaVersion string) RunArchiveOutputCompatibility {
	if strings.TrimSpace(artifactName) == "" {
		artifactName = fallbackArtifact
	}
	output := RunArchiveOutputCompatibility{
		Format:        format,
		Artifact:      artifactName,
		SchemaVersion: schemaVersion,
		Status:        "compatible",
	}
	if record == nil || strings.TrimSpace(record.ArchivePath) == "" {
		output.Status = "missing"
		output.Reason = "archive root is not available"
		return output
	}
	path, err := archiveArtifactPath(record.ArchivePath, artifactName)
	if err != nil {
		output.Status = "missing"
		output.Reason = "archive artifact path is invalid"
		return output
	}
	if _, err := os.Stat(path); err != nil {
		output.Status = "missing"
		output.Reason = "artifact is not present in the archive bundle"
	}
	return output
}

func manifestField(manifest *RunArchiveManifest, field string) string {
	if manifest == nil {
		return ""
	}
	switch field {
	case "transactions_jsonl":
		return manifest.TransactionsFile
	case "resources_jsonl":
		return manifest.ResourceSamplesFile
	case "interval_summaries_jsonl":
		return manifest.IntervalSummariesFile
	case "transactions_parquet":
		return manifest.TransactionsParquetFile
	case "resources_parquet":
		return manifest.ResourceSamplesParquetFile
	case "interval_summaries_parquet":
		return manifest.IntervalSummariesParquetFile
	default:
		return ""
	}
}

func buildRunArchiveEvidenceTiers(manifest *RunArchiveManifest, report *RunArchiveReport, transactions []TransactionEvent, resources []ResourceSample, summaries []IntervalSummaryRecord) []RunArchiveEvidenceTier {
	return buildRunArchiveEvidenceTiersFromCounts(manifest, report, countBrowserArtifacts(transactions), countExternalMetricSamples(resources), countOracleDBMetricSamples(resources))
}

func buildRunArchiveEvidenceTiersFromCounts(manifest *RunArchiveManifest, report *RunArchiveReport, browserArtifactCount, externalMetricSamples, oracleDBMetricSamples int64) []RunArchiveEvidenceTier {
	if report == nil {
		return nil
	}
	tiers := []RunArchiveEvidenceTier{
		evidenceTier("summary", "Summary", report.Overview.Transactions+report.Overview.IntervalSummaries+report.Overview.ResourceSamples, "Aggregated run totals, response-time range, resource peaks, and headline telemetry."),
		evidenceTier("intervals", "Intervals", report.Overview.IntervalSummaries, "Per-interval live metric summaries used for timelines, throughput evidence, and telemetry SLA review."),
		evidenceTier("sampled_diagnostics", "Sampled Diagnostics", int64(len(report.ErrorPatterns)+len(report.BrowserResources)), "Grouped error signatures and sampled browser resource diagnostics for triage."),
		evidenceTier("transaction_archive", "Transaction Archive", report.Overview.Transactions, "Raw transaction events with action, timing, dependency, status, correlation, and snapshot fields."),
		evidenceTier("browser_artifacts", "Browser Artifacts", browserArtifactCount, "Browser screenshots, HTML captures, DOM summaries, console entries, network entries, Core Web Vitals, and Web 2.0 source-step labels."),
		evidenceTier("provenance", "Provenance", provenanceEvidenceCount(manifest), "Execution inputs, scenario shape, runtime settings, controller environment, and generator inventory."),
		evidenceTier("external_metrics", "External Metrics", externalMetricSamples, "Target-side or imported resource metrics carried on resource samples."),
		evidenceTier("elk_evidence", "ELK Evidence", manifest.ELKEvidenceCount, "Bounded, deduplicated, redacted Elasticsearch log samples captured during the run."),
		evidenceTier("dynatrace_evidence", "Dynatrace Evidence", manifest.DynatraceEvidenceCount, "Bounded, deduplicated, redacted Dynatrace problem summaries and Grail log samples captured during the run."),
		evidenceTier("monitor_points", "Monitoring Metrics", manifest.MonitorEvidenceCount, "Canonical run-bound metric buckets with provider identity, native units, resolution, and collection quality."),
		evidenceTier("monitor_diagnostics", "Monitoring Diagnostics", manifest.MonitorDiagnosticsCount, "Canonical bounded monitoring diagnostics and historical provider summaries."),
		evidenceTier("oracle_db_metrics", "Oracle DB Metrics", oracleDBMetricSamples, "Canonical oracle.db.* target-side metrics prepared for Oracle Database correlation."),
	}
	for index := range tiers {
		if tiers[index].Count > 0 {
			tiers[index].Status = "present"
			continue
		}
		tiers[index].Status = "missing"
		tiers[index].Reason = evidenceMissingReason(tiers[index].Tier)
	}
	return tiers
}

func evidenceTier(tier string, label string, count int64, description string) RunArchiveEvidenceTier {
	return RunArchiveEvidenceTier{
		Tier:        tier,
		Label:       label,
		Count:       count,
		Description: description,
	}
}

func evidenceMissingReason(tier string) string {
	switch tier {
	case "intervals":
		return "No interval summary records were archived."
	case "sampled_diagnostics":
		return "No error patterns or sampled browser diagnostics were archived."
	case "transaction_archive":
		return "No transaction archive records were written."
	case "browser_artifacts":
		return "No browser capture artifacts were present in archived transactions."
	case "provenance":
		return "Archive manifest does not contain execution provenance."
	case "external_metrics":
		return "No external resource metrics were attached to archived resource samples."
	case "elk_evidence":
		return "No redacted Elasticsearch log samples were archived."
	case "dynatrace_evidence":
		return "No redacted Dynatrace problem summaries or Grail log samples were archived."
	case "oracle_db_metrics":
		return "No oracle.db.* external metrics were attached to archived resource samples."
	default:
		return "No evidence was archived for this tier."
	}
}

func countExternalMetricSamples(resources []ResourceSample) int64 {
	var count int64
	for _, sample := range resources {
		if len(sample.External) > 0 {
			count++
		}
	}
	return count
}

func countBrowserArtifacts(transactions []TransactionEvent) int64 {
	var count int64
	for _, tx := range transactions {
		count += countBrowserArtifactsForTransaction(tx)
	}
	return count
}

func countBrowserArtifactsForTransaction(tx TransactionEvent) int64 {
	var count int64
	if tx.Snapshot != nil {
		if tx.Snapshot.ScreenshotBase64 != "" {
			count++
		}
		if tx.Snapshot.HTML != "" {
			count++
		}
		if tx.Snapshot.DOMSummary != nil {
			count++
		}
		count += int64(len(tx.Snapshot.ConsoleEntries) + len(tx.Snapshot.NetworkEntries))
	}
	count += int64(len(tx.ConsoleEntries) + len(tx.NetworkEntries))
	if tx.DOMSummary != nil {
		count++
	}
	if tx.ClientMetrics != nil {
		count++
	}
	if tx.PageURL != "" || tx.PageTitle != "" {
		count++
	}
	if tx.SourceStepID != "" || tx.SourceStepIndex > 0 || tx.SourceTransaction != "" {
		count++
	}
	return count
}

func provenanceEvidenceCount(manifest *RunArchiveManifest) int64 {
	if manifest == nil {
		return 0
	}
	provenance := manifest.Provenance
	var count int64
	if provenance.ApplicationID != "" || provenance.ReleaseID != "" || provenance.ScenarioID != "" || provenance.ProjectID != "" {
		count++
	}
	if !provenance.RequestedAt.IsZero() || provenance.StartedAt != nil || provenance.Duration != "" || provenance.TotalVUsers > 0 {
		count++
	}
	if len(provenance.Groups) > 0 {
		count++
	}
	if len(provenance.ControllerEnv) > 0 {
		count++
	}
	if len(provenance.GeneratorInventory) > 0 {
		count++
	}
	if provenance.RuntimeSettings != nil || provenance.GoalConfig != nil || provenance.Assignment != nil {
		count++
	}
	return count
}
