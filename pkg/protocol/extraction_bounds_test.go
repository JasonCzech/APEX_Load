package protocol

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestApplyExtractionOnMissContract(t *testing.T) {
	miss := MarkExtractionMiss(errors.New("not found"))
	tests := []struct {
		name      string
		onMiss    string
		wantValue bool
		wantErr   bool
	}{
		{name: "ignore", onMiss: "ignore"},
		{name: "default", onMiss: ""},
		{name: "empty", onMiss: "empty", wantValue: true},
		{name: "fail", onMiss: "fail", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := &Result{Extractions: map[string]string{}}
			err := ApplyExtraction(result, Extractor{Name: "token", OnMiss: test.onMiss}, "", miss)
			if (err != nil) != test.wantErr {
				t.Fatalf("error = %v, wantErr %v", err, test.wantErr)
			}
			_, exists := result.Extractions["token"]
			if exists != test.wantValue {
				t.Fatalf("value exists = %v, want %v", exists, test.wantValue)
			}
		})
	}
}

func TestApplyExtractionNeverSuppressesInvalidExtractor(t *testing.T) {
	for _, onMiss := range []string{"", "ignore", "empty", "fail"} {
		t.Run(onMiss, func(t *testing.T) {
			result := &Result{}
			err := ApplyExtraction(
				result,
				Extractor{Name: "token", OnMiss: onMiss},
				"",
				errors.New("invalid regex"),
			)
			if err == nil || !strings.Contains(err.Error(), "invalid regex") {
				t.Fatalf("error = %v, want invalid extractor failure", err)
			}
			if len(result.Extractions) != 0 {
				t.Fatalf("invalid extractor published values: %#v", result.Extractions)
			}
		})
	}
}

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
