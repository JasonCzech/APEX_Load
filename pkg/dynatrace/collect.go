package dynatrace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

type operation struct {
	key, kind string
	metric    *MetricSelection
	grail     *GrailQuery
}

func operations(s Selection) []operation {
	out := []operation{}
	for i := range s.Metrics {
		out = append(out, operation{key: "metrics:" + s.Metrics[i].Name, kind: "metric", metric: &s.Metrics[i]})
	}
	if s.Problems {
		out = append(out, operation{key: "problems", kind: "problem"})
	}
	if s.Events {
		out = append(out, operation{key: "events", kind: "event"})
	}
	if s.TopologyMode != "" {
		out = append(out, operation{key: "topology", kind: s.TopologyMode})
	}
	for i := range s.Grail {
		out = append(out, operation{key: "grail:" + s.Grail[i].Name, kind: "grail", grail: &s.Grail[i]})
	}
	return out
}
func (c *Client) Collect(ctx context.Context, s Selection, from, to time.Time, runID string) (Result, error) {
	result := Result{Points: []analysis.MetricPoint{}, Evidence: []Diagnostic{}, Coverage: map[string]Coverage{}, Topology: []Entity{}}
	if err := s.Validate(); err != nil {
		return result, err
	}
	if from.Location() != time.UTC || to.Location() != time.UTC || !to.After(from) || to.Sub(from) > 31*24*time.Hour || len(runID) > 256 {
		return result, errors.New("invalid bounded collection window")
	}
	ctx, cancel := c.pollContext(ctx)
	defer cancel()
	ops := operations(s)
	result.Progress.TotalQueries = len(ops)
	cursor, err := decodeCursor(s.Cursor)
	if err != nil {
		return result, err
	}
	fingerprint := selectionFingerprint(s, from, to)
	if s.Cursor != "" && (cursor.Fingerprint != fingerprint || cursor.Query != s.QueryOffset) {
		return result, errors.New("continuation does not match frozen selection and window")
	}
	settle := s.SettleSeconds
	if settle == 0 {
		settle = 120
	}
	result.Progress.QueryCostKnown = true
	for index := s.QueryOffset; index < len(ops); index++ {
		op := ops[index]
		cursor.Query = index
		cursor.Fingerprint = fingerprint
		cursor.From, cursor.To = from, to
		coverage := Coverage{Status: "complete", From: from, To: to}
		pointsBefore, evidenceBefore := len(result.Points), len(result.Evidence)
		truncated := false
		pending := false
		switch op.kind {
		case "metric":
			var points []analysis.MetricPoint
			queryFrom, queryTo := from, to
			if len(cursor.MetricWindows) > 0 {
				queryFrom, queryTo = cursor.MetricWindows[0].From, cursor.MetricWindows[0].To
			}
			points, truncated, err = c.metrics(ctx, *op.metric, s.ProjectID, queryFrom, queryTo, settle)
			var rejection *HTTPError
			if errors.As(err, &rejection) && rejection.Kind == "query_budget" && queryTo.Sub(queryFrom) >= 2*time.Minute && len(cursor.MetricWindows) < 16 {
				midpoint := queryFrom.Add(queryTo.Sub(queryFrom) / 2)
				remaining := []metricWindow{}
				if len(cursor.MetricWindows) > 0 {
					remaining = cursor.MetricWindows[1:]
				}
				cursor.MetricWindows = append([]metricWindow{{From: queryFrom, To: midpoint}, {From: midpoint, To: queryTo}}, remaining...)
				pending = true
				err = nil
				coverage.Message = "metric data-point budget requires smaller query windows"
			} else if err == nil && len(cursor.MetricWindows) > 0 {
				cursor.MetricWindows = cursor.MetricWindows[1:]
				pending = len(cursor.MetricWindows) > 0
			}
			if len(points) > c.cfg.MaxPoints-cursor.MetricPoints {
				points = points[:max(0, c.cfg.MaxPoints-cursor.MetricPoints)]
				truncated = true
				cursor.MetricWindows = nil
				pending = false
			}
			cursor.MetricPoints += len(points)
			truncated = truncated || cursor.Truncated
			cursor.Truncated = truncated
			result.Points = append(result.Points, points...)
		case "problem", "event":
			var evidence []Diagnostic
			evidence, cursor, truncated, err = c.diagnostics(ctx, op.kind, s, from, to, cursor)
			result.Evidence = append(result.Evidence, evidence...)
			pending = cursor.Page != ""
		case "classic":
			var topology []Entity
			topology, cursor, truncated, err = c.classicTopology(ctx, s, from, to, cursor)
			result.Topology = append(result.Topology, topology...)
			pending = cursor.Page != ""
		case "smartscape":
			query := smartscapeQuery(s, c.cfg.MaxSeries+1)
			if cursor.TopologyPhase == "edges" {
				query = smartscapeEdgesQuery(s, c.cfg.MaxSeries+1)
			}
			var response grailResponse
			response, cursor, err = c.executeGrail(ctx, query, from, to, cursor, &result.Progress)
			pending = cursor.Token != ""
			if err == nil && !pending {
				for _, r := range response.Result.Records {
					if len(result.Topology) >= c.cfg.MaxSeries {
						truncated = true
						break
					}
					if cursor.TopologyPhase == "edges" {
						source, target := text(r["source_id"]), text(r["target_id"])
						if !providerEntityID.MatchString(source) || !providerEntityID.MatchString(target) || len(s.SmartscapeEntityIDs) > 0 && !containsIdentity(s.SmartscapeEntityIDs, source) && !containsIdentity(s.SmartscapeEntityIDs, target) {
							truncated = true
							continue
						}
						kind := boundedText(text(r["type"]), 128)
						result.Topology = append(result.Topology, Entity{ID: "smartscape-edge:" + hashIdentity(source, kind, target), Name: kind, Type: "relationship", EntityIDs: map[string]string{"source_id": source, "target_id": target, "source_type": boundedText(text(r["source_type"]), 128), "target_type": boundedText(text(r["target_type"]), 128)}})
						continue
					}
					id := text(r["id"])
					if !providerEntityID.MatchString(id) || len(s.SmartscapeEntityIDs) > 0 && !containsIdentity(s.SmartscapeEntityIDs, id) {
						truncated = true
						continue
					}
					ids := map[string]string{"smartscape_id": id}
					if classic := text(r["id_classic"]); entityID.MatchString(classic) {
						ids["classic_id"] = classic
					}
					result.Topology = append(result.Topology, Entity{ID: id, Name: boundedText(text(r["name"]), 256), Type: boundedText(text(r["type"]), 128), EntityIDs: ids})
				}
				truncated = truncated || grailTruncated(response) || cursor.Truncated
				if cursor.TopologyPhase == "" {
					cursor.TopologyPhase = "edges"
					cursor.Truncated = truncated
					pending = true
				}
			}
		case "grail":
			var query string
			query, err = BuildGrailQuery(*op.grail, from, to, runID)
			if err == nil {
				var response grailResponse
				response, cursor, err = c.executeGrail(ctx, query, from, to, cursor, &result.Progress)
				pending = cursor.Token != ""
				if err == nil && !pending {
					var points []analysis.MetricPoint
					var evidence []Diagnostic
					points, evidence, truncated, err = c.normalizeGrail(*op.grail, response, s.ProjectID, from, to, runID, settle)
					result.Points = append(result.Points, points...)
					result.Evidence = append(result.Evidence, evidence...)
				}
			}
		}
		if err != nil {
			coverage.Status = capabilityStatus(err)
			coverage.Message = err.Error()
			if errors.Is(err, ErrBudget) || errors.Is(err, context.DeadlineExceeded) {
				pending = true
			}
			if op.kind == "grail" || op.kind == "smartscape" {
				result.Progress.QueryCostKnown = false
			}
		}
		if truncated {
			coverage.Status = "partial"
			coverage.Truncated = true
			coverage.Message = "upstream or local collection bound reached"
		}
		if pending && err == nil {
			coverage.Status = "pending"
			if coverage.Message == "" {
				coverage.Message = "bounded query continuation outstanding"
			}
		}
		coverage.Points = len(result.Points) - pointsBefore
		coverage.Evidence = len(result.Evidence) - evidenceBefore
		result.Coverage[op.key] = coverage
		if len(result.Points) > c.cfg.MaxPoints {
			result.Points = result.Points[:c.cfg.MaxPoints]
			coverage.Points = max(0, len(result.Points)-pointsBefore)
			coverage.Truncated = true
			coverage.Status = "partial"
			result.Coverage[op.key] = coverage
		}
		if len(result.Evidence) > 500 {
			result.Evidence = result.Evidence[:500]
			coverage.Evidence = max(0, len(result.Evidence)-evidenceBefore)
			coverage.Truncated = true
			coverage.Status = "partial"
			result.Coverage[op.key] = coverage
		}
		if pending {
			result.Progress.NextQuery = index
			result.Progress.Cursor = encodeCursor(cursor)
			if !cursor.Expires.IsZero() {
				at := cursor.Expires
				result.Progress.CursorExpiresAt = &at
			}
			return result, nil
		}
		result.Progress.NextQuery = index + 1
		cursor = continuation{}
		if ctx.Err() != nil {
			return result, nil
		}
	}
	result.Progress.Complete = true
	result.Progress.NextQuery = len(ops)
	result.Progress.Cursor = ""
	return result, nil
}
func smartscapeQuery(s Selection, limit int) string {
	nodeType := s.SmartscapeNodeType
	if nodeType == "" {
		nodeType = "*"
	}
	query := "smartscapeNodes " + strconv.Quote(nodeType)
	if len(s.SmartscapeEntityIDs) > 0 {
		raw, _ := json.Marshal(s.SmartscapeEntityIDs)
		query += " | filter in(toString(id), {" + strings.Trim(string(raw), "[]") + "})"
	}
	return query + " | fields id, id_classic, name, type | limit " + strconv.Itoa(limit)
}
func smartscapeEdgesQuery(s Selection, limit int) string {
	query := "smartscapeEdges \"*\""
	if len(s.SmartscapeEntityIDs) > 0 {
		raw, _ := json.Marshal(s.SmartscapeEntityIDs)
		ids := "{" + strings.Trim(string(raw), "[]") + "}"
		query += " | filter in(toString(source_id), " + ids + ") or in(toString(target_id), " + ids + ")"
	} else if s.SmartscapeNodeType != "" {
		query += " | filter source_type == " + strconv.Quote(s.SmartscapeNodeType) + " or target_type == " + strconv.Quote(s.SmartscapeNodeType)
	}
	return query + " | fields type, source_id, source_type, target_id, target_type | limit " + strconv.Itoa(limit)
}
func selectionFingerprint(s Selection, from, to time.Time) string {
	s.QueryOffset = 0
	s.Cursor = ""
	raw, _ := json.Marshal(s)
	return hashIdentity(string(raw), from.String(), to.String())
}
func (c *Client) diagnostics(ctx context.Context, kind string, s Selection, from, to time.Time, cursor continuation) ([]Diagnostic, continuation, bool, error) {
	params := url.Values{"from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)}, "pageSize": {"100"}}
	if s.EntitySelector != "" {
		params.Set("entitySelector", s.EntitySelector)
	}
	if kind == "problem" && s.ProblemSelector != "" {
		params.Set("problemSelector", s.ProblemSelector)
	}
	if kind == "event" && s.EventSelector != "" {
		params.Set("eventSelector", s.EventSelector)
	}
	if cursor.Page != "" {
		params = url.Values{"nextPageKey": {cursor.Page}}
	}
	path := "/api/v2/problems"
	if kind == "event" {
		path = "/api/v2/events"
	}
	data, err := c.request(ctx, false, http.MethodGet, path, params, nil)
	if err != nil {
		return nil, cursor, false, err
	}
	var payload struct {
		Next     string `json:"nextPageKey"`
		Problems []struct {
			ID                                      string `json:"problemId"`
			DisplayID, Title, SeverityLevel, Status string
			StartTime, EndTime                      int64
			AffectedEntities                        []classicEntityStub `json:"affectedEntities"`
		} `json:"problems"`
		Events []struct {
			ID                       string `json:"eventId"`
			Title, EventType, Status string
			StartTime, EndTime       int64
			EntityID                 classicEntityStub `json:"entityId"`
		} `json:"events"`
	}
	if json.Unmarshal(data, &payload) != nil {
		return nil, cursor, false, fmt.Errorf("invalid diagnostic response: %w", ErrInvalidResponse)
	}
	if len(payload.Problems) > 100 || len(payload.Events) > 100 {
		return nil, cursor, false, errors.New("diagnostic page exceeds requested bound")
	}
	result := []Diagnostic{}
	for _, p := range payload.Problems {
		if p.ID == "" || len(p.ID) > 256 {
			continue
		}
		ids := map[string]string{}
		for i, e := range p.AffectedEntities {
			if i >= 16 {
				break
			}
			if entityID.MatchString(e.EntityID.ID) {
				ids["entity_"+strconv.Itoa(i)] = e.EntityID.ID
			}
		}
		at := time.UnixMilli(p.StartTime).UTC()
		properties := map[string]string{"display_id": boundedText(p.DisplayID, 128), "correlation": "entity_time"}
		if p.EndTime > 0 {
			properties["end_time"] = time.UnixMilli(p.EndTime).UTC().Format(time.RFC3339Nano)
		}
		result = append(result, Diagnostic{Provider: "dynatrace", Backend: "classic", ID: p.ID, Kind: "problem", Severity: boundedText(p.SeverityLevel, 128), Summary: boundedText(p.Title, 4096), Status: boundedText(p.Status, 128), TimestampUTC: at, EntityIDs: ids, Properties: properties, DeepLink: strings.TrimRight(c.cfg.EnvironmentURL, "/") + "/#problems/problemdetails;pid=" + url.PathEscape(p.ID)})
	}
	for _, e := range payload.Events {
		if e.ID == "" || len(e.ID) > 256 {
			continue
		}
		ids := map[string]string{}
		if entityID.MatchString(e.EntityID.EntityID.ID) {
			ids["entity"] = e.EntityID.EntityID.ID
		}
		props := map[string]string{"event_type": boundedText(e.EventType, 128), "correlation": "entity_time"}
		if e.EndTime > 0 {
			props["end_time"] = time.UnixMilli(e.EndTime).UTC().Format(time.RFC3339Nano)
		}
		result = append(result, Diagnostic{Provider: "dynatrace", Backend: "classic", ID: e.ID, Kind: "event", Summary: boundedText(e.Title, 4096), Status: boundedText(e.Status, 128), TimestampUTC: time.UnixMilli(e.StartTime).UTC(), EntityIDs: ids, Properties: props})
	}
	truncated := false
	cursor.Pages++
	if cursor.Pages >= 10 && payload.Next != "" {
		truncated = true
		cursor.Page = ""
	} else if payload.Next == cursor.Page && payload.Next != "" {
		return nil, cursor, false, errors.New("repeated diagnostic continuation")
	} else {
		cursor.Page = payload.Next
	}
	return result, cursor, truncated, nil
}
func (c *Client) classicTopology(ctx context.Context, s Selection, from, to time.Time, cursor continuation) ([]Entity, continuation, bool, error) {
	selector := s.EntitySelector
	if selector == "" {
		selector = `type("SERVICE")`
	}
	params := url.Values{"entitySelector": {selector}, "from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)}, "pageSize": {"100"}, "fields": {classicRelationshipFields}}
	if cursor.Page != "" {
		params = url.Values{"nextPageKey": {cursor.Page}}
	}
	raw, err := c.request(ctx, false, http.MethodGet, "/api/v2/entities", params, nil)
	if err != nil {
		return nil, cursor, false, err
	}
	var payload struct {
		Entities []classicEntity `json:"entities"`
		Next     string          `json:"nextPageKey"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil, cursor, false, fmt.Errorf("invalid topology response: %w", ErrInvalidResponse)
	}
	if len(payload.Entities) > 100 {
		return nil, cursor, false, errors.New("topology page exceeds requested bound")
	}
	items := []Entity{}
	for _, e := range payload.Entities {
		if !entityID.MatchString(e.ID) {
			continue
		}
		items = append(items, normalizeEntity(e))
	}
	cursor.Pages++
	truncated := cursor.Pages >= 2 && payload.Next != ""
	if truncated {
		cursor.Page = ""
	} else {
		cursor.Page = payload.Next
	}
	return items, cursor, truncated, nil
}
func (c *Client) Test(ctx context.Context) (Capabilities, error) {
	s := Selection{ProjectID: "connection-probe"}
	if c.cfg.EnvironmentURL != "" {
		s.Metrics = []MetricSelection{{Name: "cpu_probe", MetricSelector: "builtin:host.cpu.usage", Aggregation: "avg"}}
		s.Problems = true
		s.Events = true
		s.TopologyMode = "classic"
	}
	if c.cfg.PlatformURL != "" {
		s.Grail = []GrailQuery{{Name: "logs_probe", Query: "fetch logs | limit 1", Kind: "log"}, {Name: "spans_probe", Query: "fetch spans | limit 1", Kind: "span"}, {Name: "events_probe", Query: "fetch events | limit 1", Kind: "event"}, {Name: "metrics_probe", Query: "timeseries value = avg(dt.host.cpu.usage)", Kind: "metric", ValueField: "value", Unit: "%", Statistic: "average", BucketSeconds: 60}}
		s.TopologyMode = "smartscape"
	}
	caps, err := c.TestSelection(ctx, s)
	if err != nil {
		return caps, err
	}
	if c.cfg.EnvironmentURL != "" && c.cfg.PlatformURL != "" {
		classic, probeErr := c.TestSelection(ctx, Selection{ProjectID: "connection-probe", TopologyMode: "classic"})
		if probeErr != nil {
			caps.Status["classic_topology"] = capabilityStatus(probeErr)
		} else {
			caps.ClassicTopology = classic.ClassicTopology
			caps.Status["classic_topology"] = classic.Status["classic_topology"]
		}
	}
	c.populateCapabilityIdentity(&caps)
	return caps, nil
}
func (c *Client) TestSelection(ctx context.Context, s Selection) (Capabilities, error) {
	caps := Capabilities{Status: map[string]string{}}
	if err := s.Validate(); err != nil {
		return caps, err
	}
	s.QueryOffset = 0
	s.Cursor = ""
	from, to := c.now().Add(-5*time.Minute).UTC(), c.now().Add(-2*time.Minute).UTC()
	// Inspection can span multiple bounded collection passes. Each pass retains
	// its 30-second/request budget, and the complete setup operation remains
	// capped independently of the caller's deadline.
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	pendingCursor := ""
	defer func() {
		if pendingCursor != "" {
			if cursor, err := decodeCursor(pendingCursor); err == nil {
				_ = c.cancelAbandoned(cursor)
			}
		}
	}()
	result := Result{Coverage: map[string]Coverage{}}
	for pass := 0; pass < 64; pass++ {
		next, err := c.Collect(ctx, s, from, to, "")
		if err == nil || next.Progress.Cursor != "" {
			pendingCursor = next.Progress.Cursor
		}
		if err != nil {
			return caps, err
		}
		for key, coverage := range next.Coverage {
			result.Coverage[key] = coverage
		}
		if next.Progress.Complete || ctx.Err() != nil {
			break
		}
		if next.Progress.NextQuery == s.QueryOffset && next.Progress.Cursor == s.Cursor {
			break
		}
		s.QueryOffset, s.Cursor = next.Progress.NextQuery, next.Progress.Cursor
		if s.Cursor != "" {
			timer := time.NewTimer(100 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
			case <-timer.C:
			}
		}
	}
	for _, op := range operations(s) {
		coverage, ok := result.Coverage[op.key]
		status := "not_verified"
		if ok {
			status = coverage.Status
		}
		if status == "complete" {
			status = "available_no_data"
			if coverage.Points > 0 || coverage.Evidence > 0 {
				status = "available"
			}
		}
		caps.Status[op.key] = status
		available := status == "available" || status == "available_no_data"
		switch op.kind {
		case "metric":
			caps.Metrics = caps.Metrics || available
		case "problem":
			caps.Problems = available
		case "event":
			caps.Events = available
		case "classic":
			caps.ClassicTopology = available
			caps.Status["classic_topology"] = status
		case "smartscape":
			caps.Smartscape = available
			caps.Status["smartscape"] = status
		case "grail":
			switch op.grail.Kind {
			case "log":
				caps.Logs = caps.Logs || available
			case "span", "trace":
				caps.Spans = caps.Spans || available
			case "metric":
				caps.GrailMetrics = caps.GrailMetrics || available
			case "event":
				caps.GrailEvents = caps.GrailEvents || available
			}
		}
	}
	for _, group := range []struct {
		key     string
		enabled *bool
		keys    []string
	}{{"metrics", &caps.Metrics, nil}, {"logs", &caps.Logs, nil}, {"spans", &caps.Spans, nil}, {"grail_metrics", &caps.GrailMetrics, nil}, {"grail_events", &caps.GrailEvents, nil}} {
		selected, all := false, true
		for _, op := range operations(s) {
			match := group.key == "metrics" && op.kind == "metric" || op.grail != nil && (group.key == "logs" && op.grail.Kind == "log" || group.key == "spans" && (op.grail.Kind == "span" || op.grail.Kind == "trace") || group.key == "grail_metrics" && op.grail.Kind == "metric" || group.key == "grail_events" && op.grail.Kind == "event")
			if !match {
				continue
			}
			selected = true
			status := caps.Status[op.key]
			all = all && (status == "available" || status == "available_no_data")
		}
		if selected {
			*group.enabled = all
			if all {
				caps.Status[group.key] = "available"
			} else {
				caps.Status[group.key] = "not_verified"
			}
		}
	}
	c.populateCapabilityIdentity(&caps)
	return caps, nil
}
func (c *Client) populateCapabilityIdentity(caps *Capabilities) {
	if caps.Metrics || caps.Problems || caps.Events || caps.ClassicTopology {
		caps.EnvironmentID = EnvironmentIdentity(c.cfg.EnvironmentURL)
		caps.EnvironmentOrigin = c.cfg.EnvironmentURL
		caps.VerifiedOrigins = appendUnique(caps.VerifiedOrigins, c.cfg.EnvironmentURL)
	}
	if caps.GrailMetrics || caps.GrailEvents || caps.Logs || caps.Spans || caps.Smartscape {
		caps.PlatformEnvironmentID = EnvironmentIdentity(c.cfg.PlatformURL)
		caps.PlatformOrigin = c.cfg.PlatformURL
		caps.VerifiedOrigins = appendUnique(caps.VerifiedOrigins, c.cfg.PlatformURL)
	}
	if caps.EnvironmentID != "" {
		caps.TenantID = caps.EnvironmentID
	} else {
		caps.TenantID = caps.PlatformEnvironmentID
	}
}

// ValidateResult is the provider-specific collector trust boundary. Executors
// must additionally bind source/project/run/owner and durable limits.
func ValidateResult(s Selection, cfg Config, from, to time.Time, runID string, result Result) error {
	if err := s.Validate(); err != nil {
		return err
	}
	cfg = cfg.Defaults()
	if len(result.Points) > cfg.MaxPoints || len(result.Evidence) > 500 || len(result.Topology) > 200 || len(result.Coverage) > 32 || len(result.Progress.Cursor) > 16<<10 {
		return errors.New("provider result exceeds bounds")
	}
	metrics := map[string]string{}
	units := map[string]string{}
	stats := map[string]string{}
	backends := map[string]string{}
	eligibleStatistics := map[string]bool{}
	grail := map[string]GrailQuery{}
	for _, m := range s.Metrics {
		metrics[m.Name] = MetricID(m)
		if m.Unit != "" {
			units[m.Name] = NormalizeUnit(m.Unit)
		}
		stats[m.Name] = metricStatistic(m)
		eligibleStatistics[m.Name] = m.Aggregation != "p95" && m.Aggregation != "p99" && (m.Aggregation != "avg" || m.Semantics == "gauge")
		backends[m.Name] = "classic"
	}
	for _, q := range s.Grail {
		if q.Kind == "metric" {
			metrics[q.Name] = GrailMetricID(q)
			units[q.Name] = NormalizeUnit(q.Unit)
			stats[q.Name] = q.Statistic
			eligibleStatistics[q.Name] = q.Statistic != "average" && q.Statistic != "p95" && q.Statistic != "p99"
			backends[q.Name] = "grail"
			grail[q.Name] = q
		}
	}
	for _, p := range result.Points {
		if p.Validate() != nil || p.ProjectID != s.ProjectID || p.RunID != "" && p.RunID != runID || p.Provider == nil || p.Provider.Name != monitorprovider.ProviderDynatrace || p.Provider.Backend != backends[p.MetricName] || metrics[p.MetricName] != p.Provider.MetricID || units[p.MetricName] != "" && units[p.MetricName] != p.Unit || stats[p.MetricName] != p.Provider.Statistic || !p.Provider.BucketEnd.After(from) || !p.Provider.BucketStart.Before(to) || p.Provider.BucketEnd.After(to.Add(24*time.Hour)) {
			return errors.New("invalid selected Dynatrace observation")
		}
		if !validDimensions(p.Dimensions) || !validDimensions(p.Provider.EntityIDs) {
			if !dimensionLabelsSafe(p.Dimensions) || !dimensionLabelsSafe(p.Provider.EntityIDs) {
				return errUnsafeDimensions
			}
			return errors.New("Dynatrace observation dimensions exceed identity bounds")
		}
		if p.Provider.AggregationEligible && (p.Provider.Statistic == "average" || p.Provider.Statistic == "p95" || p.Provider.Statistic == "p99") {
			return errors.New("unweighted aggregate cannot claim acceptance eligibility")
		}
		if p.Provider.AggregationEligible && !eligibleStatistics[p.MetricName] {
			return errors.New("transformation cannot establish eligibility for an unweighted statistic")
		}
		if p.Provider.AggregationEligible && (p.Value == nil || p.Quality != analysis.QualityValid) {
			return errors.New("absent observation cannot establish acceptance coverage")
		}
		if q, ok := grail[p.MetricName]; ok {
			if (q.BucketSeconds == 0) != p.Provider.QueryWindow {
				return errors.New("Grail result kind differs from frozen definition")
			}
			synthetic, err := grailNumericSemantics(q)
			if err != nil || synthetic && (!p.Provider.Synthetic || p.Provider.AggregationEligible) {
				return errors.New("Grail synthetic fill cannot claim observed acceptance coverage")
			}
			if q.EntityIDField != "" && !containsIdentity(q.EntityIDs, p.Provider.EntityIDs[q.EntityIDField]) {
				return errors.New("Grail observation outside approved entity scope")
			}
			fields, err := grailGroupingFields(q)
			if err != nil {
				return err
			}
			allowed := map[string]bool{}
			for _, field := range fields {
				allowed[field] = true
			}
			for field := range p.Dimensions {
				if !allowed[field] {
					return errors.New("Grail observation contains an undeclared grouping dimension")
				}
			}
			if q.EntityIDField != "" && p.Dimensions[q.EntityIDField] != p.Provider.EntityIDs[q.EntityIDField] {
				return errors.New("Grail entity dimension differs from approved identity")
			}
		}
	}
	allowedProperties := map[string]bool{"duration_ms": true, "correlation": true, "run_id": true, "trace_id": true, "span_id": true, "parent_span_id": true, "query_name": true, "event_type": true, "display_id": true, "start_time": true, "end_time": true, "status": true, "evidence_scope": true}
	for _, d := range result.Evidence {
		if d.Provider != monitorprovider.ProviderDynatrace || d.ApplicationID != "" || d.ID == "" || len(d.ID) > 256 || len(d.Summary) > 4096 || len(d.Properties) > 16 || !validDimensions(d.EntityIDs) || d.TimestampUTC.IsZero() || d.TimestampUTC.Location() != time.UTC || len(d.Status) > 128 || len(d.Severity) > 128 {
			return errors.New("invalid Dynatrace diagnostic")
		}
		switch d.Kind {
		case "problem":
			if !s.Problems || d.Backend != "classic" {
				return errors.New("unselected problems")
			}
		case "event":
			selected := s.Events && d.Backend == "classic"
			for _, q := range s.Grail {
				selected = selected || q.Kind == "event" && d.Backend == "grail" && d.Properties["query_name"] == q.Name
			}
			if !selected {
				return errors.New("unselected events")
			}
		case "log", "span", "trace":
			selected := false
			for _, q := range s.Grail {
				selected = selected || q.Kind == d.Kind && d.Backend == "grail" && d.Properties["query_name"] == q.Name
			}
			if !selected {
				return errors.New("unselected Grail evidence")
			}
		default:
			return errors.New("unknown diagnostic kind")
		}
		for key, value := range d.Properties {
			if !allowedProperties[key] || len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") {
				return errors.New("unsafe diagnostic projection")
			}
		}
		if d.Properties["correlation"] == "run_header" && (d.Properties["run_id"] != runID || !s.RunHeader) {
			return errors.New("unsupported run header correlation")
		}
		for _, q := range s.Grail {
			if d.Backend == "grail" && d.Properties["query_name"] == q.Name && q.EntityIDField != "" && !containsIdentity(q.EntityIDs, d.EntityIDs[q.EntityIDField]) {
				return errors.New("Grail diagnostic outside approved entity scope")
			}
		}
		if d.DeepLink != "" {
			u, err := url.Parse(d.DeepLink)
			base, _ := url.Parse(cfg.EnvironmentURL)
			if err != nil || base == nil || u.Scheme != "https" || u.Host != base.Host || u.User != nil {
				return errors.New("unsafe provider link")
			}
		}
	}
	ops := operations(s)
	allowedCoverage := map[string]bool{"controller": true, "executor": true, "retention": true}
	for _, op := range ops {
		allowedCoverage[op.key] = true
	}
	for key, coverage := range result.Coverage {
		if !allowedCoverage[key] || !coverage.From.Equal(from) || !coverage.To.Equal(to) || coverage.Points < 0 || coverage.Evidence < 0 || len(coverage.Status) > 64 || len(coverage.Message) > 1024 {
			return errors.New("invalid selected query coverage")
		}
	}
	progress := result.Progress
	if progress.TotalQueries != 0 || progress.Complete || progress.Cursor != "" || progress.NextQuery != 0 || progress.ScannedBytes != 0 || progress.ScanReservationBytes != 0 || len(progress.ScanReservationIDs) > 0 || len(progress.ScanSettlements) > 0 {
		if progress.TotalQueries != len(ops) || progress.NextQuery < 0 || progress.NextQuery > len(ops) || progress.Complete && (progress.NextQuery != len(ops) || progress.Cursor != "") || progress.ScannedBytes < 0 || progress.ScanReservationBytes < 0 || len(progress.ScanReservationIDs) > 32 {
			return errors.New("invalid selected query progress")
		}
		for _, attempt := range progress.ScanReservationIDs {
			if len(attempt) != 32 || !validHexID(attempt, 32) {
				return errors.New("invalid query attempt identity")
			}
		}
		if err := ValidateScanSettlements(progress); err != nil {
			return err
		}
		if progress.Cursor != "" {
			cursor, err := decodeCursor(progress.Cursor)
			if err != nil || cursor.Query != progress.NextQuery || cursor.Fingerprint != selectionFingerprint(s, from, to) || !cursor.From.Equal(from) || !cursor.To.Equal(to) || cursor.Query >= len(ops) {
				return errors.New("continuation outside frozen selection")
			}
			if cursor.Token != "" && (ops[cursor.Query].kind != "grail" && ops[cursor.Query].kind != "smartscape" || len(cursor.Attempt) != 32 || !validHexID(cursor.Attempt, 32) || cursor.Expires.IsZero() || cursor.PlatformOrigin != cfg.PlatformURL || len(cursor.Principal) != 64 || !validHexID(cursor.Principal, 64)) {
				return errors.New("invalid Grail continuation")
			}
			if len(cursor.MetricWindows) > 0 && (ops[cursor.Query].kind != "metric" || cursor.MetricPoints > cfg.MaxPoints) {
				return errors.New("metric continuation outside selected budget")
			}
		}
	}
	for _, entity := range result.Topology {
		if s.TopologyMode == "" || entity.ID == "" || len(entity.ID) > 128 || len(entity.Name) > 256 || len(entity.Type) > 128 || !validDimensions(entity.EntityIDs) {
			return errors.New("invalid selected topology")
		}
		if s.TopologyMode == "smartscape" && entity.Type == "relationship" {
			if !providerEntityID.MatchString(entity.EntityIDs["source_id"]) || !providerEntityID.MatchString(entity.EntityIDs["target_id"]) || len(s.SmartscapeEntityIDs) > 0 && !containsIdentity(s.SmartscapeEntityIDs, entity.EntityIDs["source_id"]) && !containsIdentity(s.SmartscapeEntityIDs, entity.EntityIDs["target_id"]) {
				return errors.New("relationship outside approved Smartscape scope")
			}
		} else if s.TopologyMode == "smartscape" && len(s.SmartscapeEntityIDs) > 0 && !containsIdentity(s.SmartscapeEntityIDs, entity.ID) {
			return errors.New("topology outside approved Smartscape scope")
		}
	}
	return nil
}
