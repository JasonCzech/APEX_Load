package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/rca"
)

func TestRCABrowserArchiveDoesNotInferClockOrSourceProof(t *testing.T) {
	server, store := baselineTestServer(t)
	run := comparisonArchiveFixture(t, server, store, "browser-clock-proof", 200, 150)
	record := server.runner.archives[run.ID]
	summaries, err := readAllArchiveSummariesContext(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	from, _, bucket, ok := rcaImportedWindow(summaries)
	if !ok {
		t.Fatal("missing grid")
	}
	at := from.Add(10 * bucket)
	attachD2Documents(t, record, d2Document{id: 1, metric: "lcp", cohort: d2Cohort("141.0"), at: at.Add(bucket / 2), windowStart: at, value: 1000, final: true})
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	read := func() []rca.Series {
		t.Helper()
		series, limits, err := rcaBrowserDocumentSeries(context.Background(), record, manifest, summaries, -1, 0)
		if err != nil || len(limits) != 0 || len(series) != 1 {
			t.Fatalf("series: %+v %v %v", series, limits, err)
		}
		d := series[0].Document
		if d.ClockQualification != rca.BrowserEvidenceUnavailable || d.SourceCoverageQualification != rca.BrowserEvidenceUnavailable || d.Incomplete {
			t.Fatalf("qualification: %+v", d)
		}
		return series
	}
	original := read()
	// An actual retained generator sample proves neither the browser clock nor
	// that every expected browser source completed its measurement stream.
	observation := ClockObservation{Schema: clockObservationSchema, Source: "generator", SourceID: "generator", Controller: "controller", ClockSource: analysis.ClockSourceGenerator, ObservedAt: at, OffsetStatus: analysis.OffsetMeasured, RoundTripMs: 1, UncertaintyMs: 0.5}
	data, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(record.ArchivePath, clockObservationsFileName), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	manifest.ClockObservationsFile, manifest.ClockObservationCount = clockObservationsFileName, 1
	if err = updateRunArchiveSingleArtifactIntegrity(record.ArchivePath, manifest, RunArchiveArtifact{Name: clockObservationsFileName, Kind: "evidence", Format: "jsonl", RecordType: clockObservationRecordType, Count: 1}); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"", "required", "best_effort"} {
		manifest.SourceCoverage = &RunArchiveSourceCoverage{Mode: mode, Sources: []RunArchiveSource{{Target: "generator", GeneratorID: "generator", Collected: true}}}
		if mode == "" {
			manifest.SourceCoverage = nil
		}
		series := read()
		if !reflect.DeepEqual(original, series) {
			t.Fatal("collection or generator clock metadata changed raw browser projection")
		}
		level := rca.LoadLevel{Start: at, End: at.Add(time.Minute), VUsers: 10}
		input := rca.DetectionInput{Series: series, Levels: []rca.LoadLevel{level}, Baseline: &rca.DetectionBaseline{TestID: "baseline", Qualification: rca.BaselineQualified, Series: series, Levels: []rca.LoadLevel{level}}}
		out := rca.DetectBrowserBaseline(input, rca.DefaultBrowserBaselineParams)
		if out == nil || len(out.Results) != 1 || out.Results[0].Reason != rca.BrowserClockUnavailable || out.Results[0].Z != 0 || out.Results[0].CurrentSamples != 1 {
			t.Fatalf("unsafe qualification: %+v", out)
		}
		anomalies, detection, limits := rca.Detect(input, rca.DefaultDetectParams, nil)
		if len(anomalies) != 0 || len(detection.Spikes) != 0 || detection.SeriesEvaluated != 0 || len(limits) < 2 {
			t.Fatalf("unsafe in-run scoring: %+v %+v %v", anomalies, detection, limits)
		}
	}
}

func distributedTwoGeneratorProofFixtureWithOptions(t *testing.T, runID string, gen2WallOffset time.Duration, bracketRTT time.Duration, baseValue float64, pauseMidBucket bool, authorityMode string, dropEnvelope bool) ([]rca.Series, rca.LoadLevel) {
	t.Helper()
	record, start := nativeSeriesFixture(t)
	anchor := start.Add(2 * time.Second).UTC()
	controllerDomain, err := protocol.NewBrowserClockDomain(runID)
	if err != nil {
		t.Fatal(err)
	}
	var raw bytes.Buffer
	var journal bytes.Buffer
	var rawRecords, receiptRecords int64
	owners := map[string][]protocol.BrowserClockBracket{}

	appendOwner := func(ownerID, genChar string, wallShift time.Duration, steadyStart, steadyEnd time.Duration, docOffsets []time.Duration, values []float64) {
		authority := protocol.BrowserClockDomain{
			RunID:       runID,
			Incarnation: strings.Repeat(genChar, 32),
			DomainID:    strings.Repeat(genChar, 32),
		}
		generation := strings.Repeat(genChar, 32)
		slot := strings.Repeat(genChar, 32)
		ownerStart := anchor.Add(wallShift)
		terminal := &protocol.BrowserDocumentReceiptEvidence{
			Version:          3,
			SourceGeneration: generation,
			StartedAt:        ownerStart,
			Lifetime: &protocol.BrowserNativeLifetime{
				CreationBracketed: true,
				Closed:            true,
				EndNS:             int64(3850 * time.Millisecond),
			},
			Closure: &protocol.BrowserNativeSourceClosure{
				Method:            "chromium_cdp_commits_v1",
				DiscoveryComplete: true,
			},
		}
		for i, lower := range docOffsets {
			receive := lower + 80*time.Millisecond
			docStart := ownerStart.Add(lower).Truncate(time.Millisecond)
			docEnd := docStart.Add(50 * time.Millisecond)
			doc := d2Document{
				id:          i + 1,
				metric:      "lcp",
				cohort:      d2Cohort("distributed-fixture"),
				at:          docEnd,
				windowStart: docStart,
				value:       values[i],
				final:       true,
			}
			e := doc.records()[0]
			e.SourceGeneration = generation
			o := &e.Observations[0]
			o.SourceGeneration = generation
			o.DocumentID = fmt.Sprintf("%s-doc-%d", generation, i+1)
			o.LifecycleID = fmt.Sprintf("%s-life-%d", generation, i+1)
			o.FrameID = fmt.Sprintf("%s-frame-%d", generation, i+1)
			o.NavigationID = fmt.Sprintf("%s-nav-%d", generation, i+1)
			o.MeasurementID = o.DocumentID + "/" + o.LifecycleID + "/lcp"
			o.CapturedAt = ownerStart.Add(receive)
			data, err := json.Marshal(e)
			if err != nil || e.Clone() == nil {
				t.Fatalf("invalid raw fixture: %v", err)
			}
			raw.Write(append(data, '\n'))
			rawRecords++

			begin := protocol.BrowserDocumentReceipt{
				DocumentID:          o.DocumentID,
				LifecycleID:         o.LifecycleID,
				FrameID:             o.FrameID,
				Kind:                "begin",
				BrowserTimeOriginMS: float64(o.WindowStart.UnixMilli()),
				HostElapsedNS:       int64(lower),
				ReceivedAt:          ownerStart.Add(lower),
			}
			metric := begin
			metric.Kind = "metric"
			metric.MeasurementID = o.MeasurementID
			metric.Revision = o.Revision
			metric.BrowserOffsetMS = 50
			metric.HostElapsedNS = int64(receive)
			metric.ReceivedAt = o.CapturedAt
			terminal.Receipts = append(terminal.Receipts, begin, metric)
			terminal.Closure.Documents = append(terminal.Closure.Documents, protocol.BrowserNativeDocumentExpectation{
				NativeID:    fmt.Sprintf("%s%032x", generation, i+1),
				DocumentID:  o.DocumentID,
				LifecycleID: o.LifecycleID,
				Terminal:    true,
				LowerNS:     int64(lower),
			})
		}

		sequence := uint64(0)
		emit := func(kind string, at time.Duration, e *protocol.BrowserDocumentReceiptEvidence) {
			sequence++
			if e == nil {
				e = &protocol.BrowserDocumentReceiptEvidence{Version: 3, SourceGeneration: authority.DomainID, StartedAt: ownerStart}
			}
			expected := uint64(1)
			slotID := slot
			if kind == "owner_expected" {
				expected = 0
			}
			if strings.HasPrefix(kind, "owner_") || kind == "steady" || kind == "unknown" {
				slotID = ""
			}
			e.Source = &protocol.BrowserSourceEvent{
				Authority:     authority,
				OwnerID:       ownerID,
				Kind:          kind,
				SlotID:        slotID,
				Sequence:      sequence,
				AtNS:          int64(at),
				ExpectedSlots: expected,
				Successful:    true,
				VUsers:        2,
			}
			if e.Clone() == nil {
				t.Fatalf("invalid journal fixture %+v", e)
			}
			data, err := json.Marshal(e)
			if err != nil {
				t.Fatal(err)
			}
			journal.Write(append(data, '\n'))
			receiptRecords++
		}
		emit("owner_expected", 0, nil)
		emit("slot_expected", 10*time.Millisecond, nil)
		emit("context_expected", 20*time.Millisecond, &protocol.BrowserDocumentReceiptEvidence{
			Version:          3,
			SourceGeneration: generation,
			StartedAt:        ownerStart,
			Lifetime:         &protocol.BrowserNativeLifetime{CreationBracketed: true},
		})
		emit("steady", steadyStart, nil)
		if pauseMidBucket && ownerID == "gen-1" {
			emit("unknown", 390*time.Millisecond, nil)
			emit("steady", 395*time.Millisecond, nil)
		}
		emit("context_terminal", steadyEnd, terminal)
		emit("slot_terminal", 3950*time.Millisecond, nil)
		emit("owner_terminal", 4*time.Second, nil)

		owners[ownerID] = []protocol.BrowserClockBracket{
			{
				Authority:        authority,
				OwnerID:          ownerID,
				ControllerSendNS: int64(1 * time.Millisecond),
				ControllerRecvNS: int64(1*time.Millisecond + bracketRTT),
				OwnerMonoNS:      int64(2 * time.Millisecond),
			},
			{
				Authority:        authority,
				OwnerID:          ownerID,
				ControllerSendNS: int64(2000 * time.Millisecond),
				ControllerRecvNS: int64(2000*time.Millisecond + bracketRTT),
				OwnerMonoNS:      int64(2001 * time.Millisecond),
			},
			{
				Authority:        authority,
				OwnerID:          ownerID,
				ControllerSendNS: int64(4001 * time.Millisecond),
				ControllerRecvNS: int64(4001*time.Millisecond + bracketRTT),
				OwnerMonoNS:      int64(4002 * time.Millisecond),
			},
		}
	}

	appendOwner("gen-1", "a", 0, 100*time.Millisecond, 3900*time.Millisecond, []time.Duration{300 * time.Millisecond, 1300 * time.Millisecond, 2300 * time.Millisecond}, []float64{baseValue, baseValue + 10, baseValue + 20})
	appendOwner("gen-2", "b", gen2WallOffset, 120*time.Millisecond, 3880*time.Millisecond, []time.Duration{400 * time.Millisecond, 1400 * time.Millisecond, 2400 * time.Millisecond}, []float64{baseValue + 5, baseValue + 15, baseValue + 25})

	m, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	m.TestID = runID
	m.BrowserProofExpectedOwners = []string{"gen-1", "gen-2"}
	if authorityMode == "" {
		authorityMode = "distributed_envelope"
	}
	m.BrowserProofAuthorityMode = authorityMode
	if !dropEnvelope {
		m.BrowserProofClockEnvelope = &protocol.BrowserDistributedClockEnvelope{
			Version:            1,
			Controller:         controllerDomain,
			AuthorityStartedAt: anchor,
			MaxSkewPPM:         protocol.DefaultBrowserClockMaxSkewPPM,
			MaxBracketRTTNS:    protocol.DefaultBrowserClockMaxBracketRTTNS,
			MaxBracketGapNS:    protocol.DefaultBrowserClockMaxBracketGapNS,
			Owners:             owners,
		}
	}
	m.BrowserDocumentsFile = browserDocumentsFileName
	m.BrowserDocumentsParquetFile = browserDocumentsParquetFileName
	m.BrowserDocumentRecordCount = rawRecords
	m.BrowserDocumentsSHA256 = sha256BytesHex(raw.Bytes())
	m.BrowserDocumentsParquetSHA256 = strings.Repeat("e", 64)
	m.BrowserReceiptsFile = browserReceiptsFileName
	m.BrowserReceiptRecordCount = receiptRecords
	m.BrowserReceiptsSHA256 = sha256BytesHex(journal.Bytes())
	for name, data := range map[string][]byte{browserDocumentsFileName: raw.Bytes(), browserReceiptsFileName: journal.Bytes()} {
		if err = os.WriteFile(filepath.Join(record.ArchivePath, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	summaries := []IntervalSummaryRecord{
		{Timestamp: start},
		{Timestamp: start.Add(time.Second)},
		{Timestamp: start.Add(2 * time.Second)},
		{Timestamp: start.Add(3 * time.Second)},
		{Timestamp: start.Add(4 * time.Second)},
		{Timestamp: start.Add(5 * time.Second)},
		{Timestamp: start.Add(6 * time.Second)},
	}
	series, limits, err := rcaBrowserDocumentSeries(context.Background(), record, m, summaries, -1, 0)
	if err != nil || len(limits) != 0 || len(series) != 1 {
		t.Fatalf("distributed series: %+v %v %v", series, limits, err)
	}
	from, to, _, _ := rcaImportedWindow(summaries)
	return series, rca.LoadLevel{Start: from, End: to, VUsers: 4}
}

func distributedTwoGeneratorProofFixture(t *testing.T, runID string, gen2WallOffset time.Duration, bracketRTT time.Duration, baseValue float64) ([]rca.Series, rca.LoadLevel) {
	t.Helper()
	return distributedTwoGeneratorProofFixtureWithOptions(t, runID, gen2WallOffset, bracketRTT, baseValue, false, "distributed_envelope", false)
}

func TestRCADistributedTwoGeneratorBrowserClockQualificationAndRTTRefusal(t *testing.T) {
	baseSeries, baseLevel := distributedTwoGeneratorProofFixture(t, "dist-base", 10*time.Minute, 2*time.Millisecond, 100)
	currSeries, currLevel := distributedTwoGeneratorProofFixture(t, "dist-curr", -15*time.Minute, 3*time.Millisecond, 400)

	for _, s := range []rca.Series{baseSeries[0], currSeries[0]} {
		if s.Document.ClockQualification != rca.BrowserEvidenceQualified || s.Document.SourceCoverageQualification != rca.BrowserEvidenceQualified {
			t.Fatalf("expected distributed clock and source coverage qualification, got %+v", s.Document)
		}
		if s.Document.ClockUncertainty <= 0 || s.Document.ClockUncertainty > 10*time.Millisecond {
			t.Fatalf("expected ms-scale clock uncertainty, got %v", s.Document.ClockUncertainty)
		}
	}

	params := rca.DefaultBrowserBaselineParams
	params.MinBaselineBuckets = 1
	input := rca.DetectionInput{
		Series: currSeries,
		Levels: []rca.LoadLevel{currLevel},
		Baseline: &rca.DetectionBaseline{
			TestID:              "dist-base",
			Qualification:       rca.BaselineQualified,
			Series:              baseSeries,
			Levels:              []rca.LoadLevel{baseLevel},
			ScriptChangedGroups: map[string]bool{},
		},
	}
	out := rca.DetectBrowserBaseline(input, params)
	if out == nil || len(out.Results) != 1 || out.Results[0].State == rca.BrowserBaselineUnscored || out.Results[0].CurrentSamples != 6 || out.Results[0].BaselineSamples != 6 {
		t.Fatalf("expected scored 2-generator baseline comparison with 6 samples per run, got %+v", out)
	}

	delayedSeries, delayedLevel := distributedTwoGeneratorProofFixture(t, "dist-delayed", 0, 1500*time.Millisecond, 400)
	if delayedSeries[0].Document.SourceCoverageQualification != rca.BrowserEvidenceQualified {
		t.Fatalf("expected source coverage to remain qualified on bracket RTT exceedance, got %+v", delayedSeries[0].Document)
	}
	if delayedSeries[0].Document.ClockQualification != rca.BrowserEvidenceUnavailable || delayedSeries[0].Document.ClockUnavailableReason != "browser_clock_bracket_rtt_exceeded" {
		t.Fatalf("expected clock qualification unavailable with browser_clock_bracket_rtt_exceeded, got %+v", delayedSeries[0].Document)
	}
	delayedInput := rca.DetectionInput{
		Series: delayedSeries,
		Levels: []rca.LoadLevel{delayedLevel},
		Baseline: &rca.DetectionBaseline{
			TestID:              "dist-base",
			Qualification:       rca.BaselineQualified,
			Series:              baseSeries,
			Levels:              []rca.LoadLevel{baseLevel},
			ScriptChangedGroups: map[string]bool{},
		},
	}
	delayedOut := rca.DetectBrowserBaseline(delayedInput, params)
	if delayedOut == nil || len(delayedOut.Results) != 1 || delayedOut.Results[0].State != rca.BrowserBaselineUnscored || delayedOut.Results[0].Reason != rca.BrowserClockUnavailable || !strings.Contains(delayedOut.Results[0].Detail, "browser_clock_bracket_rtt_exceeded") {
		t.Fatalf("expected unscored browser_clock_unavailable with browser_clock_bracket_rtt_exceeded, got %+v", delayedOut)
	}

	uncertaintySeries, _ := distributedTwoGeneratorProofFixtureWithOptions(t, "dist-uncertainty", 0, 999900*time.Microsecond, 400, false, "distributed_envelope", false)
	if uncertaintySeries[0].Document.ClockQualification != rca.BrowserEvidenceQualified || len(uncertaintySeries[0].Document.Windows) == 0 {
		t.Fatalf("expected qualified clocks with invalid bucket windows, got %+v", uncertaintySeries[0].Document)
	}
	for _, w := range uncertaintySeries[0].Document.Windows {
		if !w.Invalid || w.InvalidReason != "observation_window_exceeds_bucket" {
			t.Fatalf("expected observation_window_exceeds_bucket when clockUncertainty > bucket, got %+v", w)
		}
	}

	pausedSeries, _ := distributedTwoGeneratorProofFixtureWithOptions(t, "dist-paused", 0, 2*time.Millisecond, 400, true, "distributed_envelope", false)
	invalidBuckets := 0
	for _, w := range pausedSeries[0].Document.Windows {
		if w.Invalid && !w.MonotonicVerified {
			invalidBuckets++
		}
	}
	if invalidBuckets == 0 {
		t.Fatalf("expected distributed mid-bucket pause between gen-1 and gen-2 documents to invalidate bucket window, got %+v", pausedSeries[0].Document.Windows)
	}

	noEnvSeries, _ := distributedTwoGeneratorProofFixtureWithOptions(t, "dist-no-env", 0, 2*time.Millisecond, 400, false, "distributed", true)
	if noEnvSeries[0].Document.SourceCoverageQualification != rca.BrowserEvidenceQualified || noEnvSeries[0].Document.ClockQualification != rca.BrowserEvidenceUnavailable || noEnvSeries[0].Document.ClockUnavailableReason != "browser_clock_envelope_missing" {
		t.Fatalf("expected distributed archive without clock envelope to report browser_clock_envelope_missing, got %+v", noEnvSeries[0].Document)
	}
}

func TestDistributedBrowserClockBracketsRPCTransport(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	client, _ := newTestGeneratorRPCClient(t, "gen-rpc-1", 10, generatorrpc.WithExecution())

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	initHB, err := client.OpenHeartbeat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := initHB.Send(generatorrpc.Heartbeat{GeneratorID: generatorrpc.ClockProbeGeneratorID}); err != nil {
		t.Fatal(err)
	}
	if _, err := initHB.Receive(); err != nil {
		t.Fatal(err)
	}
	_ = initHB.CloseSend()

	if _, err := client.DistributeScript(ctx, generatorrpc.Script{
		ID:                  "browser-bracket-script",
		ExecutionGeneration: 0,
		Content:             []byte(`{"id":"browser-bracket-script","name":"Browser Bracket","protocol":"browser","config":{"browser":{"capture_document_metrics":true},"run_logic":{"iterations":1}},"actions":[{"type":"browser_navigate","name":"nav","target":"https://example.com"}]}`),
	}); err != nil {
		t.Fatal(err)
	}
	assignRes, err := client.AssignWork(ctx, generatorrpc.Assignment{
		TestID:                     "test-rpc-brackets",
		ExecutionGeneration:        0,
		ScriptID:                   "browser-bracket-script",
		GroupName:                  "browser",
		VUserCount:                 0,
		TargetDuration:             time.Second,
		PrepareOnly:                true,
		PreferBrowserDocumentProof: true,
	})
	if err != nil || !assignRes.Accepted {
		t.Fatalf("AssignWork: %+v %v", assignRes, err)
	}
	commitRes, err := client.CommitWork(ctx, "test-rpc-brackets", 0)
	if err != nil || !commitRes.Accepted || len(commitRes.BrowserBrackets) != 1 {
		t.Fatalf("expected CommitWork to return 1 browser clock bracket, got %+v %v", commitRes, err)
	}
	hb, err := client.OpenHeartbeat(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer hb.CloseSend()
	if err := hb.Send(generatorrpc.Heartbeat{GeneratorID: generatorrpc.ClockProbeGeneratorID}); err != nil {
		t.Fatal(err)
	}
	if _, err := hb.Receive(); err != nil {
		t.Fatal(err)
	}
	sample, ok := hb.ClockSample()
	if !ok || len(sample.BrowserBrackets) != 1 {
		t.Fatalf("expected Heartbeat ClockSample to carry 1 browser clock bracket, got %+v ok=%v", sample, ok)
	}
	if _, err := client.StopTestAtGeneration(ctx, "test-rpc-brackets", false, 0); err != nil {
		t.Fatal(err)
	}
	brackets := client.BrowserClockBrackets("test-rpc-brackets")
	if len(brackets) < 3 {
		t.Fatalf("expected >= 3 recorded browser clock brackets across CommitWork, Heartbeat, and StopTest, got %d: %+v", len(brackets), brackets)
	}
	for i := 1; i < len(brackets); i++ {
		if brackets[i].Sample.MonoNS < brackets[i-1].Sample.MonoNS {
			t.Fatalf("expected non-decreasing generator monotonic readings, got %+v", brackets)
		}
	}
}
