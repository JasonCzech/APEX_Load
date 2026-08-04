package protocol

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestApplyExtractionOnMissContract(t *testing.T) {
	miss := errors.New("not found")
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
