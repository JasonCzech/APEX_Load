package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/trafficprofile"
)

var openSearchIndexPattern = regexp.MustCompile(`^[A-Za-z0-9._*,-]+$`)

type openSearchTrafficProfileSource struct {
	settings OpenSearchSettings
	client   *http.Client
	allow    map[string]struct{}
}

func newOpenSearchTrafficProfileSource(settings OpenSearchSettings) *openSearchTrafficProfileSource {
	settings = normalizeOpenSearchSettings(settings)
	allow := make(map[string]struct{})
	for _, host := range settings.allowlistHosts() {
		allow[strings.ToLower(host)] = struct{}{}
	}
	return &openSearchTrafficProfileSource{
		settings: settings,
		client:   newHostAllowlistedHTTPClient(settings.allowlistHosts(), time.Duration(settings.TimeoutSeconds)*time.Second),
		allow:    allow,
	}
}

func (o *openSearchTrafficProfileSource) available() (bool, bool) {
	configured := strings.TrimSpace(o.settings.BaseURL) != ""
	if o.settings.Username != "" {
		configured = configured && o.settings.password() != ""
	}
	if o.settings.BearerTokenEnvVar != "" {
		configured = configured && o.settings.bearerToken() != ""
	}
	return o.settings.Enabled, configured
}

func (o *openSearchTrafficProfileSource) query(ctx context.Context, source TrafficProfileOpenSearchSource, start, end time.Time, maxBytes int64) ([]trafficprofile.Series, error) {
	if !openSearchIndexPattern.MatchString(strings.TrimSpace(source.Index)) {
		return nil, fmt.Errorf("opensearch index contains unsupported characters")
	}
	if strings.TrimSpace(source.TimestampField) == "" {
		return nil, fmt.Errorf("opensearch timestamp_field is required")
	}
	interval, err := time.ParseDuration(source.Interval)
	if err != nil || interval <= 0 {
		return nil, fmt.Errorf("opensearch interval must be a positive duration")
	}
	if source.Range.StepSeconds <= 0 || interval != time.Duration(source.Range.StepSeconds)*time.Second {
		return nil, fmt.Errorf("opensearch interval must equal range.step_seconds")
	}
	metric := strings.ToLower(strings.TrimSpace(source.Metric))
	if metric == "" {
		metric = "count"
	}
	if metric != "count" && metric != "sum" && metric != "avg" {
		return nil, fmt.Errorf("opensearch metric must be count, sum, or avg")
	}
	if metric != "count" && strings.TrimSpace(source.MetricField) == "" {
		return nil, fmt.Errorf("opensearch metric_field is required for %s", metric)
	}
	filter := any(map[string]any{"match_all": map[string]any{}})
	if len(source.Filter) > 0 && string(source.Filter) != "null" {
		var decoded map[string]any
		if err := json.Unmarshal(source.Filter, &decoded); err != nil {
			return nil, fmt.Errorf("invalid opensearch filter: %w", err)
		}
		if decoded == nil {
			return nil, fmt.Errorf("opensearch filter must be a JSON object")
		}
		filter = decoded
	}
	aggs := map[string]any{
		"traffic": map[string]any{
			"date_histogram": map[string]any{
				"field":          source.TimestampField,
				"fixed_interval": source.Interval,
				"min_doc_count":  0,
				"extended_bounds": map[string]any{
					"min": start.UTC().Format(time.RFC3339Nano),
					"max": end.UTC().Format(time.RFC3339Nano),
				},
			},
		},
	}
	if metric != "count" {
		aggs["traffic"].(map[string]any)["aggs"] = map[string]any{"value": map[string]any{metric: map[string]any{"field": source.MetricField}}}
	}
	body := map[string]any{
		"size":  0,
		"query": map[string]any{"bool": map[string]any{"filter": []any{filter, map[string]any{"range": map[string]any{source.TimestampField: map[string]any{"gte": start.UTC().Format(time.RFC3339Nano), "lt": end.UTC().Format(time.RFC3339Nano)}}}}}},
		"aggs":  aggs,
	}
	payload, _ := json.Marshal(body)
	endpoint := strings.TrimRight(o.settings.BaseURL, "/") + "/" + url.PathEscape(source.Index) + "/_search"
	if err := validateOutboundURLAllowingHosts(endpoint, o.allow); err != nil {
		return nil, fmt.Errorf("opensearch endpoint %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token := o.settings.bearerToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if o.settings.Username != "" {
		req.SetBasicAuth(o.settings.Username, o.settings.password())
	}
	response, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query opensearch: %w", err)
	}
	defer response.Body.Close()
	if maxBytes <= 0 {
		maxBytes = 16 << 20
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read opensearch response: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("opensearch response exceeds %d bytes", maxBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("opensearch returned HTTP %d", response.StatusCode)
	}
	var decoded struct {
		Aggregations struct {
			Traffic struct {
				Buckets []struct {
					Key      int64   `json:"key"`
					DocCount float64 `json:"doc_count"`
					Value    struct {
						Value *float64 `json:"value"`
					} `json:"value"`
				} `json:"buckets"`
			} `json:"traffic"`
		} `json:"aggregations"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, fmt.Errorf("decode opensearch response: %w", err)
	}
	if len(decoded.Aggregations.Traffic.Buckets) > trafficprofile.MaxPointsPerSeries {
		return nil, fmt.Errorf("opensearch returned %d buckets; limit is %d", len(decoded.Aggregations.Traffic.Buckets), trafficprofile.MaxPointsPerSeries)
	}
	points := make([]trafficprofile.Point, 0, len(decoded.Aggregations.Traffic.Buckets))
	for _, bucket := range decoded.Aggregations.Traffic.Buckets {
		value := bucket.DocCount
		if metric != "count" {
			if bucket.Value.Value == nil {
				value = 0
			} else {
				value = *bucket.Value.Value
			}
		}
		points = append(points, trafficprofile.Point{Timestamp: time.UnixMilli(bucket.Key).UTC(), Value: value})
	}
	return []trafficprofile.Series{{Key: "opensearch", Name: source.Index, Points: points}}, nil
}
