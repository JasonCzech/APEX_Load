package api

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/rca"
)

func TestRCABrowserDocumentSeriesRetainsWholeFinalWindowEnvelope(t *testing.T) {
	server, store := baselineTestServer(t)
	run := comparisonArchiveFixture(t, server, store, "window-envelope", 200, 150)
	record := server.runner.archives[run.ID]
	summaries, err := readAllArchiveSummariesContext(context.Background(), record)
	if err != nil {
		t.Fatal(err)
	}
	from, _, bucket, ok := rcaImportedWindow(summaries)
	if !ok {
		t.Fatal("fixture lacks interval grid")
	}
	at := from.Add(10 * bucket)
	early := at.Add(-5 * bucket)
	later := at.Add(bucket / 2)
	attachD2Documents(t, record,
		d2Document{id: 1, metric: "lcp", cohort: d2Cohort("141.0"), at: at, windowStart: early, value: 1000, progress: 3, final: true},
		d2Document{id: 2, metric: "lcp", cohort: d2Cohort("141.0"), at: later, windowStart: at, value: 1100, final: true},
		d2Document{id: 3, metric: "lcp", cohort: d2Cohort("141.0"), at: at, windowStart: from, value: 9000, progress: 2},
	)
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	series, limits, err := rcaBrowserDocumentSeries(context.Background(), record, manifest, summaries, -1, 0)
	if err != nil || len(limits) != 0 || len(series) != 1 {
		t.Fatalf("series: %+v %v %v", series, limits, err)
	}
	s := series[0]
	w, exists := s.Document.Windows[at.UTC()]
	if !exists || !w.Start.Equal(early) || !w.End.Equal(later) || w.Invalid || len(s.Document.Windows) != 1 {
		t.Fatalf("final envelope: %+v", s.Document.Windows)
	}
	found := false
	for _, p := range s.Points {
		if p.Valid {
			found = true
			if !p.At.Equal(at) || p.Count != 2 || p.Value < 1000 || p.Value > 1110 {
				t.Fatalf("final population: %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("no final population")
	}
	// Even when the closing bucket is steady, its earlier source window is not.
	level := rca.LoadLevel{Start: at, End: at.Add(3 * bucket), VUsers: 10}
	params := rca.DefaultBrowserBaselineParams
	params.MinBaselineBuckets = 1
	out := rca.DetectBrowserBaseline(rca.DetectionInput{Series: series, Levels: []rca.LoadLevel{level}, Baseline: &rca.DetectionBaseline{TestID: "baseline", Qualification: rca.BaselineQualified, Series: series, Levels: []rca.LoadLevel{level}}}, params)
	if out == nil || len(out.Results) != 1 || out.Results[0].State != rca.BrowserBaselineUnscored || out.Results[0].Reason != rca.BrowserBaselineSourceWindowInvalid {
		t.Fatalf("cross-boundary window: %+v", out)
	}
	// The bounded projection must not retain window envelopes for omitted points.
	limited, truncation, err := rcaBrowserDocumentSeries(context.Background(), record, manifest, summaries, -1, 5)
	if incomplete, unreadable := rcaDocumentEvidenceLoss(truncation); !incomplete || unreadable {
		t.Fatalf("omitted later evidence must mark the projection incomplete: %+v", truncation)
	}
	if err != nil || len(limited) != 1 || len(limited[0].Document.Windows) != 0 {
		t.Fatalf("bounded projection: %+v %v", limited, err)
	}
}
