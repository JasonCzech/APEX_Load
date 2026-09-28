package api

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/internal/diskspace"
	"github.com/jasonczech/apexload/pkg/protocol"
	local "github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/reader"
)

func TestArchiveConversionConfigurationFrozenAndValidated(t *testing.T) {
	for _, key := range []string{envArchiveConversionMaxBytes, envArchiveConversionMinFreeBytes} {
		for _, value := range []string{"0", "-1", "1e3", "x", "1.5", "1125899906842625"} {
			t.Run(key+value, func(t *testing.T) {
				t.Setenv(key, value)
				if _, err := newArchiveConversionBudget(t.TempDir()); err == nil {
					t.Fatal("invalid configuration accepted")
				}
			})
		}
	}
	t.Setenv(envArchiveConversionMaxBytes, "7")
	t.Setenv(envArchiveConversionMinFreeBytes, "1")
	b, err := newArchiveConversionBudget(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envArchiveConversionMaxBytes, "999")
	if b.remaining != 7 || b.minFree != 1 {
		t.Fatal("configuration changed after admission")
	}
	if _, err := conversionBudget(b.root, []*archiveOutputBudget{nil}); err == nil {
		t.Fatal("nil shared budget accepted")
	}
}

func TestArchiveConversionConcurrentFilesShareBoundary(t *testing.T) {
	root := t.TempDir()
	b := &archiveOutputBudget{limitErr: errArchiveConversionLimit, freeSpaceErr: errArchiveConversionFreeSpace, root: root, remaining: 100, minFree: 1}
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for range 10 {
		f, err := os.CreateTemp(root, "output")
		if err != nil {
			t.Fatal(err)
		}
		wg.Go(func() {
			defer f.Close()
			w := &archiveOutputFile{file: f, budget: b}
			for range 10 {
				n, err := w.Write([]byte("xx"))
				accepted.Add(int64(n))
				if err != nil && !errors.Is(err, errArchiveConversionLimit) {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 100 || b.remaining != 0 || !errors.Is(b.failure(), errArchiveConversionLimit) {
		t.Fatal(accepted.Load(), b.remaining, b.failure())
	}
}

func TestArchiveConversionPhysicalFailureLatchesAcrossFiles(t *testing.T) {
	root := t.TempDir()
	f, err := os.CreateTemp(root, "first")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	free, err := diskspace.AvailableFile(f)
	if err != nil {
		t.Fatal(err)
	}
	b := &archiveOutputBudget{limitErr: errArchiveConversionLimit, freeSpaceErr: errArchiveConversionFreeSpace, root: root, remaining: 100, minFree: free + 1<<30}
	w := &archiveOutputFile{file: f, budget: b}
	if n, err := w.Write([]byte("no")); n != 0 || !errors.Is(err, errArchiveConversionFreeSpace) {
		t.Fatal(n, err)
	}
	b.minFree = 1
	other, err := os.CreateTemp(root, "second")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if n, err := (&archiveOutputFile{file: other, budget: b}).Write([]byte("still no")); n != 0 || !errors.Is(err, errArchiveConversionFreeSpace) {
		t.Fatal("failure not latched", n, err)
	}
	for _, file := range []*os.File{f, other} {
		if info, err := file.Stat(); err != nil || info.Size() != 0 {
			t.Fatal("denied output was written")
		}
	}
}

func conversionFixture(t *testing.T) (string, *RunArchiveManifest) {
	t.Helper()
	root := t.TempDir()
	m := &RunArchiveManifest{CompletedAt: time.Now(), TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "summaries.jsonl", TransactionsParquetFile: "transactions.parquet", ResourceSamplesParquetFile: "resources.parquet", IntervalSummariesParquetFile: "summaries.parquet"}
	for _, name := range []string{m.TransactionsFile, m.ResourceSamplesFile, m.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return root, m
}

func TestArchiveConversionParquetHeaderFooterAndSharedFiles(t *testing.T) {
	for _, limit := range []int64{3, 4} {
		t.Run(strconv.FormatInt(limit, 10), func(t *testing.T) {
			root, m := conversionFixture(t)
			b := &archiveOutputBudget{limitErr: errArchiveConversionLimit, freeSpaceErr: errArchiveConversionFreeSpace, root: root, remaining: limit, minFree: 1}
			err := writeArchiveParquetFiles(root, m, b)
			if !errors.Is(err, errArchiveConversionLimit) {
				t.Fatal(err)
			}
			info, err := os.Stat(filepath.Join(root, m.TransactionsParquetFile))
			if err != nil || info.Size() > limit {
				t.Fatal("wrote beyond conversion limit", info, err)
			}
		})
	}
	root, m := conversionFixture(t)
	if err := writeArchiveParquetFiles(root, m); err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, name := range []string{m.TransactionsParquetFile, m.ResourceSamplesParquetFile, m.IntervalSummariesParquetFile} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		total += info.Size()
		f, err := local.NewLocalFileReader(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		r, err := reader.NewParquetReader(f, nil, 1)
		if err != nil {
			f.Close()
			t.Fatal(err)
		}
		if r.GetNumRows() != 0 {
			t.Fatal("unexpected rows")
		}
		r.ReadStop()
		f.Close()
	}
	root, m = conversionFixture(t)
	b := &archiveOutputBudget{limitErr: errArchiveConversionLimit, freeSpaceErr: errArchiveConversionFreeSpace, root: root, remaining: total - 1, minFree: 1}
	if err := writeArchiveParquetFiles(root, m, b); !errors.Is(err, errArchiveConversionLimit) {
		t.Fatal("three files did not share quota", err)
	}
}

func TestArchiveConversionCSVUsesRemainingParquetAllowance(t *testing.T) {
	root, m := conversionFixture(t)
	b := &archiveOutputBudget{limitErr: errArchiveConversionLimit, freeSpaceErr: errArchiveConversionFreeSpace, root: root, remaining: 1 << 20, minFree: 1}
	if err := writeArchiveParquetFiles(root, m, b); err != nil {
		t.Fatal(err)
	}
	used := int64(1<<20) - b.remaining
	if used <= 0 {
		t.Fatal("Parquet bytes not charged")
	}
	// Empty source still requires the CSV header. Leave less than its size.
	b.remaining = 1
	m.Provenance.CSVExports = []RunArchiveCSVExport{{Group: "users", Export: dsl.CSVExport{ID: "trace", FileName: "trace-results.csv", ActionName: "sample", Columns: []dsl.CSVExportColumn{{Name: "latencyMs", Metric: "latency_ms"}}}}}
	second := m.Provenance.CSVExports[0]
	second.Export.ID = "second"
	second.Export.FileName = "second-results.csv"
	m.Provenance.CSVExports = append(m.Provenance.CSVExports, second)
	if err := materializeDeclaredCSVExports(root, m, b); !errors.Is(err, errArchiveConversionLimit) {
		t.Fatal(err)
	}
	if len(m.CSVExports) != 1 || m.CSVExports[0].Status != "incomplete" {
		t.Fatal(m.CSVExports)
	}
	if _, err := os.Stat(filepath.Join(root, "trace-results.csv")); !os.IsNotExist(err) {
		t.Fatal("partial CSV retained as output", err)
	}
	if data, err := os.ReadFile(filepath.Join(root, m.TransactionsFile)); err != nil || len(data) != 0 {
		t.Fatal("source changed", err)
	}
}

func TestArchiveConversionFailurePreservesRawSourceAndOutcome(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envArchiveConversionMaxBytes, "3")
	t.Setenv(envArchiveConversionMinFreeBytes, "1")
	run := &ManagedTest{ID: "conversion-limit"}
	w, err := newRunArchiveWriter(run, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envArchiveConversionMaxBytes, "999999") // The admitted writer keeps its limit.
	if err := w.RecordResult(&protocol.Result{ActionName: "preserve"}); err != nil {
		t.Fatal(err)
	}
	record, status, why := (&runArtifacts{recorder: w}).finalize(run, "COMPLETE", time.Now(), "")
	if record != nil || status != "COMPLETE" || why != "" {
		t.Fatal(record, status, why)
	}
	if _, err := os.Stat(w.manifestPath); !os.IsNotExist(err) {
		t.Fatal("published false complete manifest", err)
	}
	if data, err := os.ReadFile(filepath.Join(w.rootDir, "transactions.jsonl")); err != nil || len(data) == 0 {
		t.Fatal("lost raw source", err)
	}
}

func TestArchiveConversionParquetRejectsSymlinkOutput(t *testing.T) {
	root, m := conversionFixture(t)
	target := filepath.Join(t.TempDir(), "evidence")
	if err := os.WriteFile(target, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(root, m.TransactionsParquetFile)); err != nil {
		t.Skip(err)
	}
	if err := writeArchiveParquetFiles(root, m); err == nil {
		t.Fatal("linked output accepted")
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "preserve" {
		t.Fatal("linked source was modified", err)
	}
}

func TestArchiveConversionFinalizeSharesParquetAndCSVLimit(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envArchiveConversionMinFreeBytes, "1")
	provenance := RunArchiveProvenance{CSVExports: []RunArchiveCSVExport{{Group: "users", Export: dsl.CSVExport{ID: "latency", FileName: "latency-results.csv", ActionName: "sample", Columns: []dsl.CSVExportColumn{{Name: "latencyMs", Metric: "latency_ms"}}}}}}
	baseline, err := newRunArchiveWriter(&ManagedTest{ID: "conversion-baseline"}, provenance)
	if err != nil {
		t.Fatal(err)
	}
	record, err := baseline.Finalize("COMPLETE", time.Now(), "", false, false, nil)
	if err != nil || record == nil {
		t.Fatal(record, err)
	}
	var parquetBytes int64
	for _, name := range []string{baseline.manifest.TransactionsParquetFile, baseline.manifest.ResourceSamplesParquetFile, baseline.manifest.IntervalSummariesParquetFile} {
		info, err := os.Stat(filepath.Join(baseline.rootDir, name))
		if err != nil {
			t.Fatal(err)
		}
		parquetBytes += info.Size()
	}
	if len(baseline.manifest.CSVExports) != 1 || baseline.manifest.CSVExports[0].Status != "complete" {
		t.Fatal(baseline.manifest.CSVExports)
	}
	t.Setenv(envArchiveConversionMaxBytes, strconv.FormatInt(parquetBytes+1, 10))
	limited, err := newRunArchiveWriter(&ManagedTest{ID: "conversion-combined-limit"}, provenance)
	if err != nil {
		t.Fatal(err)
	}
	record, err = limited.Finalize("COMPLETE", time.Now(), "", false, false, nil)
	if record != nil || !errors.Is(err, errArchiveConversionLimit) {
		t.Fatal("finalization reset quota between formats", record, err)
	}
	if len(limited.manifest.CSVExports) != 1 || limited.manifest.CSVExports[0].Status != "incomplete" {
		t.Fatal(limited.manifest.CSVExports)
	}
	if _, err := os.Stat(limited.manifestPath); !os.IsNotExist(err) {
		t.Fatal("published incomplete conversion", err)
	}
}

func TestArchiveConversionFileCannotBypassWriteAdmission(t *testing.T) {
	root := t.TempDir()
	f, err := os.CreateTemp(root, "conversion")
	if err != nil {
		t.Fatal(err)
	}
	b := &archiveOutputBudget{limitErr: errArchiveConversionLimit, freeSpaceErr: errArchiveConversionFreeSpace, root: root, remaining: 5, minFree: 1}
	w := &archiveOutputFile{file: f, budget: b}
	if _, err := w.Read(make([]byte, 1)); err == nil {
		t.Fatal("read allowed")
	}
	if _, err := w.Seek(100, 0); err == nil {
		t.Fatal("sparse seek allowed")
	}
	if _, err := w.Open(filepath.Join(root, "other")); err == nil {
		t.Fatal("alternate open allowed")
	}
	if _, err := w.Create(filepath.Join(root, "other")); err == nil {
		t.Fatal("alternate create allowed")
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("x")); err == nil {
		t.Fatal("closed output accepted")
	}
	if b.failure() == nil {
		t.Fatal("closed descriptor failure not latched")
	}
}
