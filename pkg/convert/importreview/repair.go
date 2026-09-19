package importreview

import (
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/dslwalk"
	"strings"
)

// RemoveActionCodes removes only requirements whose behavior was repaired.
// It must never be exposed as an acknowledgement operation.
func RemoveActionCodes(a *dsl.Action, remove func(string) bool) {
	if a == nil || a.Metadata == nil {
		return
	}
	m := a.Metadata
	hadReviewCodes := len(dslwalk.MetadataStrings(m, "review_reasons")) > 0 || dslwalk.MetadataString(m, "review_gate") != ""
	for _, pair := range [][2]string{{"execution_blocker", "execution_blockers"}, {"review_gate", "review_reasons"}} {
		codes := append(dslwalk.MetadataStrings(m, pair[1]), dslwalk.MetadataString(m, pair[0]))
		kept := []string{}
		for _, c := range codes {
			if c != "" && !remove(c) {
				kept = append(kept, c)
			}
		}
		delete(m, pair[0])
		delete(m, pair[1])
		kept = uniqueSorted(kept)
		if len(kept) > 0 {
			m[pair[0]] = kept[0]
			m[pair[1]] = kept
		}
	}
	if _, ok := m["review_gate"]; !ok && (hadReviewCodes || remove("import_review_required")) {
		delete(m, "review_required")
		delete(m, "promotion_blocker")
	}
	if review, ok := m["import_review"].(map[string]any); ok {
		kept := []string{}
		for _, c := range dslwalk.MetadataStrings(review, "reason_codes") {
			if !remove(c) {
				kept = append(kept, c)
			}
		}
		if len(kept) == 0 {
			delete(m, "import_review")
		} else {
			review["reason_codes"] = kept
		}
	}
}

func RefreshDependencies(s *dsl.Script) ([]Finding, error) {
	err := dslwalk.Walk(s, func(ref dslwalk.SurfaceRef) error {
		a := dsl.Action{Metadata: ref.Metadata()}
		RemoveActionCodes(&a, func(c string) bool {
			return strings.HasPrefix(c, "missing_producer:") || strings.HasPrefix(c, "conditional_producer:")
		})
		ref.SetMetadata(a.Metadata)
		return nil
	})
	if err != nil {
		return nil, err
	}
	findings, err := AnalyzeDependencies(s)
	if err != nil {
		return nil, err
	}
	if err = ApplyToScript(s, findings); err != nil {
		return nil, err
	}
	return FindingsFromScript(s)
}
