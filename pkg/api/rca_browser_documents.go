package api

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// rcaBrowserDocumentMetrics are the document metrics RCA judges, those the
// metric catalog defines.
var rcaBrowserDocumentMetrics = []string{"fcp", "lcp", "cls", "inp"}

// rcaBrowserBaselinePolicy reads what the run's pinned browser_policy sets
// for its browser baselines. A run that pinned none has nil (the defaults). A
// pinned revision that is missing, no longer matches its pin or is invalid
// is reported and the defaults apply; a store that fails to read it fails
// the analysis, which can be retried.
func (s *Server) rcaBrowserBaselinePolicy(projectID string, manifest *RunArchiveManifest) (*rca.BrowserBaselinePolicy, error) {
	var pin *RunPolicyPin
	for index := range manifest.Provenance.PolicyPins {
		if manifest.Provenance.PolicyPins[index].Kind == storage.AnalysisDefinitionKindBrowserPolicy {
			pin = &manifest.Provenance.PolicyPins[index]
		}
	}
	if pin == nil {
		return nil, nil
	}
	out := &rca.BrowserBaselinePolicy{Ref: fmt.Sprintf("%s@%d", pin.ID, pin.Revision)}
	store, ok := s.store.(storage.AnalysisDefinitionStore)
	if !ok {
		return nil, errors.New("analysis policies are unavailable")
	}
	definition, err := store.GetAnalysisDefinition(normalizeProjectID(projectID), pin.ID, pin.Revision)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("the run's pinned browser policy could not be read: %w", err)
	}
	if err != nil || definition == nil || definition.Kind != storage.AnalysisDefinitionKindBrowserPolicy {
		out.Unavailable = "its revision was not found"
		return out, nil //nolint:nilerr // a missing pinned revision is reported in Unavailable and the defaults apply
	}
	if digest := sha256.Sum256(definition.Definition); hex.EncodeToString(digest[:]) != pin.SHA256 {
		out.Unavailable = "its revision no longer matches the pin"
		return out, nil
	}
	var policy BrowserPolicyDefinition
	if json.Unmarshal(definition.Definition, &policy) != nil || policy.validate() != nil {
		out.Unavailable = "its revision is invalid"
		return out, nil //nolint:nilerr // an invalid pinned revision is reported in Unavailable and the defaults apply
	}
	out.TimingMs, out.TimingRelative, out.CLS = policy.EffectFloors.TimingMs, policy.EffectFloors.TimingRelative, policy.EffectFloors.CLS
	out.Mode, out.RunID = policy.Baseline.Mode, policy.Baseline.RunID
	return out, nil
}

// rcaDocumentEvidenceLoss reads what rcaBrowserDocumentSeries's limitations
// say about the run's document evidence: each says some is missing
// (dropped observations or series the budget left out), and an unreadable
// sidecar that none could be read.
func rcaDocumentEvidenceLoss(limitations []rca.Limitation) (incomplete, unreadable bool) {
	for _, limitation := range limitations {
		if limitation.Code == limitationDetectionEvidenceUnreadable {
			unreadable = true
		} else {
			incomplete = true
		}
	}
	return incomplete, unreadable
}

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
		return nil, []rca.Limitation{{Code: limitationDetectionEvidenceUnreadable, Detail: "the browser document evidence could not be read: " + err.Error()}}, nil //nolint:nilerr // unreadable evidence is a limitation; only cancellation fails the read
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
			Identity: rcaRunSeriesIdentity(scope, "browser_document", digest, metric, t.tuple.Unit, analysis.KindObservation, bucket),
			Document: rcaDocumentCohort(t.tuple, accumulator.incomplete)}
		for i := 0; i < n; i++ {
			point := rca.SeriesPoint{At: from.Add(time.Duration(i) * bucket)}
			if d := t.buckets[i]; d != nil {
				if point.Value, point.Valid = d.Quantile(0.75); point.Valid {
					point.Count = int32(min(d.Count, math.MaxInt32))
				}
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

// rcaDocumentCohort describes a document series for the browser baseline:
// its metric, its measurement tuple without the runtime cohort (the family
// a changed cohort is found in) and the runtime cohort's fields.
func rcaDocumentCohort(tuple protocol.BrowserDocumentMetricSummary, incomplete bool) *rca.DocumentCohort {
	family := tuple
	family.Cohort = nil
	return &rca.DocumentCohort{Metric: tuple.Metric, Group: tuple.Group, Family: browserDocumentTupleDigest(protocol.BrowserDocumentMetricKey(family)), Fields: browserCohortFields(tuple.Cohort), Incomplete: incomplete}
}

// browserCohortFields are a runtime cohort's fields as text, so two cohorts'
// differences can be named; an unavailable cohort has one field.
func browserCohortFields(c *protocol.BrowserRuntimeCohort) map[string]string {
	if c == nil {
		return map[string]string{"cohort": "unavailable"}
	}
	optional := func(value *bool) string {
		if value == nil {
			return "unrecorded"
		}
		return strconv.FormatBool(*value)
	}
	return map[string]string{
		"browser_version": c.BrowserVersion, "platform": c.Platform, "viewport": fmt.Sprintf("%dx%d", c.ViewportWidth, c.ViewportHeight),
		"headless": optional(c.Headless), "mobile": optional(c.Mobile), "touch": optional(c.Touch),
		"device_scale_factor": strconv.FormatFloat(c.DeviceScaleFactor, 'g', -1, 64), "cpu_throttle_rate": strconv.FormatFloat(c.CPUThrottleRate, 'g', -1, 64),
		"network_latency_ms": strconv.Itoa(c.NetworkLatencyMS), "network_bandwidth_kbps": strconv.Itoa(c.NetworkBandwidthKbps), "shaping_applied": strconv.FormatBool(c.ShapingApplied),
		"cache_enabled": optional(c.CacheEnabled), "cache_content_urls": optional(c.CacheContentURLs), "check_newer_versions": optional(c.CheckNewerVersions),
		"new_user_policy": c.NewUserPolicy, "new_user_policy_applied": strconv.FormatBool(c.NewUserPolicyApplied), "unavailable_reason": c.UnavailableReason,
	}
}
