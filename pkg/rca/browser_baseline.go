package rca

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

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
	BrowserBaselineNoBaselineRun         = "no_qualified_baseline"
	BrowserBaselineNoBaselineCohort      = "no_baseline_cohort"
	BrowserBaselineCohortChanged         = "cohort_changed"
	BrowserBaselineCadenceChanged        = "cadence_changed"
	BrowserBaselineCadenceUnavailable    = "cadence_unavailable"
	BrowserBaselineBucketGridInvalid     = "bucket_grid_invalid"
	BrowserBaselineSourceWindowInvalid   = "source_window_invalid"
	BrowserBaselineBaselineWindowInvalid = "baseline_source_window_invalid"
	BrowserBaselineNoSameLoadWindow      = "no_same_load_window"
	BrowserBaselineInsufficient          = "insufficient_baseline"
	BrowserBaselineZeroVariance          = "zero_variance"
	BrowserBaselineNoEffectFloor         = "no_effect_floor"
	BrowserBaselineOutsideSteadyLoad     = "outside_steady_load"
	BrowserBaselineSourceLoss            = "source_loss"
	BrowserBaselineBaselineSourceLoss    = "baseline_source_loss"
	BrowserBaselineResultsTruncated      = "browser_baseline_truncated"
	BrowserBaselineLevelsUnavailable     = "load_levels_unavailable"
	// BrowserBaselineEvidenceUnavailable: the baseline's document evidence
	// was not fully read, so a missing cohort may exist there.
	BrowserBaselineEvidenceUnavailable = "baseline_evidence_unavailable"
	// BrowserBaselineRunEvidenceIncomplete and ...Unavailable: the run's own
	// document evidence was not fully read, or could not be read, so a
	// cohort it left out has no result.
	BrowserBaselineRunEvidenceIncomplete  = "run_evidence_incomplete"
	BrowserBaselineRunEvidenceUnavailable = "run_evidence_unavailable"
	// The run's pinned browser policy turned baseline comparison off, pins
	// another baseline run than the analysis compares with, or could not
	// be read (the default floors then apply).
	BrowserBaselinePolicyOff           = "policy_baseline_off"
	BrowserBaselinePolicyPinnedRun     = "policy_pinned_run"
	BrowserBaselinePolicyUnavailable   = "policy_unavailable"
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
	// CLSFloor. A MetricFloors entry can only raise these floors; any
	// other metric requires its own positive MetricFloors entry.
	TimingFloorMs       float64            `json:"timing_floor_ms"`
	TimingFloorFraction float64            `json:"timing_floor_fraction"`
	CLSFloor            float64            `json:"cls_floor"`
	MetricFloors        map[string]float64 `json:"metric_floors,omitempty"`
	// LevelTolerance is how far two loads may differ and still be the same
	// level. MaxResults bounds the output; anomalies are kept first.
	LevelTolerance float64 `json:"level_tolerance"`
	MaxResults     int     `json:"max_results"`
}

// BrowserBaselinePolicy is what a run's pinned browser_policy sets for its
// browser baselines: effect floors, which only raise the defaults, and the
// baseline mode (none, latest_compatible, or pinned_run with RunID).
// Unavailable says why the pinned policy could not be used.
type BrowserBaselinePolicy struct {
	MetricFloors   map[string]float64 `json:"metric_floors,omitempty"`
	Ref            string             `json:"ref"`
	TimingMs       *float64           `json:"timing_ms,omitempty"`
	TimingRelative *float64           `json:"timing_relative,omitempty"`
	CLS            *float64           `json:"cls,omitempty"`
	Mode           string             `json:"mode,omitempty"`
	RunID          string             `json:"run_id,omitempty"`
	Unavailable    string             `json:"unavailable,omitempty"`
}

// BrowserBaselineParamsFor are the defaults with a pinned policy's effect
// floors applied; a floor can only raise its default.
func BrowserBaselineParamsFor(policy *BrowserBaselinePolicy) BrowserBaselineParams {
	params := DefaultBrowserBaselineParams
	if params.MetricFloors != nil {
		original := params.MetricFloors
		params.MetricFloors = make(map[string]float64, len(original))
		for metric, value := range original {
			params.MetricFloors[metric] = value
		}
	}
	if policy == nil || policy.Unavailable != "" {
		return params
	}
	if len(policy.MetricFloors) > 0 {
		if params.MetricFloors == nil {
			params.MetricFloors = make(map[string]float64, len(policy.MetricFloors))
		}
		for metric, value := range policy.MetricFloors {
			if value <= 0 || math.IsNaN(value) || math.IsInf(value, 0) {
				params.MetricFloors[metric] = value
				continue
			}
			params.MetricFloors[metric] = max(params.MetricFloors[metric], value)
		}
	}
	if policy.TimingMs != nil {
		params.TimingFloorMs = max(params.TimingFloorMs, *policy.TimingMs)
	}
	if policy.TimingRelative != nil {
		params.TimingFloorFraction = max(params.TimingFloorFraction, *policy.TimingRelative)
	}
	if policy.CLS != nil {
		params.CLSFloor = max(params.CLSFloor, *policy.CLS)
	}
	return params
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
	// These states are set by the trusted archive adapter after validating
	// source-bound evidence for the entire retained population. Zero values,
	// local execution, and absence of reported loss are not positive proof.
	TimelineQualification       string        `json:"timeline_qualification,omitempty"`
	ClockQualification          string        `json:"clock_qualification"`
	ClockUnavailableReason      string        `json:"clock_unavailable_reason,omitempty"`
	ClockUncertainty            time.Duration `json:"clock_uncertainty,omitempty"`
	SourceCoverageQualification string        `json:"source_coverage_qualification"`
	Metric                      string        `json:"metric"`
	// Group is the scenario group that measured the document.
	Group      string            `json:"group,omitempty"`
	Family     string            `json:"family"`
	Fields     map[string]string `json:"fields,omitempty"`
	Incomplete bool              `json:"incomplete,omitempty"`
	// Windows retains the full measurement envelope of each populated bucket.
	// Keys are bucket starts in UTC; missing windows do not prove steady load.
	Windows map[time.Time]DocumentWindow `json:"windows,omitempty"`
}

// DocumentWindow is the envelope of all final measurements contributing to a
// bucket. Invalid preserves any malformed constituent instead of hiding it in
// the envelope. These timestamps do not attest source-clock synchronization.
type DocumentWindow struct {
	// MonotonicVUsers is set only when every constituent measurement fits one
	// positively recorded executor span at this load. It supplements retained
	// raw timestamps, which are never treated as clock calibration.
	MonotonicSpanID   string `json:"monotonic_span_id,omitempty"`
	MonotonicVUsers   int    `json:"monotonic_vusers,omitempty"`
	MonotonicVerified bool   `json:"monotonic_verified,omitempty"`

	Start         time.Time `json:"start"`
	End           time.Time `json:"end"`
	Invalid       bool      `json:"invalid,omitempty"`
	InvalidReason string    `json:"invalid_reason,omitempty"`
}

// BrowserBaseline is the detector's output for one assessment.
type BrowserBaseline struct {
	Version string                `json:"version"`
	Status  string                `json:"status"`
	Params  BrowserBaselineParams `json:"params"`
	// Policy names the run's pinned browser policy (id@revision) when its
	// floors and baseline mode applied; one that could not be used is named
	// in a limitation instead.
	Policy string `json:"policy,omitempty"`
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
	// LevelStart tells apart two steady levels at one load (a profile that
	// returns to a load).
	LevelStart time.Time `json:"level_start"`
	State      string    `json:"state"`
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
	// ExpectedLow and ExpectedHigh are the baseline mean ± the larger of
	// Sigma standard deviations and the effect floor (never below zero): a
	// value inside is within range, one outside an anomaly. Unscored
	// results have none.
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
		// Evidence the run kept but that could not be read, or that the
		// series budget left out entirely, is not absence.
		switch {
		case input.DocumentsUnreadable:
			return &BrowserBaseline{Version: BrowserBaselineVersion, Status: DetectionUnavailable, Params: params, Results: []BrowserBaselineResult{},
				Limitations: []Limitation{{Code: BrowserBaselineRunEvidenceUnavailable, Detail: "the run's browser document evidence could not be read, so no cohort was compared"}}}
		case input.DocumentsIncomplete:
			return &BrowserBaseline{Version: BrowserBaselineVersion, Status: DetectionUnavailable, Params: params, Results: []BrowserBaselineResult{},
				Limitations: []Limitation{{Code: BrowserBaselineRunEvidenceIncomplete, Detail: "none of the run's browser document series was read (the series budget or dropped observations left them out), so no cohort was compared"}}}
		}
		return nil
	}
	out := &BrowserBaseline{Version: BrowserBaselineVersion, Status: DetectionComplete, Params: params, Results: []BrowserBaselineResult{}, Limitations: []Limitation{}}
	if policy := input.BrowserPolicy; policy != nil {
		// The section names the policy only when its floors and mode applied.
		if policy.Unavailable == "" {
			out.Policy = policy.Ref
		}
		switch {
		case policy.Unavailable != "":
			out.Status = DetectionPartial
			out.Limitations = append(out.Limitations, Limitation{Code: BrowserBaselinePolicyUnavailable, Detail: "the run's pinned browser policy " + policy.Ref + " could not be used (" + policy.Unavailable + "), so the default effect floors applied"})
		case policy.Mode == "none":
			out.Status = DetectionUnavailable
			out.Limitations = append(out.Limitations, Limitation{Code: BrowserBaselinePolicyOff, Detail: "the run's pinned browser policy " + policy.Ref + " turns baseline comparison off"})
			return out
		case policy.Mode == "pinned_run" && input.Baseline != nil && input.Baseline.TestID != policy.RunID:
			// Without a usable baseline the comparison is unavailable for that
			// reason (below), whichever run was named.
			out.Status = DetectionUnavailable
			out.Limitations = append(out.Limitations, Limitation{Code: BrowserBaselinePolicyPinnedRun, Detail: "the run's pinned browser policy " + policy.Ref + " compares with run " + policy.RunID + ", but this analysis compares with run " + input.Baseline.TestID + ", so no cohort was compared"})
			return out
		}
	}
	if input.DocumentsIncomplete {
		out.Status = DetectionPartial
		out.Limitations = append(out.Limitations, Limitation{Code: BrowserBaselineRunEvidenceIncomplete, Detail: "the run's browser document evidence was not fully read; a cohort it left out has no result"})
	}
	seenQualification := map[string]bool{}
	addQualification := func(series []Series, baseline bool) {
		for _, s := range series {
			if s.Kind != SeriesBrowserDocument {
				continue
			}
			for _, limitation := range browserDocumentQualificationLimitations(s.Document, baseline) {
				if !seenQualification[limitation.Code] {
					seenQualification[limitation.Code] = true
					out.Limitations = append(out.Limitations, limitation)
					out.Status = DetectionPartial
				}
			}
		}
	}
	addQualification(documents, false)
	if input.Baseline != nil {
		addQualification(input.Baseline.Series, true)
	}
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
	out.Baseline = BaselineRef{TestID: baseline.TestID, Qualification: baseline.Qualification, Reasons: slices.Clone(baseline.QualificationReasons)}
	// The cohort key stands in for authored identity by the scenario group,
	// which only holds while the group ran the same script and the run the
	// same configuration: a configuration or workload change refuses every
	// cohort, a script change only its group's.
	authored := browserBaselineAuthoredChanges(baseline.QualificationReasons, baseline.ScriptChangedGroups == nil)
	if authored != "" {
		// Every cohort below is unscored for this reason; the section says
		// so once, so a summary of its results does not read as a clean
		// comparison.
		out.Status = DetectionPartial
		out.Limitations = append(out.Limitations, Limitation{Code: BrowserBaselineCohortChanged, Detail: "the baseline's configuration or workload changed or was not recorded (" + authored + "), so no cohort was scored against it"})
	}
	if baseline.DocumentsIncomplete {
		out.Status = DetectionPartial
		out.Limitations = append(out.Limitations, Limitation{Code: BrowserBaselineEvidenceUnavailable, Detail: "the baseline's browser document evidence was not fully read; a cohort missing from it is unscored as unavailable, not as absent"})
	}
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
			result := compareBrowserCohort(s, level, current, samples, byID, byFamily, baselineLevels, out.Baseline, baseline.DocumentsIncomplete, params)
			switch {
			case authored != "":
				result = browserBaselineUnscoredResult(result, BrowserBaselineCohortChanged, "the baseline's configuration or workload changed or was not recorded ("+authored+"), so its documents are not the same authored pages")
			case baseline.ScriptChangedGroups[s.Document.Group]:
				result = browserBaselineUnscoredResult(result, BrowserBaselineCohortChanged, fmt.Sprintf("group %q ran a different or unrecorded script in the baseline, so its documents are not the same authored pages", s.Document.Group))
			}
			if slices.Contains(result.Caveats, BrowserBaselineSourceLoss) || slices.Contains(result.Caveats, BrowserBaselineBaselineSourceLoss) {
				out.Status = DetectionPartial
			}
			out.Results = append(out.Results, result)
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
		if browserBaselineStateRank(a.State) != browserBaselineStateRank(b.State) {
			return browserBaselineStateRank(a.State) < browserBaselineStateRank(b.State)
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

// browserBaselineAuthoredChanges lists the qualification reasons that say
// every cohort's authored pages may differ: configuration and workload ones,
// and script ones when the changed groups are not known.
func browserBaselineAuthoredChanges(reasons []string, scriptGroupsUnknown bool) string {
	var changed []string
	for _, reason := range reasons {
		switch reason {
		case ReasonConfigChanged, ReasonConfigUnrecorded, ReasonWorkloadUnrecorded:
			changed = append(changed, reason)
		case ReasonScriptChanged, ReasonScriptUnrecorded:
			if scriptGroupsUnknown {
				changed = append(changed, reason)
			}
		}
	}
	return strings.Join(changed, ", ")
}

// browserBaselineUnscoredResult makes a result unscored, without a range or
// the baseline's evidence.
func browserBaselineUnscoredResult(result BrowserBaselineResult, reason, detail string) BrowserBaselineResult {
	result.State, result.Reason, result.Detail, result.Direction = BrowserBaselineUnscored, reason, detail, ""
	result.ExpectedLow, result.ExpectedHigh, result.Z, result.Effect, result.EffectFloor, result.BaselineMean, result.BaselineStdDev = 0, 0, 0, 0, 0, 0, 0
	if len(result.Evidence) > 1 {
		result.Evidence = result.Evidence[:1]
	}
	return result
}

func browserBaselineStateRank(state string) int {
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
			samples += int64(p.Count)
		}
	}
	return values, samples
}

// browserBucketGridValid checks sorted retained buckets without filling gaps or
// collapsing duplicate publications into extra statistical samples. Each run may
// have its own grid origin; equal durations do not require equal wall-clock starts.
func browserBucketGridValid(points []SeriesPoint, bucket time.Duration) bool {
	if bucket <= 0 {
		return false
	}
	for i, point := range points {
		if point.At.IsZero() {
			return false
		}
		if i == 0 {
			continue
		}
		previous := points[i-1].At
		gap := point.At.Sub(previous)
		// Sub saturates for ranges beyond time.Duration. Do not let that
		// saturation accidentally establish a cadence for corrupt timestamps.
		if gap <= 0 || gap%bucket != 0 || !previous.Add(gap).Equal(point.At) {
			return false
		}
	}
	return true
}

// browserSourceWindowsWithin requires the whole measurement population, not
// merely the bucket where its final publication landed, to belong to this span.
func browserSourceWindowsWithin(s Series, points []SeriesPoint, level LoadLevel) bool {
	spanID := ""
	proofMode := ""
	for _, point := range points {
		if !usable(point) {
			continue
		}
		w, ok := s.Document.Windows[point.At.UTC()]
		if !ok || w.Invalid {
			return false
		}
		if w.MonotonicVerified {
			if proofMode == "raw" || spanID != "" && spanID != w.MonotonicSpanID {
				return false
			}
			proofMode = "monotonic"
			spanID = w.MonotonicSpanID
			if w.MonotonicSpanID == "" || w.MonotonicVUsers <= 0 || float64(w.MonotonicVUsers) != level.VUsers || level.Rate != 0 {
				return false
			}
			// Projected labels place buckets but do not establish load eligibility.
			// Every constituent belongs to this independently proven monotonic span.
			continue
		}
		if proofMode == "monotonic" {
			return false
		}
		proofMode = "raw"
		if w.Start.IsZero() || w.End.IsZero() || w.End.Before(w.Start) || w.Start.Before(level.Start) || w.End.After(level.End) || w.End.Before(point.At) || !w.End.Before(point.At.Add(s.Bucket)) {
			return false
		}

	}
	return true
}

func compareBrowserCohort(s Series, level LoadLevel, current []float64, samples int64, byID map[string]Series, byFamily map[string][]Series, baselineLevels []LoadLevel, ref BaselineRef, baselineIncomplete bool, params BrowserBaselineParams) BrowserBaselineResult {
	sorted := slices.Clone(current)
	slices.Sort(sorted)
	result := BrowserBaselineResult{Series: s.ID, Metric: s.Document.Metric, Unit: s.Unit, Level: level.Label(), LevelStart: level.Start.UTC(), Current: medianSorted(sorted), CurrentBuckets: len(current), CurrentSamples: samples,
		Baseline: ref, Evidence: []EvidenceRef{s.Evidence}}
	result.ID = stableID("browser_baseline", BrowserBaselineVersion, s.ID, result.Level, level.Start.UTC().Format(time.RFC3339Nano), ref.TestID)
	if s.Document.Incomplete {
		result.Caveats = append(result.Caveats, BrowserBaselineSourceLoss)
	}
	unscored := func(reason, detail string) BrowserBaselineResult {
		return browserBaselineUnscoredResult(result, reason, detail)
	}
	b, ok := byID[s.ID]
	if !ok && baselineIncomplete {
		return unscored(BrowserBaselineEvidenceUnavailable, "the baseline's browser document evidence was not fully read, so its matching cohort may be missing")
	}
	if !ok {
		if candidates := byFamily[s.Document.Family]; len(candidates) > 0 {
			return unscored(BrowserBaselineCohortChanged, "the baseline measured this document with a different "+cohortDifference(s.Document.Fields, candidates))
		}
		return unscored(BrowserBaselineNoBaselineCohort, "the baseline run has no document of this measurement tuple")
	}
	if b.Document.Incomplete {
		result.Caveats = append(result.Caveats, BrowserBaselineBaselineSourceLoss)
	}
	// A series ID is a lookup key, not proof of the measurement contract.
	// Retained/custom inputs must still describe the same tuple and runtime.
	if s.Document.Metric != b.Document.Metric || s.Document.Family != b.Document.Family || s.Document.Group != b.Document.Group || s.Unit != b.Unit || !maps.Equal(s.Document.Fields, b.Document.Fields) {
		return unscored(BrowserBaselineCohortChanged, "the matching series ID describes a different measurement tuple or runtime cohort")
	}
	// v1 gives each interval bucket equal weight. Comparing different bucket
	// durations changes the population and cannot establish a like-for-like
	// baseline, even when both sides retained enough buckets.
	if s.Observation != ObservationInterval || b.Observation != ObservationInterval || s.Bucket <= 0 || b.Bucket <= 0 {
		return unscored(BrowserBaselineCadenceUnavailable, "both document series need a recorded positive interval bucket duration")
	}
	if s.Bucket != b.Bucket {
		return unscored(BrowserBaselineCadenceChanged, "the run and baseline document buckets have different durations")
	}
	b.Points = sortedPoints(b.Points)
	if !browserBucketGridValid(s.Points, s.Bucket) || !browserBucketGridValid(b.Points, b.Bucket) {
		return unscored(BrowserBaselineBucketGridInvalid, "document buckets need unique recorded timestamps on each run's declared cadence; gaps remain missing")
	}
	baselineSpans := segments(b, baselineLevels)
	var values []float64
	matched := false
	baselineWindowsValid := true
	for index, bl := range baselineLevels {
		if sameLoad(level, bl, params.LevelTolerance) {
			matched = true
			baselineWindowsValid = baselineWindowsValid && browserSourceWindowsWithin(b, baselineSpans[index], bl)
			v, n := spanValues(baselineSpans[index])
			values = append(values, v...)
			result.BaselineSamples += n
		}
	}
	result.BaselineBuckets = len(values)
	// Retained bucket counts are useful provenance, but a known loss in
	// either population cannot qualify an anomaly or a within-range verdict.
	if s.Document.Incomplete {
		return unscored(BrowserBaselineSourceLoss, "the run's document cohort lost observations, so the retained buckets cannot qualify this comparison")
	}
	if b.Document.Incomplete {
		return unscored(BrowserBaselineBaselineSourceLoss, "the baseline's document cohort lost observations, so the retained buckets cannot qualify this comparison")
	}
	if !matched {
		return unscored(BrowserBaselineNoSameLoadWindow, "the baseline run has no steady level at this load")
	}
	if !browserSourceWindowsWithin(s, segments(s, []LoadLevel{level})[0], level) {
		return unscored(BrowserBaselineSourceWindowInvalid, "the run's full document measurement windows are missing, invalid or cross the steady-load boundary")
	}
	if !baselineWindowsValid {
		return unscored(BrowserBaselineBaselineWindowInvalid, "the baseline's full document measurement windows are missing, invalid or cross a matching steady-load boundary")
	}
	if reason, detail := browserDocumentQualification(s.Document, false); reason != "" {
		return unscored(reason, detail)
	}
	if reason, detail := browserDocumentQualification(b.Document, true); reason != "" {
		return unscored(reason, detail)
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
	// An anomaly needs both |z| >= Sigma and the effect floor, so the
	// published range is the wider of the two: within it is within range.
	band := math.Max(params.Sigma*score.BaselineStdDev, floor)
	result.ExpectedLow = math.Max(0, score.BaselineMean-band)
	result.ExpectedHigh = score.BaselineMean + band
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
// CLSFloor for layout shift, a catalog floor otherwise. Per-metric floors
// can only raise the applicable timing/CLS floor. Invalid supplied floors
// cannot qualify a comparison.
func browserEffectFloor(metric, unit string, mean float64, params BrowserBaselineParams) (float64, bool) {
	floor, configured := params.MetricFloors[metric]
	if configured && (floor <= 0 || math.IsNaN(floor) || math.IsInf(floor, 0)) {
		return 0, false
	}
	switch {
	case metric == "cls":
		if math.IsNaN(params.CLSFloor) || math.IsInf(params.CLSFloor, 0) {
			return 0, false
		}
		floor = math.Max(floor, params.CLSFloor)
	case strings.EqualFold(unit, "ms"):
		if math.IsNaN(mean) || math.IsInf(mean, 0) || math.IsNaN(params.TimingFloorMs) || math.IsInf(params.TimingFloorMs, 0) || math.IsNaN(params.TimingFloorFraction) || math.IsInf(params.TimingFloorFraction, 0) {
			return 0, false
		}
		absolute := params.TimingFloorMs
		relative := params.TimingFloorFraction * math.Abs(mean)
		floor = math.Max(floor, math.Max(absolute, relative))
	}
	return floor, floor > 0 && !math.IsNaN(floor) && !math.IsInf(floor, 0)
}

// cohortDifference names the runtime cohort fields in which the run's cohort
// differs from the closest baseline cohort of the same measurement (the one
// differing in the fewest fields).
func cohortDifference(fields map[string]string, candidates []Series) string {
	var names []string
	for index, c := range candidates {
		var differing []string
		for name, value := range fields {
			if c.Document.Fields[name] != value {
				differing = append(differing, name)
			}
		}
		for name := range c.Document.Fields {
			if _, ok := fields[name]; !ok {
				differing = append(differing, name)
			}
		}
		sort.Strings(differing)
		if index == 0 || len(differing) < len(names) {
			names = differing
		}
	}
	if len(names) > browserBaselineUnscoredDetailLimit {
		names = names[:browserBaselineUnscoredDetailLimit]
	}
	if len(names) == 0 {
		return "runtime cohort"
	}
	return strings.Join(names, ", ")
}
