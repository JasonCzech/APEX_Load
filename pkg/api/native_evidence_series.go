package api

import (
	"context"
	"errors"
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

type NativeEvidencePoint struct {
	Coverage      *float64  `json:"coverage,omitempty"`
	From          time.Time `json:"from"`
	To            time.Time `json:"to"`
	OffsetSeconds float64   `json:"offset_seconds"`
	Count         int64     `json:"count"`
	Value         *float64  `json:"value"`
}
type NativeEvidenceSeries struct {
	Name   string                `json:"name"`
	Unit   string                `json:"unit"`
	Points []NativeEvidencePoint `json:"points"`
}
type NativeEvidenceResponse struct {
	Annotations    []storage.RunAnnotation                `json:"annotations,omitempty"`
	LiveBucketMode string                                 `json:"live_bucket_mode,omitempty"`
	Mode           string                                 `json:"mode"`
	Series         []NativeEvidenceSeries                 `json:"series"`
	Warnings       []string                               `json:"warnings"`
	Overlay        []analysis.MetricPoint                 `json:"overlay,omitempty"`
	Correlation    map[string][]analysis.CorrelationScore `json:"correlation,omitempty"`
	Start          time.Time                              `json:"start"`
	From           time.Time                              `json:"from"`
	To             time.Time                              `json:"to"`
}

func nativeMetricUnit(q storage.AnalyticsQuery) string {
	switch q.Metric {
	case "response_time":
		return "ms"
	case "error_rate":
		return "%"
	default:
		return "observations"
	}
}
func (s *Server) handleNativeEvidenceSeries(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	var q storage.AnalyticsQuery
	if !decodeJSONBody(w, r, &q) {
		return
	}
	if err := storage.ValidateNativeEvidenceQuery(q); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if !admitAnalysisPipeline(w) {
		return
	}
	defer func() { <-analysisPipelineJobs }()
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	run, ok := s.archiveDomainService().GetTest(r.PathValue("id"))
	if !ok {
		writeError(w, 404, "run not found")
		return
	}
	var result NativeEvidenceResponse
	var err error
	if q.EvidenceMode == "live" {
		if s.runner == nil {
			writeError(w, 409, "live history unavailable")
			return
		}
		live, exists := s.runner.GetTestMetricsWindow(run.ID, nil, 1000)
		if !exists || live.StartedAt == nil {
			writeError(w, 409, "execution start unavailable")
			return
		}
		result, err = buildNativeLiveEvidence(ctx, *live.StartedAt, live.History, q)
	} else {
		record, exists := s.archiveDomainService().GetRunArchiveContext(ctx, run.ID)
		if !exists {
			writeError(w, 409, "completed archive unavailable")
			return
		}
		result, err = buildNativeGroupedArchive(ctx, record, q)
	}
	if err == nil && q.OverlaySelection != nil {
		err = s.attachNativeOverlay(ctx, run.ProjectID, run.ID, q, &result)
	}
	if err != nil {
		writeError(w, 422, err.Error())
		return
	}
	if store, ok := s.store.(storage.RunAnnotationStore); ok {
		annotations, err := store.GetRunAnnotations(run.ProjectID, run.ID)
		if err != nil {
			writeError(w, 500, "failed to load annotations")
			return
		}
		for _, a := range annotations.Annotations {
			if a.DeletedAt == nil && !a.Timestamp.Before(result.From) && a.Timestamp.Before(result.To) {
				result.Annotations = append(result.Annotations, a)
			}
		}
	}
	writeJSON(w, 200, result)
}
func nativeEvidenceEnvelope(start time.Time, q storage.AnalyticsQuery) NativeEvidenceResponse {
	start = start.UTC()
	return NativeEvidenceResponse{Mode: q.EvidenceMode, Start: start, From: start.Add(time.Duration(q.StartSeconds) * time.Second), To: start.Add(time.Duration(q.EndSeconds) * time.Second), Series: []NativeEvidenceSeries{}, Warnings: []string{}}
}
func nativeGroup(q storage.AnalyticsQuery) string {
	if len(q.Dimensions) > 0 {
		return q.Dimensions[0]
	}
	return ""
}
func buildNativeLiveEvidence(ctx context.Context, start time.Time, history []*LiveMetrics, q storage.AnalyticsQuery) (NativeEvidenceResponse, error) {
	if q.LiveBucketMode == "merged" {
		return buildMergedNativeLiveEvidence(ctx, start, history, q)
	}
	out := nativeEvidenceEnvelope(start, q)
	dimension := nativeGroup(q)
	if dimension == "" || dimension == "action" {
		dimension = q.Population
	}
	if q.PropertyName != "" {
		dimension = "property"
	}
	names := map[string]bool{}
	selected := func(name string) bool {
		if dimension == "property" {
			if q.PropertyValue != "" {
				return name == q.PropertyName+"="+q.PropertyValue
			}
			return len(name) > len(q.PropertyName) && name[:len(q.PropertyName)+1] == q.PropertyName+"="
		}
		return q.Filters["action"] == "" || q.Filters["action"] == name
	}
	intervals := []*LiveMetrics{}
	available := false
	for _, h := range history {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if h == nil || h.Timestamp.Before(out.From) || !h.Timestamp.Before(out.To) {
			continue
		}
		intervals = append(intervals, h)
		e := h.NativeEvidence
		if e == nil || (e.Version < 1 || e.Version > 3) || e.WindowSeconds <= 0 || e.Incomplete {
			continue
		}
		if _, ok := e.Dimensions[dimension]; ok {
			available = true
		}
		for name := range e.Dimensions[dimension] {
			if selected(name) {
				names[name] = true
			}
		}
	}
	if len(intervals) > 0 && !available {
		return out, errors.New("selected live interval evidence is unavailable or incomplete; the peer/runtime must support native evidence v1")
	}
	if len(names) > q.MaxSeries {
		return out, errors.New("series limit exceeded; narrow the selection or raise max_series (up to 20)")
	}
	keys := make([]string, 0, len(names))
	for name := range names {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	unavailable := false
	for _, name := range keys {
		series := NativeEvidenceSeries{Name: name, Unit: nativeMetricUnit(q), Points: []NativeEvidencePoint{}}
		for _, h := range intervals {
			p := NativeEvidencePoint{From: h.Timestamp, To: h.Timestamp, OffsetSeconds: h.Timestamp.Sub(start).Seconds()}
			e := h.NativeEvidence
			if e != nil && (e.Version >= 1 && e.Version <= 3) && e.WindowSeconds > 0 && !e.Incomplete {
				p.From = h.Timestamp.Add(-time.Duration(e.WindowSeconds * float64(time.Second)))
				if m, ok := e.Dimensions[dimension][name]; ok && m.Count > 0 {
					if m.Errors < 0 || m.Errors > m.Count {
						return out, errors.New("invalid retained interval counts")
					}
					value := m.Avg
					switch q.Metric {
					case "observation_count":
						value = float64(m.Count)
					case "error_rate":
						value = 100 * float64(m.Errors) / float64(m.Count)
					default:
						switch q.Aggregation {
						case "p50":
							value = m.P50
						case "p90":
							value = m.P90
						case "p95":
							value = m.P95
						case "p99":
							value = m.P99
						}
					}
					if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
						return out, errors.New("invalid retained interval value")
					}
					p.Value = &value
					p.Count = m.Count
				}
			} else {
				unavailable = true
			}
			series.Points = append(series.Points, p)
		}
		out.Series = append(out.Series, series)
	}
	if unavailable || len(keys) == 0 {
		out.Warnings = append(out.Warnings, "Some interval evidence is unavailable, incomplete, or from an unsupported peer. No totals are substituted.")
	}
	out.Warnings = append(out.Warnings, "Retained publication intervals, labelled by end time; percentiles are not re-bucketed. History is bounded to the newest 1,000 retained points. Missing selections remain gaps.")
	return out, nil
}
func buildNativeGroupedArchive(ctx context.Context, record *storage.RunArchiveRecord, q storage.AnalyticsQuery) (NativeEvidenceResponse, error) {
	out := NativeEvidenceResponse{}
	lease, err := acquireArchiveMaterialized(ctx, record)
	if err != nil {
		return out, err
	}
	defer lease.Release()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		return out, err
	}
	if manifest.CompletedAt.IsZero() || manifest.Provenance.StartedAt == nil || manifest.Provenance.StartedAt.IsZero() {
		return out, errors.New("completed archive with execution start required")
	}
	out = nativeEvidenceEnvelope(*manifest.Provenance.StartedAt, q)
	type bucket struct {
		values []float64
		errors int
	}
	groups := map[string][]bucket{}
	selected := 0
	missing := false
	n := (q.EndSeconds - q.StartSeconds + q.BucketSeconds - 1) / q.BucketSeconds
	err = forEachArchiveTransactionContext(ctx, record, func(tx *TransactionEvent) error {
		if tx.Timestamp.Before(out.From) || !tx.Timestamp.Before(out.To) {
			return nil
		}
		population := archiveEventPopulation(*tx)
		if population == "unknown" {
			missing = true
			return nil
		}
		if population != q.Population {
			return nil
		}
		if q.Filters["action"] != "" && tx.ActionName != q.Filters["action"] {
			return nil
		}
		if !matchesNativeDimensionFilters(tx, q.Filters) {
			return nil
		}
		if q.PropertyName != "" {
			v, ok := tx.PropertyDimensions[q.PropertyName]
			if !ok {
				missing = true
				return nil
			}
			if q.PropertyValue != "" && v != q.PropertyValue {
				return nil
			}
		}
		name := "Selection"
		switch nativeGroup(q) {
		case "action":
			name = tx.ActionName
		case "property":
			name = q.PropertyName + "=" + tx.PropertyDimensions[q.PropertyName]
		case "region":
			name = tx.Region
		case "dependency":
			name = tx.DependencyTarget
		case "network_profile":
			name = tx.NetworkProfile
		case "hierarchy":
			name = tx.Hierarchy
		case "phase":
			name = tx.Phase
		}
		if name == "" {
			missing = true
			return nil
		}
		if len(name) > 256 {
			return errors.New("group identity exceeds limit")
		}
		if _, ok := groups[name]; !ok {
			if len(groups) >= q.MaxSeries {
				return errors.New("series limit exceeded; narrow the selection")
			}
			groups[name] = make([]bucket, n)
		}
		if selected >= 100000 {
			return errors.New("raw observation limit exceeded; narrow the selection")
		}
		selected++
		if math.IsNaN(tx.DurationMs) || math.IsInf(tx.DurationMs, 0) || tx.DurationMs < 0 {
			return errors.New("invalid raw duration")
		}
		i := int(tx.Timestamp.Sub(out.From)/time.Second) / q.BucketSeconds
		buckets := groups[name]
		buckets[i].values = append(buckets[i].values, tx.DurationMs)
		if !tx.Success {
			buckets[i].errors++
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		series := NativeEvidenceSeries{Name: key, Unit: nativeMetricUnit(q), Points: []NativeEvidencePoint{}}
		for i, b := range groups[key] {
			from := out.From.Add(time.Duration(i*q.BucketSeconds) * time.Second)
			to := from.Add(time.Duration(q.BucketSeconds) * time.Second)
			if to.After(out.To) {
				to = out.To
			}
			p := NativeEvidencePoint{From: from, To: to, OffsetSeconds: from.Sub(out.Start).Seconds(), Count: int64(len(b.values))}
			if len(b.values) > 0 {
				sort.Float64s(b.values)
				value := 0.0
				switch q.Metric {
				case "observation_count":
					value = float64(len(b.values))
				case "error_rate":
					value = 100 * float64(b.errors) / float64(len(b.values))
				default:
					switch q.Aggregation {
					case "avg":
						for _, v := range b.values {
							value += v / float64(len(b.values))
						}
					case "min":
						value = b.values[0]
					case "max":
						value = b.values[len(b.values)-1]
					default:
						pct := map[string]float64{"p50": .5, "p90": .9, "p95": .95, "p99": .99}[q.Aggregation]
						value = b.values[int(math.Ceil(pct*float64(len(b.values))))-1]
					}
				}
				p.Value = &value
			}
			series.Points = append(series.Points, p)
		}
		out.Series = append(out.Series, series)
	}
	if missing {
		out.Warnings = append(out.Warnings, "Legacy or missing dimension evidence was excluded; coverage is incomplete.")
	}
	if manifest.SourceCoverage != nil {
		out.Warnings = append(out.Warnings, "Review archive source coverage before interpreting groups.")
	}
	if out.To.After(manifest.CompletedAt) {
		out.Warnings = append(out.Warnings, "The selection extends past run completion.")
	}
	if selected == 0 {
		out.Warnings = append(out.Warnings, "No matching raw observations; missing is not zero.")
	}
	return out, nil
}
func (s *Server) attachNativeOverlay(ctx context.Context, project, runID string, q storage.AnalyticsQuery, out *NativeEvidenceResponse) error {
	o := q.OverlaySelection
	if o.SourceID != "" && !runMonitoringEnabled(project) {
		return errors.New("run monitoring is disabled for project")
	}
	if o.DatasetID != "" {
		imports, ok := s.store.(storage.AnalysisImportStore)
		if !ok {
			return errors.New("dataset storage unavailable")
		}
		dataset, err := imports.GetAnalysisImport(o.DatasetID)
		if err != nil || dataset.ProjectID != project || dataset.State != "ready" {
			return errors.New("committed dataset not found in project")
		}
		if dataset.RunID != runID {
			return errors.New("explicitly associate the dataset with this run before overlaying")
		}
	}
	selection := AnalysisSeriesSelection{DatasetID: o.DatasetID, SourceID: o.SourceID, Metric: o.Metric, Aggregation: o.Aggregation, Host: o.Host, Dimensions: o.Dimensions}
	if o.DatasetID == "" {
		selection.RunID = runID
	}
	request := AnalysisComparisonRequest{From: out.From, To: out.To, BucketSeconds: q.BucketSeconds, MaxLagSeconds: o.MaxLagSeconds}
	points, associated, err := s.loadAnalysisSelection(ctx, project, selection, request)
	if err != nil {
		return err
	}
	if associated != runID {
		return errors.New("explicitly associate the dataset with this run before overlaying")
	}
	for _, p := range points {
		if p.Unit == "" {
			return errors.New("overlay measurement requires an explicit unit")
		}
	}
	out.Overlay = points
	if len(points) == 0 {
		out.Warnings = append(out.Warnings, "No matching overlay evidence in this window.")
	}

	out.Warnings = append(out.Warnings, "Overlay uses its own labelled unit axis. Correlation is association, not proof of causation; gaps and invalid quality are excluded.")
	if q.EvidenceMode == "live" && q.LiveBucketMode != "merged" {
		out.Warnings = append(out.Warnings, "Lag correlation requires aligned archive buckets; live source intervals are displayed without resampling.")
		return nil
	}
	out.Correlation = map[string][]analysis.CorrelationScore{}
	// Shared correlation validates alignment and excludes missing/invalid quality.
	right := append([]analysis.MetricPoint(nil), points...)
	for i := range right {
		right[i].DatasetID = ""
		right[i].RunID = ""
		right[i].RelativeTimeMS = nil
		right[i].ActiveRuntimeMS = nil
	}
	for _, series := range out.Series {
		left := []analysis.MetricPoint{}
		for _, p := range series.Points {
			quality := analysis.QualityValid
			if p.Value == nil {
				quality = analysis.QualityMissing
			}
			left = append(left, analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, Source: "native-panel", ProjectID: project, TimestampUTC: p.From.UTC(), MetricName: q.Metric, Unit: series.Unit, Kind: analysis.KindGauge, Value: p.Value, Quality: quality})
		}
		scores, err := analysis.LaggedCorrelation(left, right, out.From, out.To, q.BucketSeconds, o.MaxLagSeconds)
		if err != nil {
			return err
		}
		out.Correlation[series.Name] = scores
	}
	return nil
}
