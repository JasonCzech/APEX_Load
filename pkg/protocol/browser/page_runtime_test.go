package browser

import "testing"

func TestBrowserResolveURL(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		target  string
		want    string
	}{
		{name: "absolute http", baseURL: "https://app.example", target: "http://target.example/path", want: "http://target.example/path"},
		{name: "absolute https", baseURL: "https://app.example", target: "https://target.example/path", want: "https://target.example/path"},
		{name: "empty base", target: "/orders", want: "/orders"},
		{name: "relative target", baseURL: "https://app.example/root/", target: "orders", want: "https://app.example/root/orders"},
		{name: "leading slash target", baseURL: "https://app.example/root/", target: "/orders", want: "https://app.example/root/orders"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := &Browser{baseURL: tt.baseURL}
			if got := b.resolveURL(tt.target); got != tt.want {
				t.Fatalf("resolveURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDOMSummaryFromPageData(t *testing.T) {
	pageURL, pageTitle, summary, err := domSummaryFromPageData(map[string]any{
		"url":        "https://example.test",
		"title":      "Example",
		"readyState": "complete",
		"visible":    []any{"first", 12, "second"},
	})
	if err != nil {
		t.Fatalf("domSummaryFromPageData returned error: %v", err)
	}
	if pageURL != "https://example.test" || pageTitle != "Example" {
		t.Fatalf("page url/title = %q/%q, want example values", pageURL, pageTitle)
	}
	if summary == nil {
		t.Fatal("summary = nil")
	}
	if summary.Title != "Example" || summary.ReadyState != "complete" {
		t.Fatalf("summary = %#v, want title/ready state copied", summary)
	}
	if len(summary.VisibleText) != 2 || summary.VisibleText[0] != "first" || summary.VisibleText[1] != "second" {
		t.Fatalf("VisibleText = %#v, want string-only visible lines", summary.VisibleText)
	}
}
