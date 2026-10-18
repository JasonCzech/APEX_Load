package diag

import (
	"reflect"
	"strings"
	"testing"
)

func TestGroupRepeatedPreservesCountsBlockersAndSourceIdentity(t *testing.T) {
	input := Diagnostics{
		{Code: "timing", Element: "Timing", Detail: "inferred", Severity: Info, Location: "entry 1"},
		{Code: "timing", Element: "Timing", Detail: "inferred", Severity: Info, Location: "entry 2"},
		{Code: "timing", Element: "Timing", Detail: "inferred", Severity: Info, Location: "entry 3"},
		{Code: "timing", Element: "Timing", Detail: "inferred", Severity: Info, Location: "entry 3"},
		{Code: "timing", Element: "Timing", Detail: "inferred", Severity: Info, SourceRef: "another source", Location: "entry 1"},
		{Code: "missing", Element: "Body", Severity: Blocker, Location: "entry 1"},
		{Code: "missing", Element: "Body", Severity: Blocker, Location: "entry 2"},
	}
	got := input.GroupRepeated(2)
	if len(got) != 4 || got[0].Count != 4 || !got[0].LocationsTruncated || !reflect.DeepEqual(got[0].Locations, []string{"entry 1", "entry 2"}) {
		t.Fatalf("unexpected groups: %+v", got)
	}
	if !strings.Contains(got[0].String(), "4 occurrences") || got[2].Location != "entry 1" || got[3].Location != "entry 2" {
		t.Fatalf("lost diagnostic detail: %+v", got)
	}
	got[0].Locations[0] = "changed"
	if input[0].Location != "entry 1" || input[0].Count != 0 {
		t.Fatal("mutated input")
	}
	if !reflect.DeepEqual(got, got.GroupRepeated(2)) {
		t.Fatal("grouping is not idempotent")
	}
}

func TestGroupRepeatedEmptyAndBoundary(t *testing.T) {
	if Diagnostics(nil).GroupRepeated(3) != nil {
		t.Fatal("empty groups")
	}
	input := Diagnostics{{Element: "note", Severity: Warning, Location: "A"}, {Element: "note", Severity: Warning, Location: "B"}}
	got := input.GroupRepeated(0)
	if len(got) != 1 || len(got[0].Locations) != 1 || !got[0].LocationsTruncated {
		t.Fatalf("%+v", got)
	}
}
