package elastic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"math/big"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jasonczech/apexload/pkg/analysis"
)

type task struct {
	id, name, kind, cursorID string
	metric                   *MetricSelection
	query                    *Query
	health                   string
	from, to                 time.Time
}
type MetricDefinition struct {
	Name      string `json:"name"`
	QueryID   string `json:"query_id"`
	Unit      string `json:"unit,omitempty"`
	Statistic string `json:"statistic"`
	Kind      string `json:"kind"`
}

func queryID(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}
func selectionTasks(s Selection) []task {
	out := []task{}
	// Query identity includes approved source and mapping semantics. It excludes
	// transient execution cursors and dynamically chosen terminal windows.
	scope := s
	scope.Metrics = nil
	scope.Queries = nil
	scope.QueryOffset = 0
	scope.Continuation = nil
	scope.TerminalWindows = nil
	scope.DisabledQueries = nil
	scope.LastHealthAt = time.Time{}
	scope.HealthCoverage = nil
	scope.FinalHealthAfter = time.Time{}
	identity := func(value any) string { return queryID([]any{scope, value}) }
	for i := range s.Metrics {
		m := s.Metrics[i]
		out = append(out, task{id: identity(m), name: m.Name, kind: "metric", metric: &m})
	}
	for i := range s.Queries {
		q := s.Queries[i]
		out = append(out, task{id: identity(q), name: q.Name, kind: q.Kind, query: &q})
	}
	for _, v := range []struct {
		enabled bool
		kind    string
	}{{s.Logs, "log"}, {s.Traces, "span"}, {s.Transactions, "transaction"}, {s.Errors, "error"}} {
		if v.enabled {
			out = append(out, task{id: identity("default:" + v.kind), name: v.kind, kind: v.kind})
		}
	}
	for _, v := range []struct {
		enabled bool
		kind    string
	}{{s.ClusterHealth, "cluster"}, {s.KibanaHealth, "kibana"}, {s.LogstashHealth, "logstash"}} {
		if v.enabled {
			out = append(out, task{id: identity("health:" + v.kind), name: v.kind, kind: "health", health: v.kind})
		}
	}
	for _, w := range s.TerminalWindows {
		for _, base := range append([]task{}, out...) {
			if base.name == w.Metric && base.kind == "metric" && base.from.IsZero() {
				copy := base
				copy.from = w.FromUTC
				copy.to = w.UntilUTC
				copy.cursorID = queryID(w)
				if copy.metric != nil {
					m := *copy.metric
					m.ExactWindow = true
					copy.metric = &m
				} else if copy.query != nil {
					q := *copy.query
					q.ExactWindow = true
					copy.query = &q
				}
				out = append(out, copy)
				break
			}
		}
	}
	return out
}
func taskIdentity(t task) string {
	if t.cursorID != "" {
		return t.cursorID
	}
	return t.id
}

// QuerySignalDefinitions exposes stable approved query identities for ingestion.
func QuerySignalDefinitions(s Selection) []MetricDefinition {
	out := []MetricDefinition{}
	for _, t := range selectionTasks(s.Defaults()) {
		out = append(out, MetricDefinition{Name: t.name, QueryID: t.id, Kind: t.kind})
	}
	return out
}
func QueryDefinitions(s Selection) []MetricDefinition { return QuerySignalDefinitions(s) }
func HasIndexedSignals(s Selection) bool {
	return s.Logs || s.Traces || s.Transactions || s.Errors || len(s.Metrics) > 0 || len(s.Queries) > 0
}

func (c *Client) appendPoint(result *Result, p analysis.MetricPoint) error {
	if result.series == nil {
		result.series = map[string]bool{}
		for _, existing := range result.Points {
			result.series[queryID([]any{existing.MetricName, existing.Dimensions})] = true
		}
	}
	key := queryID([]any{p.MetricName, p.Dimensions})
	if !result.series[key] && len(result.series) >= c.cfg.MaxSeries {
		return ErrTruncated
	}
	if len(result.Points) >= c.cfg.MaxPoints {
		return ErrBudget
	}
	result.series[key] = true
	result.Points = append(result.Points, p)
	return nil
}
func QueryMetricDefinitions(s Selection) []MetricDefinition {
	s = s.Defaults()
	out := []MetricDefinition{}
	for _, t := range selectionTasks(s) {
		if t.metric != nil {
			m := t.metric
			out = append(out, MetricDefinition{Name: m.Name, QueryID: t.id, Unit: metricUnit(m.Unit, m.Statistic), Statistic: m.Statistic, Kind: m.Kind})
		} else if t.query != nil && t.kind == "metric" {
			q := t.query
			out = append(out, MetricDefinition{Name: q.Name, QueryID: t.id, Unit: metricUnit(q.Unit, q.Statistic), Statistic: q.Statistic, Kind: "gauge"})
		}
	}
	return out
}
func metricUnit(unit, stat string) string {
	if stat == "count" || stat == "cardinality" {
		return "count"
	}
	return normalizedUnit(unit)
}
func normalizedUnit(unit string) string {
	switch unit {
	case "ns", "us", "s":
		return "ms"
	}
	return unit
}
func scaleValue(v float64, unit string) float64 {
	switch unit {
	case "ns":
		return v / 1e6
	case "us":
		return v / 1e3
	case "s":
		return v * 1e3
	}
	return v
}
func safeReason(e error) string {
	if e == nil {
		return ""
	}
	if errors.Is(e, ErrBudget) {
		return "request_budget"
	}
	if errors.Is(e, ErrTruncated) {
		return "result_limit"
	}
	if errors.Is(e, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(e, context.DeadlineExceeded) {
		return "timeout"
	}
	var h *HTTPError
	if errors.As(e, &h) {
		return h.Kind
	}
	return "invalid_or_unavailable"
}
func (c *Client) Preview(ctx context.Context, s Selection, from, to time.Time, runID string) (Result, error) {
	cfg := c.cfg
	cfg.MaxPoints = min(cfg.MaxPoints, 100)
	cfg.MaxEvidence = min(cfg.MaxEvidence, 100)
	copy := newClient(cfg, c.credentials, c.http)
	copy.rate = c.rate
	copy.componentHTTP = c.componentHTTP
	copy.capabilities = c.capabilitiesSnapshot()
	copy.verificationPolicy = c.verificationPolicySnapshot()
	copy.pendingCleanup = c.PendingCleanupContinuations()
	result, err := copy.Collect(ctx, s, from, to, runID)
	if result.Continuation != nil {
		copy.retainFailedCleanup(ctx, result.Continuation, &result)
		result.Continuation = nil
		result.Progress.Continuation = nil
	}
	return result, err
}

// Collect returns a bounded prefix with a private continuation when a result or
// request budget ends. The caller must persist the prefix and cursor atomically.
func (c *Client) Collect(ctx context.Context, s Selection, from, to time.Time, runID string) (r Result, collectionErr error) {
	s = s.Defaults()
	r = Result{Points: []analysis.MetricPoint{}, Evidence: []Evidence{}, Coverage: map[string]Coverage{}, Capabilities: c.capabilitiesSnapshot()}
	defer func() {
		r.CleanupContinuations = append(r.CleanupContinuations, c.PendingCleanupContinuations()...)
		for _, owned := range r.CleanupContinuations {
			owned.WindowFrom, owned.WindowUntil = from.UTC(), to.UTC()
		}
	}()
	if e := s.Validate(); e != nil {
		return r, e
	}
	from, to = from.UTC(), to.UTC()
	if from.IsZero() || !to.After(from) || to.Sub(from) > time.Duration(s.MaxWindowSeconds)*time.Second || from.Year() < 1 || to.Year() > 9999 || len(runID) > 256 || strings.ContainsAny(runID, "\r\n\x00") {
		return r, errors.New("invalid bounded collection window")
	}
	if s.RunIDField != "" && runID == "" {
		return r, errors.New("run-scoped collection requires a run ID")
	}
	tasks := selectionTasks(s)
	policy := c.verificationPolicySnapshot()
	r.Progress.TotalQueries = len(tasks)
	if s.QueryOffset > len(tasks) {
		return r, errors.New("invalid query continuation offset")
	}
	start := s.QueryOffset
	r.ResolvedIndices = append([]string{}, s.ResolvedIndices...)
	if s.Continuation != nil {
		r.ResolvedIndices = append([]string{}, s.Continuation.ResolvedIndices...)
		if !s.Continuation.WindowFrom.Equal(from) || !s.Continuation.WindowUntil.Equal(to) {
			c.retainFailedCleanup(ctx, s.Continuation, &r)
			return r, errors.New("continuation window mismatch")
		}
		found := false
		for i, t := range tasks {
			if taskIdentity(t) == s.Continuation.QueryID {
				start = i
				found = true
				break
			}
		}
		if !found {
			c.retainFailedCleanup(ctx, s.Continuation, &r)
			return r, errors.New("continuation query mismatch")
		}
	}
	parent := ctx
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	if r.Capabilities.Version == "" {
		cap, e := c.Verify(ctx, s.Indices)
		r.Capabilities = cap
		if e != nil {
			return r, e
		}
	}
	for i := start; i < len(tasks); i++ {
		t := tasks[i]
		if !c.cleanupCapacity(&r, 1) {
			if s.Continuation != nil && s.Continuation.QueryID == taskIdentity(t) {
				copy := *s.Continuation
				r.Continuation = &copy
			} else {
				r.Continuation = &Continuation{QueryID: taskIdentity(t), ResolvedIndices: append([]string{}, r.ResolvedIndices...), WindowFrom: from, WindowUntil: to}
			}
			r.Progress.Continuation = r.Continuation
			r.Progress.NextQuery = i
			return r, nil
		}
		if s.DisabledQueries[t.id] {
			if s.Continuation != nil && s.Continuation.QueryID == taskIdentity(t) {
				c.retainFailedCleanup(ctx, s.Continuation, &r)
			}
			r.Progress.NextQuery = i + 1
			r.Coverage[taskIdentity(t)] = Coverage{QueryID: t.id, Status: "truncated", From: from, To: to, Truncated: true, Reason: "retention_quota", Message: "query stopped at retention quota"}
			continue
		}
		if prior, reuse := c.reuseHealthCoverage(s, t, from, to, time.Now().UTC()); reuse && policy.Health {
			r.Coverage[taskIdentity(t)] = prior
			r.Progress.NextQuery = i + 1
			continue
		}
		taskFrom, taskTo := from, to
		if !t.from.IsZero() {
			taskFrom, taskTo = t.from, t.to
		}
		beforeP, beforeE := len(r.Points), len(r.Evidence)
		coverage := Coverage{QueryID: t.id, Status: "complete", From: taskFrom, To: taskTo, LateArrivalSeconds: s.OverlapSeconds}
		if s.CorrelationLimitReached {
			coverage.Reason = "correlation_limit"
			coverage.Message = "observed trace-ID set reached its bound; unmatched records retain service/time association"
		}
		var cursor *Continuation
		var e error
		var healthObservedAt time.Time
		if t.health != "" {
			// A health task may make several component requests. Its coverage
			// cannot establish a post-settlement snapshot if any reading began
			// before the cutoff, even when the task finishes after it.
			healthObservedAt = time.Now().UTC()
			if !policy.Health {
				e = &HTTPError{Status: 403, Kind: "locally_disabled"}
			} else {
				e = c.collectHealth(ctx, s, t, taskFrom, taskTo, runID, &r)
			}
		} else if t.query != nil && t.query.Language == "esql" {
			if !r.Capabilities.ESQL {
				e = errors.New("ES|QL unavailable for selected endpoint")
			} else {
				cursor, e = c.collectESQL(ctx, s, t, taskFrom, taskTo, runID, &r)
			}
		} else if t.kind == "metric" {
			cursor, e = c.collectMetric(ctx, s, t, taskFrom, taskTo, runID, &r)
		} else {
			cursor, e = c.collectRecords(ctx, s, t, taskFrom, taskTo, runID, &r)
		}
		coverage.Points = len(r.Points) - beforeP
		coverage.Evidence = len(r.Evidence) - beforeE
		now := time.Now().UTC()
		coverage.AsOf = &now
		if !healthObservedAt.IsZero() {
			coverage.AsOf = &healthObservedAt
		}
		if cursor == nil && (errors.Is(e, ErrBudget) || errors.Is(e, context.DeadlineExceeded) && !errors.Is(parent.Err(), context.Canceled)) {
			// Providers have released any rejected/unfinished owned handle.
			// Restart the same query window and deduplicate accepted prefixes.
			cursor = &Continuation{QueryID: taskIdentity(t), ResolvedIndices: append([]string{}, r.ResolvedIndices...), WindowFrom: from, WindowUntil: to}
			e = nil
		}
		if cursor != nil {
			// A terminal task queries a frozen subinterval but its durable cursor
			// belongs to the outer collection pass. The immutable task identity
			// selects that exact subinterval again when collection resumes.
			cursor.WindowFrom, cursor.WindowUntil = from, to
			coverage.Status = "partial"
			coverage.Reason = "budget"
			coverage.Message = "bounded prefix retained; collection will continue"
			r.Continuation = cursor
			r.Progress.Continuation = cursor
			r.Progress.NextQuery = i
			r.Coverage[taskIdentity(t)] = coverage
			return r, nil
		}
		if e != nil {
			coverage.Status = "unavailable"
			if errors.Is(e, ErrTruncated) {
				coverage.Status = "truncated"
				coverage.Truncated = true
			}
			var upstream *HTTPError
			if errors.As(e, &upstream) && upstream.Status == 403 {
				coverage.Status = "forbidden"
			}
			if coverage.Points+coverage.Evidence > 0 && !coverage.Truncated {
				coverage.Status = "partial"
			}
			coverage.Reason = safeReason(e)
			coverage.Message = "upstream collection did not complete"
			r.Coverage[taskIdentity(t)] = coverage
			if coverage.Truncated {
				r.Progress.NextQuery = i + 1
				continue
			}
			r.Progress.NextQuery = i + 1
			continue
		}
		if coverage.Points == 0 && coverage.Evidence == 0 {
			coverage.Status = "empty"
		}
		r.Coverage[taskIdentity(t)] = coverage
		r.Progress.NextQuery = i + 1
	}
	r.Progress.Complete = true
	return r, nil
}
func enforceFilter(s Selection, from, to time.Time, runID string, raw json.RawMessage) map[string]any {
	filters := []any{map[string]any{"range": map[string]any{s.TimestampField: map[string]any{"gte": from.Format(time.RFC3339Nano), "lt": to.Format(time.RFC3339Nano)}}}}
	if s.RunIDField != "" {
		filters = append(filters, map[string]any{"term": map[string]any{s.RunIDField: runID}})
	}
	if len(s.ServiceValues) > 0 {
		filters = append(filters, map[string]any{"terms": map[string]any{s.ServiceField: s.ServiceValues}})
	}
	if len(raw) > 0 {
		var q any
		d := json.NewDecoder(strings.NewReader(string(raw)))
		d.UseNumber()
		_ = d.Decode(&q)
		filters = append(filters, q)
	}
	return map[string]any{"bool": map[string]any{"filter": filters}}
}
func taskDSL(t task) json.RawMessage {
	if t.query != nil {
		return t.query.DSL
	}
	return nil
}
func searchFilter(s Selection, t task, from, to time.Time, runID string) map[string]any {
	q := enforceFilter(s, from, to, runID, taskDSL(t))
	f := q["bool"].(map[string]any)["filter"].([]any)
	if t.kind != "metric" && s.IngestedField != "" && !s.IngestionFrom.IsZero() {
		// Keep the full event window. Old documents without the optional
		// ingestion timestamp still participate and deduplicate canonically.
		f = append(f, map[string]any{"bool": map[string]any{"minimum_should_match": 1, "should": []any{
			map[string]any{"range": map[string]any{s.IngestedField: map[string]any{"gte": s.IngestionFrom.Format(time.RFC3339Nano)}}},
			map[string]any{"bool": map[string]any{"must_not": []any{map[string]any{"exists": map[string]any{"field": s.IngestedField}}}}},
		}}})
	}
	if s.Preset == "elastic_apm" && (t.kind == "span" || t.kind == "transaction" || t.kind == "error") {
		// Linked spans and errors also carry transaction.id. APM's explicit
		// event kind determines the signal; correlation IDs do not.
		f = append(f, map[string]any{"term": map[string]any{"processor.event": t.kind}})
	}
	if t.query == nil {
		switch t.kind {
		case "span":
			f = append(f, map[string]any{"exists": map[string]any{"field": s.SpanIDField}})
		case "transaction":
			f = append(f, map[string]any{"exists": map[string]any{"field": s.TransactionIDField}})
		case "log":
			f = append(f, map[string]any{"exists": map[string]any{"field": s.MessageField}})
		case "error":
			f = append(f, map[string]any{"bool": map[string]any{"should": []any{map[string]any{"terms": map[string]any{s.SeverityField: []string{"error", "ERROR", "fatal", "FATAL"}}}, map[string]any{"terms": map[string]any{s.StatusField: []any{"failure", "ERROR", 2}}}, map[string]any{"exists": map[string]any{"field": "error.id"}}}, "minimum_should_match": 1}})
		}
	}
	q["bool"].(map[string]any)["filter"] = f
	return q
}
func cursorFor(s Selection, t task, from, to time.Time) *Continuation {
	if s.Continuation != nil && s.Continuation.QueryID == taskIdentity(t) {
		x := *s.Continuation
		return &x
	}
	return &Continuation{QueryID: taskIdentity(t), WindowFrom: from, WindowUntil: to}
}
func escapeHandle(s string) string { return url.PathEscape(s) }
func finite(v float64) bool        { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case json.Number:
		if len(x) > 128 {
			return 0, false
		}
		if at := strings.IndexAny(string(x), "eE"); at >= 0 {
			exponent, err := strconv.Atoi(string(x)[at+1:])
			if err != nil || exponent > 308 || exponent < -308 {
				return 0, false
			}
		}
		n, e := x.Float64()
		if e != nil || !finite(n) {
			return 0, false
		}
		if rational, ok := new(big.Rat).SetString(string(x)); ok && rational.IsInt() {
			limit := new(big.Int).Lsh(big.NewInt(1), 53)
			if new(big.Int).Abs(rational.Num()).Cmp(limit) > 0 {
				return 0, false
			}
		}
		return n, true
	case float64:
		return x, finite(x)
	case int:
		return float64(x), true
	}
	return 0, false
}
func timestamp(v any) (time.Time, bool) {
	if s, ok := v.(string); ok {
		t, e := time.Parse(time.RFC3339Nano, s)
		if e == nil {
			return t.UTC(), !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999
		}
	}
	if n, ok := number(v); ok && n >= 0 && n < 253402300800000 && math.Trunc(n) == n {
		t := time.UnixMilli(int64(n)).UTC()
		return t, !t.IsZero()
	}
	return time.Time{}, false
}
func stringValue(v any, maxBytes int) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	s = strings.Map(func(r rune) rune {
		if r == '\x00' || r == '\r' {
			return -1
		}
		return r
	}, s)
	if len(s) > maxBytes {
		s = s[:maxBytes]
		for !utf8.ValidString(s) && len(s) > 0 {
			s = s[:len(s)-1]
		}
	}
	return s
}
func lookup(m map[string]any, field string) any {
	if v, ok := m[field]; ok {
		return v
	}
	var cur any = m
	for _, part := range strings.Split(field, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = obj[part]
	}
	return cur
}
func scalar(v any) (analysis.ScalarValue, error) {
	switch x := v.(type) {
	case nil:
		return analysis.ScalarValue{Kind: "null"}, nil
	case string:
		if len(x) > 4096 {
			return analysis.ScalarValue{}, errors.New("projected value exceeds bounds")
		}
		return analysis.ScalarValue{Kind: "string", StringValue: x}, nil
	case bool:
		return analysis.ScalarValue{Kind: "boolean", BoolValue: &x}, nil
	case json.Number:
		if !strings.ContainsAny(string(x), ".eE") {
			if _, e := strconv.ParseInt(string(x), 10, 64); e != nil {
				return analysis.ScalarValue{}, errors.New("integer projection exceeds exact representation")
			}
			return analysis.ScalarValue{Kind: "integer", StringValue: string(x)}, nil
		}
		if n, e := mappedNumber(x); e == nil && n != nil {
			return analysis.ScalarValue{Kind: "number", NumberValue: n}, nil
		}
	case float64:
		if finite(x) {
			return analysis.ScalarValue{Kind: "number", NumberValue: &x}, nil
		}
	}
	return analysis.ScalarValue{}, errors.New("projected fields must contain bounded scalars")
}
func scalarRaw(v analysis.ScalarValue) any {
	switch v.Kind {
	case "string":
		return v.StringValue
	case "integer":
		return json.Number(v.StringValue)
	case "number":
		return v.NumberValue
	case "boolean":
		return v.BoolValue
	}
	return nil
}
func scalarList(v []analysis.ScalarValue) []any {
	out := make([]any, len(v))
	for i, s := range v {
		out[i] = scalarRaw(s)
	}
	return out
}
func scalarMap(v map[string]analysis.ScalarValue) map[string]any {
	out := map[string]any{}
	for k, s := range v {
		out[k] = scalarRaw(s)
	}
	return out
}

type responseShards struct {
	Total      int `json:"total"`
	Successful int `json:"successful"`
	Skipped    int `json:"skipped"`
	Failed     int `json:"failed"`
}

func (s responseShards) partial() bool {
	// Skipped search shards are also included in successful shards by both
	// vendors. An unallocated shard can be absent from failed; require the
	// complete successful count whenever total was supplied.
	return s.Failed > 0 || s.Total > 0 && s.Successful < s.Total
}

type searchResponse struct {
	PITID    string         `json:"pit_id"`
	TimedOut bool           `json:"timed_out"`
	Shards   responseShards `json:"_shards"`
	Hits     struct {
		Hits []struct {
			Index  string         `json:"_index"`
			ID     string         `json:"_id"`
			Source map[string]any `json:"_source"`
			Fields map[string]any `json:"fields"`
			Sort   []any          `json:"sort"`
		} `json:"hits"`
	} `json:"hits"`
	Aggregations map[string]json.RawMessage `json:"aggregations"`
}

func checkSearch(r searchResponse) error {
	if r.TimedOut || r.Shards.partial() {
		return errors.New("upstream search returned partial results")
	}
	return nil
}
func updatePIT(cursor *Continuation, id string) error {
	if id != "" {
		if !validOwnedHandle(id) {
			return errors.New("invalid PIT handle")
		}
		cursor.PITID = id
	}
	return nil
}
func point(s Selection, t task, from, to time.Time, runID string, at time.Time, bucket int, stat, unit string, value, count, sum, minimum, maximum *float64, dimensions map[string]string, approx bool) (analysis.MetricPoint, error) {
	end := at.Add(time.Duration(bucket) * time.Second)
	settled := !end.After(time.Now().Add(-time.Duration(s.SettleSeconds) * time.Second))
	eligible := settled && !approx
	if stat == "average" {
		eligible = eligible && count != nil && sum != nil && *count > 0
	}
	quality := analysis.QualityValid
	if value == nil {
		quality = analysis.QualityMissing
		eligible = false
	}
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: s.ProjectID, RunID: runID, Source: "elastic", TimestampUTC: at, ReceivedAt: time.Now().UTC(), MetricName: t.name, Kind: analysis.KindGauge, Value: value, Unit: normalizedUnit(unit), Quality: quality, Dimensions: dimensions, Provider: &analysis.ProviderMeasurement{MetricID: t.id, QueryID: t.id, WindowKind: "bucket", BucketStart: at, BucketEnd: end, ResolutionSeconds: bucket, Statistic: stat, Count: count, Sum: sum, Min: minimum, Max: maximum, Settled: settled, AggregationEligible: eligible, Approximate: approx}}
	if service := dimensions[s.ServiceField]; service != "" {
		p.Service = service
	}
	if !at.Before(to) || !end.After(from) {
		return p, errors.New("metric bucket outside enforced window")
	}
	if at.Before(from) || end.After(to) {
		p.Provider.AggregationEligible = false
	}
	return p, nil
}
func sortedKeys(m map[string]any) []string {
	keys := []string{}
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
