package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestProviderHeadsVerifyLatestSameIdentityContent(t *testing.T) {
	s, source, start := dynatraceMonitorFixture(t)
	if _, err := s.StartRunMonitors(source.ProjectID, "monitor-run", "monitor-scenario", start, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	from, to := start, start.Add(time.Minute)
	oldPoint := dynatracePoint(source, start, 1)
	oldDiagnostic := analysis.ProviderDiagnostic{Provider: "dynatrace", Backend: "classic", ID: "problem-1", Kind: "problem", TimestampUTC: start, Status: "OPEN"}
	acceptDynatraceFixture(t, s, source, 0, ProviderMonitorBatch{Points: []analysis.MetricPoint{oldPoint}, Evidence: []analysis.ProviderDiagnostic{oldDiagnostic}, QueryFromUTC: from, QueryUntilUTC: to}, to)
	latestPoint := dynatracePoint(source, start, 7)
	latestDiagnostic := oldDiagnostic
	latestDiagnostic.Status = "CLOSED"
	acceptDynatraceFixture(t, s, source, 1, ProviderMonitorBatch{Points: []analysis.MetricPoint{latestPoint}, Evidence: []analysis.ProviderDiagnostic{latestDiagnostic}, QueryFromUTC: from, QueryUntilUTC: to}, to)
	if ObservationKey(oldPoint) != ObservationKey(latestPoint) || DiagnosticKeyForJob("monitor-run:"+source.ID, oldDiagnostic) != DiagnosticKeyForJob("monitor-run:"+source.ID, latestDiagnostic) {
		t.Fatal("fixture must describe revisions of the same canonical identities")
	}
	for _, tc := range []struct {
		kind        string
		old, latest any
	}{
		{"point", oldPoint, latestPoint},
		{"diagnostic", oldDiagnostic, latestDiagnostic},
	} {
		heads, err := s.ListProviderObservationHeads(source.ProjectID, "monitor-run", source.ID, tc.kind, "", 10)
		if err != nil || len(heads) != 1 || heads[0].Sequence != 1 {
			t.Fatal(heads, err)
		}
		head := heads[0]
		if err := VerifyProviderHeadContent(head, tc.latest); err != nil {
			t.Fatal("latest immutable receipt record rejected", err)
		}
		if err := VerifyProviderHeadContent(head, tc.old); err == nil {
			t.Fatal("same-key older receipt record bypassed canonical integrity")
		}
		body, err := json.Marshal(head)
		if err != nil || strings.Contains(string(body), head.ContentDigest) || strings.Contains(string(body), "ContentDigest") {
			t.Fatal("private canonical digest disclosed", string(body), err)
		}
		head.ContentDigest = ""
		if err := VerifyProviderHeadContent(head, tc.latest); err == nil {
			t.Fatal("missing canonical digest accepted")
		}
	}
}
