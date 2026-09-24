package jmeter

import (
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"strings"
	"testing"
)

func TestReviewEvidenceOriginalAndRepairs(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts ConvertOptions
		want int
	}{{"original", ConvertOptions{}, 2}, {"UTC", ConvertOptions{TimeMode: "utc"}, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			r, e := ConvertJMX(splitPayFixture(t, "original"), tc.opts)
			if e != nil {
				t.Fatal(e)
			}
			if r.ReviewSummary.BlockingBehaviors != tc.want || r.ReviewSummary.UnsupportedBehaviors != 0 || len(r.UnsupportedElements) != 0 {
				t.Fatalf("summary=%+v outcomes=%+v", r.ReviewSummary, r.ReviewOutcomes)
			}
			if len(r.Diagnostics) == 0 {
				t.Fatal("missing diagnostics")
			}
			for _, d := range r.Diagnostics {
				if d.Severity == diag.Blocker && d.Detail == "" {
					t.Fatal("empty blocker")
				}
			}
		})
	}
}
func TestReviewEvidenceUnsupportedProcessorsAreDistinct(t *testing.T) {
	source := string(splitPayFixture(t, "repaired"))
	source = strings.Replace(source, "def traceId =", "System.exit(0)\ndef traceId =", 1)
	r, e := ConvertJMX([]byte(source), ConvertOptions{TimeMode: "utc"})
	if e != nil {
		t.Fatal(e)
	}
	if r.ReviewSummary.UnsupportedBehaviors != 1 || r.ReviewSummary.BlockingBehaviors != 1 {
		t.Fatalf("summary=%+v outcomes=%+v", r.ReviewSummary, r.ReviewOutcomes)
	}
}

func TestReviewEvidenceEmptyAndDuplicateSources(t *testing.T) {
	r := &ConvertResult{}
	r.NormalizeReviewEvidence()
	if r.Diagnostics == nil || len(r.Diagnostics) != 0 || *r.ReviewSummary != (ReviewSummary{}) {
		t.Fatalf("empty=%+v", r)
	}
	source := string(splitPayFixture(t, "repaired"))
	a := strings.Index(source, "        <JSR223PostProcessor")
	end := strings.Index(source[a:], "</JSR223PostProcessor>") + a + len("</JSR223PostProcessor>")
	b := strings.Index(source[end:], "<hashTree/>") + end + len("<hashTree/>")
	if a < 0 || b < a {
		t.Fatal("fixture changed")
	}
	processor := strings.Replace(source[a:b], "def traceId =", "System.exit(0)\ndef traceId =", 1)
	source = source[:a] + processor + processor + source[b:]
	converted, e := ConvertJMX([]byte(source), ConvertOptions{TimeMode: "utc"})
	if e != nil {
		t.Fatal(e)
	}
	if converted.ReviewSummary.UnsupportedBehaviors != 2 || converted.ReviewSummary.BlockingBehaviors != 2 {
		t.Fatalf("distinct processors: %+v %+v", converted.ReviewSummary, converted.ReviewOutcomes)
	}
}

func TestReviewEvidenceNestedController(t *testing.T) {
	source := string(splitPayFixture(t, "original"))
	a := strings.Index(source, "        <HTTPSamplerProxy")
	b := strings.Index(source, "        <ResultCollector")
	source = source[:a] + `<TransactionController testname="wrapper"/><hashTree>` + source[a:b] + `</hashTree>` + source[b:]
	r, e := ConvertJMX([]byte(source), ConvertOptions{})
	if e != nil {
		t.Fatal(e)
	}
	if r.ReviewSummary.BlockingBehaviors != 2 {
		t.Fatalf("nested: %+v %+v", r.ReviewSummary, r.ReviewOutcomes)
	}
}

func TestReviewEvidenceBindingReasons(t *testing.T) {
	r, e := ConvertJMX(splitPayFixture(t, "original"), ConvertOptions{TimeMode: "utc", VariableBindings: map[string]string{"client_id": "cid", "client_secret": "cs"}})
	if e != nil {
		t.Fatal(e)
	}
	if r.ReviewSummary.BlockingBehaviors != 2 {
		t.Fatalf("findings=%+v", r.Findings)
	}
}

func TestReviewEvidenceMinifiedSourceKeepsDistinctElements(t *testing.T) {
	source := `<jmeterTestPlan><hashTree><TestPlan/><hashTree><ThreadGroup testname="users"/><hashTree><HTTPSamplerProxy testname="same"><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.path">/a/${missing}</stringProp><elementProp name="HTTPsampler.Arguments"><collectionProp name="Arguments.arguments"><elementProp name=""><stringProp name="Argument.value">${missing}</stringProp></elementProp></collectionProp></elementProp></HTTPSamplerProxy><hashTree/><HTTPSamplerProxy testname="same"><stringProp name="HTTPSampler.domain">example.test</stringProp><stringProp name="HTTPSampler.path">/${missing}</stringProp></HTTPSamplerProxy><hashTree/></hashTree></hashTree></hashTree></jmeterTestPlan>`
	r, e := ConvertJMX([]byte(source), ConvertOptions{})
	if e != nil {
		t.Fatal(e)
	}
	refs := map[string]bool{}
	for _, o := range r.ReviewOutcomes {
		if o.Code == "import_missing_variables" {
			refs[o.SourceRef] = true
		}
	}
	if len(refs) != 2 {
		t.Fatalf("distinct inline samplers collapsed: %+v", r.ReviewOutcomes)
	}
}

func TestReviewEvidenceRetainsIndependentFindings(t *testing.T) {
	r := &ConvertResult{Findings: []importreview.Finding{
		{Code: "source_problem", Severity: diag.Blocker, Location: "first", SourceRef: "plan.jmx:1#first"},
		{Code: "source_problem", Severity: diag.Blocker, Location: "second", SourceRef: "plan.jmx:2#second"},
	}}
	r.NormalizeReviewEvidence()
	if r.ReviewSummary.BlockingBehaviors != 2 {
		t.Fatalf("distinct source findings collapsed: %+v", r.ReviewOutcomes)
	}
}

func TestReviewEvidenceRetainsNewReasonOnGatedAction(t *testing.T) {
	r, err := ConvertJMX(splitPayFixture(t, "original"), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r.Findings = append(r.Findings, importreview.Finding{Code: "independent_source_problem", Severity: diag.Blocker, ActionPath: "actions[0]", Location: "actions[0]", Detail: "A separate source problem"})
	r.NormalizeReviewEvidence()
	if r.ReviewSummary.BlockingBehaviors != 3 {
		t.Fatalf("independent reason dropped: %+v", r.ReviewOutcomes)
	}
}

func TestReviewEvidenceMergePreservesStrongestEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b ReviewOutcome
		want ReviewSummary
	}{
		{"unsupported escalation", ReviewOutcome{Finding: importreview.Finding{Severity: diag.Warning, Gate: importreview.GatePromotion}, Unsupported: true}, ReviewOutcome{Finding: importreview.Finding{Severity: diag.Blocker}}, ReviewSummary{BlockingBehaviors: 1, UnsupportedBehaviors: 1}},
		{"promotion gate", ReviewOutcome{Finding: importreview.Finding{Severity: diag.Warning}}, ReviewOutcome{Finding: importreview.Finding{Severity: diag.Warning, Gate: importreview.GatePromotion}}, ReviewSummary{ReviewBehaviors: 1}},
		{"warning escalation", ReviewOutcome{Finding: importreview.Finding{Severity: diag.Info}}, ReviewOutcome{Finding: importreview.Finding{Severity: diag.Warning}}, ReviewSummary{Warnings: 1}},
		{"execution gate", ReviewOutcome{Finding: importreview.Finding{Severity: diag.Warning}}, ReviewOutcome{Finding: importreview.Finding{Severity: diag.Warning, Gate: importreview.GateExecution}}, ReviewSummary{BlockingBehaviors: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, pair := range [][]ReviewOutcome{{tc.a, tc.b}, {tc.b, tc.a}} {
				r := &ConvertResult{}
				r.SetReviewEvidence(pair)
				if *r.ReviewSummary != tc.want {
					t.Fatalf("summary=%+v want=%+v", r.ReviewSummary, tc.want)
				}
				if tc.want.BlockingBehaviors > 0 && r.Diagnostics[0].Severity != diag.Blocker {
					t.Fatalf("execution gate lacks blocking diagnostic: %+v", r.Diagnostics)
				}
			}
		})
	}
}

func TestReviewEvidencePreservesCodedDiagnosticReasons(t *testing.T) {
	r, err := ConvertJMX(splitPayFixture(t, "original"), ConvertOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r.Unsupported = append(r.Unsupported, diag.Diagnostic{Code: "independent_processor_problem", Severity: diag.Blocker, Location: "actions[0]", Detail: "Independent coded diagnostic"})
	r.NormalizeReviewEvidence()
	if r.ReviewSummary.BlockingBehaviors != 3 {
		t.Fatalf("coded reason lost: %+v", r.ReviewOutcomes)
	}
}

func TestReviewEvidenceAuthoredFindingEscalatesExistingEvidence(t *testing.T) {
	r := &ConvertResult{Findings: []importreview.Finding{
		{Code: "same", Severity: diag.Info, Location: "sampler"},
		{Code: "same", Severity: diag.Blocker, Gate: importreview.GateExecution, Location: "sampler"},
	}}
	r.NormalizeReviewEvidence()
	if r.ReviewSummary.BlockingBehaviors != 1 || r.ReviewSummary.Informational != 0 {
		t.Fatalf("stronger authored finding lost: %+v", r.ReviewOutcomes)
	}
}
