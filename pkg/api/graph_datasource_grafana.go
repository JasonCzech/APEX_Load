package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type grafanaGraphDataSource struct {
	settings GrafanaSettings
	client   *http.Client
	allow    map[string]struct{}
}

func newGrafanaGraphDataSource(settings GrafanaSettings) *grafanaGraphDataSource {
	settings = normalizeGrafanaSettings(settings)
	timeout := time.Duration(settings.TimeoutSeconds) * time.Second
	allow := map[string]struct{}{}
	for _, host := range settings.allowlistHosts() {
		allow[strings.ToLower(host)] = struct{}{}
	}
	return &grafanaGraphDataSource{settings: settings, client: newHostAllowlistedHTTPClient(settings.allowlistHosts(), timeout), allow: allow}
}

func (g *grafanaGraphDataSource) ID() string { return "grafana_proxy" }
func (g *grafanaGraphDataSource) Available() (bool, bool) {
	return g.settings.Enabled, strings.TrimSpace(g.settings.BaseURL) != "" && strings.TrimSpace(g.settings.ServiceAccountTokenEnvVar) != "" && g.settings.token() != ""
}

func (g *grafanaGraphDataSource) QueryRange(ctx context.Context, q graphRangeQuery) ([]PrometheusSeries, error) {
	if strings.TrimSpace(q.DatasourceUID) == "" {
		return nil, fmt.Errorf("Grafana proxy query is missing datasource UID")
	}
	token := g.settings.token()
	if token == "" {
		return nil, fmt.Errorf("Grafana service-account token environment variable %s is not set", g.settings.ServiceAccountTokenEnvVar)
	}
	payload := map[string]any{"from": strconv.FormatInt(q.Start.UnixMilli(), 10), "to": strconv.FormatInt(q.End.UnixMilli(), 10), "queries": []map[string]any{{"refId": "A", "datasource": map[string]string{"uid": q.DatasourceUID}, "expr": q.Expr, "format": "time_series", "instant": false, "intervalMs": q.StepSeconds * 1000, "maxDataPoints": maxPrometheusGraphPoints}}}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(g.settings.BaseURL, "/") + "/api/ds/query"
	if err := validateOutboundURLAllowingHosts(endpoint, g.allow); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("Grafana status %d", resp.StatusCode)
	}
	limit := q.MaxBytes
	if limit <= 0 {
		limit = maxPrometheusResponseBytes
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("Grafana response exceeds %d bytes", limit)
	}
	return decodeGrafanaFrames(raw, q)
}

func decodeGrafanaFrames(raw []byte, q graphRangeQuery) ([]PrometheusSeries, error) {
	var envelope struct {
		Results map[string]struct {
			Error  string `json:"error"`
			Frames []struct {
				Schema struct {
					Fields []struct {
						Name, Type string
						Labels     map[string]string `json:"labels"`
					} `json:"fields"`
				} `json:"schema"`
				Data struct {
					Values [][]json.RawMessage `json:"values"`
				} `json:"data"`
			} `json:"frames"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode Grafana response: %w", err)
	}
	result, ok := envelope.Results["A"]
	if !ok {
		return nil, fmt.Errorf("Grafana response is missing result A")
	}
	if result.Error != "" {
		return nil, fmt.Errorf("Grafana query: %s", result.Error)
	}
	series := []PrometheusSeries{}
	malformedFrames := 0
	for frameIndex, frame := range result.Frames {
		if len(frame.Schema.Fields) < 2 || len(frame.Data.Values) < 2 {
			malformedFrames++
			continue
		}
		timestamps, err := decodeGrafanaNumbers(frame.Data.Values[0])
		if err != nil {
			malformedFrames++
			continue
		}
		decodedFields := 0
		for fieldIndex := 1; fieldIndex < len(frame.Schema.Fields) && fieldIndex < len(frame.Data.Values); fieldIndex++ {
			values, err := decodeGrafanaNullableNumbers(frame.Data.Values[fieldIndex])
			if err != nil {
				continue
			}
			decodedFields++
			points := make([]PrometheusPoint, 0, min(len(timestamps), len(values)))
			for i := 0; i < len(timestamps) && i < len(values); i++ {
				if !values[i].ok {
					continue
				}
				ms := int64(timestamps[i])
				points = append(points, PrometheusPoint{Timestamp: time.UnixMilli(ms).UTC(), TimestampMs: ms, Value: values[i].value})
			}
			field := frame.Schema.Fields[fieldIndex]
			key := fmt.Sprintf("%s_f%d_%d", q.SeriesKeyPrefix, frameIndex, fieldIndex)
			label := strings.TrimSpace(field.Name)
			if label == "" {
				label = key
			}
			series = append(series, PrometheusSeries{Key: key, Label: label, Labels: cloneStringMap(field.Labels), Values: points})
		}
		if decodedFields == 0 {
			malformedFrames++
		}
	}
	if len(result.Frames) > 0 && malformedFrames == len(result.Frames) {
		return nil, fmt.Errorf("Grafana response contains no decodable time-series frames")
	}
	if q.MaxSeries > 0 && len(series) > q.MaxSeries {
		return nil, &seriesCapError{got: len(series), cap: q.MaxSeries}
	}
	return series, nil
}

func decodeGrafanaNumbers(raw []json.RawMessage) ([]float64, error) {
	values := make([]float64, len(raw))
	for i := range raw {
		if err := json.Unmarshal(raw[i], &values[i]); err != nil {
			return nil, err
		}
	}
	return values, nil
}

func decodeGrafanaNullableNumbers(raw []json.RawMessage) ([]struct {
	value float64
	ok    bool
}, error) {
	values := make([]struct {
		value float64
		ok    bool
	}, len(raw))
	for i := range raw {
		if string(raw[i]) == "null" {
			continue
		}
		if err := json.Unmarshal(raw[i], &values[i].value); err != nil {
			return nil, err
		}
		values[i].ok = true
	}
	return values, nil
}
