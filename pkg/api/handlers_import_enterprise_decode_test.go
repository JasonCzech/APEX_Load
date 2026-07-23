package api

import (
	"reflect"
	"testing"
)

func TestSourceReviewValuePreservesCompletenessEvidence(t *testing.T) {
	review := sourceReviewValue(map[string]any{
		"total_source_files":   float64(4),
		"omitted_source_files": []any{"Support.java", "Helper.java"},
	})
	if review == nil {
		t.Fatal("sourceReviewValue returned nil for completeness-only review evidence")
	}
	if review.TotalSourceFiles != 4 {
		t.Fatalf("TotalSourceFiles = %d, want 4", review.TotalSourceFiles)
	}
	wantOmitted := []string{"Helper.java", "Support.java"}
	if !reflect.DeepEqual(review.OmittedSourceFiles, wantOmitted) {
		t.Fatalf("OmittedSourceFiles = %#v, want %#v", review.OmittedSourceFiles, wantOmitted)
	}
}
