package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/rca/signatures"
	"github.com/jasonczech/apexload/pkg/storage"
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

// stubRCAReportBuild replaces the report build for one test.
func stubRCAReportBuild(t *testing.T, build func(*storage.RunArchiveRecord) (*RunArchiveReport, error)) {
	t.Helper()
	saved := rcaBuildArchiveReport
	rcaBuildArchiveReport = func(_ *Server, record *storage.RunArchiveRecord) (*RunArchiveReport, error) { return build(record) }
	t.Cleanup(func() { rcaBuildArchiveReport = saved })
}

// blockedRCAReportBuild stubs a build that runs until released. finish
// releases the builds and waits until the given number of them returned, so
// none the test abandoned outlives it (and its stub).
func blockedRCAReportBuild(t *testing.T) (finish func(builds int)) {
	t.Helper()
	release := make(chan struct{})
	returned := make(chan struct{}, 16)
	stubRCAReportBuild(t, func(*storage.RunArchiveRecord) (*RunArchiveReport, error) {
		defer func() { returned <- struct{}{} }()
		<-release
		return &RunArchiveReport{}, nil
	})
	return func(builds int) {
		close(release)
		for i := 0; i < builds; i++ {
			<-returned
		}
	}
}

func TestRCAPhaseAttributionInputStopsWithTheReadTime(t *testing.T) {
	finish := blockedRCAReportBuild(t)
	// Two builds are abandoned below; both return before the stub goes.
	defer finish(2)
	s := &Server{}
	record := &storage.RunArchiveRecord{TestID: "run"}

	// The read time runs out mid-build: the anatomy is left out, the
	// analysis goes on, and the assessment counts as cut by the budget.
	readCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	attributions, limitations, err := s.rcaPhaseAttributionInput(context.Background(), readCtx, record, nil)
	if err != nil || attributions != nil || len(limitations) != 1 || limitations[0].Code != limitationPhaseAttributionInterrupted ||
		!strings.Contains(limitations[0].Detail, "not built within the evidence read time") || !rca.CutByBudget(limitations) {
		t.Fatalf("%v %+v", err, limitations)
	}

	// The caller's own cancellation stops the analysis.
	parent, cancelParent := context.WithCancel(context.Background())
	child, cancelChild := context.WithTimeout(parent, time.Minute)
	defer cancelChild()
	go func() { time.Sleep(10 * time.Millisecond); cancelParent() }()
	if _, _, err := s.rcaPhaseAttributionInput(parent, child, record, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("parent cancel: %v", err)
	}
}

func TestRCAPhaseAttributionInputDoesNotStartPastTheReadTime(t *testing.T) {
	var calls atomic.Int32
	stubRCAReportBuild(t, func(*storage.RunArchiveRecord) (*RunArchiveReport, error) {
		calls.Add(1)
		return &RunArchiveReport{}, nil
	})
	readCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, limitations, err := (&Server{}).rcaPhaseAttributionInput(context.Background(), readCtx, &storage.RunArchiveRecord{TestID: "run"}, nil)
	if err != nil || calls.Load() != 0 || len(limitations) != 1 || limitations[0].Code != limitationPhaseAttributionInterrupted {
		t.Fatalf("%v %d %+v", err, calls.Load(), limitations)
	}
}

// Whether an assessment without phase attribution is reused for an
// identical request: not when the build was interrupted or may have failed
// transiently; yes when the frozen archive decided it, or simply has no
// anatomy.
func TestRCAPhaseAttributionLimitationsAndReuse(t *testing.T) {
	for _, tc := range []struct {
		name     string
		report   *RunArchiveReport
		err      error
		code     string
		reanalyz bool
	}{
		{name: "transient error", err: errors.New("read transactions.jsonl: connection reset"), code: limitationPhaseAttributionInterrupted, reanalyz: true},
		{name: "report bound", err: archiveReportBoundErrorf("archive report exceeds %d distinct %s values", 256, "action"), code: limitationPhaseAttributionUnavailable},
		{name: "no anatomy", report: &RunArchiveReport{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stubRCAReportBuild(t, func(*storage.RunArchiveRecord) (*RunArchiveReport, error) { return tc.report, tc.err })
			attributions, limitations, err := (&Server{}).rcaPhaseAttributionInput(context.Background(), context.Background(), &storage.RunArchiveRecord{TestID: "run"}, nil)
			if err != nil || len(attributions) != 0 {
				t.Fatalf("%v %+v", err, attributions)
			}
			if tc.code == "" && len(limitations) != 0 || tc.code != "" && (len(limitations) != 1 || limitations[0].Code != tc.code) {
				t.Fatalf("%+v", limitations)
			}
			if got := rca.CutByBudget(limitations); got != tc.reanalyz {
				t.Fatalf("CutByBudget = %v", got)
			}
		})
	}
}

func TestRCAPhaseAttributionInputNotesAnUnbuildableBaseline(t *testing.T) {
	current := &RunArchiveTimeAnatomy{Transactions: []RunArchiveTransactionAnatomy{phaseAnatomyFixture("ok", 480, 500, 0, 0, 0)}}
	stubRCAReportBuild(t, func(record *storage.RunArchiveRecord) (*RunArchiveReport, error) {
		if record.TestID == "baseline" {
			return nil, archiveReportBoundErrorf("archive report exceeds %d distinct %s values", 256, "action")
		}
		return &RunArchiveReport{TimeAnatomy: current}, nil
	})
	baseline := &rcaBaselineArchive{testID: "baseline", record: &storage.RunArchiveRecord{TestID: "baseline"}, manifest: &RunArchiveManifest{},
		qualification: rca.WorkloadQualification{Status: rca.BaselineQualified}}
	attributions, limitations, err := (&Server{}).rcaPhaseAttributionInput(context.Background(), context.Background(), &storage.RunArchiveRecord{TestID: "run"}, baseline)
	if err != nil || len(attributions) != 1 || attributions[0].Source != signatures.PhaseSourceTail || len(limitations) != 1 ||
		limitations[0].Code != limitationPhaseAttributionUnavailable || !strings.HasPrefix(limitations[0].Detail, "the baseline's time anatomy could not be built") {
		t.Fatalf("%v %+v %+v", err, attributions, limitations)
	}
}

// A real report build that hits the request dimension bound fails with the
// same message as before, is classified as deterministic, and leaves an
// assessment identical requests reuse.
func TestRCAPhaseAttributionRequestDimensionBoundIsReusable(t *testing.T) {
	root := t.TempDir()
	start := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	file, err := os.Create(filepath.Join(root, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	rows := maxArchiveAnalysisDimensions + 1
	for i := 0; i < rows; i++ {
		if err := encoder.Encode(TransactionEvent{Timestamp: start.Add(time.Duration(i) * time.Millisecond), ActionName: fmt.Sprintf("GET /r/%d", i), Population: "action", Success: true, DurationMs: 10}); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"resources.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	end := start.Add(time.Minute)
	manifest, _ := json.Marshal(RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: "bound", TestName: "bound", Status: "COMPLETE", CreatedAt: start, CompletedAt: end,
		Provenance: RunArchiveProvenance{StartedAt: &start}, TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "interval_summaries.jsonl", TransactionCount: int64(rows)})
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{TestID: "bound", SchemaVersion: archiveSchemaVersion, ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json"), CreatedAt: start, CompletedAt: end, TransactionCount: int64(rows)}

	_, buildErr := buildRunArchiveReport(record)
	if buildErr == nil || buildErr.Error() != "archive exceeds request dimension limit" || !errors.Is(buildErr, errArchiveReportBound) {
		t.Fatalf("build: %v", buildErr)
	}
	stubRCAReportBuild(t, buildRunArchiveReport)
	_, limitations, err := (&Server{}).rcaPhaseAttributionInput(context.Background(), context.Background(), record, nil)
	if err != nil || len(limitations) != 1 || limitations[0].Code != limitationPhaseAttributionUnavailable ||
		limitations[0].Detail != "the run's time anatomy could not be built: archive exceeds request dimension limit" || rca.CutByBudget(limitations) {
		t.Fatalf("%v %+v", err, limitations)
	}
	// The bounded-analysis loader's limit is a bound too.
	if !rcaReportDeterministicError(fmt.Errorf("read summaries: %w", archiveReportBoundErrorf("archive exceeds bounded analysis limit"))) {
		t.Fatal("a wrapped bounded-analysis error is not deterministic")
	}
}
