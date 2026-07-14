package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type downsampleFixture struct {
	Index int     `json:"index"`
	Value float64 `json:"value"`
}

func TestArchivePublicSampleLimitCoversEveryCriticalScoreDimension(t *testing.T) {
	for name, scoreCount := range map[string]int{
		"resources": len(archiveResourceSampleScores()),
		"summaries": len(archiveIntervalSummaryScores()),
	} {
		required := 2 + 2*scoreCount
		if minArchiveSampleLimit < required {
			t.Fatalf("public sample minimum %d cannot preserve endpoints and all %s extrema; need at least %d", minArchiveSampleLimit, name, required)
		}
	}

	belowMinimum := httptest.NewRequest(http.MethodGet, "/archive?sample_limit="+strconv.Itoa(minArchiveSampleLimit-1), nil)
	belowMinimumResponse := httptest.NewRecorder()
	if _, ok := parseArchiveSampleLimit(belowMinimumResponse, belowMinimum); ok || belowMinimumResponse.Code != http.StatusBadRequest {
		t.Fatalf("sample_limit below public minimum accepted: ok=%v status=%d", ok, belowMinimumResponse.Code)
	}
	minimum := httptest.NewRequest(http.MethodGet, "/archive?sample_limit="+strconv.Itoa(minArchiveSampleLimit), nil)
	minimumResponse := httptest.NewRecorder()
	if parsed, ok := parseArchiveSampleLimit(minimumResponse, minimum); !ok || parsed != minArchiveSampleLimit {
		t.Fatalf("public minimum rejected: parsed=%d ok=%v status=%d", parsed, ok, minimumResponse.Code)
	}
}

func TestReadJSONLDownsampledPreservesEndpointsAndExtrema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for index := 0; index < 100; index++ {
		value := 100 + float64(index%7)
		if index == 37 {
			value = 10_000
		}
		if index == 62 {
			value = -10_000
		}
		if err := encoder.Encode(downsampleFixture{Index: index, Value: value}); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	items, total, err := readJSONLDownsampled(path, 100, 10, func(item *downsampleFixture) float64 {
		return item.Value
	})
	if err != nil {
		t.Fatal(err)
	}
	if total != 100 {
		t.Fatalf("total = %d, want 100", total)
	}
	if len(items) > 10 {
		t.Fatalf("returned %d items, limit 10", len(items))
	}
	seen := make(map[int]bool, len(items))
	for _, item := range items {
		seen[item.Index] = true
	}
	for _, required := range []int{0, 37, 62, 99} {
		if !seen[required] {
			t.Fatalf("expected sampled indexes to retain %d; got %#v", required, seen)
		}
	}
}

func TestReadJSONLDownsampledRejectsInvalidLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readJSONLDownsampled[downsampleFixture](path, 1, 1); err == nil {
		t.Fatal("expected a sample-limit error")
	}
}

func TestReadJSONLDownsampledSmallLimitStillPreservesEndpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "samples.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	for index := 0; index < 10; index++ {
		if err := encoder.Encode(downsampleFixture{Index: index, Value: float64(index)}); err != nil {
			file.Close()
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	items, _, err := readJSONLDownsampled(path, 10, 2, func(item *downsampleFixture) float64 {
		return item.Value
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Index != 0 || items[1].Index != 9 {
		t.Fatalf("items = %#v, want indexes 0 and 9", items)
	}
}

func TestLoadArchiveResourceSamplesDownsampledPreservesIndependentContainerSpikes(t *testing.T) {
	archiveDir := t.TempDir()
	start := time.Date(2026, 7, 14, 14, 0, 0, 0, time.UTC)
	manifest := RunArchiveManifest{
		SchemaVersion:       archiveSchemaVersion,
		TestID:              "resource-extrema",
		ResourceSamplesFile: "resource_samples.jsonl",
		ResourceSampleCount: 100,
	}
	manifestPath := filepath.Join(archiveDir, runArchiveManifestFileName)
	writeJSONL(t, manifestPath, manifest)
	values := make([]any, 100)
	for index := range values {
		sample := ResourceSample{
			Timestamp:               start.Add(time.Duration(index) * time.Second),
			TestID:                  manifest.TestID,
			MemProcessMB:            4096,
			ContainerMemoryUsagePct: 20,
		}
		if index == 37 {
			sample.ContainerOOMKillCountDelta = 1
		}
		if index == 62 {
			sample.ContainerMemoryUsagePct = 99
		}
		values[index] = sample
	}
	writeJSONL(t, filepath.Join(archiveDir, manifest.ResourceSamplesFile), values...)
	record := &storage.RunArchiveRecord{TestID: manifest.TestID, ArchivePath: archiveDir, ManifestPath: manifestPath}

	items, total, err := loadArchiveResourceSamplesDownsampled(record, minArchiveSampleLimit)
	if err != nil {
		t.Fatalf("loadArchiveResourceSamplesDownsampled returned error: %v", err)
	}
	if total != 100 || len(items) > minArchiveSampleLimit {
		t.Fatalf("sampled resources total=%d len=%d, want total 100 and at most %d items", total, len(items), minArchiveSampleLimit)
	}
	var sawOOM, sawContainerMemorySpike bool
	for _, item := range items {
		sawOOM = sawOOM || item.ContainerOOMKillCountDelta == 1
		sawContainerMemorySpike = sawContainerMemorySpike || item.ContainerMemoryUsagePct == 99
	}
	if !sawOOM || !sawContainerMemorySpike {
		t.Fatalf("sampled resources omitted independent extrema: OOM=%v container-memory=%v items=%#v", sawOOM, sawContainerMemorySpike, items)
	}
}

func TestLoadArchiveIntervalSummariesDownsampledPreservesErrorRateSpike(t *testing.T) {
	archiveDir := t.TempDir()
	start := time.Date(2026, 7, 14, 15, 0, 0, 0, time.UTC)
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "summary-error-extrema",
		IntervalSummariesFile: "interval_summaries.jsonl",
		IntervalSummaryCount:  100,
	}
	manifestPath := filepath.Join(archiveDir, runArchiveManifestFileName)
	writeJSONL(t, manifestPath, manifest)
	values := make([]any, 100)
	for index := range values {
		summary := IntervalSummaryRecord{
			Timestamp:   start.Add(time.Duration(index) * time.Second),
			TestID:      manifest.TestID,
			TPS:         100,
			TotalTx:     int64((index + 1) * 1000),
			TotalErrors: int64(index * 10),
			ErrorPct:    1,
		}
		if index == 37 {
			summary.ErrorPct = 100
		}
		if index == 62 {
			summary.TotalErrors = 10_000
		}
		values[index] = summary
	}
	writeJSONL(t, filepath.Join(archiveDir, manifest.IntervalSummariesFile), values...)
	record := &storage.RunArchiveRecord{TestID: manifest.TestID, ArchivePath: archiveDir, ManifestPath: manifestPath}

	items, total, err := loadArchiveIntervalSummariesDownsampled(record, minArchiveSampleLimit)
	if err != nil {
		t.Fatalf("loadArchiveIntervalSummariesDownsampled returned error: %v", err)
	}
	if total != 100 || len(items) > minArchiveSampleLimit {
		t.Fatalf("sampled summaries total=%d len=%d, want total 100 and at most %d items", total, len(items), minArchiveSampleLimit)
	}
	var sawErrorRateSpike, sawTotalErrorsSpike bool
	for _, item := range items {
		if item.ErrorPct == 100 {
			sawErrorRateSpike = true
		}
		if item.TotalErrors == 10_000 {
			sawTotalErrorsSpike = true
		}
	}
	if !sawErrorRateSpike || !sawTotalErrorsSpike {
		t.Fatalf("sampled summaries omitted independent error extrema: rate=%v total=%v items=%#v", sawErrorRateSpike, sawTotalErrorsSpike, items)
	}
}

func TestLoadArchiveIntervalSummariesForFiltersDownsampledReturnsBoundedSeries(t *testing.T) {
	archiveDir := t.TempDir()
	start := time.Date(2026, 7, 14, 16, 0, 0, 0, time.UTC)
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "filtered-summary-sample",
		TransactionsFile:      "transactions.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		IntervalSummaryCount:  40,
	}
	manifestPath := filepath.Join(archiveDir, runArchiveManifestFileName)
	writeJSONL(t, manifestPath, manifest)
	summaries := make([]any, 40)
	transactions := make([]any, 0, 80)
	for index := range summaries {
		timestamp := start.Add(time.Duration(index) * time.Second)
		summaries[index] = IntervalSummaryRecord{
			Timestamp:    timestamp,
			TestID:       manifest.TestID,
			ActiveVUsers: 10,
			Groups: map[string]*GroupLiveMetrics{
				"Checkout": {Name: "Checkout", VUsers: 4},
				"Browse":   {Name: "Browse", VUsers: 6},
			},
		}
		transactions = append(transactions,
			TransactionEvent{Timestamp: timestamp, TestID: manifest.TestID, Group: "Checkout", ActionName: "submit", DurationMs: 100, Success: index != 17},
			TransactionEvent{Timestamp: timestamp, TestID: manifest.TestID, Group: "Browse", ActionName: "browse", DurationMs: 900, Success: true},
		)
	}
	writeJSONL(t, filepath.Join(archiveDir, manifest.IntervalSummariesFile), summaries...)
	writeJSONL(t, filepath.Join(archiveDir, manifest.TransactionsFile), transactions...)
	record := &storage.RunArchiveRecord{TestID: manifest.TestID, ArchivePath: archiveDir, ManifestPath: manifestPath}

	items, total, err := loadArchiveIntervalSummariesForFiltersDownsampled(
		record,
		minArchiveSampleLimit,
		"",
		groupFilterQuery{Names: []string{"Checkout"}, Explicit: true},
	)
	if err != nil {
		t.Fatalf("loadArchiveIntervalSummariesForFiltersDownsampled returned error: %v", err)
	}
	if total != 40 || len(items) > minArchiveSampleLimit {
		t.Fatalf("filtered sample total=%d len=%d, want total 40 and at most %d items", total, len(items), minArchiveSampleLimit)
	}
	var sawFailureInterval bool
	for _, item := range items {
		if len(item.Groups) != 1 || item.Groups["Checkout"] == nil {
			t.Fatalf("sample contains groups outside the Checkout filter: %#v", item.Groups)
		}
		if item.ErrorPct == 100 {
			sawFailureInterval = true
		}
	}
	if !sawFailureInterval {
		t.Fatalf("filtered sample omitted the selected group's failure interval: %#v", items)
	}
}
