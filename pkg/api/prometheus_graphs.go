package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPrometheusGraphStepSeconds = 15
	minPrometheusGraphStepSeconds     = 1
	maxPrometheusGraphStepSeconds     = 3600
)

type PrometheusGraphsResponse struct {
	Enabled     bool              `json:"enabled"`
	Configured  bool              `json:"configured"`
	GeneratedAt time.Time         `json:"generated_at"`
	Start       time.Time         `json:"start,omitempty"`
	End         time.Time         `json:"end,omitempty"`
	StepSeconds int               `json:"step_seconds,omitempty"`
	Graphs      []PrometheusGraph `json:"graphs"`
}

type PrometheusGraph struct {
	ID     string             `json:"id"`
	Title  string             `json:"title"`
	Unit   string             `json:"unit,omitempty"`
	Series []PrometheusSeries `json:"series"`
}

type PrometheusSeries struct {
	Key    string            `json:"key"`
	Label  string            `json:"label"`
	Labels map[string]string `json:"labels,omitempty"`
	Values []PrometheusPoint `json:"values"`
}

type PrometheusPoint struct {
	Timestamp   time.Time `json:"timestamp"`
	TimestampMs int64     `json:"timestamp_ms"`
	Value       float64   `json:"value"`
}

type prometheusGraphDefinition struct {
	ID          string
	Title       string
	Unit        string
	Query       string
	LabelFields []string
}

var prometheusGraphDefinitions = []prometheusGraphDefinition{
	{
		ID:          "prometheus_scaling_vusers",
		Title:       "Scaling VUsers",
		Unit:        "VUsers",
		Query:       "apexload_scaling_vusers",
		LabelFields: []string{"state"},
	},
	{
		ID:          "prometheus_scaling_tests",
		Title:       "Scaling Tests",
		Unit:        "Tests",
		Query:       "apexload_scaling_tests",
		LabelFields: []string{"state"},
	},
	{
		ID:          "prometheus_protocol_vusers",
		Title:       "Protocol VUsers",
		Unit:        "VUsers",
		Query:       "sum by (state, protocol) (apexload_scaling_protocol_vusers)",
		LabelFields: []string{"state", "protocol"},
	},
	{
		ID:          "prometheus_generator_capacity",
		Title:       "Generator Capacity",
		Unit:        "VUsers",
		Query:       "apexload_generator_region_available_capacity or apexload_generator_region_active_vusers",
		LabelFields: []string{"__name__", "region"},
	},
	{
		ID:          "prometheus_generator_states",
		Title:       "Generator States",
		Unit:        "Generators",
		Query:       "sum by (state) (apexload_generator_capacity_state_count)",
		LabelFields: []string{"state"},
	},
	{
		ID:          "prometheus_capacity_envelope",
		Title:       "Capacity Envelope",
		Unit:        "Generators",
		Query:       "apexload_capacity_available_generators",
		LabelFields: []string{"scope"},
	},
}

type prometheusQueryRangeResponse struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
	Data   struct {
		ResultType string                   `json:"resultType"`
		Result     []prometheusMatrixSeries `json:"result"`
	} `json:"data"`
}

type prometheusMatrixSeries struct {
	Metric map[string]string `json:"metric"`
	Values []json.RawMessage `json:"values"`
}

func (s *Server) handlePrometheusGraphs(w http.ResponseWriter, r *http.Request) {
	settings := normalizePrometheusSettings(s.runner.GetSystemSettings().Prometheus)
	configured := strings.TrimSpace(settings.BaseURL) != ""
	if !settings.Enabled || !configured {
		writeJSON(w, http.StatusOK, PrometheusGraphsResponse{
			Enabled:     settings.Enabled,
			Configured:  configured,
			GeneratedAt: time.Now().UTC(),
			Graphs:      []PrometheusGraph{},
		})
		return
	}

	start, end, stepSeconds, err := parsePrometheusGraphRange(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	graphs, err := queryPrometheusGraphPack(r.Context(), settings, start, end, stepSeconds)
	if err != nil {
		writeError(w, http.StatusBadGateway, "prometheus query failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, PrometheusGraphsResponse{
		Enabled:     true,
		Configured:  true,
		GeneratedAt: time.Now().UTC(),
		Start:       start,
		End:         end,
		StepSeconds: stepSeconds,
		Graphs:      graphs,
	})
}

func parsePrometheusGraphRange(r *http.Request) (time.Time, time.Time, int, error) {
	start, err := parsePrometheusGraphTime(r.URL.Query().Get("start"), "start")
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	end, err := parsePrometheusGraphTime(r.URL.Query().Get("end"), "end")
	if err != nil {
		return time.Time{}, time.Time{}, 0, err
	}
	if !end.After(start) {
		return time.Time{}, time.Time{}, 0, fmt.Errorf("end must be after start")
	}
	stepSeconds := defaultPrometheusGraphStepSeconds
	if raw := strings.TrimSpace(r.URL.Query().Get("step_seconds")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			return time.Time{}, time.Time{}, 0, fmt.Errorf("step_seconds must be an integer")
		}
		stepSeconds = parsed
	}
	if stepSeconds < minPrometheusGraphStepSeconds || stepSeconds > maxPrometheusGraphStepSeconds {
		return time.Time{}, time.Time{}, 0, fmt.Errorf("step_seconds must be between %d and %d", minPrometheusGraphStepSeconds, maxPrometheusGraphStepSeconds)
	}
	return start, end, stepSeconds, nil
}

func parsePrometheusGraphTime(raw string, field string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("%s is required", field)
	}
	timestamp, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("%s must be RFC3339", field)
	}
	return timestamp.UTC(), nil
}

func queryPrometheusGraphPack(ctx context.Context, settings PrometheusSettings, start time.Time, end time.Time, stepSeconds int) ([]PrometheusGraph, error) {
	timeout := time.Duration(settings.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = time.Duration(defaultPrometheusSettings().TimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client := newHostAllowlistedHTTPClient(settings.allowlistHosts(), timeout)
	graphs := make([]PrometheusGraph, 0, len(prometheusGraphDefinitions))
	for _, definition := range prometheusGraphDefinitions {
		graph, err := queryPrometheusGraph(ctx, client, settings, definition, start, end, stepSeconds)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", definition.ID, err)
		}
		graphs = append(graphs, graph)
	}
	return graphs, nil
}

func queryPrometheusGraph(
	ctx context.Context,
	client *http.Client,
	settings PrometheusSettings,
	definition prometheusGraphDefinition,
	start time.Time,
	end time.Time,
	stepSeconds int,
) (PrometheusGraph, error) {
	endpoint, err := prometheusQueryRangeURL(settings.BaseURL, definition.Query, start, end, stepSeconds)
	if err != nil {
		return PrometheusGraph{}, err
	}
	allow := make(map[string]struct{})
	for _, host := range settings.allowlistHosts() {
		allow[strings.ToLower(host)] = struct{}{}
	}
	if err := validateOutboundURLAllowingHosts(endpoint, allow); err != nil {
		return PrometheusGraph{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return PrometheusGraph{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return PrometheusGraph{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return PrometheusGraph{}, fmt.Errorf("status %d", resp.StatusCode)
	}

	var payload prometheusQueryRangeResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return PrometheusGraph{}, fmt.Errorf("decode response: %w", err)
	}
	if payload.Status != "success" {
		if payload.Error != "" {
			return PrometheusGraph{}, fmt.Errorf("%s", payload.Error)
		}
		return PrometheusGraph{}, fmt.Errorf("status %q", payload.Status)
	}
	if payload.Data.ResultType != "" && payload.Data.ResultType != "matrix" {
		return PrometheusGraph{}, fmt.Errorf("resultType %q", payload.Data.ResultType)
	}

	series := make([]PrometheusSeries, 0, len(payload.Data.Result))
	for index, result := range payload.Data.Result {
		values := make([]PrometheusPoint, 0, len(result.Values))
		for _, raw := range result.Values {
			point, ok := decodePrometheusPoint(raw)
			if ok {
				values = append(values, point)
			}
		}
		key := prometheusSeriesKey(definition.ID, result.Metric, index)
		series = append(series, PrometheusSeries{
			Key:    key,
			Label:  prometheusSeriesLabel(definition, result.Metric, index),
			Labels: cloneStringMap(result.Metric),
			Values: values,
		})
	}

	sort.SliceStable(series, func(i, j int) bool {
		return series[i].Label < series[j].Label
	})

	return PrometheusGraph{
		ID:     definition.ID,
		Title:  definition.Title,
		Unit:   definition.Unit,
		Series: series,
	}, nil
}

func prometheusQueryRangeURL(baseURL string, query string, start time.Time, end time.Time, stepSeconds int) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/api/v1/query_range")
	if err != nil {
		return "", err
	}
	values := parsed.Query()
	values.Set("query", query)
	values.Set("start", start.Format(time.RFC3339))
	values.Set("end", end.Format(time.RFC3339))
	values.Set("step", strconv.Itoa(stepSeconds))
	parsed.RawQuery = values.Encode()
	return parsed.String(), nil
}

func decodePrometheusPoint(raw json.RawMessage) (PrometheusPoint, bool) {
	var tuple []json.RawMessage
	if err := json.Unmarshal(raw, &tuple); err != nil || len(tuple) < 2 {
		return PrometheusPoint{}, false
	}
	timestampSeconds, ok := decodePrometheusFloat(tuple[0])
	if !ok {
		return PrometheusPoint{}, false
	}
	value, ok := decodePrometheusFloat(tuple[1])
	if !ok {
		return PrometheusPoint{}, false
	}
	timestamp := time.UnixMilli(int64(timestampSeconds * 1000)).UTC()
	return PrometheusPoint{
		Timestamp:   timestamp,
		TimestampMs: timestamp.UnixMilli(),
		Value:       value,
	}, true
}

func decodePrometheusFloat(raw json.RawMessage) (float64, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		value, err := strconv.ParseFloat(text, 64)
		return value, err == nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0, false
	}
	return value, true
}

func prometheusSeriesKey(graphID string, labels map[string]string, fallback int) string {
	parts := make([]string, 0, len(labels))
	for key, value := range labels {
		if key == "__name__" {
			continue
		}
		parts = append(parts, key+"="+value)
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return fmt.Sprintf("%s_series_%d", graphID, fallback+1)
	}
	return graphID + "_" + strings.NewReplacer("=", "_", ",", "_", " ", "_", "/", "_", ".", "_", "-", "_").Replace(strings.Join(parts, "_"))
}

func prometheusSeriesLabel(definition prometheusGraphDefinition, labels map[string]string, fallback int) string {
	parts := make([]string, 0, len(definition.LabelFields))
	for _, field := range definition.LabelFields {
		value := strings.TrimSpace(labels[field])
		if value == "" {
			continue
		}
		if field == "__name__" {
			value = prometheusMetricNameLabel(value)
		}
		parts = append(parts, value)
	}
	if len(parts) > 0 {
		return strings.Join(parts, " / ")
	}
	if name := strings.TrimSpace(labels["__name__"]); name != "" {
		return prometheusMetricNameLabel(name)
	}
	return fmt.Sprintf("Series %d", fallback+1)
}

func prometheusMetricNameLabel(name string) string {
	switch name {
	case "apexload_generator_region_available_capacity":
		return "Available"
	case "apexload_generator_region_active_vusers":
		return "Active"
	default:
		return strings.TrimPrefix(name, "apexload_")
	}
}
