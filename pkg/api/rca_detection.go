package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/rca/signatures"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Detection limitations the archive adapter adds.
const (
	limitationIntervalDistributionsReconstructed = "interval_distributions_reconstructed"
	limitationIntervalDistributionsMissing       = "interval_distributions_missing"
	limitationDetectionEvidenceCut               = "detection_evidence_cut"
	limitationMonitorSemanticsUnknown            = "monitor_semantics_unknown"
	limitationActionsJudged                      = "actions_judged"
	limitationDetectionEvidenceUnreadable        = "detection_evidence_unreadable"
	limitationCumulativeSeriesRated              = "cumulative_series_rated"
)

// rcaJointDimension is the archive dimension of joint (version 3) interval
// evidence, keyed by its exact [action, network profile, hierarchy, phase]
// tuple; its series IDs start with rca.SeriesJointPrefix.
const rcaJointDimension = "action_dimensions"

// rcaIdentityScope is the run a series' measurement identity belongs to.
type rcaIdentityScope struct {
	project, scenario string
}

func archiveIdentityScope(manifest *RunArchiveManifest) rcaIdentityScope {
	provenance := manifest.Provenance
	if manifest.InitialProvenance != nil {
		provenance = *manifest.InitialProvenance
	}
	return rcaIdentityScope{project: normalizeProjectID(provenance.ProjectID), scenario: provenance.ScenarioID}
}

// rcaRunSeriesIdentity is the measurement identity of a transaction, action or
// joint series: the MetricPoint projection of what it measures in the run.
func rcaRunSeriesIdentity(scope rcaIdentityScope, dimension, key, metric, unit, inputKind string, bucket time.Duration) string {
	point := analysis.MetricPoint{ProjectID: scope.project, Source: "apexload", ScenarioID: scope.scenario, MetricName: dimension + "." + metric,
		Kind: analysis.KindGauge, InputKind: inputKind, Unit: unit, Dimensions: map[string]string{dimension: key}}
	identity, err := analysis.MeasurementSeriesIdentityOfRecorded(point, analysis.SeriesObservation{Mode: rca.ObservationInterval, BucketNanos: int64(bucket)})
	if err != nil {
		return ""
	}
	return identity.SHA256
}

// errRCADetectionStopped ends an archive read once the analysis budget is
// spent; what was read so far is still analyzed.
var errRCADetectionStopped = errors.New("detection evidence read stopped")

// archiveLoadLevels are the run's steady load levels with their load: VUsers
// for a closed model, the target arrival rate for an open one, both for a
// mixed run. They are the same settled spans baseline comparison uses, which
// skip each level's first 30 s (rca.DetectParams.LevelSettle).
func archiveLoadLevels(summaries []IntervalSummaryRecord, started time.Time, arrivalModel string) []rca.LoadLevel {
	if started.IsZero() {
		return nil
	}
	levels := func(windows []BaselineLoadWindow, set func(*rca.LoadLevel, float64)) []rca.LoadLevel {
		out := make([]rca.LoadLevel, 0, len(windows))
		for _, window := range windows {
			level := rca.LoadLevel{
				Start: started.Add(time.Duration(window.StartSeconds * float64(time.Second))),
				End:   started.Add(time.Duration(window.EndSeconds * float64(time.Second))),
			}
			// The load is a duration-weighted mean; keep its float noise out
			// of the assessment.
			set(&level, math.Round(window.Load*1e6)/1e6)
			out = append(out, level)
		}
		return out
	}
	vusers := levels(baselineStableWindows(summaries, started, "", BaselineComparisonWindow{}), func(l *rca.LoadLevel, load float64) { l.VUsers = load })
	if arrivalModel != rca.ArrivalOpen && arrivalModel != rca.ArrivalMixed {
		return vusers
	}
	rate := levels(stableLoadWindows(summaries, started, "", BaselineComparisonWindow{}, func(s IntervalSummaryRecord) (float64, bool) {
		return s.TargetTPS, s.TargetTPS > 0
	}), func(l *rca.LoadLevel, load float64) { l.Rate = load })
	if arrivalModel == rca.ArrivalOpen {
		return rate
	}
	return intersectLoadLevels(vusers, rate)
}

// intersectLoadLevels returns the overlaps of two sorted, disjoint level
// lists, each carrying both loads.
func intersectLoadLevels(a, b []rca.LoadLevel) []rca.LoadLevel {
	var out []rca.LoadLevel
	for i, j := 0, 0; i < len(a) && j < len(b); {
		start, end := a[i].Start, a[i].End
		if b[j].Start.After(start) {
			start = b[j].Start
		}
		if b[j].End.Before(end) {
			end = b[j].End
		}
		if start.Before(end) {
			out = append(out, rca.LoadLevel{Start: start, End: end, VUsers: a[i].VUsers + b[j].VUsers, Rate: a[i].Rate + b[j].Rate})
		}
		if a[i].End.Before(b[j].End) {
			i++
		} else {
			j++
		}
	}
	return out
}

// rcaSeriesReader collects series up to a series limit and the budget's
// points per series, counting what it leaves out.
type rcaSeriesReader struct {
	// limit is how many series the reader may hold; negative means no limit.
	limit     int
	maxPoints int
	series    map[string]*rca.Series
	offered   map[string]int
	// Series and points left out.
	droppedSeries int
	maxOffered    int
}

func newRCASeriesReader(limit, maxPoints int) *rcaSeriesReader {
	return &rcaSeriesReader{limit: limit, maxPoints: maxPoints, series: map[string]*rca.Series{}, offered: map[string]int{}}
}

// add appends a point to a series, creating it from template when it is new.
func (r *rcaSeriesReader) add(template *rca.Series, point rca.SeriesPoint) {
	s := r.series[template.ID]
	if s == nil {
		if r.limit >= 0 && len(r.series) >= r.limit {
			if _, seen := r.offered[template.ID]; !seen {
				r.droppedSeries++
				r.offered[template.ID] = 0
			}
			return
		}
		created := *template
		created.Points = nil
		s = &created
		r.series[template.ID] = s
	}
	r.offered[template.ID]++
	r.maxOffered = max(r.maxOffered, r.offered[template.ID])
	if r.maxPoints > 0 && len(s.Points) >= r.maxPoints {
		return
	}
	s.Points = append(s.Points, point)
}

// list returns the series in ID order.
func (r *rcaSeriesReader) list() []rca.Series {
	ids := slices.Sorted(maps.Keys(r.series))
	out := make([]rca.Series, 0, len(ids))
	for _, id := range ids {
		out = append(out, *r.series[id])
	}
	return out
}

// archiveDetectionInput reads a run archive's series for onset detection:
// per transaction, the interval p95, error rate and throughput from the
// interval distributions, and every provider series from the monitor points.
// Transaction series take the series budget first. Evidence that cannot be
// read becomes a limitation; only cancellation fails the read.
// observe, when set, sees every provider point read.
func archiveDetectionInput(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, summaries []IntervalSummaryRecord, arrivalModel string, budgeter *rca.Budgeter, budget rca.Budget, observe func(analysis.MetricPoint)) (rca.DetectionInput, error) {
	input := rca.DetectionInput{Levels: archiveLoadLevels(summaries, archiveRunStarted(manifest), arrivalModel)}
	unreadable := func(what string, err error) {
		input.Limitations = append(input.Limitations, rca.Limitation{Code: limitationDetectionEvidenceUnreadable, Detail: what + " could not be read: " + err.Error()})
	}
	limit := -1
	if budget.MaxSeries > 0 {
		limit = budget.MaxSeries
	}
	transactions, joint, limitations, err := readTransactionSeries(ctx, record, manifest, budgeter, limit, budget.MaxSeriesPoints)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return input, ctxErr
	}
	if err != nil {
		unreadable("the interval distributions", err)
		transactions = newRCASeriesReader(limit, budget.MaxSeriesPoints)
		joint = newRCASeriesReader(limit, budget.MaxSeriesPoints)
	}
	input.Limitations = append(input.Limitations, limitations...)
	if limit >= 0 {
		limit = max(0, limit-len(transactions.series))
	}
	monitors := newRCASeriesReader(limit, budget.MaxSeriesPoints)
	cut, unknown, rated, err := readMonitorSeries(ctx, record, manifest, monitors, budgeter, observe)
	jointSeries := joint.list()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return input, ctxErr
	}
	if err != nil {
		unreadable("the monitor points", err)
	}
	if unknown > 0 {
		input.Limitations = append(input.Limitations, rca.Limitation{Code: limitationMonitorSemanticsUnknown, Detail: fmt.Sprintf("%d monitor points came from providers that declare no observation semantics and were not judged", unknown)})
	}
	if rated > 0 {
		input.Limitations = append(input.Limitations, rca.Limitation{Code: limitationCumulativeSeriesRated, Detail: fmt.Sprintf("%d counter series were read as the count added per bucket between samples; a counter reset is a gap", rated)})
	}
	if cut || monitors.droppedSeries > 0 {
		input.Limitations = append(input.Limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: fmt.Sprintf("the series budget or analysis budget left out monitor evidence (%d series)", monitors.droppedSeries)})
	}
	// Joint action-dimension slices take what the series budget leaves.
	jointDropped := joint.droppedSeries
	if limit >= 0 {
		if remaining := max(0, limit-len(monitors.series)); len(jointSeries) > remaining {
			jointDropped += len(jointSeries) - remaining
			jointSeries = jointSeries[:remaining]
		}
	}
	if jointDropped > 0 {
		input.Limitations = append(input.Limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: fmt.Sprintf("the series budget left out %d joint action-dimension series", jointDropped)})
	}
	// One record of what the budget cut, across every kind of series.
	budgeter.Series(len(transactions.series) + transactions.droppedSeries + len(monitors.series) + monitors.droppedSeries + len(jointSeries) + jointDropped)
	budgeter.SeriesPoints(max(transactions.maxOffered, monitors.maxOffered, joint.maxOffered))
	input.Series = append(append(transactions.list(), monitors.list()...), jointSeries...)
	return input, nil
}

// transactionSeries builds one dimension's series from the interval
// distributions: a p95, error-rate and throughput point per key and interval.
type transactionSeries struct {
	dimension string
	scope     rcaIdentityScope
	reader    *rcaSeriesReader
	// names is the sorted list of keys seen so far.
	names     []string
	templates map[string]*transactionTemplates
}

type transactionTemplates struct {
	latency, errorRate, throughput rca.Series
}

type distributionInterval struct {
	start    time.Time
	complete bool
}

func newTransactionSeries(dimension string, scope rcaIdentityScope, limit, maxPoints int) *transactionSeries {
	return &transactionSeries{dimension: dimension, scope: scope, reader: newRCASeriesReader(limit, maxPoints), templates: map[string]*transactionTemplates{}}
}

// templatesFor returns a key's series templates, created on first sight.
func (t *transactionSeries) templatesFor(name string, bucket time.Duration) (*transactionTemplates, bool) {
	if found, ok := t.templates[name]; ok {
		return found, false
	}
	evidence := rca.EvidenceRef{Kind: "interval", Locator: intervalDistributionsArchiveFileName + "#" + t.dimension + "=" + name}
	// A joint slice measures its action; transaction and action series
	// default to their own node.
	var nodes []string
	if t.dimension == rcaJointDimension {
		var labels []string
		if json.Unmarshal([]byte(name), &labels) == nil && len(labels) == 4 && labels[0] != "" {
			nodes = []string{rca.ActionNodeID(labels[0])}
		}
	}
	series := func(suffix, kind, unit, inputKind string) rca.Series {
		return rca.Series{ID: t.dimension + "/" + name + "/" + suffix, Kind: kind, Unit: unit, Observation: rca.ObservationInterval, Bucket: bucket, Evidence: evidence,
			Nodes: slices.Clone(nodes), Identity: rcaRunSeriesIdentity(t.scope, t.dimension, name, suffix, unit, inputKind, bucket)}
	}
	created := &transactionTemplates{latency: series("p95_ms", rca.SeriesLatency, "ms", analysis.KindObservation), errorRate: series("error_rate", rca.SeriesErrorRate, "%", analysis.KindCounter), throughput: series("throughput", rca.SeriesThroughput, "1/s", analysis.KindCounter)}
	t.templates[name] = created
	index, _ := slices.BinarySearch(t.names, name)
	t.names = slices.Insert(t.names, index, name)
	return created, true
}

// add records one interval. A key absent from a complete interval after it
// first appeared completed nothing in it, so its throughput is zero there.
// Before it first appeared it was not measured, so there is no point: a
// group or request that starts later than the run begins at its own first
// interval instead of climbing from a baseline of zeros, which would make
// its appearance the run's largest anomaly.
func (t *transactionSeries) add(r IntervalDistributionRecord, current distributionInterval, bucket time.Duration) {
	present := r.Dimensions[t.dimension]
	if current.complete {
		for _, name := range t.names {
			if _, ok := present[name]; !ok {
				t.reader.add(&t.templates[name].throughput, rca.SeriesPoint{At: current.start, Valid: true})
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(present)) {
		metric := present[name]
		templates, _ := t.templatesFor(name, bucket)
		counted := metric.Count > 0
		t.reader.add(&templates.latency, rca.SeriesPoint{At: current.start, Value: metric.P95, Valid: counted && finiteNonNegative(metric.P95)})
		errorRate := 0.0
		if counted {
			errorRate = float64(metric.Errors) / float64(metric.Count) * 100
		}
		t.reader.add(&templates.errorRate, rca.SeriesPoint{At: current.start, Value: errorRate, Valid: counted})
		t.reader.add(&templates.throughput, rca.SeriesPoint{At: current.start, Value: float64(metric.Count) / r.WindowSeconds, Valid: true})
	}
}

// automaticIterationTransactions are the names the engine gives the
// per-iteration transaction it records for a script that defines none: the
// script's name, or script_iteration.
func automaticIterationTransactions(manifest *RunArchiveManifest) map[string]bool {
	provenance := manifest.Provenance
	if manifest.InitialProvenance != nil {
		provenance = *manifest.InitialProvenance
	}
	names := map[string]bool{"script_iteration": true}
	for _, g := range provenance.Groups {
		if name := strings.TrimSpace(g.ScriptName); name != "" {
			names[name] = true
		}
	}
	return names
}

// readTransactionSeries reads the run's transaction series. A run whose
// scripts defined no business transactions, so that it recorded at most the
// engine's automatic per-iteration ones, is judged on its actions too; once
// an authored transaction appears, actions are no longer collected. Runs
// archived without the distributions sidecar are rebuilt from their rows,
// within bounds.
// Joint interval evidence (version 3) also becomes series, one per exact
// [action, network profile, hierarchy, phase] tuple it recorded, returned
// separately; absence from a record of another version is not a zero.
func readTransactionSeries(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, budgeter *rca.Budgeter, limit, maxPoints int) (*rcaSeriesReader, *rcaSeriesReader, []rca.Limitation, error) {
	var limitations []rca.Limitation
	scope := archiveIdentityScope(manifest)
	transactions := newTransactionSeries("transaction", scope, limit, maxPoints)
	actions := newTransactionSeries("action", scope, limit, maxPoints)
	joint := newTransactionSeries(rcaJointDimension, scope, limit, maxPoints)
	automatic := automaticIterationTransactions(manifest)
	authored := func() bool {
		for _, name := range transactions.names {
			if !automatic[name] {
				return true
			}
		}
		return false
	}
	read := 0
	emit := func(r IntervalDistributionRecord) error {
		if read%1000 == 999 && budgeter.Exhausted() {
			return errRCADetectionStopped
		}
		read++
		if r.WindowSeconds <= 0 || math.IsNaN(r.WindowSeconds) || math.IsInf(r.WindowSeconds, 0) {
			return nil
		}
		bucket := time.Duration(r.WindowSeconds * float64(time.Second))
		current := distributionInterval{start: r.Timestamp.UTC().Add(-bucket), complete: !r.Incomplete}
		transactions.add(r, current, bucket)
		if r.EvidenceVersion >= 3 {
			joint.add(r, current, bucket)
		}
		if actions != nil && authored() {
			actions = nil
		} else if actions != nil {
			actions.add(r, current, bucket)
		}
		return ctx.Err()
	}
	var err error
	if manifest.IntervalDistributionsFile != "" {
		err = forEachArchiveRecordContext(ctx, record, func(m *RunArchiveManifest) string { return m.IntervalDistributionsFile }, func(r *IntervalDistributionRecord) error { return emit(*r) })
		if manifest.IntervalDistributionsTruncated {
			limitations = append(limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: "the archived interval distributions were truncated"})
		}
	} else {
		var truncated bool
		truncated, err = reconstructIntervalDistributions(ctx, record, emit)
		detail := "the run archived no interval distributions; they were rebuilt from its rows"
		if truncated {
			detail += ", within bounds that cut some intervals"
		}
		limitations = append(limitations, rca.Limitation{Code: limitationIntervalDistributionsReconstructed, Detail: detail})
	}
	if errors.Is(err, errRCADetectionStopped) {
		limitations = append(limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: "the analysis budget stopped the interval read before every interval was collected"})
		err = nil
	}
	if err != nil {
		return nil, nil, nil, err
	}
	chosen := transactions
	switch {
	case actions == nil || len(actions.names) == 0:
	case len(transactions.names) == 0:
		chosen = actions
		limitations = append(limitations, rca.Limitation{Code: limitationActionsJudged, Detail: "the run defined no business transactions, so its actions were judged"})
	default:
		// The automatic per-iteration transactions say an iteration slowed;
		// the actions say which request did.
		for _, id := range slices.Sorted(maps.Keys(actions.reader.series)) {
			if transactions.reader.limit >= 0 && len(transactions.reader.series) >= transactions.reader.limit {
				transactions.reader.droppedSeries++
				continue
			}
			transactions.reader.series[id] = actions.reader.series[id]
			transactions.reader.maxOffered = max(transactions.reader.maxOffered, actions.reader.offered[id])
		}
		transactions.reader.droppedSeries += actions.reader.droppedSeries
		limitations = append(limitations, rca.Limitation{Code: limitationActionsJudged, Detail: "the run's scripts defined no business transactions, so its actions were judged beside their automatic per-iteration transactions"})
	}
	if len(chosen.names) == 0 {
		limitations = append(limitations, rca.Limitation{Code: limitationIntervalDistributionsMissing, Detail: "no transaction intervals were available, so only provider series were judged"})
	}
	if chosen.reader.droppedSeries > 0 {
		limitations = append(limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: fmt.Sprintf("the series budget left out %d %s series", chosen.reader.droppedSeries, chosen.dimension)})
	}
	return chosen.reader, joint.reader, limitations, nil
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

// readMonitorSeries groups the archived monitor points into one series per
// provider query, metric and dimension set, and reads each counter series
// as the count added per bucket. It reports whether the budget stopped the
// read, how many points had no declared semantics and how many counter
// series were rated.
func readMonitorSeries(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, reader *rcaSeriesReader, budgeter *rca.Budgeter, observe func(analysis.MetricPoint)) (bool, int, int, error) {
	if manifest.MonitorEvidenceFile == "" {
		return false, 0, 0, nil
	}
	read, unknown := 0, 0
	project := archiveIdentityScope(manifest).project
	// Series identities repeat for every bucket; derive each series once.
	templates := map[string]*rca.Series{}
	err := forEachArchiveRecordContext(ctx, record, func(m *RunArchiveManifest) string { return m.MonitorEvidenceFile }, func(point *analysis.MetricPoint) error {
		read++
		if read%10000 == 0 {
			if budgeter.Exhausted() {
				return errRCADetectionStopped
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if point.Provider == nil {
			unknown++
			return nil
		}
		if observe != nil {
			observe(*point)
		}
		key := monitorSeriesKey(*point)
		template, cached := templates[key]
		if !cached {
			built, ok := monitorSeriesTemplate(*point, project, manifest.MonitorEvidenceFile)
			if ok {
				template = &built
			}
			templates[key] = template
		}
		if template == nil {
			unknown++
			return nil
		}
		if len(point.Provider.EntityIDs) > 0 {
			// Entity IDs are not part of a series' identity; a later point
			// may name more of what the series measures.
			if nodes := addSeriesNodes(template.Nodes, pointNodes(*point)); len(nodes) != len(template.Nodes) {
				template.Nodes = nodes
				if s := reader.series[template.ID]; s != nil {
					s.Nodes = slices.Clone(nodes)
				}
			}
		}
		if clock := pointClockUncertainty(*point); clock > template.ClockUncertainty {
			// The series is widened by its least certain point.
			template.ClockUncertainty = clock
			if s := reader.series[template.ID]; s != nil {
				s.ClockUncertainty = clock
			}
		}
		value := 0.0
		if point.Value != nil {
			value = *point.Value
		}
		valid := point.Value != nil && point.Quality == analysis.QualityValid && !math.IsNaN(value) && !math.IsInf(value, 0)
		reader.add(template, rca.SeriesPoint{At: point.TimestampUTC.UTC(), Value: value, Valid: valid})
		return nil
	})
	cut := errors.Is(err, errRCADetectionStopped)
	if cut {
		err = nil
	}
	// A counter accumulates from an origin and cannot be compared bucket to
	// bucket; what it counted in each bucket can.
	rated := 0
	for _, s := range reader.series {
		if s.Observation != rca.ObservationCumulative {
			continue
		}
		if derived, ok := rateCumulativeSeries(*s); ok {
			*s = derived
			rated++
		}
	}
	return cut, unknown, rated, err
}

// rateCumulativeSeries turns a counter's samples into the count it added per
// bucket between consecutive samples, each on the bucket its earlier sample
// starts. Samples spaced wider or narrower than the series' bucket are
// scaled to it. A sample below its predecessor is a counter reset, whose
// bucket is a gap, as is a bucket beside an invalid sample. A series with
// fewer than two samples stays cumulative, and is excluded.
func rateCumulativeSeries(s rca.Series) (rca.Series, bool) {
	points := slices.Clone(s.Points)
	slices.SortStableFunc(points, func(a, b rca.SeriesPoint) int { return a.At.Compare(b.At) })
	bucket := s.Bucket
	if bucket <= 0 {
		for i := 1; i < len(points); i++ {
			if step := points[i].At.Sub(points[i-1].At); step > 0 && (bucket <= 0 || step < bucket) {
				bucket = step
			}
		}
	}
	if bucket <= 0 || len(points) < 2 {
		return s, false
	}
	out := make([]rca.SeriesPoint, 0, len(points)-1)
	for i := 1; i < len(points); i++ {
		previous, current := points[i-1], points[i]
		point := rca.SeriesPoint{At: previous.At}
		if dt := current.At.Sub(previous.At); previous.Valid && current.Valid && dt > 0 && current.Value >= previous.Value {
			point.Value, point.Valid = (current.Value-previous.Value)*float64(bucket)/float64(dt), true
		}
		out = append(out, point)
	}
	unit := "per " + strconv.FormatFloat(bucket.Seconds(), 'f', -1, 64) + " s"
	if base := strings.TrimSpace(s.Unit); base != "" && base != "1" {
		unit = base + " " + unit
	}
	s.Points, s.Observation, s.Bucket, s.Unit = out, rca.ObservationInterval, bucket, unit
	return s, true
}

// monitorSeriesKey is a cheap key over every field a point's measurement
// identity encodes, except provider entity IDs, which a series accumulates as
// nodes — save a provider's own series discriminator (Prometheus's label-set
// hash), which separates series the allowed labels alone would merge. Points
// with equal keys have equal identities.
func monitorSeriesKey(point analysis.MetricPoint) string {
	m := point.Provider
	parts := []string{point.ProjectID, point.Source, point.ScenarioID, point.Host, point.Service, point.MetricName, point.Population, point.MetricVersion, point.Kind, point.InputKind, point.Unit,
		m.Name, m.Backend, strconv.FormatBool(m.Synthetic), m.MetricID, strconv.Itoa(m.ResolutionSeconds), m.Statistic, strconv.FormatBool(m.QueryWindow),
		m.QueryID, m.DefinitionHash, m.BackendProfile, m.Mode, m.AnalysisStatistic, strconv.Itoa(m.EvaluationStepSeconds), m.EntityIDs[providerSeriesEntityKey],
		m.CatalogPath, m.WindowKind, strconv.FormatBool(m.Approximate), m.DerivedAggregation}
	for _, key := range slices.Sorted(maps.Keys(point.Dimensions)) {
		parts = append(parts, key+"="+point.Dimensions[key])
	}
	for _, step := range point.Derivation {
		parts = append(parts, fmt.Sprintf("%+v", step))
	}
	return strings.Join(parts, "\x00")
}

// providerSeriesEntityKey is the entity ID a provider uses to tell apart
// series whose allowed labels are equal (Prometheus: a hash of the full label
// set).
const providerSeriesEntityKey = "series"

// monitorSeriesTemplate builds the series a provider point belongs to. Its
// identity is the point's measurement series identity without entity IDs
// other than the provider's series discriminator,
// on the observation its provider declares (counters, and providers whose
// values accumulate, are cumulative); a point recorded without a project
// belongs to the run's. A point whose identity fields are incomplete keeps
// the archive.2 key and no identity.
func monitorSeriesTemplate(point analysis.MetricPoint, project, file string) (rca.Series, bool) {
	m := point.Provider
	descriptor, known := monitorprovider.ByProviderName(m.Name)
	if !known {
		return rca.Series{}, false
	}
	observation := descriptor.Observation
	if m.WindowKind == "instant" {
		observation = rca.ObservationSnapshot
	} else if point.Kind == analysis.KindCounter || m.Statistic == "counter" {
		observation = rca.ObservationCumulative
	}
	seconds := m.ResolutionSeconds
	if m.WindowKind == "instant" {
		seconds = 0
	}
	if (observation == rca.ObservationSnapshot || seconds <= 0) && m.EvaluationStepSeconds > 0 {
		seconds = m.EvaluationStepSeconds
	}
	measured := point
	if strings.TrimSpace(measured.ProjectID) == "" {
		measured.ProjectID = project
	}
	provider := *m
	provider.EntityIDs = nil
	if series := m.EntityIDs[providerSeriesEntityKey]; series != "" {
		provider.EntityIDs = map[string]string{providerSeriesEntityKey: series}
	}
	measured.Provider = &provider
	digestOf := ""
	grid := analysis.SeriesObservation{Mode: observation, BucketNanos: int64(seconds) * int64(time.Second)}
	identity, err := analysis.MeasurementSeriesIdentityOfRecorded(measured, grid)
	if err == nil {
		// The series ID is how a baseline run's series is paired with this
		// one, so it leaves out the scenario the run was launched from: a
		// copied scenario or an ad-hoc run measures the same series. The
		// full identity stays on the series.
		comparable := measured
		comparable.ScenarioID = ""
		digestOf = identity.SHA256
		if comparison, comparisonErr := analysis.MeasurementSeriesIdentityOfRecorded(comparable, grid); comparisonErr == nil {
			digestOf = comparison.SHA256
		}
	} else {
		legacy := []string{m.Name, point.Source, point.MetricName, m.QueryID, m.Statistic, point.Kind}
		for _, key := range slices.Sorted(maps.Keys(point.Dimensions)) {
			legacy = append(legacy, key+"="+point.Dimensions[key])
		}
		digest := sha256.Sum256([]byte(strings.Join(legacy, "\x00")))
		digestOf = hex.EncodeToString(digest[:])
	}
	id := fmt.Sprintf("monitor/%s/%s/%s/%s", m.Name, point.Source, point.MetricName, digestOf[:12])
	return rca.Series{
		ID: id, Kind: rca.SeriesProvider, Unit: point.Unit, Observation: observation, Identity: identity.SHA256,
		Bucket: time.Duration(seconds) * time.Second, ClockUncertainty: pointClockUncertainty(point),
		Evidence: rca.EvidenceRef{Kind: "monitor_series", Locator: file + "#" + id},
		Measures: signatures.Classify(point.MetricName + " " + m.MetricID), Nodes: pointNodes(point),
	}, true
}

// pointClockUncertainty is a point's clock uncertainty (WP2.2), or zero.
func pointClockUncertainty(point analysis.MetricPoint) time.Duration {
	if point.Timing != nil && point.Timing.ClockUncertaintyMs != nil && *point.Timing.ClockUncertaintyMs > 0 {
		return time.Duration(*point.Timing.ClockUncertaintyMs * float64(time.Millisecond))
	}
	return 0
}
