package api

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/rca"
	"github.com/jasonczech/apexload/pkg/storage"
)

// narrationTestBundle is reportRootCauseTestAssessment's bundle with one
// excerpt per anomaly.
func narrationTestBundle() RCABundle {
	a := reportRootCauseTestAssessment()
	a.Anomalies[0].Evidence = []rca.EvidenceRef{{ID: "evidence-00000000000000c1", Kind: "monitor_series", Locator: "monitor_points.jsonl#cpu"}}
	a.Anomalies[0].OnsetUncertainty = 10 * time.Second
	a.Anomalies[1].Evidence = []rca.EvidenceRef{{ID: "evidence-00000000000000a1", Kind: "interval", Locator: "interval_distributions.jsonl#transaction=Checkout"}}
	a.Anomalies[0].ID, a.Anomalies[1].ID = "anomaly-00000000000000c1", "anomaly-00000000000000a1"
	a.Hypotheses[0].ID, a.Hypotheses[1].ID = "hypothesis-0000000000000001", "hypothesis-0000000000000002"
	a.Hypotheses[0].Symptoms, a.Hypotheses[1].Symptoms = []string{"anomaly-00000000000000a1"}, []string{"anomaly-00000000000000a1"}
	a.Hypotheses[0].Chain = []rca.ChainStep{{From: "anomaly-00000000000000c1", To: "anomaly-00000000000000a1", Relation: rca.RelationPrecedes}}
	a.Hypotheses[0].Score = 0.82
	p95 := 3100.0
	return RCABundle{Schema: rcaBundleSchema, AnalysisID: "rca-1", Revision: 2, Assessment: *a, Excerpts: []RCAEvidenceExcerpt{
		{RefID: "evidence-00000000000000a1", AnomalyID: "anomaly-00000000000000a1", Series: "transaction/Checkout/p95_ms", Unit: "ms", Points: []RCAExcerptPoint{{At: time.Date(2026, 10, 5, 12, 3, 20, 0, time.UTC), Value: &p95}}},
	}}
}

func TestNarrationCheckAcceptsWhatTheBundleSays(t *testing.T) {
	narrative := "Resource pressure (hypothesis-0000000000000001, 82% score) explains Checkout p95 rising by 300 ms to 3,100 ms at 12:03:20 UTC on 2026-10-05; " +
		"CPU (anomaly-00000000000000c1, evidence-00000000000000c1) rose 60 points from 12:02:30 ± 10 s, earlier. Two hypotheses were ranked; #2 is weaker."
	check := checkRCANarration(narrationTestBundle(), narrative)
	if !check.OK || len(check.References) != 3 || check.Numbers == 0 {
		t.Fatalf("%+v", check)
	}
}

func TestNarrationCheckRejectsUnknownReferencesAndNumbers(t *testing.T) {
	for _, tc := range []struct {
		narrative, kind, text string
	}{
		{"See hypothesis-ffffffffffffffff for details.", narrationUnknownReference, "hypothesis-ffffffffffffffff"},
		{"Checkout p95 rose by 450 ms.", narrationUnsupportedNumber, "450"},
		{"Latency reached 3.4 s.", narrationUnsupportedNumber, "3.4"},
		{"The onset was at 12:07:00.", narrationUnsupportedTime, "12:07:00"},
		{"It began on 2026-10-06.", narrationUnsupportedTime, "2026-10-06"},
	} {
		check := checkRCANarration(narrationTestBundle(), tc.narrative)
		if check.OK || len(check.Issues) != 1 || check.Issues[0].Kind != tc.kind || check.Issues[0].Text != tc.text {
			t.Errorf("%q: %+v", tc.narrative, check)
		}
	}
	// Rounding is judged at the precision written: 3.1 s and 3.10 s are
	// 3,100 ms; a derived difference or false precision is not in the bundle.
	for _, narrative := range []string{"p95 hit 3.1 s", "p95 hit 3.10 s", "p95 hit 3,100.0 ms"} {
		if check := checkRCANarration(narrationTestBundle(), narrative); !check.OK {
			t.Errorf("%q: %+v", narrative, check.Issues)
		}
	}
	for _, narrative := range []string{"CPU rose 50 s earlier", "p95 hit 3100.4 ms"} {
		if check := checkRCANarration(narrationTestBundle(), narrative); check.OK {
			t.Errorf("%q passed", narrative)
		}
	}
	for _, narrative := range []string{"p95 hit 3.1 s"} {
		if check := checkRCANarration(narrationTestBundle(), narrative); !check.OK {
			t.Errorf("%q: %+v", narrative, check.Issues)
		}
	}
	// A number written against its unit is checked; one in a name is not.
	if check := checkRCANarration(narrationTestBundle(), "p95 hit 450ms"); check.OK {
		t.Error("450ms passed")
	}
	if check := checkRCANarration(narrationTestBundle(), "p95 rose 300ms"); !check.OK {
		t.Errorf("300ms: %+v", check.Issues)
	}
	// Times are compared at the precision written.
	bundle := narrationTestBundle()
	bundle.Assessment.Anomalies[1].Onset = bundle.Assessment.Anomalies[1].Onset.Add(400 * time.Millisecond)
	for narrative, ok := range map[string]bool{"at 2026-10-05T12:03:20Z": true, "at 2026-10-05T12:03:20.4Z": true, "at 2026-10-05T12:03:20.9Z": false} {
		if check := checkRCANarration(bundle, narrative); check.OK != ok {
			t.Errorf("%q: ok %v, issues %+v", narrative, check.OK, check.Issues)
		}
	}
	// Numbers that are part of a name are not claims.
	if check := checkRCANarration(narrationTestBundle(), "The p99 and v7 builds."); !check.OK || check.Numbers != 0 {
		t.Errorf("names: %+v", check)
	}
}

func TestEvidenceExcerptsFrameEachAnomaly(t *testing.T) {
	a := reportRootCauseTestAssessment()
	a.Anomalies[1].Evidence = []rca.EvidenceRef{{ID: "evidence-sym", Locator: "interval_distributions.jsonl#transaction=Checkout?from=x"}}
	a.Anomalies[1].OnsetUncertainty = 10 * time.Second
	a.Anomalies[1].End = a.Anomalies[1].Onset.Add(10 * time.Minute)
	series := rca.Series{ID: "transaction/Checkout/p95_ms", Unit: "ms", Bucket: 10 * time.Second}
	start := a.Anomalies[1].Onset.Add(-5 * time.Minute)
	for i := 0; i < 120; i++ {
		series.Points = append(series.Points, rca.SeriesPoint{At: start.Add(time.Duration(i) * 10 * time.Second), Value: float64(100 + i), Valid: i != 40})
	}
	excerpts := rcaEvidenceExcerpts(a, []rca.Series{series})
	if len(excerpts) != 1 {
		t.Fatalf("%+v", excerpts)
	}
	e := excerpts[0]
	first, last := e.Points[0].At, e.Points[len(e.Points)-1].At
	if e.RefID != "evidence-sym" || e.Unit != "ms" || !e.Sampled || len(e.Points) != maxRCAExcerptPoints ||
		first.Before(a.Anomalies[1].Onset.Add(-10*time.Second-30*time.Second)) || last.After(a.Anomalies[1].End.Add(10*time.Second)) {
		t.Fatalf("%+v from %s to %s", e, first, last)
	}
	// An excerpt survives only with its anomaly.
	a.Anomalies = a.Anomalies[:1]
	if kept := citedExcerpts(a, excerpts); len(kept) != 0 {
		t.Fatalf("kept %+v", kept)
	}
}

func TestBoundedBundleKeepsExcerptsOfWhatItKeeps(t *testing.T) {
	bundle := narrationTestBundle()
	raw := json.RawMessage(`{"inventory":"` + strings.Repeat("x", rcaMaxBundleBytes) + `"}`)
	bundle.Evidence = &raw
	// Many excerpts of an anomaly no hypothesis cites.
	bundle.Assessment.Anomalies = append(bundle.Assessment.Anomalies, rca.Anomaly{ID: "anomaly-uncited", Series: "transaction/Login/p95_ms"})
	value := 1.0
	for i := 0; i < 4000; i++ {
		bundle.Excerpts = append(bundle.Excerpts, RCAEvidenceExcerpt{RefID: "evidence-uncited", AnomalyID: "anomaly-uncited", Points: []RCAExcerptPoint{{At: time.Now(), Value: &value}}})
	}
	bounded := boundRCABundle(bundle)
	body, _ := json.Marshal(bounded)
	// The uncited excerpts and the uncited anomaly went; the oversized
	// inventory went too.
	if len(body) > rcaMaxBundleBytes || !bounded.Truncated || bounded.Evidence != nil || len(bounded.Excerpts) != 1 || bounded.Excerpts[0].AnomalyID != "anomaly-00000000000000a1" {
		t.Fatalf("%d bytes, %d excerpts, evidence %v", len(body), len(bounded.Excerpts), bounded.Evidence != nil)
	}
}

func TestRCABundleExcerptsNarrationAndCritiques(t *testing.T) {
	server, store := baselineTestServer(t)
	// Cart requests slow from 200 s.
	exemplarArchiveFixture(t, server, store, "rca-run", func(i int, tx *TransactionEvent) {
		tx.DurationMs = 100 + float64(i%7)
		if i >= 200 {
			tx.DurationMs = 400 + float64(i%7)
		}
	})
	t.Setenv("APEXLOAD_RCA_V2_PROJECTS", storage.DefaultProjectID)
	assessment := completedRCAAssessment(t, server, "rca-run", map[string]any{})
	if len(assessment.Anomalies) == 0 {
		t.Fatal("the fixture produced no anomaly")
	}
	base := rcaRunPath + "/analyses/" + assessment.AnalysisID
	reply := timeslotRequest(t, server, http.MethodGet, base+"/bundle", nil)
	var bundle RCABundle
	if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &bundle) != nil {
		t.Fatalf("bundle: %d %s", reply.Code, reply.Body.String())
	}
	if bundle.Schema != "apexload.rca_bundle.v2" || len(bundle.Excerpts) == 0 {
		t.Fatalf("bundle: %s, %d excerpts", bundle.Schema, len(bundle.Excerpts))
	}
	anomaly := bundle.Assessment.Anomalies[0]
	excerpt := bundle.Excerpts[0]
	if excerpt.AnomalyID != anomaly.ID || excerpt.RefID != anomaly.Evidence[0].ID || len(excerpt.Points) == 0 {
		t.Fatalf("excerpt %+v for %+v", excerpt, anomaly)
	}

	grounded := fmt.Sprintf("The %s anomaly (%s, %s) moved by %.0f %s.", anomaly.Series, anomaly.ID, excerpt.RefID, math.Abs(anomaly.Effect), anomaly.Unit)
	check := func(narrative string) RCANarrationCheck {
		t.Helper()
		reply := timeslotRequest(t, server, http.MethodPost, base+"/narration-check", map[string]any{"narrative": narrative})
		var out RCANarrationCheck
		if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &out) != nil {
			t.Fatalf("check: %d %s", reply.Code, reply.Body.String())
		}
		return out
	}
	if got := check(grounded); !got.OK || len(got.References) != 2 {
		t.Fatalf("grounded: %+v", got)
	}
	if got := check("See hypothesis-ffffffffffffffff; latency rose 98765 ms."); got.OK || len(got.Issues) != 2 {
		t.Fatalf("ungrounded: %+v", got)
	}

	// A critique must pass the check, and never changes the analysis.
	before := timeslotRequest(t, server, http.MethodGet, base, nil).Body.String()
	rejected := timeslotRequest(t, server, http.MethodPost, base+"/critiques", map[string]any{"narrative": "Root cause is hypothesis-ffffffffffffffff."})
	if rejected.Code != http.StatusUnprocessableEntity || !strings.Contains(rejected.Body.String(), narrationUnknownReference) {
		t.Fatalf("rejected: %d %s", rejected.Code, rejected.Body.String())
	}
	if bad := timeslotRequest(t, server, http.MethodPost, base+"/critiques", map[string]any{"narrative": grounded, "author_kind": "oracle"}); bad.Code != http.StatusBadRequest {
		t.Fatalf("author kind: %d", bad.Code)
	}
	created := timeslotRequest(t, server, http.MethodPost, base+"/critiques", map[string]any{"narrative": grounded, "model": "deterministic-local"})
	var critique RCACritique
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &critique) != nil || critique.AuthorKind != rcaCritiqueByAgent || !critique.Check.OK || critique.Revision != assessment.Revision {
		t.Fatalf("created: %d %s", created.Code, created.Body.String())
	}
	var list struct {
		Critiques []RCACritique `json:"critiques"`
	}
	if reply := timeslotRequest(t, server, http.MethodGet, base+"/critiques", nil); reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &list) != nil || len(list.Critiques) != 1 || list.Critiques[0].Narrative != grounded {
		t.Fatalf("list: %d %s", reply.Code, reply.Body.String())
	}
	if after := timeslotRequest(t, server, http.MethodGet, base, nil).Body.String(); after != before {
		t.Fatal("storing a critique changed the analysis")
	}
}
