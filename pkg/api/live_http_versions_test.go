package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestLiveMetricsCarryTheRunToDateHTTPVersionMix(t *testing.T) {
	live := buildLiveMetricsFromSnapshot(engine.MetricsSnapshot{Timestamp: time.Now(), HTTPVersions: &protocol.HTTPVersionMix{
		Actions: 4, ByVersion: map[string]int64{"3": 3, "2": 1}, HTTP3Policies: 4, UpgradeEligible: 4, FellBack: 1, QUICAttempted: 4, QUICFailed: 1,
	}}, nil)
	if live.HTTPVersions == nil || live.HTTPVersions.FallbackRatio != (protocol.Ratio{Numerator: 1, Denominator: 4, Value: 0.25}) {
		t.Fatalf("local live mix = %+v", live.HTTPVersions)
	}
	// Below the default 20 samples there is no finding yet.
	if live.HTTPVersions.Finding != nil {
		t.Fatalf("finding = %+v", live.HTTPVersions.Finding)
	}
	if empty := buildLiveMetricsFromSnapshot(engine.MetricsSnapshot{Timestamp: time.Now()}, nil); empty.HTTPVersions != nil {
		t.Fatal("no HTTP action means no live mix")
	}

	cloned := cloneLiveMetrics(live)
	cloned.HTTPVersions.ByVersion["3"] = 99
	if live.HTTPVersions.ByVersion["3"] != 3 {
		t.Fatal("cloned live metrics share the mix")
	}
	if filtered := filterLiveMetricsForGroups(live, groupFilterQuery{Explicit: true, Names: []string{"buyers"}}); filtered.HTTPVersions != nil {
		t.Fatal("a group-filtered view must not show the run-wide mix")
	}
}

func TestDistributedLiveMetricsSumGeneratorMixesAndApplyTheDeclaredExpectation(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	maxFallback, maxQUICFailure := 0.1, 0.5
	script := &dsl.Script{Config: dsl.ScriptConfig{Results: dsl.ResultsConfig{ProtocolMix: &dsl.ProtocolMixExpectation{MaxFallbackRatio: &maxFallback, MaxQUICFailureRatio: &maxQUICFailure, MinSamples: 2}}}}
	run := &ManagedTest{ID: "mix-run", Status: "RUNNING", groupSpecs: []engine.GroupConfig{{Script: script}}}
	runner.tests[run.ID] = run
	backend := &distributedExecutionBackend{}
	now := time.Now()
	backend.applyRemoteMetricsBatch(runner, run, "a", generatorrpc.MetricsBatch{TestID: run.ID, TimestampUnixMs: now.UnixMilli(), HTTPVersionMix: &protocol.HTTPVersionMix{
		Actions: 3, ByVersion: map[string]int64{"3": 2, "2": 1}, HTTP3Policies: 3, UpgradeEligible: 3, FellBack: 1, ByFallback: map[string]int64{"quic_connect_failed": 1}, QUICAttempted: 3, QUICFailed: 1,
	}})
	// An older generator sends no mix.
	backend.applyRemoteMetricsBatch(runner, run, "b", generatorrpc.MetricsBatch{TestID: run.ID, TimestampUnixMs: now.UnixMilli()})
	live, _, _ := backend.applyRemoteMetricsBatch(runner, run, "c", generatorrpc.MetricsBatch{TestID: run.ID, TimestampUnixMs: now.UnixMilli(), HTTPVersionMix: &protocol.HTTPVersionMix{
		Actions: 2, ByVersion: map[string]int64{"3": 2}, HTTP3Policies: 2, UpgradeEligible: 2, QUICAttempted: 2,
	}})
	if live == nil || live.HTTPVersions == nil || live.HTTPVersions.Actions != 5 || live.HTTPVersions.ByVersion["3"] != 4 || live.HTTPVersions.FallbackRatio.Denominator != 5 {
		t.Fatalf("fleet mix = %+v", live)
	}
	if !runner.publishLiveMetrics(run, live, nil) {
		t.Fatal("publication rejected")
	}
	finding := run.LiveMetrics.HTTPVersions.Finding
	if finding == nil || len(finding.Breaches) != 1 || finding.Breaches[0].Ratio != "fallback" || finding.Breaches[0].Expected != 0.1 {
		t.Fatalf("published finding = %+v", finding)
	}
}
