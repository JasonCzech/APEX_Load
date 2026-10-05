package api

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
)

// validityArchive is a closed-model run of n one-second intervals at one
// steady load, with a healthy sample from each generator per interval.
func validityArchive(n int, generators ...string) (*RunArchiveManifest, []IntervalSummaryRecord, []ResourceSample) {
	start := time.Date(2026, 10, 5, 13, 0, 0, 0, time.UTC)
	manifest := &RunArchiveManifest{Provenance: RunArchiveProvenance{StartedAt: &start,
		Workload: &rca.WorkloadKey{Version: rca.WorkloadKeyVersion, Source: rca.WorkloadKeyFrozen, Groups: map[string]rca.WorkloadGroup{"shop": {ArrivalModel: rca.ArrivalClosed, Unit: rca.LoadUnitVUsers, Levels: []float64{10}}}}}}
	for _, g := range generators {
		manifest.Provenance.GeneratorShards = append(manifest.Provenance.GeneratorShards, GeneratorShardSummary{Group: "shop", Target: g, VUsers: 5})
	}
	if len(generators) == 0 {
		generators = []string{""}
	}
	var summaries []IntervalSummaryRecord
	var resources []ResourceSample
	for i := 1; i <= n; i++ {
		at := start.Add(time.Duration(i) * time.Second)
		summaries = append(summaries, IntervalSummaryRecord{Timestamp: at, ActiveVUsers: 10, TPS: 100, TotalTx: 100, AvgResponseMs: 40, SampleWindowSeconds: 1,
			Telemetry: &TelemetryHealth{DroppedResultsAvailable: true}})
		for _, g := range generators {
			resources = append(resources, ResourceSample{Timestamp: at, Generator: g, CPUProcessPct: 35, CPUFreePct: 60, ContainerCPUPeriodsDelta: 10})
		}
	}
	// A capture total row carries no generator evidence.
	resources = append(resources, ResourceSample{Timestamp: start.Add(time.Duration(n) * time.Second), External: map[string]float64{"elk.errors": 3}})
	return manifest, summaries, resources
}

// judgeArchive judges an archive whose result counts are unrecorded.
func judgeArchive(manifest *RunArchiveManifest, summaries []IntervalSummaryRecord, resources []ResourceSample) *rca.Validity {
	return archiveRunValidity(manifest, archiveValidityIntervals(manifest, summaries), summaries, resources)
}

func TestArchiveValidityJudgesSteadyIntervals(t *testing.T) {
	manifest, summaries, resources := validityArchive(120)
	if v := judgeArchive(manifest, summaries, resources); v.Verdict != rca.ValidityValid || v.IntervalsJudged == 0 || v.IntervalsJudged >= 120 || containsString(v.Limitations, rca.LimitationNoSteadyLevel) {
		t.Fatalf("healthy run: %+v", v)
	}
	// Three saturated seconds inside the steady level make it suspect.
	for i := 60; i < 63; i++ {
		resources[i].CPUProcessPct = 91
	}
	v := judgeArchive(manifest, summaries, resources)
	if v.Verdict != rca.ValiditySuspect || len(v.Windows) != 3 || v.Windows[0].Findings[0].Rule != rca.RuleGeneratorCPU || v.Windows[0].Findings[0].Value != 91 {
		t.Fatalf("saturated run: %+v", v)
	}
	// Dropped results are a cumulative counter.
	for i := 80; i < len(summaries); i++ {
		summaries[i].Telemetry.DroppedResults = 5
	}
	if v := judgeArchive(manifest, summaries, resources); v.Verdict != rca.ValidityInvalid || len(v.Reasons) != 1 || v.Reasons[0] != rca.RuleDroppedResults {
		t.Fatalf("dropped results: %+v", v)
	}
}

func TestArchiveValidityUsesGeneratorIdentity(t *testing.T) {
	manifest, summaries, resources := validityArchive(120, "gen-a:7070", "gen-b:7070")
	manifest.Provenance.GeneratorShards = append(manifest.Provenance.GeneratorShards, GeneratorShardSummary{Group: "shop", Target: "gen-c:7070", VUsers: 5})
	v := judgeArchive(manifest, summaries, resources)
	if v.Verdict != rca.ValidityUnknown || v.MissingFraction != 1 || v.Reasons[0] != rca.RuleGeneratorStatsMissing {
		t.Fatalf("generator that never reported: %+v", v)
	}
	manifest.Provenance.GeneratorShards = manifest.Provenance.GeneratorShards[:2]
	manifest.SourceCoverage = &RunArchiveSourceCoverage{Mode: "partial", Sources: []RunArchiveSource{{Target: "gen-a:7070", Collected: true}, {Target: "gen-b:7070", Collected: false}}}
	if v := judgeArchive(manifest, summaries, resources); v.Verdict != rca.ValidityUnknown || !containsString(v.Limitations, validityGapGeneratorArchiveMissing) {
		t.Fatalf("uncollected generator archive: %+v", v)
	}
	// Archives merged before samples named their generator are one pool.
	manifest.SourceCoverage = nil
	for i := range resources {
		resources[i].Generator = ""
	}
	if v := judgeArchive(manifest, summaries, resources); v.Verdict != rca.ValidityValid || !containsString(v.Limitations, rca.LimitationGeneratorIdentityUnrecorded) {
		t.Fatalf("unidentified samples: %+v", v)
	}
}

func TestArchiveReportCarriesValidity(t *testing.T) {
	server, store := baselineTestServer(t)
	record := exemplarArchiveFixture(t, server, store, "validity-run", nil)
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatal(err)
	}
	// The fixture archives no generator samples, so nothing can be judged.
	if report.Validity == nil || report.Validity.Verdict != rca.ValidityUnknown || report.Validity.RulesVersion != rca.ValidityRulesVersion {
		t.Fatalf("report validity: %+v", report.Validity)
	}
}

func TestRemoteResourceMergeNamesTheGenerator(t *testing.T) {
	root := t.TempDir()
	var bundles []remoteArchiveBundle
	for _, target := range []string{"gen-a:7070", "gen-b:7070"} {
		path := filepath.Join(root, strings.ReplaceAll(target, ":", "_")+".jsonl")
		rows := `{"timestamp":"2026-10-05T13:00:01Z","cpu_process_pct":40,"cpu_free_pct":50,"mem_process_mb":1,"mem_free_mb":1,"net_sent_kbps":0,"net_recv_kbps":0,"disk_read_kbps":0,"disk_write_kbps":0,"container_cpu_periods_delta":10,"future_field":7}` + "\n"
		if target == "gen-b:7070" {
			rows = `{"timestamp":"2026-10-05T13:00:01Z","generator":"already-named","cpu_process_pct":20}` + "\n"
		}
		if err := os.WriteFile(path, []byte(rows), 0o600); err != nil {
			t.Fatal(err)
		}
		bundle := remoteArchiveBundle{target: target, resourcesPath: path}
		bundle.manifest.ResourceSampleCount = 1
		bundles = append(bundles, bundle)
	}
	merged := filepath.Join(root, "resources.jsonl")
	if err := mergeRemoteArchiveDatasetWith(merged, bundles, "resource sample", func(b remoteArchiveBundle) string { return b.resourcesPath }, func(b remoteArchiveBundle) int64 { return b.manifest.ResourceSampleCount }, stampRemoteResourceGenerator); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(merged)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	var first, second ResourceSample
	var raw map[string]any
	if len(lines) != 2 || json.Unmarshal([]byte(lines[0]), &first) != nil || json.Unmarshal([]byte(lines[1]), &second) != nil || json.Unmarshal([]byte(lines[0]), &raw) != nil {
		t.Fatalf("merged rows: %s", data)
	}
	if first.Generator != "gen-a:7070" || first.ContainerCPUPeriodsDelta != 10 || raw["future_field"] != float64(7) || second.Generator != "already-named" {
		t.Fatalf("stamped rows: %+v %+v %v", first, second, raw)
	}
}

func TestArchiveValidityDroppedShareCountsEveryResult(t *testing.T) {
	manifest, summaries, resources := validityArchive(120)
	// Each interval had 100 business transactions out of 2,000 results; one
	// result was dropped in second 80.
	for i := 79; i < len(summaries); i++ {
		summaries[i].Telemetry.DroppedResults = 1
	}
	counter := newValidityResultCounter(archiveValidityIntervals(manifest, summaries))
	for _, summary := range summaries {
		for r := 0; r < 2000; r++ {
			counter.add(summary.Timestamp.Add(-time.Duration(r) * time.Millisecond / 4))
		}
	}
	counter.add(summaries[0].Timestamp.Add(-time.Hour)) // before the run: no interval
	if v := archiveRunValidity(manifest, counter.counted(), summaries, resources); v.Verdict != rca.ValidityValid {
		t.Fatalf("one of 2,001 results dropped: %+v", v)
	}
	if v := judgeArchive(manifest, summaries, resources); v.Verdict != rca.ValidityInvalid {
		t.Fatalf("against transactions alone the share is 1%%: %+v", v)
	}
}

func TestArchiveSteadyLevelsFollowTheArrivalModel(t *testing.T) {
	manifest, summaries, _ := validityArchive(240)
	started := archiveRunStarted(manifest)
	// An open model holds 100 req/s throughout while its VUsers float.
	for i := range summaries {
		summaries[i].TargetTPS = 100
		summaries[i].ActiveVUsers = int64(10 + 4*(i%3))
	}
	if closed := archiveSteadyLevels(summaries, started, rca.ArrivalClosed); len(closed) != 0 {
		t.Fatalf("floating VUsers made a steady level: %v", closed)
	}
	open := archiveSteadyLevels(summaries, started, rca.ArrivalOpen)
	if len(open) != 1 || open[0].End.Sub(open[0].Start) < 200*time.Second {
		t.Fatalf("open-model level: %v", open)
	}
	if mixed := archiveSteadyLevels(summaries, started, rca.ArrivalMixed); len(mixed) != 0 {
		t.Fatalf("a mixed run needs both measures steady: %v", mixed)
	}
	a := []rca.LoadLevel{{Start: started, End: started.Add(100 * time.Second), VUsers: 10}, {Start: started.Add(150 * time.Second), End: started.Add(300 * time.Second), VUsers: 20}}
	b := []rca.LoadLevel{{Start: started.Add(50 * time.Second), End: started.Add(200 * time.Second), Rate: 100}}
	got := intersectLoadLevels(a, b)
	if len(got) != 2 || !got[0].Start.Equal(started.Add(50*time.Second)) || !got[1].End.Equal(started.Add(200*time.Second)) ||
		got[0].VUsers != 10 || got[1].VUsers != 20 || got[0].Rate != 100 || got[1].Rate != 100 {
		t.Fatalf("intersection: %v", got)
	}
}

// A distributed run judged from merged generator archives: the merge stamps
// each sample's generator and the shard summaries name who should report.
func TestArchiveValidityFromMergedGeneratorArchives(t *testing.T) {
	manifest, summaries, local := validityArchive(120)
	root := t.TempDir()
	var bundles []remoteArchiveBundle
	for _, target := range []string{"gen-a:7070", "gen-b:7070"} {
		var lines []byte
		for _, sample := range local[:len(local)-1] {
			sample.Generator = ""
			if target == "gen-b:7070" && sample.Timestamp.Sub(*manifest.Provenance.StartedAt) > 60*time.Second && sample.Timestamp.Sub(*manifest.Provenance.StartedAt) <= 64*time.Second {
				sample.CPUProcessPct = 97
			}
			row, _ := json.Marshal(sample)
			lines = append(append(lines, row...), '\n')
		}
		path := filepath.Join(root, strings.ReplaceAll(target, ":", "_")+".jsonl")
		if err := os.WriteFile(path, lines, 0o600); err != nil {
			t.Fatal(err)
		}
		bundle := remoteArchiveBundle{target: target, resourcesPath: path}
		bundle.manifest.ResourceSampleCount = int64(len(local) - 1)
		bundles = append(bundles, bundle)
	}
	merged := filepath.Join(root, "resources.jsonl")
	if err := mergeRemoteArchiveDatasetWith(merged, bundles, "resource sample", func(b remoteArchiveBundle) string { return b.resourcesPath }, func(b remoteArchiveBundle) int64 { return b.manifest.ResourceSampleCount }, stampRemoteResourceGenerator); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(merged)
	if err != nil {
		t.Fatal(err)
	}
	var resources []ResourceSample
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var sample ResourceSample
		if err := json.Unmarshal([]byte(line), &sample); err != nil {
			t.Fatal(err)
		}
		resources = append(resources, sample)
	}
	// Only gen-a is in the shard summaries; gen-b reported under its own name.
	manifest.Provenance.GeneratorShards = []GeneratorShardSummary{{Group: "shop", Target: "gen-a:7070", VUsers: 5}}
	v := judgeArchive(manifest, summaries, resources)
	if v.Verdict != rca.ValiditySuspect || v.Windows[0].Findings[0].Generator != "gen-b:7070" || containsString(v.Limitations, rca.LimitationGeneratorIdentityUnrecorded) {
		t.Fatalf("merged generator archives: %+v", v)
	}
}

// The report counts the archive's result rows per interval before judging,
// so the dropped share is against every result.
func TestArchiveReportCountsResultsForTheDroppedShare(t *testing.T) {
	server, store := baselineTestServer(t)
	record := exemplarArchiveFixture(t, server, store, "validity-results", nil)
	path := filepath.Join(record.ArchivePath, "interval_summaries.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for i, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var summary IntervalSummaryRecord
		if err := json.Unmarshal([]byte(line), &summary); err != nil {
			t.Fatal(err)
		}
		// Five completed transactions per ten-second interval, but ten
		// archived results; one result dropped in interval 20.
		summary.TotalTx = 5
		summary.Telemetry = &TelemetryHealth{DroppedResultsAvailable: true}
		if i >= 19 {
			summary.Telemetry.DroppedResults = 1
		}
		row, _ := json.Marshal(summary)
		out = append(append(out, row...), '\n')
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatal(err)
	}
	v := report.Validity
	if v == nil || v.Verdict != rca.ValidityInvalid {
		t.Fatalf("validity: %+v", v)
	}
	var share float64
	for _, window := range v.Windows {
		for _, f := range window.Findings {
			if f.Rule == rca.RuleDroppedResults {
				share = f.Value
			}
		}
	}
	if math.Abs(share-1.0/11) > 1e-9 {
		t.Fatalf("dropped share %v, want 1/11 against ten archived results", share)
	}
}
