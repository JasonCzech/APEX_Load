package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/jasonczech/apexload/pkg/rca"
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
)

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
	transactions, limitations, err := readTransactionSeries(ctx, record, manifest, budgeter, limit, budget.MaxSeriesPoints)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return input, ctxErr
	}
	if err != nil {
		unreadable("the interval distributions", err)
		transactions = newRCASeriesReader(limit, budget.MaxSeriesPoints)
	}
	input.Limitations = append(input.Limitations, limitations...)
	if limit >= 0 {
		limit = max(0, limit-len(transactions.series))
	}
	monitors := newRCASeriesReader(limit, budget.MaxSeriesPoints)
	cut, unknown, err := readMonitorSeries(ctx, record, manifest, monitors, budgeter, observe)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return input, ctxErr
	}
	if err != nil {
		unreadable("the monitor points", err)
	}
	if unknown > 0 {
		input.Limitations = append(input.Limitations, rca.Limitation{Code: limitationMonitorSemanticsUnknown, Detail: fmt.Sprintf("%d monitor points came from providers that declare no observation semantics and were not judged", unknown)})
	}
	if cut || monitors.droppedSeries > 0 {
		input.Limitations = append(input.Limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: fmt.Sprintf("the series budget or analysis budget left out monitor evidence (%d series)", monitors.droppedSeries)})
	}
	// One record of what the budget cut, across both kinds of series.
	budgeter.Series(len(transactions.series) + transactions.droppedSeries + len(monitors.series) + monitors.droppedSeries)
	budgeter.SeriesPoints(max(transactions.maxOffered, monitors.maxOffered))
	input.Series = append(transactions.list(), monitors.list()...)
	return input, nil
}

// transactionSeries builds one dimension's series from the interval
// distributions: a p95, error-rate and throughput point per key and interval.
type transactionSeries struct {
	dimension string
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

func newTransactionSeries(dimension string, limit, maxPoints int) *transactionSeries {
	return &transactionSeries{dimension: dimension, reader: newRCASeriesReader(limit, maxPoints), templates: map[string]*transactionTemplates{}}
}

// templatesFor returns a key's series templates, created on first sight.
func (t *transactionSeries) templatesFor(name string, bucket time.Duration) (*transactionTemplates, bool) {
	if found, ok := t.templates[name]; ok {
		return found, false
	}
	evidence := rca.EvidenceRef{Kind: "interval", Locator: intervalDistributionsArchiveFileName + "#" + t.dimension + "=" + name}
	series := func(suffix, kind, unit string) rca.Series {
		return rca.Series{ID: t.dimension + "/" + name + "/" + suffix, Kind: kind, Unit: unit, Observation: rca.ObservationInterval, Bucket: bucket, Evidence: evidence}
	}
	created := &transactionTemplates{latency: series("p95_ms", rca.SeriesLatency, "ms"), errorRate: series("error_rate", rca.SeriesErrorRate, "%"), throughput: series("throughput", rca.SeriesThroughput, "1/s")}
	t.templates[name] = created
	index, _ := slices.BinarySearch(t.names, name)
	t.names = slices.Insert(t.names, index, name)
	return created, true
}

// add records one interval. A key absent from a complete interval completed
// nothing in it, so its throughput is zero there, including the intervals
// before it first appeared.
func (t *transactionSeries) add(r IntervalDistributionRecord, current distributionInterval, earlier []distributionInterval, bucket time.Duration) {
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
		templates, created := t.templatesFor(name, bucket)
		if created {
			for _, e := range earlier {
				if e.complete {
					t.reader.add(&templates.throughput, rca.SeriesPoint{At: e.start, Valid: true})
				}
			}
		}
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

// readTransactionSeries reads the run's transaction series. A run that
// defined no business transactions is judged on its actions instead; once a
// transaction appears, actions are no longer collected. Runs archived
// without the distributions sidecar are rebuilt from their rows, within
// bounds.
func readTransactionSeries(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, budgeter *rca.Budgeter, limit, maxPoints int) (*rcaSeriesReader, []rca.Limitation, error) {
	var limitations []rca.Limitation
	transactions := newTransactionSeries("transaction", limit, maxPoints)
	actions := newTransactionSeries("action", limit, maxPoints)
	var intervals []distributionInterval
	emit := func(r IntervalDistributionRecord) error {
		if len(intervals)%1000 == 999 && budgeter.Exhausted() {
			return errRCADetectionStopped
		}
		if r.WindowSeconds <= 0 || math.IsNaN(r.WindowSeconds) || math.IsInf(r.WindowSeconds, 0) {
			return nil
		}
		bucket := time.Duration(r.WindowSeconds * float64(time.Second))
		current := distributionInterval{start: r.Timestamp.UTC().Add(-bucket), complete: !r.Incomplete}
		transactions.add(r, current, intervals, bucket)
		if len(transactions.names) > 0 {
			actions = nil
		} else if actions != nil {
			actions.add(r, current, intervals, bucket)
		}
		intervals = append(intervals, current)
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
		return nil, nil, err
	}
	chosen := transactions
	if len(transactions.names) == 0 && actions != nil && len(actions.names) > 0 {
		chosen = actions
		limitations = append(limitations, rca.Limitation{Code: limitationActionsJudged, Detail: "the run defined no business transactions, so its actions were judged"})
	}
	if len(chosen.names) == 0 {
		limitations = append(limitations, rca.Limitation{Code: limitationIntervalDistributionsMissing, Detail: "no transaction intervals were available, so only provider series were judged"})
	}
	if chosen.reader.droppedSeries > 0 {
		limitations = append(limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: fmt.Sprintf("the series budget left out %d %s series", chosen.reader.droppedSeries, chosen.dimension)})
	}
	return chosen.reader, limitations, nil
}

func finiteNonNegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

// readMonitorSeries groups the archived monitor points into one series per
// provider query, metric and dimension set. It reports whether the budget
// stopped the read and how many points had no declared semantics.
func readMonitorSeries(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, reader *rcaSeriesReader, budgeter *rca.Budgeter, observe func(analysis.MetricPoint)) (bool, int, error) {
	if manifest.MonitorEvidenceFile == "" {
		return false, 0, nil
	}
	read, unknown := 0, 0
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
		identity := monitorSeriesIdentity(*point)
		template, cached := templates[identity]
		if !cached {
			built, ok := monitorSeriesTemplate(*point, identity, manifest.MonitorEvidenceFile)
			if ok {
				template = &built
			}
			templates[identity] = template
		}
		if template == nil {
			unknown++
			return nil
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
	if errors.Is(err, errRCADetectionStopped) {
		return true, unknown, nil
	}
	return false, unknown, err
}

// monitorSeriesIdentity is everything that tells one provider series from
// another, including whether its points count up.
func monitorSeriesIdentity(point analysis.MetricPoint) string {
	m := point.Provider
	identity := []string{m.Name, point.Source, point.MetricName, m.QueryID, m.Statistic, point.Kind}
	for _, key := range slices.Sorted(maps.Keys(point.Dimensions)) {
		identity = append(identity, key+"="+point.Dimensions[key])
	}
	return strings.Join(identity, "\x00")
}

// monitorSeriesTemplate builds the series a provider point belongs to.
// Counters, and providers whose values accumulate, are cumulative.
func monitorSeriesTemplate(point analysis.MetricPoint, identity, file string) (rca.Series, bool) {
	m := point.Provider
	descriptor, known := monitorprovider.ByProviderName(m.Name)
	if !known {
		return rca.Series{}, false
	}
	observation := descriptor.Observation
	if point.Kind == analysis.KindCounter || m.Statistic == "counter" {
		observation = rca.ObservationCumulative
	}
	seconds := m.ResolutionSeconds
	if observation == rca.ObservationSnapshot && m.EvaluationStepSeconds > 0 {
		seconds = m.EvaluationStepSeconds
	}
	digest := sha256.Sum256([]byte(identity))
	id := fmt.Sprintf("monitor/%s/%s/%s/%s", m.Name, point.Source, point.MetricName, hex.EncodeToString(digest[:6]))
	return rca.Series{
		ID: id, Kind: rca.SeriesProvider, Unit: point.Unit, Observation: observation,
		Bucket: time.Duration(seconds) * time.Second, ClockUncertainty: pointClockUncertainty(point),
		Evidence: rca.EvidenceRef{Kind: "monitor_series", Locator: file + "#" + id},
	}, true
}

// pointClockUncertainty is a point's clock uncertainty (WP2.2), or zero.
func pointClockUncertainty(point analysis.MetricPoint) time.Duration {
	if point.Timing != nil && point.Timing.ClockUncertaintyMs != nil && *point.Timing.ClockUncertaintyMs > 0 {
		return time.Duration(*point.Timing.ClockUncertaintyMs * float64(time.Millisecond))
	}
	return 0
}
