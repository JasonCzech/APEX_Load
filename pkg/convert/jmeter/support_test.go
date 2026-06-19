package jmeter

import "testing"

func TestGetSupportMatrixEntriesStructure(t *testing.T) {
	entries := GetSupportMatrixEntries()
	if len(entries) == 0 {
		t.Fatal("expected structured support matrix entries")
	}

	validStatus := map[string]bool{statusSupported: true, statusPartial: true, statusReviewOnly: true}
	validExec := map[string]bool{execExecutable: true, execReviewRequired: true, execAdapterRequired: true, execNotExecutable: true}

	seenIDs := map[string]bool{}
	var sawSupported, sawReviewOnly bool
	for _, entry := range entries {
		if entry.ID == "" || entry.Construct == "" || entry.Area == "" {
			t.Fatalf("entry missing id/construct/area: %#v", entry)
		}
		if seenIDs[entry.ID] {
			t.Fatalf("duplicate support matrix id: %s", entry.ID)
		}
		seenIDs[entry.ID] = true
		if !validStatus[entry.Status] {
			t.Fatalf("entry %s has invalid status %q", entry.ID, entry.Status)
		}
		if !validExec[entry.ExecutionStatus] {
			t.Fatalf("entry %s has invalid execution_status %q", entry.ID, entry.ExecutionStatus)
		}
		switch entry.Status {
		case statusSupported:
			sawSupported = true
		case statusReviewOnly:
			sawReviewOnly = true
		}
	}
	if !sawSupported || !sawReviewOnly {
		t.Fatalf("expected both supported and review_only entries, got supported=%v review_only=%v", sawSupported, sawReviewOnly)
	}
}

func TestGetSupportMatrixDerivesFlatArrays(t *testing.T) {
	flat := GetSupportMatrix()
	if len(flat.Supported) == 0 {
		t.Fatal("expected derived supported entries")
	}
	if len(flat.Unsupported) == 0 {
		t.Fatal("expected derived unsupported entries")
	}

	// Every flat entry must trace back to a structured entry in the right bucket.
	bucket := map[string]string{}
	for _, entry := range GetSupportMatrixEntries() {
		switch entry.Status {
		case statusSupported:
			bucket[entry.Construct] = "supported"
		case statusPartial:
			bucket[entry.Construct] = "partial"
		default:
			bucket[entry.Construct] = "unsupported"
		}
	}
	for _, construct := range flat.Supported {
		if bucket[construct] != "supported" {
			t.Fatalf("supported flat entry %q not derived from a supported structured entry", construct)
		}
	}
	for _, construct := range flat.Partial {
		if bucket[construct] != "partial" {
			t.Fatalf("partial flat entry %q not derived from a partial structured entry", construct)
		}
	}
	for _, construct := range flat.Unsupported {
		if bucket[construct] != "unsupported" {
			t.Fatalf("unsupported flat entry %q not derived from a review/unsupported structured entry", construct)
		}
	}
	if len(GetSupportMatrixNotes()) == 0 {
		t.Fatal("expected support matrix notes")
	}
}

func TestCategoryForSourceMapping(t *testing.T) {
	tests := []struct {
		name       string
		status     string
		targetKind string
		wantCat    string
		wantOK     bool
	}{
		{"converted action is mapped", "converted", "action", categoryMapped, true},
		{"converted controller is mapped", "converted", "controller", categoryMapped, true},
		{"review_only is review_required", "review_only", "controller", categoryReviewRequired, true},
		{"adapter_required passes through", categoryAdapterRequired, "action", categoryAdapterRequired, true},
		{"unsupported counts", "unsupported", "unsupported", categoryUnsupported, true},
		{"contributor is skipped", "converted", "contributor", "", false},
		{"config is skipped", "converted", "config", "", false},
		{"ignored is informational", "ignored", "diagnostic", "", false},
		{"unknown status is informational", "something", "action", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := categoryForSourceMapping(tc.status, tc.targetKind)
			if ok != tc.wantOK || got != tc.wantCat {
				t.Fatalf("categoryForSourceMapping(%q,%q) = (%q,%v), want (%q,%v)", tc.status, tc.targetKind, got, ok, tc.wantCat, tc.wantOK)
			}
		})
	}
}
