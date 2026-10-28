package api

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	telemetryEvidenceFileName          = "telemetry_evidence.json"
	telemetryEvidenceSchema            = "apexload.telemetry_evidence.v1"
	telemetryEvidenceRecordType        = "telemetry_evidence_v1"
	maxTelemetryResourceSamples        = 256
	telemetryEndToEndSampleAgeTargetMs = 2000.0
)

const (
	TelemetryConditionHealthy             = "healthy"
	TelemetryConditionDegradedStageBudget = "degraded_stage_budget"
	TelemetryConditionClockUnqualified    = "clock_unqualified"
	TelemetryConditionBoundaryUnscored    = "boundary_unscored"
	TelemetryConditionSourceGap           = "source_gap"
	TelemetryConditionOverloadShed        = "overload_shed"
)

// TelemetryResourceSample records a bounded controller or generator telemetry
// resource observation during a run (TEL-B1).
type TelemetryResourceSample struct {
	Timestamp  time.Time `json:"timestamp"`
	Component  string    `json:"component"`
	Source     string    `json:"source,omitempty"`
	QueueDepth int       `json:"queue_depth"`
	ByteVolume int64     `json:"byte_volume"`
	CPUPercent float64   `json:"cpu_percent"`
	RSSBytes   int64     `json:"rss_bytes"`
}

// TelemetryValidityFinding records an explicit telemetry validity evaluation,
// distinguishing stage-budget health compliance from the independent <= 2,000 ms
// end-to-end sample-age criterion (TEL-B1).
type TelemetryValidityFinding struct {
	Criterion   string   `json:"criterion"`
	Status      string   `json:"status"`
	Compliant   bool     `json:"compliant"`
	ThresholdMs float64  `json:"threshold_ms,omitempty"`
	ObservedMs  *float64 `json:"observed_ms,omitempty"`
	Summary     string   `json:"summary"`
	Details     []string `json:"details,omitempty"`
}

// TelemetryEvidenceInterval summarizes one telemetry interval with its policy
// segment classification and canonical interval condition labels (TEL-B1).
type TelemetryEvidenceInterval struct {
	StartAt         time.Time `json:"start_at"`
	EndAt           time.Time `json:"end_at"`
	DurationMs      int64     `json:"duration_ms"`
	PolicySegment   int64     `json:"policy_segment,omitempty"`
	PolicyHash      string    `json:"policy_hash,omitempty"`
	Status          string    `json:"status"`
	ConditionLabels []string  `json:"condition_labels"`
	Unscored        []string  `json:"unscored,omitempty"`
}

// TelemetryEvidenceDocument is the versioned archive telemetry sidecar
// (telemetry_evidence.json) written at archive finalization and bound into the
// archive manifest digest (TEL-B1).
type TelemetryEvidenceDocument struct {
	SchemaVersion            string                          `json:"schema_version"`
	TestID                   string                          `json:"test_id"`
	CapturedAt               time.Time                       `json:"captured_at"`
	CoverageAlgorithm        string                          `json:"coverage_algorithm"`
	CoverageRule             string                          `json:"coverage_rule,omitempty"`
	Coverage                 *TelemetryCoverageRecord        `json:"coverage,omitempty"`
	PolicySegments           []TelemetryPolicySegmentReceipt `json:"policy_segments,omitempty"`
	SegmentCoverage          []TelemetryPolicySegment        `json:"segment_coverage,omitempty"`
	Intervals                []TelemetryEvidenceInterval     `json:"intervals,omitempty"`
	ConditionLabelCounts     map[string]int                  `json:"condition_label_counts,omitempty"`
	ResourceSamples          []TelemetryResourceSample       `json:"resource_samples,omitempty"`
	ResourceSamplesTruncated bool                            `json:"resource_samples_truncated,omitempty"`
	RenderPopulation         *TelemetryRenderPopulation      `json:"render_population,omitempty"`
	StageDistributions       []TelemetryStageDistribution    `json:"stage_distributions,omitempty"`
	ValidityFindings         []TelemetryValidityFinding      `json:"validity_findings"`
	Acceptance               *TelemetryAcceptanceVerdict     `json:"telemetry_acceptance,omitempty"`
}

// deriveTelemetryIntervalConditionLabels computes canonical interval condition
// labels (healthy, degraded_stage_budget, clock_unqualified, boundary_unscored,
// source_gap, overload_shed) for a live metrics point and its run health status.
func deriveTelemetryIntervalConditionLabels(live *LiveMetrics, status *TelemetryRunHealth) []string {
	var (
		degradedStageBudget bool
		clockUnqualified    bool
		boundaryUnscored    bool
		sourceGap           bool
		overloadShed        bool
	)
	if status != nil {
		for _, cond := range status.Conditions {
			switch cond {
			case "stale_sample", "generator_lag", "transport_lag", "controller_fanout_lag", "browser_render_lag", TelemetryConditionDegradedStageBudget:
				degradedStageBudget = true
			case "collector_drops", "generator_stream_drops", "generator_stream_gaps", "websocket_drops", TelemetryConditionSourceGap:
				sourceGap = true
			case TelemetryConditionOverloadShed:
				overloadShed = true
			case TelemetryConditionClockUnqualified:
				clockUnqualified = true
			case TelemetryConditionBoundaryUnscored:
				boundaryUnscored = true
			}
		}
		for _, unscored := range status.Unscored {
			switch {
			case unscored == telemetryPolicyBoundaryUnscored || unscored == telemetryPolicySegmentsUnavailableUnscored || unscored == TelemetryConditionBoundaryUnscored || strings.HasPrefix(unscored, "policy_segment"):
				boundaryUnscored = true
			case strings.HasPrefix(unscored, "stale_sample:") || strings.HasPrefix(unscored, "transport_lag:") || unscored == TelemetryConditionClockUnqualified:
				clockUnqualified = true
			}
		}
		if (status.Status == "degraded" || status.Status == "breached") && !sourceGap && !overloadShed {
			degradedStageBudget = true
		}
	}
	if live != nil && live.Telemetry != nil {
		tel := live.Telemetry
		if tel.DroppedResults > 0 || tel.DroppedSubscriberBatches > 0 || tel.MissingMetricWindows > 0 || tel.WebsocketDroppedMessages > 0 || tel.WebsocketDroppedEvents > 0 {
			sourceGap = true
		}
		if tel.SheddedArrivals > 0 {
			overloadShed = true
		}
		if (tel.SampleAgeQuality != nil && !tel.SampleAgeQuality.Qualified) || (tel.TransportLagQuality != nil && !tel.TransportLagQuality.Qualified) {
			clockUnqualified = true
		}
	}
	labels := make([]string, 0, 4)
	if degradedStageBudget {
		labels = append(labels, TelemetryConditionDegradedStageBudget)
	}
	if clockUnqualified {
		labels = append(labels, TelemetryConditionClockUnqualified)
	}
	if boundaryUnscored {
		labels = append(labels, TelemetryConditionBoundaryUnscored)
	}
	if sourceGap {
		labels = append(labels, TelemetryConditionSourceGap)
	}
	if overloadShed {
		labels = append(labels, TelemetryConditionOverloadShed)
	}
	if len(labels) == 0 {
		labels = append(labels, TelemetryConditionHealthy)
	}
	return labels
}

// attachTelemetrySegmentView populates the live telemetry view with active
// in-run policy segments, current stage budgets, and interval condition labels.
func attachTelemetrySegmentView(metrics *LiveMetrics, test *ManagedTest) {
	if metrics == nil || test == nil {
		return
	}
	if metrics.TelemetryStatus != nil && len(metrics.TelemetryStatus.ConditionLabels) == 0 {
		metrics.TelemetryStatus.ConditionLabels = deriveTelemetryIntervalConditionLabels(metrics, metrics.TelemetryStatus)
	}
	if metrics.Telemetry == nil || metrics.Telemetry.restoredFromHistory {
		return
	}
	if len(metrics.Telemetry.IntervalConditionLabels) == 0 {
		if metrics.TelemetryStatus != nil && len(metrics.TelemetryStatus.ConditionLabels) > 0 {
			metrics.Telemetry.IntervalConditionLabels = append([]string(nil), metrics.TelemetryStatus.ConditionLabels...)
		} else {
			metrics.Telemetry.IntervalConditionLabels = deriveTelemetryIntervalConditionLabels(metrics, metrics.TelemetryStatus)
		}
	}
	if segments := telemetryPolicySegmentsListedLocked(test); len(segments) > 0 {
		metrics.Telemetry.PolicySegments = segments
	} else if len(metrics.Telemetry.PolicySegments) == 0 && len(test.TelemetryPolicySegments) > 0 {
		metrics.Telemetry.PolicySegments = append([]TelemetryPolicySegmentReceipt(nil), test.TelemetryPolicySegments...)
	}
	if metrics.TelemetryPolicy != nil && metrics.TelemetryPolicy.Policy != nil && len(metrics.TelemetryPolicy.Policy.Budgets) > 0 {
		metrics.Telemetry.StageBudgets = append([]TelemetryStageBudget(nil), metrics.TelemetryPolicy.Policy.Budgets...)
		if metrics.TelemetryStatus != nil && len(metrics.TelemetryStatus.StageBudgets) == 0 {
			metrics.TelemetryStatus.StageBudgets = append([]TelemetryStageBudget(nil), metrics.TelemetryPolicy.Policy.Budgets...)
		}
	}
	if len(metrics.Telemetry.ValidityFindings) == 0 && (metrics.Telemetry.Coverage != nil || len(metrics.Telemetry.StageDistributions) > 0 || metrics.Telemetry.RenderPopulation != nil) {
		metrics.Telemetry.ValidityFindings = buildLiveTelemetryValidityFindings(metrics.Telemetry, metrics.TelemetryStatus)
	}
}

func buildLiveTelemetryValidityFindings(tel *TelemetryHealth, status *TelemetryRunHealth) []TelemetryValidityFinding {
	if tel == nil {
		return nil
	}
	manifest := &RunArchiveManifest{
		TelemetryStageDistributions: tel.StageDistributions,
		TelemetryRenderPopulation:   tel.RenderPopulation,
	}
	if tel.Coverage != nil {
		rec := &TelemetryCoverageRecord{
			Algorithm:    telemetryCoverageAlgorithm,
			CoverageRule: telemetryCoverageRuleQualifiedSourceWindows,
			Coverage:     *tel.Coverage,
			UnknownMs:    tel.Coverage.UnknownMs,
		}
		switch {
		case status != nil && status.Status == "breached":
			rec.BreachedMs = tel.Coverage.ObservedMs
		case status != nil && status.Status == "degraded":
			rec.DegradedMs = tel.Coverage.ObservedMs
		case status != nil && status.Status == "unscored":
			rec.UnknownMs = tel.Coverage.WindowMs
		default:
			rec.HealthyMs = tel.Coverage.ObservedMs
		}
		manifest.TelemetryCoverage = rec
	}
	return buildTelemetryValidityFindings(manifest)
}

// recordTelemetryResourceSamplesLocked captures bounded controller and generator
// resource samples during live metrics publication. The caller holds tr.mu.
func recordTelemetryResourceSamplesLocked(test *ManagedTest, live *LiveMetrics, now time.Time) {
	if test == nil || live == nil {
		return
	}
	ts := live.Timestamp.UTC()
	if ts.IsZero() {
		ts = now.UTC()
	}
	queueDepth := len(test.History)
	if queueDepth == 0 && live.Telemetry != nil && live.Telemetry.HistoryPoints > 0 {
		queueDepth = live.Telemetry.HistoryPoints
	}
	byteVolume := test.historyBytes
	if byteVolume <= 0 {
		byteVolume = managedTestHistoryPointWeight(live, maxManagedTestHistoryBytes)
	}
	var controllerCPU float64
	var controllerRSS int64
	if live.Resources != nil && len(live.GeneratorResources) == 0 {
		controllerCPU = live.Resources.CPUProcessPct
		controllerRSS = int64(live.Resources.MemProcessMb * 1024 * 1024)
	}
	if controllerRSS <= 0 {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		controllerRSS = int64(mem.HeapSys)
	}
	appendTelemetryResourceSampleLocked(test, TelemetryResourceSample{
		Timestamp:  ts,
		Component:  "controller",
		Source:     "controller",
		QueueDepth: queueDepth,
		ByteVolume: byteVolume,
		CPUPercent: controllerCPU,
		RSSBytes:   controllerRSS,
	})

	if len(live.GeneratorResources) > 0 {
		keys := make([]string, 0, len(live.GeneratorResources))
		for k := range live.GeneratorResources {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			gen := live.GeneratorResources[key]
			if gen == nil {
				continue
			}
			genTS := gen.Timestamp.UTC()
			if genTS.IsZero() {
				genTS = ts
			}
			source := strings.TrimSpace(gen.GeneratorID)
			if source == "" {
				source = strings.TrimSpace(gen.Target)
			}
			if source == "" {
				source = key
			}
			var genBytes int64
			var genCPU float64
			var genRSS int64
			if gen.Resources != nil {
				genBytes = int64((gen.Resources.NetSentKbps + gen.Resources.NetRecvKbps) * 128)
				genCPU = gen.Resources.CPUProcessPct
				genRSS = int64(gen.Resources.MemProcessMb * 1024 * 1024)
			}
			appendTelemetryResourceSampleLocked(test, TelemetryResourceSample{
				Timestamp:  genTS,
				Component:  "generator",
				Source:     source,
				QueueDepth: int(gen.ActiveVUsers),
				ByteVolume: genBytes,
				CPUPercent: genCPU,
				RSSBytes:   genRSS,
			})
		}
	} else if live.Resources != nil {
		appendTelemetryResourceSampleLocked(test, TelemetryResourceSample{
			Timestamp:  ts,
			Component:  "generator",
			Source:     "local",
			QueueDepth: int(live.ActiveVUsers),
			ByteVolume: int64((live.Resources.NetSentKbps + live.Resources.NetRecvKbps) * 128),
			CPUPercent: live.Resources.CPUProcessPct,
			RSSBytes:   int64(live.Resources.MemProcessMb * 1024 * 1024),
		})
	}
}

func appendTelemetryResourceSampleLocked(test *ManagedTest, sample TelemetryResourceSample) {
	if len(test.telemetryResourceSamples) >= maxTelemetryResourceSamples {
		copy(test.telemetryResourceSamples, test.telemetryResourceSamples[1:])
		test.telemetryResourceSamples[len(test.telemetryResourceSamples)-1] = sample
		test.telemetryResourceSamplesTruncated = true
		return
	}
	test.telemetryResourceSamples = append(test.telemetryResourceSamples, sample)
}

// buildTelemetryValidityFindings evaluates explicit telemetry validity findings,
// distinguishing stage-budget health compliance from the independent <= 2,000 ms
// end-to-end sample-age criterion (TEL-B1).
func buildTelemetryValidityFindings(manifest *RunArchiveManifest) []TelemetryValidityFinding {
	findings := make([]TelemetryValidityFinding, 0, 2)
	if manifest == nil {
		return findings
	}

	// 1. Stage-budget health compliance finding.
	stageFinding := TelemetryValidityFinding{
		Criterion: "stage_budget_health",
		Status:    "unscored",
		Compliant: false,
		Summary:   "stage-budget health coverage is unavailable",
	}
	if manifest.TelemetryCoverage != nil {
		rec := manifest.TelemetryCoverage
		windowMs := rec.Coverage.WindowMs
		switch {
		case rec.BreachedMs > 0:
			stageFinding.Status = "failed"
			stageFinding.Compliant = false
			stageFinding.Summary = fmt.Sprintf("stage-budget health breached for %d ms of %d ms run duration", rec.BreachedMs, windowMs)
		case rec.DegradedMs > 0:
			stageFinding.Status = "degraded"
			stageFinding.Compliant = false
			stageFinding.Summary = fmt.Sprintf("stage-budget health degraded for %d ms of %d ms run duration", rec.DegradedMs, windowMs)
		case rec.UnknownMs > 0 || rec.HealthyMs <= 0:
			stageFinding.Status = "unscored"
			stageFinding.Compliant = false
			stageFinding.Summary = fmt.Sprintf("stage-budget health has %d ms unscored or unknown of %d ms run duration", rec.UnknownMs, windowMs)
		default:
			stageFinding.Status = "passed"
			stageFinding.Compliant = true
			stageFinding.Summary = fmt.Sprintf("stage-budget health compliant across %d ms", rec.HealthyMs)
		}
		if len(rec.Segments) > 0 {
			for _, seg := range rec.Segments {
				stageFinding.Details = append(stageFinding.Details, fmt.Sprintf(
					"segment %d: healthy=%dms degraded=%dms breached=%dms",
					seg.Sequence, seg.HealthyMs, seg.DegradedMs, seg.BreachedMs,
				))
			}
		}
	}
	findings = append(findings, stageFinding)

	// 2. Independent <= 2,000 ms end-to-end sample-age criterion.
	e2eFinding := TelemetryValidityFinding{
		Criterion:   "end_to_end_sample_age",
		Status:      "unscored",
		Compliant:   false,
		ThresholdMs: telemetryEndToEndSampleAgeTargetMs,
		Summary:     "independent <= 2,000 ms end-to-end sample-age criterion is unscored: no qualified end-to-end or sample-age observations",
	}
	var endToEndStage *TelemetryStageDistribution
	var sampleAgeStage *TelemetryStageDistribution
	for i := range manifest.TelemetryStageDistributions {
		stage := &manifest.TelemetryStageDistributions[i]
		switch stage.Stage {
		case telemetryStageEndToEnd:
			endToEndStage = stage
		case telemetryStageSampleAge:
			sampleAgeStage = stage
		}
	}
	pop := manifest.TelemetryRenderPopulation
	eligible := int64(0)
	if endToEndStage != nil && endToEndStage.EligiblePublications > eligible {
		eligible = endToEndStage.EligiblePublications
	}
	if pop != nil {
		popDenom := max(pop.PublishedEligible, pop.Eligible)
		for _, prior := range pop.PriorEpochs {
			popDenom += max(prior.PublishedEligible, prior.Eligible)
		}
		if popDenom > eligible {
			eligible = popDenom
		}
	}
	if endToEndStage != nil && endToEndStage.Samples > 0 {
		excluded := totalExcludedCount(endToEndStage.Excluded)
		if eligible > endToEndStage.Samples && eligible-endToEndStage.Samples > excluded {
			excluded = eligible - endToEndStage.Samples
		}
		denom := endToEndStage.Samples + excluded
		if eligible > denom {
			denom = eligible
		}
		allowedExclusions := int64(math.Floor(float64(denom) * 0.01))
		e2eFinding.ObservedMs = cloneFloat64Ptr(endToEndStage.P99Ms)
		if endToEndStage.DeliverySignal != "" {
			e2eFinding.Details = append(e2eFinding.Details, "delivery_signal="+endToEndStage.DeliverySignal)
		}
		switch {
		case excluded > allowedExclusions:
			e2eFinding.Status = "failed"
			e2eFinding.Compliant = false
			e2eFinding.Summary = fmt.Sprintf(
				"independent <= 2,000 ms end-to-end sample-age criterion failed: %d of %d eligible publications excluded (exceeds 1%% P99 exclusion bound)",
				excluded, denom,
			)
		case endToEndStage.P99Ms == nil:
			e2eFinding.Status = "unscored"
			e2eFinding.Compliant = false
			e2eFinding.Summary = "independent <= 2,000 ms end-to-end sample-age criterion unscored: P99 quantile is unbounded"
		case *endToEndStage.P99Ms <= telemetryEndToEndSampleAgeTargetMs:
			e2eFinding.Status = "passed"
			e2eFinding.Compliant = true
			e2eFinding.Summary = fmt.Sprintf(
				"independent <= 2,000 ms end-to-end sample-age criterion passed (end_to_end P99 %.1f ms <= 2,000 ms)",
				*endToEndStage.P99Ms,
			)
		default:
			e2eFinding.Status = "failed"
			e2eFinding.Compliant = false
			e2eFinding.Summary = fmt.Sprintf(
				"independent <= 2,000 ms end-to-end sample-age criterion failed (end_to_end P99 %.1f ms > 2,000 ms)",
				*endToEndStage.P99Ms,
			)
		}
	} else if pop != nil && pop.ReferenceClient != "" && eligible > 0 {
		e2eFinding.Status = "failed"
		e2eFinding.Compliant = false
		e2eFinding.Summary = fmt.Sprintf(
			"independent <= 2,000 ms end-to-end sample-age criterion failed: 0 of %d eligible reference publications produced qualified end-to-end observations",
			eligible,
		)
	} else if sampleAgeStage != nil && sampleAgeStage.Samples > 0 {
		excluded := totalExcludedCount(sampleAgeStage.Excluded)
		denom := sampleAgeStage.Samples + excluded
		allowedExclusions := int64(math.Floor(float64(denom) * 0.01))
		e2eFinding.ObservedMs = cloneFloat64Ptr(sampleAgeStage.P99Ms)
		switch {
		case excluded > allowedExclusions || sampleAgeStage.P99Ms == nil:
			e2eFinding.Status = "unscored"
			e2eFinding.Compliant = false
			e2eFinding.Summary = "independent <= 2,000 ms end-to-end sample-age criterion unscored: sample_age exclusions exceed 1% P99 bound"
		case *sampleAgeStage.P99Ms > telemetryEndToEndSampleAgeTargetMs:
			e2eFinding.Status = "failed"
			e2eFinding.Compliant = false
			e2eFinding.Summary = fmt.Sprintf(
				"independent <= 2,000 ms end-to-end sample-age criterion failed on sample_age stage (P99 %.1f ms > 2,000 ms)",
				*sampleAgeStage.P99Ms,
			)
		default:
			e2eFinding.Status = "passed"
			e2eFinding.Compliant = true
			e2eFinding.Summary = fmt.Sprintf(
				"independent <= 2,000 ms end-to-end sample-age criterion passed on sample_age stage (P99 %.1f ms <= 2,000 ms)",
				*sampleAgeStage.P99Ms,
			)
		}
	}
	findings = append(findings, e2eFinding)
	return findings
}

func totalExcludedCount(excluded map[string]int64) int64 {
	var total int64
	for _, count := range excluded {
		if count > 0 {
			total += count
		}
	}
	return total
}

func (tr *TestRunner) persistTelemetryEvidenceArchive(ctx context.Context, test *ManagedTest, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, budget *archiveOutputBudget) error {
	if test == nil || record == nil || manifest == nil || strings.TrimSpace(record.ArchivePath) == "" || manifest.TelemetryEvidenceFile != "" {
		return nil
	}
	tr.mu.RLock()
	history := make([]*LiveMetrics, len(test.History))
	for i, point := range test.History {
		history[i] = cloneLiveMetrics(point)
	}
	resourceSamples := append([]TelemetryResourceSample(nil), test.telemetryResourceSamples...)
	resourceSamplesTruncated := test.telemetryResourceSamplesTruncated
	segments := telemetryPolicySegmentsListedLocked(test)
	startedAt := cloneTimePtr(test.StartedAt)
	tr.mu.RUnlock()

	if len(manifest.TelemetryPolicySegments) > 0 {
		segments = append([]TelemetryPolicySegmentReceipt(nil), manifest.TelemetryPolicySegments...)
	}
	segmentHashes := make(map[int64]string, len(segments))
	for _, seg := range segments {
		if seg.Policy != nil && seg.Policy.Hash != "" {
			segmentHashes[seg.Sequence] = seg.Policy.Hash
		}
	}

	if len(resourceSamples) == 0 && len(history) > 0 {
		tempTest := &ManagedTest{ID: test.ID}
		for _, point := range history {
			if point == nil {
				continue
			}
			recordTelemetryResourceSamplesLocked(tempTest, point, point.Timestamp)
		}
		resourceSamples = tempTest.telemetryResourceSamples
		resourceSamplesTruncated = tempTest.telemetryResourceSamplesTruncated
	}
	if len(resourceSamples) == 0 {
		var mem runtime.MemStats
		runtime.ReadMemStats(&mem)
		resourceSamples = []TelemetryResourceSample{{
			Timestamp:  time.Now().UTC(),
			Component:  "controller",
			Source:     "controller",
			QueueDepth: len(history),
			ByteVolume: 0,
			CPUPercent: 0,
			RSSBytes:   int64(mem.HeapSys),
		}}
	}

	intervals := make([]TelemetryEvidenceInterval, 0, len(history))
	labelCounts := map[string]int{}
	var prevEnd time.Time
	if startedAt != nil {
		prevEnd = startedAt.UTC()
	}
	for _, point := range history {
		if point == nil || point.Timestamp.IsZero() {
			continue
		}
		endAt := point.Timestamp.UTC()
		startAt := prevEnd
		if point.SampleWindowSeconds > 0 {
			candidate := endAt.Add(-time.Duration(point.SampleWindowSeconds * float64(time.Second)))
			if startAt.IsZero() || candidate.After(startAt) {
				startAt = candidate
			}
		}
		if startAt.IsZero() || startAt.After(endAt) {
			startAt = endAt
		}
		prevEnd = endAt
		labels := deriveTelemetryIntervalConditionLabels(point, point.TelemetryStatus)
		for _, label := range labels {
			labelCounts[label]++
		}
		status := "unknown"
		var policySeg int64
		var unscored []string
		if point.TelemetryStatus != nil {
			if point.TelemetryStatus.Status != "" {
				status = point.TelemetryStatus.Status
			}
			policySeg = point.TelemetryStatus.PolicySegment
			unscored = append([]string(nil), point.TelemetryStatus.Unscored...)
		}
		intervals = append(intervals, TelemetryEvidenceInterval{
			StartAt:         startAt,
			EndAt:           endAt,
			DurationMs:      max(0, endAt.Sub(startAt).Milliseconds()),
			PolicySegment:   policySeg,
			PolicyHash:      segmentHashes[policySeg],
			Status:          status,
			ConditionLabels: labels,
			Unscored:        unscored,
		})
	}
	if len(labelCounts) == 0 {
		labelCounts = nil
	}

	findings := buildTelemetryValidityFindings(manifest)
	manifest.TelemetryValidityFindings = findings
	if manifest.TelemetryAcceptance == nil {
		manifest.TelemetryAcceptance = telemetryAcceptanceForManifest(manifest)
	}

	var segmentCoverage []TelemetryPolicySegment
	coverageRule := "qualified_source_windows"
	if manifest.TelemetryCoverage != nil {
		if manifest.TelemetryCoverage.CoverageRule != "" {
			coverageRule = manifest.TelemetryCoverage.CoverageRule
		}
		if len(manifest.TelemetryCoverage.Segments) > 0 {
			segmentCoverage = append([]TelemetryPolicySegment(nil), manifest.TelemetryCoverage.Segments...)
		}
	}

	doc := TelemetryEvidenceDocument{
		SchemaVersion:            telemetryEvidenceSchema,
		TestID:                   test.ID,
		CapturedAt:               time.Now().UTC(),
		CoverageAlgorithm:        telemetryCoverageAlgorithm,
		CoverageRule:             coverageRule,
		Coverage:                 manifest.TelemetryCoverage,
		PolicySegments:           segments,
		SegmentCoverage:          segmentCoverage,
		Intervals:                intervals,
		ConditionLabelCounts:     labelCounts,
		ResourceSamples:          resourceSamples,
		ResourceSamplesTruncated: resourceSamplesTruncated,
		RenderPopulation:         manifest.TelemetryRenderPopulation.clone(),
		StageDistributions:       cloneTelemetryStageDistributions(manifest.TelemetryStageDistributions),
		ValidityFindings:         findings,
		Acceptance:               cloneTelemetryAcceptanceVerdict(manifest.TelemetryAcceptance),
	}

	if err := writeArchiveJSONArtifactDurable(record.ArchivePath, telemetryEvidenceFileName, doc, budget); err != nil {
		return err
	}
	manifest.TelemetryEvidenceFile = telemetryEvidenceFileName
	return updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{
		Name:       telemetryEvidenceFileName,
		Kind:       "evidence",
		Format:     "json",
		RecordType: telemetryEvidenceRecordType,
		Count:      int64(len(intervals) + len(resourceSamples) + len(findings)),
	})
}

func cloneTelemetryValidityFindings(in []TelemetryValidityFinding) []TelemetryValidityFinding {
	if len(in) == 0 {
		return nil
	}
	out := make([]TelemetryValidityFinding, len(in))
	for i, f := range in {
		out[i] = f
		out[i].ObservedMs = cloneFloat64Ptr(f.ObservedMs)
		out[i].Details = slices.Clone(f.Details)
	}
	return out
}
