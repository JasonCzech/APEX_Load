package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
)

const (
	defaultPrometheusGraphStepSeconds = 15
	minPrometheusGraphStepSeconds     = 1
	maxPrometheusGraphStepSeconds     = 3600
	// maxPrometheusGraphRange bounds the lookback window a single request may
	// ask for, so a caller cannot force an unbounded scan.
	maxPrometheusGraphRange = 7 * 24 * time.Hour
	// maxPrometheusGraphPoints caps the number of samples per series; this
	// matches Prometheus's own default per-query resolution limit (11000) so an
	// oversized range/step combination is rejected up front rather than relying
	// on the upstream to error.
	maxPrometheusGraphPoints = 11000
	// maxPrometheusResponseBytes caps how much of an upstream response body we
	// will read, protecting the controller from a misbehaving/compromised
	// Prometheus that streams an unbounded body.
	maxPrometheusResponseBytes = 16 << 20 // 16 MiB
	// Prometheus controls error/status strings. Bound anything reflected to API
	// clients or persisted in dashboard snapshots.
	maxPrometheusErrorBytes = 1024
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
		writeError(w, http.StatusBadGateway, "prometheus query failed: "+boundedtext.TruncateUTF8(err.Error(), maxPrometheusErrorBytes))
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
	if span := end.Sub(start); span > maxPrometheusGraphRange {
		return time.Time{}, time.Time{}, 0, fmt.Errorf("requested range %s exceeds the maximum of %s", span, maxPrometheusGraphRange)
	}
	if points := int(end.Sub(start).Seconds())/stepSeconds + 1; points > maxPrometheusGraphPoints {
		return time.Time{}, time.Time{}, 0, fmt.Errorf("range/step would produce %d points per series, exceeding the maximum of %d; widen step_seconds or narrow the range", points, maxPrometheusGraphPoints)
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
	// The allowlist (host/scheme posture) is invariant across the pack — it
	// derives from settings.BaseURL, not the per-graph query — so build it once
	// here instead of rebuilding it inside every queryPrometheusGraph call.
	allow := make(map[string]struct{})
	for _, host := range settings.allowlistHosts() {
		allow[strings.ToLower(host)] = struct{}{}
	}

	// The graph queries are independent, so run them concurrently and reassemble
	// in definition order. This keeps handler latency near the slowest single
	// query rather than the sum of all six.
	graphs := make([]PrometheusGraph, len(prometheusGraphDefinitions))
	errs := make([]error, len(prometheusGraphDefinitions))
	var wg sync.WaitGroup
	for i, definition := range prometheusGraphDefinitions {
		wg.Add(1)
		go func(i int, definition prometheusGraphDefinition) {
			defer wg.Done()
			graph, err := queryPrometheusGraph(ctx, client, settings, allow, definition, start, end, stepSeconds)
			if err != nil {
				errs[i] = fmt.Errorf("%s: %w", definition.ID, err)
				return
			}
			graphs[i] = graph
		}(i, definition)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return graphs, nil
}

// queryPrometheusRange is the datasource transport: it issues one range query and
// returns the raw matrix series, applying the outbound-host allowlist, a bounded body
// read, and the standard Prometheus response envelope checks. It is deliberately free
// of any graph-shaping so both the curated pack and the imported-dashboard path (which
// supplies user PromQL and its own byte budget) can share it.
func queryPrometheusRange(
	ctx context.Context,
	client *http.Client,
	settings PrometheusSettings,
	allow map[string]struct{},
	expr string,
	start time.Time,
	end time.Time,
	stepSeconds int,
	maxBytes int64,
) ([]prometheusMatrixSeries, error) {
	endpoint, err := prometheusQueryRangeURL(settings.BaseURL, expr, start, end, stepSeconds, settings.TimeoutSeconds)
	if err != nil {
		return nil, err
	}
	if err := validateOutboundURLAllowingHosts(endpoint, allow); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	// Bound the response body: a compromised/misbehaving upstream must not be
	// able to stream an unbounded body into the controller.
	limited := io.LimitReader(resp.Body, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("response exceeds %d bytes", maxBytes)
	}

	var payload prometheusQueryRangeResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if payload.Status != "success" {
		if payload.Error != "" {
			return nil, errors.New(boundedtext.TruncateUTF8(payload.Error, maxPrometheusErrorBytes))
		}
		return nil, fmt.Errorf("status %q", boundedtext.TruncateUTF8(payload.Status, maxPrometheusErrorBytes))
	}
	if payload.Data.ResultType != "" && payload.Data.ResultType != "matrix" {
		return nil, fmt.Errorf("resultType %q", boundedtext.TruncateUTF8(payload.Data.ResultType, maxPrometheusErrorBytes))
	}
	return payload.Data.Result, nil
}

func queryPrometheusGraph(
	ctx context.Context,
	client *http.Client,
	settings PrometheusSettings,
	allow map[string]struct{},
	definition prometheusGraphDefinition,
	start time.Time,
	end time.Time,
	stepSeconds int,
) (PrometheusGraph, error) {
	results, err := queryPrometheusRange(ctx, client, settings, allow, definition.Query, start, end, stepSeconds, maxPrometheusResponseBytes)
	if err != nil {
		return PrometheusGraph{}, err
	}

	series := make([]PrometheusSeries, 0, len(results))
	for index, result := range results {
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

func prometheusQueryRangeURL(baseURL string, query string, start time.Time, end time.Time, stepSeconds int, timeoutSeconds int) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/") + "/api/v1/query_range")
	if err != nil {
		return "", err
	}
	values := parsed.Query()
	values.Set("query", query)
	values.Set("start", start.Format(time.RFC3339))
	values.Set("end", end.Format(time.RFC3339))
	values.Set("step", strconv.Itoa(stepSeconds))
	// Ask Prometheus to abandon the query server-side if it outlives our budget,
	// rather than APEX just closing the connection and leaving the query running.
	if timeoutSeconds > 0 {
		values.Set("timeout", strconv.Itoa(timeoutSeconds)+"s")
	}
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
	// Prometheus legitimately returns non-finite sample values (e.g. "NaN" from
	// 0/0, or ±Inf). encoding/json cannot marshal NaN/Inf and would fail the
	// whole graphs response mid-stream after the 200 header is committed, so
	// drop the point — a gap is the correct charting behavior.
	if math.IsNaN(value) || math.IsInf(value, 0) || math.IsNaN(timestampSeconds) || math.IsInf(timestampSeconds, 0) {
		return PrometheusPoint{}, false
	}
	timestampMillis := timestampSeconds * 1000
	if timestampMillis > float64(math.MaxInt64) || timestampMillis < float64(math.MinInt64) {
		return PrometheusPoint{}, false
	}
	timestamp := time.UnixMilli(int64(timestampMillis)).UTC()
	// time.Time's JSON encoder rejects years outside [0,9999]. A compromised
	// upstream must not be able to poison the complete graph response or terminal
	// snapshot with one out-of-range timestamp.
	if timestamp.Year() < 0 || timestamp.Year() > 9999 {
		return PrometheusPoint{}, false
	}
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
	readable := strings.NewReplacer("=", "_", ",", "_", " ", "_", "/", "_", ".", "_", "-", "_").Replace(strings.Join(parts, "_"))
	readable = boundedtext.TruncateUTF8(readable, 128)
	identity, _ := json.Marshal(labels) // map keys are encoded in deterministic order
	digest := sha256.Sum256(identity)
	return fmt.Sprintf("%s_%s_%x", graphID, readable, digest[:6])
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
		return boundedtext.TruncateUTF8(strings.Join(parts, " / "), 256)
	}
	if name := strings.TrimSpace(labels["__name__"]); name != "" {
		return boundedtext.TruncateUTF8(prometheusMetricNameLabel(name), 256)
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
