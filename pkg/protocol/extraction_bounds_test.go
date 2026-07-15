package protocol

import (
	"fmt"
	"strings"
	"testing"
)

func TestStoreExtractionEnforcesValueAndAggregateBudgets(t *testing.T) {
	result := &Result{}
	if err := StoreExtraction(result, "token", "ok"); err != nil {
		t.Fatal(err)
	}
	if err := StoreExtraction(result, "oversized", strings.Repeat("x", MaxExtractionValueBytes+1)); err == nil {
		t.Fatal("oversized extraction was accepted")
	}
	for index := 0; ; index++ {
		err := StoreExtraction(result, fmt.Sprintf("value-%d", index), strings.Repeat("v", MaxExtractionValueBytes))
		if err != nil {
			break
		}
	}
	if err := ValidateExtractions(result.Extractions); err != nil {
		t.Fatalf("retained extraction set is invalid: %v", err)
	}
}
