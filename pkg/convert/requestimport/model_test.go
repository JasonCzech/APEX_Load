package requestimport

import (
	"reflect"
	"strings"
	"testing"
)

func TestHeaderListSummaryUsesCaseInsensitiveLastOccurrence(t *testing.T) {
	t.Parallel()
	headers := HeaderList{
		{Name: " X-Trace ", Value: "first"},
		{Name: "Accept", Value: "application/json"},
		{Name: "x-trace", Value: "second"},
		{Name: "X-Trace", Value: "third"},
		{Name: " ", Value: "ignored"},
	}
	got := headers.Summary()
	want := map[string]string{
		"Accept":  "application/json",
		"X-Trace": "third",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Summary() = %#v, want %#v", got, want)
	}
	if headers[0].Name != " X-Trace " {
		t.Fatal("Summary mutated the authoritative header list")
	}
	got["Accept"] = "mutated"
	if headers[1].Value != "application/json" {
		t.Fatal("Summary returned storage shared with the header list")
	}
}

func TestHeaderListHasIsCaseInsensitiveAndTrimmed(t *testing.T) {
	t.Parallel()
	headers := HeaderList{{Name: " Content-Type ", Value: "text/plain"}}
	if !headers.Has("content-type") || !headers.Has(" CONTENT-TYPE ") {
		t.Fatal("Has did not recognize a case-insensitive trimmed name")
	}
	if headers.Has("Accept") || HeaderList(nil).Has("anything") {
		t.Fatal("Has reported a missing header")
	}
}

func TestHeaderListFromMapIsDeterministic(t *testing.T) {
	t.Parallel()
	headers := map[string]string{
		"z-last":  "z",
		"a-lower": "a",
		"A-Lower": "A",
		"M-Mid":   "m",
	}
	want := HeaderList{
		{Name: "A-Lower", Value: "A"},
		{Name: "a-lower", Value: "a"},
		{Name: "M-Mid", Value: "m"},
		{Name: "z-last", Value: "z"},
	}
	for range 20 {
		if got := HeaderListFromMap(headers); !reflect.DeepEqual(got, want) {
			t.Fatalf("HeaderListFromMap() = %#v, want %#v", got, want)
		}
	}
	if HeaderListFromMap(nil) != nil {
		t.Fatal("nil map should produce a nil header list")
	}
}

func TestHeaderListFromMapReportsUnavailableSourceOrder(t *testing.T) {
	t.Parallel()
	headers, diagnostic := HeaderListFromMapWithDiagnostic(map[string]string{
		"Z-Last":  "z",
		"A-First": "a",
	}, "fixture.json#/headers")
	if got := []string{headers[0].Name, headers[1].Name}; !reflect.DeepEqual(got, []string{"A-First", "Z-Last"}) {
		t.Fatalf("headers = %#v", headers)
	}
	if diagnostic.Severity != "info" ||
		!strings.Contains(diagnostic.Detail, "source_order_unavailable") ||
		diagnostic.Location != "fixture.json#/headers" {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
	if headers, diagnostic := HeaderListFromMapWithDiagnostic(nil, "unused"); headers != nil || diagnostic.Detail != "" {
		t.Fatalf("empty map = %#v, %#v", headers, diagnostic)
	}
}
