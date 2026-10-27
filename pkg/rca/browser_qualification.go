package rca

// BrowserEvidenceQualified means the trusted adapter verified evidence bound to
// every contributing source and its full measurement window. It is not a claim
// inferred from a collection snapshot or from generator-only clock samples.
const BrowserEvidenceQualified = "qualified"
const BrowserEvidenceUnavailable = "unavailable"

const (
	BrowserClockUnavailable                  = "browser_clock_unavailable"
	BrowserSourceCoverageUnavailable         = "browser_source_coverage_unavailable"
	BrowserTimelineUnavailable               = "browser_timeline_unavailable"
	BrowserBaselineClockUnavailable          = "baseline_browser_clock_unavailable"
	BrowserBaselineSourceCoverageUnavailable = "baseline_browser_source_coverage_unavailable"
)

func browserDocumentQualificationLimitations(document *DocumentCohort, baseline bool) []Limitation {
	clock, coverage, side := BrowserClockUnavailable, BrowserSourceCoverageUnavailable, "run"
	if baseline {
		clock, coverage, side = BrowserBaselineClockUnavailable, BrowserBaselineSourceCoverageUnavailable, "baseline"
	}
	var out []Limitation
	if document == nil || document.ClockQualification != BrowserEvidenceQualified {
		detail := "the " + side + " lacks verified browser-to-controller clock bounds covering the full document measurement windows; recorded timestamps alone do not qualify load attribution"
		if document != nil && document.ClockUnavailableReason != "" {
			detail += " (" + document.ClockUnavailableReason + ")"
		}
		out = append(out, Limitation{Code: clock, Detail: detail})
	}
	if document == nil || document.SourceCoverageQualification != BrowserEvidenceQualified {
		out = append(out, Limitation{Code: coverage, Detail: "the " + side + " lacks verified complete browser source coverage for the document population; successful collection and no reported loss do not prove completeness"})
	}
	return out
}

func browserDocumentQualification(document *DocumentCohort, baseline bool) (string, string) {
	if limits := browserDocumentQualificationLimitations(document, baseline); len(limits) > 0 {
		return limits[0].Code, limits[0].Detail
	}
	return "", ""
}
