package paramize

import (
	"errors"
	"regexp"
	"testing"
)

func TestSuggestionScanBudgetIsSharedAcrossLiteralAndRegexScans(t *testing.T) {
	for _, indexes := range []bool{false, true} {
		budget := &suggestionRegexBudget{}
		if err := budget.chargeSearchBytes(int(maxSuggestionRegexSearchBytes) - 1); err != nil {
			t.Fatal(err)
		}
		pattern := regexp.MustCompile("x")
		var err error
		if indexes {
			_, err = budget.findAllSubmatchIndexes(pattern, "x")
		} else {
			_, err = budget.findAllSubmatches(pattern, "x")
		}
		if err != nil || budget.matches != 1 {
			t.Fatalf("exact scan boundary: indexes=%t matches=%d err=%v", indexes, budget.matches, err)
		}
		if err := budget.chargeSearchBytes(0); err != nil {
			t.Fatalf("empty scan at boundary: %v", err)
		}
		if err := budget.chargeSearchBytes(1); !errors.Is(err, ErrSuggestionWorkLimit) || budget.searchBytes != maxSuggestionRegexSearchBytes {
			t.Fatalf("excess scan: bytes=%d err=%v", budget.searchBytes, err)
		}
	}
}
