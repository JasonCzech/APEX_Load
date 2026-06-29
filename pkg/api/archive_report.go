package api

// report — split from archive.go (same package, behavior-preserving).

import (
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func buildRunArchiveReport(record *storage.RunArchiveRecord) (*RunArchiveReport, error) {
	if err := ensureArchiveMaterialized(record); err != nil {
		return nil, err
	}
	manifest, err := loadRunArchiveManifest(record.ManifestPath)
	if err != nil {
		return nil, err
	}
	transactions, err := readAllArchiveTransactions(record)
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

	report := &RunArchiveReport{
		SchemaVersion:       runArchiveReportSchemaVersion,
		Manifest:            manifest,
		ByAction:            make(map[string]RunArchiveActionStats),
		ByGroup:             make(map[string]RunArchiveActionStats),
		BySOAPOperation:     make(map[string]RunArchiveActionStats),
		ByNetworkProfile:    make(map[string]RunArchiveActionStats),
		ByRegion:            make(map[string]RunArchiveActionStats),
		BySOAPFaultCode:     make(map[string]int64),
		BySOAPFaultCategory: make(map[string]int64),
		BrowserByEngine:     make(map[string]*BrowserEngineLiveMetrics),
		BrowserByPlatform:   make(map[string]*BrowserPlatformLiveMetrics),
		BrowserByDevice:     make(map[string]*BrowserDeviceLiveMetrics),
	}
	faultTimelineBuckets := make(map[time.Time]*RunArchiveSOAPFaultTimelineBucket)
	payloadTimelineBuckets := make(map[time.Time]*RunArchiveSOAPPayloadTimelineBucket)
	actionSummaryBuckets := make(map[string][]archiveActionSummaryBucket)
	errorPatterns := make(map[string]*RunArchiveErrorPattern)
	errorPatternActions := make(map[string]map[string]struct{})

	var totalDuration float64
	overallDurations := make([]float64, 0, len(transactions))
	for index, tx := range transactions {
		report.Overview.Transactions++
		totalDuration += tx.DurationMs
		overallDurations = append(overallDurations, tx.DurationMs)
		if !tx.Success {
			report.Overview.Errors++
			mergeArchiveErrorPattern(errorPatterns, errorPatternActions, tx)
		}
		if index == 0 || tx.DurationMs < report.Overview.MinResponseMs {
			report.Overview.MinResponseMs = tx.DurationMs
		}
		if tx.DurationMs > report.Overview.MaxResponseMs {
			report.Overview.MaxResponseMs = tx.DurationMs
		}

		stats := report.ByAction[tx.ActionName]
		report.ByAction[tx.ActionName] = mergeRunArchiveActionStats(stats, tx)
		if len(summaries) > 0 {
			index := archiveSummaryBucketIndex(summaries, tx.Timestamp)
			if index >= 0 {
				buckets := actionSummaryBuckets[tx.ActionName]
				if len(buckets) == 0 {
					buckets = make([]archiveActionSummaryBucket, len(summaries))
				}
				buckets[index].transactions++
				actionSummaryBuckets[tx.ActionName] = buckets
			}
		}
		if tx.Group != "" {
			report.ByGroup[tx.Group] = mergeRunArchiveActionStats(report.ByGroup[tx.Group], tx)
		}
		mergeArchiveSOAPFaultTimelineBucket(faultTimelineBuckets, tx)
		mergeArchiveSOAPPayloadTimelineBucket(payloadTimelineBuckets, tx)
		if operation := archiveSOAPOperation(tx); operation != "" {
			report.BySOAPOperation[operation] = mergeRunArchiveActionStats(report.BySOAPOperation[operation], tx)
		}
		if tx.NetworkProfile != "" {
			report.ByNetworkProfile[tx.NetworkProfile] = mergeRunArchiveActionStats(report.ByNetworkProfile[tx.NetworkProfile], tx)
		}
		if tx.Region != "" {
			report.ByRegion[tx.Region] = mergeRunArchiveActionStats(report.ByRegion[tx.Region], tx)
		}
		incrementArchiveCount(report.BySOAPFaultCode, archiveSOAPFaultCode(tx))
		incrementArchiveCount(report.BySOAPFaultCategory, archiveSOAPFaultCategory(tx))
		if tx.ClientMetrics != nil {
			if report.Overview.Browser == nil {
				report.Overview.Browser = &BrowserLiveMetrics{}
			}
			mergeReportBrowserMetrics(report.Overview.Browser, tx.ClientMetrics)
			if engine := tx.ClientMetrics.BrowserEngine; engine != "" {
				engineMetrics, ok := report.BrowserByEngine[engine]
				if !ok {
					engineMetrics = &BrowserEngineLiveMetrics{BrowserEngine: engine}
					report.BrowserByEngine[engine] = engineMetrics
				}
				mergeReportBrowserEngineMetrics(engineMetrics, tx.ClientMetrics)
			}
			if platform := tx.ClientMetrics.DevicePlatform; platform != "" {
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
		report.BrowserResources = append(report.BrowserResources, browserResourceTimingsFromTransaction(tx)...)
	}
	if report.Overview.Transactions > 0 {
		report.Overview.AvgResponseMs = totalDuration / float64(report.Overview.Transactions)
		report.Overview.ErrorPct = float64(report.Overview.Errors) / float64(report.Overview.Transactions) * 100
		sort.Float64s(overallDurations)
		report.Overview.P50Ms = archiveAnalysisPercentile(overallDurations, 0.50)
		report.Overview.P90Ms = archiveAnalysisPercentile(overallDurations, 0.90)
		report.Overview.P95Ms = archiveAnalysisPercentile(overallDurations, 0.95)
		report.Overview.P99Ms = archiveAnalysisPercentile(overallDurations, 0.99)
	}
	if len(errorPatterns) > 0 {
		report.ErrorPatterns = make([]RunArchiveErrorPattern, 0, len(errorPatterns))
		for signature, pattern := range errorPatterns {
			if pattern == nil {
				continue
			}
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
	report.Web2Migration = buildRunArchiveWeb2MigrationEvidence(transactions)
	for action, stats := range report.ByAction {
		stats.PeakTPS = archiveActionPeakTPSFromBuckets(summaries, actionSummaryBuckets[action])
		report.ByAction[action] = finalizeRunArchiveActionStats(stats)
	}
	for group, stats := range report.ByGroup {
		report.ByGroup[group] = finalizeRunArchiveActionStats(stats)
	}
	if len(report.ByGroup) == 0 {
		report.ByGroup = nil
	}
	for operation, stats := range report.BySOAPOperation {
		report.BySOAPOperation[operation] = finalizeRunArchiveActionStats(stats)
	}
	if len(report.BySOAPOperation) == 0 {
		report.BySOAPOperation = nil
	}
	for profile, stats := range report.ByNetworkProfile {
		report.ByNetworkProfile[profile] = finalizeRunArchiveActionStats(stats)
	}
	if len(report.ByNetworkProfile) == 0 {
		report.ByNetworkProfile = nil
	}
	for region, stats := range report.ByRegion {
		report.ByRegion[region] = finalizeRunArchiveActionStats(stats)
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
			report.SOAPFaultTimeline = append(report.SOAPFaultTimeline, *bucket)
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
		mergeRunArchiveTelemetrySummary(&telemetrySummary, summary)
		report.SummaryTimeline = append(report.SummaryTimeline, RunArchiveSummaryPoint{
			Timestamp:              summary.Timestamp,
			ActiveVUsers:           summary.ActiveVUsers,
			TPS:                    summary.TPS,
			TargetTPS:              summary.TargetTPS,
			ThroughputGapPct:       summary.ThroughputGapPct,
			ThroughputTargetMissed: summary.ThroughputTargetMissed,
			ErrorPct:               summary.ErrorPct,
			P95Ms:                  summary.P95Ms,
			TotalTx:                summary.TotalTx,
			TotalErrors:            summary.TotalErrors,
		})
	}
	if telemetrySummary.Samples > 0 {
		report.Overview.Telemetry = finalizeRunArchiveTelemetrySummary(telemetrySummary)
	}
	report.Overview.LoadModelGoals, report.Overview.GoalCompletion = buildRunArchiveGoalCompletion(manifest.Provenance, report.Overview)
	report.EvidenceTiers = buildRunArchiveEvidenceTiers(manifest, report, transactions, resources, summaries)
	report.Compatibility = buildRunArchiveReportCompatibility(record, manifest)
	report.Completeness = buildRunArchiveCompletenessScore(manifest, report, transactions, resources, summaries)
	report.ChangeAttribution = buildRunArchiveChangeAttribution(manifest)
	report.ReportProfiles = buildRunArchiveReportProfiles()
	report.NetworkPhases = buildRunArchiveNetworkPhaseSummary(transactions)
	report.ArtifactInventory = buildRunArchiveArtifactInventory(record, manifest)

	return report, nil
}

func buildRunArchiveWeb2MigrationEvidence(transactions []TransactionEvent) *RunArchiveWeb2MigrationEvidence {
	stepSet := make(map[string]struct{})
	transactionSet := make(map[string]struct{})
	profileSet := make(map[string]struct{})
	evidence := map[string]int{
		"screenshots":        0,
		"dom_summaries":      0,
		"console_entries":    0,
		"network_entries":    0,
		"client_metrics":     0,
		"source_step_labels": 0,
	}
	var replayed int
	var failed int
	for _, tx := range transactions {
		stepKey := web2SourceStepKey(tx)
		if stepKey == "" {
			continue
		}
		stepSet[stepKey] = struct{}{}
		replayed++
		if !tx.Success {
			failed++
		}
		evidence["source_step_labels"]++
		if tx.SourceTransaction != "" {
			transactionSet[tx.SourceTransaction] = struct{}{}
		}
		if tx.SourceProfile != "" {
			profileSet[tx.SourceProfile] = struct{}{}
		}
		if tx.Snapshot != nil {
			if tx.Snapshot.ScreenshotBase64 != "" {
				evidence["screenshots"]++
			}
			if tx.Snapshot.DOMSummary != nil || tx.Snapshot.HTML != "" {
				evidence["dom_summaries"]++
			}
			evidence["console_entries"] += len(tx.Snapshot.ConsoleEntries)
			evidence["network_entries"] += len(tx.Snapshot.NetworkEntries)
		}
		if tx.DOMSummary != nil {
			evidence["dom_summaries"]++
		}
		evidence["console_entries"] += len(tx.ConsoleEntries)
		evidence["network_entries"] += len(tx.NetworkEntries)
		if tx.ClientMetrics != nil {
			evidence["client_metrics"]++
		}
	}
	if replayed == 0 {
		return nil
	}
	status := "ready_for_review"
	nextActions := []string{"Review browser replay evidence, runtime parity, and selector stability before approval."}
	if failed > 0 {
		status = "needs_repair"
		nextActions = append(nextActions, "Repair failed Web 2.0 replay steps and rerun browser replay.")
	}
	conversionReady := evidence["network_entries"] > 0 && failed == 0
	if !conversionReady {
		nextActions = append(nextActions, "Capture replay network entries before generating a protocol-load HTTP draft.")
	}
	return &RunArchiveWeb2MigrationEvidence{
		Status:                status,
		ImportedSteps:         len(stepSet),
		ReplayedSteps:         replayed,
		FailedSteps:           failed,
		TransactionGroups:     sortedArchiveStringSet(transactionSet),
		SourceProfiles:        sortedArchiveStringSet(profileSet),
		ReplayEvidence:        evidence,
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
			Label:         "Executive Summary",
			Description:   "Decision-focused summary with analyst commentary, supporting graphs, SLA verdict, error rate, throughput, completeness, and change attribution.",
			Audience:      "Business stakeholders, release owners, and sign-off authorities.",
			BestFor:       "Ship/no-ship decisions, business risk, SLA posture, and executive sign-off.",
			ExportFormats: []string{"html", "json", "csv", "pdf"},
			// "release_trend" renders real release-over-release data: buildRunArchiveReport
			// stays a pure single-record function, so the multi-run trend is attached at the
			// storage-aware handler layer ((*Server).attachReleaseTrend) and the renderers
			// gate the section on report.ReleaseTrend, degrading to a no-baseline/no-release
			// note rather than a ghost.
			IncludedSections: []string{"verdict", "executive_overview", "sla_rollup", "release_trend", "risk_summary", "recommendations", "signoff", "completeness", "change_attribution"},
			SectionOrder:     []string{"verdict", "executive_overview", "sla_rollup", "release_trend", "risk_summary", "recommendations", "signoff", "completeness", "change_attribution"},
		},
		{
			ID:               "engineer",
			Label:            "Detailed Technical",
			Description:      "Engineering drilldown with analyst commentary, supporting graphs, timelines, action stats, browser diagnostics, resource peaks, and error patterns.",
			Audience:         "Performance engineers, SREs, developers, and test analysts.",
			BestFor:          "Bottleneck isolation, regression diagnosis, resource saturation, and remediation planning.",
			ExportFormats:    []string{"html", "json", "csv", "pdf", "jsonl", "parquet"},
			IncludedSections: []string{"verdict", "technical_overview", "graphs", "sla_detail", "summary_timeline", "actions", "network_phases", "errors", "resources", "browser", "soap", "web2_migration", "root_cause", "completeness", "schema_compatibility", "change_attribution", "raw_exports"},
			SectionOrder:     []string{"verdict", "technical_overview", "graphs", "sla_detail", "summary_timeline", "actions", "network_phases", "errors", "resources", "browser", "soap", "web2_migration", "root_cause", "completeness", "schema_compatibility", "change_attribution", "raw_exports"},
		},
		{
			ID:               "audit",
			Label:            "Audit",
			Description:      "Governance package with schema compatibility, evidence tiers, completeness findings, provenance, and artifact inventory.",
			Audience:         "Compliance, governance, change-control, and QA gatekeepers.",
			BestFor:          "Evidence integrity, authorization traceability, reproducibility, and audit review.",
			ExportFormats:    []string{"html", "json", "csv", "pdf"},
			IncludedSections: []string{"audit_identity", "authorization", "governance", "schema_compatibility", "evidence_tiers", "completeness", "provenance", "change_attribution", "artifact_integrity", "report_authorship", "official_outcome"},
			SectionOrder:     []string{"audit_identity", "authorization", "governance", "schema_compatibility", "evidence_tiers", "completeness", "provenance", "change_attribution", "artifact_integrity", "report_authorship", "official_outcome"},
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
		Environment:           archiveFirstNonEmpty(provenance.ControllerEnv["APEXLOAD_ENVIRONMENT"], provenance.ControllerEnv["APEXLOAD_ENV"], provenance.ProjectID),
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
		if strings.TrimSpace(manifest.Error) != "" {
			addFinding("archive_writer", "critical", 25, "Archive writer recorded an error: "+manifest.Error)
		}
		if manifest.TransactionCount > int64(len(transactions)) {
			addFinding("artifact_truncation", "critical", 20, fmt.Sprintf("Transaction archive contains %d of %d expected events.", len(transactions), manifest.TransactionCount))
		}
		if manifest.ResourceSampleCount > int64(len(resources)) {
			addFinding("artifact_truncation", "warning", 10, fmt.Sprintf("Resource archive contains %d of %d expected samples.", len(resources), manifest.ResourceSampleCount))
		}
		if manifest.IntervalSummaryCount > int64(len(summaries)) {
			addFinding("missing_intervals", "critical", 20, fmt.Sprintf("Interval archive contains %d of %d expected summaries.", len(summaries), manifest.IntervalSummaryCount))
		}
	}
	if len(summaries) == 0 {
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
		SupportedManifestSchemas: []string{archiveSchemaVersionV1, archiveSchemaVersion},
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
	case archiveSchemaVersion, archiveSchemaVersionV1:
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
	if report == nil {
		return nil
	}
	externalMetricSamples := countExternalMetricSamples(resources)
	oracleDBMetricSamples := countOracleDBMetricSamples(resources)
	tiers := []RunArchiveEvidenceTier{
		evidenceTier("summary", "Summary", report.Overview.Transactions+report.Overview.IntervalSummaries+report.Overview.ResourceSamples, "Aggregated run totals, response-time range, resource peaks, and headline telemetry."),
		evidenceTier("intervals", "Intervals", int64(len(summaries)), "Per-interval live metric summaries used for timelines, throughput evidence, and telemetry SLA review."),
		evidenceTier("sampled_diagnostics", "Sampled Diagnostics", int64(len(report.ErrorPatterns)+len(report.BrowserResources)), "Grouped error signatures and sampled browser resource diagnostics for triage."),
		evidenceTier("transaction_archive", "Transaction Archive", int64(len(transactions)), "Raw transaction events with action, timing, dependency, status, correlation, and snapshot fields."),
		evidenceTier("browser_artifacts", "Browser Artifacts", countBrowserArtifacts(transactions), "Browser screenshots, HTML captures, DOM summaries, console entries, network entries, Core Web Vitals, and Web 2.0 source-step labels."),
		evidenceTier("provenance", "Provenance", provenanceEvidenceCount(manifest), "Execution inputs, scenario shape, runtime settings, controller environment, and generator inventory."),
		evidenceTier("external_metrics", "External Metrics", externalMetricSamples, "Target-side or imported resource metrics carried on resource samples."),
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
