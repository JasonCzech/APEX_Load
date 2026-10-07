package storage

import (
	"errors"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/engine"
)

// NativeIntervalEvidence retains original collection-interval summaries. Version 3
// adds bounded measured action/network/hierarchy/lifecycle tuples; version 2
// also retains mergeable distributions; scalar percentiles are never averaged.
type NativeIntervalEvidence struct {
	Publication   *NativePublicationEvidence                 `json:"publication,omitempty"`
	Version       int                                        `json:"version"`
	WindowSeconds float64                                    `json:"window_seconds"`
	Dimensions    map[string]map[string]NativeIntervalMetric `json:"dimensions"`
	Incomplete    bool                                       `json:"incomplete,omitempty"`
}

// Publication preserves one accepted generator batch so retaining the latest
// fleet summary cannot cause the same source window to be counted repeatedly.
type NativePublicationEvidence struct {
	SourceID            string                                     `json:"source_id"`
	Sequence            uint64                                     `json:"sequence"`
	ExecutionGeneration uint64                                     `json:"execution_generation"`
	Timestamp           time.Time                                  `json:"timestamp"`
	Version             int                                        `json:"version"`
	WindowSeconds       float64                                    `json:"window_seconds"`
	Dimensions          map[string]map[string]NativeIntervalMetric `json:"dimensions"`
	Incomplete          bool                                       `json:"incomplete,omitempty"`
	ExpectedSources     []string                                   `json:"expected_sources"`
}
type NativeIntervalMetric struct {
	DurationDigest *engine.TDigestSnapshot `json:"duration_digest,omitempty"`
	Count          int64                   `json:"count"`
	Errors         int64                   `json:"errors"`
	Avg            float64                 `json:"avg"`
	P50            float64                 `json:"p50"`
	P90            float64                 `json:"p90"`
	P95            float64                 `json:"p95"`
	P99            float64                 `json:"p99"`
}

func CloneNativeIntervalEvidence(in *NativeIntervalEvidence) *NativeIntervalEvidence {
	if in == nil {
		return nil
	}
	out := *in
	if in.Publication != nil {
		p := *in.Publication
		p.ExpectedSources = append([]string(nil), p.ExpectedSources...)
		p.Dimensions = CloneNativeIntervalEvidence(&NativeIntervalEvidence{Dimensions: p.Dimensions}).Dimensions
		out.Publication = &p
	}
	out.Dimensions = make(map[string]map[string]NativeIntervalMetric, len(in.Dimensions))
	for dimension, series := range in.Dimensions {
		values := make(map[string]NativeIntervalMetric, len(series))
		for name, value := range series {
			value.DurationDigest = engine.CloneTDigestSnapshot(value.DurationDigest)
			values[name] = value
		}
		out.Dimensions[dimension] = values
	}
	return &out
}

// ValidateNativeEvidenceQuery describes the combinations supported by retained
// evidence. Independent live marginals cannot answer intersection queries.
func ValidateNativeEvidenceQuery(q AnalyticsQuery) error {
	if q.EvidenceMode != "live" && q.EvidenceMode != "archive" {
		return errors.New("choose live or archive evidence")
	}
	if q.ComparisonRunID != "" || q.Overlay {
		return errors.New("use the saved archive comparison workflow for run comparisons")
	}
	if len(q.Dimensions) > 1 || q.MaxSeries < 1 || q.MaxSeries > 20 {
		return errors.New("choose at most one grouping and 1–20 series")
	}
	// The evidence executors read action and transaction observations only;
	// message and browser document populations belong to archive panels.
	if q.Population != "action" && q.Population != "transaction" {
		return errors.New("evidence queries select the action or transaction population")
	}
	group := ""
	if len(q.Dimensions) > 0 {
		group = q.Dimensions[0]
	}
	switch group {
	case "", "action", "property", "region", "dependency", "network_profile", "hierarchy", "phase":
	default:
		return errors.New("unsupported grouping")
	}
	if group == "property" && (q.PropertyName == "" || q.Population != "action") {
		return errors.New("property grouping requires an action population and property name")
	}
	plain := q
	plain.EvidenceMode = ""
	plain.LiveBucketMode = ""
	plain.OverlaySelection = nil
	plain.Dimensions = nil
	plain.MaxSeries = 1
	if group == "property" && plain.PropertyValue == "" {
		plain.PropertyValue = "validation"
	}
	if err := ValidateNativeArchiveQuery(plain); err != nil {
		return err
	}
	if q.LiveBucketMode != "" && (q.EvidenceMode != "live" || q.LiveBucketMode != "source" && q.LiveBucketMode != "merged") {
		return errors.New("invalid live bucket mode")
	}
	// Per-source live intervals are not one bucket grid, so a trailing
	// window of buckets has no meaning there.
	if q.Derivation != nil && q.EvidenceMode == "live" && q.LiveBucketMode != "merged" {
		if name := analysis.CanonicalDerivation(q.Derivation.Operation); name == "moving_mean" || name == "moving_stddev" {
			return errors.New("moving derivations need merged live buckets or archive evidence")
		}
	}
	if q.EvidenceMode == "live" {
		jointDimensions := NativeActionDimensionsRequired(q)
		if jointDimensions && (q.Population != "action" || q.LiveBucketMode != "merged" || q.PropertyName != "" || (group != "" && group != "action" && group != "network_profile" && group != "hierarchy" && group != "phase")) {
			return errors.New("live dimension intersections require merged action evidence v3; property intersections are separate")
		}
		for key := range q.Filters {
			if key != "action" && key != "network_profile" && key != "hierarchy" && key != "phase" {
				return errors.New("live dimension filters require joint evidence that is unavailable")
			}
		}
		if group == "network_profile" && !jointDimensions && (q.Population != "action" || q.Filters["action"] != "" || q.PropertyName != "") {
			return errors.New("live network profiles describe independent action observations")
		}

		if q.LiveBucketMode != "merged" && (q.Aggregation == "min" || q.Aggregation == "max") {
			return errors.New("live evidence does not retain min/max")
		}
		if group == "region" && q.Population != "transaction" || group == "dependency" && q.Population != "action" {
			return errors.New("live regions describe business transactions; dependencies describe actions")
		}
		if (group == "region" || group == "dependency") && (q.Filters["action"] != "" || q.PropertyName != "") || q.LiveBucketMode != "merged" && (q.PropertyName != "" && q.Filters["action"] != "" || group == "action" && q.PropertyName != "") {
			return errors.New("live evidence does not retain intersections of independent dimensions")
		}
		if group == "" && q.Filters["action"] == "" && q.PropertyName == "" {
			return errors.New("live selection requires an exact identity, property category, or grouping")
		}
	}
	if o := q.OverlaySelection; o != nil {
		if (o.DatasetID == "") == (o.SourceID == "") || len(o.DatasetID) > 128 || len(o.SourceID) > 128 || o.Metric == "" || len(o.Metric) > 256 || len(o.Host) > 256 || len(o.Dimensions) > 16 {
			return errors.New("overlay requires one bounded dataset or monitor source")
		}
		if o.DatasetID != "" && (o.Host != "" || len(o.Dimensions) > 0) {
			return errors.New("imported overlay filters are not available; select a normalized metric")
		}
		for k, v := range o.Dimensions {
			if k == "" || len(k) > 128 || len(v) > 128 {
				return errors.New("invalid overlay dimensions")
			}
		}
		switch o.Aggregation {
		case "avg", "min", "max", "sum", "count", "rate", "p50", "p95", "p99":
		default:
			return errors.New("unsupported overlay aggregation")
		}
		if o.MaxLagSeconds < 0 || o.MaxLagSeconds > 3600 || o.MaxLagSeconds%q.BucketSeconds != 0 || 2*o.MaxLagSeconds/q.BucketSeconds+1 > 121 {
			return errors.New("invalid bounded lag window")
		}
	}
	return nil
}

// NativeActionDimensionsRequired selects a retained measured intersection.
func NativeActionDimensionsRequired(q AnalyticsQuery) bool {
	group := ""
	if len(q.Dimensions) > 0 {
		group = q.Dimensions[0]
	}
	return group == "hierarchy" || group == "phase" || q.Filters["network_profile"] != "" || q.Filters["hierarchy"] != "" || q.Filters["phase"] != "" || group == "network_profile" && q.Filters["action"] != ""
}
