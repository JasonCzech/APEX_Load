package api

import (
	"testing"
	"time"
)

func TestBrowserBenchmarkEvidenceUsesDecimalByteTargets(t *testing.T) {
	evidence := buildBrowserBenchmarkEvidence(time.Now())
	for _, profile := range evidence.Profiles {
		a := profile.Acceptance
		if a.MeasurementVersion != "apexload.browser_benchmark.v2" || a.RequiredRawRunCount != 5 || profile.DensityEnvelope.MaximumClaimedVUsersPerGenerator != 5 || profile.HighDensityClaimAllowed {
			t.Fatal("measurement version or qualification envelope changed", profile)
		}
		if profile.BrowserEngine == "chromium" {
			if a.RSSAttribution != "shared_playwright_driver_descendants_or_driver" {
				t.Fatal("sampler mislabeled as CDP or per-VUser attribution", a)
			}
			if a.RSSPerVUserTargetMB != 250 || a.RSSPerVUserTargetBytes != 250_000_000 || a.RSSPerVUserTargetBytes == 250<<20 {
				t.Fatal("published decimal target was relaxed", a)
			}
		} else if a.RSSPerVUserTargetBytes != 0 || a.RSSPerVUserTargetMB != 0 {
			t.Fatal("unsupported engine gained RSS target", a)
		}
	}
}
