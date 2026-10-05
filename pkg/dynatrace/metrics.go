package dynatrace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/scrub"
)

var errUnsafeDimensions = errors.New("Dynatrace dimension contains protected credential material")

type metricDescriptor struct {
	ID               string   `json:"metricId"`
	Name             string   `json:"displayName"`
	Unit             string   `json:"unit"`
	AggregationTypes []string `json:"aggregationTypes"`
	MetricValueType  struct {
		Type string `json:"type"`
	} `json:"metricValueType"`
	DefaultAggregation struct {
		Type string `json:"type"`
	} `json:"defaultAggregation"`
	DimensionDefinitions []struct {
		Key string `json:"key"`
	} `json:"dimensionDefinitions"`
}
type metricResponse struct {
	Resolution  string   `json:"resolution"`
	NextPageKey string   `json:"nextPageKey"`
	Warnings    []string `json:"warnings"`
	Result      []struct {
		ID             string   `json:"metricId"`
		Warnings       []string `json:"warnings"`
		PointRatio     float64  `json:"dataPointCountRatio"`
		DimensionRatio float64  `json:"dimensionCountRatio"`
		Data           []struct {
			DimensionMap json.RawMessage `json:"dimensionMap"`
			Dimensions   []*string       `json:"dimensions"`
			Timestamps   []int64         `json:"timestamps"`
			Values       []*float64      `json:"values"`
		} `json:"data"`
	} `json:"result"`
}

var transformations = regexp.MustCompile(`:(?:avg|min|max|sum|count|value|auto|percentile|splitBy|filter|merge|parents|sort|limit|rate|setUnit|toUnit)(?:\(|:|$)`)

func baseMetricID(selector string) (string, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return "", errors.New("empty metric selector")
	}
	if selector[0] == '"' {
		var out strings.Builder
		for i := 1; i < len(selector); i++ {
			if selector[i] == '~' {
				i++
				if i >= len(selector) {
					return "", errors.New("invalid escaped metric key")
				}
				out.WriteByte(selector[i])
				continue
			}
			if selector[i] == '"' {
				if tail := strings.TrimSpace(selector[i+1:]); tail != "" && !strings.HasPrefix(tail, ":") {
					return "", errors.New("native metric queries require one explicit metric key")
				}
				return out.String(), nil
			}
			out.WriteByte(selector[i])
		}
		return "", errors.New("unterminated metric key")
	}
	base := selector
	if loc := transformations.FindStringIndex(selector); loc != nil {
		base = selector[:loc[0]]
	}
	if len(base) > 256 || strings.ContainsAny(base, "(), *\t\r\n\x00") {
		return "", errors.New("native metric queries require one explicit metric key")
	}
	return base, nil
}

var errUnsupportedTransform = errors.New("metric selector transformation is outside the supported set")

// allowedSelectorTransforms lists the transformations whose effect on time,
// gaps, units and statistics is modelled. Anything else could fold or shift
// time, fill gaps or reshape series without changing the frozen statistic.
var allowedSelectorTransforms = map[string]bool{"avg": true, "min": true, "max": true, "sum": true, "count": true, "value": true, "percentile": true, "splitBy": true, "filter": true, "merge": true, "parents": true, "sort": true, "limit": true, "rate": true, "setUnit": true, "toUnit": true}

// validateSelectorTransforms requires everything after the metric key to be a
// contiguous chain of allowed transformations.
func validateSelectorTransforms(selector string) error {
	selector = strings.TrimSpace(selector)
	base, err := baseMetricID(selector)
	if err != nil {
		return err
	}
	var chain string
	if selector[0] == '"' {
		chain = strings.TrimSpace(selector[quotedMetricKeyEnd(selector)+1:])
	} else {
		// Metric keys carry at most one colon (builtin:, ext:, calc:, func:), so
		// a further colon inside the unquoted key is an unrecognised transform.
		if strings.Count(base, ":") > 1 {
			return errUnsupportedTransform
		}
		chain = selector[len(base):]
	}
	transforms, err := selectorTransforms(chain)
	if err != nil {
		return err
	}
	next := 0
	for _, transform := range transforms {
		if transform.start != next || !allowedSelectorTransforms[transform.name] {
			return errUnsupportedTransform
		}
		next = transform.end
	}
	if next != len(chain) {
		return errUnsupportedTransform
	}
	return nil
}

// quotedMetricKeyEnd returns the index of the closing quote of a quoted key
// already accepted by baseMetricID.
func quotedMetricKeyEnd(selector string) int {
	for i := 1; i < len(selector); i++ {
		if selector[i] == '~' {
			i++
			continue
		}
		if selector[i] == '"' {
			return i
		}
	}
	return len(selector) - 1
}

// MetricID is stable across aliases and returned descriptor display names.
func MetricID(m MetricSelection) string {
	return "metrics:" + hashIdentity(m.MetricSelector, m.Aggregation, m.Semantics)
}
func GrailMetricID(q GrailQuery) string {
	return "grail:" + hashIdentity(q.Name, q.Query, q.ValueField, q.Statistic, strconv.Itoa(q.BucketSeconds))
}
func metricStatistic(m MetricSelection) string {
	if transforms, err := selectorTransforms(m.MetricSelector); err == nil {
		for _, transform := range transforms {
			if transform.name == "rate" {
				return "rate"
			}
		}
	}
	switch m.Aggregation {
	case "avg":
		if m.Semantics == "gauge" {
			return "time_average"
		}
		return "average"
	case "count":
		return "sum"
	default:
		return m.Aggregation
	}
}
func MetricStatistic(m MetricSelection) string { return metricStatistic(m) }

type selectorTransform struct {
	name, argument string
	start, end     int
	hasArgument    bool
}

// Inspect only top-level transformations. Colons in quoted metric keys and
// nested filter strings cannot change statistic or unit metadata.
func selectorTransforms(selector string) ([]selectorTransform, error) {
	var out []selectorTransform
	quoted, depth := false, 0
	for i := 0; i < len(selector); i++ {
		if selector[i] == '~' {
			i++
			if i >= len(selector) {
				return nil, errors.New("invalid escaped metric selector")
			}
			continue
		}
		if selector[i] == '"' {
			quoted = !quoted
			continue
		}
		if quoted {
			continue
		}
		switch selector[i] {
		case ',':
			if depth == 0 {
				return nil, errors.New("native metric queries require one explicit metric key")
			}
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return nil, errors.New("invalid metric selector parentheses")
			}
		case ':':
			if depth != 0 {
				continue
			}
			end := i + 1
			for end < len(selector) && (selector[end] >= 'a' && selector[end] <= 'z' || selector[end] >= 'A' && selector[end] <= 'Z') {
				end++
			}
			if end == i+1 || end < len(selector) && selector[end] != '(' && selector[end] != ':' {
				continue
			}
			transform := selectorTransform{name: selector[i+1 : end], start: i, end: end}
			if end < len(selector) && selector[end] == '(' {
				transform.hasArgument = true
				argumentStart, nesting, argumentQuoted := end+1, 1, false
				for end++; end < len(selector); end++ {
					if selector[end] == '~' {
						end++
						continue
					}
					if selector[end] == '"' {
						argumentQuoted = !argumentQuoted
					}
					if argumentQuoted {
						continue
					}
					if selector[end] == '(' {
						nesting++
					}
					if selector[end] == ')' {
						nesting--
						if nesting == 0 {
							break
						}
					}
				}
				if end == len(selector) {
					return nil, errors.New("invalid metric selector argument")
				}
				transform.argument, transform.end = selector[argumentStart:end], end+1
			}
			out = append(out, transform)
			i = transform.end - 1
		}
	}
	if quoted || depth != 0 {
		return nil, errors.New("invalid metric selector syntax")
	}
	return out, nil
}

func selectorAggregations(selector string) []string {
	parts := []string{}
	transforms, _ := selectorTransforms(selector)
	for _, transform := range transforms {
		switch transform.name {
		case "avg", "min", "max", "sum", "count", "value":
			parts = append(parts, transform.name)
		case "percentile":
			parts = append(parts, "percentile("+transform.argument+")")
		}
	}
	return parts
}

func validateSelectorAggregation(m MetricSelection) error {
	transforms, err := selectorTransforms(m.MetricSelector)
	if err != nil {
		return err
	}
	parts := selectorAggregations(m.MetricSelector)
	wanted := m.Aggregation
	if wanted == "p95" {
		wanted = "percentile(95)"
	}
	if wanted == "p99" {
		wanted = "percentile(99)"
	}
	if len(parts) > 1 || len(parts) == 1 && parts[0] != wanted {
		return errors.New("metric selector aggregation differs from frozen statistic")
	}
	seenAggregation, rates := false, 0
	for _, transform := range transforms {
		switch transform.name {
		case "avg", "min", "max", "sum", "count", "value", "percentile":
			seenAggregation = true
		case "rate", "setUnit", "toUnit":
			if len(parts) > 0 && !seenAggregation {
				return errors.New("metric aggregation must precede rate and unit transformations")
			}
			if transform.name == "rate" {
				rates++
				if _, err := rateDenominator(transform.argument); err != nil || rates > 1 {
					return errors.New("invalid or repeated rate transformation")
				}
			} else if _, err := unitArguments(transform); err != nil {
				return err
			}
		}
	}
	return nil
}
func querySelector(m MetricSelection) string {
	selector := m.MetricSelector
	// Explicit existing aggregations must match the frozen selection.
	if len(selectorAggregations(selector)) > 0 {
		return selector
	}
	agg := m.Aggregation
	if agg == "p95" {
		agg = "percentile(95)"
	}
	if agg == "p99" {
		agg = "percentile(99)"
	}
	transforms, _ := selectorTransforms(selector)
	for _, transform := range transforms {
		if transform.name == "rate" || transform.name == "setUnit" || transform.name == "toUnit" {
			return selector[:transform.start] + ":" + agg + selector[transform.start:]
		}
	}
	return selector + ":" + agg
}
func parseResolution(raw string) (int, error) {
	if raw == "" || strings.EqualFold(raw, "Inf") {
		return 0, errors.New("metric response requires actual bucket resolution")
	}
	duration, err := time.ParseDuration(raw)
	if err != nil {
		if strings.HasSuffix(raw, "d") {
			days, e := strconv.Atoi(strings.TrimSuffix(raw, "d"))
			if e == nil {
				duration = time.Duration(days) * 24 * time.Hour
				err = nil
			}
		}
	}
	if err != nil || duration < time.Second || duration > 24*time.Hour || duration%time.Second != 0 {
		return 0, errors.New("unsupported metric resolution")
	}
	return int(duration / time.Second), nil
}
func NormalizeUnit(raw string) string {
	switch raw {
	case "MicroSecond":
		return "us"
	case "MilliSecond":
		return "ms"
	case "Second":
		return "s"
	case "Minute":
		return "min"
	case "Percent":
		return "%"
	case "Byte":
		return "bytes"
	case "BytePerSecond":
		return "bytes/s"
	case "Count":
		return "count"
	case "CountPerSecond":
		return "count/s"
	case "Ratio":
		return "ratio"
	case "NotApplicable", "Unspecified", "":
		return "value"
	}
	return raw
}
func transformedUnit(selector, descriptor string) (string, error) {
	unit := NormalizeUnit(descriptor)
	transforms, err := selectorTransforms(selector)
	if err != nil {
		return "", err
	}
	for _, transform := range transforms {
		switch transform.name {
		case "setUnit", "toUnit":
			parts, err := unitArguments(transform)
			if err != nil {
				return "", err
			}
			if transform.name == "toUnit" && NormalizeUnit(parts[0]) != unit {
				return "", errors.New("unit conversion source differs from current metric unit")
			}
			unit = NormalizeUnit(parts[len(parts)-1])
		case "rate":
			denominator, err := rateDenominator(transform.argument)
			if err != nil {
				return "", err
			}
			unit += "/" + denominator
		}
	}
	return unit, nil
}

var transformedUnitName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,63}$`)
var rateTimebase = regexp.MustCompile(`^([0-9]*)([smhdwMy])$`)

func unitArguments(transform selectorTransform) ([]string, error) {
	parts := strings.Split(transform.argument, ",")
	expected := 1
	if transform.name == "toUnit" {
		expected = 2
	}
	if !transform.hasArgument || len(parts) != expected {
		return nil, errors.New("invalid unit transformation")
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if !transformedUnitName.MatchString(parts[i]) {
			return nil, errors.New("invalid transformed unit")
		}
	}
	return parts, nil
}

func rateDenominator(argument string) (string, error) {
	argument = strings.TrimSpace(argument)
	if argument == "" {
		return "min", nil // Dynatrace defaults rate() to one minute.
	}
	match := rateTimebase.FindStringSubmatch(argument)
	if match == nil {
		return "", errors.New("invalid rate timebase")
	}
	factor := 1
	if match[1] != "" {
		var err error
		factor, err = strconv.Atoi(match[1])
		if err != nil || factor < 1 || factor > 1_000_000 {
			return "", errors.New("rate timebase exceeds bounds")
		}
	}
	denominator := map[string]string{"s": "s", "m": "min", "h": "h", "d": "d", "w": "week", "M": "month", "y": "year"}[match[2]]
	if factor > 1 {
		denominator = strconv.Itoa(factor) + denominator
	}
	return denominator, nil
}
func (c *Client) metrics(ctx context.Context, m MetricSelection, project string, from, to time.Time, settle int) ([]analysis.MetricPoint, bool, error) {
	base, err := baseMetricID(m.MetricSelector)
	if err != nil {
		return nil, false, err
	}
	descriptorData, err := c.request(ctx, false, http.MethodGet, "/api/v2/metrics/"+url.PathEscape(base), nil, nil)
	if err != nil {
		return nil, false, err
	}
	var descriptor metricDescriptor
	if json.Unmarshal(descriptorData, &descriptor) != nil || descriptor.ID != base {
		return nil, false, errors.New("metric descriptor identity mismatch")
	}
	if m.Semantics == "gauge" && !strings.EqualFold(descriptor.MetricValueType.Type, "gauge") {
		return nil, false, errors.New("metric descriptor does not corroborate gauge semantics")
	}
	unit, err := transformedUnit(m.MetricSelector, descriptor.Unit)
	if err != nil {
		return nil, false, err
	}
	if m.Unit != "" && NormalizeUnit(m.Unit) != unit {
		return nil, false, errors.New("metric descriptor unit changed")
	}
	if m.Unit != "" {
		unit = NormalizeUnit(m.Unit)
	}
	if len(descriptor.AggregationTypes) > 0 {
		agg := m.Aggregation
		if agg == "p95" || agg == "p99" {
			agg = "percentile"
		}
		available := false
		for _, a := range descriptor.AggregationTypes {
			if a == agg {
				available = true
			}
		}
		if !available {
			return nil, false, errors.New("metric descriptor does not support selected aggregation")
		}
	}
	params := url.Values{"metricSelector": {querySelector(m)}, "from": {from.Format(time.RFC3339Nano)}, "to": {to.Format(time.RFC3339Nano)}, "resolution": {"1m"}}
	if m.EntitySelector != "" {
		params.Set("entitySelector", m.EntitySelector)
	}
	data, err := c.request(ctx, false, http.MethodGet, "/api/v2/metrics/query", params, nil)
	if err != nil {
		return nil, false, err
	}
	var payload metricResponse
	if json.Unmarshal(data, &payload) != nil {
		return nil, false, fmt.Errorf("invalid metric response: %w", ErrInvalidResponse)
	}
	seconds, err := parseResolution(payload.Resolution)
	if err != nil {
		return nil, false, err
	}
	truncated := payload.NextPageKey != "" || len(payload.Warnings) > 0
	points := []analysis.MetricPoint{}
	series := 0
	for _, result := range payload.Result {
		if result.ID != "" && result.ID != base && !strings.HasPrefix(result.ID, base+":") {
			return nil, false, errors.New("metric result outside selected identity")
		}
		if len(result.Warnings) > 0 || result.PointRatio >= 1 || result.DimensionRatio >= 1 {
			truncated = true
		}
		for _, row := range result.Data {
			series++
			if series > c.cfg.MaxSeries {
				truncated = true
				break
			}
			if len(row.Timestamps) != len(row.Values) {
				return nil, false, errors.New("metric timestamp/value length mismatch")
			}
			// Dynatrace uses an explicitly null dimensionMap for its remainder
			// aggregate after dropping dimension tuples. It has no exact selected
			// identity and must not collide with a genuine dimensionless series.
			if string(row.DimensionMap) == "null" {
				truncated = true
				continue
			}
			dimensions := map[string]string{}
			if len(row.DimensionMap) > 0 && json.Unmarshal(row.DimensionMap, &dimensions) != nil {
				return nil, false, errors.New("invalid metric dimension map")
			}
			if len(dimensions) == 0 && len(row.Dimensions) > 0 {
				dimensions = map[string]string{}
				for i, v := range row.Dimensions {
					if v == nil || i >= len(descriptor.DimensionDefinitions) {
						return nil, false, errors.New("ambiguous legacy metric dimensions")
					}
					dimensions[descriptor.DimensionDefinitions[i].Key] = *v
				}
			}
			if !validDimensions(dimensions) {
				if !dimensionLabelsSafe(dimensions) {
					return nil, false, errUnsafeDimensions
				}
				return nil, false, errors.New("metric dimensions exceed identity bounds")
			}
			entities := map[string]string{}
			for k, v := range dimensions {
				if strings.HasPrefix(k, "dt.entity.") {
					entities[k] = v
				}
			}
			for i, endMS := range row.Timestamps {
				end := time.UnixMilli(endMS).UTC()
				start := end.Add(-time.Duration(seconds) * time.Second)
				if !end.After(from) || !start.Before(to) {
					continue
				}
				if len(points) >= c.cfg.MaxPoints {
					truncated = true
					break
				}
				value := row.Values[i]
				quality := analysis.QualityValid
				if value == nil {
					quality = analysis.QualityMissing
				} else if math.IsNaN(*value) || math.IsInf(*value, 0) {
					return nil, false, errors.New("non-finite metric observation")
				}
				stat := metricStatistic(m)
				eligible := stat == "value" || stat == "current" || stat == "rate" || stat == "sum" || stat == "min" || stat == "max" || stat == "time_average"
				eligible = eligible && m.Aggregation != "p95" && m.Aggregation != "p99" && (m.Aggregation != "avg" || m.Semantics == "gauge")
				meta := &analysis.ProviderMeasurement{Name: "dynatrace", Backend: "classic", MetricID: MetricID(m), CatalogPath: m.MetricSelector, EntityIDs: entities, BucketStart: start, BucketEnd: end, ResolutionSeconds: seconds, Statistic: stat, Settled: !c.now().UTC().Before(end.Add(time.Duration(settle) * time.Second)), AggregationEligible: eligible && value != nil}
				p := analysis.MetricPoint{SchemaVersion: analysis.SchemaVersion, ProjectID: project, Source: "dynatrace", TimestampUTC: start, MetricName: m.Name, Kind: analysis.KindGauge, Value: value, Unit: unit, Dimensions: dimensions, Quality: quality, Provider: meta}
				points = append(points, p)
			}
		}
	}
	return points, truncated, nil
}
func validDimensions(m map[string]string) bool {
	if len(m) > 16 {
		return false
	}
	for k, v := range m {
		if k == "" || len(k) > 128 || len(v) > 128 || strings.ContainsAny(k+v, "\r\n\x00") {
			return false
		}
	}
	return dimensionLabelsSafe(m)
}

// Identity labels must not be redacted in place: doing so would merge distinct
// credential-bearing tuples into the same canonical head. Reject them before
// any public or durable projection instead, preserving benign labels exactly.
func dimensionLabelsSafe(dimensions map[string]string) bool {
	for key, value := range dimensions {
		if scrub.SensitiveName(key) || scrub.ValueLabel(key, scrub.Policy{}) != key || scrub.ValueLabel(value, scrub.Policy{}) != value {
			return false
		}
		// Custom Grail primitive dimensions use canonical JSON literals. Inspect
		// their decoded string too, where quote escaping can hide an assignment
		// from the plain-text credential matcher.
		var decoded string
		if json.Unmarshal([]byte(value), &decoded) == nil && scrub.ValueLabel(decoded, scrub.Policy{}) != decoded {
			return false
		}
	}
	return true
}
