package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBrowserBenchmarkEvidenceUsesDecimalByteTargets(t *testing.T) {
	evidence := buildBrowserBenchmarkEvidence(time.Now())
	rawJSON, err := json.Marshal(evidence)
	if err != nil {
		t.Fatalf("marshal browser benchmark evidence: %v", err)
	}
	if strings.Contains(string(rawJSON), "rss_per_vuser_target_mb") {
		t.Fatalf("expected legacy rss_per_vuser_target_mb field to be omitted in v3: %s", rawJSON)
	}
	for _, profile := range evidence.Profiles {
		a := profile.Acceptance
		if a.MeasurementVersion != "apexload.browser_benchmark.v3" || a.RequiredRawRunCount != 5 || profile.DensityEnvelope.MaximumClaimedVUsersPerGenerator != 5 || profile.HighDensityClaimAllowed {
			t.Fatal("measurement version or qualification envelope changed", profile)
		}
		if profile.BrowserEngine == "chromium" {
			if a.RSSAttribution != "cdp_renderer_per_vuser" {
				t.Fatal("expected chromium acceptance to require cdp_renderer_per_vuser attribution", a)
			}
			if a.RSSPerVUserTargetBytes != 250_000_000 || a.RSSPerVUserTargetBytes == 250<<20 {
				t.Fatal("published decimal target was relaxed", a)
			}
		} else {
			if a.RSSAttribution != "unavailable" {
				t.Fatal("expected non-chromium acceptance to declare unavailable RSS attribution", a)
			}
			if a.RSSPerVUserTargetBytes != 0 {
				t.Fatal("unsupported engine gained RSS target", a)
			}
		}
	}
}
