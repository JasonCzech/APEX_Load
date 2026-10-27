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
	"strings"
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
	if len(policy.EffectFloors.TimingByMetricMs) > 0 {
		out.MetricFloors = make(map[string]float64, len(policy.EffectFloors.TimingByMetricMs))
		for metric, value := range policy.EffectFloors.TimingByMetricMs {
			out.MetricFloors[metric] = value
		}
	}
	out.Mode, out.RunID = policy.Baseline.Mode, policy.Baseline.RunID
	if out.Mode == "pinned_run" {
		// Stored revisions may name a reference key. Compare canonical run
		// identities, while preserving unavailable pins for normal disclosure.
		run, lookupErr := s.store.GetTest(out.RunID)
		if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
			return nil, fmt.Errorf("the pinned baseline identity could not be read: %w", lookupErr)
		}
		if run != nil && normalizeProjectID(run.ProjectID) == normalizeProjectID(projectID) {
			out.RunID = run.ID
		}
	}
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
	proof, proofErr := readBrowserSourceProof(ctx, record, manifest)
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	// Remote owner clocks retain their evidence, but cannot be substituted for
	// the controller authority without a separately acquired causal envelope.
	localProof := proofErr == nil && proof != nil && manifest.BrowserProofAuthorityMode == "local" && len(manifest.BrowserProofExpectedOwners) == 1 && manifest.BrowserProofExpectedOwners[0] == "controller"
	var clockUnavailableReason string
	distributedProof := false
	if proofErr == nil && proof != nil && !localProof {
		if manifest.BrowserProofAuthorityMode == "distributed_envelope" && manifest.BrowserProofClockEnvelope != nil {
			distributedProof, clockUnavailableReason = proof.ValidateDistributedClocks()
		} else {
			clockUnavailableReason = "browser_clock_envelope_missing"
		}
	}
	// The journal must cover raw sources too, including censored observations.
	if proof != nil {
		for _, measurement := range accumulator.measurements {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			p, _, ok := proof.Context(measurement.observation.SourceGeneration)
			known := false
			if ok {
				for _, d := range p.Receipts.Closure.Documents {
					if d.DocumentID == measurement.observation.DocumentID && d.LifecycleID == measurement.observation.LifecycleID {
						known = true
						break
					}
				}
			}
			if !known {
				proof = nil
				localProof = false
				distributedProof = false
				break
			}
		}
	}
	hasClockProof := localProof || distributedProof
	finals, _ := browserDocumentFinals(accumulator, rcaBrowserDocumentMetrics, func(o protocol.BrowserDocumentObservation) bool {
		return hasClockProof || !o.WindowEnd.Before(from) && o.WindowEnd.Before(to)
	})
	clockProofComplete := hasClockProof
	type tupleSeries struct {
		tuple            protocol.BrowserDocumentMetricSummary
		buckets          map[int]*protocol.BrowserMetricDistribution
		windows          map[int]rca.DocumentWindow
		mapped           bool
		clockUncertainty time.Duration
	}
	tuples := map[string]*tupleSeries{}
	for _, f := range finals {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		mappedVUsers := 0
		mappedSpanID := ""
		var clockUncertainty time.Duration
		if localProof {
			window, spans, mapErr := proof.Map(f.observation)
			if mapErr == nil {
				p, _, _ := proof.Context(f.observation.SourceGeneration)
				// These UTC values label the owner's monotonic bounds. They are not
				// interpolated browser event times or cross-telemetry alignment claims.
				f.start = p.AuthorityStartedAt.Add(time.Duration(window.StartNS))
				f.at = p.AuthorityStartedAt.Add(time.Duration(window.EndNS))
				for _, span := range spans {
					if protocol.ContainedInBrowserSteadySpan(window, []protocol.BrowserMonotonicSpan{span.BrowserMonotonicSpan}) {
						mappedVUsers = span.VUsers
						mappedSpanID = sha256BytesHex([]byte(fmt.Sprintf("%s/%s/%s/%d/%d", span.Authority.RunID, span.Authority.Incarnation, span.Authority.DomainID, span.StartNS, span.EndNS)))
						break
					}
				}
			}
			if mapErr != nil {
				clockProofComplete = false
			}
		} else if distributedProof {
			window, uncertainty, spans, mapErr := proof.MapDistributed(f.observation)
			if mapErr == nil {
				clockUncertainty = uncertainty
				authorityStart := manifest.BrowserProofClockEnvelope.AuthorityStartedAt.UTC()
				f.start = authorityStart.Add(time.Duration(window.StartNS))
				f.at = authorityStart.Add(time.Duration(window.EndNS))
				for _, span := range spans {
					if protocol.ContainedInBrowserSteadySpan(window, []protocol.BrowserMonotonicSpan{span.BrowserMonotonicSpan}) {
						mappedVUsers = span.VUsers
						mappedSpanID = sha256BytesHex([]byte(fmt.Sprintf("%s/%s/%s/%d/%d", span.Authority.RunID, span.Authority.Incarnation, span.Authority.DomainID, span.StartNS, span.EndNS)))
						break
					}
				}
			}
			if mapErr != nil {
				clockProofComplete = false
				if clockUnavailableReason == "" {
					clockUnavailableReason = "browser_clock_projection_failed"
				}
			}
		}
		if f.at.Before(from) || !f.at.Before(to) {
			if hasClockProof {
				clockProofComplete = false
			}
			continue
		}

		t := tuples[f.key]
		if t == nil {
			t = &tupleSeries{tuple: f.tuple, buckets: map[int]*protocol.BrowserMetricDistribution{}, windows: map[int]rca.DocumentWindow{}, mapped: hasClockProof}
			tuples[f.key] = t
		}
		if clockUncertainty > t.clockUncertainty {
			t.clockUncertainty = clockUncertainty
		}
		i := int(f.at.Sub(from) / bucket)
		if t.buckets[i] == nil {
			t.buckets[i] = &protocol.BrowserMetricDistribution{}
		}
		t.buckets[i].Add(f.value)
		w, exists := t.windows[i]
		if !exists || f.start.Before(w.Start) {
			w.Start = f.start
		}
		if !exists || f.at.After(w.End) {
			w.End = f.at
		}
		validMap := mappedVUsers > 0 && (!exists || w.MonotonicVerified && w.MonotonicVUsers == mappedVUsers && w.MonotonicSpanID == mappedSpanID)
		w.MonotonicVerified = validMap
		w.MonotonicVUsers = mappedVUsers
		w.MonotonicSpanID = mappedSpanID
		if distributedProof && clockUncertainty > bucket {
			w.Invalid = true
			w.InvalidReason = "observation_window_exceeds_bucket"
		}
		t.mapped = t.mapped && clockProofComplete
		w.Invalid = w.Invalid || hasClockProof && !validMap
		w.Invalid = w.Invalid || f.start.IsZero() || f.at.IsZero() || f.at.Before(f.start)
		t.windows[i] = w
	}
	var limitations []rca.Limitation
	if accumulator.incomplete {
		limitations = append(limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: "the browser document evidence is incomplete; dropped document observations were not judged"})
	}
	scope := archiveIdentityScope(manifest)
	n := int(to.Sub(from) / bucket)
	if maxPoints > 0 && n > maxPoints {
		limitations = append(limitations, rca.Limitation{Code: limitationDetectionEvidenceCut, Detail: fmt.Sprintf("the point budget left out %d browser document buckets", n-maxPoints)})
		n = maxPoints
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
		if t.mapped && clockProofComplete {
			s.Document.ClockQualification = rca.BrowserEvidenceQualified
			s.Document.TimelineQualification = rca.BrowserEvidenceUnavailable
			s.Document.ClockUncertainty = t.clockUncertainty
		} else if clockUnavailableReason != "" {
			s.Document.ClockUnavailableReason = clockUnavailableReason
		}
		if proof != nil && proofErr == nil {
			s.Document.SourceCoverageQualification = rca.BrowserEvidenceQualified
		}
		s.Document.Windows = make(map[time.Time]rca.DocumentWindow, min(n, len(t.windows)))
		for i := 0; i < n; i++ {
			point := rca.SeriesPoint{At: from.Add(time.Duration(i) * bucket)}
			if d := t.buckets[i]; d != nil {
				if point.Value, point.Valid = d.Quantile(0.75); point.Valid {
					point.Count = int32(min(d.Count, math.MaxInt32))
					s.Document.Windows[point.At.UTC()] = t.windows[i]
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
	// Retained v1 browser documents have no browser-to-host calibration or
	// expected-source/terminal ledger. Do not infer either from generator clock
	// samples, local execution, or successful archive collection.
	return &rca.DocumentCohort{ClockQualification: rca.BrowserEvidenceUnavailable, SourceCoverageQualification: rca.BrowserEvidenceUnavailable, Metric: tuple.Metric, Group: tuple.Group, Family: browserDocumentTupleDigest(protocol.BrowserDocumentMetricKey(family)), Fields: browserCohortFields(tuple.Cohort), Incomplete: incomplete}
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

// rcaBrowserScriptChangedGroups requires original authored identity in addition
// to executable identity. Runtime overrides can make different original sources
// execute alike, while equal originals do not prove equal runtime behavior.
// Missing historical provenance remains unknown; it is never reconstructed from
// executable digests. Only browser detection consumes this additional guard.
func rcaBrowserScriptChangedGroups(current, candidate rca.WorkloadKey, currentManifest, candidateManifest *RunArchiveManifest, observed ...rca.Series) map[string]bool {
	changed := rca.ScriptChangedGroups(current, candidate)
	source := func(manifest *RunArchiveManifest, group string) protocol.BrowserAuthoredDefinition {
		if manifest == nil {
			return protocol.BrowserAuthoredDefinition{}
		}
		if manifest.InitialProvenance != nil {
			return manifest.InitialProvenance.BrowserAuthoredSources[group]
		}
		return manifest.Provenance.BrowserAuthoredSources[group]
	}
	groups := make(map[string]bool, len(current.Groups))
	for group := range current.Groups {
		groups[group] = true
	}
	// Retained observations may have blank or foreign groups. They must not
	// acquire identity merely because neither workload names that group.
	for _, series := range observed {
		if series.Kind == rca.SeriesBrowserDocument && series.Document != nil {
			groups[series.Document.Group] = true
		}
	}
	for group := range groups {
		_, inCurrent := current.Groups[group]
		_, inCandidate := candidate.Groups[group]
		if strings.TrimSpace(group) == "" || !inCurrent || !inCandidate {
			changed[group] = true
			continue
		}
		a, b := source(currentManifest, group), source(candidateManifest, group)
		if !a.Valid() || !b.Valid() || a != b {
			changed[group] = true
		}
	}
	return changed
}
