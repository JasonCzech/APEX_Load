package prometheus

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
)

func (c *Client) Test(ctx context.Context) (Capabilities, error) { return c.test(ctx, nil) }
func (c *Client) TestSelection(ctx context.Context, s Selection) (Capabilities, error) {
	if err := (JobConfig{c.cfg, s}).Validate(); err != nil {
		return Capabilities{}, err
	}
	return c.test(ctx, &s)
}

// Selection readiness is an inspection, with its own fixed one-minute bound.
// Runtime collection retains the configured (at most 30-second) poll budget.
func (c *Client) selectedInspectionContext(ctx context.Context, s Selection) (context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	requests := len(s.Queries)
	for _, query := range s.Queries {
		if query.FreshnessExpression != "" {
			requests++
		}
		if query.TerminalExpression != "" {
			requests++
		}
	}
	if s.Targets {
		requests++
	}
	if s.Alerts {
		requests++
	}
	if requests > c.cfg.MaxRequestsPerPoll {
		cancel()
		return nil, func() {}, fmt.Errorf("%w: selected inspection requires %d requests; limit is %d", ErrBudget, requests, c.cfg.MaxRequestsPerPoll)
	}
	if err := ctx.Err(); err != nil {
		cancel()
		return nil, func() {}, err
	}
	// Account for known shared-quota delay and leave one HTTP request deadline
	// after the final reservation. Busy peers may still exhaust the deadline;
	// their runtime failure is explicitly classified as an inspection budget.
	first := time.Now()
	c.rate.mu.Lock()
	if c.rate.next.After(first) {
		first = c.rate.next
	}
	c.rate.mu.Unlock()
	finish := first.Add(time.Duration(max(0, requests-1)) * (time.Minute / time.Duration(c.cfg.RequestsPerMinute))).Add(time.Duration(c.cfg.TimeoutSeconds) * time.Second)
	if deadline, exists := ctx.Deadline(); exists && finish.After(deadline) {
		cancel()
		return nil, func() {}, fmt.Errorf("%w: selected probes cannot fit the one-minute inspection deadline", ErrBudget)
	}
	return context.WithValue(ctx, budgetKey{}, &pollBudget{requests}), cancel, nil
}

func (c *Client) test(ctx context.Context, s *Selection) (Capabilities, error) {
	caps := Capabilities{BackendProfile: c.cfg.BackendProfile, Status: map[string]string{"exemplars": "unsupported"}}
	var cancel context.CancelFunc
	var err error
	if s != nil {
		ctx, cancel, err = c.selectedInspectionContext(ctx, *s)
		if err != nil {
			caps.Status["metrics"] = statusFor(err)
			return caps, err
		}
	} else {
		ctx, cancel = c.boundedContext(ctx)
	}
	defer cancel()
	now := time.Now().UTC()
	caps.Metrics = true
	probes := []Query{{ID: "probe", Expression: "vector(1)"}}
	if s != nil {
		probes = s.Queries
	}
	expandedSeries := 0
	for _, q := range probes {
		rendered, renderErr := materializeQuery(q)
		if renderErr != nil {
			return caps, renderErr
		}
		expressionProbes := []string{rendered.Expression}
		if q.FreshnessExpression != "" {
			expressionProbes = append(expressionProbes, rendered.FreshnessExpression)
		}
		if q.TerminalExpression != "" {
			expressionProbes = append(expressionProbes, strings.ReplaceAll(rendered.TerminalExpression, "{{window.duration}}", "1m"))
		}
		for pi, expression := range expressionProbes {
			env, queryErr := c.query(ctx, "/api/v1/query", url.Values{"query": {expression}, "time": {unixSeconds(now)}})
			if queryErr != nil {
				if s != nil && errors.Is(queryErr, context.DeadlineExceeded) {
					queryErr = fmt.Errorf("%w: selected inspection deadline exhausted", ErrBudget)
				}
				caps.Metrics = false
				caps.Status["metrics"] = statusFor(queryErr)
				return caps, queryErr
			}
			rows, truncated, parseErr := c.observations(env, false)
			if parseErr != nil {
				caps.Metrics = false
				caps.Status["metrics"] = "unsupported"
				return caps, parseErr
			}
			if s != nil && pi == 0 {
				expandedSeries += len(rows)
				if expandedSeries > c.cfg.MaxSeries {
					caps.Metrics = false
					caps.Status["metrics"] = "partial"
					return caps, errors.New("selected queries exceed the source expanded-series budget")
				}
			}
			finite := 0
			for _, row := range rows {
				if s != nil {
					if labelErr := c.validateResultLabels(row.labels, q.AllowedLabels); labelErr != nil {
						caps.Metrics = false
						caps.Status["metrics"] = "invalid_result"
						return caps, labelErr
					}
				}
				for _, v := range row.samples {
					if v != nil {
						finite++
					}
				}
			}
			status := "ok"
			if finite < max(1, q.MinSeries) {
				status = "no_data"
			}
			if truncated || len(env.Warnings) > 0 {
				status = "partial"
			}
			if s != nil && status != "ok" {
				caps.Metrics = false
			}
			if caps.Status["metrics"] == "" || caps.Status["metrics"] == "ok" {
				caps.Status["metrics"] = status
			}
			if pi == 1 && q.FreshnessExpression != "" {
				caps.Status["freshness"] = status
			}
		}
	}
	if s != nil {
		caps.Status["discovery"], caps.Status["buildinfo"], caps.Status["metadata"] = "not_selected", "not_selected", "not_selected"
		for _, diagnostic := range []struct {
			name, path string
			selected   bool
			value      *bool
		}{{"targets", "/api/v1/targets", s.Targets, &caps.Targets}, {"alerts", "/api/v1/alerts", s.Alerts, &caps.Alerts}} {
			caps.Status[diagnostic.name] = "not_selected"
			if c.cfg.BackendProfile == BackendAzureManaged {
				caps.Status[diagnostic.name] = "unsupported"
				continue
			}
			if !diagnostic.selected {
				continue
			}
			params := url.Values{}
			if diagnostic.name == "targets" {
				params.Set("state", "active")
			}
			env, queryErr := c.query(ctx, diagnostic.path, params)
			if queryErr != nil {
				if errors.Is(queryErr, context.DeadlineExceeded) {
					queryErr = fmt.Errorf("%w: selected inspection deadline exhausted", ErrBudget)
				}
				caps.Status[diagnostic.name] = statusFor(queryErr)
				return caps, queryErr
			}
			var rows []monitorprovider.Diagnostic
			var truncated bool
			if diagnostic.name == "targets" {
				rows, truncated, err = c.targetEvidence(env, now, s.EvidenceSelector)
			} else {
				rows, truncated, err = c.alertEvidence(env, now, s.EvidenceSelector)
			}
			if err != nil {
				caps.Status[diagnostic.name] = "unsupported"
				return caps, err
			}
			*diagnostic.value = true
			caps.Status[diagnostic.name] = "ok"
			if len(rows) == 0 {
				caps.Status[diagnostic.name] = "no_data"
			}
			if truncated || len(env.Warnings) > 0 {
				*diagnostic.value = false
				caps.Status[diagnostic.name] = "partial"
			}
		}
		return caps, nil
	}
	_, err = c.query(ctx, "/api/v1/label/__name__/values", url.Values{"limit": {"201"}})
	caps.Discovery = err == nil
	caps.Status["discovery"] = statusFor(err)
	if c.cfg.BackendProfile == BackendAzureManaged {
		caps.Status["targets"] = "unsupported"
		caps.Status["alerts"] = "unsupported"
		caps.Status["metadata"] = "unsupported"
		caps.Status["buildinfo"] = "unsupported"
		return caps, nil
	}
	info, err := c.query(ctx, "/api/v1/status/buildinfo", nil)
	caps.Status["buildinfo"] = statusFor(err)
	if err == nil {
		var data struct {
			Version string `json:"version"`
		}
		if decodeJSON(info.Data, &data) == nil {
			caps.Version = c.safeText(data.Version, 64)
		}
	}
	for _, cap := range []struct {
		name, path string
		value      *bool
	}{{"targets", "/api/v1/targets", &caps.Targets}, {"alerts", "/api/v1/alerts", &caps.Alerts}} {
		_, err := c.query(ctx, cap.path, nil)
		*cap.value = err == nil
		caps.Status[cap.name] = statusFor(err)
	}
	return caps, nil
}

// Discovery is bounded and deliberately returns a truncation sentinel rather
// than implying that a limit-limited catalog is exhaustive.
func (c *Client) Discover(ctx context.Context, selector, kind, label string) ([]monitorprovider.Entity, error) {
	ctx, cancel := c.boundedContext(ctx)
	defer cancel()
	params := url.Values{"limit": {"201"}}
	if selector != "" {
		parsed, err := parseExpression(selector)
		if err != nil {
			return nil, err
		}
		v, ok := parsed.(*parser.VectorSelector)
		if !ok {
			return nil, errors.New("discovery requires a metric selector")
		}
		if c.cfg.BackendProfile == BackendAzureManaged {
			if v.Name == "" {
				return nil, errors.New("Azure discovery requires an explicit metric name")
			}
			for _, m := range v.LabelMatchers {
				if m.Type == labels.MatchRegexp || m.Type == labels.MatchNotRegexp {
					return nil, errors.New("Azure series discovery does not support regex matchers")
				}
			}
		}
		params.Set("match[]", selector)
	}
	now := time.Now().UTC()
	params.Set("start", unixSeconds(now.Add(-12*time.Hour)))
	params.Set("end", unixSeconds(now))
	path := ""
	switch kind {
	case "", "metrics":
		path = "/api/v1/label/__name__/values"
	case "labels":
		path = "/api/v1/labels"
	case "label_values":
		if !labelRE.MatchString(label) || sensitiveLabel(label) {
			return nil, errors.New("invalid discovery label")
		}
		if c.cfg.BackendProfile == BackendAzureManaged {
			// The series projection uses canonical Azure identities. Label names
			// returned by discovery may preserve any display casing.
			label = strings.ToLower(label)
		}
		if c.cfg.BackendProfile == BackendAzureManaged && label != "__name__" {
			if selector == "" {
				return nil, errors.New("Azure label discovery requires a metric selector")
			}
			series, err := c.Discover(ctx, selector, "series", "")
			if err != nil {
				return nil, err
			}
			values := map[string]bool{}
			truncated := false
			for _, entity := range series {
				if entity.Type == "truncation" {
					truncated = true
					continue
				}
				if v := entity.EntityIDs[label]; v != "" {
					values[v] = true
				}
			}
			keys := make([]string, 0, len(values))
			for v := range values {
				keys = append(keys, v)
			}
			sort.Strings(keys)
			out := []monitorprovider.Entity{}
			for _, v := range keys {
				out = append(out, monitorprovider.Entity{ID: stableHash(v), Name: v, Type: "label_value"})
			}
			if truncated {
				out = append(out, monitorprovider.Entity{ID: "truncated", Name: "Discovery results truncated by collection limits", Type: "truncation"})
			}
			return out, nil
		}
		path = "/api/v1/label/" + label + "/values"
	case "series":
		if selector == "" {
			return nil, errors.New("series discovery requires a metric selector")
		}
		path = "/api/v1/series"
	case "targets":
		if c.cfg.BackendProfile == BackendAzureManaged {
			return nil, errors.New("Azure target discovery is unsupported")
		}
		if selector != "" {
			if _, err := parseEvidenceSelector(selector); err != nil {
				return nil, err
			}
		}
		path = "/api/v1/targets"
		params.Set("state", "active")
	case "metadata":
		if c.cfg.BackendProfile == BackendAzureManaged {
			return nil, errors.New("Azure metric metadata is unsupported")
		}
		path = "/api/v1/metadata"
		if selector != "" {
			parsed, err := parseExpression(selector)
			if err != nil {
				return nil, err
			}
			metric := parsed.(*parser.VectorSelector).Name
			if metric == "" {
				return nil, errors.New("metadata discovery requires an explicit metric name; metadata is not label-specific")
			}
			params.Set("metric", metric)
		}
		params.Del("match[]")
	default:
		return nil, errors.New("unsupported discovery kind")
	}
	e, err := c.query(ctx, path, params)
	if err != nil {
		return nil, err
	}
	out := []monitorprovider.Entity{}
	truncated := hasTruncation(e.Warnings)
	switch kind {
	case "", "metrics", "labels", "label_values":
		var values []string
		if err := decodeJSON(e.Data, &values); err != nil {
			return nil, err
		}
		sort.Strings(values)
		for _, v := range values {
			if kind == "labels" && sensitiveLabel(v) {
				continue
			}
			safe := c.safeText(v, 128)
			out = append(out, monitorprovider.Entity{ID: stableHash(safe), Name: safe, Type: strings.TrimSuffix(kind, "s")})
			if len(out) >= c.cfg.MaxSeries {
				truncated = len(values) > len(out)
				break
			}
		}
	case "series":
		var values []map[string]string
		if err := decodeJSON(e.Data, &values); err != nil {
			return nil, err
		}
		for _, v := range values {
			display := c.safeLabels(v, nil)
			out = append(out, monitorprovider.Entity{ID: seriesIdentity(v, c.cfg.BackendProfile), Name: c.safeText(v["__name__"], 128), Type: "series", EntityIDs: display})
			if len(out) >= c.cfg.MaxSeries {
				truncated = len(values) > len(out)
				break
			}
		}
	case "targets":
		selectors := []string{}
		if selector != "" {
			selectors = append(selectors, selector)
		}
		diagnostics, targetTruncated, err := c.targetEvidence(e, now, selectors...)
		if err != nil {
			return nil, err
		}
		truncated = truncated || targetTruncated
		for _, d := range diagnostics {
			out = append(out, monitorprovider.Entity{ID: d.ID, Name: d.Summary, Type: "target", EntityIDs: d.EntityIDs})
		}
	case "metadata":
		var values map[string][]struct{ Type, Unit, Help string }
		if err := decodeJSON(e.Data, &values); err != nil {
			return nil, err
		}
		keys := make([]string, 0, len(values))
		for k := range values {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := values[k]
			ids := map[string]string{}
			if len(v) == 1 {
				ids["type"] = c.safeText(v[0].Type, 128)
				ids["unit"] = c.safeText(v[0].Unit, 128)
			} else {
				ids["type"] = "conflicting"
			}
			out = append(out, monitorprovider.Entity{ID: stableHash(k), Name: c.safeText(k, 128), Type: "metric_metadata", EntityIDs: ids})
			if len(out) >= c.cfg.MaxSeries {
				truncated = len(keys) > len(out)
				break
			}
		}
	}
	if truncated {
		out = append(out, monitorprovider.Entity{ID: "truncated", Name: "Discovery results truncated by collection limits", Type: "truncation"})
	}
	return out, nil
}
func hasTruncation(warnings []string) bool {
	for _, v := range warnings {
		low := strings.ToLower(v)
		if strings.Contains(low, "truncat") || strings.Contains(low, "limit") {
			return true
		}
	}
	return false
}

// Retain safe source fields only; scrape URLs, discovered labels, arbitrary
// alert annotations and lastError narratives never enter evidence artifacts.
func (c *Client) targetEvidence(e envelope, now time.Time, selectors ...string) ([]monitorprovider.Diagnostic, bool, error) {
	var data struct {
		ActiveTargets []struct {
			Labels     map[string]string `json:"labels"`
			Health     string            `json:"health"`
			LastScrape time.Time         `json:"lastScrape"`
			LastError  string            `json:"lastError"`
		} `json:"activeTargets"`
	}
	if err := decodeJSON(e.Data, &data); err != nil {
		return nil, false, err
	}
	out := []monitorprovider.Diagnostic{}
	truncated := false
	for _, t := range data.ActiveTargets {
		if !evidenceMatches(t.Labels, selectors) {
			continue
		}
		if len(out) >= 100 {
			truncated = true
			break
		}
		status := t.Health
		if status != "up" && status != "down" && status != "unknown" {
			status = "unknown"
		}
		stamp := now.UTC()
		properties := map[string]string{}
		if !t.LastScrape.IsZero() {
			properties["last_scrape_utc"] = t.LastScrape.UTC().Format(time.RFC3339Nano)
		}
		out = append(out, monitorprovider.Diagnostic{ID: "target-" + seriesIdentity(t.Labels, c.cfg.BackendProfile), Kind: "target", TimestampUTC: stamp, Status: status, Summary: "Prometheus target " + status, Properties: properties, DeepLink: strings.TrimRight(c.cfg.Endpoint, "/") + c.cfg.ContextPath + "/targets", EntityIDs: c.safeLabels(t.Labels, evidenceLabels(selectors, []string{"job", "instance"}))})
	}
	return out, truncated, nil
}
func (c *Client) alertEvidence(e envelope, now time.Time, selectors ...string) ([]monitorprovider.Diagnostic, bool, error) {
	var data struct {
		Alerts []struct {
			Labels   map[string]string `json:"labels"`
			State    string            `json:"state"`
			ActiveAt time.Time         `json:"activeAt"`
		} `json:"alerts"`
	}
	if err := decodeJSON(e.Data, &data); err != nil {
		return nil, false, err
	}
	out := []monitorprovider.Diagnostic{}
	truncated := false
	for _, a := range data.Alerts {
		if !evidenceMatches(a.Labels, selectors) {
			continue
		}
		if len(out) >= 100 {
			truncated = true
			break
		}
		state := a.State
		if state != "pending" && state != "firing" && state != "inactive" {
			state = "unknown"
		}
		stamp := now.UTC()
		properties := map[string]string{}
		if !a.ActiveAt.IsZero() {
			properties["active_at"] = a.ActiveAt.UTC().Format(time.RFC3339Nano)
		}
		out = append(out, monitorprovider.Diagnostic{ID: "alert-" + seriesIdentity(a.Labels, c.cfg.BackendProfile), Kind: "alert", TimestampUTC: stamp, Status: state, Severity: c.safeText(a.Labels["severity"], 32), Summary: c.safeText(a.Labels["alertname"], 128), Properties: properties, DeepLink: strings.TrimRight(c.cfg.Endpoint, "/") + c.cfg.ContextPath + "/alerts", EntityIDs: c.safeLabels(a.Labels, evidenceLabels(selectors, []string{"job", "instance", "alertname"}))})
	}
	return out, truncated, nil
}

func evidenceMatches(values map[string]string, selectors []string) bool {
	if len(selectors) == 0 {
		return true
	} // Administrator-only unscoped discovery.
	return MatchesEvidence(selectors[0], values)
}

// MatchesEvidence independently validates a diagnostic's approved scope. The
// ingestion service uses the same contract to distrust collector submissions.
func MatchesEvidence(selector string, values map[string]string) bool {
	matchers, err := parseEvidenceSelector(selector)
	if err != nil {
		return false
	}
	for _, matcher := range matchers {
		if !matcher.Matches(values[matcher.Name]) {
			return false
		}
	}
	return true
}
func evidenceLabels(selectors, defaults []string) []string {
	labels := append([]string(nil), defaults...)
	if len(selectors) == 0 {
		return labels
	}
	matchers, err := parseEvidenceSelector(selectors[0])
	if err != nil {
		return labels
	}
	seen := map[string]bool{}
	for _, v := range labels {
		seen[v] = true
	}
	for _, m := range matchers {
		if !seen[m.Name] {
			labels = append(labels, m.Name)
			seen[m.Name] = true
		}
	}
	return labels
}
