package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/rca/signatures"
)

func phaseAnatomyFixture(status string, decompositionInstances int64, serverMs float64, reused, created int64, tlsObservedPct float64) RunArchiveTransactionAnatomy {
	share := func(v float64) *float64 { return &v }
	return RunArchiveTransactionAnatomy{
		Name: "checkout", Instances: 500, JoinedInstances: 500,
		Decomposition: RunArchiveTimeDecomposition{Instances: decompositionInstances, AvgDurationMs: 200 + serverMs, Phases: RunArchivePhaseMeans{ConnectMs: 5, ServerMs: serverMs, DownloadMs: 20}},
		Requests: []RunArchiveAnatomyRequest{{Key: "1|GET /cart#1", Samples: 500,
			Phases:      RunArchiveAnatomyPhaseStats{TLS: RunArchivePhaseStat{Samples: 500, ObservedPct: share(tlsObservedPct)}},
			Connections: RunArchiveConnectionStats{New: created, Reused: reused}}},
		Tail: &RunArchiveTailAttribution{
			Status: status, Instances: 500,
			Tail:   RunArchiveTailBand{Count: 25, Joined: 25, AvgDurationMs: 1000},
			Median: RunArchiveTailBand{Count: 250, Joined: 240, AvgDurationMs: 580},
			Delta: RunArchiveTailDelta{TotalMs: 420, Components: []RunArchiveTailComponent{
				{Component: "server", DeltaMs: 360}, {Component: "connect", DeltaMs: 45}, {Component: "non_request", DeltaMs: 15},
			}},
		},
	}
}

func TestRCAPhaseAttributionsFromTimeAnatomy(t *testing.T) {
	current := &RunArchiveTimeAnatomy{Transactions: []RunArchiveTransactionAnatomy{phaseAnatomyFixture("ok", 480, 500, 100, 400, 80)}}
	baseline := &RunArchiveTimeAnatomy{Transactions: []RunArchiveTransactionAnatomy{phaseAnatomyFixture("ok", 490, 150, 450, 50, 10)}}
	attributions := rcaPhaseAttributions(current, baseline)
	if len(attributions) != 2 {
		t.Fatalf("%+v", attributions)
	}
	tail, base := attributions[0], attributions[1]
	if tail.Source != signatures.PhaseSourceTail || tail.Population != 500 || tail.Instances != 25 || tail.ReferenceInstances != 240 ||
		tail.DeltaMs != 420 || tail.Phases["server"] != 360 || tail.Phases["connect"] != 45 || len(tail.Phases) != 2 ||
		tail.ReferenceJoinCoverage != 240.0/250 || !strings.Contains(tail.Evidence.Locator, "transaction=checkout") || tail.Evidence.Kind != "time_anatomy" {
		t.Fatalf("tail %+v", tail)
	}
	if shift, ok := tail.Shift(); !ok || shift.Dominant != signatures.PhaseServer {
		t.Fatalf("tail shift %+v", shift)
	}
	if base.Source != signatures.PhaseSourceBaseline || base.DeltaMs != 350 || base.Phases["server"] != 350 || base.JoinCoverage != 480.0/500 ||
		base.ReusePct == nil || *base.ReusePct != 20 || *base.ReferenceReusePct != 90 || *base.TLSObservedPct != 80 || *base.ReferenceTLSObservedPct != 10 {
		t.Fatalf("baseline %+v", base)
	}
	if shift, ok := base.Shift(); !ok || !shift.HandshakesUp || shift.Dominant != signatures.PhaseServer {
		t.Fatalf("baseline shift %+v", shift)
	}
}

func TestRCAPhaseAttributionsSkipPartialAnatomy(t *testing.T) {
	for _, status := range []string{"insufficient_samples", "no_tail", "low_join_coverage"} {
		current := &RunArchiveTimeAnatomy{Transactions: []RunArchiveTransactionAnatomy{phaseAnatomyFixture(status, 480, 500, 0, 0, 0)}}
		if attributions := rcaPhaseAttributions(current, nil); len(attributions) != 0 {
			t.Fatalf("%s: %+v", status, attributions)
		}
	}
	// A baseline decomposition covering under 80% of its instances is read
	// but is not evidence.
	current := &RunArchiveTimeAnatomy{Transactions: []RunArchiveTransactionAnatomy{phaseAnatomyFixture("no_tail", 480, 500, 0, 0, 0)}}
	baseline := &RunArchiveTimeAnatomy{Transactions: []RunArchiveTransactionAnatomy{phaseAnatomyFixture("no_tail", 300, 150, 0, 0, 0)}}
	attributions := rcaPhaseAttributions(current, baseline)
	if len(attributions) != 1 {
		t.Fatalf("%+v", attributions)
	}
	if _, ok := attributions[0].Shift(); ok {
		t.Fatalf("a low-coverage baseline became evidence: %+v", attributions[0])
	}
	if rcaPhaseAttributions(nil, baseline) != nil {
		t.Fatal("no anatomy, no attributions")
	}
}
