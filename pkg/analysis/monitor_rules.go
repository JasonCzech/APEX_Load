package analysis

import (
	"errors"
	"maps"
	"math"
	"sort"
	"time"
)

// MonitorRule is a terminal guardrail. It deliberately has no runtime stop flag.
type MonitorRule struct {
	SourceID           string            `json:"source_id"`
	Metric             string            `json:"metric"`
	Dimensions         map[string]string `json:"dimensions,omitempty"`
	Unit               string            `json:"unit"`
	Statistic          string            `json:"statistic"`
	Comparator         string            `json:"comparator"`
	Threshold          float64           `json:"threshold"`
	Severity           string            `json:"severity,omitempty"`
	MinCoveragePct     float64           `json:"min_coverage_pct,omitempty"`
	StartOffsetSeconds int               `json:"start_offset_seconds,omitempty"`
	EndOffsetSeconds   *int              `json:"end_offset_seconds,omitempty"`
}
type MonitorRuleResult struct {
	Rule            MonitorRule `json:"rule"`
	Status          string      `json:"status"`
	Observed        *float64    `json:"observed,omitempty"`
	CoveragePct     float64     `json:"coverage_pct"`
	EligibleBuckets int         `json:"eligible_buckets"`
	Message         string      `json:"message"`
}

func (r MonitorRule) Validate() error {
	if r.SourceID == "" || r.Metric == "" || r.Unit == "" || len(r.SourceID) > 256 || len(r.Metric) > 256 || len(r.Unit) > 64 || math.IsNaN(r.Threshold) || math.IsInf(r.Threshold, 0) || math.IsNaN(r.MinCoveragePct) || r.MinCoveragePct < 0 || r.MinCoveragePct > 100 || len(r.Dimensions) > 16 || r.StartOffsetSeconds < 0 || r.EndOffsetSeconds != nil && *r.EndOffsetSeconds <= r.StartOffsetSeconds {
		return errors.New("invalid monitoring rule identity, threshold or window")
	}
	for k, v := range r.Dimensions {
		if k == "" || len(k) > 128 || len(v) > 128 {
			return errors.New("invalid monitoring rule dimensions")
		}
	}
	switch r.Statistic {
	case "avg", "min", "max", "sum":
	default:
		return errors.New("unsupported monitor statistic")
	}
	switch r.Comparator {
	case "lt", "lte", "gt", "gte", "eq", "neq":
	default:
		return errors.New("unsupported monitor comparator")
	}
	if r.Severity != "" && r.Severity != "fail" && r.Severity != "warning" {
		return errors.New("unsupported monitoring rule severity")
	}
	return nil
}
func EvaluateMonitorRule(rule MonitorRule, points []MetricPoint, start, end time.Time) MonitorRuleResult {
	result := MonitorRuleResult{Rule: rule, Status: "not_evaluated", Message: "insufficient settled monitoring evidence"}
	if rule.Validate() != nil {
		return result
	}
	from := start.Add(time.Duration(rule.StartOffsetSeconds) * time.Second)
	until := end
	if rule.EndOffsetSeconds != nil {
		until = start.Add(time.Duration(*rule.EndOffsetSeconds) * time.Second)
		if until.After(end) {
			until = end
		}
	}
	if !until.After(from) {
		return result
	}
	queryFrom := from.Truncate(time.Second)
	if queryFrom.Before(from) {
		queryFrom = queryFrom.Add(time.Second)
	}
	queryUntil := until.Truncate(time.Second)
	var eligible []MetricPoint
	for _, p := range points {
		if p.Source != rule.SourceID || p.MetricName != rule.Metric || p.Unit != rule.Unit || p.Value == nil || p.Quality != QualityValid || p.Provider == nil || !p.Provider.Settled || !p.Provider.AggregationEligible || p.Provider.BucketStart.Before(from) || p.Provider.BucketEnd.After(until) || p.Validate() != nil {
			continue
		}
		if len(rule.Dimensions) > 0 && !maps.Equal(rule.Dimensions, p.Dimensions) {
			continue
		}
		// Unbucketed Analytics aggregates describe their entire query window.
		// Earlier overlapping live queries cannot be combined or substituted
		// for the terminal evaluation window.
		if p.Provider.QueryWindow && (!p.Provider.BucketStart.Equal(queryFrom) || !p.Provider.BucketEnd.Equal(queryUntil)) {
			continue
		}
		eligible = append(eligible, p)
	}
	if len(eligible) == 0 {
		return result
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].TimestampUTC.Before(eligible[j].TimestampUTC) })
	identity := eligible[0]
	previousEnd := time.Time{}
	var covered, numerator, weight, sum float64
	min, max := math.Inf(1), math.Inf(-1)
	resolution := time.Duration(identity.Provider.ResolutionSeconds) * time.Second
	windowFrom := from.Truncate(resolution)
	if windowFrom.Before(from) {
		windowFrom = windowFrom.Add(resolution)
	}
	windowEnd := until.Truncate(resolution)
	if identity.Provider.QueryWindow {
		windowFrom, windowEnd = from, until
	}
	if !windowEnd.After(windowFrom) {
		return result
	}
	for _, p := range eligible {
		if !maps.Equal(identity.Dimensions, p.Dimensions) || identity.Provider.MetricID != p.Provider.MetricID || p.Provider.ResolutionSeconds != identity.Provider.ResolutionSeconds {
			result.Message = "rule must identify one consistent source series"
			return result
		}
		if !previousEnd.IsZero() && p.Provider.BucketStart.Before(previousEnd) {
			result.Message = "overlapping monitoring buckets"
			return result
		}
		previousEnd = p.Provider.BucketEnd
		value := *p.Value
		covered += p.Provider.BucketEnd.Sub(p.Provider.BucketStart).Seconds()
		min = math.Min(min, value)
		max = math.Max(max, value)
		if p.Provider.Statistic == "average" {
			if p.Provider.Count == nil || *p.Provider.Count <= 0 || p.Provider.Sum == nil {
				result.Message = "source average has no valid observation weights"
				return result
			}
			numerator += *p.Provider.Sum
			weight += *p.Provider.Count
		} else {
			seconds := p.Provider.BucketEnd.Sub(p.Provider.BucketStart).Seconds()
			numerator += value * seconds
			weight += seconds
		}
		sum += value
	}
	result.CoveragePct = math.Min(100, 100*covered/windowEnd.Sub(windowFrom).Seconds())
	result.EligibleBuckets = len(eligible)
	required := rule.MinCoveragePct
	if required == 0 {
		required = 95
	}
	if result.CoveragePct < required {
		return result
	}
	var observed float64
	switch rule.Statistic {
	case "avg":
		observed = numerator / weight
	case "min":
		observed = min
	case "max":
		observed = max
	case "sum":
		if identity.Provider.Statistic != "sum" {
			result.Message = "sum requires an interval count or sum metric"
			return result
		}
		observed = sum
	}
	if math.IsNaN(observed) || math.IsInf(observed, 0) {
		return result
	}
	result.Observed = &observed
	pass := false
	switch rule.Comparator {
	case "lt":
		pass = observed < rule.Threshold
	case "lte":
		pass = observed <= rule.Threshold
	case "gt":
		pass = observed > rule.Threshold
	case "gte":
		pass = observed >= rule.Threshold
	case "eq":
		pass = observed == rule.Threshold
	case "neq":
		pass = observed != rule.Threshold
	}
	result.Status = "pass"
	result.Message = "monitoring criterion met"
	if !pass {
		result.Status = "fail"
		if rule.Severity == "warning" {
			result.Status = "warning"
		}
		result.Message = "monitoring criterion breached"
	}
	return result
}
