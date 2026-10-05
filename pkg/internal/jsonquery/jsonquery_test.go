package jsonquery

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

const storeDocument = `{"Id":"a1k4M000004XZFrQAO","Name":"Store & Café <1>","Rank":1.50,"Tags":["a","b"],"Owner":{"Id":"005A","Name":"Pat"},"Queues":[{"Id":"q1"},{"Id":"q2"}],"Empty":{},"Null":null,"Open":true}`

func selectStrings(t *testing.T, document any, expr string, limit int) []string {
	t.Helper()
	path, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	matches, err := path.Select(document, limit)
	if err != nil {
		t.Fatalf("Select(%q): %v", expr, err)
	}
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		text, err := ValueString(match)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, text)
	}
	return out
}

func TestSelectFollowsLoadRunnerQueries(t *testing.T) {
	document, err := Decode([]byte(storeDocument))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		expr string
		want string
	}{
		{"$.Id", "a1k4M000004XZFrQAO"},
		{"$.Name", "Store & Café <1>"},
		{"$['Name']", "Store & Café <1>"},
		{`$["Owner"]["Name"]`, "Pat"},
		{"$.Rank", "1.50"}, // number spelling is preserved
		{"$.Open", "true"},
		{"$.Null", "null"},
		{"$.Tags", `["a","b"]`},
		{"$.Owner", `{"Id":"005A","Name":"Pat"}`},
		{"$.Empty", `{}`},
		{"$.Tags[1]", "b"},
		{"$.Tags[-1]", "b"},
		{"$.Queues[*].Id", "q1|q2"},
		{"$.Queues.*.Id", "q1|q2"},
		{"$..Id", "a1k4M000004XZFrQAO|005A|q1|q2"}, // document order
		{"$..[0]", "a|{\"Id\":\"q1\"}"},
		{"$.Missing", ""},
		{"$.Tags[9]", ""},
	} {
		if got := strings.Join(selectStrings(t, document, tt.expr, 0), "|"); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.expr, got, tt.want)
		}
	}
	if got := selectStrings(t, document, "$..Id", 1); len(got) != 1 || got[0] != "a1k4M000004XZFrQAO" {
		t.Errorf("first match = %v", got)
	}
}

func TestParseRejectsUnsupportedAndMalformedQueries(t *testing.T) {
	for _, expr := range []string{"$.a[0:2]", "$.a[0,1]", "$['a','b']", "$.a[(@.length-1)]", "$.a[?(@.b.length() > 1)]", "$.a[?(@..b)]", "$.a[?(1 == 1)]", "$.a[?(@.b == 'x')"} {
		if _, err := Parse(expr); !errors.Is(err, ErrUnsupported) {
			t.Errorf("Parse(%q) error = %v, want ErrUnsupported", expr, err)
		}
	}
	for _, expr := range []string{"Id", "$.", "$.a[", "$['a"} {
		if _, err := Parse(expr); err == nil || errors.Is(err, ErrUnsupported) {
			t.Errorf("Parse(%q) error = %v, want a syntax error", expr, err)
		}
	}
}

func TestEncodeRoundTripsOrderAndText(t *testing.T) {
	document, err := Decode([]byte(storeDocument))
	if err != nil {
		t.Fatal(err)
	}
	compact, err := Encode(document, "")
	if err != nil {
		t.Fatal(err)
	}
	if compact != storeDocument {
		t.Fatalf("compact = %s\nwant      %s", compact, storeDocument)
	}
	indented, err := Encode(Object{{Key: "a", Value: []any{"x"}}, {Key: "b", Value: Object{}}}, "  ")
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"a\": [\n    \"x\"\n  ],\n  \"b\": {}\n}"; indented != want {
		t.Fatalf("indented = %q, want %q", indented, want)
	}
}

func TestDecodeRejectsInvalidAndOversizedInput(t *testing.T) {
	for _, input := range []string{"", "{", `{"a":1} {"b":2}`, `{"a":}`, strings.Repeat("[", MaxDepth+2) + strings.Repeat("]", MaxDepth+2)} {
		if _, err := Decode([]byte(input)); err == nil {
			t.Errorf("Decode(%.20q) succeeded", input)
		}
	}
	if _, err := Decode(make([]byte, MaxDocumentBytes+1)); err == nil {
		t.Error("oversized document accepted")
	}
}

// Recursive descent over a large document (far beyond the old 40,000-node
// candidate cap) still finds its matches.
func TestRecursiveDescentScalesToLargeDocuments(t *testing.T) {
	var body strings.Builder
	body.WriteString(`{"records":[`)
	for index := 0; index < 60000; index++ {
		if index > 0 {
			body.WriteString(",")
		}
		body.WriteString(`{"attributes":{"type":"Account"},"Name":"n"}`)
	}
	body.WriteString(`],"Id":"last"}`)
	document, err := Decode([]byte(body.String()))
	if err != nil {
		t.Fatal(err)
	}
	if got := selectStrings(t, document, "$..Id", 1); len(got) != 1 || got[0] != "last" {
		t.Fatalf("$..Id = %v", got)
	}
}

// A first-match query over more matches than MaxMatches returns the first
// instead of failing, and a large intermediate array is not a result set.
func TestFirstMatchIgnoresTheResultCeiling(t *testing.T) {
	var body strings.Builder
	body.WriteString(`{"records":[`)
	for index := 0; index < MaxMatches+500; index++ {
		if index > 0 {
			body.WriteString(",")
		}
		fmt.Fprintf(&body, `{"Id":"r%d"}`, index)
	}
	body.WriteString(`]}`)
	document, err := Decode([]byte(body.String()))
	if err != nil {
		t.Fatal(err)
	}
	for _, expr := range []string{"$..Id", "$.records[*].Id"} {
		if got := selectStrings(t, document, expr, 1); len(got) != 1 || got[0] != "r0" {
			t.Errorf("%s first match = %v", expr, got)
		}
	}
	path, err := Parse("$..Id")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := path.Select(document, 0); err == nil {
		t.Error("select all over more than MaxMatches truncated silently instead of failing")
	}
}

const accountsDocument = `{"customers":[{"accounts":[
	{"plan":"mobile","number":"M-100","billingId":"B-1","active":true,"balance":12.5},
	{"plan":"fiber","number":"F-200","billingId":"B-2","active":false,"balance":0},
	{"plan":"mobile","number":"M-300","billingId":"B-3","active":true,"balance":99,"note":null},
	{"number":"X-400","balance":"n/a"}]}]}`

// Recorded scripts select array elements with JSONPath filters; LoadRunner
// returns the first match for SelectAll=No.
func TestFilterSelectsMatchingElements(t *testing.T) {
	document, err := Decode([]byte(accountsDocument))
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct{ expr, want string }{
		{"$.customers[0].accounts[?(@.plan=='mobile')].number", "M-100|M-300"},
		{"$.customers[0].accounts[?(@.plan=='fiber')].billingId", "B-2"},
		{`$.customers[0].accounts[?(@.plan == "mobile")].number`, "M-100|M-300"},
		{"$.customers[0].accounts[?(@.plan != 'mobile')].number", "F-200"},
		{"$..accounts[?(@.balance > 10)].number", "M-100|M-300"},
		{"$..accounts[?(@.balance <= 12.5 && @.active == true)].number", "M-100"},
		{"$..accounts[?(@.active == false || @.balance >= 99)].number", "F-200|M-300"},
		{"$..accounts[?(!(@.active == true))].number", "F-200|X-400"}, // negation includes elements without the field
		{"$..accounts[?(@.note)].number", "M-300"},
		{"$..accounts[?(@.note == null)].number", "M-300"},
		{"$..accounts[?(@['plan'] == 'fiber')].number", "F-200"},
		{"$..accounts[?(@.missing == 'x')].number", ""},
		{"$..accounts[?(@.balance == 'n/a')].number", "X-400"},
	} {
		if got := strings.Join(selectStrings(t, document, tt.expr, 0), "|"); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.expr, got, tt.want)
		}
	}
	if got := selectStrings(t, document, "$.customers[0].accounts[?(@.plan=='mobile')].number", 1); len(got) != 1 || got[0] != "M-100" {
		t.Errorf("first mobile account = %v", got)
	}
}
