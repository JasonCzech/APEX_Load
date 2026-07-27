package dsl

import (
	"reflect"
	"testing"
)

func TestHeaderListFromMapUsesDeterministicNameOrder(t *testing.T) {
	t.Parallel()
	input := map[string]string{
		"z-last":  "z",
		"a-lower": "a",
		"A-Lower": "A",
	}
	want := HeaderList{
		{Name: "A-Lower", Value: "A"},
		{Name: "a-lower", Value: "a"},
		{Name: "z-last", Value: "z"},
	}
	for iteration := 0; iteration < 25; iteration++ {
		if got := HeaderListFromMap(input); !reflect.DeepEqual(got, want) {
			t.Fatalf("iteration %d: HeaderListFromMap() = %#v, want %#v", iteration, got, want)
		}
	}
}

func TestHeaderListToMapUsesCaseInsensitiveLastOccurrence(t *testing.T) {
	t.Parallel()
	input := HeaderList{
		{Name: "X-Trace", Value: "first"},
		{Name: "Accept", Value: "application/json"},
		{Name: "x-trace", Value: "second"},
		{Name: "X-Trace", Value: "last"},
	}
	want := map[string]string{
		"Accept":  "application/json",
		"X-Trace": "last",
	}
	if got := input.ToMap(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ToMap() = %#v, want %#v", got, want)
	}
}
