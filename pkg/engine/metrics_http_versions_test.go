package engine

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestMetricsSnapshotCarriesTheRunToDateHTTPVersionMix(t *testing.T) {
	mc := NewMetricsCollector(16)
	if snap := mc.computeSnapshot([]*protocol.Result{{ActionName: "think", BusinessTransaction: true, Success: true}}, time.Second); snap.HTTPVersions != nil {
		t.Fatalf("no HTTP action yet: %+v", snap.HTTPVersions)
	}
	upgraded := &protocol.Result{ActionName: "home", Success: true, HTTPVersionPolicy: "auto+h3", HTTPVersionNegotiated: "3", HTTPVersionSource: "wire",
		HTTPReceipt: map[string]any{"http_version_selection_reason": "upgraded", "http_version_fallback": "none", "attempt_count": 1, "quic_attempted": true}}
	fellBack := &protocol.Result{ActionName: "home", Success: true, HTTPVersionPolicy: "auto+h3", HTTPVersionNegotiated: "2", HTTPVersionSource: "wire",
		HTTPReceipt: map[string]any{"http_version_selection_reason": "upgraded", "http_version_fallback": "quic_connect_failed", "quic_error_class": "handshake", "attempt_count": 2, "quic_attempted": true}}
	first := mc.computeSnapshot([]*protocol.Result{compactMetricsResult(upgraded), compactMetricsResult(fellBack)}, time.Second)
	if first.HTTPVersions == nil || first.HTTPVersions.Actions != 2 || first.HTTPVersions.FellBack != 1 || first.HTTPVersions.Attempts != 3 {
		t.Fatalf("first window mix = %+v", first.HTTPVersions)
	}
	second := mc.computeSnapshot([]*protocol.Result{{ActionName: "legacy", Success: true, HTTPVersionPolicy: "auto", HTTPVersionNegotiated: "1.1", HTTPVersionSource: "wire"}}, time.Second)
	if second.HTTPVersions.Actions != 3 || second.HTTPVersions.ByVersion["1.1"] != 1 || second.HTTPVersions.ByVersion["3"] != 1 || second.HTTPVersions.UpgradeEligible != 2 {
		t.Fatalf("the mix is run-to-date: %+v", second.HTTPVersions)
	}
	// Snapshots own their copy.
	second.HTTPVersions.ByVersion["3"] = 99
	if third := mc.computeSnapshot(nil, time.Second); third.HTTPVersions.ByVersion["3"] != 1 {
		t.Fatalf("snapshot shares the collector's counts: %+v", third.HTTPVersions)
	}
}
