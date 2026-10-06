package api

import (
	"math"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
)

// maxRCAExcerptPoints bounds the points one evidence excerpt keeps.
const maxRCAExcerptPoints = 24

// RCAEvidenceExcerpt is the part of a series an anomaly's evidence reference
// names, frozen with the analysis so an agent can cite what it read: the
// points from just before the onset's uncertainty to the anomaly's end.
type RCAEvidenceExcerpt struct {
	// RefID is the evidence reference's ID; hypotheses cite the same IDs.
	RefID     string `json:"ref_id"`
	AnomalyID string `json:"anomaly_id"`
	Series    string `json:"series"`
	Unit      string `json:"unit,omitempty"`
	Locator   string `json:"locator"`
	// Points are the series' values in the window; a nil value is an
	// interval the series could not judge.
	Points []RCAExcerptPoint `json:"points"`
	// Sampled says the window held more points than an excerpt keeps.
	Sampled bool `json:"sampled,omitempty"`
}

type RCAExcerptPoint struct {
	At    time.Time `json:"at"`
	Value *float64  `json:"value"`
}

// rcaEvidenceExcerpts takes one excerpt per anomaly evidence reference from
// the series the analysis judged.
func rcaEvidenceExcerpts(a *rca.Assessment, series []rca.Series) []RCAEvidenceExcerpt {
	byID := make(map[string]*rca.Series, len(series))
	for i := range series {
		byID[series[i].ID] = &series[i]
	}
	out := []RCAEvidenceExcerpt{}
	for _, anomaly := range a.Anomalies {
		s := byID[anomaly.Series]
		if s == nil {
			continue
		}
		bucket := s.Bucket
		if bucket <= 0 {
			bucket = 10 * time.Second
		}
		from := anomaly.Onset.Add(-anomaly.OnsetUncertainty - 3*bucket)
		to := anomaly.End.Add(bucket)
		var points []RCAExcerptPoint
		for _, p := range s.Points {
			if p.At.Before(from) || p.At.After(to) {
				continue
			}
			point := RCAExcerptPoint{At: p.At.UTC()}
			if p.Valid && !math.IsNaN(p.Value) && !math.IsInf(p.Value, 0) {
				value := math.Round(p.Value*1000) / 1000
				point.Value = &value
			}
			points = append(points, point)
		}
		sampled := false
		if len(points) > maxRCAExcerptPoints {
			points, sampled = sampleExcerptPoints(points, maxRCAExcerptPoints), true
		}
		for _, ref := range anomaly.Evidence {
			out = append(out, RCAEvidenceExcerpt{
				RefID: ref.ID, AnomalyID: anomaly.ID, Series: anomaly.Series, Unit: s.Unit, Locator: ref.Locator,
				Points: append([]RCAExcerptPoint{}, points...), Sampled: sampled,
			})
		}
	}
	return out
}

// sampleExcerptPoints keeps n points spread evenly, first and last included.
func sampleExcerptPoints(points []RCAExcerptPoint, n int) []RCAExcerptPoint {
	out := make([]RCAExcerptPoint, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, points[i*(len(points)-1)/(n-1)])
	}
	return out
}

// citedExcerpts keeps the excerpts whose anomalies the assessment still
// carries, in their order.
func citedExcerpts(a *rca.Assessment, excerpts []RCAEvidenceExcerpt) []RCAEvidenceExcerpt {
	kept := map[string]bool{}
	for _, anomaly := range a.Anomalies {
		kept[anomaly.ID] = true
	}
	out := excerpts[:0:0]
	for _, e := range excerpts {
		if kept[e.AnomalyID] {
			out = append(out, e)
		}
	}
	return out
}

// dropUncitedRCAExcerpts drops, unless everything fits, the excerpts of
// anomalies no hypothesis cites. It reports whether it cut.
func dropUncitedRCAExcerpts(a *rca.Assessment, excerpts *[]RCAEvidenceExcerpt, fits func() bool) bool {
	if fits() || len(*excerpts) == 0 {
		return false
	}
	cited := citedAnomalies(a)
	keep := []RCAEvidenceExcerpt{}
	for _, e := range *excerpts {
		if cited[e.AnomalyID] {
			keep = append(keep, e)
		}
	}
	cut := len(keep) != len(*excerpts)
	*excerpts = keep
	return cut
}

// halveRCAExcerpts drops excerpts from the end until fits reports true.
// Hypotheses are stored best first, and so are the anomalies they cite
// first. It reports whether it cut.
func halveRCAExcerpts(excerpts *[]RCAEvidenceExcerpt, fits func() bool) bool {
	cut := false
	for !fits() && len(*excerpts) > 0 {
		*excerpts, cut = (*excerpts)[:len(*excerpts)/2], true
	}
	return cut
}
