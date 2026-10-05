package protocol

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
)

func browserTestCohort() *BrowserRuntimeCohort {
	yes, no := true, false
	return &BrowserRuntimeCohort{Version: 1, BrowserVersion: "141.0.7390.37", Platform: "linux", ViewportWidth: 1440, ViewportHeight: 900, Headless: &yes, Mobile: &no, Touch: &no, DeviceScaleFactor: 1, CPUThrottleRate: 1, ShapingApplied: true, CacheEnabled: &yes, CacheContentURLs: &yes, CheckNewerVersions: &no, NewUserPolicy: "persistent_context", NewUserPolicyApplied: true}
}
func browserTestObservation() BrowserDocumentObservation {
	gen := "0123456789abcdef0123456789abcdef"
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	value := 0.1
	return BrowserDocumentObservation{Version: BrowserEvidenceVersion, SourceGeneration: gen, DocumentID: gen + "-d1", LifecycleID: gen + "-l1", FrameID: gen + "-f1", NavigationID: gen + "-n1", MeasurementID: gen + "-d1/" + gen + "-l1/cls", Revision: 1, Metric: "cls", Algorithm: BrowserVitalsAlgorithm, AlgorithmSHA256: BrowserVitalsAlgorithmSHA256, Unit: "score", Engine: "chromium", Cohort: browserTestCohort(), FrameScope: "main_frame", LifecyclePhase: "navigation", State: "final", Boundary: "pagehide", WindowStart: now, WindowEnd: now.Add(time.Second), CapturedAt: now.Add(time.Second), Available: true, Qualified: true, Value: &value}
}
func TestBrowserMetricDistributionFractionalObservationsAndMerge(t *testing.T) {
	for _, count := range []int{1000000, 10000000} {
		var all, first, second BrowserMetricDistribution
		for i := 0; i < count; i++ {
			all.Add(0.1)
			if i < count/2 {
				first.Add(0.1)
			} else {
				second.Add(0.1)
			}
		}
		if !all.Valid() || !first.Valid() || !second.Valid() || !first.Merge(second) || !first.Valid() || first.Count != all.Count {
			t.Fatalf("valid fractional observations rejected at count %d", count)
		}
		forged := all
		forged.Sum += math.Max(1, all.Sum*0.001)
		if forged.Valid() {
			t.Fatal("forged sum admitted")
		}
		forged = all
		forged.Min = 0.2
		if forged.Valid() {
			t.Fatal("forged min admitted")
		}
		forged = all
		forged.Max = 0.05
		if forged.Valid() {
			t.Fatal("forged max admitted")
		}
	}
}
func TestBrowserDocumentQualificationRequiresActualCohort(t *testing.T) {
	original := browserTestObservation()
	if !original.Valid() {
		t.Fatal("complete observation refused")
	}
	cases := map[string]func(*BrowserDocumentObservation){
		"absent":   func(o *BrowserDocumentObservation) { o.Cohort = nil },
		"version":  func(o *BrowserDocumentObservation) { o.Cohort.BrowserVersion = "" },
		"platform": func(o *BrowserDocumentObservation) { o.Cohort.Platform = "" },
		"viewport": func(o *BrowserDocumentObservation) { o.Cohort.ViewportWidth = 0 },
		"headless": func(o *BrowserDocumentObservation) { o.Cohort.Headless = nil },
		"cache":    func(o *BrowserDocumentObservation) { o.Cohort.CacheEnabled = nil },
		"shaping":  func(o *BrowserDocumentObservation) { o.Cohort.ShapingApplied = false },
		"policy":   func(o *BrowserDocumentObservation) { o.Cohort.NewUserPolicyApplied = false },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			o := original
			o.Cohort = original.Cohort.Clone()
			change(&o)
			if o.Valid() {
				t.Fatal("unknown cohort qualified")
			}
			o.Qualified = false
			if !o.Valid() {
				t.Fatal("truthful unqualified observation refused")
			}
		})
	}
}
func TestBrowserDocumentLifecycleIdentityAndClones(t *testing.T) {
	o := browserTestObservation()
	e := (&BrowserDocumentEvidence{Version: BrowserEvidenceVersion, SourceGeneration: o.SourceGeneration, Observations: []BrowserDocumentObservation{o}}).Clone()
	if e == nil {
		t.Fatal("clone refused")
	}
	*e.Observations[0].Value = 0.5
	*e.Observations[0].Cohort.CacheEnabled = false
	if *o.Value != 0.1 || !*o.Cohort.CacheEnabled {
		t.Fatal("clone aliases provider memory")
	}
	restored := o
	restored.LifecycleID = o.SourceGeneration + "-l2"
	restored.LifecyclePhase = "bfcache_restore"
	if restored.Valid() {
		t.Fatal("restored visit reused measurement identity")
	}
	restored.MeasurementID = restored.DocumentID + "/" + restored.LifecycleID + "/cls"
	if !restored.Valid() {
		t.Fatal("restored lifecycle refused")
	}
	o.DeviceProfile = "profile\x00other"
	if o.Valid() {
		t.Fatal("tuple separator injection admitted")
	}
}
func TestBrowserDocumentSummarySeparatesCohortsAndMetricDenominators(t *testing.T) {
	var d BrowserMetricDistribution
	d.Add(0.1)
	metric := BrowserDocumentMetricSummary{Metric: "cls", Algorithm: BrowserVitalsAlgorithm, AlgorithmSHA256: BrowserVitalsAlgorithmSHA256, Unit: "score", Engine: "chromium", Cohort: browserTestCohort(), FrameScope: "main_frame", LifecyclePhase: "navigation", EligibleFinal: 1, Distribution: d}
	next := metric
	next.Cohort = metric.Cohort.Clone()
	next.Cohort.BrowserVersion = "142.0"
	var merged *BrowserDocumentSummary
	MergeBrowserDocumentSummaries(&merged, &BrowserDocumentSummary{Version: BrowserEvidenceVersion, Metrics: []BrowserDocumentMetricSummary{metric}})
	MergeBrowserDocumentSummaries(&merged, &BrowserDocumentSummary{Version: BrowserEvidenceVersion, Metrics: []BrowserDocumentMetricSummary{next}})
	if merged == nil || len(merged.Metrics) != 2 || merged.Clone() == nil {
		t.Fatal("distinct actual browser versions merged")
	}
	metric.Cohort = nil
	if (&BrowserDocumentSummary{Version: BrowserEvidenceVersion, Metrics: []BrowserDocumentMetricSummary{metric}}).Clone() != nil {
		t.Fatal("qualified samples admitted without cohort")
	}
}

func TestBrowserMetricDistributionRejectsUnsupportedBinsAndAdmitsRoundedBoundaries(t *testing.T) {
	forged := BrowserMetricDistribution{Count: 1}
	forged.Buckets[100] = 1
	if forged.Valid() {
		t.Fatal("high bucket invented quantile for observed zero")
	}
	for i := 0; i < 190; i++ {
		value := 0.000001 * math.Pow(1.2, float64(i))
		if value > 86400000 {
			break
		}
		for _, v := range []float64{value, math.Nextafter(value, 0), math.Nextafter(value, math.Inf(1))} {
			var d BrowserMetricDistribution
			d.Add(v)
			if !d.Valid() {
				t.Fatalf("legitimate boundary refused at bucket %d: %g", i, v)
			}
		}
	}
}
func TestBrowserResourceOriginRejectsUnsanitizedIdentity(t *testing.T) {
	for _, bad := range []string{"https://user:secret@example.com", "https://example.com/path", "https://example.com?secret=x", "https://example.com\x00", "https://example.com\n", "https://example.com/\xff", "javascript:secret"} {
		if browserEvidenceOrigin(bad) {
			t.Fatalf("unsanitized origin admitted: %q", bad)
		}
	}
	for _, good := range []string{"", "https://example.com", "http://[::1]:1234"} {
		if !browserEvidenceOrigin(good) {
			t.Fatalf("native origin refused: %q", good)
		}
	}
}

func TestSummarizeBrowserDocumentsRetainsIndependentStatesAndRejectsDuplicateIDs(t *testing.T) {
	base := browserTestObservation()
	observations := []BrowserDocumentObservation{base}
	for i, state := range []string{"unavailable", "unqualified", "censored", "provisional"} {
		o := base
		o.DocumentID = fmt.Sprintf("%s-d%d", o.SourceGeneration, i+2)
		o.MeasurementID = o.DocumentID + "/" + o.LifecycleID + "/" + o.Metric
		o.Qualified = false
		switch state {
		case "unavailable":
			o.Available = false
			o.Value = nil
			o.UnavailableReason = "unsupported"
		case "unqualified":
			o.Cohort = nil
		case "censored":
			o.State = "censored"
			o.Boundary = "cancelled"
			o.Available = false
			o.Value = nil
			o.UnavailableReason = "source_censored"
		case "provisional":
			o.State = "provisional"
			o.Boundary = "hidden"
		}
		observations = append(observations, o)
	}
	summary := SummarizeBrowserDocumentObservations(observations, 3, true)
	if summary == nil || summary.Clone() == nil || summary.DroppedObservations != 3 || !summary.Incomplete {
		t.Fatal("bounded evidence summary lost coverage")
	}
	var final, unavailable, unqualified, censored, provisional, population int64
	for _, m := range summary.Metrics {
		final += m.EligibleFinal
		unavailable += m.UnavailableFinal
		unqualified += m.UnqualifiedFinal
		censored += m.Censored
		provisional += m.Provisional
		population += m.Distribution.Count
	}
	if final != 1 || unavailable != 1 || unqualified != 1 || censored != 1 || provisional != 1 || population != 1 {
		t.Fatalf("document populations mixed: %#v", summary)
	}
	duplicates := SummarizeBrowserDocumentObservations([]BrowserDocumentObservation{base, base}, 0, false)
	if duplicates == nil || !duplicates.Incomplete || duplicates.DroppedObservations != 1 || duplicates.Metrics[0].EligibleFinal != 1 {
		t.Fatal("duplicate identity counted twice")
	}
	if (&BrowserDocumentEvidence{Version: 2, SourceGeneration: base.SourceGeneration, Observations: []BrowserDocumentObservation{base, base}}).Clone() != nil {
		t.Fatal("ambiguous repeated source observation admitted")
	}
	if SummarizeBrowserDocumentObservations(nil, 0, false) != nil {
		t.Fatal("empty complete input invented observations")
	}
}
func TestSummarizeBrowserDocumentsBoundsTotalObservationsAndTuples(t *testing.T) {
	observations := make([]BrowserDocumentObservation, 4100)
	for i := range observations {
		o := browserTestObservation()
		o.DocumentID = fmt.Sprintf("%s-d%d", o.SourceGeneration, i)
		o.MeasurementID = o.DocumentID + "/" + o.LifecycleID + "/" + o.Metric
		observations[i] = o
	}
	summary := SummarizeBrowserDocumentObservations(observations, 0, false)
	if summary == nil || !summary.Incomplete || summary.DroppedObservations != 4 || summary.Metrics[0].EligibleFinal != 4096 {
		t.Fatal("total observation budget not enforced")
	}
	for i := range observations[:65] {
		observations[i].DeviceProfile = fmt.Sprint(i)
	}
	summary = SummarizeBrowserDocumentObservations(observations[:65], 0, false)
	if summary == nil || !summary.Incomplete || summary.DroppedObservations != 1 || len(summary.Metrics) != 64 {
		t.Fatal("tuple budget not enforced")
	}
}

func TestDecodeBrowserDocumentEvidenceUsesBoundedStrictAdmission(t *testing.T) {
	o := browserTestObservation()
	evidence := BrowserDocumentEvidence{Version: 2, SourceGeneration: o.SourceGeneration, Observations: []BrowserDocumentObservation{o}}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeBrowserDocumentEvidence(raw)
	if err != nil || decoded == nil {
		t.Fatal("valid evidence refused")
	}
	*decoded.Observations[0].Value = 0.2
	if *o.Value != 0.1 {
		t.Fatal("decoded evidence aliases original")
	}
	for _, bad := range []string{"null", "{}", string(raw) + "{}", strings.TrimSuffix(string(raw), "}") + `,"extra":true}`, strings.Repeat(" ", MaxBrowserDocumentEvidenceBytes+1)} {
		if got, err := DecodeBrowserDocumentEvidence([]byte(bad)); err == nil || got != nil {
			t.Fatal("malformed sidecar admitted")
		}
	}
}

func TestPackBrowserDocumentEvidencePreservesExplicitOversizeCoverage(t *testing.T) {
	o := browserTestObservation()
	evidence := BrowserDocumentEvidence{Version: 2, SourceGeneration: o.SourceGeneration}
	for i := 0; i < 256; i++ {
		o := o
		o.DocumentID = fmt.Sprintf("%s-d%d", o.SourceGeneration, i)
		o.MeasurementID = o.DocumentID + "/" + o.LifecycleID + "/" + o.Metric
		o.Group = strings.Repeat("&", 256)
		o.Region = strings.Repeat("&", 256)
		o.DeviceProfile = strings.Repeat("&", 128)
		o.Cohort = o.Cohort.Clone()
		o.Cohort.BrowserVersion = strings.Repeat("&", 128)
		evidence.Observations = append(evidence.Observations, o)
	}
	raw, err := json.Marshal(evidence)
	if err != nil || len(raw) <= MaxBrowserDocumentEvidenceBytes {
		t.Fatal("fixture does not exceed encoded record budget")
	}
	packed := PackBrowserDocumentEvidence(&evidence)
	if packed == nil || !packed.Incomplete || packed.DroppedObservations <= 0 || len(packed.Observations)+int(packed.DroppedObservations) != 256 {
		t.Fatal("oversize omission hidden")
	}
	raw, err = json.Marshal(packed)
	if err != nil || len(raw) > MaxBrowserDocumentEvidenceBytes {
		t.Fatal("packed evidence exceeds limit")
	}
	decoded, err := DecodeBrowserDocumentEvidence(raw)
	if err != nil || decoded == nil {
		t.Fatal("producer packing and strict consumer disagree")
	}
	if len(evidence.Observations) != 256 || evidence.Incomplete {
		t.Fatal("packing mutated provider snapshot")
	}
}

func TestBrowserDocumentCohortAdmitsActuallyAppliedFractionalScale(t *testing.T) {
	cohort := browserTestCohort()
	cohort.DeviceScaleFactor = 0.5
	if !cohort.Eligible() {
		t.Fatal("valid native fractional scale excluded")
	}
	cohort.DeviceScaleFactor = 0
	if cohort.Eligible() {
		t.Fatal("absent native scale qualified")
	}
}
