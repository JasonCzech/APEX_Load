package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func apiProgressFixture() *protocol.MessagingProgressSnapshot {
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	age := int64(10)
	position := protocol.MessagingProgressPosition{Sequence: "9007199254740993", ProviderOffset: "2:-1:-1", State: "observed", ObservedAt: now, ReceivedAt: now, ObservationAgeMs: &age}
	row := protocol.MessagingProgressRow{Origin: "forged", Group: "workers", Protocol: "eventhubs", Mode: "processor", SourceID: "source", Source: "<hub>", Subscription: "consumer", SessionID: "session", Partition: "0", Generation: "generation", Revision: "1", CaptureRevision: "1", CapturedAt: now, UpdatedAt: now, ExpectedPartitions: 1, InventoryComplete: true, Ownership: "owned", OwnershipVerifiedAt: now, OwnershipVerifiedAgeMs: &age, Acquired: 1, Delivered: 1, DeliveredPosition: position, CheckpointPosition: position, TailPosition: position, Checkpoint: protocol.MessagingCheckpointProgress{Duration: protocol.MessagingDurationSummary{Metric: "checkpoint", Population: "checkpoint_operation"}}}
	row.TailPosition.Sequence = "9007199254740995"
	return &protocol.MessagingProgressSnapshot{Version: 1, Lifecycle: "running", CapturedAt: now, Rows: []protocol.MessagingProgressRow{row}}
}
func TestMessagingProgressLocalDistributedHistoryAndParquet(t *testing.T) {
	snapshot := apiProgressFixture()
	if !snapshot.Valid() {
		t.Fatal("invalid fixture")
	}
	local := buildLiveMetricsFromSnapshot(engine.MetricsSnapshot{MessagingProgress: snapshot}, nil)
	batch, _, _ := aggregateDistributedMetricsBatches("run", map[string]generatorrpc.MetricsBatch{"authenticated-peer": {MessagingProgress: snapshot, MessagingProgressVersion: 1}, "http-peer": {}})
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, batch, 0, nil)
	if live.TotalTx != 0 || live.MessagingProgress.Incomplete || len(live.MessagingProgress.Rows) != 1 || live.MessagingProgress.Rows[0].Origin != "authenticated-peer" {
		t.Fatalf("standalone peer coverage: %+v", live)
	}
	for _, projected := range []*LiveMetrics{local, live, cloneLiveMetrics(local), intervalSummaryRecordToLiveMetrics(storage.IntervalSummaryRecord{MessagingProgress: snapshot})} {
		if projected.MessagingProgress.Rows[0].DeliveredPosition.Sequence != "9007199254740993" {
			t.Fatal("cursor rounded or dropped")
		}
		projected.MessagingProgress.Rows[0].Delivered = 9
	}
	if snapshot.Rows[0].Delivered != 1 {
		t.Fatal("unsafe projection alias")
	}
	rows := toParquetSummaries([]IntervalSummaryRecord{{MessagingProgress: snapshot}})
	if !strings.Contains(rows[0].MessagingProgressJSON, `"sequence":"9007199254740993"`) {
		t.Fatal("parquet lost exact evidence")
	}
}
func TestMessagingProgressGroupFilterAndQualification(t *testing.T) {
	snapshot := apiProgressFixture()
	other := snapshot.Rows[0]
	other.Group = "audit"
	other.SessionID = "other"
	snapshot.Rows = append(snapshot.Rows, other)
	filtered := filterLiveMetricsForGroups(&LiveMetrics{MessagingProgress: snapshot}, groupFilterQuery{Explicit: true, Names: []string{"workers"}})
	if len(filtered.MessagingProgress.Rows) != 1 || filtered.MessagingProgress.Rows[0].Group != "workers" {
		t.Fatalf("group evidence: %+v", filtered.MessagingProgress)
	}
	if filterLiveMetricsForGroups(&LiveMetrics{MessagingProgress: snapshot}, groupFilterQuery{Explicit: true}).MessagingProgress != nil {
		t.Fatal("empty group selection leaked progress")
	}
	result := messagingProgressAnalysis(snapshot, "archive", "workers", "source")
	if len(result.Lag) != 1 || result.Lag[0].Delivered == nil || *result.Lag[0].Delivered != "2" || result.Lag[0].Checkpoint == nil || *result.Lag[0].Checkpoint != "2" {
		t.Fatalf("lag %+v", result.Lag)
	}
	for _, mutate := range []func(*protocol.MessagingProgressSnapshot){
		func(s *protocol.MessagingProgressSnapshot) { s.Incomplete = true },
		func(s *protocol.MessagingProgressSnapshot) { s.Rows[0].Ownership = "unverified" },
		func(s *protocol.MessagingProgressSnapshot) {
			age := int64(30001)
			s.Rows[0].TailPosition.ObservationAgeMs = &age
		},
		func(s *protocol.MessagingProgressSnapshot) {
			s.Rows[0].CheckpointPosition = protocol.MessagingProgressPosition{State: "missing"}
		},
	} {
		copy := snapshot.Clone()
		mutate(copy)
		result = messagingProgressAnalysis(copy, "archive", "workers", "source")
		if result.Lag[0].Checkpoint != nil {
			t.Fatal("unqualified checkpoint scored")
		}
	}
}
func TestMessagingProgressArchiveReadReportAndAccess(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/tests/run/messaging-progress?mode=archive", nil)
	access, ok := accessRequirementForRequest(request)
	if !ok || access.ServiceAccountScope != "results:read" || access.ProjectLookup == nil {
		t.Fatalf("access %+v", access)
	}
	record, _ := nativeSeriesFixture(t)
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	manifest.IntervalSummariesFile = "summaries.jsonl"
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(record.ManifestPath, manifestData, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot := apiProgressFixture()
	newer := snapshot.Clone()
	newer.Rows[0].Revision = "2"
	newer.Rows[0].CaptureRevision = "2"
	newer.Rows[0].Delivered = 2
	var body []byte
	for _, s := range []*protocol.MessagingProgressSnapshot{snapshot, newer, newer, nil} {
		data, e := json.Marshal(IntervalSummaryRecord{Timestamp: snapshot.CapturedAt, TestID: "run", MessagingProgress: s})
		if e != nil {
			t.Fatal(e)
		}
		body = append(body, data...)
		body = append(body, '\n')
	}
	if err = os.WriteFile(filepath.Join(record.ArchivePath, manifest.IntervalSummariesFile), body, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := readArchiveMessagingProgress(context.Background(), record)
	if err != nil || len(got.Rows) != 1 || got.Rows[0].Delivered != 2 {
		t.Fatalf("cumulative repeated windows %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = readArchiveMessagingProgress(ctx, record); err == nil {
		t.Fatal("cancellation ignored")
	}
	report := &RunArchiveReport{MessagingProgress: got}
	lines := strings.Join(protocolReportLines(report), "\n")
	if !strings.Contains(lines, "delivered 2") || !strings.Contains(lines, "sequence backlog as of source capture: 2") {
		t.Fatal(lines)
	}
	html, err := protocolDiagnosticReportSection().HTML(&runArchiveHTMLReportData{Report: report})
	if err != nil || strings.Contains(string(html), "<hub>") || !strings.Contains(string(html), "&lt;hub&gt;") {
		t.Fatal("unescaped report", err)
	}
	server, project := newGrafanaDashboardAPIServer(t)
	server.runner.tests["run"] = &ManagedTest{ID: "run", ProjectID: project, Status: "COMPLETE"}
	server.runner.archives["run"] = record
	response := dashboardAPIRequest(t, server, "GET", "/api/v1/tests/run/messaging-progress?mode=archive&group=workers", nil)
	if response.Code != 200 {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	for _, query := range []string{"mode=sql", "mode=live&group=" + strings.Repeat("x", 257)} {
		response = dashboardAPIRequest(t, server, "GET", "/api/v1/tests/run/messaging-progress?"+query, nil)
		if response.Code != 400 {
			t.Fatalf("invalid selection %d", response.Code)
		}
	}
}

func TestMessagingProgressRetainsTerminalBeforeUnrelatedLiveIntervals(t *testing.T) {
	server, project := newGrafanaDashboardAPIServer(t)
	snapshot := apiProgressFixture()
	snapshot.Lifecycle = "released"
	snapshot.Rows[0].Ownership = "released"
	server.runner.tests["run"] = &ManagedTest{ID: "run", ProjectID: project, Status: "RUNNING", LiveMetrics: &LiveMetrics{}, History: []*LiveMetrics{{MessagingProgress: snapshot}, nil, {}}}
	got, _, ok := server.runner.latestMessagingProgress("run")
	if !ok || got == nil || got.Lifecycle != "released" {
		t.Fatal("terminal capture erased by unrelated intervals")
	}
	got.Rows[0].Delivered = 99
	if snapshot.Rows[0].Delivered != 1 {
		t.Fatal("latest progress aliases resident history")
	}
	response := dashboardAPIRequest(t, server, "GET", "/api/v1/tests/run/messaging-progress", nil)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"lifecycle":"released"`) {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
}

func TestMessagingProgressExpectedOnlyForAdmittedProcessors(t *testing.T) {
	claim := &pb.RuntimeResourceClaim{Requested: &pb.RuntimeResourceDemand{Processors: 1, Workers: 1, MaxPartitionsPerProcessor: 1}}
	test := &ManagedTest{GeneratorShards: []GeneratorShardSummary{{Target: "processor", RuntimeResourceClaim: claim}, {Target: "http"}}}
	for _, tc := range []struct {
		name    string
		batches map[string]generatorrpc.MetricsBatch
		reason  string
	}{
		{"cold processor", map[string]generatorrpc.MetricsBatch{"http": {}}, "source_missing"},
		{"legacy processor", map[string]generatorrpc.MetricsBatch{"processor": {}, "http": {}}, "legacy_peer"},
		{"modern processor", map[string]generatorrpc.MetricsBatch{"processor": {MessagingProgress: apiProgressFixture(), MessagingProgressVersion: 1}, "http": {}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			live := &LiveMetrics{}
			qualifyExpectedMessagingProgress(test, live, tc.batches)
			if tc.reason == "" {
				if live.MessagingProgress != nil {
					t.Fatal("invented missing HTTP evidence")
				}
				return
			}
			if live.MessagingProgress == nil || !live.MessagingProgress.Incomplete || live.MessagingProgress.UnavailableReason != tc.reason {
				t.Fatalf("expected %s: %+v", tc.reason, live.MessagingProgress)
			}
		})
	}
	test.GeneratorShards = []GeneratorShardSummary{{Target: "http"}}
	live := &LiveMetrics{}
	qualifyExpectedMessagingProgress(test, live, map[string]generatorrpc.MetricsBatch{"http": {}})
	if live.MessagingProgress != nil {
		t.Fatal("nonprocessor source is not applicable")
	}
}

func TestMessagingProgressHAIdentityRevalidation(t *testing.T) {
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "ha.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &countingListTestStore{Store: base}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if err = store.SaveProject(&storage.Project{ID: "project-b", Name: "B", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err = store.SaveTest(&storage.TestRecord{ID: "reused", ProjectID: "project-b", Name: "Replacement", Status: "COMPLETE", StartTime: now, Groups: []any{}}); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(2, NewHub(), store, WithExecutionLeadership(newExecutionLeadership(store, "follower")))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	old := &ManagedTest{ID: "reused", ProjectID: "project-a", Name: "Old", Status: "RUNNING", CreatedAt: now.Add(-time.Hour), executionCtx: ctx, cancel: cancel, LiveMetrics: &LiveMetrics{MessagingProgress: apiProgressFixture()}}
	runner.mu.Lock()
	runner.tests["reused"] = old
	runner.mu.Unlock()
	runner.haTestRefresh.markFresh(time.Now())
	store.resetReadCounts()
	got, project, ok := runner.latestMessagingProgress("reused")
	if !ok || got != nil || project != "project-b" {
		t.Fatal("stale cross-project evidence disclosed", got, project)
	}
	if store.getCalls.Load() == 0 {
		t.Fatal("durable identity was not revalidated")
	}
}

func TestMessagingProgressRejectsChangedAuthorizedProject(t *testing.T) {
	for _, mode := range []string{"live", "archive"} {
		t.Run(mode, func(t *testing.T) {
			server, project := newGrafanaDashboardAPIServer(t)
			server.runner.tests["run"] = &ManagedTest{ID: "run", ProjectID: project, Status: "COMPLETE", LiveMetrics: &LiveMetrics{MessagingProgress: apiProgressFixture()}}
			// Only the manifest exists: project revalidation must precede evidence I/O.
			root := t.TempDir()
			manifest := RunArchiveManifest{TestID: "run", Provenance: RunArchiveProvenance{ProjectID: project}, IntervalSummariesFile: "missing.jsonl"}
			body, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(root, "manifest.json"), body, 0600); err != nil {
				t.Fatal(err)
			}
			server.runner.archives["run"] = &storage.RunArchiveRecord{TestID: "run", ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json")}
			req := httptest.NewRequest("GET", "/api/v1/tests/run/messaging-progress?mode="+mode, nil)
			req.SetPathValue("id", "run")
			req = req.WithContext(context.WithValue(req.Context(), authorizedProjectContextKey{}, "other-project"))
			response := httptest.NewRecorder()
			server.handleMessagingProgress(response, req)
			if response.Code != 403 || strings.Contains(response.Body.String(), "9007199254740993") {
				t.Fatalf("changed project disclosed evidence: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestMessagingProgressReportCheckpointTimingPopulation(t *testing.T) {
	snapshot := apiProgressFixture()
	lines := strings.Join(protocolReportLines(&RunArchiveReport{MessagingProgress: snapshot}), "\n")
	if !strings.Contains(lines, "Checkpoint timing: unavailable (0 eligible operations; 0 observed)") || !strings.Contains(lines, "opaque provider offset 2:-1:-1") {
		t.Fatal(lines)
	}
	row := &snapshot.Rows[0]
	row.Checkpoint.Attempts = 1
	row.Checkpoint.Committed = 1
	row.Checkpoint.Duration.Eligible = 1
	row.Checkpoint.Duration.Distribution.Add(0)
	row.Checkpoint.LastOutcome = "committed"
	row.Checkpoint.LastCompletedAt = row.CapturedAt
	if !snapshot.Valid() {
		t.Fatal("invalid measured fixture")
	}
	lines = strings.Join(protocolReportLines(&RunArchiveReport{MessagingProgress: snapshot}), "\n")
	if !strings.Contains(lines, "1/1 operations observed; mean 0.000 ms; approximate P95 0.000 ms") {
		t.Fatal(lines)
	}
}

func TestMessagingProgressArchiveUsesControllerAdmissionClaims(t *testing.T) {
	claim := &pb.RuntimeResourceClaim{Requested: &pb.RuntimeResourceDemand{Processors: 1}}
	for _, tc := range []struct {
		name           string
		modern         bool
		peerProcessor  bool
		initialOnly    bool
		forgedRemote   bool
		wantIncomplete bool
	}{
		{name: "admitted legacy peer", modern: true, peerProcessor: true, wantIncomplete: true},
		{name: "unrelated HTTP peer", modern: true},
		{name: "all legacy processors", peerProcessor: true, wantIncomplete: true},
		{name: "initial retired processor claim", modern: true, peerProcessor: true, initialOnly: true, wantIncomplete: true},
		{name: "remote claims cannot invent expectations", modern: true, forgedRemote: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := apiProgressFixture().CapturedAt
			var progress *protocol.MessagingProgressSnapshot
			if tc.modern {
				progress = apiProgressFixture()
			}
			bundles := []remoteArchiveBundle{
				messagingArchiveCoverageBundle(t, "modern", IntervalSummaryRecord{Timestamp: base, TestID: "run", MessagingProgress: progress}),
				messagingArchiveCoverageBundle(t, "peer", IntervalSummaryRecord{Timestamp: base, TestID: "run"}),
			}
			manifest := &RunArchiveManifest{TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "summaries.jsonl"}
			if tc.modern {
				manifest.Provenance.GeneratorShards = append(manifest.Provenance.GeneratorShards, GeneratorShardSummary{Target: "modern", RuntimeResourceClaim: claim})
			}
			if tc.peerProcessor {
				shard := GeneratorShardSummary{Target: "peer", RuntimeResourceClaim: claim}
				if tc.initialOnly {
					manifest.controllerInitialProvenance = &RunArchiveProvenance{GeneratorShards: []GeneratorShardSummary{shard}}
				} else {
					manifest.Provenance.GeneratorShards = append(manifest.Provenance.GeneratorShards, shard)
				}
			}
			if tc.forgedRemote {
				bundles[1].manifest.Provenance.GeneratorShards = []GeneratorShardSummary{{Target: "peer", RuntimeResourceClaim: claim}}
				manifest.InitialProvenance = &RunArchiveProvenance{GeneratorShards: []GeneratorShardSummary{{Target: "peer", RuntimeResourceClaim: claim}}}
			}
			root := t.TempDir()
			if err := writeMergedRemoteArchiveFiles(root, manifest, bundles, nil); err != nil {
				t.Fatal(err)
			}
			for _, bundle := range bundles {
				if bundle.processorProgressExpected {
					t.Fatal("caller bundle mutated")
				}
			}
			rows := readMergedSummaries(t, filepath.Join(root, manifest.IntervalSummariesFile))
			if len(rows) != 1 || rows[0].TotalTx != 0 {
				t.Fatalf("standalone records: %+v", rows)
			}
			got := rows[0].MessagingProgress
			if got == nil || got.Incomplete != tc.wantIncomplete {
				t.Fatalf("qualification: %+v", got)
			}
			if tc.wantIncomplete && got.UnavailableReason != "source_missing" {
				t.Fatalf("missing peer reason: %+v", got)
			}
			analysis := messagingProgressAnalysis(got, "archive", "", "")
			for _, lag := range analysis.Lag {
				if tc.wantIncomplete && (lag.Delivered != nil || lag.Checkpoint != nil) {
					t.Fatal("missing processor evidence qualified backlog")
				}
			}
			if !tc.wantIncomplete && (len(analysis.Lag) != 1 || analysis.Lag[0].Delivered == nil) {
				t.Fatal("unrelated peer suppressed valid backlog")
			}
		})
	}
}

func TestMessagingProgressArchiveReleasedPeerStopsExpectation(t *testing.T) {
	base := apiProgressFixture().CapturedAt
	released := apiProgressFixture()
	released.Lifecycle = "released"
	released.Rows[0].Ownership = "released"
	second := apiProgressFixture()
	second.CapturedAt = base.Add(2 * time.Second)
	second.Rows[0].CapturedAt = second.CapturedAt
	second.Rows[0].CaptureRevision = "2"
	bundles := []remoteArchiveBundle{
		messagingArchiveCoverageBundle(t, "modern", IntervalSummaryRecord{Timestamp: base, TestID: "run", MessagingProgress: apiProgressFixture()}, IntervalSummaryRecord{Timestamp: second.CapturedAt, TestID: "run", MessagingProgress: second}),
		messagingArchiveCoverageBundle(t, "retired", IntervalSummaryRecord{Timestamp: base, TestID: "run", MessagingProgress: released}),
	}
	claim := &pb.RuntimeResourceClaim{Requested: &pb.RuntimeResourceDemand{Processors: 1}}
	manifest := &RunArchiveManifest{TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "summaries.jsonl", Provenance: RunArchiveProvenance{GeneratorShards: []GeneratorShardSummary{{Target: "modern", RuntimeResourceClaim: claim}, {Target: "retired", RuntimeResourceClaim: claim}}}}
	root := t.TempDir()
	if err := writeMergedRemoteArchiveFiles(root, manifest, bundles, nil); err != nil {
		t.Fatal(err)
	}
	rows := readMergedSummaries(t, filepath.Join(root, manifest.IntervalSummariesFile))
	if len(rows) != 2 || rows[1].MessagingProgress == nil || rows[1].MessagingProgress.Incomplete {
		t.Fatalf("released source remained expected: %+v", rows)
	}
}

func TestMessagingProgressArchiveRemoteInitialProvenanceIsNotAdmission(t *testing.T) {
	claim := &pb.RuntimeResourceClaim{Requested: &pb.RuntimeResourceDemand{Processors: 1}}
	initial := &RunArchiveProvenance{GeneratorShards: []GeneratorShardSummary{{Target: "peer", RuntimeResourceClaim: claim}}}
	for _, controllerOwnsInitial := range []bool{false, true} {
		test := &ManagedTest{ID: "run", CreatedAt: apiProgressFixture().CapturedAt}
		if controllerOwnsInitial {
			test.initialArchiveProvenance = initial
		}
		bundles := []remoteArchiveBundle{{target: "peer", manifest: RunArchiveManifest{InitialProvenance: initial}}}
		manifest := mergeRemoteArchiveManifest(nil, test, bundles, "COMPLETE", "", test.CreatedAt)
		if manifest.InitialProvenance == nil {
			t.Fatal("historical provenance compatibility lost")
		}
		bound := remoteArchiveBundlesWithProgressExpectations(&manifest, bundles)
		if bound[0].processorProgressExpected != controllerOwnsInitial {
			t.Fatalf("remote initial crossed controller admission boundary: %+v", bound)
		}
	}
}
