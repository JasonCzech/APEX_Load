package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/storage"
	"maps"
	"math"
	"slices"
	"sort"
	"time"
)

// Merged buckets contain whole publications whose end falls in (from,to].
// Distributions cannot be split by time; this completion-cohort convention is
// explicit in the response and never claims exact transaction event-time bins.
func buildMergedNativeLiveEvidence(ctx context.Context, start time.Time, history []*LiveMetrics, q storage.AnalyticsQuery) (NativeEvidenceResponse, error) {
	out := nativeEvidenceEnvelope(start, q)
	out.LiveBucketMode = "merged"
	n := (q.EndSeconds - q.StartSeconds + q.BucketSeconds - 1) / q.BucketSeconds
	type aggregate struct {
		count, errors int64
		sum           float64
		durations     *engine.DurationDistributionMerge
		invalid       bool
	}
	groups := map[string][]aggregate{}
	invalid := make([]bool, n)
	type sourceWindow struct{ from, to time.Time }
	windows := make([]map[string][]sourceWindow, n)
	rateUnsafe := make([]bool, n)
	expected := make([]map[string]bool, n)
	for i := range windows {
		windows[i] = map[string][]sourceWindow{}
		expected[i] = map[string]bool{}
	}
	dimension := nativeGroup(q)
	if dimension == "" || dimension == "action" {
		dimension = q.Population
	}
	joint := q.PropertyName != "" && (q.Filters["action"] != "" || nativeGroup(q) == "action")
	if joint {
		dimension = "action_property"
	} else if q.PropertyName != "" {
		dimension = "property"
	}
	jointDimensions := storage.NativeActionDimensionsRequired(q)
	if jointDimensions {
		dimension = "action_dimensions"
	}
	// Joint evidence v4 (B5): tuples under the run's projection, grouped by
	// up to three projected dimensions. Series are named by the JSON array
	// of the selected values, so distinct tuples never share a label.
	jointV4 := q.JointEvidence
	var projected []string
	if jointV4 {
		dimension, joint, jointDimensions = storage.NativeJointDimension, false, false
	}
	adoptProjection := func(evidence *storage.NativeJointEvidence) (bool, error) {
		if projected == nil {
			if evidence.Population != q.Population {
				return false, fmt.Errorf("the run's joint evidence counts the %s population", evidence.Population)
			}
			for _, name := range append(append([]string(nil), q.Dimensions...), slices.Sorted(maps.Keys(q.Filters))...) {
				if !slices.Contains(evidence.Dimensions, name) {
					return false, fmt.Errorf("the run's joint evidence does not project %q", name)
				}
			}
			projected = append([]string(nil), evidence.Dimensions...)
			return true, nil
		}
		return evidence.Population == q.Population && slices.Equal(evidence.Dimensions, projected), nil
	}
	nameFor := func(key string) (string, bool, error) {
		if jointV4 {
			values, ok := engine.JointTupleValues(key, len(projected))
			if !ok {
				return "", false, errors.New("invalid joint evidence tuple")
			}
			byDimension := make(map[string]string, len(projected))
			for i, name := range projected {
				byDimension[name] = values[i]
			}
			for name, value := range q.Filters {
				if byDimension[name] != value {
					return "", false, nil
				}
			}
			if len(q.Dimensions) == 0 {
				return "Selection", true, nil
			}
			selected := make([]string, len(q.Dimensions))
			for i, name := range q.Dimensions {
				selected[i] = byDimension[name]
			}
			label, _ := engine.JointTupleKey(selected)
			return label, true, nil
		}
		name := key
		if jointDimensions {
			var labels []string
			if len(key) > 512 || json.Unmarshal([]byte(key), &labels) != nil || len(labels) != 4 {
				return "", false, errors.New("invalid action dimension identity")
			}
			dimensions := map[string]string{"action": labels[0], "network_profile": labels[1], "hierarchy": labels[2], "phase": labels[3]}
			for key, value := range q.Filters {
				if dimensions[key] != value {
					return "", false, nil
				}
			}
			name = dimensions[nativeGroup(q)]
			if nativeGroup(q) == "" {
				name = "Selection"
			} else if name == "" {
				return "", false, errors.New("selected dimension is unavailable for some observations")
			}
		} else if joint {
			var labels []string
			if json.Unmarshal([]byte(key), &labels) != nil || len(labels) != 3 {
				return "", false, errors.New("invalid joint identity")
			}
			if labels[1] != q.PropertyName || q.PropertyValue != "" && labels[2] != q.PropertyValue || q.Filters["action"] != "" && labels[0] != q.Filters["action"] {
				return "", false, nil
			}
			if nativeGroup(q) == "property" {
				name = labels[1] + "=" + labels[2]
			} else {
				name = labels[0]
			}
		} else if dimension == "property" {
			prefix := q.PropertyName + "="
			if len(key) < len(prefix) || key[:len(prefix)] != prefix || q.PropertyValue != "" && key != prefix+q.PropertyValue {
				return "", false, nil
			}
		} else if q.Filters["action"] != "" && key != q.Filters["action"] {
			return "", false, nil
		}
		if nativeGroup(q) == "" {
			name = "Selection"
		}
		return name, true, nil
	}
	seen := map[string]bool{}
	supported := false
	publications := 0
	if jointV4 {
		// A distributed point also carries the joint evidence of the source
		// batches accepted since the previous point (publications are
		// throttled fleet-wide): each is a window of its own.
		expanded := make([]*LiveMetrics, 0, len(history))
		for _, h := range history {
			expanded = append(expanded, h)
			if h == nil || h.NativeEvidence == nil {
				continue
			}
			for _, p := range h.NativeEvidence.JointPublications {
				if p != nil {
					expanded = append(expanded, &LiveMetrics{Timestamp: p.Timestamp, NativeEvidence: &storage.NativeIntervalEvidence{Publication: p}})
				}
			}
		}
		history = expanded
	}
	for _, h := range history {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if h == nil {
			continue
		}
		timestamp := h.Timestamp
		e := h.NativeEvidence
		source := "local"
		stamp := fmt.Sprintf("local/%d", timestamp.UnixNano())
		sources := []string{"local"}
		distributed := false
		if e != nil && e.Publication != nil {
			p := e.Publication
			timestamp = p.Timestamp
			source = p.SourceID
			if source == "" || len(source) > 512 || p.Sequence == 0 || len(p.ExpectedSources) > 128 {
				return out, errors.New("invalid source publication identity")
			}
			stamp = nativePublicationIdentity(p)
			sources = p.ExpectedSources
			distributed = true
			e = &storage.NativeIntervalEvidence{Version: p.Version, WindowSeconds: p.WindowSeconds, Dimensions: p.Dimensions, Incomplete: p.Incomplete, Joint: p.Joint}
		}
		if !timestamp.After(out.From) || timestamp.After(out.To) {
			continue
		}
		if seen[stamp] {
			if distributed {
				continue
			}
			return out, errors.New("duplicate publication timestamp")
		}
		seen[stamp] = true
		i := int((timestamp.Sub(out.From) - time.Nanosecond) / (time.Duration(q.BucketSeconds) * time.Second))
		if i < 0 || i >= n {
			continue
		}
		publications++
		expected[i][source] = true
		for _, id := range sources {
			if id == "" || len(id) > 512 {
				return out, errors.New("invalid expected source identity")
			}
			expected[i][id] = true
		}
		versionInvalid := !jointV4 && e != nil && (e.Version < 2 || e.Version > 3 || jointDimensions && e.Version != 3)
		// Joint evidence carries its own completeness (tuple bound and sample
		// loss); the marginal dimensions' key caps do not make it partial.
		marginalIncomplete := !jointV4 && e != nil && e.Incomplete
		// A window whose joint evidence is unavailable, incomplete (tuples
		// past the bound) or under another projection is a gap, never a
		// partial total.
		jointInvalid := false
		if jointV4 && e != nil {
			if e.Joint == nil || e.Joint.Availability != "measured" || e.Joint.Incomplete || e.Joint.UnavailableSources > 0 {
				jointInvalid = true
			} else if matches, err := adoptProjection(e.Joint); err != nil {
				return out, err
			} else {
				jointInvalid = !matches
			}
		}
		if e == nil || versionInvalid || jointInvalid || marginalIncomplete || math.IsNaN(e.WindowSeconds) || math.IsInf(e.WindowSeconds, 0) || e.WindowSeconds <= 0 || e.WindowSeconds > 3600 {
			invalid[i] = true
			continue
		}
		metrics, ok := e.Dimensions[dimension]
		if !ok {
			invalid[i] = true
			continue
		}
		supported = true
		window := sourceWindow{from: timestamp.Add(-time.Duration(math.Round(e.WindowSeconds * float64(time.Second)))), to: timestamp}
		windows[i][source] = append(windows[i][source], window)
		bucketFrom := out.From.Add(time.Duration(i*q.BucketSeconds) * time.Second)
		bucketTo := bucketFrom.Add(time.Duration(q.BucketSeconds) * time.Second)
		if bucketTo.After(out.To) {
			bucketTo = out.To
		}
		// Counts from a whole publication cannot be split across bucket edges.
		// Such cohorts remain useful for distributions, but not a bucket rate.
		if window.from.Before(bucketFrom) || window.to.After(bucketTo) {
			rateUnsafe[i] = true
		}
		for key, m := range metrics {
			name, selected, err := nameFor(key)
			if err != nil {
				return out, err
			}
			if !selected {
				continue
			}
			if _, ok := groups[name]; !ok {
				if len(groups) >= q.MaxSeries {
					return out, errors.New("series limit exceeded; narrow the selection")
				}
				groups[name] = make([]aggregate, n)
			}
			b := &groups[name][i]
			if m.Count == 0 {
				continue
			}
			if m.Count < 0 || m.Count > 1e12 || m.Errors < 0 || m.Errors > m.Count || math.IsNaN(m.Avg) || math.IsInf(m.Avg, 0) || m.Avg < 0 {
				b.invalid = true
				continue
			}
			if b.durations == nil {
				b.durations = engine.NewDurationDistributionMerge()
			}
			if !b.durations.Add(m.Count, m.DurationDigest, m.DurationSketch) {
				b.invalid = true
				continue
			}
			if b.count > 1e12-m.Count {
				return out, errors.New("bucket observation limit exceeded")
			}
			b.count += m.Count
			b.errors += m.Errors
			b.sum += m.Avg * float64(m.Count)
		}
	}
	if publications > 0 && !supported {
		if jointV4 {
			return out, errors.New("merged live joint queries require measured joint evidence v4 (a run projection and capable generators)")
		}
		return out, errors.New("merged live queries require native evidence v2, or v3 for dimension intersections, from every contributing runtime")
	}
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)
	partial := false
	for _, name := range names {
		series := NativeEvidenceSeries{Name: name, Unit: nativeMetricUnit(q)}
		for i, b := range groups[name] {
			if b.count > 0 && b.durations.Kind() == "" {
				// Its sources' distributions do not merge into one: some
				// carried only a sketch and others only a t-digest.
				b.invalid = true
			}
			from := out.From.Add(time.Duration(i*q.BucketSeconds) * time.Second)
			to := from.Add(time.Duration(q.BucketSeconds) * time.Second)
			if to.After(out.To) {
				to = out.To
			}
			coverage := 0.0
			if len(expected[i]) > 0 {
				coverage = 1
				for source := range expected[i] {
					intervals := windows[i][source]
					if len(intervals) == 0 {
						invalid[i] = true
					}
					sort.Slice(intervals, func(a, b int) bool { return intervals[a].from.Before(intervals[b].from) })
					covered := time.Duration(0)
					coveredTo := from
					lastEnd := time.Time{}
					for _, interval := range intervals {
						if !lastEnd.IsZero() && interval.from.Before(lastEnd) {
							rateUnsafe[i] = true
						}
						if interval.to.After(lastEnd) {
							lastEnd = interval.to
						}
						clippedFrom, clippedTo := interval.from, interval.to
						if clippedFrom.Before(coveredTo) {
							clippedFrom = coveredTo
						}
						if clippedTo.After(to) {
							clippedTo = to
						}
						if clippedTo.After(clippedFrom) {
							covered += clippedTo.Sub(clippedFrom)
							coveredTo = clippedTo
						}
					}
					coverage = math.Min(coverage, math.Min(1, float64(covered)/float64(to.Sub(from))))
				}
			}
			// A count (or a rate of one) also needs the bucket's whole window
			// covered by publications that do not straddle its edges: a
			// partly collected bucket undercounts. A ratio derivation reads
			// the counts as a sample and needs no more than other metrics.
			counted := slices.Contains(nativeCountMetrics, q.Metric) && !nativeRatioDerivation(q)
			p := NativeEvidencePoint{From: from, To: to, OffsetSeconds: from.Sub(start).Seconds(), Coverage: &coverage, failures: b.errors,
				// Some source published into the bucket, and every expected
				// source did so with valid evidence. A bucket nobody published
				// into is not an observed zero.
				complete: len(expected[i]) > 0 && !invalid[i] && !b.invalid && (!counted || coverage >= 1 && !rateUnsafe[i])}
			if b.count > 0 && !invalid[i] && !b.invalid {
				// A ratio reads the counts as a sample, whatever the panel.
				p.samples = b.count
			}
			if b.count > 0 && !invalid[i] && !b.invalid && (!storage.IsNativeObservationRate(q.Metric) || coverage >= 1 && !rateUnsafe[i]) {
				value := b.sum / float64(b.count)
				switch q.Metric {
				case "observation_rate", "error_observation_rate", "success_observation_rate":
					value = *storage.NativeObservationRate(q.Metric, b.count, b.errors, to.Sub(from).Seconds())
				case "observation_count":
					value = float64(b.count)
				case "error_rate":
					value = 100 * float64(b.errors) / float64(b.count)
				default:
					switch q.Aggregation {
					case "min":
						value = b.durations.Quantile(0) / float64(time.Millisecond)
					case "max":
						value = b.durations.Quantile(1) / float64(time.Millisecond)
					case "p50", "p90", "p95", "p99":
						pct := map[string]float64{"p50": .5, "p90": .9, "p95": .95, "p99": .99}[q.Aggregation]
						value = b.durations.Quantile(pct) / float64(time.Millisecond)
					}
				}
				if math.IsNaN(value) || math.IsInf(value, 0) {
					return out, errors.New("invalid merged value")
				}
				p.Value = &value
				p.Count = b.count
			}
			if coverage < 1 || invalid[i] || b.invalid || counted && rateUnsafe[i] {
				partial = true
			}
			series.Points = append(series.Points, p)
		}
		out.Series = append(out.Series, series)
	}
	if partial {
		out.Warnings = append(out.Warnings, "Collection coverage is partial in some buckets; unsupported, dropped or invalid evidence remains missing. Correlation pair coverage does not establish within-bucket completeness.")
	}
	if storage.IsNativeObservationRate(q.Metric) {
		out.Warnings = append(out.Warnings, "Merged rates require nonoverlapping source windows wholly contained in each bucket and complete collection coverage. Boundary-spanning or overlapping cohorts remain unavailable because their counts cannot be divided by the bucket span.")
	}
	out.Warnings = append(out.Warnings, "Completion-cohort buckets merge whole source distributions by publication end time. Source intervals are not split, and boundary attribution is approximate at source-window resolution. Percentiles are estimated from merged distributions, never averaged. History is limited to 1,000 retained publications.")
	return out, nil
}
