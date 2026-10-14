package sample

import (
	"strings"
	"testing"
)

// A page's object URLs (blob:) and inline data never reach the network, and
// a page's own JSON in a query string ({d:1,p:{cn:2}}) holds a literal "}}"
// the script would read as a template: a recorded Tableau Public viz blocked
// replay on both.
func TestConvertHARKeepsRecordedURLsReplayable(t *testing.T) {
	entry := func(method, url string) string {
		return `{"startedDateTime":"2026-01-01T00:00:00Z","time":5,"request":{"method":"` + method + `","url":"` + url + `","headers":[]},` +
			`"response":{"status":200,"headers":[{"name":"Content-Type","value":"application/json"}],"content":{"mimeType":"application/json","text":"{}"}}}`
	}
	content := `{"log":{"entries":[` + strings.Join([]string{
		entry("GET", "https://viz.example.test/views/home"),
		entry("GET", "blob:https://viz.example.test/6a1f0c2e-1111-4222-8333-944455556666"),
		entry("GET", "data:application/json;base64,e30="),
		entry("GET", "file:///Users/tester/report.json"),
		entry("GET", "https://viz.example.test/inst?v=1&wp={d:EXEREM,p:{cn:notify}}"),
		entry("GET", "https://viz.example.test/inst?v=1&init={id:42}"),
	}, ",") + `]}}`
	result, err := Convert(ImportRequest{Name: "viz", Format: "har", Content: content, Options: ConvertOptions{AsyncScan: new(bool)}})
	if err != nil {
		t.Fatal(err)
	}
	var targets []string
	for _, action := range result.Script.Actions {
		targets = append(targets, action.Target)
		if blockers := action.Metadata["execution_blockers"]; blockers != nil {
			t.Errorf("%s is blocked: %v", action.Target, blockers)
		}
	}
	want := []string{"/views/home", "/inst?v=1&wp=%7Bd:EXEREM,p:%7Bcn:notify%7D%7D", "/inst?v=1&init={id:42}"}
	if strings.Join(targets, " ") != strings.Join(want, " ") {
		t.Fatalf("targets = %q, want %q", targets, want)
	}
	if result.Stats.SkippedRequests != 3 {
		t.Fatalf("skipped = %d, want the blob:, data: and file: entries", result.Stats.SkippedRequests)
	}
	// One note each, naming the entries, however many there are.
	notes := map[string]string{}
	for _, item := range result.Unsupported {
		if _, seen := notes[item.Element]; seen && (item.Element == "HAR local URLs" || item.Element == "HAR URL braces") {
			t.Fatalf("%s reported more than once", item.Element)
		}
		notes[item.Element] = item.Location + ": " + item.Detail
	}
	if got := notes["HAR local URLs"]; !strings.Contains(got, "HAR entries 2, 3, 4:") || !strings.Contains(got, "blob: 1, data: 1, file: 1") {
		t.Fatalf("local URL note = %q", got)
	}
	if got := notes["HAR URL braces"]; !strings.HasPrefix(got, "HAR entry 5:") {
		t.Fatalf("brace note = %q", got)
	}
}

func TestHAREntryListShortensLongLists(t *testing.T) {
	var entries []int
	for entry := 1; entry <= 13; entry++ {
		entries = append(entries, entry)
	}
	if got := harEntryList(entries); got != "HAR entries 1, 2, 3, 4, 5, 6, 7, 8, 9, 10 and 3 more" {
		t.Fatalf("list = %q", got)
	}
	if got := harEntryList([]int{7}); got != "HAR entry 7" {
		t.Fatalf("single = %q", got)
	}
}
