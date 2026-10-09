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
	// Joint describes the joint evidence v4 tuples in
	// Dimensions[NativeJointDimension].
	Joint *NativeJointEvidence `json:"joint,omitempty"`
	// JointPublications are the joint evidence (only) of the source batches
	// accepted since the previous published point: a distributed run publishes
	// at most once a second, so most batches are stored without being
	// published, and their joint tuples would otherwise be lost.
	JointPublications []*NativePublicationEvidence `json:"joint_publications,omitempty"`
}

// NativeJointDimension holds joint evidence v4 tuples, keyed by the JSON
// array of their values for NativeJointEvidence.Dimensions.
const NativeJointDimension = "joint_v4"

// NativeJointEvidence describes one interval's or publication's joint
// evidence v4 (B5): the projection it was counted under, whether the source
// measured it, the samples past the tuple bound (Other) and how many distinct
// tuples were omitted. An unavailable source has no tuples.
type NativeJointEvidence struct {
	Population   string   `json:"population"`
	Dimensions   []string `json:"dimensions"`
	Availability string   `json:"availability"`
	Reason       string   `json:"reason,omitempty"`
	// Other aggregates the samples of tuples past the bound.
	Other         *NativeIntervalMetric `json:"other,omitempty"`
	OmittedTuples int64                 `json:"omitted_tuples,omitempty"`
	Incomplete    bool                  `json:"incomplete,omitempty"`
	// UnavailableSources counts fleet sources without joint evidence in a
	// merged live interval.
	UnavailableSources int `json:"unavailable_sources,omitempty"`
}

// Clone copies a joint evidence description.
func (j *NativeJointEvidence) Clone() *NativeJointEvidence {
	if j == nil {
		return nil
	}
	out := *j
	out.Dimensions = append([]string(nil), j.Dimensions...)
	if j.Other != nil {
		other := *j.Other
		other.DurationDigest = engine.CloneTDigestSnapshot(j.Other.DurationDigest)
		out.Other = &other
	}
	return &out
}

// Publication preserves one accepted generator batch so retaining the latest
// fleet summary cannot cause the same source window to be counted repeatedly.
type NativePublicationEvidence struct {
	SourceID string `json:"source_id"`
	// SourceEpoch is the generator process the batch came from: sequences
	// restart with each epoch.
	SourceEpoch         string                                     `json:"source_epoch,omitempty"`
	Sequence            uint64                                     `json:"sequence"`
	ExecutionGeneration uint64                                     `json:"execution_generation"`
	Timestamp           time.Time                                  `json:"timestamp"`
	Version             int                                        `json:"version"`
	WindowSeconds       float64                                    `json:"window_seconds"`
	Dimensions          map[string]map[string]NativeIntervalMetric `json:"dimensions"`
	Incomplete          bool                                       `json:"incomplete,omitempty"`
	ExpectedSources     []string                                   `json:"expected_sources"`
	Joint               *NativeJointEvidence                       `json:"joint,omitempty"`
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
	clonePublication := func(in *NativePublicationEvidence) *NativePublicationEvidence {
		p := *in
		p.ExpectedSources = append([]string(nil), p.ExpectedSources...)
		p.Dimensions = CloneNativeIntervalEvidence(&NativeIntervalEvidence{Dimensions: p.Dimensions}).Dimensions
		p.Joint = in.Joint.Clone()
		return &p
	}
	if in.Publication != nil {
		out.Publication = clonePublication(in.Publication)
	}
	if in.JointPublications != nil {
		out.JointPublications = make([]*NativePublicationEvidence, 0, len(in.JointPublications))
		for _, p := range in.JointPublications {
			if p != nil {
				out.JointPublications = append(out.JointPublications, clonePublication(p))
			}
		}
	}
	out.Joint = in.Joint.Clone()
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
	if q.JointEvidence {
		return validateNativeJointQuery(q)
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
		if o.DatasetArtifactSHA256 != "" && (o.DatasetID == "" || !lowerSHA256Hex(o.DatasetArtifactSHA256)) {
			return errors.New("dataset_artifact_sha256 pins a dataset overlay and is a lowercase SHA-256")
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

// validateNativeJointQuery checks a joint evidence v4 query: merged live
// buckets of one population, grouped by up to three projected dimensions and
// filtered on projected dimensions. Whether the run projected them is known
// only from its evidence, which the executor checks.
func validateNativeJointQuery(q AnalyticsQuery) error {
	if q.EvidenceMode != "live" || q.LiveBucketMode != "merged" {
		return errors.New("joint evidence queries read merged live buckets")
	}
	if q.PropertyName != "" || q.PropertyValue != "" || q.OverlaySelection != nil || q.Derivation != nil {
		return errors.New("joint evidence queries select projected dimensions only")
	}
	if q.MaxSeries < 1 || q.MaxSeries > 20 || len(q.Filters) > 16 {
		return errors.New("joint evidence queries return 1–20 series with at most 16 filters")
	}
	// The projection rules bound the grouping (at most three dimensions).
	if len(q.Dimensions) > 0 {
		if err := (engine.JointProjection{Population: q.Population, Dimensions: q.Dimensions}).Validate(); err != nil {
			return err
		}
	} else if q.Population != "action" && q.Population != "transaction" {
		return errors.New("evidence queries select the action or transaction population")
	}
	for key, value := range q.Filters {
		if err := (engine.JointProjection{Population: q.Population, Dimensions: []string{key}}).Validate(); err != nil {
			return errors.New("joint evidence filters name projected dimensions")
		}
		if len(value) > 256 {
			return errors.New("joint evidence filter value exceeds 256 bytes")
		}
	}
	plain := q
	plain.EvidenceMode, plain.LiveBucketMode, plain.JointEvidence = "", "", false
	plain.Dimensions, plain.Filters, plain.MaxSeries = nil, nil, 1
	return ValidateNativeArchiveQuery(plain)
}
