package api

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type BaselineMetricDefinition struct {
	Identity    string `json:"identity"`
	Unit        string `json:"unit"`
	Kind        string `json:"kind"`
	Source      string `json:"source"`
	Aggregation string `json:"aggregation,omitempty"`
}

type baselineResourceSeries struct {
	prometheus                                             *BaselinePrometheusIdentity
	gridPhase                                              float64
	name, unit, kind, source, identity, group, aggregation string
	known                                                  bool
	instances                                              int
	points                                                 []BaselineMetricPoint
}

func canonicalBaselineUnit(unit string) (string, float64) {
	switch strings.ToLower(unit) {
	case "bytes", "b":
		return "bytes", 1
	case "kbytes", "kb":
		return "bytes", 1000
	case "mbytes", "mb":
		return "bytes", 1e6
	case "gbytes", "gb":
		return "bytes", 1e9
	case "kib":
		return "bytes", 1024
	case "mib":
		return "bytes", 1048576
	case "gib":
		return "bytes", 1073741824
	case "seconds", "s":
		return "ms", 1000
	case "milliseconds", "ms":
		return "ms", 1
	case "percent", "%":
		return "%", 1
	default:
		return unit, 1
	}
}

func (s *Server) readBaselineResources(ctx context.Context, record *storage.RunArchiveRecord, r *baselineComparisonRun, started time.Time, policy BaselineComparisonPolicy) error {
	points := 0
	add := func(key string, definition BaselineMetricDefinition, known bool, seconds, value float64) error {
		if !finiteComparison(value) || !finiteComparison(seconds) {
			return nil
		}
		points++
		if points > 100000 {
			return fmt.Errorf("resource comparison exceeds 100000 recorded points per run")
		}
		if len(r.resources) > 2000 {
			return fmt.Errorf("resource comparison exceeds 2000 series")
		}
		series := r.resources[key]
		series.name = key
		series.unit, series.kind, series.source, series.identity, series.known = definition.Unit, definition.Kind, definition.Source, definition.Identity, known
		series.aggregation = definition.Aggregation
		series.instances = 1
		series.points = append(series.points, BaselineMetricPoint{Seconds: seconds, Value: value})
		r.resources[key] = series
		return nil
	}
	err := forEachArchiveRecordContext(ctx, record, func(m *RunArchiveManifest) string { return m.ResourceSamplesFile }, func(raw *map[string]json.RawMessage) error {
		var sample ResourceSample
		encoded, _ := json.Marshal(raw)
		if err := json.Unmarshal(encoded, &sample); err != nil {
			return err
		}
		seconds := sample.Timestamp.Sub(started).Seconds()
		for key, encoded := range *raw {
			if key == "timestamp" || key == "test_id" || key == "test_name" || key == "active_vusers" || key == "external_metrics" {
				continue
			}
			var value float64
			if json.Unmarshal(encoded, &value) != nil {
				continue
			}
			unit := "count"
			switch {
			case strings.HasSuffix(key, "_pct"):
				unit = "%"
			case strings.HasSuffix(key, "_mb"):
				unit = "MiB"
			case strings.HasSuffix(key, "_kbps"):
				unit = "KB/s"
			case strings.HasSuffix(key, "_ms"):
				unit = "ms"
			case strings.HasSuffix(key, "_per_sec"):
				unit = "count/s"
			case strings.HasSuffix(key, "_cores"):
				unit = "cores"
			case strings.HasSuffix(key, "_seconds_rate"):
				unit = "s/s"
			}
			d := BaselineMetricDefinition{Identity: "generator/" + key, Unit: unit, Kind: "gauge", Source: "generator"}
			// Interval deltas need collection-window semantics; preserve them as
			// unverified evidence rather than treating them as cumulative counters.
			known := !strings.HasSuffix(key, "_delta")
			if err := add(d.Identity, d, known, seconds, value); err != nil {
				return err
			}
		}
		for key, value := range sample.External {
			definition, known := r.manifest.Provenance.ComparisonMetrics[key]
			if !known {
				definition = BaselineMetricDefinition{Identity: key, Source: "application"}
			}
			if err := add("external/"+key, definition, known && definition.Unit != "" && definition.Kind != "", seconds, value); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("resource archive: %w", err)
	}
	if err := s.readBaselinePrometheusResources(ctx, record, r, started, &points); err != nil {
		return err
	}
	if store, ok := s.store.(importedDashboardSnapshotStore); ok {
		snapshot, e := store.GetGrafanaDashboardSnapshot(r.identity.ID, normalizeProjectID(r.manifest.Provenance.ProjectID))
		if e == nil && snapshot != nil {
			var panels ImportedDashboardGraphsResponse
			if e = json.Unmarshal(snapshot.Payload, &panels); e != nil {
				return fmt.Errorf("invalid saved monitoring snapshot")
			}
			for _, dashboard := range panels.Dashboards {
				for _, panel := range dashboard.Panels {
					if panel.Error != "" {
						r.warnings = append(r.warnings, "Monitoring capture unavailable: "+panel.Title)
						continue
					}
					if len(panel.Series) == 0 {
						r.warnings = append(r.warnings, "Monitoring panel "+panel.Title+" has no time series; load alignment unavailable")
					}
					for _, series := range panel.Series {
						stableLabels := map[string]string{}
						for k, v := range series.Labels {
							if k != "test_id" && k != "test_name" {
								stableLabels[k] = v
							}
						}
						labels, _ := json.Marshal(stableLabels)
						key := fmt.Sprintf("dashboard/%s/%d/%s", dashboard.DashboardID, panel.PanelID, series.Key)
						d := BaselineMetricDefinition{Unit: panel.Unit, Source: "application"}
						if series.ComparisonDefinition != nil {
							d = *series.ComparisonDefinition
						}
						known := d.Identity != "" && d.Unit != "" && (d.Kind == "gauge" || d.Kind == "counter")
						d.Identity = dashboard.DatasourceID + "/" + d.Identity + "/" + string(labels)
						for _, point := range series.Values {
							stamp := point.Timestamp
							if stamp.IsZero() {
								stamp = time.UnixMilli(point.TimestampMs)
							}
							if e = add(key, d, known, stamp.Sub(started).Seconds(), point.Value); e != nil {
								return e
							}
						}
					}
				}
			}
		} else if e != nil {
			r.warnings = append(r.warnings, "No frozen imported-dashboard snapshot available")
		}
	}
	for key, series := range r.resources {
		sort.SliceStable(series.points, func(i, j int) bool { return series.points[i].Seconds < series.points[j].Seconds })
		if series.prometheus != nil {
			r.resources[key] = series
			continue
		}
		unit, factor := canonicalBaselineUnit(series.unit)
		series.unit = unit
		for i := range series.points {
			series.points[i].Value *= factor
		}
		r.resources[key] = series
	}
	// Explicit mappings never silently drop labels or sum per-instance gauges.
	mapped := map[string]baselineResourceSeries{}
	consumed := map[string]bool{}
	for _, m := range policy.ResourceMappings {
		if (m.RunID != r.identity.ID && m.RunID != "*") || !m.Confirmed {
			continue
		}
		prometheusMapping := false
		for _, key := range m.Keys {
			prometheusMapping = prometheusMapping || r.resources[key].prometheus != nil
		}
		if prometheusMapping {
			r.warnings = append(r.warnings, "Prometheus query identities cannot be remapped or combined; define the intended aggregation in approved PromQL before capture")
			continue
		}
		target := baselineResourceSeries{name: m.Identity, identity: m.Identity, unit: m.Unit, kind: m.Kind, group: m.Group, aggregation: m.Aggregation, known: true, instances: len(m.Keys)}
		unit, factor := canonicalBaselineUnit(m.Unit)
		target.unit = unit
		if m.Kind == "counter" {
			target.kind = "gauge"
			target.unit += "/s"
		}
		sources := make([][]BaselineMetricPoint, 0, len(m.Keys))
		for _, key := range m.Keys {
			series, ok := r.resources[key]
			if !ok {
				target.known = false
				sources = append(sources, nil)
				continue
			}
			if target.source == "" {
				target.source = series.source
			} else if target.source != series.source {
				target.source = "mixed"
				target.known = false
			}
			consumed[key] = true
			// A unit supplied for legacy evidence is explicit operator attestation. For
			// known units, require dimensional equivalence and retain canonical values.
			scale := 1.0
			if series.unit == "" {
				scale = factor
			} else if series.unit != unit {
				target.known = false
			}
			points := make([]BaselineMetricPoint, len(series.points))
			for i, point := range series.points {
				points[i] = point
				points[i].Value *= scale
			}
			sources = append(sources, points)
		}
		target.points = aggregateBaselineResourcePoints(sources, m.Kind, m.Aggregation)

		mapped["mapped/"+m.Identity] = target
	}
	for key := range consumed {
		delete(r.resources, key)
	}
	for key, series := range mapped {
		r.resources[key] = series
	}
	return nil
}

func baselineResourceMeasurement(series baselineResourceSeries, w BaselineLoadWindow) *BaselineMeasurement {
	if series.prometheus != nil {
		return baselinePrometheusResourceMeasurement(series, w)
	}
	if len(series.points) < 2 {
		return nil
	}
	gaps := []float64{}
	for i := 1; i < len(series.points); i++ {
		d := series.points[i].Seconds - series.points[i-1].Seconds
		if d > 0 {
			gaps = append(gaps, d)
		}
	}
	if len(gaps) == 0 {
		return nil
	}
	sort.Float64s(gaps)
	maxGap := gaps[len(gaps)/2] * 1.5
	m := &BaselineMeasurement{Min: math.Inf(1), Max: math.Inf(-1), Instances: series.instances}
	weighted, coverage := 0.0, 0.0
	values := []float64{}
	for i := 1; i < len(series.points); i++ {
		a, b := series.points[i-1], series.points[i]
		gap := b.Seconds - a.Seconds
		if gap <= 0 || gap > maxGap {
			continue
		}
		start, end := math.Max(a.Seconds, w.StartSeconds), math.Min(b.Seconds, w.EndSeconds)
		if end <= start {
			continue
		}
		value := a.Value
		if series.kind == "counter" {
			if b.Value < a.Value {
				continue
			}
			value = (b.Value - a.Value) / gap
		}
		if !finiteComparison(value) {
			continue
		}
		duration := end - start
		weighted += value * duration
		coverage += duration
		m.Samples++
		m.Min = math.Min(m.Min, value)
		m.Max = math.Max(m.Max, value)
		values = append(values, value)
		m.Points = append(m.Points, BaselineMetricPoint{Seconds: start, Value: value, Load: w.Load})
	}
	if coverage <= 0 {
		return nil
	}
	m.Mean = weighted / coverage
	m.CoveragePct = math.Min(100, 100*coverage/(w.EndSeconds-w.StartSeconds))
	sort.Float64s(values)
	quantile := func(q float64) float64 { return values[int(float64(len(values)-1)*q)] }
	m.P50 = quantile(.5)
	m.P90 = quantile(.9)
	m.P95 = quantile(.95)
	m.P99 = quantile(.99)
	if len(m.Points) > 120 {
		reduced := make([]BaselineMetricPoint, 0, 120)
		for i := 0; i < 120; i++ {
			reduced = append(reduced, m.Points[i*(len(m.Points)-1)/119])
		}
		m.Points = reduced
	}
	return m
}

func compareBaselineResources(current *baselineComparisonRun, baselines []*baselineComparisonRun, p BaselineComparisonPolicy, qualifications map[string][]string) []BaselineComparisonRow {
	rows := []BaselineComparisonRow{}
	for _, baseline := range baselines {
		alignBaselineResourceIdentities(current, baseline)
	}
	keys := map[string]bool{}
	for k := range current.resources {
		keys[k] = true
	}
	for _, b := range baselines {
		for k := range b.resources {
			keys[k] = true
		}
	}
	if len(keys)*max(1, len(current.windows)) > maxBaselineComparisonCells {
		return nil
	}
	ordered := make([]string, 0, len(keys))
	for k := range keys {
		ordered = append(ordered, k)
	}
	sort.Strings(ordered)
	for _, key := range ordered {
		series, exists := current.resources[key]
		if !exists {
			for _, b := range baselines {
				if v, ok := b.resources[key]; ok {
					series = v
					break
				}
			}
		}
		found := false
		for _, w := range current.windows {
			if w.Group != series.group {
				continue
			}
			found = true
			unit := series.unit
			if series.kind == "counter" {
				unit += "/s"
			}
			row := BaselineComparisonRow{ID: fmt.Sprintf("resource/%d", len(rows)), Name: key, Group: series.group, Source: series.source, Unit: unit, Window: &w, Provider: series.prometheus, Baselines: map[string]BaselineComparisonCell{}}
			if exists {
				row.Current = baselineResourceMeasurement(series, w)
			}
			for _, b := range baselines {
				cell := BaselineComparisonCell{Qualification: append([]string{}, qualifications[b.identity.ID]...)}
				bs, ok := b.resources[key]
				bw := matchingBaselineWindow(w, b.windows, p.LoadTolerancePct)
				cell.Window = bw
				if !ok || !exists {
					if series.prometheus != nil || bs.prometheus != nil {
						cell.Qualification = append(cell.Qualification, "Prometheus series missing or its saved query identity differs")
					} else {
						cell.Qualification = append(cell.Qualification, "Metric missing; map changed source identities explicitly")
					}
				}
				if !series.known || !bs.known {
					if series.prometheus != nil || bs.prometheus != nil {
						cell.Qualification = append(cell.Qualification, "Prometheus saved query provenance unavailable or unverified")
					} else {
						cell.Qualification = append(cell.Qualification, "Metric definition unverified; confirm units, kind and identity in a mapping")
					}
				}
				if series.group != bs.group || series.aggregation != bs.aggregation {
					cell.Qualification = append(cell.Qualification, "Resource group mapping or aggregation differs")
				}
				if series.identity != bs.identity || series.unit != bs.unit || series.kind != bs.kind || series.source != bs.source {
					cell.Qualification = append(cell.Qualification, "Metric source, definition or units differ")
				}
				if series.prometheus != nil || bs.prometheus != nil {
					if !compatibleBaselinePrometheusIdentity(series.prometheus, bs.prometheus) {
						cell.Qualification = append(cell.Qualification, "Prometheus query definition, backend, statistic, labels or evaluation step differs")
					}
				}
				if bw == nil {
					cell.Qualification = append(cell.Qualification, "No matching stable observed load")
				} else {
					cell.Approximate = math.Abs(w.Load-bw.Load) > 1e-9
					cell.Qualification = append(cell.Qualification, baselineWindowQualification(w, *bw, p.LoadTolerancePct)...)
					if ok {
						cell.Measurement = baselineResourceMeasurement(bs, *bw)
					}
				}
				if row.Current == nil || cell.Measurement == nil {
					cell.Qualification = append(cell.Qualification, "Resource has no usable samples in the selected window")
				}
				if row.Current != nil && cell.Measurement != nil {
					// Numeric deltas across incompatible units would be misleading.
					if series.unit == bs.unit && series.kind == bs.kind && compatibleBaselinePrometheusIdentity(series.prometheus, bs.prometheus) {
						cell.Delta, cell.DeltaPct = baselineDelta(row.Current.Mean, cell.Measurement.Mean)
					}
					if row.Current.Samples < 10 || cell.Measurement.Samples < 10 || row.Current.CoveragePct < 80 || cell.Measurement.CoveragePct < 80 {
						cell.Qualification = append(cell.Qualification, "Resource evidence requires 10 samples and 80% window coverage")
					}
				}
				row.Baselines[b.identity.ID] = cell
			}
			rows = append(rows, row)
		}
		if !found {
			row := BaselineComparisonRow{ID: fmt.Sprintf("resource/%d", len(rows)), Name: key, Group: series.group, Source: series.source, Unit: series.unit, Provider: series.prometheus, Baselines: map[string]BaselineComparisonCell{}}
			for _, b := range baselines {
				row.Baselines[b.identity.ID] = BaselineComparisonCell{Qualification: []string{"No comparable current-run resource window"}}
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// Auto-match only one-to-one recorded metric definitions. Ambiguous identities
// remain separate and require explicit operator mappings.
func alignBaselineResourceIdentities(current, baseline *baselineComparisonRun) {
	signature := func(s baselineResourceSeries) string {
		return s.identity + "\x00" + s.unit + "\x00" + s.kind + "\x00" + s.group + "\x00" + s.source + "\x00" + s.aggregation
	}
	currentKeys := map[string][]string{}
	baselineKeys := map[string][]string{}
	for key, series := range current.resources {
		if series.known {
			identity := signature(series)
			currentKeys[identity] = append(currentKeys[identity], key)
		}
	}
	for key, series := range baseline.resources {
		if series.known {
			identity := signature(series)
			baselineKeys[identity] = append(baselineKeys[identity], key)
		}
	}
	for identity, keys := range currentKeys {
		matches := baselineKeys[identity]
		if len(keys) != 1 || len(matches) != 1 || keys[0] == matches[0] {
			continue
		}
		if _, exists := baseline.resources[keys[0]]; exists {
			continue
		}
		baseline.resources[keys[0]] = baseline.resources[matches[0]]
		delete(baseline.resources, matches[0])
	}
}

// Keep every boundary, including missing evidence. Dropping incomplete timestamps
// would bridge gaps and manufacture coverage. Each source must cover exactly the
// same interval; counter resets are checked before combining instance rates.
func aggregateBaselineResourcePoints(sources [][]BaselineMetricPoint, kind, aggregation string) []BaselineMetricPoint {
	times := map[float64]bool{}
	values := make([]map[float64]float64, len(sources))
	maxGaps := make([]float64, len(sources))
	for i, points := range sources {
		values[i] = map[float64]float64{}
		gaps := []float64{}
		for j, point := range points {
			times[point.Seconds] = true
			if _, exists := values[i][point.Seconds]; exists {
				values[i][point.Seconds] = math.NaN()
			} else {
				values[i][point.Seconds] = point.Value
			}
			if j > 0 && point.Seconds > points[j-1].Seconds {
				gaps = append(gaps, point.Seconds-points[j-1].Seconds)
			}
		}
		if len(gaps) > 0 {
			sort.Float64s(gaps)
			maxGaps[i] = 1.5 * gaps[len(gaps)/2]
		}
	}
	ordered := make([]float64, 0, len(times))
	for second := range times {
		ordered = append(ordered, second)
	}
	sort.Float64s(ordered)
	out := make([]BaselineMetricPoint, len(ordered))
	for i, second := range ordered {
		out[i] = BaselineMetricPoint{Seconds: second, Value: math.NaN()}
		if i+1 == len(ordered) {
			out[i].Value = 0
			continue
		} // end boundary, never a measured interval
		next := ordered[i+1]
		combined := 0.0
		valid := len(sources) > 0
		for source, samples := range values {
			a, hasA := samples[second]
			b, hasB := samples[next]
			if !hasA || !hasB || !finiteComparison(a) || !finiteComparison(b) || next-second > maxGaps[source] {
				valid = false
				break
			}
			value := a
			if kind == "counter" {
				if b < a {
					valid = false
					break
				}
				value = (b - a) / (next - second)
			}
			if source == 0 {
				combined = value
			} else if aggregation == "max" {
				combined = math.Max(combined, value)
			} else {
				combined += value
			}
		}
		if valid {
			if aggregation == "mean" {
				combined /= float64(len(sources))
			}
			out[i].Value = combined
		}
	}
	return out
}
