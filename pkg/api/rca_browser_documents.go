package api

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// rcaBrowserDocumentMetrics are the document metrics RCA judges, those the
// metric catalog defines.
var rcaBrowserDocumentMetrics = []string{"fcp", "lcp", "cls", "inp"}

// rcaBrowserDocumentSeries reads the run's browser document metrics as
// detection series on the run's interval grid: per measurement tuple, the
// p75 of the final eligible document measurements whose window closed in
// each bucket. Each measurement counts once, at its latest retained state; a
// bucket without a final document is a gap, never zero. At most limit series
// (limit < 0 means unbounded) of at most maxPoints buckets are read; what
// cannot be read is a limitation, and only cancellation fails the read.
func rcaBrowserDocumentSeries(ctx context.Context, record *storage.RunArchiveRecord, manifest *RunArchiveManifest, summaries []IntervalSummaryRecord, limit, maxPoints int) ([]rca.Series, []rca.Limitation, error) {
	from, to, bucket, ok := rcaImportedWindow(summaries)
	if !ok || manifest.BrowserDocumentsFile == "" {
		return nil, nil, nil
	}
	accumulator, _, err := readArchiveBrowserDocumentMeasurements(ctx, record, manifest)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, nil, ctxErr
	}
	if err != nil {
		return nil, []rca.Limitation{{Code: limitationDetectionEvidenceUnreadable, Detail: "the browser document evidence could not be read: " + err.Error()}}, nil
	}
	if accumulator == nil {
		return nil, nil, nil
	}
	finals, _ := browserDocumentFinals(accumulator, rcaBrowserDocumentMetrics, func(o protocol.BrowserDocumentObservation) bool {
		return !o.WindowEnd.Before(from) && o.WindowEnd.Before(to)
	})
	type tupleSeries struct {
		tuple   protocol.BrowserDocumentMetricSummary
		buckets map[int]*protocol.BrowserMetricDistribution
	}
	tuples := map[string]*tupleSeries{}
	for _, f := range finals {
		t := tuples[f.key]
		if t == nil {
			t = &tupleSeries{tuple: f.tuple, buckets: map[int]*protocol.BrowserMetricDistribution{}}
			tuples[f.key] = t
		}
		i := int(f.at.Sub(from) / bucket)
		if t.buckets[i] == nil {
			t.buckets[i] = &protocol.BrowserMetricDistribution{}
		}
		t.buckets[i].Add(f.value)
	}
	var limitations []rca.Limitation
	if accumulator.incomplete {
		limitations = append(limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: "the browser document evidence is incomplete; dropped document observations were not judged"})
	}
	scope := archiveIdentityScope(manifest)
	n := int(to.Sub(from) / bucket)
	if maxPoints > 0 {
		n = min(n, maxPoints)
	}
	var series []rca.Series
	for key, t := range tuples {
		digest := browserDocumentTupleDigest(key)
		label := browserDocumentTupleLabel(t.tuple)
		metric := t.tuple.Metric + "_p75"
		s := rca.Series{ID: "browser_document/" + t.tuple.Metric + "/" + digest[:12] + "/p75", Kind: rca.SeriesBrowserDocument, Unit: t.tuple.Unit, Observation: rca.ObservationInterval, Bucket: bucket,
			Evidence: rca.EvidenceRef{Kind: "browser_document", Locator: browserDocumentsFileName + "#" + label},
			Identity: rcaRunSeriesIdentity(scope, "browser_document", digest, metric, t.tuple.Unit, analysis.KindObservation, bucket)}
		for i := 0; i < n; i++ {
			point := rca.SeriesPoint{At: from.Add(time.Duration(i) * bucket)}
			if d := t.buckets[i]; d != nil {
				point.Value, point.Valid = d.Quantile(0.75)
			}
			s.Points = append(s.Points, point)
		}
		series = append(series, s)
	}
	// A stable order by ID, which starts with the metric.
	slices.SortFunc(series, func(a, b rca.Series) int { return cmp.Compare(a.ID, b.ID) })
	if limit >= 0 && len(series) > limit {
		limitations = append(limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: fmt.Sprintf("the series budget left out %d browser document series", len(series)-limit)})
		series = series[:limit]
	}
	return series, limitations, nil
}
