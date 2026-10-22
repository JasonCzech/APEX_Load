package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

func nativeBrowserProofArchive(t *testing.T, id, url string) (*storage.RunArchiveRecord, *RunArchiveManifest, []IntervalSummaryRecord) {
	t.Helper()
	w, err := newRunArchiveWriter(&ManagedTest{ID: id}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	j, err := w.BrowserDocumentSourceJournal()
	if err != nil {
		t.Fatal(err)
	}
	lease, err := j.Begin()
	if err != nil {
		t.Fatal(err)
	}
	b := &browserprotocol.Browser{}
	b.SetBrowserDocumentSourceLease(lease)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now().Add(-time.Second).Truncate(time.Second).UTC()
	if err = b.Init(ctx, map[string]any{"browser_engine": "chromium", "browser_headless": true, "browser_capture_document_metrics": true, "browser_runtime_new_user_policy": "persistent_context", "browser_viewport_width": 1280, "browser_viewport_height": 900}); err != nil {
		t.Fatal(err)
	}
	defer b.Teardown(context.Background())
	if err = j.Phase(true, 1); err != nil {
		t.Fatal(err)
	}
	for iteration := 1; iteration <= 4; iteration++ {
		result, execErr := b.Execute(ctx, &protocol.Action{Type: "browser_navigate", Target: fmt.Sprintf("%s?paint=%d", url, iteration)})
		if execErr != nil || result == nil || !result.Success {
			t.Fatal("native navigate", result, execErr)
		}
		deadline := time.Now().Add(5 * time.Second)
		painted := false
		for time.Now().Before(deadline) {
			count := 0
			for _, o := range b.BrowserDocumentEvidence().Observations {
				if o.Metric == "fcp" && o.Available {
					count++
				}
			}
			if count >= iteration {
				painted = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !painted {
			t.Fatal("native fixture never painted")
		}
		// Separate real measurements into distinct baseline buckets; no metric is fabricated.
		time.Sleep(time.Second)
	}

	if err = b.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
	if err = lease.Finish(true); err != nil {
		t.Fatal(err)
	}
	if err = w.RecordBrowserDocumentEvidence(b.BrowserDocumentEvidence()); err != nil {
		t.Fatal(err)
	}
	end := time.Now().Add(2 * time.Second).Truncate(time.Second).UTC()
	for at := start; !at.After(end); at = at.Add(time.Second) {
		if err = w.RecordIntervalSummary(&LiveMetrics{Timestamp: at, ActiveVUsers: 1}); err != nil {
			t.Fatal(err)
		}
	}
	record, err := w.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	summaries, err := readAllArchiveSummariesContext(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	return record, m, summaries
}

func TestBrowserSourceNativeTwoArchiveQualification(t *testing.T) {
	if os.Getenv("APEXLOAD_BROWSER_TESTS") != "require" {
		t.Skip("native browser qualification requires APEXLOAD_BROWSER_TESTS=require")
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paint, _ := strconv.Atoi(r.URL.Query().Get("paint"))
		time.Sleep(time.Duration(paint) * 50 * time.Millisecond)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<!doctype html><title>Proof</title><h1>Native source and clock proof</h1>")
	}))
	defer server.Close()
	defer func() {
		if !browserprotocol.ShutdownPool(10 * time.Second) {
			t.Error("native pool did not close")
		}
	}()
	var runs [][]rca.Series
	var levels []rca.LoadLevel
	for _, id := range []string{"proof-baseline", "proof-current"} {
		record, m, summaries := nativeBrowserProofArchive(t, id, server.URL)
		series, limits, err := rcaBrowserDocumentSeries(context.Background(), record, m, summaries, -1, 0)
		if err != nil || len(limits) != 0 || len(series) == 0 {
			t.Fatalf("archive projection: %v %v %v", series, limits, err)
		}
		for _, s := range series {
			if s.Document.ClockQualification != rca.BrowserEvidenceQualified || s.Document.SourceCoverageQualification != rca.BrowserEvidenceQualified || s.Document.TimelineQualification != rca.BrowserEvidenceUnavailable {
				t.Fatalf("missing bounded qualification: %+v", s.Document)
			}
		}
		from, to, _, _ := rcaImportedWindow(summaries)
		levels = append(levels, rca.LoadLevel{Start: from, End: to, VUsers: 1})
		runs = append(runs, series)
		// A missing independently expected generator must invalidate source proof.
		m.BrowserProofExpectedOwners = append(m.BrowserProofExpectedOwners, "missing-generator")
		if proof, _ := readBrowserSourceProof(context.Background(), record, m); proof != nil {
			t.Fatal("missing generator disappeared")
		}
	}
	params := rca.DefaultBrowserBaselineParams
	params.MinBaselineBuckets = 1
	input := rca.DetectionInput{Series: runs[1], Levels: []rca.LoadLevel{levels[1]}, Baseline: &rca.DetectionBaseline{TestID: "proof-baseline", Qualification: rca.BaselineQualified, Series: runs[0], Levels: []rca.LoadLevel{levels[0]}, ScriptChangedGroups: map[string]bool{}}}
	out := rca.DetectBrowserBaseline(input, params)
	if out == nil || len(out.Results) == 0 {
		t.Fatal("no native baseline result")
	}
	scored := 0
	for _, r := range out.Results {
		if r.State != rca.BrowserBaselineUnscored {
			scored++
		} else if r.Reason != "zero_variance" {
			t.Fatalf("native baseline unscored: %+v", r)
		}
	}
	if scored == 0 {
		t.Fatal("native baseline produced no scored metric")
	}

	_, detection, limits := rca.Detect(input, rca.DefaultDetectParams, nil)
	if detection.SeriesEvaluated != 0 || len(limits) == 0 {
		t.Fatal("bounded load proof enabled timeline scoring", detection, limits)
	}
}

// Synthetic contracts isolate placement and pause refusals; the test above
// independently exercises acquisition through the real native/archive path.
func sourceProofSeriesFixture(t *testing.T, shift time.Duration, pause bool) []rca.Series {
	t.Helper()
	record, start := nativeSeriesFixture(t)
	anchor := start.Add(2 * time.Second).UTC()
	generation := d2Generation
	domain := strings.Repeat("b", 32)
	authority := protocol.BrowserClockDomain{RunID: "proof-fixture", Incarnation: strings.Repeat("c", 32), DomainID: domain}
	slot := strings.Repeat("d", 32)
	var raw bytes.Buffer
	terminal := &protocol.BrowserDocumentReceiptEvidence{Version: 3, SourceGeneration: generation, StartedAt: anchor, Lifetime: &protocol.BrowserNativeLifetime{CreationBracketed: true, Closed: true, EndNS: int64(850 * time.Millisecond)}, Closure: &protocol.BrowserNativeSourceClosure{Method: "chromium_cdp_commits_v1", DiscoveryComplete: true}}
	for i := 0; i < 2; i++ {
		lower := time.Duration(150+i*350) * time.Millisecond
		receive := time.Duration(300+i*350) * time.Millisecond
		document := d2Document{id: i + 1, metric: "lcp", cohort: d2Cohort("native-fixture"), at: anchor.Add(lower + 50*time.Millisecond + shift), windowStart: anchor.Add(lower + shift), value: 100 + float64(i), final: true}
		e := document.records()[0]
		o := &e.Observations[0]
		o.CapturedAt = anchor.Add(receive)
		data, err := json.Marshal(e)
		if err != nil || e.Clone() == nil {
			t.Fatal("invalid raw fixture", err)
		}
		raw.Write(append(data, '\n'))
		begin := protocol.BrowserDocumentReceipt{DocumentID: o.DocumentID, LifecycleID: o.LifecycleID, FrameID: o.FrameID, Kind: "begin", BrowserTimeOriginMS: float64(o.WindowStart.UnixMilli()), HostElapsedNS: int64(lower + time.Millisecond), ReceivedAt: anchor.Add(lower + time.Millisecond)}
		metric := begin
		metric.Kind = "metric"
		metric.MeasurementID = o.MeasurementID
		metric.Revision = o.Revision
		metric.BrowserOffsetMS = 50
		metric.HostElapsedNS = int64(receive)
		metric.ReceivedAt = o.CapturedAt
		terminal.Receipts = append(terminal.Receipts, begin, metric)
		terminal.Closure.Documents = append(terminal.Closure.Documents, protocol.BrowserNativeDocumentExpectation{NativeID: fmt.Sprintf("%064x", i+1), DocumentID: o.DocumentID, LifecycleID: o.LifecycleID, Terminal: true, LowerNS: int64(lower)})
	}
	var journal bytes.Buffer
	sequence := uint64(0)
	emit := func(kind string, at time.Duration, e *protocol.BrowserDocumentReceiptEvidence) {
		sequence++
		if e == nil {
			e = &protocol.BrowserDocumentReceiptEvidence{Version: 3, SourceGeneration: domain, StartedAt: anchor}
		}
		expected := uint64(1)
		slotID := slot
		if kind == "owner_expected" {
			expected = 0
		}
		if strings.HasPrefix(kind, "owner_") || kind == "steady" || kind == "unknown" {
			slotID = ""
		}
		e.Source = &protocol.BrowserSourceEvent{Authority: authority, OwnerID: "controller", Kind: kind, SlotID: slotID, Sequence: sequence, AtNS: int64(at), ExpectedSlots: expected, Successful: true, VUsers: 1}
		if e.Clone() == nil {
			t.Fatalf("invalid journal fixture %+v", e)
		}
		data, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		journal.Write(append(data, '\n'))
	}
	emit("owner_expected", 0, nil)
	emit("slot_expected", 10*time.Millisecond, nil)
	emit("context_expected", 50*time.Millisecond, &protocol.BrowserDocumentReceiptEvidence{Version: 3, SourceGeneration: generation, StartedAt: anchor, Lifetime: &protocol.BrowserNativeLifetime{CreationBracketed: true}})
	emit("steady", 100*time.Millisecond, nil)
	if pause {
		emit("unknown", 400*time.Millisecond, nil)
		emit("steady", 500*time.Millisecond, nil)
	}
	emit("context_terminal", 900*time.Millisecond, terminal)
	emit("slot_terminal", 950*time.Millisecond, nil)
	emit("owner_terminal", time.Second, nil)
	m, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	m.TestID = authority.RunID
	m.BrowserProofExpectedOwners = []string{"controller"}
	m.BrowserProofAuthorityMode = "local"
	m.BrowserDocumentsFile = browserDocumentsFileName
	m.BrowserDocumentsParquetFile = browserDocumentsParquetFileName
	m.BrowserDocumentRecordCount = 2
	m.BrowserDocumentsSHA256 = sha256BytesHex(raw.Bytes())
	m.BrowserDocumentsParquetSHA256 = strings.Repeat("e", 64)
	m.BrowserReceiptsFile = browserReceiptsFileName
	m.BrowserReceiptRecordCount = int64(sequence)
	m.BrowserReceiptsSHA256 = sha256BytesHex(journal.Bytes())
	for name, data := range map[string][]byte{browserDocumentsFileName: raw.Bytes(), browserReceiptsFileName: journal.Bytes()} {
		if err = os.WriteFile(filepath.Join(record.ArchivePath, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	summaries := []IntervalSummaryRecord{{Timestamp: start}, {Timestamp: start.Add(time.Second)}, {Timestamp: start.Add(5 * time.Second)}}
	series, limits, err := rcaBrowserDocumentSeries(context.Background(), record, m, summaries, -1, 0)
	if err != nil || len(limits) != 0 || len(series) != 1 {
		t.Fatal(series, limits, err)
	}
	return series
}
func TestBrowserSourceProjectionIgnoresTargetWallClock(t *testing.T) {
	original := sourceProofSeriesFixture(t, 0, false)
	shifted := sourceProofSeriesFixture(t, 3*time.Hour, false)
	if original[0].Document.ClockQualification != rca.BrowserEvidenceQualified || shifted[0].Document.ClockQualification != rca.BrowserEvidenceQualified {
		t.Fatal("missing positive load proof")
	}
	// Fixture roots differ but the native-series grid is deterministic.
	if !reflect.DeepEqual(original[0].Points, shifted[0].Points) || !reflect.DeepEqual(original[0].Document.Windows, shifted[0].Document.Windows) {
		t.Fatal("target wall clock changed projected population")
	}
}
func TestBrowserSourceSameBucketCannotBridgePause(t *testing.T) {
	s := sourceProofSeriesFixture(t, 0, true)[0]

	for _, w := range s.Document.Windows {
		if w.MonotonicVerified || !w.Invalid {
			t.Fatal("pause-crossing bucket qualified")
		}
	}
}

func TestBrowserSourceEngineNativePhases(t *testing.T) {
	if os.Getenv("APEXLOAD_BROWSER_TESTS") != "require" {
		t.Skip("native browser qualification requires APEXLOAD_BROWSER_TESTS=require")
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<!doctype html><h1>Engine phase fixture</h1>")
	}))
	defer server.Close()
	defer func() {
		if !browserprotocol.ShutdownPool(10 * time.Second) {
			t.Error("native pool did not close")
		}
	}()
	for _, early := range []bool{false, true} {
		t.Run(fmt.Sprintf("early=%v", early), func(t *testing.T) {
			id := fmt.Sprintf("engine-proof-%v", early)
			w, err := newRunArchiveWriter(&ManagedTest{ID: id}, RunArchiveProvenance{})
			if err != nil {
				t.Fatal(err)
			}
			yes := true
			script := &dsl.Script{ID: id, Name: id, Protocol: "browser", Actions: []dsl.Action{{Type: "browser_navigate", Target: server.URL, ThinkTimeAfter: true, ThinkTime: dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 1200}}}}
			script.Config.Browser.CaptureDocumentMetrics = &yes
			if early {
				script.Config.RunLogic.Iterations = 1
			}
			m, err := engine.NewMultiGroup(engine.MultiGroupConfig{Duration: 2 * time.Second, Groups: []engine.GroupConfig{{Name: "browser", Script: script, VUsers: 1, ResultSink: w, RunID: id}}})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if _, err = m.Run(ctx); err != nil {
				t.Fatal(err)
			}
			record, err := w.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := loadRunArchiveManifestFromRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			proof, proofErr := readBrowserSourceProof(ctx, record, manifest)
			if early {
				if proof != nil {
					t.Fatal("unexpected engine departure qualified")
				}
			} else if proofErr != nil || proof == nil {
				_ = forEachBrowserReceiptArtifact(ctx, record.ArchivePath, manifest.BrowserReceiptsFile, manifest.BrowserReceiptsSHA256, manifest.BrowserReceiptRecordCount, func(e *protocol.BrowserDocumentReceiptEvidence) error {
					if e.Source != nil {
						t.Logf("event=%+v native=%+v incomplete=%v receipts=%d", e.Source, e.Closure, e.Incomplete, len(e.Receipts))
					}
					return nil
				})
				t.Fatal("normal engine source closure unavailable", proofErr)
			}
			steady, failed := false, false
			err = forEachBrowserReceiptArtifact(ctx, record.ArchivePath, manifest.BrowserReceiptsFile, manifest.BrowserReceiptsSHA256, manifest.BrowserReceiptRecordCount, func(e *protocol.BrowserDocumentReceiptEvidence) error {
				if e.Source != nil {
					steady = steady || e.Source.Kind == "steady"
					failed = failed || !e.Source.Successful
				}
				return nil
			})
			if err != nil || !steady || failed != early {
				t.Fatal("engine phase sequence", steady, failed, err)
			}
		})
	}
}
