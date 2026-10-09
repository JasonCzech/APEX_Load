package analysis

import "slices"

// NativeQueryProfile advertises executable combinations, not future joint
// capture. Each profile names a single population, mode and grouping. Metric
// aggregations are intersected with AllowedAggregations. Required evidence must
// still be captured and qualified for the selected run before execution.
type NativeQueryProfile struct {
	ID              string   `json:"id"`
	Population      string   `json:"population"`
	EvidenceMode    string   `json:"evidence_mode"`
	LiveBucketMode  string   `json:"live_bucket_mode,omitempty"`
	Grouping        string   `json:"grouping,omitempty"`
	AllowedFilters  []string `json:"allowed_filters"`
	RequiredFilters []string `json:"required_filters,omitempty"`
	// PropertyMode is disabled, optional_exact, required_category, or (joint
	// evidence v4) projected_category: one property:<category> the run's
	// projection names may be grouped by or filtered on like a dimension.
	PropertyMode        string   `json:"property_mode"`
	AllowedAggregations []string `json:"allowed_aggregations"`
	RequiredEvidence    []string `json:"required_evidence"`
	MaxSeries           int      `json:"max_series"`
	MaxBuckets          int      `json:"max_buckets"`
	MaxWindowSeconds    int      `json:"max_window_seconds"`
	MaxBucketSeconds    int      `json:"max_bucket_seconds"`
	// JointEvidence profiles read a run's joint evidence v4 (B5): they group
	// by up to MaxGroupingDimensions of the run's projection, chosen from
	// GroupingDimensions or one property:<category>, and filter on projected
	// dimensions.
	JointEvidence         bool     `json:"joint_evidence,omitempty"`
	GroupingDimensions    []string `json:"grouping_dimensions,omitempty"`
	MaxGroupingDimensions int      `json:"max_grouping_dimensions,omitempty"`
}

// JointEvidenceDimensions are the dimensions a joint evidence v4
// projection may name besides one property:<category> (PropertyMode
// projected_category); they match the engine's.
var JointEvidenceDimensions = []string{"name", "protocol", "group", "region", "dependency", "page", "hierarchy", "phase", "network_profile", "browser_profile", "browser_engine", "browser_platform"}

func nativeQueryProfiles() []NativeQueryProfile {
	allAggregations := []string{"avg", "min", "max", "count", "rate", "p50", "p90", "p95", "p99"}
	filters := []string{"action", "network_profile", "hierarchy", "phase"}
	profiles := []NativeQueryProfile{}
	add := func(population, mode, buckets, grouping, property string, allowed, required []string, evidence string) {
		idMode := mode
		if mode == "" {
			idMode = "archive_selection"
		}
		idGroup := grouping
		if grouping == "" {
			idGroup = "exact"
		}
		id := idMode + "/" + buckets + "/" + population + "/" + idGroup
		aggregations := slices.Clone(allAggregations)
		if mode == "live" && buckets == "source" {
			aggregations = []string{"avg", "count", "rate", "p50", "p90", "p95", "p99"}
		}
		series := 20
		if mode == "" || grouping == "" {
			series = 1
		}
		profiles = append(profiles, NativeQueryProfile{ID: id, Population: population, EvidenceMode: mode, LiveBucketMode: buckets, Grouping: grouping, AllowedFilters: append([]string{}, allowed...), RequiredFilters: slices.Clone(required), PropertyMode: property, AllowedAggregations: aggregations, RequiredEvidence: []string{evidence}, MaxSeries: series, MaxBuckets: 1000, MaxWindowSeconds: 86400, MaxBucketSeconds: 3600})
	}
	for _, population := range []string{PopulationAction, PopulationTransaction} {
		property := "disabled"
		if population == PopulationAction {
			property = "optional_exact"
		}
		add(population, "", "", "", property, filters, nil, "qualified_raw_observations.v1")
		for _, group := range []string{"action", "region", "dependency", "network_profile", "hierarchy", "phase"} {
			add(population, "archive", "", group, property, filters, nil, "qualified_raw_observations.v1")
		}
		if population == PopulationAction {
			add(population, "archive", "", "property", "required_category", filters, nil, "qualified_raw_observations.v1")
		}
		for _, buckets := range []string{"source", "merged"} {
			evidence := "native_evidence.v1"
			if buckets == "merged" {
				evidence = "native_evidence.v2"
			}
			add(population, "live", buckets, "", "disabled", []string{"action"}, []string{"action"}, evidence)
			groupProperty := "disabled"
			if population == PopulationAction && buckets == "merged" {
				groupProperty = "optional_exact"
			}
			add(population, "live", buckets, "action", groupProperty, []string{"action"}, nil, evidence)
			if population == PopulationTransaction {
				add(population, "live", buckets, "region", "disabled", nil, nil, evidence)
			}
			if population == PopulationAction {
				for _, group := range []string{"dependency", "network_profile"} {
					add(population, "live", buckets, group, "disabled", nil, nil, evidence)
				}
				add(population, "live", buckets, "property", "required_category", nil, nil, evidence)
			}
		}
	}
	for _, group := range []string{"", "action", "network_profile", "hierarchy", "phase"} {
		required := []string(nil)
		if group == "" {
			required = []string{"action"}
		}
		before := len(profiles)
		add(PopulationAction, "live", "merged", group, "disabled", filters, required, "native_joint_evidence.v3")
		profiles[before].ID += "/joint_v3"
	}
	// Joint evidence v4: up to three dimensions of the run's projection.
	for _, population := range []string{PopulationAction, PopulationTransaction} {
		before := len(profiles)
		add(population, "live", "merged", "projected", "projected_category", JointEvidenceDimensions, nil, "native_joint_evidence.v4")
		profile := &profiles[before]
		profile.ID += "/joint_v4"
		profile.JointEvidence, profile.GroupingDimensions, profile.MaxGroupingDimensions = true, slices.Clone(JointEvidenceDimensions), 3
	}
	// One archive selection of one browser document measurement tuple.
	before := len(profiles)
	add(PopulationBrowserDocument, "", "", "", "disabled", BrowserDocumentFilters(), nil, "browser_document_evidence.v2_final_qualified_distribution")
	profiles[before].AllowedAggregations = []string{"p50", "p75", "p90", "p95", "p99"}
	return profiles
}
