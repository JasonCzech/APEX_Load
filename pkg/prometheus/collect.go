package prometheus

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

type queryData struct {
	ResultType string          `json:"resultType"`
	Result     json.RawMessage `json:"result"`
}
type matrixSeries struct {
	Metric     map[string]string   `json:"metric"`
	Values     [][]json.RawMessage `json:"values"`
	Value      []json.RawMessage   `json:"value"`
	Histograms [][]json.RawMessage `json:"histograms"`
	Histogram  []json.RawMessage   `json:"histogram"`
}
type observation struct {
	labels      map[string]string
	samples     map[int64]*float64
	unsupported map[int64]bool
}

func stableHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum)
}
func identityLabels(values map[string]string, profile string, withoutName bool) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		if withoutName && k == "__name__" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	normalized := map[string]string{}
	for _, k := range keys {
		nk, v := k, values[k]
		if profile == BackendAzureManaged {
			nk = strings.ToLower(nk)
			v = strings.ToLower(v)
		}
		normalized[nk] = v
	}
	encoded, _ := json.Marshal(normalized)
	return string(encoded)
}
func seriesIdentity(values map[string]string, profile string) string {
	return stableHash(identityLabels(values, profile, false))
}
func (c *Client) safeLabels(values map[string]string, allowed []string) map[string]string {
	out := map[string]string{}
	set := map[string]bool{}
	for _, k := range allowed {
		set[k] = true
		if c.cfg.BackendProfile == BackendAzureManaged {
			set[strings.ToLower(k)] = true
		}
	}
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		nk := k
		if c.cfg.BackendProfile == BackendAzureManaged {
			nk = strings.ToLower(k)
		}
		if nk == "__name__" || len(nk) > 128 || sensitiveLabel(nk) || !labelRE.MatchString(nk) || allowed != nil && !set[nk] {
			continue
		}
		value := values[k]
		if c.cfg.BackendProfile == BackendAzureManaged {
			value = strings.ToLower(value)
		}
		out[nk] = c.safeText(value, 128)
		if len(out) >= 16 {
			break
		}
	}
	return out
}
func decodeSample(raw []json.RawMessage) (time.Time, *float64, error) {
	if len(raw) != 2 {
		return time.Time{}, nil, errors.New("invalid Prometheus sample tuple")
	}
	var ts json.Number
	if err := json.Unmarshal(raw[0], &ts); err != nil {
		return time.Time{}, nil, errors.New("invalid Prometheus sample timestamp")
	}
	seconds, err := ts.Float64()
	if err != nil || math.IsInf(seconds, 0) || math.IsNaN(seconds) || seconds < 1 || seconds > 253402300799 {
		return time.Time{}, nil, errors.New("invalid Prometheus sample timestamp")
	}
	t := time.UnixMilli(int64(math.Round(seconds * 1000))).UTC()
	var text string
	if err := json.Unmarshal(raw[1], &text); err != nil {
		return time.Time{}, nil, errors.New("invalid Prometheus numeric sample")
	}
	value, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return time.Time{}, nil, errors.New("invalid Prometheus numeric sample")
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return t, nil, nil
	}
	return t, &value, nil
}
func (c *Client) observations(e envelope, rangeQuery bool) ([]observation, bool, error) {
	var d queryData
	if err := decodeJSON(e.Data, &d); err != nil {
		return nil, false, err
	}
	truncated := hasTruncation(e.Warnings)
	if rangeQuery && d.ResultType != "matrix" || !rangeQuery && d.ResultType != "vector" && d.ResultType != "scalar" {
		return nil, false, errors.New("PromQL monitoring requires numeric matrix, vector or scalar output")
	}
	var series []matrixSeries
	if d.ResultType == "scalar" {
		var tuple []json.RawMessage
		if err := decodeJSON(d.Result, &tuple); err != nil {
			return nil, false, err
		}
		series = []matrixSeries{{Value: tuple, Metric: map[string]string{}}}
	} else if err := decodeJSON(d.Result, &series); err != nil {
		return nil, false, err
	}
	if len(series) > c.cfg.MaxSeries {
		series = series[:c.cfg.MaxSeries]
		truncated = true
	}
	out := []observation{}
	seen := map[string]bool{}
	for _, s := range series {
		if len(s.Metric) > 128 {
			return nil, false, errors.New("Prometheus label set exceeds identity budget")
		}
		folded := map[string]bool{}
		for k, v := range s.Metric {
			if len(k) > 256 || len(v) > 4096 {
				return nil, false, errors.New("Prometheus label identity exceeds bounds")
			}
			if c.cfg.BackendProfile == BackendAzureManaged {
				nk := strings.ToLower(k)
				if folded[nk] {
					return nil, false, errors.New("Azure case-insensitive label identity collision")
				}
				folded[nk] = true
			}
		}
		id := seriesIdentity(s.Metric, c.cfg.BackendProfile)
		if seen[id] {
			return nil, false, errors.New("Prometheus result contains duplicate series identities")
		}
		seen[id] = true
		o := observation{labels: s.Metric, samples: map[int64]*float64{}, unsupported: map[int64]bool{}}
		values := s.Values
		if len(s.Value) > 0 {
			values = append(values, s.Value)
		}
		for _, v := range values {
			ts, value, err := decodeSample(v)
			if err != nil {
				return nil, false, err
			}
			key := ts.UnixMilli()
			if _, ok := o.samples[key]; ok {
				return nil, false, errors.New("Prometheus result contains duplicate sample timestamps")
			}
			o.samples[key] = value
		}
		hist := s.Histograms
		if len(s.Histogram) > 0 {
			hist = append(hist, s.Histogram)
		}
		for _, h := range hist {
			if len(h) != 2 {
				return nil, false, errors.New("invalid native histogram sample")
			}
			ts, _, err := decodeSample([]json.RawMessage{h[0], json.RawMessage(`"0"`)})
			if err != nil {
				return nil, false, err
			}
			key := ts.UnixMilli()
			if _, ok := o.samples[key]; ok {
				return nil, false, errors.New("mixed samples share a timestamp")
			}
			o.samples[key] = nil
			o.unsupported[key] = true
		}
		out = append(out, o)
	}
	return out, truncated, nil
}
func gridStart(from, anchor time.Time, step time.Duration) time.Time {
	if anchor.IsZero() {
		anchor = from
	}
	delta := from.Sub(anchor)
	remainder := delta % step
	t := from
	if remainder > 0 {
		t = from.Add(step - remainder)
	} else if remainder < 0 {
		t = from.Add(-remainder)
	}
	return t.UTC()
}

// Collect preserves one immutable evaluation grid across chunks and overlaps.
// Missing evaluations produce gaps; cached lookback values are never assigned a
// fabricated source sample timestamp or bucket interval.
func (c *Client) Collect(ctx context.Context, s Selection, from, to time.Time, runID string) (Result, error) {
	s = s.Defaults()
	result := Result{Coverage: map[string]monitorprovider.Coverage{}, Capabilities: Capabilities{BackendProfile: c.cfg.BackendProfile}}
	if err := (JobConfig{c.cfg, s}).Validate(); err != nil {
		return result, err
	}
	from = from.UTC()
	to = to.UTC()
	if from.IsZero() || !to.After(from) || to.Sub(from) > 32*24*time.Hour || runID == "" || len(runID) > 256 {
		return result, errors.New("invalid Prometheus collection window")
	}
	ctx, cancel := c.boundedContext(ctx)
	defer cancel()
	step := time.Duration(s.StepSeconds) * time.Second
	anchor := from.Truncate(time.Millisecond)
	if anchor.Before(from) {
		anchor = anchor.Add(time.Millisecond)
	}
	if s.GridAnchor != nil {
		anchor = s.GridAnchor.UTC()
	}
	start := gridStart(from, anchor, step)
	total := len(s.Queries)
	if s.Targets {
		total++
	}
	if s.Alerts {
		total++
	}
	if s.QueryOffset > total {
		return result, errors.New("query cursor exceeds the collection plan")
	}
	result.Progress = monitorprovider.QueryProgress{NextQuery: s.QueryOffset, TotalQueries: total}
	for qi := s.QueryOffset; qi < len(s.Queries); qi++ {
		q := s.Queries[qi]
		rendered, renderErr := materializeQuery(q)
		if renderErr != nil {
			return result, renderErr
		}
		current := start
		if qi == s.QueryOffset && s.QueryTime != nil && s.QueryTime.After(current) {
			current = gridStart(*s.QueryTime, anchor, step)
		}
		coverage := monitorprovider.Coverage{Status: "complete", From: from, To: to, Freshness: "unknown"}
		observed := map[string]bool{}
		nativeHistogram, finiteNumber := false, false
		for !current.After(to) {
			// The point limit controls client memory even when a server ignores its
			// limit parameter. No upstream query exceeds 11000 evaluation intervals.
			remaining := c.cfg.MaxPoints - len(result.Points)
			chunkSteps := min(1000, remaining/c.cfg.MaxSeries)
			if chunkSteps < 1 {
				next := current
				result.Progress.NextQuery = qi
				result.Progress.NextTime = &next
				coverage.Status = "partial"
				coverage.Message = "Collection continues in the next accepted batch"
				result.Coverage[q.ID] = coverage
				return result, nil
			}
			end := current.Add(time.Duration(chunkSteps-1) * step)
			if end.After(to) {
				end = current.Add(to.Sub(current).Truncate(step))
			}
			params := url.Values{"query": {rendered.Expression}, "start": {unixSeconds(current)}, "end": {unixSeconds(end)}, "step": {strconv.Itoa(s.StepSeconds)}}
			if c.cfg.BackendProfile != BackendAzureManaged {
				params.Set("limit", strconv.Itoa(c.cfg.MaxSeries+1))
			}
			env, err := c.query(ctx, "/api/v1/query_range", params)
			if errors.Is(err, ErrBudget) || errors.Is(err, context.DeadlineExceeded) {
				next := current
				result.Progress.NextQuery = qi
				result.Progress.NextTime = &next
				coverage.Status = "partial"
				coverage.Message = "Collection budget exhausted; window remains uncommitted"
				result.Coverage[q.ID] = coverage
				return result, nil
			}
			if err != nil {
				coverage.Status = statusFor(err)
				coverage.Message = err.Error()
				result.Coverage[q.ID] = coverage
				return result, err
			}
			rows, truncated, err := c.observations(env, true)
			if err != nil {
				return result, err
			}
			if err := validateGridRows(rows, current, end, step); err != nil {
				return result, err
			}
			freshness := map[string]map[int64]*float64{}
			if q.FreshnessExpression != "" {
				fp := url.Values{}
				for k, v := range params {
					fp[k] = append([]string(nil), v...)
				}
				fp.Set("query", rendered.FreshnessExpression)
				fenv, err := c.query(ctx, "/api/v1/query_range", fp)
				if errors.Is(err, ErrBudget) || errors.Is(err, context.DeadlineExceeded) {
					next := current
					result.Progress.NextQuery = qi
					result.Progress.NextTime = &next
					coverage.Status = "partial"
					coverage.Message = "Freshness query awaits collection budget"
					result.Coverage[q.ID] = coverage
					return result, nil
				}
				if err != nil {
					return result, err
				}
				frows, ft, err := c.observations(fenv, true)
				if err != nil {
					return result, err
				}
				if err := validateGridRows(frows, current, end, step); err != nil {
					return result, err
				}
				truncated = truncated || ft
				for _, f := range frows {
					if err := c.validateResultLabels(f.labels, q.AllowedLabels); err != nil {
						return result, err
					}
					key := identityLabels(f.labels, c.cfg.BackendProfile, true)
					if _, exists := freshness[key]; exists {
						return result, errors.New("freshness companion contains ambiguous label identity")
					}
					freshness[key] = f.samples
				}
				env.Warnings = append(env.Warnings, fenv.Warnings...)
				env.Infos = append(env.Infos, fenv.Infos...)
			}
			coverage.Truncated = coverage.Truncated || truncated
			coverage.Warnings = appendBounded(coverage.Warnings, env.Warnings)
			coverage.Infos = appendBounded(coverage.Infos, env.Infos)
			nSteps := int(end.Sub(current)/step) + 1
			expected := nSteps * max(1, q.MinSeries, len(rows))
			coverage.ExpectedPoints += expected
			valid := 0
			known := false
			stale := false
			for _, row := range rows {
				if err := c.validateResultLabels(row.labels, q.AllowedLabels); err != nil {
					return result, err
				}
				sid := seriesIdentity(row.labels, c.cfg.BackendProfile)
				observed[sid] = true
				dims := c.safeLabels(row.labels, q.AllowedLabels)

				for eval := current; !eval.After(end); eval = eval.Add(step) {
					value, present := row.samples[eval.UnixMilli()]
					point := c.point(q, s, runID, eval, dims, value)
					point.Provider.DisplayLabels = c.displayLabels(row.labels, q.AllowedLabels)
					point.Provider.EntityIDs = map[string]string{"series": sid}
					if !present || value == nil {
						point.Quality = analysis.QualityMissing
						point.Value = nil
						point.Provider.AggregationEligible = false
					} else {
						valid++
						finiteNumber = true
					}
					if row.unsupported[eval.UnixMilli()] {
						nativeHistogram = true
						coverage.Warnings = appendBounded(coverage.Warnings, []string{"Native histogram samples require an explicit numeric PromQL projection"})
					}
					if f, ok := freshness[identityLabels(row.labels, c.cfg.BackendProfile, true)][eval.UnixMilli()]; ok && f != nil && *f >= 1 && *f <= 253402300799 {
						stamp := time.UnixMilli(int64(math.Round(*f * 1000))).UTC()
						if !stamp.After(eval.Add(30 * time.Second)) {
							point.Provider.UnderlyingSampleTime = &stamp
							point.Provider.FreshnessKnown = true
							known = true
							age := q.MaxSampleAgeSeconds
							if age == 0 {
								age = 90
							}
							if eval.Sub(stamp) > time.Duration(age)*time.Second {
								stale = true
								point.Provider.AggregationEligible = false
								if point.Value != nil {
									point.Quality = analysis.QualityStale
									valid--
								}
							}
						}
					}
					if err := point.Validate(); err != nil {
						return result, err
					}
					result.Points = append(result.Points, point)
				}
			}
			coverage.Points += len(rows) * nSteps
			coverage.MissingPoints += max(0, expected-valid)
			if known {
				coverage.Freshness = "fresh"
			}
			if stale {
				coverage.Freshness = "stale"
			}
			if len(rows) < max(1, q.MinSeries) {
				coverage.Status = "no_data"
				coverage.Message = "Query returned fewer series than required"
			}
			if truncated {
				coverage.Status = "partial"
				coverage.Message = "Series or upstream result limits truncated query coverage"
			}
			current = end.Add(step)
		}
		if nativeHistogram {
			coverage.Status = "partial"
			if !finiteNumber {
				coverage.Status = "unsupported"
			}
			coverage.Message = "Native histogram samples require an explicit numeric PromQL projection"
		} else if len(coverage.Warnings) > 0 && coverage.Status == "complete" {
			coverage.Status = "partial"
		}
		if len(observed) == 0 && coverage.ExpectedPoints == 0 {
			coverage.Status = "no_data"
			coverage.Message = "No evaluation grid points overlap this window"
		}
		result.Coverage[q.ID] = coverage
		result.Progress.NextQuery = qi + 1
		result.Progress.NextTime = nil
	}
	index := len(s.Queries)
	for _, diag := range []struct {
		enabled    bool
		name, path string
	}{{s.Targets, "targets", "/api/v1/targets"}, {s.Alerts, "alerts", "/api/v1/alerts"}} {
		if !diag.enabled {
			continue
		}
		if index < s.QueryOffset {
			index++
			continue
		}
		cov := monitorprovider.Coverage{Status: "complete", From: from, To: to}
		if c.cfg.BackendProfile == BackendAzureManaged {
			cov.Status = "unsupported"
			cov.Message = "Azure managed workspace does not expose this capability"

		} else {
			records, diagnosticCoverage, err := c.collectDiagnostics(ctx, s, from, to, runID, diag.name, diag.path)
			if errors.Is(err, ErrBudget) || errors.Is(err, context.DeadlineExceeded) {
				result.Progress.NextQuery = index
				result.Coverage[diag.name] = monitorprovider.Coverage{Status: "partial", From: from, To: to, Message: "Diagnostic query awaits collection budget"}
				return result, nil
			}
			if err != nil {
				cov.Status = statusFor(err)
				cov.Message = err.Error()
			} else {
				cov = diagnosticCoverage
				if len(result.Evidence)+len(records) > 100 {
					records = records[:max(0, 100-len(result.Evidence))]
					cov.Truncated = true
					cov.Status = "partial"
				}
				result.Evidence = append(result.Evidence, records...)
				cov.Evidence = len(records)
			}

		}
		result.Coverage[diag.name] = cov
		index++
		result.Progress.NextQuery = index
	}
	result.Progress.Complete = true
	result.Progress.NextTime = nil
	return result, nil
}
func (c *Client) point(q Query, s Selection, runID string, eval time.Time, dims map[string]string, value *float64) analysis.MetricPoint {
	semantics := QueryStatistic(q, false)
	settled := !eval.After(time.Now().Add(-time.Duration(s.SettleSeconds) * time.Second))
	quality := analysis.QualityValid
	if value == nil {
		quality = analysis.QualityMissing
	}
	return analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: s.ProjectID, RunID: runID, Source: "prometheus", TimestampUTC: eval.UTC(), ReceivedAt: time.Now().UTC(), MetricName: q.Name, Kind: analysis.KindGauge, Value: value, Unit: q.Unit, Dimensions: dims, Quality: quality, Provider: &analysis.ProviderMeasurement{Name: "prometheus", Mode: "evaluation_point", BackendProfile: c.cfg.BackendProfile, QueryID: q.ID, DefinitionHash: DefinitionHash(q), MetricID: q.ID, Statistic: semantics, EvaluationStepSeconds: s.StepSeconds, Settled: settled, AggregationEligible: settled && value != nil}}
}
func appendBounded(existing, extra []string) []string {
	seen := map[string]bool{}
	for _, v := range existing {
		seen[v] = true
	}
	for _, v := range extra {
		if len(existing) >= 20 {
			break
		}
		if !seen[v] {
			existing = append(existing, v)
			seen[v] = true
		}
	}
	return existing
}

// CollectTerminal executes explicit whole-window PromQL aggregates once at the
// terminal timestamp. It never averages percentile or rate timeline points.
func (c *Client) CollectTerminal(ctx context.Context, s Selection, from, to time.Time, runID string) (Result, error) {
	s = s.Defaults()
	r := Result{Coverage: map[string]monitorprovider.Coverage{}}
	if err := (JobConfig{c.cfg, s}).Validate(); err != nil {
		return r, err
	}
	from = from.UTC()
	to = to.UTC()
	duration := to.Sub(from)
	if from.IsZero() || duration <= 0 || duration > 32*24*time.Hour || runID == "" {
		return r, errors.New("invalid exact terminal evaluation window")
	}
	ctx, cancel := c.boundedContext(ctx)
	defer cancel()
	for _, q := range s.Queries {
		if q.TerminalExpression == "" {
			continue
		}
		rendered, renderErr := materializeQuery(q)
		if renderErr != nil {
			return r, renderErr
		}
		expression := strings.ReplaceAll(rendered.TerminalExpression, "{{window.duration}}", strconv.FormatInt(duration.Milliseconds(), 10)+"ms")
		e, err := c.query(ctx, "/api/v1/query", url.Values{"query": {expression}, "time": {unixSeconds(to)}, "limit": {strconv.Itoa(c.cfg.MaxSeries + 1)}})
		if err != nil {
			return r, err
		}
		rows, truncated, err := c.observations(e, false)
		if err != nil {
			return r, err
		}
		cov := monitorprovider.Coverage{Status: "complete", From: from, To: to, Truncated: truncated, ExpectedPoints: max(1, q.MinSeries, len(rows)), Warnings: e.Warnings, Infos: e.Infos, Freshness: "unknown"}
		freshness := map[string]*float64{}
		if q.FreshnessExpression != "" {
			fenv, err := c.query(ctx, "/api/v1/query", url.Values{"query": {rendered.FreshnessExpression}, "time": {unixSeconds(to)}, "limit": {strconv.Itoa(c.cfg.MaxSeries + 1)}})
			if err != nil {
				return r, err
			}
			frows, ft, err := c.observations(fenv, false)
			if err != nil {
				return r, err
			}
			cov.Truncated = cov.Truncated || ft
			cov.Warnings = appendBounded(cov.Warnings, fenv.Warnings)
			cov.Infos = appendBounded(cov.Infos, fenv.Infos)
			for _, f := range frows {
				if err := c.validateResultLabels(f.labels, q.AllowedLabels); err != nil {
					return r, err
				}
				key := identityLabels(f.labels, c.cfg.BackendProfile, true)
				if _, exists := freshness[key]; exists {
					return r, errors.New("freshness companion contains ambiguous label identity")
				}
				for ts, v := range f.samples {
					if ts != to.UnixMilli() {
						return r, errors.New("freshness result timestamp does not match terminal evaluation")
					}
					freshness[key] = v
				}
			}
		}

		for _, row := range rows {
			if err := c.validateResultLabels(row.labels, q.AllowedLabels); err != nil {
				return r, err
			}
			if len(row.samples) != 1 {
				return r, errors.New("terminal expression must return one value per series")
			}
			for ts, value := range row.samples {
				if ts != to.UnixMilli() {
					return r, errors.New("terminal result timestamp does not match the requested window")
				}
				dims := c.safeLabels(row.labels, q.AllowedLabels)

				point := c.point(q, s, runID, to, dims, value)
				point.Provider.DisplayLabels = c.displayLabels(row.labels, q.AllowedLabels)
				point.Provider.EntityIDs = map[string]string{"series": seriesIdentity(row.labels, c.cfg.BackendProfile)}
				point.Provider.Mode = "query_window"
				point.Provider.Statistic = QueryStatistic(q, true)
				point.Provider.QueryWindow = true
				point.Provider.BucketStart = from
				point.Provider.BucketEnd = to
				point.Provider.AggregationEligible = point.Provider.Settled && value != nil
				if fv := freshness[identityLabels(row.labels, c.cfg.BackendProfile, true)]; fv != nil && *fv >= 1 && *fv <= 253402300799 {
					stamp := time.UnixMilli(int64(math.Round(*fv * 1000))).UTC()
					if !stamp.After(to.Add(30 * time.Second)) {
						point.Provider.UnderlyingSampleTime = &stamp
						point.Provider.FreshnessKnown = true
						cov.Freshness = "fresh"
						age := q.MaxSampleAgeSeconds
						if age == 0 {
							age = 90
						}
						if to.Sub(stamp) > time.Duration(age)*time.Second {
							point.Provider.AggregationEligible = false
							cov.Freshness = "stale"
							if point.Value != nil {
								point.Quality = analysis.QualityStale
							}
						}
					}
				}
				if err := point.Validate(); err != nil {
					return r, err
				}
				if len(r.Points) >= c.cfg.MaxPoints {
					cov.Truncated = true
					break
				}
				r.Points = append(r.Points, point)
				cov.Points++
				if row.unsupported[ts] {
					cov.Status = "unsupported"
					cov.Warnings = appendBounded(cov.Warnings, []string{"Native histogram samples require an explicit numeric PromQL projection"})
				}
				if value == nil {
					cov.MissingPoints++
				}
			}
		}
		if len(rows) < max(1, q.MinSeries) {
			cov.Status = "no_data"
			cov.MissingPoints = max(0, cov.ExpectedPoints-cov.Points)
		}
		if cov.Truncated || len(cov.Warnings) > 0 && cov.Status == "complete" {
			cov.Status = "partial"
		}
		r.Coverage[q.ID] = cov
	}
	r.Progress = monitorprovider.QueryProgress{TotalQueries: len(s.Queries), NextQuery: len(s.Queries), Complete: true}
	return r, nil
}

// Every output label is part of the declared series identity. Dropping labels
// would silently merge unrelated observations, so administrators must aggregate
// away unwanted dimensions in PromQL or approve them explicitly.
func (c *Client) validateResultLabels(values map[string]string, allowed []string) error {
	permitted := map[string]bool{}
	for _, v := range allowed {
		if c.cfg.BackendProfile == BackendAzureManaged {
			v = strings.ToLower(v)
		}
		permitted[v] = true
	}
	seen := map[string]bool{}
	for k, v := range values {
		if k == "__name__" {
			if len(v) > 128 || strings.ContainsAny(v, "\r\n\x00") {
				return errors.New("Prometheus metric identity exceeds safe bounds")
			}
			continue
		}
		if c.cfg.BackendProfile == BackendAzureManaged {
			k = strings.ToLower(k)
			v = strings.ToLower(v)
		}
		if len(k) > 128 || !permitted[k] || seen[k] || sensitiveLabel(k) || !labelRE.MatchString(k) || len(v) > 128 || strings.ContainsAny(v, "\r\n\x00") || c.safeText(v, 128) != v {
			return errors.New("Prometheus result contains an unapproved or unsafe output label")
		}
		seen[k] = true
	}
	return nil
}

func validateGridRows(rows []observation, from, to time.Time, step time.Duration) error {
	for _, row := range rows {
		for ts := range row.samples {
			stamp := time.UnixMilli(ts).UTC()
			if stamp.Before(from) || stamp.After(to) || stamp.Sub(from)%step != 0 {
				return errors.New("Prometheus response is outside the requested evaluation grid")
			}
		}
	}
	return nil
}

// Azure identities are case insensitive, while retained display labels preserve
// the approved spelling returned by the backend. OSS identities are unchanged.
func (c *Client) displayLabels(values map[string]string, allowed []string) map[string]string {
	if c.cfg.BackendProfile != BackendAzureManaged {
		return nil
	}
	permitted := map[string]bool{}
	for _, name := range allowed {
		permitted[strings.ToLower(name)] = true
	}
	out := map[string]string{}
	for name, value := range values {
		if strings.EqualFold(name, "__name__") || !permitted[strings.ToLower(name)] {
			continue
		}
		out[name] = c.safeText(value, 128)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
