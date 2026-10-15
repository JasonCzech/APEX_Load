package api

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func skewEvent(records ...map[string]any) TransactionEvent {
	list := make([]any, 0, len(records))
	for _, record := range records {
		list = append(list, record)
	}
	return TransactionEvent{ProtocolEvidence: map[string]any{"browser_version_skew": list}}
}

// The run report counts the sessions that reported each browser version
// mismatch, the driver's first, and skips malformed records.
func TestArchiveBrowserVersionSkewRollup(t *testing.T) {
	rollup := newArchiveBrowserVersionSkewAccumulator()
	driver := map[string]any{"source": "driver", "actual": "152.0.7900.1", "expected": "149.0.7827.55"}
	recording := map[string]any{"source": "recording", "actual": "152.0.7900.1", "expected": "148.0.7000.2"}
	rollup.merge(skewEvent(recording))
	rollup.merge(skewEvent(driver, recording))
	rollup.merge(skewEvent(driver))
	rollup.merge(skewEvent(map[string]any{"source": "other", "actual": "1.0", "expected": "2.0"}, map[string]any{"source": "driver", "actual": "<script>", "expected": "149.0"}))
	rollup.merge(TransactionEvent{})
	got := rollup.result()
	want := []BrowserVersionSkew{
		{Source: "driver", Actual: "152.0.7900.1", Expected: "149.0.7827.55", Sessions: 2},
		{Source: "recording", Actual: "152.0.7900.1", Expected: "148.0.7000.2", Sessions: 2},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("rollup = %+v\nwant %+v", got, want)
	}

	many := newArchiveBrowserVersionSkewAccumulator()
	for i := 0; i <= maxBrowserVersionSkews; i++ {
		many.merge(skewEvent(map[string]any{"source": "driver", "actual": fmt.Sprintf("%d.0", 100+i), "expected": "149.0"}))
	}
	if rows := many.result(); len(rows) != maxBrowserVersionSkews {
		t.Fatalf("a run lists %d mismatches", len(rows))
	}
	if newArchiveBrowserVersionSkewAccumulator().result() != nil {
		t.Fatal("a run without mismatches lists some")
	}
}

// A run with browser version mismatches reports them in HTML, PDF text and
// CSV, even without page metrics.
func TestArchiveReportRendersBrowserVersionSkews(t *testing.T) {
	report := consistencyMatrixReport()
	report.Overview.Browser = nil
	report.BrowserVersionSkews = []BrowserVersionSkew{
		{Source: "driver", Actual: "152.0.7900.1", Expected: "149.0.7827.55", Sessions: 3},
		{Source: "recording", Actual: "152.0.7900.1", Expected: "148.0.7000.2", Sessions: 1},
	}
	html, err := renderRunArchiveHTMLReportWithProfile(report, "engineer")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Browser version mismatches", "The Playwright driver&#39;s tested browser", "The recording&#39;s browser", "149.0.7827.55"} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("the HTML report lacks %q", want)
		}
	}
	var csv bytes.Buffer
	if err := writeRunArchiveReportCSV(report, &csv); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(csv.String(), "browser_version_skew") || !strings.Contains(csv.String(), "launched=152.0.7900.1;expected=149.0.7827.55;sessions=3") {
		t.Fatalf("CSV:\n%s", csv.String())
	}
	lines := []string{}
	data := &runArchiveHTMLReportData{Report: report}
	buildReportSectionRegistry()["browser"].PDF(data, &lines)
	if text := strings.Join(lines, "\n"); !strings.Contains(text, "The Playwright driver's tested browser: launched 152.0.7900.1, expected 149.0.7827.55, 3 session(s)") {
		t.Fatalf("PDF lines:\n%s", text)
	}
}
