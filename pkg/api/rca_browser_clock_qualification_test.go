package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
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
