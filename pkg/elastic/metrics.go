package elastic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func metricFor(t task) MetricSelection {
	if t.metric != nil {
		return *t.metric
	}
	q := t.query
	return MetricSelection{Name: t.name, Field: q.ValueColumn, Statistic: q.Statistic, Unit: q.Unit, Kind: "gauge", BucketSeconds: q.BucketSeconds, GroupBy: q.GroupBy, SumField: q.SumColumn, CountField: q.CountColumn, ExactWindow: q.ExactWindow}
}
func metricAggs(m MetricSelection) map[string]any {
	a := map[string]any{}
	if m.Statistic == "cardinality" {
		a["cardinality"] = map[string]any{"cardinality": map[string]any{"field": m.Field}}
		return a
	}
	if m.Statistic == "count" && m.Field != "" {
		a["count"] = map[string]any{"value_count": map[string]any{"field": m.Field}}
		return a
	}
	if m.SumField != "" {
		a["sum"] = map[string]any{"sum": map[string]any{"field": m.SumField}}
		a["count"] = map[string]any{"sum": map[string]any{"field": m.CountField}}
	} else if m.Field != "" {
		for _, name := range []string{"sum", "min", "max"} {
			a[name] = map[string]any{name: map[string]any{"field": m.Field}}
		}
		a["count"] = map[string]any{"value_count": map[string]any{"field": m.Field}}
	}
	if strings.HasPrefix(m.Statistic, "p") {
		pct, _ := strconv.Atoi(strings.TrimPrefix(m.Statistic, "p"))
		a["percentile"] = map[string]any{"percentiles": map[string]any{"field": m.Field, "percents": []int{pct}}}
	}
	if m.Statistic == "cardinality" {
		a["cardinality"] = map[string]any{"cardinality": map[string]any{"field": m.Field}}
	}
	return a
}
func metricSources(s Selection, m MetricSelection) []any {
	sources := []any{}
	if !m.ExactWindow {
		sources = append(sources, map[string]any{"timestamp": map[string]any{"date_histogram": map[string]any{"field": s.TimestampField, "fixed_interval": fmt.Sprintf("%ds", m.BucketSeconds), "time_zone": "UTC", "order": "asc"}}})
	}
	for i, f := range m.GroupBy {
		sources = append(sources, map[string]any{fmt.Sprintf("d%d", i): map[string]any{"terms": map[string]any{"field": f, "order": "asc", "missing_bucket": true}}})
	}
	return sources
}

type aggregatePage struct {
	AfterKey map[string]any `json:"after_key"`
	Buckets  []struct {
		Key         map[string]any       `json:"key"`
		DocCount    json.Number          `json:"doc_count"`
		Count       aggregateNumber      `json:"count"`
		Sum         aggregateNumber      `json:"sum"`
		Min         aggregateNumber      `json:"min"`
		Max         aggregateNumber      `json:"max"`
		Cardinality aggregateNumber      `json:"cardinality"`
		Percentile  aggregatePercentiles `json:"percentile"`
	} `json:"buckets"`
}

// Decode source integers before converting to float64; otherwise encoding/json
// rounds 2^53+1 down to 2^53 before a later bounds check can detect the loss.
type aggregateNumber struct {
	Value *float64 `json:"value"`
}

func (a *aggregateNumber) UnmarshalJSON(data []byte) error {
	var value struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	number, err := decodeMappedNumber(value.Value)
	a.Value = number
	return err
}
func decodeMappedNumber(data []byte) (*float64, error) {
	if len(data) == 0 || string(data) == "null" {
		return nil, nil
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return mappedNumber(value)
}

type aggregatePercentiles struct {
	Values map[string]*float64 `json:"values"`
}

func (a *aggregatePercentiles) UnmarshalJSON(data []byte) error {
	var raw struct {
		Values map[string]json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	a.Values = map[string]*float64{}
	for key, value := range raw.Values {
		number, err := decodeMappedNumber(value)
		if err != nil {
			return err
		}
		a.Values[key] = number
	}
	return nil
}

func (c *Client) collectMetric(ctx context.Context, s Selection, t task, from, to time.Time, runID string, r *Result) (next *Continuation, err error) {
	m := metricFor(t)
	if m.ExactWindow && len(m.GroupBy) == 0 {
		return c.collectExactMetric(ctx, s, t, from, to, runID, r)
	}
	cursor := cursorFor(s, t, from, to)
	retained := false
	defer func() {
		if !retained {
			c.retainFailedCleanup(ctx, cursor, r)
		}
	}()
	if cursor.PITID == "" {
		cursor.PITID, err = c.openPIT(ctx, s.Indices)
		if cursor.PITID != "" {
			cursor.KeepAliveUntil = time.Now().UTC().Add(2 * time.Minute)
		}
		if err != nil {
			return nil, err
		}
	}
	restarted := false
	seen := map[string]int{}
	for i, p := range r.Points {
		seen[metricPointKey(p)] = i
	}
	series := map[string]bool{}
	for {
		remaining := c.cfg.MaxPoints - len(r.Points)
		if remaining <= 0 {
			retained = true
			return cursor, nil
		}
		composite := map[string]any{"size": min(s.PageSize, remaining), "sources": metricSources(s, m)}
		if len(cursor.CompositeAfter) > 0 {
			composite["after"] = scalarMap(cursor.CompositeAfter)
		}
		body := map[string]any{"size": 0, "track_total_hits": false, "pit": map[string]any{"id": cursor.PITID, "keep_alive": pitKeepAlive}, "query": enforceFilter(s, from, to, runID, taskDSL(t)), "aggs": map[string]any{"series": map[string]any{"composite": composite, "aggs": metricAggs(m)}}, "timeout": fmt.Sprintf("%ds", c.cfg.TimeoutSeconds)}
		var response searchResponse
		if e := c.searchPIT(ctx, cursor, body, &response); e != nil {
			var upstream *HTTPError
			if !restarted && errors.As(e, &upstream) && upstream.Status == 404 {
				restarted = true
				err = c.restartPIT(ctx, s.Indices, cursor, r)
				if errors.Is(err, ErrBudget) {
					retained = true
					return cursor, nil
				}
				if err != nil {
					return nil, err
				}
				continue
			}
			if errors.Is(e, ErrBudget) {
				retained = true
				return cursor, nil
			}
			return nil, e
		}
		if e := checkSearch(response); e != nil {
			return nil, e
		}
		var page aggregatePage
		d := json.NewDecoder(strings.NewReader(string(response.Aggregations["series"])))
		d.UseNumber()
		if e := d.Decode(&page); e != nil {
			return nil, errors.New("invalid metric aggregation response")
		}
		if len(page.Buckets) > min(s.PageSize, remaining) || len(page.AfterKey) > 5 {
			return nil, errors.New("metric page exceeds controlled pagination bounds")
		}
		// Validate the complete page before committing it, so an invalid row can
		// never advance an authoritative composite after_key past unseen data.
		points := []analysis.MetricPoint{}
		pageIdentities := map[string]bool{}
		for _, b := range page.Buckets {
			at := from
			if !m.ExactWindow {
				var ok bool
				at, ok = timestamp(b.Key["timestamp"])
				if !ok {
					return nil, errors.New("invalid metric bucket timestamp")
				}
			}
			rawDimensions := map[string]analysis.ScalarValue{}
			for i, f := range m.GroupBy {
				v := b.Key[fmt.Sprintf("d%d", i)]
				sv, e := scalar(v)
				if e != nil {
					return nil, e
				}
				rawDimensions[f] = sv
			}
			dims, dimensionErr := c.mappedDimensions(t.id, m.Statistic, rawDimensions)
			if dimensionErr != nil {
				return nil, dimensionErr
			}
			series[queryID(dims)] = true
			if len(series) > c.cfg.MaxSeries {
				return nil, ErrTruncated
			}
			count, sum, minValue, maxValue := b.Count.Value, b.Sum.Value, b.Min.Value, b.Max.Value
			if count == nil && m.Field == "" {
				var numericErr error
				count, numericErr = mappedNumber(b.DocCount)
				if numericErr != nil {
					return nil, numericErr
				}
			}
			for _, p := range []*float64{count, sum, minValue, maxValue} {
				if p != nil && (*p > 1<<53 || *p < -(1<<53)) && math.Trunc(*p) == *p {
					return nil, errors.New("metric integer exceeds precision bounds")
				}
				if p != nil && !finite(*p) {
					return nil, errors.New("nonfinite metric aggregate")
				}
			}
			if count != nil && *count < 0 {
				return nil, errors.New("negative metric count")
			}
			scale := func(p *float64) *float64 {
				if p == nil {
					return nil
				}
				n := scaleValue(*p, m.Unit)
				return &n
			}
			sum, minValue, maxValue = scale(sum), scale(minValue), scale(maxValue)
			var value *float64
			switch m.Statistic {
			case "count":
				value = count
			case "sum":
				if count != nil && *count > 0 {
					value = sum
				}
			case "min":
				value = minValue
			case "max":
				value = maxValue
			case "average":
				if count != nil && sum != nil && *count > 0 {
					n := *sum / *count
					value = &n
				}
			case "p50", "p90", "p95", "p99":
				key := strings.TrimPrefix(m.Statistic, "p") + ".0"
				value = scale(b.Percentile.Values[key])
			case "cardinality":
				value = b.Cardinality.Value
			}
			var p analysis.MetricPoint
			var e error
			if m.ExactWindow {
				p, e = c.windowPoint(s, t, from, to, runID, m.Statistic, m.Unit, value, count, sum, minValue, maxValue, dims, strings.HasPrefix(m.Statistic, "p") || m.Statistic == "cardinality")
			} else {
				p, e = point(s, t, from, to, runID, at, m.BucketSeconds, m.Statistic, m.Unit, value, count, sum, minValue, maxValue, dims, strings.HasPrefix(m.Statistic, "p") || m.Statistic == "cardinality")
			}
			if e != nil {
				return nil, e
			}
			p.Source = c.cfg.Product
			p.Provider.Name = c.cfg.Product
			if m.Kind == analysis.KindCounter {
				p.InputKind = analysis.KindCounter
				p.Provider.AggregationEligible = false
			}
			if m.Statistic == "count" || m.Statistic == "cardinality" {
				p.Unit = "count"
			}
			if e := p.Validate(); e != nil {
				return nil, e
			}
			key := metricPointKey(p)
			if index, ok := seen[key]; ok {
				r.Points[index] = p
			} else {
				if pageIdentities[key] {
					return nil, errors.New("duplicate metric bucket identity in source page")
				}
				pageIdentities[key] = true
				points = append(points, p)
			}
		}
		for _, p := range points {
			seen[metricPointKey(p)] = len(r.Points)
			if err := c.appendPoint(r, p); err != nil {
				return nil, err
			}
		}
		if len(page.Buckets) == 0 || len(page.AfterKey) == 0 {
			return nil, nil
		}
		after := map[string]analysis.ScalarValue{}
		for k, v := range page.AfterKey {
			sv, e := scalar(v)
			if e != nil {
				return nil, e
			}
			after[k] = sv
		}
		if cursorEqual(cursor.CompositeAfter, after) {
			return nil, errors.New("metric cursor did not advance")
		}
		cursor.CompositeAfter = after
	}
}
func scalarText(s analysis.ScalarValue) string {
	switch s.Kind {
	case "string", "integer":
		return s.StringValue
	case "null":
		return ""
	case "number":
		if s.NumberValue != nil {
			return fmt.Sprint(*s.NumberValue)
		}
	case "boolean":
		if s.BoolValue != nil {
			return fmt.Sprint(*s.BoolValue)
		}
	}
	return ""
}
func cursorEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (c *Client) collectExactMetric(ctx context.Context, s Selection, t task, from, to time.Time, runID string, r *Result) (*Continuation, error) {
	m := metricFor(t)
	body := map[string]any{"size": 0, "_source": false, "track_total_hits": true, "query": enforceFilter(s, from, to, runID, taskDSL(t)), "aggs": metricAggs(m), "timeout": fmt.Sprintf("%ds", c.cfg.TimeoutSeconds)}
	var response struct {
		TimedOut bool           `json:"timed_out"`
		Shards   responseShards `json:"_shards"`
		Hits     struct {
			Total struct {
				Value    json.Number `json:"value"`
				Relation string      `json:"relation"`
			} `json:"total"`
		} `json:"hits"`
		Aggregations map[string]struct {
			Value  any            `json:"value"`
			Values map[string]any `json:"values"`
		} `json:"aggregations"`
	}
	if err := c.request(ctx, "POST", "/"+indexPath(s.Indices)+"/_search?allow_partial_search_results=false", body, &response); err != nil {
		return nil, err
	}
	if response.TimedOut || response.Shards.partial() {
		return nil, errors.New("exact metric query returned partial results")
	}
	read := func(name string) (*float64, error) { return mappedNumber(response.Aggregations[name].Value) }
	count, err := read("count")
	if err != nil {
		return nil, err
	}
	sum, err := read("sum")
	if err != nil {
		return nil, err
	}
	minimum, err := read("min")
	if err != nil {
		return nil, err
	}
	maximum, err := read("max")
	if err != nil {
		return nil, err
	}
	if m.Statistic == "count" && m.Field == "" {
		if response.Hits.Total.Relation != "eq" {
			return nil, errors.New("document count is not exact")
		}
		count, err = mappedNumber(response.Hits.Total.Value)
		if err != nil {
			return nil, err
		}
	}
	if count != nil && *count < 0 {
		return nil, errors.New("negative metric weight")
	}
	scale := func(value *float64) *float64 {
		if value == nil {
			return nil
		}
		v := scaleValue(*value, m.Unit)
		return &v
	}
	sum, minimum, maximum = scale(sum), scale(minimum), scale(maximum)
	var value *float64
	switch m.Statistic {
	case "count":
		value = count
	case "sum":
		if count != nil && *count > 0 {
			value = sum
		}
	case "min":
		value = minimum
	case "max":
		value = maximum
	case "average":
		if count != nil && sum != nil && *count > 0 {
			v := *sum / *count
			value = &v
		}
	case "cardinality":
		value, err = read("cardinality")
	case "p50", "p90", "p95", "p99":
		value, err = mappedNumber(response.Aggregations["percentile"].Values[strings.TrimPrefix(m.Statistic, "p")+".0"])
		value = scale(value)
	}
	if err != nil {
		return nil, err
	}
	unit := m.Unit
	if m.Statistic == "count" || m.Statistic == "cardinality" {
		unit = "count"
	}
	p, err := c.windowPoint(s, t, from, to, runID, m.Statistic, unit, value, count, sum, minimum, maximum, map[string]string{"query_id": t.id, "statistic": m.Statistic}, strings.HasPrefix(m.Statistic, "p") || m.Statistic == "cardinality")
	if err != nil {
		return nil, err
	}
	if m.Kind == analysis.KindCounter {
		p.InputKind = analysis.KindCounter
		p.Provider.AggregationEligible = false
	}
	if len(r.Points) >= c.cfg.MaxPoints {
		return nil, ErrTruncated
	}
	if err := c.appendPoint(r, p); err != nil {
		return nil, err
	}
	return nil, nil
}

func metricPointKey(p analysis.MetricPoint) string {
	return queryID([]any{p.MetricName, p.TimestampUTC, p.Dimensions})
}
