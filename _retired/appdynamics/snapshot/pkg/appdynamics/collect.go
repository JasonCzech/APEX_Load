package appdynamics

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/jasonczech/apexload/pkg/scrub"
)

type metricResponse struct {
	ID        json.Number   `json:"metricId"`
	Path      string        `json:"metricPath"`
	Name      string        `json:"metricName"`
	Frequency string        `json:"frequency"`
	Values    []metricValue `json:"metricValues"`
}
type metricValue struct {
	Start    int64    `json:"startTimeInMillis"`
	Value    *float64 `json:"value"`
	Count    *float64 `json:"count"`
	Sum      *float64 `json:"sum"`
	Min      *float64 `json:"min"`
	Max      *float64 `json:"max"`
	Current  *float64 `json:"current"`
	UseRange bool     `json:"useRange"`
}

func finite(v *float64) bool { return v != nil && !math.IsNaN(*v) && !math.IsInf(*v, 0) }
func frequencySeconds(f string) int {
	switch f {
	case "ONE_MIN":
		return 60
	case "TEN_MIN":
		return 600
	case "ONE_HOUR", "SIXTY_MIN":
		return 3600
	case "ONE_DAY":
		return 86400
	}
	return 0
}

func AnalyticsMetricID(q AnalyticsQuery) string {
	hash := sha256.Sum256([]byte(q.ApplicationID + "\x00" + q.Name + "\x00" + q.Query + "\x00" + q.ApplicationIDField + "\x00" + q.ApplicationName + "\x00" + q.RunIDField))
	return hex.EncodeToString(hash[:16])
}

// NormalizeMetric preserves explicitly reported zeros for sum/count metrics.
// Auxiliary extrema and counts never determine a universal missing predicate.
func NormalizeMetric(selection MetricSelection, row metricResponse, value metricValue, projectID, runID string, now time.Time, settleSeconds int) (analysis.MetricPoint, error) {
	resolution := frequencySeconds(row.Frequency)
	if resolution == 0 || value.Start <= 0 {
		return analysis.MetricPoint{}, errors.New("unknown metric frequency or invalid bucket timestamp")
	}
	if !numericID.MatchString(row.ID.String()) || row.Path == "" || len(row.Path) > 4096 {
		return analysis.MetricPoint{}, errors.New("invalid metric identity")
	}
	start := time.UnixMilli(value.Start).UTC()
	end := start.Add(time.Duration(resolution) * time.Second)
	statistic := selection.Statistic
	if statistic == "" {
		statistic = "value"
	}
	lower := strings.ToLower(row.Path)
	if statistic == "value" {
		switch {
		case strings.Contains(lower, "95th percentile"):
			statistic = "p95"
		case strings.Contains(lower, "99th percentile"):
			statistic = "p99"
		case strings.Contains(lower, "average"):
			statistic = "average"
		case strings.Contains(lower, "count") || strings.Contains(lower, "number of slow calls"):
			statistic = "sum"
		}
	}
	count, sum, minimum, maximum := value.Count, value.Sum, value.Min, value.Max
	if !finite(count) || *count < 0 {
		count = nil
	}
	if !finite(sum) {
		sum = nil
	}
	if !finite(minimum) || !finite(maximum) || *minimum > *maximum || !value.UseRange {
		minimum = nil
		maximum = nil
	}
	primary := value.Value
	if statistic == "current" {
		primary = value.Current
	} else if statistic == "sum" && (sum != nil || selection.Statistic == "sum") {
		// Explicit sums require the reported sum. Inferred count metrics may
		// retain value-only legacy responses when no usable sum is available.
		primary = sum
	}
	quality := analysis.QualityValid
	if !finite(primary) {
		primary = nil
		quality = analysis.QualityMissing
	}
	// Average and percentile buckets with no observations are unsuitable as
	// latency measurements; retain their provenance but not a false zero.
	if (statistic == "average" || statistic == "p95" || statistic == "p99") && count != nil && *count == 0 {
		primary = nil
		quality = analysis.QualityMissing
	}
	entities := map[string]string{"application": selection.ApplicationID}
	for k, v := range selection.EntityIDs {
		entities[k] = v
	}
	unit := selection.Unit
	if unit == "" {
		switch {
		case strings.Contains(lower, "(ms)"):
			unit = "ms"
		case strings.Contains(lower, "%") || strings.Contains(lower, "percentage"):
			unit = "percent"
		case strings.Contains(lower, "per minute"):
			unit = "per_minute"
		case strings.Contains(lower, "count") || strings.Contains(lower, "number of"):
			unit = "count"
		}
	}
	eligible := finite(primary) && (statistic == "value" || statistic == "current" || statistic == "sum" || statistic == "average" && count != nil && *count > 0 && sum != nil)
	pathHash := sha256.Sum256([]byte(row.Path))
	p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: projectID, RunID: runID, Source: "appdynamics", TimestampUTC: start, ReceivedAt: now.UTC(), MetricName: selection.Name, Kind: analysis.KindGauge, Value: primary, Unit: unit, Quality: quality, Dimensions: map[string]string{"application_id": selection.ApplicationID, "metric_id": row.ID.String(), "catalog_path_id": hex.EncodeToString(pathHash[:8]), "selection": selection.Name, "statistic": statistic}, Provider: &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: row.ID.String(), CatalogPath: row.Path, EntityIDs: entities, BucketStart: start, BucketEnd: end, ResolutionSeconds: resolution, Statistic: statistic, Count: count, Sum: sum, Min: minimum, Max: maximum, Settled: !end.After(now.Add(-time.Duration(settleSeconds) * time.Second)), AggregationEligible: eligible}}
	return p, p.Validate()
}

func (c *Client) metrics(ctx context.Context, m MetricSelection, from, to time.Time, depth int) ([]metricResponse, bool, error) {
	c.capMu.Lock()
	api := c.capabilities.MetricAPI
	c.capMu.Unlock()
	if api == "" {
		api = "metric-data-v2"
	}
	params := timeParams(from, to)
	params.Set("metric-path", m.Path)
	params.Set("rollup", "false")
	var rows []metricResponse
	err := c.query(ctx, "GET", "/rest/applications/"+m.ApplicationID+"/"+api, params, nil, false, &rows)
	var status *HTTPError
	if unsupportedMetricAPI(err) && api == "metric-data-v2" {
		api = "metric-data"
		err = c.query(ctx, "GET", "/rest/applications/"+m.ApplicationID+"/"+api, params, nil, false, &rows)
	}
	if err == nil {
		c.capMu.Lock()
		c.capabilities.MetricAPI = api
		c.capabilities.Metrics = true
		c.capMu.Unlock()
		if len(rows) > c.cfg.MaxSeries {
			return rows[:c.cfg.MaxSeries], true, nil
		}
		return rows, false, nil
	}
	if errors.As(err, &status) && status.Kind == "point budget exceeded" && depth < 8 && to.Sub(from) > 2*time.Minute {
		mid := from.Add(to.Sub(from) / 2).Truncate(time.Minute)
		left, lt, le := c.metrics(ctx, m, from, mid, depth+1)
		right, rt, re := c.metrics(ctx, m, mid, to, depth+1)
		if le != nil || re != nil {
			return append(left, right...), true, err
		}
		return append(left, right...), lt || rt, nil
	}
	return nil, false, err
}

func (c *Client) Collect(ctx context.Context, selection Selection, from, to time.Time, runID string) (Result, error) {
	result := Result{Points: []analysis.MetricPoint{}, Evidence: []Diagnostic{}, Coverage: map[string]Coverage{}}
	if err := selection.Validate(); err != nil {
		return result, err
	}
	from, to = from.UTC().Truncate(time.Millisecond), to.UTC().Truncate(time.Millisecond)
	now := time.Now().UTC()
	window := to.Sub(from)
	// Reconciliation may span a multi-day run. Query/request/response budgets,
	// rather than an artificial frame length, bound the work in each poll.
	if from.IsZero() || from.Year() < 1 || to.Year() > 9999 || !to.After(from) || !from.Add(window).Equal(to) || to.After(now.Add(time.Minute)) || len(runID) > 256 {
		return result, errors.New("collection requires a nonempty representable past window and bounded run ID")
	}
	if selection.RunHeader {
		selection.CollectorHeader = "X-Apex-Test-Run"
		selection.CollectorValue = runID
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.cfg.MaxPollSeconds)*time.Second)
	defer cancel()
	budget := &pollBudget{remaining: c.cfg.MaxRequestsPerPoll}
	ctx = context.WithValue(ctx, budgetKey{}, budget)
	ctx = withAnalyticsApplicationCache(ctx)
	settle := selection.SettleSeconds
	if settle == 0 {
		settle = 120
	}
	lookup := entityLookup{client: c, ctx: ctx, rows: map[string][]map[string]any{}, failures: map[string]error{}}
	seenPoints := map[string]bool{}
	seenEvidence := map[string]bool{}
	evidenceCounts := map[string]int{}
	evidenceLimits := map[string]int{"event": 1000, "violation": 200, "snapshot": 200, "log": 100, "transaction": 100}
	totalQueries := len(selection.Metrics) + len(selection.Analytics)
	for _, enabled := range []bool{selection.Events, selection.Violations, selection.Snapshots} {
		if enabled {
			totalQueries += len(selection.Applications)
		}
	}
	if selection.QueryOffset > totalQueries {
		return result, errors.New("query continuation exceeds selected query count")
	}
	result.Progress = QueryProgress{NextQuery: selection.QueryOffset, TotalQueries: totalQueries}
	queryIndex := 0
	deferred := false
	startQuery := func(analytics bool) bool {
		index := queryIndex
		queryIndex++
		if index < selection.QueryOffset || deferred {
			return false
		}
		budget.mu.Lock()
		remaining := budget.remaining
		budget.mu.Unlock()
		required := 1
		if !analytics && c.cfg.AuthType == AuthOAuth {
			c.authState.mu.Lock()
			if c.authState.token == "" || !time.Now().Before(c.authState.expires) {
				required = 2
			}
			c.authState.mu.Unlock()
		}
		if ctx.Err() != nil || remaining < required {
			deferred = true
			return false
		}
		result.Progress.NextQuery = index + 1
		return true
	}
	for i, m := range selection.Metrics {
		if !startQuery(false) {
			continue
		}
		key := fmt.Sprintf("metrics:%d", i)
		coverage := Coverage{Status: "complete", From: from, To: to}
		rows, truncated, err := c.metrics(ctx, m, from, to, 0)
		coverage.Truncated = truncated
		if err != nil {
			coverage.Status = "error"
			coverage.Message = err.Error()
		}
		if truncated {
			coverage.Status = "partial"
			coverage.Message = "metric series limit or point budget reached"
		}
		for _, row := range rows {
			if row.Path == "" || len(row.Path) > 4096 {
				coverage.Status, coverage.Truncated, coverage.Message = "partial", true, "metric catalog path exceeds bound"
				continue
			}
			resolved, identityErr := lookup.selection(m, row)
			if identityErr != nil {
				coverage.Status = "partial"
				coverage.Message = "metric entity identity could not be verified"
				// Missing discovery must not imply a match with the selected IDs.
				resolved.EntityIDs = map[string]string{}
				if len(selection.ScopeIDs) > 0 || len(m.EntityIDs) > 0 {
					continue
				}
			}
			row.Path = safeLabel(row.Path, 4096, c.credentials)
			for _, v := range row.Values {
				point, err := NormalizeMetric(resolved, row, v, selection.ProjectID, runID, now, settle)
				if err != nil {
					coverage.Status = "partial"
					coverage.Message = "some metric buckets were invalid"
					continue
				}
				if !point.Provider.BucketEnd.After(from) || !point.TimestampUTC.Before(to) {
					continue
				}
				if point.TimestampUTC.Before(from) || point.Provider.BucketEnd.After(to) {
					point.Provider.AggregationEligible = false
				}
				if !matchesScope(point.Provider.EntityIDs, selection.ScopeIDs) {
					continue
				}
				identity := m.ApplicationID + "|" + m.Name + "|" + point.Unit + "|" + point.Provider.Statistic + "|" + row.ID.String() + "|" + row.Path + "|" + strconv.FormatInt(v.Start, 10)
				if seenPoints[identity] {
					continue
				}
				if len(result.Points) >= c.cfg.MaxPoints {
					coverage.Status = "partial"
					coverage.Truncated = true
					coverage.Message = "metric point limit reached"
					break
				}
				seenPoints[identity] = true
				result.Points = append(result.Points, point)
				coverage.Points++
			}
		}
		if coverage.Points == 0 && coverage.Status == "complete" {
			coverage.Status = "partial"
			coverage.Message = "no metric buckets were returned"
		}
		result.Coverage[key] = coverage
	}
	for _, application := range selection.Applications {
		for _, feature := range []struct {
			kind    string
			enabled bool
		}{{"events", selection.Events}, {"violations", selection.Violations}, {"snapshots", selection.Snapshots}} {
			if !feature.enabled {
				continue
			}
			if !startQuery(false) {
				continue
			}
			key := feature.kind + ":" + application
			coverage := Coverage{Status: "complete", From: from, To: to}
			items, truncated, err := c.diagnostics(ctx, application, feature.kind, from, to, selection, 0)
			coverage.Truncated = truncated
			if err != nil {
				coverage.Status = "error"
				coverage.Message = err.Error()
			}
			if truncated {
				coverage.Status = "partial"
				coverage.Message = "evidence result cap reached"
			}
			for _, item := range items {
				identity := item.Kind + "|" + item.ApplicationID + "|" + item.ID
				if seenEvidence[identity] {
					continue
				}
				if evidenceCounts[item.Kind] >= evidenceLimits[item.Kind] {
					coverage.Status = "partial"
					coverage.Truncated = true
					break
				}
				seenEvidence[identity] = true
				evidenceCounts[item.Kind]++
				result.Evidence = append(result.Evidence, item)
				coverage.Evidence++
			}
			result.Coverage[key] = coverage
		}
	}
	for i, q := range selection.Analytics {
		if !startQuery(true) {
			continue
		}
		key := fmt.Sprintf("analytics:%d", i)
		queryFrom, queryTo := from.Truncate(time.Second), to.Truncate(time.Second)
		if queryFrom.Before(from) {
			queryFrom = queryFrom.Add(time.Second)
		}
		coverage := Coverage{Status: "complete", From: queryFrom, To: queryTo}
		if !queryTo.After(queryFrom) {
			coverage.From, coverage.To = from, to
			coverage.Status, coverage.Truncated, coverage.Message = "partial", true, "no complete second available for Analytics"
			result.Coverage[key] = coverage
			continue
		}
		items, points, truncated, err := c.analytics(ctx, q, selection.ProjectID, runID, queryFrom, queryTo, now, settle, selection.RunHeader)
		coverage.Truncated = truncated
		if err != nil {
			coverage.Status = "error"
			coverage.Message = err.Error()
		}
		if truncated {
			coverage.Status = "partial"
			coverage.Message = "Analytics result cap reached"
		}
		for _, point := range points {
			if len(result.Points) >= c.cfg.MaxPoints {
				coverage.Truncated = true
				coverage.Status = "partial"
				break
			}
			result.Points = append(result.Points, point)
			coverage.Points++
		}
		for _, item := range items {
			identity := item.Kind + "|" + item.ApplicationID + "|" + item.ID
			if seenEvidence[identity] {
				continue
			}
			if evidenceCounts[item.Kind] >= evidenceLimits[item.Kind] {
				coverage.Truncated = true
				coverage.Status = "partial"
				break
			}
			seenEvidence[identity] = true
			evidenceCounts[item.Kind]++
			result.Evidence = append(result.Evidence, item)
			coverage.Evidence++
		}
		result.Coverage[key] = coverage
	}
	sort.Slice(result.Points, func(i, j int) bool { return result.Points[i].TimestampUTC.Before(result.Points[j].TimestampUTC) })
	usedTopology := map[string]bool{}
	for _, point := range result.Points {
		for kind, id := range point.Provider.EntityIDs {
			usedTopology[point.Provider.EntityIDs["application"]+":"+kind+":"+id] = true
		}
	}
	for _, entity := range lookup.topology {
		if usedTopology[entity.EntityIDs["application"]+":"+entity.Type+":"+entity.ID] {
			result.Topology = append(result.Topology, entity)
		}
	}
	sort.Slice(result.Topology, func(i, j int) bool {
		return result.Topology[i].Type+":"+result.Topology[i].ID+":"+result.Topology[i].ParentID < result.Topology[j].Type+":"+result.Topology[j].ID+":"+result.Topology[j].ParentID
	})
	if lookup.truncated {
		result.Coverage["topology"] = Coverage{Status: "partial", From: from, To: to, Truncated: true, Message: "topology entity limit reached"}
	}
	sort.Slice(result.Evidence, func(i, j int) bool { return result.Evidence[i].TimestampUTC.Before(result.Evidence[j].TimestampUTC) })
	c.capMu.Lock()
	result.Capabilities = cloneCapabilities(c.capabilities)
	c.capMu.Unlock()
	result.Progress.Complete = result.Progress.NextQuery >= result.Progress.TotalQueries
	return result, nil
}

func matchesScope(ids map[string]string, scope map[string][]string) bool {
	for kind, wanted := range scope {
		if len(wanted) == 0 {
			continue
		}
		if ids[kind] == "" {
			return false
		}
		matched := false
		for _, id := range wanted {
			if id == ids[kind] {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func scalar(v any) string {
	switch value := v.(type) {
	case string:
		return value
	case json.Number:
		return value.String()
	case float64:
		return strconv.FormatFloat(value, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(value)
	}
	return ""
}
func number(v any) float64 { f, _ := strconv.ParseFloat(scalar(v), 64); return f }
func safeText(value string, limit int, credentials Credentials) string {
	return boundedSafeText(value, limit, credentials, false)
}
func safeLabel(value string, limit int, credentials Credentials) string {
	return boundedSafeText(value, limit, credentials, true)
}
func boundedSafeText(value string, limit int, credentials Credentials, label bool) string {
	for _, secret := range []string{credentials.ClientID, credentials.ClientSecret, credentials.Username, credentials.Password, credentials.EventsAPIKey} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
			value = strings.ReplaceAll(value, url.QueryEscape(secret), "[REDACTED]")
		}
	}
	if label {
		value = scrub.ValueLabel(value, scrub.Policy{})
	} else {
		value = scrub.Value(value, scrub.Policy{})
	}
	value = strings.Map(func(r rune) rune {
		if r < ' ' && r != '\t' {
			return ' '
		}
		return r
	}, value)
	if len(value) > limit {
		value = value[:limit]
	}
	return value
}

func (c *Client) diagnostics(ctx context.Context, application, kind string, from, to time.Time, s Selection, depth int) ([]Diagnostic, bool, error) {
	from, to = from.Truncate(time.Millisecond), to.Truncate(time.Millisecond)
	path := "/rest/applications/" + application + "/"
	params := timeParams(from, to)
	switch kind {
	case "events":
		path += "events"
		params.Set("event-types", "APPLICATION_ERROR,APPLICATION_WARNING,APPLICATION_INFO,AGENT_EVENT,DIAGNOSTIC_SESSION,POLICY_OPEN_CRITICAL,POLICY_OPEN_WARNING,POLICY_CLOSE_CRITICAL,POLICY_CLOSE_WARNING,CUSTOM")
		params.Set("severities", "INFO,WARN,ERROR")
	case "violations":
		path += "problems/healthrule-violations"
	case "snapshots":
		path += "request-snapshots"
		params.Set("maximum-results", "600")
		params.Set("need-props", "false")
		params.Set("need-exit-calls", "false")
		params.Set("first-in-chain", "true")
		for key, upstream := range map[string]string{"tier": "application-component-ids", "node": "application-component-node-ids", "business_transaction": "business-transaction-ids"} {
			if len(s.ScopeIDs[key]) > 0 {
				params.Set(upstream, strings.Join(s.ScopeIDs[key], ","))
			}
		}
		if s.CollectorHeader != "" {
			params.Set("data-collector-type", "Http Header")
			params.Set("data-collector-name", s.CollectorHeader)
			params.Set("data-collector-value", s.CollectorValue)
		}
	default:
		return nil, false, errors.New("unsupported evidence kind")
	}
	var rows []map[string]any
	if err := c.query(ctx, "GET", path, params, nil, false, &rows); err != nil {
		return nil, false, err
	}
	if len(rows) >= 600 && depth < 12 && to.Sub(from) > time.Second {
		mid := from.Add(to.Sub(from) / 2)
		left, lt, le := c.diagnostics(ctx, application, kind, from, mid, s, depth+1)
		right, rt, re := c.diagnostics(ctx, application, kind, mid, to, s, depth+1)
		if le != nil || re != nil {
			return append(left, right...), true, errors.New("evidence recovery reached collection budget")
		}
		return append(left, right...), lt || rt, nil
	}
	truncated := len(rows) >= 600
	if len(rows) > 600 {
		rows = rows[:600]
	}
	result := make([]Diagnostic, 0, len(rows))
	for _, row := range rows {
		id := scalar(row["id"])
		at := int64(number(row["eventTime"]))
		if kind == "violations" {
			at = int64(number(row["startTimeInMillis"]))
		}
		if kind == "snapshots" {
			id = scalar(row["requestGUID"])
			at = int64(number(row["serverStartTime"]))
			if at == 0 {
				at = int64(number(row["localStartTime"]))
			}
		}
		if (kind == "snapshots" && !requestGUID.MatchString(id)) || (kind != "snapshots" && !numericID.MatchString(id)) || at <= 0 {
			truncated = true
			continue
		}
		timestamp := time.UnixMilli(at).UTC()
		incidentStart, incidentEnd := time.Time{}, time.Time{}
		if kind == "violations" {
			incidentStart = timestamp
			if ended := int64(number(row["endTimeInMillis"])); ended > 0 {
				incidentEnd = time.UnixMilli(ended).UTC()
				if incidentEnd.Before(incidentStart) || incidentEnd.Before(from) {
					continue
				}
				if incidentEnd.Before(to) {
					timestamp = incidentEnd
				}
			}
			if timestamp.Before(from) && (incidentEnd.IsZero() || !incidentEnd.Before(to)) {
				// An already-open incident returned for this window is observed at
				// its lower bound; its actual start remains explicit provenance.
				timestamp = from
			}
		}
		if timestamp.Before(from) || !timestamp.Before(to) {
			continue
		}
		d := Diagnostic{ID: id, Kind: map[string]string{"events": "event", "violations": "violation", "snapshots": "snapshot"}[kind], ApplicationID: application, TimestampUTC: timestamp, EntityIDs: map[string]string{"application": application}, Severity: safeText(scalar(row["severity"]), 32, c.credentials), Status: safeText(scalar(row["incidentStatus"]), 64, c.credentials), Properties: map[string]string{"correlation": "entity_time"}}
		for raw, normalized := range map[string]string{"applicationComponentId": "tier", "applicationComponentNodeId": "node", "businessTransactionId": "business_transaction"} {
			if v := scalar(row[raw]); numericID.MatchString(v) {
				d.EntityIDs[normalized] = v
			}
		}
		for _, raw := range []string{"affectedEntities", "affectedEntityDefinition"} {
			switch value := row[raw].(type) {
			case []any:
				for _, item := range value {
					if entity, ok := item.(map[string]any); ok {
						addEntity(d.EntityIDs, entity)
					}
				}
			case map[string]any:
				addEntity(d.EntityIDs, value)
			}
		}
		if !matchesScope(d.EntityIDs, s.ScopeIDs) {
			continue
		}
		// Narrative text can contain captured SQL, stack traces, identities or
		// credentials. Only explicit safe classification fields are projected.
		if kind == "snapshots" {
			d.Summary = "Transaction snapshot"
			for raw, normalized := range map[string]string{"userExperience": "experience", "timeTakenInMilliSecs": "duration_ms", "errorOccured": "error"} {
				if v := scalar(row[raw]); v != "" {
					d.Properties[normalized] = safeText(v, 64, c.credentials)
				}
			}
			d.Properties["requestguid"] = id
			if s.RunHeader && s.CollectorHeader == "X-Apex-Test-Run" {
				d.Properties["run_id"] = s.CollectorValue
				d.Properties["correlation"] = "run_header"
			}
			d.DeepLink = strings.TrimRight(c.cfg.Endpoint, "/") + c.cfg.ContextPath + "/#location=APP_SNAPSHOT_VIEWER&application=" + application + "&requestGUID=" + id
		} else {
			d.Summary = "AppDynamics " + safeText(scalar(row["type"]), 64, c.credentials)
			if kind == "violations" {
				d.Summary = "Health rule violation"
				d.Properties["incident_start_utc"] = incidentStart.Format(time.RFC3339Nano)
				if !incidentEnd.IsZero() {
					d.Properties["incident_end_utc"] = incidentEnd.Format(time.RFC3339Nano)
				}
			}
		}
		if numericID.MatchString(id) {
			location := "APP_EVENT_VIEWER_MODAL&eventSummary=" + id
			if kind == "violations" {
				location = "APP_INCIDENT_DETAIL&incident=" + id
			}
			d.DeepLink = strings.TrimRight(c.cfg.Endpoint, "/") + c.cfg.ContextPath + "/#location=" + location
		}
		result = append(result, d)
	}
	c.capMu.Lock()
	switch kind {
	case "events":
		c.capabilities.Events = true
	case "violations":
		c.capabilities.Violations = true
	case "snapshots":
		c.capabilities.Snapshots = true
	}
	c.capMu.Unlock()
	return result, truncated, nil
}
func addEntity(ids map[string]string, row map[string]any) {
	kind := ""
	switch scalar(row["entityType"]) {
	case "APPLICATION_COMPONENT":
		kind = "tier"
	case "APPLICATION_COMPONENT_NODE":
		kind = "node"
	case "BUSINESS_TRANSACTION":
		kind = "business_transaction"
	}
	if v := scalar(row["entityId"]); kind != "" && numericID.MatchString(v) {
		ids[kind] = v
	}
}

type analyticsResponse struct {
	Fields []struct {
		Label string `json:"label"`
	} `json:"fields"`
	Results  [][]any `json:"results"`
	MoreData bool    `json:"moreData"`
	ScrollID string  `json:"scrollid"`
}

func (c *Client) analytics(ctx context.Context, q AnalyticsQuery, projectID, runID string, from, to, now time.Time, settle int, runHeader bool) ([]Diagnostic, []analysis.MetricPoint, bool, error) {
	window := to.Sub(from)
	if !to.After(from) || !from.Add(window).Equal(to) || int64(window/time.Second) > int64(math.MaxInt) {
		return nil, nil, false, errors.New("Analytics query window exceeds representable duration")
	}
	if err := c.validateAnalyticsApplication(ctx, q); err != nil {
		return nil, nil, false, err
	}
	query, err := CompileAnalyticsQuery(q, runID, runHeader)
	if err != nil {
		return nil, nil, false, err
	}
	params := url.Values{"start": {strconv.FormatInt(from.UnixMilli(), 10)}, "end": {strconv.FormatInt(to.UnixMilli()-1, 10)}, "limit": {"1000"}}
	var results []analyticsResponse
	if err := c.query(ctx, "POST", "/events/query", params, []byte(query), true, &results); err != nil {
		return nil, nil, false, err
	}
	if len(results) > 1 {
		return nil, nil, false, errors.New("unexpected Analytics multi-query response")
	}
	kind, statistic, unit := q.Kind, q.Statistic, q.Unit
	if kind == "" {
		kind = "transaction"
	}
	if statistic == "" {
		statistic = "value"
	}
	if unit == "" {
		unit = "count"
	}
	queryID := AnalyticsMetricID(q)
	diagnostics := []Diagnostic{}
	points := []analysis.MetricPoint{}
	truncated := false
	for _, result := range results {
		if len(result.Fields) > 64 {
			return nil, nil, false, errors.New("Analytics field count exceeds bound")
		}
		if result.MoreData || len(result.Results) >= 1000 {
			truncated = true
		}
		if len(result.Results) > 1000 {
			result.Results = result.Results[:1000]
		}
		for _, row := range result.Results {
			if len(row) != len(result.Fields) {
				return nil, nil, false, errors.New("invalid Analytics result shape")
			}
			allowed := map[string]string{"query_name": q.Name, "correlation": "entity_time"}
			if runHeader && q.RunIDField != "" {
				allowed["correlation"] = "run_header"
				allowed["run_id"] = runID
			}
			var bucket int64
			var projectedBucket, projectedValue bool
			var measurement, count, sum *float64
			for i, f := range result.Fields {
				label := strings.ToLower(f.Label)
				if label == "applicationid" || q.ApplicationIDField != "" && label == strings.ToLower(q.ApplicationIDField) {
					if scalar(row[i]) != q.ApplicationID {
						return nil, nil, false, errors.New("Analytics result differs from its approved application scope")
					}
				}
				if q.ApplicationName != "" && (label == "application" || label == "applicationname") && scalar(row[i]) != q.ApplicationName {
					return nil, nil, false, errors.New("Analytics result differs from its verified application name")
				}
				switch label {
				case "eventtimestamp", "timestamp", "bucket":
					projectedBucket = true
					bucket = int64(number(row[i]))
				case "value", "count", "sum":
					value, err := strconv.ParseFloat(scalar(row[i]), 64)
					if label == "value" {
						projectedValue = true
					}
					if err != nil || math.IsInf(value, 0) || math.IsNaN(value) {
						continue
					}
					switch label {
					case "value":
						measurement = &value
					case "count":
						if value >= 0 {
							count = &value
						}
					case "sum":
						sum = &value
					}
				case "application", "applicationname", "transactionname", "tiername", "nodename", "userexperience":
					allowed[label] = safeText(scalar(row[i]), 128, c.credentials)
				case "requestguid":
					if guid := scalar(row[i]); requestGUID.MatchString(guid) {
						allowed[label] = guid
					}
				}
			}
			if projectedBucket && bucket <= 0 {
				truncated = true
				continue
			}
			if !projectedBucket {
				bucket = from.UnixMilli()
			}
			at := time.UnixMilli(bucket).UTC()
			if at.Before(from) || !at.Before(to) {
				continue
			}
			if projectedValue || kind == "metric" {
				if !projectedValue {
					return nil, nil, false, errors.New("Analytics metrics require an explicit value alias")
				}
				duration := int(to.Sub(from) / time.Second)
				if projectedBucket {
					if q.BucketSeconds == 0 {
						return nil, nil, false, errors.New("Analytics numeric buckets require bucket_seconds")
					}
					duration = q.BucketSeconds
				}
				if duration <= 0 || int64(duration) > math.MaxInt64/int64(time.Second) {
					return nil, nil, false, errors.New("Analytics metric window must have a positive representable duration")
				}
				end := at.Add(time.Duration(duration) * time.Second)
				quality := analysis.QualityValid
				if !finite(measurement) || statistic == "average" && count != nil && *count == 0 {
					measurement = nil
					quality = analysis.QualityMissing
				}
				queryWindow := !projectedBucket
				if queryWindow != AnalyticsQueryWindow(q) {
					return nil, nil, false, errors.New("Analytics numeric bucket shape differs from the approved query projection")
				}
				eligible := finite(measurement) && AnalyticsAggregationEligible(q, quality, count, sum)
				if end.After(to) || !projectedBucket && end.Sub(at) != to.Sub(from) {
					eligible = false
				}
				p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: projectID, RunID: runID, Source: "appdynamics", TimestampUTC: at, ReceivedAt: now.UTC(), MetricName: q.Name, Kind: analysis.KindGauge, Value: measurement, Unit: unit, Quality: quality, Dimensions: map[string]string{"application_id": q.ApplicationID, "analytics_query": q.Name, "query_id": queryID, "statistic": statistic}, Provider: &analysis.ProviderMeasurement{Name: "appdynamics", MetricID: queryID, EntityIDs: map[string]string{"application": q.ApplicationID}, BucketStart: at, BucketEnd: end, ResolutionSeconds: duration, Statistic: statistic, Count: count, Sum: sum, Settled: !end.After(now.Add(-time.Duration(settle) * time.Second)), AggregationEligible: eligible, QueryWindow: queryWindow}}
				if err := p.Validate(); err != nil {
					return nil, nil, false, errors.New("invalid Analytics metric bucket")
				}
				points = append(points, p)
			} else {
				encoded, _ := json.Marshal(allowed)
				hash := sha256.Sum256(append([]byte(queryID+"|"+strconv.FormatInt(bucket, 10)), encoded...))
				diagnostics = append(diagnostics, Diagnostic{ID: hex.EncodeToString(hash[:16]), Kind: kind, ApplicationID: q.ApplicationID, TimestampUTC: at, Summary: "Analytics query result: " + q.Name, EntityIDs: map[string]string{"application": q.ApplicationID}, Properties: allowed})
			}
		}
	}
	c.capMu.Lock()
	c.capabilities.Analytics = true
	c.capMu.Unlock()
	return diagnostics, points, truncated, nil
}
