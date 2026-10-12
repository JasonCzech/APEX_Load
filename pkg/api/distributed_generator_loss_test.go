package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// finalizingArchiveClient answers that its archive is still being written for
// the first few manifest requests, as a generator does while its test stops.
type finalizingArchiveClient struct {
	*archiveStreamingFakeClient
	mu         sync.Mutex
	finalizing int
	asked      int
}

func (c *finalizingArchiveClient) StreamArchiveArtifact(ctx context.Context, request generatorrpc.ArchiveArtifactRequest, destination io.Writer, maxBytes int64) (int64, error) {
	c.mu.Lock()
	if request.Name == runArchiveManifestFileName {
		c.asked++
		if c.asked <= c.finalizing {
			c.mu.Unlock()
			return 0, status.Error(codes.Unavailable, generatorrpc.ArchiveFinalizingMessage)
		}
	}
	c.mu.Unlock()
	return c.archiveStreamingFakeClient.StreamArchiveArtifact(ctx, request, destination, maxBytes)
}

func TestFailedRunCollectsASurvivingGeneratorStillWritingItsArchive(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	previous := remoteArchiveFinalizingPoll
	remoteArchiveFinalizingPoll = [2]time.Duration{5 * time.Millisecond, 20 * time.Millisecond}
	t.Cleanup(func() { remoteArchiveFinalizingPoll = previous })

	const id = "surviving-generator-finalizing"
	survivor := &finalizingArchiveClient{archiveStreamingFakeClient: coverageArchiveClient(t, id), finalizing: 3}
	run := &ManagedTest{ID: id, Name: "Loss", Status: "FAILED", CreatedAt: time.Now().UTC(),
		remoteClientsByTarget: map[string]generatorClient{"gen-a:1": survivor, "gen-b:1": &startupLifecycleFakeClient{}},
		GeneratorShards: []GeneratorShardSummary{
			{Group: "Store_Browse", Target: "gen-a:1", VUsers: 3},
			{Group: "Store_Cart", Target: "gen-b:1", VUsers: 3},
		},
		remoteMetricsBatchesByTarget: map[string]generatorrpc.MetricsBatch{"gen-a:1": {GeneratorID: "gen-a"}, "gen-b:1": {GeneratorID: "gen-b"}},
	}
	runner := NewTestRunner(1, nil, nil)
	runner.tests[id] = run
	backend := newDistributedExecutionBackend([]string{"gen-a:1|region=us-east", "gen-b:1|region=us-west"}, time.Second, nil)
	record, err := backend.stageRemoteArchive(runner, run, "FAILED", "", time.Now().UTC(), false)
	if err != nil || record == nil {
		t.Fatalf("stage: %+v %v", record, err)
	}
	if survivor.asked <= survivor.finalizing {
		t.Fatalf("collection asked %d times, want it to wait past %d finalizing answers", survivor.asked, survivor.finalizing)
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatal(err)
	}
	if report.Overview.Transactions == 0 {
		t.Fatalf("the surviving generator's evidence was not collected: %+v", report.Overview)
	}
	sources := map[string]RunArchiveSource{}
	for _, source := range report.Manifest.SourceCoverage.Sources {
		sources[source.Target] = source
	}
	a, b := sources["gen-a:1"], sources["gen-b:1"]
	if !a.Collected || a.GeneratorID != "gen-a" || a.Region != "us-east" || strings.Join(a.Groups, ",") != "Store_Browse" {
		t.Fatalf("surviving source = %+v", a)
	}
	if b.Collected || b.GeneratorID != "gen-b" || b.Region != "us-west" || strings.Join(b.Groups, ",") != "Store_Cart" {
		t.Fatalf("lost source = %+v", b)
	}
	var message string
	for _, finding := range report.Completeness.Findings {
		if finding.Dimension == "generator_sources" {
			message = finding.Message
		}
	}
	if message != "1 of 2 known generator archive sources were not collected: gen-b (gen-b:1; us-west; group Store_Cart). Surviving measurements cover Store_Browse only." {
		t.Fatalf("coverage finding = %q", message)
	}
	// Every export names the sources the way the dashboard does.
	var csv bytes.Buffer
	if err := writeRunArchiveReportCSV(report, &csv); err != nil {
		t.Fatal(err)
	}
	for _, row := range []string{"archive_source,gen-b:1,generator_id,gen-b", "archive_source,gen-b:1,region,us-west", "archive_source,gen-b:1,groups,Store_Cart"} {
		if !strings.Contains(csv.String(), row) {
			t.Fatalf("CSV is missing %q", row)
		}
	}
	html, err := renderRunArchiveHTMLReport(report)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(html, []byte("<td>gen-b (gen-b:1; us-west; group Store_Cart)</td><td>Not collected</td>")) {
		t.Fatal("HTML source inventory does not name the lost generator")
	}
	profile, _ := lookupRunArchiveReportProfile("engineer")
	if lines := strings.Join(buildRunArchiveReportPDFLines(report, profile, nil, nil), "\n"); !strings.Contains(lines, "- gen-b (gen-b:1; us-west; group Store_Cart): not collected") {
		t.Fatal("PDF source inventory does not name the lost generator")
	}
}

func TestAwaitRemoteArchiveFinalizedStopsAtItsDeadline(t *testing.T) {
	previous := remoteArchiveFinalizingPoll
	remoteArchiveFinalizingPoll = [2]time.Duration{5 * time.Millisecond, 10 * time.Millisecond}
	t.Cleanup(func() { remoteArchiveFinalizingPoll = previous })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	calls := 0
	err := awaitRemoteArchiveFinalized(ctx, func() error {
		calls++
		return status.Error(codes.Unavailable, generatorrpc.ArchiveFinalizingMessage)
	})
	if !generatorrpc.IsArchiveFinalizing(err) || calls < 2 {
		t.Fatalf("after the deadline = %v (%d calls), want the finalizing answer after retrying", err, calls)
	}
	calls = 0
	if err := awaitRemoteArchiveFinalized(context.Background(), func() error {
		calls++
		return status.Error(codes.NotFound, "archive not found")
	}); status.Code(err) != codes.NotFound || calls != 1 {
		t.Fatalf("a missing archive = %v after %d calls, want not found at once", err, calls)
	}
}

func TestArchiveMissingSourcesMessageSaysWhatIsLeft(t *testing.T) {
	sources := []RunArchiveSource{
		{Target: "a", Collected: true, GeneratorID: "gen-a", Groups: []string{"Browse", "Cart"}},
		{Target: "b", Collected: false, GeneratorID: "gen-b", Region: "us-west", Groups: []string{"Cart", "Pay"}},
		{Target: "c", Collected: false},
	}
	got := archiveMissingSourcesMessage(sources, 2)
	want := "2 of 3 known generator archive sources were not collected: gen-b (b; us-west; groups Cart, Pay), c. Surviving measurements cover Browse and part of Cart."
	if got != want {
		t.Fatalf("message = %q\nwant      %q", got, want)
	}
	if got := archiveMissingSourcesMessage([]RunArchiveSource{{Target: "x"}}, 1); got != "1 of 1 known generator archive sources were not collected: x. Surviving measurements are partial evidence." {
		t.Fatalf("message without groups = %q", got)
	}
	manifest := &RunArchiveManifest{Provenance: RunArchiveProvenance{LiveTables: []RunArchiveLiveTableSummary{{Alias: "work"}}}}
	if note := liveTableLossNote(archiveLiveTableAliases(manifest)); !strings.HasPrefix(note, " Live table work: leases the lost generator still held may be redelivered") {
		t.Fatalf("live-table note = %q", note)
	}
	if note := liveTableLossNote(nil); note != "" {
		t.Fatalf("note without live tables = %q", note)
	}
}

func TestGeneratorFailureMessageNamesTheGenerator(t *testing.T) {
	started := time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)
	run := &ManagedTest{
		StartedAt:                    &started,
		GeneratorShards:              []GeneratorShardSummary{{Group: "Store_Cart", Target: "gen-b:1"}, {Group: "Store_Cart", Target: "gen-b:1"}, {Group: "Store_Browse", Target: "gen-a:1"}},
		remoteMetricsBatchesByTarget: map[string]generatorrpc.MetricsBatch{"gen-b:1": {GeneratorID: "gen-b"}},
		DataPools:                    []storage.ScenarioDataPoolBinding{{Alias: "work", AccessMode: "live"}, {Alias: "customers", AccessMode: "read_only"}},
	}
	backend := newDistributedExecutionBackend([]string{"gen-b:1|region=us-west"}, time.Second, nil)
	got := backend.remoteTargetFailureMessageLocked(run, "gen-b:1", "failed", "generator shut down before test completion", started.Add(98*time.Second))
	want := "Generator gen-b (gen-b:1; us-west; group Store_Cart) failed 1m38s into the run: generator shut down before test completion"
	if got != want {
		t.Fatalf("message = %q\nwant      %q", got, want)
	}
	if aliases := testLiveTableAliases(run); strings.Join(aliases, ",") != "work" {
		t.Fatalf("live-table aliases = %v, want only the live binding", aliases)
	}
	lost := backend.remoteTargetLossMessageLocked(run, "gen-b:1", errors.New("EOF"), started.Add(98*time.Second))
	if lost != "Generator gen-b (gen-b:1; us-west; group Store_Cart) was lost 1m38s into the run: its metrics stream stopped: EOF. Live table work: leases the lost generator still held may be redelivered once they expire, and pops it had in flight may have unknown outcomes." {
		t.Fatalf("lost-generator message = %q", lost)
	}
	if got := backend.describeRemoteTargetLocked(&ManagedTest{}, "10.0.0.9:7000"); got != "10.0.0.9:7000" {
		t.Fatalf("unknown generator = %q", got)
	}
}

func TestGeneratorSnapshotKeepsAZeroErrorRate(t *testing.T) {
	backend := newDistributedExecutionBackend([]string{"gen-a:1"}, time.Second, nil)
	snapshots := backend.buildGeneratorResourceSnapshots(nil, map[string]generatorrpc.MetricsBatch{
		"gen-a:1": {GeneratorID: "gen-a", WindowMs: 1000, Transactions: []generatorrpc.TransactionMetric{{Success: true, DurationMs: 10}}},
		"gen-b:1": {GeneratorID: "gen-b", WindowMs: 1000},
	})
	if got := snapshots["gen-a"].ErrorPct; got == nil || *got != 0 {
		t.Fatalf("error rate with only successes = %v, want 0", got)
	}
	if got := snapshots["gen-b"].ErrorPct; got != nil {
		t.Fatalf("error rate with no transactions = %v, want absent", *got)
	}
}

func TestRunErrorFindingReadsAsTheRunsError(t *testing.T) {
	score := buildRunArchiveCompletenessScoreFromCounts(&RunArchiveManifest{Error: "Generator gen-b failed"}, &RunArchiveReport{}, 0, 0, 0)
	var message string
	for _, finding := range score.Findings {
		if finding.Dimension == "archive_writer" {
			message = finding.Message
		}
	}
	if message != "The run ended with an error, so its evidence may stop short: Generator gen-b failed" {
		t.Fatalf("run-error finding = %q", message)
	}
	stopped := buildRunArchiveCompletenessScoreFromCounts(&RunArchiveManifest{Status: "COMPLETE", Error: "max errors reached"}, &RunArchiveReport{}, 0, 0, 0)
	for _, finding := range stopped.Findings {
		if finding.Dimension == "archive_writer" && finding.Message != "The run stopped early, so its evidence may stop short: max errors reached" {
			t.Fatalf("completed run's early-stop finding = %q", finding.Message)
		}
	}
	if got := completenessDimensionLabel("archive_writer"); got != "run error" {
		t.Fatalf("label = %q", got)
	}
	if got := completenessDimensionLabel("generator_sources"); got != "generator sources" {
		t.Fatalf("label = %q", got)
	}
}
