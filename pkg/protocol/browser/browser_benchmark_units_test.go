package browser

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
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
		{Engine: "chromium", Profile: "desktop_1080p", PeakRSSBytes: unitsBytes(250_000_000), RSSPopulation: "observed_test_process_descendants", RSSPartialSamples: 1},
		{Engine: "webkit", Profile: "iphone_15_pro", RSSPopulation: "observed_test_process_descendants", RSSUnavailableSamples: 2},
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
	if doc.Schema != "apexload.browser_benchmark.v2" || len(doc.Results) != 2 {
		t.Fatal(string(data))
	}
	if string(doc.Results[0]["peak_rss_bytes"]) != "250000000" || string(doc.Results[1]["peak_rss_bytes"]) != "null" {
		t.Fatal("byte value or unavailable state changed", string(data))
	}
	for _, result := range doc.Results {
		if _, ok := result["peak_rss_mb"]; ok {
			t.Fatal("ambiguous legacy MB field emitted")
		}
		if string(result["rss_population"]) != `"observed_test_process_descendants"` {
			t.Fatal("RSS population lost")
		}
	}
	if _, ok := doc.Host["memory_mb"]; ok {
		t.Fatal("ambiguous host memory unit emitted")
	}
	if got := benchmarkRSSNote(BrowserBenchmarkResult{PeakRSSBytes: unitsBytes(250_000_000)}); got != "238 MiB" {
		t.Fatal(got)
	}
	if got := benchmarkRSSNote(BrowserBenchmarkResult{}); got != "n/a" {
		t.Fatal(got)
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
