package api

import (
	"encoding/csv"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/llmperf/econ"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/rca"
)

// CapacitySweepLevel summarizes one steady measurement stage of a capacity sweep.
type CapacitySweepLevel struct {
	StageIndex                int      `json:"stage_index"`
	Name                      string   `json:"name"`
	Group                     string   `json:"group"`
	TargetVUsers              int      `json:"target_vusers,omitempty"`
	TargetTPS                 float64  `json:"target_tps,omitempty"`
	OfferedLoad               float64  `json:"offered_load"`
	ObservedDurationSeconds   float64  `json:"observed_duration_seconds"`
	AchievedTPS               float64  `json:"achieved_tps"`
	GoodputTPS                float64  `json:"goodput_tps"`
	GoodputPct                float64  `json:"goodput_pct"`
	Requests                  int64    `json:"requests"`
	Errors                    int64    `json:"errors"`
	ErrorRatePct              float64  `json:"error_rate_pct"`
	ThrottleRatePct           float64  `json:"throttle_rate_pct"`
	TruncationRatePct         float64  `json:"truncation_rate_pct"`
	P50Ms                     float64  `json:"p50_ms"`
	P95Ms                     float64  `json:"p95_ms"`
	P99Ms                     float64  `json:"p99_ms"`
	TTFTP50Ms                 float64  `json:"ttft_p50_ms"`
	TTFTP95Ms                 float64  `json:"ttft_p95_ms"`
	TTFTP99Ms                 float64  `json:"ttft_p99_ms"`
	TPOTP50Ms                 float64  `json:"tpot_p50_ms"`
	TPOTP95Ms                 float64  `json:"tpot_p95_ms"`
	TPOTP99Ms                 float64  `json:"tpot_p99_ms"`
	ITLP95Ms                  float64  `json:"itl_p95_ms"`
	ITLP99Ms                  float64  `json:"itl_p99_ms"`
	SystemOutputTokensPerSec  float64  `json:"system_output_tokens_per_sec"`
	PerUserOutputTokensPerSec float64  `json:"per_user_output_tokens_per_sec"`
	MeetsSLO                  bool     `json:"meets_slo"`
	SLOBreaches               []string `json:"slo_breaches,omitempty"`
	ParetoOptimal             bool     `json:"pareto_optimal,omitempty"`

	// Server-side telemetry columns (WP4.6)
	ServerKVCachePeakPct *float64 `json:"server_kv_cache_peak_pct,omitempty"`
	ServerGPUUtilAvgPct  *float64 `json:"server_gpu_util_avg_pct,omitempty"`
	ServerQueueDepthAvg  *float64 `json:"server_queue_depth_avg,omitempty"`
	ServerPreemptions    *float64 `json:"server_preemptions,omitempty"`

	// Cost columns (WP5.3)
	CostPerMillionOutputTokensUSD *float64 `json:"cost_per_million_output_tokens_usd,omitempty"`
	CostPerGoodRequestUSD         *float64 `json:"cost_per_good_request_usd,omitempty"`
	HourlyEffectiveCostUSD        *float64 `json:"hourly_effective_cost_usd,omitempty"`
}

// CapacitySweepKnee describes the load knee detected by piecewise linear fit.
type CapacitySweepKnee struct {
	Load        float64 `json:"load"`
	LoadUnit    string  `json:"load_unit"`
	Metric      string  `json:"metric"`
	SlopeBefore float64 `json:"slope_before"`
	SlopeAfter  float64 `json:"slope_after"`
	Reason      string  `json:"reason,omitempty"`
}

// CapacitySweepOperatingPoint describes the highest-throughput level that meets all SLOs.
type CapacitySweepOperatingPoint struct {
	StageIndex               int     `json:"stage_index"`
	OfferedLoad              float64 `json:"offered_load"`
	AchievedTPS              float64 `json:"achieved_tps"`
	GoodputTPS               float64 `json:"goodput_tps"`
	GoodputPct               float64 `json:"goodput_pct"`
	SystemOutputTokensPerSec float64 `json:"system_output_tokens_per_sec"`
	TTFTP95Ms                float64 `json:"ttft_p95_ms"`
	TPOTP95Ms                float64 `json:"tpot_p95_ms"`
	P95Ms                    float64 `json:"p95_ms"`
}

// GoldenPromptDriftCase captures drift for a deterministic golden prompt between
// low-load and high-load stages.
type GoldenPromptDriftCase struct {
	PromptKey      string  `json:"prompt_key"`
	LowLoadHash    string  `json:"low_load_hash"`
	HighLoadHash   string  `json:"high_load_hash"`
	HashMismatch   bool    `json:"hash_mismatch"`
	LowLoadTokens  int64   `json:"low_load_tokens"`
	HighLoadTokens int64   `json:"high_load_tokens"`
	LengthRatio    float64 `json:"length_ratio"`
	SchemaFailure  bool    `json:"schema_failure,omitempty"`
	DriftDetected  bool    `json:"drift_detected"`
}

// GoldenPromptDriftSummary summarizes temperature-0 golden prompt stability under load.
type GoldenPromptDriftSummary struct {
	EvaluatedPrompts int                     `json:"evaluated_prompts"`
	DriftedPrompts   int                     `json:"drifted_prompts"`
	DriftRatePct     float64                 `json:"drift_rate_pct"`
	Cases            []GoldenPromptDriftCase `json:"cases,omitempty"`
}

// CapacitySweepReport is the complete capacity-sweep analysis for a run.
type CapacitySweepReport struct {
	TestID             string                       `json:"test_id"`
	TestName           string                       `json:"test_name"`
	Mode               string                       `json:"mode"`
	SLOTTFTMs          *float64                     `json:"slo_ttft_ms,omitempty"`
	SLOTPOTMs          *float64                     `json:"slo_tpot_ms,omitempty"`
	SLOE2EMs           *float64                     `json:"slo_e2e_ms,omitempty"`
	MinGoodputPct      *float64                     `json:"min_goodput_pct,omitempty"`
	Levels             []CapacitySweepLevel         `json:"levels"`
	Knee               *CapacitySweepKnee           `json:"knee,omitempty"`
	KneeNote           string                       `json:"knee_note,omitempty"`
	MaxSustainableRate *CapacitySweepOperatingPoint `json:"max_sustainable_rate,omitempty"`
	ParetoFrontier     []CapacitySweepLevel         `json:"pareto_frontier,omitempty"`
	GoldenDrift        *GoldenPromptDriftSummary    `json:"golden_drift,omitempty"`
}

type stageTimeSpan struct {
	level      RunArchiveLoadLevelSummary
	start      time.Time
	end        time.Time
	llmSummary *engine.LLMSummary
	txRecords  []TransactionEvent
	intervals  []IntervalSummaryRecord
}

// BuildCapacitySweepReport builds the per-level capacity sweep report, knee fit,
// max rate at SLO, Pareto frontier, server metrics, and cost columns.
func BuildCapacitySweepReport(
	testID, testName string,
	cfg *CapacitySweepConfig,
	summaries []IntervalSummaryRecord,
	transactions []TransactionEvent,
	monitorPoints []analysis.MetricPoint,
	deployments []econ.DeploymentSpec,
) *CapacitySweepReport {
	baseLevels := buildRunArchiveLoadLevelSummaries(summaries)
	mode := "concurrency"
	var sloTTFT, sloTPOT, sloE2E, minGoodput *float64
	if cfg != nil {
		if strings.EqualFold(strings.TrimSpace(cfg.Mode), "rate") {
			mode = "rate"
		}
		sloTTFT = cfg.SLOTTFTMs
		sloTPOT = cfg.SLOTPOTMs
		sloE2E = cfg.SLOE2EMs
		minGoodput = cfg.MinGoodputPct
	} else {
		for _, l := range baseLevels {
			if l.Kind == "rate" || (l.TargetTPS > 0 && l.TargetVUsers == 0) {
				mode = "rate"
				break
			}
		}
	}

	report := &CapacitySweepReport{
		TestID:        testID,
		TestName:      testName,
		Mode:          mode,
		SLOTTFTMs:     sloTTFT,
		SLOTPOTMs:     sloTPOT,
		SLOE2EMs:      sloE2E,
		MinGoodputPct: minGoodput,
		Levels:        make([]CapacitySweepLevel, 0, len(baseLevels)),
	}
	if len(baseLevels) < 2 && cfg != nil && len(transactions) > 0 {
		if synthLevels, synthSpans := synthesizeSweepSpansFromConfig(cfg, transactions); len(synthLevels) >= 2 {
			baseLevels = synthLevels
			spans := synthSpans
			return finalizeCapacitySweepSpans(report, mode, sloTTFT, sloTPOT, sloE2E, minGoodput, spans, transactions, monitorPoints, deployments)
		}
	}
	if len(baseLevels) == 0 {
		return report
	}

	// Prefer __total__ levels if present; otherwise use the first group's levels.
	selectedGroup := "__total__"
	hasTotal := false
	for _, l := range baseLevels {
		if l.Group == "__total__" {
			hasTotal = true
			break
		}
	}
	if !hasTotal {
		selectedGroup = baseLevels[0].Group
	}

	spans := make(map[int]*stageTimeSpan)
	for _, l := range baseLevels {
		if l.Group != selectedGroup {
			continue
		}
		spans[l.StageIndex] = &stageTimeSpan{
			level: l,
		}
	}

	// Map interval summaries to stage spans to establish time windows and interval-level LLM fallbacks.
	for idx, s := range summaries {
		sec := archiveSummaryIntervalSeconds(summaries, idx)
		dur := time.Duration(sec * float64(time.Second))
		if dur <= 0 {
			dur = time.Second
		}
		intStart := s.Timestamp.Add(-dur)
		intEnd := s.Timestamp
		for _, active := range s.LoadLevels {
			if active.Warmup || active.Group != selectedGroup {
				continue
			}
			sp := spans[active.StageIndex]
			if sp == nil {
				continue
			}
			if sp.start.IsZero() || intStart.Before(sp.start) {
				sp.start = intStart
			}
			if sp.end.IsZero() || intEnd.After(sp.end) {
				sp.end = intEnd
			}
			sp.intervals = append(sp.intervals, s)
		}
	}
	return finalizeCapacitySweepSpans(report, mode, sloTTFT, sloTPOT, sloE2E, minGoodput, spans, transactions, monitorPoints, deployments)
}

func synthesizeSweepSpansFromConfig(cfg *CapacitySweepConfig, transactions []TransactionEvent) ([]RunArchiveLoadLevelSummary, map[int]*stageTimeSpan) {
	stages, totalDur, err := ExpandCapacitySweepStages(cfg)
	if err != nil || len(stages) == 0 || totalDur <= 0 {
		return nil, nil
	}
	var minTs, maxTs time.Time
	for _, tx := range transactions {
		if tx.Timestamp.IsZero() {
			continue
		}
		if minTs.IsZero() || tx.Timestamp.Before(minTs) {
			minTs = tx.Timestamp
		}
		if maxTs.IsZero() || tx.Timestamp.After(maxTs) {
			maxTs = tx.Timestamp
		}
	}
	if minTs.IsZero() {
		return nil, nil
	}
	actualWindow := maxTs.Sub(minTs)
	if actualWindow < time.Second {
		actualWindow = totalDur
	}
	groupName := strings.TrimSpace(cfg.Group)
	if groupName == "" {
		groupName = "default"
	}
	kind := "vusers"
	if strings.EqualFold(strings.TrimSpace(cfg.Mode), "rate") {
		kind = "rate"
	}
	levels := make([]RunArchiveLoadLevelSummary, 0, len(stages))
	spans := make(map[int]*stageTimeSpan, len(stages))
	elapsed := time.Duration(0)
	for idx, st := range stages {
		stDur, err := time.ParseDuration(st.Duration)
		if err != nil || stDur <= 0 {
			stDur = time.Second
		}
		startFrac := float64(elapsed) / float64(totalDur)
		elapsed += stDur
		endFrac := float64(elapsed) / float64(totalDur)
		stStart := minTs.Add(time.Duration(startFrac * float64(actualWindow)))
		stEnd := minTs.Add(time.Duration(endFrac * float64(actualWindow)))
		if idx == len(stages)-1 {
			stEnd = maxTs.Add(time.Millisecond)
		}
		if st.Warmup {
			continue
		}
		targetTPS := 0.0
		if st.TargetTPS != nil {
			targetTPS = *st.TargetTPS
		}
		lvl := RunArchiveLoadLevelSummary{
			Group:                   groupName,
			StageIndex:              idx,
			Name:                    st.Name,
			Kind:                    kind,
			TargetVUsers:            st.TargetVUsers,
			TargetTPS:               targetTPS,
			ConfiguredDuration:      st.Duration,
			ObservedDurationSeconds: stDur.Seconds(),
		}
		levels = append(levels, lvl)
		spans[idx] = &stageTimeSpan{
			level: lvl,
			start: stStart,
			end:   stEnd,
		}
	}
	return levels, spans
}

func finalizeCapacitySweepSpans(
	report *CapacitySweepReport,
	mode string,
	sloTTFT, sloTPOT, sloE2E, minGoodput *float64,
	spans map[int]*stageTimeSpan,
	transactions []TransactionEvent,
	monitorPoints []analysis.MetricPoint,
	deployments []econ.DeploymentSpec,
) *CapacitySweepReport {

	// Assign TransactionEvents to spans by timestamp.
	for _, tx := range transactions {
		if tx.BusinessTransaction || tx.Population == "transaction" {
			continue
		}
		for _, sp := range spans {
			if !sp.start.IsZero() && !sp.end.IsZero() && !tx.Timestamp.Before(sp.start) && !tx.Timestamp.After(sp.end) {
				sp.txRecords = append(sp.txRecords, tx)
				if tx.ServerTelemetry != nil && tx.ServerTelemetry.LLM != nil {
					engine.ObserveLLMAction(&sp.llmSummary, &protocol.Result{
						Success:         tx.Success && tx.StatusCode < 400 && tx.ErrorMessage == "",
						StatusCode:      tx.StatusCode,
						Duration:        time.Duration(tx.DurationMs * float64(time.Millisecond)),
						ServerTelemetry: tx.ServerTelemetry,
					})
				}
				break
			}
		}
	}

	sloTTFTVal := 0.0
	if sloTTFT != nil {
		sloTTFTVal = *sloTTFT
	}
	sloTPOTVal := 0.0
	if sloTPOT != nil {
		sloTPOTVal = *sloTPOT
	}
	sloE2EVal := 0.0
	if sloE2E != nil {
		sloE2EVal = *sloE2E
	}
	minGoodputVal := 95.0
	if minGoodput != nil {
		minGoodputVal = *minGoodput
	}

	stageIndices := make([]int, 0, len(spans))
	for idx := range spans {
		stageIndices = append(stageIndices, idx)
	}
	sort.Ints(stageIndices)

	for _, stageIdx := range stageIndices {
		sp := spans[stageIdx]
		base := sp.level
		offered := float64(base.TargetVUsers)
		if mode == "rate" && base.TargetTPS > 0 {
			offered = base.TargetTPS
		}
		lvl := CapacitySweepLevel{
			StageIndex:              base.StageIndex,
			Name:                    base.Name,
			Group:                   base.Group,
			TargetVUsers:            base.TargetVUsers,
			TargetTPS:               base.TargetTPS,
			OfferedLoad:             offered,
			ObservedDurationSeconds: base.ObservedDurationSeconds,
			AchievedTPS:             base.Throughput,
			Requests:                base.Requests,
			Errors:                  base.Errors,
			ErrorRatePct:            base.ErrorRatePct,
			P50Ms:                   base.P50Ms,
			P95Ms:                   base.P95Ms,
			P99Ms:                   base.P99Ms,
		}

		// If no raw transactions were recorded for this span, merge interval summaries.
		if sp.llmSummary == nil {
			for _, s := range sp.intervals {
				if s.LLM == nil {
					continue
				}
				if s.LLM.IntervalSummary != nil && s.LLM.IntervalSummary.Requests > 0 {
					engine.MergeLLMSummary(&sp.llmSummary, s.LLM.IntervalSummary)
				} else if s.LLM.Summary != nil && s.LLM.Summary.Requests > 0 {
					engine.MergeLLMSummary(&sp.llmSummary, s.LLM.Summary)
				}
			}
		}

		var totalInputToks, totalOutputToks, totalCachedToks int64
		if sp.llmSummary != nil && sp.llmSummary.Requests > 0 {
			engine.FinalizeLLMSummary(sp.llmSummary)
			sum := sp.llmSummary
			totalInputToks = sum.PromptTokens
			totalOutputToks = sum.CompletionTokens
			totalCachedToks = sum.CachedTokens
			lvl.TTFTP50Ms = sum.P50TTFTMs
			lvl.TTFTP95Ms = sum.P95TTFTMs
			lvl.TTFTP99Ms = sum.P99TTFTMs
			lvl.TPOTP50Ms = sum.P50TPOTMs
			lvl.TPOTP95Ms = sum.P95TPOTMs
			lvl.TPOTP99Ms = sum.P99TPOTMs
			lvl.ITLP95Ms = sum.P95ITLMs
			lvl.ITLP99Ms = sum.P99ITLMs
			if sum.Requests > 0 {
				lvl.ThrottleRatePct = float64(sum.ThrottledRequests) * 100.0 / float64(sum.Requests)
				lvl.TruncationRatePct = float64(sum.TruncatedRequests) * 100.0 / float64(sum.Requests)
			}
			if base.ObservedDurationSeconds > 0 {
				lvl.SystemOutputTokensPerSec = float64(sum.CompletionTokens) / base.ObservedDurationSeconds
			}
			if lvl.TPOTP50Ms > 0 {
				lvl.PerUserOutputTokensPerSec = 1000.0 / lvl.TPOTP50Ms
			}
			if len(sp.txRecords) > 0 {
				var goodCount int64
				for _, tx := range sp.txRecords {
					if tx.ServerTelemetry == nil || tx.ServerTelemetry.LLM == nil {
						continue
					}
					diag := tx.ServerTelemetry.LLM
					if !tx.Success || tx.StatusCode >= 400 || tx.ErrorMessage != "" || diag.Truncated || diag.Throttled || diag.ErrorCode != "" || diag.EmptyOutput {
						continue
					}
					ok := true
					if sloTTFTVal > 0 && diag.TTFTMs > sloTTFTVal {
						ok = false
					}
					if sloTPOTVal > 0 && diag.TPOTMs > sloTPOTVal {
						ok = false
					}
					if sloE2EVal > 0 && tx.DurationMs > sloE2EVal {
						ok = false
					}
					if ok {
						goodCount++
					}
				}
				lvl.GoodputPct = float64(goodCount) * 100.0 / float64(sum.Requests)
			} else {
				lvl.GoodputPct = sum.GoodputPct(sloTTFTVal, sloTPOTVal, sloE2EVal)
			}
		} else {
			if base.Requests+base.Errors > 0 {
				goodPct := 100.0 - base.ErrorRatePct
				if sloE2EVal > 0 && base.P95Ms > sloE2EVal {
					goodPct = 0
				}
				lvl.GoodputPct = goodPct
			}
		}
		lvl.GoodputTPS = lvl.AchievedTPS * (lvl.GoodputPct / 100.0)

		// Evaluate SLO compliance for this level.
		var breaches []string
		if sloTTFTVal > 0 && lvl.TTFTP95Ms > sloTTFTVal {
			breaches = append(breaches, fmt.Sprintf("ttft_p95_ms %.1f > %.1f", lvl.TTFTP95Ms, sloTTFTVal))
		}
		if sloTPOTVal > 0 && lvl.TPOTP95Ms > sloTPOTVal {
			breaches = append(breaches, fmt.Sprintf("tpot_p95_ms %.1f > %.1f", lvl.TPOTP95Ms, sloTPOTVal))
		}
		if sloE2EVal > 0 && lvl.P95Ms > sloE2EVal {
			breaches = append(breaches, fmt.Sprintf("p95_ms %.1f > %.1f", lvl.P95Ms, sloE2EVal))
		}
		if minGoodput != nil && lvl.GoodputPct+1e-9 < minGoodputVal {
			breaches = append(breaches, fmt.Sprintf("goodput_pct %.1f < %.1f", lvl.GoodputPct, minGoodputVal))
		}
		lvl.SLOBreaches = breaches
		lvl.MeetsSLO = len(breaches) == 0

		// Populate server-side metrics for this level window (WP4.6).
		populateSweepLevelServerMetrics(&lvl, sp.start, sp.end, monitorPoints)

		// Populate cost metrics if a deployment profile is configured (WP5.3).
		if len(deployments) > 0 && base.ObservedDurationSeconds > 0 {
			goodReqs := int64(math.Round(float64(base.Requests) * (lvl.GoodputPct / 100.0)))
			econSum := econ.Evaluate(deployments[0], econ.RunObservation{
				DurationSeconds: base.ObservedDurationSeconds,
				PromptTokens:    totalInputToks,
				OutputTokens:    totalOutputToks,
				CachedTokens:    totalCachedToks,
				Requests:        base.Requests + base.Errors,
				GoodputRequests: goodReqs,
				SystemOutputTPS: lvl.SystemOutputTokensPerSec,
			})
			if econSum.CostPer1MOutputTokensUSD > 0 {
				v := econSum.CostPer1MOutputTokensUSD
				lvl.CostPerMillionOutputTokensUSD = &v
			}
			if econSum.CostPer1KGoodputRequestsUSD > 0 {
				v := econSum.CostPer1KGoodputRequestsUSD / 1000.0
				lvl.CostPerGoodRequestUSD = &v
			}
			if econSum.HourlyInfraCostUSD > 0 {
				v := econSum.HourlyInfraCostUSD
				lvl.HourlyEffectiveCostUSD = &v
			}
		}

		report.Levels = append(report.Levels, lvl)
	}

	// Fit knee using rca.KneeFit on TTFT p95 (if LLM data present) or E2E p95.
	useTTFT := false
	for _, l := range report.Levels {
		if l.TTFTP95Ms > 0 {
			useTTFT = true
			break
		}
	}
	capLevels := make([]rca.CapacityLevel, 0, len(report.Levels))
	metricName := "p95_ms"
	if useTTFT {
		metricName = "ttft_p95_ms"
	}
	for _, l := range report.Levels {
		val := l.P95Ms
		if useTTFT {
			val = l.TTFTP95Ms
		}
		capLevels = append(capLevels, rca.CapacityLevel{
			Load:  l.OfferedLoad,
			P95Ms: val,
		})
	}
	kneeLoad, before, after, reason := rca.KneeFit(capLevels, 5.0)
	loadUnit := "vusers"
	if mode == "rate" {
		loadUnit = "tps"
	}
	if kneeLoad != nil {
		report.Knee = &CapacitySweepKnee{
			Load:        *kneeLoad,
			LoadUnit:    loadUnit,
			Metric:      metricName,
			SlopeBefore: before,
			SlopeAfter:  after,
		}
	} else {
		report.KneeNote = reason
	}

	// Max sustainable rate at SLO: highest GoodputTPS (or SystemOutputTokensPerSec) among levels with MeetsSLO.
	for _, l := range report.Levels {
		if !l.MeetsSLO {
			continue
		}
		if report.MaxSustainableRate == nil || l.GoodputTPS > report.MaxSustainableRate.GoodputTPS ||
			(l.GoodputTPS == report.MaxSustainableRate.GoodputTPS && l.SystemOutputTokensPerSec > report.MaxSustainableRate.SystemOutputTokensPerSec) {
			report.MaxSustainableRate = &CapacitySweepOperatingPoint{
				StageIndex:               l.StageIndex,
				OfferedLoad:              l.OfferedLoad,
				AchievedTPS:              l.AchievedTPS,
				GoodputTPS:               l.GoodputTPS,
				GoodputPct:               l.GoodputPct,
				SystemOutputTokensPerSec: l.SystemOutputTokensPerSec,
				TTFTP95Ms:                l.TTFTP95Ms,
				TPOTP95Ms:                l.TPOTP95Ms,
				P95Ms:                    l.P95Ms,
			}
		}
	}

	// Pareto frontier: non-dominated in (higher SystemOutputTokensPerSec / AchievedTPS, lower TTFTP95Ms / P95Ms).
	for i := range report.Levels {
		dominated := false
		aThroughput := report.Levels[i].SystemOutputTokensPerSec
		if aThroughput == 0 {
			aThroughput = report.Levels[i].AchievedTPS
		}
		aLatency := report.Levels[i].TTFTP95Ms
		if aLatency == 0 {
			aLatency = report.Levels[i].P95Ms
		}
		for j := range report.Levels {
			if i == j {
				continue
			}
			bThroughput := report.Levels[j].SystemOutputTokensPerSec
			if bThroughput == 0 {
				bThroughput = report.Levels[j].AchievedTPS
			}
			bLatency := report.Levels[j].TTFTP95Ms
			if bLatency == 0 {
				bLatency = report.Levels[j].P95Ms
			}
			if bThroughput >= aThroughput && bLatency <= aLatency && (bThroughput > aThroughput || bLatency < aLatency) {
				dominated = true
				break
			}
		}
		if !dominated {
			report.Levels[i].ParetoOptimal = true
			report.ParetoFrontier = append(report.ParetoFrontier, report.Levels[i])
		}
	}

	// Golden prompt drift check between the lowest and highest load levels.
	if len(stageIndices) >= 2 {
		lowSpan := spans[stageIndices[0]]
		highSpan := spans[stageIndices[len(stageIndices)-1]]
		if drift := evaluateGoldenPromptDrift(lowSpan.txRecords, highSpan.txRecords); drift != nil && drift.EvaluatedPrompts > 0 {
			report.GoldenDrift = drift
		}
	}

	return report
}

func populateSweepLevelServerMetrics(lvl *CapacitySweepLevel, start, end time.Time, points []analysis.MetricPoint) {
	if len(points) == 0 || start.IsZero() || end.IsZero() {
		return
	}
	var kvMax float64
	var hasKV bool
	var gpuSum float64
	var gpuCount int
	var queueSum float64
	var queueCount int
	var preemptMax, preemptMin float64
	var hasPreempt bool

	for _, pt := range points {
		if pt.Value == nil || pt.TimestampUTC.Before(start) || pt.TimestampUTC.After(end) {
			continue
		}
		name := strings.ToLower(pt.MetricName)
		val := *pt.Value
		switch {
		case strings.Contains(name, "kv_cache") || strings.Contains(name, "gpu_cache_usage"):
			if val <= 1.0 && (strings.Contains(name, "perc") || strings.Contains(name, "ratio") || strings.Contains(name, "usage")) && pt.Unit != "%" && pt.Unit != "percent" {
				val *= 100.0
			}
			if !hasKV || val > kvMax {
				kvMax = val
				hasKV = true
			}
		case strings.Contains(name, "dcgm_fi_dev_gpu_util") || strings.Contains(name, "gpu_utilization"):
			gpuSum += val
			gpuCount++
		case strings.Contains(name, "num_requests_waiting") || strings.Contains(name, "queue_size") || strings.Contains(name, "queue_depth"):
			queueSum += val
			queueCount++
		case strings.Contains(name, "num_preemptions"):
			if !hasPreempt {
				preemptMin = val
				preemptMax = val
				hasPreempt = true
			} else {
				if val < preemptMin {
					preemptMin = val
				}
				if val > preemptMax {
					preemptMax = val
				}
			}
		}
	}
	if hasKV {
		lvl.ServerKVCachePeakPct = &kvMax
	}
	if gpuCount > 0 {
		avg := gpuSum / float64(gpuCount)
		lvl.ServerGPUUtilAvgPct = &avg
	}
	if queueCount > 0 {
		avg := queueSum / float64(queueCount)
		lvl.ServerQueueDepthAvg = &avg
	}
	if hasPreempt {
		delta := math.Max(0, preemptMax-preemptMin)
		lvl.ServerPreemptions = &delta
	}
}

func evaluateGoldenPromptDrift(lowRecords, highRecords []TransactionEvent) *GoldenPromptDriftSummary {
	type sample struct {
		hash       string
		tokens     int64
		schemaFail bool
	}
	extract := func(records []TransactionEvent) map[string]sample {
		out := make(map[string]sample)
		for _, r := range records {
			if r.ServerTelemetry == nil || r.ServerTelemetry.LLM == nil {
				continue
			}
			diag := r.ServerTelemetry.LLM
			key := strings.TrimSpace(diag.GoldenPromptID)
			if key == "" {
				continue
			}
			var h string
			if diag.OutputHash != 0 {
				h = fmt.Sprintf("%016x", diag.OutputHash)
			}
			out[key] = sample{
				hash:       h,
				tokens:     int64(diag.CompletionTokens),
				schemaFail: diag.SchemaFailed,
			}
		}
		return out
	}
	lowMap := extract(lowRecords)
	highMap := extract(highRecords)
	if len(lowMap) == 0 || len(highMap) == 0 {
		return nil
	}
	keys := make([]string, 0, len(lowMap))
	for k := range lowMap {
		if _, ok := highMap[k]; ok {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		return nil
	}
	summary := &GoldenPromptDriftSummary{
		EvaluatedPrompts: len(keys),
		Cases:            make([]GoldenPromptDriftCase, 0, len(keys)),
	}
	for _, k := range keys {
		lo := lowMap[k]
		hi := highMap[k]
		ratio := 1.0
		if lo.tokens > 0 {
			ratio = float64(hi.tokens) / float64(lo.tokens)
		}
		hashMismatch := lo.hash != "" && hi.hash != "" && lo.hash != hi.hash
		schemaFail := lo.schemaFail || hi.schemaFail
		drifted := hashMismatch || schemaFail || math.Abs(ratio-1.0) > 0.15
		if drifted {
			summary.DriftedPrompts++
		}
		summary.Cases = append(summary.Cases, GoldenPromptDriftCase{
			PromptKey:      k,
			LowLoadHash:    lo.hash,
			HighLoadHash:   hi.hash,
			HashMismatch:   hashMismatch,
			LowLoadTokens:  lo.tokens,
			HighLoadTokens: hi.tokens,
			LengthRatio:    ratio,
			SchemaFailure:  schemaFail,
			DriftDetected:  drifted,
		})
	}
	summary.DriftRatePct = float64(summary.DriftedPrompts) * 100.0 / float64(summary.EvaluatedPrompts)
	return summary
}

// FormatCapacitySweepCSV renders the per-level capacity sweep table as CSV.
func FormatCapacitySweepCSV(report *CapacitySweepReport) string {
	var sb strings.Builder
	w := csv.NewWriter(&sb)
	_ = w.Write([]string{
		"stage_index",
		"name",
		"offered_load",
		"achieved_tps",
		"goodput_tps",
		"goodput_pct",
		"ttft_p50_ms",
		"ttft_p95_ms",
		"ttft_p99_ms",
		"tpot_p50_ms",
		"tpot_p95_ms",
		"tpot_p99_ms",
		"itl_p95_ms",
		"p95_ms",
		"system_output_tokens_per_sec",
		"per_user_output_tokens_per_sec",
		"error_rate_pct",
		"throttle_rate_pct",
		"truncation_rate_pct",
		"meets_slo",
		"pareto_optimal",
	})
	if report != nil {
		for _, l := range report.Levels {
			_ = w.Write([]string{
				strconv.Itoa(l.StageIndex),
				l.Name,
				fmt.Sprintf("%.2f", l.OfferedLoad),
				fmt.Sprintf("%.2f", l.AchievedTPS),
				fmt.Sprintf("%.2f", l.GoodputTPS),
				fmt.Sprintf("%.2f", l.GoodputPct),
				fmt.Sprintf("%.2f", l.TTFTP50Ms),
				fmt.Sprintf("%.2f", l.TTFTP95Ms),
				fmt.Sprintf("%.2f", l.TTFTP99Ms),
				fmt.Sprintf("%.2f", l.TPOTP50Ms),
				fmt.Sprintf("%.2f", l.TPOTP95Ms),
				fmt.Sprintf("%.2f", l.TPOTP99Ms),
				fmt.Sprintf("%.2f", l.ITLP95Ms),
				fmt.Sprintf("%.2f", l.P95Ms),
				fmt.Sprintf("%.2f", l.SystemOutputTokensPerSec),
				fmt.Sprintf("%.2f", l.PerUserOutputTokensPerSec),
				fmt.Sprintf("%.2f", l.ErrorRatePct),
				fmt.Sprintf("%.2f", l.ThrottleRatePct),
				fmt.Sprintf("%.2f", l.TruncationRatePct),
				strconv.FormatBool(l.MeetsSLO),
				strconv.FormatBool(l.ParetoOptimal),
			})
		}
	}
	w.Flush()
	return sb.String()
}
