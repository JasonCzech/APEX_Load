package analysis

import "slices"

// NativeQueryProfile advertises executable combinations, not future joint
// capture. Each profile names a single population, mode and grouping. Metric
// aggregations are intersected with AllowedAggregations. Required evidence must
// still be captured and qualified for the selected run before execution.
type NativeQueryProfile struct {
	ID                  string   `json:"id"`
	Population          string   `json:"population"`
	EvidenceMode        string   `json:"evidence_mode"`
	LiveBucketMode      string   `json:"live_bucket_mode,omitempty"`
	Grouping            string   `json:"grouping,omitempty"`
	AllowedFilters      []string `json:"allowed_filters"`
	RequiredFilters     []string `json:"required_filters,omitempty"`
	PropertyMode        string   `json:"property_mode"` // disabled, optional_exact, required_category
	AllowedAggregations []string `json:"allowed_aggregations"`
	RequiredEvidence    []string `json:"required_evidence"`
	MaxSeries           int      `json:"max_series"`
	MaxBuckets          int      `json:"max_buckets"`
	MaxWindowSeconds    int      `json:"max_window_seconds"`
	MaxBucketSeconds    int      `json:"max_bucket_seconds"`
}

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
	return profiles
}
