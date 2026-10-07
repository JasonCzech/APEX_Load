package elastic

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

var ErrTruncated = errors.New("bounded result is truncated")

type esqlResponse struct {
	ID      string `json:"id"`
	Running bool   `json:"is_running"`
	Partial bool   `json:"is_partial"`
	Columns []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"columns"`
	Values [][]any `json:"values"`
}

func esqlLimit(query string) int {
	parts, _ := splitPipeline(query)
	if len(parts) == 0 {
		return 0
	}
	words := strings.Fields(parts[len(parts)-1])
	if len(words) != 2 {
		return 0
	}
	n, _ := strconv.Atoi(words[1])
	return n
}

func (c *Client) collectESQL(ctx context.Context, s Selection, t task, from, to time.Time, runID string, result *Result) (next *Continuation, err error) {
	if c.cfg.Product != ProductElasticsearch || t.query == nil || t.kind != "metric" {
		return nil, errors.New("ES|QL requires an Elasticsearch metric query")
	}
	q := *t.query
	if q.Statistic == "count" || q.Statistic == "cardinality" {
		q.Unit = "count"
	}
	if q.Async && !result.Capabilities.AsyncESQL {
		return nil, errors.New("async ES|QL capability unavailable")
	}
	cursor := cursorFor(s, t, from, to)
	retained := false
	defer func() {
		if !retained {
			c.retainFailedCleanup(ctx, cursor, result)
		}
	}()
	var response esqlResponse
	if cursor.AsyncID != "" {
		err = c.request(ctx, "GET", "/_query/async/"+escapeHandle(cursor.AsyncID)+"?format=json&wait_for_completion_timeout=1s", nil, &response)
		var upstream *HTTPError
		if errors.As(err, &upstream) && upstream.Status == 404 {
			cursor.AsyncID = ""
			err = nil
		}
		if err != nil {
			return nil, err
		}
	}
	if cursor.AsyncID == "" {
		body := map[string]any{"query": q.ESQL, "filter": enforceFilter(s, from, to, runID, nil), "columnar": false}
		if len(q.Params) > 0 {
			body["params"] = q.Params
		}
		endpoint := "/_query?format=json"
		if q.Async {
			endpoint = "/_query/async?format=json"
			body["wait_for_completion_timeout"] = "1s"
			body["keep_alive"] = "2m"
			body["keep_on_completion"] = true
		}
		if err = c.request(ctx, "POST", endpoint, body, &response); err != nil {
			if q.Async && validOwnedHandle(response.ID) {
				cursor.AsyncID = response.ID
				cursor.KeepAliveUntil = time.Now().UTC().Add(2 * time.Minute)
			}
			return nil, err
		}
	}
	if response.ID != "" {
		if len(response.ID) > 16384 || strings.ContainsAny(response.ID, "\r\n\x00") {
			return nil, errors.New("invalid async query identity")
		}
		if cursor.AsyncID != "" && cursor.AsyncID != response.ID {
			return nil, errors.New("async query identity changed")
		}
		if cursor.AsyncID == "" {
			cursor.AsyncID = response.ID
			cursor.KeepAliveUntil = time.Now().UTC().Add(2 * time.Minute)
		}
	}
	if response.Running {
		if !q.Async || cursor.AsyncID == "" {
			return nil, errors.New("async response lacks owned query identity")
		}
		retained = true
		return cursor, nil
	}
	if response.Partial {
		return nil, errors.New("ES|QL returned partial results")
	}
	if len(response.Columns) == 0 || len(response.Columns) > 128 || len(response.Values) > 10000 {
		return nil, errors.New("ES|QL table exceeds bounds")
	}
	columns := map[string]int{}
	for i, column := range response.Columns {
		if column.Name == "" || len(column.Name) > 128 {
			return nil, errors.New("invalid ES|QL column identity")
		}
		if _, ok := columns[column.Name]; ok {
			return nil, errors.New("duplicate ES|QL column")
		}
		columns[column.Name] = i
		if column.Type == "unsupported" {
			return nil, errors.New("ES|QL returned conflicting or unsupported field types")
		}
	}
	for _, name := range append(append([]string{q.ValueColumn, q.CountColumn, q.SumColumn}, q.DimensionColumns...), q.GroupBy...) {
		if name != "" {
			if _, ok := columns[name]; !ok {
				return nil, fmt.Errorf("missing mapped ES|QL output column %q", name)
			}
		}
	}
	if !q.ExactWindow {
		if _, ok := columns[q.TimestampColumn]; !ok {
			return nil, errors.New("missing mapped ES|QL timestamp column")
		}
	}
	points := []analysis.MetricPoint{}
	series := map[string]bool{}
	seenRows := map[string]bool{}
	for _, row := range response.Values {
		if len(row) != len(response.Columns) {
			return nil, errors.New("invalid ES|QL row width")
		}
		value, err := mappedNumber(row[columns[q.ValueColumn]])
		if err != nil {
			return nil, err
		}
		var count, sum *float64
		if q.CountColumn != "" {
			count, err = mappedNumber(row[columns[q.CountColumn]])
			if err != nil {
				return nil, err
			}
			sum, err = mappedNumber(row[columns[q.SumColumn]])
			if err != nil {
				return nil, err
			}
			if count != nil && *count < 0 {
				return nil, errors.New("negative ES|QL weight")
			}
			if sum != nil {
				v := scaleValue(*sum, q.Unit)
				sum = &v
			}
		}
		if q.Statistic == "average" {
			value = nil
			if count != nil && sum != nil && *count > 0 {
				v := *sum / *count
				value = &v
			}
		} else if value != nil {
			v := scaleValue(*value, q.Unit)
			value = &v
		}
		rawDimensions := map[string]analysis.ScalarValue{}
		for _, name := range append(append([]string{}, q.DimensionColumns...), q.GroupBy...) {
			scalarValue, e := scalar(row[columns[name]])
			if e != nil {
				return nil, e
			}
			rawDimensions[name] = scalarValue
		}
		dimensions, err := c.mappedDimensions(t.id, q.Statistic, rawDimensions)
		if err != nil {
			return nil, err
		}
		identity := queryID(dimensions)
		series[identity] = true
		if len(series) > c.cfg.MaxSeries {
			return nil, ErrTruncated
		}
		at := from
		if !q.ExactWindow {
			var ok bool
			at, ok = timestamp(row[columns[q.TimestampColumn]])
			if !ok {
				return nil, errors.New("invalid ES|QL bucket timestamp")
			}
		}
		approximate := q.Approximate || approximateFunction.MatchString(q.ESQL) || strings.HasPrefix(q.Statistic, "p") || q.Statistic == "cardinality"
		var p analysis.MetricPoint
		if q.ExactWindow {
			p, err = c.windowPoint(s, t, from, to, runID, q.Statistic, q.Unit, value, count, sum, nil, nil, dimensions, approximate)
		} else {
			p, err = point(s, t, from, to, runID, at, q.BucketSeconds, q.Statistic, q.Unit, value, count, sum, nil, nil, dimensions, approximate)
			p.Source = c.cfg.Product
			p.Provider.Name = c.cfg.Product
		}
		if err != nil {
			return nil, err
		}
		if err = p.Validate(); err != nil {
			return nil, err
		}
		identity = metricPointKey(p)
		if seenRows[identity] {
			return nil, errors.New("ES|QL mappings collapse distinct output rows")
		}
		seenRows[identity] = true
		points = append(points, p)
	}
	available := max(0, c.cfg.MaxPoints-len(result.Points))
	for _, p := range points[:min(available, len(points))] {
		if err := c.appendPoint(result, p); err != nil {
			return nil, err
		}
	}
	if len(points) > available || esqlMayTruncate(q) && len(response.Values) >= esqlLimit(q.ESQL) {
		return nil, ErrTruncated
	}
	return nil, nil
}

var esqlGrouping = regexp.MustCompile(`(?i)\bBY\b`)
var approximateFunction = regexp.MustCompile(`(?i)\b(?:PERCENTILE|COUNT_DISTINCT)\s*\(`)

func esqlMayTruncate(q Query) bool {
	plain, _ := stripStrings(q.ESQL)
	return esqlGrouping.MatchString(plain)
}

// mappedNumber never quietly rounds an integer outside IEEE-754's exact range.
// Projected integer attributes retain their decimal representation instead.
func mappedNumber(v any) (*float64, error) {
	if v == nil {
		return nil, nil
	}
	n, ok := number(v)
	if !ok {
		return nil, errors.New("mapped metric value must be finite numeric data")
	}
	if math.Trunc(n) == n && math.Abs(n) > 1<<53 {
		return nil, errors.New("mapped integer cannot be represented precisely")
	}
	return &n, nil
}

func (c *Client) windowPoint(s Selection, t task, from, to time.Time, runID, stat, unit string, value, count, sum, minimum, maximum *float64, dimensions map[string]string, approximate bool) (analysis.MetricPoint, error) {
	quality := analysis.QualityValid
	if value == nil {
		quality = analysis.QualityMissing
	}
	settled := !to.After(time.Now().Add(-time.Duration(s.SettleSeconds) * time.Second))
	eligible := settled && value != nil && !approximate
	if stat == "average" {
		eligible = eligible && count != nil && *count > 0 && sum != nil
	}
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: s.ProjectID, RunID: runID, Source: c.cfg.Product, TimestampUTC: from, ReceivedAt: time.Now().UTC(), MetricName: t.name, Kind: analysis.KindGauge, Value: value, Unit: normalizedUnit(unit), Quality: quality, Dimensions: dimensions, Provider: &analysis.ProviderMeasurement{Name: c.cfg.Product, MetricID: t.id, QueryID: t.id, WindowKind: "query_window", QueryWindow: true, BucketStart: from, BucketEnd: to, ResolutionSeconds: int(math.Ceil(to.Sub(from).Seconds())), Statistic: stat, Count: count, Sum: sum, Min: minimum, Max: maximum, Settled: settled, AggregationEligible: eligible, Approximate: approximate}}
	return p, p.Validate()
}
