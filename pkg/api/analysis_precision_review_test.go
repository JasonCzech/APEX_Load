package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestAnalysisPrecisionAcknowledgementBindsExactSnapshot(t *testing.T) {
	in := &storage.AnalysisImport{ID: "import", ProjectID: "project", Revision: 7}
	mapping := analysis.ParquetMapping{Version: 3, PrecisionPolicy: analysis.PrecisionAllowFloat64Rounding, Timestamp: analysis.ParquetColumnPath{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []analysis.ParquetMeasure{{Value: analysis.ParquetColumnPath{"value"}, Name: "value", Kind: analysis.KindGauge, Unit: "count"}}}
	review := strings.Repeat("a", 64)
	first, err := analysisPrecisionAcknowledgement(in, mapping, review)
	if err != nil || len(first) != 64 {
		t.Fatal(first, err)
	}
	if again, err := analysisPrecisionAcknowledgement(in, mapping, review); err != nil || again != first {
		t.Fatal("unstable snapshot binding", again, err)
	}
	for _, tc := range []struct {
		name   string
		change func(*storage.AnalysisImport, *analysis.ParquetMapping, *string)
	}{
		{"project", func(i *storage.AnalysisImport, _ *analysis.ParquetMapping, _ *string) { i.ProjectID = "other" }},
		{"import", func(i *storage.AnalysisImport, _ *analysis.ParquetMapping, _ *string) { i.ID = "other" }},
		{"revision", func(i *storage.AnalysisImport, _ *analysis.ParquetMapping, _ *string) { i.Revision++ }},
		{"policy", func(_ *storage.AnalysisImport, m *analysis.ParquetMapping, _ *string) {
			m.PrecisionPolicy = analysis.PrecisionRejectPotentialLoss
		}},
		{"source-review", func(_ *storage.AnalysisImport, _ *analysis.ParquetMapping, r *string) { *r = strings.Repeat("b", 64) }},
		{"mapping", func(_ *storage.AnalysisImport, m *analysis.ParquetMapping, _ *string) { m.Timezone = "Europe/London" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other, m, r := *in, mapping, review
			tc.change(&other, &m, &r)
			got, err := analysisPrecisionAcknowledgement(&other, m, r)
			if err != nil || got == first {
				t.Fatal("stale acknowledgement remains valid", got, err)
			}
		})
	}
	for _, bad := range []string{"", strings.Repeat("A", 64), strings.Repeat("a", 65)} {
		if _, err := analysisPrecisionAcknowledgement(in, mapping, bad); err == nil {
			t.Fatal("malformed review digest accepted")
		}
	}
	if _, err := analysisPrecisionAcknowledgement(nil, mapping, review); err == nil {
		t.Fatal("missing snapshot accepted")
	}
}

func TestAnalysisPrecisionReviewFinalDisclosureRechecksAuthority(t *testing.T) {
	for _, change := range []string{"none", "revision", "cancelled", "foreign", "deadline"} {
		t.Run(change, func(t *testing.T) {
			s, store, in := nativePreviewAuthorityFixture(t, "uploading")
			ctx := t.Context()
			if change == "deadline" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			req := nativePreviewRequest(in, ctx)
			if change == "foreign" {
				bindAuthorizedProjectToRequest(req, "foreign")
			}
			if change == "revision" || change == "cancelled" {
				current := *in
				if change == "cancelled" {
					current.State = "cancelled"
				} else {
					current.Name = "later revision"
				}
				if _, err := store.SaveAnalysisImport(current, current.Revision); err != nil {
					t.Fatal(err)
				}
			}
			result := AnalysisPrecisionReview{Version: 1, ImportID: in.ID, Revision: in.Revision, AcknowledgementSHA256: "private-review-canary"}
			w := httptest.NewRecorder()
			ok := s.writeNativeAnalysisPrecisionReview(w, req, in, result)
			if change == "none" {
				if !ok || w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "private-review-canary") {
					t.Fatal(w.Code, w.Body.String())
				}
			} else if ok || w.Code == http.StatusOK || strings.Contains(w.Body.String(), "private-review-canary") {
				t.Fatal("revoked precision review disclosed", w.Code, w.Body.String())
			}
		})
	}
}
