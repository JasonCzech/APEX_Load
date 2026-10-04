package storage

import (
	"encoding/json"
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestAnalysisImportOwnedOutputEqualityPreservesOmittedZeroFields(t *testing.T) {
	empty := AnalysisImport{Normalized: []string{}, NormalizedSHA256: map[string]string{}, RejectionArtifacts: []string{}, RejectionSHA256: map[string]string{}, Rejections: []analysis.ParquetRejectionSummary{}, ObsoleteObjects: []string{}}
	body, err := json.Marshal(empty)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded AnalysisImport
	if err := json.Unmarshal(body, &reloaded); err != nil {
		t.Fatal(err)
	}
	if !analysisImportOwnedOutputsEqual(empty, reloaded) || !analysisImportOwnedOutputsEqual(reloaded, empty) {
		t.Fatal("omitted empty collection was treated as changed evidence")
	}
	for _, tc := range []struct {
		name   string
		mutate func(*AnalysisImport)
	}{
		{"normalized", func(in *AnalysisImport) { in.Normalized = []string{"owned.parquet"} }},
		{"normalized-hash", func(in *AnalysisImport) { in.NormalizedSHA256 = map[string]string{"owned.parquet": "hash"} }},
		{"rejection-artifact", func(in *AnalysisImport) { in.RejectionArtifacts = []string{"rejected.parquet"} }},
		{"rejection-hash", func(in *AnalysisImport) { in.RejectionSHA256 = map[string]string{"rejected.parquet": "hash"} }},
		{"summary", func(in *AnalysisImport) { in.Rejections = []analysis.ParquetRejectionSummary{{Count: 1}} }},
		{"obsolete", func(in *AnalysisImport) { in.ObsoleteObjects = []string{"prior.parquet"} }},
		{"accepted", func(in *AnalysisImport) { in.Accepted = 1 }},
		{"rejected", func(in *AnalysisImport) { in.Rejected = 1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := empty
			tc.mutate(&changed)
			if analysisImportOwnedOutputsEqual(empty, changed) || analysisImportOwnedOutputsEqual(changed, empty) {
				t.Fatal("new nonempty evidence or count was admitted")
			}
		})
	}
}

func TestAnalysisImportOwnedOutputEqualityKeepsNonemptyOrderAndNestedRowShape(t *testing.T) {
	a := AnalysisImport{Normalized: []string{"first", "second"}, Rejections: []analysis.ParquetRejectionSummary{{Count: 1, Rows: nil}}}
	b := a
	b.Normalized = []string{"second", "first"}
	if analysisImportOwnedOutputsEqual(a, b) {
		t.Fatal("nonempty output order changed")
	}
	b = a
	b.Rejections = []analysis.ParquetRejectionSummary{{Count: 1, Rows: []int64{}}}
	if analysisImportOwnedOutputsEqual(a, b) {
		t.Fatal("non-omitted nested rows shape silently changed")
	}
}
