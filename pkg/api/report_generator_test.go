package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/protocol"
)

func TestRenderRunArchiveHTMLReportIncludesBrowserAndSOAPSections(t *testing.T) {
	now := time.Date(2026, time.May, 15, 12, 30, 0, 0, time.UTC)
	report := &RunArchiveReport{
		Manifest: &RunArchiveManifest{
			TestID:      "test-123",
			TestName:    "Checkout Flow",
			Status:      "COMPLETE",
			CompletedAt: now,
			SLABreached: true,
			SLADetails:  []string{"Latency breached"},
		},
		Overview: RunArchiveOverview{
			Transactions:     120,
			Errors:           4,
			ErrorPct:         3.33,
			AvgResponseMs:    210.2,
			MinResponseMs:    80,
			MaxResponseMs:    800,
			ResourceSamples:  10,
			PeakTPS:          34.5,
			PeakActiveVUsers: 25,
			Browser: &BrowserLiveMetrics{
				Samples:         8,
				AvgFCPMs:        120,
				AvgLCPMs:        280,
				AvgCLSScore:     0.032,
				AvgINPMs:        65,
				AvgJSHeapUsedMB: 42,
			},
		},
		SummaryTimeline: []RunArchiveSummaryPoint{{
			Timestamp:    now,
			ActiveVUsers: 25,
			TPS:          34.5,
			ErrorPct:     3.33,
			P95Ms:        410.5,
			TotalTx:      120,
			TotalErrors:  4,
		}},
		ErrorPatterns: []RunArchiveErrorPattern{{
			Signature:  "soap fault: request validation failed for order #",
			Sample:     "soap fault: Request validation failed for order 1001",
			Count:      2,
			FirstSeen:  now,
			LatestSeen: now.Add(2 * time.Minute),
			Actions:    []string{"CreateOrder"},
			Snapshot:   &protocol.ErrorSnapshot{RequestURL: "https://soap.example.test/orders"},
		}},
		ResourcePeaks: RunArchiveResourcePeaks{
			CPUProcessPct: 64.2,
			MemProcessMB:  512,
			NetSentKbps:   220,
			NetRecvKbps:   330,
		},
		ByAction: map[string]RunArchiveActionStats{
			"checkout": {Transactions: 100, Errors: 4, ErrorPct: 4, AvgResponseMs: 210, MaxResponseMs: 800},
		},
		BySOAPOperation: map[string]RunArchiveActionStats{
			"CreateOrder": {Transactions: 30, Errors: 2, ErrorPct: 6.67, AvgResponseMs: 320, MaxResponseMs: 700},
		},
		BySOAPFaultCode: map[string]int64{
			"env:Sender": 2,
		},
		BySOAPFaultCategory: map[string]int64{
			"client": 2,
		},
		SOAPFaultTimeline: []RunArchiveSOAPFaultTimelineBucket{{
			Timestamp:  now,
			Total:      2,
			ByCode:     map[string]int64{"env:Sender": 2},
			ByCategory: map[string]int64{"client": 2},
		}},
		SOAPPayloadTimeline: []RunArchiveSOAPPayloadTimelineBucket{{
			Timestamp:             now,
			Samples:               3,
			AvgRequestPayloadKB:   12.5,
			AvgResponsePayloadKB:  18.8,
			AvgEnvelopeOverheadKB: 3.1,
			MaxEnvelopeOverheadKB: 5.2,
		}},
		BrowserByEngine: map[string]*BrowserEngineLiveMetrics{
			"webkit": {BrowserEngine: "webkit", Samples: 8, AvgLCPMs: 280, AvgCLSScore: 0.032, AvgINPMs: 65},
		},
		BrowserByPlatform: map[string]*BrowserPlatformLiveMetrics{
			"ios": {DevicePlatform: "ios", BrowserEngine: "webkit", Samples: 8, AvgLCPMs: 280, AvgCLSScore: 0.032, AvgINPMs: 65},
		},
		BrowserByDevice: map[string]*BrowserDeviceLiveMetrics{
			"iphone-se": {DeviceProfile: "iphone-se", DevicePlatform: "ios", DeviceTier: "low", BrowserEngine: "webkit", Samples: 4, AvgLCPMs: 300, AvgINPMs: 70},
		},
	}

	html, err := renderRunArchiveHTMLReport(report)
	if err != nil {
		t.Fatalf("renderRunArchiveHTMLReport returned error: %v", err)
	}

	body := string(html)
	checks := []string{
		"Checkout Flow",
		"@media print",
		"@page",
		"break-inside: avoid",
		"Summary Timeline",
		"Error Patterns",
		"soap fault: Request validation failed for order 1001",
		"retained",
		"2026-05-15T12:30:00Z",
		"410.5",
		"Browser Metrics",
		"Engine Rollup",
		"Platform Rollup",
		"iphone-se",
		"SOAP Diagnostics",
		"SOAP Operation Summary",
		"CreateOrder",
		"SOAP Fault Categories",
		"client",
		"SOAP Fault Timeline",
		"env:Sender (2)",
		"SOAP Payload Timeline",
		"Latency breached",
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Fatalf("expected report HTML to contain %q, got %q", check, body)
		}
	}
}
