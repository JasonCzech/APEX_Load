package analysis

import (
	"math"
	"testing"
)

func TestJavaHTTPPairedSumsPreserveZerosAndRefuseUnqualifiedEvidence(t *testing.T) {
	base := JavaHTTPPairedSums{Version: 1, Eligible: 2, Samples: 2, Numerator: 7, Denominator: 12, Complete: true}
	got, err := EvaluateJavaHTTPPairedSums("java_http_prepared_per_decoded", base)
	if err != nil || got.Value == nil || *got.Value != 7.0/12 {
		t.Fatal(got, err)
	}
	zero := base
	zero.Numerator = 0
	got, err = EvaluateJavaHTTPPairedSums("java_http_prepared_per_decoded", zero)
	if err != nil || got.Value == nil || *got.Value != 0 {
		t.Fatal("measured zero", got, err)
	}
	for name, change := range map[string]func(*JavaHTTPPairedSums){
		"version": func(p *JavaHTTPPairedSums) { p.Version = 2 }, "missing": func(p *JavaHTTPPairedSums) { p.Eligible++ }, "empty": func(p *JavaHTTPPairedSums) { p.Samples = 0; p.Eligible = 0 }, "bound": func(p *JavaHTTPPairedSums) { p.Samples = 100001; p.Eligible = p.Samples }, "partial": func(p *JavaHTTPPairedSums) { p.Complete = false }, "zero denominator": func(p *JavaHTTPPairedSums) { p.Denominator = 0 }, "sum bound": func(p *JavaHTTPPairedSums) { p.Numerator = 1 << 53 }, "fractional bytes": func(p *JavaHTTPPairedSums) { p.Numerator = .5 }, "nonfinite": func(p *JavaHTTPPairedSums) { p.Numerator = math.Inf(1) }, "negative": func(p *JavaHTTPPairedSums) { p.Denominator = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			p := base
			change(&p)
			got, err := EvaluateJavaHTTPPairedSums("java_http_prepared_per_decoded", p)
			if err != nil || got.Value != nil || got.Reason == "" {
				t.Fatal(got, err)
			}
		})
	}
	if _, err := EvaluateJavaHTTPPairedSums("ratio", base); err == nil {
		t.Fatal("arbitrary ratio")
	}
	work := base
	work.Numerator = .5
	work.Denominator = 1.5
	got, err = EvaluateJavaHTTPPairedSums("java_http_base64_work_share", work)
	if err != nil || got.Value == nil || *got.Value != 1.0/3 {
		t.Fatal(got, err)
	}
	work.Numerator = 2
	got, _ = EvaluateJavaHTTPPairedSums("java_http_base64_work_share", work)
	if got.Value != nil {
		t.Fatal("impossible work share", got)
	}
	work.Numerator = 0
	work.Denominator = 2*24*60*60*1000 + 1
	got, _ = EvaluateJavaHTTPPairedSums("java_http_base64_work_share", work)
	if got.Value != nil {
		t.Fatal("time bound", got)
	}
}
