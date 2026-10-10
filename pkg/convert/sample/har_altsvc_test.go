package sample

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/diag"
)

func harEntry(url, version string, responseHeaders ...map[string]string) map[string]any {
	headers := []any{}
	for _, header := range responseHeaders {
		for name, value := range header {
			headers = append(headers, map[string]any{"name": name, "value": value})
		}
	}
	return map[string]any{
		"request":  map[string]any{"method": "GET", "url": url, "httpVersion": version, "headers": []any{}},
		"response": map[string]any{"status": 200, "httpVersion": version, "headers": headers, "content": map[string]any{"text": "{}", "mimeType": "application/json"}},
	}
}

func TestHARReportsOriginsAdvertisingHTTP3(t *testing.T) {
	entries := []any{
		harEntry("https://app.example.test/", "http/2.0", map[string]string{"Alt-Svc": `h3=":443"; ma=86400`}),
		harEntry("https://app.example.test/next", "h3"),
		// A static asset is skipped as an action, but its response still
		// describes what the CDN origin offers.
		harEntry("https://cdn.example.test/app.js", "http/2.0", map[string]string{"Alt-Svc": `h3=":443"`, "Content-Type": "application/javascript"}),
		harEntry("https://legacy.example.test/", "http/2.0", map[string]string{"Alt-Svc": `h3-29=":443"`}),
		harEntry("http://plain.example.test/", "HTTP/1.1", map[string]string{"Alt-Svc": `h3=":443"`}),
		harEntry("https://app.example.test/again", "http/2.0", map[string]string{"alt-svc": `h3=":443"`}),
	}
	data, err := json.Marshal(map[string]any{"log": map[string]any{"entries": entries}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert(ImportRequest{Format: "har", Content: string(data)})
	if err != nil {
		t.Fatal(err)
	}
	var origins []string
	details := map[string]string{}
	for _, d := range result.Unsupported {
		if d.Code != OriginAdvertisesH3Code {
			continue
		}
		if d.Severity != diag.Info {
			t.Fatalf("advertisement finding severity = %s, want info", d.Severity)
		}
		origins = append(origins, d.Location)
		details[d.Location] = d.Detail
	}
	// app.example.test has an h3 entry; the CDN only advertised.
	if !strings.Contains(details["https://app.example.test"], "captured requests used it") || !strings.Contains(details["https://cdn.example.test"], "no captured request used it") {
		t.Fatalf("details = %v", details)
	}
	want := []string{"https://app.example.test", "https://cdn.example.test"}
	if len(origins) != len(want) || origins[0] != want[0] || origins[1] != want[1] {
		t.Fatalf("origins = %v, want %v", origins, want)
	}
	// The observation never changes what the requests replay with.
	for _, action := range result.Script.Actions {
		if action.HTTPVersion.IsPresent() && action.HTTPVersion.Value() == "3" {
			t.Fatalf("advertisement produced an HTTP/3 policy: %+v", action)
		}
		if action.Metadata["execution_blocker"] != nil {
			t.Fatalf("advertisement gated execution: %+v", action.Metadata)
		}
	}
}

func TestCollectorAddCodedDedupesByCode(t *testing.T) {
	var c diag.Collector
	c.AddCoded(diag.Info, "a", "Element", "loc", "detail")
	c.AddCoded(diag.Info, "a", "Element", "loc", "detail")
	c.AddCoded(diag.Info, "b", "Element", "loc", "detail")
	c.Add(diag.Info, "Element", "loc", "detail")
	items := c.Items()
	if len(items) != 3 || items[0].Code != "a" || items[1].Code != "b" || items[2].Code != "" {
		t.Fatalf("items = %+v", items)
	}
}
