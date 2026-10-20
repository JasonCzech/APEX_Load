package api

import (
	"fmt"
	"math"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Java representations are advisory comparisons, not acceptance thresholds.
// Every value requires the same fully observed action cohort on both runs.
// A byte ratio is a ratio of paired sums, never a mean of per-action ratios.
type baselineJavaValue struct {
	name, unit string
	value      float64
	available  bool
}

// baselineJavaHTTPPopulation is the summary's own java_http action count, or
// the measurement's samples for a summary saved before it was counted.
func baselineJavaHTTPPopulation(m *BaselineMeasurement) int64 {
	if m.JavaHTTP != nil && m.JavaHTTP.Population > 0 {
		return m.JavaHTTP.Population
	}
	return m.Samples
}

func baselineJavaHTTPValues(m *BaselineMeasurement) ([]baselineJavaValue, string) {
	if m == nil || m.JavaHTTP == nil {
		return nil, "unavailable"
	}
	s := m.JavaHTTP
	if !s.Valid() || s.Incomplete || s.SampleLimitReached || s.InvalidSamples != 0 || s.Samples != baselineJavaHTTPPopulation(m) || s.Samples < 100 {
		return nil, "incomplete or insufficient action evidence"
	}
	compression := ""
	for name, count := range map[string]int64{"none": s.RequestCompressionNone, "gzip": s.RequestCompressionGzip, "deflate": s.RequestCompressionDeflate} {
		if count == s.Samples {
			compression = name
		}
	}
	if compression == "" {
		return nil, "mixed or unavailable request compression"
	}
	var values []baselineJavaValue
	// Collection-window and discarded responses do not retain a complete
	// response representation; a discarded body's zero size is logging policy.
	completeResponses := s.ResponseWindow == 0 && s.ResponseDiscarded == 0
	for _, item := range []struct {
		name     string
		m        protocol.JavaHTTPByteMoments
		response bool
	}{
		{"Runtime input representation", s.RequestEncoded, false}, {"Decoded request", s.RequestDecoded, false}, {"Prepared request body", s.PreparedRequestBody, false}, {"Retained response", s.ResponseRetained, true}, {"Response base64 representation", s.ResponseBase64, true},
	} {
		value, ok := item.m.MeanBytes()
		ok = ok && item.m.Count == s.Samples && (!item.response || completeResponses)
		values = append(values, baselineJavaValue{item.name + " mean", "bytes", value, ok})
	}
	for _, item := range []struct {
		name     string
		p        protocol.JavaHTTPBytePair
		response bool
	}{
		{"Decoded / encoded request", s.DecodedPerEncoded, false}, {"Prepared / decoded request", s.PreparedPerDecoded, false}, {"Base64 / retained response", s.Base64PerRetained, true},
	} {
		value, ok := item.p.Ratio()
		ok = ok && item.p.Samples == s.Samples && (!item.response || completeResponses)
		values = append(values, baselineJavaValue{item.name, "ratio", value, ok})
	}
	value, ok := s.Base64Work.Share()
	values = append(values, baselineJavaValue{"Base64 work / enclosing time", "ratio", value, ok && s.Base64Work.Samples == s.Samples})
	for _, item := range []struct {
		name string
		d    protocol.DiagnosticDistribution
	}{{"Base64 decode", s.Base64Decode}, {"Base64 encode", s.Base64Encode}, {"Enclosing action", s.Enclosing}} {
		value, ok := item.d.Quantile(.95)
		values = append(values, baselineJavaValue{item.name + " approximate P95", "ms", value, ok && item.d.Count == s.Samples})
	}
	return values, compression
}
func baselineJavaHTTPFindings(row BaselineComparisonRow, baselineID string, cell BaselineComparisonCell, policy BaselineComparisonPolicy) []BaselineComparisonFinding {
	if row.Current == nil || cell.Measurement == nil || row.Current.JavaHTTP == nil && cell.Measurement.JavaHTTP == nil {
		return nil
	}
	current, identity := baselineJavaHTTPValues(row.Current)
	baseline, baseIdentity := baselineJavaHTTPValues(cell.Measurement)
	unverified := func(metric, reason string) BaselineComparisonFinding {
		return BaselineComparisonFinding{BaselineID: baselineID, RowID: row.ID, Category: "transaction", Metric: "Java " + metric, Message: row.Group + " / " + row.Name + ": " + reason + "; no representation comparison scored", Status: "unverified"}
	}
	if len(current) == 0 || len(baseline) == 0 || identity != baseIdentity || len(cell.Qualification) > 0 || row.Window == nil || cell.Window == nil {
		return []BaselineComparisonFinding{unverified("representation coverage", "Missing, partial or incompatible action/compression/window evidence")}
	}
	var findings []BaselineComparisonFinding
	for i, a := range current {
		b := baseline[i]
		if !a.available || !b.available {
			findings = append(findings, unverified(a.name, "Missing measurement, zero denominator or exact-sum limit"))
			continue
		}
		if a.value == b.value {
			continue
		}
		delta, pct := baselineDelta(a.value, b.value)
		// Timing jitter is reviewed under the comparison's latency policy, as
		// other protocol timings are. Byte representations and ratios are not.
		if a.unit == "ms" && !(math.Abs(*delta) >= policy.LatencyMs && (pct != nil && math.Abs(*pct) >= policy.LatencyPct || pct == nil && policy.LatencyMs > 0)) {
			continue
		}
		change := "percentage unavailable: baseline zero"
		if pct != nil {
			change = fmt.Sprintf("%+.3f%%", *pct)
		}
		message := fmt.Sprintf("%s / %s: %s %.6g vs %.6g %s (%+.6g %s; %s); %d/%d observed actions; request compression %s. Advisory evidence only; no acceptance threshold or causal claim.", row.Group, row.Name, a.name, a.value, b.value, a.unit, *delta, a.unit, change, baselineJavaHTTPPopulation(row.Current), baselineJavaHTTPPopulation(cell.Measurement), identity)
		findings = append(findings, BaselineComparisonFinding{BaselineID: baselineID, RowID: row.ID, Category: "transaction", Metric: "Java " + a.name, Message: message, Status: "review"})
	}
	return findings
}

// Text report sections are bounded; structured comparison/CSV rows retain the
// full versioned summaries. A truncation label never implies absent evidence.
func baselineJavaHTTPReportLines(c *BaselineComparisonSnapshot) []string {
	if c == nil {
		return nil
	}
	var lines []string
	for _, row := range c.Transactions {
		entries := []struct {
			id string
			m  *BaselineMeasurement
		}{{c.Current.ID, row.Current}}
		for _, b := range c.Baselines {
			entries = append(entries, struct {
				id string
				m  *BaselineMeasurement
			}{b.ID, row.Baselines[b.ID].Measurement})
		}
		for _, entry := range entries {
			if entry.m == nil || entry.m.JavaHTTP == nil {
				continue
			}
			next := javaHTTPReportLines(entry.id+" / "+row.Group+" / "+row.Name, entry.m.JavaHTTP, baselineJavaHTTPPopulation(entry.m))
			if len(lines)+len(next) > 200 {
				return append(lines, "Additional Java representation evidence remains available in the structured comparison and CSV export.")
			}
			lines = append(lines, next...)
		}
	}
	return lines
}
