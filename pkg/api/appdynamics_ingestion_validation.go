package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/appdynamics"
	"github.com/jasonczech/apexload/pkg/storage"
)

var appDynamicsNumericID = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
var appDynamicsOpaqueID = regexp.MustCompile(`^(?:[a-fA-F0-9]{32}|[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12})$`)

func validateAppDynamicsProviderBatch(job storage.RunMonitorJob, payload *storage.ProviderMonitorBatch, now time.Time) error {
	return validateAppDynamicsProviderBatchCursor(job, payload, now, true)
}

// Receipt retries validate immutable scope and payload shape without consulting
// a cursor which may already have advanced into a newer query window.
func validateAppDynamicsProviderBatchCursor(job storage.RunMonitorJob, payload *storage.ProviderMonitorBatch, now time.Time, validateCursor bool) error {
	if payload == nil || job.Source.AppDynamics == nil || job.ConnectionSnapshot == nil || len(payload.Coverage) > 128 || len(payload.Topology) > 256 {
		return errors.New("invalid AppDynamics provider projection")
	}
	selection := job.Source.AppDynamics
	if err := selection.Validate(); err != nil {
		return errors.New("invalid immutable AppDynamics selection")
	}
	from, to := payload.QueryFromUTC, payload.QueryUntilUTC
	if from.Location() != time.UTC || to.Location() != time.UTC || from.IsZero() || !to.After(from) || from.Before(job.StartedAt.Truncate(time.Millisecond)) || to.After(now.Add(time.Minute)) || !from.Add(to.Sub(from)).Equal(to) || job.LoadEndedAt != nil && to.After(job.LoadEndedAt.Truncate(time.Millisecond)) {
		return errors.New("invalid immutable AppDynamics query window")
	}
	if validateCursor && !job.QueryUntilUTC.IsZero() && (!from.Equal(job.QueryFromUTC) || !to.Equal(job.QueryUntilUTC)) {
		return errors.New("query chunk changed its immutable window")
	}
	if payload.Progress != nil && payload.Progress.TotalQueries > 0 {
		expected := len(selection.Metrics) + len(selection.Analytics)
		for _, enabled := range []bool{selection.Events, selection.Violations, selection.Snapshots} {
			if enabled {
				expected += len(selection.Applications)
			}
		}
		p := payload.Progress
		if p.TotalQueries != expected || p.NextQuery < 0 || validateCursor && p.NextQuery < job.QueryOffset || p.NextQuery > expected || p.Complete != (p.NextQuery == expected) {
			return errors.New("invalid approved query continuation")
		}
	}
	if payload.QueryWindowEnd != nil && (payload.Progress == nil || !payload.Progress.Complete || !payload.QueryWindowEnd.Equal(payload.QueryUntilUTC)) {
		return errors.New("watermark requires a complete approved query window")
	}
	settle := selection.SettleSeconds
	if settle == 0 {
		settle = 120
	}
	for i, p := range payload.Points {
		if p.Provider == nil || p.Validate() != nil || p.Kind != analysis.KindGauge || p.Provider.Name != "appdynamics" {
			return errors.New("invalid AppDynamics metric measurement")
		}
		m := *p.Provider
		p.Provider = &m
		if !slices.Contains(selection.Applications, m.EntityIDs["application"]) || !validAppDynamicsEntityIDs(m.EntityIDs) || !m.BucketEnd.After(from) || !m.BucketStart.Before(to) || m.BucketStart.Before(from.Add(-time.Duration(m.ResolutionSeconds)*time.Second)) || m.BucketEnd.After(to.Add(time.Duration(m.ResolutionSeconds)*time.Second)) {
			return errors.New("AppDynamics bucket outside approved scope or window")
		}
		matched := false
		var analyticsQuery *appdynamics.AnalyticsQuery
		for _, q := range selection.Analytics {
			if m.MetricID != appdynamics.AnalyticsMetricID(q) {
				continue
			}
			statistic := q.Statistic
			if statistic == "" {
				statistic = "value"
			}
			unit := q.Unit
			if unit == "" {
				unit = "count"
			}
			if p.MetricName != q.Name || m.EntityIDs["application"] != q.ApplicationID || m.CatalogPath != "" || p.Unit != unit || m.Statistic != statistic || len(m.EntityIDs) != 1 || !m.QueryWindow && m.ResolutionSeconds != q.BucketSeconds {
				return errors.New("Analytics measurement differs from approved query")
			}
			if m.QueryWindow != appdynamics.AnalyticsQueryWindow(q) {
				return errors.New("Analytics bucket kind differs from the approved query definition")
			}
			if m.QueryWindow {
				queryStart, queryEnd := from.Truncate(time.Second), to.Truncate(time.Second)
				if queryStart.Before(from) {
					queryStart = queryStart.Add(time.Second)
				}
				if !m.BucketStart.Equal(queryStart) || !m.BucketEnd.Equal(queryEnd) {
					return errors.New("Analytics query aggregate changed its approved window")
				}
			}
			p.Dimensions = map[string]string{"application_id": q.ApplicationID, "analytics_query": q.Name, "query_id": m.MetricID, "statistic": statistic}
			analyticsQuery = &q
			matched = true
			break
		}
		if !matched {
			if m.QueryWindow {
				return errors.New("Controller metrics cannot be query-window aggregates")
			}
			if !appDynamicsNumericID.MatchString(m.MetricID) || !validControllerResolution(m.ResolutionSeconds) || !appDynamicsScopeMatches(m.EntityIDs, selection.ScopeIDs) {
				return errors.New("Controller measurement outside approved entity scope")
			}
			for _, selected := range selection.Metrics {
				if p.MetricName != selected.Name || m.EntityIDs["application"] != selected.ApplicationID || !appDynamicsCatalogMatches(selected.Path, m.CatalogPath) || m.Statistic != appDynamicsExpectedStatistic(selected, m.CatalogPath) || p.Unit != appDynamicsExpectedUnit(selected, m.CatalogPath) {
					continue
				}
				expectedIDs := true
				if !strings.Contains(selected.Path, "*") {
					for kind, id := range selected.EntityIDs {
						expectedIDs = expectedIDs && m.EntityIDs[kind] == id
					}
				}
				if !expectedIDs {
					continue
				}
				hash := sha256.Sum256([]byte(m.CatalogPath))
				p.Dimensions = map[string]string{"application_id": selected.ApplicationID, "metric_id": m.MetricID, "catalog_path_id": hex.EncodeToString(hash[:8]), "selection": selected.Name, "statistic": m.Statistic}
				matched = true
				break
			}
		}
		if !matched {
			return errors.New("metric was not selected by the immutable AppDynamics grant")
		}
		// Recompute eligibility from source semantics; provider percentiles never
		// become whole-run observations or terminal acceptance statistics.
		eligible := p.Quality == analysis.QualityValid && (m.Statistic == "value" || m.Statistic == "current" || m.Statistic == "sum" || m.Statistic == "average" && m.Count != nil && *m.Count > 0 && m.Sum != nil)
		if analyticsQuery != nil {
			eligible = appdynamics.AnalyticsAggregationEligible(*analyticsQuery, p.Quality, m.Count, m.Sum)
		}
		m.AggregationEligible = m.AggregationEligible && eligible && !m.BucketStart.Before(from) && !m.BucketEnd.After(to)
		// A provisional observation remains provisional on an identical retry.
		// Only a fresh provider observation can advance its settled state.
		if providerSettledClaimPremature(m.Settled, m.BucketEnd, settle, now) {
			return errors.New("AppDynamics bucket marked settled before its settling allowance")
		}
		payload.Points[i] = p
	}
	for i, d := range payload.Evidence {
		if d.TimestampUTC.Location() != time.UTC || d.TimestampUTC.Before(from) || !d.TimestampUTC.Before(to) || !slices.Contains(selection.Applications, d.ApplicationID) {
			return errors.New("diagnostic outside approved application or window")
		}
		if d.EntityIDs == nil {
			d.EntityIDs = map[string]string{"application": d.ApplicationID}
		}
		if d.EntityIDs["application"] != d.ApplicationID || !validAppDynamicsEntityIDs(d.EntityIDs) {
			return errors.New("diagnostic entity identity differs from its application")
		}
		switch d.Kind {
		case "event":
			if !selection.Events || !appDynamicsNumericID.MatchString(d.ID) {
				return errors.New("unapproved event evidence")
			}
		case "violation":
			if !selection.Violations || !appDynamicsNumericID.MatchString(d.ID) {
				return errors.New("unapproved violation evidence")
			}
		case "snapshot":
			if !selection.Snapshots || !appDynamicsOpaqueID.MatchString(d.ID) {
				return errors.New("unapproved snapshot evidence")
			}
		case "log", "transaction":
			if !appDynamicsOpaqueID.MatchString(d.ID) {
				return errors.New("invalid Analytics evidence identity")
			}
			found := false
			for _, q := range selection.Analytics {
				kind := q.Kind
				if kind == "" {
					kind = "transaction"
				}
				if q.ApplicationID == d.ApplicationID && kind == d.Kind && q.Name == d.Properties["query_name"] {
					found = true
					break
				}
			}
			if !found {
				return errors.New("Analytics evidence differs from approved query")
			}
		default:
			return errors.New("unsupported diagnostic evidence")
		}
		if d.Kind != "log" && d.Kind != "transaction" && !appDynamicsScopeMatches(d.EntityIDs, selection.ScopeIDs) {
			return errors.New("diagnostic outside approved entity scope")
		}
		if id := d.Properties["run_id"]; id != "" && id != job.RunID {
			return errors.New("diagnostic run association differs from managed run")
		}
		if d.Properties == nil {
			d.Properties = map[string]string{}
		}
		switch d.Properties["correlation"] {
		case "":
			d.Properties["correlation"] = "entity_time"
		case "entity_time", "unmatched":
		case "run_header":
			if !selection.RunHeader || d.Properties["run_id"] != job.RunID || d.Kind != "snapshot" && d.Kind != "log" && d.Kind != "transaction" {
				return errors.New("unapproved diagnostic run-header association")
			}
		default:
			return errors.New("unsupported diagnostic correlation classification")
		}
		if d.Kind == "snapshot" {
			d.Properties["requestguid"] = d.ID
		}
		if guid := d.Properties["requestguid"]; guid != "" && !appDynamicsOpaqueID.MatchString(guid) {
			return errors.New("invalid AppDynamics request GUID")
		}
		for _, key := range []string{"incident_start_utc", "incident_end_utc"} {
			if value := d.Properties[key]; value != "" {
				at, err := time.Parse(time.RFC3339Nano, value)
				if d.Kind != "violation" || err != nil || at.Location() != time.UTC || at.IsZero() {
					return errors.New("invalid violation time provenance")
				}
			}
		}
		d.DeepLink = appDynamicsSafeDiagnosticLink(job.ConnectionSnapshot.Config, d)
		payload.Evidence[i] = d
	}
	for key, c := range payload.Coverage {
		if !validAppDynamicsCoverageKey(key, *selection) || c.From.IsZero() || c.From.Before(from) || !c.To.After(c.From) || c.To.After(to.Add(time.Minute)) || c.Points < 0 || c.Points > 10000 || c.Evidence < 0 || c.Evidence > 2000 {
			return errors.New("coverage differs from approved query bounds")
		}
		switch c.Status {
		case "complete", "partial", "error", "unavailable":
		default:
			return errors.New("unsupported coverage status")
		}
	}
	for i, e := range payload.Topology {
		if !appDynamicsNumericID.MatchString(e.ID) || !appDynamicsNumericID.MatchString(e.ParentID) || !validAppDynamicsEntityIDs(e.EntityIDs) || !slices.Contains(selection.Applications, e.EntityIDs["application"]) || e.EntityIDs[e.Type] != e.ID || len(e.Name) == 0 || len(e.Name) > 256 || e.Path != "" {
			return errors.New("invalid provider topology entity")
		}
		switch e.Type {
		case "application":
			if e.ID != e.EntityIDs["application"] {
				return errors.New("invalid application topology")
			}
		case "tier", "backend", "machine":
			if e.ParentID != e.EntityIDs["application"] {
				return errors.New("invalid tier topology parent")
			}
		case "node", "business_transaction":
			if e.EntityIDs["tier"] != e.ParentID {
				return errors.New("invalid child topology parent")
			}
		default:
			return errors.New("unsupported topology entity type")
		}
		for kind, ids := range selection.ScopeIDs {
			if id := e.EntityIDs[kind]; id != "" && len(ids) > 0 && !slices.Contains(ids, id) {
				return errors.New("topology outside approved entity scope")
			}
		}
		e.Name = scrubTextBounded(e.Name, 256)
		payload.Topology[i] = e
	}
	return nil
}
func validAppDynamicsEntityIDs(ids map[string]string) bool {
	if len(ids) == 0 || len(ids) > 8 {
		return false
	}
	for kind, id := range ids {
		switch kind {
		case "application", "tier", "node", "business_transaction", "backend", "machine":
		default:
			return false
		}
		if !appDynamicsNumericID.MatchString(id) {
			return false
		}
	}
	return true
}
func appDynamicsScopeMatches(ids map[string]string, scope map[string][]string) bool {
	for kind, wanted := range scope {
		if len(wanted) > 0 && !slices.Contains(wanted, ids[kind]) {
			return false
		}
	}
	return true
}
func validControllerResolution(seconds int) bool {
	return seconds == 60 || seconds == 600 || seconds == 3600 || seconds == 86400
}
func appDynamicsCatalogMatches(selector, path string) bool {
	if path == "" || len(path) > 4096 {
		return false
	}
	pattern := strings.ReplaceAll(regexp.QuoteMeta(selector), `\*`, `[^|]*`)
	matched, _ := regexp.MatchString("^"+pattern+"$", path)
	return matched
}
func appDynamicsExpectedStatistic(m appdynamics.MetricSelection, path string) string {
	statistic := m.Statistic
	if statistic == "" {
		statistic = "value"
	}
	lower := strings.ToLower(path)
	if statistic == "value" {
		switch {
		case strings.Contains(lower, "95th percentile"):
			return "p95"
		case strings.Contains(lower, "99th percentile"):
			return "p99"
		case strings.Contains(lower, "average"):
			return "average"
		case strings.Contains(lower, "count") || strings.Contains(lower, "number of slow calls"):
			return "sum"
		}
	}
	return statistic
}
func appDynamicsExpectedUnit(m appdynamics.MetricSelection, path string) string {
	if m.Unit != "" {
		return m.Unit
	}
	lower := strings.ToLower(path)
	switch {
	case strings.Contains(lower, "(ms)"):
		return "ms"
	case strings.Contains(lower, "%") || strings.Contains(lower, "percentage"):
		return "percent"
	case strings.Contains(lower, "per minute"):
		return "per_minute"
	case strings.Contains(lower, "count") || strings.Contains(lower, "number of"):
		return "count"
	}
	return ""
}
func appDynamicsSafeDiagnosticLink(cfg appdynamics.Config, d appdynamics.Diagnostic) string {
	if d.DeepLink == "" || len(d.DeepLink) > 2048 {
		return ""
	}
	link, err := url.Parse(d.DeepLink)
	origin, originErr := url.Parse(cfg.Endpoint)
	if err != nil || originErr != nil || link.Scheme != "https" || link.Host != origin.Host || link.User != nil || link.RawQuery != "" || link.Opaque != "" || link.Path != cfg.Defaults().ContextPath+"/" {
		return ""
	}
	expected := ""
	if appDynamicsNumericID.MatchString(d.ID) {
		switch d.Kind {
		case "event":
			expected = "location=APP_EVENT_VIEWER_MODAL&eventSummary=" + d.ID
		case "violation":
			expected = "location=APP_INCIDENT_DETAIL&incident=" + d.ID
		}
	}
	if d.Kind == "snapshot" && appDynamicsOpaqueID.MatchString(d.ID) && appDynamicsNumericID.MatchString(d.ApplicationID) {
		expected = "location=APP_SNAPSHOT_VIEWER&application=" + d.ApplicationID + "&requestGUID=" + d.ID
	}
	if expected == "" || link.Fragment != expected {
		return ""
	}
	return d.DeepLink
}
func validAppDynamicsCoverageKey(key string, s appdynamics.Selection) bool {
	switch key {
	case "controller", "retention", "topology":
		return true
	}
	kind, id, ok := strings.Cut(key, ":")
	if !ok {
		return false
	}
	switch kind {
	case "metrics", "analytics":
		index, err := strconv.Atoi(id)
		if err != nil || index < 0 {
			return false
		}
		if kind == "metrics" {
			return index < len(s.Metrics)
		}
		return index < len(s.Analytics)
	case "events":
		return s.Events && slices.Contains(s.Applications, id)
	case "violations":
		return s.Violations && slices.Contains(s.Applications, id)
	case "snapshots":
		return s.Snapshots && slices.Contains(s.Applications, id)
	}
	return false
}
