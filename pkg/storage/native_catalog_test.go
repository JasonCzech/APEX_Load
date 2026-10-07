package storage

import (
	"github.com/jasonczech/apexload/pkg/analysis"
	"slices"
	"testing"
)

func TestNativeQueryUsesCanonicalMetricCatalog(t *testing.T) {
	for _, metric := range analysis.Catalog().Metrics {
		// Message and byte metrics have no native query profile: received
		// messages are read by the archive message query, bytes by no native
		// query yet.
		if len(metric.QueryProfiles) == 0 {
			q := AnalyticsQuery{Population: metric.Populations[0], Metric: metric.Name, Aggregation: metric.Aggregations[0], EndSeconds: 60, BucketSeconds: 1, MaxSeries: 1}
			if err := ValidateNativeArchiveQuery(q); (metric.Name == "message_count") != (err == nil) {
				t.Fatalf("native archive query for %s: %v", metric.Name, err)
			}
			continue
		}
		for _, population := range metric.Populations {
			for _, aggregation := range metric.Aggregations {
				t.Run(metric.Name+"/"+population+"/"+aggregation, func(t *testing.T) {
					q := AnalyticsQuery{Population: population, Metric: metric.Name, Aggregation: aggregation, EndSeconds: 60, BucketSeconds: 1, MaxSeries: 1}
					if err := ValidateNativeArchiveQuery(q); err != nil {
						t.Fatal(err)
					}
					q.Population = "message"
					if err := ValidateNativeArchiveQuery(q); err == nil {
						t.Fatal("unsupported population accepted")
					}
				})
			}
		}
	}
	for _, metric := range []string{"latency", "tps", "transaction_duration"} {
		q := AnalyticsQuery{Population: "action", Metric: metric, Aggregation: "avg", EndSeconds: 60, BucketSeconds: 1, MaxSeries: 1}
		if err := ValidateNativeArchiveQuery(q); err == nil {
			t.Fatalf("catalog manufactured native support for %s", metric)
		}
	}
}

func TestCatalogQueryProfilesMatchNativeValidators(t *testing.T) {
	for _, metric := range analysis.Catalog().Metrics {
		for _, profile := range analysis.Catalog().QueryProfiles {
			if !slices.Contains(metric.QueryProfiles, profile.ID) {
				continue
			}
			for _, aggregation := range metric.Aggregations {
				if !slices.Contains(profile.AllowedAggregations, aggregation) {
					continue
				}
				for mask := 0; mask < 1<<len(profile.AllowedFilters); mask++ {
					q := AnalyticsQuery{Population: profile.Population, Metric: metric.Name, Aggregation: aggregation, EndSeconds: profile.MaxWindowSeconds, BucketSeconds: profile.MaxBucketSeconds, MaxSeries: profile.MaxSeries, EvidenceMode: profile.EvidenceMode, LiveBucketMode: profile.LiveBucketMode, Filters: map[string]string{}}
					if profile.Grouping != "" {
						q.Dimensions = []string{profile.Grouping}
					}
					for i, filter := range profile.AllowedFilters {
						if mask&(1<<i) != 0 {
							q.Filters[filter] = "selected"
						}
					}
					for _, required := range profile.RequiredFilters {
						q.Filters[required] = "selected"
					}
					if profile.PropertyMode == "required_category" {
						q.PropertyName = "category"
					}
					check := func(q AnalyticsQuery) {
						var err error
						if profile.EvidenceMode == "" {
							err = ValidateNativeArchiveQuery(q)
						} else {
							err = ValidateNativeEvidenceQuery(q)
						}
						if err != nil {
							t.Fatalf("advertised profile %s metric %s aggregation %s filters %v property %s: %v", profile.ID, metric.Name, aggregation, q.Filters, q.PropertyName, err)
						}
					}
					check(q)
					if profile.PropertyMode == "optional_exact" {
						q.PropertyName, q.PropertyValue = "category", "selected"
						check(q)
					}
				}
			}
		}
	}
}

func TestCatalogDoesNotAdvertiseUnavailableNativeDimensions(t *testing.T) {
	for _, profile := range analysis.Catalog().QueryProfiles {
		if profile.Grouping == "protocol" || profile.Grouping == "group" || profile.Population == analysis.PopulationTransaction && profile.PropertyMode != "disabled" {
			t.Fatalf("unsupported native dimension advertised: %+v", profile)
		}
		if profile.EvidenceMode == "live" && (profile.Grouping == "phase" || profile.Grouping == "hierarchy") && (profile.Population != analysis.PopulationAction || profile.LiveBucketMode != "merged" || !slices.Contains(profile.RequiredEvidence, "native_joint_evidence.v3")) {
			t.Fatalf("ungated measured joint advertised: %+v", profile)
		}
	}
}
