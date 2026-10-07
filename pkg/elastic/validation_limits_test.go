package elastic

import (
	"fmt"
	"testing"
)

func TestSelectionMappedMetricTotalIncludesAdvancedQueries(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		native, advanced, records int
		valid                     bool
	}{
		{"twenty_native", 20, 0, 0, true},
		{"twenty_one_native", 21, 0, 0, false},
		{"twenty_mixed", 19, 1, 0, true},
		{"twenty_one_mixed", 20, 1, 0, false},
		{"twenty_with_five_advanced", 15, 5, 0, true},
		{"twenty_one_with_five_advanced", 16, 5, 0, false},
		{"records_do_not_count", 20, 0, 5, true},
		{"mixed_records_do_not_count", 19, 1, 4, true},
		{"advanced_definition_limit_independent", 14, 6, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testSelection()
			s.Logs = false
			for i := 0; i < tc.native; i++ {
				s.Metrics = append(s.Metrics, MetricSelection{Name: fmt.Sprintf("native_%d", i), Statistic: "count"})
			}
			for i := 0; i < tc.advanced; i++ {
				s.Queries = append(s.Queries, Query{Name: fmt.Sprintf("advanced_%d", i), Language: "dsl", Kind: "metric", Statistic: "count"})
			}
			for i := 0; i < tc.records; i++ {
				s.Queries = append(s.Queries, Query{Name: fmt.Sprintf("record_%d", i), Language: "dsl", Kind: "log"})
			}
			if err := s.Validate(); (err == nil) != tc.valid {
				t.Fatalf("native=%d advanced=%d records=%d: %v", tc.native, tc.advanced, tc.records, err)
			}
		})
	}
}
