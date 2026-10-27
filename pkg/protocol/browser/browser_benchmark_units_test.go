package browser

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

func TestBrowserBenchmarkRSSPreservesRawBytesAndMissingness(t *testing.T) {
	for _, tc := range []struct {
		name    string
		values  []uint64
		bad     map[int]bool
		want    *uint64
		missing int
	}{
		{name: "no observed descendants"},
		{name: "all reads fail", values: []uint64{1, 2}, bad: map[int]bool{0: true, 1: true}, missing: 2},
		{name: "exact decimal target", values: []uint64{125_000_000, 125_000_000}, want: unitsBytes(250_000_000)},
		{name: "binary value is not decimal target", values: []uint64{250 << 20}, want: unitsBytes(262_144_000)},
		{name: "partial", values: []uint64{100, 200}, bad: map[int]bool{1: true}, want: unitsBytes(100), missing: 1},
		{name: "measured zero", values: []uint64{0}, want: unitsBytes(0)},
		{name: "overflow unavailable", values: []uint64{^uint64(0), 1}, missing: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ps := make([]*process.Process, len(tc.values))
			for i := range ps {
				ps[i] = &process.Process{Pid: int32(i)}
			}
			value, missing := benchmarkRSSFromProcesses(ps, func(p *process.Process) (uint64, error) {
				i := int(p.Pid)
				if tc.bad[i] {
					return 0, errors.New("unreadable")
				}
				return tc.values[i], nil
			})
			if missing != tc.missing || (value == nil) != (tc.want == nil) || value != nil && *value != *tc.want {
				t.Fatal(value, missing)
			}
		})
	}
}

func unitsBytes(value uint64) *uint64 { return &value }

func TestBrowserBenchmarkV2ArtifactKeepsRawMemoryPopulation(t *testing.T) {
	t.Setenv("APEXLOAD_COMMIT_SHA", "")
	dir := t.TempDir()
	writeBrowserBenchmark(t, dir, []BrowserBenchmarkResult{
		{
			Engine:             "chromium",
			Profile:            "desktop_1080p",
			ActionOverheadMs:   4.2,
			ActionOverheadKind: "click_evidence_capture_overhead",
			PerVUserRSSBytes:   unitsBytes(180_000_000),
			PerVUserPSSBytes:   unitsBytes(150_000_000),
			FixedOverheadBytes: unitsBytes(70_000_000),
			RSSAttribution:     BrowserRSSAttributionCDPRendererPerVUser,
			PeakRSSBytes:       unitsBytes(250_000_000),
			RSSPopulation:      "observed_test_process_descendants",
			RSSPartialSamples:  1,
		},
		{
			Engine:                "webkit",
			Profile:               "iphone_15_pro",
			ActionOverheadMs:      6.1,
			ActionOverheadKind:    "touch_over_click_baseline",
			RSSAttribution:        BrowserRSSAttributionUnavailable,
			RSSPopulation:         BrowserRSSAttributionUnavailable,
			RSSUnavailableSamples: 2,
		},
	})
	data, err := os.ReadFile(filepath.Join(dir, "browser-benchmark.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema  string                       `json:"schema"`
		Results []map[string]json.RawMessage `json:"results"`
		Host    map[string]json.RawMessage   `json:"host"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema != BrowserBenchmarkSchemaV3 || len(doc.Results) != 2 {
		t.Fatal(string(data))
	}
	if string(doc.Results[0]["per_vuser_rss_bytes"]) != "180000000" ||
		string(doc.Results[0]["per_vuser_pss_bytes"]) != "150000000" ||
		string(doc.Results[0]["fixed_overhead_bytes"]) != "70000000" ||
		string(doc.Results[0]["rss_attribution"]) != `"cdp_renderer_per_vuser"` ||
		string(doc.Results[0]["peak_rss_bytes"]) != "250000000" {
		t.Fatal("chromium v3 attribution fields changed", string(data))
	}
	if string(doc.Results[1]["per_vuser_rss_bytes"]) != "null" ||
		string(doc.Results[1]["fixed_overhead_bytes"]) != "null" ||
		string(doc.Results[1]["rss_attribution"]) != `"unavailable"` ||
		string(doc.Results[1]["peak_rss_bytes"]) != "null" {
		t.Fatal("webkit v3 unavailable fields changed", string(data))
	}
	for _, result := range doc.Results {
		if _, ok := result["peak_rss_mb"]; ok {
			t.Fatal("ambiguous legacy MB field emitted")
		}
	}
	if _, ok := doc.Host["memory_mb"]; ok {
		t.Fatal("ambiguous host memory_mb field emitted")
	}
	if _, ok := doc.Host["memory_mib"]; ok {
		t.Fatal("legacy host memory_mib field emitted")
	}
	if _, ok := doc.Host["memory_bytes"]; !ok {
		t.Fatal("host memory_bytes missing")
	}
	if _, err := parseBrowserBenchmarkArtifact(data); err != nil {
		t.Fatalf("parseBrowserBenchmarkArtifact(v3): %v", err)
	}
	v2Raw := []byte(`{"schema":"apexload.browser_benchmark.v2","generated_at":"2026-10-20T00:00:00Z","host":{"memory_bytes":8589934592},"results":[{"engine":"chromium","profile":"desktop_1080p","peak_rss_bytes":250000000,"rss_population":"observed_test_process_descendants"}]}`)
	v2Doc, err := parseBrowserBenchmarkArtifact(v2Raw)
	if err != nil || v2Doc.Schema != BrowserBenchmarkSchemaV2 || len(v2Doc.Results) != 1 || v2Doc.Results[0].PeakRSSBytes == nil || *v2Doc.Results[0].PeakRSSBytes != 250_000_000 {
		t.Fatalf("parseBrowserBenchmarkArtifact(v2) = %+v, %v", v2Doc, err)
	}
	if _, err := parseBrowserBenchmarkArtifact([]byte(`{"schema":"apexload.browser_benchmark.v1"}`)); err == nil {
		t.Fatal("expected unsupported v1 schema to fail")
	}
	if got := benchmarkRSSNote(BrowserBenchmarkResult{PerVUserRSSBytes: unitsBytes(180_000_000), PeakRSSBytes: unitsBytes(250_000_000)}); got != "172 MiB" {
		t.Fatal(got)
	}
	if got := benchmarkRSSNote(BrowserBenchmarkResult{PeakRSSBytes: unitsBytes(250_000_000)}); got != "238 MiB" {
		t.Fatal(got)
	}
	if got := benchmarkRSSNote(BrowserBenchmarkResult{}); got != "n/a" {
		t.Fatal(got)
	}
}

func TestParseCDPProcessInfoAndAttributeVUserRendererPIDs(t *testing.T) {
	raw := map[string]any{
		"processInfo": []any{
			map[string]any{"id": float64(100), "type": "browser", "cpuTime": 1.2},
			map[string]any{"id": float64(101), "type": "gpu", "cpuTime": 0.4},
			map[string]any{"id": float64(102), "type": "utility", "cpuTime": 0.1},
			map[string]any{"id": float64(103), "type": "zygote", "cpuTime": 0.0},
			map[string]any{"id": float64(201), "type": "renderer", "cpuTime": 0.5},
			map[string]any{"id": float64(202), "type": "renderer", "cpuTime": 0.6},
		},
	}
	classified, err := parseCDPProcessInfo(raw)
	if err != nil {
		t.Fatalf("parseCDPProcessInfo: %v", err)
	}
	if !classified.RendererPIDs[201] || !classified.RendererPIDs[202] || len(classified.RendererPIDs) != 2 {
		t.Fatalf("renderer PIDs = %+v", classified.RendererPIDs)
	}
	if !classified.SharedBrowserPIDs[100] || !classified.SharedBrowserPIDs[101] || !classified.SharedBrowserPIDs[102] || !classified.SharedBrowserPIDs[103] || len(classified.SharedBrowserPIDs) != 4 {
		t.Fatalf("shared browser PIDs = %+v", classified.SharedBrowserPIDs)
	}

	// Diffing against baseline renderer 201 isolates new context renderer 202,
	// while partitioning driver (90) + browser/gpu/utility/zygote into fixedPIDs.
	renderers, fixed, err := attributeVUserRendererPIDs(map[int32]bool{201: true}, classified, []int32{90, 100, 101, 201, 202}, 90, false)
	if err != nil {
		t.Fatalf("attributeVUserRendererPIDs: %v", err)
	}
	if !reflect.DeepEqual(renderers, []int32{202}) {
		t.Fatalf("attributed renderer PIDs = %v, want [202]", renderers)
	}
	if !reflect.DeepEqual(fixed, []int32{90, 100, 101, 102, 103}) {
		t.Fatalf("fixed overhead PIDs = %v, want [90 100 101 102 103]", fixed)
	}

	// Compute memory attribution with both RSS and Linux PSS readers, and
	// verify each unique PID is sampled once per attribution pass.
	rssMap := map[int32]uint64{
		90:  30_000_000,
		100: 80_000_000,
		101: 40_000_000,
		102: 20_000_000,
		103: 10_000_000,
		201: 95_000_000,
		202: 110_000_000,
	}
	pssMap := map[int32]uint64{
		202: 88_000_000,
	}
	readCalls := make(map[int32]int)
	sample := computeAttributedVUserMemory(
		map[int32]bool{201: true},
		classified,
		[]int32{90, 100, 101, 102, 103, 201, 202},
		90,
		false,
		func(pid int32) (uint64, error) {
			readCalls[pid]++
			v, ok := rssMap[pid]
			if !ok {
				return 0, errors.New("missing")
			}
			return v, nil
		},
		func(pid int32) (uint64, error) {
			v, ok := pssMap[pid]
			if !ok {
				return 0, errors.New("missing pss")
			}
			return v, nil
		},
	)
	for pid, count := range readCalls {
		if count != 1 {
			t.Fatalf("pid %d read %d times, want 1", pid, count)
		}
	}
	if sample.PerVUserRSSBytes == nil || *sample.PerVUserRSSBytes != 110_000_000 {
		t.Fatalf("PerVUserRSSBytes = %v, want 110000000", sample.PerVUserRSSBytes)
	}
	if sample.PerVUserPSSBytes == nil || *sample.PerVUserPSSBytes != 88_000_000 {
		t.Fatalf("PerVUserPSSBytes = %v, want 88000000", sample.PerVUserPSSBytes)
	}
	if sample.FixedOverheadBytes == nil || *sample.FixedOverheadBytes != 180_000_000 {
		t.Fatalf("FixedOverheadBytes = %v, want 180000000", sample.FixedOverheadBytes)
	}

	// Partial fixed-overhead read (e.g. short-lived utility PID 102 exits)
	// retains attributed renderer RSS + readable fixed overhead and increments
	// RSSPartialSamples when recorded.
	partialFixedSample := computeAttributedVUserMemory(
		map[int32]bool{201: true},
		classified,
		[]int32{90, 100, 101, 102, 103, 201, 202},
		90,
		false,
		func(pid int32) (uint64, error) {
			if pid == 102 {
				return 0, errors.New("exited utility process")
			}
			return rssMap[pid], nil
		},
		nil,
	)
	if partialFixedSample.PerVUserRSSBytes == nil || *partialFixedSample.PerVUserRSSBytes != 110_000_000 ||
		partialFixedSample.FixedOverheadBytes == nil || *partialFixedSample.FixedOverheadBytes != 160_000_000 ||
		partialFixedSample.MissingProcesses != 1 {
		t.Fatalf("partial fixed sample = %+v", partialFixedSample)
	}
	var benchRes BrowserBenchmarkResult
	benchRes.recordAttributedRSSSample(partialFixedSample)
	if benchRes.RSSSamples != 1 || benchRes.RSSPartialSamples != 1 || benchRes.RSSUnavailableSamples != 0 {
		t.Fatalf("expected partial fixed sample to increment RSSPartialSamples: %+v", benchRes)
	}

	// Partial/unreadable attributed renderer PID refuses per-VUser RSS and
	// increments RSSUnavailableSamples.
	unreadableRendererSample := computeAttributedVUserMemory(
		map[int32]bool{201: true},
		classified,
		[]int32{90, 100, 101, 102, 103, 201, 202},
		90,
		false,
		func(pid int32) (uint64, error) {
			if pid == 202 {
				return 0, errors.New("exited renderer process")
			}
			return rssMap[pid], nil
		},
		nil,
	)
	if unreadableRendererSample.PerVUserRSSBytes != nil || unreadableRendererSample.MissingProcesses != 1 {
		t.Fatalf("expected unreadable renderer to null PerVUserRSSBytes: %+v", unreadableRendererSample)
	}
	benchRes.recordAttributedRSSSample(unreadableRendererSample)
	if benchRes.RSSSamples != 2 || benchRes.RSSPartialSamples != 1 || benchRes.RSSUnavailableSamples != 1 {
		t.Fatalf("expected unreadable renderer sample to increment RSSUnavailableSamples: %+v", benchRes)
	}

	// Reject malformed CDP payloads.
	for _, bad := range []any{
		nil,
		map[string]any{},
		map[string]any{"processInfo": []any{}},
		map[string]any{"processInfo": []any{map[string]any{"id": float64(0), "type": "renderer"}}},
		map[string]any{"processInfo": []any{map[string]any{"id": float64(10), "type": ""}}},
	} {
		if _, err := parseCDPProcessInfo(bad); err == nil {
			t.Fatalf("expected error for malformed CDP payload %#v", bad)
		}
	}
}

func TestParseLinuxSmapsPSSBytes(t *testing.T) {
	rollup := "00400000-7fff0000 ---p 00000000 00:00 0 [rollup]\nRss:              120000 kB\nPss:               92500 kB\nShared_Clean:      27500 kB\n"
	pss, ok := parseLinuxSmapsPSSBytes(rollup)
	if !ok || pss != 92500*1024 {
		t.Fatalf("parseLinuxSmapsPSSBytes(rollup) = %d, %v; want %d, true", pss, ok, 92500*1024)
	}
	multi := "Pss: 100 kB\nPss: 250 kB\n"
	pssMulti, ok := parseLinuxSmapsPSSBytes(multi)
	if !ok || pssMulti != 350*1024 {
		t.Fatalf("parseLinuxSmapsPSSBytes(multi) = %d, %v; want %d, true", pssMulti, ok, 350*1024)
	}
	for _, bad := range []string{"", "Rss: 100 kB\n", "Pss: -5 kB\n", "Pss: 100 MB\n", "Pss:\n"} {
		if _, ok := parseLinuxSmapsPSSBytes(bad); ok {
			t.Fatalf("expected %q to fail PSS parse", bad)
		}
	}
}

func TestComputeMarginalRSSLinearRegressionAndActionOverhead(t *testing.T) {
	p1, p2, p3, p4, p5 := uint64(80_000_000), uint64(160_000_000), uint64(240_000_000), uint64(320_000_000), uint64(400_000_000)
	points := []marginalRSSPoint{
		{VUsers: 1, TotalRendererRSSBytes: 110_000_000, TotalRendererPSSBytes: &p1, FixedOverheadBytes: 180_000_000},
		{VUsers: 2, TotalRendererRSSBytes: 210_000_000, TotalRendererPSSBytes: &p2, FixedOverheadBytes: 182_000_000},
		{VUsers: 3, TotalRendererRSSBytes: 310_000_000, TotalRendererPSSBytes: &p3, FixedOverheadBytes: 184_000_000},
		{VUsers: 4, TotalRendererRSSBytes: 410_000_000, TotalRendererPSSBytes: &p4, FixedOverheadBytes: 185_000_000},
		{VUsers: 5, TotalRendererRSSBytes: 510_000_000, TotalRendererPSSBytes: &p5, FixedOverheadBytes: 186_000_000},
	}
	slopeRSS, interceptRSS, slopePSS := computeMarginalRSSLinearRegression(points)
	if math.Abs(slopeRSS-100_000_000) > 1 || math.Abs(interceptRSS-10_000_000) > 1 {
		t.Fatalf("regression = slope %.1f intercept %.1f, want 100000000 / 10000000", slopeRSS, interceptRSS)
	}
	if slopePSS == nil || math.Abs(*slopePSS-80_000_000) > 1 {
		t.Fatalf("PSS slope = %v, want 80000000", slopePSS)
	}
	desktopKind, desktopOverhead := computeBenchmarkActionOverhead(false, []float64{10, 12, 11}, []float64{14, 15, 16}, nil)
	if desktopKind != "click_evidence_capture_overhead" || math.Abs(desktopOverhead-4) > 1e-6 {
		t.Fatalf("desktop action overhead = %s %.2f", desktopKind, desktopOverhead)
	}
	mobileKind, mobileOverhead := computeBenchmarkActionOverhead(true, []float64{10, 12, 11}, nil, []float64{17, 18, 19})
	if mobileKind != "touch_over_click_baseline" || math.Abs(mobileOverhead-7) > 1e-6 {
		t.Fatalf("mobile action overhead = %s %.2f", mobileKind, mobileOverhead)
	}
}

func TestBrowserBenchmarkRSSSampleDenominatorIncludesUnavailable(t *testing.T) {
	var result BrowserBenchmarkResult
	result.recordRSSSample(nil, 2)
	result.recordRSSSample(unitsBytes(0), 0)
	value := uint64(250_000_000)
	result.recordRSSSample(&value, 1)
	value = 1
	result.recordRSSSample(unitsBytes(200_000_000), 0)
	if result.RSSSamples != 4 || result.RSSUnavailableSamples != 1 || result.RSSPartialSamples != 1 || result.PeakRSSBytes == nil || *result.PeakRSSBytes != 250_000_000 {
		t.Fatalf("sample accounting: %+v", result)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["rss_samples"] != float64(4) || fields["rss_unavailable_samples"] != float64(1) || fields["rss_partial_samples"] != float64(1) {
		t.Fatal("sample denominator lost", fields)
	}
}

func TestBrowserBenchmarkRSSRuntimeConversionRequiresAvailableFiniteSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mib       float64
		stamped   bool
		want      float64
		available bool
	}{
		{"missing timestamp", 1, false, 0, false},
		{"zero", 0, true, 0, false},
		{"negative", -1, true, 0, false},
		{"nan", math.NaN(), true, 0, false},
		{"infinite", math.Inf(1), true, 0, false},
		{"overflow", math.MaxFloat64, true, 0, false},
		{"one platform KiB", 1.0 / 1024, true, 1024, true},
		{"250 decimal MB", 250_000_000.0 / (1 << 20), true, 250_000_000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := browserProcessSnapshot{MemoryMB: tc.mib}
			if tc.stamped {
				snapshot.CapturedAt = time.Now()
			}
			got, available := benchmarkRuntimeRSSBytes(snapshot)
			if got != tc.want || available != tc.available {
				t.Fatal(got, available)
			}
		})
	}
}
