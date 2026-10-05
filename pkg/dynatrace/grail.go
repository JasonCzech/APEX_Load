package dynatrace

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// splitPipeline recognizes nesting and strings before admitting commands.
// Subqueries, comments, additional roots and scan/timeframe overrides are
// forbidden; runtime filters are inserted before all administrator transforms.
func splitPipeline(query string) ([]string, error) {
	if len(query) == 0 || len(query) > 16<<10 || strings.ContainsAny(query, ";\x00") {
		return nil, errors.New("invalid bounded DQL")
	}
	depth := 0
	quote := byte(0)
	escaped := false
	start := 0
	parts := []string{}
	var tokens strings.Builder
	for i := 0; i < len(query); i++ {
		ch := query[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		if ch == '"' || ch == '\'' || ch == '`' {
			quote = ch
			tokens.WriteByte(' ')
			continue
		}
		if ch == '[' || ch == ']' || ch == '/' && i+1 < len(query) && (query[i+1] == '/' || query[i+1] == '*') {
			return nil, errors.New("DQL nested queries and comments are unsupported")
		}
		switch ch {
		case '(', '{':
			depth++
		case ')', '}':
			depth--
			if depth < 0 {
				return nil, errors.New("unbalanced DQL")
			}
		case '|':
			if depth != 0 {
				return nil, errors.New("DQL pipeline inside expressions is unsupported")
			}
			parts = append(parts, strings.TrimSpace(query[start:i]))
			start = i + 1
		}
		tokens.WriteByte(ch)
	}
	if quote != 0 || depth != 0 {
		return nil, errors.New("unbalanced DQL")
	}
	parts = append(parts, strings.TrimSpace(query[start:]))
	if len(parts) > 32 {
		return nil, errors.New("too many DQL commands")
	}
	lower := strings.ToLower(tokens.String())
	rootTokens := regexp.MustCompile(`\b(?:fetch|timeseries|smartscapenodes|smartscapeedges|lookup|join|append|union|read|load)\b`).FindAllString(lower, -1)
	if len(rootTokens) != 1 {
		return nil, errors.New("DQL must contain exactly one read root and no subqueries")
	}
	if regexp.MustCompile(`(?i)\b(?:scanlimitgbytes|samplingratio|from|to|timeframe|shift|bins|scalar)\s*:`).MatchString(lower) {
		return nil, errors.New("DQL timeframe, sampling and scan overrides are forbidden")
	}
	for i, part := range parts {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			return nil, errors.New("empty DQL command")
		}
		command := strings.ToLower(fields[0])
		if i == 0 {
			switch command {
			case "fetch", "timeseries", "smartscapenodes", "smartscapeedges":
			default:
				return nil, errors.New("DQL requires one approved read root")
			}
		} else {
			switch command {
			case "filter", "fields", "fieldsadd", "sort", "limit", "summarize", "maketimeseries":
			default:
				return nil, errors.New("DQL command is outside the read-only bounded subset")
			}
		}
	}
	return parts, nil
}

var sampleFields = map[string]bool{"timestamp": true, "start_time": true, "end_time": true, "status": true, "loglevel": true, "content": true, "event.id": true, "event.name": true, "event.type": true, "event.status": true, "event.severity": true, "trace_id": true, "trace.id": true, "span_id": true, "span.id": true, "parent_span_id": true, "span.parent_id": true, "span.name": true, "duration": true, "request.is_failed": true, "dt.entity.service": true, "dt.entity.host": true, "dt.entity.process_group": true, "dt.smartscape.service": true, "apex.test_run": true, "apex.test_id": true, "http.request.header.x_apex_test_run": true}

func (q GrailQuery) Validate() error {
	if !safeName.MatchString(q.Name) || len(q.Fields) > 16 || len(q.EntityIDs) > 200 || len(q.Unit) > 64 || q.BucketSeconds < 0 || q.BucketSeconds > 86400 {
		return errors.New("invalid Grail definition bounds")
	}
	switch q.Kind {
	case "metric", "log", "span", "trace", "event":
	default:
		return errors.New("Grail kind must be metric, log, span, trace or event")
	}
	parts, err := splitPipeline(q.Query)
	if err != nil {
		return err
	}
	root := strings.ToLower(strings.TrimSpace(parts[0]))
	if strings.HasPrefix(root, "fetch ") {
		table := strings.Fields(root)
		if len(table) != 2 || table[1] != "logs" && table[1] != "spans" && table[1] != "events" {
			return errors.New("Grail fetch requires one approved table")
		}
		if q.Kind == "log" && table[1] != "logs" || (q.Kind == "span" || q.Kind == "trace") && table[1] != "spans" || q.Kind == "event" && table[1] != "events" {
			return errors.New("Grail table does not match result kind")
		}
	} else if !strings.HasPrefix(root, "timeseries ") || q.Kind != "metric" {
		return errors.New("Grail numeric root must be timeseries or fetch")
	}
	for _, field := range []string{q.RunIDField, q.EntityIDField, q.ValueField} {
		if field != "" && !safeField.MatchString(field) {
			return errors.New("invalid Grail field")
		}
	}
	if (q.EntityIDField == "") != (len(q.EntityIDs) == 0) {
		return errors.New("Grail entity field and approved IDs must be supplied together")
	}
	for _, id := range q.EntityIDs {
		if !providerEntityID.MatchString(id) {
			return errors.New("invalid Grail entity ID")
		}
	}
	for _, field := range q.Fields {
		if !sampleFields[field] {
			return errors.New("sample projection contains an excluded field")
		}
	}
	if q.Kind == "metric" {
		if q.ValueField == "" || q.Unit == "" {
			return errors.New("numeric Grail definitions require value field and unit")
		}
		switch q.Statistic {
		case "value", "sum", "average", "time_average", "min", "max", "p95", "p99":
		default:
			return errors.New("invalid numeric Grail statistic")
		}
		if strings.HasPrefix(root, "timeseries ") && q.BucketSeconds == 0 {
			return errors.New("timeseries requires an explicit bucket interval")
		}
		hasAggregate := strings.HasPrefix(root, "timeseries ")
		for _, part := range parts[1:] {
			if strings.HasPrefix(strings.ToLower(part), "summarize ") || strings.HasPrefix(strings.ToLower(part), "maketimeseries ") {
				hasAggregate = true
			}
			if strings.HasPrefix(strings.ToLower(part), "maketimeseries ") && q.BucketSeconds == 0 {
				return errors.New("makeTimeseries requires an explicit bucket interval")
			}
		}
		if !hasAggregate {
			return errors.New("numeric Grail definitions require an aggregate")
		}
		if _, err := grailNumericSemantics(q); err != nil {
			return err
		}
		if _, err := grailGroupingFields(q); err != nil {
			return err
		}
	}
	return nil
}
func BuildGrailQuery(q GrailQuery, from, to time.Time, runID string) (string, error) {
	if err := q.Validate(); err != nil {
		return "", err
	}
	parts, _ := splitPipeline(q.Query)
	if q.Kind == "metric" && q.BucketSeconds > 0 {
		for i := 1; i < len(parts); i++ {
			if strings.HasPrefix(strings.ToLower(parts[i]), "maketimeseries ") && !regexp.MustCompile(`(?i)\binterval\s*:`).MatchString(dqlOutsideStrings(parts[i])) {
				parts[i] += fmt.Sprintf(", interval: %ds", q.BucketSeconds)
			}
		}
	}
	root := parts[0]
	filters := []string{}
	if q.RunIDField != "" && runID != "" {
		raw, _ := json.Marshal(runID)
		filters = append(filters, q.RunIDField+" == "+string(raw))
	}
	if q.EntityIDField != "" {
		raw, _ := json.Marshal(q.EntityIDs)
		array := strings.ReplaceAll(strings.Trim(string(raw), "[]"), ",", ",")
		filters = append(filters, "in(toString("+q.EntityIDField+"), {"+array+"})")
	}
	isTimeseries := strings.HasPrefix(strings.ToLower(root), "timeseries ")
	if isTimeseries {
		if len(filters) > 0 {
			root += ", filter: (" + strings.Join(filters, " and ") + ")"
		}
		if !regexp.MustCompile(`(?i)\binterval\s*:`).MatchString(root) {
			root += fmt.Sprintf(", interval: %ds", q.BucketSeconds)
		}
	}
	result := []string{root}
	if !isTimeseries && len(filters) > 0 {
		result = append(result, "filter "+strings.Join(filters, " and "))
	}
	result = append(result, parts[1:]...)
	if q.Kind != "metric" {
		fields := q.Fields
		if len(fields) == 0 {
			fields = []string{"timestamp", "status", "content", "trace.id", "span.id", "span.parent_id", "duration", "span.name", "request.is_failed", "dt.entity.service"}
			if q.Kind == "event" {
				fields = []string{"timestamp", "event.id", "event.name", "event.type", "event.status", "event.severity", "dt.entity.service"}
			}
		}
		if q.RunIDField != "" {
			fields = appendUnique(append([]string(nil), fields...), q.RunIDField)
		}
		if q.EntityIDField != "" {
			fields = appendUnique(append([]string(nil), fields...), q.EntityIDField)
		}
		fields = appendUnique(append([]string(nil), fields...), "timestamp")
		if q.Kind == "span" || q.Kind == "trace" {
			for _, field := range []string{"trace.id", "span.id", "span.parent_id", "span.name", "duration", "request.is_failed"} {
				fields = appendUnique(fields, field)
			}
		}
		result = append(result, "fields "+strings.Join(fields, ", "), "limit 101")
	} else if q.BucketSeconds > 0 {
		result = append(result, "fields timeframe, interval, "+q.ValueField+grailDimensionProjection(q))
	} else {
		result = append(result, "fields "+q.ValueField+grailDimensionProjection(q))
	}
	return strings.Join(result, " | "), nil
}
func grailDimensionProjection(q GrailQuery) string {
	fields, _ := grailGroupingFields(q)
	if len(fields) > 0 {
		return ", " + strings.Join(fields, ", ")
	}
	return ""
}

type continuation struct {
	Query          int            `json:"query"`
	Fingerprint    string         `json:"fingerprint"`
	Page           string         `json:"page,omitempty"`
	Token          string         `json:"token,omitempty"`
	Attempt        string         `json:"attempt,omitempty"`
	Expires        time.Time      `json:"expires,omitempty"`
	From           time.Time      `json:"from"`
	To             time.Time      `json:"to"`
	Pages          int            `json:"pages,omitempty"`
	Scanned        int64          `json:"scanned,omitempty"`
	TopologyPhase  string         `json:"topology_phase,omitempty"`
	Truncated      bool           `json:"truncated,omitempty"`
	PlatformOrigin string         `json:"platform_origin,omitempty"`
	Principal      string         `json:"principal,omitempty"`
	MetricWindows  []metricWindow `json:"metric_windows,omitempty"`
	MetricPoints   int            `json:"metric_points,omitempty"`
}
type metricWindow struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

func encodeCursor(cursor continuation) string { raw, _ := json.Marshal(cursor); return string(raw) }
func decodeCursor(raw string) (continuation, error) {
	var cursor continuation
	if raw == "" {
		return cursor, nil
	}
	if len(raw) > 16<<10 || json.Unmarshal([]byte(raw), &cursor) != nil || len(cursor.Page) > 8192 || len(cursor.Token) > 8192 || len(cursor.Attempt) > 128 || cursor.Query < 0 || cursor.Pages > 100 || cursor.TopologyPhase != "" && cursor.TopologyPhase != "edges" {
		return cursor, errors.New("invalid provider continuation")
	}
	if len(cursor.MetricWindows) > 16 || cursor.MetricPoints < 0 || cursor.MetricPoints > 10000 {
		return cursor, errors.New("invalid bounded metric continuation")
	}
	for _, window := range cursor.MetricWindows {
		if window.From.Location() != time.UTC || window.To.Location() != time.UTC || !window.To.After(window.From) || window.From.Before(cursor.From) || window.To.After(cursor.To) {
			return cursor, errors.New("metric continuation changed query scope")
		}
	}
	return cursor, nil
}

type grailResponse struct {
	State  string `json:"state"`
	Token  string `json:"requestToken"`
	TTL    int    `json:"ttlSeconds"`
	Result struct {
		Records []map[string]json.RawMessage `json:"records"`
		Types   []struct {
			IndexRange []int `json:"indexRange"`
			Mappings   map[string]struct {
				Type        string `json:"type"`
				ElementType any    `json:"elementType"`
			} `json:"mappings"`
		} `json:"types"`
		Metadata struct {
			Grail struct {
				ScannedBytes  *int64 `json:"scannedBytes"`
				Sampled       bool   `json:"sampled"`
				Notifications []struct {
					Severity string `json:"severity"`
					Type     string `json:"notificationType"`
				} `json:"notifications"`
			} `json:"grail"`
			Metrics []struct {
				FieldName   string `json:"fieldName"`
				Unit        string `json:"unit"`
				Aggregation string `json:"aggregation"`
				Scalar      *bool  `json:"scalar"`
				Shifted     bool   `json:"shifted"`
			} `json:"metrics"`
		} `json:"metadata"`
	} `json:"result"`
}

func (c *Client) executeGrail(ctx context.Context, query string, from, to time.Time, cursor continuation, progress *QueryProgress) (grailResponse, continuation, error) {
	response := grailResponse{}
	var data []byte
	var err error
	if cursor.Token != "" && (cursor.PlatformOrigin != c.cfg.PlatformURL || cursor.Principal != c.queryPrincipal()) {
		return response, cursor, ErrPrincipalChanged
	}
	if cursor.Token != "" && (!cursor.Expires.After(c.now()) || cursor.From != from || cursor.To != to) {
		cursor.Token = ""
		cursor.Attempt = ""
	}
	if cursor.Token == "" {
		cursor.PlatformOrigin, cursor.Principal = c.cfg.PlatformURL, c.queryPrincipal()
		nonce := make([]byte, 16)
		if _, err = rand.Read(nonce); err != nil {
			return response, cursor, errors.New("cannot create query attempt identity")
		}
		cursor.Attempt = hex.EncodeToString(nonce)
		if c.scanHook != nil {
			if err = c.scanHook(cursor.Attempt, c.cfg.MaxScanBytes); err != nil {
				return response, cursor, err
			}
		}
		if c.scan != nil {
			if err = c.scan(ctx, c.cfg.MaxScanBytes); err != nil {
				return response, cursor, err
			}
		}
		progress.ScanReservationBytes += c.cfg.MaxScanBytes
		progress.ScanReservationIDs = append(progress.ScanReservationIDs, cursor.Attempt)
		body := map[string]any{"query": query, "defaultTimeframeStart": from.Format(time.RFC3339Nano), "defaultTimeframeEnd": to.Format(time.RFC3339Nano), "defaultScanLimitGbytes": float64(c.cfg.MaxScanBytes) / 1_000_000_000, "defaultSamplingRatio": 1, "maxResultRecords": min(c.cfg.MaxSeries, 201), "maxResultBytes": c.cfg.MaxResponseBytes - 1024, "requestTimeoutMilliseconds": 1000, "pollingPromiseSeconds": 30, "enablePreview": false, "enforceQueryConsumptionLimit": true, "timezone": "UTC", "includeTypes": true}
		data, err = c.request(ctx, true, http.MethodPost, "/platform/storage/query/v1/query:execute", nil, body)
	} else {
		data, err = c.request(ctx, true, http.MethodGet, "/platform/storage/query/v1/query:poll", url.Values{"request-token": {cursor.Token}, "request-timeout-milliseconds": {"1000"}}, nil)
	}
	if err != nil {
		if cursor.Token != "" && !errors.Is(err, ErrBudget) {
			if c.cancelAbandoned(cursor) == nil {
				cursor.Token = ""
			}
		}
		return response, cursor, err
	}
	if json.Unmarshal(data, &response) != nil {
		if cursor.Token != "" {
			if c.cancelAbandoned(cursor) == nil {
				cursor.Token = ""
			}
		}
		return response, cursor, fmt.Errorf("invalid Grail query response: %w", ErrInvalidResponse)
	}
	if response.State == "SUCCEEDED" {
		if cost := response.Result.Metadata.Grail.ScannedBytes; cost != nil && *cost >= 0 {
			progress.ScannedBytes += *cost
			progress.ScanSettlements = append(progress.ScanSettlements, QueryScanSettlement{AttemptID: cursor.Attempt, ScannedBytes: *cost})
			if *cost > c.cfg.MaxScanBytes {
				return response, cursor, ErrBudget
			}
		} else {
			// A completed result without cost metadata cannot release its durable
			// worst-case admission reservation.
			progress.QueryCostKnown = false
		}
		cursor.Token = ""
		return response, cursor, nil
	}
	switch response.State {
	case "RUNNING", "NOT_STARTED":
		progress.QueryCostKnown = false
		if response.Token != "" {
			cursor.Token = response.Token
		}
		if cursor.Token == "" || len(cursor.Token) > 8192 || response.TTL < 0 || response.TTL > 86400 {
			return response, cursor, errors.New("invalid Grail continuation token")
		}
		ttl := response.TTL
		if ttl == 0 {
			ttl = 30
		}
		cursor.Expires = c.now().Add(time.Duration(ttl) * time.Second)
		cursor.From, cursor.To = from, to
		return response, cursor, nil
	case "RESULT_GONE":
		cursor.Token = ""
		return response, cursor, &HTTPError{Status: 410, Kind: "query_expired"}
	case "CANCELLED":
		cursor.Token = ""
		return response, cursor, errors.New("Grail query cancelled")
	default:
		cursor.Token = ""
		return response, cursor, errors.New("Grail query failed")
	}
}

var ErrPrincipalChanged = errors.New("Dynatrace query originating principal changed")

// ValidateScanSettlements bounds per-attempt scan costs. Each attempt settles at
// most once per pass. Storage charges only settlements; public previews strip
// them and keep ScannedBytes as a display total, so equality is required only
// when settlements are present.
func ValidateScanSettlements(progress QueryProgress) error {
	if len(progress.ScanSettlements) > 32 {
		return errors.New("too many query scan settlements")
	}
	seen := map[string]bool{}
	var total int64
	for _, settlement := range progress.ScanSettlements {
		if !validHexID(settlement.AttemptID, 32) || seen[settlement.AttemptID] || settlement.ScannedBytes < 0 || settlement.ScannedBytes > 100_000_000_000 {
			return errors.New("invalid query scan settlement")
		}
		seen[settlement.AttemptID] = true
		total += settlement.ScannedBytes
	}
	if len(progress.ScanSettlements) > 0 && total != progress.ScannedBytes {
		return errors.New("scanned bytes differ from settled query attempts")
	}
	return nil
}

// Cancel abandons one private query token. The origin and principal must match
// those that executed it; returned completion results are not persisted.
func (c *Client) Cancel(ctx context.Context, rawCursor string) error {
	cursor, err := decodeCursor(rawCursor)
	if err != nil {
		return err
	}
	if cursor.Token == "" {
		return nil
	}
	if !cursor.Expires.After(c.now()) {
		return nil
	}
	if cursor.PlatformOrigin != c.cfg.PlatformURL || cursor.Principal != c.queryPrincipal() {
		return ErrPrincipalChanged
	}
	_, err = c.request(ctx, true, http.MethodPost, "/platform/storage/query/v1/query:cancel", url.Values{"request-token": {cursor.Token}}, nil)
	var upstream *HTTPError
	if errors.As(err, &upstream) && (upstream.Status == 404 || upstream.Status == 410) {
		return nil
	}
	return err
}
func (c *Client) cancelAbandoned(cursor continuation) error {
	cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return c.Cancel(cleanup, encodeCursor(cursor))
}
func appendUnique(items []string, item string) []string {
	if item == "" {
		return items
	}
	for _, v := range items {
		if v == item {
			return items
		}
	}
	return append(items, item)
}
func grailTruncated(response grailResponse) bool {
	if response.Result.Metadata.Grail.Sampled {
		return true
	}
	for _, n := range response.Result.Metadata.Grail.Notifications {
		if n.Severity == "WARN" || n.Severity == "ERROR" {
			return true
		}
	}
	return false
}
func text(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}
func boundedText(s string, n int) string {
	s = scrub.Value(s, scrub.Policy{})
	s = strings.ReplaceAll(s, "\x00", "")
	if len(s) > n {
		s = s[:n]
	}
	return s
}
func numeric(raw json.RawMessage) (*float64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var v float64
	if json.Unmarshal(raw, &v) != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, errors.New("numeric Grail result has invalid type")
	}
	return &v, nil
}
func (c *Client) normalizeGrail(q GrailQuery, response grailResponse, project string, from, to time.Time, runID string, settle int) ([]analysis.MetricPoint, []Diagnostic, bool, error) {
	points := []analysis.MetricPoint{}
	evidence := []Diagnostic{}
	seenTraces := map[string]bool{}
	truncated := grailTruncated(response)
	synthetic := false
	groupingFields := []string{}
	if q.Kind == "metric" {
		var err error
		if synthetic, err = grailNumericSemantics(q); err != nil {
			return nil, nil, false, err
		}
		if groupingFields, err = grailGroupingFields(q); err != nil {
			return nil, nil, false, err
		}
		for _, metric := range response.Result.Metadata.Metrics {
			if metric.FieldName != q.ValueField {
				continue
			}
			if metric.Shifted || metric.Scalar != nil && *metric.Scalar != (q.BucketSeconds == 0) || metric.Unit != "" && NormalizeUnit(metric.Unit) != NormalizeUnit(q.Unit) {
				return nil, nil, false, errors.New("Grail metric metadata differs from frozen definition")
			}
			statistic := strings.ToLower(metric.Aggregation)
			if statistic == "avg" {
				statistic = "average"
			}
			if statistic == "count" || statistic == "countif" {
				statistic = "sum"
				if strings.HasPrefix(strings.ToLower(strings.TrimSpace(q.Query)), "timeseries ") {
					statistic = "value"
				}
			}
			if statistic == "percentile" && (q.Statistic == "p95" || q.Statistic == "p99") {
				statistic = q.Statistic // The governed query fixes the literal rank.
			}
			if statistic != "" && statistic != q.Statistic && !(statistic == "average" && q.Statistic == "time_average") {
				return nil, nil, false, errors.New("Grail returned aggregation differs from frozen statistic")
			}
		}
	}
	for index, record := range response.Result.Records {
		if record == nil {
			continue
		}
		if index >= c.cfg.MaxSeries {
			truncated = true
			break
		}
		if q.Kind != "metric" {
			if len(evidence) >= 100 {
				truncated = true
				break
			}
			stamp := text(record["timestamp"])
			if stamp == "" {
				stamp = text(record["start_time"])
			}
			at, err := time.Parse(time.RFC3339Nano, stamp)
			if err != nil || at.Before(from) || at.After(to) {
				continue
			}
			trace := firstString(record, "trace.id", "trace_id")
			if q.Kind == "trace" && (!validHexID(trace, 32) || seenTraces[trace]) {
				continue
			}
			span := firstString(record, "span.id", "span_id")
			summary := firstString(record, "content", "span.name", "event.name")
			status := firstString(record, "status", "loglevel", "event.status")
			properties := map[string]string{"query_name": q.Name}
			if validHexID(trace, 32) {
				properties["trace_id"] = trace
			}
			if validHexID(span, 16) {
				properties["span_id"] = span
			}
			if parent := firstString(record, "span.parent_id", "parent_span_id"); validHexID(parent, 16) {
				properties["parent_span_id"] = parent
			}
			if q.Kind == "trace" {
				properties["evidence_scope"] = "sampled_span"
			}
			if nanos, ok := grailDurationNanos(record["duration"]); ok && nanos >= 0 {
				properties["duration_ms"] = strconv.FormatFloat(float64(nanos)/1_000_000, 'f', -1, 64)
			}
			if string(record["request.is_failed"]) == "true" {
				status = "ERROR"
			}
			correlation := "entity_time"
			if q.RunIDField != "" && text(record[q.RunIDField]) == runID && runID != "" {
				correlation = "run_header"
				properties["run_id"] = runID
			}
			properties["correlation"] = correlation
			if q.Kind == "event" {
				properties["event_type"] = boundedText(text(record["event.type"]), 128)
			}
			entities := map[string]string{}
			for k, v := range record {
				if strings.HasPrefix(k, "dt.entity.") || strings.HasPrefix(k, "dt.smartscape.") {
					id := text(v)
					if providerEntityID.MatchString(id) {
						entities[k] = id
					}
				}
			}
			if q.EntityIDField != "" {
				id := text(record[q.EntityIDField])
				if !containsIdentity(q.EntityIDs, id) {
					return nil, nil, false, errors.New("Grail evidence entity is outside approved scope")
				}
				entities[q.EntityIDField] = id
			}
			id := hashIdentity(q.Name, stamp, trace, span, summary)
			if q.Kind == "trace" {
				id = hashIdentity(q.Name, trace)
				seenTraces[trace] = true
			}
			if q.Kind == "event" && text(record["event.id"]) != "" {
				id = hashIdentity(q.Name, text(record["event.id"]))
			}
			evidence = append(evidence, Diagnostic{Provider: "dynatrace", Backend: "grail", ID: id, Kind: q.Kind, TimestampUTC: at.UTC(), Status: boundedText(status, 128), Severity: boundedText(text(record["event.severity"]), 128), Summary: boundedText(summary, 4096), EntityIDs: entities, Properties: properties})
			continue
		}
		dimensions := map[string]string{}
		for _, field := range groupingFields {
			raw, present := record[field]
			if !present {
				return nil, nil, false, errors.New("numeric Grail result omitted a selected grouping field")
			}
			if string(raw) == "null" {
				if field == q.EntityIDField || strings.HasPrefix(field, "dt.entity.") || strings.HasPrefix(field, "dt.smartscape.") {
					return nil, nil, false, errors.New("numeric Grail entity grouping requires a string identity")
				}
				dimensions[field] = "null" // Distinct from the string "null" and from an omitted field.
				continue
			}
			var primitive any
			decoder := json.NewDecoder(bytes.NewReader(raw))
			decoder.UseNumber()
			if decoder.Decode(&primitive) != nil {
				return nil, nil, false, errors.New("invalid numeric Grail grouping value")
			}
			switch primitive.(type) {
			case string:
			case json.Number:
			case bool:
			default:
				return nil, nil, false, errors.New("numeric Grail grouping value must be primitive")
			}
			if field == q.EntityIDField || strings.HasPrefix(field, "dt.entity.") || strings.HasPrefix(field, "dt.smartscape.") {
				identity, ok := primitive.(string)
				if !ok || !providerEntityID.MatchString(identity) {
					return nil, nil, false, errors.New("numeric Grail entity grouping requires a string identity")
				}
				dimensions[field] = identity
			} else {
				// JSON literals preserve type and exact integer identity. A string
				// "1", number 1 and boolean true remain distinct canonical tuples.
				canonical, err := json.Marshal(primitive)
				if err != nil {
					return nil, nil, false, errors.New("invalid numeric Grail grouping primitive")
				}
				dimensions[field] = string(canonical)
			}
		}
		if !validDimensions(dimensions) {
			if !dimensionLabelsSafe(dimensions) {
				return nil, nil, false, errUnsafeDimensions
			}
			return nil, nil, false, errors.New("numeric Grail grouping values exceed identity bounds")
		}
		if q.EntityIDField != "" {
			id := text(record[q.EntityIDField])
			if !containsIdentity(q.EntityIDs, id) {
				return nil, nil, false, errors.New("numeric query omitted approved entity identity")
			}
			dimensions[q.EntityIDField] = id
		}
		raw := record[q.ValueField]
		if len(raw) > 0 && raw[0] == '[' {
			var values []json.RawMessage
			var timeframe struct{ Start, End time.Time }
			if json.Unmarshal(raw, &values) != nil || json.Unmarshal(record["timeframe"], &timeframe) != nil || !timeframe.End.After(timeframe.Start) {
				return nil, nil, false, errors.New("invalid Grail timeseries timeframe")
			}
			interval, err := grailInterval(record["interval"])
			if err != nil || q.BucketSeconds == 0 || timeframe.End.Sub(timeframe.Start) != time.Duration(len(values))*time.Duration(interval)*time.Second {
				return nil, nil, false, errors.New("invalid actual Grail bucket interval")
			}
			for i, value := range values {
				start := timeframe.Start.UTC().Add(time.Duration(i*interval) * time.Second)
				end := start.Add(time.Duration(interval) * time.Second)
				if !end.After(from) || !start.Before(to) {
					continue
				}
				p, err := c.grailPoint(q, project, start, end, dimensions, value, false, synthetic, settle)
				if err != nil {
					return nil, nil, false, err
				}
				if len(points) >= c.cfg.MaxPoints {
					truncated = true
					break
				}
				points = append(points, p)
			}
		} else {
			if q.BucketSeconds > 0 {
				return nil, nil, false, errors.New("numeric bucket definition returned scalar data")
			}
			start, end := from.Truncate(time.Second), to.Truncate(time.Second)
			if !end.After(start) {
				return nil, nil, false, errors.New("query window too short")
			}
			p, err := c.grailPoint(q, project, start, end, dimensions, raw, true, synthetic, settle)
			if err != nil {
				return nil, nil, false, err
			}
			points = append(points, p)
		}
	}
	return points, evidence, truncated, nil
}
func (c *Client) grailPoint(q GrailQuery, project string, start, end time.Time, dimensions map[string]string, raw json.RawMessage, window, synthetic bool, settle int) (analysis.MetricPoint, error) {
	value, err := numeric(raw)
	if err != nil {
		return analysis.MetricPoint{}, err
	}
	quality := analysis.QualityValid
	if value == nil {
		quality = analysis.QualityMissing
	}
	eligible := !synthetic && q.Statistic != "average" && q.Statistic != "p95" && q.Statistic != "p99"
	eligible = eligible && value != nil
	if window && q.Statistic == "average" {
		eligible = false
	}
	entities := map[string]string{}
	for field, value := range dimensions {
		if field == q.EntityIDField || strings.HasPrefix(field, "dt.entity.") || strings.HasPrefix(field, "dt.smartscape.") {
			entities[field] = value
		}
	}
	return analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: project, Source: "dynatrace", TimestampUTC: start, MetricName: q.Name, Kind: analysis.KindGauge, Value: value, Unit: NormalizeUnit(q.Unit), Dimensions: dimensions, Quality: quality, Provider: &analysis.ProviderMeasurement{Name: "dynatrace", Backend: "grail", Synthetic: synthetic, MetricID: GrailMetricID(q), CatalogPath: q.Name, EntityIDs: entities, BucketStart: start, BucketEnd: end, ResolutionSeconds: int(end.Sub(start) / time.Second), Statistic: q.Statistic, Settled: !c.now().UTC().Before(end.Add(time.Duration(settle) * time.Second)), AggregationEligible: eligible, QueryWindow: window}}, nil
}
func containsIdentity(ids []string, id string) bool {
	for _, allowed := range ids {
		if allowed == id && id != "" {
			return true
		}
	}
	return false
}
func grailInterval(raw json.RawMessage) (int, error) {
	if nanos, ok := grailDurationNanos(raw); ok {
		if nanos > 0 && nanos%int64(time.Second) == 0 && nanos/int64(time.Second) <= 86400 {
			return int(nanos / int64(time.Second)), nil
		}
		return 0, errors.New("invalid Grail duration")
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return parseResolution(s)
	}
	return 0, errors.New("invalid Grail duration type")
}

// grailDurationNanos reads a Grail duration as integer nanoseconds. The query
// API may encode 64-bit values as JSON numbers or as decimal strings, so both
// encodings are accepted; textual forms such as "1m" are left to the caller.
func grailDurationNanos(raw json.RawMessage) (int64, bool) {
	var nanos int64
	if json.Unmarshal(raw, &nanos) == nil {
		return nanos, true
	}
	var text string
	if json.Unmarshal(raw, &text) != nil || text == "" {
		return 0, false
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	return parsed, err == nil
}
func firstString(record map[string]json.RawMessage, fields ...string) string {
	for _, k := range fields {
		if s := text(record[k]); s != "" {
			return s
		}
	}
	return ""
}
func validHexID(s string, n int) bool {
	if len(s) != n {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.Trim(s, "0") != ""
}
