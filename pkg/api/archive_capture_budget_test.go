package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/internal/diskspace"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestArchiveCaptureBudgetConfiguration(t *testing.T) {
	for _, name := range []string{envArchiveCaptureMaxBytes, envArchiveCaptureMinFreeBytes} {
		for _, value := range []string{"0", "-1", "no", "1.5", "1125899906842625"} {
			t.Run(name+value, func(t *testing.T) {
				t.Setenv(name, value)
				if _, err := newArchiveCaptureBudget(t.TempDir()); err == nil {
					t.Fatal("invalid limit accepted")
				}
			})
		}
	}
	t.Setenv(envArchiveCaptureMaxBytes, "10")
	t.Setenv(envArchiveCaptureMinFreeBytes, "1")
	b, err := newArchiveCaptureBudget(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envArchiveCaptureMaxBytes, "100")
	t.Setenv(envArchiveCaptureMinFreeBytes, "100")
	if b.remaining != 10 || b.minFree != 1 {
		t.Fatal("configuration not frozen")
	}
	if err = b.reserve(-1); !errors.Is(err, errArchiveCaptureLimit) {
		t.Fatal(err)
	}
}

func TestArchiveCaptureBudgetSharedConcurrentBoundary(t *testing.T) {
	b := &archiveCaptureBudget{remaining: 100}
	var accepted atomic.Int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			w := archiveCaptureRecordWriter{destination: io.Discard, budget: b}
			for range 10 {
				n, err := w.Write([]byte("xx"))
				if err == nil {
					accepted.Add(int64(n))
				} else if !errors.Is(err, errArchiveCaptureLimit) {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 100 || b.remaining != 0 {
		t.Fatal(accepted.Load(), b.remaining)
	}
}

func TestArchiveCaptureBudgetRejectsWholeRecord(t *testing.T) {
	var dst bytes.Buffer
	b := &archiveCaptureBudget{remaining: 5}
	w := archiveCaptureRecordWriter{destination: &dst, budget: b}
	if n, err := w.Write([]byte("12345")); n != 5 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := w.Write([]byte("6")); n != 0 || !errors.Is(err, errArchiveCaptureLimit) {
		t.Fatal(n, err)
	}
	if dst.String() != "12345" {
		t.Fatal(dst.String())
	}
}

func TestArchiveCaptureBudgetShortAndInvalidWrites(t *testing.T) {
	for _, count := range []int{-1, 2, 5} {
		t.Run(strconv.Itoa(count), func(t *testing.T) {
			b := &archiveCaptureBudget{remaining: 4}
			w := archiveCaptureRecordWriter{destination: transferCountWriter(count), budget: b}
			n, err := w.Write([]byte("1234"))
			if err == nil {
				t.Fatal("invalid write accepted")
			}
			if count == 2 {
				if n != 2 || !errors.Is(err, io.ErrShortWrite) || b.remaining != 2 {
					t.Fatal(n, err, b.remaining)
				}
			} else if n != 0 || b.remaining != 0 {
				t.Fatal("uncertain bytes refunded")
			}
		})
	}
}

func TestArchiveCapturePhysicalHeadroom(t *testing.T) {
	root := t.TempDir()
	f, err := os.CreateTemp(root, "capture")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b := &archiveCaptureBudget{root: root, minFree: 1}
	w := archiveCaptureFileWriter{file: f, budget: b}
	if n, err := w.Write([]byte("ok")); n != 2 || err != nil {
		t.Fatal(n, err)
	}
	free, err := diskspace.AvailableFile(f)
	if err != nil {
		t.Fatal(err)
	}
	b.minFree = free + 1<<30
	if n, err := w.Write([]byte("denied")); n != 0 || !errors.Is(err, errArchiveCaptureFreeSpace) {
		t.Fatal(n, err)
	}
	if info, err := f.Stat(); err != nil || info.Size() != 2 {
		t.Fatal("denied write changed file")
	}
	b.minFree = 1
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("closed")); err == nil {
		t.Fatal("closed file accepted")
	}
}

func TestArchiveCaptureAllRecordFailuresPreventManifest(t *testing.T) {
	cases := []struct {
		name   string
		record func(*runArchiveWriter) error
	}{
		{"transactions", func(w *runArchiveWriter) error { return w.RecordResult(&protocol.Result{ActionName: "test"}) }},
		{"resources", func(w *runArchiveWriter) error {
			return w.RecordResourceSample(time.Now(), 1, &engine.SystemResources{})
		}},
		{"summaries", func(w *runArchiveWriter) error { return w.RecordIntervalSummary(&LiveMetrics{Timestamp: time.Now()}) }},
		{"elk", func(w *runArchiveWriter) error { return w.RecordELKSamples([]ELKLogSample{{}}) }},
		{"dynatrace", func(w *runArchiveWriter) error {
			return w.RecordDynatraceEvidence([]DynatraceEvidenceRecord{{Type: "log"}})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv(envArchiveCaptureMaxBytes, "1")
			t.Setenv(envArchiveCaptureMinFreeBytes, "1")
			run := &ManagedTest{ID: "limit-" + tc.name, ELKCapture: &ELKCaptureConfig{}, DynatraceCapture: &DynatraceCaptureConfig{}}
			w, err := newRunArchiveWriter(run, RunArchiveProvenance{})
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.record(w); !errors.Is(err, errArchiveCaptureLimit) {
				t.Fatal(err)
			}
			if err := w.RecordResult(&protocol.Result{}); !errors.Is(err, errArchiveCaptureLimit) {
				t.Fatal("capture error was not sticky", err)
			}
			rec, err := w.Finalize("COMPLETE", time.Now(), "", false, false, nil)
			if rec != nil || !errors.Is(err, errArchiveCaptureLimit) {
				t.Fatal(rec, err)
			}
			if _, err := os.Stat(w.manifestPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("incomplete capture published manifest", err)
			}
		})
	}
}

func TestArchiveCaptureFailurePreservesAcceptedPrefixAndExecutionOutcome(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envArchiveCaptureMaxBytes, "4096")
	t.Setenv(envArchiveCaptureMinFreeBytes, "1")
	run := &ManagedTest{ID: "capture-prefix"}
	w, err := newRunArchiveWriter(run, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.RecordResult(&protocol.Result{ActionName: "accepted"}); err != nil {
		t.Fatal(err)
	}
	// Consume the remaining allowance using complete small records. This also
	// tests the actual production encoder, buffering and sticky error boundary.
	for range 100 {
		err = w.RecordResult(&protocol.Result{ActionName: strings.Repeat("x", 200)})
		if err != nil {
			break
		}
	}
	if !errors.Is(err, errArchiveCaptureLimit) {
		t.Fatal(err)
	}
	a := &runArtifacts{recorder: w}
	record, status, why := a.finalize(run, "COMPLETE", time.Now(), "")
	if record != nil || status != "COMPLETE" || why != "" {
		t.Fatal(record, status, why)
	}
	raw, err := os.ReadFile(filepath.Join(w.rootDir, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte("\n"))
	if len(raw) == 0 || len(raw) > 4096 || int64(len(lines)) != w.manifest.TransactionCount {
		t.Fatal("accepted prefix lost", len(raw), len(lines), w.manifest.TransactionCount)
	}
	for _, line := range lines {
		if !json.Valid(line) {
			t.Fatal("partial record retained")
		}
	}
	if !bytes.Contains(lines[0], []byte("accepted")) {
		t.Fatal("first accepted record lost")
	}
	if _, err := os.Stat(w.manifestPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("false complete manifest", err)
	}
}

func TestArchiveCaptureFiveEncodersShareOneAllowance(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envArchiveCaptureMaxBytes, "15")
	t.Setenv(envArchiveCaptureMinFreeBytes, "1")
	w, err := newRunArchiveWriter(&ManagedTest{ID: "shared-five", ELKCapture: &ELKCaptureConfig{}, DynatraceCapture: &DynatraceCaptureConfig{}}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	for _, encoder := range []*json.Encoder{w.txEncoder, w.resourceEncoder, w.summaryEncoder, w.elkEncoder, w.dynatraceEncoder} {
		if err := encoder.Encode(struct{}{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.RecordResult(&protocol.Result{}); !errors.Is(err, errArchiveCaptureLimit) {
		t.Fatal("five files did not share the limit", err)
	}
	if _, err := w.Finalize("COMPLETE", time.Now(), "", false, false, nil); !errors.Is(err, errArchiveCaptureLimit) {
		t.Fatal(err)
	}
	var total int
	for _, name := range []string{"transactions.jsonl", "resources.jsonl", "interval_summaries.jsonl", "elk_evidence.jsonl", "dynatrace_evidence.jsonl"} {
		data, err := os.ReadFile(filepath.Join(w.rootDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "{}\n" {
			t.Fatalf("%s lost accepted record: %q", name, data)
		}
		total += len(data)
	}
	if total != 15 {
		t.Fatal(total)
	}
}
