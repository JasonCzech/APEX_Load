package api

import (
	"math"
	"strings"
	"testing"
)

func TestRootCauseBrowserLegacyAvailability(t *testing.T) {
	for _, tc := range []struct {
		name     string
		browser  *BrowserLiveMetrics
		advisory bool
	}{
		{"missing components", &BrowserLiveMetrics{Samples: 3}, false},
		{"below legacy thresholds", &BrowserLiveMetrics{Samples: 3, AvgLCPMs: 100, AvgINPMs: 10, AvgCLSScore: .01}, false},
		{"absent samples", &BrowserLiveMetrics{AvgLCPMs: 4500}, false},
		{"negative samples", &BrowserLiveMetrics{Samples: -1, AvgLCPMs: 4500}, false},
		{"unsafe sample count", &BrowserLiveMetrics{Samples: 1 << 53, AvgLCPMs: 4500}, false},
		{"negative components", &BrowserLiveMetrics{Samples: 3, AvgLCPMs: -1, AvgINPMs: -1, AvgCLSScore: -1}, false},
		{"nonfinite components", &BrowserLiveMetrics{Samples: 3, AvgLCPMs: math.Inf(1), AvgINPMs: math.NaN(), AvgCLSScore: math.Inf(-1)}, false},
		{"exact thresholds", &BrowserLiveMetrics{Samples: 3, AvgLCPMs: 2500, AvgINPMs: 200, AvgCLSScore: .1}, false},
		{"reported LCP violation", &BrowserLiveMetrics{Samples: 3, AvgLCPMs: 4500}, true},
		{"reported INP violation", &BrowserLiveMetrics{Samples: 3, AvgINPMs: 210}, true},
		{"reported CLS violation", &BrowserLiveMetrics{Samples: 3, AvgCLSScore: .2}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := &RunArchiveReport{Manifest: &RunArchiveManifest{}}
			report.Overview.Browser = tc.browser
			summary := buildRunRootCauseSummary("browser-availability", report)
			found := false
			for _, signal := range summary.Signals {
				if signal.Name == "Browser UX" {
					found = true
					if signal.Status != "unscored" || signal.Severity != "unknown" || signal.Score != 0 {
						t.Fatalf("legacy browser health was invented: %#v", signal)
					}
				}
				if strings.Contains(signal.Value, "NaN") || strings.Contains(signal.Value, "Inf") {
					t.Fatalf("invalid component escaped into a finding: %#v", signal)
				}
			}
			if !found || summary.Report["browser_ux_model"] != "legacy-summary-availability-v2" {
				t.Fatal("browser assessment omitted applicability/provenance")
			}
			browserFindings := 0
			for _, finding := range summary.TopFindings {
				if finding.Category == "browser" {
					browserFindings++
					if !strings.Contains(finding.Detail, "not a standards-qualified") || !strings.Contains(finding.Title, "Legacy") {
						t.Fatalf("legacy advisory claimed document qualification: %#v", finding)
					}
				}
			}
			if (browserFindings > 0) != tc.advisory {
				t.Fatalf("browser advisory count %d, expected presence %t", browserFindings, tc.advisory)
			}
			if !tc.advisory && (summary.Status != "unknown" || summary.Confidence != "low" || !strings.Contains(summary.Summary, "unscored")) {
				t.Fatalf("unqualified browser evidence produced overall health: %#v", summary)
			}
			if tc.advisory && summary.Status != "degraded" {
				t.Fatalf("observed legacy threshold warning lost: %#v", summary)
			}
		})
	}
}

func TestRootCauseBrowserUnqualifiedPreservesExplicitSLAOutcome(t *testing.T) {
	report := &RunArchiveReport{Manifest: &RunArchiveManifest{SLABreached: true}}
	report.Overview.Browser = &BrowserLiveMetrics{Samples: 3}
	summary := buildRunRootCauseSummary("browser-sla", report)
	if summary.Status != "breached" {
		t.Fatalf("advisory qualification overrode explicit SLA: %#v", summary)
	}
	for _, signal := range summary.Signals {
		if signal.Name == "Browser UX" && signal.Status != "unscored" {
			t.Fatal("SLA outcome invented browser metric eligibility")
		}
	}
}

func TestLegacyBrowserAdvisoryDoesNotInventMissingValues(t *testing.T) {
	got := legacyBrowserAdvisoryThresholds(&BrowserLiveMetrics{Samples: 1, AvgLCPMs: 4500})
	if len(got) != 1 || got[0] != "Reported LCP 4500 ms" {
		t.Fatalf("partial legacy observation broadened: %#v", got)
	}
}
