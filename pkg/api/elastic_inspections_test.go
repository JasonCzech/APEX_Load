package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/collectoragent"
	"github.com/jasonczech/apexload/pkg/elastic"
)

func TestElasticInspectionRequestedResultLimit(t *testing.T) {
	for _, limit := range []int{0, 1, 2, 3} {
		t.Run(string(rune('0'+limit)), func(t *testing.T) {
			result := collectoragent.ElasticInspectionData{
				Indices: make([]elastic.Index, 2), Fields: make([]elastic.Field, 2),
				Preview: &elastic.Result{Evidence: make([]analysis.ProviderEvidence, 2), Points: make([]analysis.MetricPoint, 2)},
			}
			limitElasticInspectionResult(&result, limit)
			want := 2
			if limit > 0 && limit < want {
				want = limit
			}
			if len(result.Indices) != want || len(result.Fields) != want || len(result.Preview.Evidence) != want || len(result.Preview.Points) != want || result.Truncated != (want < 2) {
				t.Fatalf("requested limit %d did not bound every inspection table: %+v", limit, result)
			}
		})
	}
	limitElasticInspectionResult(nil, 1)
	result := collectoragent.ElasticInspectionData{Truncated: true}
	limitElasticInspectionResult(&result, 1)
	if !result.Truncated {
		t.Fatal("requested limit erased existing truncation disclosure")
	}
}
