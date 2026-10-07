package elastic

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

var fieldName = regexp.MustCompile(`^[A-Za-z_@][A-Za-z0-9_@.-]{0,127}$`)
var observedTraceID = regexp.MustCompile(`^[A-Fa-f0-9]{32}$`)
var metricName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,127}$`)
var indexName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.?*-]{0,254}$`)

// Reverse proxies may expose a fixed base path. It cannot contain escaping,
// traversal, an encoded separator or user-controlled query/fragment segments.
func validEndpoint(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || len(raw) > 2048 || strings.ContainsAny(raw, "\r\n\x00\\") || strings.Contains(raw, "%") {
		return errors.New("destination must be a bounded HTTPS endpoint")
	}
	if u.Path != "" && (path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/") && u.Path != "/" || strings.Contains(u.Path, "//")) {
		return errors.New("invalid destination base path")
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." || part != "" && !regexp.MustCompile(`^[A-Za-z0-9._~-]+$`).MatchString(part) {
			return errors.New("invalid destination base path")
		}
	}
	return nil
}

func (c Config) Defaults() Config {
	if c.Product == "" {
		c.Product = ProductElasticsearch
	}
	if c.AuthType == "" {
		c.AuthType = AuthAPIKey
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 10
	}
	if c.RequestsPerMinute == 0 {
		c.RequestsPerMinute = 60
	}
	if c.MaxResponseBytes == 0 {
		c.MaxResponseBytes = 4 << 20
	}
	if c.MaxRequestsPerPoll == 0 {
		c.MaxRequestsPerPoll = 20
	}
	if c.MaxPollSeconds == 0 {
		c.MaxPollSeconds = 30
	}
	if c.MaxPoints == 0 {
		c.MaxPoints = 10000
	}
	if c.MaxEvidence == 0 {
		c.MaxEvidence = 1000
	}
	if c.MaxSeries == 0 {
		c.MaxSeries = 200
	}
	return c
}
func validOrigin(raw string, httpAllowed bool) error {
	u, e := url.Parse(raw)
	if e != nil || (u.Scheme != "https" && !(httpAllowed && u.Scheme == "http")) || u.Hostname() == "" || len(raw) > 2048 || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/") {
		return errors.New("destination must be a bounded HTTPS origin without credentials, path, query or fragment")
	}
	if strings.ContainsAny(raw, "\r\n\x00") {
		return errors.New("invalid destination")
	}
	return nil
}
func (c Config) Validate() error {
	c = c.Defaults()
	if c.Product != ProductElasticsearch && c.Product != ProductOpenSearch {
		return errors.New("unsupported search product")
	}
	if e := validEndpoint(c.Endpoint); e != nil {
		return e
	}
	for _, v := range []string{c.KibanaEndpoint, c.LogstashEndpoint, c.DashboardsURL} {
		if v != "" {
			if e := validEndpoint(v); e != nil {
				return e
			}
		}
	}
	if c.AuthType != AuthAPIKey && c.AuthType != AuthBasic && c.AuthType != AuthBearer && c.AuthType != AuthMTLS {
		return errors.New("unsupported search authentication")
	}
	if c.Product == ProductOpenSearch && c.AuthType == AuthAPIKey {
		return errors.New("OpenSearch requires Basic, Bearer or client-certificate authentication")
	}
	for _, trust := range []struct {
		ca    string
		cidrs []string
	}{{c.CAPEM, c.ApprovedCIDRs}, {c.KibanaCAPEM, c.KibanaApprovedCIDRs}, {c.LogstashCAPEM, c.LogstashApprovedCIDRs}} {
		if len(trust.ca) > 65536 || len(trust.cidrs) > 32 {
			return errors.New("search trust configuration exceeds bounds")
		}
		for _, v := range trust.cidrs {
			if _, e := netip.ParsePrefix(v); e != nil {
				return errors.New("invalid approved CIDR")
			}
		}
	}
	for _, b := range []struct{ v, lo, hi int }{{c.TimeoutSeconds, 1, 30}, {c.RequestsPerMinute, 1, 600}, {c.MaxResponseBytes, 1024, 4 << 20}, {c.MaxRequestsPerPoll, 1, 100}, {c.MaxPollSeconds, 1, 60}, {c.MaxPoints, 1, 10000}, {c.MaxEvidence, 1, 10000}, {c.MaxSeries, 1, 200}, {c.MaxRetries, 0, 3}} {
		if b.v < b.lo || b.v > b.hi {
			return errors.New("search collection limit outside supported bounds")
		}
	}
	return nil
}
func (s Selection) Defaults() Selection {
	if s.Preset == "" {
		s.Preset = "ecs"
	}
	if s.TimestampField == "" {
		s.TimestampField = "@timestamp"
	}
	if s.IngestedField == "" {
		s.IngestedField = "event.ingested"
	}
	if s.ServiceField == "" {
		s.ServiceField = "service.name"
	}
	if s.TraceIDField == "" {
		s.TraceIDField = "trace.id"
	}
	if s.SpanIDField == "" {
		s.SpanIDField = "span.id"
	}
	if s.TransactionIDField == "" {
		s.TransactionIDField = "transaction.id"
	}
	if s.ParentSpanIDField == "" {
		s.ParentSpanIDField = "parent.id"
	}
	if s.MessageField == "" {
		s.MessageField = "message"
	}
	if s.SeverityField == "" {
		s.SeverityField = "log.level"
	}
	if s.StatusField == "" {
		s.StatusField = "event.outcome"
	}
	if s.DurationField == "" {
		s.DurationField = "event.duration"
	}
	if s.DurationUnit == "" {
		s.DurationUnit = "ns"
	}
	if s.Preset == "elastic_apm" {
		if s.DurationField == "event.duration" {
			s.DurationField = "transaction.duration.us"
			s.DurationUnit = "us"
		}
	}
	if s.Preset == "opensearch_otel" || s.Preset == "otel" {
		if s.TimestampField == "@timestamp" {
			s.TimestampField = "startTime"
		}
		if s.ServiceField == "service.name" {
			s.ServiceField = "serviceName"
		}
		if s.TraceIDField == "trace.id" {
			s.TraceIDField = "traceId"
		}
		if s.SpanIDField == "span.id" {
			s.SpanIDField = "spanId"
		}
		if s.ParentSpanIDField == "parent.id" {
			s.ParentSpanIDField = "parentSpanId"
		}
		if s.DurationField == "event.duration" {
			s.DurationField = "durationInNanos"
		}
		if s.StatusField == "event.outcome" {
			s.StatusField = "status.code"
		}
	}
	if s.Preset == "jaeger" {
		if s.TimestampField == "@timestamp" {
			s.TimestampField = "startTimeMillis"
		}
		if s.ServiceField == "service.name" {
			s.ServiceField = "process.serviceName"
		}
		if s.TraceIDField == "trace.id" {
			s.TraceIDField = "traceID"
		}
		if s.SpanIDField == "span.id" {
			s.SpanIDField = "spanID"
		}
		if s.DurationField == "event.duration" {
			s.DurationField = "duration"
			s.DurationUnit = "us"
		}
	}
	if s.MaxWindowSeconds == 0 {
		s.MaxWindowSeconds = 86400
	}
	if s.PageSize == 0 {
		s.PageSize = 100
	}
	if s.SettleSeconds == 0 {
		s.SettleSeconds = 60
	}
	if s.OverlapSeconds == 0 {
		s.OverlapSeconds = 120
	}
	for i := range s.Metrics {
		if s.Metrics[i].BucketSeconds == 0 {
			s.Metrics[i].BucketSeconds = 10
		}
		if s.Metrics[i].Statistic == "" {
			s.Metrics[i].Statistic = "average"
		}
		if s.Metrics[i].Kind == "" {
			s.Metrics[i].Kind = "gauge"
		}
	}
	for i := range s.Queries {
		if s.Queries[i].Kind == "" {
			s.Queries[i].Kind = "log"
		}
		if s.Queries[i].Statistic == "" {
			s.Queries[i].Statistic = "value"
		}
		if s.Queries[i].BucketSeconds == 0 {
			s.Queries[i].BucketSeconds = 10
		}
	}
	return s
}
func validateIndices(indices []string) error {
	if len(indices) == 0 || len(indices) > 32 {
		return errors.New("select 1-32 index patterns")
	}
	seen := map[string]bool{}
	for _, v := range indices {
		if !indexName.MatchString(v) || v == "*" || v == "_all" || seen[v] || strings.HasPrefix(v, ".") {
			return errors.New("index patterns must be unique, explicit and bounded; system and cross-cluster sources are unsupported")
		}
		seen[v] = true
	}
	return nil
}
func validUnit(u string) bool {
	switch u {
	case "", "count", "ms", "us", "ns", "s", "bytes", "percent", "ratio", "requests/s", "errors/s", "operations/s", "connections", "threads":
		return true
	}
	return false
}
func validStatistic(s string) bool {
	switch s {
	case "value", "count", "average", "sum", "min", "max", "p50", "p90", "p95", "p99", "cardinality":
		return true
	}
	return false
}
func (s Selection) Validate() error {
	s = s.Defaults()
	if s.ProjectID == "" || len(s.ProjectID) > 256 || strings.ContainsAny(s.ProjectID, "\r\n\x00") {
		return errors.New("selection requires a bounded project ID")
	}
	if len(s.Indices) > 0 || HasIndexedSignals(s) {
		if e := validateIndices(s.Indices); e != nil {
			return e
		}
	}
	switch s.Preset {
	case "ecs", "elastic_apm", "otel", "opensearch_otel", "jaeger", "custom":
	default:
		return errors.New("unsupported observability preset")
	}
	for _, f := range []string{s.TimestampField, s.IngestedField, s.ServiceField, s.RunIDField, s.TraceIDField, s.SpanIDField, s.TransactionIDField, s.ParentSpanIDField, s.MessageField, s.SeverityField, s.DurationField, s.StatusField, s.UniqueSortField} {
		if f != "" && !fieldName.MatchString(f) {
			return errors.New("invalid selected field")
		}
	}
	if !validUnit(s.DurationUnit) || s.DurationUnit != "ns" && s.DurationUnit != "us" && s.DurationUnit != "ms" && s.DurationUnit != "s" {
		return errors.New("invalid duration unit")
	}
	mappedMetrics := len(s.Metrics)
	for _, query := range s.Queries {
		if query.Kind == "metric" {
			mappedMetrics++
		}
	}
	if len(s.ServiceValues) > 32 || len(s.AttributeFields)+len(s.ProjectedFields) > 16 || mappedMetrics > 20 || len(s.Queries) > 5 || s.QueryOffset < 0 || s.QueryOffset > 64 {
		return errors.New("selection exceeds query or projection bounds")
	}
	for _, v := range s.ServiceValues {
		if v == "" || len(v) > 256 || strings.ContainsAny(v, "\r\n\x00") {
			return errors.New("invalid service scope")
		}
	}
	for _, f := range append(append([]string{}, s.AttributeFields...), s.ProjectedFields...) {
		if !fieldName.MatchString(f) {
			return errors.New("invalid projected field")
		}
	}
	names := map[string]bool{}
	for _, m := range s.Metrics {
		if !metricName.MatchString(m.Name) || names[m.Name] || !validStatistic(m.Statistic) || m.Statistic == "value" || !validUnit(m.Unit) || m.BucketSeconds < 1 || m.BucketSeconds > 86400 || len(m.GroupBy) > 4 {
			return errors.New("invalid metric selection")
		}
		names[m.Name] = true
		if m.Statistic != "count" && !fieldName.MatchString(m.Field) && !(fieldName.MatchString(m.SumField) && fieldName.MatchString(m.CountField)) {
			return errors.New("metric requires a field or sum/count mapping")
		}
		for _, f := range append(append([]string{}, m.GroupBy...), m.SumField, m.CountField) {
			if f != "" && !fieldName.MatchString(f) {
				return errors.New("invalid metric field")
			}
		}
		for _, f := range m.GroupBy {
			if !validDimensionField(f) {
				return errors.New("reserved or credential grouping field is unsupported")
			}
		}
		if (m.SumField == "") != (m.CountField == "") {
			return errors.New("sum and count mappings must be supplied together")
		}
		if err := validateStatisticMapping(m.Statistic, m.Field, m.SumField); err != nil {
			return err
		}
		switch m.Kind {
		case "gauge", "counter", "observation", "histogram", "summary":
		default:
			return errors.New("invalid metric kind")
		}
	}
	for _, q := range s.Queries {
		if !metricName.MatchString(q.Name) || names[q.Name] || !validStatistic(q.Statistic) || !validUnit(q.Unit) || q.BucketSeconds < 1 || q.BucketSeconds > 86400 || len(q.GroupBy)+len(q.DimensionColumns) > 4 {
			return errors.New("invalid custom query")
		}
		names[q.Name] = true
		switch q.Kind {
		case "metric", "log", "span", "transaction", "error":
		default:
			return errors.New("invalid query record kind")
		}
		for _, f := range append(append([]string{q.TimestampColumn, q.ValueColumn, q.CountColumn, q.SumColumn}, q.GroupBy...), q.DimensionColumns...) {
			if f != "" && !fieldName.MatchString(f) {
				return errors.New("invalid query output field")
			}
		}
		for _, f := range append(append([]string{}, q.GroupBy...), q.DimensionColumns...) {
			if !validDimensionField(f) {
				return errors.New("reserved or credential grouping field is unsupported")
			}
		}
		if q.Language == "dsl" {
			if q.ESQL != "" || len(q.Params) > 0 || q.Async {
				return errors.New("DSL query contains ES|QL options")
			}
			if e := ValidateDSL(q.DSL); e != nil {
				return e
			}
			if q.Kind == "metric" && (q.Statistic == "value" || q.Statistic != "count" && q.ValueColumn == "" && (q.SumColumn == "" || q.CountColumn == "")) {
				return errors.New("DSL metric requires a statistic and mapped value field")
			}
			if q.Kind == "metric" {
				if (q.SumColumn == "") != (q.CountColumn == "") {
					return errors.New("DSL sum and count mappings must be paired")
				}
				if err := validateStatisticMapping(q.Statistic, q.ValueColumn, q.SumColumn); err != nil {
					return err
				}
			}
		} else if q.Language == "esql" {
			if len(q.DSL) > 0 {
				return errors.New("ES|QL query contains DSL body")
			}
			if e := ValidateESQL(q, s.Indices); e != nil {
				return e
			}
			if q.Kind != "metric" {
				return errors.New("ES|QL is restricted to bounded metrics, not exhaustive document capture")
			}
			if q.ValueColumn == "" || !q.ExactWindow && q.TimestampColumn == "" {
				return errors.New("ES|QL metrics require value and timestamp/bucket mappings")
			}
			if q.Statistic == "average" && (q.CountColumn == "" || q.SumColumn == "") {
				return errors.New("ES|QL averages require sum and count mappings")
			}
			if (q.CountColumn == "") != (q.SumColumn == "") {
				return errors.New("ES|QL sum/count mappings must be paired")
			}
		} else {
			return errors.New("unsupported query language")
		}
	}
	if s.SettleSeconds < 0 || s.SettleSeconds > 3600 || s.OverlapSeconds < 0 || s.OverlapSeconds > 3600 || s.MaxWindowSeconds < 1 || s.MaxWindowSeconds > 7*86400 || s.PageSize < 1 || s.PageSize > 100 || len(s.HeaderOrigins) > 32 {
		return errors.New("selection window or page bounds rejected")
	}
	for _, v := range s.HeaderOrigins {
		if e := validOrigin(v, true); e != nil {
			return e
		}
	}
	if s.RunHeader && len(s.HeaderOrigins) == 0 {
		return errors.New("run-header propagation requires approved origins")
	}
	if !(s.Logs || s.Traces || s.Transactions || s.Errors || s.ClusterHealth || s.KibanaHealth || s.LogstashHealth || len(s.Metrics) > 0 || len(s.Queries) > 0) {
		return errors.New("select at least one observability signal")
	}
	if s.MaxRetainedRecords < 0 || s.MaxRetainedRecords > 10000 || s.MaxRetainedBytes < 0 || s.MaxRetainedBytes > 8<<20 {
		return errors.New("source retention ceilings exceed policy")
	}
	if err := analysis.ValidateResolvedIndices(s.ResolvedIndices); err != nil {
		return err
	}
	if !s.IngestionFrom.IsZero() && (s.IngestedField == "" || s.IngestionFrom.Location() != time.UTC || s.IngestionFrom.Year() < 1 || s.IngestionFrom.Year() > 9999) {
		return errors.New("ingestion cursor requires a UTC ingestion timestamp mapping")
	}
	if len(s.ObservedTraceIDs) > 1024 {
		return errors.New("observed trace-ID bound exceeded")
	}
	for _, id := range s.ObservedTraceIDs {
		if !observedTraceID.MatchString(id) {
			return errors.New("invalid observed trace identity")
		}
	}
	if len(s.DisabledQueries) > 64 {
		return errors.New("disabled query count exceeds bounds")
	}
	if !s.FinalHealthAfter.IsZero() && (s.FinalHealthAfter.Location() != time.UTC || s.FinalHealthAfter.Year() < 1 || s.FinalHealthAfter.Year() > 9999) {
		return errors.New("final health cutoff must be a bounded UTC timestamp")
	}
	if len(s.HealthCoverage) > 3 {
		return errors.New("health coverage count exceeds bounds")
	}
	healthQueries := map[string]bool{}
	for _, task := range selectionTasks(s) {
		if task.health != "" {
			healthQueries[task.id] = true
		}
	}
	for queryID, c := range s.HealthCoverage {
		if !healthQueries[queryID] || c.QueryID != queryID || c.From.IsZero() || c.From.Location() != time.UTC || c.From.Year() < 1 || c.To.Location() != time.UTC || c.To.Year() > 9999 || !c.To.After(c.From) || c.Points < 0 || c.Points > 10000 || c.Evidence != 0 || c.LateArrivalSeconds < 0 || c.LateArrivalSeconds > 86400 || len(c.Reason) > 128 || len(c.Message) > 256 || c.AsOf != nil && (c.AsOf.IsZero() || c.AsOf.Location() != time.UTC || c.AsOf.Year() < 1 || c.AsOf.Year() > 9999 || c.AsOf.After(time.Now().UTC().Add(time.Minute))) {
			return errors.New("invalid prior health coverage")
		}
		switch c.Status {
		case "complete", "empty", "partial", "unavailable", "forbidden", "unsupported", "stale", "truncated":
		default:
			return errors.New("invalid prior health coverage status")
		}
	}
	if len(s.TerminalWindows) > 32 {
		return errors.New("terminal window count exceeds bounds")
	}
	for _, w := range s.TerminalWindows {
		if w.Metric == "" || !names[w.Metric] || w.FromUTC.IsZero() || w.FromUTC.Location() != time.UTC || w.UntilUTC.Location() != time.UTC || !w.UntilUTC.After(w.FromUTC) || w.UntilUTC.Sub(w.FromUTC) > time.Duration(s.MaxWindowSeconds)*time.Second {
			return errors.New("invalid terminal metric window")
		}
	}
	if s.Continuation != nil {
		return s.Continuation.Validate()
	}
	return nil
}

func validateStatisticMapping(statistic, field, sumField string) error {
	switch statistic {
	case "min", "max", "p50", "p90", "p95", "p99", "cardinality":
		if field == "" || sumField != "" {
			return errors.New("selected statistic requires a native value field without sum/count mapping")
		}
	}
	return nil
}

func (j JobConfig) Validate() error {
	if e := j.Config.Validate(); e != nil {
		return e
	}
	s := j.Selection
	s.TerminalWindows = j.TerminalWindows
	s.ObservedTraceIDs = j.ObservedTraceIDs
	s.CorrelationLimitReached = j.CorrelationLimitReached
	s.DisabledQueries = j.DisabledQueries
	s.LastHealthAt = j.LastHealthAt
	s.HealthCoverage = j.HealthCoverage
	s.FinalHealthAfter = j.FinalHealthAfter
	s.IngestionFrom = j.IngestionFrom
	s.ResolvedIndices = j.ResolvedIndices
	if e := s.Validate(); e != nil {
		return e
	}
	if j.Continuation != nil {
		return j.Continuation.Validate()
	}
	return nil
}

// ValidateDSL accepts a query clause, not a search envelope. This prevents q,
// global/post_filter, scripts, runtime mappings and caller-owned paging from
// escaping the collector's enforced index, time and run scope.
func ValidateDSL(raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	if len(raw) > 16<<10 {
		return errors.New("DSL query exceeds limit")
	}
	var v any
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.UseNumber()
	if e := d.Decode(&v); e != nil {
		return errors.New("invalid DSL query")
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("multiple or malformed DSL documents rejected")
	}
	return validateClause(v, 0)
}
func validateClause(v any, depth int) error {
	if depth > 8 {
		return errors.New("DSL nesting exceeds bounds")
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return errors.New("DSL must be a single allowed query clause")
	}
	for k, body := range m {
		switch k {
		case "match_all":
			if b, ok := body.(map[string]any); !ok || len(b) != 0 {
				return errors.New("invalid match_all clause")
			}
		case "bool":
			b, ok := body.(map[string]any)
			if !ok {
				return errors.New("invalid bool clause")
			}
			for key, child := range b {
				if key == "minimum_should_match" {
					if n, ok := child.(json.Number); !ok || n != "1" {
						return errors.New("only minimum_should_match 1 is allowed")
					}
					continue
				}
				if key != "filter" && key != "must" && key != "must_not" && key != "should" {
					return errors.New("unsupported bool option")
				}
				if list, ok := child.([]any); ok {
					if len(list) > 32 {
						return errors.New("too many DSL clauses")
					}
					for _, c := range list {
						if e := validateClause(c, depth+1); e != nil {
							return e
						}
					}
				} else if e := validateClause(child, depth+1); e != nil {
					return e
				}
			}
		case "term", "terms", "range", "exists", "match", "match_phrase", "prefix":
			b, ok := body.(map[string]any)
			if !ok || len(b) != 1 {
				return errors.New("invalid DSL field clause")
			}
			for f, value := range b {
				if k == "exists" {
					if f != "field" {
						return errors.New("invalid exists clause")
					}
					sv, ok := value.(string)
					if !ok || !fieldName.MatchString(sv) {
						return errors.New("invalid exists field")
					}
					continue
				}
				if !fieldName.MatchString(f) {
					return errors.New("invalid DSL field")
				}
				if k == "range" {
					opts, ok := value.(map[string]any)
					if !ok || len(opts) == 0 || len(opts) > 4 {
						return errors.New("invalid range clause")
					}
					for op, x := range opts {
						if op != "gte" && op != "gt" && op != "lte" && op != "lt" {
							return errors.New("unsupported range option")
						}
						if !boundedScalar(x) {
							return errors.New("invalid range bound")
						}
					}
				} else if k == "terms" {
					xs, ok := value.([]any)
					if !ok || len(xs) == 0 || len(xs) > 64 {
						return errors.New("terms lookup is unsupported")
					}
					for _, x := range xs {
						if !boundedScalar(x) {
							return errors.New("invalid term value")
						}
					}
				} else if !boundedScalar(value) {
					return errors.New("DSL values must be bounded scalars")
				}
			}
		default:
			return fmt.Errorf("unsupported DSL clause %q", k)
		}
	}
	return nil
}
func boundedScalar(v any) bool {
	switch x := v.(type) {
	case nil, bool:
		return true
	case string:
		return len(x) <= 4096 && !strings.ContainsAny(x, "\x00\r\n")
	case json.Number:
		f, e := x.Float64()
		return e == nil && !math.IsInf(f, 0) && !math.IsNaN(f)
	case float64:
		return !math.IsInf(x, 0) && !math.IsNaN(x)
	case int, int32, int64, uint, uint32, uint64:
		return true
	}
	return false
}

// ValidateESQL implements a deliberately small read-only grammar. Values are
// bound separately; source identifiers and commands cannot be parameters.
func ValidateESQL(q Query, indices []string) error {
	if len(q.ESQL) == 0 || len(q.ESQL) > 16<<10 || strings.ContainsAny(q.ESQL, ";\x00\r") || strings.Contains(q.ESQL, "//") || strings.Contains(q.ESQL, "/*") || strings.Contains(q.ESQL, "*/") {
		return errors.New("ES|QL query exceeds permitted grammar")
	}
	parts, e := splitPipeline(q.ESQL)
	if e != nil {
		return e
	}
	if len(parts) < 2 || len(parts) > 16 {
		return errors.New("ES|QL requires a bounded FROM pipeline")
	}
	first := strings.Fields(parts[0])
	if len(first) != 2 || strings.ToUpper(first[0]) != "FROM" || first[1] != strings.Join(indices, ",") {
		return errors.New("ES|QL FROM must exactly match selected index patterns")
	}
	stats, limited := false, false
	questions := 0
	for i, p := range parts {
		if i == 0 {
			continue
		}
		word := strings.ToUpper(strings.Fields(p)[0])
		switch word {
		case "WHERE", "EVAL", "KEEP", "DROP", "RENAME", "SORT":
			if limited {
				return errors.New("LIMIT must be final")
			}
		case "STATS":
			if limited || stats {
				return errors.New("ES|QL supports one aggregate stage before final LIMIT")
			}
			stats = true
		case "LIMIT":
			if i != len(parts)-1 {
				return errors.New("LIMIT must be final")
			}
			fs := strings.Fields(p)
			if len(fs) != 2 || !regexp.MustCompile(`^([1-9][0-9]{0,3}|10000)$`).MatchString(fs[1]) {
				return errors.New("invalid ES|QL LIMIT")
			}
			limited = true
		default:
			return errors.New("unsupported ES|QL command")
		}
		// Lexical punctuation outside strings can introduce subqueries, parameters
		// in source identifiers, comments or newer expression constructs.
		plain, e := stripStrings(p)
		if e != nil {
			return e
		}
		if strings.ContainsAny(plain, "{}[]:`\\") || strings.Contains(plain, "??") {
			return errors.New("unsupported ES|QL expression")
		}
		questions += strings.Count(plain, "?")
		for _, tok := range regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`).FindAllString(plain, -1) {
			switch strings.ToUpper(tok) {
			case "FROM", "JOIN", "LOOKUP", "ENRICH", "METRICS", "TS", "ROW", "FORK", "CHANGE_POINT", "COMPLETION", "SET", "EXPLAIN":
				return errors.New("unsupported ES|QL expression")
			}
		}
	}
	if !limited {
		return errors.New("ES|QL requires an explicit final LIMIT")
	}
	if q.Kind == "metric" && !stats {
		return errors.New("ES|QL metric requires STATS")
	}
	if q.Kind != "metric" && stats {
		return errors.New("record ES|QL queries cannot aggregate")
	}
	if len(q.Params) > 32 {
		return errors.New("ES|QL parameter count exceeds bounds")
	}
	plain, _ := stripStrings(strings.Join(parts[1:], "|"))
	placeholders := regexp.MustCompile(`\?([A-Za-z_][A-Za-z0-9_]*|[0-9]+)?`).FindAllStringSubmatch(plain, -1)
	style := ""
	named := map[string]bool{}
	positions := map[int]bool{}
	for _, placeholder := range placeholders {
		current := "anonymous"
		if placeholder[1] != "" {
			if position, err := strconv.Atoi(placeholder[1]); err == nil {
				current = "positional"
				if position < 1 || position > len(q.Params) {
					return errors.New("invalid ES|QL positional parameter")
				}
				positions[position] = true
			} else {
				current = "named"
				named[placeholder[1]] = true
			}
		}
		if style != "" && style != current {
			return errors.New("mixed ES|QL parameter styles are unsupported")
		}
		style = current
	}
	if style == "named" {
		provided := map[string]bool{}
		for _, param := range q.Params {
			value, ok := param.(map[string]any)
			if !ok || len(value) != 1 {
				return errors.New("named ES|QL parameter must be a single scalar binding")
			}
			for name, scalar := range value {
				if !named[name] || provided[name] || !boundedScalar(scalar) {
					return errors.New("invalid named ES|QL parameter")
				}
				provided[name] = true
			}
		}
		if len(provided) != len(named) {
			return errors.New("ES|QL named parameters do not match placeholders")
		}
	} else {
		if style == "positional" && len(positions) != len(q.Params) || style != "positional" && questions != len(q.Params) {
			return errors.New("ES|QL parameters do not match placeholders")
		}
		for _, v := range q.Params {
			if !boundedScalar(v) {
				return errors.New("invalid ES|QL parameter")
			}
		}
	}
	return nil
}
func splitPipeline(s string) ([]string, error) {
	parts := []string{}
	start := 0
	quoted := false
	escaped := false
	for i, r := range s {
		if quoted {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == '"' {
				quoted = false
			}
			continue
		}
		if r == '"' {
			quoted = true
		} else if r == '|' {
			p := strings.TrimSpace(s[start:i])
			if p == "" {
				return nil, errors.New("empty ES|QL stage")
			}
			parts = append(parts, p)
			start = i + 1
		}
	}
	if quoted {
		return nil, errors.New("unterminated ES|QL string")
	}
	p := strings.TrimSpace(s[start:])
	if p == "" {
		return nil, errors.New("empty ES|QL stage")
	}
	return append(parts, p), nil
}
func stripStrings(s string) (string, error) {
	var b strings.Builder
	quoted, escaped := false, false
	for _, r := range s {
		if quoted {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == '"' {
				quoted = false
			}
			b.WriteByte(' ')
		} else if r == '"' {
			quoted = true
			b.WriteByte(' ')
		} else {
			b.WriteRune(r)
		}
	}
	if quoted {
		return "", errors.New("unterminated ES|QL string")
	}
	return b.String(), nil
}
