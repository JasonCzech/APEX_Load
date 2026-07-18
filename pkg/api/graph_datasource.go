package api

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// graphRangeQuery is one resolved range query, expressed independently of any
// particular datasource. The Prometheus backend turns it into /api/v1/query_range; a
// future Grafana-proxy backend would turn it into a POST to /api/ds/query with a
// service-account token. DatasourceUID is carried from the imported panel and ignored
// by the Prometheus backend, but is load-bearing for that future proxy.
type graphRangeQuery struct {
	Expr            string
	DatasourceUID   string
	Start           time.Time
	End             time.Time
	StepSeconds     int
	MaxSeries       int
	MaxBytes        int64
	LabelFields     []string
	SeriesKeyPrefix string
}

// graphDataSource abstracts where imported-dashboard panel data comes from, so the
// storage and rendering layers never depend on Prometheus specifically.
type graphDataSource interface {
	// ID is stable and persisted alongside snapshots ("prometheus" | "grafana_proxy")
	// so a snapshot records which backend produced it.
	ID() string
	Available() (enabled bool, configured bool)
	QueryRange(ctx context.Context, q graphRangeQuery) ([]PrometheusSeries, error)
}

// seriesCapError signals that a query returned more series than the per-panel cap.
// It is surfaced to the user as a per-panel error, never truncated.
type seriesCapError struct {
	got int
	cap int
}

func (e *seriesCapError) Error() string {
	return fmt.Sprintf("query returned %d series, exceeding the per-panel limit of %d; add a `by (...)` aggregation or a label matcher", e.got, e.cap)
}

// promGraphDataSource is the default backend: it runs PromQL against the Prometheus
// configured in Settings → Integrations, reusing the hardened query_range transport.
type promGraphDataSource struct {
	settings PrometheusSettings
	client   *http.Client
	allow    map[string]struct{}
}

func newPromGraphDataSource(settings PrometheusSettings) *promGraphDataSource {
	timeout := time.Duration(settings.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = time.Duration(defaultPrometheusSettings().TimeoutSeconds) * time.Second
	}
	return &promGraphDataSource{
		settings: settings,
		client:   newHostAllowlistedHTTPClient(settings.allowlistHosts(), timeout),
		allow:    buildPrometheusAllowMap(settings),
	}
}

func buildPrometheusAllowMap(settings PrometheusSettings) map[string]struct{} {
	allow := make(map[string]struct{})
	for _, host := range settings.allowlistHosts() {
		allow[strings.ToLower(host)] = struct{}{}
	}
	return allow
}

func (p *promGraphDataSource) ID() string { return "prometheus" }

func (p *promGraphDataSource) Available() (bool, bool) {
	configured := strings.TrimSpace(p.settings.BaseURL) != ""
	return p.settings.Enabled, configured
}

func (p *promGraphDataSource) QueryRange(ctx context.Context, q graphRangeQuery) ([]PrometheusSeries, error) {
	maxBytes := q.MaxBytes
	if maxBytes <= 0 {
		maxBytes = maxPrometheusResponseBytes
	}
	results, err := queryPrometheusRange(ctx, p.client, p.settings, p.allow, q.Expr, q.Start, q.End, q.StepSeconds, maxBytes)
	if err != nil {
		return nil, err
	}
	// Series cap: user PromQL can fan out to thousands of series. Refuse rather than
	// truncate — a silently truncated chart is a lying chart.
	if q.MaxSeries > 0 && len(results) > q.MaxSeries {
		return nil, &seriesCapError{got: len(results), cap: q.MaxSeries}
	}

	// Reuse the curated-pack shaping helpers by feeding them a synthetic definition
	// carrying the panel's key prefix and legend label fields.
	def := prometheusGraphDefinition{ID: q.SeriesKeyPrefix, LabelFields: q.LabelFields}
	series := make([]PrometheusSeries, 0, len(results))
	for index, result := range results {
		values := make([]PrometheusPoint, 0, len(result.Values))
		for _, raw := range result.Values {
			if point, ok := decodePrometheusPoint(raw); ok {
				values = append(values, point)
			}
		}
		series = append(series, PrometheusSeries{
			Key:    prometheusSeriesKey(def.ID, result.Metric, index),
			Label:  prometheusSeriesLabel(def, result.Metric, index),
			Labels: cloneStringMap(result.Metric),
			Values: values,
		})
	}
	sort.SliceStable(series, func(i, j int) bool { return series[i].Label < series[j].Label })
	return series, nil
}

// graphDataSource returns the active backend for imported-dashboard panels. Today it is
// always Prometheus; when a Grafana-proxy backend lands this becomes a switch on a new
// GrafanaSettings.Mode with no change to storage or rendering.
func (s *Server) graphDataSource() graphDataSource {
	return newPromGraphDataSource(normalizePrometheusSettings(s.runner.GetSystemSettings().Prometheus))
}
