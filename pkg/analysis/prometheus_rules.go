package analysis

import (
	"maps"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

// evaluatePrometheusRule keeps evaluation-grid statistics separate from source
// buckets and transaction statistics. Only exact-window totals permit sum.
func evaluatePrometheusRule(rule MonitorRule, points []MetricPoint, start, end time.Time) MonitorRuleResult {
	r := MonitorRuleResult{Rule: rule, Status: "not_evaluated", Message: "insufficient settled monitoring evidence"}
	if rule.Validate() != nil {
		return r
	}
	from := start.Add(time.Duration(rule.StartOffsetSeconds) * time.Second)
	to := end
	if rule.EndOffsetSeconds != nil {
		candidate := start.Add(time.Duration(*rule.EndOffsetSeconds) * time.Second)
		if candidate.Before(to) {
			to = candidate
		}
	}
	if !to.After(from) {
		return r
	}
	mode := rule.EvaluationMode
	if mode == "" {
		mode = "series_points"
	}
	var candidates []MetricPoint
	var identity *MetricPoint
	azureDimensions, azureDimensionsValid := foldAzureRuleDimensions(rule.Dimensions)
	for _, p := range points {
		m := p.Provider
		if p.Source != rule.SourceID || p.MetricName != rule.Metric || p.Unit != rule.Unit || m == nil || m.Name != monitorprovider.ProviderPrometheus || rule.QueryID != "" && m.QueryID != rule.QueryID {
			continue
		}
		if len(rule.Dimensions) > 0 {
			dimensions := rule.Dimensions
			if m.BackendProfile == "azure_managed" {
				if !azureDimensionsValid {
					r.Message = "Azure rule dimensions contain conflicting case-insensitive identities"
					return r
				}
				dimensions = azureDimensions
			}
			if !maps.Equal(dimensions, p.Dimensions) {
				continue
			}
		}
		if mode == "exact_window" {
			if m.Mode != "query_window" || !m.BucketStart.Equal(from) || !m.BucketEnd.Equal(to) {
				continue
			}
		} else if m.Mode != "evaluation_point" || p.TimestampUTC.Before(from) || p.TimestampUTC.After(to) {
			continue
		}
		if identity == nil {
			copy := p
			identity = &copy
		} else if !maps.Equal(identity.Dimensions, p.Dimensions) || identity.Provider.QueryID != m.QueryID || identity.Provider.DefinitionHash != m.DefinitionHash || identity.Provider.BackendProfile != m.BackendProfile || identity.Provider.EvaluationStepSeconds != m.EvaluationStepSeconds || identity.Provider.Statistic != m.Statistic || identity.Provider.EntityIDs["series"] != m.EntityIDs["series"] {
			r.Message = "rule must identify one consistent source series"
			return r
		}
		if p.Validate() != nil || p.Quality != QualityValid || p.Value == nil || !m.Settled || !m.AggregationEligible || rule.RequireFreshness && !m.FreshnessKnown {
			continue
		}
		candidates = append(candidates, p)
	}
	if identity == nil || len(candidates) == 0 {
		return r
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].TimestampUTC.Before(candidates[j].TimestampUTC) })
	for i := 1; i < len(candidates); i++ {
		if candidates[i].TimestampUTC.Equal(candidates[i-1].TimestampUTC) {
			r.Message = "duplicate canonical evaluation"
			return r
		}
	}
	expected := int64(1)
	if mode == "series_points" {
		step := time.Duration(identity.Provider.EvaluationStepSeconds) * time.Second
		anchor := start.UTC().Truncate(time.Millisecond)
		if anchor.Before(start) {
			anchor = anchor.Add(time.Millisecond)
		}
		first := anchor
		if first.Before(from) {
			n := from.Sub(anchor) / step
			first = anchor.Add(n * step)
			if first.Before(from) {
				first = first.Add(step)
			}
		}
		if first.After(to) {
			return r
		}
		expected = int64(to.Sub(first)/step) + 1
		for _, p := range candidates {
			if p.TimestampUTC.Sub(anchor)%step != 0 {
				r.Message = "evaluation differs from frozen run grid"
				return r
			}
		}
	}
	r.CoveragePct = math.Min(100, 100*float64(len(candidates))/float64(expected))
	r.EligibleBuckets = len(candidates)
	minimum := rule.MinCoveragePct
	if minimum == 0 {
		minimum = 95
	}
	if r.CoveragePct < minimum {
		return r
	}
	observed := *candidates[0].Value
	if mode == "exact_window" {
		if len(candidates) != 1 {
			r.Message = "exact window requires one numeric aggregate"
			return r
		}
		if rule.Statistic == "sum" && identity.Provider.Statistic != "sum" && identity.Provider.Statistic != "total" {
			r.Message = "sum requires a declared exact-window total"
			return r
		}
	} else {
		switch rule.Statistic {
		case "avg":
			observed = 0
			for _, p := range candidates {
				observed += *p.Value
			}
			observed /= float64(len(candidates))
		case "min":
			for _, p := range candidates {
				observed = math.Min(observed, *p.Value)
			}
		case "max":
			for _, p := range candidates {
				observed = math.Max(observed, *p.Value)
			}
		default:
			r.Message = "sum cannot aggregate cumulative counters or rolling evaluations"
			return r
		}
		if identity.Kind == KindCounter || identity.Provider.Statistic == "counter" {
			r.Message = "counter levels require an approved rate or exact-window expression"
			return r
		}
	}
	if math.IsNaN(observed) || math.IsInf(observed, 0) {
		return r
	}
	r.Observed = &observed
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
	r.Status = "pass"
	r.Message = "monitoring criterion met"
	if !pass {
		r.Status = "fail"
		if rule.Severity == "warning" {
			r.Status = "warning"
		}
		r.Message = "monitoring criterion breached"
	}
	return r
}

// Azure's label names and values are case-insensitive. Keep ordinary
// Prometheus selectors case-sensitive and preserve the original rule for
// provenance; only the Azure matching copy is folded.
func foldAzureRuleDimensions(dimensions map[string]string) (map[string]string, bool) {
	if len(dimensions) == 0 {
		return nil, true
	}
	folded := make(map[string]string, len(dimensions))
	for key, value := range dimensions {
		key, value = strings.ToLower(key), strings.ToLower(value)
		if previous, exists := folded[key]; exists && previous != value {
			return nil, false
		}
		folded[key] = value
	}
	return folded, true
}
