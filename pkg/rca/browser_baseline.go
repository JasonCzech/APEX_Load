package rca

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Browser baseline anomalies (browser_baseline.v1, BI-2). Each browser
// document cohort of the run — one exact measurement tuple: metric and
// algorithm version, engine, device profile, group, frame scope, lifecycle
// phase and the actual runtime cohort (browser version, platform, viewport,
// shaping, cache and new-user policy) — is compared, per steady load level,
// with the same tuple of a workload-qualified baseline run at the same load.
// Documents are grouped into per-bucket p75 values before any comparison,
// and a measurement is counted once at its latest retained state, so
// repeated publications cannot manufacture samples. The result is advisory:
// application SLAs own CI and stop decisions.

// BrowserBaselineVersion identifies the detector and its parameters.
const BrowserBaselineVersion = "browser_baseline.v1"

// Browser baseline result states.
const (
	BrowserBaselineAnomaly     = "anomaly"
	BrowserBaselineWithinRange = "within_range"
	BrowserBaselineUnscored    = "unscored"
)

// Reasons a browser baseline result is unscored, or evidence was excluded.
const (
	BrowserBaselineNoBaselineRun       = "no_qualified_baseline"
	BrowserBaselineNoBaselineCohort    = "no_baseline_cohort"
	BrowserBaselineCohortChanged       = "cohort_changed"
	BrowserBaselineNoSameLoadWindow    = "no_same_load_window"
	BrowserBaselineInsufficient        = "insufficient_baseline"
	BrowserBaselineZeroVariance        = "zero_variance"
	BrowserBaselineNoEffectFloor       = "no_effect_floor"
	BrowserBaselineOutsideSteadyLoad   = "outside_steady_load"
	BrowserBaselineSourceLoss          = "source_loss"
	BrowserBaselineBaselineSourceLoss  = "baseline_source_loss"
	BrowserBaselineResultsTruncated    = "browser_baseline_truncated"
	BrowserBaselineLevelsUnavailable   = "load_levels_unavailable"
	browserBaselineUnscoredDetailLimit = 8
)

// BrowserBaselineParams are the detector's thresholds.
type BrowserBaselineParams struct {
	Version string `json:"version"`
	// MinBaselineBuckets valid baseline buckets at the same load are needed
	// to score a cohort; Sigma is the |z| an anomaly reaches.
	MinBaselineBuckets int     `json:"min_baseline_buckets"`
	Sigma              float64 `json:"sigma"`
	// A timing anomaly must also move at least max(TimingFloorMs,
	// TimingFloorFraction of the baseline mean); layout shift at least
	// CLSFloor; any other metric its MetricFloors entry.
	TimingFloorMs       float64            `json:"timing_floor_ms"`
	TimingFloorFraction float64            `json:"timing_floor_fraction"`
	CLSFloor            float64            `json:"cls_floor"`
	MetricFloors        map[string]float64 `json:"metric_floors,omitempty"`
	// LevelTolerance is how far two loads may differ and still be the same
	// level. MaxResults bounds the output; anomalies are kept first.
	LevelTolerance float64 `json:"level_tolerance"`
	MaxResults     int     `json:"max_results"`
}

// DefaultBrowserBaselineParams are the specification's values.
var DefaultBrowserBaselineParams = BrowserBaselineParams{
	Version: BrowserBaselineVersion, MinBaselineBuckets: analysis.DefaultAnomalyMinBuckets, Sigma: analysis.DefaultAnomalySigma,
	TimingFloorMs: 50, TimingFloorFraction: 0.10, CLSFloor: 0.02, LevelTolerance: DefaultDetectParams.LevelTolerance, MaxResults: 200,
}

// DocumentCohort describes a browser document series: its metric, the
// measurement tuple without the runtime cohort (Family) and the runtime
// cohort's fields, so a cohort without an exact baseline match can say what
// changed. Incomplete marks evidence that dropped document observations.
type DocumentCohort struct {
	Metric     string            `json:"metric"`
	Family     string            `json:"family"`
	Fields     map[string]string `json:"fields,omitempty"`
	Incomplete bool              `json:"incomplete,omitempty"`
}

// BrowserBaseline is the detector's output for one assessment.
type BrowserBaseline struct {
	Version string                `json:"version"`
	Status  string                `json:"status"`
	Params  BrowserBaselineParams `json:"params"`
	// Baseline names the workload-qualified baseline run, when there was one.
	Baseline BaselineRef             `json:"baseline"`
	Results  []BrowserBaselineResult `json:"results"`
	// Excluded counts current document buckets left out, by reason.
	Excluded    map[string]int64 `json:"excluded,omitempty"`
	Limitations []Limitation     `json:"limitations"`
}

// BrowserBaselineResult is one cohort's comparison at one load level.
type BrowserBaselineResult struct {
	ID     string `json:"id"`
	Series string `json:"series"`
	Metric string `json:"metric"`
	Unit   string `json:"unit"`
	Level  string `json:"level"`
	State  string `json:"state"`
	// Reason says why a result is unscored; Detail names what differed.
	Reason    string `json:"reason,omitempty"`
	Detail    string `json:"detail,omitempty"`
	Direction string `json:"direction,omitempty"`
	// Current is the median of the run's per-bucket p75 values at the level.
	Current         float64 `json:"current"`
	CurrentBuckets  int     `json:"current_buckets"`
	CurrentSamples  int64   `json:"current_samples"`
	BaselineMean    float64 `json:"baseline_mean,omitempty"`
	BaselineStdDev  float64 `json:"baseline_stddev,omitempty"`
	BaselineBuckets int     `json:"baseline_buckets"`
	BaselineSamples int64   `json:"baseline_samples"`
	// ExpectedLow and ExpectedHigh are the baseline mean ± Sigma standard
	// deviations (never below zero): the range a value is expected in.
	ExpectedLow  float64 `json:"expected_low,omitempty"`
	ExpectedHigh float64 `json:"expected_high,omitempty"`
	Z            float64 `json:"z,omitempty"`
	// Effect is Current minus the baseline mean; EffectFloor the smallest
	// effect an anomaly needs.
	Effect      float64       `json:"effect,omitempty"`
	EffectFloor float64       `json:"effect_floor,omitempty"`
	Caveats     []string      `json:"caveats,omitempty"`
	Baseline    BaselineRef   `json:"baseline"`
	Evidence    []EvidenceRef `json:"evidence"`
}

// DetectBrowserBaseline compares the run's browser document cohorts with the
// baseline run's. It returns nil when the run has no browser document series.
func DetectBrowserBaseline(input DetectionInput, params BrowserBaselineParams) *BrowserBaseline {
	var documents []Series
	for _, s := range input.Series {
		if s.Kind == SeriesBrowserDocument && s.Document != nil {
			documents = append(documents, s)
		}
	}
	if len(documents) == 0 {
		return nil
	}
	out := &BrowserBaseline{Version: BrowserBaselineVersion, Status: DetectionComplete, Params: params, Results: []BrowserBaselineResult{}, Limitations: []Limitation{}}
	levels := sortedLevels(input.Levels)
	if len(levels) == 0 {
		out.Status = DetectionUnavailable
		out.Limitations = append(out.Limitations, Limitation{Code: BrowserBaselineLevelsUnavailable, Detail: "no steady load level was measured, so no document has a same-load baseline"})
		return out
	}
	baseline := input.Baseline
	if baseline == nil || baseline.Qualification == BaselineUnqualified {
		out.Status = DetectionUnavailable
		out.Limitations = append(out.Limitations, Limitation{Code: BrowserBaselineNoBaselineRun, Detail: "no workload-qualified baseline run was given, so no browser document cohort was compared"})
		return out
	}
	out.Baseline = BaselineRef{TestID: baseline.TestID, Qualification: baseline.Qualification}
	byID, byFamily := map[string]Series{}, map[string][]Series{}
	for _, s := range baseline.Series {
		if s.Kind == SeriesBrowserDocument && s.Document != nil {
			byID[s.ID] = s
			byFamily[s.Document.Family] = append(byFamily[s.Document.Family], s)
		}
	}
	baselineLevels := sortedLevels(baseline.Levels)
	out.Excluded = map[string]int64{}
	for _, s := range documents {
		s.Points = sortedPoints(s.Points)
		spans := segments(s, levels)
		for index, level := range levels {
			current, samples := spanValues(spans[index])
			if len(current) == 0 {
				continue
			}
			out.Results = append(out.Results, compareBrowserCohort(s, level, current, samples, byID, byFamily, baselineLevels, out.Baseline, params))
		}
		for _, p := range s.Points {
			if usable(p) && !insideAny(s, p, levels) {
				out.Excluded[BrowserBaselineOutsideSteadyLoad]++
			}
		}
	}
	if len(out.Excluded) == 0 {
		out.Excluded = nil
	}
	sort.SliceStable(out.Results, func(i, j int) bool {
		a, b := out.Results[i], out.Results[j]
		if rank(a.State) != rank(b.State) {
			return rank(a.State) < rank(b.State)
		}
		if math.Abs(a.Z) != math.Abs(b.Z) {
			return math.Abs(a.Z) > math.Abs(b.Z)
		}
		return a.ID < b.ID
	})
	if params.MaxResults > 0 && len(out.Results) > params.MaxResults {
		cut := len(out.Results) - params.MaxResults
		out.Results = out.Results[:params.MaxResults]
		out.Status = DetectionPartial
		out.Limitations = append(out.Limitations, Limitation{Code: BrowserBaselineResultsTruncated, Detail: fmt.Sprintf("kept %d of %d browser baseline results, anomalies first", params.MaxResults, params.MaxResults+cut)})
	}
	return out
}

func rank(state string) int {
	switch state {
	case BrowserBaselineAnomaly:
		return 0
	case BrowserBaselineWithinRange:
		return 1
	}
	return 2
}

func insideAny(s Series, p SeriesPoint, levels []LoadLevel) bool {
	for _, level := range levels {
		if !p.At.Before(level.Start) && s.within(p, level) {
			return true
		}
	}
	return false
}

func spanValues(points []SeriesPoint) ([]float64, int64) {
	var values []float64
	var samples int64
	for _, p := range points {
		if usable(p) {
			values = append(values, p.Value)
			samples += p.Count
		}
	}
	return values, samples
}

func compareBrowserCohort(s Series, level LoadLevel, current []float64, samples int64, byID map[string]Series, byFamily map[string][]Series, baselineLevels []LoadLevel, ref BaselineRef, params BrowserBaselineParams) BrowserBaselineResult {
	sorted := slices.Clone(current)
	slices.Sort(sorted)
	result := BrowserBaselineResult{Series: s.ID, Metric: s.Document.Metric, Unit: s.Unit, Level: level.Label(), Current: medianSorted(sorted), CurrentBuckets: len(current), CurrentSamples: samples,
		Baseline: ref, Evidence: []EvidenceRef{s.Evidence}}
	sum := sha256.Sum256([]byte(BrowserBaselineVersion + "\x00" + s.ID + "\x00" + result.Level + "\x00" + ref.TestID))
	result.ID = "browser_baseline/" + hex.EncodeToString(sum[:8])
	if s.Document.Incomplete {
		result.Caveats = append(result.Caveats, BrowserBaselineSourceLoss)
	}
	unscored := func(reason, detail string) BrowserBaselineResult {
		result.State, result.Reason, result.Detail = BrowserBaselineUnscored, reason, detail
		return result
	}
	b, ok := byID[s.ID]
	if !ok {
		if candidates := byFamily[s.Document.Family]; len(candidates) > 0 {
			return unscored(BrowserBaselineCohortChanged, "the baseline measured this document with a different "+cohortDifference(s.Document.Fields, candidates))
		}
		return unscored(BrowserBaselineNoBaselineCohort, "the baseline run has no document of this measurement tuple")
	}
	if b.Document.Incomplete {
		result.Caveats = append(result.Caveats, BrowserBaselineBaselineSourceLoss)
	}
	b.Points = sortedPoints(b.Points)
	baselineSpans := segments(b, baselineLevels)
	var values []float64
	matched := false
	for index, bl := range baselineLevels {
		if sameLoad(level, bl, params.LevelTolerance) {
			matched = true
			v, n := spanValues(baselineSpans[index])
			values = append(values, v...)
			result.BaselineSamples += n
		}
	}
	result.BaselineBuckets = len(values)
	if !matched {
		return unscored(BrowserBaselineNoSameLoadWindow, "the baseline run has no steady level at this load")
	}
	if len(values) < params.MinBaselineBuckets {
		return unscored(BrowserBaselineInsufficient, fmt.Sprintf("%d of the %d valid baseline buckets needed", len(values), params.MinBaselineBuckets))
	}
	score, ok := analysis.ThreeSigmaScore(result.Current, values, params.Sigma)
	if !ok {
		return unscored(BrowserBaselineZeroVariance, "the baseline buckets have no spread")
	}
	floor, ok := browserEffectFloor(result.Metric, s.Unit, score.BaselineMean, params)
	if !ok {
		return unscored(BrowserBaselineNoEffectFloor, "no practical effect floor is defined for "+result.Metric)
	}
	result.BaselineMean, result.BaselineStdDev, result.Z = score.BaselineMean, score.BaselineStdDev, score.ZScore
	result.ExpectedLow = math.Max(0, score.BaselineMean-params.Sigma*score.BaselineStdDev)
	result.ExpectedHigh = score.BaselineMean + params.Sigma*score.BaselineStdDev
	result.Effect, result.EffectFloor = result.Current-score.BaselineMean, floor
	result.Evidence = append(result.Evidence, b.Evidence)
	result.State = BrowserBaselineWithinRange
	if score.Exceeded && math.Abs(result.Effect) >= floor {
		result.State, result.Direction = BrowserBaselineAnomaly, DirectionIncrease
		if result.Effect < 0 {
			result.Direction = DirectionDecrease
		}
	}
	return result
}

// browserEffectFloor is the smallest change a cohort's anomaly must show:
// max(TimingFloorMs, TimingFloorFraction of the baseline mean) for a timing,
// CLSFloor for layout shift, a catalog floor otherwise.
func browserEffectFloor(metric, unit string, mean float64, params BrowserBaselineParams) (float64, bool) {
	if floor, ok := params.MetricFloors[metric]; ok && floor > 0 {
		return floor, true
	}
	switch {
	case metric == "cls":
		return params.CLSFloor, params.CLSFloor > 0
	case strings.EqualFold(unit, "ms"):
		return math.Max(params.TimingFloorMs, params.TimingFloorFraction*math.Abs(mean)), true
	}
	return 0, false
}

// cohortDifference names the runtime cohort fields in which the run's cohort
// differs from every baseline cohort of the same measurement.
func cohortDifference(fields map[string]string, candidates []Series) string {
	differing := map[string]bool{}
	for _, c := range candidates {
		for name, value := range fields {
			if c.Document.Fields[name] != value {
				differing[name] = true
			}
		}
		for name := range c.Document.Fields {
			if _, ok := fields[name]; !ok {
				differing[name] = true
			}
		}
	}
	names := make([]string, 0, len(differing))
	for name := range differing {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > browserBaselineUnscoredDetailLimit {
		names = names[:browserBaselineUnscoredDetailLimit]
	}
	if len(names) == 0 {
		return "runtime cohort"
	}
	return strings.Join(names, ", ")
}
