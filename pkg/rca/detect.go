package rca

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Series kinds. Transaction series come from the archived interval
// distributions; provider series from archived monitor points; imported
// series from analysis datasets associated with the run; browser document
// series from the run's final document measurements (a document metric's
// p75 per measurement tuple).
const (
	SeriesLatency         = "latency"
	SeriesErrorRate       = "error_rate"
	SeriesThroughput      = "throughput"
	SeriesProvider        = "provider"
	SeriesImported        = "imported"
	SeriesBrowserDocument = "browser_document"
)

// SeriesJointPrefix starts the ID of a series measured on an exact action
// dimension tuple (joint interval evidence), never one synthesized from
// marginals.
const SeriesJointPrefix = "action_dimensions/"

// Series observations. Interval and snapshot values can be compared bucket to
// bucket; cumulative values accumulate from an origin and cannot.
const (
	ObservationInterval   = "interval"
	ObservationSnapshot   = "snapshot"
	ObservationCumulative = "cumulative"
)

// Anomaly scopes: a change over time within one load level of the run, or a
// difference from the baseline run at the same level.
const (
	ScopeTime = "time"
	ScopeLoad = "load"
)

// Anomaly directions.
const (
	DirectionIncrease = "increase"
	DirectionDecrease = "decrease"
)

// BaselineSameRun qualifies a time-scoped anomaly: it was measured against
// earlier intervals at the same load in this run.
const BaselineSameRun = "same_run"

// Detection statuses.
const (
	DetectionComplete    = "complete"
	DetectionPartial     = "partial"
	DetectionSkipped     = "skipped"
	DetectionUnavailable = "unavailable"
)

// Detection limitations.
const (
	LimitationCumulativeExcluded    = "cumulative_series_excluded"
	LimitationLoadLevelsUnavailable = "load_levels_unavailable"
	LimitationInsufficientBaseline  = "insufficient_baseline"
	LimitationAnomaliesTruncated    = "anomalies_truncated"
	LimitationSpikesTruncated       = "spikes_truncated"
	LimitationBaselineUnmatched     = "baseline_series_unmatched"
)

// SeriesPoint is one bucket of a series. At is the bucket start for interval
// series and the evaluation time for snapshot series. An invalid point is a
// gap: the bucket was expected but held no usable value.
type SeriesPoint struct {
	At    time.Time
	Value float64
	Valid bool
	// Count is how many observations a valid value summarizes, when known
	// (browser document buckets); 0 otherwise. It is an int32 so it packs
	// beside Valid and every point stays 40 bytes.
	Count int32
}

// Series is one measured quantity of the run, in time order.
type Series struct {
	ID          string
	Kind        string
	Unit        string
	Observation string
	Bucket      time.Duration
	// ClockUncertainty widens every onset found in the series.
	ClockUncertainty time.Duration
	// Evidence names where the series came from; anomalies cite it with
	// their window.
	Evidence EvidenceRef
	Points   []SeriesPoint
	// Measures is the evidence kind the series measures (signatures.Kind*),
	// or "" when none applies; transaction series default by Kind.
	Measures string
	// Nodes are the graph nodes the series measures. Transaction, action
	// and dependency series default to their own node.
	Nodes []string
	// Identity is the versioned measurement identity (analysis
	// SeriesIdentity SHA-256) of what the series measures, when known.
	// Equal identities name the same measurement; they never by themselves
	// authorize a baseline comparison.
	Identity string
	// Document describes a browser document series' cohort (browser
	// baseline, browser_baseline.v1); nil for every other series.
	Document *DocumentCohort
}

// External reports whether a series measures the system under test from
// outside the run's own results: a provider or an imported dataset.
func (s Series) External() bool {
	return s.Kind == SeriesProvider || s.Kind == SeriesImported
}

// LoadLevel is one steady span of offered load, after its settle period.
// VUsers is the closed-model load, Rate the open-model target arrival rate;
// a mixed run sets both.
type LoadLevel struct {
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	VUsers float64   `json:"vusers,omitempty"`
	Rate   float64   `json:"rate,omitempty"`
}

// Label names the level's load.
func (l LoadLevel) Label() string {
	var parts []string
	if l.VUsers > 0 {
		parts = append(parts, "vusers="+strconv.FormatFloat(math.Round(l.VUsers*100)/100, 'f', -1, 64))
	}
	if l.Rate > 0 {
		parts = append(parts, "rate="+strconv.FormatFloat(math.Round(l.Rate*100)/100, 'f', -1, 64)+"/s")
	}
	if len(parts) == 0 {
		return "unmeasured"
	}
	return strings.Join(parts, ",")
}

// sameLoad reports whether two levels offer the same load within tolerance.
func sameLoad(a, b LoadLevel, tolerance float64) bool {
	near := func(x, y float64) bool {
		if x <= 0 || y <= 0 {
			return x <= 0 && y <= 0
		}
		return math.Abs(x-y) <= math.Max(x, y)*tolerance+1e-9
	}
	return near(a.VUsers, b.VUsers) && near(a.Rate, b.Rate)
}

// DetectionBaseline is a workload-qualified baseline run's levels and series.
type DetectionBaseline struct {
	TestID        string
	Qualification string
	// QualificationReasons say why a partial baseline is partial.
	QualificationReasons []string
	// ScriptChangedGroups are the groups whose script differs from the run's
	// or is unrecorded; nil when unknown (every script reason then applies
	// to the whole run).
	ScriptChangedGroups map[string]bool
	// DocumentsIncomplete says the baseline's browser document evidence
	// was not fully read (unreadable, or cut by the series budget), so a
	// cohort missing from it may exist.
	DocumentsIncomplete bool
	Levels              []LoadLevel
	Series              []Series
}

// DetectionInput is the frozen evidence onset detection reads.
type DetectionInput struct {
	Levels   []LoadLevel
	Series   []Series
	Baseline *DetectionBaseline
	// Limitations the caller found while building the series.
	Limitations []Limitation
	// DocumentsIncomplete says the run's own browser document evidence was
	// not fully read (dropped observations or series the budget left out);
	// DocumentsUnreadable that it could not be read at all.
	DocumentsIncomplete bool
	DocumentsUnreadable bool
	// BrowserPolicy is what the run's pinned browser_policy sets for its
	// browser baselines; nil when the run pinned none.
	BrowserPolicy *BrowserBaselinePolicy
}

// JudgedSeries records, per scope, the series detection compared with a
// baseline. A series it never judged says nothing by staying quiet.
type JudgedSeries map[string]map[string]bool

// Judged reports whether detection judged a series in a scope.
func (j JudgedSeries) Judged(scope, series string) bool { return j[scope][series] }

func (j JudgedSeries) mark(scope, series string) {
	if j[scope] == nil {
		j[scope] = map[string]bool{}
	}
	j[scope][series] = true
}

// Detection summarizes how onset detection ran.
type Detection struct {
	Status          string       `json:"status"`
	ParamsVersion   string       `json:"params_version"`
	Params          DetectParams `json:"params"`
	SeriesEvaluated int          `json:"series_evaluated"`
	SeriesExcluded  int          `json:"series_excluded"`
	Levels          []LoadLevel  `json:"levels"`
	// Spikes are single-bucket excursions: annotated, never onsets.
	Spikes []Spike `json:"spikes"`
	// Judged is what detection compared with a baseline; it is not part of
	// the assessment.
	Judged JudgedSeries `json:"-"`
}

// Spike is a one-bucket excursion beyond the alarm, with in-band neighbours.
type Spike struct {
	Series string    `json:"series"`
	At     time.Time `json:"at"`
	Value  float64   `json:"value"`
	Z      float64   `json:"z"`
	Scope  string    `json:"scope"`
	Level  string    `json:"level"`
}

// Detect finds onsets in the run's series against both baselines: earlier
// intervals at the same load in this run, and the baseline run at the same
// level. It never fails; what it could not judge becomes a limitation.
func Detect(input DetectionInput, params DetectParams, budgeter *Budgeter) ([]Anomaly, Detection, []Limitation) {
	if budgeter == nil {
		budgeter = NewBudgeter(DefaultBudget, nil, nil)
	}
	detection := Detection{Status: DetectionComplete, ParamsVersion: params.Version, Params: params, Levels: sortedLevels(input.Levels), Spikes: []Spike{}}
	limitations := append([]Limitation{}, input.Limitations...)
	anomalies := []Anomaly{}
	if len(detection.Levels) == 0 {
		detection.Status = DetectionUnavailable
		limitations = append(limitations, Limitation{Code: LimitationLoadLevelsUnavailable, Detail: "no steady load level was measured, so no interval has a same-load baseline"})
		return anomalies, detection, limitations
	}
	series, excluded := comparableSeries(input.Series)
	detection.SeriesExcluded = excluded
	if excluded > 0 {
		limitations = append(limitations, Limitation{Code: LimitationCumulativeExcluded, Detail: fmt.Sprintf("%d cumulative series accumulate from an origin and are not compared bucket to bucket", excluded)})
	}
	if keep := budgeter.Series(len(series)); keep < len(series) {
		series, detection.Status = series[:keep], DetectionPartial
	}
	var baseline map[string]Series
	var baselineLevels []LoadLevel
	if b := input.Baseline; b != nil && b.Qualification != BaselineUnqualified {
		baselineLevels = sortedLevels(b.Levels)
		comparable, _ := comparableSeries(b.Series)
		baseline = make(map[string]Series, len(comparable))
		for _, s := range comparable {
			// Browser document series are compared with the baseline by
			// browser_baseline.v1, which refuses cohorts whose authored
			// pages changed; here they are judged within the run only, so
			// the two never disagree about one cohort.
			if s.Kind == SeriesBrowserDocument {
				continue
			}
			baseline[s.ID] = boundedSeries(s, budgeter)
		}
	}
	out := &detectOutput{params: params, judged: JudgedSeries{}, insufficient: map[string]bool{}}
	matched := 0
	qualificationSeen := map[string]bool{}
	for _, s := range series {
		if budgeter.Exhausted() {
			detection.Status = DetectionPartial
			break
		}
		if s.Kind == SeriesBrowserDocument {
			limits := browserDocumentQualificationLimitations(s.Document, false)
			if len(limits) > 0 {
				detection.Status = DetectionPartial
				detection.SeriesExcluded++
				for _, limit := range limits {
					if !qualificationSeen[limit.Code] {
						limitations = append(limitations, limit)
						qualificationSeen[limit.Code] = true
					}
				}
				continue
			}
		}
		offered := len(s.Points)
		if s = boundedSeries(s, budgeter); len(s.Points) < offered {
			detection.Status = DetectionPartial
		}
		detection.SeriesEvaluated++
		out.inRun(s, detection.Levels)
		if b, ok := baseline[s.ID]; ok {
			matched++
			out.againstBaseline(s, detection.Levels, b, baselineLevels, BaselineRef{TestID: input.Baseline.TestID, Qualification: input.Baseline.Qualification})
		}
	}
	if baseline != nil && matched == 0 && detection.SeriesEvaluated > 0 {
		limitations = append(limitations, Limitation{Code: LimitationBaselineUnmatched, Detail: "no series of the baseline run matches one of this run's, so no level was compared with the baseline"})
	}
	if len(out.insufficient) > 0 {
		limitations = append(limitations, Limitation{Code: LimitationInsufficientBaseline, Detail: fmt.Sprintf("%d series had fewer baseline buckets at some level than their resolution needs (%d buckets, or enough to span %s, at least %d)", len(out.insufficient), params.MinBaselineBuckets, params.MinBaselineSpan, params.MinBaselineBucketsFloor)})
	}
	anomalies, cut := keepAnomalies(out.anomalies, params.MaxAnomalies)
	if cut > 0 {
		limitations = append(limitations, Limitation{Code: LimitationAnomaliesTruncated, Detail: fmt.Sprintf("kept %d of %d anomalies, the largest first and joint action-dimension slices last", len(anomalies), len(anomalies)+cut)})
	}
	sort.SliceStable(anomalies, func(i, j int) bool {
		if !anomalies[i].Onset.Equal(anomalies[j].Onset) {
			return anomalies[i].Onset.Before(anomalies[j].Onset)
		}
		return anomalies[i].ID < anomalies[j].ID
	})
	spikes, cut := strongest(out.spikes, params.MaxSpikes, func(s Spike) float64 { return math.Abs(s.Z) }, func(s Spike) string { return s.Series + s.Scope + s.At.Format(time.RFC3339Nano) })
	if cut > 0 {
		limitations = append(limitations, Limitation{Code: LimitationSpikesTruncated, Detail: fmt.Sprintf("kept the %d largest of %d spikes", len(spikes), len(spikes)+cut)})
	}
	sort.SliceStable(spikes, func(i, j int) bool {
		if !spikes[i].At.Equal(spikes[j].At) {
			return spikes[i].At.Before(spikes[j].At)
		}
		return spikes[i].Series+spikes[i].Scope < spikes[j].Series+spikes[j].Scope
	})
	detection.Spikes, detection.Judged = spikes, out.judged
	if anomalies == nil {
		anomalies = []Anomaly{}
	}
	return anomalies, detection, limitations
}

func sortedLevels(levels []LoadLevel) []LoadLevel {
	out := make([]LoadLevel, 0, len(levels))
	for _, l := range levels {
		if l.End.After(l.Start) {
			out = append(out, LoadLevel{Start: l.Start.UTC(), End: l.End.UTC(), VUsers: l.VUsers, Rate: l.Rate})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// comparableSeries drops cumulative series and orders the rest by rank, so a
// series budget keeps what the run measured itself, then by ID.
func comparableSeries(series []Series) ([]Series, int) {
	out := make([]Series, 0, len(series))
	excluded := 0
	for _, s := range series {
		if s.Observation == ObservationCumulative {
			excluded++
			continue
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if ri, rj := seriesRank(out[i]), seriesRank(out[j]); ri != rj {
			return ri < rj
		}
		return out[i].ID < out[j].ID
	})
	return out, excluded
}

// seriesRank orders series for the budget: transactions and actions, then
// the run's own dependency timing, then providers and imported datasets,
// then joint action-dimension slices.
func seriesRank(s Series) int {
	switch {
	case strings.HasPrefix(s.ID, SeriesJointPrefix):
		return 3
	case s.External():
		return 2
	case strings.HasPrefix(s.ID, NodeDependency+"/"):
		return 1
	}
	return 0
}

// boundedSeries orders a series' points and keeps the earliest its budget
// allows, inferring a missing bucket width from the point spacing.
func boundedSeries(s Series, budgeter *Budgeter) Series {
	points := sortedPoints(s.Points)
	points = points[:budgeter.SeriesPoints(len(points))]
	if s.Bucket <= 0 {
		for i := 1; i < len(points); i++ {
			if step := points[i].At.Sub(points[i-1].At); step > 0 && (s.Bucket <= 0 || step < s.Bucket) {
				s.Bucket = step
			}
		}
	}
	s.Points = points
	return s
}

// sortedPoints returns the points in time order, copying only when the
// input is out of order; the input is never modified.
func sortedPoints(points []SeriesPoint) []SeriesPoint {
	less := func(i, j int) bool { return points[i].At.Before(points[j].At) }
	if sort.SliceIsSorted(points, less) {
		return points
	}
	out := append([]SeriesPoint(nil), points...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

// keepAnomalies keeps at most n anomalies, the largest first. Anomalies of
// series that can be a symptom or a cause come first: joint action-dimension
// slices (which no hypothesis uses) fill only the room left, so they never
// push out a cause.
func keepAnomalies(anomalies []Anomaly, n int) ([]Anomaly, int) {
	if n <= 0 || len(anomalies) <= n {
		return anomalies, 0
	}
	var primary, joint []Anomaly
	for _, anomaly := range anomalies {
		if strings.HasPrefix(anomaly.Series, SeriesJointPrefix) {
			joint = append(joint, anomaly)
		} else {
			primary = append(primary, anomaly)
		}
	}
	score := func(a Anomaly) float64 { return math.Abs(a.PeakZ) }
	key := func(a Anomaly) string { return a.ID }
	kept, _ := strongest(primary, n, score, key)
	if room := n - len(kept); room > 0 {
		fill, _ := strongest(joint, room, score, key)
		kept = append(kept, fill...)
	}
	return kept, len(anomalies) - len(kept)
}

// strongest keeps the n items with the largest score, ties broken by key.
func strongest[T any](items []T, n int, score func(T) float64, key func(T) string) ([]T, int) {
	if n <= 0 || len(items) <= n {
		return items, 0
	}
	sorted := append([]T(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if score(sorted[i]) != score(sorted[j]) {
			return score(sorted[i]) > score(sorted[j])
		}
		return key(sorted[i]) < key(sorted[j])
	})
	return sorted[:n], len(items) - n
}

type detectOutput struct {
	judged    JudgedSeries
	params    DetectParams
	anomalies []Anomaly
	spikes    []Spike
	// insufficient names the series that lacked baseline at some level, in
	// either scope, each once.
	insufficient map[string]bool
	// Scratch space for robust references, reused across series.
	sorted, deviations []float64
}

// reference is a robust location and scale.
type reference struct {
	median, scale float64
}

func (o *detectOutput) reference(n int, value func(int) float64, s Series) reference {
	sorted := o.sorted[:0]
	for i := 0; i < n; i++ {
		sorted = append(sorted, value(i))
	}
	sort.Float64s(sorted)
	median := medianSorted(sorted)
	deviations := o.deviations[:0]
	for _, v := range sorted {
		deviations = append(deviations, math.Abs(v-median))
	}
	sort.Float64s(deviations)
	o.sorted, o.deviations = sorted, deviations
	p := o.params
	scale := math.Max(p.MADScale*medianSorted(deviations), math.Max(p.RelativeFloor*math.Abs(median), unitFloor(s, p)))
	if scale <= 0 {
		scale = p.UnitFloor
	}
	return reference{median: median, scale: scale}
}

func medianSorted(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// unitFloor is the smallest scale a series' unit allows: 5 ms of latency, 0.5
// points of error rate, a tenth of a request per second.
func unitFloor(s Series, p DetectParams) float64 {
	switch s.Kind {
	case SeriesLatency:
		return p.LatencyFloorMs
	case SeriesErrorRate:
		return p.ErrorRateFloorPct
	case SeriesThroughput:
		return p.ThroughputFloor
	}
	switch strings.ToLower(strings.TrimSpace(s.Unit)) {
	case "ms", "millisecond", "milliseconds":
		return p.LatencyFloorMs
	case "s", "second", "seconds":
		return p.LatencyFloorMs / 1e3
	case "us", "µs", "microsecond", "microseconds":
		return p.LatencyFloorMs * 1e3
	case "ns", "nanosecond", "nanoseconds":
		return p.LatencyFloorMs * 1e6
	case "%", "percent":
		return p.ErrorRateFloorPct
	case "score":
		return p.ScoreFloor
	}
	return 0
}

// segments splits a series' time-ordered points into the level spans that
// contain them, without copying. Points outside every span (ramps, pauses)
// are never judged; a span's points still include gaps, which usable skips.
func segments(s Series, levels []LoadLevel) [][]SeriesPoint {
	out := make([][]SeriesPoint, len(levels))
	for i, level := range levels {
		first := sort.Search(len(s.Points), func(j int) bool { return !s.Points[j].At.Before(level.Start) })
		last := first
		for last < len(s.Points) && s.within(s.Points[last], level) {
			last++
		}
		out[i] = s.Points[first:last]
	}
	return out
}

// within reports whether a point's bucket lies inside a level span.
func (s Series) within(p SeriesPoint, level LoadLevel) bool {
	if s.Observation == ObservationSnapshot {
		return p.At.Before(level.End)
	}
	return !p.At.Add(s.Bucket).After(level.End.Add(time.Millisecond))
}

// usable reports whether a point holds a value to judge.
func usable(p SeriesPoint) bool {
	return p.Valid && !math.IsNaN(p.Value) && !math.IsInf(p.Value, 0)
}

// levelGroups groups level indexes that offer the same load, in order of
// their first span.
func levelGroups(levels []LoadLevel, tolerance float64) [][]int {
	var groups [][]int
	for i, l := range levels {
		placed := false
		for g := range groups {
			if sameLoad(levels[groups[g][0]], l, tolerance) {
				groups[g] = append(groups[g], i)
				placed = true
				break
			}
		}
		if !placed {
			groups = append(groups, []int{i})
		}
	}
	return groups
}

// inRun compares each bucket with earlier buckets at the same load in this
// run. The baseline is the first buckets of the level, so a slow drift is
// measured against where the level began.
func (o *detectOutput) inRun(s Series, levels []LoadLevel) {
	p := o.params
	minimum := p.minBaseline(s)
	spans := segments(s, levels)
	short := false
	for _, group := range levelGroups(levels, p.LevelTolerance) {
		var baseline []SeriesPoint
		var ref reference
		// Whether the level ever had enough baseline to judge; an onset can
		// later prune it below the minimum.
		judged := false
		rebuild := func() reference {
			return o.reference(len(baseline), func(i int) float64 { return baseline[i].Value }, s)
		}
		for _, index := range group {
			label := levels[index].Label()
			d := o.newDetector(s, ScopeTime, label, BaselineRef{Qualification: BaselineSameRun}, len(spans[index]))
			for _, point := range spans[index] {
				if !usable(point) {
					continue
				}
				if !judged {
					baseline = append(baseline, point)
					if len(baseline) == minimum {
						ref, judged = rebuild(), true
					}
					d.skip(point)
					continue
				}
				d.ref = ref
				z := (point.Value - ref.median) / ref.scale
				onset := d.observe(point, z)
				if !onset.IsZero() {
					// Buckets from the onset on were never baseline.
					kept := baseline[:0]
					for _, b := range baseline {
						if b.At.Before(onset) {
							kept = append(kept, b)
						}
					}
					baseline = kept
					if len(baseline) >= minimum {
						ref = rebuild()
					}
				}
				if len(baseline) < p.MaxBaselineBuckets && d.quiet() && math.Abs(z) <= 3 {
					baseline = append(baseline, point)
					ref = rebuild()
				}
			}
			d.finish()
		}
		if !judged {
			short = true
		}
	}
	if short {
		o.insufficient[s.ID] = true
	}
}

// againstBaseline compares each bucket of a level with the baseline run's
// buckets at the same load.
func (o *detectOutput) againstBaseline(s Series, levels []LoadLevel, b Series, baselineLevels []LoadLevel, ref BaselineRef) {
	p := o.params
	minimum := p.minBaseline(s)
	spans := segments(s, levels)
	baselineSpans := segments(b, baselineLevels)
	short := false
	for index, level := range levels {
		if len(spans[index]) == 0 {
			continue
		}
		var values []float64
		for bi, bl := range baselineLevels {
			if sameLoad(level, bl, p.LevelTolerance) {
				for _, point := range baselineSpans[bi] {
					if usable(point) {
						values = append(values, point.Value)
					}
				}
			}
		}
		if len(values) < minimum {
			short = true
			continue
		}
		d := o.newDetector(s, ScopeLoad, level.Label(), ref, len(spans[index]))
		d.ref = o.reference(len(values), func(i int) float64 { return values[i] }, s)
		for _, point := range spans[index] {
			if usable(point) {
				d.observe(point, (point.Value-d.ref.median)/d.ref.scale)
			}
		}
		d.finish()
	}
	if short {
		o.insufficient[s.ID] = true
	}
}

func pointValues(points []SeriesPoint) []float64 {
	out := make([]float64, len(points))
	for i, p := range points {
		out[i] = p.Value
	}
	return out
}

// detector runs a two-sided CUSUM over one level span of one series.
type detector struct {
	o        *detectOutput
	series   Series
	scope    string
	level    string
	baseline BaselineRef
	ref      reference
	points   []SeriesPoint
	zs       []float64
	last     time.Time
	dirs     [2]direction
}

// direction is one side of the CUSUM: index 0 watches increases, 1 decreases.
type direction struct {
	sign     float64
	sum      float64
	lastZero int
	// alarmRun counts consecutive buckets beyond K while the alarm is on.
	alarmRun int
	// A one-bucket excursion over the alarm, pending its next bucket.
	spike       int
	spikeSum    float64
	active      bool
	onset       int
	uncertainty time.Duration
	ref         reference
	peak        float64
	inBand      int
}

func (o *detectOutput) newDetector(s Series, scope, level string, baseline BaselineRef, capacity int) *detector {
	d := &detector{o: o, series: s, scope: scope, level: level, baseline: baseline, points: make([]SeriesPoint, 0, capacity), zs: make([]float64, 0, capacity)}
	d.dirs[0] = direction{sign: 1, lastZero: -1, spike: -1}
	d.dirs[1] = direction{sign: -1, lastZero: -1, spike: -1}
	return d
}

// skip notes a bucket that was not tested, so gaps are measured from it.
func (d *detector) skip(point SeriesPoint) { d.last = point.At }

// quiet reports whether neither side holds an anomaly, an alarm or a pending
// spike. Ordinary noise beyond K stays quiet, so the baseline keeps its spread.
func (d *detector) quiet() bool {
	for _, dir := range d.dirs {
		if dir.active || dir.sum >= d.o.params.H || dir.spike >= 0 {
			return false
		}
	}
	return true
}

// observe tests one bucket and returns the onset time when it confirms one.
func (d *detector) observe(point SeriesPoint, z float64) time.Time {
	p := d.o.params
	if !d.last.IsZero() && d.series.Bucket > 0 && point.At.Sub(d.last) > time.Duration(p.MaxGapBuckets+1)*d.series.Bucket {
		// Too long without data to call anything continuous.
		d.finish()
		d.points, d.zs = d.points[:0], d.zs[:0]
		for i := range d.dirs {
			d.dirs[i] = direction{sign: d.dirs[i].sign, lastZero: -1, spike: -1}
		}
	}
	d.last = point.At
	d.o.judged.mark(d.scope, d.series.ID)
	d.points = append(d.points, point)
	d.zs = append(d.zs, z)
	i := len(d.points) - 1
	var confirmed time.Time
	for k := range d.dirs {
		dir := &d.dirs[k]
		x := dir.sign * z
		previous := dir.sum
		dir.sum = math.Max(0, dir.sum+x-p.K)
		if dir.active {
			dir.peak = math.Max(dir.peak, x)
			if x <= p.K {
				dir.inBand++
			} else {
				dir.inBand = 0
			}
			if dir.inBand >= p.RecoveryBuckets {
				d.close(dir, i-p.RecoveryBuckets+1, true)
				dir.sum, dir.lastZero, dir.alarmRun = 0, i, 0
			}
			continue
		}
		if dir.spike >= 0 {
			if x <= p.K {
				// The excursion lasted one bucket: a spike, not an onset.
				d.spikeAt(dir, dir.spike)
				// Its contribution leaves the CUSUM, and no later change point
				// may start at or before it.
				dir.sum = math.Max(0, dir.spikeSum+x-p.K)
				dir.lastZero = max(dir.lastZero, dir.spike)
				dir.spike, dir.alarmRun = -1, 0
				if dir.sum == 0 {
					dir.lastZero = i
				}
				continue
			}
			dir.spike = -1
		}
		// Only buckets beyond K while the alarm is on confirm an onset, so
		// noise that happened to precede a change cannot complete it.
		if dir.sum >= p.H && x > p.K {
			dir.alarmRun++
		} else {
			dir.alarmRun = 0
		}
		switch {
		case dir.alarmRun >= p.ConfirmBuckets:
			d.open(dir, i)
			confirmed = d.points[dir.onset].At
		case dir.alarmRun == 1 && x-p.K >= p.H:
			// This bucket alone reaches the alarm; its next bucket decides
			// whether it was a spike.
			dir.spike, dir.spikeSum = i, previous
		}
		if dir.sum == 0 {
			dir.lastZero = i
		}
	}
	return confirmed
}

// open declares an onset confirmed at bucket i. The onset is the maximum-
// likelihood change point of a mean shift in the standardized scores since
// the CUSUM last left zero: the start t maximizing S(t)^2/n(t) over the
// suffix from t to i. Every start within two log-likelihood units of the
// best widens the onset's uncertainty.
func (d *detector) open(dir *direction, i int) {
	floor := dir.lastZero + 1
	scores := make([]float64, i-floor+1)
	best, onset, sum := 0.0, i, 0.0
	for j := i; j >= floor; j-- {
		sum += dir.sign * d.zs[j]
		if sum > 0 {
			scores[j-floor] = sum * sum / float64(i-j+1)
		}
		if scores[j-floor] > best {
			best, onset = scores[j-floor], j
		}
	}
	var spread time.Duration
	for j := floor; j <= i; j++ {
		if scores[j-floor] > 0 && best-scores[j-floor] <= 4 {
			spread = max(spread, absDuration(d.points[j].At.Sub(d.points[onset].At)))
		}
	}
	s := d.series
	dir.active, dir.onset, dir.ref, dir.inBand, dir.alarmRun = true, onset, d.ref, 0, 0
	dir.uncertainty = spread + s.Bucket + s.ClockUncertainty
	dir.peak = 0
	for j := onset; j <= i; j++ {
		dir.peak = math.Max(dir.peak, dir.sign*d.zs[j])
	}
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// close ends an active anomaly at bucket end (exclusive); recovered says
// whether in-band buckets ended it rather than the span.
func (d *detector) close(dir *direction, end int, recovered bool) {
	s := d.series
	onsetAt := d.points[dir.onset].At
	var endAt time.Time
	if recovered {
		endAt = d.points[end].At
	} else {
		endAt = d.points[len(d.points)-1].At.Add(s.Bucket)
		end = len(d.points)
	}
	values := pointValues(d.points[dir.onset:end])
	sort.Float64s(values)
	direction := DirectionIncrease
	if dir.sign < 0 {
		direction = DirectionDecrease
	}
	id := stableID("anomaly", s.ID, d.scope, direction, onsetAt.UTC().Format(time.RFC3339Nano))
	window := fmt.Sprintf("%s?from=%s&to=%s", s.Evidence.Locator, onsetAt.UTC().Format(time.RFC3339Nano), endAt.UTC().Format(time.RFC3339Nano))
	d.o.anomalies = append(d.o.anomalies, Anomaly{
		ID: id, Series: s.ID, Onset: onsetAt.UTC(), End: endAt.UTC(), OnsetUncertainty: dir.uncertainty,
		Scope: d.scope, Direction: direction, Recovered: recovered, Level: d.level,
		Effect: medianSorted(values) - dir.ref.median, Unit: s.Unit, PeakZ: dir.sign * dir.peak,
		Baseline: d.baseline,
		Evidence: []EvidenceRef{{ID: stableID("evidence", window), Kind: s.Evidence.Kind, Locator: window}},
	})
	dir.active = false
}

func (d *detector) spikeAt(dir *direction, i int) {
	d.o.spikes = append(d.o.spikes, Spike{Series: d.series.ID, At: d.points[i].At.UTC(), Value: d.points[i].Value, Z: d.zs[i], Scope: d.scope, Level: d.level})
}

// finish closes what is still open when the span ends.
func (d *detector) finish() {
	for k := range d.dirs {
		dir := &d.dirs[k]
		if dir.active {
			d.close(dir, len(d.points), false)
		}
		if dir.spike >= 0 {
			// No later bucket arrived; as far as the span shows, it was one bucket.
			d.spikeAt(dir, dir.spike)
			dir.spike = -1
		}
	}
}

// stableID derives an ID from the values that identify the object, so the
// same inputs always name it the same way.
func stableID(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return prefix + "-" + hex.EncodeToString(sum[:8])
}
