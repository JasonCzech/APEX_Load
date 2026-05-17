package api

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/engine"
)

type externalMetricCollector struct {
	client *http.Client
}

type externalMetricTarget struct {
	Name       string
	URL        string
	MetricName string
	Labels     map[string]string
}

func newExternalMetricCollector(client *http.Client) *externalMetricCollector {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Second}
	}
	return &externalMetricCollector{client: client}
}

func (c *externalMetricCollector) collect(ctx context.Context, targets []externalMetricTarget) map[string]float64 {
	if c == nil || c.client == nil || len(targets) == 0 {
		return nil
	}
	values := make(map[string]float64)
	for _, target := range targets {
		value, ok := c.collectOne(ctx, target)
		if ok {
			values[target.Name] = value
		}
	}
	if len(values) == 0 {
		return nil
	}
	return values
}

func (c *externalMetricCollector) collectOne(ctx context.Context, target externalMetricTarget) (float64, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.URL, nil)
	if err != nil {
		return 0, false
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return 0, false
	}
	value, ok, err := parsePrometheusMetricValue(resp.Body, target.MetricName, target.Labels)
	if err != nil {
		return 0, false
	}
	return value, ok
}

func externalMetricTargetsForTest(test *ManagedTest) []externalMetricTarget {
	if test == nil {
		return nil
	}
	seen := make(map[string]struct{})
	targets := make([]externalMetricTarget, 0)
	for _, group := range test.Groups {
		if group.script == nil {
			continue
		}
		for _, metric := range group.script.Config.Observability.ExternalMetrics {
			target, ok := externalMetricTargetFromConfig(metric)
			if !ok {
				continue
			}
			key := externalMetricTargetKey(target)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			targets = append(targets, target)
		}
	}
	return targets
}

func externalMetricTargetFromConfig(metric dsl.ExternalMetricConfig) (externalMetricTarget, bool) {
	name := strings.TrimSpace(metric.Name)
	rawURL := strings.TrimSpace(metric.URL)
	metricName := strings.TrimSpace(metric.MetricName)
	format := strings.TrimSpace(metric.Format)
	if format == "" {
		format = "prometheus"
	}
	if name == "" || rawURL == "" || metricName == "" || !strings.EqualFold(format, "prometheus") {
		return externalMetricTarget{}, false
	}
	return externalMetricTarget{
		Name:       name,
		URL:        rawURL,
		MetricName: metricName,
		Labels:     cloneStringMap(metric.Labels),
	}, true
}

func externalMetricTargetKey(target externalMetricTarget) string {
	labels := make([]string, 0, len(target.Labels))
	for key, value := range target.Labels {
		labels = append(labels, key+"="+value)
	}
	sort.Strings(labels)
	return target.Name + "|" + target.URL + "|" + target.MetricName + "|" + strings.Join(labels, ",")
}

func attachExternalMetrics(resources *engine.SystemResources, values map[string]float64) *engine.SystemResources {
	if len(values) == 0 {
		return resources
	}
	if resources == nil {
		resources = &engine.SystemResources{}
	}
	resources.External = cloneFloat64Map(values)
	return resources
}

func parsePrometheusMetricValue(reader io.Reader, metricName string, labels map[string]string) (float64, bool, error) {
	scanner := bufio.NewScanner(reader)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		sampleName, sampleLabels, value, ok := parsePrometheusSampleLine(line)
		if !ok || sampleName != metricName || !prometheusLabelsMatch(sampleLabels, labels) {
			continue
		}
		return value, true, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, false, err
	}
	return 0, false, nil
}

func parsePrometheusSampleLine(line string) (string, map[string]string, float64, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return "", nil, 0, false
	}
	nameAndLabels := fields[0]
	value, err := strconv.ParseFloat(fields[1], 64)
	if err != nil {
		return "", nil, 0, false
	}
	name := nameAndLabels
	labels := map[string]string{}
	if open := strings.Index(nameAndLabels, "{"); open >= 0 {
		close := strings.LastIndex(nameAndLabels, "}")
		if close < open {
			return "", nil, 0, false
		}
		name = nameAndLabels[:open]
		parsedLabels, err := parsePrometheusLabels(nameAndLabels[open+1 : close])
		if err != nil {
			return "", nil, 0, false
		}
		labels = parsedLabels
	}
	if name == "" {
		return "", nil, 0, false
	}
	return name, labels, value, true
}

func parsePrometheusLabels(raw string) (map[string]string, error) {
	labels := make(map[string]string)
	for _, part := range splitPrometheusLabelParts(raw) {
		if strings.TrimSpace(part) == "" {
			continue
		}
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("invalid label %q", part)
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if key == "" {
			return nil, fmt.Errorf("empty label key")
		}
		labels[key] = strings.ReplaceAll(value, `\"`, `"`)
	}
	return labels, nil
}

func splitPrometheusLabelParts(raw string) []string {
	parts := make([]string, 0)
	var current strings.Builder
	inQuotes := false
	escaped := false
	for _, ch := range raw {
		switch {
		case escaped:
			current.WriteRune(ch)
			escaped = false
		case ch == '\\':
			current.WriteRune(ch)
			escaped = true
		case ch == '"':
			current.WriteRune(ch)
			inQuotes = !inQuotes
		case ch == ',' && !inQuotes:
			parts = append(parts, current.String())
			current.Reset()
		default:
			current.WriteRune(ch)
		}
	}
	parts = append(parts, current.String())
	return parts
}

func prometheusLabelsMatch(sample map[string]string, expected map[string]string) bool {
	for key, want := range expected {
		if sample[key] != want {
			return false
		}
	}
	return true
}

func cloneFloat64Map(values map[string]float64) map[string]float64 {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]float64, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
