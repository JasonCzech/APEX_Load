package browser

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

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
		{name: "about blank", baseURL: "https://app.example/root/", target: "about:blank", want: "about:blank"},
		{name: "browser error page", baseURL: "https://app.example/root/", target: "chrome-error://chromewebdata/", want: "chrome-error://chromewebdata/"},
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

// A recording that ended on the browser's error page kept it as a step; replay
// refuses it instead of loading http://chrome-error://... or a path under the
// base URL.
func TestBrowserNavigateRefusesBrowserInternalPage(t *testing.T) {
	b := &Browser{baseURL: "https://app.example"}
	err := b.navigate(context.Background(), &protocol.Action{Type: "browser_navigate"}, &protocol.Result{}, b.resolveURL("chrome-error://chromewebdata/"))
	if err == nil || !strings.Contains(err.Error(), "draws itself") {
		t.Fatalf("navigate() error = %v, want the browser-internal page refused", err)
	}
}

func TestDOMSummaryFromPageDataBoundsUntrustedPageText(t *testing.T) {
	visible := make([]any, browserDOMVisibleEntryLimit+100)
	for index := range visible {
		visible[index] = strings.Repeat("界", browserDOMVisibleLineLimitBytes)
	}
	pageURL, pageTitle, summary, err := domSummaryFromPageData(map[string]any{
		"url":        strings.Repeat("u", browserDOMURLLimitBytes+100),
		"title":      strings.Repeat("t", browserDOMTitleLimitBytes+100),
		"readyState": strings.Repeat("r", browserDOMReadyStateLimitBytes+100),
		"visible":    visible,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(pageURL) > browserDOMURLLimitBytes || len(pageTitle) > browserDOMTitleLimitBytes || len(summary.ReadyState) > browserDOMReadyStateLimitBytes {
		t.Fatalf("unbounded DOM metadata: url=%d title=%d ready=%d", len(pageURL), len(pageTitle), len(summary.ReadyState))
	}
	if len(summary.VisibleText) > browserDOMVisibleEntryLimit {
		t.Fatalf("visible entries = %d, want <= %d", len(summary.VisibleText), browserDOMVisibleEntryLimit)
	}
	total := 0
	for _, line := range summary.VisibleText {
		total += len(line)
		if len(line) > browserDOMVisibleLineLimitBytes {
			t.Fatalf("visible line = %d bytes, want <= %d", len(line), browserDOMVisibleLineLimitBytes)
		}
	}
	if total > browserDOMVisibleAggregateLimitByte {
		t.Fatalf("visible text = %d bytes, want <= %d", total, browserDOMVisibleAggregateLimitByte)
	}
}

func TestBrowserScreenshotAcquisitionBounds(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height float64
		wantErr       bool
	}{
		{name: "normal", width: 1920, height: 1080},
		{name: "invalid", width: 0, height: 1080, wantErr: true},
		{name: "dimension", width: browserScreenshotDimensionLimit + 1, height: 1, wantErr: true},
		{name: "area", width: 8192, height: 8192, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateBrowserScreenshotDimensions(tc.width, tc.height)
			if (err != nil) != tc.wantErr {
				t.Fatalf("validateBrowserScreenshotDimensions error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
	if encoded, err := encodeBrowserScreenshot(make([]byte, browserScreenshotLimitBytes+1), nil); err == nil || encoded != "" {
		t.Fatalf("oversized screenshot encoded=%d bytes error=%v", len(encoded), err)
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
