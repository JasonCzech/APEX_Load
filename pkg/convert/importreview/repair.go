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

// RemoveDependencyCodes removes the dependency review codes
// (missing_producer:<name> and conditional_producer:<name>) from every step.
// RefreshDependencies stamps them again from the script.
func RemoveDependencyCodes(s *dsl.Script) error {
	return dslwalk.Walk(s, func(ref dslwalk.SurfaceRef) error {
		a := dsl.Action{Metadata: ref.Metadata()}
		RemoveActionCodes(&a, IsDependencyCode)
		ref.SetMetadata(a.Metadata)
		return nil
	})
}

// IsDependencyCode reports whether code is a dependency review code
// (missing_producer:<name> or conditional_producer:<name>), which
// RefreshDependencies removes and stamps again from the script.
func IsDependencyCode(code string) bool {
	return strings.HasPrefix(code, "missing_producer:") || strings.HasPrefix(code, "conditional_producer:")
}

func RefreshDependencies(s *dsl.Script) ([]Finding, error) {
	if err := RemoveDependencyCodes(s); err != nil {
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
